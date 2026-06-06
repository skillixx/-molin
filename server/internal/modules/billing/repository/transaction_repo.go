package repository

import (
	"context"

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

// Create 追加写入一条流水记录（在事务内调用时传入 tx）。
func (r *TransactionRepository) Create(tx *gorm.DB, txRecord *model.WalletTransaction) error {
	return tx.Create(txRecord).Error
}

// ListByUserID 查询用户钱包流水（分页，按 created_at 倒序）。
func (r *TransactionRepository) ListByUserID(ctx context.Context, userID uint64, offset, limit int) ([]model.WalletTransaction, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.WalletTransaction{}).Where("user_id = ?", userID)
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
func (r *TransactionRepository) AdminListAll(ctx context.Context, userID uint64, offset, limit int) ([]model.WalletTransaction, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.WalletTransaction{})
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
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
