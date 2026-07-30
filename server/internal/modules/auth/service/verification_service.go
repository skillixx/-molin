package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"molin/server/internal/modules/auth/model"
	"molin/server/pkg/crypto"
)

var ErrInvalidCode = errors.New("验证码错误或已过期")

// VerificationService 负责验证码的生成、发送和校验。
type VerificationService struct {
	repo        verificationRepository
	emailSender EmailOTPSender
	emailKeyer  EmailTargetKeyer
}

type verificationRepository interface {
	Create(context.Context, *model.VerificationCode) error
	CreateEmailSendPending(context.Context, *model.VerificationCode, *model.EmailSendLog) error
	CheckAndMarkUsed(context.Context, string, string, string, string) error
	FindLatestByScope(context.Context, string, time.Time) (*model.VerificationCode, error)
	FailStaleEmailSend(context.Context, string, string, time.Time) (bool, error)
	FinalizeEmailSend(context.Context, uint64, string, *time.Time, *model.EmailSendLog) error
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
	Code      string
	ExpiresIn int
}

func NewVerificationService(repo verificationRepository) *VerificationService {
	return &VerificationService{repo: repo}
}

func (s *VerificationService) SetEmailSender(sender EmailOTPSender) { s.emailSender = sender }

func (s *VerificationService) SetEmailTargetKeyer(keyer EmailTargetKeyer) { s.emailKeyer = keyer }

// Send 生成 6 位数字验证码，存库前用 SHA-256 哈希（防止 DB 泄露后 OTP 可直接使用）。
// 返回明文 rawCode，由调用方通过 SMS/邮件发给用户；明文不入库。
func (s *VerificationService) Send(ctx context.Context, targetType, targetValue, scene string) (VerificationSendResult, error) {
	targetValue = normalizeVerificationTarget(targetType, targetValue)
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
		return VerificationSendResult{Code: rawCode, ExpiresIn: expiresIn}, nil
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
	return VerificationSendResult{Code: rawCode, ExpiresIn: 600}, nil
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
