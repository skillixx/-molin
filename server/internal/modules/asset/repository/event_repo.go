package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/asset/model"
)

// EventRepository 资产事件日志数据访问层。
type EventRepository struct {
	db *gorm.DB
}

// NewEventRepository 创建事件仓库实例。
func NewEventRepository(db *gorm.DB) *EventRepository {
	return &EventRepository{db: db}
}

// Create 写入资产事件日志。
func (r *EventRepository) Create(ctx context.Context, event *model.AssetEvent) error {
	return r.db.WithContext(ctx).Create(event).Error
}

// FindByAssetID 查询某资产的所有事件（按时间倒序）。
func (r *EventRepository) FindByAssetID(ctx context.Context, assetID uint64) ([]model.AssetEvent, error) {
	var events []model.AssetEvent
	if err := r.db.WithContext(ctx).
		Where("asset_id = ?", assetID).
		Order("created_at DESC").
		Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}
