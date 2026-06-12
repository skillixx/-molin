package repository

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/iam/model"
)

// ErrUserRoleExists 用户已拥有该角色（违反 uk_user_roles 唯一约束）。
var ErrUserRoleExists = errors.New("该用户已拥有此角色")

// UserRoleRepository 用户角色关联数据访问层。
type UserRoleRepository struct {
	db *gorm.DB
}

func NewUserRoleRepository(db *gorm.DB) *UserRoleRepository {
	return &UserRoleRepository{db: db}
}

// Assign 为用户分配角色。若已存在相同记录（违反唯一键），返回 ErrUserRoleExists。
func (r *UserRoleRepository) Assign(ctx context.Context, userID, roleID uint64) error {
	ur := &model.UserRole{UserID: userID, RoleID: roleID}
	err := r.db.WithContext(ctx).Create(ur).Error
	if err != nil {
		// 捕获 MySQL 唯一键冲突错误（error number 1062），转换为业务错误
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrUserRoleExists
		}
		return err
	}
	return nil
}

func (r *UserRoleRepository) Revoke(ctx context.Context, userID, roleID uint64) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND role_id = ?", userID, roleID).
		Delete(&model.UserRole{}).Error
}

func (r *UserRoleRepository) FindByUser(ctx context.Context, userID uint64) ([]model.UserRole, error) {
	var urs []model.UserRole
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).Find(&urs).Error
	return urs, err
}

// GetRoleIDs 仅返回用户的角色 ID 列表。
func (r *UserRoleRepository) GetRoleIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	urs, err := r.FindByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, len(urs))
	for i, ur := range urs {
		ids[i] = ur.RoleID
	}
	return ids, nil
}

// FindRolesByUserPaged 分页查询用户已分配的角色详情，返回当前页数据及总条数。
func (r *UserRoleRepository) FindRolesByUserPaged(ctx context.Context, userID uint64, offset, limit int) ([]model.Role, int64, error) {
	var roles []model.Role
	var total int64
	db := r.db.WithContext(ctx).
		Table("roles").
		Joins("INNER JOIN user_roles ur ON ur.role_id = roles.id").
		Where("ur.user_id = ?", userID)
	// 先查总数
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// 再查分页数据
	if err := db.Select("roles.id, roles.code, roles.name, roles.description, roles.created_at, roles.updated_at").
		Offset(offset).Limit(limit).Find(&roles).Error; err != nil {
		return nil, 0, err
	}
	return roles, total, nil
}

// FindRolesByUser 通过 JOIN roles 表，返回用户所有已分配的角色详情（含 id、code、name、created_at）。
// 返回的 created_at 为 user_roles 表的创建时间（即分配时间）。
func (r *UserRoleRepository) FindRolesByUser(ctx context.Context, userID uint64) ([]model.Role, error) {
	var roles []model.Role
	err := r.db.WithContext(ctx).
		Table("roles").
		Joins("INNER JOIN user_roles ur ON ur.role_id = roles.id").
		Where("ur.user_id = ?", userID).
		Select("roles.id, roles.code, roles.name, roles.description, roles.created_at, roles.updated_at").
		Find(&roles).Error
	return roles, err
}

// GetUserIDsByRole 查询拥有指定角色的所有用户 ID，用于修改角色权限后批量失效相关用户的权限缓存。
func (r *UserRoleRepository) GetUserIDsByRole(ctx context.Context, roleID uint64) ([]uint64, error) {
	var userIDs []uint64
	err := r.db.WithContext(ctx).
		Model(&model.UserRole{}).
		Where("role_id = ?", roleID).
		Pluck("user_id", &userIDs).Error
	return userIDs, err
}

// ReplaceUserRoles 全量替换用户的角色集合：先删除该用户现有的所有角色关联，
// 再批量插入新的角色关联（roleIDs 为空数组时仅删除，相当于清空用户所有角色）。
// 整个操作在事务内完成，保证原子性。
func (r *UserRoleRepository) ReplaceUserRoles(ctx context.Context, userID uint64, roleIDs []uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先删除该用户现有的所有角色关联
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserRole{}).Error; err != nil {
			return err
		}
		// 再批量插入新的角色关联
		if len(roleIDs) == 0 {
			return nil
		}
		urs := make([]model.UserRole, len(roleIDs))
		for i, rid := range roleIDs {
			urs[i] = model.UserRole{UserID: userID, RoleID: rid}
		}
		return tx.Create(&urs).Error
	})
}
