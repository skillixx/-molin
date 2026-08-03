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

// VerificationService 负责验证码的生成、发送和校验。
type VerificationService struct {
	repo          verificationRepository
	emailSender   EmailOTPSender
	emailKeyer    EmailTargetKeyer
	smsDispatcher *smsservice.Dispatcher
}

type verificationRepository interface {
	Create(context.Context, *model.VerificationCode) error
	CreateEmailSendPending(context.Context, *model.VerificationCode, *model.EmailSendLog) error
	CheckAndMarkUsed(context.Context, string, string, string, string) error
	FindLatestByScope(context.Context, string, time.Time) (*model.VerificationCode, error)
	FailStaleEmailSend(context.Context, string, string, time.Time) (bool, error)
	FinalizeEmailSend(context.Context, uint64, string, *time.Time, *model.EmailSendLog) error
	UpdateSMSSendState(context.Context, uint64, string, *time.Time, string, string) error
}

// EmailOTPSender 是 auth 与具体邮件供应商之间的稳定边界，不暴露 TemplateId 或 SDK 类型。
type EmailOTPSender interface {
	SendOTP(ctx context.Context, businessRequestNo, scene, recipient, code string, expireMinutes int) (EmailAcceptance, uint64, error)
}

// EmailTargetKeyer 将邮箱规范化并转换为持久化查询键，与供应商发送能力解耦。
type EmailTargetKeyer interface {
	TargetKey(email string) (string, error)
}

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

func (s *VerificationService) SetEmailSender(sender EmailOTPSender) { s.emailSender = sender }

func (s *VerificationService) SetEmailTargetKeyer(keyer EmailTargetKeyer) { s.emailKeyer = keyer }

// SetSMSDispatcher 注入短信发送编排器；未注入时手机号发码必须失败关闭。
func (s *VerificationService) SetSMSDispatcher(dispatcher *smsservice.Dispatcher) {
	s.smsDispatcher = dispatcher
}

// Send 保留 auth 服务现有调用契约，并把详细结果交给统一实现生成。
func (s *VerificationService) Send(ctx context.Context, targetType, targetValue, scene string) (VerificationSendResult, error) {
	return s.SendDetailed(ctx, targetType, targetValue, scene)
}

// SendDetailed 生成验证码并返回安全发送结果；手机验证码明文绝不进入返回值。
func (s *VerificationService) SendDetailed(ctx context.Context, targetType, targetValue, scene string) (VerificationSendResult, error) {
	targetValue = normalizeVerificationTarget(targetType, targetValue)
	if targetType == "phone" {
		return s.sendPhoneCode(ctx, targetValue, scene)
	}
	rawCode, err := generateCode(6)
	if err != nil {
		return VerificationSendResult{}, errors.New("验证码生成失败")
	}
	if targetType == "email" {
		if s.emailSender == nil {
			return VerificationSendResult{}, ErrEmailNotReady
		}
		businessNo, err := randomBusinessNo()
		if err != nil {
			return VerificationSendResult{}, errors.New("业务请求号生成失败")
		}
		acceptance, _, err := s.emailSender.SendOTP(ctx, businessNo, scene, targetValue, rawCode, 10)
		if err != nil {
			return VerificationSendResult{}, err
		}
		expiresIn := 600
		// 首次受理必须返回完整 600 秒，只有幂等重放才按原过期时间递减。
		if acceptance.Idempotent && !acceptance.ExpiresAt.IsZero() {
			expiresIn = int(time.Until(acceptance.ExpiresAt).Seconds())
			if expiresIn < 0 {
				expiresIn = 0
			}
		}
		if acceptance.Idempotent {
			rawCode = ""
		}
		return VerificationSendResult{Code: rawCode, Sent: true, ExpiresIn: expiresIn}, nil
	}
	// 存库前对明文 OTP 做 SHA-256 哈希；OTP 短时效，SHA-256 无需 HMAC 密钥
	codeHash := crypto.SHA256Hex(rawCode)
	// 验证码 DATETIME 统一写 UTC，避免进程位于 Asia/Shanghai 时与邮件验证码时间域分裂。
	now := time.Now().UTC().Truncate(time.Second)
	v := &model.VerificationCode{
		TargetType:  targetType,
		TargetValue: &targetValue,
		CodeHash:    codeHash, // 只存 hash，不存明文
		Scene:       scene,
		SendStatus:  "accepted", // 手机验证码兼容路径显式 accepted
		AcceptedAt:  &now,
		ExpiresAt:   now.Add(10 * time.Minute),
	}
	if err := s.repo.Create(ctx, v); err != nil {
		return VerificationSendResult{}, err
	}
	// 返回明文供调用方发送给用户
	return VerificationSendResult{Code: rawCode, Sent: true, ExpiresIn: 600}, nil
}

// sendPhoneCode 执行 pending → accepted/failed 状态机；任何失败都不会留下可消费验证码。
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
	rawCode, err := generateCode(6)
	if err != nil {
		return VerificationSendResult{}, errors.New("验证码生成失败")
	}
	businessRequestID, err := generateBusinessRequestID()
	if err != nil {
		return VerificationSendResult{}, err
	}
	provider := plan.Provider
	codeHash := crypto.SHA256Hex(rawCode)
	v := &model.VerificationCode{
		TargetType:        "phone",
		TargetValue:       &phone,
		CodeHash:          codeHash,
		Scene:             scene,
		SendStatus:        "pending",
		Provider:          &provider,
		BusinessRequestNo: &businessRequestID,
		ExpiresAt:         time.Now().UTC().Truncate(time.Second).Add(10 * time.Minute),
	}
	if err := s.repo.Create(ctx, v); err != nil {
		return VerificationSendResult{}, err
	}
	result, sendErr := s.smsDispatcher.Submit(ctx, plan, phone, rawCode, businessRequestID)
	// 即使 HTTP 请求已经取消，也要尽力把 pending 收敛为明确终态。
	terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if sendErr != nil || !result.Accepted {
		if err := s.repo.UpdateSMSSendState(terminalCtx, v.ID, "failed", nil, plan.Provider, result.ProviderRequestID); err != nil {
			return VerificationSendResult{}, err
		}
		return VerificationSendResult{}, ErrSMSSendFailed
	}
	acceptedAt := time.Now().UTC().Truncate(time.Second)
	if err := s.repo.UpdateSMSSendState(terminalCtx, v.ID, "accepted", &acceptedAt, plan.Provider, result.ProviderRequestID); err != nil {
		return VerificationSendResult{}, err
	}
	// 明文验证码已经提交给 Sender，禁止继续返回给 handler 或日志。
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
	if targetType == "email" {
		if s.emailKeyer == nil {
			return ErrInvalidCode
		}
		key, err := s.emailKeyer.TargetKey(targetValue)
		if err != nil {
			return ErrInvalidCode
		}
		targetValue = key
	}
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

func generateCode(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	code := ""
	for _, byt := range b {
		code += fmt.Sprintf("%d", int(byt)%10)
	}
	return code, nil
}

func generateBusinessRequestID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", errors.New("生成短信业务请求标识失败")
	}
	return "sms_" + hex.EncodeToString(random), nil
}
