package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// ModelHandler 处理 Token 网关对外模型目录管理（管理端，需 token:manage + 管理员双重认证）。
type ModelHandler struct {
	svc *service.CatalogService
}

// NewModelHandler 创建模型目录管理 handler。
func NewModelHandler(svc *service.CatalogService) *ModelHandler {
	return &ModelHandler{svc: svc}
}

// ListModels GET /api/admin/token/models
// 支持 ?status= / ?modality= 过滤 + 分页 ?page=&page_size=。
func (h *ModelHandler) ListModels(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	modality := r.URL.Query().Get("modality")
	p := pagination.Parse(r)
	items, total, err := h.svc.ListPaged(r.Context(), status, modality, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// GetModel GET /api/admin/token/models/{id}
func (h *ModelHandler) GetModel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "模型不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// CreateModel POST /api/admin/token/models
func (h *ModelHandler) CreateModel(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.Create(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrModelCodeExists) {
			response.Error(w, http.StatusConflict, 40900, err.Error())
			return
		}
		if isValidationErr(err) {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "创建失败")
		return
	}
	response.JSON(w, http.StatusCreated, resp)
}

// UpdateModel PATCH /api/admin/token/models/{id}
func (h *ModelHandler) UpdateModel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.UpdateModelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "模型不存在")
			return
		}
		if isValidationErr(err) {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// DeleteModel DELETE /api/admin/token/models/{id}
func (h *ModelHandler) DeleteModel(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "模型不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}
