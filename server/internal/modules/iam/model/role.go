package model

import "time"

// Role 角色表。
type Role struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement"`
	Code        string    `gorm:"uniqueIndex;size:128;not null"`
	Name        string    `gorm:"size:128;not null"`
	Description *string   `gorm:"size:512"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Permission 权限表，格式：resource:action（如 user:list）。
type Permission struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	Code      string    `gorm:"uniqueIndex;size:191;not null"`
	Name      string    `gorm:"size:128;not null"`
	Resource  string    `gorm:"size:128;not null"`
	Action    string    `gorm:"size:64;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserRole 用户角色关联表。
type UserRole struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	UserID    uint64    `gorm:"not null;uniqueIndex:uk_user_roles"`
	RoleID    uint64    `gorm:"not null;uniqueIndex:uk_user_roles;index"`
	CreatedAt time.Time
}

// RolePermission 角色权限关联表。
type RolePermission struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement"`
	RoleID       uint64    `gorm:"not null;uniqueIndex:uk_role_permissions"`
	PermissionID uint64    `gorm:"not null;uniqueIndex:uk_role_permissions"`
	CreatedAt    time.Time
}

// UserPermissionOverride 用户级权限覆盖（allow/deny，优先级高于角色）。
type UserPermissionOverride struct {
	ID             uint64     `gorm:"primaryKey;autoIncrement"`
	UserID         uint64     `gorm:"not null;uniqueIndex:uk_user_perm_override"`
	PermissionID   uint64     `gorm:"not null;uniqueIndex:uk_user_perm_override"`
	PermissionCode string     `gorm:"size:191;not null"` // 冗余存储，查权限时避免 JOIN
	Effect         string     `gorm:"size:16;not null"`  // allow / deny
	Reason         *string    `gorm:"size:512"`
	ExpiresAt      *time.Time
	CreatedBy      *uint64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// RoleChangeLog 角色变更审计日志。
type RoleChangeLog struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	UserID     uint64    `gorm:"not null;index"`
	RoleID     uint64    `gorm:"not null"`
	Action     string    `gorm:"size:32;not null"` // assign / revoke
	OperatorID *uint64
	Reason     *string   `gorm:"size:512"`
	CreatedAt  time.Time
}
