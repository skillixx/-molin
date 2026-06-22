package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/workbench/model"
)

// ErrPluginNotFound Plugin 记录不存在（RowsAffected==0 守卫）。
var ErrPluginNotFound = errors.New("plugin 不存在")

// PluginRepository 外部第三方工具（Plugin）数据访问层。
type PluginRepository struct {
	db *gorm.DB
}

// NewPluginRepository 创建 Plugin 仓库实例。
func NewPluginRepository(db *gorm.DB) *PluginRepository {
	return &PluginRepository{db: db}
}

// Create 创建 Plugin 记录。
func (r *PluginRepository) Create(ctx context.Context, m *model.Plugin) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindByID 按 ID 查询 Plugin。
func (r *PluginRepository) FindByID(ctx context.Context, id uint64) (*model.Plugin, error) {
	var m model.Plugin
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindByCode 按 code 查询 Plugin（唯一性校验用）。
func (r *PluginRepository) FindByCode(ctx context.Context, code string) (*model.Plugin, error) {
	var m model.Plugin
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindByIDs 按 ID 集合批量查询（自建 Agent 绑定校验：仅可绑 active 官方 plugin）。
func (r *PluginRepository) FindByIDs(ctx context.Context, ids []uint64) ([]model.Plugin, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var items []model.Plugin
	if err := r.db.WithContext(ctx).Where("id IN ?", ids).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ListPaged 分页查询 Plugin，支持 status 过滤（空字符串不过滤）。
func (r *PluginRepository) ListPaged(ctx context.Context, status string, offset, limit int) ([]model.Plugin, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.Plugin{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.Plugin
	if err := query.Order("id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Update 更新 Plugin 字段（map 方式支持零值更新）。
func (r *PluginRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.Plugin{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrPluginNotFound
	}
	return nil
}

// Delete 删除 Plugin 记录。
func (r *PluginRepository) Delete(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&model.Plugin{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrPluginNotFound
	}
	return nil
}
