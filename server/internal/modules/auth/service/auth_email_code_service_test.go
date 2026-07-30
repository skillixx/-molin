package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/auth/dto"
	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
)

type recordingVerificationSender struct {
	calls int
}

func (s *recordingVerificationSender) Send(context.Context, string, string, string) (VerificationSendResult, error) {
	s.calls++
	return VerificationSendResult{ExpiresIn: 600}, nil
}

func TestAdminVerifyEmailSendRequiresCurrentPhoneMFA(t *testing.T) {
	email := fakeAddress("admin")
	now := time.Now().UTC()
	expired := now.Add(-3 * time.Hour)
	valid := now.Add(-time.Minute)
	emailMFA := now
	tests := []struct {
		name      string
		phoneMFA  *time.Time
		wantError error
		wantCalls int
	}{
		{name: "未完成手机认证", wantError: ErrAdminPhoneNotVerified},
		{name: "手机认证已过期", phoneMFA: &expired, wantError: ErrAdminPhoneNotVerified},
		{name: "手机认证有效且无需邮箱认证", phoneMFA: &valid, wantCalls: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender := &recordingVerificationSender{}
			user := &model.User{ID: 7, Email: &email, AdminPhoneVerifiedAt: tc.phoneMFA}
			// 即使邮箱认证时间存在，也不能替代手机认证前置条件。
			if tc.phoneMFA == nil {
				user.AdminEmailVerifiedAt = &emailMFA
			}
			_, err := sendAdminVerifyEmailCode(context.Background(), user.ID, user, 2, sender)
			if !errors.Is(err, tc.wantError) || sender.calls != tc.wantCalls {
				t.Fatalf("手机认证门禁结果错误: err=%v calls=%d", err, sender.calls)
			}
		})
	}
}

func TestEmailCodeLoginSharesD16CounterWithPassword(t *testing.T) {
	failures := 4 // 模拟同一邮箱此前已有四次密码登录失败。
	user := &model.User{ID: 7, Status: "active"}
	flow := emailCodeLoginFlow{
		locked: func(context.Context, string) bool { return failures >= loginFailLimit },
		find:   func(context.Context, string) (*model.User, error) { return user, nil },
		verify: func(context.Context, string, string) error { return ErrInvalidCode },
		fail:   func(context.Context, string) { failures++ },
		clear:  func(context.Context, string) { failures = 0 },
		issue: func(context.Context, *model.User, string, string, string) (*dto.LoginResp, error) {
			return &dto.LoginResp{}, nil
		},
		record: func(context.Context, *uint64, string, string, string, string) {},
	}
	if _, err := executeEmailCodeLogin(context.Background(), "user@example.invalid", "bad", "127.0.0.1", "ua", flow); !errors.Is(err, ErrInvalidCode) || failures != 5 {
		t.Fatalf("第五次跨方式失败必须触发共享计数: failures=%d err=%v", failures, err)
	}
	if _, err := executeEmailCodeLogin(context.Background(), "user@example.invalid", "123456", "127.0.0.1", "ua", flow); !errors.Is(err, ErrLoginLocked) {
		t.Fatalf("锁定期必须在消费验证码前拒绝: %v", err)
	}
}

func TestEmailCodeLoginAddsSessionWithoutChangingOldSessionOrMFA(t *testing.T) {
	phoneMFA := time.Now().UTC().Add(-time.Minute)
	emailMFA := time.Now().UTC().Add(-time.Minute)
	user := &model.User{ID: 7, Status: "active", AdminPhoneVerifiedAt: &phoneMFA, AdminEmailVerifiedAt: &emailMFA}
	sessions := []string{"old-session"}
	failures := 2
	verified := 0
	flow := emailCodeLoginFlow{
		locked: func(context.Context, string) bool { return false },
		find:   func(context.Context, string) (*model.User, error) { return user, nil },
		verify: func(context.Context, string, string) error { verified++; return nil },
		fail:   func(context.Context, string) { failures++ },
		clear:  func(context.Context, string) { failures = 0 },
		issue: func(context.Context, *model.User, string, string, string) (*dto.LoginResp, error) {
			sessions = append(sessions, "new-session")
			return &dto.LoginResp{TokenPair: dto.TokenPair{AccessToken: "test-access", RefreshToken: "test-refresh"}}, nil
		},
		record: func(context.Context, *uint64, string, string, string, string) {},
	}
	pair, err := executeEmailCodeLogin(context.Background(), "user@example.invalid", "123456", "127.0.0.1", "ua", flow)
	if err != nil || pair == nil || verified != 1 || failures != 0 {
		t.Fatalf("验证码登录成功语义错误: pair=%#v verified=%d failures=%d err=%v", pair, verified, failures, err)
	}
	if len(sessions) != 2 || sessions[0] != "old-session" || sessions[1] != "new-session" {
		t.Fatalf("新会话必须与旧会话共存: %#v", sessions)
	}
	if user.AdminPhoneVerifiedAt != &phoneMFA || user.AdminEmailVerifiedAt != &emailMFA || !user.AdminPhoneVerifiedAt.Equal(phoneMFA) || !user.AdminEmailVerifiedAt.Equal(emailMFA) {
		t.Fatal("普通邮箱验证码登录不得刷新或改写管理员 MFA")
	}
}

func TestEmailCodeLoginRejectsDisabledBeforeConsumption(t *testing.T) {
	consumed := false
	flow := emailCodeLoginFlow{
		locked: func(context.Context, string) bool { return false },
		find:   func(context.Context, string) (*model.User, error) { return &model.User{ID: 7, Status: "disabled"}, nil },
		verify: func(context.Context, string, string) error { consumed = true; return nil },
		fail:   func(context.Context, string) {}, clear: func(context.Context, string) {},
		issue:  func(context.Context, *model.User, string, string, string) (*dto.LoginResp, error) { return nil, nil },
		record: func(context.Context, *uint64, string, string, string, string) {},
	}
	if _, err := executeEmailCodeLogin(context.Background(), "user@example.invalid", "123456", "127.0.0.1", "ua", flow); !errors.Is(err, ErrUserDisabled) || consumed {
		t.Fatalf("禁用账号必须在验证码消费前拒绝: consumed=%v err=%v", consumed, err)
	}
}

type concurrentVerificationRepo struct {
	verificationRepository
	mu   sync.Mutex
	used bool
}

func (r *concurrentVerificationRepo) CheckAndMarkUsed(context.Context, string, string, string, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.used {
		return repository.ErrVerificationNotFound
	}
	r.used = true
	return nil
}

func TestAcceptedLoginCodeConcurrentConsumptionSucceedsOnce(t *testing.T) {
	repo := &concurrentVerificationRepo{}
	svc := NewVerificationService(repo)
	svc.SetEmailTargetKeyer(fakeTargetKeyer{key: "target-hmac"})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- svc.Check(context.Background(), "email", "user@example.invalid", "login", "123456")
		}()
	}
	wg.Wait()
	close(results)
	success, failed := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInvalidCode) {
			failed++
		}
	}
	if success != 1 || failed != 1 {
		t.Fatalf("并发消费必须恰好一次成功: success=%d failed=%d", success, failed)
	}
}
