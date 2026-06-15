package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/billing/model"
)

// PaymentRepository 支付回调记录数据访问层。
type PaymentRepository struct {
	db *gorm.DB
}

// NewPaymentRepository 创建支付回调仓库实例。
func NewPaymentRepository(db *gorm.DB) *PaymentRepository {
	return &PaymentRepository{db: db}
}

// Upsert 幂等写入支付回调记录（provider + provider_trade_no 联合唯一，重复时只更新 notify_body）。
// 注意：不能更新 status 字段，否则已处理的回调（processed）会被重置为 received，导致重复入账。
func (r *PaymentRepository) Upsert(ctx context.Context, callback *model.PaymentCallback) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "provider"},
			{Name: "provider_trade_no"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"notify_body", "updated_at"}),
	}).Create(callback).Error
}

// MarkIgnored 将尚未处理的回调标记为 ignored（B-01 金额不匹配场景）。
// 仅当当前 status 不为 processed 时更新，避免覆盖已成功入账的记录。
func (r *PaymentRepository) MarkIgnored(ctx context.Context, provider, providerTradeNo string) error {
	return r.db.WithContext(ctx).Model(&model.PaymentCallback{}).
		Where("provider = ? AND provider_trade_no = ? AND status <> ?", provider, providerTradeNo, "processed").
		Update("status", "ignored").Error
}

// IsProcessed 检查支付回调是否已处理完成（status = processed）。
func (r *PaymentRepository) IsProcessed(ctx context.Context, provider, providerTradeNo string) bool {
	var count int64
	r.db.WithContext(ctx).Model(&model.PaymentCallback{}).
		Where("provider = ? AND provider_trade_no = ? AND status = ?", provider, providerTradeNo, "processed").
		Count(&count)
	return count > 0
}

// MarkProcessed 在事务内将支付回调标记为已处理。
func (r *PaymentRepository) MarkProcessed(tx *gorm.DB, provider, providerTradeNo string) error {
	now := time.Now()
	return tx.Model(&model.PaymentCallback{}).
		Where("provider = ? AND provider_trade_no = ?", provider, providerTradeNo).
		Updates(map[string]interface{}{
			"status":       "processed",
			"processed_at": now,
		}).Error
}

// FindByProviderTradeNo 按第三方流水号查询回调记录。
func (r *PaymentRepository) FindByProviderTradeNo(ctx context.Context, provider, providerTradeNo string) (*model.PaymentCallback, error) {
	var callback model.PaymentCallback
	if err := r.db.WithContext(ctx).Where("provider = ? AND provider_trade_no = ?", provider, providerTradeNo).
		First(&callback).Error; err != nil {
		return nil, err
	}
	return &callback, nil
}

// AdminListAll 管理员分页查询支付回调记录。
func (r *PaymentRepository) AdminListAll(ctx context.Context, provider, status string, offset, limit int) ([]model.PaymentCallback, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.PaymentCallback{})
	if provider != "" {
		query = query.Where("provider = ?", provider)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var callbacks []model.PaymentCallback
	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&callbacks).Error; err != nil {
		return nil, 0, err
	}
	return callbacks, total, nil
}
