package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/conversation/model"
)

// ErrConversationNotFound 会话不存在或不属于该用户（隔离守卫）。
var ErrConversationNotFound = errors.New("会话不存在")

// ConversationRepository 会话与消息数据访问层。所有查询强制带 user_id，杜绝越权读取他人会话。
type ConversationRepository struct {
	db *gorm.DB
}

// NewConversationRepository 创建仓库实例。
func NewConversationRepository(db *gorm.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

// Create 新建会话，回填 c.ID。
func (r *ConversationRepository) Create(ctx context.Context, c *model.Conversation) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// FindOwned 按 id + user_id 查询会话（隔离）。不存在返回 ErrConversationNotFound。
func (r *ConversationRepository) FindOwned(ctx context.Context, id, userID uint64) (*model.Conversation, error) {
	var c model.Conversation
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListByUser 分页列出某用户的会话，按 last_message_at DESC, id DESC。
// scope: "plain" 仅普通聊天(agent_id IS NULL)；"agent" 仅 Agent 会话；其它=全部。
func (r *ConversationRepository) ListByUser(ctx context.Context, userID uint64, scope string, offset, limit int) ([]model.Conversation, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Conversation{}).Where("user_id = ?", userID)
	switch scope {
	case "plain":
		q = q.Where("agent_id IS NULL")
	case "agent":
		q = q.Where("agent_id IS NOT NULL")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.Conversation
	if err := q.Order("last_message_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// UpdateTitle 更新标题（带 user_id 隔离）。
func (r *ConversationRepository) UpdateTitle(ctx context.Context, id, userID uint64, title string) error {
	res := r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("title", title)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrConversationNotFound
	}
	return nil
}

// UpdateSummary 更新滚动摘要与水位线（压缩后写回）。
func (r *ConversationRepository) UpdateSummary(ctx context.Context, id uint64, summary string, untilID uint64) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"summary":             summary,
			"summarized_until_id": untilID,
		}).Error
}

// Delete 删除会话及其全部消息（单事务，带 user_id 隔离）。
func (r *ConversationRepository) Delete(ctx context.Context, id, userID uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&model.Conversation{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrConversationNotFound
		}
		return tx.Where("conversation_id = ?", id).Delete(&model.Message{}).Error
	})
}

// AppendMessage 追加一条消息，并在同事务内递增会话 message_count、刷新 last_message_at。
func (r *ConversationRepository) AppendMessage(ctx context.Context, m *model.Message) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(m).Error; err != nil {
			return err
		}
		return tx.Model(&model.Conversation{}).Where("id = ?", m.ConversationID).
			Updates(map[string]interface{}{
				"message_count":   gorm.Expr("message_count + 1"),
				"last_message_at": time.Now(),
			}).Error
	})
}

// ListMessages 分页列出会话内消息，按 id ASC（最早→最新）。供详情/历史回看。
func (r *ConversationRepository) ListMessages(ctx context.Context, convID uint64, offset, limit int) ([]model.Message, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Message{}).Where("conversation_id = ?", convID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.Message
	if err := q.Order("id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListAfter 取水位线之后（id > afterID）的全部消息，按 id ASC。供上下文重建与压缩。
func (r *ConversationRepository) ListAfter(ctx context.Context, convID, afterID uint64) ([]model.Message, error) {
	var items []model.Message
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND id > ?", convID, afterID).
		Order("id ASC").Find(&items).Error
	if err != nil {
		return nil, err
	}
	return items, nil
}
