package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// Complete的工作租约只保护同一会话；慢IO在事务外执行，发布前重查当前鉴权、期限和版本围栏。
func (s *VideoUploadService) Complete(ctx context.Context, caller VideoCaller, id, key string) (*VideoUploadReply, error) {
	if s == nil {
		return nil, ErrVideoUploadUnavailable
	}
	if !videoHTTPIdempotency.MatchString(key) {
		return nil, ErrVideoUploadInvalid
	}
	hash := videoBillingDigest("upload_complete\x00" + key)
	var record videoUploadRecord
	claimed := false
	expired := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.ownerForSession(ctx, tx, caller, id)
		if err != nil {
			return err
		}
		record, err = s.load(tx, owner, id, true)
		if err != nil {
			return err
		}
		if record.control.CompleteKeyHash != nil && *record.control.CompleteKeyHash != hash {
			return ErrVideoUploadConflict
		}
		if record.session.Status == "completed" {
			return nil
		}
		if !videoUploadActive(record.session.Status) {
			return ErrVideoUploadConflict
		}
		now := s.now().UTC()
		if !record.session.ExpiresAt.After(now) {
			expired = true
			return s.advance(tx, &record, "expired", map[string]any{"lease_until": nil, "cleanup_pending": true})
		}
		if record.session.Status == "verifying" && record.control.LeaseUntil != nil && record.control.LeaseUntil.After(now) {
			return nil
		}
		until := now.Add(2 * time.Minute)
		if err := s.advance(tx, &record, "verifying", map[string]any{"complete_key_hash": hash, "lease_until": until}); err != nil {
			return err
		}
		record.control.CompleteKeyHash = &hash
		record.control.LeaseUntil = &until
		claimed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if expired {
		_ = s.cleanup(ctx, caller, record)
		return nil, ErrVideoUploadConflict
	}
	if !claimed {
		return record.reply(s.now(), true), nil
	}
	workCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	snapshot, err := s.options.Store.Seal(workCtx, record.target(), videoUploadMaxBytes)
	if err != nil {
		return nil, s.failClaim(ctx, caller, record, ErrVideoUploadUnavailable)
	}
	if snapshot == nil || uint64(len(snapshot.Bytes)) != record.session.SizeBytes || len(snapshot.Bytes) == 0 || int64(len(snapshot.Bytes)) > videoUploadMaxBytes || snapshot.MIMEType != record.session.MIMEType || videoPayloadSHA256(snapshot.Bytes) != record.control.ExpectedSHA256 || !validVideoUploadVersion(snapshot.ETag, snapshot.VersionID) {
		return nil, s.failClaim(ctx, caller, record, ErrVideoUploadInvalid)
	}
	normalized, err := s.normalizer.Normalize(workCtx, video.ReferenceImageInput{Filename: "reference" + record.control.FileExtension, DeclaredMIME: record.session.MIMEType, Body: bytes.NewReader(snapshot.Bytes)})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, video.ErrReferenceImageBusy) {
			return nil, s.failClaim(ctx, caller, record, ErrVideoUploadUnavailable)
		}
		return nil, s.failClaim(ctx, caller, record, ErrVideoUploadInvalid)
	}
	if normalized.Width < 640 || normalized.Height < 640 {
		return nil, s.failClaim(ctx, caller, record, ErrVideoUploadInvalid)
	}
	if err := s.options.Safety.AssessReference(workCtx, normalized); err != nil {
		return nil, s.failClaim(ctx, caller, record, err)
	}
	if err := s.options.Store.PutNormalized(workCtx, record.target(), normalized.Bytes, normalized.NormalizedSHA256); err != nil {
		if errors.Is(err, ErrVideoUploadConflict) {
			return nil, s.failClaim(ctx, caller, record, ErrVideoUploadConflict)
		}
		return nil, s.failClaim(ctx, caller, record, ErrVideoUploadUnavailable)
	}
	err = s.db.WithContext(workCtx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.ownerForSession(workCtx, tx, caller, id)
		if err != nil {
			return err
		}
		current, err := s.load(tx, owner, id, true)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if current.session.Status != "verifying" || current.control.VersionNo != record.control.VersionNo || current.control.LeaseUntil == nil || !current.control.LeaseUntil.After(now) || !current.session.ExpiresAt.After(now) {
			return ErrVideoUploadConflict
		}
		mime, size, width, height, policy := "image/png", normalized.SizeBytes, uint32(normalized.Width), uint32(normalized.Height), s.options.ModerationPolicyVersion
		asset := model.AIGatewayInputAsset{PublicID: current.control.InputPublicID, UserID: owner.UserID, ProjectID: owner.ProjectID, SourceType: current.session.SourceType, UploadSessionID: &current.session.ID, OriginalSHA256: normalized.OriginalSHA256, NormalizedSHA256: &normalized.NormalizedSHA256, Bucket: &current.control.NormalizedBucket, ObjectKey: &current.control.NormalizedKey, MIMEType: &mime, SizeBytes: &size, Width: &width, Height: &height, ModerationPolicyVersion: &policy, ModerationStatus: model.AIModerationPassed, VersionNo: 1, LifecycleState: model.AIInputAssetReady, ExpiresAt: now.Add(currentVideoRetentionPolicy.InputBound), CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&asset).Error; err != nil {
			return err
		}
		updates := map[string]any{"status": "completed", "final_input_asset_id": asset.ID, "completed_at": now, "updated_at": now}
		if snapshot.ETag != "" {
			updates["source_etag"] = snapshot.ETag
		}
		if snapshot.VersionID != "" {
			updates["source_version_id"] = snapshot.VersionID
		}
		changed := tx.Model(&model.AIUploadSession{}).Where("id=? AND status='verifying'", current.session.ID).Updates(updates)
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoUploadConflict
		}
		current.session.Status = "completed"
		if err := s.advance(tx, &current, "completed", map[string]any{"lease_until": nil, "cleanup_pending": false}); err != nil {
			return err
		}
		// 发布与当前权限/工作期限一致；任何尾部失败整体回滚输入、会话和控制版本。
		if err := s.access.AuthorizeSubjectTx(workCtx, tx, owner, s.now().UTC()); err != nil {
			return err
		}
		if !record.control.LeaseUntil.After(s.now()) || !record.session.ExpiresAt.After(s.now()) {
			return ErrVideoUploadConflict
		}
		record = current
		return nil
	})
	if err != nil {
		return nil, s.failClaim(ctx, caller, record, err)
	}
	return record.reply(s.now(), false), nil
}

func validVideoUploadVersion(etag, version string) bool {
	if etag == "" && version == "" {
		return false
	}
	for _, v := range []string{etag, version} {
		if len(v) > 191 || strings.ContainsAny(v, "\x00\r\n") || strings.Contains(v, "://") {
			return false
		}
	}
	return true
}

// 只有仍拥有有效工作围栏的失败者可以标记拒绝并清理；旧worker不得删除新worker发布的输入。
func (s *VideoUploadService) failClaim(ctx context.Context, caller VideoCaller, claim videoUploadRecord, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	marked := false
	// 鉴权读取不可用不等于撤权；发布写入的死锁/锁超时也不能把合法输入永久拒绝。
	// 只认定明确的临时数据库错误，不把唯一键、外键或安全约束失败当成可恢复内容。
	var mysqlError *mysqlDriver.MySQLError
	databaseRetryable := errors.As(cause, &mysqlError) && (mysqlError.Number == 1213 || mysqlError.Number == 1205)
	retryable := databaseRetryable || errors.Is(cause, ErrVideoAccessUnavailable) || errors.Is(cause, ErrVideoUploadUnavailable) || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, video.ErrReferenceImageBusy) || errors.Is(cause, video.ErrVideoModerationFailed)
	err := s.db.WithContext(cleanupCtx).Transaction(func(tx *gorm.DB) error {
		owner := repository.VideoOwner{UserID: claim.session.UserID, ProjectID: claim.session.ProjectID, APIKeyID: claim.session.APIKeyID}
		current, err := s.load(tx, owner, claim.session.PublicID, true)
		if err != nil {
			return err
		}
		if current.session.Status != "verifying" || current.control.VersionNo != claim.control.VersionNo || current.control.LeaseUntil == nil || !current.control.LeaseUntil.After(s.now()) {
			return nil
		}
		// 临时故障只使本次工作租约立即失效，保留封存原件和同一完成命令供安全重试。
		if retryable {
			return s.advance(tx, &current, "verifying", map[string]any{"lease_until": s.now().UTC(), "last_safe_error": "processing_unavailable"})
		}
		if err := s.advance(tx, &current, "rejected", map[string]any{"lease_until": nil, "cleanup_pending": true, "last_safe_error": "verification_failed"}); err != nil {
			return err
		}
		claim = current
		marked = true
		return nil
	})
	if err != nil {
		return ErrVideoUploadUnavailable
	}
	if marked {
		if err := s.cleanup(cleanupCtx, caller, claim); err != nil {
			return ErrVideoUploadUnavailable
		}
	}
	if errors.Is(cause, video.ErrVideoModerationRejected) {
		return video.ErrVideoModerationRejected
	}
	if errors.Is(cause, ErrVideoUploadInvalid) {
		return ErrVideoUploadInvalid
	}
	if errors.Is(cause, ErrVideoUploadConflict) {
		return ErrVideoUploadConflict
	}
	for _, accessError := range []error{ErrRealNameRequired, ErrVideoBillingAccess, ErrVideoCapabilityDenied, ErrVideoEntitlementDenied} {
		if errors.Is(cause, accessError) {
			return accessError
		}
	}
	return ErrVideoUploadUnavailable
}

// LoadReference只读取数据库所指向的私有规范化对象；上层仍复核归属、ready和hash/version。
func (s *VideoUploadService) LoadReference(ctx context.Context, asset model.AIGatewayInputAsset) (*video.NormalizedReferenceImage, error) {
	if s == nil || !videoHTTPInputReferenceable(asset, s.now().UTC()) {
		return nil, ErrVideoUploadUnavailable
	}
	return s.readReferenceObject(ctx, asset)
}

// 私有对象IO由普通ready检查或专用TaskInput事务授权后调用，不对外暴露存储定位参数。
func (s *VideoUploadService) readReferenceObject(ctx context.Context, asset model.AIGatewayInputAsset) (*video.NormalizedReferenceImage, error) {
	if s == nil || asset.Bucket == nil || asset.ObjectKey == nil || asset.NormalizedSHA256 == nil || asset.MIMEType == nil || asset.SizeBytes == nil || asset.Width == nil || asset.Height == nil {
		return nil, ErrVideoUploadUnavailable
	}
	data, err := s.options.Store.ReadNormalized(ctx, *asset.Bucket, *asset.ObjectKey, videoUploadMaxBytes)
	if err != nil || uint64(len(data)) != *asset.SizeBytes || videoPayloadSHA256(data) != *asset.NormalizedSHA256 {
		return nil, ErrVideoUploadUnavailable
	}
	return &video.NormalizedReferenceImage{Bytes: data, MIMEType: *asset.MIMEType, Width: int(*asset.Width), Height: int(*asset.Height), SizeBytes: *asset.SizeBytes, OriginalSHA256: asset.OriginalSHA256, NormalizedSHA256: *asset.NormalizedSHA256}, nil
}
