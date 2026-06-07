package model

import "time"

// MembershipLevel 会员等级，对应 membership_levels 表。
type MembershipLevel struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	LevelCode   string    `gorm:"size:64;not null;uniqueIndex:uk_membership_levels_code" json:"level_code"`
	Name        string    `gorm:"size:191;not null" json:"name"`
	Description *string   `gorm:"type:text" json:"description,omitempty"`
	SortOrder   int       `gorm:"not null;default:0" json:"sort_order"`
	Status      string    `gorm:"size:32;not null;default:active" json:"status"` // active/inactive
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (MembershipLevel) TableName() string { return "membership_levels" }

// MembershipBenefit 会员权益，对应 membership_benefits 表。
type MembershipBenefit struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	LevelID      uint64    `gorm:"not null;index:idx_membership_benefits_level_id" json:"level_id"`
	BenefitType  string    `gorm:"size:64;not null" json:"benefit_type"`
	BenefitValue string    `gorm:"type:json;not null" json:"benefit_value"` // JSON 字符串
	Status       string    `gorm:"size:32;not null;default:active" json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (MembershipBenefit) TableName() string { return "membership_benefits" }

// UserMembership 用户会员记录，对应 user_memberships 表。
// 查询有效会员：status = active AND (expires_at IS NULL OR expires_at > NOW())
type UserMembership struct {
	ID        uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint64     `gorm:"not null;index:idx_user_memberships_user_id" json:"user_id"`
	LevelID   uint64     `gorm:"not null" json:"level_id"`
	AssetID   *uint64    `json:"asset_id,omitempty"`
	Status    string     `gorm:"size:32;not null;default:active;index:idx_user_memberships_status" json:"status"` // active/expired/cancelled
	StartedAt time.Time  `gorm:"not null" json:"started_at"`
	ExpiresAt *time.Time `gorm:"index:idx_user_memberships_expires_at" json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (UserMembership) TableName() string { return "user_memberships" }
