package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/app/model"
)

// AppRepository 应用数据访问层。
type AppRepository struct {
	db *gorm.DB
}

// NewAppRepository 创建应用仓库实例。
func NewAppRepository(db *gorm.DB) *AppRepository {
	return &AppRepository{db: db}
}

// Create 创建应用。
func (r *AppRepository) Create(ctx context.Context, a *model.Application) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// FindByID 按 ID 查询应用。
func (r *AppRepository) FindByID(ctx context.Context, id uint64) (*model.Application, error) {
	var a model.Application
	if err := r.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// FindByCode 按 code 查询应用（用于唯一性校验）。
func (r *AppRepository) FindByCode(ctx context.Context, code string) (*model.Application, error) {
	var a model.Application
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAll 管理端分页查询应用，支持按 status / type 筛选。
func (r *AppRepository) ListAll(ctx context.Context, status, appType string, offset, limit int) ([]*model.Application, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Application{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if appType != "" {
		query = query.Where("type = ?", appType)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*model.Application
	if err := query.Offset(offset).Limit(limit).Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Update 更新应用字段。
func (r *AppRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.Application{}).
		Where("id = ?", id).Updates(updates).Error
}
