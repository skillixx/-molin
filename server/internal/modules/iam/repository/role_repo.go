package repository

import (
	"context"

	"gorm.io/gorm"
	"molin/server/internal/modules/iam/model"
)

// RoleRepository 角色数据访问层。
type RoleRepository struct {
	db *gorm.DB
}

func NewRoleRepository(db *gorm.DB) *RoleRepository {
	return &RoleRepository{db: db}
}

func (r *RoleRepository) Create(ctx context.Context, role *model.Role) error {
	return r.db.WithContext(ctx).Create(role).Error
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

func (r *RoleRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return r.db.WithContext(ctx).Model(&model.Role{}).Where("id = ?", id).Updates(updates).Error
}

func (r *RoleRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&model.Role{}, id).Error
}

// ListPaged 分页查询角色列表，返回当前页数据及总条数。
func (r *RoleRepository) ListPaged(ctx context.Context, offset, limit int) ([]model.Role, int64, error) {
	var roles []model.Role
	var total int64
	db := r.db.WithContext(ctx).Model(&model.Role{})
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
