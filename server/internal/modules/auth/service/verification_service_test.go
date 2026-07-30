package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeOTPSender struct{ acceptance EmailAcceptance }

func (f fakeOTPSender) SendOTP(context.Context, string, string, string, string, int) (EmailAcceptance, uint64, error) {
	return f.acceptance, 1, nil
}

type fakeVerificationCheckRepo struct {
	verificationRepository
	target string
}

func (f *fakeVerificationCheckRepo) CheckAndMarkUsed(_ context.Context, _, target, _, _ string) error {
	f.target = target
	return nil
}

type fakeTargetKeyer struct{ key string }

func (f fakeTargetKeyer) TargetKey(string) (string, error) {
	if f.key == "" {
		return "", errors.New("缺少键")
	}
	return f.key, nil
}

func TestEmailOTPFirstResponseIsExactly600Seconds(t *testing.T) {
	svc := NewVerificationService(nil)
	svc.SetEmailSender(fakeOTPSender{acceptance: EmailAcceptance{ExpiresAt: time.Now().Add(599 * time.Second)}})
	result, err := svc.Send(context.Background(), "email", fakeAddress("user"), "register")
	if err != nil || result.ExpiresIn != 600 {
		t.Fatalf("首次发送必须固定返回 600: %#v %v", result, err)
	}
}

func TestEmailCheckUsesInjectedTargetKeyer(t *testing.T) {
	repo := &fakeVerificationCheckRepo{}
	svc := NewVerificationService(repo)
	expected := strings.Repeat("a", 64)
	svc.SetEmailTargetKeyer(fakeTargetKeyer{key: expected})
	if err := svc.Check(context.Background(), "email", "user"+"@example"+".invalid", "login", strings.Repeat("1", 6)); err != nil || repo.target != expected {
		t.Fatalf("邮箱校验必须使用注入的目标键接口: %v", err)
	}
}

func TestEmailOTPReplayDecrementsAndSuppressesCode(t *testing.T) {
	svc := NewVerificationService(nil)
	svc.SetEmailSender(fakeOTPSender{acceptance: EmailAcceptance{Idempotent: true, ExpiresAt: time.Now().Add(300 * time.Second)}})
	result, err := svc.Send(context.Background(), "email", fakeAddress("user"), "login")
	if err != nil || result.ExpiresIn < 298 || result.ExpiresIn > 300 || result.Code != "" {
		t.Fatalf("幂等重放必须递减且不回传验证码: %#v %v", result, err)
	}
}
