package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/workbench/dto"
	"molin/server/internal/modules/workbench/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// AgentHandler 处理 Agent 管理（管理端 agent:manage）与用户端（自建 + 选用）。
type AgentHandler struct {
	svc *service.AgentService
}

// NewAgentHandler 创建 Agent handler。
func NewAgentHandler(svc *service.AgentService) *AgentHandler {
	return &AgentHandler{svc: svc}
}

// ——— 管理端 ———

// AdminList GET /api/admin/agents，默认列官方，?owner_type= / ?status= 过滤 + 分页。
func (h *AgentHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	ownerType := r.URL.Query().Get("owner_type")
	if ownerType == "" {
		ownerType = "official"
	}
	status := r.URL.Query().Get("status")
	category := r.URL.Query().Get("category")
	visibleScope := r.URL.Query().Get("visible_scope")
	p := pagination.Parse(r)
	items, total, err := h.svc.AdminList(r.Context(), ownerType, status, category, visibleScope, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// AdminGet GET /api/admin/agents/{id}
func (h *AgentHandler) AdminGet(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "agent 不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// AdminCreate POST /api/admin/agents
func (h *AgentHandler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	var req dto.AdminCreateAgentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.AdminCreate(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrAgentCodeExists) {
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

// AdminUpdate PATCH /api/admin/agents/{id}
func (h *AgentHandler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.AdminUpdateAgentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.AdminUpdate(r.Context(), id, req)
	if err != nil {
		h.writeMutationErr(w, err, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// AdminDelete DELETE /api/admin/agents/{id}
func (h *AgentHandler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if err := h.svc.AdminDelete(r.Context(), id); err != nil {
		h.writeMutationErr(w, err, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// BindSkills POST /api/admin/agents/{id}/skills（覆盖式绑定/解绑，body {ids:[...]}）。
func (h *AgentHandler) BindSkills(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.BindIDsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.BindSkills(r.Context(), id, req.IDs)
	if err != nil {
		h.writeMutationErr(w, err, "绑定失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// BindPlugins POST /api/admin/agents/{id}/plugins（覆盖式绑定/解绑，body {ids:[...]}）。
func (h *AgentHandler) BindPlugins(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.BindIDsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.BindPlugins(r.Context(), id, req.IDs)
	if err != nil {
		h.writeMutationErr(w, err, "绑定失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// SetVisibility PUT /api/admin/agents/{id}/visibility：覆盖式设置定向可见性。
func (h *AgentHandler) SetVisibility(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.SetVisibilityReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.SetVisibility(r.Context(), id, req)
	if err != nil {
		h.writeMutationErr(w, err, "设置可见性失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// ——— 用户端 ———

// UserList GET /api/agents：官方 active + 本人自建。
func (h *AgentHandler) UserList(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	category := r.URL.Query().Get("category")
	p := pagination.Parse(r)
	items, total, err := h.svc.UserList(r.Context(), userID, category, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// UserGet GET /api/agents/{id}：官方 active 或本人自建，否则 40003。
func (h *AgentHandler) UserGet(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	userID := middleware.UserIDFromContext(r.Context())
	resp, err := h.svc.UserGet(r.Context(), userID, id)
	if err != nil {
		h.writeMutationErr(w, err, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// UserCreate POST /api/agents：用户自建（owner_type=user）。
func (h *AgentHandler) UserCreate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.UserCreateAgentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.UserCreate(r.Context(), userID, req)
	if err != nil {
		if isValidationErr(err) {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "创建失败")
		return
	}
	response.JSON(w, http.StatusCreated, resp)
}

// UserUpdate PATCH /api/agents/{id}：仅本人自建，越权 40003。
func (h *AgentHandler) UserUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	userID := middleware.UserIDFromContext(r.Context())
	var req dto.UserUpdateAgentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.UserUpdate(r.Context(), userID, id, req)
	if err != nil {
		h.writeMutationErr(w, err, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// UserDelete DELETE /api/agents/{id}：仅本人自建，越权 40003。
func (h *AgentHandler) UserDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	userID := middleware.UserIDFromContext(r.Context())
	if err := h.svc.UserDelete(r.Context(), userID, id); err != nil {
		h.writeMutationErr(w, err, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// writeMutationErr 统一映射 NotFound(404)/Forbidden(40003)/Validation(400)/其他(500)。
func (h *AgentHandler) writeMutationErr(w http.ResponseWriter, err error, fallbackMsg string) {
	switch {
	case isForbidden(err):
		response.Error(w, http.StatusForbidden, 40003, "无权操作该 Agent")
	case isNotFound(err):
		response.Error(w, http.StatusNotFound, 40400, "agent 不存在")
	case isValidationErr(err):
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
	default:
		response.Error(w, http.StatusInternalServerError, 50000, fallbackMsg)
	}
}
