package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

// ErrChannelNotFound 渠道记录不存在（RowsAffected==0 守卫）。
var ErrChannelNotFound = errors.New("渠道不存在")

// ChannelRepository 上游供应商渠道数据访问层。
type ChannelRepository struct {
	db *gorm.DB
}

// NewChannelRepository 创建渠道仓库实例。
func NewChannelRepository(db *gorm.DB) *ChannelRepository {
	return &ChannelRepository{db: db}
}

// Create 创建渠道记录。
func (r *ChannelRepository) Create(ctx context.Context, c *model.TokenChannel) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// FindByID 按 ID 查询渠道。
func (r *ChannelRepository) FindByID(ctx context.Context, id uint64) (*model.TokenChannel, error) {
	var c model.TokenChannel
	if err := r.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// FindByCode 按渠道编码查询渠道（创建前唯一性校验/转发选渠道用）。
func (r *ChannelRepository) FindByCode(ctx context.Context, code string) (*model.TokenChannel, error) {
	var c model.TokenChannel
	if err := r.db.WithContext(ctx).Where("code = ?", code).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// ListPaged 分页查询渠道，支持 status 过滤（空字符串不过滤）。
// 返回扁平分页二元组 (items, total)，handler 后续包 {items,page,page_size,total}。
func (r *ChannelRepository) ListPaged(ctx context.Context, status string, offset, limit int) ([]model.TokenChannel, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.TokenChannel{})
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.TokenChannel
	if err := query.Order("priority DESC, id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Update 更新渠道字段（map 方式支持零值更新）。
func (r *ChannelRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.TokenChannel{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrChannelNotFound
	}
	return nil
}

// Delete 删除渠道记录。
func (r *ChannelRepository) Delete(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&model.TokenChannel{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrChannelNotFound
	}
	return nil
}
