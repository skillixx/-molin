package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/iam/dto"
	"molin/server/internal/modules/iam/model"
	"molin/server/internal/modules/iam/repository"
	"molin/server/internal/modules/iam/service"
	"molin/server/pkg/response"
)

// IAMHandler 处理角色、权限、用户角色分配相关 HTTP 请求。
type IAMHandler struct {
	iamSvc *service.IAMService
}

func NewIAMHandler(iamSvc *service.IAMService) *IAMHandler {
	return &IAMHandler{iamSvc: iamSvc}
}

// ListRoles GET /api/admin/roles
func (h *IAMHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.iamSvc.ListRoles(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	resp := make([]dto.RoleResp, len(roles))
	for i, role := range roles {
		resp[i] = dto.RoleResp{ID: role.ID, Code: role.Code, Name: role.Name, Description: role.Description}
	}
	response.JSON(w, http.StatusOK, resp)
}

// CreateRole POST /api/admin/roles
func (h *IAMHandler) CreateRole(w http.ResponseWriter, r *http.Request) {
	var req dto.RoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	role := &model.Role{Code: req.Code, Name: req.Name, Description: req.Description}
	if err := h.iamSvc.CreateRole(r.Context(), role); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "创建失败")
		return
	}
	response.JSON(w, http.StatusCreated, dto.RoleResp{ID: role.ID, Code: role.Code, Name: role.Name})
}

// UpdateRole PUT /api/admin/roles/{id}
func (h *IAMHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.RoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	updates := map[string]interface{}{"name": req.Name, "description": req.Description}
	if err := h.iamSvc.UpdateRole(r.Context(), id, updates); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// DeleteRole DELETE /api/admin/roles/{id}
func (h *IAMHandler) DeleteRole(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if err := h.iamSvc.DeleteRole(r.Context(), id); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// ListPermissions GET /api/admin/permissions
func (h *IAMHandler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	perms, err := h.iamSvc.ListPermissions(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	resp := make([]dto.PermissionResp, len(perms))
	for i, p := range perms {
		resp[i] = dto.PermissionResp{ID: p.ID, Code: p.Code, Name: p.Name, Resource: p.Resource, Action: p.Action}
	}
	response.JSON(w, http.StatusOK, resp)
}

// GetUserRoles GET /api/admin/users/{id}/roles
// 返回用户已分配角色的详情（id、code、name、created_at），而非关联表原始字段。
func (h *IAMHandler) GetUserRoles(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	roles, err := h.iamSvc.GetUserRoles(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	// 将 model.Role 映射为符合 API 规范的 DTO，确保返回小写 JSON 字段
	resp := make([]dto.UserRoleResp, len(roles))
	for i, role := range roles {
		resp[i] = dto.UserRoleResp{
			ID:          role.ID,
			Code:        role.Code,
			Name:        role.Name,
			Description: role.Description,
			CreatedAt:   role.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	response.JSON(w, http.StatusOK, resp)
}

// AssignRole POST /api/admin/users/{id}/roles
func (h *IAMHandler) AssignRole(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	var req dto.AssignRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.iamSvc.AssignRole(r.Context(), userID, req.RoleID, operatorID, req.Reason); err != nil {
		// 重复分配：该用户已拥有此角色，返回 409 Conflict
		if errors.Is(err, repository.ErrUserRoleExists) {
			response.Error(w, http.StatusConflict, 40900, "该用户已拥有此角色")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "分配失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// RevokeRole DELETE /api/admin/users/{id}/roles/{role_id}
func (h *IAMHandler) RevokeRole(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	roleID, err := pathUint64(r, "role_id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效角色 ID")
		return
	}
	if err := h.iamSvc.RevokeRole(r.Context(), userID, roleID); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "撤销失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// GetPermissionOverrides GET /api/admin/users/{id}/permission-overrides
func (h *IAMHandler) GetPermissionOverrides(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	overrides, err := h.iamSvc.GetPermissionOverrides(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, overrides)
}

// SetPermissionOverride POST /api/admin/users/{id}/permission-overrides
func (h *IAMHandler) SetPermissionOverride(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	var req dto.OverrideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// 枚举校验 effect 字段：只允许 "allow" 或 "deny"，防止非标准值（如 "DENY"、"Allow"）
	// 绕过精确匹配导致 deny override 静默失效
	if req.Effect != "allow" && req.Effect != "deny" {
		response.Error(w, http.StatusBadRequest, 40000, "effect 只能为 allow 或 deny")
		return
	}
	// 查权限码
	perms, _ := h.iamSvc.ListPermissions(r.Context())
	permCode := ""
	for _, p := range perms {
		if p.ID == req.PermissionID {
			permCode = p.Code
			break
		}
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	override := &model.UserPermissionOverride{
		UserID:         userID,
		PermissionID:   req.PermissionID,
		PermissionCode: permCode,
		Effect:         req.Effect,
		Reason:         req.Reason,
		CreatedBy:      &operatorID,
	}
	if err := h.iamSvc.SetPermissionOverride(r.Context(), override); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "设置失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// DeletePermissionOverride DELETE /api/admin/users/{id}/permission-overrides/{override_id}
func (h *IAMHandler) DeletePermissionOverride(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	overrideID, err := pathUint64(r, "override_id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效覆盖 ID")
		return
	}
	if err := h.iamSvc.DeletePermissionOverride(r.Context(), overrideID, userID); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// pathUint64 从 Go 1.22 路由 PathValue 中解析 uint64 参数。
func pathUint64(r *http.Request, key string) (uint64, error) {
	return strconv.ParseUint(r.PathValue(key), 10, 64)
}
