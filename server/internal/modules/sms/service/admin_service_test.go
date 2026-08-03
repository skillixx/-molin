package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"molin/server/internal/config"
	"molin/server/internal/modules/sms/model"
	"molin/server/internal/modules/sms/sender"
)

type fakeSMSAdminSummaryRepository struct {
	summary SMSAdminSummary
	err     error
}

type fakeTestSendRepo struct {
	reserved      *model.SendLog
	created       bool
	reserveCalls  int
	completeCalls int
}

func (f *fakeTestSendRepo) ReserveTestSend(_ context.Context, log *model.SendLog) (*model.SendLog, bool, error) {
	f.reserveCalls++
	if f.reserved != nil {
		return f.reserved, f.created, nil
	}
	log.ID = 9
	return log, true, nil
}
func (f *fakeTestSendRepo) CompleteTestSend(_ context.Context, _ uint64, _ string, _, _, _ *string, _ time.Time) error {
	f.completeCalls++
	return nil
}

type fakeTestDispatcher struct{ sendCalls int }

func (f *fakeTestDispatcher) Prepare(_ context.Context, scene, _ string) (PreparedSend, error) {
	return PreparedSend{Scene: scene, TemplateID: 7, TemplateCode: "SMS_OK", SignName: "固定签名", Provider: "aliyun"}, nil
}
func (f *fakeTestDispatcher) SendProvider(context.Context, PreparedSend, string, string, string) (DispatchResult, error) {
	f.sendCalls++
	return DispatchResult{Accepted: true, ProviderRequestID: "request-safe", ProviderCode: "OK"}, nil
}

type fakeTestLimiter struct {
	calls   int
	allowed bool
}

func (f *fakeTestLimiter) Allow(context.Context, uint64, string) (bool, int64, error) {
	f.calls++
	return f.allowed, 30, nil
}

func testSendConfig() config.Config {
	return config.Config{SMSEnabled: true, SMSTestMode: true, SMSPhoneHMACSecret: strings.Repeat("x", 32), SMSTestPhoneWhitelist: []string{"phone-test-a"}}
}

func TestAdminTestSendFailsClosedWhenSMSDisabled(t *testing.T) {
	repo := &fakeTestSendRepo{}
	svc := NewSMSAdminService(repo)
	svc.testDispatcher = &fakeTestDispatcher{}
	svc.testLimiter = &fakeTestLimiter{allowed: true}
	_, err := svc.TestSend(context.Background(), 1, 7, "register", "phone-test-a", "same-key")
	if !errors.Is(err, ErrSMSTestSendUnavailable) || repo.reserveCalls != 0 {
		t.Fatalf("关闭态必须在落库和发送前拒绝: err=%v calls=%d", err, repo.reserveCalls)
	}
}

func TestAdminTestSendReplaysAcceptedWithoutRateLimitOrProvider(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeTestSendRepo{created: false, reserved: &model.SendLog{ID: 9, SubmitStatus: "accepted", BusinessRequestID: "sms_existing", TemplateCode: "SMS_OK", PhoneMasked: "138****0000", SubmittedAt: now}}
	dispatcher := &fakeTestDispatcher{}
	limiter := &fakeTestLimiter{allowed: true}
	svc := NewSMSAdminService(repo)
	svc.testConfig = testSendConfig()
	svc.testDispatcher = dispatcher
	svc.testLimiter = limiter
	result, err := svc.TestSend(context.Background(), 1, 7, "register", "phone-test-a", "same-key")
	if err != nil || !result.Idempotent || result.BusinessRequestID != "sms_existing" || limiter.calls != 0 || dispatcher.sendCalls != 0 {
		t.Fatalf("幂等重放错误: result=%#v err=%v limiter=%d send=%d", result, err, limiter.calls, dispatcher.sendCalls)
	}
}

func TestAdminTestSendFirstRequestUsesDualLimiterAndOneProviderCall(t *testing.T) {
	repo := &fakeTestSendRepo{}
	dispatcher := &fakeTestDispatcher{}
	limiter := &fakeTestLimiter{allowed: true}
	svc := NewSMSAdminService(repo)
	svc.testConfig = testSendConfig()
	svc.testDispatcher = dispatcher
	svc.testLimiter = limiter
	result, err := svc.TestSend(context.Background(), 1, 7, "register", "phone-test-a", "new-key")
	if err != nil || result.SubmitStatus != "accepted" || limiter.calls != 1 || dispatcher.sendCalls != 1 || repo.completeCalls != 1 {
		t.Fatalf("首次测试发送错误: result=%#v err=%v limiter=%d send=%d complete=%d", result, err, limiter.calls, dispatcher.sendCalls, repo.completeCalls)
	}
}

type fakeTemplateProvider struct {
	items []sender.TemplateSnapshot
	err   error
}

func (f fakeTemplateProvider) ListTemplates(context.Context) ([]sender.TemplateSnapshot, error) {
	return f.items, f.err
}

type fakeSyncRepository struct {
	called int
	items  []model.TemplateSnapshot
	result model.TemplateSyncResult
}

func (f *fakeSyncRepository) ApplyTemplateSnapshots(_ context.Context, items []model.TemplateSnapshot, _ time.Time) (model.TemplateSyncResult, error) {
	f.called++
	f.items = items
	return f.result, nil
}

func TestSyncTemplatesDoesNotWriteWhenProviderQueryFails(t *testing.T) {
	repo := &fakeSyncRepository{}
	svc := NewSMSAdminService(repo)
	svc.ConfigureTemplateSync(fakeTemplateProvider{err: errors.New("provider failed")}, "固定签名")

	_, err := svc.SyncTemplates(context.Background())
	if !errors.Is(err, ErrSMSTemplateSyncFailed) || repo.called != 0 {
		t.Fatalf("供应商查询失败不得写入，err=%v called=%d", err, repo.called)
	}
}

func TestSyncTemplatesFiltersFixedSignAndVerificationCode(t *testing.T) {
	repo := &fakeSyncRepository{result: model.TemplateSyncResult{CreatedCount: 1, TotalCount: 1}}
	svc := NewSMSAdminService(repo)
	svc.ConfigureTemplateSync(fakeTemplateProvider{items: []sender.TemplateSnapshot{
		{Provider: "aliyun", TemplateCode: "SMS_OK", TemplateName: "注册", TemplateType: "verification", Content: "验证码 ${code}", AuditStatus: "approved", SignName: "固定签名"},
		{Provider: "aliyun", TemplateCode: "SMS_OTHER", TemplateType: "verification", Content: "验证码 ${code}", AuditStatus: "approved", SignName: "其他签名"},
		{Provider: "aliyun", TemplateCode: "SMS_NOTICE", TemplateType: "other", Content: "通知 ${name}", AuditStatus: "approved", SignName: "固定签名"},
	}}, "固定签名")

	result, err := svc.SyncTemplates(context.Background())
	if err != nil || result.CreatedCount != 1 || result.IgnoredCount != 2 || result.TotalCount != 3 || len(repo.items) != 1 || repo.items[0].TemplateCode != "SMS_OK" {
		t.Fatalf("同步过滤错误: result=%#v items=%#v err=%v", result, repo.items, err)
	}
}

func (f *fakeSMSAdminSummaryRepository) GetAdminSummary(context.Context) (SMSAdminSummary, error) {
	return f.summary, f.err
}

func TestSMSAdminServiceSummaryUsesFiveFixedScenes(t *testing.T) {
	syncedAt := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	svc := NewSMSAdminService(&fakeSMSAdminSummaryRepository{summary: SMSAdminSummary{
		TemplateTotal:   7,
		ApprovedTotal:   5,
		EnabledTotal:    4,
		BoundSceneTotal: 3,
		LastSyncedAt:    &syncedAt,
	}})

	got, err := svc.Summary(context.Background())
	if err != nil {
		t.Fatalf("查询短信概览失败: %v", err)
	}
	if got.TemplateTotal != 7 || got.ApprovedTotal != 5 || got.EnabledTotal != 4 {
		t.Fatalf("模板统计错误: %#v", got)
	}
	if got.BoundSceneTotal != 3 || got.UnboundSceneTotal != 2 {
		t.Fatalf("五场景绑定统计错误: %#v", got)
	}
	if got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(syncedAt) {
		t.Fatalf("最后同步时间错误: %#v", got.LastSyncedAt)
	}
}

type fakeSMSAdminTemplateRepository struct {
	template    *model.Template
	getErr      error
	updateErr   error
	updateCalls int
	gotVersion  uint64
	gotEnabled  bool
}

func (f *fakeSMSAdminTemplateRepository) GetAdminTemplate(context.Context, uint64) (*model.Template, error) {
	return f.template, f.getErr
}

func (f *fakeSMSAdminTemplateRepository) UpdateAdminTemplateStatus(_ context.Context, _ uint64, version uint64, enabled bool) error {
	f.updateCalls++
	f.gotVersion = version
	f.gotEnabled = enabled
	return f.updateErr
}

func TestSMSAdminServiceRejectsEnablingUnapprovedTemplate(t *testing.T) {
	repo := &fakeSMSAdminTemplateRepository{template: &model.Template{
		ID:                  7,
		ProviderAuditStatus: "rejected",
		TemplateType:        "verification",
		Content:             "验证码 ${code}",
		Version:             2,
	}}
	svc := NewSMSAdminService(repo)

	_, err := svc.SetTemplateStatus(context.Background(), 7, 2, true)
	if !errors.Is(err, ErrSMSTemplateNotApproved) {
		t.Fatalf("未审核模板应拒绝启用，实际错误: %v", err)
	}
	if repo.updateCalls != 0 {
		t.Fatal("业务校验失败时不得执行状态更新")
	}
}

func TestSMSAdminServiceUpdatesApprovedTemplateWithVersion(t *testing.T) {
	repo := &fakeSMSAdminTemplateRepository{template: &model.Template{
		ID:                  7,
		ProviderAuditStatus: "approved",
		TemplateType:        "verification",
		Content:             "验证码 ${code}",
		Version:             2,
	}}
	svc := NewSMSAdminService(repo)

	got, err := svc.SetTemplateStatus(context.Background(), 7, 2, true)
	if err != nil {
		t.Fatalf("启用审核通过模板失败: %v", err)
	}
	if repo.updateCalls != 1 || repo.gotVersion != 2 || !repo.gotEnabled {
		t.Fatalf("状态更新参数错误: %#v", repo)
	}
	if !got.LocalEnabled || got.Version != 3 {
		t.Fatalf("返回的新版本状态错误: %#v", got)
	}
}
