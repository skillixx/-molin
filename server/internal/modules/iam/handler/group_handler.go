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
		List:       list,
		Pagination: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
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
		response.Error(w, http.StatusNotFound, 40400, "分组不存在")
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
	groupType := req.Type
	if groupType == "" {
		groupType = "custom"
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
	updates := map[string]interface{}{
		"name":        req.Name,
		"type":        req.Type,
		"description": req.Description,
		"is_default":  req.IsDefault,
	}
	if err := h.groupSvc.UpdateGroup(r.Context(), id, updates); err != nil {
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
		List:       list,
		Pagination: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
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
		response.Error(w, http.StatusInternalServerError, 50000, "移除失败")
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
		List:       list,
		Pagination: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
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
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, parseErr := time.Parse(isoFmt, *req.ExpiresAt)
		if parseErr != nil {
			response.Error(w, http.StatusBadRequest, 40000, "expires_at 格式错误，需 ISO 8601")
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
		response.Error(w, http.StatusInternalServerError, 50000, "创建失败")
		return
	}
	response.JSON(w, http.StatusCreated, inviteCodeToResp(*ic))
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
