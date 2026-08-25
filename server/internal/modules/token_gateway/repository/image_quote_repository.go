package repository

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrImageQuoteNotFound = errors.New("图片报价不存在")
	ErrImageQuoteConflict = errors.New("图片报价请求指纹冲突")
	ErrImageQuoteExpired  = errors.New("图片报价已过期")
	ErrImageQuoteConsumed = errors.New("图片报价已被其他请求消费")
)

// ImageQuoteRepository 只负责一次性 Quote 持久化和行锁消费，不计算价格或触发钱包与 Provider。
type ImageQuoteRepository struct {
	db *gorm.DB
}

func NewImageQuoteRepository(db *gorm.DB) *ImageQuoteRepository {
	return &ImageQuoteRepository{db: db}
}

func (r *ImageQuoteRepository) Create(ctx context.Context, quote *model.AIGatewayQuote) error {
	if r == nil || r.db == nil || quote == nil {
		return ErrImageQuoteNotFound
	}
	return r.db.WithContext(ctx).Create(quote).Error
}

// Consume 通过 SELECT FOR UPDATE 串行化消费；相同 request_id 重放返回原事实，不同请求只能有一个胜者。
func (r *ImageQuoteRepository) Consume(ctx context.Context, publicID string, userID, projectID uint64, apiKeyID *uint64, fingerprint, requestID string, now time.Time) (*model.AIGatewayQuote, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, ErrImageQuoteNotFound
	}
	var consumed *model.AIGatewayQuote
	idempotent := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var consumeErr error
		consumed, idempotent, consumeErr = r.ConsumeTx(tx, publicID, userID, projectID, apiKeyID, fingerprint, requestID, now)
		return consumeErr
	})
	if err != nil {
		return nil, false, err
	}
	return consumed, idempotent, nil
}

// ConsumeTx 让请求、Quote和钱包hold在同一个外部事务内形成原子事实。
func (r *ImageQuoteRepository) ConsumeTx(tx *gorm.DB, publicID string, userID, projectID uint64, apiKeyID *uint64, fingerprint, requestID string, now time.Time) (*model.AIGatewayQuote, bool, error) {
	if tx == nil {
		return nil, false, ErrImageQuoteNotFound
	}
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("public_id = ? AND user_id = ? AND project_id = ?", publicID, userID, projectID)
	if apiKeyID == nil {
		query = query.Where("api_key_id IS NULL")
	} else {
		query = query.Where("api_key_id = ?", *apiKeyID)
	}
	var quote model.AIGatewayQuote
	if err := query.First(&quote).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, ErrImageQuoteNotFound
		}
		return nil, false, err
	}
	if subtle.ConstantTimeCompare([]byte(quote.RequestFingerprint), []byte(fingerprint)) != 1 {
		return nil, false, ErrImageQuoteConflict
	}
	if quote.ConsumedRequestID != nil {
		if *quote.ConsumedRequestID != requestID {
			return nil, false, ErrImageQuoteConsumed
		}
		return &quote, true, nil
	}
	if !quote.ExpiresAt.After(now) {
		return nil, false, ErrImageQuoteExpired
	}
	result := tx.Model(&model.AIGatewayQuote{}).
		Where("id = ? AND consumed_request_id IS NULL", quote.ID).
		Updates(map[string]interface{}{"consumed_request_id": requestID, "consumed_at": now})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, false, ErrImageQuoteConsumed
	}
	quote.ConsumedRequestID = &requestID
	quote.ConsumedAt = &now
	return &quote, false, nil
}
