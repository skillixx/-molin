package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	auditmodel "molin/server/internal/modules/audit/model"
	auditrepo "molin/server/internal/modules/audit/repository"
	"molin/server/internal/modules/token_gateway/model"
)

type VideoProjectGrantCommand struct {
	Caller                                VideoCaller `json:"-"`
	ProjectID, VersionNo                  uint64      `json:"-"`
	Action, Model, IdempotencyKey, Reason string      `json:"-"`
}
type VideoProjectGrantReply struct {
	ProjectID  uint64 `json:"project_id"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	VersionNo  uint64 `json:"version_no"`
	Idempotent bool   `json:"idempotent"`
}
type videoProjectGrantCommandRecord struct {
	ID                                  uint64 `gorm:"primaryKey"`
	PublicID, Action, CommandKeyHash    string
	ActorUserID, OwnerUserID, ProjectID uint64
	ModelCode, InputSHA256              string
	InitialVersion, ResultVersion       uint64
	ResultJSON                          json.RawMessage
	ResultSHA256                        string
	VideoAdminReasonEnvelope            `gorm:"embedded"`
	BeforeAuditID, AfterAuditID         uint64
	CreatedAt                           time.Time
}

func (videoProjectGrantCommandRecord) TableName() string { return "ai_video_project_grant_commands" }

func projectGrantReasonID(c videoProjectGrantCommandRecord) VideoAdminReasonIdentity {
	return VideoAdminReasonIdentity{ActorID: c.ActorUserID, ProjectID: c.ProjectID, ProjectModelCode: c.ModelCode, ProjectAction: c.Action, CommandKeyHash: c.CommandKeyHash, VersionNo: c.InitialVersion + 1}
}
func projectGrantAuditFields(c videoProjectGrantCommandRecord) map[string]any {
	return map[string]any{"command_id": c.PublicID, "command_key_hash": c.CommandKeyHash, "project_id": c.ProjectID, "model": c.ModelCode, "initial_version": c.InitialVersion, "input_sha256": c.InputSHA256, "reason_hmac": c.ReasonHMAC}
}
func writeProjectGrantAudit(ctx context.Context, tx *gorm.DB, c videoProjectGrantCommandRecord, before bool) (uint64, error) {
	action := "video_project_grant_" + c.Action + "_after"
	if before {
		action = "video_project_grant_" + c.Action + "_before"
	}
	raw, err := json.Marshal(projectGrantAuditFields(c))
	if err != nil {
		return 0, err
	}
	target, summary := "video_project_grant", string(raw)
	a := auditmodel.AuditLog{OperatorID: &c.ActorUserID, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &c.ModelCode, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	err = auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &a)
	return a.ID, err
}
func verifyProjectGrantAudits(tx *gorm.DB, c videoProjectGrantCommandRecord) error {
	want, _ := json.Marshal(projectGrantAuditFields(c))
	wantHash, _ := modelDraftResultHash(want)
	for _, entry := range []struct {
		id     uint64
		action string
	}{{c.BeforeAuditID, "video_project_grant_" + c.Action + "_before"}, {c.AfterAuditID, "video_project_grant_" + c.Action + "_after"}} {
		var a auditmodel.AuditLog
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", entry.id).Take(&a).Error; err != nil {
			return errors.Join(ErrVideoAccessUnavailable, err)
		}
		if a.OperatorID == nil || *a.OperatorID != c.ActorUserID || a.Module != "token_gateway" || a.Action != entry.action || a.TargetType == nil || *a.TargetType != "video_project_grant" || a.TargetID == nil || *a.TargetID != c.ModelCode || a.RequestSummary == nil {
			return ErrVideoAccessUnavailable
		}
		gotHash, err := modelDraftResultHash([]byte(*a.RequestSummary))
		if err != nil || gotHash != wantHash {
			return ErrVideoAccessUnavailable
		}
	}
	return nil
}

func (s *VideoAdminService) verifyProjectGrantCommand(tx *gorm.DB, c videoProjectGrantCommandRecord, reason string) error {
	var stored videoProjectGrantCommandRecord
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", c.ID).Take(&stored).Error; err != nil {
		return errors.Join(ErrVideoAccessUnavailable, err)
	}
	if stored.PublicID != c.PublicID || stored.Action != c.Action || stored.CommandKeyHash != c.CommandKeyHash || stored.ActorUserID != c.ActorUserID || stored.OwnerUserID != c.OwnerUserID || stored.ProjectID != c.ProjectID || stored.ModelCode != c.ModelCode || stored.InputSHA256 != c.InputSHA256 || stored.InitialVersion != c.InitialVersion || stored.ResultVersion != c.ResultVersion || stored.ResultSHA256 != c.ResultSHA256 || stored.BeforeAuditID != c.BeforeAuditID || stored.AfterAuditID != c.AfterAuditID || stored.KeyVersion != c.KeyVersion || stored.AADSHA256 != c.AADSHA256 || stored.CiphertextSHA256 != c.CiphertextSHA256 || stored.ReasonHMAC != c.ReasonHMAC || stored.ReasonLength != c.ReasonLength || !bytes.Equal(stored.Nonce, c.Nonce) || !bytes.Equal(stored.Ciphertext, c.Ciphertext) {
		return ErrVideoAccessUnavailable
	}
	plain, err := s.reasons.Open(projectGrantReasonID(stored), stored.VideoAdminReasonEnvelope)
	if err != nil || string(plain) != reason {
		return ErrVideoAccessUnavailable
	}
	if err := verifyProjectGrantAudits(tx, stored); err != nil {
		return err
	}
	hash, err := modelDraftResultHash(stored.ResultJSON)
	if err != nil || hash != stored.ResultSHA256 {
		return ErrVideoAccessUnavailable
	}
	var result VideoProjectGrantReply
	if json.Unmarshal(stored.ResultJSON, &result) != nil || result.ProjectID != stored.ProjectID || result.Model != stored.ModelCode || result.VersionNo != stored.ResultVersion || result.Status != map[string]string{"grant": "active", "revoke": "revoked"}[stored.Action] || result.Idempotent {
		return ErrVideoAccessUnavailable
	}
	return nil
}

func (s *VideoAdminService) ManageProjectGrant(ctx context.Context, c VideoProjectGrantCommand) (*VideoProjectGrantReply, error) {
	if !s.WritesReady() || c.ProjectID == 0 || !videoAdminModelCode.MatchString(c.Model) || len(c.Model) > 128 || (c.Action != "grant" && c.Action != "revoke") || c.VersionNo >= math.MaxUint64-1 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) {
		return nil, ErrVideoAdminCommandInvalid
	}
	reason := strings.TrimSpace(c.Reason)
	if reason == "" || !utf8.ValidString(reason) || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage") }); err != nil {
		return nil, err
	}
	input, _ := json.Marshal([]any{c.Action, c.ProjectID, c.Model, c.VersionNo})
	inputHash, keyHash := videoPayloadSHA256(input), videoBillingDigest("video_project_grant_"+c.Action+"\n"+c.IdempotencyKey)
	var reply *VideoProjectGrantReply
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var ownerProbe struct{ UserID uint64 }
			if err := tx.Table("ai_projects").Select("user_id").Where("id=?", c.ProjectID).Take(&ownerProbe).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrVideoAdminCommandConflict
				}
				return errors.Join(ErrVideoAccessUnavailable, err)
			}
			ids := []uint64{c.Caller.UserID, ownerProbe.UserID}
			if ids[0] > ids[1] {
				ids[0], ids[1] = ids[1], ids[0]
			}
			if ids[0] == ids[1] {
				ids = ids[:1]
			}
			var lockedUsers []struct{ ID uint64 }
			if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id IN ?", ids).Order("id").Find(&lockedUsers).Error; err != nil {
				return errors.Join(ErrVideoAccessUnavailable, err)
			}
			if len(lockedUsers) != len(ids) {
				return ErrVideoAdminCommandConflict
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage"); err != nil {
				return err
			}
			var project struct {
				ID, UserID                         uint64
				Status, UserStatus, RealNameStatus string
			}
			if err := tx.Raw("SELECT p.id,p.user_id,p.status,u.status AS user_status,u.real_name_status FROM ai_projects p JOIN users u ON u.id=p.user_id WHERE p.id=? FOR UPDATE", c.ProjectID).Scan(&project).Error; err != nil {
				return errors.Join(ErrVideoAccessUnavailable, err)
			}
			if project.ID == 0 || project.UserID != ownerProbe.UserID {
				return ErrVideoAdminCommandConflict
			}
			var existing videoProjectGrantCommandRecord
			readErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("actor_user_id=? AND action=? AND command_key_hash=?", c.Caller.UserID, c.Action, keyHash).Take(&existing).Error
			if readErr == nil {
				plain, err := s.reasons.Open(projectGrantReasonID(existing), existing.VideoAdminReasonEnvelope)
				if err != nil {
					return ErrVideoAccessUnavailable
				}
				if err := verifyProjectGrantAudits(tx, existing); err != nil {
					return err
				}
				hash, err := modelDraftResultHash(existing.ResultJSON)
				if err != nil || hash != existing.ResultSHA256 {
					return ErrVideoAccessUnavailable
				}
				if existing.InputSHA256 != inputHash || string(plain) != reason {
					return ErrVideoAdminCommandConflict
				}
				var result VideoProjectGrantReply
				if json.Unmarshal(existing.ResultJSON, &result) != nil || result.VersionNo != existing.ResultVersion {
					return ErrVideoAccessUnavailable
				}
				result.Idempotent = true
				reply = &result
				return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage")
			}
			if !errors.Is(readErr, gorm.ErrRecordNotFound) {
				return readErr
			}
			// 撤销是失败关闭动作，即使Project、所有者或模型已经停用也必须可执行。
			if c.Action == "grant" {
				if project.Status != "active" || project.UserStatus != "active" || project.RealNameStatus != "verified" {
					return ErrVideoAdminCommandConflict
				}
				if _, err := videoPublishedModel(tx, c.Model, time.Now().UTC()); err != nil {
					return ErrVideoAdminCommandConflict
				}
			}
			var grant struct {
				ID, VersionNo uint64
				Status        string
			}
			grantErr := tx.Table("ai_project_model_capability_grants").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id,status,version_no").Where("project_id=? AND logical_model_code=? AND capability=?", c.ProjectID, c.Model, model.AIVideoCapability).Take(&grant).Error
			newRow := errors.Is(grantErr, gorm.ErrRecordNotFound)
			if grantErr != nil && !newRow {
				return grantErr
			}
			if newRow {
				if c.Action != "grant" || c.VersionNo != 0 {
					return ErrVideoAdminCommandConflict
				}
			} else if grant.VersionNo != c.VersionNo || grant.Status == map[string]string{"grant": "active", "revoke": "revoked"}[c.Action] {
				return ErrVideoAdminCommandConflict
			}
			publicID, err := newVideoHTTPID("vpg_")
			if err != nil {
				return err
			}
			command := videoProjectGrantCommandRecord{PublicID: publicID, Action: c.Action, CommandKeyHash: keyHash, ActorUserID: c.Caller.UserID, OwnerUserID: project.UserID, ProjectID: c.ProjectID, ModelCode: c.Model, InputSHA256: inputHash, InitialVersion: c.VersionNo, ResultVersion: c.VersionNo + 1, CreatedAt: time.Now().UTC()}
			env, err := s.reasons.Seal(projectGrantReasonID(command), []byte(reason))
			if err != nil {
				return err
			}
			command.VideoAdminReasonEnvelope = *env
			command.BeforeAuditID, err = writeProjectGrantAudit(ctx, tx, command, true)
			if err != nil {
				return err
			}
			status := map[string]string{"grant": "active", "revoke": "revoked"}[c.Action]
			now := time.Now().UTC()
			if newRow {
				row := map[string]any{"user_id": project.UserID, "project_id": c.ProjectID, "logical_model_code": c.Model, "capability": model.AIVideoCapability, "status": status, "version_no": 1, "granted_by": c.Caller.UserID, "created_at": now, "updated_at": now}
				if err := tx.Table("ai_project_model_capability_grants").Create(row).Error; err != nil {
					return err
				}
			} else {
				updates := map[string]any{"status": status, "version_no": c.VersionNo + 1, "updated_at": now}
				if c.Action == "grant" {
					updates["granted_by"] = c.Caller.UserID
				}
				r := tx.Table("ai_project_model_capability_grants").Where("id=? AND version_no=?", grant.ID, c.VersionNo).Updates(updates)
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected != 1 {
					return ErrVideoAdminCommandConflict
				}
			}
			reply = &VideoProjectGrantReply{ProjectID: c.ProjectID, Model: c.Model, Status: status, VersionNo: c.VersionNo + 1}
			command.ResultJSON, err = json.Marshal(reply)
			if err != nil {
				return err
			}
			command.ResultSHA256, err = modelDraftResultHash(command.ResultJSON)
			if err != nil {
				return err
			}
			command.AfterAuditID, err = writeProjectGrantAudit(ctx, tx, command, false)
			if err != nil {
				return err
			}
			if err := tx.Create(&command).Error; err != nil {
				return err
			}
			if err := s.verifyProjectGrantCommand(tx, command, reason); err != nil {
				return err
			}
			return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage")
		})
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}
