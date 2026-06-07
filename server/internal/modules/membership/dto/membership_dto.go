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

// MembershipResponse 用户会员响应。
type MembershipResponse struct {
	ID        uint64     `json:"id"`
	UserID    uint64     `json:"user_id"`
	LevelID   uint64     `json:"level_id"`
	AssetID   *uint64    `json:"asset_id,omitempty"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
