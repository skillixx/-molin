package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var ErrVideoInputDeleteConflict = errors.New("输入删除申请冲突")

// 删除申请回执不是正文删除证明；保留期或执行租约未结束时始终pending_delete。
type VideoInputDeletionReply struct {
	InputAssetID      string    `json:"input_asset_id"`
	LifecycleState    string    `json:"lifecycle_state"`
	VersionNo         uint64    `json:"version_no"`
	DeleteRequestedAt time.Time `json:"delete_requested_at"`
	MediaDeleted      bool      `json:"media_deleted"`
	Idempotent        bool      `json:"idempotent"`
}

func (s *VideoHTTPService) RequestInputDeletion(ctx context.Context, caller VideoCaller, id string, expectedVersion uint64, key string) (*VideoInputDeletionReply, error) {
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoAccessUnavailable
	}
	if expectedVersion == 0 || expectedVersion == math.MaxUint64 || !videoHTTPIdempotency.MatchString(key) {
		return nil, ErrVideoGenerationIntent
	}
	if !videoBillingPublicID.MatchString(id) {
		return nil, repository.ErrVideoInputNotFound
	}
	var reply *VideoInputDeletionReply
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if caller.APIKeyID == 0 && caller.ProjectID == 0 {
			var identity struct{ ProjectID uint64 }
			// 已接受命令的历史归属不依赖来源仍能生成；首次申请仍走既有可信来源资格。
			err := videoDeletionHistoryQuery(tx, caller, id).Select("d.project_id").Take(&identity).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = videoInputMetadataQuery(tx, caller, now).Select("inputs.project_id").Where("inputs.public_id=?", id).Take(&identity).Error
			}
			if err != nil {
				return videoAccessReadError(err, repository.ErrVideoInputNotFound)
			}
			caller.ProjectID = identity.ProjectID
		}
		owner, err := s.access.ResolveSubjectTx(ctx, tx, caller, now)
		if err != nil {
			return videoInputSubjectError(err)
		}
		caller.ProjectID = owner.ProjectID
		commandHash := videoBillingDigest(fmt.Sprintf("video-input-delete:%d:%d:%d:%s", owner.UserID, owner.ProjectID, caller.APIKeyID, key))
		var previous repository.VideoInputDeletionRequest
		historyErr := videoDeletionHistoryQuery(tx, caller, id).Select("d.*").Take(&previous).Error
		if historyErr == nil {
			var asset model.AIGatewayInputAsset
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND project_id=?", previous.InputAssetID, owner.UserID, owner.ProjectID).Take(&asset).Error; err != nil {
				return err
			}
			var err error
			reply, err = videoDeletionReplyTx(tx, asset, owner, expectedVersion, commandHash, true)
			if err != nil {
				return err
			}
			return videoInputSubjectError(s.access.AuthorizeSubjectTx(ctx, tx, owner, time.Now().UTC()))
		}
		if !errors.Is(historyErr, gorm.ErrRecordNotFound) {
			return historyErr
		}
		var visible model.AIGatewayInputAsset
		if err := videoInputMetadataQuery(tx, caller, now).Where("inputs.public_id=?", id).Take(&visible).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoInputNotFound)
		}
		// 同一主体和命令键跨输入也必须冲突，原键不含输入ID，不能被用于另建删除意图。
		asset, replay, err := repository.NewVideoInputAssetRepository(tx).RequestDeferredDelete(ctx, id, owner, expectedVersion, commandHash, now)
		if errors.Is(err, repository.ErrVideoInputConflict) || repository.IsDuplicateKeyForHandler(err) {
			return ErrVideoInputDeleteConflict
		}
		if err != nil {
			return err
		}
		reply, err = videoDeletionReplyTx(tx, *asset, owner, expectedVersion, commandHash, replay)
		if err != nil {
			return err
		}
		if err := s.access.AuthorizeSubjectTx(ctx, tx, owner, time.Now().UTC()); err != nil {
			return videoInputSubjectError(err)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return reply, err
}

// 只为删除回执提供历史身份路径，不扩大内容读取、新Quote或新绑定的来源资格。
func videoDeletionHistoryQuery(tx *gorm.DB, caller VideoCaller, id string) *gorm.DB {
	q := tx.Table("ai_video_input_deletion_requests d").Joins("JOIN ai_gateway_input_assets a ON a.id=d.input_asset_id AND a.user_id=d.user_id AND a.project_id=d.project_id").Where("a.public_id=? AND d.user_id=?", id, caller.UserID)
	if caller.ProjectID != 0 {
		q = q.Where("d.project_id=?", caller.ProjectID)
	}
	if caller.APIKeyID == 0 {
		return q.Where("d.api_key_id IS NULL")
	}
	return q.Where("d.api_key_id=?", caller.APIKeyID)
}

func videoDeletionReplyTx(tx *gorm.DB, asset model.AIGatewayInputAsset, owner repository.VideoOwner, version uint64, keyHash string, replay bool) (*VideoInputDeletionReply, error) {
	var d repository.VideoInputDeletionRequest
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("input_asset_id=? AND user_id=? AND project_id=?", asset.ID, owner.UserID, owner.ProjectID).Take(&d).Error; err != nil {
		return nil, err
	}
	if !cleanupOwnerKeyEqual(d.APIKeyID, owner.APIKeyID) {
		return nil, repository.ErrVideoInputNotFound
	}
	if d.OriginalVersion != version || d.CommandKeyHash != keyHash {
		return nil, ErrVideoInputDeleteConflict
	}
	if asset.LifecycleState == model.AIInputAssetPendingDelete {
		if !repository.VideoPendingDeletionMatches(&asset, d) {
			return nil, ErrVideoInputDeleteConflict
		}
		return videoCleanupReply(asset, d, false, replay), nil
	}
	if asset.LifecycleState == model.AIInputAssetDeleted {
		var proof videoInputCleanupFact
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("input_asset_id=?", asset.ID).Take(&proof).Error; err != nil {
			return nil, ErrVideoAccessUnavailable
		}
		if !videoCleanupFactMatches(asset, d, proof) {
			return nil, ErrVideoAccessUnavailable
		}
		return videoCleanupReply(asset, d, true, replay), nil
	}
	return nil, ErrVideoInputDeleteConflict
}
