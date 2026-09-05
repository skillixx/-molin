package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

var ErrVideoMediaRunning = errors.New("运行或待对账任务不能删除媒体")
var ErrVideoMediaProtected = errors.New("媒体受保护或删除事实不一致")
var ErrVideoMediaDeleteUnavailable = errors.New("媒体删除尚未确认")

// 删除边界必须提供同步墓碑确认；不允许用一次普通Head错误代表正文已消失。
type VideoMediaDeleteStore interface {
	VideoContentStore
	Delete(context.Context, video.VideoObjectRef) error
	SupportsSynchronousDeletion() bool
	VerifyDeleted(context.Context, video.VideoObjectRef) (bool, error)
}
type VideoDeleted struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Deleted   bool   `json:"deleted"`
	RequestID string `json:"-"`
}
type videoMediaDeletion struct {
	TaskID      uint64          `json:"-"`
	UserID      uint64          `json:"-"`
	ProjectID   uint64          `json:"-"`
	APIKeyID    *uint64         `json:"-"`
	RequestID   string          `json:"-"`
	Status      string          `json:"-"`
	VersionNo   uint64          `json:"-"`
	PlanJSON    json.RawMessage `json:"-"`
	PlanSHA256  string          `json:"-"`
	CreatedAt   time.Time       `json:"-"`
	CompletedAt *time.Time      `json:"-"`
}

func (videoMediaDeletion) TableName() string { return "ai_video_media_deletions" }

type videoMediaDeleteCommand struct {
	UserID, ProjectID, TaskID uint64
	APIKeyID                  *uint64
	CommandKeyHash            string
	CreatedAt                 time.Time
}

func (videoMediaDeleteCommand) TableName() string { return "ai_video_media_delete_commands" }

// 计划仅用于内部恢复，固定原对象及元数据摘要；不进入普通HTTP响应。
type videoMediaDeleteItem struct {
	AssetID         uint64 `json:"asset_id"`
	PublicID        string `json:"public_id"`
	Role            string `json:"role"`
	Delete          bool   `json:"delete"`
	PreDeleted      bool   `json:"pre_deleted,omitempty"`
	PreparedVersion uint64 `json:"prepared_version"`
	Bucket          string `json:"bucket"`
	ObjectKey       string `json:"object_key"`
	SHA256          string `json:"sha256"`
	Size            uint64 `json:"size"`
	MetadataSHA256  string `json:"metadata_sha256"`
}

func mediaMetadataSHA(a model.AIImageAsset) string {
	// 只排除本命令允许改变的生命周期字段，其余安全、规格、归属和期限均冻结。
	a.VersionNo = 0
	a.LifecycleState = ""
	a.UpdatedAt = time.Time{}
	a.DeletedAt = nil
	a.MediaDeletedAt = nil
	raw, _ := json.Marshal(a)
	return videoPayloadSHA256(raw)
}

func (s *VideoHTTPService) DeleteMedia(ctx context.Context, caller VideoCaller, id, key string) (*VideoDeleted, error) {
	return s.deleteMedia(ctx, caller, id, key, nil)
}

func (s *VideoHTTPService) deleteMedia(ctx context.Context, caller VideoCaller, id, key string, selected *videoAssetDeleteRequest) (*VideoDeleted, error) {
	retentionAt, retention := videoRetentionDeleteTime(ctx)
	// 准备阶段也会持锁读取Store，必须自行设定上限，不能依赖客户端主动断连。
	ctx, cancelPrepare := context.WithTimeout(ctx, 30*time.Second)
	defer cancelPrepare()
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoMediaDeleteUnavailable
	}
	if !videoHTTPIdempotency.MatchString(key) {
		return nil, ErrVideoGenerationIntent
	}
	// 先持久化隐藏意图；即使后续对象已删而确认提交失败，也不能重新公开旧Job。
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if !retention {
				if err := revalidateVideoReadCredential(ctx, caller); err != nil {
					return err
				}
			}
			task, owner, err := s.taskForMediaDeleteTx(ctx, tx, caller, id, retention)
			if err != nil {
				return err
			}
			if !videoG4TerminalStatus(task.Status) || (task.BillingStatus != model.AIBillingSettled && task.BillingStatus != model.AIBillingReleased) {
				return ErrVideoMediaRunning
			}
			if task.Operation == nil {
				return ErrVideoMediaProtected
			}
			if !retention {
				if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
					return err
				}
			}
			hash := videoBillingDigest(fmt.Sprintf("media-delete:%d:%d:%s", owner.UserID, owner.ProjectID, key))
			if selected != nil {
				selected.Existing, err = readAssetDeleteCommand(tx, task, owner, selected)
				if err != nil {
					return err
				}
				hash = videoBillingDigest(fmt.Sprintf("platform-root-delete:%d:%d:%s", owner.UserID, owner.ProjectID, key))
			}
			var command videoMediaDeleteCommand
			err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("user_id=? AND project_id=? AND command_key_hash=?", owner.UserID, owner.ProjectID, hash).Take(&command).Error
			if err == nil {
				if command.TaskID != task.ID || !equalOptionalUint64(command.APIKeyID, owner.APIKeyID) {
					return ErrVideoCancelConflict
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			} else {
				command = videoMediaDeleteCommand{UserID: owner.UserID, ProjectID: owner.ProjectID, TaskID: task.ID, APIKeyID: owner.APIKeyID, CommandKeyHash: hash, CreatedAt: time.Now().UTC()}
				if err := tx.Create(&command).Error; err != nil {
					if repository.IsDuplicateKeyForHandler(err) {
						return ErrVideoCancelConflict
					}
					return err
				}
			}
			var op videoMediaDeletion
			err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=?", task.ID).Take(&op).Error
			if err == nil {
				if selected != nil && !selected.Existing {
					return ErrVideoAssetDeleteConflict
				}
				return mediaDeletionOwner(op, task, owner)
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if selected != nil && selected.Existing {
				return ErrVideoMediaProtected
			}
			if err := verifyMediaDeleteFinancial(tx, task, owner); err != nil {
				return err
			}
			var assets []model.AIImageAsset
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=?", task.ID).Order("id").Find(&assets).Error; err != nil {
				return err
			}
			if selected != nil {
				matched := false
				for _, a := range assets {
					if a.ID == selected.AssetID && a.PublicID == selected.PublicID && a.AssetRole == "content" && a.ParentAssetID == nil && a.VersionNo == selected.Version {
						matched = true
					}
				}
				if !matched {
					return ErrVideoAssetDeleteConflict
				}
				if err := insertAssetDeleteCommand(tx, task, owner, selected); err != nil {
					return err
				}
			}
			if len(assets) > 0 && (task.Status != model.AIImageTaskSucceeded || task.DeliveryStatus != model.AIDeliveryAvailable) {
				return ErrVideoMediaProtected
			}
			if len(assets) > 0 && (s.mediaDeleteStore == nil || !s.mediaDeleteStore.SupportsSynchronousDeletion()) {
				return ErrVideoMediaDeleteUnavailable
			}
			if err := mediaDeleteProtection(tx, task, assets, s.saveStore); err != nil {
				return err
			}
			if retention && !videoRetentionAssetsEligible(assets, retentionAt) {
				return ErrVideoMediaProtected
			}
			if len(assets) > 0 {
				if _, err := loadVideoSettlementMediaTx(tx, task, false, time.Now().UTC()); err != nil {
					return ErrVideoMediaProtected
				}
			}
			plan := []videoMediaDeleteItem{}
			for _, asset := range assets {
				remove := asset.AssetRole != "moderation_copy"
				if remove && asset.LifecycleState == "deleted" {
					prior, _, err := verifySingleAssetDeletionTx(tx, task, &asset)
					if err != nil || prior.Status != "completed" {
						return ErrVideoMediaProtected
					}
					gone, err := s.mediaDeleteStore.VerifyDeleted(ctx, video.VideoObjectRef{Bucket: *asset.Bucket, ObjectKey: *asset.ObjectKey})
					if err != nil || !gone {
						return ErrVideoMediaDeleteUnavailable
					}
					plan = append(plan, videoMediaDeleteItem{AssetID: asset.ID, PublicID: asset.PublicID, Role: asset.AssetRole, Delete: true, PreDeleted: true, PreparedVersion: asset.VersionNo, Bucket: *asset.Bucket, ObjectKey: *asset.ObjectKey, SHA256: *asset.SHA256, Size: *asset.SizeBytes, MetadataSHA256: mediaMetadataSHA(asset)})
					continue
				}
				if asset.LifecycleState != model.AIImageAssetAvailable && asset.LifecycleState != model.AIImageAssetExpiring {
					return ErrVideoMediaProtected
				}
				if asset.Bucket == nil || asset.ObjectKey == nil || asset.SHA256 == nil || asset.SizeBytes == nil {
					return ErrVideoMediaProtected
				}
				meta, err := s.mediaDeleteStore.Head(ctx, video.VideoObjectRef{Bucket: *asset.Bucket, ObjectKey: *asset.ObjectKey})
				if err != nil || meta.Ref.Bucket != *asset.Bucket || meta.Ref.ObjectKey != *asset.ObjectKey || meta.SHA256 != *asset.SHA256 || meta.SizeBytes != *asset.SizeBytes {
					return ErrVideoMediaDeleteUnavailable
				}
				item := videoMediaDeleteItem{AssetID: asset.ID, PublicID: asset.PublicID, Role: asset.AssetRole, Delete: remove, Bucket: *asset.Bucket, ObjectKey: *asset.ObjectKey, SHA256: *asset.SHA256, Size: *asset.SizeBytes, MetadataSHA256: mediaMetadataSHA(asset)}
				if remove {
					for asset.LifecycleState != model.AIImageAssetDeleting {
						next := model.AIImageAssetDeleting
						if asset.LifecycleState == model.AIImageAssetAvailable {
							next = model.AIImageAssetExpiring
						}
						updated, err := repository.NewVideoOutputAssetRepository(tx, nil).TransitionLifecycle(ctx, asset.PublicID, owner, asset.VersionNo, next, time.Now().UTC())
						if err != nil {
							return err
						}
						asset = *updated
					}
				}
				item.PreparedVersion = asset.VersionNo
				plan = append(plan, item)
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			op = videoMediaDeletion{TaskID: task.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, RequestID: task.RequestID, Status: "deleting", VersionNo: 1, PlanJSON: raw, PlanSHA256: videoPayloadSHA256(raw), CreatedAt: time.Now().UTC()}
			if len(plan) == 0 {
				now := time.Now().UTC()
				op.Status = "completed"
				op.CompletedAt = &now
			}
			if err := tx.Create(&op).Error; err != nil {
				return err
			}
			if !retention {
				if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
					return err
				}
				return revalidateVideoReadCredential(ctx, caller)
			}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return s.executeMediaDelete(ctx, caller, id)
}

func mediaDeletionOwner(op videoMediaDeletion, task *repository.VideoTaskRecord, owner repository.VideoOwner) error {
	if op.TaskID != task.ID || op.UserID != owner.UserID || op.ProjectID != owner.ProjectID || op.RequestID != task.RequestID || !equalOptionalUint64(op.APIKeyID, owner.APIKeyID) {
		return ErrVideoMediaProtected
	}
	return nil
}

func verifyMediaDeleteFinancial(tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner) error {
	report, err := NewVideoReconciliationService(tx).Reconcile(tx.Statement.Context, task.PublicID, owner)
	if err != nil {
		return err
	}
	// 生命周期/到期不再代表金融不一致；其余G5检查一项也不能跳过，安全状态另行当前读。
	for name, passed := range report.Checks {
		if name != "output_asset" && !passed {
			return ErrVideoMediaProtected
		}
	}
	return nil
}

func mediaDeleteProtection(tx *gorm.DB, task *repository.VideoTaskRecord, assets []model.AIImageAsset, store VideoContentStore) error {
	if err := videoSaveProtectsDeletion(tx, task, store); err != nil {
		return err
	}
	ids := []string{task.PublicID}
	for _, a := range assets {
		if a.TaskID != task.ID || a.UserID != task.UserID || a.ProjectID != task.ProjectID || a.Modality != "video" || a.LegalHold || a.DisputeStatus == model.AIImageDisputeOpen || a.LifecycleState == model.AIImageAssetQuarantined {
			return ErrVideoMediaProtected
		}
		switch a.AssetRole {
		case "content", "cover", "preview", "thumbnail", "moderation_copy":
		case "derived":
			if a.Source != "derived" {
				return ErrVideoMediaProtected
			}
		default:
			return ErrVideoMediaProtected
		}
		ids = append(ids, a.PublicID)
	}
	// 保存协调尚未交付时保守拒绝已存在的业务引用，不能连带删除长期资产。
	var references int64
	if err := tx.Table("user_assets").Where("user_id=? AND business_instance_id IN ?", task.UserID, ids).Count(&references).Error; err != nil {
		return err
	}
	if references != 0 {
		return ErrVideoMediaProtected
	}
	return nil
}

func (s *VideoHTTPService) executeMediaDelete(ctx context.Context, caller VideoCaller, id string) (*VideoDeleted, error) {
	retentionAt, retention := videoRetentionDeleteTime(ctx)
	operation, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 存储取消后仍保留短暂事务期限完成失败标记，不提前释放安全锁给其他清理或保全操作。
	dbctx, dbCancel := context.WithTimeout(context.WithoutCancel(ctx), 35*time.Second)
	defer dbCancel()
	var result *VideoDeleted
	var storeFailure error
	err := s.db.WithContext(dbctx).Transaction(func(tx *gorm.DB) error {
		if !retention {
			if err := revalidateVideoReadCredential(operation, caller); err != nil {
				return err
			}
		}
		task, owner, err := s.taskForMediaDeleteTx(dbctx, tx, caller, id, retention)
		if err != nil {
			return err
		}
		// 准备事务已提交不代表执行仍获准；模型可在两阶段之间撤下原操作。
		if !retention {
			if !retention {
				if err := s.checkCurrentVideoDeleteAuthority(operation, tx, caller, task, owner); err != nil {
					return err
				}
			}
		}
		var op videoMediaDeletion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=?", task.ID).Take(&op).Error; err != nil {
			return err
		}
		if err := mediaDeletionOwner(op, task, owner); err != nil {
			return err
		}
		var plan []videoMediaDeleteItem
		decoder := json.NewDecoder(bytes.NewReader(op.PlanJSON))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&plan) != nil {
			return ErrVideoMediaProtected
		}
		raw, _ := json.Marshal(plan)
		if videoPayloadSHA256(raw) != op.PlanSHA256 {
			return ErrVideoMediaProtected
		}
		// 保持G5的Task→财务→资产锁序，完整性校验不能提前锁资产后再倒取财务锁。
		if err := verifyMediaDeleteFinancial(tx, task, owner); err != nil {
			return err
		}
		var actual []model.AIImageAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=?", task.ID).Order("id").Find(&actual).Error; err != nil {
			return err
		}
		if err := validateMediaDeletePlanShape(task, actual, plan); err != nil {
			return err
		}
		if op.Status == "completed" {
			for _, p := range plan {
				if !p.Delete {
					continue
				}
				var a model.AIImageAsset
				if err := tx.Where("id=? AND task_id=? AND user_id=? AND project_id=?", p.AssetID, task.ID, owner.UserID, owner.ProjectID).Take(&a).Error; err != nil {
					return err
				}
				if a.LifecycleState != "deleted" || a.MediaDeletedAt == nil || a.DeletedAt == nil || a.Bucket == nil || a.ObjectKey == nil || a.SHA256 == nil || *a.Bucket != p.Bucket || *a.ObjectKey != p.ObjectKey || *a.SHA256 != p.SHA256 {
					return ErrVideoMediaProtected
				}
			}
			if err := s.checkCurrentVideoDeleteAuthority(operation, tx, caller, task, owner); err != nil {
				return err
			}
			result = &VideoDeleted{ID: task.PublicID, Object: "video.deleted", Deleted: true, RequestID: task.RequestID}
			return nil
		}
		if s.mediaDeleteStore == nil || !s.mediaDeleteStore.SupportsSynchronousDeletion() {
			return ErrVideoMediaDeleteUnavailable
		}
		var assets []model.AIImageAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=?", task.ID).Order("id").Find(&assets).Error; err != nil {
			return err
		}
		if len(assets) != len(plan) || len(plan) != 6 {
			return ErrVideoMediaProtected
		}
		if err := mediaDeleteProtection(tx, task, assets, s.saveStore); err != nil {
			return err
		}
		if retention && !videoRetentionAssetsEligible(assets, retentionAt) {
			return ErrVideoMediaProtected
		}
		for i, a := range assets {
			p := plan[i]
			expected := p.PreparedVersion
			if p.Delete && !p.PreDeleted {
				if op.VersionNo == 0 || expected > math.MaxUint64-(op.VersionNo-1) {
					return ErrVideoMediaProtected
				}
				expected += op.VersionNo - 1
			}
			if a.ID != p.AssetID || a.PublicID != p.PublicID || a.AssetRole != p.Role || a.VersionNo != expected || a.Bucket == nil || a.ObjectKey == nil || *a.Bucket != p.Bucket || *a.ObjectKey != p.ObjectKey || mediaMetadataSHA(a) != p.MetadataSHA256 {
				return ErrVideoMediaProtected
			}
			if p.Delete && !p.PreDeleted && a.LifecycleState != op.Status {
				return ErrVideoMediaProtected
			}
			if p.PreDeleted {
				prior, _, err := verifySingleAssetDeletionTx(tx, task, &a)
				if err != nil || prior.Status != "completed" {
					return ErrVideoMediaProtected
				}
			}
		}
		advance := func(status string) error {
			for i, p := range plan {
				if !p.Delete || p.PreDeleted {
					continue
				}
				a := assets[i]
				next := status
				if status == "completed" {
					next = model.AIImageAssetDeleted
				}
				updated, err := repository.NewVideoOutputAssetRepository(tx, nil).TransitionLifecycle(dbctx, a.PublicID, owner, a.VersionNo, next, time.Now().UTC())
				if err != nil {
					return err
				}
				assets[i] = *updated
			}
			updates := map[string]any{"status": status, "version_no": gorm.Expr("version_no+1")}
			if status == "completed" {
				updates["completed_at"] = time.Now().UTC()
			}
			r := tx.Model(&videoMediaDeletion{}).Where("task_id=? AND status=? AND version_no=?", op.TaskID, op.Status, op.VersionNo).Updates(updates)
			if r.Error != nil {
				return r.Error
			}
			if r.RowsAffected != 1 {
				return ErrVideoMediaProtected
			}
			op.Status = status
			op.VersionNo++
			return nil
		}
		if op.Status == "delete_failed" {
			if err := advance("deleting"); err != nil {
				return err
			}
		}
		if op.Status != "deleting" {
			return ErrVideoMediaProtected
		}
		for _, p := range plan {
			if !p.Delete {
				continue
			}
			ref := video.VideoObjectRef{Bucket: p.Bucket, ObjectKey: p.ObjectKey}
			verified, err := s.mediaDeleteStore.VerifyDeleted(operation, ref)
			if err == nil && !verified {
				meta, headErr := s.mediaDeleteStore.Head(operation, ref)
				if headErr != nil || meta.Ref != ref || meta.SHA256 != p.SHA256 || meta.SizeBytes != p.Size {
					err = ErrVideoMediaDeleteUnavailable
				} else {
					// Head属于外部等待；实际删除每个对象前重新确认资格，不能等删完再报401/403。
					deleteCtx, finish := operation, func() {}
					if !retention {
						var authErr error
						deleteCtx, finish, authErr = s.currentVideoDeleteAuthority(operation, tx, caller, task, owner)
						if authErr != nil {
							return authErr
						}
					}
					err = s.mediaDeleteStore.Delete(deleteCtx, ref)
					finish()
					if err == nil {
						verified, err = s.mediaDeleteStore.VerifyDeleted(operation, ref)
					}
				}
			}
			if err != nil || !verified {
				storeFailure = ErrVideoMediaDeleteUnavailable
				return advance("delete_failed")
			}
		}
		if !retention {
			if err := s.access.AuthorizeTx(dbctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
				return err
			}
			if err := revalidateVideoReadCredential(operation, caller); err != nil {
				return err
			}
		}
		// 交付对象删除不能附带清除审核副本；确认提交前复核保留对象的实际hash和大小。
		for _, p := range plan {
			if p.Delete {
				continue
			}
			ref := video.VideoObjectRef{Bucket: p.Bucket, ObjectKey: p.ObjectKey}
			meta, err := s.mediaDeleteStore.Head(operation, ref)
			if err != nil || meta.Ref != ref || meta.SHA256 != p.SHA256 || meta.SizeBytes != p.Size {
				storeFailure = ErrVideoMediaDeleteUnavailable
				return advance("delete_failed")
			}
		}
		if err := advance("completed"); err != nil {
			return err
		}
		if !retention {
			if err := s.checkCurrentVideoDeleteAuthority(operation, tx, caller, task, owner); err != nil {
				return err
			}
		}
		result = &VideoDeleted{ID: task.PublicID, Object: "video.deleted", Deleted: true, RequestID: task.RequestID}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	if storeFailure != nil {
		return nil, storeFailure
	}
	return result, nil
}

// 计划摘要只能证明计划字节未漂移，不能证明计划完整；必须与原Task的实际父子树逐一核对。
func validateMediaDeletePlanShape(task *repository.VideoTaskRecord, assets []model.AIImageAsset, plan []videoMediaDeleteItem) error {
	if len(plan) == 0 {
		if len(assets) != 0 || (task.Status != model.AIImageTaskFailed && task.Status != model.AIImageTaskCancelled && task.Status != model.AIImageTaskExpired) {
			return ErrVideoMediaProtected
		}
		return nil
	}
	if len(plan) != 6 || len(assets) != 6 {
		return ErrVideoMediaProtected
	}
	roles := map[string]bool{"content": false, "cover": false, "preview": false, "thumbnail": false, "derived": false, "moderation_copy": false}
	var parent uint64
	for i, a := range assets {
		p := plan[i]
		seen, allowed := roles[a.AssetRole]
		if !allowed || seen || a.TaskID != task.ID || a.UserID != task.UserID || a.ProjectID != task.ProjectID || a.Modality != "video" || p.AssetID != a.ID || p.PublicID != a.PublicID || p.Role != a.AssetRole || p.Delete != (a.AssetRole != "moderation_copy") || p.PreparedVersion == 0 {
			return ErrVideoMediaProtected
		}
		roles[a.AssetRole] = true
		if a.AssetRole == "content" {
			if a.ParentAssetID != nil {
				return ErrVideoMediaProtected
			}
			parent = a.ID
		}
	}
	if parent == 0 {
		return ErrVideoMediaProtected
	}
	for _, a := range assets {
		if a.AssetRole != "content" && (a.ParentAssetID == nil || *a.ParentAssetID != parent) {
			return ErrVideoMediaProtected
		}
	}
	return nil
}
