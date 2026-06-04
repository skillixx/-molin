package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
)

// VerificationRepository 验证码数据访问层。
type VerificationRepository struct {
	db *gorm.DB
}

func NewVerificationRepository(db *gorm.DB) *VerificationRepository {
	return &VerificationRepository{db: db}
}

func (r *VerificationRepository) Create(ctx context.Context, v *model.VerificationCode) error {
	return r.db.WithContext(ctx).Create(v).Error
}

// FindValid 查询未使用且未过期的最新验证码。
func (r *VerificationRepository) FindValid(ctx context.Context, targetType, targetValue, scene string) (*model.VerificationCode, error) {
	var v model.VerificationCode
	err := r.db.WithContext(ctx).
		Where("target_type = ? AND target_value = ? AND scene = ? AND used_at IS NULL AND expires_at > ?",
			targetType, targetValue, scene, time.Now()).
		Order("created_at DESC").
		First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// MarkUsed 标记验证码已使用。
func (r *VerificationRepository) MarkUsed(ctx context.Context, id uint64) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.VerificationCode{}).
		Where("id = ?", id).
		Update("used_at", &now).Error
}
