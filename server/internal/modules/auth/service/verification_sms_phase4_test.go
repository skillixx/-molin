package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"molin/server/internal/modules/auth/model"
	smssender "molin/server/internal/modules/sms/sender"
	"molin/server/pkg/crypto"
)

// phase4VerificationRepo 通过公开仓储接口模拟验证码消费资格，不依赖私有实现细节。
type phase4VerificationRepo struct {
	created     *model.VerificationCode
	status      string
	correctHash string
	used        bool
}

func (r *phase4VerificationRepo) Create(_ context.Context, code *model.VerificationCode) error {
	r.created = code
	return nil
}

func (r *phase4VerificationRepo) CheckAndMarkUsed(_ context.Context, targetType, _, _, codeHash string) error {
	if targetType != "phone" || r.status != "accepted" || r.used || codeHash != r.correctHash {
		return errors.New("验证码不可消费")
	}
	r.used = true
	return nil
}

func (r *phase4VerificationRepo) UpdateSMSSendState(_ context.Context, _ uint64, status string, _ *time.Time, _, _ string) error {
	r.status = status
	return nil
}

func (r *phase4VerificationRepo) CreateEmailSendPending(context.Context, *model.VerificationCode, *model.EmailSendLog) error {
	return nil
}

func (r *phase4VerificationRepo) FindLatestByScope(context.Context, string, time.Time) (*model.VerificationCode, error) {
	return nil, errors.New("未找到记录")
}

func (r *phase4VerificationRepo) FailStaleEmailSend(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (r *phase4VerificationRepo) FinalizeEmailSend(context.Context, uint64, string, *time.Time, *model.EmailSendLog) error {
	return nil
}

// phase4SMSGuard 用可观察状态验证 VerificationService 是否执行了发送限流和错误次数门禁。
type phase4SMSGuard struct {
	allowSend  bool
	failures   int
	clearCalls int
	sendErr    error
	checkErr   error
}

func (g *phase4SMSGuard) AllowSend(context.Context, string, string) (bool, error) {
	return g.allowSend, g.sendErr
}

func (g *phase4SMSGuard) AllowCheckAttempt(context.Context, string, string) (bool, error) {
	if g.checkErr != nil {
		return false, g.checkErr
	}
	g.failures++
	return g.failures <= 5, nil
}

func TestPhase4RedisGuardErrorsFailClosedWithoutProviderOrConsumption(t *testing.T) {
	correctCode := "runtime-correct-code"
	repo := &phase4VerificationRepo{status: "accepted", correctHash: crypto.SHA256Hex(correctCode)}
	mock := smssender.NewMockSender(smssender.Result{ProviderCode: "OK"}, nil)
	svc := NewVerificationService(repo)
	svc.SetSMSDispatcher(newTestDispatcher(mock))
	svc.SetSMSVerificationGuard(&phase4SMSGuard{allowSend: true, sendErr: errors.New("redis unavailable")})
	if _, err := svc.SendDetailed(context.Background(), "phone", "phone-test-value", "register"); !errors.Is(err, ErrSMSUnavailable) {
		t.Fatalf("发码门禁异常必须返回短信不可用，实际 %v", err)
	}
	if repo.created != nil || mock.CallCount() != 0 {
		t.Fatal("发码门禁异常不得落库或调用供应商")
	}

	svc.SetSMSVerificationGuard(&phase4SMSGuard{allowSend: true, checkErr: errors.New("redis unavailable")})
	if err := svc.Check(context.Background(), "phone", "phone-test-value", "register", correctCode); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("校验门禁异常必须使用统一验证码错误，实际 %v", err)
	}
	if repo.used {
		t.Fatal("校验门禁异常不得消费验证码")
	}
}

func (g *phase4SMSGuard) ClearCheckFailures(context.Context, string, string) error {
	g.clearCalls++
	g.failures = 0
	return nil
}

func TestPhase4PhoneSendRateLimitRejectsBeforeOTPAndProvider(t *testing.T) {
	repo := &phase4VerificationRepo{}
	mock := smssender.NewMockSender(smssender.Result{ProviderCode: "OK"}, nil)
	svc := NewVerificationService(repo)
	svc.SetSMSDispatcher(newTestDispatcher(mock))
	svc.SetSMSVerificationGuard(&phase4SMSGuard{allowSend: false})

	_, err := svc.SendDetailed(context.Background(), "phone", "phone-test-value", "register")
	if !errors.Is(err, ErrSMSRateLimited) {
		t.Fatalf("手机号与场景达到发码上限后必须返回稳定限流错误，实际 %v", err)
	}
	if repo.created != nil || mock.CallCount() != 0 {
		t.Fatal("限流拒绝必须发生在生成验证码、落库和供应商调用之前")
	}
}

func TestPhase4PhoneOTPBlocksAfterFiveWrongAttemptsAndClearsOnSuccess(t *testing.T) {
	correctCode := "runtime-correct-code"
	repo := &phase4VerificationRepo{status: "accepted", correctHash: crypto.SHA256Hex(correctCode)}
	guard := &phase4SMSGuard{allowSend: true}
	svc := NewVerificationService(repo)
	svc.SetSMSVerificationGuard(guard)

	for attempt := 1; attempt <= 5; attempt++ {
		if err := svc.Check(context.Background(), "phone", "phone-test-value", "bind_phone", "runtime-wrong-code"); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("第 %d 次错误验证码必须使用统一安全错误，实际 %v", attempt, err)
		}
	}
	if guard.failures != 5 {
		t.Fatalf("第五次错误后必须用完当前手机号与场景的尝试额度，failures=%d", guard.failures)
	}
	if err := svc.Check(context.Background(), "phone", "phone-test-value", "bind_phone", correctCode); !errors.Is(err, ErrInvalidCode) {
		t.Fatal("达到最大错误次数后，即使随后提交正确验证码也必须在窗口内拒绝")
	}
	if repo.used {
		t.Fatal("被错误次数门禁锁定后不得消费验证码")
	}

	guard.failures = 0
	if err := svc.Check(context.Background(), "phone", "phone-test-value", "bind_phone", correctCode); err != nil {
		t.Fatalf("门禁恢复后正确验证码应可消费: %v", err)
	}
	if !repo.used || guard.clearCalls != 1 {
		t.Fatalf("成功消费后必须清理错误次数，used=%v clear=%d", repo.used, guard.clearCalls)
	}
}
