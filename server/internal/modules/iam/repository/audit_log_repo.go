package repository

import (
	"context"

	"gorm.io/gorm"

	"molin/server/internal/modules/iam/model"
)

// AuditLogRepository 审计日志查询仓储层（只读，写入由各业务模块完成）。
type AuditLogRepository struct {
	db *gorm.DB
}

func NewAuditLogRepository(db *gorm.DB) *AuditLogRepository {
	return &AuditLogRepository{db: db}
}

// ListPaged 分页查询审计日志，支持按 module、action 关键字过滤，按 created_at 倒序。
func (r *AuditLogRepository) ListPaged(ctx context.Context, module, action string, offset, limit int) ([]model.AuditLog, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.AuditLog{})
	// 按模块过滤（精确匹配）
	if module != "" {
		query = query.Where("module = ?", module)
	}
	// 按 action 过滤（精确匹配）
	if action != "" {
		query = query.Where("action = ?", action)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var logs []model.AuditLog
	if err := query.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}
