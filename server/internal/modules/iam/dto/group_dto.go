package dto

// ——— 分组请求 ———

// CreateGroupReq 创建分组请求。
type CreateGroupReq struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`        // region / org / custom（默认 custom）
	IsDefault   bool    `json:"is_default"`
	Description *string `json:"description,omitempty"`
}

// UpdateGroupReq 更新分组请求（仅 name/type/description/is_default 可改，code 不可改）。
type UpdateGroupReq struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	IsDefault   bool    `json:"is_default"`
	Description *string `json:"description,omitempty"`
}

// AddMemberReq 加成员请求。
type AddMemberReq struct {
	UserID    uint64 `json:"user_id"`
	GroupRole string `json:"group_role"` // admin / member（默认 member）
}

// UpdateMemberRoleReq 修改成员组内角色请求。
type UpdateMemberRoleReq struct {
	GroupRole string `json:"group_role"` // admin / member
}

// AddGroupPermissionReq 给分组添加权限码请求。
type AddGroupPermissionReq struct {
	PermissionCode string `json:"permission_code"`
}

// CreateInviteCodeReq 生成邀请码请求。
// Code 为空时自动生成 8 字符随机码。
type CreateInviteCodeReq struct {
	Code             string  `json:"code"`
	DefaultGroupRole string  `json:"default_group_role"` // admin / member（默认 member）
	MaxUses          int     `json:"max_uses"`           // 0 = 不限次数
	ExpiresAt        *string `json:"expires_at"`         // ISO 8601，null = 永不过期
}

// ——— 分组响应 ———

// GroupResp 分组响应 DTO。
type GroupResp struct {
	ID          uint64  `json:"id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	IsDefault   bool    `json:"is_default"`
	Description *string `json:"description,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// GroupMemberResp 分组成员响应 DTO。
type GroupMemberResp struct {
	ID        uint64 `json:"id"`
	UserID    uint64 `json:"user_id"`
	GroupID   uint64 `json:"group_id"`
	GroupRole string `json:"group_role"`
	CreatedAt string `json:"created_at"`
}

// GroupPermissionResp 分组权限响应 DTO。
type GroupPermissionResp struct {
	ID             uint64 `json:"id"`
	GroupID        uint64 `json:"group_id"`
	PermissionCode string `json:"permission_code"`
	CreatedAt      string `json:"created_at"`
}

// InviteCodeResp 邀请码响应 DTO。
type InviteCodeResp struct {
	ID               uint64  `json:"id"`
	Code             string  `json:"code"`
	GroupID          uint64  `json:"group_id"`
	DefaultGroupRole string  `json:"default_group_role"`
	MaxUses          int     `json:"max_uses"`
	UsedCount        int     `json:"used_count"`
	ExpiresAt        *string `json:"expires_at,omitempty"`
	Status           string  `json:"status"`
	CreatedBy        *uint64 `json:"created_by,omitempty"`
	CreatedAt        string  `json:"created_at"`
}

// UserGroupsResp 某用户所在分组列表（含组内角色）响应 DTO。
type UserGroupsResp struct {
	GroupID   uint64 `json:"group_id"`
	GroupRole string `json:"group_role"`
	JoinedAt  string `json:"joined_at"`
}
