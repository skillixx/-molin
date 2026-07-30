package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
)

// phase4OTPRepository 用进程内互斥模拟验证码的原子终态和消费条件，测试不会连接数据库。
type phase4OTPRepository struct {
	verificationRepository
	mu     sync.Mutex
	record *model.VerificationCode
	log    *model.EmailSendLog
	now    func() time.Time
}

func (r *phase4OTPRepository) CreateEmailSendPending(_ context.Context, code *model.VerificationCode, log *model.EmailSendLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	code.ID = 1
	log.ID = 2
	log.VerificationCodeID = &code.ID
	codeCopy, logCopy := *code, *log
	r.record, r.log = &codeCopy, &logCopy
	return nil
}

func (r *phase4OTPRepository) FinalizeEmailSend(_ context.Context, _ uint64, status string, acceptedAt *time.Time, log *model.EmailSendLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.record.SendStatus = status
	r.record.AcceptedAt = acceptedAt
	if log != nil {
		copy := *log
		r.log = &copy
	}
	return nil
}

func (r *phase4OTPRepository) FindLatestByScope(context.Context, string, time.Time) (*model.VerificationCode, error) {
	return nil, gorm.ErrRecordNotFound
}

func (r *phase4OTPRepository) FailStaleEmailSend(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (r *phase4OTPRepository) CheckAndMarkUsed(_ context.Context, targetType, targetHash, scene, codeHash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	if r.now != nil {
		now = r.now().UTC()
	}
	if r.record == nil || r.record.TargetType != targetType || r.record.TargetHash == nil || *r.record.TargetHash != targetHash ||
		r.record.Scene != scene || r.record.CodeHash != codeHash || r.record.SendStatus != "accepted" ||
		r.record.UsedAt != nil || !r.record.ExpiresAt.After(now) {
		return repository.ErrVerificationNotFound
	}
	usedAt := now
	r.record.UsedAt = &usedAt
	return nil
}

// phase4TestSendRepository 仅实现模板测试发送需要的持久化表面，并为并发断言提供互斥保护。
type phase4TestSendRepository struct {
	emailRepository
	mu            sync.Mutex
	template      *model.EmailProviderTemplate
	allowStatus   string
	allowErr      error
	log           *model.EmailSendLog
	createCalls   int
	finalizeCalls int
}

func (r *phase4TestSendRepository) FindAllowlistByHMAC(context.Context, string) (*model.EmailTestRecipientAllowlist, error) {
	if r.allowErr != nil {
		return nil, r.allowErr
	}
	return &model.EmailTestRecipientAllowlist{ID: 1, Status: r.allowStatus}, nil
}

func (r *phase4TestSendRepository) GetTemplate(context.Context, uint64) (*model.EmailProviderTemplate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.template == nil {
		return nil, repository.ErrEmailNotFound
	}
	copy := *r.template
	return &copy, nil
}

func (r *phase4TestSendRepository) FindSendLogByIdempotency(_ context.Context, scope, keyHash string) (*model.EmailSendLog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.log == nil || r.log.IdempotencyScope != scope || r.log.IdempotencyKeyHash != keyHash {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *r.log
	return &copy, nil
}

func (r *phase4TestSendRepository) FindBlockingSendByScope(_ context.Context, scope string, now time.Time) (*model.EmailSendLog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.log == nil || r.log.IdempotencyScope != scope {
		return nil, gorm.ErrRecordNotFound
	}
	blocked := r.log.Status == "pending" ||
		(r.log.Status == "failed" && r.log.FailureReason != nil && *r.log.FailureReason == "provider_outcome_unknown" && sendCooldownUntil(r.log).After(now))
	if !blocked {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *r.log
	return &copy, nil
}

func (r *phase4TestSendRepository) FailStalePendingSend(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}

func (r *phase4TestSendRepository) CreateSendLog(_ context.Context, entry *model.EmailSendLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.log != nil && r.log.IdempotencyScope == entry.IdempotencyScope && r.log.IdempotencyKeyHash == entry.IdempotencyKeyHash {
		return repository.ErrEmailConflict
	}
	r.createCalls++
	entry.ID = 10
	copy := *entry
	r.log = &copy
	return nil
}

func (r *phase4TestSendRepository) FinalizeSendLog(_ context.Context, _ uint64, status string, requestID, reason *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finalizeCalls++
	r.log.Status, r.log.ProviderRequestID, r.log.FailureReason = status, requestID, reason
	return nil
}

func (r *phase4TestSendRepository) counters() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.createCalls, r.finalizeCalls
}

// phase4BlockingAdapter 让首个外呼停在进程内，便于确定性验证并发同 Key 不会二次外呼。
type phase4BlockingAdapter struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *phase4BlockingAdapter) Ready() bool { return true }
func (a *phase4BlockingAdapter) QueryTemplates(context.Context, int, int) ([]ProviderTemplate, bool, error) {
	return nil, false, nil
}
func (a *phase4BlockingAdapter) DescribeTemplate(context.Context, string) (ProviderTemplate, error) {
	return ProviderTemplate{}, nil
}
func (a *phase4BlockingAdapter) SingleSendMail(context.Context, EmailMessage) (EmailAcceptance, error) {
	a.calls.Add(1)
	a.once.Do(func() { close(a.started) })
	<-a.release
	return EmailAcceptance{RequestID: "phase4-concurrent-request"}, nil
}

func phase4SceneContext(scene, recipient string) context.Context {
	ctx := context.Background()
	switch scene {
	case "bind_email":
		return withEmailOTPIdentity(ctx, "/api/me/verification-codes/email", 7, recipient)
	case "admin_verify":
		return withEmailOTPIdentity(ctx, "/api/admin/auth/verification-codes/email", 7, recipient)
	default:
		return ctx
	}
}

func phase4ApprovedTemplate(id uint64) *model.EmailProviderTemplate {
	return &model.EmailProviderTemplate{
		ID: id, ProviderTemplateID: "phase4-template", Subject: "验证码通知", TemplateText: validEmailTemplateText,
		ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1,
	}
}

func TestPhase4EverySceneRejectAndTimeoutFailClosed(t *testing.T) {
	for _, scene := range []string{"register", "login", "reset_password", "bind_email", "admin_verify"} {
		for _, outcome := range []struct {
			name    string
			sendErr error
			wantErr error
		}{
			{name: "供应商明确拒绝", sendErr: ErrDirectMailUpstream, wantErr: ErrEmailUpstream},
			{name: "供应商结果未知", sendErr: context.DeadlineExceeded, wantErr: ErrEmailOutcomeUnknown},
		} {
			t.Run(scene+"/"+outcome.name, func(t *testing.T) {
				templateID := uint64(1)
				repo := &fakeEmailRepo{template: phase4ApprovedTemplate(templateID), binding: &model.EmailSceneBinding{Scene: scene, TemplateID: &templateID, Enabled: true, Version: 1}}
				verification := &fakeVerificationRepo{}
				adapter := &MockEmailAdapter{SendError: outcome.sendErr}
				svc := newFakeService(repo, verification, adapter, &fakeAuditor{})
				recipient := fakeAddress("phase4-" + scene)

				_, _, err := svc.SendOTP(phase4SceneContext(scene, recipient), "phase4-"+scene, scene, recipient, "654321", 10)
				if !errors.Is(err, outcome.wantErr) || verification.finalized != "failed" || adapter.Calls != 1 {
					t.Fatalf("五场景拒绝或超时必须失败关闭: err=%v finalized=%q calls=%d", err, verification.finalized, adapter.Calls)
				}
				if verification.finalLog == nil || verification.finalLog.Status != "failed" || verification.finalLog.ProviderRequestID != nil {
					t.Fatalf("失败必须同时使 OTP 与脱敏日志不可用: %#v", verification.finalLog)
				}
			})
		}
	}
}

func TestPhase4EveryAcceptedSceneCanBeConsumedExactlyOnceAndExpiresStrictly(t *testing.T) {
	for _, scene := range []string{"register", "login", "reset_password", "bind_email", "admin_verify"} {
		t.Run(scene, func(t *testing.T) {
			templateID := uint64(1)
			repo := &fakeEmailRepo{template: phase4ApprovedTemplate(templateID), binding: &model.EmailSceneBinding{Scene: scene, TemplateID: &templateID, Enabled: true, Version: 1}}
			verificationRepo := &phase4OTPRepository{}
			emailSvc := NewEmailService(repo, verificationRepo, &MockEmailAdapter{RequestID: "phase4-accepted"}, &fakeAuditor{}, nil, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "test", "mock")
			emailSvc.lockOverride = testLock()
			emailSvc.rateLimitOverride = func(context.Context, string, int, time.Duration) (bool, error) { return true, nil }
			emailSvc.recipientAuthorizer = allowEmailRecipient{}
			verificationSvc := NewVerificationService(verificationRepo)
			verificationSvc.SetEmailTargetKeyer(emailSvc)
			recipient, code := fakeAddress("phase4-consume-"+scene), "654321"

			if _, _, err := emailSvc.SendOTP(phase4SceneContext(scene, recipient), "phase4-consume-"+scene, scene, recipient, code, 10); err != nil {
				t.Fatalf("accepted 前置发送失败: %v", err)
			}
			businessEffects := 0
			if err := verificationSvc.Check(context.Background(), "email", recipient, scene, code); err == nil {
				businessEffects++
			} else {
				t.Fatalf("accepted OTP 必须允许一次业务效果: %v", err)
			}
			if err := verificationSvc.Check(context.Background(), "email", recipient, scene, code); !errors.Is(err, ErrInvalidCode) {
				t.Fatalf("同一 OTP 重放必须拒绝: %v", err)
			}
			if businessEffects != 1 {
				t.Fatalf("业务效果门必须只通过一次: %d", businessEffects)
			}

			// 将同一记录恢复为未消费并精确放在当前秒边界，证明 expires_at == now 时严格失效。
			verificationRepo.mu.Lock()
			verificationRepo.record.UsedAt = nil
			boundary := time.Now().UTC().Truncate(time.Second)
			verificationRepo.record.ExpiresAt = boundary
			verificationRepo.now = func() time.Time { return boundary }
			verificationRepo.mu.Unlock()
			if err := verificationSvc.Check(context.Background(), "email", recipient, scene, code); !errors.Is(err, ErrInvalidCode) {
				t.Fatalf("恰到过期秒边界的 OTP 必须拒绝: %v", err)
			}
		})
	}
}

func TestPhase4TestSendAcceptedReplayDoesNotCallProviderAgain(t *testing.T) {
	repo := &phase4TestSendRepository{template: phase4ApprovedTemplate(7), allowStatus: "active"}
	adapter := &MockEmailAdapter{RequestID: "phase4-test-request"}
	svc := NewEmailService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{}, nil, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "test", "mock")
	svc.lockOverride = testLock()
	svc.rateLimitOverride = func(context.Context, string, int, time.Duration) (bool, error) { return true, nil }
	recipient := fakeAddress("phase4-test-send")

	first, err := svc.TestSend(context.Background(), 7, "register", recipient, "same-key", 9, "127.0.0.1")
	if err != nil || first == nil || first.Status != "accepted" || first.Idempotent {
		t.Fatalf("首次测试发送必须明确 accepted: result=%#v err=%v", first, err)
	}
	replay, err := svc.TestSend(context.Background(), 7, "register", recipient, "same-key", 9, "127.0.0.1")
	if err != nil || replay == nil || replay.Status != "accepted" || !replay.Idempotent || adapter.Calls != 1 {
		t.Fatalf("相同 Key 必须重放 accepted 且不二次外呼: result=%#v calls=%d err=%v", replay, adapter.Calls, err)
	}
}

func TestPhase4ConcurrentSameTestSendKeyCallsProviderOnce(t *testing.T) {
	repo := &phase4TestSendRepository{template: phase4ApprovedTemplate(7), allowStatus: "active"}
	adapter := &phase4BlockingAdapter{started: make(chan struct{}), release: make(chan struct{})}
	svc := NewEmailService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{}, nil, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "test", "mock")
	svc.rateLimitOverride = func(context.Context, string, int, time.Duration) (bool, error) { return true, nil }
	svc.lockOverride = testLock()
	recipient := fakeAddress("phase4-concurrent")
	firstResult := make(chan error, 1)
	go func() {
		_, err := svc.TestSend(context.Background(), 7, "register", recipient, "same-key", 9, "127.0.0.1")
		firstResult <- err
	}()
	<-adapter.started

	_, secondErr := svc.TestSend(context.Background(), 7, "register", recipient, "same-key", 9, "127.0.0.1")
	close(adapter.release)
	if firstErr := <-firstResult; firstErr != nil {
		t.Fatalf("首个并发请求应受理成功: %v", firstErr)
	}
	if !errors.Is(secondErr, ErrEmailSending) || adapter.calls.Load() != 1 {
		t.Fatalf("并发相同 Key 只能有一次外呼，竞争者应看到发送中: err=%v calls=%d", secondErr, adapter.calls.Load())
	}
}

func TestPhase4TestSendAllowlistAndPrerequisitesHaveZeroSideEffects(t *testing.T) {
	tests := []struct {
		name        string
		scene       string
		key         string
		recipient   string
		allowStatus string
		allowErr    error
		template    *model.EmailProviderTemplate
		wantErr     error
	}{
		{name: "非法场景", scene: "unknown", key: "key", recipient: fakeAddress("invalid-scene"), allowStatus: "active", template: phase4ApprovedTemplate(7), wantErr: ErrEmailInvalid},
		{name: "空幂等键", scene: "register", recipient: fakeAddress("blank-key"), allowStatus: "active", template: phase4ApprovedTemplate(7), wantErr: ErrEmailInvalid},
		{name: "非法邮箱", scene: "register", key: "key", recipient: "invalid", allowStatus: "active", template: phase4ApprovedTemplate(7), wantErr: ErrEmailInvalid},
		{name: "白名单缺失", scene: "register", key: "key", recipient: fakeAddress("missing"), allowErr: gorm.ErrRecordNotFound, template: phase4ApprovedTemplate(7), wantErr: ErrEmailNotAllowlisted},
		{name: "白名单已撤销", scene: "register", key: "key", recipient: fakeAddress("revoked"), allowStatus: "revoked", template: phase4ApprovedTemplate(7), wantErr: ErrEmailNotAllowlisted},
		{name: "模板缺失", scene: "register", key: "key", recipient: fakeAddress("template-missing"), allowStatus: "active", wantErr: repository.ErrEmailNotFound},
		{name: "模板本地停用", scene: "register", key: "key", recipient: fakeAddress("template-off"), allowStatus: "active", template: func() *model.EmailProviderTemplate { v := phase4ApprovedTemplate(7); v.LocalEnabled = false; return v }(), wantErr: ErrEmailTemplateOff},
		{name: "模板供应商缺失", scene: "register", key: "key", recipient: fakeAddress("provider-missing"), allowStatus: "active", template: func() *model.EmailProviderTemplate { v := phase4ApprovedTemplate(7); v.Missing = true; return v }(), wantErr: ErrEmailTemplateGone},
		{name: "模板变量不完整", scene: "register", key: "key", recipient: fakeAddress("variables"), allowStatus: "active", template: func() *model.EmailProviderTemplate {
			v := phase4ApprovedTemplate(7)
			v.VariablesComplete = false
			return v
		}(), wantErr: ErrEmailVariables},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &phase4TestSendRepository{template: tc.template, allowStatus: tc.allowStatus, allowErr: tc.allowErr}
			adapter := &MockEmailAdapter{}
			audit := &fakeAuditor{}
			svc := NewEmailService(repo, &fakeVerificationRepo{}, adapter, audit, nil, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "test", "mock")
			svc.lockOverride = testLock()
			svc.rateLimitOverride = func(context.Context, string, int, time.Duration) (bool, error) { return true, nil }

			_, err := svc.TestSend(context.Background(), 7, tc.scene, tc.recipient, tc.key, 9, "127.0.0.1")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("前置失败错误不精确: got=%v want=%v", err, tc.wantErr)
			}
			createCalls, finalizeCalls := repo.counters()
			if adapter.Calls != 0 || createCalls != 0 || finalizeCalls != 0 || len(audit.actions) != 0 {
				t.Fatalf("统一前置失败必须零副作用: adapter=%d create=%d finalize=%d audit=%d", adapter.Calls, createCalls, finalizeCalls, len(audit.actions))
			}
		})
	}
}

func TestPhase4AcceptedTestSendStoresOnlyHashedIdempotencyKey(t *testing.T) {
	repo := &phase4TestSendRepository{template: phase4ApprovedTemplate(7), allowStatus: "active"}
	svc := NewEmailService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{RequestID: "phase4-safe"}, &fakeAuditor{}, nil, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "test", "mock")
	svc.lockOverride = testLock()
	svc.rateLimitOverride = func(context.Context, string, int, time.Duration) (bool, error) { return true, nil }
	key := "phase4-secret-idempotency-key"
	if _, err := svc.TestSend(context.Background(), 7, "register", fakeAddress("phase4-safe"), key, 9, "127.0.0.1"); err != nil {
		t.Fatalf("测试发送失败: %v", err)
	}
	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.log == nil || repo.log.IdempotencyKeyHash != crypto.SHA256Hex(key) || repo.log.IdempotencyKeyHash == key {
		t.Fatal("测试发送日志必须只持久化幂等键摘要")
	}
}
