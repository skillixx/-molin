package service

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

// GORMVideoInputSnapshotResolver 只返回当前调用方拥有、审核通过、ready且未过期的不可变输入快照。
type GORMVideoInputSnapshotResolver struct {
	db  *gorm.DB
	now func() time.Time
}

func NewGORMVideoInputSnapshotResolver(db *gorm.DB) (*GORMVideoInputSnapshotResolver, error) {
	if db == nil {
		return nil, ErrVideoInputMismatch
	}
	return &GORMVideoInputSnapshotResolver{db: db, now: time.Now}, nil
}

func (r *GORMVideoInputSnapshotResolver) ResolveReadyInput(ctx context.Context, userID, projectID, apiKeyID uint64, inputAssetID string) (*VideoQuoteInputBinding, error) {
	if r == nil || r.db == nil || userID == 0 || projectID == 0 || strings.TrimSpace(inputAssetID) == "" {
		return nil, ErrVideoInputMismatch
	}
	var asset model.AIGatewayInputAsset
	now := r.now().UTC()
	query := r.db.WithContext(ctx).Table("ai_gateway_input_assets AS inputs").Select("inputs.*").
		Where("inputs.public_id=? AND inputs.user_id=? AND inputs.project_id=? AND inputs.lifecycle_state=? AND inputs.moderation_status=? AND inputs.expires_at>? AND inputs.delete_requested_at IS NULL AND inputs.pending_delete_at IS NULL AND inputs.deleted_at IS NULL",
			strings.TrimSpace(inputAssetID), userID, projectID, model.AIInputAssetReady, model.AIModerationPassed, now)
	query = scopeTrustedVideoInputSource(query, apiKeyID, now)
	if err := query.First(&asset).Error; err != nil {
		return nil, ErrVideoInputMismatch
	}
	if asset.ID == 0 || asset.NormalizedSHA256 == nil || !lowerHex64.MatchString(*asset.NormalizedSHA256) || asset.VersionNo == 0 {
		return nil, ErrVideoInputMismatch
	}
	return &VideoQuoteInputBinding{InternalID: asset.ID, InputAssetID: asset.PublicID, NormalizedSHA256: *asset.NormalizedSHA256, Version: asset.VersionNo}, nil
}

// scopeTrustedVideoInputSource 把API Key归属和GeneratedImageAsset当前可用性绑定到每次可信读取。
func scopeTrustedVideoInputSource(query *gorm.DB, apiKeyID uint64, now time.Time) *gorm.DB {
	uploadPredicate := "s.api_key_id IS NULL"
	generatedPredicate := "source_task.api_key_id IS NULL"
	if apiKeyID != 0 {
		uploadPredicate = "s.api_key_id=?"
		generatedPredicate = "source_task.api_key_id=?"
	}
	condition := `(
(inputs.upload_session_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM ai_upload_sessions s
  WHERE s.id=inputs.upload_session_id AND s.user_id=inputs.user_id AND s.project_id=inputs.project_id
    AND s.status='completed' AND s.final_input_asset_id=inputs.id AND ` + uploadPredicate + `
)) OR
(inputs.source_gateway_asset_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM ai_gateway_assets source_asset
  JOIN ai_gateway_tasks source_task ON source_task.id=source_asset.task_id
  WHERE source_asset.id=inputs.source_gateway_asset_id
    AND source_asset.user_id=inputs.user_id AND source_asset.project_id=inputs.project_id
    AND source_asset.modality='image' AND source_asset.lifecycle_state='available'
    AND source_asset.moderation_status='passed' AND source_asset.explicit_label_status='applied'
    AND source_asset.implicit_label_status='applied' AND source_asset.expires_at>?
    AND source_asset.deleted_at IS NULL AND source_asset.media_deleted_at IS NULL
    AND source_asset.dispute_status<>'open'
    AND source_task.capability='image.generate' AND source_task.operation IS NULL
    AND ` + generatedPredicate + `
)))`
	if apiKeyID != 0 {
		return query.Where(condition, apiKeyID, now, apiKeyID)
	}
	return query.Where(condition, now)
}
