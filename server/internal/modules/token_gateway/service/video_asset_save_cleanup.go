package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	assetmodel "molin/server/internal/modules/asset/model"
	assetrepo "molin/server/internal/modules/asset/repository"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type VideoSaveCleanupPolicy struct{ Purpose, Version string }
type VideoSaveCleanupReply struct {
	SaveID     string `json:"save_id"`
	Aborted    bool   `json:"aborted"`
	Idempotent bool   `json:"idempotent"`
}

// 仅处理有持久化到期依据的未发布副本；不是用户删除接口，也不启动任何后台Worker。
func (s *VideoHTTPService) CleanupVideoAssetSave(ctx context.Context, id string, owner repository.VideoOwner, policy VideoSaveCleanupPolicy) (*VideoSaveCleanupReply, error) {
	if s == nil || s.db == nil || !cleanupAdapterPresent(s.saveStore) || !s.saveStore.SupportsSynchronousDeletion() || policy.Purpose != "non_commercial_test_fixture" || !videoIntentPolicyCode.MatchString(policy.Version) {
		return nil, ErrVideoSaveUnavailable
	}
	if owner.UserID == 0 || owner.ProjectID == 0 || !videoBillingPublicID.MatchString(id) {
		return nil, repository.ErrVideoTaskNotFound
	}
	operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var repeated bool
	err := retryVideoBillingTransaction(operationCtx, func() error {
		return s.db.WithContext(operationCtx).Transaction(func(tx *gorm.DB) error {
			op, _, sources, ent, parent, err := s.loadSaveCleanupTx(tx, id, owner)
			if err != nil {
				return err
			}
			if op.Status == "aborted" {
				repeated = true
				return verifyVideoSaveCleanupTx(operationCtx, tx, op, s.saveStore)
			}
			if op.Status == "cleanup_pending" {
				return validateVideoSaveCleanupIntent(op, policy.Version)
			}
			if op.Status != "copying" && op.Status != "copy_failed" {
				return ErrVideoSaveConflict
			}
			reason, eligible, ok := videoSaveCleanupEligibility(sources, ent, parent, time.Now().UTC())
			if !ok {
				return ErrVideoSaveConflict
			}
			now := time.Now().UTC()
			updates := map[string]any{"status": "cleanup_pending", "version_no": op.VersionNo + 1, "cleanup_policy_version": policy.Version, "cleanup_reason": reason, "cleanup_eligible_at": eligible, "cleanup_started_at": now}
			changed := tx.Model(&videoAssetSave{}).Where("task_id=? AND public_id=? AND status=? AND version_no=?", op.TaskID, op.PublicID, op.Status, op.VersionNo).Updates(updates)
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return ErrVideoSaveConflict
			}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	if repeated {
		return &VideoSaveCleanupReply{SaveID: id, Aborted: true, Idempotent: true}, nil
	}
	// 已提交清理意图阻止保存重启；保留数据库锁直到同步适配器返回，取消不能让旧复制绕过目标围栏。
	dbCtx, dbCancel := context.WithTimeout(context.WithoutCancel(ctx), 35*time.Second)
	defer dbCancel()
	err = s.db.WithContext(dbCtx).Transaction(func(tx *gorm.DB) error {
		if err := operationCtx.Err(); err != nil {
			return err
		}
		op, plan, _, ent, parent, err := s.loadSaveCleanupTx(tx, id, owner)
		if err != nil {
			return err
		}
		if op.Status == "aborted" {
			repeated = true
			return verifyVideoSaveCleanupTx(operationCtx, tx, op, s.saveStore)
		}
		if op.Status != "cleanup_pending" {
			return ErrVideoSaveConflict
		}
		if err := validateVideoSaveCleanupIntent(op, policy.Version); err != nil {
			return err
		}
		for _, p := range plan {
			ref := video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}
			gone, err := s.saveStore.VerifyDeleted(operationCtx, ref)
			if err != nil {
				return ErrVideoSaveUnavailable
			}
			if gone {
				continue
			}
			meta, headErr := s.saveStore.Head(operationCtx, ref)
			if headErr != nil && !errors.Is(headErr, video.ErrVideoObjectNotFound) {
				return ErrVideoSaveUnavailable
			}
			if headErr == nil && (meta.Ref != ref || meta.SHA256 != p.SHA256 || meta.SizeBytes != p.Size) {
				return ErrVideoMediaProtected
			}
			// 不存在但尚无围栏的目标也须执行Delete，阻止旧执行者稍后把它创建出来。
			if err := s.saveStore.Delete(operationCtx, ref); err != nil {
				return ErrVideoSaveUnavailable
			}
			gone, err = s.saveStore.VerifyDeleted(operationCtx, ref)
			if err != nil || !gone {
				return ErrVideoSaveUnavailable
			}
		}
		if err := operationCtx.Err(); err != nil {
			return err
		}
		if ent.QuotaReserved.LessThan(op.QuotaAmount) {
			return ErrVideoSaveUnavailable
		}
		if err := assetrepo.NewEntitlementRepository(tx).ReleaseQuota(dbCtx, tx, ent.ID, op.QuotaAmount); err != nil {
			return err
		}
		remark := videoSaveCleanupRemark(op)
		status := parent.Status
		if err := assetrepo.NewEventRepository(tx).Create(dbCtx, &assetmodel.AssetEvent{AssetID: parent.ID, UserID: owner.UserID, EventType: "video_save_aborted", BeforeStatus: &status, AfterStatus: &status, Remark: &remark, CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
		now := time.Now().UTC()
		proof := videoSaveCleanupProof(op)
		changed := tx.Model(&videoAssetSave{}).Where("task_id=? AND public_id=? AND status='cleanup_pending' AND version_no=?", op.TaskID, op.PublicID, op.VersionNo).Updates(map[string]any{"status": "aborted", "version_no": op.VersionNo + 1, "cleanup_finished_at": now, "cleanup_proof_sha256": proof})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoSaveConflict
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	return &VideoSaveCleanupReply{SaveID: id, Aborted: true, Idempotent: repeated}, nil
}

// 沿原Task/财务/源资产/存储父资产/权益顺序持锁，过期或暂停不妨碍释放原预占，但不能解除保全。
func (s *VideoHTTPService) loadSaveCleanupTx(tx *gorm.DB, id string, owner repository.VideoOwner) (*videoAssetSave, []videoAssetSaveItem, []model.AIImageAsset, *assetmodel.UserEntitlement, *assetmodel.UserAsset, error) {
	fail := func(err error) (*videoAssetSave, []videoAssetSaveItem, []model.AIImageAsset, *assetmodel.UserEntitlement, *assetmodel.UserAsset, error) {
		return nil, nil, nil, nil, nil, err
	}
	var identity struct{ PublicID string }
	if err := tx.Table("ai_video_asset_saves s").Select("t.public_id").Joins("JOIN ai_gateway_tasks t ON t.id=s.task_id AND t.user_id=s.user_id AND t.project_id=s.project_id AND t.request_id=s.request_id").Where("s.public_id=? AND s.user_id=? AND s.project_id=? AND s.api_key_id <=> ?", id, owner.UserID, owner.ProjectID, owner.APIKeyID).Take(&identity).Error; err != nil {
		return fail(videoAccessReadError(err, repository.ErrVideoTaskNotFound))
	}
	task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, identity.PublicID, owner)
	if err != nil {
		return fail(err)
	}
	var op videoAssetSave
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=? AND public_id=?", task.ID, id).Take(&op).Error; err != nil {
		return fail(err)
	}
	if !sameVideoSaveOwner(&op, task, owner) || op.PublicID != id || op.Status == "completed" || op.SavedUserAssetID != nil || op.CompletedAt != nil {
		return fail(ErrVideoSaveConflict)
	}
	var published int64
	if err := tx.Table("user_assets").Where("business_instance_id=?", op.PublicID).Count(&published).Error; err != nil {
		return fail(err)
	}
	if published != 0 {
		return fail(ErrVideoMediaProtected)
	}
	if err := verifyMediaDeleteFinancial(tx, task, owner); err != nil {
		return fail(err)
	}
	var sources []model.AIImageAsset
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=? AND request_id=?", task.ID, task.RequestID).Order("id").Find(&sources).Error; err != nil {
		return fail(err)
	}
	if len(sources) != 6 {
		return fail(ErrVideoMediaProtected)
	}
	for _, a := range sources {
		if a.UserID != owner.UserID || a.ProjectID != owner.ProjectID || a.LegalHold || a.DisputeStatus == model.AIImageDisputeOpen || a.LifecycleState == model.AIImageAssetQuarantined {
			return fail(ErrVideoMediaProtected)
		}
	}
	plan, err := decodeVideoSavePlan(&op)
	if err != nil {
		return fail(err)
	}
	rootID := ""
	for _, p := range plan {
		if p.Role == "content" {
			rootID = p.PublicID
		}
	}
	if err := matchVideoSaveSources(plan, sources, rootID, false); err != nil {
		return fail(err)
	}
	var entID struct{ AssetID uint64 }
	if err := tx.Table("user_entitlements").Select("asset_id").Where("id=? AND user_id=? AND product_id=?", op.StorageEntitlementID, owner.UserID, op.StorageProductID).Take(&entID).Error; err != nil {
		return fail(ErrVideoSaveUnavailable)
	}
	var parent assetmodel.UserAsset
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND product_id=?", entID.AssetID, owner.UserID, op.StorageProductID).Take(&parent).Error; err != nil {
		return fail(ErrVideoSaveUnavailable)
	}
	var ent assetmodel.UserEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND product_id=? AND asset_id=?", op.StorageEntitlementID, owner.UserID, op.StorageProductID, parent.ID).Take(&ent).Error; err != nil {
		return fail(ErrVideoSaveUnavailable)
	}
	if ent.QuotaUnit == nil || *ent.QuotaUnit != op.QuotaUnit || ent.QuotaUsed.IsNegative() || ent.QuotaReserved.IsNegative() {
		return fail(ErrVideoSaveUnavailable)
	}
	var held struct{ Amount decimal.Decimal }
	if err := tx.Table("ai_video_asset_saves").Select("COALESCE(SUM(quota_amount),0) AS amount").Where("storage_entitlement_id=? AND status IN ('copying','copy_failed','cleanup_pending')", ent.ID).Scan(&held).Error; err != nil {
		return fail(err)
	}
	if ent.QuotaReserved.LessThan(held.Amount) || (op.Status != "aborted" && ent.QuotaReserved.LessThan(op.QuotaAmount)) {
		return fail(ErrVideoSaveUnavailable)
	}
	return &op, plan, sources, &ent, &parent, nil
}

func videoSaveCleanupEligibility(sources []model.AIImageAsset, ent *assetmodel.UserEntitlement, parent *assetmodel.UserAsset, now time.Time) (string, time.Time, bool) {
	for _, a := range sources {
		if !a.ExpiresAt.After(now) {
			return "source_expired", a.ExpiresAt, true
		}
	}
	for _, until := range []*time.Time{ent.ExpiresAt, parent.ExpiresAt} {
		if until != nil && !until.After(now) {
			return "entitlement_expired", *until, true
		}
	}
	return "", time.Time{}, false
}

func validateVideoSaveCleanupIntent(op *videoAssetSave, version string) error {
	if op.CleanupPolicyVersion == nil || *op.CleanupPolicyVersion != version || op.CleanupReason == nil || (*op.CleanupReason != "source_expired" && *op.CleanupReason != "entitlement_expired") || op.CleanupEligibleAt == nil || op.CleanupStartedAt == nil || op.CleanupEligibleAt.After(*op.CleanupStartedAt) || op.CleanupStartedAt.After(time.Now().UTC()) {
		return ErrVideoSaveConflict
	}
	return nil
}
func videoSaveCleanupProof(op *videoAssetSave) string {
	return videoBillingDigest(fmt.Sprintf("video-save-cleanup:%s:%s:%s:%s", op.PublicID, op.PlanSHA256, op.QuotaAmount.StringFixed(6), op.QuotaUnit))
}
func videoSaveCleanupRemark(op *videoAssetSave) string {
	return "视频未发布副本清理完成:" + op.PublicID
}

// aborted和摘要不是对象删除的替代证明，重放和原结果删除仍逐目标核对不可复活标记。
func verifyVideoSaveCleanupTx(ctx context.Context, tx *gorm.DB, op *videoAssetSave, store VideoContentStore) error {
	checker, ok := store.(interface {
		VerifyDeleted(context.Context, video.VideoObjectRef) (bool, error)
	})
	if !ok || !cleanupAdapterPresent(checker) || op.Status != "aborted" || op.CleanupPolicyVersion == nil || validateVideoSaveCleanupIntent(op, *op.CleanupPolicyVersion) != nil || op.CleanupFinishedAt == nil || op.CleanupFinishedAt.Before(*op.CleanupStartedAt) || op.CleanupProofSHA256 == nil || *op.CleanupProofSHA256 != videoSaveCleanupProof(op) {
		return ErrVideoSaveUnavailable
	}
	plan, err := decodeVideoSavePlan(op)
	if err != nil {
		return err
	}
	for _, p := range plan {
		gone, err := checker.VerifyDeleted(ctx, video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey})
		if err != nil || !gone {
			return ErrVideoSaveUnavailable
		}
	}
	var events int64
	if err := tx.Table("asset_events").Where("user_id=? AND event_type='video_save_aborted' AND remark=? AND asset_id=(SELECT asset_id FROM user_entitlements WHERE id=? AND user_id=?)", op.UserID, videoSaveCleanupRemark(op), op.StorageEntitlementID, op.UserID).Count(&events).Error; err != nil {
		return err
	}
	if events != 1 {
		return ErrVideoSaveUnavailable
	}
	return nil
}
