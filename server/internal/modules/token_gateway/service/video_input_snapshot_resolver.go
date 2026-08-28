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

func (r *GORMVideoInputSnapshotResolver) ResolveReadyInput(ctx context.Context, userID, projectID uint64, inputAssetID string) (*VideoQuoteInputBinding, error) {
	if r == nil || r.db == nil || userID == 0 || projectID == 0 || strings.TrimSpace(inputAssetID) == "" {
		return nil, ErrVideoInputMismatch
	}
	var asset model.AIGatewayInputAsset
	if err := r.db.WithContext(ctx).Where("public_id=? AND user_id=? AND project_id=? AND lifecycle_state=? AND moderation_status=? AND expires_at>?",
		strings.TrimSpace(inputAssetID), userID, projectID, model.AIInputAssetReady, model.AIModerationPassed, r.now().UTC()).First(&asset).Error; err != nil {
		return nil, ErrVideoInputMismatch
	}
	if asset.ID == 0 || asset.NormalizedSHA256 == nil || !lowerHex64.MatchString(*asset.NormalizedSHA256) || asset.VersionNo == 0 {
		return nil, ErrVideoInputMismatch
	}
	return &VideoQuoteInputBinding{InternalID: asset.ID, InputAssetID: asset.PublicID, NormalizedSHA256: *asset.NormalizedSHA256, Version: asset.VersionNo}, nil
}
