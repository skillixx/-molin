package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/shopspring/decimal"
	"strconv"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func savedVideoRole(role string) bool {
	switch role {
	case "content", "cover", "preview", "thumbnail", "derived":
		return true
	}
	return false
}

// 长期读取只排除已证明的临时生命周期变化；归属、hash、树关系、安全版本与原财务必须重新核验。
func (s *VideoHTTPService) withSavedReadTx(ctx context.Context, caller VideoCaller, userAssetID uint64, role string, read func(context.Context, *gorm.DB, *model.AIImageAsset) error) error {
	if s == nil || s.db == nil || s.access == nil || !cleanupAdapterPresent(s.saveStore) || len(s.downloadSecret) != 32 {
		return ErrVideoContentUnavailable
	}
	if userAssetID == 0 || caller.UserID == 0 || !savedVideoRole(role) {
		return repository.ErrVideoTaskNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := revalidateVideoReadCredential(ctx, caller); err != nil {
			return err
		}
		var identity struct{ PublicID string }
		q := videoTaskOwnerQuery(tx, caller).Joins("JOIN ai_video_asset_saves s ON s.task_id=t.id AND s.request_id=t.request_id AND s.user_id=t.user_id AND s.project_id=t.project_id")
		if err := q.Select("t.public_id").Where("s.saved_user_asset_id=? AND s.status='completed'", userAssetID).Take(&identity).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
		}
		task, owner, err := s.taskForPlatformTx(ctx, tx, caller, identity.PublicID, false)
		if err != nil {
			return err
		}
		if task.Operation == nil || task.Status != model.AIImageTaskSucceeded || task.BillingStatus != model.AIBillingSettled || task.DeliveryStatus != model.AIDeliveryAvailable {
			return repository.ErrVideoTaskNotFound
		}
		if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
			return err
		}
		var op videoAssetSave
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("task_id=? AND saved_user_asset_id=? AND status='completed'", task.ID, userAssetID).Take(&op).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
		}
		if !sameVideoSaveOwner(&op, task, owner) {
			return repository.ErrVideoTaskNotFound
		}
		if err := verifyMediaDeleteFinancial(tx, task, owner); err != nil {
			return err
		}
		if _, err := loadVideoSettlementMediaTx(tx, task, false, time.Now().UTC()); err != nil {
			if errors.Is(err, ErrVideoBillingState) {
				return ErrVideoMediaProtected
			}
			return ErrVideoAccessUnavailable
		}
		var sources []model.AIImageAsset
		if err := tx.Where("task_id=? AND request_id=?", task.ID, task.RequestID).Find(&sources).Error; err != nil {
			return err
		}
		var deletion int64
		if err := tx.Table("ai_video_media_deletions").Where("task_id=? AND user_id=? AND project_id=?", task.ID, owner.UserID, owner.ProjectID).Count(&deletion).Error; err != nil {
			return err
		}
		for _, a := range sources {
			if a.LegalHold || a.DisputeStatus == model.AIImageDisputeOpen || a.LifecycleState == model.AIImageAssetQuarantined {
				return ErrVideoMediaProtected
			}
			switch a.LifecycleState {
			case "available":
			case "expiring":
				if a.ExpiresAt.After(time.Now().UTC()) && deletion != 1 {
					return ErrVideoMediaProtected
				}
			case "deleting", "deleted", "delete_failed":
				if deletion != 1 {
					if _, _, err := verifySingleAssetDeletionTx(tx, task, &a); err != nil {
						return err
					}
				}
			default:
				return ErrVideoMediaProtected
			}
		}
		plan, err := verifyVideoSavedAssetRecordTx(tx, &op)
		if err != nil {
			return err
		}
		rootID := ""
		for _, p := range plan {
			if p.Role == "content" {
				rootID = p.PublicID
			}
		}
		if err := matchVideoSaveSources(plan, sources, rootID, false); err != nil {
			return err
		}
		until, err := savedVideoStorageDeadline(ctx, tx, &op, caller)
		if err != nil {
			return err
		}
		for _, p := range plan {
			if p.Role != role {
				continue
			}
			for _, a := range sources {
				if a.ID != p.AssetID {
					continue
				}
				if !videoPublicDownloadAsset(&a) {
					return ErrVideoMediaProtected
				}
				// 仅在内存构建已证明的长期对象视图，原短期资产的期限、位置和删除事实保持不变。
				a.Bucket = &p.TargetBucket
				a.ObjectKey = &p.TargetKey
				a.VersionNo = op.VersionNo
				a.ExpiresAt = until
				if err := read(ctx, tx, &a); err != nil {
					return err
				}
				// 回调已经通过签名和共享名额；完整副本确认失败时不交出内存中的媒体片段。
				if err := verifyVideoSavedObjects(ctx, plan, s.saveStore); err != nil {
					return err
				}
				if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
					return err
				}
				if _, err := savedVideoStorageDeadline(ctx, tx, &op, caller); err != nil {
					return err
				}
				return revalidateVideoReadCredential(ctx, caller)
			}
		}
		return repository.ErrVideoTaskNotFound
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func savedVideoStorageDeadline(ctx context.Context, tx *gorm.DB, op *videoAssetSave, caller VideoCaller) (time.Time, error) {
	now := time.Now().UTC()
	if op.StorageEntitlementType == "" {
		return time.Time{}, ErrVideoEntitlementDenied
	}
	var product int64
	if err := tx.Table("products").Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND product_type='storage' AND status='active'", op.StorageProductID).Count(&product).Error; err != nil {
		return time.Time{}, ErrVideoAccessUnavailable
	}
	if product != 1 {
		return time.Time{}, ErrVideoEntitlementDenied
	}
	if err := videoProductAccess(ctx, tx, newVideoFreshIAM(tx), op.UserID, op.StorageProductID, VideoModelContract{}, now); err != nil {
		return time.Time{}, err
	}
	var link struct{ AssetID uint64 }
	if err := tx.Table("user_entitlements").Select("asset_id").Where("id=? AND user_id=? AND product_id=?", op.StorageEntitlementID, op.UserID, op.StorageProductID).Take(&link).Error; err != nil {
		return time.Time{}, videoAccessReadError(err, ErrVideoEntitlementDenied)
	}
	var parent, saved assetmodel.UserAsset
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND product_id=?", link.AssetID, op.UserID, op.StorageProductID).Take(&parent).Error; err != nil {
		return time.Time{}, videoAccessReadError(err, ErrVideoEntitlementDenied)
	}
	if op.SavedUserAssetID == nil {
		return time.Time{}, ErrVideoMediaProtected
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=?", *op.SavedUserAssetID, op.UserID).Take(&saved).Error; err != nil {
		return time.Time{}, videoAccessReadError(err, ErrVideoMediaProtected)
	}
	var ent assetmodel.UserEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND asset_id=? AND product_id=?", op.StorageEntitlementID, op.UserID, parent.ID, op.StorageProductID).Take(&ent).Error; err != nil {
		return time.Time{}, videoAccessReadError(err, ErrVideoEntitlementDenied)
	}
	if ent.QuotaUnit == nil {
		return time.Time{}, ErrVideoEntitlementDenied
	}
	var allocated struct {
		Amount  decimal.Decimal
		Invalid int64
	}
	if err := tx.Table("ai_video_asset_saves").Select("COALESCE(SUM(quota_amount),0) AS amount, COALESCE(SUM(CASE WHEN BINARY quota_unit=BINARY ? AND BINARY storage_entitlement_type=BINARY ? THEN 0 ELSE 1 END),0) AS invalid", *ent.QuotaUnit, ent.EntitlementType).Where("storage_entitlement_id=? AND status='completed'", ent.ID).Scan(&allocated).Error; err != nil {
		return time.Time{}, ErrVideoAccessUnavailable
	}
	if allocated.Invalid != 0 || ent.EntitlementType != op.StorageEntitlementType || ent.QuotaUsed.LessThan(allocated.Amount) {
		return time.Time{}, ErrVideoEntitlementDenied
	}
	var key struct{ ExpiresAt *time.Time }
	if caller.APIKeyID != 0 {
		if err := tx.Table("api_keys").Select("expires_at").Where("id=? AND user_id=? AND status='active'", caller.APIKeyID, caller.UserID).Take(&key).Error; err != nil {
			return time.Time{}, videoAccessReadError(err, ErrVideoCapabilityDenied)
		}
	}
	if err := revalidateVideoReadCredential(ctx, caller); err != nil {
		return time.Time{}, err
	}
	now = time.Now().UTC()
	until := now.Add(15 * time.Minute)
	if parent.Status != "active" || saved.Status != "active" || ent.Status != "active" || ent.QuotaUnit == nil || *ent.QuotaUnit != op.QuotaUnit || ent.QuotaUsed.LessThan(op.QuotaAmount) {
		return time.Time{}, ErrVideoEntitlementDenied
	}
	for _, start := range []*time.Time{parent.StartedAt, saved.StartedAt, ent.StartedAt} {
		if start != nil && start.After(now) {
			return time.Time{}, ErrVideoEntitlementDenied
		}
	}
	for _, end := range []*time.Time{parent.ExpiresAt, saved.ExpiresAt, ent.ExpiresAt, key.ExpiresAt} {
		if end != nil {
			if !end.After(now) {
				return time.Time{}, ErrVideoEntitlementDenied
			}
			if end.Before(until) {
				until = *end
			}
		}
	}
	if caller.credential != nil && caller.credential.expiresAt.Before(until) {
		until = caller.credential.expiresAt
	}
	return until, nil
}

func (s *VideoHTTPService) savedVideoSignature(caller VideoCaller, userAssetID uint64, role string, a *model.AIImageAsset, expiry int64) string {
	mac := hmac.New(sha256.New, s.downloadSecret)
	fmt.Fprintf(mac, "molin-video-saved-download-v1\nGET\n/api/token/video-saved-assets/%d/%s/content\n%d\n%d\n%d\n%d\n%s\n%d\n%s\n%s\n%d", userAssetID, role, a.UserID, a.ProjectID, caller.APIKeyID, a.VersionNo, *a.SHA256, *a.SizeBytes, *a.Bucket, *a.ObjectKey, expiry)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *VideoHTTPService) SavedVideoDownloadURL(ctx context.Context, caller VideoCaller, id uint64, role string) (*VideoAssetDownloadURL, error) {
	var pinned model.AIImageAsset
	err := s.withSavedReadTx(ctx, caller, id, role, func(ctx context.Context, _ *gorm.DB, a *model.AIImageAsset) error {
		if err := checkVideoContentObject(ctx, s.saveStore, a); err != nil {
			return err
		}
		pinned = *a
		return nil
	})
	if err != nil {
		return nil, err
	}
	expires := pinned.ExpiresAt.Truncate(time.Second)
	if !expires.After(time.Now()) {
		return nil, ErrVideoContentUnavailable
	}
	path := fmt.Sprintf("/api/token/video-saved-assets/%d/%s/content", id, role)
	return &VideoAssetDownloadURL{AssetID: strconv.FormatUint(id, 10), ExpiresAt: expires, DownloadURL: path + "?expires=" + strconv.FormatInt(expires.Unix(), 10) + "&signature=" + s.savedVideoSignature(caller, id, role, &pinned, expires.Unix())}, nil
}

func (s *VideoHTTPService) GetSavedVideoContent(ctx context.Context, caller VideoCaller, id uint64, role, expiry, signature string) (*VideoContent, error) {
	if s == nil || s.db == nil || !cleanupAdapterPresent(s.saveStore) || len(s.downloadSecret) != 32 {
		return nil, ErrVideoContentUnavailable
	}
	seconds, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != expiry || !lowerHex64.MatchString(signature) {
		return nil, repository.ErrVideoTaskNotFound
	}
	expires := time.Unix(seconds, 0)
	var deadline atomic.Int64
	deadline.Store(expires.UnixNano())
	validate := func(ctx context.Context, a *model.AIImageAsset) error {
		if err := revalidateVideoReadCredential(ctx, caller); err != nil {
			return err
		}
		now := time.Now()
		if !expires.After(now) || expires.After(now.Add(15*time.Minute)) || !a.ExpiresAt.After(now) || !hmac.Equal([]byte(signature), []byte(s.savedVideoSignature(caller, id, role, a, seconds))) {
			return repository.ErrVideoTaskNotFound
		}
		for old := deadline.Load(); a.ExpiresAt.UnixNano() < old; old = deadline.Load() {
			if deadline.CompareAndSwap(old, a.ExpiresAt.UnixNano()) {
				break
			}
		}
		return nil
	}
	readTx := func(ctx context.Context, read func(context.Context, *gorm.DB, *model.AIImageAsset) error) error {
		return s.withSavedReadTx(ctx, caller, id, role, read)
	}
	content, err := s.videoContentCapability(ctx, s.saveStore, readTx, validate)
	if err != nil {
		return nil, err
	}
	renew := content.BeforeWrite
	content.BeforeWrite = func(ctx context.Context) (time.Time, error) {
		until, err := renew(ctx)
		if err != nil {
			return time.Time{}, err
		}
		end := time.Unix(0, deadline.Load())
		if !end.After(time.Now()) {
			return time.Time{}, ErrVideoContentUnavailable
		}
		if end.Before(until) {
			until = end
		}
		return until, nil
	}
	return content, nil
}
