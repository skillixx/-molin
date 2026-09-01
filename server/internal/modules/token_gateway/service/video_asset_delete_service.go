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

var ErrVideoAssetDeleteConflict = errors.New("资产删除范围、版本或命令冲突")

type videoAssetDeletion struct {
	AssetID, TaskID, UserID, ProjectID uint64          `json:"-"`
	APIKeyID                           *uint64         `json:"-"`
	RequestID                          string          `json:"-"`
	InputVersionNo, VersionNo          uint64          `json:"-"`
	Status                             string          `json:"-"`
	PlanJSON                           json.RawMessage `json:"-"`
	PlanSHA256                         string          `json:"-"`
	CreatedAt                          time.Time       `json:"-"`
	CompletedAt                        *time.Time      `json:"-"`
}

func (videoAssetDeletion) TableName() string { return "ai_video_asset_deletions" }

type videoAssetDeleteCommand struct {
	UserID, ProjectID, TaskID, AssetID uint64    `json:"-"`
	APIKeyID                           *uint64   `json:"-"`
	CommandKeyHash, DeletionScope      string    `json:"-"`
	InputVersionNo                     uint64    `json:"-"`
	CreatedAt                          time.Time `json:"-"`
}

func (videoAssetDeleteCommand) TableName() string { return "ai_video_asset_delete_commands" }

type videoAssetDeleteRequest struct {
	AssetID              uint64
	PublicID, Scope, Key string
	Version              uint64
	Existing             bool
}
type VideoAssetDeleted struct {
	AssetID        string `json:"asset_id"`
	VideoID        string `json:"video_id"`
	RequestID      string `json:"request_id"`
	VersionNo      uint64 `json:"version_no"`
	LifecycleState string `json:"lifecycle_state"`
	MediaDeleted   bool   `json:"media_deleted"`
	Scope          string `json:"scope"`
	Idempotent     bool   `json:"idempotent"`
}

// 平台asset_id控制真实删除粒度：根联动普通派生物，子资产仅删除自身，永不扩张到父或兄弟。
func (s *VideoHTTPService) DeleteVideoAsset(ctx context.Context, caller VideoCaller, id string, version uint64, key string) (*VideoAssetDeleted, error) {
	if s == nil || s.db == nil || s.access == nil || !cleanupAdapterPresent(s.mediaDeleteStore) {
		return nil, ErrVideoMediaDeleteUnavailable
	}
	if !videoBillingPublicID.MatchString(id) || caller.UserID == 0 {
		return nil, repository.ErrVideoTaskNotFound
	}
	if version == 0 || version > math.MaxUint64-4 || !videoHTTPIdempotency.MatchString(key) {
		return nil, ErrVideoGenerationIntent
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := revalidateVideoReadCredential(ctx, caller); err != nil {
		return nil, err
	}
	var identity struct {
		ID            uint64
		VideoID, Role string
	}
	q := videoTaskOwnerQuery(s.db.WithContext(ctx), caller).Joins("JOIN ai_gateway_assets a ON a.task_id=t.id AND a.request_id=t.request_id AND a.user_id=t.user_id AND a.project_id=t.project_id")
	if err := q.Select("a.id,t.public_id AS video_id,a.asset_role AS role").Where("a.public_id=? AND a.modality='video' AND (a.asset_role IN ('content','cover','preview','thumbnail') OR (a.asset_role='derived' AND a.source='derived'))", id).Take(&identity).Error; err != nil {
		return nil, videoAccessReadError(err, repository.ErrVideoTaskNotFound)
	}
	request := &videoAssetDeleteRequest{AssetID: identity.ID, PublicID: id, Scope: "asset", Key: key, Version: version}
	if identity.Role == "content" {
		request.Scope = "video"
		if _, err := s.deleteMedia(ctx, caller, identity.VideoID, key, request); err != nil {
			return nil, err
		}
	} else if err := s.deleteSingleVideoAsset(ctx, caller, identity.VideoID, request); err != nil {
		return nil, err
	}
	life, err := s.GetAssetLifecycle(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	return &VideoAssetDeleted{AssetID: id, VideoID: identity.VideoID, RequestID: life.RequestID, VersionNo: life.VersionNo, LifecycleState: life.LifecycleState, MediaDeleted: life.MediaDeleted, Scope: request.Scope, Idempotent: request.Existing}, nil
}

func readAssetDeleteCommand(tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner, r *videoAssetDeleteRequest) (bool, error) {
	var command videoAssetDeleteCommand
	hash := videoBillingDigest(fmt.Sprintf("asset-delete:%d:%d:%s", owner.UserID, owner.ProjectID, r.Key))
	err := tx.Where("user_id=? AND project_id=? AND command_key_hash=?", owner.UserID, owner.ProjectID, hash).Take(&command).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if command.TaskID != task.ID || command.AssetID != r.AssetID || command.InputVersionNo != r.Version || command.DeletionScope != r.Scope || !equalOptionalUint64(command.APIKeyID, owner.APIKeyID) {
		return false, ErrVideoAssetDeleteConflict
	}
	return true, nil
}
func insertAssetDeleteCommand(tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner, r *videoAssetDeleteRequest) error {
	return tx.Create(&videoAssetDeleteCommand{UserID: owner.UserID, ProjectID: owner.ProjectID, TaskID: task.ID, AssetID: r.AssetID, APIKeyID: owner.APIKeyID, InputVersionNo: r.Version, DeletionScope: r.Scope, CommandKeyHash: videoBillingDigest(fmt.Sprintf("asset-delete:%d:%d:%s", owner.UserID, owner.ProjectID, r.Key)), CreatedAt: time.Now().UTC()}).Error
}

func (s *VideoHTTPService) deleteSingleVideoAsset(ctx context.Context, caller VideoCaller, id string, r *videoAssetDeleteRequest) error {
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := revalidateVideoReadCredential(ctx, caller); err != nil {
				return err
			}
			task, owner, err := s.taskForPlatformTx(ctx, tx, caller, id, false)
			if err != nil {
				return err
			}
			if task.Status != model.AIImageTaskSucceeded || task.BillingStatus != model.AIBillingSettled || task.DeliveryStatus != model.AIDeliveryAvailable || task.Operation == nil {
				return ErrVideoMediaRunning
			}
			if err = s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
				return err
			}
			exists, err := readAssetDeleteCommand(tx, task, owner, r)
			if err != nil {
				return err
			}
			r.Existing = exists
			var op videoAssetDeletion
			err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("asset_id=? AND task_id=?", r.AssetID, task.ID).Take(&op).Error
			if err == nil {
				if op.InputVersionNo != r.Version {
					return ErrVideoAssetDeleteConflict
				}
				// 旧意图不豁免当前财务、安全与快照检查；拒绝的新键不能先留下命令映射。
				if err := verifyMediaDeleteFinancial(tx, task, owner); err != nil {
					return err
				}
				if _, err := loadVideoSettlementMediaTx(tx, task, false, time.Now().UTC()); err != nil {
					return ErrVideoMediaProtected
				}
				var all []model.AIImageAsset
				if err := tx.Where("task_id=?", task.ID).Order("id").Find(&all).Error; err != nil {
					return err
				}
				if err := mediaDeleteProtection(tx, task, all, s.saveStore); err != nil {
					return err
				}
				matched := false
				for i := range all {
					if all[i].ID == r.AssetID {
						if _, _, err := verifySingleAssetDeletionTx(tx, task, &all[i]); err != nil {
							return err
						}
						matched = true
					}
				}
				if !matched {
					return ErrVideoMediaProtected
				}
				if !exists {
					if err := insertAssetDeleteCommand(tx, task, owner, r); err != nil {
						return err
					}
				}
				return revalidateVideoReadCredential(ctx, caller)
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if exists {
				return ErrVideoMediaProtected
			}
			var whole int64
			if err := tx.Table("ai_video_media_deletions").Where("task_id=?", task.ID).Count(&whole).Error; err != nil {
				return err
			}
			if whole != 0 {
				return ErrVideoAssetDeleteConflict
			}
			if err := verifyMediaDeleteFinancial(tx, task, owner); err != nil {
				return err
			}
			if _, err := loadVideoSettlementMediaTx(tx, task, false, time.Now().UTC()); err != nil {
				return ErrVideoMediaProtected
			}
			var all []model.AIImageAsset
			if err := tx.Where("task_id=?", task.ID).Order("id").Find(&all).Error; err != nil {
				return err
			}
			if err := mediaDeleteProtection(tx, task, all, s.saveStore); err != nil {
				return err
			}
			var asset *model.AIImageAsset
			for i := range all {
				if all[i].ID == r.AssetID {
					asset = &all[i]
				}
			}
			if asset == nil || asset.PublicID != r.PublicID || asset.ParentAssetID == nil || asset.AssetRole == "content" || !videoPublicDownloadAsset(asset) {
				return repository.ErrVideoTaskNotFound
			}
			if asset.VersionNo != r.Version || (asset.LifecycleState != "available" && asset.LifecycleState != "expiring") {
				return ErrVideoAssetDeleteConflict
			}
			if !s.mediaDeleteStore.SupportsSynchronousDeletion() {
				return ErrVideoMediaDeleteUnavailable
			}
			if err := checkVideoContentObject(ctx, s.mediaDeleteStore, asset); err != nil {
				return ErrVideoMediaDeleteUnavailable
			}
			p := videoMediaDeleteItem{AssetID: asset.ID, PublicID: asset.PublicID, Role: asset.AssetRole, Delete: true, Bucket: *asset.Bucket, ObjectKey: *asset.ObjectKey, SHA256: *asset.SHA256, Size: *asset.SizeBytes, MetadataSHA256: mediaMetadataSHA(*asset)}
			for asset.LifecycleState != "deleting" {
				next := "deleting"
				if asset.LifecycleState == "available" {
					next = "expiring"
				}
				asset, err = repository.NewVideoOutputAssetRepository(tx, nil).TransitionLifecycle(ctx, asset.PublicID, owner, asset.VersionNo, next, time.Now().UTC())
				if err != nil {
					return err
				}
			}
			p.PreparedVersion = asset.VersionNo
			raw, err := json.Marshal(p)
			if err != nil {
				return err
			}
			op = videoAssetDeletion{AssetID: asset.ID, TaskID: task.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, RequestID: task.RequestID, InputVersionNo: r.Version, VersionNo: 1, Status: "deleting", PlanJSON: raw, PlanSHA256: videoPayloadSHA256(raw), CreatedAt: time.Now().UTC()}
			if err := tx.Create(&op).Error; err != nil {
				return err
			}
			if err := insertAssetDeleteCommand(tx, task, owner, r); err != nil {
				return err
			}
			return revalidateVideoReadCredential(ctx, caller)
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return err
	}
	return s.executeSingleVideoAssetDelete(ctx, caller, id, r.AssetID)
}

// 删除见证只接受冻结目标及当前CAS；用于重放、整组清理合并和长期副本读取，不访问客户端位置。
func verifySingleAssetDeletionTx(tx *gorm.DB, task *repository.VideoTaskRecord, a *model.AIImageAsset) (*videoAssetDeletion, *videoMediaDeleteItem, error) {
	var op videoAssetDeletion
	if err := tx.Where("asset_id=? AND task_id=? AND request_id=? AND user_id=? AND project_id=?", a.ID, task.ID, task.RequestID, task.UserID, task.ProjectID).Take(&op).Error; err != nil {
		return nil, nil, videoAccessReadError(err, ErrVideoMediaProtected)
	}
	var p videoMediaDeleteItem
	decoder := json.NewDecoder(bytes.NewReader(op.PlanJSON))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return nil, nil, ErrVideoMediaProtected
	}
	raw, _ := json.Marshal(p)
	// 校验与删除必须指向同一对象；通用元数据JSON不包含内部存储位置，不能只比较其摘要。
	if a.ParentAssetID == nil || a.AssetRole == "content" || !videoPublicDownloadAsset(a) || p.PreDeleted || a.Bucket == nil || a.ObjectKey == nil || a.SHA256 == nil || a.SizeBytes == nil || p.Bucket != *a.Bucket || p.ObjectKey != *a.ObjectKey || p.SHA256 != *a.SHA256 || p.Size != *a.SizeBytes || op.RequestID != task.RequestID {
		return nil, nil, ErrVideoMediaProtected
	}
	if videoPayloadSHA256(raw) != op.PlanSHA256 || !equalOptionalUint64(op.APIKeyID, task.APIKeyID) || p.AssetID != a.ID || p.PublicID != a.PublicID || p.Role != a.AssetRole || !p.Delete || p.PreparedVersion == 0 || op.VersionNo == 0 || p.PreparedVersion > math.MaxUint64-(op.VersionNo-1) || a.VersionNo != p.PreparedVersion+op.VersionNo-1 || mediaMetadataSHA(*a) != p.MetadataSHA256 {
		return nil, nil, ErrVideoMediaProtected
	}
	state := op.Status
	if state == "completed" {
		state = "deleted"
	}
	if a.LifecycleState != state || (state == "deleted" && (a.DeletedAt == nil || a.MediaDeletedAt == nil)) {
		return nil, nil, ErrVideoMediaProtected
	}
	return &op, &p, nil
}

func (s *VideoHTTPService) executeSingleVideoAssetDelete(ctx context.Context, caller VideoCaller, id string, assetID uint64) error {
	operation, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dbctx, dbCancel := context.WithTimeout(context.WithoutCancel(ctx), 35*time.Second)
	defer dbCancel()
	var storeErr error
	err := s.db.WithContext(dbctx).Transaction(func(tx *gorm.DB) error {
		if err := revalidateVideoReadCredential(operation, caller); err != nil {
			return err
		}
		task, owner, err := s.taskForPlatformTx(operation, tx, caller, id, false)
		if err != nil {
			return err
		}
		if err := s.checkCurrentVideoDeleteAuthority(operation, tx, caller, task, owner); err != nil {
			return err
		}
		if err := verifyMediaDeleteFinancial(tx, task, owner); err != nil {
			return err
		}
		if _, err := loadVideoSettlementMediaTx(tx, task, false, time.Now().UTC()); err != nil {
			return ErrVideoMediaProtected
		}
		var assets []model.AIImageAsset
		if err := tx.Where("task_id=?", task.ID).Order("id").Find(&assets).Error; err != nil {
			return err
		}
		if err := mediaDeleteProtection(tx, task, assets, s.saveStore); err != nil {
			return err
		}
		var a *model.AIImageAsset
		for i := range assets {
			if assets[i].ID == assetID {
				a = &assets[i]
			}
		}
		if a == nil {
			return repository.ErrVideoTaskNotFound
		}
		op, p, err := verifySingleAssetDeletionTx(tx, task, a)
		if err != nil {
			return err
		}
		if !cleanupAdapterPresent(s.mediaDeleteStore) || !s.mediaDeleteStore.SupportsSynchronousDeletion() {
			return ErrVideoMediaDeleteUnavailable
		}
		ref := video.VideoObjectRef{Bucket: p.Bucket, ObjectKey: p.ObjectKey}
		if op.Status == "completed" {
			gone, err := s.mediaDeleteStore.VerifyDeleted(operation, ref)
			if err != nil || !gone {
				return ErrVideoMediaDeleteUnavailable
			}
			return s.checkCurrentVideoDeleteAuthority(operation, tx, caller, task, owner)
		}
		advance := func(status string) error {
			next := status
			if status == "completed" {
				next = "deleted"
			}
			updated, err := repository.NewVideoOutputAssetRepository(tx, nil).TransitionLifecycle(dbctx, a.PublicID, owner, a.VersionNo, next, time.Now().UTC())
			if err != nil {
				return err
			}
			a = updated
			values := map[string]any{"status": status, "version_no": op.VersionNo + 1}
			if status == "completed" {
				values["completed_at"] = time.Now().UTC()
			}
			changed := tx.Model(&videoAssetDeletion{}).Where("asset_id=? AND status=? AND version_no=?", op.AssetID, op.Status, op.VersionNo).Updates(values)
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected != 1 {
				return ErrVideoAssetDeleteConflict
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
		gone, err := s.mediaDeleteStore.VerifyDeleted(operation, ref)
		if err == nil && !gone {
			if err = checkVideoContentObject(operation, s.mediaDeleteStore, a); err == nil {
				// 资格检查必须位于最后一次外部Head之后；失败保留首阶段意图和全部媒体。
				deleteCtx, finish, authErr := s.currentVideoDeleteAuthority(operation, tx, caller, task, owner)
				if authErr != nil {
					return authErr
				}
				err = s.mediaDeleteStore.Delete(deleteCtx, ref)
				finish()
				if err == nil {
					gone, err = s.mediaDeleteStore.VerifyDeleted(operation, ref)
				}
			}
		}
		if err != nil || !gone {
			storeErr = ErrVideoMediaDeleteUnavailable
			return advance("delete_failed")
		}
		if err := s.access.AuthorizeTx(operation, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
			return err
		}
		if err := revalidateVideoReadCredential(operation, caller); err != nil {
			return err
		}
		if err := advance("completed"); err != nil {
			return err
		}
		return s.checkCurrentVideoDeleteAuthority(operation, tx, caller, task, owner)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	return storeErr
}
