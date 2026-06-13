package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

// Create 创建权限覆盖记录（单次插入，不处理唯一键冲突，内部测试用；业务层请用 Upsert）。
func (r *OverrideRepository) Create(ctx context.Context, o *model.UserPermissionOverride) error {
	return r.db.WithContext(ctx).Create(o).Error
}

// Upsert 创建或更新权限覆盖。当 (user_id, permission_id) 唯一键已存在时，
// 使用 ON DUPLICATE KEY UPDATE 更新 effect/reason/expires_at/created_by/updated_at，
// 避免触发 1062 唯一键冲突错误。
// D-25：替代原 Create 在重复设置时返回 500 的问题。
func (r *OverrideRepository) Upsert(ctx context.Context, o *model.UserPermissionOverride) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			// 唯一键列：uk_user_perm_override (user_id, permission_id)
			Columns: []clause.Column{{Name: "user_id"}, {Name: "permission_id"}},
			// 冲突时更新以下字段
			DoUpdates: clause.AssignmentColumns([]string{
				"permission_code", "effect", "reason", "expires_at", "created_by", "updated_at",
			}),
		}).
		Create(o).Error
}

// Delete 删除指定的权限覆盖，必须同时满足 id=? AND user_id=? 两个条件，
// 防止越权删除其他用户的 override 记录。
// D-27：增加 userID 过滤条件，返回 rowsAffected（0 表示记录不存在或越权）。
func (r *OverrideRepository) Delete(ctx context.Context, overrideID, userID uint64) (int64, error) {
	result := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", overrideID, userID).
		Delete(&model.UserPermissionOverride{})
	return result.RowsAffected, result.Error
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

// ReplaceByUserWithValidation 全量替换用户的权限覆盖集合，并在同一事务内校验每条覆盖记录的
// permission_id 是否存在于 permissions 表中。
// D-29：将 permission_id 存在性校验与写入操作放在同一事务，消除校验期间记录被删除的竟态窗口。
// 如果某个 permission_id 不存在，返回 gorm.ErrRecordNotFound，事务回滚。
func (r *OverrideRepository) ReplaceByUserWithValidation(ctx context.Context, userID uint64, overrides []model.UserPermissionOverride) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 事务内校验每个 permission_id 是否存在（避免竟态）
		for _, o := range overrides {
			var count int64
			if err := tx.Table("permissions").Where("id = ?", o.PermissionID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return gorm.ErrRecordNotFound
			}
		}
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
