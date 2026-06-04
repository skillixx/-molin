package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/iam/model"
)

// OverrideRepository 用户权限覆盖数据访问层。
type OverrideRepository struct {
	db *gorm.DB
}

func NewOverrideRepository(db *gorm.DB) *OverrideRepository {
	return &OverrideRepository{db: db}
}

// FindByUser 查询用户所有有效的权限覆盖（未过期）。
func (r *OverrideRepository) FindByUser(ctx context.Context, userID uint64) ([]model.UserPermissionOverride, error) {
	var overrides []model.UserPermissionOverride
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND (expires_at IS NULL OR expires_at > ?)", userID, time.Now()).
		Find(&overrides).Error
	return overrides, err
}

func (r *OverrideRepository) Create(ctx context.Context, o *model.UserPermissionOverride) error {
	return r.db.WithContext(ctx).Create(o).Error
}

func (r *OverrideRepository) Delete(ctx context.Context, overrideID uint64) error {
	return r.db.WithContext(ctx).Delete(&model.UserPermissionOverride{}, overrideID).Error
}
