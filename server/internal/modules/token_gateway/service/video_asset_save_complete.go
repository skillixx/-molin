package service

import (
	"context"
	"time"

	"database/sql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	assetmodel "molin/server/internal/modules/asset/model"
	assetrepo "molin/server/internal/modules/asset/repository"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func (s *VideoHTTPService) finishVideoSave(ctx context.Context, caller VideoCaller, videoID, assetID, saveID string) (*VideoAssetSaveReply, error) {
	var result *VideoAssetSaveReply
	var copyFailure error
	err := retryVideoBillingTransaction(ctx, func() error {
		result = nil
		copyFailure = nil
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, owner, err := s.saveTaskTx(ctx, tx, caller, videoID)
			if err != nil {
				return err
			}
			var op videoAssetSave
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=? AND public_id=?", task.ID, saveID).Take(&op).Error; err != nil {
				return err
			}
			// 缺失的原权益类型不能用当前配置补证；旧未完成计划保持原状，不复制或发布新资产。
			if !sameVideoSaveOwner(&op, task, owner) || !matchesVideoSaveExecutionPolicy(&op, s.savePolicy) {
				return ErrVideoSaveConflict
			}
			if op.Status == "completed" {
				result, err = s.savedReplyTx(ctx, tx, caller, task, owner, &op, assetID, true)
				return err
			}
			if op.Status != "copying" && op.Status != "copy_failed" {
				return ErrVideoSaveConflict
			}
			assets, err := s.saveSourceTx(ctx, tx, task, owner)
			if err != nil {
				return err
			}
			plan, err := decodeVideoSavePlan(&op)
			if err != nil {
				return err
			}
			if err := matchVideoSaveSources(plan, assets, assetID, true); err != nil {
				return err
			}
			ent, err := s.saveEntitlementTx(ctx, tx, owner.UserID, op.StorageEntitlementID, op.QuotaAmount)
			if err != nil {
				return err
			}
			if ent.QuotaReserved.LessThan(op.QuotaAmount) {
				return ErrVideoSaveUnavailable
			}
			if op.Status == "copy_failed" {
				if err := transitionVideoSave(tx, &op, "copying", nil); err != nil {
					return err
				}
			}
			// 原Task与资产锁覆盖同步复制，其他保存或原媒体删除不能交错修改源；目标始终按已提交计划恢复。
			for _, p := range plan {
				target := video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}
				copied, err := s.saveStore.CopyImmutable(ctx, video.VideoObjectRef{Bucket: p.SourceBucket, ObjectKey: p.SourceKey}, target, p.SHA256, p.Size)
				if err == nil && (copied.Ref != target || copied.SHA256 != p.SHA256 || copied.SizeBytes != p.Size) {
					err = ErrVideoSaveUnavailable
				}
				if err == nil {
					var confirmed video.StoredVideoObject
					confirmed, err = s.saveStore.Head(ctx, target)
					if err == nil && (confirmed.Ref != target || confirmed.SHA256 != p.SHA256 || confirmed.SizeBytes != p.Size) {
						err = ErrVideoSaveUnavailable
					}
				}
				if err != nil {
					copyFailure = ErrVideoSaveUnavailable
					return transitionVideoSave(tx, &op, "copy_failed", nil)
				}
			}
			// 复制等待可能跨过权益、JWT或媒体期限；发布用户资产之前再次失败关闭，旧预占仍有计划归属。
			if err := s.saveAccessTx(ctx, tx, caller, task, owner); err != nil {
				return err
			}
			if _, err := s.saveSourceTx(ctx, tx, task, owner); err != nil {
				return err
			}
			if _, err := s.saveEntitlementTx(ctx, tx, owner.UserID, op.StorageEntitlementID, op.QuotaAmount); err != nil {
				return err
			}
			now := time.Now().UTC()
			// 原user_assets开始时间只有秒精度；立即生效须向下取整，防止数据库舍入到未来而短暂拒绝读取。
			startedAt := now.Truncate(time.Second)
			userAsset := assetmodel.UserAsset{UserID: owner.UserID, ProductID: op.StorageProductID, AssetType: "video_file", BusinessInstanceID: &op.PublicID, Status: "active", StartedAt: &startedAt}
			if err := assetrepo.NewAssetRepository(tx).Create(ctx, &userAsset); err != nil {
				return err
			}
			after := "active"
			remark := "视频独立副本保存完成"
			operator := owner.UserID
			if err := assetrepo.NewEventRepository(tx).Create(ctx, &assetmodel.AssetEvent{AssetID: userAsset.ID, UserID: owner.UserID, EventType: "created", AfterStatus: &after, OperatorID: &operator, Remark: &remark, CreatedAt: now}); err != nil {
				return err
			}
			if err := assetrepo.NewEntitlementRepository(tx).SettleQuota(ctx, tx, ent.ID, op.QuotaAmount, op.QuotaAmount); err != nil {
				return err
			}
			if err := transitionVideoSave(tx, &op, "completed", &userAsset.ID); err != nil {
				return err
			}
			if err := videoSaveCommitFence(ctx, tx, caller, task, ent.ID); err != nil {
				return err
			}
			result = &VideoAssetSaveReply{AssetID: assetID, VideoID: task.PublicID, RequestID: task.RequestID, UserAssetID: userAsset.ID, Status: "completed", SizeBytes: op.TotalBytes, Idempotent: false}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	if copyFailure != nil {
		return nil, copyFailure
	}
	return result, nil
}

// 写入等待也可能跨期；最终检查已锁实体的期限，失败时连同用户资产、事件、额度结转和completed一起回滚。
// 此处不重扫财务，避免为时间复验新增锁顺序；第一阶段计划和预占仍然保留。
func videoSaveCommitFence(ctx context.Context, tx *gorm.DB, caller VideoCaller, task *repository.VideoTaskRecord, entID uint64) error {
	var assets []struct{ ExpiresAt time.Time }
	if err := tx.Table("ai_gateway_assets").Select("expires_at").Where("task_id=? AND request_id=? AND user_id=? AND project_id=? AND lifecycle_state='available'", task.ID, task.RequestID, task.UserID, task.ProjectID).Find(&assets).Error; err != nil {
		return err
	}
	if len(assets) != 6 {
		return ErrVideoSaveConflict
	}
	var terms struct{ EntitlementExpiresAt, EntitlementStartedAt, AssetExpiresAt, AssetStartedAt *time.Time }
	if err := tx.Table("user_entitlements e").Select("e.expires_at AS entitlement_expires_at,e.started_at AS entitlement_started_at,a.expires_at AS asset_expires_at,a.started_at AS asset_started_at").Joins("JOIN user_assets a ON a.id=e.asset_id AND a.user_id=e.user_id AND a.product_id=e.product_id").Where("e.id=? AND e.user_id=? AND e.status='active' AND a.status='active'", entID, task.UserID).Take(&terms).Error; err != nil {
		return ErrVideoEntitlementDenied
	}
	var key struct{ ExpiresAt *time.Time }
	if caller.APIKeyID != 0 {
		if err := tx.Table("api_keys").Select("expires_at").Where("id=? AND user_id=? AND status='active'", caller.APIKeyID, task.UserID).Take(&key).Error; err != nil {
			return ErrVideoCapabilityDenied
		}
	}
	if err := revalidateVideoReadCredential(ctx, caller); err != nil {
		return err
	}
	now := time.Now().UTC()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, a := range assets {
		if !a.ExpiresAt.After(now) {
			return ErrVideoSaveConflict
		}
	}
	for _, until := range []*time.Time{terms.EntitlementExpiresAt, terms.AssetExpiresAt, key.ExpiresAt} {
		if until != nil && !until.After(now) {
			return ErrVideoSaveConflict
		}
	}
	for _, from := range []*time.Time{terms.EntitlementStartedAt, terms.AssetStartedAt} {
		if from != nil && from.After(now) {
			return ErrVideoSaveConflict
		}
	}
	return nil
}

func transitionVideoSave(tx *gorm.DB, op *videoAssetSave, status string, userAssetID *uint64) error {
	updates := map[string]any{"status": status, "version_no": op.VersionNo + 1}
	var completed *time.Time
	if status == "completed" {
		now := time.Now().UTC()
		completed = &now
		updates["saved_user_asset_id"] = userAssetID
		updates["completed_at"] = now
	}
	changed := tx.Model(&videoAssetSave{}).Where("task_id=? AND public_id=? AND version_no=? AND status=?", op.TaskID, op.PublicID, op.VersionNo, op.Status).Updates(updates)
	if changed.Error != nil {
		return changed.Error
	}
	if changed.RowsAffected != 1 {
		return ErrVideoSaveConflict
	}
	op.VersionNo++
	op.Status = status
	op.SavedUserAssetID = userAssetID
	op.CompletedAt = completed
	return nil
}

func matchVideoSaveSources(plan []videoAssetSaveItem, assets []model.AIImageAsset, rootID string, requireVersion bool) error {
	byID := map[uint64]model.AIImageAsset{}
	for _, a := range assets {
		byID[a.ID] = a
	}
	rootFound := false
	for _, p := range plan {
		a, ok := byID[p.AssetID]
		if !ok || a.PublicID != p.PublicID || a.AssetRole != p.Role || a.SHA256 == nil || a.SizeBytes == nil || a.Bucket == nil || a.ObjectKey == nil || *a.SHA256 != p.SHA256 || *a.SizeBytes != p.Size || *a.Bucket != p.SourceBucket || *a.ObjectKey != p.SourceKey || mediaMetadataSHA(a) != p.MetadataSHA256 || (requireVersion && a.VersionNo != p.VersionNo) {
			return ErrVideoSaveConflict
		}
		if p.Role == "content" {
			rootFound = p.PublicID == rootID
		}
	}
	if !rootFound {
		return repository.ErrVideoTaskNotFound
	}
	return nil
}

// 完成后原临时对象可以已删除；重放只核对持久化财务、安全元数据及独立长期对象，不复活源。
func (s *VideoHTTPService) savedReplyTx(ctx context.Context, tx *gorm.DB, caller VideoCaller, task *repository.VideoTaskRecord, owner repository.VideoOwner, op *videoAssetSave, rootID string, replay bool) (*VideoAssetSaveReply, error) {
	if !sameVideoSaveOwner(op, task, owner) || op.Status != "completed" || op.SavedUserAssetID == nil || op.PolicyVersion != s.savePolicy.Version {
		return nil, ErrVideoSaveConflict
	}
	if err := verifyMediaDeleteFinancial(tx, task, owner); err != nil {
		return nil, err
	}
	plan, err := decodeVideoSavePlan(op)
	if err != nil {
		return nil, err
	}
	var sources []model.AIImageAsset
	if err := tx.Where("task_id=? AND request_id=?", task.ID, task.RequestID).Find(&sources).Error; err != nil {
		return nil, err
	}
	// 原审核摘要不包含行政隔离与保全状态；历史保存成功不能替代当前六角色安全门禁。
	// 临时正文正常到期或删除不影响独立长期副本，因此这里不能强制源仍为available。
	if len(sources) != 6 {
		return nil, ErrVideoSaveUnavailable
	}
	for _, source := range sources {
		if source.LifecycleState == model.AIImageAssetQuarantined || source.LegalHold || source.DisputeStatus == model.AIImageDisputeOpen {
			return nil, ErrVideoSaveConflict
		}
	}
	if err := matchVideoSaveSources(plan, sources, rootID, false); err != nil {
		return nil, err
	}
	ent, err := s.saveEntitlementTx(ctx, tx, owner.UserID, op.StorageEntitlementID, op.QuotaAmount)
	if err != nil {
		return nil, err
	}
	if ent.QuotaUsed.LessThan(op.QuotaAmount) {
		return nil, ErrVideoSaveUnavailable
	}
	if err := verifyVideoSavedAssetTx(ctx, tx, op, s.saveStore); err != nil {
		return nil, err
	}
	if err := s.saveAccessTx(ctx, tx, caller, task, owner); err != nil {
		return nil, err
	}
	return &VideoAssetSaveReply{AssetID: rootID, VideoID: task.PublicID, RequestID: task.RequestID, UserAssetID: *op.SavedUserAssetID, Status: "completed", SizeBytes: op.TotalBytes, Idempotent: replay}, nil
}

// 原媒体删除必须确认长期资产关联及五份独立副本仍存在，不能把单个completed标志当作保存证明。
func verifyVideoSavedAssetTx(ctx context.Context, tx *gorm.DB, op *videoAssetSave, store VideoContentStore) error {
	plan, err := verifyVideoSavedAssetRecordTx(tx, op)
	if err != nil {
		return err
	}
	return verifyVideoSavedObjects(ctx, plan, store)
}

// 先核验本地保存事实，不访问存储；读取方必须完成当前权益、签名及并发名额检查后才做外部验证。
func verifyVideoSavedAssetRecordTx(tx *gorm.DB, op *videoAssetSave) ([]videoAssetSaveItem, error) {
	if op.Status != "completed" || op.SavedUserAssetID == nil || op.CompletedAt == nil {
		return nil, ErrVideoMediaProtected
	}
	var asset assetmodel.UserAsset
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND product_id=? AND asset_type='video_file' AND business_instance_id=? AND status='active' AND expires_at IS NULL", *op.SavedUserAssetID, op.UserID, op.StorageProductID, op.PublicID).Take(&asset).Error; err != nil {
		return nil, videoAccessReadError(err, ErrVideoMediaProtected)
	}
	var events int64
	if err := tx.Table("asset_events").Where("asset_id=? AND user_id=? AND event_type='created' AND operator_id=?", asset.ID, op.UserID, op.UserID).Count(&events).Error; err != nil {
		return nil, err
	}
	if events != 1 {
		return nil, ErrVideoMediaProtected
	}
	return decodeVideoSavePlan(op)
}

// 交付前仍核验全部五份目标；拆分调用顺序不能减少保存完整性要求。
func verifyVideoSavedObjects(ctx context.Context, plan []videoAssetSaveItem, store VideoContentStore) error {
	if store == nil || len(plan) != 5 {
		return ErrVideoMediaProtected
	}
	for _, p := range plan {
		ref := video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}
		meta, err := store.Head(ctx, ref)
		if err != nil || meta.Ref != ref || meta.SHA256 != p.SHA256 || meta.SizeBytes != p.Size {
			return ErrVideoMediaProtected
		}
	}
	return nil
}

func videoSaveProtectsDeletion(tx *gorm.DB, task *repository.VideoTaskRecord, store VideoContentStore) error {
	var attempts []videoAssetSave
	if err := tx.Where("task_id=?", task.ID).Order("attempt_no").Find(&attempts).Error; err != nil {
		return err
	}
	// 删除必须考虑全部历史，不能因先读到旧aborted就忽略新copying。
	for i := range attempts {
		op := &attempts[i]
		if op.UserID != task.UserID || op.ProjectID != task.ProjectID || op.RequestID != task.RequestID || !equalOptionalUint64(op.APIKeyID, task.APIKeyID) {
			return ErrVideoMediaProtected
		}
		if op.Status == "aborted" {
			if err := verifyVideoSaveCleanupTx(tx.Statement.Context, tx, op, store); err != nil {
				return err
			}
		} else if op.Status == "completed" {
			if err := verifyVideoSavedAssetTx(tx.Statement.Context, tx, op, store); err != nil {
				return err
			}
		} else {
			return ErrVideoMediaProtected
		}
	}
	return nil
}
