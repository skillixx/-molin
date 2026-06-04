package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
)

var ErrInvalidCode = errors.New("验证码错误或已过期")

// VerificationService 负责验证码的生成、发送和校验。
type VerificationService struct {
	repo *repository.VerificationRepository
}

func NewVerificationService(repo *repository.VerificationRepository) *VerificationService {
	return &VerificationService{repo: repo}
}

// Send 生成 6 位数字验证码并存库，实际发送由调用方对接 SMS/邮件服务。
// 返回明文 code（本地/测试环境可记录日志，生产环境由邮件服务发出）。
func (s *VerificationService) Send(ctx context.Context, targetType, targetValue, scene string) (string, error) {
	code := generateCode(6)
	v := &model.VerificationCode{
		TargetType:  targetType,
		TargetValue: targetValue,
		Code:        code,
		Scene:       scene,
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := s.repo.Create(ctx, v); err != nil {
		return "", err
	}
	return code, nil
}

// Check 校验验证码，通过后立即标记为已使用（防止重放）。
func (s *VerificationService) Check(ctx context.Context, targetType, targetValue, scene, code string) error {
	v, err := s.repo.FindValid(ctx, targetType, targetValue, scene)
	if err != nil || v == nil {
		return ErrInvalidCode
	}
	if v.Code != code {
		return ErrInvalidCode
	}
	return s.repo.MarkUsed(ctx, v.ID)
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
