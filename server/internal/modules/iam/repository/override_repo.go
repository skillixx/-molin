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

// FindByUser 查询用户所有有效的权限覆盖（未过期），不分页，兼容旧调用。
func (r *OverrideRepository) FindByUser(ctx context.Context, userID uint64) ([]model.UserPermissionOverride, error) {
	var overrides []model.UserPermissionOverride
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND (expires_at IS NULL OR expires_at > ?)", userID, time.Now()).
		Find(&overrides).Error
	return overrides, err
}

// ListByUserPaged 分页查询用户有效权限覆盖（未过期），返回当前页数据及总条数。
// offset 为从第几条开始，limit 为每页条数，均由 pagination.Params 计算后传入。
// effect 非空时只返回对应 effect（allow/deny）的记录；permCode 非空时按权限码精确匹配。
func (r *OverrideRepository) ListByUserPaged(ctx context.Context, userID uint64, effect, permCode string, offset, limit int) ([]model.UserPermissionOverride, int64, error) {
	var overrides []model.UserPermissionOverride
	var total int64

	// 基础查询条件：指定用户且未过期
	baseQuery := r.db.WithContext(ctx).Model(&model.UserPermissionOverride{}).
		Where("user_id = ? AND (expires_at IS NULL OR expires_at > ?)", userID, time.Now())

	// effect 非空时附加过滤
	if effect != "" {
		baseQuery = baseQuery.Where("effect = ?", effect)
	}

	// permission_code 非空时附加精确匹配过滤
	if permCode != "" {
		baseQuery = baseQuery.Where("permission_code = ?", permCode)
	}

	// 先查总数
	if err := baseQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 再查当页数据，按创建时间降序排列
	if err := baseQuery.Order("created_at DESC").Offset(offset).Limit(limit).Find(&overrides).Error; err != nil {
		return nil, 0, err
	}

	return overrides, total, nil
}

func (r *OverrideRepository) Create(ctx context.Context, o *model.UserPermissionOverride) error {
	return r.db.WithContext(ctx).Create(o).Error
}

func (r *OverrideRepository) Delete(ctx context.Context, overrideID uint64) error {
	return r.db.WithContext(ctx).Delete(&model.UserPermissionOverride{}, overrideID).Error
}

// ReplaceByUser 全量替换用户的权限覆盖集合：先删除该用户现有的所有权限覆盖记录，
// 再批量插入新的覆盖记录（overrides 为空数组时仅删除，相当于清空该用户所有权限覆盖）。
// 整个操作在事务内完成，保证原子性。
func (r *OverrideRepository) ReplaceByUser(ctx context.Context, userID uint64, overrides []model.UserPermissionOverride) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先删除该用户现有的所有权限覆盖记录
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserPermissionOverride{}).Error; err != nil {
			return err
		}
		// 再批量插入新的覆盖记录
		if len(overrides) == 0 {
			return nil
		}
		return tx.Create(&overrides).Error
	})
}
