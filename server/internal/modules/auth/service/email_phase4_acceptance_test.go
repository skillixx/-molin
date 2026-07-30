package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"molin/server/internal/modules/auth/model"
)

// phase4RecordingAdapter 只在进程内记录脱敏验收所需的调用参数，不访问任何外部邮件服务。
type phase4RecordingAdapter struct {
	message EmailMessage
	calls   int
}

func (a *phase4RecordingAdapter) Ready() bool { return true }
func (a *phase4RecordingAdapter) QueryTemplates(context.Context, int, int) ([]ProviderTemplate, bool, error) {
	return nil, false, nil
}
func (a *phase4RecordingAdapter) DescribeTemplate(context.Context, string) (ProviderTemplate, error) {
	return ProviderTemplate{}, nil
}
func (a *phase4RecordingAdapter) SingleSendMail(_ context.Context, message EmailMessage) (EmailAcceptance, error) {
	a.calls++
	a.message = message
	return EmailAcceptance{RequestID: "phase4-provider-request"}, nil
}

func TestPhase4FiveScenesUseBoundTemplateAndFrozenVariables(t *testing.T) {
	templateIDs := map[string]string{
		"register":       "437227",
		"login":          "437228",
		"reset_password": "437229",
		"bind_email":     "437230",
		"admin_verify":   "437231",
	}

	for scene, providerTemplateID := range templateIDs {
		t.Run(scene, func(t *testing.T) {
			platformTemplateID := uint64(101)
			recipient := fakeAddress("phase4-" + strings.ReplaceAll(scene, "_", "-"))
			rawCode := "654321"
			repo := &fakeEmailRepo{
				template: &model.EmailProviderTemplate{
					ID: platformTemplateID, ProviderTemplateID: providerTemplateID, Subject: "验证码通知", TemplateText: validEmailTemplateText,
					ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 3,
				},
				binding: &model.EmailSceneBinding{Scene: scene, TemplateID: &platformTemplateID, Enabled: true, Version: 5},
			}
			verification := &fakeVerificationRepo{}
			adapter := &phase4RecordingAdapter{}
			svc := newFakeService(repo, verification, adapter, &fakeAuditor{})
			ctx := context.Background()
			if scene == "bind_email" {
				ctx = withEmailOTPIdentity(ctx, "/api/me/verification-codes/email", 7, recipient)
			}
			if scene == "admin_verify" {
				ctx = withEmailOTPIdentity(ctx, "/api/admin/auth/verification-codes/email", 7, recipient)
			}

			result, _, err := svc.SendOTP(ctx, "phase4-"+scene, scene, recipient, rawCode, 10)
			if err != nil {
				t.Fatalf("五场景发送契约失败: %v", err)
			}
			if adapter.calls != 1 {
				t.Fatalf("每个场景必须只调用一次供应商 Adapter: calls=%d", adapter.calls)
			}
			if !strings.Contains(adapter.message.HTMLBody, rawCode) || !strings.Contains(adapter.message.HTMLBody, "10") || strings.Contains(adapter.message.HTMLBody, "{Code}") || strings.Contains(adapter.message.HTMLBody, "{ExpireMinutes}") {
				t.Fatal("供应商正文必须已在本地固定映射 Code/ExpireMinutes")
			}
			if result.RequestID == "" || result.Mock || verification.finalized != "accepted" {
				t.Fatalf("只有供应商明确受理才能收敛 accepted: result=%#v status=%q", result, verification.finalized)
			}
			if verification.finalLog == nil || verification.finalLog.Status != "accepted" || verification.finalLog.ProviderTemplateID != providerTemplateID {
				t.Fatalf("accepted 日志必须保留本次模板快照: %#v", verification.finalLog)
			}

			serialized, marshalErr := json.Marshal(verification.finalLog)
			if marshalErr != nil {
				t.Fatalf("序列化发送日志失败: %v", marshalErr)
			}
			text := string(serialized)
			for _, secret := range []string{recipient, rawCode, "AccessKey", "TemplateData"} {
				if strings.Contains(text, secret) {
					t.Fatalf("发送日志不得包含敏感值类别: scene=%s category=%s", scene, fmt.Sprintf("长度%d", len(secret)))
				}
			}
			if verification.finalLog.RecipientMasked == "" || strings.Contains(verification.finalLog.RecipientMasked, recipient) {
				t.Fatal("发送日志必须只保存脱敏收件人")
			}
		})
	}
}

func TestPhase4ProviderFailureKeepsOTPUnavailable(t *testing.T) {
	platformTemplateID := uint64(101)
	repo := &fakeEmailRepo{
		template: &model.EmailProviderTemplate{ID: platformTemplateID, ProviderTemplateID: "437227", Subject: "验证码通知", TemplateText: validEmailTemplateText, ProviderStatus: "approved", VariablesComplete: true, LocalEnabled: true, Version: 1},
		binding:  &model.EmailSceneBinding{Scene: "register", TemplateID: &platformTemplateID, Enabled: true, Version: 1},
	}
	verification := &fakeVerificationRepo{}
	svc := newFakeService(repo, verification, &MockEmailAdapter{SendError: ErrDirectMailUpstream}, &fakeAuditor{})

	if _, _, err := svc.SendOTP(context.Background(), "phase4-failed", "register", fakeAddress("phase4-failed"), "654321", 10); err == nil {
		t.Fatal("供应商失败时必须返回错误")
	}
	if verification.finalized != "failed" || verification.finalLog == nil || verification.finalLog.Status != "failed" {
		t.Fatalf("供应商失败必须同时收敛验证码和发送日志为 failed: verification=%q log=%#v", verification.finalized, verification.finalLog)
	}
	if verification.finalLog.ProviderRequestID != nil {
		t.Fatal("失败日志不得伪造供应商受理 RequestId")
	}
}
