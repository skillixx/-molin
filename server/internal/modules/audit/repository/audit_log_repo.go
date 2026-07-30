package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/audit/model"
)

// AuditLogRepository 审计日志仓储层，提供读取和写入能力。
type AuditLogRepository struct {
	db *gorm.DB
}

func NewAuditLogRepository(db *gorm.DB) *AuditLogRepository {
	return &AuditLogRepository{db: db}
}

// Create 写入一条审计日志。
func (r *AuditLogRepository) Create(ctx context.Context, log *model.AuditLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

// CreateWithTx 使用调用方事务写入审计，使高风险业务结果与业务数据同生共死。
func (r *AuditLogRepository) CreateWithTx(ctx context.Context, tx *gorm.DB, log *model.AuditLog) error {
	if tx == nil {
		return gorm.ErrInvalidDB
	}
	return tx.WithContext(ctx).Create(log).Error
}

// ListPaged 分页查询审计日志，支持多维度过滤，按 created_at 倒序。
// D-82：新增 operatorID（按操作人过滤）、startTime/endTime（按时间范围过滤），
// 合规审计必备，同时对应 migration 中的 idx_audit_operator_id / idx_audit_module_action 索引。
func (r *AuditLogRepository) ListPaged(
	ctx context.Context,
	module, action string,
	operatorID uint64,
	startTime, endTime time.Time,
	offset, limit int,
) ([]model.AuditLog, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.AuditLog{})
	// 按模块过滤（精确匹配，走 idx_audit_module_action 索引）
	if module != "" {
		query = query.Where("module = ?", module)
	}
	// 按 action 过滤（精确匹配，走 idx_audit_module_action 索引）
	if action != "" {
		query = query.Where("action = ?", action)
	}
	// D-82：按操作人过滤（走 idx_audit_operator_id 索引）
	if operatorID > 0 {
		query = query.Where("operator_id = ?", operatorID)
	}
	// D-82：按时间范围过滤（走 idx_audit_created_at 索引）
	if !startTime.IsZero() {
		query = query.Where("created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		query = query.Where("created_at <= ?", endTime)
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
