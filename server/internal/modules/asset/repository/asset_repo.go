package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/asset/model"
)

// AssetRepository 用户资产数据访问层。
type AssetRepository struct {
	db *gorm.DB
}

// NewAssetRepository 创建资产仓库实例。
func NewAssetRepository(db *gorm.DB) *AssetRepository {
	return &AssetRepository{db: db}
}

// Create 创建用户资产记录。
func (r *AssetRepository) Create(ctx context.Context, asset *model.UserAsset) error {
	return r.db.WithContext(ctx).Create(asset).Error
}

// FindByID 按 ID 查询资产。
func (r *AssetRepository) FindByID(ctx context.Context, id uint64) (*model.UserAsset, error) {
	var asset model.UserAsset
	if err := r.db.WithContext(ctx).First(&asset, id).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

// FindByUserID 查询用户所有资产（支持按状态过滤）。
func (r *AssetRepository) FindByUserID(ctx context.Context, userID uint64, status string) ([]model.UserAsset, error) {
	query := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var assets []model.UserAsset
	if err := query.Order("created_at DESC").Find(&assets).Error; err != nil {
		return nil, err
	}
	return assets, nil
}

// ListAll 管理端查询所有资产（支持 user_id 过滤、分页）。
func (r *AssetRepository) ListAll(ctx context.Context, userID uint64, status string, offset, limit int) ([]model.UserAsset, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.UserAsset{})
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var assets []model.UserAsset
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&assets).Error; err != nil {
		return nil, 0, err
	}
	return assets, total, nil
}

// UpdateStatus 更新资产状态。
func (r *AssetRepository) UpdateStatus(ctx context.Context, id uint64, status string) error {
	return r.db.WithContext(ctx).Model(&model.UserAsset{}).
		Where("id = ?", id).Update("status", status).Error
}

// BatchExpire 批量将到期资产标记为 expired（定时任务使用，每次最多处理 limit 条）。
func (r *AssetRepository) BatchExpire(ctx context.Context, limit int) ([]model.UserAsset, error) {
	var assets []model.UserAsset
	err := r.db.WithContext(ctx).
		Where("status = 'active' AND expires_at IS NOT NULL AND expires_at < NOW()").
		Limit(limit).
		Find(&assets).Error
	if err != nil {
		return nil, err
	}

	if len(assets) == 0 {
		return nil, nil
	}

	// 批量更新状态
	ids := make([]uint64, len(assets))
	for i, a := range assets {
		ids[i] = a.ID
	}
	if err := r.db.WithContext(ctx).Model(&model.UserAsset{}).
		Where("id IN ?", ids).
		Update("status", "expired").Error; err != nil {
		return nil, err
	}
	return assets, nil
}
