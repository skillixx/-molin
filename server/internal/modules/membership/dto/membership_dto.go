package dto

import "time"

// CreateLevelReq 创建会员等级请求。
type CreateLevelReq struct {
	LevelCode   string  `json:"level_code"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	SortOrder   int     `json:"sort_order"`
}

// UpdateLevelReq 修改会员等级请求。
type UpdateLevelReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	SortOrder   *int    `json:"sort_order"`
	Status      *string `json:"status"`
}

// CreateBenefitReq 创建权益请求。
type CreateBenefitReq struct {
	LevelID      uint64 `json:"level_id"`
	BenefitType  string `json:"benefit_type"`
	BenefitValue string `json:"benefit_value"` // JSON 字符串
}

// UpdateBenefitReq 修改权益请求。
type UpdateBenefitReq struct {
	BenefitType  *string `json:"benefit_type"`
	BenefitValue *string `json:"benefit_value"`
	Status       *string `json:"status"`
}

// GrantMembershipReq 管理端手动开通/续期用户会员请求。
type GrantMembershipReq struct {
	UserID       uint64 `json:"user_id"`
	LevelID      uint64 `json:"level_id"`
	DurationDays *int   `json:"duration_days"` // nil 表示永久会员
}

// UpdateUserMembershipReq 管理端调整/取消用户会员请求。
type UpdateUserMembershipReq struct {
	Action    *string    `json:"action"`     // cancel：取消会员（status→cancelled）
	ExpiresAt *time.Time `json:"expires_at"` // 可选：直接覆盖到期时间
}

// MembershipResponse 用户会员响应。
// 纯增量：在原有字段基础上内联 level_code / level_name，前端可直接展示等级名，
// 无需再按 level_id 映射等级列表。level_id 等原字段保持不变。
type MembershipResponse struct {
	ID        uint64     `json:"id"`
	UserID    uint64     `json:"user_id"`
	LevelID   uint64     `json:"level_id"`
	LevelCode string     `json:"level_code"`
	LevelName string     `json:"level_name"`
	AssetID   *uint64    `json:"asset_id,omitempty"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// AdminUserMembershipResponse 管理端用户会员列表单项响应。
// = 原 user_memberships 全部字段 + 内联 level_code / level_name。
// 不含用户名/邮箱（属后端甲用户域，本轮不做）。
type AdminUserMembershipResponse struct {
	ID        uint64     `json:"id"`
	UserID    uint64     `json:"user_id"`
	LevelID   uint64     `json:"level_id"`
	LevelCode string     `json:"level_code"`
	LevelName string     `json:"level_name"`
	AssetID   *uint64    `json:"asset_id,omitempty"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}
