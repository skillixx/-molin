package dto

// RoleReq 创建/更新角色请求。
type RoleReq struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// RoleResp 角色响应。
type RoleResp struct {
	ID          uint64  `json:"id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// PermissionResp 权限响应。
type PermissionResp struct {
	ID       uint64 `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// AssignRoleReq 分配/撤销角色请求。
type AssignRoleReq struct {
	RoleID uint64  `json:"role_id"`
	Reason *string `json:"reason,omitempty"`
}

// OverrideReq 设置用户权限覆盖请求。
type OverrideReq struct {
	PermissionID uint64  `json:"permission_id"`
	Effect       string  `json:"effect"` // allow / deny
	Reason       *string `json:"reason,omitempty"`
}

// UserRoleResp 用户已分配角色的响应 DTO（GET /api/admin/users/{id}/roles）。
// 返回角色详情而非关联表原始字段，符合 API 规范。
type UserRoleResp struct {
	ID          uint64  `json:"id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// OverrideResp 用户权限覆盖响应 DTO（GET /api/admin/users/{id}/permission-overrides）。
// 所有字段均使用 snake_case JSON tag，避免直接序列化 model 导致 PascalCase 输出。
type OverrideResp struct {
	ID             uint64  `json:"id"`
	UserID         uint64  `json:"user_id"`
	PermissionID   uint64  `json:"permission_id"`
	PermissionCode string  `json:"permission_code"`
	Effect         string  `json:"effect"`
	Reason         *string `json:"reason,omitempty"`
	ExpiresAt      *string `json:"expires_at,omitempty"` // ISO 8601，nil 时忽略
	CreatedBy      *uint64 `json:"created_by,omitempty"`
	CreatedAt      string  `json:"created_at"` // ISO 8601
}

// PermissionReq 创建权限请求。
type PermissionReq struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// SetRolePermissionsReq 配置角色权限请求（全量替换）。
type SetRolePermissionsReq struct {
	PermissionIDs []uint64 `json:"permission_ids"`
}

// ReplaceUserRolesReq 批量替换用户角色请求（全量替换）。
type ReplaceUserRolesReq struct {
	RoleIDs []uint64 `json:"role_ids"`
	Reason  *string  `json:"reason,omitempty"`
}

// OverrideItemReq 批量替换用户权限覆盖时的单项内容。
type OverrideItemReq struct {
	PermissionID uint64  `json:"permission_id"`
	Effect       string  `json:"effect"` // allow / deny
	Reason       *string `json:"reason,omitempty"`
	ExpiresAt    *string `json:"expires_at,omitempty"` // ISO 8601
}

// ReplaceOverridesReq 批量替换用户权限覆盖请求（全量替换）。
type ReplaceOverridesReq struct {
	Items []OverrideItemReq `json:"items"`
}

// PermissionsResp 权限码列表响应 DTO。
// A-10：GET /api/me/permissions；A-11：GET /api/admin/roles/{id}/permissions。
type PermissionsResp struct {
	Permissions []string `json:"permissions"`
}

// EffectiveOverrideResp 用户最终生效的权限覆盖明细（仅列出未过期的有效记录）。
// A-12：GET /api/admin/users/{id}/effective-permissions 响应中的 overrides 字段。
type EffectiveOverrideResp struct {
	Code   string `json:"code"`
	Effect string `json:"effect"` // allow / deny
}

// EffectivePermissionsResp 用户最终生效权限码及 overrides 调整明细响应 DTO。
// A-12：GET /api/admin/users/{id}/effective-permissions。
type EffectivePermissionsResp struct {
	Permissions []string                `json:"permissions"`
	Overrides   []EffectiveOverrideResp `json:"overrides"`
}
