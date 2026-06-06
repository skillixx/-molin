package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
)

// PlanRepository 套餐数据访问层。
type PlanRepository struct {
	db *gorm.DB
}

// NewPlanRepository 创建套餐仓库实例。
func NewPlanRepository(db *gorm.DB) *PlanRepository {
	return &PlanRepository{db: db}
}

// Create 创建套餐。
func (r *PlanRepository) Create(ctx context.Context, plan *model.ProductPlan) error {
	return r.db.WithContext(ctx).Create(plan).Error
}

// FindByID 按 ID 查询套餐。
func (r *PlanRepository) FindByID(ctx context.Context, id uint64) (*model.ProductPlan, error) {
	var plan model.ProductPlan
	if err := r.db.WithContext(ctx).First(&plan, id).Error; err != nil {
		return nil, err
	}
	return &plan, nil
}

// FindByProductID 查询商品下所有套餐（仅活跃状态）。
func (r *PlanRepository) FindByProductID(ctx context.Context, productID uint64) ([]model.ProductPlan, error) {
	var plans []model.ProductPlan
	err := r.db.WithContext(ctx).Where("product_id = ? AND status = ?", productID, "active").
		Order("id ASC").Find(&plans).Error
	return plans, err
}

// FindAllByProductID 查询商品下所有套餐（管理端，不过滤状态）。
func (r *PlanRepository) FindAllByProductID(ctx context.Context, productID uint64) ([]model.ProductPlan, error) {
	var plans []model.ProductPlan
	err := r.db.WithContext(ctx).Where("product_id = ?", productID).Order("id ASC").Find(&plans).Error
	return plans, err
}

// Update 更新套餐字段。
func (r *PlanRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.ProductPlan{}).Where("id = ?", id).Updates(updates).Error
}
