package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func loadVideoImport(tx *gorm.DB, claim videoInputImportRecord) (videoInputImportRecord, error) {
	var r videoInputImportRecord
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("input_asset_id=? AND user_id=? AND project_id=?", claim.InputAssetID, claim.UserID, claim.ProjectID).Take(&r).Error
	return r, err
}

// 发布事务重新锁定来源和命令，不使用IO之前的权限或版本；任何失败一起回滚规范化字段与完成回执。
func (s *VideoInputImportService) publish(ctx context.Context, caller VideoCaller, claim videoInputImportRecord, image video.NormalizedReferenceImage) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.access.ResolveSubjectTx(ctx, tx, caller, s.now().UTC())
		if err != nil {
			return videoInputSubjectError(err)
		}
		caller.ProjectID = owner.ProjectID
		source, err := s.source(tx, caller, claim.SourcePublicID)
		if err != nil {
			return err
		}
		if !importSourceMatches(claim, source) {
			return ErrVideoImportConflict
		}
		r, err := loadVideoImport(tx, claim)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if r.Status != "processing" || r.VersionNo != claim.VersionNo || r.LeaseUntil == nil || !r.LeaseUntil.After(now) || !r.ExpiresAt.After(now) {
			return ErrVideoImportConflict
		}
		var input model.AIGatewayInputAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND project_id=?", r.InputAssetID, r.UserID, r.ProjectID).Take(&input).Error; err != nil {
			return err
		}
		if input.LifecycleState != model.AIInputAssetNormalizing || input.VersionNo != 1 || input.LegalHold || input.DeleteRequestedAt != nil || input.SourceGatewayAssetID == nil || *input.SourceGatewayAssetID != r.SourceAssetID || !input.ExpiresAt.After(now) {
			return ErrVideoImportConflict
		}
		changed := tx.Model(&model.AIGatewayInputAsset{}).Where("id=? AND version_no=1 AND lifecycle_state='normalizing'", input.ID).Updates(map[string]any{
			"normalized_sha256": image.NormalizedSHA256, "bucket": r.NormalizedBucket, "object_key": r.NormalizedKey, "mime_type": "image/png", "size_bytes": image.SizeBytes, "width": image.Width, "height": image.Height,
			"moderation_policy_version": s.options.ModerationPolicyVersion, "moderation_status": "passed", "lifecycle_state": "moderating", "version_no": 2, "updated_at": now,
		})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoImportConflict
		}
		// 首次ready才冻结完整输入留存期；原24小时处理命令期限不变，完成重放不再进入此发布分支。
		changed = tx.Model(&model.AIGatewayInputAsset{}).Where("id=? AND version_no=2 AND lifecycle_state='moderating'", input.ID).Updates(map[string]any{"lifecycle_state": "ready", "version_no": 3, "expires_at": now.Add(7 * 24 * time.Hour), "updated_at": now})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoImportConflict
		}
		if err := s.update(tx, &r, map[string]any{"status": "completed", "lease_until": nil, "cleanup_pending": false, "reserved_bytes": image.SizeBytes}); err != nil {
			return err
		}
		if err := s.access.AuthorizeSubjectTx(ctx, tx, owner, s.now().UTC()); err != nil {
			return err
		}
		if !claim.LeaseUntil.After(s.now()) || !source.ExpiresAt.After(s.now()) {
			return ErrVideoImportConflict
		}
		return nil
	})
}

// 提交结果未知时先读取同一回执。已完成或被新租约接管时绝不清理；临时失败只到期本次租约。
func (s *VideoInputImportService) fail(ctx context.Context, claim videoInputImportRecord, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	cleanup := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := loadVideoImport(tx, claim)
		if err != nil {
			return err
		}
		if r.Status != "processing" || r.VersionNo != claim.VersionNo {
			return nil
		}
		if importRetryable(cause) {
			return s.update(tx, &r, map[string]any{"lease_until": s.now().UTC(), "last_safe_error": "processing_unavailable"})
		}
		var input model.AIGatewayInputAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&input, r.InputAssetID).Error; err != nil {
			return err
		}
		if input.LifecycleState == model.AIInputAssetNormalizing && !input.LegalHold {
			moderation := "error"
			if errors.Is(cause, video.ErrVideoModerationRejected) {
				moderation = "rejected"
			}
			changed := tx.Model(&model.AIGatewayInputAsset{}).Where("id=? AND version_no=?", input.ID, input.VersionNo).Updates(map[string]any{"lifecycle_state": "rejected", "moderation_status": moderation, "version_no": input.VersionNo + 1, "updated_at": s.now().UTC()})
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return ErrVideoImportConflict
			}
		}
		if err := s.update(tx, &r, map[string]any{"status": "rejected", "lease_until": nil, "cleanup_pending": true, "last_safe_error": "source_or_verification_failed"}); err != nil {
			return err
		}
		claim = r
		cleanup = true
		return nil
	})
	if err != nil {
		return ErrVideoImportUnavailable
	}
	if cleanup {
		if err := s.cleanup(ctx, claim); err != nil {
			return ErrVideoImportUnavailable
		}
	}
	if importRetryable(cause) {
		return ErrVideoImportUnavailable
	}
	return cause
}

func (s *VideoInputImportService) cleanup(ctx context.Context, claim videoInputImportRecord) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// 先证明回执已终止，且目标未被保全或发布；不删除原来源对象。
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := loadVideoImport(tx, claim)
		if err != nil {
			return err
		}
		if r.Status != "rejected" {
			return ErrVideoImportConflict
		}
		var input model.AIGatewayInputAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&input, r.InputAssetID).Error; err != nil {
			return err
		}
		if input.LegalHold {
			return ErrVideoImportConflict
		}
		// 保全解除后，原拒绝命令可收口自己的未发布目标；不能修改已形成规范化快照或隔离输入。
		if input.LifecycleState == model.AIInputAssetNormalizing && input.NormalizedSHA256 == nil && input.Bucket == nil && input.ObjectKey == nil && input.SourceGatewayAssetID != nil && *input.SourceGatewayAssetID == r.SourceAssetID && input.UserID == r.UserID && input.ProjectID == r.ProjectID {
			changed := tx.Model(&model.AIGatewayInputAsset{}).Where("id=? AND version_no=? AND legal_hold=0 AND lifecycle_state='normalizing' AND normalized_sha256 IS NULL", input.ID, input.VersionNo).Updates(map[string]any{"lifecycle_state": "rejected", "moderation_status": "error", "version_no": input.VersionNo + 1, "updated_at": s.now().UTC()})
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return ErrVideoImportConflict
			}
			input.LifecycleState = model.AIInputAssetRejected
		}
		if input.LifecycleState != model.AIInputAssetRejected {
			return ErrVideoImportConflict
		}
		if r.CleanedAt != nil && !r.CleanupPending {
			return nil
		}
		// 围栏删除在持有同一输入锁时执行，保全不能在检查和删除间插入；此接口本身必须服从context限时。
		if err := s.options.Store.Discard(ctx, r.target()); err != nil {
			return ErrVideoImportUnavailable
		}
		return s.update(tx, &r, map[string]any{"cleanup_pending": false, "cleaned_at": s.now().UTC()})
	})
	return err
}

func (s *VideoInputImportService) LoadReference(ctx context.Context, asset model.AIGatewayInputAsset) (*video.NormalizedReferenceImage, error) {
	if s == nil || asset.SourceGatewayAssetID == nil || !videoHTTPInputReferenceable(asset, s.now().UTC()) {
		return nil, repository.ErrVideoInputUnavailable
	}
	return s.readReferenceObject(ctx, asset)
}

// 只读取已经由上层ready资格或已有TaskInput资格验证的规范化对象。
func (s *VideoInputImportService) readReferenceObject(ctx context.Context, asset model.AIGatewayInputAsset) (*video.NormalizedReferenceImage, error) {
	raw, err := s.options.Store.Read(ctx, VideoImportObject{*asset.Bucket, *asset.ObjectKey}, videoUploadMaxBytes)
	if err != nil {
		return nil, ErrVideoImportUnavailable
	}
	if uint64(len(raw)) != *asset.SizeBytes || videoPayloadSHA256(raw) != *asset.NormalizedSHA256 {
		return nil, ErrVideoImportInvalid
	}
	return &video.NormalizedReferenceImage{Bytes: raw, MIMEType: *asset.MIMEType, SizeBytes: *asset.SizeBytes, Width: int(*asset.Width), Height: int(*asset.Height), OriginalSHA256: asset.OriginalSHA256, NormalizedSHA256: *asset.NormalizedSHA256}, nil
}
