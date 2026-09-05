package service

import (
	"encoding/json"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 调账事件只携带低敏金额与序号；方向和复核主体保存在不可变Usage，不进入普通事件载荷。
func videoAdjustmentPayload(task *repository.VideoTaskRecord, item *model.VideoUsageItem) (json.RawMessage, error) {
	return json.Marshal(map[string]interface{}{"request_id": task.RequestID, "status": task.BillingStatus, "amount": item.Amount.StringFixed(8), "currency": "CNY", "operation": *task.Operation, "version": 1, "sequence_no": item.SequenceNo})
}

// validateVideoAdjustmentsTx 独立核对调整与钱包动作，不能把原Hold消费与后续修正混为一笔。
func validateVideoAdjustmentsTx(tx *gorm.DB, task *repository.VideoTaskRecord) error {
	return validateVideoAdjustments(tx, task, true)
}

func validateVideoAdjustmentsSnapshotTx(tx *gorm.DB, task *repository.VideoTaskRecord) error {
	return validateVideoAdjustments(tx, task, false)
}

func validateVideoAdjustments(tx *gorm.DB, task *repository.VideoTaskRecord, lockWallet bool) error {
	var items []model.VideoUsageItem
	if err := tx.Where("request_id=? AND record_kind='adjustment'", task.RequestID).Order("sequence_no").Find(&items).Error; err != nil {
		return err
	}
	var events []model.AIOutboxEvent
	// 先取该请求的全部调整事件，再校验聚合类型；预先过滤错误类型会隐藏额外财务事实。
	if err := tx.Where("aggregate_id=? AND event_type='video_adjustment_recorded'", task.RequestID).Find(&events).Error; err != nil {
		return err
	}
	var actions []billingmodel.WalletTransaction
	prefix := "video_adjustment:" + task.RequestID + ":"
	if err := tx.Where("LEFT(remark,?)=?", len(prefix), prefix).Find(&actions).Error; err != nil {
		return err
	}
	if len(items) != len(events) || len(items) != len(actions) {
		return ErrVideoReconciliation
	}
	if len(items) == 0 {
		return nil
	}
	if task.BillingStatus != model.AIBillingSettled && task.BillingStatus != model.AIBillingReleased {
		return ErrVideoReconciliation
	}
	var q model.AIGatewayQuote
	var link model.AIRequestWalletLink
	if tx.First(&q, task.QuoteID).Error != nil || tx.Where("request_id=?", task.RequestID).First(&link).Error != nil {
		return ErrVideoReconciliation
	}
	if !videoWalletHistoryConsistentMode(tx, link.WalletID, task.UserID, lockWallet) {
		return ErrVideoReconciliation
	}
	seen := map[uint32]bool{}
	for _, item := range items {
		if item.AdjustmentDirection == nil || item.AdjustmentReason == nil || item.AdjustmentOperatorID == nil || item.AdjustmentReviewedBy == nil || item.Amount == nil {
			return ErrVideoReconciliation
		}
		if !validVideoAdjustmentCommand(VideoAdjustmentCommand{Direction: *item.AdjustmentDirection, Reason: *item.AdjustmentReason, Amount: *item.Amount, MakerID: *item.AdjustmentOperatorID, CheckerID: *item.AdjustmentReviewedBy, SequenceNo: item.SequenceNo}) || seen[item.SequenceNo] {
			return ErrVideoReconciliation
		}
		seen[item.SequenceNo] = true
		if item.TaskID != task.ID || item.QuoteID != task.QuoteID || item.UserID != task.UserID || item.ProjectID != task.ProjectID || !equalOptionalUint64(item.APIKeyID, task.APIKeyID) || item.Capability != model.AIVideoCapability || item.LogicalModelCode != task.LogicalModelCode || item.Operation == nil || *item.Operation != *task.Operation || item.PriceVersionID == nil || *item.PriceVersionID != q.PriceVersionID || item.VariantHash != q.RequestVariantHash || !equalVideoFinancialJSON(item.VariantJSON, task.InputJSON) || item.MeterType != VideoMeterSeconds || item.UsageUnit != "seconds" || item.Source != "reconciled" || !item.Quantity.IsZero() || !item.UnitSize.Equal(decimal.NewFromInt(1)) || item.UnitPrice == nil || !item.UnitPrice.IsZero() || item.Currency == nil || *item.Currency != "CNY" || item.EvidenceEventID != nil || item.AdjustmentWalletTransactionID == nil {
			return ErrVideoReconciliation
		}
		var movement *billingmodel.WalletTransaction
		for i := range actions {
			if actions[i].ID == *item.AdjustmentWalletTransactionID {
				movement = &actions[i]
				break
			}
		}
		kind, direction := "refund", "in"
		if *item.AdjustmentDirection == "debit" {
			kind, direction = "consume", "out"
		}
		if movement == nil || movement.WalletID != link.WalletID || movement.UserID != task.UserID || movement.Type != kind || movement.Direction != direction || !movement.Amount.Equal(*item.Amount) || movement.Remark != videoAdjustmentWalletRemark(task.RequestID, item.SequenceNo) || movement.RelatedOrderID != nil || link.ReleaseTransactionID == nil || movement.ID <= *link.ReleaseTransactionID || (link.SettleTransactionID != nil && movement.ID <= *link.SettleTransactionID) {
			return ErrVideoReconciliation
		}
		payload, err := videoAdjustmentPayload(task, &item)
		if err != nil {
			return err
		}
		found := false
		for _, event := range events {
			if event.EventID == videoAdjustmentEventID(task.RequestID, item.SequenceNo) {
				found = event.AggregateType == "video_request" && event.AggregateID == task.RequestID && event.EventType == "video_adjustment_recorded" && validVideoOutboxTransportState(event) && equalVideoFinancialJSON(payload, event.PayloadJSON)
				break
			}
		}
		if !found {
			return ErrVideoReconciliation
		}
	}
	return nil
}
