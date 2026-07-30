package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"molin/server/internal/modules/auth/model"
)

type fakeEmailBootstrapRepo struct {
	receipt        *model.EmailAdminVerifyBootstrapReceipt
	applyReceipt   *model.EmailAdminVerifyBootstrapReceipt
	applyCreated   bool
	applyErr       error
	capturedInput  model.EmailAdminVerifyBootstrapReceipt
	capturedMirror model.EmailProviderTemplate
	findCalls      int
	applyCalls     int
}

func (f *fakeEmailBootstrapRepo) FindAdminVerifyBootstrapReceipt(context.Context) (*model.EmailAdminVerifyBootstrapReceipt, error) {
	f.findCalls++
	if f.receipt == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return f.receipt, nil
}

func (f *fakeEmailBootstrapRepo) ApplyAdminVerifyBootstrap(_ context.Context, template model.EmailProviderTemplate, receipt model.EmailAdminVerifyBootstrapReceipt, audit func(*gorm.DB, uint64, uint64) error) (*model.EmailAdminVerifyBootstrapReceipt, bool, error) {
	f.applyCalls++
	f.capturedInput, f.capturedMirror = receipt, template
	if f.applyErr != nil {
		return nil, false, f.applyErr
	}
	if f.applyReceipt != nil {
		return f.applyReceipt, f.applyCreated, nil
	}
	receipt.ID, receipt.TemplateID = 11, 22
	if err := audit(nil, receipt.ID, receipt.TemplateID); err != nil {
		return nil, false, err
	}
	return &receipt, true, nil
}

type fakeEmailBootstrapAudit struct {
	actions          []string
	resultTargetType string
	resultTargetID   string
	failAttempt      bool
	failResult       bool
}

func (f *fakeEmailBootstrapAudit) Record(_ context.Context, _ *uint64, _, action string, _, _ *string, _ string, _ any) error {
	f.actions = append(f.actions, action)
	if f.failAttempt {
		return errors.New("attempt审计失败")
	}
	return nil
}

func (f *fakeEmailBootstrapAudit) RecordWithTx(_ context.Context, _ *gorm.DB, _ *uint64, _, action string, targetType, targetID *string, _ string, _ any) error {
	f.actions = append(f.actions, action)
	if targetType != nil {
		f.resultTargetType = *targetType
	}
	if targetID != nil {
		f.resultTargetID = *targetID
	}
	if f.failResult {
		return errors.New("result审计失败")
	}
	return nil
}

func newEmailBootstrapFixture(repo *fakeEmailBootstrapRepo, audit *fakeEmailBootstrapAudit, adapter *MockEmailAdapter) *EmailBootstrapService {
	email := NewEmailService(nil, nil, adapter, nil, nil, strings.Repeat("a", 32), strings.Repeat("b", 32), "test", "mock")
	return NewEmailBootstrapService(repo, email, audit)
}

func validBootstrapTemplate(id string) ProviderTemplate {
	return ProviderTemplate{TemplateID: id, Name: emailAdminVerifyTemplateName, Subject: "验证码", TemplateText: "{Code} {ExpireMinutes}", Status: "approved"}
}

func TestEmailBootstrapSuccessWritesScopedReceiptAndTransactionalAudit(t *testing.T) {
	repo, audit := &fakeEmailBootstrapRepo{}, &fakeEmailBootstrapAudit{}
	providerTemplateID := "0" + strings.Repeat("1", 63)
	adapter := &MockEmailAdapter{Templates: []ProviderTemplate{validBootstrapTemplate(providerTemplateID)}}
	svc := newEmailBootstrapFixture(repo, audit, adapter)
	result, err := svc.ConfigureAdminVerify(context.Background(), providerTemplateID, "idempotency-key-0001", 7, "192.0.2.7")
	if err != nil || result == nil || !result.Configured || result.Idempotent {
		t.Fatalf("首次配置结果异常: %#v %v", result, err)
	}
	if repo.capturedInput.CompletedBy != 7 || repo.capturedInput.Scope != "admin_verify" || len(repo.capturedInput.IdempotencyKeyHash) != 64 || len(repo.capturedInput.RequestFingerprint) != 64 {
		t.Fatalf("receipt 安全摘要不完整: %#v", repo.capturedInput)
	}
	if repo.capturedInput.ProviderTemplateID != providerTemplateID || repo.capturedMirror.ProviderTemplateID != providerTemplateID {
		t.Fatalf("供应商模板编号必须保持原值: receipt=%q mirror=%q", repo.capturedInput.ProviderTemplateID, repo.capturedMirror.ProviderTemplateID)
	}
	if repo.capturedMirror.Name != emailAdminVerifyTemplateName || !repo.capturedMirror.VariablesComplete || repo.capturedMirror.TemplateText == "" {
		t.Fatalf("镜像资格字段异常: %#v", repo.capturedMirror)
	}
	if len(audit.actions) != 2 || audit.actions[0] != "email.admin_verify.bootstrap.attempt" || audit.actions[1] != "email.admin_verify.bootstrap.result" {
		t.Fatalf("审计顺序异常: %#v", audit.actions)
	}
	if audit.resultTargetType != "email_admin_verify_bootstrap_receipt" || audit.resultTargetID != "11" {
		t.Fatalf("result 审计必须指向成功 receipt: type=%q id=%q", audit.resultTargetType, audit.resultTargetID)
	}
	if svc.email.AdapterCallCount("describe_template", "template_sync", "accepted") != 1 {
		t.Fatal("真实 Describe 必须复用既有指标并恰增一次")
	}
}

func TestEmailBootstrapReplayAndCrossAdminNeverDescribe(t *testing.T) {
	baseRepo, audit := &fakeEmailBootstrapRepo{}, &fakeEmailBootstrapAudit{}
	adapter := &MockEmailAdapter{Templates: []ProviderTemplate{validBootstrapTemplate("123456")}}
	svc := newEmailBootstrapFixture(baseRepo, audit, adapter)
	keyHash, fingerprint := svc.bootstrapDigests(7, "idempotency-key-0001", "123456")
	baseRepo.receipt = &model.EmailAdminVerifyBootstrapReceipt{CompletedBy: 7, IdempotencyKeyHash: keyHash, RequestFingerprint: fingerprint}
	result, err := svc.ConfigureAdminVerify(context.Background(), "123456", "idempotency-key-0001", 7, "")
	if err != nil || result == nil || !result.Idempotent {
		t.Fatalf("同管理员重放失败: %#v %v", result, err)
	}
	if len(audit.actions) != 0 || svc.email.AdapterCallCount("describe_template", "template_sync", "accepted") != 0 {
		t.Fatal("receipt 预检重放不得审计 attempt 或 Describe")
	}
	if _, err := svc.ConfigureAdminVerify(context.Background(), "123456", "idempotency-key-0001", 8, ""); !errors.Is(err, ErrEmailBootstrapCompleted) {
		t.Fatalf("跨管理员同 key 必须固定已完成冲突: %v", err)
	}
}

func TestEmailBootstrapConcurrentSameAdminReplaysAfterDescribe(t *testing.T) {
	repo, audit := &fakeEmailBootstrapRepo{}, &fakeEmailBootstrapAudit{}
	adapter := &MockEmailAdapter{Templates: []ProviderTemplate{validBootstrapTemplate("123456")}}
	svc := newEmailBootstrapFixture(repo, audit, adapter)
	keyHash, fingerprint := svc.bootstrapDigests(7, "idempotency-key-0001", "123456")
	repo.applyReceipt = &model.EmailAdminVerifyBootstrapReceipt{CompletedBy: 7, IdempotencyKeyHash: keyHash, RequestFingerprint: fingerprint}
	result, err := svc.ConfigureAdminVerify(context.Background(), "123456", "idempotency-key-0001", 7, "")
	if err != nil || result == nil || !result.Idempotent {
		t.Fatalf("行锁后并发凭据必须返回原成功: %#v %v", result, err)
	}
	if svc.email.AdapterCallCount("describe_template", "template_sync", "accepted") != 1 {
		t.Fatal("并发首次请求允许在行锁前各完成一次 Describe")
	}
}

func TestEmailBootstrapAttemptAndQualificationFailClosed(t *testing.T) {
	repo := &fakeEmailBootstrapRepo{}
	audit := &fakeEmailBootstrapAudit{failAttempt: true}
	adapter := &MockEmailAdapter{Templates: []ProviderTemplate{validBootstrapTemplate("123456")}}
	svc := newEmailBootstrapFixture(repo, audit, adapter)
	if _, err := svc.ConfigureAdminVerify(context.Background(), "123456", "idempotency-key-0001", 7, ""); err == nil {
		t.Fatal("attempt 审计失败必须阻断")
	}
	if svc.email.AdapterCallCount("describe_template", "template_sync", "accepted") != 0 {
		t.Fatal("attempt 审计失败不得 Describe")
	}

	for name, tc := range map[string]struct {
		template ProviderTemplate
		wantErr  error
	}{
		"名称不精确":     {ProviderTemplate{TemplateID: "123456", Name: strings.ToUpper(emailAdminVerifyTemplateName), TemplateText: "${Code} ${ExpireMinutes}", Status: "approved"}, ErrEmailBootstrapName},
		"官方变量缺失":    {ProviderTemplate{TemplateID: "123456", Name: emailAdminVerifyTemplateName, TemplateText: "{Code}", Status: "approved"}, ErrEmailVariables},
		"CSS不得补齐变量": {ProviderTemplate{TemplateID: "123456", Name: emailAdminVerifyTemplateName, TemplateText: "{Code}<style>.x { ExpireMinutes: 10; }</style>", Status: "approved"}, ErrEmailVariables},
		"小写变量不得通过":  {ProviderTemplate{TemplateID: "123456", Name: emailAdminVerifyTemplateName, TemplateText: "{code} {expireminutes}", Status: "approved"}, ErrEmailVariables},
		"不完整变量不得通过": {ProviderTemplate{TemplateID: "123456", Name: emailAdminVerifyTemplateName, TemplateText: "{{Code} {ExpireMinutes}", Status: "approved"}, ErrEmailVariables},
		"尾随花括号不得通过": {ProviderTemplate{TemplateID: "123456", Name: emailAdminVerifyTemplateName, TemplateText: "${Code}} {{ExpireMinutes}}}", Status: "approved"}, ErrEmailVariables},
		"三重与嵌套不得通过": {ProviderTemplate{TemplateID: "123456", Name: emailAdminVerifyTemplateName, TemplateText: "{{{Code}}} {{${ExpireMinutes}}}", Status: "approved"}, ErrEmailVariables},
	} {
		t.Run(name, func(t *testing.T) {
			s := newEmailBootstrapFixture(&fakeEmailBootstrapRepo{}, &fakeEmailBootstrapAudit{}, &MockEmailAdapter{Templates: []ProviderTemplate{tc.template}})
			if _, err := s.ConfigureAdminVerify(context.Background(), "123456", "idempotency-key-0001", 7, ""); !errors.Is(err, tc.wantErr) {
				t.Fatalf("资格错误不精确: got=%v want=%v", err, tc.wantErr)
			}
		})
	}
}

func TestEmailBootstrapInvalidProviderTemplateIDHasNoSideEffects(t *testing.T) {
	invalidValues := []string{"", "0", "000", "abc", "-1", "+1", "1.0", "1e2", " 1", strings.Repeat("1", 65)}
	for _, value := range invalidValues {
		t.Run(value, func(t *testing.T) {
			repo, audit := &fakeEmailBootstrapRepo{}, &fakeEmailBootstrapAudit{}
			adapter := &MockEmailAdapter{Templates: []ProviderTemplate{validBootstrapTemplate("123456")}}
			svc := newEmailBootstrapFixture(repo, audit, adapter)

			if _, err := svc.ConfigureAdminVerify(context.Background(), value, "idempotency-key-0001", 7, "192.0.2.7"); !errors.Is(err, ErrEmailInvalid) {
				t.Fatalf("非法模板编号必须返回参数错误: value=%q err=%v", value, err)
			}
			if repo.findCalls != 0 || repo.applyCalls != 0 {
				t.Fatalf("非法模板编号不得访问数据库: value=%q find=%d apply=%d", value, repo.findCalls, repo.applyCalls)
			}
			if len(audit.actions) != 0 {
				t.Fatalf("非法模板编号不得写审计: value=%q actions=%v", value, audit.actions)
			}
			if svc.email.AdapterCallCount("describe_template", "template_sync", "accepted") != 0 {
				t.Fatalf("非法模板编号不得调用供应商 Adapter: value=%q", value)
			}
		})
	}
}
