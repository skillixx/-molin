package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
)

// ProductRepository 商品 CRUD 数据访问层。
type ProductRepository struct {
	db *gorm.DB
}

// NewProductRepository 创建商品仓库实例。
func NewProductRepository(db *gorm.DB) *ProductRepository {
	return &ProductRepository{db: db}
}

// Create 创建商品。
func (r *ProductRepository) Create(ctx context.Context, product *model.Product) error {
	return r.db.WithContext(ctx).Create(product).Error
}

// FindByID 按 ID 查询商品。
func (r *ProductRepository) FindByID(ctx context.Context, id uint64) (*model.Product, error) {
	var p model.Product
	if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// Update 更新商品字段（map 方式支持零值更新）。
func (r *ProductRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.Product{}).Where("id = ?", id).Updates(updates).Error
}

// UpdateStatus 更新商品上下架状态。
func (r *ProductRepository) UpdateStatus(ctx context.Context, id uint64, status string) error {
	return r.db.WithContext(ctx).Model(&model.Product{}).Where("id = ?", id).Update("status", status).Error
}

// ListAll 查询所有商品（管理端，支持分页和过滤）。
func (r *ProductRepository) ListAll(ctx context.Context, keyword, status, productType string, offset, limit int) ([]model.Product, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Product{})
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("name LIKE ? OR product_code LIKE ?", like, like)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if productType != "" {
		query = query.Where("product_type = ?", productType)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var products []model.Product
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&products).Error; err != nil {
		return nil, 0, err
	}
	return products, total, nil
}

// ListVisible 查询用户可见的商品（状态为 active，且角色在 product_role_access 中 can_view=1）。
// 支持 keyword（名称/描述模糊匹配）和 productType 过滤，空字符串表示不过滤。
func (r *ProductRepository) ListVisible(ctx context.Context, roleIDs []uint64, keyword, productType string, offset, limit int) ([]model.Product, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Product{}).Where("status = ?", "active")
	if len(roleIDs) > 0 {
		// 只显示有 can_view 权限的商品
		query = query.Where(`id IN (
			SELECT DISTINCT product_id FROM product_role_access WHERE role_id IN ? AND can_view = 1
		)`, roleIDs)
	} else {
		// 无角色用户不可见任何商品
		query = query.Where("1 = 0")
	}

	// 关键词模糊过滤：匹配 name 或 description
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where("(name LIKE ? OR description LIKE ?)", like, like)
	}
	// 商品类型过滤
	if productType != "" {
		query = query.Where("product_type = ?", productType)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var products []model.Product
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&products).Error; err != nil {
		return nil, 0, err
	}
	return products, total, nil
}
