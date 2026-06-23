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

// MCPHandler 处理 MCP server 管理（管理端 plugin:manage）与用户端只读列表。
// 安全红线：任何响应不返回 server 凭证（以 has_auth 表征）；用户端不回 endpoint。
type MCPHandler struct {
	svc *service.MCPService
}

// NewMCPHandler 创建 MCP handler。
func NewMCPHandler(svc *service.MCPService) *MCPHandler {
	return &MCPHandler{svc: svc}
}

// ——— 管理端 server CRUD ———

// List GET /api/admin/mcp-servers，支持 ?status= 过滤 + 分页。
func (h *MCPHandler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	p := pagination.Parse(r)
	items, total, err := h.svc.ListServersPaged(r.Context(), status, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// Get GET /api/admin/mcp-servers/{id}
func (h *MCPHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.svc.GetServer(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "mcp server 不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// Create POST /api/admin/mcp-servers
func (h *MCPHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateMCPServerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.CreateServer(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrMCPServerCodeExists) {
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

// Update PATCH /api/admin/mcp-servers/{id}
func (h *MCPHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.UpdateMCPServerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.UpdateServer(r.Context(), id, req)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "mcp server 不存在")
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

// Delete DELETE /api/admin/mcp-servers/{id}
func (h *MCPHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if err := h.svc.DeleteServer(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "mcp server 不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// Discover POST /api/admin/mcp-servers/{id}/discover：触发 initialize+tools/list，回写快照。
func (h *MCPHandler) Discover(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	resp, err := h.svc.Discover(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "mcp server 不存在")
			return
		}
		if errors.Is(err, service.ErrMCPDiscoverFailed) {
			// 连接/握手失败：502，不改 server 状态（契约 §6.3）。
			response.Error(w, http.StatusBadGateway, 50200, "mcp discover 失败: "+err.Error())
			return
		}
		if isValidationErr(err) {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "discover 失败")
		return
	}
	response.JSON(w, http.StatusOK, resp)
}

// ListTools GET /api/admin/mcp-servers/{id}/tools：列已发现工具（含未审核）。
func (h *MCPHandler) ListTools(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	items, err := h.svc.ListTools(r.Context(), id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "mcp server 不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	// 工具列表不分页（单 server 工具数有限），仍以 items 包裹保持契约一致。
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// UpdateTool PATCH /api/admin/mcp-servers/{id}/tools/{toolId}：审核启用/停用单工具。
func (h *MCPHandler) UpdateTool(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	toolID, err := pathUint64(r, "toolId")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效工具 ID")
		return
	}
	var req dto.UpdateMCPToolReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	resp, err := h.svc.UpdateToolEnabled(r.Context(), id, toolID, req)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "mcp 工具不存在")
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

// BindAgentServers POST /api/admin/agents/{id}/mcp-servers：覆盖式绑定 Agent ↔ MCP server。
func (h *MCPHandler) BindAgentServers(w http.ResponseWriter, r *http.Request) {
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
	if err := h.svc.BindAgentServers(r.Context(), id, req.IDs); err != nil {
		switch {
		case errors.Is(err, service.ErrMCPBindNonOfficial):
			response.Error(w, http.StatusForbidden, 40003, err.Error())
		case isNotFound(err):
			response.Error(w, http.StatusNotFound, 40400, "agent 不存在")
		case isValidationErr(err):
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "绑定失败")
		}
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"bound": true})
}

// ——— 用户端只读 ———

// ListPublic GET /api/mcp-servers（用户端登录态）：仅 active，精简视图（不回 endpoint/凭证）。
func (h *MCPHandler) ListPublic(w http.ResponseWriter, r *http.Request) {
	p := pagination.Parse(r)
	items, total, err := h.svc.ListPublicServers(r.Context(), p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PagedResp{
		List:   items,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}
