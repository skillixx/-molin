package repository

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

// UsageQueryFilter 用量日志分页查询过滤条件（零值不参与过滤）。
// 用户端只填 UserID + Model + Start/End；管理端可额外填 APIKeyID 并按任意 UserID 查询。
type UsageQueryFilter struct {
	UserID           uint64     // 按用户过滤（0 表示不过滤，仅管理端可不填）
	APIKeyID         *uint64    // 按平台 API Key（sk）过滤（nil 表示不过滤）
	LogicalModelCode string     // 按逻辑模型名过滤（空表示不过滤）
	Start            *time.Time // created_at >= Start（nil 表示不限）
	End              *time.Time // created_at <= End（nil 表示不限）
}

// UsageLogRepository 用量与计费日志数据访问层。
type UsageLogRepository struct {
	db *gorm.DB
}

// NewUsageLogRepository 创建用量日志仓库实例。
func NewUsageLogRepository(db *gorm.DB) *UsageLogRepository {
	return &UsageLogRepository{db: db}
}

// Create 写入一条用量日志（request_id 唯一键兜底幂等）。
func (r *UsageLogRepository) Create(ctx context.Context, log *model.TokenUsageLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

// UpdateSaleAmountByRequestID 按 request_id 回填本次实扣金额/额度到 sale_amount（S2-丁5，修 M1 P3）。
// 结算阶段（postpaid 钱包实扣 / prepaid 扣套餐额度）确定金额后调用；best-effort，失败由上层记日志。
func (r *UsageLogRepository) UpdateSaleAmountByRequestID(ctx context.Context, requestID string, saleAmount decimal.Decimal) error {
	return r.db.WithContext(ctx).
		Model(&model.TokenUsageLog{}).
		Where("request_id = ?", requestID).
		Update("sale_amount", saleAmount).Error
}

// ListPagedByUser 分页查询某用户的用量日志，支持 logicalModelCode、status 过滤（空字符串不过滤）。
// 返回扁平分页二元组 (items, total)。
func (r *UsageLogRepository) ListPagedByUser(ctx context.Context, userID uint64, logicalModelCode, status string, offset, limit int) ([]model.TokenUsageLog, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.TokenUsageLog{}).Where("user_id = ?", userID)
	if logicalModelCode != "" {
		query = query.Where("logical_model_code = ?", logicalModelCode)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.TokenUsageLog
	if err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListPagedByFilter 按过滤条件分页查询用量日志，供用户端「我的用量」与管理端「全量用量」共用。
// 返回扁平分页二元组 (items, total)。
func (r *UsageLogRepository) ListPagedByFilter(ctx context.Context, f UsageQueryFilter, offset, limit int) ([]model.TokenUsageLog, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.TokenUsageLog{})
	if f.UserID != 0 {
		query = query.Where("user_id = ?", f.UserID)
	}
	if f.APIKeyID != nil {
		query = query.Where("api_key_id = ?", *f.APIKeyID)
	}
	if f.LogicalModelCode != "" {
		query = query.Where("logical_model_code = ?", f.LogicalModelCode)
	}
	if f.Start != nil {
		query = query.Where("created_at >= ?", *f.Start)
	}
	if f.End != nil {
		query = query.Where("created_at <= ?", *f.End)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.TokenUsageLog
	if err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListPagedByAPIKey 分页查询某平台 API Key（sk）的用量日志，为按 sk 计费统计预留。
// 返回扁平分页二元组 (items, total)。
func (r *UsageLogRepository) ListPagedByAPIKey(ctx context.Context, apiKeyID uint64, offset, limit int) ([]model.TokenUsageLog, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.TokenUsageLog{}).Where("api_key_id = ?", apiKeyID)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.TokenUsageLog
	if err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
