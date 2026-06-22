package repository

import (
	"context"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/asset/model"
)

// EntitlementRepository 用户权益数据访问层。
type EntitlementRepository struct {
	db *gorm.DB
}

// NewEntitlementRepository 创建权益仓库实例。
func NewEntitlementRepository(db *gorm.DB) *EntitlementRepository {
	return &EntitlementRepository{db: db}
}

// Create 创建权益记录。
func (r *EntitlementRepository) Create(ctx context.Context, e *model.UserEntitlement) error {
	return r.db.WithContext(ctx).Create(e).Error
}

// FindByAssetID 查询某资产的所有权益。
func (r *EntitlementRepository) FindByAssetID(ctx context.Context, assetID uint64) ([]model.UserEntitlement, error) {
	var list []model.UserEntitlement
	if err := r.db.WithContext(ctx).Where("asset_id = ?", assetID).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// FindByUserID 查询用户所有活跃权益。
func (r *EntitlementRepository) FindByUserID(ctx context.Context, userID uint64) ([]model.UserEntitlement, error) {
	var list []model.UserEntitlement
	if err := r.db.WithContext(ctx).Where("user_id = ? AND status = 'active'", userID).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// FindByID 普通读取权益记录（无行锁）。
// 供只读场景使用（如 S2-丙3 余额前置闸粗筛），不参与扣减、不需要 FOR UPDATE。
func (r *EntitlementRepository) FindByID(ctx context.Context, id uint64) (*model.UserEntitlement, error) {
	var e model.UserEntitlement
	if err := r.db.WithContext(ctx).First(&e, id).Error; err != nil {
		return nil, err
	}
	return &e, nil
}

// FindByIDForUpdate 使用 SELECT FOR UPDATE 锁定权益记录（并发安全）。
// 必须在事务中调用。
func (r *EntitlementRepository) FindByIDForUpdate(ctx context.Context, tx *gorm.DB, id uint64) (*model.UserEntitlement, error) {
	var e model.UserEntitlement
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&e, id).Error; err != nil {
		return nil, err
	}
	return &e, nil
}

// ConsumeQuota 并发安全地消耗权益配额（SELECT FOR UPDATE）。
// 必须在外部开启事务后调用，或由 service 层通过 db.Transaction 包裹。
func (r *EntitlementRepository) ConsumeQuota(ctx context.Context, db *gorm.DB, id uint64, amount decimal.Decimal) error {
	return db.WithContext(ctx).Model(&model.UserEntitlement{}).
		Where("id = ? AND status = 'active'", id).
		Update("quota_used", gorm.Expr("quota_used + ?", amount)).Error
}

// ReserveQuota 在事务内预占额度：quota_reserved += amount（S2-丙4）。
// 调用方须已在同一事务内 FOR UPDATE 锁行并完成 available 校验，本方法仅做原子加。
func (r *EntitlementRepository) ReserveQuota(ctx context.Context, db *gorm.DB, id uint64, amount decimal.Decimal) error {
	return db.WithContext(ctx).Model(&model.UserEntitlement{}).
		Where("id = ?", id).
		Update("quota_reserved", gorm.Expr("quota_reserved + ?", amount)).Error
}

// SettleQuota 在事务内结算预占：quota_reserved -= reserved（释放预占），quota_used += used（计入已用）。
// 多退少补由 service 计算 used（封顶到预占额）后传入。调用方须已 FOR UPDATE 锁行。
func (r *EntitlementRepository) SettleQuota(ctx context.Context, db *gorm.DB, id uint64, reserved, used decimal.Decimal) error {
	return db.WithContext(ctx).Model(&model.UserEntitlement{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"quota_reserved": gorm.Expr("quota_reserved - ?", reserved),
			"quota_used":     gorm.Expr("quota_used + ?", used),
		}).Error
}

// ReleaseQuota 在事务内释放预占：quota_reserved -= reserved（不计 used，失败/异常路径）。
// 调用方须已 FOR UPDATE 锁行。
func (r *EntitlementRepository) ReleaseQuota(ctx context.Context, db *gorm.DB, id uint64, reserved decimal.Decimal) error {
	return db.WithContext(ctx).Model(&model.UserEntitlement{}).
		Where("id = ?", id).
		Update("quota_reserved", gorm.Expr("quota_reserved - ?", reserved)).Error
}

// UpdateStatus 批量更新某资产下所有权益状态。
func (r *EntitlementRepository) UpdateStatusByAssetID(ctx context.Context, assetID uint64, status string) error {
	return r.db.WithContext(ctx).Model(&model.UserEntitlement{}).
		Where("asset_id = ?", assetID).Update("status", status).Error
}
