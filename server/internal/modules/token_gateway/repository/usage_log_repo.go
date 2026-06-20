package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

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
