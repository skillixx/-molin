package model

import "time"

// Announcement 公告，对应 announcements 表。
// visible_scope 取值规范（与 content/CLAUDE.md 保持一致）：
//   all     —— 所有用户可见
//   roles   —— 按 target_roles_json（JSON 字符串数组，存角色 code）匹配用户角色，命中任意一个即可见
//   members —— 仅对拥有有效会员的用户可见
//   admins  —— 管理端专属，用户端永不展示
type Announcement struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Title           string     `gorm:"size:512;not null" json:"title"`
	Content         string     `gorm:"type:text;not null" json:"content"`
	VisibleScope    string     `gorm:"size:32;not null;default:all" json:"visible_scope"` // all/roles/members/admins
	TargetRolesJSON *string    `gorm:"type:json" json:"target_roles_json,omitempty"`      // JSON 数组，存角色 code，仅 visible_scope=roles 时生效
	Status          string     `gorm:"size:32;not null;default:draft;index:idx_announcements_status" json:"status"` // draft/published/offline
	StartAt         *time.Time `gorm:"index:idx_announcements_start_at" json:"start_at,omitempty"`
	EndAt           *time.Time `json:"end_at,omitempty"`
	SortOrder       int        `gorm:"not null;default:0" json:"sort_order"`
	CreatedBy       uint64     `gorm:"not null" json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (Announcement) TableName() string { return "announcements" }

// HelpCategory 帮助文档分类，对应 help_categories 表。
type HelpCategory struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"size:191;not null" json:"name"`
	Description *string   `gorm:"type:text" json:"description,omitempty"`
	SortOrder   int       `gorm:"not null;default:0" json:"sort_order"`
	Status      string    `gorm:"size:32;not null;default:active" json:"status"` // active/inactive
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (HelpCategory) TableName() string { return "help_categories" }

// HelpArticle 帮助文章，对应 help_articles 表。
type HelpArticle struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	CategoryID uint64    `gorm:"not null;index:idx_help_articles_category_id" json:"category_id"`
	Title      string    `gorm:"size:512;not null" json:"title"`
	Content    string    `gorm:"type:longtext;not null" json:"content"`
	SortOrder  int       `gorm:"not null;default:0" json:"sort_order"`
	Status     string    `gorm:"size:32;not null;default:draft;index:idx_help_articles_status" json:"status"` // draft/published
	CreatedBy  uint64    `gorm:"not null" json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (HelpArticle) TableName() string { return "help_articles" }
