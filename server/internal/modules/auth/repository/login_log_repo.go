package repository

import (
	"context"

	"gorm.io/gorm"
	"molin/server/internal/modules/auth/model"
)

// LoginLogRepository 登录日志数据访问层。
type LoginLogRepository struct {
	db *gorm.DB
}

func NewLoginLogRepository(db *gorm.DB) *LoginLogRepository {
	return &LoginLogRepository{db: db}
}

func (r *LoginLogRepository) Create(ctx context.Context, log *model.LoginLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}
