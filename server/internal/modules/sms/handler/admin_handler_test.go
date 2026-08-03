package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	syncResult     model.TemplateSyncResult
	sceneResult    *model.AdminScene
	sceneErr       error
	testResult     service.TestSendResult
	testErr        error
	templateItems  []model.Template
	sendLogItems   []model.SendLog
}

func (f *fakeSMSAdminApplication) ListTemplates(context.Context, model.TemplateListFilter) ([]model.Template, int64, error) {
	return f.templateItems, int64(len(f.templateItems)), nil
}
func (f *fakeSMSAdminApplication) GetTemplate(context.Context, uint64) (*model.Template, error) {
	return nil, service.ErrSMSTemplateNotFound
}
func (f *fakeSMSAdminApplication) SyncTemplates(context.Context) (model.TemplateSyncResult, error) {
	return f.syncResult, nil
}
func (f *fakeSMSAdminApplication) ListScenes(context.Context) ([]model.AdminScene, error) {
	return nil, nil
}
func (f *fakeSMSAdminApplication) SetScene(context.Context, string, uint64, uint64, uint64, bool) (*model.AdminScene, error) {
	return f.sceneResult, f.sceneErr
}
func (f *fakeSMSAdminApplication) ListSendLogs(context.Context, model.SendLogListFilter) ([]model.SendLog, int64, error) {
	return f.sendLogItems, int64(len(f.sendLogItems)), nil
}
func (f *fakeSMSAdminApplication) TestSend(context.Context, uint64, uint64, string, string, string) (service.TestSendResult, error) {
	return f.testResult, f.testErr
}

type auditRecord struct {
	action  string
	summary any
}

type fakeSMSAuditRecorder struct{ records []auditRecord }

func (f *fakeSMSAuditRecorder) Record(_ context.Context, _ *uint64, module, action string, _, _ *string, _ string, summary any) error {
	if module != "sms" {
		return errors.New("审计模块错误")
	}
	f.records = append(f.records, auditRecord{action: action, summary: summary})
	return nil
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

func TestSMSAdminTemplateStatusMapsMissingFixedSignToUnavailable(t *testing.T) {
	app := &fakeSMSAdminApplication{statusErr: service.ErrSMSAdminUnavailable}
	h := NewSMSAdminHandler(app)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/sms/templates/7/status", bytes.NewBufferString(`{"enabled":true,"version":1}`))
	req.SetPathValue("id", "7")
	recorder := httptest.NewRecorder()

	h.SetTemplateStatus(recorder, req)

	var body response.Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != http.StatusServiceUnavailable || body.Code != 50300 {
		t.Fatalf("固定签名缺失必须失败关闭为 503/50300: status=%d body=%s err=%v", recorder.Code, recorder.Body.String(), err)
	}
}

func TestSMSAdminWriteEndpointsRecordSanitizedAudit(t *testing.T) {
	app := &fakeSMSAdminApplication{
		syncResult:     model.TemplateSyncResult{TotalCount: 1},
		sceneResult:    &model.AdminScene{Scene: "register", Version: 1},
		statusTemplate: &model.Template{ID: 7, LocalEnabled: true, Version: 2},
		testResult:     service.TestSendResult{BusinessRequestID: "sms_safe", SubmitStatus: "accepted", TemplateCode: "SMS_SAFE", PhoneMasked: "pho****st-a"},
	}
	audit := &fakeSMSAuditRecorder{}
	h := NewSMSAdminHandler(app, audit)

	requests := []struct {
		call func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{call: h.SyncTemplates, req: httptest.NewRequest(http.MethodPost, "/api/admin/sms/templates/sync", nil)},
		{call: h.SetScene, req: httptest.NewRequest(http.MethodPut, "/api/admin/sms/scenes/register", bytes.NewBufferString(`{"template_id":7,"enabled":true,"version":0}`))},
		{call: h.SetTemplateStatus, req: httptest.NewRequest(http.MethodPatch, "/api/admin/sms/templates/7/status", bytes.NewBufferString(`{"enabled":true,"version":1}`))},
		{call: h.TestSend, req: httptest.NewRequest(http.MethodPost, "/api/admin/sms/templates/7/test-send", bytes.NewBufferString(`{"scene":"register","phone":"phone-test-a"}`))},
	}
	requests[1].req.SetPathValue("scene", "register")
	requests[2].req.SetPathValue("id", "7")
	requests[3].req.SetPathValue("id", "7")
	requests[3].req.Header.Set("Idempotency-Key", "private-idempotency-key")
	for _, item := range requests {
		recorder := httptest.NewRecorder()
		item.call(recorder, item.req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("写接口应成功并审计: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if len(audit.records) != 4 {
		t.Fatalf("四类写操作必须各有审计: %#v", audit.records)
	}
	wantActions := []string{"template_sync", "scene_binding_update", "template_status_update", "template_test_send"}
	for index, record := range audit.records {
		if record.action != wantActions[index] {
			t.Fatalf("审计动作错误: got=%s want=%s", record.action, wantActions[index])
		}
		raw, err := json.Marshal(record.summary)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, "phone-test-a") || strings.Contains(text, "private-idempotency-key") {
			t.Fatalf("审计摘要不得包含完整测试目标或幂等键: %s", text)
		}
	}
}

func TestSMSAdminTestSendRateLimitKeepsHeaderAndBodyConsistent(t *testing.T) {
	h := NewSMSAdminHandler(&fakeSMSAdminApplication{
		testResult: service.TestSendResult{RetryAfterSeconds: 27},
		testErr:    service.ErrSMSTestSendRateLimited,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/sms/templates/7/test-send", bytes.NewBufferString(`{"scene":"register","phone":"phone-test-a"}`))
	req.SetPathValue("id", "7")
	req.Header.Set("Idempotency-Key", "rate-limit-replay-key")
	recorder := httptest.NewRecorder()

	h.TestSend(recorder, req)

	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "27" {
		t.Fatalf("限流响应头错误: status=%d retry=%s", recorder.Code, recorder.Header().Get("Retry-After"))
	}
	var body response.Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析限流响应失败: %v", err)
	}
	data, ok := body.Data.(map[string]any)
	if body.Code != 42900 || !ok || data["retry_after_seconds"] != float64(27) {
		t.Fatalf("限流响应体必须与 Retry-After 一致: %#v", body)
	}
}

func TestSMSAdminFailedConfigurationWritesRecordSafeAudit(t *testing.T) {
	app := &fakeSMSAdminApplication{sceneErr: service.ErrSMSSceneVersionConflict, statusErr: service.ErrSMSTemplateVersionConflict}
	audit := &fakeSMSAuditRecorder{}
	h := NewSMSAdminHandler(app, audit)

	sceneRequest := httptest.NewRequest(http.MethodPut, "/api/admin/sms/scenes/register", bytes.NewBufferString(`{"template_id":7,"enabled":true,"version":3}`))
	sceneRequest.SetPathValue("scene", "register")
	statusRequest := httptest.NewRequest(http.MethodPatch, "/api/admin/sms/templates/7/status", bytes.NewBufferString(`{"enabled":false,"version":4}`))
	statusRequest.SetPathValue("id", "7")
	for _, item := range []struct {
		call func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{call: h.SetScene, req: sceneRequest},
		{call: h.SetTemplateStatus, req: statusRequest},
	} {
		recorder := httptest.NewRecorder()
		item.call(recorder, item.req)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("配置冲突应返回 409: status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
	if len(audit.records) != 2 {
		t.Fatalf("两类失败写操作都必须记录审计: %#v", audit.records)
	}
	for _, record := range audit.records {
		encoded, err := json.Marshal(record.summary)
		if err != nil || !strings.Contains(string(encoded), `"outcome":"failed"`) || strings.Contains(string(encoded), "phone") || strings.Contains(string(encoded), "idempotency") {
			t.Fatalf("失败审计摘要不安全或不完整: action=%s summary=%s err=%v", record.action, encoded, err)
		}
	}
}

func TestSMSAdminListsUseFlatPaginationAndHideInternalDigests(t *testing.T) {
	t.Run("模板空列表", func(t *testing.T) {
		h := NewSMSAdminHandler(&fakeSMSAdminApplication{})
		recorder := httptest.NewRecorder()
		h.ListTemplates(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/sms/templates?page=2&page_size=10", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("模板列表失败: %s", recorder.Body.String())
		}
		var envelope struct {
			Data struct {
				Items    []model.Template `json:"items"`
				Page     int              `json:"page"`
				PageSize int              `json:"page_size"`
				Total    int64            `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Data.Items == nil || envelope.Data.Page != 2 || envelope.Data.PageSize != 10 || envelope.Data.Total != 0 {
			t.Fatalf("模板 D-95 契约错误: envelope=%#v err=%v", envelope, err)
		}
	})

	t.Run("发送记录脱敏", func(t *testing.T) {
		scope, keyHash, ownerHash, fingerprint := "private-scope", "private-key-hash", "private-owner-hash", "private-fingerprint"
		app := &fakeSMSAdminApplication{sendLogItems: []model.SendLog{{
			ID: 1, Purpose: "test", Scene: "register", PhoneMasked: "pho****st-a", PhoneHMAC: "private-phone-hmac",
			TemplateCode: "SMS_SAFE", SignName: "固定签名", Provider: "aliyun", BusinessRequestID: "sms_safe",
			IdempotencyScope: &scope, IdempotencyKeyHash: &keyHash, IdempotencyOwnerKeyHash: &ownerHash,
			RequestFingerprint: &fingerprint, SubmitStatus: "accepted",
		}}}
		h := NewSMSAdminHandler(app)
		recorder := httptest.NewRecorder()
		h.ListSendLogs(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/sms/send-logs", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("发送记录列表失败: %s", recorder.Body.String())
		}
		body := recorder.Body.String()
		for _, forbidden := range []string{"private-phone-hmac", "private-scope", "private-key-hash", "private-owner-hash", "private-fingerprint"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("发送记录响应泄露内部摘要 %q: %s", forbidden, body)
			}
		}
		if !strings.Contains(body, `"items":[`) || !strings.Contains(body, `"page":1`) || !strings.Contains(body, `"page_size":20`) || !strings.Contains(body, `"total":1`) {
			t.Fatalf("发送记录 D-95 契约错误: %s", body)
		}
	})
}

func TestSMSAdminRejectsInvalidFiltersAndInjectedSign(t *testing.T) {
	app := &fakeSMSAdminApplication{sceneResult: &model.AdminScene{}}
	h := NewSMSAdminHandler(app)
	cases := []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{name: "非法模板审核状态", call: h.ListTemplates, req: httptest.NewRequest(http.MethodGet, "/api/admin/sms/templates?audit_status=unknown", nil)},
		{name: "非法模板场景", call: h.ListTemplates, req: httptest.NewRequest(http.MethodGet, "/api/admin/sms/templates?scene=marketing", nil)},
		{name: "非法日志状态", call: h.ListSendLogs, req: httptest.NewRequest(http.MethodGet, "/api/admin/sms/send-logs?status=pending", nil)},
		{name: "日志时间倒置", call: h.ListSendLogs, req: httptest.NewRequest(http.MethodGet, "/api/admin/sms/send-logs?start_time=2026-08-03T12:00:00Z&end_time=2026-08-02T12:00:00Z", nil)},
		{name: "日志跨度超过31天", call: h.ListSendLogs, req: httptest.NewRequest(http.MethodGet, "/api/admin/sms/send-logs?start_time=2026-06-01T00:00:00Z&end_time=2026-08-03T00:00:00Z", nil)},
		{name: "场景签名注入", call: h.SetScene, req: httptest.NewRequest(http.MethodPut, "/api/admin/sms/scenes/register", bytes.NewBufferString(`{"template_id":7,"enabled":true,"version":0,"sign_name":"禁止注入"}`))},
	}
	cases[5].req.SetPathValue("scene", "register")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			tc.call(recorder, tc.req)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("非法参数必须返回 400: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
