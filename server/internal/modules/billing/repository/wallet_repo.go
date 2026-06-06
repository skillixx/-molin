package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/billing/model"
)

// WalletRepository 钱包数据访问层（含 SELECT FOR UPDATE 行锁支持）。
type WalletRepository struct {
	db *gorm.DB
}

// NewWalletRepository 创建钱包仓库实例。
func NewWalletRepository(db *gorm.DB) *WalletRepository {
	return &WalletRepository{db: db}
}

// GetOrCreate 获取用户钱包，若不存在则自动创建余额为 0 的钱包。
// 解决 Wallet 初始化时机问题：billing 模块假设 wallets 已存在，
// 通过此方法首次访问时自动初始化，无需注册时单独创建。
func (r *WalletRepository) GetOrCreate(ctx context.Context, userID uint64) (*model.Wallet, error) {
	var wallet model.Wallet
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&wallet).Error
	if err == nil {
		return &wallet, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	// 首次创建，余额为 0
	wallet = model.Wallet{
		UserID:   userID,
		Currency: "CNY",
	}
	if createErr := r.db.WithContext(ctx).Create(&wallet).Error; createErr != nil {
		// 并发场景：另一个请求已创建，重新查询
		if queryErr := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&wallet).Error; queryErr == nil {
			return &wallet, nil
		}
		return nil, createErr
	}
	return &wallet, nil
}

// GetByUserID 按用户 ID 查询钱包。
func (r *WalletRepository) GetByUserID(ctx context.Context, userID uint64) (*model.Wallet, error) {
	var wallet model.Wallet
	if err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&wallet).Error; err != nil {
		return nil, err
	}
	return &wallet, nil
}

// GetForUpdate 在事务内加行锁查询钱包（SELECT FOR UPDATE）。
// 必须在事务内调用，tx 参数即为事务 DB 实例。
func (r *WalletRepository) GetForUpdate(tx *gorm.DB, userID uint64) (*model.Wallet, error) {
	var wallet model.Wallet
	if err := tx.Set("gorm:query_option", "FOR UPDATE").
		Where("user_id = ?", userID).First(&wallet).Error; err != nil {
		return nil, err
	}
	return &wallet, nil
}

// UpdateWithOptimisticLock 使用乐观锁更新钱包余额和版本号。
// 返回 (影响行数, error)，影响行数为 0 表示乐观锁冲突，调用方应重试。
func (r *WalletRepository) UpdateWithOptimisticLock(tx *gorm.DB, walletID int64, version int64, updates map[string]interface{}) (int64, error) {
	updates["version"] = gorm.Expr("version + 1")
	result := tx.Model(&model.Wallet{}).
		Where("id = ? AND version = ?", walletID, version).
		Updates(updates)
	return result.RowsAffected, result.Error
}

// AdminListWallets 管理员分页查询钱包列表。
func (r *WalletRepository) AdminListWallets(ctx context.Context, offset, limit int) ([]model.Wallet, int64, error) {
	var total int64
	if err := r.db.WithContext(ctx).Model(&model.Wallet{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var wallets []model.Wallet
	if err := r.db.WithContext(ctx).Offset(offset).Limit(limit).Order("created_at DESC").Find(&wallets).Error; err != nil {
		return nil, 0, err
	}
	return wallets, total, nil
}
