package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	auditmodel "molin/server/internal/modules/audit/model"
	auditrepo "molin/server/internal/modules/audit/repository"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type VideoAdminOutputQuarantineCommand struct {
	Caller                          VideoCaller `json:"-"`
	AssetID, IdempotencyKey, Reason string      `json:"-"`
	VersionNo                       uint64      `json:"-"`
}
type VideoAdminOutputQuarantineReply struct {
	*VideoAdminOutputDetails
	Idempotent bool `json:"idempotent"`
}
type videoAdminOutputQuarantineRecord struct {
	ID                                 uint64  `gorm:"primaryKey" json:"-"`
	ActorUserID                        uint64  `json:"-"`
	CommandKeyHash                     string  `json:"-"`
	AssetID, TaskID, UserID, ProjectID uint64  `json:"-"`
	RequestID                          string  `json:"-"`
	APIKeyID                           *uint64 `json:"-"`
	InitialVersion, FinalVersion       uint64  `json:"-"`
	InitialState                       string  `json:"-"`
	SnapshotSHA256                     string  `gorm:"column:snapshot_sha256" json:"-"`
	Status                             string  `json:"-"`
	VersionNo                          uint64  `json:"-"`
	VideoAdminReasonEnvelope           `gorm:"embedded" json:"-"`
	BeforeAuditID                      uint64    `json:"-"`
	AfterAuditID                       *uint64   `json:"-"`
	CreatedAt                          time.Time `json:"-"`
}

func (videoAdminOutputQuarantineRecord) TableName() string {
	return "ai_video_admin_output_quarantines"
}

// 与96迁移的原列摘要相同；只返回hash，不持久化或公开对象定位、媒体正文和完整快照。
const videoAdminOutputSnapshotSQL = `LOWER(SHA2(CAST(JSON_OBJECT('public_id',a.public_id,'user_id',a.user_id,'project_id',a.project_id,'request_id',a.request_id,'task_id',a.task_id,'result_index',a.result_index,'asset_role',a.asset_role,'parent_asset_id',a.parent_asset_id,'is_billable_output',a.is_billable_output,'bucket',a.bucket,'object_key',a.object_key,'mime_type',a.mime_type,'size_bytes',a.size_bytes,'sha256',a.sha256,'width',a.width,'height',a.height,'modality',a.modality,'duration_seconds',a.duration_seconds,'frame_rate',a.frame_rate,'container',a.container,'video_codec',a.video_codec,'audio_codec',a.audio_codec,'has_audio',a.has_audio,'source',a.source,'moderation_status',a.moderation_status,'moderation_policy_version',a.moderation_policy_version,'explicit_label_status',a.explicit_label_status,'explicit_label_version',a.explicit_label_version,'implicit_label_status',a.implicit_label_status,'implicit_label_version',a.implicit_label_version,'retention_policy_id',a.retention_policy_id,'created_at',a.created_at) AS CHAR CHARACTER SET utf8mb4),256))`

func videoAdminOutputSnapshot(tx *gorm.DB, id uint64) (string, error) {
	var hash string
	if err := tx.Table("ai_gateway_assets a").Select(videoAdminOutputSnapshotSQL).Where("a.id=?", id).Scan(&hash).Error; err != nil {
		return "", errors.Join(ErrVideoAccessUnavailable, err)
	}
	if !lowerHex64.MatchString(hash) {
		return "", ErrVideoAccessUnavailable
	}
	return hash, nil
}

type videoAdminOutputAuditSummary struct {
	CommandKeyHash string `json:"command_key_hash"`
	ReasonHMAC     string `json:"reason_hmac"`
	ReasonLength   uint32 `json:"reason_length"`
	AssetID        string `json:"asset_id"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	InitialVersion uint64 `json:"initial_version"`
	CurrentVersion uint64 `json:"current_version"`
	Result         string `json:"result"`
}

func videoAdminOutputAuditData(c videoAdminOutputQuarantineRecord, id string, before bool) videoAdminOutputAuditSummary {
	d := videoAdminOutputAuditSummary{CommandKeyHash: c.CommandKeyHash, ReasonHMAC: c.ReasonHMAC, ReasonLength: c.ReasonLength, AssetID: id, SnapshotSHA256: c.SnapshotSHA256, InitialVersion: c.InitialVersion, CurrentVersion: c.FinalVersion, Result: "quarantined"}
	if before {
		d.CurrentVersion = c.InitialVersion
		d.Result = "requested"
	}
	return d
}
func writeVideoAdminOutputAudit(ctx context.Context, tx *gorm.DB, c videoAdminOutputQuarantineRecord, id string, before bool) (uint64, error) {
	raw, err := json.Marshal(videoAdminOutputAuditData(c, id, before))
	if err != nil {
		return 0, err
	}
	action := "video_admin_output_quarantine_after"
	if before {
		action = "video_admin_output_quarantine_before"
	}
	target, summary := "video_output_asset", string(raw)
	row := auditmodel.AuditLog{OperatorID: &c.ActorUserID, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &id, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	if err := auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &row); err != nil {
		return 0, err
	}
	return row.ID, nil
}
func verifyVideoAdminOutputAudits(tx *gorm.DB, c videoAdminOutputQuarantineRecord, id string) error {
	if c.AfterAuditID == nil {
		return ErrVideoAccessUnavailable
	}
	for _, before := range []bool{true, false} {
		auditID, action := *c.AfterAuditID, "video_admin_output_quarantine_after"
		if before {
			auditID, action = c.BeforeAuditID, "video_admin_output_quarantine_before"
		}
		var row auditmodel.AuditLog
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", auditID).Take(&row).Error; err != nil {
			return errors.Join(ErrVideoAccessUnavailable, err)
		}
		if row.OperatorID == nil || *row.OperatorID != c.ActorUserID || row.Module != "token_gateway" || row.Action != action || row.TargetType == nil || *row.TargetType != "video_output_asset" || row.TargetID == nil || *row.TargetID != id || row.RequestSummary == nil {
			return ErrVideoAccessUnavailable
		}
		var d videoAdminOutputAuditSummary
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(*row.RequestSummary), &d) != nil || json.Unmarshal([]byte(*row.RequestSummary), &fields) != nil || len(fields) != 8 || !reflect.DeepEqual(d, videoAdminOutputAuditData(c, id, before)) {
			return ErrVideoAccessUnavailable
		}
	}
	return nil
}

// 单资产行政隔离保留原审核/标识；原六资产门禁阻断整条视频，不在这里退款或批量改兄弟状态。
func (s *VideoAdminService) QuarantineOutput(ctx context.Context, c VideoAdminOutputQuarantineCommand) (*VideoAdminOutputQuarantineReply, error) {
	if !s.WritesReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if !utf8.ValidString(reason) || reason == "" || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || c.VersionNo == 0 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var reply *VideoAdminOutputQuarantineReply
	err := retryVideoBillingTransaction(ctx, func() error {
		reply = nil
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:safety_review"); err != nil {
				return err
			}
			if !videoBillingPublicID.MatchString(c.AssetID) {
				return repository.ErrVideoAssetNotFound
			}
			var identity struct {
				TaskPublicID      string
				UserID, ProjectID uint64
				APIKeyID          *uint64
			}
			if err := tx.Table("ai_gateway_assets a").Select("t.public_id AS task_public_id,t.user_id,t.project_id,t.api_key_id").Joins("JOIN ai_gateway_tasks t ON t.id=a.task_id AND t.request_id=a.request_id AND t.user_id=a.user_id AND t.project_id=a.project_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.logical_model_code=t.logical_model_code AND r.operation=t.operation").Where("a.public_id=? AND a.modality='video' AND t.capability='video.generate' AND t.operation IN ('text_to_video','image_to_video') AND r.modality='video' AND r.capability='video.generate'", c.AssetID).Take(&identity).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoAssetNotFound)
			}
			owner := repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, identity.TaskPublicID, owner)
			if err != nil {
				return err
			}
			var asset model.AIImageAsset
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id=? AND task_id=? AND request_id=? AND user_id=? AND project_id=? AND modality='video'", c.AssetID, task.ID, task.RequestID, owner.UserID, owner.ProjectID).Take(&asset).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoAssetNotFound)
			}
			if _, err := videoAdminOutputDetails(tx, asset); err != nil {
				return err
			}
			var pointer struct {
				ID *uint64 `gorm:"column:admin_quarantine_command_id"`
			}
			if err := tx.Table("ai_gateway_assets").Select("admin_quarantine_command_id").Where("id=?", asset.ID).Take(&pointer).Error; err != nil {
				return errors.Join(ErrVideoAccessUnavailable, err)
			}
			snapshot, err := videoAdminOutputSnapshot(tx, asset.ID)
			if err != nil {
				return err
			}
			hash := videoBillingDigest(fmt.Sprintf("video-admin-output-quarantine:%d:%s", c.Caller.UserID, c.IdempotencyKey))
			var command videoAdminOutputQuarantineRecord
			err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("actor_user_id=? AND command_key_hash=?", c.Caller.UserID, hash).Take(&command).Error
			replayed := err == nil
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.Join(ErrVideoAccessUnavailable, err)
			}
			envelopeID := VideoAdminReasonIdentity{ActorID: c.Caller.UserID, OutputAssetID: asset.PublicID, CommandKeyHash: hash, VersionNo: c.VersionNo}
			if replayed {
				if command.AssetID != asset.ID || command.TaskID != task.ID || command.RequestID != task.RequestID || command.UserID != owner.UserID || command.ProjectID != owner.ProjectID || !equalOptionalUint64(command.APIKeyID, owner.APIKeyID) || command.InitialVersion != c.VersionNo {
					return ErrVideoAdminCommandConflict
				}
				// prepared说明缺少完整提交事实，不能通过重放自动制造后审计或冒充已完成。
				if command.Status != "completed" || command.VersionNo != 2 {
					return ErrVideoAccessUnavailable
				}
				if _, err := s.reasons.Open(envelopeID, command.VideoAdminReasonEnvelope); err != nil {
					return ErrVideoAccessUnavailable
				}
				if command.ReasonHMAC != s.reasons.digest("reason", reason) {
					return ErrVideoAdminCommandConflict
				}
			} else {
				if pointer.ID != nil || asset.VersionNo != c.VersionNo || (asset.LifecycleState != "temporary" && asset.LifecycleState != "available") || asset.DeletedAt != nil || asset.MediaDeletedAt != nil {
					return ErrVideoAdminCommandConflict
				}
				// 已接收的删除计划优先完成自己的证明链，隔离不能插入正在清理的原任务。
				for _, table := range []string{"ai_video_media_deletions", "ai_video_asset_deletions"} {
					var count int64
					if err := tx.Table(table).Where("task_id=?", task.ID).Count(&count).Error; err != nil {
						return errors.Join(ErrVideoAccessUnavailable, err)
					}
					if count != 0 {
						return ErrVideoAdminCommandConflict
					}
				}
				sealed, err := s.reasons.Seal(envelopeID, []byte(reason))
				if err != nil {
					return ErrVideoAccessUnavailable
				}
				command = videoAdminOutputQuarantineRecord{ActorUserID: c.Caller.UserID, CommandKeyHash: hash, AssetID: asset.ID, TaskID: task.ID, RequestID: task.RequestID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, InitialVersion: c.VersionNo, FinalVersion: c.VersionNo + 1, InitialState: asset.LifecycleState, SnapshotSHA256: snapshot, Status: "prepared", VersionNo: 1, VideoAdminReasonEnvelope: *sealed, CreatedAt: time.Now().UTC()}
				command.BeforeAuditID, err = writeVideoAdminOutputAudit(ctx, tx, command, asset.PublicID, true)
				if err != nil {
					return errors.Join(ErrVideoAccessUnavailable, err)
				}
				if err := tx.Create(&command).Error; err != nil {
					if repository.IsDuplicateKeyForHandler(err) {
						return ErrVideoAdminCommandConflict
					}
					return errors.Join(ErrVideoAccessUnavailable, err)
				}
				changed := tx.Table("ai_gateway_assets").Where("id=? AND version_no=? AND lifecycle_state=? AND admin_quarantine_command_id IS NULL", asset.ID, c.VersionNo, asset.LifecycleState).Updates(map[string]any{"lifecycle_state": "quarantined", "version_no": gorm.Expr("version_no+1"), "updated_at": time.Now().UTC(), "admin_quarantine_command_id": command.ID})
				if changed.Error != nil {
					return errors.Join(ErrVideoAccessUnavailable, changed.Error)
				}
				if changed.RowsAffected != 1 {
					return ErrVideoAdminCommandConflict
				}
				afterID, err := writeVideoAdminOutputAudit(ctx, tx, command, asset.PublicID, false)
				if err != nil {
					return errors.Join(ErrVideoAccessUnavailable, err)
				}
				changed = tx.Model(&videoAdminOutputQuarantineRecord{}).Where("id=? AND status='prepared' AND version_no=1", command.ID).Updates(map[string]any{"status": "completed", "version_no": 2, "after_audit_id": afterID})
				if changed.Error != nil {
					return errors.Join(ErrVideoAccessUnavailable, changed.Error)
				}
				if changed.RowsAffected != 1 {
					return ErrVideoAdminCommandConflict
				}
				command.Status, command.VersionNo, command.AfterAuditID = "completed", 2, &afterID
				if err := tx.Where("id=?", asset.ID).Take(&asset).Error; err != nil {
					return err
				}
				pointer.ID = &command.ID
			}
			current, err := videoAdminOutputSnapshot(tx, asset.ID)
			if err != nil {
				return err
			}
			if command.Status != "completed" || pointer.ID == nil || *pointer.ID != command.ID || asset.LifecycleState != "quarantined" || command.FinalVersion > asset.VersionNo || command.SnapshotSHA256 != current {
				return ErrVideoAccessUnavailable
			}
			if err := verifyVideoAdminOutputAudits(tx, command, asset.PublicID); err != nil {
				return err
			}
			d, err := videoAdminOutputDetails(tx, asset)
			if err != nil {
				return err
			}
			// 元数据查询可能等待数据库锁；完成查询后再做最后鉴权，过期身份不能提交隔离事实。
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:safety_review"); err != nil {
				return err
			}
			reply = &VideoAdminOutputQuarantineReply{VideoAdminOutputDetails: &d, Idempotent: replayed}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}
