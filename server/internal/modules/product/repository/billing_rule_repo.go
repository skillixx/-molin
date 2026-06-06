package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
)

// BillingRuleRepository 商品计费规则数据访问层。
type BillingRuleRepository struct {
	db *gorm.DB
}

// NewBillingRuleRepository 创建计费规则仓库实例。
func NewBillingRuleRepository(db *gorm.DB) *BillingRuleRepository {
	return &BillingRuleRepository{db: db}
}

// FindRule 匹配计费规则：优先按 (product_id, product_plan_id, usage_type) 精确匹配，
// 无匹配则按 (product_id, usage_type) 通用匹配（plan_id 为 NULL）。
func (r *BillingRuleRepository) FindRule(ctx context.Context, productID uint64, planID *uint64, usageType string) (*model.ProductBillingRule, error) {
	var rule model.ProductBillingRule
	query := r.db.WithContext(ctx).Where("product_id = ? AND usage_type = ? AND status = ?", productID, usageType, "active")
	if planID != nil {
		// 优先精确匹配套餐
		err := query.Where("product_plan_id = ?", *planID).First(&rule).Error
		if err == nil {
			return &rule, nil
		}
	}
	// 通用规则（product_plan_id IS NULL）
	err := query.Where("product_plan_id IS NULL").First(&rule).Error
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

// FindByProductID 查询商品所有计费规则（管理端）。
func (r *BillingRuleRepository) FindByProductID(ctx context.Context, productID uint64) ([]model.ProductBillingRule, error) {
	var rules []model.ProductBillingRule
	err := r.db.WithContext(ctx).Where("product_id = ?", productID).Order("id ASC").Find(&rules).Error
	return rules, err
}
