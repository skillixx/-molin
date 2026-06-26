package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

// ErrTokenModelNotFound 模型目录记录不存在（RowsAffected==0 守卫）。
var ErrTokenModelNotFound = errors.New("模型不存在")

// TokenModelRepository 对外模型目录数据访问层。
type TokenModelRepository struct {
	db *gorm.DB
}

// NewTokenModelRepository 创建模型目录仓库实例。
func NewTokenModelRepository(db *gorm.DB) *TokenModelRepository {
	return &TokenModelRepository{db: db}
}

// Create 创建模型目录记录。
func (r *TokenModelRepository) Create(ctx context.Context, m *model.TokenModel) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindByID 按 ID 查询模型。
func (r *TokenModelRepository) FindByID(ctx context.Context, id uint64) (*model.TokenModel, error) {
	var m model.TokenModel
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindByCode 按逻辑模型名查询模型（chat 透传前的模型校验用）。
func (r *TokenModelRepository) FindByCode(ctx context.Context, code string) (*model.TokenModel, error) {
	var m model.TokenModel
	if err := r.db.WithContext(ctx).Where("logical_model_code = ?", code).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// ListPaged 分页查询模型目录，支持 status、modality 过滤（空字符串不过滤）。
// 返回扁平分页二元组 (items, total)，handler 后续包 {items,page,page_size,total}。
func (r *TokenModelRepository) ListPaged(ctx context.Context, status, modality string, offset, limit int) ([]model.TokenModel, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.TokenModel{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if modality != "" {
		query = query.Where("modality = ?", modality)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.TokenModel
	if err := query.Order("sort_order ASC, id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListActiveCandidates 返回全部 active 模型（可选 modality 过滤），按展示顺序排序。
// 供用户端定向可见性过滤：模型目录规模小，全量加载候选后在应用层判可见性再分页（保证分页 total 准确）。
func (r *TokenModelRepository) ListActiveCandidates(ctx context.Context, modality string) ([]model.TokenModel, error) {
	query := r.db.WithContext(ctx).Model(&model.TokenModel{}).Where("status = ?", "active")
	if modality != "" {
		query = query.Where("modality = ?", modality)
	}
	var items []model.TokenModel
	if err := query.Order("sort_order ASC, id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// Update 更新模型字段（map 方式支持零值更新）。
func (r *TokenModelRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.TokenModel{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTokenModelNotFound
	}
	return nil
}

// Delete 删除模型目录记录。
func (r *TokenModelRepository) Delete(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&model.TokenModel{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTokenModelNotFound
	}
	return nil
}
