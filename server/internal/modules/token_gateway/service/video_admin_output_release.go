package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// 申请和复核都从当前认证取得操作者，不接受外部maker/checker身份。
type VideoAdminOutputReleaseCommand struct {
	Caller                                              VideoCaller `json:"-"`
	AssetID, Action, ApprovalID, IdempotencyKey, Reason string      `json:"-"`
	VersionNo                                           uint64      `json:"-"`
}
type VideoAdminOutputReleaseReply struct {
	ApprovalID   string    `json:"approval_id"`
	AssetID      string    `json:"asset_id"`
	VideoID      string    `json:"video_id"`
	RequestID    string    `json:"request_id"`
	Status       string    `json:"status"`
	RestoreState string    `json:"restore_state"`
	VersionNo    uint64    `json:"version_no"`
	ExpiresAt    time.Time `json:"expires_at"`
	Idempotent   bool      `json:"idempotent"`
}

// 不可变申请保存原隔离与资产版本；操作审批期限不是媒体保留期限。
type videoOutputReleaseRequest struct {
	ID                                               uint64 `gorm:"primaryKey"`
	PublicID, CommandKeyHash                         string
	MakerUserID, AssetID, QuarantineID, AssetVersion uint64
	RestoreState                                     string
	SnapshotSHA256                                   string `gorm:"column:snapshot_sha256"`
	VideoAdminReasonEnvelope                         `gorm:"embedded"`
	BeforeAuditID, AfterAuditID                      uint64
	CreatedAt, ExpiresAt                             time.Time
}

func (videoOutputReleaseRequest) TableName() string { return "ai_video_output_release_requests" }

// 独立复核与执行同事务，prepared不能被读取为已完成；每个原隔离只能消费一次。
type videoOutputReleaseExecution struct {
	ID                                     uint64 `gorm:"primaryKey"`
	RequestID, QuarantineID, CheckerUserID uint64
	CommandKeyHash, Status                 string
	VersionNo                              uint64
	VideoAdminReasonEnvelope               `gorm:"embedded"`
	BeforeAuditID                          uint64
	AfterAuditID                           *uint64
	CreatedAt                              time.Time
}

func (videoOutputReleaseExecution) TableName() string { return "ai_video_output_release_executions" }

func releaseReasonID(actor uint64, asset, key string, version uint64) VideoAdminReasonIdentity {
	return VideoAdminReasonIdentity{ActorID: actor, OutputReleaseAssetID: asset, CommandKeyHash: key, VersionNo: version}
}

// 审计只存冻结公开审批号、资产、原版本与加密原因摘要，不存自由文本。
func writeOutputReleaseAudit(ctx context.Context, tx *gorm.DB, actor uint64, action, asset, approval, command, reason string, version uint64) (uint64, error) {
	raw, err := json.Marshal(map[string]any{"approval_id": approval, "asset_id": asset, "command_key_hash": command, "reason_hmac": reason, "asset_version": version})
	if err != nil {
		return 0, err
	}
	target, summary := "video_output_asset", string(raw)
	row := auditmodel.AuditLog{OperatorID: &actor, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &asset, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	if err := auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &row); err != nil {
		return 0, err
	}
	return row.ID, nil
}

func verifyOutputReleaseAudit(tx *gorm.DB, id, actor uint64, action, asset, approval, command, reason string, version uint64) error {
	var row auditmodel.AuditLog
	if err := tx.Where("id=?", id).Take(&row).Error; err != nil {
		return errors.Join(ErrVideoAccessUnavailable, err)
	}
	if row.OperatorID == nil || *row.OperatorID != actor || row.Module != "token_gateway" || row.Action != action || row.TargetType == nil || *row.TargetType != "video_output_asset" || row.TargetID == nil || *row.TargetID != asset || row.RequestSummary == nil {
		return ErrVideoAccessUnavailable
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(*row.RequestSummary), &fields) != nil || len(fields) != 5 {
		return ErrVideoAccessUnavailable
	}
	for key, value := range map[string]any{"approval_id": approval, "asset_id": asset, "command_key_hash": command, "reason_hmac": reason, "asset_version": version} {
		want, _ := json.Marshal(value)
		if string(fields[key]) != string(want) {
			return ErrVideoAccessUnavailable
		}
	}
	return nil
}

// 两次真实认证请求完成maker/checker；恢复原状态不代表绕过G5交付或其他角色的安全限制。
func (s *VideoAdminService) ReleaseOutput(ctx context.Context, c VideoAdminOutputReleaseCommand) (*VideoAdminOutputReleaseReply, error) {
	if !s.WritesReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if !utf8.ValidString(reason) || reason == "" || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || c.VersionNo == 0 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) || (c.Action != "request" && c.Action != "approve") || (c.Action == "request" && c.ApprovalID != "") || (c.Action == "approve" && !videoBillingPublicID.MatchString(c.ApprovalID)) {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result *VideoAdminOutputReleaseReply
	err := retryVideoBillingTransaction(ctx, func() error {
		result = nil
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:safety_review"); err != nil {
				return err
			}
			if !videoBillingPublicID.MatchString(c.AssetID) {
				return repository.ErrVideoAssetNotFound
			}
			var identity struct {
				PublicID          string
				UserID, ProjectID uint64
				APIKeyID          *uint64
			}
			if err := tx.Table("ai_gateway_assets a").Select("t.public_id,t.user_id,t.project_id,t.api_key_id").Joins("JOIN ai_gateway_tasks t ON t.id=a.task_id AND t.request_id=a.request_id AND t.user_id=a.user_id AND t.project_id=a.project_id").Where("a.public_id=? AND a.modality='video' AND t.capability='video.generate'", c.AssetID).Take(&identity).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoAssetNotFound)
			}
			owner := repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, identity.PublicID, owner)
			if err != nil {
				return err
			}
			var asset model.AIImageAsset
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id=? AND task_id=? AND request_id=?", c.AssetID, task.ID, task.RequestID).Take(&asset).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoAssetNotFound)
			}
			if _, err := videoAdminOutputDetails(tx, asset); err != nil {
				return err
			}
			var pointer struct {
				ID *uint64 `gorm:"column:admin_quarantine_command_id"`
			}
			if err := tx.Table("ai_gateway_assets").Select("admin_quarantine_command_id").Where("id=?", asset.ID).Take(&pointer).Error; err != nil {
				return err
			}
			hash := videoBillingDigest(fmt.Sprintf("video-output-release:%s:%d:%s", c.Action, c.Caller.UserID, c.IdempotencyKey))
			var request videoOutputReleaseRequest
			replayed := false
			if c.Action == "request" {
				err = tx.Where("maker_user_id=? AND command_key_hash=?", c.Caller.UserID, hash).Take(&request).Error
				replayed = err == nil
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if !replayed {
					q, err := s.outputReleaseCandidateTx(tx, asset, pointer.ID, c.VersionNo)
					if err != nil {
						return err
					}
					var entropy [24]byte
					if _, err := rand.Read(entropy[:]); err != nil {
						return err
					}
					sealed, err := s.reasons.Seal(releaseReasonID(c.Caller.UserID, asset.PublicID, hash, c.VersionNo), []byte(reason))
					if err != nil {
						return ErrVideoAccessUnavailable
					}
					now := time.Now().UTC()
					request = videoOutputReleaseRequest{PublicID: "vapproval_" + hex.EncodeToString(entropy[:]), CommandKeyHash: hash, MakerUserID: c.Caller.UserID, AssetID: asset.ID, QuarantineID: q.ID, AssetVersion: c.VersionNo, RestoreState: q.InitialState, SnapshotSHA256: q.SnapshotSHA256, VideoAdminReasonEnvelope: *sealed, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
					request.BeforeAuditID, err = writeOutputReleaseAudit(ctx, tx, c.Caller.UserID, "video_output_release_request_before", asset.PublicID, request.PublicID, hash, request.ReasonHMAC, c.VersionNo)
					if err != nil {
						return err
					}
					request.AfterAuditID, err = writeOutputReleaseAudit(ctx, tx, c.Caller.UserID, "video_output_release_request_after", asset.PublicID, request.PublicID, hash, request.ReasonHMAC, c.VersionNo)
					if err != nil {
						return err
					}
					if err := tx.Create(&request).Error; err != nil {
						return err
					}
				}
				if request.AssetID != asset.ID || request.AssetVersion != c.VersionNo {
					return ErrVideoAdminCommandConflict
				}
				if _, err := s.reasons.Open(releaseReasonID(request.MakerUserID, asset.PublicID, request.CommandKeyHash, request.AssetVersion), request.VideoAdminReasonEnvelope); err != nil {
					return ErrVideoAccessUnavailable
				}
				if request.ReasonHMAC != s.reasons.digest("reason", reason) {
					return ErrVideoAdminCommandConflict
				}
			} else {
				if err := tx.Where("public_id=? AND asset_id=?", c.ApprovalID, asset.ID).Take(&request).Error; err != nil {
					return videoAccessReadError(err, repository.ErrVideoAssetNotFound)
				}
				if request.MakerUserID == c.Caller.UserID {
					return ErrVideoAdminForbidden
				}
				if request.AssetVersion != c.VersionNo {
					return ErrVideoAdminCommandConflict
				}
			}
			if err := s.verifyOutputReleaseRequest(tx, request, asset.PublicID); err != nil {
				return err
			}
			var execution videoOutputReleaseExecution
			execErr := tx.Where("quarantine_id=?", request.QuarantineID).Take(&execution).Error
			if execErr != nil && !errors.Is(execErr, gorm.ErrRecordNotFound) {
				return execErr
			}
			status := "pending"
			if execErr == nil {
				if execution.RequestID != request.ID {
					return ErrVideoAdminCommandConflict
				}
				if c.Action == "approve" {
					if execution.CheckerUserID != c.Caller.UserID || execution.CommandKeyHash != hash {
						return ErrVideoAdminCommandConflict
					}
					if _, err := s.reasons.Open(releaseReasonID(c.Caller.UserID, asset.PublicID, hash, c.VersionNo), execution.VideoAdminReasonEnvelope); err != nil {
						return ErrVideoAccessUnavailable
					}
					if execution.ReasonHMAC != s.reasons.digest("reason", reason) {
						return ErrVideoAdminCommandConflict
					}
				}
				if err := s.verifyOutputReleaseExecution(tx, request, execution, asset); err != nil {
					return err
				}
				replayed, status = true, "released"
			} else if c.Action == "approve" {
				if !request.ExpiresAt.After(time.Now().UTC()) {
					return ErrVideoAdminCommandConflict
				}
				q, err := s.outputReleaseCandidateTx(tx, asset, pointer.ID, c.VersionNo)
				if err != nil {
					return err
				}
				if q.ID != request.QuarantineID || q.SnapshotSHA256 != request.SnapshotSHA256 || q.InitialState != request.RestoreState {
					return ErrVideoAdminCommandConflict
				}
				// 发起者权限也必须仍有效，但不能伪造其JWT；这里只复验锁定的用户/IAM/MFA事实。
				if _, err := s.authorizeReleaseMakerTx(ctx, tx, request.MakerUserID); err != nil {
					return err
				}
				sealed, err := s.reasons.Seal(releaseReasonID(c.Caller.UserID, asset.PublicID, hash, c.VersionNo), []byte(reason))
				if err != nil {
					return ErrVideoAccessUnavailable
				}
				execution = videoOutputReleaseExecution{RequestID: request.ID, QuarantineID: request.QuarantineID, CheckerUserID: c.Caller.UserID, CommandKeyHash: hash, Status: "prepared", VersionNo: 1, VideoAdminReasonEnvelope: *sealed, CreatedAt: time.Now().UTC()}
				execution.BeforeAuditID, err = writeOutputReleaseAudit(ctx, tx, c.Caller.UserID, "video_output_release_approve_before", asset.PublicID, request.PublicID, hash, execution.ReasonHMAC, c.VersionNo)
				if err != nil {
					return err
				}
				if err := tx.Create(&execution).Error; err != nil {
					return err
				}
				changed := tx.Table("ai_gateway_assets").Where("id=? AND version_no=? AND lifecycle_state='quarantined' AND admin_quarantine_command_id=?", asset.ID, c.VersionNo, request.QuarantineID).Updates(map[string]any{"lifecycle_state": request.RestoreState, "version_no": c.VersionNo + 1, "updated_at": time.Now().UTC(), "admin_quarantine_command_id": nil})
				if changed.Error != nil {
					return changed.Error
				}
				if changed.RowsAffected != 1 {
					return ErrVideoAdminCommandConflict
				}
				after, err := writeOutputReleaseAudit(ctx, tx, c.Caller.UserID, "video_output_release_approve_after", asset.PublicID, request.PublicID, hash, execution.ReasonHMAC, c.VersionNo+1)
				if err != nil {
					return err
				}
				changed = tx.Model(&videoOutputReleaseExecution{}).Where("id=? AND status='prepared' AND version_no=1", execution.ID).Updates(map[string]any{"status": "completed", "version_no": 2, "after_audit_id": after})
				if changed.Error != nil {
					return changed.Error
				}
				if changed.RowsAffected != 1 {
					return ErrVideoAdminCommandConflict
				}
				execution.Status, execution.VersionNo, execution.AfterAuditID = "completed", 2, &after
				if err := tx.Where("id=?", asset.ID).Take(&asset).Error; err != nil {
					return err
				}
				if err := s.verifyOutputReleaseExecution(tx, request, execution, asset); err != nil {
					return err
				}
				status = "released"
			} else if !request.ExpiresAt.After(time.Now().UTC()) {
				status = "expired"
			}
			var makerUntil *videoReleaseMakerDeadline
			if c.Action == "approve" && status == "released" && !replayed {
				makerUntil, err = s.authorizeReleaseMakerTx(ctx, tx, request.MakerUserID)
				if err != nil {
					return err
				}
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:safety_review"); err != nil {
				return err
			}
			// 最后的checker查询也可能等待；其后统一检查审批、媒体及maker资格截止，不再做数据库读取。
			if c.Action == "approve" && status == "released" && !replayed {
				now := time.Now().UTC()
				if !request.ExpiresAt.After(now) || !asset.ExpiresAt.After(now) {
					return ErrVideoAdminCommandConflict
				}
				if makerUntil != nil && makerUntil.permissionUntil != nil && !makerUntil.permissionUntil.After(now) {
					return ErrVideoAdminForbidden
				}
				if makerUntil != nil && makerUntil.mfaUntil != nil && !makerUntil.mfaUntil.After(now) {
					return ErrVideoAdminMFA
				}
			}
			result = &VideoAdminOutputReleaseReply{ApprovalID: request.PublicID, AssetID: asset.PublicID, VideoID: task.PublicID, RequestID: task.RequestID, Status: status, RestoreState: request.RestoreState, VersionNo: asset.VersionNo, ExpiresAt: request.ExpiresAt, Idempotent: replayed}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		if repository.IsDuplicateKeyForHandler(err) {
			return nil, ErrVideoAdminCommandConflict
		}
		return nil, err
	}
	return result, nil
}

func (s *VideoAdminService) outputReleaseCandidateTx(tx *gorm.DB, a model.AIImageAsset, pointer *uint64, version uint64) (*videoAdminOutputQuarantineRecord, error) {
	if pointer == nil || a.VersionNo != version || a.LifecycleState != "quarantined" || a.LegalHold || a.DisputeStatus == model.AIImageDisputeOpen || a.DeletedAt != nil || a.MediaDeletedAt != nil || !a.ExpiresAt.After(time.Now().UTC()) {
		return nil, ErrVideoAdminCommandConflict
	}
	var q videoAdminOutputQuarantineRecord
	if err := tx.Where("id=? AND asset_id=? AND status='completed' AND version_no=2", *pointer, a.ID).Take(&q).Error; err != nil {
		return nil, errors.Join(ErrVideoAccessUnavailable, err)
	}
	hash, err := videoAdminOutputSnapshot(tx, a.ID)
	if err != nil {
		return nil, err
	}
	if hash != q.SnapshotSHA256 || q.FinalVersion > a.VersionNo || q.TaskID != a.TaskID || q.RequestID != a.RequestID {
		return nil, ErrVideoAccessUnavailable
	}
	if q.InitialState == "available" && (a.ModerationStatus != "passed" || a.ExplicitLabelStatus != "applied" || a.ImplicitLabelStatus != "applied") {
		return nil, ErrVideoAdminCommandConflict
	}
	// 人工解除不能把真实审核拒绝或标识失败包装成普通行政限制。
	if a.ModerationStatus == "rejected" || a.ModerationStatus == "error" || a.ExplicitLabelStatus == "failed" || a.ImplicitLabelStatus == "failed" {
		return nil, ErrVideoAdminCommandConflict
	}
	if q.InitialState != "available" && q.InitialState != "temporary" {
		return nil, ErrVideoAccessUnavailable
	}
	if err := verifyVideoAdminOutputAudits(tx, q, a.PublicID); err != nil {
		return nil, err
	}
	for _, table := range []string{"ai_video_media_deletions", "ai_video_asset_deletions"} {
		var count int64
		if err := tx.Table(table).Where("task_id=?", a.TaskID).Count(&count).Error; err != nil {
			return nil, err
		}
		if count != 0 {
			return nil, ErrVideoAdminCommandConflict
		}
	}
	return &q, nil
}

func (s *VideoAdminService) verifyOutputReleaseRequest(tx *gorm.DB, r videoOutputReleaseRequest, asset string) error {
	if _, err := s.reasons.Open(releaseReasonID(r.MakerUserID, asset, r.CommandKeyHash, r.AssetVersion), r.VideoAdminReasonEnvelope); err != nil {
		return ErrVideoAccessUnavailable
	}
	for _, before := range []bool{true, false} {
		id, action := r.AfterAuditID, "video_output_release_request_after"
		if before {
			id, action = r.BeforeAuditID, "video_output_release_request_before"
		}
		if err := verifyOutputReleaseAudit(tx, id, r.MakerUserID, action, asset, r.PublicID, r.CommandKeyHash, r.ReasonHMAC, r.AssetVersion); err != nil {
			return err
		}
	}
	return nil
}
func (s *VideoAdminService) verifyOutputReleaseExecution(tx *gorm.DB, r videoOutputReleaseRequest, e videoOutputReleaseExecution, a model.AIImageAsset) error {
	if e.Status != "completed" || e.VersionNo != 2 || e.AfterAuditID == nil || e.CheckerUserID == r.MakerUserID || e.RequestID != r.ID || e.QuarantineID != r.QuarantineID || a.VersionNo < r.AssetVersion+1 {
		return ErrVideoAccessUnavailable
	}
	if _, err := s.reasons.Open(releaseReasonID(e.CheckerUserID, a.PublicID, e.CommandKeyHash, r.AssetVersion), e.VideoAdminReasonEnvelope); err != nil {
		return ErrVideoAccessUnavailable
	}
	for _, before := range []bool{true, false} {
		id, action, version := *e.AfterAuditID, "video_output_release_approve_after", r.AssetVersion+1
		if before {
			id, action, version = e.BeforeAuditID, "video_output_release_approve_before", r.AssetVersion
		}
		if err := verifyOutputReleaseAudit(tx, id, e.CheckerUserID, action, a.PublicID, r.PublicID, e.CommandKeyHash, e.ReasonHMAC, version); err != nil {
			return err
		}
	}
	return nil
}
