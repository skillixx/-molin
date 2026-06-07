package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/app/model"
)

// AdapterRepository 应用适配器数据访问层。
type AdapterRepository struct {
	db *gorm.DB
}

// NewAdapterRepository 创建适配器仓库实例。
func NewAdapterRepository(db *gorm.DB) *AdapterRepository {
	return &AdapterRepository{db: db}
}

// Create 创建适配器。
func (r *AdapterRepository) Create(ctx context.Context, a *model.ApplicationAdapter) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// FindByID 按 ID 查询适配器。
func (r *AdapterRepository) FindByID(ctx context.Context, id uint64) (*model.ApplicationAdapter, error) {
	var a model.ApplicationAdapter
	if err := r.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// FindByAppCode 按 app_code 查询适配器（用于唯一性校验）。
func (r *AdapterRepository) FindByAppCode(ctx context.Context, appCode string) (*model.ApplicationAdapter, error) {
	var a model.ApplicationAdapter
	if err := r.db.WithContext(ctx).Where("app_code = ?", appCode).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAll 管理端分页查询适配器，支持按 status 筛选。
func (r *AdapterRepository) ListAll(ctx context.Context, status string, offset, limit int) ([]*model.ApplicationAdapter, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.ApplicationAdapter{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*model.ApplicationAdapter
	if err := query.Offset(offset).Limit(limit).Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Update 更新适配器字段。
func (r *AdapterRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.ApplicationAdapter{}).
		Where("id = ?", id).Updates(updates).Error
}
