package repository

import (
	"context"

	"gorm.io/gorm"
)

// userRecord 用于从 users 表查询实名状态的内部结构（避免导入 auth 模块 model）。
type userRecord struct {
	RealNameStatus string
}

// UserRepoAdapter 实现 product.UserRepository 接口，直接查 users 表获取实名状态。
// 避免跨模块导入 auth 仓库（只能通过 service 接口，但此处实名状态查询足够简单，直接读表）。
type UserRepoAdapter struct {
	db *gorm.DB
}

// NewUserRepoAdapter 创建用户信息适配器实例。
func NewUserRepoAdapter(db *gorm.DB) *UserRepoAdapter {
	return &UserRepoAdapter{db: db}
}

// GetRealNameStatus 查询用户实名认证状态（直接读 users 表，不依赖 auth 模块 model）。
func (r *UserRepoAdapter) GetRealNameStatus(ctx context.Context, userID uint64) (string, error) {
	var rec userRecord
	err := r.db.WithContext(ctx).
		Table("users").
		Select("real_name_status").
		Where("id = ?", userID).
		First(&rec).Error
	if err != nil {
		return "", err
	}
	return rec.RealNameStatus, nil
}
