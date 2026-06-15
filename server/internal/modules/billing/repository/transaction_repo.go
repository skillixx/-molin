package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/billing/model"
)

// TransactionRepository 钱包流水数据访问层（只追加写入，禁止 UPDATE/DELETE）。
type TransactionRepository struct {
	db *gorm.DB
}

// NewTransactionRepository 创建流水仓库实例。
func NewTransactionRepository(db *gorm.DB) *TransactionRepository {
	return &TransactionRepository{db: db}
}

// TransactionFilter 流水列表过滤条件。
type TransactionFilter struct {
	// UserID 管理端按用户过滤（0=不过滤）。
	UserID uint64
	// Type 流水类型：recharge/consume/refund/freeze/unfreeze（空=不过滤）。
	Type string
	// Direction 流水方向：in/out（空=不过滤）。
	Direction string
	// CreatedFrom 起始时间，查询 created_at >= CreatedFrom（nil=不过滤）。
	CreatedFrom *time.Time
	// CreatedTo 截止时间，查询 created_at <= CreatedTo（nil=不过滤）。
	CreatedTo *time.Time
}

// applyFilter 将过滤条件追加到 GORM query 上，返回新的 query。
func applyTransactionFilter(query *gorm.DB, f TransactionFilter) *gorm.DB {
	if f.Type != "" {
		query = query.Where("type = ?", f.Type)
	}
	if f.Direction != "" {
		query = query.Where("direction = ?", f.Direction)
	}
	if f.CreatedFrom != nil {
		query = query.Where("created_at >= ?", *f.CreatedFrom)
	}
	if f.CreatedTo != nil {
		query = query.Where("created_at <= ?", *f.CreatedTo)
	}
	return query
}

// Create 追加写入一条流水记录（在事务内调用时传入 tx）。
func (r *TransactionRepository) Create(tx *gorm.DB, txRecord *model.WalletTransaction) error {
	return tx.Create(txRecord).Error
}

// ListByUserID 查询用户钱包流水（分页，按 created_at 倒序）。
// filter 可指定 Type/Direction/CreatedFrom/CreatedTo 过滤条件，空值不生效。
func (r *TransactionRepository) ListByUserID(ctx context.Context, userID uint64, filter TransactionFilter, offset, limit int) ([]model.WalletTransaction, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.WalletTransaction{}).Where("user_id = ?", userID)
	// 追加可选过滤条件（type/direction/时间区间）
	query = applyTransactionFilter(query, filter)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []model.WalletTransaction
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&records).Error; err != nil {
		return nil, 0, err
	}
	return records, total, nil
}

// AdminListAll 管理员查询所有流水（分页）。
// filter.UserID > 0 时按用户过滤；其余字段按 Type/Direction/时间区间过滤。
func (r *TransactionRepository) AdminListAll(ctx context.Context, filter TransactionFilter, offset, limit int) ([]model.WalletTransaction, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.WalletTransaction{})
	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	// 追加可选过滤条件（type/direction/时间区间）
	query = applyTransactionFilter(query, filter)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []model.WalletTransaction
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&records).Error; err != nil {
		return nil, 0, err
	}
	return records, total, nil
}
