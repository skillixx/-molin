package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/modules/token_gateway/repository"
)

type rejectingAudit struct {
	action string
}

func TestG5ConflictReturnsHTTP409(t *testing.T) {
	for _, conflict := range []error{repository.ErrModelReleaseConflict, repository.ErrRouteVersionConflict, repository.ErrPriceStateConflict} {
		recorder := httptest.NewRecorder()
		writeG5Error(recorder, conflict)
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":40900`) {
			t.Fatalf("G5 并发冲突必须统一映射为 HTTP 409: err=%v code=%d body=%s", conflict, recorder.Code, recorder.Body.String())
		}
	}
}

func TestCreateRouteRejectsUnknownFieldBeforeAuditAndWrite(t *testing.T) {
	handler := NewG5AdminHandler(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token/routes", strings.NewReader(`{"logical_model_code":"molin/qwen","unknown":true}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.CreateRoute(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("G5 DTO 未知字段必须返回 400: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateRouteAuditFailurePreventsWrite(t *testing.T) {
	audit := &rejectingAudit{}
	handler := NewG5AdminHandler(nil, audit)
	body := `{"logical_model_code":"molin/qwen","channel_id":1,"provider_model":"openrouter/qwen","priority":100,"weight":100,"timeout_ms":30000,"max_retries":0,"circuit_breaker_threshold":5,"fallback_order":0,"status":"active","version_no":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token/routes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.CreateRoute(recorder, req)
	if recorder.Code != http.StatusInternalServerError || audit.action != "route.create" {
		t.Fatalf("审计失败必须阻止路由写入: code=%d action=%s body=%s", recorder.Code, audit.action, recorder.Body.String())
	}
}

func (a *rejectingAudit) Record(_ context.Context, _ *uint64, _, action string, _, _ *string, _ string, _ any) error {
	a.action = action
	return errors.New("audit unavailable")
}

func TestDeleteModelAuditFailurePreventsWrite(t *testing.T) {
	audit := &rejectingAudit{}
	handler := NewModelHandler(nil).WithAudit(audit)
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/token/models/7", nil)
	req.SetPathValue("id", "7")
	recorder := httptest.NewRecorder()
	handler.DeleteModel(recorder, req)
	if recorder.Code != http.StatusInternalServerError || audit.action != "model.delete" {
		t.Fatalf("删除前审计失败应返回 500 且不得进入空服务，code=%d action=%s", recorder.Code, audit.action)
	}
}

func TestUpdateModelRejectsUnknownFieldBeforeWrite(t *testing.T) {
	handler := NewModelHandler(nil)
	req := httptest.NewRequest(http.MethodPatch, "/api/admin/token/models/7", strings.NewReader(`{"logical_model_code":"immutable"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "7")
	recorder := httptest.NewRecorder()
	handler.UpdateModel(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("未知字段必须被严格解码器拒绝，实际 code=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
