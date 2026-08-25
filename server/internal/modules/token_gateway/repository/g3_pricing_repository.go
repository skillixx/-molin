package repository

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrActivePriceNotFound        = errors.New("有效价格版本不存在")
	ErrPriceVersionNotPublishable = errors.New("价格版本不满足发布条件")
	ErrPriceWindowOverlap         = errors.New("价格生效区间重叠")
)

// G3PricingRepository 只读取已发布价格；G3 不开放管理 API，避免绕过后续 G5 发布门禁。
type G3PricingRepository struct {
	db *gorm.DB
}

func NewG3PricingRepository(db *gorm.DB) *G3PricingRepository { return &G3PricingRepository{db: db} }

// PublishApprovedVersion 是 G3 唯一价格发布入口；发布后只允许暂停，不提供原地改价能力。
func (r *G3PricingRepository) PublishApprovedVersion(ctx context.Context, versionID uint64, publishedAt time.Time) error {
	if r == nil || r.db == nil || versionID == 0 {
		return ErrPriceVersionNotPublishable
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var version model.AIPriceVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&version, versionID).Error; err != nil {
			return err
		}
		if version.Status != model.AIPriceApproved || version.ApprovedBy == nil || version.ApprovedAt == nil ||
			version.PricePurpose != "commercial" ||
			version.Currency != "CNY" || !version.ExchangeRate.Equal(decimal.NewFromInt(1)) || !version.CostExpiresAt.After(publishedAt) ||
			(version.ExpiresAt != nil && !version.ExpiresAt.After(version.EffectiveAt)) {
			return ErrPriceVersionNotPublishable
		}
		// 同模型所有发布事务争抢同一数据库行，保证多节点并发发布也只能串行检查时间区间。
		if err := tx.Exec(`INSERT INTO ai_price_model_locks(logical_model_code) VALUES(?)
			ON DUPLICATE KEY UPDATE updated_at=updated_at`, version.LogicalModelCode).Error; err != nil {
			return err
		}
		var lockedModel string
		if err := tx.Raw("SELECT logical_model_code FROM ai_price_model_locks WHERE logical_model_code = ? FOR UPDATE", version.LogicalModelCode).
			Scan(&lockedModel).Error; err != nil || lockedModel == "" {
			return ErrPriceVersionNotPublishable
		}
		var skus []model.AIPriceSKU
		if err := tx.Where("price_version_id = ?", version.ID).Order("meter_type ASC, id ASC").Find(&skus).Error; err != nil {
			return err
		}
		if len(skus) != 4 {
			return ErrPriceVersionNotPublishable
		}
		requiredMeters := map[string]bool{"input_tokens": false, "output_tokens": false, "cached_tokens": false, "reasoning_tokens": false}
		for _, sku := range skus {
			seen, expected := requiredMeters[sku.MeterType]
			if !expected || seen || sku.Currency != "CNY" || sku.Scale.LessThanOrEqual(decimal.Zero) ||
				sku.SaleUnitPrice.LessThanOrEqual(decimal.Zero) || sku.CostUnitPrice.LessThan(decimal.Zero) {
				return ErrPriceVersionNotPublishable
			}
			margin := sku.SaleUnitPrice.Sub(sku.CostUnitPrice).Div(sku.SaleUnitPrice)
			if margin.LessThan(version.MinMarginRate) {
				return ErrPriceVersionNotPublishable
			}
			requiredMeters[sku.MeterType] = true
		}
		query := tx.Model(&model.AIPriceVersion{}).
			Where("id <> ? AND logical_model_code = ? AND status = ? AND (expires_at IS NULL OR expires_at > ?)",
				version.ID, version.LogicalModelCode, model.AIPriceActive, version.EffectiveAt)
		if version.ExpiresAt != nil {
			query = query.Where("effective_at < ?", *version.ExpiresAt)
		}
		var overlap int64
		if err := query.Count(&overlap).Error; err != nil {
			return err
		}
		if overlap != 0 {
			return ErrPriceWindowOverlap
		}
		result := tx.Model(&model.AIPriceVersion{}).Where("id = ? AND status = ?", version.ID, model.AIPriceApproved).
			Updates(map[string]interface{}{"status": model.AIPriceActive, "published_at": publishedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrPriceVersionNotPublishable
		}
		return nil
	})
}

// FindActiveVersion 返回指定时刻唯一生效的价格及 SKU；多版本重叠按配置错误失败关闭。
func (r *G3PricingRepository) FindActiveVersion(ctx context.Context, modelCode string, at time.Time) (*model.AIPriceVersion, []model.AIPriceSKU, error) {
	var versions []model.AIPriceVersion
	var skus []model.AIPriceSKU
	// 版本和 SKU 必须来自同一个一致性读事务，避免与价格发布时间窗交错形成混合快照。
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("logical_model_code = ? AND status = ? AND effective_at <= ? AND (expires_at IS NULL OR expires_at > ?)", modelCode, model.AIPriceActive, at, at).
			Order("effective_at DESC, id DESC").Limit(2).Find(&versions).Error; err != nil {
			return err
		}
		if len(versions) != 1 {
			return ErrActivePriceNotFound
		}
		return tx.Where("price_version_id = ?", versions[0].ID).Order("meter_type ASC, id ASC").Find(&skus).Error
	})
	if err != nil {
		return nil, nil, err
	}
	return &versions[0], skus, nil
}

// SuspendVersionTx 在发现预占不足等 P0 财务异常时停止该价格版本继续接单。
func (r *G3PricingRepository) SuspendVersionTx(tx *gorm.DB, versionID uint64, reason string) error {
	result := tx.Model(&model.AIPriceVersion{}).
		Where("id = ? AND status = ?", versionID, model.AIPriceActive).
		Updates(map[string]interface{}{"status": model.AIPriceSuspended, "suspended_reason": reason})
	return result.Error
}
