package repository

import (
	"context"
	"time"

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

// lastLoginRow 用于接收 GROUP BY 批量查询的结果集。
type lastLoginRow struct {
	UserID    uint64    `gorm:"column:user_id"`
	LastLogin time.Time `gorm:"column:last_login"`
}

// FindPagedByUser A-30：分页查询指定用户的全部登录日志，按时间降序。
func (r *LoginLogRepository) FindPagedByUser(ctx context.Context, userID uint64, offset, limit int) ([]model.LoginLog, int64, error) {
	var logs []model.LoginLog
	var total int64
	db := r.db.WithContext(ctx).Model(&model.LoginLog{}).Where("user_id = ?", userID)
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// FindLastSuccessBatch D-50：批量查询多个用户最后一次登录成功时间，以 GROUP BY 单次查询代替逐条循环。
// 返回 map[userID]lastLoginTime；未登录过的用户不在 map 中。
func (r *LoginLogRepository) FindLastSuccessBatch(ctx context.Context, userIDs []int64) (map[int64]time.Time, error) {
	result := make(map[int64]time.Time, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	var rows []lastLoginRow
	err := r.db.WithContext(ctx).
		Raw(`SELECT user_id, MAX(created_at) AS last_login
			 FROM user_login_logs
			 WHERE user_id IN ? AND status = 'success'
			 GROUP BY user_id`, userIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[int64(row.UserID)] = row.LastLogin
	}
	return result, nil
}
