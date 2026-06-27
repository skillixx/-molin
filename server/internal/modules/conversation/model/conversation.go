package model

import "time"

// Conversation 聊天会话（有状态记忆），对应 chat_conversations 表。
// AgentID 为空 = 普通聊天会话；非空 = Agent 会话。所有读写按 UserID 强隔离。
// Summary + SummarizedUntilID 构成滚动压缩记忆：水位线之前的消息被压成 Summary（长期记忆），
// 水位线之后的消息作为原文上下文随请求带给模型。
type Conversation struct {
	ID                uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID            uint64     `gorm:"column:user_id;not null" json:"user_id"`                                   // 所属用户（隔离维度）
	AgentID           *uint64    `gorm:"column:agent_id" json:"agent_id"`                                          // NULL=普通聊天；非空=Agent 会话（空值渲染为 null，对齐契约）
	Title             string     `gorm:"size:255;not null;default:''" json:"title"`                                // 会话标题
	ModelCode         string     `gorm:"column:model_code;size:128;not null;default:''" json:"model_code"`         // 逻辑模型名
	Summary           string     `gorm:"type:mediumtext" json:"summary"`                                           // 滚动压缩历史摘要
	SummarizedUntilID uint64     `gorm:"column:summarized_until_id;not null;default:0" json:"summarized_until_id"` // 摘要水位线
	MessageCount      int        `gorm:"column:message_count;not null;default:0" json:"message_count"`             // 消息总数
	LastMessageAt     *time.Time `gorm:"column:last_message_at" json:"last_message_at"`                            // 最后消息时间（空值渲染为 null，对齐契约）
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// TableName 指定表名。
func (Conversation) TableName() string { return "chat_conversations" }

// Message 聊天消息，对应 chat_messages 表。
type Message struct {
	ID             uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	ConversationID uint64    `gorm:"column:conversation_id;not null" json:"conversation_id"`
	UserID         uint64    `gorm:"column:user_id;not null" json:"user_id"` // 冗余隔离
	Role           string    `gorm:"size:16;not null" json:"role"`           // user/assistant/tool/system
	Content        string    `gorm:"type:mediumtext" json:"content"`
	ToolCalls      *string   `gorm:"column:tool_calls;type:json" json:"tool_calls,omitempty"` // assistant tool_calls 原文
	ToolCallID     string    `gorm:"column:tool_call_id;size:128;not null;default:''" json:"tool_call_id,omitempty"`
	TokenEst       int       `gorm:"column:token_est;not null;default:0" json:"token_est"` // token 估算
	CreatedAt      time.Time `json:"created_at"`
}

// TableName 指定表名。
func (Message) TableName() string { return "chat_messages" }
