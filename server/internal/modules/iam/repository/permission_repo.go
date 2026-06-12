package repository

import (
	"context"

	"gorm.io/gorm"
	"molin/server/internal/modules/iam/model"
)

// PermissionRepository 权限数据访问层。
type PermissionRepository struct {
	db *gorm.DB
}

func NewPermissionRepository(db *gorm.DB) *PermissionRepository {
	return &PermissionRepository{db: db}
}

func (r *PermissionRepository) List(ctx context.Context) ([]model.Permission, error) {
	var perms []model.Permission
	err := r.db.WithContext(ctx).Find(&perms).Error
	return perms, err
}

func (r *PermissionRepository) FindByID(ctx context.Context, id uint64) (*model.Permission, error) {
	var p model.Permission
	err := r.db.WithContext(ctx).First(&p, id).Error
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListPaged 分页查询权限列表，支持关键字搜索（匹配 code 或 name），空字符串时不过滤。
func (r *PermissionRepository) ListPaged(ctx context.Context, keyword string, offset, limit int) ([]model.Permission, int64, error) {
	var perms []model.Permission
	var total int64
	db := r.db.WithContext(ctx).Model(&model.Permission{})
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
	if err := db.Offset(offset).Limit(limit).Find(&perms).Error; err != nil {
		return nil, 0, err
	}
	return perms, total, nil
}

// FindByRoleIDs 查询一批角色拥有的所有权限（JOIN role_permissions）。
func (r *PermissionRepository) FindByRoleIDs(ctx context.Context, roleIDs []uint64) ([]model.Permission, error) {
	if len(roleIDs) == 0 {
		return nil, nil
	}
	var perms []model.Permission
	err := r.db.WithContext(ctx).
		Joins("JOIN role_permissions rp ON rp.permission_id = permissions.id").
		Where("rp.role_id IN ?", roleIDs).
		Distinct("permissions.*").
		Find(&perms).Error
	return perms, err
}

// Create 创建一个新的权限码。
func (r *PermissionRepository) Create(ctx context.Context, perm *model.Permission) error {
	return r.db.WithContext(ctx).Create(perm).Error
}

// SetRolePermissions 全量替换角色的权限集合：先删除该角色现有的所有权限关联，
// 再批量插入新的权限关联（permissionIDs 为空数组时仅删除，相当于清空角色权限）。
// 整个操作在事务内完成，保证原子性。
func (r *PermissionRepository) SetRolePermissions(ctx context.Context, roleID uint64, permissionIDs []uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 先删除该角色现有的所有权限关联
		if err := tx.Where("role_id = ?", roleID).Delete(&model.RolePermission{}).Error; err != nil {
			return err
		}
		// 再批量插入新的权限关联
		if len(permissionIDs) == 0 {
			return nil
		}
		rps := make([]model.RolePermission, len(permissionIDs))
		for i, pid := range permissionIDs {
			rps[i] = model.RolePermission{RoleID: roleID, PermissionID: pid}
		}
		return tx.Create(&rps).Error
	})
}
