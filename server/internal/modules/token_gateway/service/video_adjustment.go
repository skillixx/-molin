package service

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	billingmodel "molin/server/internal/modules/billing/model"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// VideoAdjustmentCommand 只描述本地已复核调整，原因使用低敏枚举，不接受Prompt或任意正文。
type VideoAdjustmentCommand struct {
	Direction  string
	Reason     string
	Amount     decimal.Decimal
	MakerID    uint64
	CheckerID  uint64
	SequenceNo uint32
}

type VideoAdjustmentResult struct {
	UsageID             uint64
	WalletTransactionID uint64
	Existing            bool
}

// ApplyAdjustment 与原Hold结算分离，只追加修正，不覆盖已经形成的原财务终态。
func (s *VideoBillingService) ApplyAdjustment(ctx context.Context, taskID string, owner repository.VideoOwner, cmd VideoAdjustmentCommand) (*VideoAdjustmentResult, error) {
	if s == nil || s.db == nil || !validVideoAdjustmentCommand(cmd) {
		return nil, ErrVideoBillingState
	}
	var result *VideoAdjustmentResult
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			// 主体取锁后核验，不能在检查后撤销主体又继续形成资金动作。
			var actors []struct {
				ID     uint64
				Status string
			}
			if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", []uint64{cmd.MakerID, cmd.CheckerID}).Order("id").Find(&actors).Error; err != nil {
				return err
			}
			if len(actors) != 2 || actors[0].Status != "active" || actors[1].Status != "active" {
				return ErrVideoBillingAccess
			}
			// 原业务账先完整闭合；调整不能用来掩盖未完成的Hold或未知Provider结果。
			report, err := reconcileVideoTx(tx, taskID, owner, false, nil, s.now().UTC())
			if err != nil {
				return err
			}
			if !report.Passed {
				return ErrVideoReconciliation
			}
			var old model.VideoUsageItem
			err = tx.Where("request_id=? AND record_kind='adjustment' AND sequence_no=?", task.RequestID, cmd.SequenceNo).First(&old).Error
			if err == nil {
				if old.Amount == nil || !old.Amount.Equal(cmd.Amount) || old.AdjustmentDirection == nil || *old.AdjustmentDirection != cmd.Direction || old.AdjustmentReason == nil || *old.AdjustmentReason != cmd.Reason || old.AdjustmentOperatorID == nil || *old.AdjustmentOperatorID != cmd.MakerID || old.AdjustmentReviewedBy == nil || *old.AdjustmentReviewedBy != cmd.CheckerID || old.AdjustmentWalletTransactionID == nil {
					return ErrVideoBillingConflict
				}
				result = &VideoAdjustmentResult{UsageID: old.ID, WalletTransactionID: *old.AdjustmentWalletTransactionID, Existing: true}
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			walletRepo := billingrepo.NewWalletRepository(tx)
			wallet, err := walletRepo.GetForUpdate(tx, owner.UserID)
			if err != nil {
				return err
			}
			now := s.now().UTC().Truncate(time.Second)
			amount := cmd.Amount
			direction, kind := "in", "refund"
			balance := wallet.BalanceAmount.Add(amount)
			if cmd.Direction == "debit" {
				direction, kind = "out", "consume"
				balance = wallet.BalanceAmount.Sub(amount)
			}
			if balance.IsNegative() {
				return billingservice.ErrInsufficientBalance
			}
			if balance.GreaterThanOrEqual(decimal.RequireFromString("1000000000000")) {
				return ErrVideoBillingState
			}
			rows, err := walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{"balance_amount": balance, "updated_at": now})
			if err != nil {
				return err
			}
			if rows != 1 {
				return billingservice.ErrConcurrentUpdate
			}
			if err := s.injectVideoFault("adjustment_wallet"); err != nil {
				return err
			}
			movement := billingmodel.WalletTransaction{WalletID: wallet.ID, UserID: owner.UserID, Type: kind, Direction: direction, Amount: amount, BalanceAfter: balance, Remark: videoAdjustmentWalletRemark(task.RequestID, cmd.SequenceNo), CreatedAt: now}
			if err := billingrepo.NewTransactionRepository(tx).Create(tx, &movement); err != nil {
				return err
			}
			if err := s.injectVideoFault("adjustment_movement"); err != nil {
				return err
			}
			zero, currency := decimal.Zero, "CNY"
			fact := model.AIUsageItem{RecordKind: model.AIUsageAdjustment, Source: "reconciled", SequenceNo: cmd.SequenceNo, Quantity: zero, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &amount, Currency: &currency, AdjustmentDirection: &cmd.Direction, AdjustmentReason: &cmd.Reason, AdjustmentOperatorID: &cmd.MakerID, AdjustmentReviewedBy: &cmd.CheckerID}
			item, existing, err := repository.NewVideoUsageRepository(tx).AppendAdjustmentTx(tx, taskID, owner, fact, now, &movement.ID)
			if err != nil {
				return err
			}
			if existing {
				return ErrVideoBillingConflict
			}
			if err := s.injectVideoFault("adjustment_usage"); err != nil {
				return err
			}
			payload, err := videoAdjustmentPayload(task, item)
			if err != nil {
				return err
			}
			e := model.AIOutboxEvent{EventID: videoAdjustmentEventID(task.RequestID, cmd.SequenceNo), AggregateType: "video_request", AggregateID: task.RequestID, EventType: "video_adjustment_recorded", PayloadJSON: payload, Status: model.AIOutboxPending, NextRetryAt: now}
			if err := tx.Create(&e).Error; err != nil {
				return err
			}
			if err := s.injectVideoFault("adjustment_outbox"); err != nil {
				return err
			}
			report, err = reconcileVideoTx(tx, taskID, owner, false, nil, s.now().UTC())
			if err != nil {
				return err
			}
			if !report.Passed {
				return ErrVideoReconciliation
			}
			result = &VideoAdjustmentResult{UsageID: item.ID, WalletTransactionID: movement.ID}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validVideoAdjustmentCommand(c VideoAdjustmentCommand) bool {
	return (c.Direction == "credit" || c.Direction == "debit") && (c.Reason == "billing_correction" || c.Reason == "service_credit") && c.Amount.IsPositive() && c.Amount.Equal(c.Amount.Round(8)) && c.Amount.LessThan(decimal.RequireFromString("1000000000000")) && c.MakerID > 0 && c.CheckerID > 0 && c.MakerID != c.CheckerID && c.SequenceNo > 0
}

func videoAdjustmentWalletRemark(requestID string, sequence uint32) string {
	return "video_adjustment:" + requestID + ":" + strconv.FormatUint(uint64(sequence), 10)
}
func videoAdjustmentEventID(requestID string, sequence uint32) string {
	return "vg5_" + videoBillingDigest(videoAdjustmentWalletRemark(requestID, sequence))
}
