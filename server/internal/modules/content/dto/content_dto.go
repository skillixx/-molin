package dto

import "time"

// CreateAnnouncementReq 创建公告请求。
type CreateAnnouncementReq struct {
	Title           string     `json:"title"`
	Content         string     `json:"content"`
	VisibleScope    string     `json:"visible_scope"`      // all/roles/members/admins
	TargetRolesJSON *string    `json:"target_roles_json"`  // JSON 字符串数组，仅 visible_scope=roles 时生效，例如 ["merchant","vip"]
	StartAt         *time.Time `json:"start_at"`
	EndAt           *time.Time `json:"end_at"`
	SortOrder       int        `json:"sort_order"`
}

// UpdateAnnouncementReq 修改公告请求（含发布/下线）。
type UpdateAnnouncementReq struct {
	Title           *string    `json:"title"`
	Content         *string    `json:"content"`
	VisibleScope    *string    `json:"visible_scope"`     // all/roles/members/admins
	TargetRolesJSON *string    `json:"target_roles_json"` // JSON 字符串数组，仅 visible_scope=roles 时生效
	Status          *string    `json:"status"`            // published/offline/draft
	StartAt         *time.Time `json:"start_at"`
	EndAt           *time.Time `json:"end_at"`
	SortOrder       *int       `json:"sort_order"`
}

// CreateCategoryReq 创建帮助分类请求。
type CreateCategoryReq struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	SortOrder   int     `json:"sort_order"`
}

// UpdateCategoryReq 修改帮助分类请求。
type UpdateCategoryReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	SortOrder   *int    `json:"sort_order"`
	Status      *string `json:"status"`
}

// CreateArticleReq 创建帮助文章请求。
type CreateArticleReq struct {
	CategoryID uint64 `json:"category_id"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	SortOrder  int    `json:"sort_order"`
}

// UpdateArticleReq 修改帮助文章请求。
type UpdateArticleReq struct {
	CategoryID *uint64 `json:"category_id"`
	Title      *string `json:"title"`
	Content    *string `json:"content"`
	SortOrder  *int    `json:"sort_order"`
	Status     *string `json:"status"` // published/draft
}
