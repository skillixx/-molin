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

// BillingRuleFilter 计费规则列表过滤条件（管理端 P15）。
type BillingRuleFilter struct {
	ProductID *uint64 // 可选，按商品过滤
	Status    string  // 可选，按状态过滤
}

// ListPaged 分页查询计费规则（管理端 P15，支持 product_id/status 过滤）。
// 返回当前页数据、符合条件的总数。
func (r *BillingRuleRepository) ListPaged(ctx context.Context, filter BillingRuleFilter, offset, limit int) ([]model.ProductBillingRule, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.ProductBillingRule{})
	if filter.ProductID != nil {
		query = query.Where("product_id = ?", *filter.ProductID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rules []model.ProductBillingRule
	if err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&rules).Error; err != nil {
		return nil, 0, err
	}
	return rules, total, nil
}

// FindByID 按主键查询计费规则（管理端 P17）。
func (r *BillingRuleRepository) FindByID(ctx context.Context, id uint64) (*model.ProductBillingRule, error) {
	var rule model.ProductBillingRule
	if err := r.db.WithContext(ctx).First(&rule, id).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// Create 新增计费规则（管理端 P16）。
func (r *BillingRuleRepository) Create(ctx context.Context, rule *model.ProductBillingRule) error {
	return r.db.WithContext(ctx).Create(rule).Error
}

// Update 部分更新计费规则字段（管理端 P17，map 方式支持零值更新）。
func (r *BillingRuleRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.ProductBillingRule{}).Where("id = ?", id).Updates(updates).Error
}
