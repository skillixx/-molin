package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// PluginHandler 处理 Plugin 管理（管理端 plugin:manage）与用户端只读列表。
// 安全红线：任何响应不返回插件凭证（以 has_auth 表征）。
type PluginHandler struct {
	svc *service.PluginService
}

// NewPluginHandler 创建 Plugin handler。
func NewPluginHandler(svc *service.PluginService) *PluginHandler {
	return &PluginHandler{svc: svc}
}

// List GET /api/admin/plugins，支持 ?status= 过滤 + 分页。
func (h *PluginHandler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	p := pagination.Parse(r)
	items, total, err := h.svc.ListPaged(r.Context(), status, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// ListPublic GET /api/plugins（用户端，登录态）：仅 active，精简视图（不回 endpoint/凭证）。
func (h *PluginHandler) ListPublic(w http.ResponseWriter, r *http.Request) {
	p := pagination.Parse(r)
	items, total, err := h.svc.ListPublic(r.Context(), p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// Get GET /api/admin/plugins/{id}
func (h *PluginHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "plugin 不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// Create POST /api/admin/plugins
func (h *PluginHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreatePluginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.Create(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrPluginCodeExists) {
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

// Update PATCH /api/admin/plugins/{id}
func (h *PluginHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.UpdatePluginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "plugin 不存在")
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

// Delete DELETE /api/admin/plugins/{id}
func (h *PluginHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "plugin 不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}
