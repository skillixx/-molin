package service

import (
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
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type VideoModelPublicationCommand struct {
	Caller                              VideoCaller `json:"-"`
	ModelID, VersionNo, TargetVersionNo uint64      `json:"-"`
	Action, IdempotencyKey, Reason      string      `json:"-"`
}

type VideoModelPublicationReply struct {
	ModelID           uint64 `json:"model_id"`
	VersionNo         uint64 `json:"version_no"`
	ReleaseVersionNo  uint64 `json:"release_version_no"`
	PublicationStatus string `json:"publication_status"`
	Idempotent        bool   `json:"idempotent"`
}

// PublishModel只编排原模型和发布版本；不创建Quote/Hold、授权、Provider任务或钱包动作。
func (s *VideoAdminService) ManageModelPublication(ctx context.Context, c VideoModelPublicationCommand) (*VideoModelPublicationReply, error) {
	if !s.ModelDraftsReady() || (c.Action != "unpublish" && s.modelPublishing == nil) {
		return nil, ErrVideoAccessUnavailable
	}
	if c.ModelID == 0 || c.VersionNo == 0 || c.VersionNo >= math.MaxUint64-1 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) || (c.Action != "publish" && c.Action != "unpublish" && c.Action != "rollback") || (c.Action == "rollback" && c.TargetVersionNo == 0) || (c.Action != "rollback" && c.TargetVersionNo != 0) {
		return nil, ErrVideoAdminCommandInvalid
	}
	reason := strings.TrimSpace(c.Reason)
	if reason == "" || !utf8.ValidString(reason) || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	input, _ := json.Marshal([]any{c.Action, c.ModelID, c.VersionNo, c.TargetVersionNo})
	inputHash, keyHash := videoPayloadSHA256(input), videoBillingDigest("video_model_"+c.Action+"\n"+c.IdempotencyKey)
	var reply *VideoModelPublicationReply
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var actor struct{ ID uint64 }
			if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=?", c.Caller.UserID).Take(&actor).Error; err != nil {
				return errors.Join(ErrVideoAccessUnavailable, err)
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage"); err != nil {
				return err
			}
			var existing videoModelDraftRecord
			readErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("actor_user_id=? AND action=? AND command_key_hash=?", c.Caller.UserID, c.Action, keyHash).Take(&existing).Error
			if readErr == nil {
				plain, err := s.reasons.Open(modelDraftReasonID(existing), existing.VideoAdminReasonEnvelope)
				if err != nil {
					return ErrVideoAccessUnavailable
				}
				if err := verifyModelDraftAudits(tx, existing); err != nil {
					return err
				}
				hash, err := modelDraftResultHash(existing.ResultJSON)
				if err != nil || hash != existing.ResultSHA256 {
					return ErrVideoAccessUnavailable
				}
				if existing.InputSHA256 != inputHash || string(plain) != reason {
					return ErrVideoAdminCommandConflict
				}
				var result VideoModelPublicationReply
				if json.Unmarshal(existing.ResultJSON, &result) != nil || result.ModelID != existing.ModelID || result.VersionNo != existing.ResultVersion {
					return ErrVideoAccessUnavailable
				}
				result.Idempotent = true
				reply = &result
				return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage")
			}
			if !errors.Is(readErr, gorm.ErrRecordNotFound) {
				return readErr
			}
			// 所有视频发布、回滚、下架先争同一数据库行，再判断默认模型，禁止两个模型并发成为默认。
			var guard struct{ ID uint64 }
			if err := tx.Table("ai_video_model_publication_guard").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=1").Take(&guard).Error; err != nil {
				return errors.Join(ErrVideoAccessUnavailable, err)
			}
			var m model.TokenModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND modality='video'", c.ModelID).Take(&m).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return repository.ErrTokenModelNotFound
				}
				return err
			}
			var state videoModelDraftState
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("model_id=?", m.ID).Take(&state).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrVideoAdminCommandConflict
				}
				return err
			}
			hash, err := modelDraftSnapshotHash(m)
			if err != nil || state.VersionNo != c.VersionNo || state.SnapshotSHA256 != hash {
				return ErrVideoAdminCommandConflict
			}
			if c.Action == "rollback" {
				var target model.AIModelReleaseVersion
				if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("model_id=? AND version_no=?", m.ID, c.TargetVersionNo).Take(&target).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return ErrVideoAdminCommandConflict
					}
					return err
				}
				var snapshot struct {
					model.TokenModelReleaseSnapshot
					VideoExecution videoModelExecutionBinding `json:"video_execution"`
				}
				if json.Unmarshal(target.SnapshotJSON, &snapshot) != nil || snapshot.Modality != "video" || snapshot.LogicalModelCode != m.LogicalModelCode || snapshot.VideoExecution.SchemaVersion != 1 || snapshot.VideoExecution.Purpose != VideoPricePurposeNonCommercialFixture || snapshot.VideoExecution.ExecutionDriver != "fake-native-async" || snapshot.VideoExecution.ProviderContract != videoRunwarePublicationContract || snapshot.VideoExecution.ProviderModel != "runway:1@2" {
					return ErrVideoAdminCommandConflict
				}
				m.DisplayName, m.ProviderName, m.Description = snapshot.DisplayName, snapshot.ProviderName, snapshot.Description
				m.CapabilitiesJSON, m.VideoContractJSON, m.ProductID = snapshot.Capabilities, snapshot.VideoContract, snapshot.ProductID
				m.IntroURL, m.IntroURLHealthStatus = snapshot.IntroURL, snapshot.IntroURLHealthStatus
				m.DocsURL, m.DocsURLHealthStatus = snapshot.DocsURL, snapshot.DocsURLHealthStatus
				m.QuickStartURL, m.QuickStartURLHealthStatus = snapshot.QuickStartURL, snapshot.QuickStartURLHealthStatus
				m.VisibleScope, m.TargetAudience, m.ContextWindow = snapshot.VisibleScope, snapshot.TargetAudience, snapshot.ContextWindow
			}
			var snapshot json.RawMessage
			var price *model.AIPriceVersion
			if c.Action != "unpublish" {
				snapshot, price, err = s.modelPublicationSnapshot(ctx, tx, m)
				if err != nil {
					return err
				}
				contract, err := ParseVideoModelContract(m.VideoContractJSON, m.ProductID)
				if err != nil {
					return err
				}
				if contract.DefaultModel {
					var others []struct{ ID uint64 }
					if err := tx.Table("token_models AS m").Clauses(clause.Locking{Strength: "SHARE"}).Select("m.id").Joins("JOIN ai_model_release_versions r ON r.model_id=m.id AND r.version_no=m.release_version_no").Where("m.id<>? AND m.modality='video' AND m.status='active' AND r.status='active' AND JSON_UNQUOTE(JSON_EXTRACT(r.snapshot_json,'$.video_contract.default_model'))='true'", m.ID).Find(&others).Error; err != nil {
						return err
					}
					if len(others) > 0 {
						return ErrVideoAdminCommandConflict
					}
				}
			} else if m.Status != "active" || m.ReleaseVersionNo == 0 {
				return ErrVideoAdminCommandConflict
			}
			publicID, err := newVideoHTTPID("vmp_")
			if err != nil {
				return err
			}
			command := videoModelDraftRecord{PublicID: publicID, Action: c.Action, CommandKeyHash: keyHash, ActorUserID: c.Caller.UserID, ModelID: m.ID, ModelCode: m.LogicalModelCode, InitialVersion: c.VersionNo, ResultVersion: c.VersionNo + 1, InputSHA256: inputHash, CreatedAt: time.Now().UTC()}
			envelope, err := s.reasons.Seal(modelDraftReasonID(command), []byte(reason))
			if err != nil {
				return err
			}
			command.VideoAdminReasonEnvelope = *envelope
			command.BeforeAuditID, err = writeModelDraftAudit(ctx, tx, command, true)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			if err := tx.Model(&model.AIModelReleaseVersion{}).Where("model_id=? AND status='active'", m.ID).Updates(map[string]any{"status": "retired", "retired_at": now}).Error; err != nil {
				return err
			}
			if c.Action != "unpublish" {
				var history []model.AIModelReleaseVersion
				if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("model_id=?", m.ID).Order("version_no DESC").Limit(1).Find(&history).Error; err != nil {
					return err
				}
				next := m.ReleaseVersionNo
				if len(history) > 0 && history[0].VersionNo > next {
					next = history[0].VersionNo
				}
				if next == math.MaxUint64 {
					return ErrVideoAdminCommandConflict
				}
				next++
				release := model.AIModelReleaseVersion{ModelID: m.ID, VersionNo: next, Status: "active", SnapshotJSON: snapshot, Reason: "video_model_command:" + publicID, CreatedBy: c.Caller.UserID, PublishedAt: now}
				if err := tx.Create(&release).Error; err != nil {
					return err
				}
				m.Status, m.ReleaseVersionNo, m.PublishedAt = "active", next, &now
			} else {
				m.Status = "inactive"
			}
			updates := map[string]any{"status": m.Status, "release_version_no": m.ReleaseVersionNo, "published_at": m.PublishedAt, "updated_by": c.Caller.UserID}
			if c.Action == "rollback" {
				for key, value := range map[string]any{"display_name": m.DisplayName, "provider_name": m.ProviderName, "description": m.Description, "capabilities_json": m.CapabilitiesJSON, "video_contract_json": m.VideoContractJSON, "product_id": m.ProductID, "intro_url": m.IntroURL, "intro_url_health_status": m.IntroURLHealthStatus, "docs_url": m.DocsURL, "docs_url_health_status": m.DocsURLHealthStatus, "quick_start_url": m.QuickStartURL, "quick_start_url_health_status": m.QuickStartURLHealthStatus, "visible_scope": m.VisibleScope, "target_audience_json": m.TargetAudience, "context_window": m.ContextWindow} {
					updates[key] = value
				}
			}
			if err := tx.Model(&model.TokenModel{}).Where("id=?", m.ID).Updates(updates).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", m.ID).Take(&m).Error; err != nil {
				return err
			}
			hash, err = modelDraftSnapshotHash(m)
			if err != nil {
				return err
			}
			result := tx.Model(&videoModelDraftState{}).Where("model_id=? AND version_no=?", m.ID, c.VersionNo).Updates(map[string]any{"version_no": c.VersionNo + 1, "snapshot_sha256": hash, "updated_by": c.Caller.UserID, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrVideoAdminCommandConflict
			}
			reply = &VideoModelPublicationReply{ModelID: m.ID, VersionNo: c.VersionNo + 1, ReleaseVersionNo: m.ReleaseVersionNo, PublicationStatus: m.Status}
			command.ResultJSON, err = json.Marshal(reply)
			if err != nil {
				return err
			}
			command.ResultSHA256, err = modelDraftResultHash(command.ResultJSON)
			if err != nil {
				return err
			}
			command.AfterAuditID, err = writeModelDraftAudit(ctx, tx, command, false)
			if err != nil {
				return err
			}
			if err := tx.Create(&command).Error; err != nil {
				return err
			}
			if err := verifyModelDraftAudits(tx, command); err != nil {
				return err
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage"); err != nil {
				return err
			}
			if price != nil {
				now = time.Now().UTC()
				if !price.CostExpiresAt.After(now) || (price.ExpiresAt != nil && !price.ExpiresAt.After(now)) {
					return ErrVideoPriceExpired
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}
