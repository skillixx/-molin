package repository

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
		// C-FIX-1：永久会员（expires_at IS NULL）优先，其次按到期时间最晚者，语义明确。
		Order("expires_at IS NULL DESC, expires_at DESC").
		First(&m).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// FindActiveByLevelForUpdate 加行锁查询用户在指定等级下的有效会员（续期定位用，必须在事务中调用）。
// 返回 nil 表示该等级下无有效会员（应新建）。
func (r *UserMembershipRepository) FindActiveByLevelForUpdate(ctx context.Context, tx *gorm.DB, userID, levelID uint64) (*model.UserMembership, error) {
	var m model.UserMembership
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND level_id = ? AND status = 'active' AND (expires_at IS NULL OR expires_at > NOW())", userID, levelID).
		Order("expires_at IS NULL DESC, expires_at DESC").
		First(&m).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// BatchExpire 批量将到期的 active 会员标记为 expired（定时任务使用，每次最多处理 limit 条）。
// 返回受影响行数。C-FIX-5：与资产到期任务对齐，避免 status 长期陈旧。
func (r *UserMembershipRepository) BatchExpire(ctx context.Context, limit int) (int64, error) {
	var ids []uint64
	if err := r.db.WithContext(ctx).Model(&model.UserMembership{}).
		Where("status = 'active' AND expires_at IS NOT NULL AND expires_at < NOW()").
		Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.WithContext(ctx).Model(&model.UserMembership{}).
		Where("id IN ?", ids).Update("status", "expired")
	return res.RowsAffected, res.Error
}

// FindByID 按 ID 查询用户会员记录。
func (r *UserMembershipRepository) FindByID(ctx context.Context, id uint64) (*model.UserMembership, error) {
	var m model.UserMembership
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateByID 更新用户会员记录字段（管理端调整/取消用）。
func (r *UserMembershipRepository) UpdateByID(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.UserMembership{}).
		Where("id = ?", id).Updates(updates).Error
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

// UserExists 校验 users 表中是否存在指定用户。
// BUG-M10-01：管理端手动开通会员前需确认 user_id 存在，避免产生孤儿会员记录。
// 与 iam 模块校验用户存在的方式一致（Table("users") 计数），不跨模块调用其 repository。
func (r *UserMembershipRepository) UserExists(ctx context.Context, userID uint64) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Table("users").
		Where("id = ?", userID).Count(&count).Error; err != nil {
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
