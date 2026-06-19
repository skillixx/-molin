package model

import "time"

// UserGroup 用户分组，权限/应用的容器。
// type: region(区域) / org(机构) / custom(自定义)
// IsDefault: 无邀请码注册时的兜底组，全局最多一个。
type UserGroup struct {
	ID          uint64     `gorm:"primaryKey;autoIncrement"`
	Code        string     `gorm:"uniqueIndex;size:128;not null"`
	Name        string     `gorm:"size:128;not null"`
	Type        string     `gorm:"size:32;default:custom"`
	ParentID    *uint64    `gorm:"index"` // 预留层级，当前阶段不使用
	IsDefault   bool       `gorm:"default:false;index"`
	Description *string    `gorm:"size:512"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UserGroupMember 用户与分组的成员关系（多对多）。
// GroupRole: admin(组管理员，可管理本组用户) / member(普通组员)
// 一个用户可在多个组且各组身份不同。
type UserGroupMember struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	UserID    uint64    `gorm:"not null;uniqueIndex:uk_user_group_members"`
	GroupID   uint64    `gorm:"not null;uniqueIndex:uk_user_group_members;index:idx_ugm_group_role"`
	GroupRole string    `gorm:"size:32;default:member;index:idx_ugm_group_role"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GroupPermission 组权限，组员加入后继承。
// PermissionCode 复用全局权限码体系，应用访问用 app:use:xxx 表达。
type GroupPermission struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement"`
	GroupID        uint64    `gorm:"not null;uniqueIndex:uk_group_permissions"`
	PermissionCode string    `gorm:"size:191;not null;uniqueIndex:uk_group_permissions;index"`
	CreatedAt      time.Time
}

// GroupRole 组绑定的全局角色（多对多）。组员继承所在组绑定的角色。
// 用途：商品访问/定价授权（GetUserRoleIDs 合并组角色）。
// A 版约束：组角色只参与商品访问，不进入权限码判定（CheckPermission 不读组角色）。
type GroupRole struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	GroupID   uint64    `gorm:"not null;uniqueIndex:uk_group_roles"`
	RoleID    uint64    `gorm:"not null;uniqueIndex:uk_group_roles;index:idx_group_roles_role_id"`
	CreatedAt time.Time
}

// GroupInviteCode 组邀请码/注册渠道，注册时按码落到对应组并赋默认组内角色。
// MaxUses: 0 表示不限次数；Status: active / disabled
type GroupInviteCode struct {
	ID               uint64     `gorm:"primaryKey;autoIncrement"`
	Code             string     `gorm:"uniqueIndex;size:64;not null"`
	GroupID          uint64     `gorm:"not null;index"`
	DefaultGroupRole string     `gorm:"size:32;default:member"`
	MaxUses          int        `gorm:"default:0"`
	UsedCount        int        `gorm:"default:0"`
	ExpiresAt        *time.Time
	Status           string     `gorm:"size:32;default:active;index"`
	CreatedBy        *uint64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
