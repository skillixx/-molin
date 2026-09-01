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

type VideoAdminInputQuarantineCommand struct {
	Caller                               VideoCaller `json:"-"`
	InputAssetID, IdempotencyKey, Reason string      `json:"-"`
	VersionNo                            uint64      `json:"-"`
}
type VideoAdminInputQuarantineReply struct {
	*VideoAdminInputDetails
	Idempotent bool `json:"idempotent"`
}
type videoAdminInputQuarantineRecord struct {
	ActorUserID                     uint64  `gorm:"primaryKey" json:"-"`
	CommandKeyHash                  string  `gorm:"primaryKey" json:"-"`
	InputAssetID, UserID, ProjectID uint64  `json:"-"`
	APIKeyID                        *uint64 `json:"-"`
	InitialVersion, FinalVersion    uint64  `json:"-"`
	InitialState                    string  `json:"-"`
	VideoAdminReasonEnvelope        `gorm:"embedded" json:"-"`
	BeforeAuditID, AfterAuditID     uint64    `json:"-"`
	CreatedAt                       time.Time `json:"-"`
}

func (videoAdminInputQuarantineRecord) TableName() string { return "ai_video_admin_input_quarantines" }

type videoAdminInputAuditSummary struct {
	CommandKeyHash string `json:"command_key_hash"`
	ReasonHMAC     string `json:"reason_hmac"`
	ReasonLength   uint32 `json:"reason_length"`
	InputAssetID   string `json:"input_asset_id"`
	InitialVersion uint64 `json:"initial_version"`
	CurrentVersion uint64 `json:"current_version"`
	Result         string `json:"result"`
}

func videoAdminInputAuditData(c videoAdminInputQuarantineRecord, id string, before bool) videoAdminInputAuditSummary {
	d := videoAdminInputAuditSummary{CommandKeyHash: c.CommandKeyHash, ReasonHMAC: c.ReasonHMAC, ReasonLength: c.ReasonLength, InputAssetID: id, InitialVersion: c.InitialVersion, CurrentVersion: c.FinalVersion, Result: "quarantined"}
	if before {
		d.CurrentVersion = c.InitialVersion
		d.Result = "requested"
	}
	return d
}
func writeVideoAdminInputAudit(ctx context.Context, tx *gorm.DB, c videoAdminInputQuarantineRecord, id string, before bool) (uint64, error) {
	raw, err := json.Marshal(videoAdminInputAuditData(c, id, before))
	if err != nil {
		return 0, err
	}
	action := "video_admin_input_quarantine_after"
	if before {
		action = "video_admin_input_quarantine_before"
	}
	target, summary := "video_input_asset", string(raw)
	row := auditmodel.AuditLog{OperatorID: &c.ActorUserID, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &id, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	if err := auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &row); err != nil {
		return 0, err
	}
	return row.ID, nil
}
func verifyVideoAdminInputAudit(tx *gorm.DB, c videoAdminInputQuarantineRecord, id string) error {
	for _, before := range []bool{true, false} {
		auditID, action := c.AfterAuditID, "video_admin_input_quarantine_after"
		if before {
			auditID, action = c.BeforeAuditID, "video_admin_input_quarantine_before"
		}
		var row auditmodel.AuditLog
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", auditID).Take(&row).Error; err != nil {
			return errors.Join(ErrVideoAccessUnavailable, err)
		}
		if row.OperatorID == nil || *row.OperatorID != c.ActorUserID || row.Module != "token_gateway" || row.Action != action || row.TargetType == nil || *row.TargetType != "video_input_asset" || row.TargetID == nil || *row.TargetID != id || row.RequestSummary == nil {
			return ErrVideoAccessUnavailable
		}
		var d videoAdminInputAuditSummary
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(*row.RequestSummary), &d) != nil || json.Unmarshal([]byte(*row.RequestSummary), &fields) != nil || len(fields) != 7 || !reflect.DeepEqual(d, videoAdminInputAuditData(c, id, before)) {
			return ErrVideoAccessUnavailable
		}
	}
	return nil
}

// 输入隔离只增加使用限制；不读取正文、不改原审核结论、不变更任务或释放执行租约。
func (s *VideoAdminService) QuarantineInput(ctx context.Context, c VideoAdminInputQuarantineCommand) (*VideoAdminInputQuarantineReply, error) {
	if !s.WritesReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if !utf8.ValidString(reason) || reason == "" || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || c.VersionNo == 0 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var reply *VideoAdminInputQuarantineReply
	err := retryVideoBillingTransaction(ctx, func() error {
		reply = nil
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:safety_review"); err != nil {
				return err
			}
			if !videoBillingPublicID.MatchString(c.InputAssetID) {
				return repository.ErrVideoInputNotFound
			}
			var asset model.AIGatewayInputAsset
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id=?", c.InputAssetID).Take(&asset).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoInputNotFound)
			}
			key, upload, source, err := videoAdminInputSource(tx, asset)
			if err != nil {
				return err
			}
			owner := repository.VideoOwner{UserID: asset.UserID, ProjectID: asset.ProjectID, APIKeyID: key}
			hash := videoBillingDigest(fmt.Sprintf("video-admin-input-quarantine:%d:%s", c.Caller.UserID, c.IdempotencyKey))
			var command videoAdminInputQuarantineRecord
			err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("actor_user_id=? AND command_key_hash=?", c.Caller.UserID, hash).Take(&command).Error
			replayed := err == nil
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.Join(ErrVideoAccessUnavailable, err)
			}
			identity := VideoAdminReasonIdentity{ActorID: c.Caller.UserID, InputAssetID: asset.PublicID, CommandKeyHash: hash, VersionNo: c.VersionNo}
			if replayed {
				if command.InputAssetID != asset.ID || command.UserID != asset.UserID || command.ProjectID != asset.ProjectID || !equalOptionalUint64(command.APIKeyID, key) || command.InitialVersion != c.VersionNo {
					return ErrVideoAdminCommandConflict
				}
				if _, err := s.reasons.Open(identity, command.VideoAdminReasonEnvelope); err != nil {
					return ErrVideoAccessUnavailable
				}
				if command.ReasonHMAC != s.reasons.digest("reason", reason) {
					return ErrVideoAdminCommandConflict
				}
			} else {
				if asset.VersionNo != c.VersionNo {
					return ErrVideoAdminCommandConflict
				}
				sealed, err := s.reasons.Seal(identity, []byte(reason))
				if err != nil {
					return ErrVideoAccessUnavailable
				}
				command = videoAdminInputQuarantineRecord{ActorUserID: c.Caller.UserID, CommandKeyHash: hash, InputAssetID: asset.ID, UserID: asset.UserID, ProjectID: asset.ProjectID, APIKeyID: key, InitialVersion: c.VersionNo, InitialState: asset.LifecycleState, VideoAdminReasonEnvelope: *sealed, CreatedAt: time.Now().UTC()}
				command.BeforeAuditID, err = writeVideoAdminInputAudit(ctx, tx, command, asset.PublicID, true)
				if err != nil {
					return errors.Join(ErrVideoAccessUnavailable, err)
				}
				updated, err := repository.NewVideoInputAssetRepository(tx).QuarantineForManagementTx(ctx, tx, asset.PublicID, owner, c.VersionNo, time.Now().UTC())
				if err != nil {
					if errors.Is(err, repository.ErrVideoInputConflict) {
						return ErrVideoAdminCommandConflict
					}
					return errors.Join(ErrVideoAccessUnavailable, err)
				}
				asset = *updated
				command.FinalVersion = asset.VersionNo
				command.AfterAuditID, err = writeVideoAdminInputAudit(ctx, tx, command, asset.PublicID, false)
				if err != nil {
					return errors.Join(ErrVideoAccessUnavailable, err)
				}
				if err := tx.Create(&command).Error; err != nil {
					if repository.IsDuplicateKeyForHandler(err) {
						return ErrVideoAdminCommandConflict
					}
					return errors.Join(ErrVideoAccessUnavailable, err)
				}
			}
			if command.FinalVersion != command.InitialVersion+1 || command.FinalVersion > asset.VersionNo {
				return ErrVideoAccessUnavailable
			}
			if err := verifyVideoAdminInputAudit(tx, command, asset.PublicID); err != nil {
				return err
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:safety_review"); err != nil {
				return err
			}
			metadata := videoAdminInputMetadata(asset, key, upload, source)
			reply = &VideoAdminInputQuarantineReply{VideoAdminInputDetails: &metadata, Idempotent: replayed}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}
