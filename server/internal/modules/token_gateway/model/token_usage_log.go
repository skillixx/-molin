package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// TokenUsageLog 用量与计费日志，对应 token_usage_logs 表。
// 用于用量展示 + 对账（不依赖 one-api 的库），支持按用户/按 api_key（sk）统计。
// 安全约定：禁止记录对话内容明文，仅记 tokens/状态等元数据。
type TokenUsageLog struct {
	ID               uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	RequestID        string          `gorm:"size:128;not null;uniqueIndex:uk_token_usage_logs_request_id" json:"request_id"` // 请求唯一标识，幂等键来源
	UserID           uint64          `gorm:"not null;index:idx_token_usage_logs_user_created,priority:1" json:"user_id"`     // 调用用户 ID
	APIKeyID         *uint64         `gorm:"index:idx_token_usage_logs_apikey_created,priority:1" json:"api_key_id,omitempty"` // 平台 API Key（sk）ID，登录态调用为空
	LogicalModelCode string          `gorm:"size:128;not null;index:idx_token_usage_logs_model" json:"logical_model_code"`   // 调用的逻辑模型名
	Modality         string          `gorm:"size:32;not null;default:chat" json:"modality"`                                  // 模态：chat/image/audio/video
	InputTokens      int64           `gorm:"not null;default:0" json:"input_tokens"`                                         // 输入 token 数
	OutputTokens     int64           `gorm:"not null;default:0" json:"output_tokens"`                                        // 输出 token 数
	TotalTokens      int64           `gorm:"not null;default:0" json:"total_tokens"`                                         // 总 token 数
	Units            decimal.Decimal `gorm:"type:decimal(18,6);not null;default:0" json:"units"`                             // 非 token 计量用量（生图/语音等）
	SaleAmount       decimal.Decimal `gorm:"type:decimal(18,6);not null;default:0" json:"sale_amount"`                       // 本次销售金额（扣费金额）
	IsStream         bool            `gorm:"not null;default:0" json:"is_stream"`                                            // 是否流式调用
	Status           string          `gorm:"size:32;not null" json:"status"`                                                 // success/failed/timeout
	ErrorCode        *string         `gorm:"size:64" json:"error_code,omitempty"`                                            // 错误码，失败时记录
	CreatedAt        time.Time       `gorm:"index:idx_token_usage_logs_user_created,priority:2;index:idx_token_usage_logs_apikey_created,priority:2" json:"created_at"`
}

// TableName 指定表名。
func (TokenUsageLog) TableName() string { return "token_usage_logs" }
