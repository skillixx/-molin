package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/membership/model"
)

// BenefitRepository 会员权益数据访问层。
type BenefitRepository struct {
	db *gorm.DB
}

// NewBenefitRepository 创建权益仓库实例。
func NewBenefitRepository(db *gorm.DB) *BenefitRepository {
	return &BenefitRepository{db: db}
}

// Create 创建权益记录。
func (r *BenefitRepository) Create(ctx context.Context, benefit *model.MembershipBenefit) error {
	return r.db.WithContext(ctx).Create(benefit).Error
}

// FindByID 按 ID 查询权益。
func (r *BenefitRepository) FindByID(ctx context.Context, id uint64) (*model.MembershipBenefit, error) {
	var b model.MembershipBenefit
	if err := r.db.WithContext(ctx).First(&b, id).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

// FindByLevelID 查询某等级的所有权益（支持 levelID=0 时查全部）。
func (r *BenefitRepository) FindByLevelID(ctx context.Context, levelID uint64) ([]*model.MembershipBenefit, error) {
	query := r.db.WithContext(ctx).Model(&model.MembershipBenefit{})
	if levelID > 0 {
		query = query.Where("level_id = ?", levelID)
	}
	var benefits []*model.MembershipBenefit
	if err := query.Order("id ASC").Find(&benefits).Error; err != nil {
		return nil, err
	}
	return benefits, nil
}

// FindActiveByLevelID 查询某等级的所有 active 权益（公开端点用，仅返回上架权益）。
func (r *BenefitRepository) FindActiveByLevelID(ctx context.Context, levelID uint64) ([]*model.MembershipBenefit, error) {
	var benefits []*model.MembershipBenefit
	if err := r.db.WithContext(ctx).Model(&model.MembershipBenefit{}).
		Where("level_id = ? AND status = 'active'", levelID).
		Order("id ASC").Find(&benefits).Error; err != nil {
		return nil, err
	}
	return benefits, nil
}

// Update 更新权益字段。
func (r *BenefitRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.MembershipBenefit{}).
		Where("id = ?", id).Updates(updates).Error
}
