package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"molin/server/internal/modules/auth/model"
	smsservice "molin/server/internal/modules/sms/service"
	"molin/server/pkg/crypto"
)

var (
	ErrInvalidCode    = errors.New("验证码错误或已过期")
	ErrSMSUnavailable = errors.New("短信功能当前不可用")
	ErrSMSSendFailed  = errors.New("短信提交失败")
)

type verificationRepository interface {
	Create(ctx context.Context, v *model.VerificationCode) error
	CheckAndMarkUsed(ctx context.Context, targetType, targetValue, scene, codeHash string) error
	UpdateSendState(ctx context.Context, id uint64, status string, sentAt *time.Time, provider, providerRequestID string) error
}

// VerificationService 负责验证码的生成、发送和校验。
type VerificationService struct {
	repo          verificationRepository
	smsDispatcher *smsservice.Dispatcher
}

// VerificationSendResult 是发码链路向上层返回的安全结果；手机验证码明文永远不会写入该结果。
type VerificationSendResult struct {
	Code              string
	Sent              bool
	ExpiresIn         int
	BusinessRequestID string
	SubmitStatus      string
}

func NewVerificationService(repo verificationRepository) *VerificationService {
	return &VerificationService{repo: repo}
}

// SetSMSDispatcher 注入短信发送编排器；未注入时手机号发码必须 fail-closed。
func (s *VerificationService) SetSMSDispatcher(dispatcher *smsservice.Dispatcher) {
	s.smsDispatcher = dispatcher
}

// Send 生成 6 位数字验证码，存库前用 SHA-256 哈希（防止 DB 泄露后 OTP 可直接使用）。
// 返回明文 rawCode，由调用方通过 SMS/邮件发给用户；明文不入库。
func (s *VerificationService) Send(ctx context.Context, targetType, targetValue, scene string) (string, error) {
	result, err := s.SendDetailed(ctx, targetType, targetValue, scene)
	return result.Code, err
}

// SendDetailed 返回统一的安全发码结果，供 HTTP 层实现稳定的短信响应契约。
func (s *VerificationService) SendDetailed(ctx context.Context, targetType, targetValue, scene string) (VerificationSendResult, error) {
	targetValue = normalizeVerificationTarget(targetType, targetValue)
	if targetType == "phone" {
		return s.sendPhoneCode(ctx, targetValue, scene)
	}
	rawCode := generateCode(6)
	// 存库前对明文 OTP 做 SHA-256 哈希；OTP 短时效，SHA-256 无需 HMAC 密钥
	codeHash := crypto.SHA256Hex(rawCode)
	v := &model.VerificationCode{
		TargetType:  targetType,
		TargetValue: targetValue,
		Code:        codeHash, // 只存 hash，不存明文
		Scene:       scene,
		SendStatus:  "not_applicable",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := s.repo.Create(ctx, v); err != nil {
		return VerificationSendResult{}, err
	}
	// 返回明文供调用方发送给用户
	return VerificationSendResult{Code: rawCode, Sent: true, ExpiresIn: 600}, nil
}

// sendPhoneCode 执行 pending → sent/failed 状态机；任何失败都不会留下可校验验证码。
func (s *VerificationService) sendPhoneCode(ctx context.Context, phone, scene string) (VerificationSendResult, error) {
	if s.smsDispatcher == nil {
		return VerificationSendResult{}, ErrSMSUnavailable
	}
	plan, err := s.smsDispatcher.Prepare(ctx, scene, phone)
	if err != nil {
		if errors.Is(err, smsservice.ErrSMSUnavailable) || errors.Is(err, smsservice.ErrSceneNotBound) || errors.Is(err, smsservice.ErrPhoneNotAllowed) {
			return VerificationSendResult{}, ErrSMSUnavailable
		}
		return VerificationSendResult{}, err
	}
	rawCode := generateCode(6)
	codeHash := crypto.SHA256Hex(rawCode)
	businessRequestID, err := generateBusinessRequestID()
	if err != nil {
		return VerificationSendResult{}, err
	}
	provider := plan.Provider
	v := &model.VerificationCode{
		TargetType:        "phone",
		TargetValue:       phone,
		Code:              codeHash,
		Scene:             scene,
		SendStatus:        "pending",
		Provider:          &provider,
		BusinessRequestID: &businessRequestID,
		ExpiresAt:         time.Now().Add(10 * time.Minute),
	}
	if err := s.repo.Create(ctx, v); err != nil {
		return VerificationSendResult{}, err
	}
	result, sendErr := s.smsDispatcher.Submit(ctx, plan, phone, rawCode, businessRequestID)
	// 即使 HTTP 请求已取消，也必须尽力把 pending 收敛为不可误用的明确终态。
	terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if sendErr != nil || !result.Accepted {
		if err := s.repo.UpdateSendState(terminalCtx, v.ID, "failed", nil, plan.Provider, result.ProviderRequestID); err != nil {
			return VerificationSendResult{}, err
		}
		return VerificationSendResult{}, ErrSMSSendFailed
	}
	now := time.Now()
	if err := s.repo.UpdateSendState(terminalCtx, v.ID, "sent", &now, plan.Provider, result.ProviderRequestID); err != nil {
		return VerificationSendResult{}, err
	}
	// 明文验证码已由 Sender 提交，禁止继续返回给 handler 或日志。
	return VerificationSendResult{
		Sent:              true,
		ExpiresIn:         600,
		BusinessRequestID: businessRequestID,
		SubmitStatus:      "accepted",
	}, nil
}

// Check D-49：校验验证码，通过后原子标记为已使用（防止重放和并发竞态）。
// 使用 CheckAndMarkUsed 将"查找有效验证码 + 标记已用"合并为单条原子 UPDATE，
// 避免高并发下同一 OTP 被多个请求同时通过校验（FindValid→MarkUsed 的 TOCTOU 竞态）。
func (s *VerificationService) Check(ctx context.Context, targetType, targetValue, scene, code string) error {
	targetValue = normalizeVerificationTarget(targetType, targetValue)
	// 对用户输入做 SHA-256 哈希，与存库值保持一致
	codeHash := crypto.SHA256Hex(code)
	if err := s.repo.CheckAndMarkUsed(ctx, targetType, targetValue, scene, codeHash); err != nil {
		return ErrInvalidCode
	}
	return nil
}

// normalizeVerificationTarget 保证验证码发送和校验使用同一套账号格式。
func normalizeVerificationTarget(targetType, targetValue string) string {
	switch targetType {
	case "email":
		return normalizeEmail(targetValue)
	case "phone":
		return normalizePhone(targetValue)
	default:
		return targetValue
	}
}

func generateCode(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	code := ""
	for _, byt := range b {
		code += fmt.Sprintf("%d", int(byt)%10)
	}
	return code
}

func generateBusinessRequestID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", errors.New("生成短信业务请求标识失败")
	}
	return "sms_" + hex.EncodeToString(random), nil
}
