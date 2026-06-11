package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/identity/dto"
	"molin/server/internal/modules/identity/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// PagedResp 通用分页响应结构，包含列表数据和分页元数据。
type PagedResp struct {
	List       interface{}       `json:"items"`
	Pagination pagination.Result `json:"pagination"`
}

// IdentityHandler 处理实名认证相关 HTTP 请求。
type IdentityHandler struct {
	identitySvc *service.IdentityService
}

func NewIdentityHandler(identitySvc *service.IdentityService) *IdentityHandler {
	return &IdentityHandler{identitySvc: identitySvc}
}

// Submit POST /api/identity/verifications
func (h *IdentityHandler) Submit(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.SubmitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	id, err := h.identitySvc.Submit(r.Context(), userID, req)
	if err != nil {
		switch err {
		case service.ErrAlreadySubmitted:
			response.Error(w, http.StatusConflict, 40901, err.Error())
		case service.ErrIDCardAlreadyBound:
			response.Error(w, http.StatusConflict, 40902, err.Error())
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "提交失败")
		}
		return
	}
	response.JSON(w, http.StatusCreated, dto.SubmitResp{ID: id})
}

// GetMyVerification GET /api/identity/verifications/me
func (h *IdentityHandler) GetMyVerification(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	resp, err := h.identitySvc.GetMyVerification(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "暂无认证记录")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// ListPending GET /api/admin/identity-verifications
// 支持 ?status=pending|verified|rejected（空值=全部）和分页参数 ?page=1&page_size=20。
func (h *IdentityHandler) ListPending(w http.ResponseWriter, r *http.Request) {
	// status 非空时按状态过滤，空字符串时查全部状态（兼容旧调用方默认只看 pending）
	status := r.URL.Query().Get("status")
	p := pagination.Parse(r)
	list, total, err := h.identitySvc.ListPaged(r.Context(), status, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:       list,
		Pagination: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// GetDetail GET /api/admin/identity-verifications/{id}
func (h *IdentityHandler) GetDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.identitySvc.GetVerification(r.Context(), id)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "记录不存在")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// Review PATCH /api/admin/identity-verifications/{id}/review
func (h *IdentityHandler) Review(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.ReviewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.identitySvc.Review(r.Context(), id, operatorID, req.Approve, req.Reason); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "审核失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}
