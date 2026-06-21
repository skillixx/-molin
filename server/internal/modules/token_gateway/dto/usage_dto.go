package dto

import (
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

// UserUsageResp 用户端「我的用量」单条流水（§14.3）。
// 精简视图：不回 user_id / api_key_id 等内部字段。
type UserUsageResp struct {
	RequestID        string          `json:"request_id"`
	LogicalModelCode string          `json:"logical_model_code"`
	Modality         string          `json:"modality"`
	InputTokens      int64           `json:"input_tokens"`
	OutputTokens     int64           `json:"output_tokens"`
	TotalTokens      int64           `json:"total_tokens"`
	SaleAmount       decimal.Decimal `json:"sale_amount"`
	IsStream         bool            `json:"is_stream"`
	Status           string          `json:"status"`
	ErrorCode        *string         `json:"error_code"`
	CreatedAt        time.Time       `json:"created_at"`
}

// AdminUsageResp 管理端「全量用量」单条流水（§14.7）。
// 在用户端字段基础上额外含 user_id、api_key_id（可空）。
type AdminUsageResp struct {
	UserID   uint64  `json:"user_id"`
	APIKeyID *uint64 `json:"api_key_id"`
	UserUsageResp
}

// ToUserUsageResp 将用量日志模型转为用户端精简视图。
func ToUserUsageResp(m *model.TokenUsageLog) UserUsageResp {
	return UserUsageResp{
		RequestID:        m.RequestID,
		LogicalModelCode: m.LogicalModelCode,
		Modality:         m.Modality,
		InputTokens:      m.InputTokens,
		OutputTokens:     m.OutputTokens,
		TotalTokens:      m.TotalTokens,
		SaleAmount:       m.SaleAmount,
		IsStream:         m.IsStream,
		Status:           m.Status,
		ErrorCode:        m.ErrorCode,
		CreatedAt:        m.CreatedAt,
	}
}

// ToAdminUsageResp 将用量日志模型转为管理端视图（含 user_id / api_key_id）。
func ToAdminUsageResp(m *model.TokenUsageLog) AdminUsageResp {
	return AdminUsageResp{
		UserID:        m.UserID,
		APIKeyID:      m.APIKeyID,
		UserUsageResp: ToUserUsageResp(m),
	}
}
