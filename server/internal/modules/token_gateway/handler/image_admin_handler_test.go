package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/service"
)

type fakeImageAdminApplication struct {
	quarantineCalled int
	reconcileCalled  int
	tasks            []dto.ImageAdminTaskResp
	total            int64
}

func (f *fakeImageAdminApplication) ListAdminTasks(context.Context, service.ImageAdminTaskListInput) ([]dto.ImageAdminTaskResp, int64, error) {
	return f.tasks, f.total, nil
}
func (f *fakeImageAdminApplication) GetAdminTask(context.Context, string) (*dto.ImageAdminTaskResp, error) {
	return &dto.ImageAdminTaskResp{}, nil
}
func (f *fakeImageAdminApplication) ListAdminAssets(context.Context, service.ImageAdminAssetListInput) ([]dto.ImageAdminAssetResp, int64, error) {
	return nil, 0, nil
}
func (f *fakeImageAdminApplication) QuarantineAsset(_ context.Context, assetID string, version uint64) (*dto.ImageAdminAssetResp, error) {
	f.quarantineCalled++
	return &dto.ImageAdminAssetResp{ImageAssetResp: dto.ImageAssetResp{AssetID: assetID}, VersionNo: version + 1}, nil
}
func (f *fakeImageAdminApplication) Reconcile(context.Context, string) (*service.ImageReconciliationReport, error) {
	f.reconcileCalled++
	return &service.ImageReconciliationReport{}, nil
}
func (f *fakeImageAdminApplication) ReconciliationSummary(context.Context) (*dto.ImageReconciliationSummaryResp, error) {
	return &dto.ImageReconciliationSummaryResp{}, nil
}

type fakeImageAudit struct {
	err     error
	called  int
	action  string
	summary interface{}
}

func (f *fakeImageAudit) Record(_ context.Context, _ *uint64, _ string, action string, _, _ *string, _ string, summary any) error {
	f.called++
	f.action = action
	f.summary = summary
	return f.err
}

func TestImageAdminQuarantineFailsClosedWhenAuditFails(t *testing.T) {
	app := &fakeImageAdminApplication{}
	audit := &fakeImageAudit{err: errors.New("审计不可用")}
	h := NewImageAdminHandler(app, audit)
	r := httptest.NewRequest(http.MethodPost, "/api/admin/token/image-assets/asset-1/quarantine", strings.NewReader(`{"reason":"安全复核","version_no":1}`))
	r.SetPathValue("asset_id", "asset-1")
	w := httptest.NewRecorder()
	h.QuarantineAsset(w, r)
	if w.Code != http.StatusInternalServerError || audit.called != 1 || app.quarantineCalled != 0 {
		t.Fatalf("前置审计失败必须阻断隔离: status=%d audit=%d service=%d", w.Code, audit.called, app.quarantineCalled)
	}
}

func TestImageAdminQuarantineRequiresReasonAndVersion(t *testing.T) {
	for _, body := range []string{`{"reason":"","version_no":1}`, `{"reason":"安全复核","version_no":0}`} {
		app := &fakeImageAdminApplication{}
		audit := &fakeImageAudit{}
		h := NewImageAdminHandler(app, audit)
		r := httptest.NewRequest(http.MethodPost, "/api/admin/token/image-assets/asset-1/quarantine", strings.NewReader(body))
		r.SetPathValue("asset_id", "asset-1")
		w := httptest.NewRecorder()
		h.QuarantineAsset(w, r)
		if w.Code != http.StatusBadRequest || audit.called != 0 || app.quarantineCalled != 0 {
			t.Fatalf("隔离参数门禁错误: status=%d audit=%d service=%d", w.Code, audit.called, app.quarantineCalled)
		}
	}
}

func TestImageAdminReconcileAuditsBeforeService(t *testing.T) {
	app := &fakeImageAdminApplication{}
	audit := &fakeImageAudit{}
	h := NewImageAdminHandler(app, audit)
	r := httptest.NewRequest(http.MethodPost, "/api/admin/token/image-requests/req-1/reconcile", strings.NewReader(`{"reason":"处理结算异常"}`))
	r.SetPathValue("request_id", "req-1")
	w := httptest.NewRecorder()
	h.ReconcileRequest(w, r)
	if w.Code != http.StatusOK || audit.called != 1 || audit.action != "image.request.reconcile" || app.reconcileCalled != 1 {
		t.Fatalf("对账前置审计错误: status=%d audit=%d action=%s service=%d", w.Code, audit.called, audit.action, app.reconcileCalled)
	}
}

func TestImageAdminTaskListUsesD95(t *testing.T) {
	app := &fakeImageAdminApplication{tasks: []dto.ImageAdminTaskResp{{ImageTaskResp: dto.ImageTaskResp{TaskID: "task-1"}}}, total: 1}
	h := NewImageAdminHandler(app, &fakeImageAudit{})
	r := httptest.NewRequest(http.MethodGet, "/api/admin/token/image-tasks?page=1&page_size=20", nil)
	w := httptest.NewRecorder()
	h.ListTasks(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"items"`) || !strings.Contains(w.Body.String(), `"total":1`) || strings.Contains(w.Body.String(), "pagination") {
		t.Fatalf("管理端D-95错误: status=%d body=%s", w.Code, w.Body.String())
	}
}
