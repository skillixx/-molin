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

// ListPaged 分页查询权限列表，返回当前页数据及总条数。
func (r *PermissionRepository) ListPaged(ctx context.Context, offset, limit int) ([]model.Permission, int64, error) {
	var perms []model.Permission
	var total int64
	db := r.db.WithContext(ctx).Model(&model.Permission{})
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
