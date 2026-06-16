package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
)

// ErrPlanNotFound 套餐不存在（D-006：UpdatePlan 时 plan_id 不存在需返回该错误）。
var ErrPlanNotFound = errors.New("套餐不存在")

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
// D-006：检查 RowsAffected，若为 0 说明 plan_id 不存在，返回 ErrPlanNotFound。
func (r *PlanRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.ProductPlan{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrPlanNotFound
	}
	return nil
}
