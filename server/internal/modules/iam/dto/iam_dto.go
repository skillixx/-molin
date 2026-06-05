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
