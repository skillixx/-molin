package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/auth/repository"
	"molin/server/pkg/crypto"
)

func fakeAddress(local string) string { return local + "@example" + ".invalid" }

const validEmailTemplateText = "<p>{Code}</p><p>{ExpireMinutes}</p>"

type fakeEmailRepo struct {
	emailRepository
	template         *model.EmailProviderTemplate
	binding          *model.EmailSceneBinding
	syncRun          *model.EmailTemplateSyncRun
	sendLog          *model.EmailSendLog
	applyCounts      [4]uint
	applyErr         error
	applyCalled      bool
	updateCalled     bool
	cleanupRows      int64
	cleanupErr       error
	staleSendChanged bool
	failStaleCalled  bool
	failSyncCalled   bool
	runningSync      bool
	runningSyncErr   error
	onCreateSendLog  func()
}

func (f *fakeEmailRepo) GetTemplate(context.Context, uint64) (*model.EmailProviderTemplate, error) {
	if f.template == nil {
		return nil, repository.ErrEmailNotFound
	}
	copy := *f.template
	return &copy, nil
}
func (f *fakeEmailRepo) UpdateTemplateStatus(_ context.Context, _ uint64, version uint64, enabled bool) error {
	f.updateCalled = true
	if f.template == nil || f.template.Version != version {
		return repository.ErrEmailConflict
	}
	f.template.LocalEnabled, f.template.Version = enabled, version+1
	return nil
}
func (f *fakeEmailRepo) BoundScenes(context.Context, []uint64) (map[uint64][]string, error) {
	return map[uint64][]string{}, nil
}
func (f *fakeEmailRepo) GetBinding(context.Context, string) (*model.EmailSceneBinding, *model.EmailProviderTemplate, error) {
	if f.binding == nil || f.template == nil {
		return nil, nil, gorm.ErrRecordNotFound
	}
	b, tpl := *f.binding, *f.template
	return &b, &tpl, nil
}
func (f *fakeEmailRepo) FindSyncByIdempotency(context.Context, string, string) (*model.EmailTemplateSyncRun, error) {
	if f.syncRun == nil {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *f.syncRun
	return &copy, nil
}
func (f *fakeEmailRepo) CreateSyncRun(_ context.Context, run *model.EmailTemplateSyncRun) error {
	run.ID = 41
	copy := *run
	f.syncRun = &copy
	return nil
}
func (f *fakeEmailRepo) GetSyncRun(context.Context, uint64) (*model.EmailTemplateSyncRun, error) {
	if f.syncRun == nil {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *f.syncRun
	return &copy, nil
}
func (f *fakeEmailRepo) HasRunningSync(context.Context) (bool, error) {
	return f.runningSync, f.runningSyncErr
}
func (f *fakeEmailRepo) FindStaleSyncRuns(context.Context, time.Time) ([]model.EmailTemplateSyncRun, error) {
	return nil, nil
}
func (f *fakeEmailRepo) FailStaleSync(_ context.Context, _ uint64, now time.Time) error {
	f.failStaleCalled = true
	f.syncRun.Status = "failed"
	f.syncRun.CompletedAt = &now
	code := "sync_interrupted"
	f.syncRun.ErrorCode = &code
	return nil
}
func (f *fakeEmailRepo) ApplyTemplateSync(_ context.Context, _ uint64, _ []model.EmailProviderTemplate, now time.Time) (uint, uint, uint, uint, error) {
	f.applyCalled = true
	if f.applyErr != nil {
		return 0, 0, 0, 0, f.applyErr
	}
	f.syncRun.Status = "succeeded"
	f.syncRun.CompletedAt = &now
	f.syncRun.CreatedCount, f.syncRun.UpdatedCount, f.syncRun.MissingCount, f.syncRun.UnchangedCount = f.applyCounts[0], f.applyCounts[1], f.applyCounts[2], f.applyCounts[3]
	return f.applyCounts[0], f.applyCounts[1], f.applyCounts[2], f.applyCounts[3], nil
}
func (f *fakeEmailRepo) FailSync(_ context.Context, _ uint64, code, message string) error {
	f.failSyncCalled = true
	f.syncRun.Status = "failed"
	f.syncRun.ErrorCode, f.syncRun.ErrorMessage = &code, &message
	now := time.Now().UTC()
	f.syncRun.CompletedAt = &now
	return nil
}
func (f *fakeEmailRepo) FindAllowlistByHMAC(context.Context, string) (*model.EmailTestRecipientAllowlist, error) {
	return &model.EmailTestRecipientAllowlist{ID: 1, Status: "active"}, nil
}
func (f *fakeEmailRepo) FindSendLogByIdempotency(_ context.Context, scope, keyHash string) (*model.EmailSendLog, error) {
	if f.sendLog == nil || f.sendLog.IdempotencyScope != scope || f.sendLog.IdempotencyKeyHash != keyHash {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *f.sendLog
	return &copy, nil
}
func (f *fakeEmailRepo) FindBlockingSendByScope(_ context.Context, scope string, now time.Time) (*model.EmailSendLog, error) {
	if f.sendLog == nil || f.sendLog.IdempotencyScope != scope {
		return nil, gorm.ErrRecordNotFound
	}
	blocked := f.sendLog.Status == "pending" || (f.sendLog.Status == "failed" && f.sendLog.FailureReason != nil && *f.sendLog.FailureReason == "provider_outcome_unknown" && sendCooldownUntil(f.sendLog).After(now))
	if !blocked {
		return nil, gorm.ErrRecordNotFound
	}
	copy := *f.sendLog
	return &copy, nil
}
func (f *fakeEmailRepo) FailStalePendingSend(context.Context, string, string, time.Time) (bool, error) {
	if f.staleSendChanged && f.sendLog != nil {
		reason := "provider_outcome_unknown"
		f.sendLog.Status, f.sendLog.FailureReason = "failed", &reason
		return true, nil
	}
	return false, nil
}
func (f *fakeEmailRepo) CreateSendLog(_ context.Context, entry *model.EmailSendLog) error {
	entry.ID = 51
	copy := *entry
	f.sendLog = &copy
	if f.onCreateSendLog != nil {
		f.onCreateSendLog()
	}
	return nil
}
func (f *fakeEmailRepo) FinalizeSendLog(_ context.Context, _ uint64, status string, requestID, reason *string) error {
	f.sendLog.Status, f.sendLog.ProviderRequestID, f.sendLog.FailureReason = status, requestID, reason
	return nil
}
func (f *fakeEmailRepo) DeleteRevokedAllowlistBefore(context.Context, time.Time) (int64, error) {
	return f.cleanupRows, f.cleanupErr
}

type fakeVerificationRepo struct {
	verificationRepository
	created            *model.VerificationCode
	finalized          string
	finalLog           *model.EmailSendLog
	finalizeContextErr error
	latest             *model.VerificationCode
	staleLog           *model.EmailSendLog
	staleChanged       bool
	onCreatePending    func()
}

func (f *fakeVerificationRepo) Create(_ context.Context, v *model.VerificationCode) error {
	v.ID = 61
	copy := *v
	f.created = &copy
	return nil
}
func (f *fakeVerificationRepo) CreateEmailSendPending(_ context.Context, v *model.VerificationCode, entry *model.EmailSendLog) error {
	v.ID = 61
	entry.ID = 62
	entry.VerificationCodeID = &v.ID
	vCopy, logCopy := *v, *entry
	f.created, f.finalLog = &vCopy, &logCopy
	if f.onCreatePending != nil {
		f.onCreatePending()
	}
	return nil
}
func (f *fakeVerificationRepo) FindLatestByScope(context.Context, string, time.Time) (*model.VerificationCode, error) {
	if f.latest != nil {
		copy := *f.latest
		return &copy, nil
	}
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeVerificationRepo) FailStaleEmailSend(context.Context, string, string, time.Time) (bool, error) {
	if !f.staleChanged {
		return false, nil
	}
	reason := "provider_outcome_unknown"
	if f.latest != nil {
		f.latest.SendStatus = "failed"
	}
	if f.staleLog != nil {
		f.staleLog.Status, f.staleLog.FailureReason = "failed", &reason
	}
	return true, nil
}
func (f *fakeVerificationRepo) FinalizeEmailSend(ctx context.Context, _ uint64, status string, _ *time.Time, entry *model.EmailSendLog) error {
	f.finalized = status
	f.finalizeContextErr = ctx.Err()
	if entry != nil {
		copy := *entry
		f.finalLog = &copy
	}
	return nil
}

type cancelingAdapter struct{ cancel context.CancelFunc }

func (a cancelingAdapter) Ready() bool { return true }
func (a cancelingAdapter) QueryTemplates(context.Context, int, int) ([]ProviderTemplate, bool, error) {
	return nil, false, ErrDirectMailUpstream
}
func (a cancelingAdapter) DescribeTemplate(context.Context, string) (ProviderTemplate, error) {
	return ProviderTemplate{}, ErrDirectMailUpstream
}
func (a cancelingAdapter) SingleSendMail(context.Context, EmailMessage) (EmailAcceptance, error) {
	a.cancel()
	return EmailAcceptance{}, context.DeadlineExceeded
}

type recordingEmailAdapter struct {
	message EmailMessage
	calls   int
}

func (a *recordingEmailAdapter) Ready() bool { return true }
func (a *recordingEmailAdapter) QueryTemplates(context.Context, int, int) ([]ProviderTemplate, bool, error) {
	return nil, false, ErrDirectMailUpstream
}
func (a *recordingEmailAdapter) DescribeTemplate(context.Context, string) (ProviderTemplate, error) {
	return ProviderTemplate{}, ErrDirectMailUpstream
}
func (a *recordingEmailAdapter) SingleSendMail(_ context.Context, message EmailMessage) (EmailAcceptance, error) {
	a.calls++
	a.message = message
	return EmailAcceptance{RequestID: "recorded-request"}, nil
}

type fakeAuditor struct {
	mu                      sync.Mutex
	failAttempt, failResult bool
	actions                 []string
}

func (f *fakeAuditor) Record(_ context.Context, _ *uint64, _, action string, _, _ *string, _ string, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, action)
	if (f.failAttempt && len(action) >= 8 && action[len(action)-8:] == ".attempt") || (f.failResult && len(action) >= 7 && action[len(action)-7:] == ".result") {
		return errors.New("审计测试失败")
	}
	return nil
}

type failingQueryAdapter struct{}

func (failingQueryAdapter) Ready() bool { return true }
func (failingQueryAdapter) QueryTemplates(context.Context, int, int) ([]ProviderTemplate, bool, error) {
	return nil, false, ErrDirectMailUpstream
}
func (failingQueryAdapter) DescribeTemplate(context.Context, string) (ProviderTemplate, error) {
	return ProviderTemplate{}, ErrDirectMailUpstream
}
func (failingQueryAdapter) SingleSendMail(context.Context, EmailMessage) (EmailAcceptance, error) {
	return EmailAcceptance{}, ErrDirectMailUpstream
}

type allowEmailRecipient struct{}

func (allowEmailRecipient) AuthorizeEmailOTPRecipient(context.Context, string, string, uint64, string, string) error {
	return nil
}

type denyEmailRecipient struct{}

func (denyEmailRecipient) AuthorizeEmailOTPRecipient(context.Context, string, string, uint64, string, string) error {
	return errors.New("拒绝")
}

// blockingSendAdapter 在测试中把供应商外呼停在可观测位置，用于验证配置写入无法穿过发送边界。
type blockingSendAdapter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *blockingSendAdapter) Ready() bool { return true }
func (a *blockingSendAdapter) QueryTemplates(context.Context, int, int) ([]ProviderTemplate, bool, error) {
	return nil, false, nil
}
func (a *blockingSendAdapter) DescribeTemplate(context.Context, string) (ProviderTemplate, error) {
	return ProviderTemplate{}, nil
}
func (a *blockingSendAdapter) SingleSendMail(ctx context.Context, _ EmailMessage) (EmailAcceptance, error) {
	a.once.Do(func() { close(a.entered) })
	select {
	case <-a.release:
		return EmailAcceptance{RequestID: "mock-blocking-request", Mock: true}, nil
	case <-ctx.Done():
		return EmailAcceptance{}, ctx.Err()
	}
}

func testLock() func(context.Context, string, time.Duration) (*emailLease, bool, error) {
	var mu sync.Mutex
	locked := map[string]bool{}
	return func(_ context.Context, scope string, _ time.Duration) (*emailLease, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		if locked[scope] {
			return nil, false, nil
		}
		locked[scope] = true
		lease := &emailLease{owned: func(context.Context) bool { return true }}
		lease.release = func() { mu.Lock(); delete(locked, scope); mu.Unlock() }
		return lease, true, nil
	}
}

func newFakeService(repo *fakeEmailRepo, verification *fakeVerificationRepo, adapter DirectMailAdapter, audit *fakeAuditor) *EmailService {
	svc := NewEmailService(repo, verification, adapter, audit, nil, strings.Repeat("a", 32), strings.Repeat("b", 32), "test", "mock")
	svc.lockOverride = testLock()
	svc.rateLimitOverride = func(context.Context, string, int, time.Duration) (bool, error) { return true, nil }
	svc.recipientAuthorizer = allowEmailRecipient{}
	return svc
}

func TestSyncCountsIdempotencyAndRollback(t *testing.T) {
	t.Run("新增更新missing计数及幂等", func(t *testing.T) {
		repo := &fakeEmailRepo{applyCounts: [4]uint{1, 2, 3, 4}}
		adapter := &MockEmailAdapter{Templates: []ProviderTemplate{{TemplateID: "fake-template", Name: "模板", Subject: "主题", TemplateText: "${Code} ${ExpireMinutes}", Status: "approved"}}}
		svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
		first, err := svc.Sync(context.Background(), "sync-key", 7, "127.0.0.1")
		if err != nil || first.CreatedCount != 1 || first.UpdatedCount != 2 || first.MissingCount != 3 || first.UnchangedCount != 4 {
			t.Fatalf("同步计数错误: %#v %v", first, err)
		}
		replay, err := svc.Sync(context.Background(), "sync-key", 7, "127.0.0.1")
		if err != nil || !replay.Idempotent || replay.RunID != first.RunID {
			t.Fatalf("同步幂等错误: %#v %v", replay, err)
		}
	})
	t.Run("上游失败不应用镜像", func(t *testing.T) {
		repo := &fakeEmailRepo{}
		svc := newFakeService(repo, &fakeVerificationRepo{}, failingQueryAdapter{}, &fakeAuditor{})
		_, err := svc.Sync(context.Background(), "rollback-key", 7, "127.0.0.1")
		if !errors.Is(err, ErrEmailUpstream) || repo.applyCalled || repo.syncRun == nil || repo.syncRun.Status != "failed" {
			t.Fatalf("同步回滚语义错误: %v", err)
		}
	})
	t.Run("fencing冲突不误记提交失败", func(t *testing.T) {
		repo := &fakeEmailRepo{applyErr: repository.ErrEmailConflict}
		adapter := &MockEmailAdapter{Templates: []ProviderTemplate{{TemplateID: "fake-template", Name: "模板", Subject: "主题", TemplateText: "${Code} ${ExpireMinutes}", Status: "approved"}}}
		svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
		_, err := svc.Sync(context.Background(), "fencing-key", 7, "127.0.0.1")
		if !errors.Is(err, ErrEmailConflict) || repo.failSyncCalled {
			t.Fatalf("fencing冲突不得误记database_commit_failed: %v", err)
		}
	})
}

func TestTemplateVersionAndAuditFreeze(t *testing.T) {
	tpl := &model.EmailProviderTemplate{ID: 1, ProviderStatus: "approved", VariablesComplete: true, Version: 3}
	repo := &fakeEmailRepo{template: tpl}
	audit := &fakeAuditor{failAttempt: true}
	svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, audit)
	if _, err := svc.SetTemplateStatus(context.Background(), 1, 3, 7, true, "127.0.0.1"); err == nil || repo.updateCalled {
		t.Fatal("attempt 审计失败时不得执行写操作")
	}
	audit.failAttempt, audit.failResult = false, true
	result, err := svc.SetTemplateStatus(context.Background(), 1, 3, 7, true, "127.0.0.1")
	if err != nil || result.Version != 4 {
		t.Fatalf("结果审计失败不得把已生效动作返回为失败: %#v %v", result, err)
	}
	if _, err := svc.SetTemplateStatus(context.Background(), 1, 3, 7, false, "127.0.0.1"); !errors.Is(err, ErrEmailConflict) {
		t.Fatalf("旧版本必须冲突: %v", err)
	}
}

func TestOTPFiveScenesAndProviderFailures(t *testing.T) {
	for _, scene := range []string{"register", "login", "reset_password", "bind_email", "admin_verify"} {
		t.Run(scene, func(t *testing.T) {
			templateID := uint64(1)
			repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: scene, TemplateID: &templateID, Enabled: true, Version: 1}}
			verification := &fakeVerificationRepo{}
			svc := newFakeService(repo, verification, &MockEmailAdapter{RequestID: "fake-request"}, &fakeAuditor{})
			ctx := context.Background()
			if scene == "bind_email" {
				ctx = withEmailOTPIdentity(ctx, "/api/me/verification-codes/email", 7, fakeAddress("recipient"))
			} else if scene == "admin_verify" {
				ctx = withEmailOTPIdentity(ctx, "/api/admin/auth/verification-codes/email", 7, fakeAddress("recipient"))
			}
			if _, _, err := svc.SendOTP(ctx, "business-fake", scene, fakeAddress("recipient"), strings.Repeat("0", 6), 10); err != nil || verification.finalized != "accepted" || verification.created.TargetValue != nil || verification.created.TargetHash == nil {
				t.Fatalf("场景发送隔离失败: %v", err)
			}
		})
	}
	for _, tc := range []struct {
		upstreamErr error
		expectedErr error
	}{{errors.New("供应商拒绝"), ErrEmailUpstream}, {context.DeadlineExceeded, ErrEmailOutcomeUnknown}} {
		templateID := uint64(1)
		repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}}
		verification := &fakeVerificationRepo{}
		svc := newFakeService(repo, verification, &MockEmailAdapter{SendError: tc.upstreamErr}, &fakeAuditor{})
		if _, _, err := svc.SendOTP(context.Background(), "business-fake", "register", fakeAddress("recipient"), strings.Repeat("0", 6), 10); !errors.Is(err, tc.expectedErr) || verification.finalized != "failed" {
			t.Fatalf("拒绝或超时必须失败关闭: %v", err)
		}
	}
}

func TestOTPSevenPrerequisitesStopBeforeAdapter(t *testing.T) {
	templateID := uint64(1)
	baseTemplate := model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}
	baseBinding := model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}
	tests := []struct {
		name     string
		binding  *model.EmailSceneBinding
		template *model.EmailProviderTemplate
		want     error
	}{
		{name: "未绑定", want: ErrEmailBindingMissing},
		{name: "绑定停用", binding: func() *model.EmailSceneBinding { v := baseBinding; v.Enabled = false; return &v }(), template: &baseTemplate, want: ErrEmailSceneDisabled},
		{name: "本地模板停用", binding: &baseBinding, template: func() *model.EmailProviderTemplate { v := baseTemplate; v.LocalEnabled = false; return &v }(), want: ErrEmailTemplateOff},
		{name: "草稿", binding: &baseBinding, template: func() *model.EmailProviderTemplate { v := baseTemplate; v.ProviderStatus = "draft"; return &v }(), want: ErrEmailTemplateDraft},
		{name: "审核中", binding: &baseBinding, template: func() *model.EmailProviderTemplate { v := baseTemplate; v.ProviderStatus = "pending"; return &v }(), want: ErrEmailTemplateReview},
		{name: "审核拒绝", binding: &baseBinding, template: func() *model.EmailProviderTemplate { v := baseTemplate; v.ProviderStatus = "rejected"; return &v }(), want: ErrEmailTemplateReject},
		{name: "供应商缺失", binding: &baseBinding, template: func() *model.EmailProviderTemplate { v := baseTemplate; v.Missing = true; return &v }(), want: ErrEmailTemplateGone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &MockEmailAdapter{}
			svc := newFakeService(&fakeEmailRepo{binding: tc.binding, template: tc.template}, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
			if _, _, err := svc.SendOTP(context.Background(), "business", "register", fakeAddress("recipient"), "000000", 10); !errors.Is(err, tc.want) {
				t.Fatalf("前置状态错误不精确: got=%v want=%v", err, tc.want)
			}
			if adapter.Calls != 0 || svc.AdapterCallCount("send_mail", "register", "accepted") != 0 {
				t.Fatal("前置校验失败不得调用或计量供应商")
			}
		})
	}
}

func TestDedicatedRecipientAuthorizationFailsClosed(t *testing.T) {
	templateID := uint64(1)
	repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: "bind_email", TemplateID: &templateID, Enabled: true, Version: 1}}
	tests := []struct {
		name string
		ctx  context.Context
		deny bool
	}{
		{name: "缺少身份", ctx: context.Background()},
		{name: "用户为零", ctx: withEmailOTPIdentity(context.Background(), "/api/me/verification-codes/email", 0, fakeAddress("recipient"))},
		{name: "端点错误", ctx: withEmailOTPIdentity(context.Background(), "/wrong", 7, fakeAddress("recipient"))},
		{name: "目标不一致", ctx: withEmailOTPIdentity(context.Background(), "/api/me/verification-codes/email", 7, fakeAddress("other"))},
		{name: "真相源拒绝", ctx: withEmailOTPIdentity(context.Background(), "/api/me/verification-codes/email", 7, fakeAddress("recipient")), deny: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &MockEmailAdapter{}
			svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
			if tc.deny {
				svc.recipientAuthorizer = denyEmailRecipient{}
			}
			if _, _, err := svc.SendOTP(tc.ctx, "business", "bind_email", fakeAddress("recipient"), "000000", 10); !errors.Is(err, ErrEmailRecipientDeny) || adapter.Calls != 0 {
				t.Fatalf("专属收件人校验必须关闭失败: err=%v calls=%d", err, adapter.Calls)
			}
		})
	}
}

func TestEmailAccountRateLimitUsesOpaqueKey(t *testing.T) {
	templateID := uint64(1)
	adapter := &MockEmailAdapter{}
	svc := newFakeService(&fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}}, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
	count := 0
	var key string
	svc.rateLimitOverride = func(_ context.Context, got string, limit int, window time.Duration) (bool, error) {
		key = got
		count++
		return count <= limit && limit == 10 && window == time.Minute, nil
	}
	recipient := fakeAddress("rate-limit-target")
	for i := 0; i < 10; i++ {
		if _, _, err := svc.SendOTP(context.Background(), fmt.Sprintf("business-%d", i), "register", recipient, "000000", 10); err != nil {
			t.Fatalf("限流窗口内请求失败: %v", err)
		}
	}
	if _, _, err := svc.SendOTP(context.Background(), "business-11", "register", recipient, "000000", 10); !errors.Is(err, ErrEmailRateLimited) {
		t.Fatalf("第十一次请求必须限流: %v", err)
	}
	if strings.Contains(key, recipient) || strings.Contains(key, "rate-limit-target") || adapter.Calls != 10 {
		t.Fatalf("限流键必须脱敏且超限不得外呼: key=%s calls=%d", key, adapter.Calls)
	}
}

func TestEmailAccountRateLimitSharesBucketsAcrossScenes(t *testing.T) {
	svc := newFakeService(&fakeEmailRepo{}, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
	keys := make([]string, 0, 7)
	svc.rateLimitOverride = func(_ context.Context, key string, limit int, window time.Duration) (bool, error) {
		keys = append(keys, key)
		return limit == 10 && window == time.Minute, nil
	}
	targetA := crypto.HMAC256(fakeAddress("account-a"), svc.addressSecret)
	targetB := crypto.HMAC256(fakeAddress("account-b"), svc.addressSecret)

	for _, scene := range []string{"register", "login", "reset_password"} {
		if err := svc.checkAccountRateLimit(context.Background(), scene, 0, targetA); err != nil {
			t.Fatal(err)
		}
	}
	for _, scene := range []string{"bind_email", "admin_verify"} {
		if err := svc.checkAccountRateLimit(context.Background(), scene, 7, targetA); err != nil {
			t.Fatal(err)
		}
	}
	_ = svc.checkAccountRateLimit(context.Background(), "register", 0, targetB)
	_ = svc.checkAccountRateLimit(context.Background(), "admin_verify", 8, targetA)

	if keys[0] != keys[1] || keys[1] != keys[2] {
		t.Fatalf("同一邮箱的公开场景必须累计到同一限流桶: %#v", keys[:3])
	}
	if keys[3] != keys[4] {
		t.Fatalf("同一用户的绑定与管理员场景必须累计到同一限流桶: %#v", keys[3:5])
	}
	if keys[0] == keys[5] || keys[3] == keys[6] {
		t.Fatalf("不同账号必须保持限流隔离: %#v", keys)
	}
}

func TestAdapterMetricsCountOnlyActualCalls(t *testing.T) {
	if EmailAdapterCallsMetricName != "email_adapter_calls_total" {
		t.Fatalf("指标名称不符合冻结契约: %s", EmailAdapterCallsMetricName)
	}
	repo := &fakeEmailRepo{applyCounts: [4]uint{1, 0, 0, 0}}
	adapter := &MockEmailAdapter{Templates: []ProviderTemplate{{TemplateID: "fake-template", Name: "模板", Subject: "主题", TemplateText: "${Code} ${ExpireMinutes}", Status: "approved"}}}
	svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
	if _, err := svc.Sync(context.Background(), "metric-key", 7, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if got := svc.AdapterCallCount("query_templates", "template_sync", "accepted"); got != 1 {
		t.Fatalf("查询模板调用计数错误: %d", got)
	}
	if got := svc.AdapterCallCount("describe_template", "template_sync", "accepted"); got != 1 {
		t.Fatalf("模板详情调用计数错误: %d", got)
	}
	if _, err := svc.Sync(context.Background(), "metric-key", 7, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if svc.AdapterCallCount("query_templates", "template_sync", "accepted") != 1 || svc.AdapterCallCount("describe_template", "template_sync", "accepted") != 1 {
		t.Fatal("幂等重放不得增加供应商调用指标")
	}
}

func TestAdapterMetricsClassifiesTimeout(t *testing.T) {
	templateID := uint64(1)
	svc := newFakeService(&fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}}, &fakeVerificationRepo{}, &MockEmailAdapter{SendError: context.DeadlineExceeded}, &fakeAuditor{})
	if _, _, err := svc.SendOTP(context.Background(), "business", "register", fakeAddress("recipient"), "000000", 10); !errors.Is(err, ErrEmailOutcomeUnknown) {
		t.Fatal(err)
	}
	if svc.AdapterCallCount("send_mail", "register", "timeout") != 1 || svc.AdapterCallCount("send_mail", "register", "failed") != 0 {
		t.Fatal("超时调用必须且只能计入 timeout")
	}
}

func TestLockCompetitionReturnsBusinessStateBeforeReadiness(t *testing.T) {
	lockBusy := func(context.Context, string, time.Duration) (*emailLease, bool, error) { return nil, false, nil }
	t.Run("模板测试发送正在处理", func(t *testing.T) {
		email := fakeAddress("recipient")
		repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake", Subject: "主题", ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true}}
		svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
		scope := fmt.Sprintf("admin-email-template-test:admin:%d:template:%d:scene:%s:recipient:%s", 9, 7, "register", svc.emailHMAC(email))
		repo.sendLog = &model.EmailSendLog{IdempotencyScope: scope, IdempotencyKeyHash: hash("other-key"), Purpose: "test", Status: "pending", SubmittedAt: time.Now().UTC()}
		svc.lockOverride = lockBusy
		if _, err := svc.TestSend(context.Background(), 7, "register", email, "new-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailSending) {
			t.Fatalf("锁竞争必须返回业务处理中: %v", err)
		}
	})
	t.Run("OTP 正在处理", func(t *testing.T) {
		templateID := uint64(1)
		repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}}
		svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
		email := fakeAddress("recipient")
		scope := "auth:register:email:" + svc.emailHMAC(email)
		expires := time.Now().UTC().Add(10 * time.Minute)
		repo.sendLog = &model.EmailSendLog{IdempotencyScope: scope, IdempotencyKeyHash: "other", Purpose: "otp", Status: "pending", SubmittedAt: time.Now().UTC(), ExpiresAt: &expires}
		svc.lockOverride = lockBusy
		if _, _, err := svc.SendOTP(context.Background(), "business", "register", email, "000000", 10); !errors.Is(err, ErrEmailSending) {
			t.Fatalf("锁竞争必须返回 OTP 处理中: %v", err)
		}
	})
	t.Run("模板同步正在处理", func(t *testing.T) {
		repo := &fakeEmailRepo{runningSync: true}
		svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
		svc.lockOverride = lockBusy
		if _, err := svc.Sync(context.Background(), "new-key", 7, "127.0.0.1"); !errors.Is(err, ErrEmailSyncRunning) {
			t.Fatalf("锁竞争必须返回同步正在进行: %v", err)
		}
	})
}

func TestStalePendingConvergesToUnknownTombstone(t *testing.T) {
	email := fakeAddress("recipient")
	repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake", Subject: "主题", ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true}, staleSendChanged: true}
	svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
	scope := fmt.Sprintf("admin-email-template-test:admin:%d:template:%d:scene:%s:recipient:%s", 9, 7, "register", svc.emailHMAC(email))
	repo.sendLog = &model.EmailSendLog{IdempotencyScope: scope, IdempotencyKeyHash: hash("old-key"), Purpose: "test", Status: "pending", SubmittedAt: time.Now().UTC().Add(-6 * time.Minute)}
	if _, err := svc.TestSend(context.Background(), 7, "register", email, "new-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailOutcomePending) {
		t.Fatalf("陈旧 pending 必须收敛为未知墓碑并阻断新 key: %v", err)
	}
	if repo.sendLog.Status != "failed" || repo.sendLog.FailureReason == nil || *repo.sendLog.FailureReason != "provider_outcome_unknown" {
		t.Fatalf("陈旧 pending 收敛结果错误: %#v", repo.sendLog)
	}
}

func TestStalePendingOldKeyActivelyConverges(t *testing.T) {
	t.Run("模板测试旧 key", func(t *testing.T) {
		email := fakeAddress("recipient")
		repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake", Subject: "主题", ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true}, staleSendChanged: true}
		svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
		rh := svc.emailHMAC(email)
		scope := fmt.Sprintf("admin-email-template-test:admin:%d:template:%d:scene:%s:recipient:%s", 9, 7, "register", rh)
		fp := hash("POST\n/api/admin/email/templates/7/test-send\nregister\n" + rh)
		repo.sendLog = &model.EmailSendLog{IdempotencyScope: scope, IdempotencyKeyHash: hash("old-key"), RequestFingerprint: fp, Purpose: "test", Status: "pending", SubmittedAt: time.Now().UTC().Add(-6 * time.Minute)}
		if _, err := svc.TestSend(context.Background(), 7, "register", email, "old-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailOutcomeUnknown) {
			t.Fatalf("陈旧旧 key 必须主动收敛后重放 502 语义: %v", err)
		}
		if repo.sendLog.Status != "failed" || repo.sendLog.FailureReason == nil || *repo.sendLog.FailureReason != "provider_outcome_unknown" {
			t.Fatal("测试发送旧 key 未收敛为未知墓碑")
		}
	})

	t.Run("OTP 旧 key", func(t *testing.T) {
		templateID := uint64(1)
		email := fakeAddress("recipient")
		repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}}
		verification := &fakeVerificationRepo{staleChanged: true}
		svc := newFakeService(repo, verification, &MockEmailAdapter{}, &fakeAuditor{})
		target := svc.emailHMAC(email)
		scope := "auth:register:email:" + target
		business := "business-old"
		fingerprint := hash(fmt.Sprintf("%s|%s|%s|otp|%d|%d|%d", "", "register", target, 10, 1, 1))
		expires := time.Now().UTC().Add(4 * time.Minute)
		verification.latest = &model.VerificationCode{ID: 61, SendStatus: "pending", BusinessRequestNo: &business, IdempotencyScope: &scope, RequestFingerprint: &fingerprint, ExpiresAt: expires}
		verificationID := uint64(61)
		repo.sendLog = &model.EmailSendLog{VerificationCodeID: &verificationID, BusinessRequestNo: business, IdempotencyScope: scope, IdempotencyKeyHash: crypto.HMAC256(business+"|"+scope, svc.idempotencySecret), RequestFingerprint: fingerprint, Purpose: "otp", Status: "pending", SubmittedAt: time.Now().UTC().Add(-6 * time.Minute), ExpiresAt: &expires}
		verification.staleLog = repo.sendLog
		if _, _, err := svc.SendOTP(context.Background(), business, "register", email, "000000", 10); !errors.Is(err, ErrEmailOutcomeUnknown) {
			t.Fatalf("OTP 陈旧旧 key 必须在 FindLatest pending 返回前事务收敛: %v", err)
		}
		if verification.latest.SendStatus != "failed" || repo.sendLog.Status != "failed" || repo.sendLog.FailureReason == nil || *repo.sendLog.FailureReason != "provider_outcome_unknown" {
			t.Fatal("OTP 验证码与发送日志未共同收敛")
		}
	})

	t.Run("未到阈值保持处理中", func(t *testing.T) {
		email := fakeAddress("recipient")
		repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake", Subject: "主题", ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true}}
		svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
		rh := svc.emailHMAC(email)
		scope := fmt.Sprintf("admin-email-template-test:admin:%d:template:%d:scene:%s:recipient:%s", 9, 7, "register", rh)
		fp := hash("POST\n/api/admin/email/templates/7/test-send\nregister\n" + rh)
		repo.sendLog = &model.EmailSendLog{IdempotencyScope: scope, IdempotencyKeyHash: hash("old-key"), RequestFingerprint: fp, Purpose: "test", Status: "pending", SubmittedAt: time.Now().UTC().Add(-4 * time.Minute)}
		if _, err := svc.TestSend(context.Background(), 7, "register", email, "old-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailSending) {
			t.Fatalf("五分钟内 pending 必须保持 409: %v", err)
		}
	})
}

func TestOTPUnknownFinalizesVerificationAndTombstoneTogether(t *testing.T) {
	templateID := uint64(1)
	repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}}
	verification := &fakeVerificationRepo{}
	svc := newFakeService(repo, verification, &MockEmailAdapter{SendError: context.DeadlineExceeded}, &fakeAuditor{})
	if _, _, err := svc.SendOTP(context.Background(), "business-fake", "register", fakeAddress("recipient"), strings.Repeat("0", 6), 10); !errors.Is(err, ErrEmailOutcomeUnknown) {
		t.Fatalf("OTP 超时必须返回未知结果: %v", err)
	}
	if verification.finalized != "failed" || verification.finalLog == nil || verification.finalLog.FailureReason == nil || *verification.finalLog.FailureReason != "provider_outcome_unknown" {
		t.Fatal("OTP unknown 必须通过同一事务入口同时写验证码 failed 与发送墓碑")
	}
	if verification.finalLog.ExpiresAt == nil || !sendCooldownUntil(verification.finalLog).Equal(*verification.finalLog.ExpiresAt) {
		t.Fatal("OTP cooldown_until 必须由原 expires_at 派生")
	}
}

func TestOTPUnknownUsesDetachedFinalizationContext(t *testing.T) {
	templateID := uint64(1)
	repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 1, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}, binding: &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}}
	verification := &fakeVerificationRepo{}
	ctx, cancel := context.WithCancel(context.Background())
	svc := newFakeService(repo, verification, cancelingAdapter{cancel: cancel}, &fakeAuditor{})
	if _, _, err := svc.SendOTP(ctx, "business-fake", "register", fakeAddress("recipient"), strings.Repeat("0", 6), 10); !errors.Is(err, ErrEmailOutcomeUnknown) {
		t.Fatalf("取消请求后的未知结果错误不正确: %v", err)
	}
	if verification.finalizeContextErr != nil || verification.finalized != "failed" {
		t.Fatal("请求 context 取消后仍必须使用独立短上下文提交 unknown 墓碑")
	}
}

func TestSendOTPRendersFrozenTemplateBodyWithoutTouchingCSSOrJSON(t *testing.T) {
	templateID := uint64(1)
	templateText := `<style>.box { color: red; }.empty{color}</style><pre>{"Code":"metadata"}</pre><strong>{Code}</strong><i>${Code}</i><b>{{ Code }}</b><span>{ExpireMinutes}</span><em>${ExpireMinutes}</em><small>{{ ExpireMinutes }}</small>`
	repo := &fakeEmailRepo{
		template: &model.EmailProviderTemplate{ID: templateID, ProviderTemplateID: "fake-template", Subject: "验证码通知", TemplateText: templateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1},
		binding:  &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1},
	}
	adapter := &recordingEmailAdapter{}
	verification := &fakeVerificationRepo{}
	svc := newFakeService(repo, verification, adapter, &fakeAuditor{})
	if _, _, err := svc.SendOTP(context.Background(), "business-render", "register", fakeAddress("recipient"), "654321", 10); err != nil {
		t.Fatalf("本地模板渲染发送失败: %v", err)
	}
	want := `<style>.box { color: red; }.empty{color}</style><pre>{"Code":"metadata"}</pre><strong>654321</strong><i>654321</i><b>654321</b><span>10</span><em>10</em><small>10</small>`
	if adapter.calls != 1 || adapter.message.HTMLBody != want {
		t.Fatalf("本地渲染正文不符合契约: calls=%d body_length=%d want_length=%d", adapter.calls, len(adapter.message.HTMLBody), len(want))
	}
	if verification.finalLog == nil || verification.finalLog.ProviderTemplateID != "fake-template" {
		t.Fatal("TemplateId 必须保留在发送日志用于平台追踪")
	}
}

func TestSendOTPRejectsMalformedOrIncompleteTemplateBeforePendingState(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "空正文", body: ""},
		{name: "缺少有效期", body: "验证码 {Code}"},
		{name: "变量大小写错误", body: "验证码 {code}，有效 {expireminutes} 分钟"},
		{name: "变量不完整", body: "验证码 {Code， 有效 {ExpireMinutes} 分钟"},
		{name: "尾随花括号", body: "验证码 ${Code}}，有效 {{ExpireMinutes}}} 分钟"},
		{name: "三重与嵌套", body: "验证码 {{{Code}}}，有效 {{${ExpireMinutes}}} 分钟"},
		{name: "残留额外变量", body: "验证码 {Code}，有效 {ExpireMinutes} 分钟，用户 ${UserName}"},
		{name: "官方风格额外变量", body: "验证码 {Code}，有效 {ExpireMinutes} 分钟，用户 {UserName}"},
		{name: "官方风格额外变量外围空格", body: "验证码 {Code}，有效 {ExpireMinutes} 分钟，用户 { UserName }"},
		{name: "官方风格小写开头驼峰变量", body: "验证码 {Code}，有效 {ExpireMinutes} 分钟，用户 {userName}"},
		{name: "美元风格额外变量缺右括号", body: "验证码 {Code}，有效 {ExpireMinutes} 分钟，用户 ${UserName"},
		{name: "双花括号额外变量尾随括号", body: "验证码 {Code}，有效 {ExpireMinutes} 分钟，用户 {{UserName}}}"},
		{name: "正文超过八十KB", body: "{Code}{ExpireMinutes}" + strings.Repeat("a", 80*1024)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			templateID := uint64(1)
			repo := &fakeEmailRepo{
				template: &model.EmailProviderTemplate{ID: templateID, ProviderTemplateID: "fake-template", Subject: "验证码", TemplateText: tc.body, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1},
				binding:  &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1},
			}
			adapter := &recordingEmailAdapter{}
			verification := &fakeVerificationRepo{}
			svc := newFakeService(repo, verification, adapter, &fakeAuditor{})
			if _, _, err := svc.SendOTP(context.Background(), "business-invalid", "register", fakeAddress("recipient"), "654321", 10); !errors.Is(err, ErrEmailVariables) {
				t.Fatalf("非法模板必须返回变量契约错误: %v", err)
			}
			if adapter.calls != 0 || verification.created != nil || verification.finalLog != nil {
				t.Fatalf("非法模板不得创建 pending 或调用供应商: calls=%d created=%t finalized=%t", adapter.calls, verification.created != nil, verification.finalLog != nil)
			}
		})
	}
}

func TestEmailHMACSecretsMustBeStrongAndSeparated(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer redisClient.Close()
	for _, tc := range []struct{ address, idem string }{
		{"short", strings.Repeat("b", 32)},
		{strings.Repeat("a", 32), "short"},
		{strings.Repeat("a", 32), strings.Repeat("a", 32)},
	} {
		svc := NewEmailService(nil, nil, &MockEmailAdapter{}, nil, redisClient, tc.address, tc.idem, "test", "mock")
		if svc.Ready() {
			t.Fatal("邮件 HMAC 密钥过短或复用时必须失败关闭")
		}
	}
}

func TestTestSendScopeUnknownTombstoneAndNewKeyCooldown(t *testing.T) {
	template := &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}
	repo := &fakeEmailRepo{template: template}
	adapter := &MockEmailAdapter{SendError: context.DeadlineExceeded}
	svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
	email := fakeAddress("recipient")
	if _, err := svc.TestSend(context.Background(), 7, "register", email, "old-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailOutcomeUnknown) {
		t.Fatalf("首次超时必须持久化未知结果: %v", err)
	}
	if repo.sendLog == nil || repo.sendLog.FailureReason == nil || *repo.sendLog.FailureReason != "provider_outcome_unknown" || repo.sendLog.ExpiresAt != nil {
		t.Fatal("测试发送未知结果必须复用 failed 行作为 expires_at 为空的墓碑")
	}
	if adapter.Calls != 1 {
		t.Fatalf("首次动作应只外呼一次: %d", adapter.Calls)
	}
	if _, err := svc.TestSend(context.Background(), 7, "register", email, "old-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailOutcomeUnknown) || adapter.Calls != 1 {
		t.Fatalf("旧 key 必须重放原未知结果且不外呼: %v calls=%d", err, adapter.Calls)
	}
	if _, err := svc.TestSend(context.Background(), 7, "register", email, "new-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailOutcomePending) || adapter.Calls != 1 {
		t.Fatalf("墓碑期新 key 必须 409 语义且不外呼: %v calls=%d", err, adapter.Calls)
	}
	// test 的 cooldown_until 固定由 submitted_at 加十分钟派生；到期后新 key 可重新动作。
	repo.sendLog.SubmittedAt = time.Now().UTC().Add(-11 * time.Minute)
	adapter.SendError = nil
	if _, err := svc.TestSend(context.Background(), 7, "register", email, "new-key", 9, "127.0.0.1"); err != nil || adapter.Calls != 2 {
		t.Fatalf("冷却到期后新 key 应允许重新发送: %v calls=%d", err, adapter.Calls)
	}
}

func TestProviderRejectPersistsOnlySafeCategory(t *testing.T) {
	template := &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}
	repo := &fakeEmailRepo{template: template}
	adapter := &MockEmailAdapter{SendError: newDirectMailProviderReject("Sensitive.Internal.Detail", 500)}
	svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
	if _, err := svc.TestSend(context.Background(), 7, "register", fakeAddress("recipient"), "safe-observation-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailUpstream) {
		t.Fatalf("供应商明确拒绝必须保持公开上游失败语义: %v", err)
	}
	if repo.sendLog == nil || repo.sendLog.FailureReason == nil || *repo.sendLog.FailureReason != "provider_rejected_other_http_5xx" {
		t.Fatalf("发送日志只应保存安全归一类别: %#v", repo.sendLog)
	}
	for _, forbidden := range []string{"Sensitive.Internal.Detail", "recipient", "safe-observation-key"} {
		if strings.Contains(*repo.sendLog.FailureReason, forbidden) {
			t.Fatalf("安全失败原因不得包含供应商原始 Code、邮箱或幂等值: %q", *repo.sendLog.FailureReason)
		}
	}
}

func TestTestSendLockScopeExcludesIdempotencyKeyAndPreCallLossStopsAdapter(t *testing.T) {
	repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}}
	adapter := &MockEmailAdapter{}
	svc := NewEmailService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{}, nil, strings.Repeat("a", 32), strings.Repeat("b", 32), "test", "mock")
	var acquiredScopes []string
	svc.lockOverride = func(_ context.Context, scope string, _ time.Duration) (*emailLease, bool, error) {
		acquiredScopes = append(acquiredScopes, scope)
		return &emailLease{release: func() {}, owned: func(context.Context) bool { return false }}, true, nil
	}
	if _, err := svc.TestSend(context.Background(), 7, "register", fakeAddress("recipient"), "secret-idempotency-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailNotReady) {
		t.Fatalf("外呼前丢锁必须返回未就绪: %v", err)
	}
	if len(acquiredScopes) != 2 || strings.Contains(acquiredScopes[0], "secret-idempotency-key") || acquiredScopes[0] != "admin-email-template-test:admin:9:template:7:scene:register:recipient:"+svc.emailHMAC(fakeAddress("recipient")) || acquiredScopes[1] != emailDispatchConfigScope {
		t.Fatalf("测试发送的业务锁与配置锁 scope 不符合冻结契约: %#v", acquiredScopes)
	}
	if adapter.Calls != 0 {
		t.Fatalf("外呼前丢锁不得调用 Adapter: %d", adapter.Calls)
	}
}

func TestTestSendHoldsConfigLockAcrossProviderCall(t *testing.T) {
	repo := &fakeEmailRepo{template: &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}}
	adapter := &blockingSendAdapter{entered: make(chan struct{}), release: make(chan struct{})}
	svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})

	type sendResult struct {
		err error
	}
	done := make(chan sendResult, 1)
	go func() {
		_, err := svc.TestSend(context.Background(), 7, "register", fakeAddress("recipient"), "concurrent-test-key", 9, "127.0.0.1")
		done <- sendResult{err: err}
	}()
	select {
	case <-adapter.entered:
	case <-time.After(time.Second):
		t.Fatal("测试发送未进入供应商外呼")
	}

	// 外呼进行期间模板停用必须竞争同一配置锁并确定性失败，不能改写已校验的发送快照。
	if _, err := svc.SetTemplateStatus(context.Background(), 7, 1, 8, false, "127.0.0.1"); !errors.Is(err, ErrEmailConflict) {
		t.Fatalf("并发模板停用必须返回冲突: %v", err)
	}
	if repo.updateCalled || !repo.template.LocalEnabled {
		t.Fatal("供应商外呼期间不得写入模板停用状态")
	}
	close(adapter.release)
	if result := <-done; result.err != nil {
		t.Fatalf("原测试发送应按锁内快照完成: %v", result.err)
	}
}

func TestSendOTPHoldsConfigLockAcrossProviderCall(t *testing.T) {
	templateID := uint64(7)
	repo := &fakeEmailRepo{
		template: &model.EmailProviderTemplate{ID: templateID, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1},
		binding:  &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1},
	}
	adapter := &blockingSendAdapter{entered: make(chan struct{}), release: make(chan struct{})}
	svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})

	done := make(chan error, 1)
	go func() {
		_, _, err := svc.SendOTP(context.Background(), "concurrent-otp-business", "register", fakeAddress("recipient"), "123456", otpExpireMinutes)
		done <- err
	}()
	select {
	case <-adapter.entered:
	case <-time.After(time.Second):
		t.Fatal("OTP 发送未进入供应商外呼")
	}

	// OTP 外呼开始后，模板停用必须等价于并发配置冲突，不能让已创建的 pending OTP 脱离配置快照。
	if _, err := svc.SetTemplateStatus(context.Background(), templateID, 1, 8, false, "127.0.0.1"); !errors.Is(err, ErrEmailConflict) {
		t.Fatalf("并发模板停用必须返回冲突: %v", err)
	}
	if repo.updateCalled || !repo.template.LocalEnabled {
		t.Fatal("OTP 外呼期间不得写入模板停用状态")
	}
	close(adapter.release)
	if err := <-done; err != nil {
		t.Fatalf("原 OTP 发送应按锁内快照完成: %v", err)
	}
}

func TestTestSendFinalSnapshotRejectsConfigChangeBeforeProviderCall(t *testing.T) {
	template := &model.EmailProviderTemplate{ID: 7, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}
	repo := &fakeEmailRepo{template: template}
	repo.onCreateSendLog = func() {
		// 模拟异常失锁后的配置写入恰好发生在 pending 占位与供应商外呼之间。
		template.LocalEnabled = false
		template.Version++
	}
	adapter := &MockEmailAdapter{}
	svc := newFakeService(repo, &fakeVerificationRepo{}, adapter, &fakeAuditor{})
	if _, err := svc.TestSend(context.Background(), 7, "register", fakeAddress("recipient"), "snapshot-change-key", 9, "127.0.0.1"); !errors.Is(err, ErrEmailConflict) {
		t.Fatalf("外呼前模板快照变化必须确定性拒绝: %v", err)
	}
	if adapter.Calls != 0 {
		t.Fatalf("配置变化后不得调用供应商: calls=%d", adapter.Calls)
	}
	if repo.sendLog == nil || repo.sendLog.Status != "failed" || repo.sendLog.FailureReason == nil || *repo.sendLog.FailureReason != "dispatch_config_changed_before_call" {
		t.Fatalf("pending 测试日志必须收敛为安全失败: %#v", repo.sendLog)
	}
}

func TestSendOTPFinalSnapshotRejectsBindingChangeBeforeProviderCall(t *testing.T) {
	templateID := uint64(7)
	template := &model.EmailProviderTemplate{ID: templateID, ProviderTemplateID: "fake-template", Subject: "主题", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1}
	binding := &model.EmailSceneBinding{Scene: "register", TemplateID: &templateID, Enabled: true, Version: 1}
	repo := &fakeEmailRepo{template: template, binding: binding}
	verification := &fakeVerificationRepo{}
	verification.onCreatePending = func() {
		// 模拟解绑或停用发生在 OTP pending 落库之后，最终快照必须阻止外呼。
		binding.Enabled = false
		binding.Version++
	}
	adapter := &MockEmailAdapter{}
	svc := newFakeService(repo, verification, adapter, &fakeAuditor{})
	_, verificationID, err := svc.SendOTP(context.Background(), "snapshot-change-business", "register", fakeAddress("recipient"), "123456", otpExpireMinutes)
	if !errors.Is(err, ErrEmailConflict) || verificationID == 0 {
		t.Fatalf("外呼前绑定快照变化必须保留记录并确定性拒绝: id=%d err=%v", verificationID, err)
	}
	if adapter.Calls != 0 {
		t.Fatalf("绑定变化后不得调用供应商: calls=%d", adapter.Calls)
	}
	if verification.finalized != "failed" || verification.finalLog == nil || verification.finalLog.FailureReason == nil || *verification.finalLog.FailureReason != "dispatch_config_changed_before_call" {
		t.Fatalf("OTP 与发送日志必须共同收敛为不可使用: finalized=%s log=%#v", verification.finalized, verification.finalLog)
	}
}

func TestStaleTestSendAndCleanup(t *testing.T) {
	reason := "provider_outcome_unknown"
	repo := &fakeEmailRepo{sendLog: &model.EmailSendLog{ID: 1, IdempotencyScope: "scope", IdempotencyKeyHash: "key", RequestFingerprint: "fingerprint", Purpose: "test", Status: "failed", FailureReason: &reason, SubmittedAt: time.Now().UTC().Add(-6 * time.Minute)}, cleanupRows: 2}
	svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
	if _, err := svc.replayTestSend(context.Background(), repo.sendLog, "fingerprint", 7, "127.0.0.1"); !errors.Is(err, ErrEmailOutcomeUnknown) {
		t.Fatalf("未知结果旧 key 必须稳定重放专用错误: %v", err)
	}
	rows, err := svc.CleanupRevokedAllowlist(context.Background())
	if err != nil || rows != 2 {
		t.Fatalf("30天清理错误: %d %v", rows, err)
	}
}

func TestStaleSyncSameKeyReturnsOriginalFailure(t *testing.T) {
	fingerprint := hash("POST\n/api/admin/email/templates/sync\n" + emailProvider)
	repo := &fakeEmailRepo{syncRun: &model.EmailTemplateSyncRun{ID: 9, Provider: emailProvider, RequestFingerprint: fingerprint, Status: "running", StartedAt: time.Now().UTC().Add(-6 * time.Minute)}}
	svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
	result, err := svc.Sync(context.Background(), "same-key", 7, "127.0.0.1")
	if err != nil || result.Status != "failed" || !result.Idempotent || result.ErrorCode == nil || *result.ErrorCode != "sync_interrupted" {
		t.Fatalf("陈旧同步同 key 未返回原失败结果: %#v %v", result, err)
	}
}

func TestEmailSyncStaleBoundaryUsesDatabaseSecondPrecision(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 5, 0, 900000000, time.UTC)
	cutoff := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	if emailSyncRunStale(cutoff, now) {
		t.Fatal("started_at 恰等于五分钟前的数据库秒值时不得提前收敛")
	}
	if !emailSyncRunStale(cutoff.Add(-time.Second), now) {
		t.Fatal("started_at 早于数据库截止边界一秒时必须允许收敛")
	}
}

func TestStaleSyncCannotFenceActiveLeaseOwner(t *testing.T) {
	fingerprint := hash("POST\n/api/admin/email/templates/sync\n" + emailProvider)
	repo := &fakeEmailRepo{syncRun: &model.EmailTemplateSyncRun{ID: 10, Provider: emailProvider, RequestFingerprint: fingerprint, Status: "running", StartedAt: time.Now().UTC().Add(-6 * time.Minute)}}
	svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
	// 模拟原同步执行者仍持有并续租同一全局 lease，重放无法取得锁。
	svc.lockOverride = func(context.Context, string, time.Duration) (*emailLease, bool, error) { return nil, false, nil }
	if _, err := svc.Sync(context.Background(), "same-key", 7, "127.0.0.1"); !errors.Is(err, ErrEmailNotReady) {
		t.Fatalf("未取得同步 lease 必须失败关闭: %v", err)
	}
	if repo.failStaleCalled || repo.syncRun.Status != "running" {
		t.Fatal("原执行者持有 lease 时不得把 running 收敛为 failed")
	}
}

func TestSyncReturnsPreciseRunningSentinelAfterLock(t *testing.T) {
	repo := &fakeEmailRepo{runningSync: true}
	svc := newFakeService(repo, &fakeVerificationRepo{}, &MockEmailAdapter{}, &fakeAuditor{})
	if _, err := svc.Sync(context.Background(), "new-key", 7, "127.0.0.1"); !errors.Is(err, ErrEmailSyncRunning) {
		t.Fatalf("数据库已有 running 必须返回精确同步中错误: %v", err)
	}
}
