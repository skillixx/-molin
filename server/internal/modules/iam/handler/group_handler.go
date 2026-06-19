package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/iam/dto"
	"molin/server/internal/modules/iam/model"
	"molin/server/internal/modules/iam/repository"
	"molin/server/internal/modules/iam/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

const isoFmt = "2006-01-02T15:04:05Z07:00"

// GroupHandler 处理用户分组相关 HTTP 请求（超管权限 group:manage）。
type GroupHandler struct {
	groupSvc *service.GroupService
}

func NewGroupHandler(groupSvc *service.GroupService) *GroupHandler {
	return &GroupHandler{groupSvc: groupSvc}
}

// ——— 分组 CRUD ———

// ListGroups GET /api/admin/user-groups
func (h *GroupHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groupType := r.URL.Query().Get("type")
	keyword := r.URL.Query().Get("keyword")
	p := pagination.Parse(r)
	groups, total, err := h.groupSvc.ListGroupsPaged(r.Context(), groupType, keyword, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	list := make([]dto.GroupResp, len(groups))
	for i, g := range groups {
		list[i] = groupToResp(g)
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:   list,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// GetGroup GET /api/admin/user-groups/{id}
func (h *GroupHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	g, err := h.groupSvc.GetGroup(r.Context(), id)
	if err != nil {
		// D-74：区分"分组不存在"与其他 DB 错误，避免 DB 故障时客户端误以为资源不存在
		if errors.Is(err, repository.ErrGroupNotFound) {
			response.Error(w, http.StatusNotFound, 40400, "分组不存在")
		} else {
			response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		}
		return
	}
	response.JSON(w, http.StatusOK, groupToResp(*g))
}

// CreateGroup POST /api/admin/user-groups
func (h *GroupHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateGroupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.Code == "" || req.Name == "" {
		response.Error(w, http.StatusBadRequest, 40000, "code 和 name 不能为空")
		return
	}
	// D-76：字段长度上限校验，防止超长输入被 MySQL 截断或导致 500
	if len(req.Code) > 128 {
		response.Error(w, http.StatusBadRequest, 40000, "code 长度不能超过 128 字符")
		return
	}
	if len(req.Name) > 128 {
		response.Error(w, http.StatusBadRequest, 40000, "name 长度不能超过 128 字符")
		return
	}
	if req.Description != nil && len(*req.Description) > 512 {
		response.Error(w, http.StatusBadRequest, 40000, "description 长度不能超过 512 字符")
		return
	}
	groupType := req.Type
	if groupType == "" {
		groupType = "custom"
	}
	// D-73：type 枚举白名单校验，拒绝非法值
	validTypes := map[string]bool{"region": true, "org": true, "custom": true}
	if !validTypes[groupType] {
		response.Error(w, http.StatusBadRequest, 40000, "type 只能为 region/org/custom")
		return
	}
	g := &model.UserGroup{
		Code:        req.Code,
		Name:        req.Name,
		Type:        groupType,
		IsDefault:   req.IsDefault,
		Description: req.Description,
	}
	if err := h.groupSvc.CreateGroup(r.Context(), g); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "创建失败")
		return
	}
	response.JSON(w, http.StatusCreated, groupToResp(*g))
}

// UpdateGroup PUT /api/admin/user-groups/{id}
func (h *GroupHandler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	var req dto.UpdateGroupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// 只将 JSON 中明确传了值的字段（非 nil）放入 map，避免零值覆盖现有数据（PATCH 语义）。
	updates := map[string]interface{}{}
	if req.Name != nil {
		// D-76：name 长度上限校验
		if len(*req.Name) > 128 {
			response.Error(w, http.StatusBadRequest, 40000, "name 长度不能超过 128 字符")
			return
		}
		updates["name"] = *req.Name
	}
	if req.Type != nil {
		// D-73：UpdateGroup 的 type 枚举白名单校验
		validTypes := map[string]bool{"region": true, "org": true, "custom": true}
		if !validTypes[*req.Type] {
			response.Error(w, http.StatusBadRequest, 40000, "type 只能为 region/org/custom")
			return
		}
		updates["type"] = *req.Type
	}
	if req.Description != nil {
		// D-76：description 长度上限校验
		if len(*req.Description) > 512 {
			response.Error(w, http.StatusBadRequest, 40000, "description 长度不能超过 512 字符")
			return
		}
		updates["description"] = *req.Description
	}
	if req.IsDefault != nil {
		updates["is_default"] = *req.IsDefault
	}
	if len(updates) == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "至少需要提供一个更新字段")
		return
	}
	if err := h.groupSvc.UpdateGroup(r.Context(), id, updates); err != nil {
		if errors.Is(err, repository.ErrGroupNotFound) {
			response.Error(w, http.StatusNotFound, 40400, "分组不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// DeleteGroup DELETE /api/admin/user-groups/{id}
func (h *GroupHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if err := h.groupSvc.DeleteGroup(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrGroupNotFound) {
			response.Error(w, http.StatusNotFound, 40400, "分组不存在")
			return
		}
		if errors.Is(err, repository.ErrGroupNotEmpty) {
			response.Error(w, http.StatusConflict, 40901, err.Error())
			return
		}
		if errors.Is(err, repository.ErrGroupHasActiveCodes) {
			response.Error(w, http.StatusConflict, 40902, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// ——— 成员管理 ———

// ListMembers GET /api/admin/user-groups/{id}/members
func (h *GroupHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	groupRole := r.URL.Query().Get("group_role")
	p := pagination.Parse(r)
	members, total, err := h.groupSvc.ListMembersPaged(r.Context(), groupID, groupRole, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	list := make([]dto.GroupMemberResp, len(members))
	for i, m := range members {
		list[i] = dto.GroupMemberResp{
			ID:        m.ID,
			UserID:    m.UserID,
			GroupID:   m.GroupID,
			GroupRole: m.GroupRole,
			CreatedAt: m.CreatedAt.Format(isoFmt),
		}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:   list,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// AddMember POST /api/admin/user-groups/{id}/members
func (h *GroupHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	var req dto.AddMemberReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.UserID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "user_id 不能为空")
		return
	}
	if err := h.groupSvc.AddMember(r.Context(), groupID, req.UserID, req.GroupRole); err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			// D-35：用户不存在，返回 404
			response.Error(w, http.StatusNotFound, 40400, "用户不存在")
			return
		}
		// D-71：分组不存在，返回 404
		if errors.Is(err, repository.ErrGroupNotFound) {
			response.Error(w, http.StatusNotFound, 40400, "分组不存在")
			return
		}
		if errors.Is(err, repository.ErrMemberAlreadyExists) {
			response.Error(w, http.StatusConflict, 40900, err.Error())
			return
		}
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusCreated, nil)
}

// UpdateMemberRole PATCH /api/admin/user-groups/{id}/members/{uid}
func (h *GroupHandler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	userID, err := pathUint64(r, "uid")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	var req dto.UpdateMemberRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if err := h.groupSvc.UpdateMemberRole(r.Context(), groupID, userID, req.GroupRole); err != nil {
		if errors.Is(err, repository.ErrMemberNotFound) {
			response.Error(w, http.StatusNotFound, 40400, err.Error())
			return
		}
		response.Error(w, http.StatusBadRequest, 40000, err.Error())
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// RemoveMember DELETE /api/admin/user-groups/{id}/members/{uid}
func (h *GroupHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	userID, err := pathUint64(r, "uid")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	if err := h.groupSvc.RemoveMember(r.Context(), groupID, userID); err != nil {
		if errors.Is(err, repository.ErrMemberNotFound) {
			response.Error(w, http.StatusNotFound, 40400, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "移除失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// GetUserGroups GET /api/admin/users/{id}/groups
// 查询指定用户所属的分组列表。
func (h *GroupHandler) GetUserGroups(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	members, err := h.groupSvc.GetUserGroups(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	list := make([]dto.UserGroupsResp, len(members))
	for i, m := range members {
		list[i] = dto.UserGroupsResp{
			GroupID:   m.GroupID,
			GroupRole: m.GroupRole,
			JoinedAt:  m.CreatedAt.Format(isoFmt),
		}
	}
	response.JSON(w, http.StatusOK, list)
}

// ——— 组权限 ———

// ListGroupPermissions GET /api/admin/user-groups/{id}/permissions
func (h *GroupHandler) ListGroupPermissions(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	gps, err := h.groupSvc.ListGroupPermissions(r.Context(), groupID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	list := make([]dto.GroupPermissionResp, len(gps))
	for i, gp := range gps {
		list[i] = dto.GroupPermissionResp{
			ID:             gp.ID,
			GroupID:        gp.GroupID,
			PermissionCode: gp.PermissionCode,
			CreatedAt:      gp.CreatedAt.Format(isoFmt),
		}
	}
	response.JSON(w, http.StatusOK, list)
}

// AddGroupPermission POST /api/admin/user-groups/{id}/permissions
func (h *GroupHandler) AddGroupPermission(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	var req dto.AddGroupPermissionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.PermissionCode == "" {
		response.Error(w, http.StatusBadRequest, 40000, "permission_code 不能为空")
		return
	}
	if err := h.groupSvc.AddGroupPermission(r.Context(), groupID, req.PermissionCode); err != nil {
		if errors.Is(err, repository.ErrGroupPermissionExists) {
			response.Error(w, http.StatusConflict, 40900, err.Error())
			return
		}
		// D-62：权限码不存在时返回 400，而非 500
		if errors.Is(err, repository.ErrPermissionNotFound) {
			response.Error(w, http.StatusBadRequest, 40000, "权限码不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "添加失败")
		return
	}
	response.JSON(w, http.StatusCreated, nil)
}

// RemoveGroupPermission DELETE /api/admin/user-groups/{id}/permissions/{code}
func (h *GroupHandler) RemoveGroupPermission(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	permCode := r.PathValue("code")
	if permCode == "" {
		response.Error(w, http.StatusBadRequest, 40000, "权限码不能为空")
		return
	}
	if err := h.groupSvc.RemoveGroupPermission(r.Context(), groupID, permCode); err != nil {
		if errors.Is(err, repository.ErrPermissionNotFound) {
			// D-38：权限记录不存在，返回 404
			response.Error(w, http.StatusNotFound, 40400, "权限记录不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "移除失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// ListGroupRoles GET /api/admin/user-groups/{id}/roles
func (h *GroupHandler) ListGroupRoles(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	grs, err := h.groupSvc.ListGroupRoles(r.Context(), groupID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	list := make([]dto.GroupRoleResp, len(grs))
	for i, gr := range grs {
		list[i] = dto.GroupRoleResp{
			ID:        gr.ID,
			GroupID:   gr.GroupID,
			RoleID:    gr.RoleID,
			CreatedAt: gr.CreatedAt.Format(isoFmt),
		}
	}
	response.JSON(w, http.StatusOK, list)
}

// AddGroupRole POST /api/admin/user-groups/{id}/roles  body: {"role_id": 5}
func (h *GroupHandler) AddGroupRole(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	var req dto.AddGroupRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	if req.RoleID == 0 {
		response.Error(w, http.StatusBadRequest, 40000, "role_id 不能为空")
		return
	}
	if err := h.groupSvc.AddGroupRole(r.Context(), groupID, req.RoleID); err != nil {
		switch {
		case errors.Is(err, repository.ErrGroupRoleExists):
			response.Error(w, http.StatusConflict, 40900, err.Error())
		case errors.Is(err, repository.ErrGroupNotFound):
			response.Error(w, http.StatusNotFound, 40400, "分组不存在")
		case errors.Is(err, repository.ErrRoleNotFound):
			response.Error(w, http.StatusBadRequest, 40000, "角色不存在")
		case errors.Is(err, service.ErrCannotBindSystemRole):
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
		default:
			response.Error(w, http.StatusInternalServerError, 50000, "绑定失败")
		}
		return
	}
	response.JSON(w, http.StatusCreated, nil)
}

// RemoveGroupRole DELETE /api/admin/user-groups/{id}/roles/{role_id}
func (h *GroupHandler) RemoveGroupRole(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	roleID, err := pathUint64(r, "role_id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效角色 ID")
		return
	}
	if err := h.groupSvc.RemoveGroupRole(r.Context(), groupID, roleID); err != nil {
		if errors.Is(err, repository.ErrGroupRoleNotBound) {
			response.Error(w, http.StatusNotFound, 40400, "该角色未绑定到此分组")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "解绑失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// ——— 邀请码 ———

// ListInviteCodes GET /api/admin/user-groups/{id}/invite-codes
func (h *GroupHandler) ListInviteCodes(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	status := r.URL.Query().Get("status")
	p := pagination.Parse(r)
	codes, total, err := h.groupSvc.ListInviteCodesPaged(r.Context(), groupID, status, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	list := make([]dto.InviteCodeResp, len(codes))
	for i, ic := range codes {
		list[i] = inviteCodeToResp(ic)
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:   list,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// CreateInviteCode POST /api/admin/user-groups/{id}/invite-codes
func (h *GroupHandler) CreateInviteCode(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	var req dto.CreateInviteCodeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// D-69：max_uses 不能为负数（负值导致 used_count < max_uses 恒 false，邀请码永不可用）
	if req.MaxUses < 0 {
		response.Error(w, http.StatusBadRequest, 40000, "max_uses 不能为负数")
		return
	}
	// D-76：自定义邀请码长度上限校验
	if len(req.Code) > 64 {
		response.Error(w, http.StatusBadRequest, 40000, "邀请码长度不能超过 64 字符")
		return
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, parseErr := time.Parse(isoFmt, *req.ExpiresAt)
		if parseErr != nil {
			response.Error(w, http.StatusBadRequest, 40000, "expires_at 格式错误，需 ISO 8601")
			return
		}
		// D-72：expires_at 不能为过去时间，否则邀请码创建后立即失效
		if t.Before(time.Now()) {
			response.Error(w, http.StatusBadRequest, 40000, "expires_at 不能为过去时间")
			return
		}
		expiresAt = &t
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	ic, err := h.groupSvc.CreateInviteCode(r.Context(), groupID, req.Code, req.DefaultGroupRole, req.MaxUses, expiresAt, operatorID)
	if err != nil {
		if errors.Is(err, repository.ErrInviteCodeExists) {
			response.Error(w, http.StatusConflict, 40900, err.Error())
			return
		}
		// D-68 残留修复：default_group_role 枚举校验错误返回 400，而非 500
		if errors.Is(err, repository.ErrInvalidDefaultGroupRole) {
			response.Error(w, http.StatusBadRequest, 40000, err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "创建失败")
		return
	}
	response.JSON(w, http.StatusCreated, inviteCodeToResp(*ic))
}

// JoinGroup POST /api/user-groups/join
// 普通登录用户凭邀请码加入群组，无需 group:manage 权限。
func (h *GroupHandler) JoinGroup(w http.ResponseWriter, r *http.Request) {
	var req dto.JoinGroupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InviteCode == "" {
		response.Error(w, http.StatusBadRequest, 40000, "invite_code 不能为空")
		return
	}
	userID := middleware.UserIDFromContext(r.Context())
	groupID, groupRole, err := h.groupSvc.JoinByInviteCode(r.Context(), userID, req.InviteCode)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			// D-35：用户不存在（理论上已登录不应出现，作为防御性处理）
			response.Error(w, http.StatusNotFound, 40400, "用户不存在")
			return
		}
		if errors.Is(err, repository.ErrInviteCodeNotFound) {
			response.Error(w, http.StatusBadRequest, 40000, "邀请码无效或已过期")
			return
		}
		if errors.Is(err, repository.ErrInviteCodeFull) {
			// D-34：并发竞态被原子 UPDATE 拦住，邀请码已达使用上限
			response.Error(w, http.StatusConflict, 40901, "邀请码已达到使用上限")
			return
		}
		if errors.Is(err, repository.ErrMemberAlreadyExists) {
			response.Error(w, http.StatusConflict, 40900, "已是该群组成员")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "加入失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.JoinGroupResp{GroupID: groupID, GroupRole: groupRole})
}

// DisableInviteCode PATCH /api/admin/user-groups/{id}/invite-codes/{invite_id}/disable
func (h *GroupHandler) DisableInviteCode(w http.ResponseWriter, r *http.Request) {
	groupID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效分组 ID")
		return
	}
	inviteID, err := pathUint64(r, "invite_id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效邀请码 ID")
		return
	}
	if err := h.groupSvc.DisableInviteCode(r.Context(), groupID, inviteID); err != nil {
		if errors.Is(err, repository.ErrInviteCodeNotFound) {
			// D-38：邀请码不存在或不属于该分组，返回 404
			response.Error(w, http.StatusNotFound, 40400, "邀请码不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "操作失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// ——— 内部映射工具 ———

func groupToResp(g model.UserGroup) dto.GroupResp {
	return dto.GroupResp{
		ID:          g.ID,
		Code:        g.Code,
		Name:        g.Name,
		Type:        g.Type,
		IsDefault:   g.IsDefault,
		Description: g.Description,
		CreatedAt:   g.CreatedAt.Format(isoFmt),
	}
}

func inviteCodeToResp(ic model.GroupInviteCode) dto.InviteCodeResp {
	resp := dto.InviteCodeResp{
		ID:               ic.ID,
		Code:             ic.Code,
		GroupID:          ic.GroupID,
		DefaultGroupRole: ic.DefaultGroupRole,
		MaxUses:          ic.MaxUses,
		UsedCount:        ic.UsedCount,
		Status:           ic.Status,
		CreatedBy:        ic.CreatedBy,
		CreatedAt:        ic.CreatedAt.Format(isoFmt),
	}
	if ic.ExpiresAt != nil {
		s := ic.ExpiresAt.Format(isoFmt)
		resp.ExpiresAt = &s
	}
	return resp
}
