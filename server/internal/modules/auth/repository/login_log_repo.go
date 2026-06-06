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

// FindLastSuccessByUser 查询该用户最近一次登录成功的记录，用于个人信息中心展示最后登录时间。
func (r *LoginLogRepository) FindLastSuccessByUser(ctx context.Context, userID uint64) (*model.LoginLog, error) {
	var log model.LoginLog
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, "success").
		Order("created_at DESC").
		First(&log).Error
	if err != nil {
		return nil, err
	}
	return &log, nil
}
