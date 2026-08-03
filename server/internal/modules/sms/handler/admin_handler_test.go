package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"molin/server/internal/modules/sms/model"
	"molin/server/internal/modules/sms/service"
	"molin/server/pkg/response"
)

type fakeSMSAdminApplication struct {
	summary        service.SMSAdminSummary
	err            error
	statusTemplate *model.Template
	statusErr      error
	statusCalls    int
}

func (f *fakeSMSAdminApplication) ListTemplates(context.Context, model.TemplateListFilter) ([]model.Template, int64, error) {
	return nil, 0, nil
}
func (f *fakeSMSAdminApplication) GetTemplate(context.Context, uint64) (*model.Template, error) {
	return nil, service.ErrSMSTemplateNotFound
}
func (f *fakeSMSAdminApplication) SyncTemplates(context.Context) (model.TemplateSyncResult, error) {
	return model.TemplateSyncResult{}, nil
}
func (f *fakeSMSAdminApplication) ListScenes(context.Context) ([]model.AdminScene, error) {
	return nil, nil
}
func (f *fakeSMSAdminApplication) SetScene(context.Context, string, uint64, uint64, uint64, bool) (*model.AdminScene, error) {
	return nil, nil
}
func (f *fakeSMSAdminApplication) ListSendLogs(context.Context, model.SendLogListFilter) ([]model.SendLog, int64, error) {
	return nil, 0, nil
}
func (f *fakeSMSAdminApplication) TestSend(context.Context, uint64, uint64, string, string, string) (service.TestSendResult, error) {
	return service.TestSendResult{}, nil
}

func (f *fakeSMSAdminApplication) Summary(context.Context) (service.SMSAdminSummary, error) {
	return f.summary, f.err
}

func (f *fakeSMSAdminApplication) SetTemplateStatus(_ context.Context, _ uint64, _ uint64, _ bool) (*model.Template, error) {
	f.statusCalls++
	return f.statusTemplate, f.statusErr
}

func TestSMSAdminSummaryReturnsUnifiedResponse(t *testing.T) {
	h := NewSMSAdminHandler(&fakeSMSAdminApplication{summary: service.SMSAdminSummary{
		TemplateTotal:     5,
		ApprovedTotal:     5,
		EnabledTotal:      4,
		BoundSceneTotal:   4,
		UnboundSceneTotal: 1,
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/admin/sms/summary", nil)
	rec := httptest.NewRecorder()

	h.Summary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("概览接口状态码错误: %d body=%s", rec.Code, rec.Body.String())
	}
	var body response.Body
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析统一响应失败: %v", err)
	}
	if body.Code != 0 || body.Message != "ok" {
		t.Fatalf("统一响应元数据错误: %#v", body)
	}
	data, ok := body.Data.(map[string]any)
	if !ok || data["unbound_scene_total"] != float64(1) {
		t.Fatalf("概览响应数据错误: %#v", body.Data)
	}
}

func TestSMSAdminTemplateStatusRejectsUnknownFields(t *testing.T) {
	app := &fakeSMSAdminApplication{}
	h := NewSMSAdminHandler(app)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/sms/templates/7/status", bytes.NewBufferString(`{"enabled":true,"version":1,"sign_name":"禁止注入"}`))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()

	h.SetTemplateStatus(rec, req)

	if rec.Code != http.StatusBadRequest || app.statusCalls != 0 {
		t.Fatalf("未知字段必须在业务调用前拒绝: status=%d calls=%d body=%s", rec.Code, app.statusCalls, rec.Body.String())
	}
}

func TestSMSAdminTemplateStatusMapsAuditConflict(t *testing.T) {
	app := &fakeSMSAdminApplication{statusErr: service.ErrSMSTemplateNotApproved}
	h := NewSMSAdminHandler(app)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/sms/templates/7/status", bytes.NewBufferString(`{"enabled":true,"version":1}`))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()

	h.SetTemplateStatus(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("未审核模板应映射为 409: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
