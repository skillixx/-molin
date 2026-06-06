package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/finance_consumer/model"
)

// ConsumptionRepository 消费计费记录数据访问层（只追加写入）。
type ConsumptionRepository struct {
	db *gorm.DB
}

// NewConsumptionRepository 创建消费记录仓库实例。
func NewConsumptionRepository(db *gorm.DB) *ConsumptionRepository {
	return &ConsumptionRepository{db: db}
}

// FindByIdempotencyKey 按幂等键查询消费记录（用于幂等检查）。
func (r *ConsumptionRepository) FindByIdempotencyKey(ctx context.Context, key string) (*model.ProductConsumptionRecord, error) {
	var record model.ProductConsumptionRecord
	if err := r.db.WithContext(ctx).Where("idempotency_key = ?", key).First(&record).Error; err != nil {
		return nil, err
	}
	return &record, nil
}

// Create 在事务内追加写入一条消费记录。
func (r *ConsumptionRepository) Create(tx *gorm.DB, record *model.ProductConsumptionRecord) error {
	return tx.Create(record).Error
}
