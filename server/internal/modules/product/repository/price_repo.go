package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
)

// PriceRepository 价格数据访问层。
type PriceRepository struct {
	db *gorm.DB
}

// NewPriceRepository 创建价格仓库实例。
func NewPriceRepository(db *gorm.DB) *PriceRepository {
	return &PriceRepository{db: db}
}

// FindByPlanID 查询套餐所有价格配置（含会员价、角色价、默认价）。
func (r *PriceRepository) FindByPlanID(ctx context.Context, planID uint64) ([]model.ProductPrice, error) {
	var prices []model.ProductPrice
	err := r.db.WithContext(ctx).Where("product_plan_id = ?", planID).Find(&prices).Error
	return prices, err
}

// FindMembershipPrice 查询套餐的会员专属价（membership_level_id IS NOT NULL AND role_id IS NULL）。
func (r *PriceRepository) FindMembershipPrice(ctx context.Context, planID, membershipLevelID uint64) (*model.ProductPrice, error) {
	var price model.ProductPrice
	err := r.db.WithContext(ctx).Where(
		"product_plan_id = ? AND membership_level_id = ? AND role_id IS NULL",
		planID, membershipLevelID,
	).First(&price).Error
	if err != nil {
		return nil, err
	}
	return &price, nil
}

// ListMembershipLevelIDs 查询套餐配置的所有会员专属价对应的会员等级 ID 列表
//（membership_level_id IS NOT NULL AND role_id IS NULL）。
// 返回非空列表代表该套餐为"会员专属"套餐：只有持有列表中任一等级有效会员资格的用户才允许购买。
// 用于购买访问控制阶段的会员专属门槛校验。
func (r *PriceRepository) ListMembershipLevelIDs(ctx context.Context, planID uint64) ([]uint64, error) {
	var levelIDs []uint64
	err := r.db.WithContext(ctx).Model(&model.ProductPrice{}).
		Where("product_plan_id = ? AND membership_level_id IS NOT NULL AND role_id IS NULL", planID).
		Pluck("membership_level_id", &levelIDs).Error
	if err != nil {
		return nil, err
	}
	return levelIDs, nil
}

// FindRolePrice 查询套餐的角色价（role_id IS NOT NULL AND membership_level_id IS NULL）。
func (r *PriceRepository) FindRolePrice(ctx context.Context, planID, roleID uint64) (*model.ProductPrice, error) {
	var price model.ProductPrice
	err := r.db.WithContext(ctx).Where(
		"product_plan_id = ? AND role_id = ? AND membership_level_id IS NULL",
		planID, roleID,
	).First(&price).Error
	if err != nil {
		return nil, err
	}
	return &price, nil
}

// FindDefaultPrice 查询套餐的默认价格（role_id IS NULL AND membership_level_id IS NULL）。
func (r *PriceRepository) FindDefaultPrice(ctx context.Context, planID uint64) (*model.ProductPrice, error) {
	var price model.ProductPrice
	err := r.db.WithContext(ctx).Where(
		"product_plan_id = ? AND role_id IS NULL AND membership_level_id IS NULL",
		planID,
	).First(&price).Error
	if err != nil {
		return nil, err
	}
	return &price, nil
}

// ReplaceByPlanID 覆盖写入套餐价格（先删后插，事务保证原子性）。
func (r *PriceRepository) ReplaceByPlanID(ctx context.Context, planID uint64, prices []model.ProductPrice) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 删除该套餐已有价格
		if err := tx.Where("product_plan_id = ?", planID).Delete(&model.ProductPrice{}).Error; err != nil {
			return err
		}
		if len(prices) == 0 {
			return nil
		}
		return tx.Create(&prices).Error
	})
}
