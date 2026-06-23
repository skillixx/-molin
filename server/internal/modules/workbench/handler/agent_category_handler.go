package handler

import (
	"net/http"

	"molin/server/internal/modules/workbench/service"
	"molin/server/pkg/response"
)

// AgentCategoryHandler 处理 Agent 分类字典只读接口（用户端导航 + 管理端全量）。
type AgentCategoryHandler struct {
	svc *service.AgentCategoryService
}

// NewAgentCategoryHandler 创建分类字典 handler。
func NewAgentCategoryHandler(svc *service.AgentCategoryService) *AgentCategoryHandler {
	return &AgentCategoryHandler{svc: svc}
}

// UserList GET /api/agent-categories（登录态）：仅 active，按 sort_order。
// 量小不分页，但统一包 {items}（D-95 习惯）。
func (h *AgentCategoryHandler) UserList(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListActive(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}

// AdminList GET /api/admin/agent-categories（agent:manage）：含 inactive 全量。
func (h *AgentCategoryHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListAll(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"items": items})
}
