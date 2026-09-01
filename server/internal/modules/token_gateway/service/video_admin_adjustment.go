package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var videoAdjustmentAmountText = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(\.[0-9]{1,8})?$`)

type VideoAdminAdjustmentCommand struct {
	Caller                                                                                  VideoCaller `json:"-"`
	Action, TaskID, ApprovalID, IdempotencyKey, Reason, Amount, Direction, AdjustmentReason string      `json:"-"`
	VersionNo                                                                               uint64      `json:"-"`
}
type VideoAdminAdjustmentReply struct {
	ApprovalID          string    `json:"approval_id"`
	TaskID              string    `json:"task_id"`
	RequestID           string    `json:"request_id"`
	Status              string    `json:"status"`
	Amount              string    `json:"amount"`
	Direction           string    `json:"direction"`
	AdjustmentReason    string    `json:"adjustment_reason"`
	SequenceNo          uint32    `json:"sequence_no"`
	VersionNo           uint64    `json:"version_no"`
	TaskVersionNo       uint64    `json:"task_version_no"`
	ExpiresAt           time.Time `json:"expires_at"`
	Idempotent          bool      `json:"idempotent"`
	UsageID             *uint64   `json:"usage_id"`
	WalletTransactionID *uint64   `json:"wallet_transaction_id"`
}
type videoAdjustmentApproval struct {
	ID                                                             uint64 `gorm:"primaryKey"`
	PublicID, CommandKeyHash                                       string
	MakerUserID, TaskID, UserID, ProjectID, TaskVersion, VersionNo uint64
	RequestID                                                      string
	APIKeyID                                                       *uint64
	Amount                                                         decimal.Decimal
	Direction, ReasonCode                                          string
	SequenceNo                                                     uint32
	PlanSHA256                                                     string `gorm:"column:plan_sha256"`
	VideoAdminReasonEnvelope                                       `gorm:"embedded" json:"-"`
	BeforeAuditID, AfterAuditID                                    uint64
	CreatedAt, ExpiresAt                                           time.Time
}

func (videoAdjustmentApproval) TableName() string { return "ai_video_adjustment_approvals" }

type videoAdjustmentApprovalExecution struct {
	ID                                         uint64 `gorm:"primaryKey"`
	ApprovalID, CheckerUserID, VersionNo       uint64
	CommandKeyHash, Status                     string
	VideoAdminReasonEnvelope                   `gorm:"embedded" json:"-"`
	BeforeAuditID                              uint64
	AfterAuditID, UsageID, WalletTransactionID *uint64
	CreatedAt                                  time.Time
}

func (videoAdjustmentApprovalExecution) TableName() string {
	return "ai_video_adjustment_approval_executions"
}
func (s *VideoAdminService) AdjustmentsReady() bool { return s.WritesReady() && s.adjustmentsEnabled }

func adjustmentApprovalReasonID(actor uint64, task, hash string, version uint64) VideoAdminReasonIdentity {
	return VideoAdminReasonIdentity{ActorID: actor, AdjustmentTaskID: task, CommandKeyHash: hash, VersionNo: version}
}
func adjustmentPlanSHA(a videoAdjustmentApproval) string {
	return videoBillingDigest(fmt.Sprintf("%d:%d:%s:%d:%s:%s:%s:%d:%s", a.MakerUserID, a.TaskID, a.RequestID, a.TaskVersion, a.Direction, a.Amount.StringFixed(8), a.ReasonCode, a.SequenceNo, a.ReasonHMAC))
}
func adjustmentAuditRecord(a videoAdjustmentApproval, actor uint64, hash string, e VideoAdminReasonEnvelope, result string) videoAdminPollRecord {
	return videoAdminPollRecord{PublicID: a.PublicID, ActorUserID: actor, CommandKeyHash: hash, TaskID: a.TaskID, RequestID: a.RequestID, InitialVersion: a.TaskVersion, VideoAdminReasonEnvelope: e, Status: "completed", ResultCode: result}
}

func verifyAdjustmentApproval(tx *gorm.DB, s *VideoAdminService, a videoAdjustmentApproval, task string) error {
	if a.VersionNo != 1 || a.PlanSHA256 != adjustmentPlanSHA(a) {
		return ErrVideoAccessUnavailable
	}
	if _, err := s.reasons.Open(adjustmentApprovalReasonID(a.MakerUserID, task, a.CommandKeyHash, a.TaskVersion), a.VideoAdminReasonEnvelope); err != nil {
		return ErrVideoAccessUnavailable
	}
	base := adjustmentAuditRecord(a, a.MakerUserID, a.CommandKeyHash, a.VideoAdminReasonEnvelope, "planned")
	base.BeforeAuditID = a.BeforeAuditID
	base.AfterAuditID = &a.AfterAuditID
	return verifyAdminOperationAudits(tx, base, task, "adjustment_request")
}

// 两个认证步骤组成真实审批；只复用原G5资金协调器，不在HTTP层计算或覆盖原结算。
func (s *VideoAdminService) ManageAdjustment(ctx context.Context, c VideoAdminAdjustmentCommand) (*VideoAdminAdjustmentReply, error) {
	if !s.AdjustmentsReady() {
		return nil, ErrVideoAccessUnavailable
	}
	reason := strings.TrimSpace(c.Reason)
	if reason == "" || !utf8.ValidString(reason) || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) || c.VersionNo == 0 || (c.Action != "request" && c.Action != "approve") {
		return nil, ErrVideoAdminCommandInvalid
	}
	amount := decimal.Zero
	if c.Action == "request" {
		var err error
		amount, err = decimal.NewFromString(c.Amount)
		if !videoBillingPublicID.MatchString(c.TaskID) || !videoAdjustmentAmountText.MatchString(c.Amount) || err != nil || !amount.IsPositive() || (c.Direction != "credit" && c.Direction != "debit") || (c.AdjustmentReason != "billing_correction" && c.AdjustmentReason != "service_credit") {
			return nil, ErrVideoAdminCommandInvalid
		}
	} else if !videoBillingPublicID.MatchString(c.ApprovalID) {
		return nil, ErrVideoAdminCommandInvalid
	}
	if c.Action == "approve" && c.VersionNo != 1 {
		return nil, ErrVideoAdminCommandConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 入口先认证但不持该事务的用户锁等待Task，避免与G5的Task→双主体UPDATE锁形成升级死锁。
	if err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:reconcile_manage") }); err != nil {
		return nil, err
	}
	var reply *VideoAdminAdjustmentReply
	err := retryVideoBillingTransaction(ctx, func() error {
		reply = nil
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var a videoAdjustmentApproval
			taskID := c.TaskID
			if c.Action == "approve" {
				if err := tx.Where("public_id=?", c.ApprovalID).Take(&a).Error; err != nil {
					return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
				}
				var target struct{ PublicID string }
				if err := tx.Table("ai_gateway_tasks").Select("public_id").Where("id=?", a.TaskID).Take(&target).Error; err != nil {
					return err
				}
				taskID = target.PublicID
			}
			var identity struct {
				UserID, ProjectID uint64
				APIKeyID          *uint64
			}
			if err := tx.Table("ai_gateway_tasks t").Select("t.user_id,t.project_id,t.api_key_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id").Where("t.public_id=? AND t.capability='video.generate' AND r.capability='video.generate' AND r.modality='video'", taskID).Take(&identity).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
			}
			owner := repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			actors := []uint64{c.Caller.UserID}
			if c.Action == "approve" {
				actors = append(actors, a.MakerUserID)
			}
			var lockedActors []struct{ ID uint64 }
			if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id IN ?", actors).Order("id").Find(&lockedActors).Error; err != nil {
				return err
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:reconcile_manage"); err != nil {
				return err
			}
			hash := videoBillingDigest(fmt.Sprintf("video-admin-adjustment:%s:%d:%s", c.Action, c.Caller.UserID, c.IdempotencyKey))
			replayed := false
			if c.Action == "request" {
				err = tx.Where("maker_user_id=? AND command_key_hash=?", c.Caller.UserID, hash).Take(&a).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				replayed = err == nil
				if !replayed {
					if task.VersionNo != c.VersionNo {
						return ErrVideoAdminCommandConflict
					}
					report, err := reconcileVideoTx(tx, taskID, owner, false, nil, time.Now().UTC())
					if err != nil {
						return err
					}
					if !report.Passed {
						return ErrVideoReconciliation
					}
					var fromUsage, fromApprovals uint64
					if err := tx.Table("ai_usage_items").Select("COALESCE(MAX(sequence_no),0)").Where("request_id=? AND record_kind='adjustment'", task.RequestID).Scan(&fromUsage).Error; err != nil {
						return err
					}
					if err := tx.Table("ai_video_adjustment_approvals").Select("COALESCE(MAX(sequence_no),0)").Where("task_id=?", task.ID).Scan(&fromApprovals).Error; err != nil {
						return err
					}
					if fromApprovals > fromUsage {
						fromUsage = fromApprovals
					}
					if fromUsage >= math.MaxUint32 {
						return ErrVideoAdminCommandConflict
					}
					var entropy [24]byte
					if _, err := rand.Read(entropy[:]); err != nil {
						return err
					}
					sealed, err := s.reasons.Seal(adjustmentApprovalReasonID(c.Caller.UserID, taskID, hash, c.VersionNo), []byte(reason))
					if err != nil {
						return ErrVideoAccessUnavailable
					}
					now := time.Now().UTC().Truncate(time.Microsecond)
					a = videoAdjustmentApproval{PublicID: "vadj_" + hex.EncodeToString(entropy[:]), CommandKeyHash: hash, MakerUserID: c.Caller.UserID, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID, RequestID: task.RequestID, TaskVersion: c.VersionNo, VersionNo: 1, Amount: amount, Direction: c.Direction, ReasonCode: c.AdjustmentReason, SequenceNo: uint32(fromUsage + 1), VideoAdminReasonEnvelope: *sealed, CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
					a.PlanSHA256 = adjustmentPlanSHA(a)
					base := adjustmentAuditRecord(a, c.Caller.UserID, hash, *sealed, "planned")
					a.BeforeAuditID, err = writeAdminOperationAudit(ctx, tx, base, taskID, true, "adjustment_request")
					if err != nil {
						return err
					}
					a.AfterAuditID, err = writeAdminOperationAudit(ctx, tx, base, taskID, false, "adjustment_request")
					if err != nil {
						return err
					}
					if err := tx.Create(&a).Error; err != nil {
						return err
					}
				}
				if a.TaskID != task.ID || a.TaskVersion != c.VersionNo || !a.Amount.Equal(amount) || a.Direction != c.Direction || a.ReasonCode != c.AdjustmentReason {
					return ErrVideoAdminCommandConflict
				}
			} else if a.MakerUserID == c.Caller.UserID {
				return ErrVideoAdminForbidden
			}
			if err := verifyAdjustmentApproval(tx, s, a, taskID); err != nil {
				return err
			}
			if c.Action == "request" && a.ReasonHMAC != s.reasons.digest("reason", reason) {
				return ErrVideoAdminCommandConflict
			}
			if a.TaskID != task.ID || a.RequestID != task.RequestID || a.UserID != owner.UserID || a.ProjectID != owner.ProjectID || !equalOptionalUint64(a.APIKeyID, owner.APIKeyID) {
				return ErrVideoAccessUnavailable
			}
			var e videoAdjustmentApprovalExecution
			execErr := tx.Where("approval_id=?", a.ID).Take(&e).Error
			if execErr != nil && !errors.Is(execErr, gorm.ErrRecordNotFound) {
				return execErr
			}
			status := "pending"
			version := uint64(1)
			var usage, movement *uint64
			var makerUntil *videoReleaseMakerDeadline
			if execErr == nil {
				if c.Action == "approve" && (e.CheckerUserID != c.Caller.UserID || e.CommandKeyHash != hash) {
					return ErrVideoAdminCommandConflict
				}
				if err := s.verifyAdjustmentExecution(tx, a, e, taskID); err != nil {
					return err
				}
				if c.Action == "approve" && e.ReasonHMAC != s.reasons.digest("reason", reason) {
					return ErrVideoAdminCommandConflict
				}
				status, version, usage, movement, replayed = "executed", 2, e.UsageID, e.WalletTransactionID, true
			} else if c.Action == "approve" {
				if !a.ExpiresAt.After(time.Now().UTC()) || task.VersionNo != a.TaskVersion {
					return ErrVideoAdminCommandConflict
				}
				makerUntil, err = s.authorizeApprovalMakerTx(ctx, tx, a.MakerUserID, "ai_gateway:reconcile_manage")
				if err != nil {
					return err
				}
				sealed, err := s.reasons.Seal(adjustmentApprovalReasonID(c.Caller.UserID, taskID, hash, 1), []byte(reason))
				if err != nil {
					return ErrVideoAccessUnavailable
				}
				e = videoAdjustmentApprovalExecution{ApprovalID: a.ID, CheckerUserID: c.Caller.UserID, CommandKeyHash: hash, Status: "prepared", VersionNo: 1, VideoAdminReasonEnvelope: *sealed, CreatedAt: time.Now().UTC()}
				base := adjustmentAuditRecord(a, c.Caller.UserID, hash, *sealed, "executed")
				e.BeforeAuditID, err = writeAdminOperationAudit(ctx, tx, base, taskID, true, "adjustment_approve")
				if err != nil {
					return err
				}
				if err := tx.Create(&e).Error; err != nil {
					return err
				}
				inner := context.WithValue(ctx, videoBillingOuterTransactionKey{}, true)
				billing := *s.app.billing
				billing.db = tx.WithContext(inner)
				adjusted, err := billing.ApplyAdjustment(inner, taskID, owner, VideoAdjustmentCommand{Direction: a.Direction, Reason: a.ReasonCode, Amount: a.Amount, MakerID: a.MakerUserID, CheckerID: c.Caller.UserID, SequenceNo: a.SequenceNo})
				if err != nil {
					return err
				}
				if adjusted.Existing {
					return ErrVideoAdminCommandConflict
				}
				after, err := writeAdminOperationAudit(ctx, tx, base, taskID, false, "adjustment_approve")
				if err != nil {
					return err
				}
				changed := tx.Model(&videoAdjustmentApprovalExecution{}).Where("id=? AND status='prepared' AND version_no=1", e.ID).Updates(map[string]any{"status": "executed", "version_no": 2, "after_audit_id": after, "usage_id": adjusted.UsageID, "wallet_transaction_id": adjusted.WalletTransactionID})
				if changed.Error != nil {
					return changed.Error
				}
				if changed.RowsAffected != 1 {
					return ErrVideoAdminCommandConflict
				}
				e.Status, e.VersionNo, e.AfterAuditID, e.UsageID, e.WalletTransactionID = "executed", 2, &after, &adjusted.UsageID, &adjusted.WalletTransactionID
				if err := s.verifyAdjustmentExecution(tx, a, e, taskID); err != nil {
					return err
				}
				status, version, usage, movement = "executed", 2, e.UsageID, e.WalletTransactionID
				makerUntil, err = s.authorizeApprovalMakerTx(ctx, tx, a.MakerUserID, "ai_gateway:reconcile_manage")
				if err != nil {
					return err
				}
			} else if !a.ExpiresAt.After(time.Now().UTC()) {
				status = "expired"
			}
			if err := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:reconcile_manage"); err != nil {
				return err
			}
			if makerUntil != nil {
				now := time.Now().UTC()
				if !a.ExpiresAt.After(now) {
					return ErrVideoAdminCommandConflict
				}
				if makerUntil.permissionUntil != nil && !makerUntil.permissionUntil.After(now) {
					return ErrVideoAdminForbidden
				}
				if makerUntil.mfaUntil != nil && !makerUntil.mfaUntil.After(now) {
					return ErrVideoAdminMFA
				}
			}
			reply = &VideoAdminAdjustmentReply{ApprovalID: a.PublicID, TaskID: taskID, RequestID: task.RequestID, Status: status, Amount: a.Amount.StringFixed(8), Direction: a.Direction, AdjustmentReason: a.ReasonCode, SequenceNo: a.SequenceNo, VersionNo: version, TaskVersionNo: a.TaskVersion, ExpiresAt: a.ExpiresAt, Idempotent: replayed, UsageID: usage, WalletTransactionID: movement}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		if repository.IsDuplicateKeyForHandler(err) {
			return nil, ErrVideoAdminCommandConflict
		}
		return nil, err
	}
	return reply, nil
}

func (s *VideoAdminService) verifyAdjustmentExecution(tx *gorm.DB, a videoAdjustmentApproval, e videoAdjustmentApprovalExecution, task string) error {
	if e.ApprovalID != a.ID || e.Status != "executed" || e.VersionNo != 2 || e.CheckerUserID == a.MakerUserID || e.UsageID == nil || e.WalletTransactionID == nil || e.AfterAuditID == nil {
		return ErrVideoAccessUnavailable
	}
	if _, err := s.reasons.Open(adjustmentApprovalReasonID(e.CheckerUserID, task, e.CommandKeyHash, 1), e.VideoAdminReasonEnvelope); err != nil {
		return ErrVideoAccessUnavailable
	}
	base := adjustmentAuditRecord(a, e.CheckerUserID, e.CommandKeyHash, e.VideoAdminReasonEnvelope, "executed")
	base.BeforeAuditID = e.BeforeAuditID
	base.AfterAuditID = e.AfterAuditID
	if err := verifyAdminOperationAudits(tx, base, task, "adjustment_approve"); err != nil {
		return err
	}
	var item model.VideoUsageItem
	if err := tx.Where("id=? AND task_id=? AND request_id=? AND record_kind='adjustment'", *e.UsageID, a.TaskID, a.RequestID).Take(&item).Error; err != nil {
		return errors.Join(ErrVideoAccessUnavailable, err)
	}
	if item.SequenceNo != a.SequenceNo || item.Amount == nil || !item.Amount.Equal(a.Amount) || item.AdjustmentDirection == nil || *item.AdjustmentDirection != a.Direction || item.AdjustmentReason == nil || *item.AdjustmentReason != a.ReasonCode || item.AdjustmentOperatorID == nil || *item.AdjustmentOperatorID != a.MakerUserID || item.AdjustmentReviewedBy == nil || *item.AdjustmentReviewedBy != e.CheckerUserID || item.AdjustmentWalletTransactionID == nil || *item.AdjustmentWalletTransactionID != *e.WalletTransactionID {
		return ErrVideoAccessUnavailable
	}
	report, err := reconcileVideoTx(tx, task, repository.VideoOwner{UserID: a.UserID, ProjectID: a.ProjectID, APIKeyID: a.APIKeyID}, false, nil, time.Now().UTC())
	if err != nil {
		return err
	}
	if !report.Passed {
		return ErrVideoReconciliation
	}
	return nil
}
