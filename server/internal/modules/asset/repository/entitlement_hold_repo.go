package repository

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/asset/model"
)

// EntitlementHoldRepository 权益额度预占（hold）数据访问层。
type EntitlementHoldRepository struct {
	db *gorm.DB
}

// NewEntitlementHoldRepository 创建预占仓库实例。
func NewEntitlementHoldRepository(db *gorm.DB) *EntitlementHoldRepository {
	return &EntitlementHoldRepository{db: db}
}

// ErrDuplicateHold 幂等键冲突：该 idempotency_key 已存在（重复预占请求）。
var ErrDuplicateHold = errors.New("权益预占幂等键已存在")

// Create 在事务内创建一条预占记录（gorm 回填自增 ID）。
// idempotency_key 触发唯一键冲突（MySQL 1062）时返回 ErrDuplicateHold，供 service 识别重复请求。
func (r *EntitlementHoldRepository) Create(ctx context.Context, tx *gorm.DB, hold *model.EntitlementHold) error {
	if err := tx.WithContext(ctx).Create(hold).Error; err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrDuplicateHold
		}
		return err
	}
	return nil
}

// FindByIdempotencyKey 按幂等键查询预占记录（非事务，用于重复请求时取回首次记录）。
// 未找到返回 (nil, gorm.ErrRecordNotFound)。
func (r *EntitlementHoldRepository) FindByIdempotencyKey(ctx context.Context, key string) (*model.EntitlementHold, error) {
	var hold model.EntitlementHold
	if err := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&hold).Error; err != nil {
		return nil, err
	}
	return &hold, nil
}

// FindByIdempotencyKeyTx 在给定事务内按幂等键查询预占记录。
func (r *EntitlementHoldRepository) FindByIdempotencyKeyTx(ctx context.Context, tx *gorm.DB, key string) (*model.EntitlementHold, error) {
	var hold model.EntitlementHold
	if err := tx.WithContext(ctx).Where("idempotency_key = ?", key).First(&hold).Error; err != nil {
		return nil, err
	}
	return &hold, nil
}

// FindByIDForUpdate 在事务内加行锁查询预占记录（SELECT ... FOR UPDATE）。
// 必须在事务中调用，保证结算/释放阶段对同一 hold 的并发处理串行化。
func (r *EntitlementHoldRepository) FindByIDForUpdate(ctx context.Context, tx *gorm.DB, id uint64) (*model.EntitlementHold, error) {
	var hold model.EntitlementHold
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", id).First(&hold).Error; err != nil {
		return nil, err
	}
	return &hold, nil
}

// FindByIdempotencyKeyForUpdate 在事务内加行锁按幂等键查询预占记录（结算/释放用 idempotency_key 定位时）。
func (r *EntitlementHoldRepository) FindByIdempotencyKeyForUpdate(ctx context.Context, tx *gorm.DB, key string) (*model.EntitlementHold, error) {
	var hold model.EntitlementHold
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("idempotency_key = ?", key).First(&hold).Error; err != nil {
		return nil, err
	}
	return &hold, nil
}

// UpdateFields 在事务内更新 hold 指定字段。
func (r *EntitlementHoldRepository) UpdateFields(ctx context.Context, tx *gorm.DB, id uint64, updates map[string]interface{}) error {
	return tx.WithContext(ctx).Model(&model.EntitlementHold{}).Where("id = ?", id).Updates(updates).Error
}
