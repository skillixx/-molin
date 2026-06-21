package repository

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"molin/server/internal/modules/asset/model"
)

// EntitlementConsumeLogRepository 权益额度扣减幂等日志数据访问层。
type EntitlementConsumeLogRepository struct {
	db *gorm.DB
}

// NewEntitlementConsumeLogRepository 创建幂等日志仓库实例。
func NewEntitlementConsumeLogRepository(db *gorm.DB) *EntitlementConsumeLogRepository {
	return &EntitlementConsumeLogRepository{db: db}
}

// ErrDuplicateConsumeLog 幂等键冲突：表示该 idempotency_key 已存在（重复请求）。
var ErrDuplicateConsumeLog = errors.New("权益扣减幂等键已存在")

// Create 在给定事务内插入一条扣减幂等日志。
// 当 idempotency_key 触发唯一键冲突（MySQL 1062）时返回 ErrDuplicateConsumeLog，
// 供 service 层据此识别「重复请求」并返回首次结果，不再二次扣减。
// 必须在事务中调用（与 ConsumeQuota 同一事务，保证幂等与扣减原子）。
func (r *EntitlementConsumeLogRepository) Create(ctx context.Context, tx *gorm.DB, log *model.EntitlementConsumeLog) error {
	if err := tx.WithContext(ctx).Create(log).Error; err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrDuplicateConsumeLog
		}
		return err
	}
	return nil
}

// FindByIdempotencyKey 按幂等键查询已有扣减日志（用于重复请求时取回首次记录）。
// 未找到返回 (nil, nil)。
func (r *EntitlementConsumeLogRepository) FindByIdempotencyKey(ctx context.Context, db *gorm.DB, key string) (*model.EntitlementConsumeLog, error) {
	var log model.EntitlementConsumeLog
	err := db.WithContext(ctx).Where("idempotency_key = ?", key).First(&log).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &log, nil
}
