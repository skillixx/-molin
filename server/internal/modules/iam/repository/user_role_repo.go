package repository

import (
	"context"

	"gorm.io/gorm"
	"molin/server/internal/modules/iam/model"
)

// UserRoleRepository 用户角色关联数据访问层。
type UserRoleRepository struct {
	db *gorm.DB
}

func NewUserRoleRepository(db *gorm.DB) *UserRoleRepository {
	return &UserRoleRepository{db: db}
}

func (r *UserRoleRepository) Assign(ctx context.Context, userID, roleID uint64) error {
	ur := &model.UserRole{UserID: userID, RoleID: roleID}
	return r.db.WithContext(ctx).Create(ur).Error
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
