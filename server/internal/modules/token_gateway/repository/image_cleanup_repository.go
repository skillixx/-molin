package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

type ImageCleanupRepository struct {
	db *gorm.DB
}

func NewImageCleanupRepository(db *gorm.DB) *ImageCleanupRepository {
	return &ImageCleanupRepository{db: db}
}

// ListCleanupCandidates 只返回资金终态、无活动补偿、无legal hold且无开放争议的到期资产。
func (r *ImageCleanupRepository) ListCleanupCandidates(ctx context.Context, now time.Time, limit int) ([]model.AIImageAsset, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var items []model.AIImageAsset
	err := r.db.WithContext(ctx).Table("ai_gateway_assets AS assets").
		Select("assets.*").
		Joins("JOIN ai_requests AS requests ON requests.request_id = assets.request_id").
		Where(`assets.legal_hold = 0
AND assets.dispute_status <> ?
AND assets.deleted_at IS NULL
AND NOT EXISTS (
  SELECT 1 FROM ai_compensation_tasks AS compensation
  WHERE compensation.aggregate_id = assets.request_id
    AND compensation.status IN ('pending','running','retry','manual_review')
)
AND (
  (requests.billing_status = ? AND (
    (assets.lifecycle_state = ? AND assets.created_at <= ?) OR
    (assets.lifecycle_state = ? AND assets.expires_at <= ?) OR
    (assets.lifecycle_state = ? AND assets.updated_at <= ?) OR
    (assets.lifecycle_state = ? AND assets.updated_at <= ?)
  )) OR
  (requests.billing_status = ? AND assets.is_billable_output = 0 AND (
    (assets.lifecycle_state = ? AND assets.created_at <= ?) OR
    (assets.lifecycle_state = ? AND assets.updated_at <= ?) OR
    (assets.lifecycle_state = ? AND assets.updated_at <= ?)
  ))
)`,
			model.AIImageDisputeOpen,
			model.AIBillingReleased,
			model.AIImageAssetTemporary, now.Add(-24*time.Hour),
			model.AIImageAssetQuarantined, now,
			model.AIImageAssetDeleteFailed, now.Add(-10*time.Minute),
			model.AIImageAssetDeleting, now.Add(-10*time.Minute),
			model.AIBillingSettled,
			model.AIImageAssetTemporary, now.Add(-24*time.Hour),
			model.AIImageAssetDeleteFailed, now.Add(-10*time.Minute),
			model.AIImageAssetDeleting, now.Add(-10*time.Minute)).
		Order("assets.id ASC").Limit(limit).Find(&items).Error
	return items, err
}
