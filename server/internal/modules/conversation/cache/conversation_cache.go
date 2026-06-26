package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/internal/modules/conversation/model"
)

const (
	ctxTTL    = 24 * time.Hour // 会话上下文热缓存 TTL
	recentCap = 60             // 快照保留的最近消息上限（防膨胀；活跃窗口已由压缩压小）
)

// ContextSnapshot 会话上下文热缓存快照：摘要 + 水位线 + 水位线之后的最近消息。
// 与 conversation 的「滚动摘要 + 近期原文」记忆模型一一对应，命中即可免查库组装上下文。
type ContextSnapshot struct {
	Summary           string          `json:"summary"`
	SummarizedUntilID uint64          `json:"summarized_until_id"`
	Messages          []model.Message `json:"messages"`
}

// ConversationCache 会话上下文 Redis 热缓存。
//
// 设计红线：MySQL 是唯一真相源，本缓存仅加速。任何 Redis 错误（连接失败/未命中/序列化失败）
// 都被吞掉并按「未命中 / 无操作」处理，由上层回落查库——缓存绝不参与正确性（fail-open）。
// 接收者为 nil（Redis 未注入）时所有方法安全空转，等价于纯 DB 行为。
type ConversationCache struct {
	rdb *redis.Client
}

// NewConversationCache 构造缓存。rdb 为 nil 时返回的实例所有方法空转（fail-open）。
func NewConversationCache(rdb *redis.Client) *ConversationCache {
	return &ConversationCache{rdb: rdb}
}

func ctxKey(convID uint64) string { return fmt.Sprintf("chat:conv:%d:ctx", convID) }

// Get 读取快照。未命中 / nil 接收者 / 任何错误 → (nil, false)。
func (c *ConversationCache) Get(ctx context.Context, convID uint64) (*ContextSnapshot, bool) {
	if c == nil || c.rdb == nil {
		return nil, false
	}
	raw, err := c.rdb.Get(ctx, ctxKey(convID)).Bytes()
	if err != nil {
		return nil, false // redis.Nil（未命中）或连接错误，统一当未命中
	}
	var snap ContextSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, false
	}
	return &snap, true
}

// Set 覆盖写入快照并刷新 TTL（先按水位线/上限裁剪）。错误吞掉。
func (c *ConversationCache) Set(ctx context.Context, convID uint64, snap *ContextSnapshot) {
	if c == nil || c.rdb == nil || snap == nil {
		return
	}
	trimSnapshot(snap)
	raw, err := json.Marshal(snap)
	if err != nil {
		return
	}
	_ = c.rdb.Set(ctx, ctxKey(convID), raw, ctxTTL).Err()
}

// Append 把一条新消息写穿到已有快照。快照不存在则无操作（下次读时按库重建并回填）。
func (c *ConversationCache) Append(ctx context.Context, convID uint64, m model.Message) {
	if c == nil || c.rdb == nil {
		return
	}
	snap, ok := c.Get(ctx, convID)
	if !ok {
		return
	}
	snap.Messages = append(snap.Messages, m)
	c.Set(ctx, convID, snap)
}

// Invalidate 删除会话缓存（删除会话等场景）。
func (c *ConversationCache) Invalidate(ctx context.Context, convID uint64) {
	if c == nil || c.rdb == nil {
		return
	}
	_ = c.rdb.Del(ctx, ctxKey(convID)).Err()
}

// trimSnapshot 丢弃水位线之前（已被摘要覆盖）的消息，并按上限保留最近若干条，防快照无限膨胀。
func trimSnapshot(snap *ContextSnapshot) {
	if snap.SummarizedUntilID > 0 {
		kept := snap.Messages[:0]
		for _, m := range snap.Messages {
			if m.ID > snap.SummarizedUntilID {
				kept = append(kept, m)
			}
		}
		snap.Messages = kept
	}
	if len(snap.Messages) > recentCap {
		snap.Messages = snap.Messages[len(snap.Messages)-recentCap:]
	}
}
