package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 调用方显式提供原任务的受控内容读取器，接口没有Submit、Query或删除能力。
type VideoAdminArchiveContent interface {
	Name() string
	OpenContent(context.Context, video.ControlledContentRef) (video.StreamContent, error)
}
type VideoAdminArchiveOptions struct {
	Content VideoAdminArchiveContent
	Store   video.VideoArchiveObjectStore
	Probe   *video.VideoMediaProbe
	Safety  *video.VideoSafetyPipeline
	Labeler video.VideoAILabeler
	Locator repository.VideoObjectLocationFactory
}
type VideoAdminArchiveCommand struct {
	Caller                         VideoCaller `json:"-"`
	TaskID, IdempotencyKey, Reason string      `json:"-"`
	VersionNo                      uint64      `json:"-"`
}
type videoAdminArchiveRecord struct {
	VideoAdminOperationFields `gorm:"embedded" json:"-"`
	ArchiveGeneration         uint64
	InitialPhase              string
}

func (videoAdminArchiveRecord) TableName() string { return "ai_video_admin_archive_commands" }
func (s *VideoAdminService) ArchiveReady() bool {
	return s.WritesReady() && s.archive != nil && s.archive.Content != nil && s.archive.Content.Name() == "fake-native-async" && s.archive.Store != nil && s.archive.Probe != nil && s.archive.Safety != nil && s.archive.Labeler != nil && s.archive.Locator != nil
}
func archiveReasonID(c videoAdminArchiveRecord, id string) VideoAdminReasonIdentity {
	return VideoAdminReasonIdentity{ActorID: c.ActorUserID, ArchiveTaskID: id, CommandKeyHash: c.CommandKeyHash, VersionNo: c.InitialVersion}
}

// 起点只从当前原任务和资产推导，客户端不能指定技术phase或任意内容来源。
func archiveRecoveryPhaseTx(tx *gorm.DB, r *repository.VideoTaskRecord) (string, error) {
	if r.ProviderCode == nil || *r.ProviderCode != "fake-native-async" || r.ProviderTaskID == nil || !videoBillingPublicID.MatchString(*r.ProviderTaskID) || r.AttemptCount != 1 {
		return "", ErrVideoAdminCommandConflict
	}
	switch r.Status {
	case "fetching", "storing", "moderating", "labeling", "pending_reconcile":
	default:
		return "", ErrVideoAdminCommandConflict
	}
	var quote model.AIGatewayQuote
	if err := tx.Where("id=?", r.QuoteID).Take(&quote).Error; err != nil {
		return "", err
	}
	if _, err := loadVideoConfirmedCostTx(tx, r, &quote); err != nil {
		return "", err
	}
	for _, table := range []string{"ai_video_media_deletions", "ai_video_asset_deletions"} {
		var count int64
		if err := tx.Table(table).Where("task_id=?", r.ID).Count(&count).Error; err != nil {
			return "", err
		}
		if count != 0 {
			return "", ErrVideoMediaProtected
		}
	}
	var assets []model.AIImageAsset
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_id=?", r.ID).Order("id").Find(&assets).Error; err != nil {
		return "", err
	}
	if len(assets) != 0 && len(assets) != 1 && len(assets) != 6 {
		return "", ErrVideoReconciliation
	}
	var root *model.AIImageAsset
	for i := range assets {
		a := &assets[i]
		if a.RequestID != r.RequestID || a.UserID != r.UserID || a.ProjectID != r.ProjectID || a.Modality != "video" || a.LifecycleState != "temporary" || a.LegalHold || a.DisputeStatus == model.AIImageDisputeOpen || a.DeletedAt != nil || a.MediaDeletedAt != nil || !a.ExpiresAt.After(time.Now().UTC()) || a.ModerationStatus == "rejected" || a.ModerationStatus == "error" || a.ExplicitLabelStatus == "failed" || a.ImplicitLabelStatus == "failed" {
			return "", ErrVideoMediaProtected
		}
		if a.AssetRole == "content" {
			if root != nil || a.ParentAssetID != nil {
				return "", ErrVideoReconciliation
			}
			root = a
		}
	}
	phase := "fetching"
	if len(assets) > 0 {
		if root == nil || mapVideoG4Asset(root) == nil {
			return "", ErrVideoReconciliation
		}
		phase = "moderating"
		if root.ModerationStatus == "passed" {
			phase = "labeling"
		}
		for _, a := range assets {
			if a.ID != root.ID && (a.ParentAssetID == nil || *a.ParentAssetID != root.ID) {
				return "", ErrVideoReconciliation
			}
		}
	}
	if r.Status != "pending_reconcile" {
		if (r.Status == "fetching" || r.Status == "storing") && len(assets) == 0 {
			return r.Status, nil
		}
		if r.Status != phase {
			return "", ErrVideoReconciliation
		}
	}
	return phase, nil
}

// 管理命令和前审计/原Task围栏原子提交；媒体IO永远不在数据库重试闭包内。
func (s *VideoAdminService) RetryArchive(ctx context.Context, c VideoAdminArchiveCommand) (*VideoAdminPollReply, error) {
	if !s.ArchiveReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if !utf8.ValidString(reason) || reason == "" || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || c.VersionNo == 0 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var command videoAdminArchiveRecord
	var task *repository.VideoTaskRecord
	var proof *repository.VideoArchiveFenceProof
	var owner repository.VideoOwner
	claimed := false
	err := retryVideoBillingTransaction(ctx, func() error {
		claimed = false
		proof = nil
		command = videoAdminArchiveRecord{}
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			if !videoBillingPublicID.MatchString(c.TaskID) {
				return repository.ErrVideoTaskNotFound
			}
			var identity struct {
				UserID, ProjectID uint64
				APIKeyID          *uint64
			}
			if err := tx.Table("ai_gateway_tasks t").Select("t.user_id,t.project_id,t.api_key_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.logical_model_code=t.logical_model_code AND r.operation=t.operation").Where("t.public_id=? AND t.capability='video.generate' AND r.capability='video.generate' AND r.modality='video'", c.TaskID).Take(&identity).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
			}
			owner = repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
			repo := repository.NewVideoTaskRepository(tx)
			var err error
			task, err = repo.LockForOwnerTx(tx, c.TaskID, owner)
			if err != nil {
				return err
			}
			hash := videoBillingDigest(fmt.Sprintf("video-admin-archive:%d:%s", c.Caller.UserID, c.IdempotencyKey))
			err = tx.Where("actor_user_id=? AND command_key_hash=?", c.Caller.UserID, hash).Take(&command).Error
			if err == nil {
				if command.TaskID != task.ID || command.RequestID != task.RequestID || command.InitialVersion != c.VersionNo || command.BindingSHA256 != pollBinding(task) {
					return ErrVideoAdminCommandConflict
				}
				if _, err := s.reasons.Open(archiveReasonID(command, c.TaskID), command.VideoAdminReasonEnvelope); err != nil {
					return ErrVideoAccessUnavailable
				}
				if command.ReasonHMAC != s.reasons.digest("reason", reason) {
					return ErrVideoAdminCommandConflict
				}
				if err := verifyAdminOperationAudits(tx, command.VideoAdminOperationFields, c.TaskID, "archive"); err != nil {
					return err
				}
				if command.Status == "running" && !command.DeadlineAt.After(time.Now().UTC()) {
					if err := closeArchiveCommandTx(ctx, tx, &command, task, "unknown", "needs_reconcile"); err != nil {
						return err
					}
				}
				return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage")
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if task.VersionNo != c.VersionNo {
				return ErrVideoAdminCommandConflict
			}
			var old []videoAdminArchiveRecord
			if err := tx.Where("task_id=? AND status='running'", task.ID).Find(&old).Error; err != nil {
				return err
			}
			for i := range old {
				if old[i].DeadlineAt.After(time.Now().UTC()) {
					return ErrVideoAdminCommandConflict
				}
				if err := closeArchiveCommandTx(ctx, tx, &old[i], task, "unknown", "needs_reconcile"); err != nil {
					return err
				}
			}
			phase, err := archiveRecoveryPhaseTx(tx, task)
			if err != nil {
				return err
			}
			var entropy [24]byte
			if _, err := rand.Read(entropy[:]); err != nil {
				return err
			}
			command = videoAdminArchiveRecord{VideoAdminOperationFields: videoAdminPollRecord{PublicID: "varchive_" + hex.EncodeToString(entropy[:]), CommandKeyHash: hash, ActorUserID: c.Caller.UserID, TaskID: task.ID, RequestID: task.RequestID, UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID, InitialVersion: task.VersionNo, BindingSHA256: pollBinding(task), Status: "running", ResultCode: "requested", VersionNo: 1, CreatedAt: time.Now().UTC()}, InitialPhase: phase}
			sealed, err := s.reasons.Seal(archiveReasonID(command, c.TaskID), []byte(reason))
			if err != nil {
				return ErrVideoAccessUnavailable
			}
			command.VideoAdminReasonEnvelope = *sealed
			command.BeforeAuditID, err = writeAdminOperationAudit(ctx, tx, command.VideoAdminOperationFields, c.TaskID, true, "archive")
			if err != nil {
				return err
			}
			proof, task, err = repo.ClaimArchiveFence(ctx, repository.VideoArchiveFenceClaim{TaskPublicID: c.TaskID, Owner: owner, ExpectedVersion: c.VersionNo, InitialPhase: phase, Now: time.Now().UTC()})
			if err != nil {
				return err
			}
			command.ArchiveGeneration = proof.Generation()
			command.DeadlineAt = *task.ArchiveLeaseUntil
			if err := tx.Create(&command).Error; err != nil {
				return err
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			if err := repository.CheckVideoArchiveFence(task, proof, time.Now().UTC()); err != nil {
				return err
			}
			claimed = true
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		if repository.IsDuplicateKeyForHandler(err) {
			return nil, ErrVideoAdminCommandConflict
		}
		return nil, err
	}
	if !claimed {
		return archiveReply(command, task, true), nil
	}
	o := videoArchiveExecutionOptions{content: s.archive.Content, store: s.archive.Store, probe: s.archive.Probe, safety: s.archive.Safety, labeler: s.archive.Labeler, locator: s.archive.Locator}
	o.completeTx = func(ctx context.Context, tx *gorm.DB, r *repository.VideoTaskRecord) error {
		var current videoAdminArchiveRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", command.ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Status != "running" || current.ArchiveGeneration != proof.Generation() || r.ArchiveGeneration == nil || *r.ArchiveGeneration != current.ArchiveGeneration {
			return ErrVideoAdminCommandConflict
		}
		return closeArchiveCommandTx(ctx, tx, &current, r, "completed", "archived")
	}
	o.failureTx = func(ctx context.Context, tx *gorm.DB, r *repository.VideoTaskRecord) error {
		var current videoAdminArchiveRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", command.ID).Take(&current).Error; err != nil {
			return err
		}
		if current.Status != "running" || current.ArchiveGeneration != proof.Generation() || r.ArchiveGeneration == nil || *r.ArchiveGeneration != current.ArchiveGeneration {
			return ErrVideoAdminCommandConflict
		}
		return closeArchiveCommandTx(ctx, tx, &current, r, "unknown", "needs_reconcile")
	}
	runErr := runVideoArchiveRecovery(ctx, s, c.Caller, c.TaskID, owner, proof, o)
	if runErr != nil {
		if err := s.closeUnknownArchive(ctx, command, owner, proof); err != nil {
			return nil, errors.Join(ErrVideoAccessUnavailable, runErr, err)
		}
	}
	// 返回前再验证当前主体；执行成功不赋予撤权后读取旧幂等结果的权利。
	err = s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		task, err = repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, c.TaskID, owner)
		if err != nil {
			return err
		}
		if err := tx.Where("id=?", command.ID).Take(&command).Error; err != nil {
			return err
		}
		if err := verifyAdminOperationAudits(tx, command.VideoAdminOperationFields, c.TaskID, "archive"); err != nil {
			return err
		}
		return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:task_manage")
	})
	if err != nil {
		return nil, err
	}
	return archiveReply(command, task, false), nil
}

func archiveReply(c videoAdminArchiveRecord, t *repository.VideoTaskRecord, replay bool) *VideoAdminPollReply {
	return &VideoAdminPollReply{CommandID: c.PublicID, TaskID: t.PublicID, RequestID: t.RequestID, Status: c.Status, ExecutionStatus: t.Status, VersionNo: t.VersionNo, Idempotent: replay}
}

func closeArchiveCommandTx(ctx context.Context, tx *gorm.DB, c *videoAdminArchiveRecord, t *repository.VideoTaskRecord, status, result string) error {
	if c.Status != "running" {
		return nil
	}
	if c.TaskID != t.ID || c.RequestID != t.RequestID || c.BindingSHA256 != pollBinding(t) {
		return ErrVideoAccessUnavailable
	}
	// 成功事务内锁定并验证原前审计，不能等执行状态已提交才发现审计被损坏。
	if err := verifyAdminOperationAudits(tx, c.VideoAdminOperationFields, t.PublicID, "archive"); err != nil {
		return err
	}
	c.ResultCode = result
	after, err := writeAdminOperationAudit(ctx, tx, c.VideoAdminOperationFields, t.PublicID, false, "archive")
	if err != nil {
		return err
	}
	projected := c.VideoAdminOperationFields
	projected.Status, projected.VersionNo, projected.AfterAuditID = status, 2, &after
	if err := verifyAdminOperationAudits(tx, projected, t.PublicID, "archive"); err != nil {
		return err
	}
	changed := tx.Model(&videoAdminArchiveRecord{}).Where("id=? AND status='running' AND version_no=1", c.ID).Updates(map[string]any{"status": status, "result_code": result, "version_no": 2, "after_audit_id": after})
	if changed.Error != nil {
		return changed.Error
	}
	if changed.RowsAffected != 1 {
		return ErrVideoAdminCommandConflict
	}
	c.Status, c.VersionNo, c.AfterAuditID = status, 2, &after
	return nil
}

// 善后只保存未知事实和原G5核对任务，不重新抓取、结算、退款或假装安全审核失败。
func (s *VideoAdminService) closeUnknownArchive(parent context.Context, c videoAdminArchiveRecord, owner repository.VideoOwner, proof *repository.VideoArchiveFenceProof) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	return retryVideoBillingTransaction(ctx, func() error {
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var identity struct{ PublicID string }
			if err := tx.Table("ai_gateway_tasks").Select("public_id").Where("id=?", c.TaskID).Take(&identity).Error; err != nil {
				return err
			}
			repo := repository.NewVideoTaskRepository(tx)
			r, err := repo.LockForOwnerTx(tx, identity.PublicID, owner)
			if err != nil {
				return err
			}
			var current videoAdminArchiveRecord
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", c.ID).Take(&current).Error; err != nil {
				return err
			}
			if current.Status != "running" {
				return nil
			}
			if proof != nil && repository.CheckVideoArchiveFence(r, proof, time.Now().UTC()) == nil {
				inner := context.WithValue(ctx, videoBillingOuterTransactionKey{}, true)
				if !videoG4TerminalStatus(r.Status) && r.Status != "pending_reconcile" {
					r, err = repo.TransitionExecution(inner, repository.VideoStateTransition{TaskPublicID: r.PublicID, Owner: owner, ExpectedVersion: r.VersionNo, ToStatus: "pending_reconcile", Progress: r.Progress, EventID: "vg6_archive_unknown_" + videoBillingDigest(c.PublicID), Source: "worker", Now: time.Now().UTC(), ArchiveFence: proof})
					if err != nil {
						return err
					}
				}
				if r.Status == "pending_reconcile" {
					if _, err := s.app.billing.reconcileVideoExecutionTx(inner, tx, r, owner); err != nil {
						return err
					}
				}
				r, err = repo.ReleaseArchiveFence(inner, r.PublicID, owner, r.VersionNo, proof, time.Now().UTC())
				if err != nil {
					return err
				}
			}
			return closeArchiveCommandTx(ctx, tx, &current, r, "unknown", "needs_reconcile")
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
}
