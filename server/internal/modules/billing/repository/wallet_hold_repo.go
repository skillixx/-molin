package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/billing/model"
)

// WalletHoldRepository 钱包预扣保证金（hold）数据访问层。
type WalletHoldRepository struct {
	db *gorm.DB
}

// NewWalletHoldRepository 创建 hold 仓库实例。
func NewWalletHoldRepository(db *gorm.DB) *WalletHoldRepository {
	return &WalletHoldRepository{db: db}
}

// FindByIdempotencyKey 按幂等键查询 hold（非事务，用于幂等预检查）。
// 返回 (nil, gorm.ErrRecordNotFound) 表示不存在。
func (r *WalletHoldRepository) FindByIdempotencyKey(ctx context.Context, key string) (*model.WalletHold, error) {
	var hold model.WalletHold
	if err := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&hold).Error; err != nil {
		return nil, err
	}
	return &hold, nil
}

// FindByIDForUpdate 在事务内加行锁查询 hold（SELECT ... FOR UPDATE）。
//
// D-M2-03 根因修复：同 wallet_repo.GetForUpdate，原 GORM v1 写法在 v2 下失效不锁行，
// 改用 clause.Locking 真正下发 FOR UPDATE，保证结算阶段对同一 hold 的并发处理串行化。
func (r *WalletHoldRepository) FindByIDForUpdate(tx *gorm.DB, id uint64) (*model.WalletHold, error) {
	var hold model.WalletHold
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).First(&hold).Error; err != nil {
		return nil, err
	}
	return &hold, nil
}

// Create 在事务内创建一条 hold 记录（gorm 回填自增 ID）。
func (r *WalletHoldRepository) Create(tx *gorm.DB, hold *model.WalletHold) error {
	return tx.Create(hold).Error
}

// UpdateFields 在事务内更新 hold 指定字段。
func (r *WalletHoldRepository) UpdateFields(tx *gorm.DB, id uint64, updates map[string]interface{}) error {
	return tx.Model(&model.WalletHold{}).Where("id = ?", id).Updates(updates).Error
}
