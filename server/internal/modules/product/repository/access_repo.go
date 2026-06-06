package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
)

// AccessRepository 商品角色访问规则数据访问层。
type AccessRepository struct {
	db *gorm.DB
}

// NewAccessRepository 创建访问规则仓库实例。
func NewAccessRepository(db *gorm.DB) *AccessRepository {
	return &AccessRepository{db: db}
}

// FindByProductID 查询商品所有角色访问规则。
func (r *AccessRepository) FindByProductID(ctx context.Context, productID uint64) ([]model.ProductRoleAccess, error) {
	var accesses []model.ProductRoleAccess
	err := r.db.WithContext(ctx).Where("product_id = ?", productID).Find(&accesses).Error
	return accesses, err
}

// CanBuy 校验给定角色列表中是否有任意角色对该商品具有购买权限。
func (r *AccessRepository) CanBuy(ctx context.Context, productID uint64, roleIDs []uint64) bool {
	if len(roleIDs) == 0 {
		return false
	}
	var count int64
	r.db.WithContext(ctx).Model(&model.ProductRoleAccess{}).
		Where("product_id = ? AND role_id IN ? AND can_buy = ?", productID, roleIDs, true).
		Count(&count)
	return count > 0
}

// CanView 校验给定角色列表是否可以查看该商品。
func (r *AccessRepository) CanView(ctx context.Context, productID uint64, roleIDs []uint64) bool {
	if len(roleIDs) == 0 {
		return false
	}
	var count int64
	r.db.WithContext(ctx).Model(&model.ProductRoleAccess{}).
		Where("product_id = ? AND role_id IN ? AND can_view = ?", productID, roleIDs, true).
		Count(&count)
	return count > 0
}

// ReplaceByProductID 覆盖写入商品访问规则（先删后插，事务保证原子性）。
func (r *AccessRepository) ReplaceByProductID(ctx context.Context, productID uint64, accesses []model.ProductRoleAccess) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 删除该商品已有规则
		if err := tx.Where("product_id = ?", productID).Delete(&model.ProductRoleAccess{}).Error; err != nil {
			return err
		}
		if len(accesses) == 0 {
			return nil
		}
		return tx.Create(&accesses).Error
	})
}
