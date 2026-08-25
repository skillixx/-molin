package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/httputil"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

type ImageAdminHandler struct {
	service ImageAdminApplication
	audit   governanceAuditRecorder
}

type ImageAdminApplication interface {
	ListAdminTasks(context.Context, service.ImageAdminTaskListInput) ([]dto.ImageAdminTaskResp, int64, error)
	GetAdminTask(context.Context, string) (*dto.ImageAdminTaskResp, error)
	ListAdminAssets(context.Context, service.ImageAdminAssetListInput) ([]dto.ImageAdminAssetResp, int64, error)
	QuarantineAsset(context.Context, string, uint64) (*dto.ImageAdminAssetResp, error)
	Reconcile(context.Context, string) (*service.ImageReconciliationReport, error)
	ReconciliationSummary(context.Context) (*dto.ImageReconciliationSummaryResp, error)
}

func NewImageAdminHandler(imageService ImageAdminApplication, audit governanceAuditRecorder) *ImageAdminHandler {
	return &ImageAdminHandler{service: imageService, audit: audit}
}

func (h *ImageAdminHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	params := pagination.Parse(r)
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if !validImageTaskStatus(status) {
		response.Error(w, http.StatusBadRequest, 40000, "status参数错误")
		return
	}
	userID, ok := optionalImageAdminUint64(w, r, "user_id")
	if !ok {
		return
	}
	projectID, ok := optionalImageAdminUint64(w, r, "project_id")
	if !ok {
		return
	}
	items, total, err := h.service.ListAdminTasks(r.Context(), service.ImageAdminTaskListInput{
		UserID: userID, ProjectID: projectID, Status: status, Model: strings.TrimSpace(r.URL.Query().Get("model")),
		Page: params.Page, PageSize: params.PageSize,
	})
	if err != nil {
		writeImageAdminError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": params.Page, "page_size": params.PageSize, "total": total})
}

func (h *ImageAdminHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetAdminTask(r.Context(), r.PathValue("task_id"))
	if err != nil {
		writeImageAdminError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *ImageAdminHandler) ListAssets(w http.ResponseWriter, r *http.Request) {
	params := pagination.Parse(r)
	lifecycle := strings.TrimSpace(r.URL.Query().Get("lifecycle_state"))
	dispute := strings.TrimSpace(r.URL.Query().Get("dispute_status"))
	if !validImageAssetFilters(lifecycle, dispute) {
		response.Error(w, http.StatusBadRequest, 40000, "资产状态参数错误")
		return
	}
	userID, ok := optionalImageAdminUint64(w, r, "user_id")
	if !ok {
		return
	}
	projectID, ok := optionalImageAdminUint64(w, r, "project_id")
	if !ok {
		return
	}
	items, total, err := h.service.ListAdminAssets(r.Context(), service.ImageAdminAssetListInput{
		UserID: userID, ProjectID: projectID, LifecycleState: lifecycle,
		DisputeStatus: dispute, Page: params.Page, PageSize: params.PageSize,
	})
	if err != nil {
		writeImageAdminError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items, "page": params.Page, "page_size": params.PageSize, "total": total})
}

func validImageAssetFilters(lifecycle, dispute string) bool {
	lifecycles := map[string]bool{"": true, "temporary": true, "available": true, "quarantined": true, "expiring": true, "deleting": true, "deleted": true, "delete_failed": true}
	disputes := map[string]bool{"": true, "none": true, "open": true, "resolved": true}
	return lifecycles[lifecycle] && disputes[dispute]
}

func (h *ImageAdminHandler) QuarantineAsset(w http.ResponseWriter, r *http.Request) {
	var request dto.ImageQuarantineReq
	if !decodeImageJSON(w, r, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 512 || request.VersionNo == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "reason和version_no必须提供")
		return
	}
	assetID := r.PathValue("asset_id")
	if !h.auditBeforeWrite(w, r, "image.asset.quarantine", "image_asset", assetID, map[string]interface{}{
		"reason": request.Reason, "version_no": request.VersionNo,
	}) {
		return
	}
	item, err := h.service.QuarantineAsset(r.Context(), assetID, request.VersionNo)
	if err != nil {
		writeImageAdminError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *ImageAdminHandler) ReconcileRequest(w http.ResponseWriter, r *http.Request) {
	var request dto.ImageReconcileReq
	if !decodeImageJSON(w, r, &request) {
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 512 {
		response.Error(w, http.StatusBadRequest, 40000, "reason必须提供且不超过512字节")
		return
	}
	requestID := r.PathValue("request_id")
	if !h.auditBeforeWrite(w, r, "image.request.reconcile", "image_request", requestID, map[string]interface{}{"reason": request.Reason}) {
		return
	}
	item, err := h.service.Reconcile(r.Context(), requestID)
	if err != nil {
		writeImageAdminError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *ImageAdminHandler) ReconciliationSummary(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.ReconciliationSummary(r.Context())
	if err != nil {
		writeImageAdminError(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *ImageAdminHandler) auditBeforeWrite(w http.ResponseWriter, r *http.Request, action, targetType, targetID string, summary interface{}) bool {
	operatorID := middleware.UserIDFromContext(r.Context())
	if h.audit == nil || h.audit.Record(r.Context(), &operatorID, "token_gateway", action, &targetType, &targetID, httputil.ClientIP(r), summary) != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "审计记录失败，操作未执行")
		return false
	}
	return true
}

func optionalImageAdminUint64(w http.ResponseWriter, r *http.Request, name string) (uint64, bool) {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		response.Error(w, http.StatusBadRequest, 40000, name+"参数错误")
		return 0, false
	}
	return parsed, true
}

func writeImageAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, repository.ErrImageTaskNotFound), errors.Is(err, repository.ErrImageAssetNotFound), errors.Is(err, repository.ErrRequestNotFound):
		response.Error(w, http.StatusNotFound, 40400, "记录不存在")
	case errors.Is(err, repository.ErrImageAssetConflict), errors.Is(err, repository.ErrImageAssetTransition):
		response.Error(w, http.StatusConflict, 40900, "资产状态已变化或不允许当前操作")
	case errors.Is(err, service.ErrImagePendingReconcile), errors.Is(err, service.ErrImageReconcileMismatch):
		response.ErrorWithType(w, http.StatusConflict, 40930, "reconciliation_mismatch", "图片请求仍存在对账差异")
	default:
		response.Error(w, http.StatusInternalServerError, 50000, "图片管理操作失败")
	}
}
