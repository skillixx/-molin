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
