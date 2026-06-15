package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/finance_consumer/dto"
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

// ListPaged 按过滤条件分页查询消费记录，返回当前页数据与总数。
// 过滤维度：user_id / product_id / usage_type / created_from / created_to。
// 注意：user_id 由 service/handler 控制——F2 强制写入登录用户 ID，F3 为可选过滤，
// 仓库层只负责「filter.UserID > 0 即按其过滤」，不感知越权语义。
func (r *ConsumptionRepository) ListPaged(ctx context.Context, filter dto.ConsumptionRecordFilter, offset, limit int) ([]model.ProductConsumptionRecord, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.ProductConsumptionRecord{})
	if filter.UserID > 0 {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.ProductID > 0 {
		query = query.Where("product_id = ?", filter.ProductID)
	}
	if filter.UsageType != "" {
		query = query.Where("usage_type = ?", filter.UsageType)
	}
	if filter.CreatedFrom != nil {
		query = query.Where("created_at >= ?", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		query = query.Where("created_at <= ?", *filter.CreatedTo)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var list []model.ProductConsumptionRecord
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC, id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
