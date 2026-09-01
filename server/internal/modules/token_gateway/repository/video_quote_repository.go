package repository

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrVideoQuoteNotFound = errors.New("视频报价不存在")
	ErrVideoQuoteConflict = errors.New("视频报价请求指纹冲突")
	ErrVideoQuoteExpired  = errors.New("视频报价已过期")
	ErrVideoQuoteConsumed = errors.New("视频报价已被其他请求消费")
)

// VideoQuoteRepository 只处理视频Quote的幂等创建和单次消费，不调用钱包、队列或Provider。
type VideoQuoteRepository struct {
	db *gorm.DB
}

// NewVideoQuoteRepository 装配共享Quote表的视频持久化边界。
func NewVideoQuoteRepository(db *gorm.DB) *VideoQuoteRepository { return &VideoQuoteRepository{db: db} }

// FindIdempotent 在重新选价前读取已存在的冻结Quote，保证调价或停价后重放仍返回原事实。
func (r *VideoQuoteRepository) FindIdempotent(ctx context.Context, userID, projectID uint64, commandKind, idempotencyKey, fingerprint string) (*model.AIGatewayQuote, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, ErrVideoQuoteNotFound
	}
	var item model.AIGatewayQuote
	err := r.db.WithContext(ctx).Where("capability=? AND user_id=? AND project_id=? AND command_kind=? AND idempotency_key=?",
		model.AIVideoCapability, userID, projectID, commandKind, idempotencyKey).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if subtle.ConstantTimeCompare([]byte(item.RequestFingerprint), []byte(fingerprint)) != 1 {
		return nil, false, ErrVideoQuoteConflict
	}
	return &item, true, nil
}

// CreateIdempotent 以用户、Project、命令类型和幂等键锁定唯一报价；同键异指纹稳定冲突。
func (r *VideoQuoteRepository) CreateIdempotent(ctx context.Context, quote *model.AIGatewayQuote) (*model.AIGatewayQuote, bool, error) {
	if r == nil || r.db == nil || !validVideoQuoteFact(quote) {
		return nil, false, ErrVideoQuoteNotFound
	}
	for attempt := 0; attempt < 7; attempt++ {
		var result *model.AIGatewayQuote
		existing := false
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 不对不存在的唯一范围先取gap lock；让唯一键直接决定赢家，避免100并发互锁。
			copyQuote := *quote
			if createErr := tx.Create(&copyQuote).Error; createErr == nil {
				result = &copyQuote
				return nil
			} else if !isVideoQuoteDuplicate(createErr) {
				return createErr
			}
			var item model.AIGatewayQuote
			// 外层事务可能已在FindIdempotent建立“不存在”的RR快照；savepoint不会刷新该快照。
			// 唯一键冲突已证明赢家提交，使用共享当前读获取原事实，不升级排他锁制造竞争死锁。
			if err := videoQuoteIdempotencyQuery(tx, quote).Clauses(clause.Locking{Strength: "SHARE"}).First(&item).Error; err != nil {
				return err
			}
			if subtle.ConstantTimeCompare([]byte(item.RequestFingerprint), []byte(quote.RequestFingerprint)) != 1 {
				return ErrVideoQuoteConflict
			}
			result, existing = &item, true
			return nil
		})
		if err == nil || !isRetryableVideoQuoteTransaction(err) || attempt == 6 {
			return result, existing, err
		}
		delay := time.Duration(10*(1<<attempt)) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, false, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, false, ErrVideoQuoteConflict
}

// Consume 使用行锁和CAS保证100并发只允许一个request_id成为赢家。
func (r *VideoQuoteRepository) Consume(ctx context.Context, publicID string, userID, projectID uint64, apiKeyID *uint64, operation, fingerprint, requestID string, now time.Time) (*model.AIGatewayQuote, bool, error) {
	if r == nil || r.db == nil {
		return nil, false, ErrVideoQuoteNotFound
	}
	var consumed *model.AIGatewayQuote
	replay := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var consumeErr error
		consumed, replay, consumeErr = r.ConsumeTx(tx, publicID, userID, projectID, apiKeyID, operation, fingerprint, requestID, now)
		return consumeErr
	})
	return consumed, replay, err
}

// ConsumeTx 允许请求、钱包Hold与任务在调用方的同一事务内原子落库。
func (r *VideoQuoteRepository) ConsumeTx(tx *gorm.DB, publicID string, userID, projectID uint64, apiKeyID *uint64, operation, fingerprint, requestID string, now time.Time) (*model.AIGatewayQuote, bool, error) {
	if tx == nil {
		return nil, false, ErrVideoQuoteNotFound
	}
	var item model.AIGatewayQuote
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("capability = ? AND operation = ?", model.AIVideoCapability, operation).
		Where("public_id = ? AND user_id = ? AND project_id = ?", publicID, userID, projectID)
	if apiKeyID == nil {
		query = query.Where("api_key_id IS NULL")
	} else {
		query = query.Where("api_key_id = ?", *apiKeyID)
	}
	if err := query.First(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, ErrVideoQuoteNotFound
		}
		return nil, false, err
	}
	if subtle.ConstantTimeCompare([]byte(item.RequestFingerprint), []byte(fingerprint)) != 1 {
		return nil, false, ErrVideoQuoteConflict
	}
	if item.ConsumedRequestID != nil {
		if *item.ConsumedRequestID != requestID {
			return nil, false, ErrVideoQuoteConsumed
		}
		return &item, true, nil
	}
	if !item.ExpiresAt.After(now) {
		return nil, false, ErrVideoQuoteExpired
	}
	update := tx.Model(&model.AIGatewayQuote{}).Where("id = ? AND consumed_request_id IS NULL", item.ID).
		Updates(map[string]interface{}{"consumed_request_id": requestID, "consumed_at": now})
	if update.Error != nil {
		return nil, false, update.Error
	}
	if update.RowsAffected != 1 {
		return nil, false, ErrVideoQuoteConsumed
	}
	item.ConsumedRequestID = &requestID
	item.ConsumedAt = &now
	return &item, false, nil
}

func validVideoQuoteFact(quote *model.AIGatewayQuote) bool {
	return quote != nil && quote.Capability == model.AIVideoCapability && quote.Operation != nil &&
		(*quote.Operation == model.AIVideoOperationTextToVideo || *quote.Operation == model.AIVideoOperationImageToVideo) &&
		quote.CommandKind != nil && (*quote.CommandKind == "quote" || *quote.CommandKind == "create_video") &&
		quote.IdempotencyKey != nil && strings.TrimSpace(*quote.IdempotencyKey) != "" && len(quote.RequestFingerprint) == 64
}

func videoQuoteIdempotencyQuery(tx *gorm.DB, quote *model.AIGatewayQuote) *gorm.DB {
	return tx.Where("capability = ? AND user_id = ? AND project_id = ? AND command_kind = ? AND idempotency_key = ?",
		model.AIVideoCapability, quote.UserID, quote.ProjectID, *quote.CommandKind, *quote.IdempotencyKey)
}

func isVideoQuoteDuplicate(err error) bool {
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func isRetryableVideoQuoteTransaction(err error) bool {
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) && (mysqlErr.Number == 1213 || mysqlErr.Number == 1205)
}
