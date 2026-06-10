package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
)

var ErrInvalidCode = errors.New("验证码错误或已过期")

// VerificationService 负责验证码的生成、发送和校验。
type VerificationService struct {
	repo *repository.VerificationRepository
}

func NewVerificationService(repo *repository.VerificationRepository) *VerificationService {
	return &VerificationService{repo: repo}
}

// Send 生成 6 位数字验证码，存库前用 SHA-256 哈希（防止 DB 泄露后 OTP 可直接使用）。
// 返回明文 rawCode，由调用方通过 SMS/邮件发给用户；明文不入库。
func (s *VerificationService) Send(ctx context.Context, targetType, targetValue, scene string) (string, error) {
	targetValue = normalizeVerificationTarget(targetType, targetValue)
	rawCode := generateCode(6)
	// 存库前对明文 OTP 做 SHA-256 哈希；OTP 短时效，SHA-256 无需 HMAC 密钥
	codeHash := crypto.SHA256Hex(rawCode)
	v := &model.VerificationCode{
		TargetType:  targetType,
		TargetValue: targetValue,
		Code:        codeHash, // 只存 hash，不存明文
		Scene:       scene,
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := s.repo.Create(ctx, v); err != nil {
		return "", err
	}
	// 返回明文供调用方发送给用户
	return rawCode, nil
}

// Check 校验验证码，通过后立即标记为已使用（防止重放）。
// 校验时对用户输入做相同的 SHA-256 哈希后与库中值比对。
func (s *VerificationService) Check(ctx context.Context, targetType, targetValue, scene, code string) error {
	targetValue = normalizeVerificationTarget(targetType, targetValue)
	v, err := s.repo.FindValid(ctx, targetType, targetValue, scene)
	if err != nil || v == nil {
		return ErrInvalidCode
	}
	// 对输入值做 SHA-256 后比对，与存库值保持一致
	if crypto.SHA256Hex(code) != v.Code {
		return ErrInvalidCode
	}
	return s.repo.MarkUsed(ctx, v.ID)
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
