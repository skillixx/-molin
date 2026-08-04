package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// G5AdminHandler 提供 AI 网关管理工作台接口；所有写操作先审计，审计失败即拒绝业务变更。
type G5AdminHandler struct {
	service *service.G5AdminService
	audit   governanceAuditRecorder
}

func NewG5AdminHandler(adminService *service.G5AdminService, audit governanceAuditRecorder) *G5AdminHandler {
	return &G5AdminHandler{service: adminService, audit: audit}
}

func (h *G5AdminHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	query := dto.G5DashboardQuery{From: now.Add(-24 * time.Hour), To: now, Model: r.URL.Query().Get("model"), Status: r.URL.Query().Get("status")}
	if value := strings.TrimSpace(r.URL.Query().Get("from")); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "from 必须是 RFC3339 时间")
			return
		}
		query.From = parsed
	}
	if value := strings.TrimSpace(r.URL.Query().Get("to")); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "to 必须是 RFC3339 时间")
			return
		}
		query.To = parsed
	}
	if value := strings.TrimSpace(r.URL.Query().Get("channel_id")); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed == 0 {
			response.Error(w, http.StatusBadRequest, 40000, "channel_id 不合法")
			return
		}
		query.ChannelID = parsed
	}
	item, err := h.service.Dashboard(r.Context(), query)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *G5AdminHandler) ListModelReleases(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	items, err := h.service.ListModelReleases(r.Context(), id)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

func (h *G5AdminHandler) PublishModel(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req dto.PublishModelReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "model.publish", "token_model", strconv.FormatUint(id, 10), map[string]interface{}{"reason_length": len(req.Reason)}) {
		return
	}
	item, err := h.service.PublishModel(r.Context(), id, operatorID, req.Reason)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *G5AdminHandler) UnpublishModel(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "model.unpublish", "token_model", strconv.FormatUint(id, 10), nil) {
		return
	}
	if err := h.service.UnpublishModel(r.Context(), id, operatorID); err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"unpublished": true})
}

func (h *G5AdminHandler) RollbackModel(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req dto.RollbackModelReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "model.rollback", "token_model", strconv.FormatUint(id, 10), map[string]interface{}{"target_version_no": req.TargetVersionNo, "reason_length": len(req.Reason)}) {
		return
	}
	item, err := h.service.RollbackModel(r.Context(), id, operatorID, req)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *G5AdminHandler) ListRoutes(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListRoutes(r.Context(), r.URL.Query().Get("model"), r.URL.Query().Get("status"), page, pageSize)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{List: items, Result: pagination.Result{Page: page, PageSize: pageSize, Total: total}})
}

func (h *G5AdminHandler) CreateRoute(w http.ResponseWriter, r *http.Request) {
	var req dto.RouteWriteReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "route.create", "model_route", "new", routeAuditSummary(req)) {
		return
	}
	item, err := h.service.CreateRoute(r.Context(), operatorID, req)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *G5AdminHandler) UpdateRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req dto.RouteWriteReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "route.update", "model_route", strconv.FormatUint(id, 10), routeAuditSummary(req)) {
		return
	}
	if err := h.service.UpdateRoute(r.Context(), id, operatorID, req); err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *G5AdminHandler) ListPrices(w http.ResponseWriter, r *http.Request) {
	page, pageSize := pageValues(r)
	items, total, err := h.service.ListPrices(r.Context(), r.URL.Query().Get("model"), r.URL.Query().Get("status"), page, pageSize)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{List: items, Result: pagination.Result{Page: page, PageSize: pageSize, Total: total}})
}

func (h *G5AdminHandler) PriceDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	item, err := h.service.PriceDetail(r.Context(), id)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, item)
}

func (h *G5AdminHandler) CreatePrice(w http.ResponseWriter, r *http.Request) {
	var req dto.CreatePriceReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "price.create", "price_version", "new", map[string]interface{}{"logical_model_code": req.LogicalModelCode, "sku_count": len(req.SKUs)}) {
		return
	}
	item, err := h.service.CreatePrice(r.Context(), operatorID, req)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *G5AdminHandler) ApprovePrice(w http.ResponseWriter, r *http.Request) {
	h.priceAction(w, r, "approve")
}
func (h *G5AdminHandler) PublishPrice(w http.ResponseWriter, r *http.Request) {
	h.priceAction(w, r, "publish")
}
func (h *G5AdminHandler) RetirePrice(w http.ResponseWriter, r *http.Request) {
	h.priceAction(w, r, "retire")
}

func (h *G5AdminHandler) SuspendPrice(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req dto.PriceStatusReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "price.suspend", "price_version", strconv.FormatUint(id, 10), map[string]interface{}{"reason_length": len(req.Reason)}) {
		return
	}
	if err := h.service.SuspendPrice(r.Context(), id, req.Reason); err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{"suspended": true})
}

func (h *G5AdminHandler) RollbackPrice(w http.ResponseWriter, r *http.Request) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	var req dto.RollbackPriceReq
	if !decodeGovernanceJSON(w, r, &req) {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "price.rollback", "price_version", strconv.FormatUint(id, 10), map[string]interface{}{"reason_length": len(req.Reason), "effective_at": req.EffectiveAt, "cost_expires_at": req.CostExpiresAt}) {
		return
	}
	item, err := h.service.RollbackPrice(r.Context(), id, operatorID, req)
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, item)
}

func (h *G5AdminHandler) priceAction(w http.ResponseWriter, r *http.Request, action string) {
	id, ok := governancePathUint64(w, r, "id")
	if !ok {
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if !h.auditBeforeWrite(w, r, operatorID, "price."+action, "price_version", strconv.FormatUint(id, 10), nil) {
		return
	}
	var err error
	switch action {
	case "approve":
		err = h.service.ApprovePrice(r.Context(), id, operatorID)
	case "publish":
		err = h.service.PublishPrice(r.Context(), id)
	case "retire":
		err = h.service.RetirePrice(r.Context(), id)
	}
	if err != nil {
		writeG5Error(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]bool{action + "d": true})
}

func (h *G5AdminHandler) auditBeforeWrite(w http.ResponseWriter, r *http.Request, operatorID uint64, action, targetType, targetID string, summary interface{}) bool {
	if h.audit == nil || h.audit.Record(r.Context(), &operatorID, "token_gateway", action, &targetType, &targetID, r.RemoteAddr, summary) != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "审计记录失败，操作未执行")
		return false
	}
	return true
}

func routeAuditSummary(req dto.RouteWriteReq) map[string]interface{} {
	return map[string]interface{}{"logical_model_code": req.LogicalModelCode, "channel_id": req.ChannelID, "provider_model": req.ProviderModel, "version_no": req.VersionNo, "status": req.Status}
}

func writeG5Error(w http.ResponseWriter, err error) {
	switch {
	case service.IsValidation(err):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		response.Error(w, http.StatusNotFound, 40400, "记录不存在")
	case service.IsG5Conflict(err), repository.IsDuplicateKeyForHandler(err):
		response.Error(w, http.StatusConflict, 40900, "状态、版本或唯一键冲突")
	default:
		message := "AI 网关管理操作失败"
		if strings.Contains(err.Error(), "doesn't exist") {
			message = "数据库尚未执行 G5 Migration"
		}
		response.Error(w, http.StatusInternalServerError, 50000, message)
	}
}
