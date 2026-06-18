package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/membership/model"
)

// LevelRepository 会员等级数据访问层。
type LevelRepository struct {
	db *gorm.DB
}

// NewLevelRepository 创建等级仓库实例。
func NewLevelRepository(db *gorm.DB) *LevelRepository {
	return &LevelRepository{db: db}
}

// Create 创建会员等级。
func (r *LevelRepository) Create(ctx context.Context, level *model.MembershipLevel) error {
	return r.db.WithContext(ctx).Create(level).Error
}

// FindByID 按 ID 查询会员等级。
func (r *LevelRepository) FindByID(ctx context.Context, id uint64) (*model.MembershipLevel, error) {
	var level model.MembershipLevel
	if err := r.db.WithContext(ctx).First(&level, id).Error; err != nil {
		return nil, err
	}
	return &level, nil
}

// ListActive 查询所有激活状态的会员等级（用户端展示）。
func (r *LevelRepository) ListActive(ctx context.Context) ([]*model.MembershipLevel, error) {
	var levels []*model.MembershipLevel
	if err := r.db.WithContext(ctx).
		Where("status = 'active'").
		Order("sort_order ASC, id ASC").
		Find(&levels).Error; err != nil {
		return nil, err
	}
	return levels, nil
}

// ListAll 查询所有会员等级（管理端，不过滤状态）。
func (r *LevelRepository) ListAll(ctx context.Context) ([]*model.MembershipLevel, error) {
	var levels []*model.MembershipLevel
	if err := r.db.WithContext(ctx).
		Order("sort_order ASC, id ASC").
		Find(&levels).Error; err != nil {
		return nil, err
	}
	return levels, nil
}

// FindByIDs 批量按 ID 查询会员等级（用于 M9 列表内联等级名，避免 N+1 查询）。
// 返回切片可直接用于在 service 层构建 id→level 映射。
func (r *LevelRepository) FindByIDs(ctx context.Context, ids []uint64) ([]*model.MembershipLevel, error) {
	if len(ids) == 0 {
		return []*model.MembershipLevel{}, nil
	}
	var levels []*model.MembershipLevel
	if err := r.db.WithContext(ctx).
		Where("id IN ?", ids).
		Find(&levels).Error; err != nil {
		return nil, err
	}
	return levels, nil
}

// Update 更新会员等级字段。
func (r *LevelRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.MembershipLevel{}).
		Where("id = ?", id).Updates(updates).Error
}
