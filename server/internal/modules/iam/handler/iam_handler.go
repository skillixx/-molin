package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/middleware"
	"molin/server/internal/modules/iam/dto"
	"molin/server/internal/modules/iam/model"
	"molin/server/internal/modules/iam/repository"
	"molin/server/internal/modules/iam/service"
	"molin/server/pkg/pagination"
	"molin/server/pkg/response"
)

// errIsNotFound 判断错误是否为记录不存在（兼容 gorm.ErrRecordNotFound 和 repository.ErrRoleNotFound）。
func errIsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, repository.ErrRoleNotFound)
}

// PagedResp 通用分页响应结构，包含列表数据和分页元数据。
// D-95：分页元数据匿名嵌入 pagination.Result，使 page/page_size/total 与 items 同级（扁平结构），
// 符合 docs/full-api-design.md 1.2 节规范。
type PagedResp struct {
	List interface{} `json:"items"`
	pagination.Result
}

// IAMHandler 处理角色、权限、用户角色分配相关 HTTP 请求。
type IAMHandler struct {
	iamSvc *service.IAMService
}

func NewIAMHandler(iamSvc *service.IAMService) *IAMHandler {
	return &IAMHandler{iamSvc: iamSvc}
}

// ListRoles GET /api/admin/roles
// 支持 ?keyword= 关键字搜索（匹配 code 或 name）和分页参数 ?page=1&page_size=20。
func (h *IAMHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	p := pagination.Parse(r)
	roles, total, err := h.iamSvc.ListRolesPaged(r.Context(), keyword, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	list := make([]dto.RoleResp, len(roles))
	for i, role := range roles {
		list[i] = dto.RoleResp{ID: role.ID, Code: role.Code, Name: role.Name, Description: role.Description}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:   list,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// CreateRole POST /api/admin/roles
func (h *IAMHandler) CreateRole(w http.ResponseWriter, r *http.Request) {
	var req dto.RoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// D-57：code 和 name 为必填字段，不允许写入空值角色
	if req.Code == "" || req.Name == "" {
		response.Error(w, http.StatusBadRequest, 40000, "code 和 name 均为必填")
		return
	}
	// D-60：长度校验，防止超长输入触发 MySQL Data too long
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
	role := &model.Role{Code: req.Code, Name: req.Name, Description: req.Description}
	if err := h.iamSvc.CreateRole(r.Context(), role); err != nil {
		// D-57：code 重复时返回 409，而非 500
		if errors.Is(err, repository.ErrRoleCodeExists) {
			response.Error(w, http.StatusConflict, 40900, "角色 code 已存在")
			return
		}
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
	// D-65：code 字段不可修改，调用方若传入 code 直接拒绝，消除接口语义歧义
	if req.Code != "" {
		response.Error(w, http.StatusBadRequest, 40000, "角色 code 不可修改")
		return
	}
	// D-58：name 为空时拒绝更新，防止 GORM map 更新将 DB 中的非空 name 覆盖为空字符串
	if req.Name == "" {
		response.Error(w, http.StatusBadRequest, 40000, "name 不能为空")
		return
	}
	// D-60：长度校验，防止超长输入触发 MySQL Data too long
	if len(req.Name) > 128 {
		response.Error(w, http.StatusBadRequest, 40000, "name 长度不能超过 128 字符")
		return
	}
	if req.Description != nil && len(*req.Description) > 512 {
		response.Error(w, http.StatusBadRequest, 40000, "description 长度不能超过 512 字符")
		return
	}
	// 只允许更新 name 和 description，不包含 code
	updates := map[string]interface{}{"name": req.Name, "description": req.Description}
	if err := h.iamSvc.UpdateRole(r.Context(), id, updates); err != nil {
		// D-31：角色不存在时返回 404，而非 200 或 500
		if errIsNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "角色不存在")
			return
		}
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
		// D-31：角色不存在时返回 404，而非 200 或 500
		if errIsNotFound(err) {
			response.Error(w, http.StatusNotFound, 40400, "角色不存在")
			return
		}
		// D-61：角色下仍有用户时返回 409，拒绝删除
		if errors.Is(err, service.ErrRoleHasUsers) {
			response.Error(w, http.StatusConflict, 40900, "角色下仍有用户，无法删除")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// ListPermissions GET /api/admin/permissions
// 支持 ?keyword= 关键字搜索（匹配 code 或 name）和分页参数 ?page=1&page_size=20。
func (h *IAMHandler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	p := pagination.Parse(r)
	perms, total, err := h.iamSvc.ListPermissionsPaged(r.Context(), keyword, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	list := make([]dto.PermissionResp, len(perms))
	for i, perm := range perms {
		list[i] = dto.PermissionResp{ID: perm.ID, Code: perm.Code, Name: perm.Name, Resource: perm.Resource, Action: perm.Action}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:   list,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// GetUserRoles GET /api/admin/users/{id}/roles
// 返回用户已分配角色的详情（id、code、name、created_at），支持分页参数 ?page=1&page_size=20。
func (h *IAMHandler) GetUserRoles(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	p := pagination.Parse(r)
	roles, total, err := h.iamSvc.GetUserRolesPaged(r.Context(), userID, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	// 将 model.Role 映射为符合 API 规范的 DTO，确保返回小写 JSON 字段
	list := make([]dto.UserRoleResp, len(roles))
	for i, role := range roles {
		list[i] = dto.UserRoleResp{
			ID:          role.ID,
			Code:        role.Code,
			Name:        role.Name,
			Description: role.Description,
			CreatedAt:   role.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:   list,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
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
	if err := h.iamSvc.AssignRole(r.Context(), userID, req.RoleID, operatorID, req.Reason, r.RemoteAddr); err != nil {
		// 重复分配：该用户已拥有此角色，返回 409 Conflict
		if errors.Is(err, repository.ErrUserRoleExists) {
			response.Error(w, http.StatusConflict, 40900, "该用户已拥有此角色")
			return
		}
		// D-26：role_id 不存在时返回 400（gorm.ErrRecordNotFound）
		if errIsNotFound(err) {
			response.Error(w, http.StatusBadRequest, 40000, "角色不存在")
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
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.iamSvc.RevokeRole(r.Context(), userID, roleID, operatorID, r.RemoteAddr); err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "撤销失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// GetPermissionOverrides GET /api/admin/users/{id}/permission-overrides
// 支持分页参数 ?page=1&page_size=20，不传则使用默认值。
// 支持过滤参数：?effect=allow|deny（空字符串查全部）、?permission_code=xxx（空字符串查全部）。
func (h *IAMHandler) GetPermissionOverrides(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	// 读取过滤参数
	effect := r.URL.Query().Get("effect")
	permCode := r.URL.Query().Get("permission_code")
	// effect 不为空时做枚举校验，防止非标准值
	if effect != "" && effect != "allow" && effect != "deny" {
		response.Error(w, http.StatusBadRequest, 40000, "effect 只能为 allow 或 deny")
		return
	}
	// 解析分页参数，默认 page=1 page_size=20，最大 page_size=100
	p := pagination.Parse(r)
	overrides, total, err := h.iamSvc.GetPermissionOverridesPaged(r.Context(), userID, effect, permCode, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	// 将 model.UserPermissionOverride 映射为 DTO，确保响应字段名符合 snake_case 规范
	const isoLayout = "2006-01-02T15:04:05Z07:00"
	list := make([]dto.OverrideResp, len(overrides))
	for i, o := range overrides {
		item := dto.OverrideResp{
			ID:             o.ID,
			UserID:         o.UserID,
			PermissionID:   o.PermissionID,
			PermissionCode: o.PermissionCode,
			Effect:         o.Effect,
			Reason:         o.Reason,
			CreatedBy:      o.CreatedBy,
			CreatedAt:      o.CreatedAt.Format(isoLayout),
		}
		if o.ExpiresAt != nil {
			s := o.ExpiresAt.Format(isoLayout)
			item.ExpiresAt = &s
		}
		list[i] = item
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:   list,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
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
	// D-28：用 FindPermissionByID 单条精确查询，替代原来对全量权限列表的 O(n) 遍历
	perm, err := h.iamSvc.FindPermissionByID(r.Context(), req.PermissionID)
	if err != nil {
		// permission_id 不存在，返回 400
		response.Error(w, http.StatusBadRequest, 40000, "permission_id 不存在")
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	override := &model.UserPermissionOverride{
		UserID:         userID,
		PermissionID:   req.PermissionID,
		PermissionCode: perm.Code,
		Effect:         req.Effect,
		Reason:         req.Reason,
		CreatedBy:      &operatorID,
	}
	// D-25：service 层改用 Upsert，当唯一键冲突时更新记录，不再返回 500
	if err := h.iamSvc.SetPermissionOverride(r.Context(), override, operatorID, r.RemoteAddr); err != nil {
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
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.iamSvc.DeletePermissionOverride(r.Context(), overrideID, userID, operatorID, r.RemoteAddr); err != nil {
		// D-27：记录不存在或越权（override 不属于该用户）时返回 404
		if errors.Is(err, service.ErrOverrideNotFound) {
			response.Error(w, http.StatusNotFound, 40400, "权限覆盖记录不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "删除失败")
		return
	}
	response.JSON(w, http.StatusOK, nil)
}

// GetRole GET /api/admin/roles/{id}
// BUG-04 修复：该路由此前未注册，请求落到其他路由返回 405。
// handler 逻辑：通过 id 查角色，不存在返回 404。
func (h *IAMHandler) GetRole(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	role, err := h.iamSvc.GetRoleByID(r.Context(), id)
	if err != nil {
		response.Error(w, http.StatusNotFound, 40400, "角色不存在")
		return
	}
	response.JSON(w, http.StatusOK, dto.RoleResp{
		ID:          role.ID,
		Code:        role.Code,
		Name:        role.Name,
		Description: role.Description,
	})
}

// GetRolePermissions GET /api/admin/roles/{id}/permissions
// A-11：返回指定角色当前拥有的权限码列表。角色不存在返回 404。
func (h *IAMHandler) GetRolePermissions(w http.ResponseWriter, r *http.Request) {
	id, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效 ID")
		return
	}
	if _, err := h.iamSvc.GetRoleByID(r.Context(), id); err != nil {
		response.Error(w, http.StatusNotFound, 40400, "角色不存在")
		return
	}
	codes, err := h.iamSvc.GetRolePermissionCodes(r.Context(), id)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	response.JSON(w, http.StatusOK, dto.PermissionsResp{Permissions: codes})
}

// GetUserEffectivePermissions GET /api/admin/users/{id}/effective-permissions
// A-12：返回指定用户最终生效的权限码（角色权限 ∪ 组权限，叠加 user_permission_overrides
// 的 allow/deny 调整），并附带实际生效（未过期）的 overrides 调整明细。
func (h *IAMHandler) GetUserEffectivePermissions(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	codes, err := h.iamSvc.GetEffectivePermissionCodes(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	overrides, err := h.iamSvc.GetEffectiveOverrides(r.Context(), userID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	overrideList := make([]dto.EffectiveOverrideResp, len(overrides))
	for i, o := range overrides {
		overrideList[i] = dto.EffectiveOverrideResp{Code: o.PermissionCode, Effect: o.Effect}
	}
	response.JSON(w, http.StatusOK, dto.EffectivePermissionsResp{
		Permissions: codes,
		Overrides:   overrideList,
	})
}

// ListAuditLogs GET /api/admin/audit-logs
// BUG-05 修复：该路由此前未注册，请求返回 404。
// 支持 ?module=&action=&operator_id=&start_time=&end_time=&page=&page_size= 参数。
// D-82：新增 operator_id（int64）、start_time/end_time（RFC3339）过滤参数，合规必备。
// D-83：接口权限已改为 RequirePerm("audit:read")，不再与 role:manage 共用。
func (h *IAMHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	module := r.URL.Query().Get("module")
	action := r.URL.Query().Get("action")

	// D-82：解析 operator_id 过滤参数，0 表示不过滤
	var operatorID uint64
	if raw := r.URL.Query().Get("operator_id"); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "operator_id 必须为正整数")
			return
		}
		operatorID = v
	}
	// D-82：解析时间范围过滤参数（RFC3339 格式，如 2026-01-01T00:00:00Z）
	var startTime, endTime time.Time
	if raw := r.URL.Query().Get("start_time"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "start_time 格式不正确，请使用 RFC3339（如 2026-01-01T00:00:00Z）")
			return
		}
		startTime = t
	}
	if raw := r.URL.Query().Get("end_time"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.Error(w, http.StatusBadRequest, 40000, "end_time 格式不正确，请使用 RFC3339（如 2026-12-31T23:59:59Z）")
			return
		}
		endTime = t
	}

	p := pagination.Parse(r)
	logs, total, err := h.iamSvc.ListAuditLogs(r.Context(), module, action, operatorID, startTime, endTime, p.Offset(), p.PageSize)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, 50000, "查询失败")
		return
	}
	const isoLayout = "2006-01-02T15:04:05Z07:00"
	list := make([]map[string]interface{}, len(logs))
	for i, l := range logs {
		item := map[string]interface{}{
			"id":          l.ID,
			"operator_id": l.OperatorID,
			"module":      l.Module,
			"action":      l.Action,
			"target_type": l.TargetType,
			"target_id":   l.TargetID,
			"ip":          l.IP,
			"created_at":  l.CreatedAt.Format(isoLayout),
		}
		// request_summary 存储为 JSON 字符串，反序列化为对象/数组返回，避免被转义成字符串
		if l.RequestSummary == nil {
			item["request_summary"] = nil
		} else {
			var summary interface{}
			if err := json.Unmarshal([]byte(*l.RequestSummary), &summary); err != nil {
				// 反序列化失败时兜底返回原始字符串，不中断整个列表的响应
				item["request_summary"] = *l.RequestSummary
			} else {
				item["request_summary"] = summary
			}
		}
		list[i] = item
	}
	response.JSON(w, http.StatusOK, PagedResp{
		List:   list,
		Result: pagination.Result{Page: p.Page, PageSize: p.PageSize, Total: total},
	})
}

// CreatePermission POST /api/admin/permissions
// 创建权限码，code/name/resource/action 均为必填。
func (h *IAMHandler) CreatePermission(w http.ResponseWriter, r *http.Request) {
	var req dto.PermissionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// 校验必填字段
	if req.Code == "" || req.Name == "" || req.Resource == "" || req.Action == "" {
		response.Error(w, http.StatusBadRequest, 40000, "code/name/resource/action 均为必填")
		return
	}
	perm := &model.Permission{
		Code:     req.Code,
		Name:     req.Name,
		Resource: req.Resource,
		Action:   req.Action,
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.iamSvc.CreatePermission(r.Context(), perm, operatorID, r.RemoteAddr); err != nil {
		// 权限码已存在：返回 409 Conflict
		if errors.Is(err, repository.ErrPermissionCodeExists) {
			response.Error(w, http.StatusConflict, 40900, "权限码已存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "创建失败")
		return
	}
	response.JSON(w, http.StatusCreated, dto.PermissionResp{
		ID:       perm.ID,
		Code:     perm.Code,
		Name:     perm.Name,
		Resource: perm.Resource,
		Action:   perm.Action,
	})
}

// SetRolePermissions PATCH /api/admin/roles/{id}/permissions
// 全量配置角色的权限集合，permission_ids 为空数组表示清空该角色权限。
func (h *IAMHandler) SetRolePermissions(w http.ResponseWriter, r *http.Request) {
	roleID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效角色 ID")
		return
	}
	var req dto.SetRolePermissionsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// D-63：permission_ids 数量上限 200，防止万条输入耗尽 DB 连接
	if len(req.PermissionIDs) > 200 {
		response.Error(w, http.StatusBadRequest, 40000, "最多支持 200 条")
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.iamSvc.SetRolePermissions(r.Context(), roleID, req.PermissionIDs, operatorID, r.RemoteAddr); err != nil {
		// D-26：role_id 或 permission_id 不存在时返回 400
		if errIsNotFound(err) {
			response.Error(w, http.StatusBadRequest, 40000, "角色或权限不存在")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "设置失败")
		return
	}
	response.JSON(w, http.StatusOK, "updated")
}

// ReplaceUserRoles PATCH /api/admin/users/{id}/roles
// 全量替换用户的角色集合，role_ids 为空数组表示清空该用户所有角色。
func (h *IAMHandler) ReplaceUserRoles(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	var req dto.ReplaceUserRolesReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// D-63：role_ids 数量上限 200，防止万条输入耗尽 DB 连接
	if len(req.RoleIDs) > 200 {
		response.Error(w, http.StatusBadRequest, 40000, "最多支持 200 条")
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.iamSvc.ReplaceUserRoles(r.Context(), userID, req.RoleIDs, operatorID, req.Reason, r.RemoteAddr); err != nil {
		// D-59：roleID 不存在时返回 400
		if errIsNotFound(err) {
			response.Error(w, http.StatusBadRequest, 40000, "包含不存在的角色 ID")
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, "updated")
}

// ReplaceUserOverrides PATCH /api/admin/users/{id}/permission-overrides
// 全量替换用户的权限覆盖集合，items 为空数组表示清空该用户所有权限覆盖。
func (h *IAMHandler) ReplaceUserOverrides(w http.ResponseWriter, r *http.Request) {
	userID, err := pathUint64(r, "id")
	if err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "无效用户 ID")
		return
	}
	var req dto.ReplaceOverridesReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, 40000, "请求参数错误")
		return
	}
	// D-63：items 数量上限 200，防止万条输入耗尽 DB 连接
	if len(req.Items) > 200 {
		response.Error(w, http.StatusBadRequest, 40000, "最多支持 200 条")
		return
	}
	operatorID := middleware.UserIDFromContext(r.Context())
	if err := h.iamSvc.ReplaceUserOverrides(r.Context(), userID, req.Items, operatorID, r.RemoteAddr); err != nil {
		// effect 不合法、expires_at 格式不合法、permission_id 不存在均返回 400
		if errors.Is(err, service.ErrInvalidEffect) || errors.Is(err, service.ErrInvalidExpiresAt) || errors.Is(err, gorm.ErrRecordNotFound) {
			response.Error(w, http.StatusBadRequest, 40000, "请求参数错误："+err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, 50000, "更新失败")
		return
	}
	response.JSON(w, http.StatusOK, "updated")
}

// pathUint64 从 Go 1.22 路由 PathValue 中解析 uint64 参数。
func pathUint64(r *http.Request, key string) (uint64, error) {
	return strconv.ParseUint(r.PathValue(key), 10, 64)
}
