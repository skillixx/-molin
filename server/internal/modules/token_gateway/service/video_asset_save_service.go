package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	assetrepo "molin/server/internal/modules/asset/repository"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// SaveVideoAsset先持久化预占与不可变计划，再执行复制；中断后所有目标仍有原命令与容量归属。
func (s *VideoHTTPService) SaveVideoAsset(ctx context.Context, caller VideoCaller, assetID, key string) (*VideoAssetSaveReply, error) {
	if s == nil || s.db == nil || s.access == nil || s.saveStore == nil || !s.saveStore.SupportsSynchronousDeletion() || s.savePolicy == nil || s.savePolicy.validate() != nil {
		return nil, ErrVideoSaveUnavailable
	}
	if !videoHTTPIdempotency.MatchString(key) {
		return nil, ErrVideoGenerationIntent
	}
	if !videoBillingPublicID.MatchString(assetID) || caller.UserID == 0 {
		return nil, repository.ErrVideoTaskNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var identity struct{ PublicID string }
	q := videoTaskOwnerQuery(s.db.WithContext(ctx), caller).Joins("JOIN ai_gateway_assets a ON a.task_id=t.id AND a.request_id=t.request_id AND a.user_id=t.user_id AND a.project_id=t.project_id")
	if err := q.Select("t.public_id").Where("a.public_id=? AND a.modality='video' AND a.asset_role='content'", assetID).Take(&identity).Error; err != nil {
		return nil, videoAccessReadError(err, repository.ErrVideoTaskNotFound)
	}
	var ready *VideoAssetSaveReply
	var saveID string
	alert := false
	err := retryVideoBillingTransaction(ctx, func() error {
		ready = nil
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, owner, err := s.saveTaskTx(ctx, tx, caller, identity.PublicID)
			if err != nil {
				return err
			}
			hash := videoBillingDigest(fmt.Sprintf("video-save:%d:%d:%s", owner.UserID, owner.ProjectID, key))
			var command videoAssetSaveCommand
			err = tx.Where("user_id=? AND project_id=? AND command_key_hash=?", owner.UserID, owner.ProjectID, hash).Take(&command).Error
			if err == nil && (command.TaskID != task.ID || !equalOptionalUint64(command.APIKeyID, owner.APIKeyID)) {
				return ErrVideoSaveConflict
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			hasCommand := err == nil
			var priorCommand *videoAssetSaveCommand
			if hasCommand {
				priorCommand = &command
			}
			existing, nextAttempt, previousSaveID, err := s.selectVideoSaveAttemptTx(ctx, tx, task, owner, priorCommand)
			if err != nil {
				return err
			}
			var op videoAssetSave
			if existing == nil {
				assets, err := s.saveSourceTx(ctx, tx, task, owner)
				if err != nil {
					return err
				}
				var entropy [24]byte
				if _, err := rand.Read(entropy[:]); err != nil {
					return err
				}
				op = videoAssetSave{TaskID: task.ID, PublicID: "vsave_" + hex.EncodeToString(entropy[:]), RequestID: task.RequestID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, Status: "copying", VersionNo: 1, StorageProductID: s.savePolicy.StorageProductID, QuotaUnit: s.savePolicy.QuotaUnit, PolicyVersion: s.savePolicy.Version, CreatedAt: time.Now().UTC()}
				op.StorageEntitlementType = s.savePolicy.EntitlementType
				op.AttemptNo, op.PreviousSaveID = nextAttempt, previousSaveID
				plan := make([]videoAssetSaveItem, 0, 5)
				for _, a := range assets {
					if a.AssetRole == "moderation_copy" {
						continue
					}
					if !videoPublicDownloadAsset(&a) || a.SizeBytes == nil || a.SHA256 == nil || a.Bucket == nil || a.ObjectKey == nil || op.TotalBytes > math.MaxUint64-*a.SizeBytes {
						return ErrVideoSaveUnavailable
					}
					op.TotalBytes += *a.SizeBytes
					plan = append(plan, videoAssetSaveItem{AssetID: a.ID, PublicID: a.PublicID, Role: a.AssetRole, VersionNo: a.VersionNo, SHA256: *a.SHA256, Size: *a.SizeBytes, SourceBucket: *a.Bucket, SourceKey: *a.ObjectKey, TargetBucket: "ai-user-assets", TargetKey: op.PublicID + "/" + a.PublicID + "/" + a.AssetRole + ".bin", MetadataSHA256: mediaMetadataSHA(a)})
				}
				if len(plan) != 5 {
					return ErrVideoSaveUnavailable
				}
				op.PlanJSON, err = json.Marshal(plan)
				if err != nil {
					return err
				}
				op.PlanSHA256 = videoPayloadSHA256(op.PlanJSON)
				op.QuotaAmount, err = videoSaveQuota(op.TotalBytes, op.QuotaUnit)
				if err != nil {
					return err
				}
				alert, err = s.reserveSaveCapacityTx(tx, owner.UserID, owner.ProjectID, op.TotalBytes)
				if err != nil {
					return err
				}
				ent, err := s.saveEntitlementTx(ctx, tx, owner.UserID, 0, op.QuotaAmount)
				if err != nil {
					return err
				}
				op.StorageEntitlementID = ent.ID
				if err := assetrepo.NewEntitlementRepository(tx).ReserveQuota(ctx, tx, ent.ID, op.QuotaAmount); err != nil {
					return err
				}
				if err := tx.Create(&op).Error; err != nil {
					return err
				}
			} else {
				op = *existing
				if !sameVideoSaveOwner(&op, task, owner) {
					return repository.ErrVideoTaskNotFound
				}
				// 在绑定任何新命令之前拒绝无法恢复的旧计划，不能等到finish才拒绝并留下新键映射。
				if (op.Status == "copying" || op.Status == "copy_failed") && !matchesVideoSaveExecutionPolicy(&op, s.savePolicy) {
					return ErrVideoSaveConflict
				}
				if op.Status == "completed" {
					ready, err = s.savedReplyTx(ctx, tx, caller, task, owner, &op, assetID, true)
					if err != nil {
						return err
					}
				}
				if op.Status == "aborted" && hasCommand {
					if err := verifyVideoSaveCleanupTx(ctx, tx, &op, s.saveStore); err != nil {
						return err
					}
					// 旧键只返回原终止事实；不创建对象、资产或新容量，也不漂移到后继尝试。
					ready = &VideoAssetSaveReply{AssetID: assetID, VideoID: task.PublicID, RequestID: task.RequestID, Status: "aborted", SizeBytes: op.TotalBytes, Idempotent: true}
				}
				if op.Status != "copying" && op.Status != "copy_failed" && op.Status != "completed" && !(op.Status == "aborted" && hasCommand) {
					return ErrVideoSaveConflict
				}
			}
			if !hasCommand {
				command = videoAssetSaveCommand{UserID: owner.UserID, ProjectID: owner.ProjectID, TaskID: task.ID, APIKeyID: owner.APIKeyID, CommandKeyHash: hash, CreatedAt: time.Now().UTC()}
				command.SavePublicID = op.PublicID
				if err := tx.Create(&command).Error; err != nil {
					if repository.IsDuplicateKeyForHandler(err) {
						return ErrVideoSaveConflict
					}
					return err
				}
			}
			if ready == nil {
				if _, err := s.saveSourceTx(ctx, tx, task, owner); err != nil {
					return err
				}
			}
			saveID = op.PublicID
			return s.saveAccessTx(ctx, tx, caller, task, owner)
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	if alert {
		log.Print("video_saved_storage_capacity_threshold")
	}
	if ready != nil {
		return ready, nil
	}
	return s.finishVideoSave(ctx, caller, identity.PublicID, assetID, saveID)
}

func (s *VideoHTTPService) saveTaskTx(ctx context.Context, tx *gorm.DB, caller VideoCaller, id string) (*repository.VideoTaskRecord, repository.VideoOwner, error) {
	task, owner, err := s.taskForPlatformTx(ctx, tx, caller, id, false)
	if err != nil {
		return nil, owner, err
	}
	if err := s.saveAccessTx(ctx, tx, caller, task, owner); err != nil {
		return nil, owner, err
	}
	return task, owner, nil
}

func (s *VideoHTTPService) saveAccessTx(ctx context.Context, tx *gorm.DB, caller VideoCaller, task *repository.VideoTaskRecord, owner repository.VideoOwner) error {
	if err := revalidateVideoReadCredential(ctx, caller); err != nil {
		return err
	}
	if task.Operation == nil || task.Status != model.AIImageTaskSucceeded || task.BillingStatus != model.AIBillingSettled || task.DeliveryStatus != model.AIDeliveryAvailable {
		return ErrVideoSaveConflict
	}
	allowed := false
	for _, code := range s.savePolicy.AllowedModels {
		allowed = allowed || code == task.LogicalModelCode
	}
	if !allowed {
		return ErrVideoCapabilityDenied
	}
	return s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation)
}

// 新保存必须仍有完整未过期交付树，删除意图已生效后不可利用转存复活媒体。
func (s *VideoHTTPService) saveSourceTx(ctx context.Context, tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner) ([]model.AIImageAsset, error) {
	var deleting int64
	if err := tx.Table("ai_video_media_deletions").Where("task_id=?", task.ID).Count(&deleting).Error; err != nil {
		return nil, err
	}
	if deleting != 0 {
		return nil, ErrVideoSaveConflict
	}
	report, err := NewVideoReconciliationService(tx).Reconcile(ctx, task.PublicID, owner)
	if err != nil {
		return nil, err
	}
	if !report.Passed {
		return nil, ErrVideoSaveConflict
	}
	var assets []model.AIImageAsset
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=? AND request_id=?", task.ID, task.RequestID).Order("id").Find(&assets).Error; err != nil {
		return nil, err
	}
	if len(assets) != 6 {
		return nil, ErrVideoSaveUnavailable
	}
	return assets, nil
}

func sameVideoSaveOwner(op *videoAssetSave, task *repository.VideoTaskRecord, owner repository.VideoOwner) bool {
	return op.TaskID == task.ID && op.RequestID == task.RequestID && op.UserID == owner.UserID && op.ProjectID == owner.ProjectID && equalOptionalUint64(op.APIKeyID, owner.APIKeyID)
}

func decodeVideoSavePlan(op *videoAssetSave) ([]videoAssetSaveItem, error) {
	var plan []videoAssetSaveItem
	decoder := json.NewDecoder(bytes.NewReader(op.PlanJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || len(plan) != 5 {
		return nil, ErrVideoSaveUnavailable
	}
	// MySQL JSON读回会调整空格和键顺序；先严格解码，再按冻结结构规范编码计算同一摘要。
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrVideoSaveUnavailable
	}
	canonical, err := json.Marshal(plan)
	if err != nil || videoPayloadSHA256(canonical) != op.PlanSHA256 {
		return nil, ErrVideoSaveUnavailable
	}
	expected, err := videoSaveQuota(op.TotalBytes, op.QuotaUnit)
	if err != nil || !expected.Equal(op.QuotaAmount) {
		return nil, ErrVideoSaveUnavailable
	}
	roles := map[string]bool{}
	ids := map[uint64]bool{}
	var total uint64
	for _, p := range plan {
		if p.AssetID == 0 || ids[p.AssetID] || roles[p.Role] || p.VersionNo == 0 || p.Size == 0 || !lowerHex64.MatchString(p.SHA256) || !lowerHex64.MatchString(p.MetadataSHA256) || !videoBillingPublicID.MatchString(p.PublicID) || p.TargetBucket != "ai-user-assets" || p.TargetKey != op.PublicID+"/"+p.PublicID+"/"+p.Role+".bin" || total > math.MaxUint64-p.Size {
			return nil, ErrVideoSaveUnavailable
		}
		switch p.Role {
		case "content", "cover", "preview", "thumbnail", "derived":
		default:
			return nil, ErrVideoSaveUnavailable
		}
		roles[p.Role] = true
		ids[p.AssetID] = true
		total += p.Size
	}
	if total != op.TotalBytes {
		return nil, ErrVideoSaveUnavailable
	}
	return plan, nil
}
