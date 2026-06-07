package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/membership/model"
)

// UserMembershipRepository 用户会员数据访问层。
type UserMembershipRepository struct {
	db *gorm.DB
}

// NewUserMembershipRepository 创建用户会员仓库实例。
func NewUserMembershipRepository(db *gorm.DB) *UserMembershipRepository {
	return &UserMembershipRepository{db: db}
}

// Create 创建用户会员记录。
func (r *UserMembershipRepository) Create(ctx context.Context, m *model.UserMembership) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindActive 查询用户当前有效会员。
// 查询条件：status = active AND (expires_at IS NULL OR expires_at > NOW())
func (r *UserMembershipRepository) FindActive(ctx context.Context, userID uint64) (*model.UserMembership, error) {
	var m model.UserMembership
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = 'active' AND (expires_at IS NULL OR expires_at > NOW())", userID).
		Order("created_at DESC").
		First(&m).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// HasActiveLevelIn 校验用户当前是否拥有有效会员资格，且等级属于给定的等级 ID 集合。
// 查询条件：status = active AND (expires_at IS NULL OR expires_at > NOW()) AND level_id IN (...)
// 用于"会员专属商品"购买门槛校验：判断用户是否具备购买所需的会员等级。
func (r *UserMembershipRepository) HasActiveLevelIn(ctx context.Context, userID uint64, levelIDs []uint64) (bool, error) {
	if len(levelIDs) == 0 {
		return false, nil
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&model.UserMembership{}).
		Where("user_id = ? AND status = 'active' AND (expires_at IS NULL OR expires_at > NOW()) AND level_id IN ?", userID, levelIDs).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListByUserID 查询用户所有会员记录（支持 userID=0 时查全部）。
func (r *UserMembershipRepository) ListByUserID(ctx context.Context, userID uint64, offset, limit int) ([]*model.UserMembership, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.UserMembership{})
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []*model.UserMembership
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
