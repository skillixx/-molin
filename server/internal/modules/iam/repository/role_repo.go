package repository

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/iam/model"
)

// ErrRoleNotFound 角色不存在（UPDATE/DELETE 影响行数为 0）。
var ErrRoleNotFound = gorm.ErrRecordNotFound

// ErrRoleCodeExists 角色 code 已存在（违反 uk_roles_code 唯一约束）。
var ErrRoleCodeExists = errors.New("角色 code 已存在")

// RoleRepository 角色数据访问层。
type RoleRepository struct {
	db *gorm.DB
}

func NewRoleRepository(db *gorm.DB) *RoleRepository {
	return &RoleRepository{db: db}
}

// Create 创建角色。若 code 已存在（违反 uk_roles_code 唯一约束），返回 ErrRoleCodeExists。
func (r *RoleRepository) Create(ctx context.Context, role *model.Role) error {
	err := r.db.WithContext(ctx).Create(role).Error
	if err != nil {
		// 捕获 MySQL 唯一键冲突错误（error number 1062），转换为业务错误
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrRoleCodeExists
		}
		return err
	}
	return nil
}

func (r *RoleRepository) FindByID(ctx context.Context, id uint64) (*model.Role, error) {
	var role model.Role
	err := r.db.WithContext(ctx).First(&role, id).Error
	if err != nil {
		return nil, err
	}
	return &role, nil
}

func (r *RoleRepository) List(ctx context.Context) ([]model.Role, error) {
	var roles []model.Role
	err := r.db.WithContext(ctx).Find(&roles).Error
	return roles, err
}

// Update 更新角色字段，若角色不存在（rowsAffected==0）则返回 ErrRoleNotFound。
func (r *RoleRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.Role{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	// rowsAffected 为 0 说明该 id 对应的角色不存在
	if result.RowsAffected == 0 {
		return ErrRoleNotFound
	}
	return nil
}

// Delete 删除角色，若角色不存在（rowsAffected==0）则返回 ErrRoleNotFound。
func (r *RoleRepository) Delete(ctx context.Context, id uint64) error {
	result := r.db.WithContext(ctx).Delete(&model.Role{}, id)
	if result.Error != nil {
		return result.Error
	}
	// rowsAffected 为 0 说明该 id 对应的角色不存在
	if result.RowsAffected == 0 {
		return ErrRoleNotFound
	}
	return nil
}

// DeleteWithCleanup 在事务内先删除 role_permissions 中该角色的权限关联，再删除 roles 记录。
// 若角色不存在（rowsAffected==0），返回 ErrRoleNotFound。
// D-61：取代原 Delete 方法，防止删除角色后 role_permissions 产生孤儿记录。
func (r *RoleRepository) DeleteWithCleanup(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先删除该角色的所有权限关联，防止孤儿记录
		if err := tx.Where("role_id = ?", id).Delete(&model.RolePermission{}).Error; err != nil {
			return err
		}
		// 再删除角色本身
		result := tx.Delete(&model.Role{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrRoleNotFound
		}
		return nil
	})
}

// CountUsersByRole 统计拥有指定角色的用户数，用于删除前检查是否有用户引用。
func (r *RoleRepository) CountUsersByRole(ctx context.Context, roleID uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("user_roles").
		Where("role_id = ?", roleID).
		Count(&count).Error
	return count, err
}

// ListPaged 分页查询角色列表，支持关键字搜索（匹配 code 或 name），空字符串时不过滤。
func (r *RoleRepository) ListPaged(ctx context.Context, keyword string, offset, limit int) ([]model.Role, int64, error) {
	var roles []model.Role
	var total int64
	db := r.db.WithContext(ctx).Model(&model.Role{})
	// keyword 非空时在 code 和 name 字段中做 LIKE 模糊匹配
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("code LIKE ? OR name LIKE ?", like, like)
	}
	// 先查总数
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// 再查分页数据
	if err := db.Offset(offset).Limit(limit).Find(&roles).Error; err != nil {
		return nil, 0, err
	}
	return roles, total, nil
}
