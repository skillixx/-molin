package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var ErrVideoReconciliation = errors.New("视频请求对账存在差异，禁止交付")

// VideoReconciliationReport 只暴露检查项及低敏差异代码，不返回Prompt、钱包明细或Provider正文。
type VideoReconciliationReport struct {
	RequestID   string          `json:"request_id"`
	Stage       string          `json:"stage"`
	Passed      bool            `json:"passed"`
	Checks      map[string]bool `json:"checks"`
	Differences []string        `json:"differences"`
}

type VideoReconciliationService struct {
	db  *gorm.DB
	now func() time.Time
}

func NewVideoReconciliationService(db *gorm.DB) *VideoReconciliationService {
	return &VideoReconciliationService{db: db, now: time.Now}
}

// Reconcile 以一致事务逐项核对真实持久化事实；没有正式HTTP路由或外部调用。
func (s *VideoReconciliationService) Reconcile(ctx context.Context, taskID string, owner repository.VideoOwner) (*VideoReconciliationReport, error) {
	var report *VideoReconciliationReport
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		report, err = reconcileVideoTx(tx, taskID, owner, false, nil, s.now().UTC())
		if err == nil && report.Passed {
			// 等待钱包锁和扫描流水可能跨过媒体期限，返回前必须用新时钟检查已锁定资产。
			var invalid int64
			if e := tx.Model(&model.AIImageAsset{}).Where("request_id=? AND lifecycle_state='available' AND (expires_at<=? OR media_deleted_at IS NOT NULL OR deleted_at IS NOT NULL OR legal_hold=1 OR dispute_status='open')", report.RequestID, s.now().UTC()).Count(&invalid).Error; e != nil {
				return e
			}
			if invalid != 0 {
				report.Passed = false
				report.Checks["output_asset"] = false
				report.Differences = append(report.Differences, "output_asset_expired_during_read")
			}
		}
		return err
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return report, err
}

func reconcileVideoTx(tx *gorm.DB, taskID string, owner repository.VideoOwner, preDelivery bool, lease *repository.VideoCompensationLease, now time.Time) (*VideoReconciliationReport, error) {
	stage := "final"
	if preDelivery {
		stage = "publication_precheck"
	}
	report := &VideoReconciliationReport{Stage: stage, Passed: true, Checks: map[string]bool{}, Differences: []string{}}
	for _, name := range []string{"request", "quote", "hold", "freeze", "consume", "unfreeze", "usage_fact", "sale_line", "cost_line", "adjustment", "task", "task_input", "output_asset", "task_event", "provider_callback_event", "compensation", "outbox"} {
		report.Checks[name] = false
	}
	check := func(name string, ok bool) {
		report.Checks[name] = ok
		if !ok {
			report.Passed = false
			report.Differences = append(report.Differences, name)
		}
	}
	task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, owner)
	if err != nil {
		return nil, err
	}
	report.RequestID = task.RequestID
	r, q, link, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
	if err != nil {
		check("request", false)
		return report, nil
	}
	settled := r.BillingStatus == model.AIBillingSettled && task.Status == model.AIImageTaskSucceeded
	released := r.BillingStatus == model.AIBillingReleased && (task.Status == model.AIImageTaskCancelled || task.Status == model.AIImageTaskFailed)
	submittedRelease := released && task.ProviderTaskID != nil
	check("request", (settled || released) && r.SettledAmount != nil && r.ExecutionStatus == task.Status && equalVideoFinancialJSON(r.PriceSnapshotJSON, q.PriceSnapshotJSON))
	snapshot, err := DecodeVideoPriceSnapshot(q.PriceSnapshotJSON)
	if err != nil {
		check("quote", false)
		return report, nil
	}
	check("quote", snapshot.PriceVersionID == q.PriceVersionID && snapshot.LogicalModelCode == task.LogicalModelCode && snapshot.Operation == *task.Operation && snapshot.SelectedLines[0].VariantHash == q.RequestVariantHash && equalVideoFinancialJSON(snapshot.SelectedLines[0].VariantJSON, task.InputJSON))
	var assets []model.AIImageAsset
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id=?", task.RequestID).Order("id ASC").Find(&assets).Error; err != nil {
		return nil, err
	}
	quantity := decimal.Zero
	mediaOK := released && len(assets) == 0
	if submittedRelease {
		_, e := loadVideoProviderReleaseProofTx(tx, task, q)
		mediaOK = e == nil
	}
	if settled {
		root, loadErr := loadVideoSettlementMediaTx(tx, task, preDelivery, now)
		mediaOK = loadErr == nil
		if mediaOK {
			quantity = *root.DurationSeconds
			spec, specErr := parseVideoG4TaskSpec(task.InputJSON)
			mediaOK = specErr == nil && root.Width != nil && root.Height != nil && *root.Width == spec.Width && *root.Height == spec.Height && root.FrameRate != nil && root.FrameRate.Equal(decimal.NewFromInt(int64(spec.FrameRate))) && root.HasAudio != nil && *root.HasAudio == spec.Audio
			for _, a := range assets {
				want := model.AIImageAssetAvailable
				if preDelivery {
					want = model.AIImageAssetTemporary
				}
				if a.LifecycleState != want || a.MediaDeletedAt != nil || a.DeletedAt != nil || a.LegalHold || a.DisputeStatus == model.AIImageDisputeOpen || !a.ExpiresAt.After(now) {
					mediaOK = false
				}
				if a.MIMEType != nil && *a.MIMEType == "video/mp4" && (root.FrameRate == nil || root.Width == nil || root.Height == nil || a.DurationSeconds == nil || !a.DurationSeconds.Equal(quantity) || a.FrameRate == nil || !a.FrameRate.Equal(*root.FrameRate) || a.Width == nil || a.Height == nil || *a.Width != *root.Width || *a.Height != *root.Height) {
					mediaOK = false
				}
			}
		}
	}
	check("output_asset", mediaOK)
	price, priceErr := NewVideoPricingService(nil).CalculateVideoFinal(task.RequestID, q.PriceSnapshotJSON, quantity)
	if priceErr != nil {
		check("sale_line", false)
		return report, nil
	}
	check("hold", hold.Status == r.BillingStatus && hold.SettledAmount != nil && hold.SettledAmount.Equal(price.SettledAmount) && r.SettledAmount != nil && r.SettledAmount.Equal(price.SettledAmount) && videoWalletHistoryConsistent(tx, hold.WalletID, owner.UserID))
	var freeze, unfreeze, consume billingmodel.WalletTransaction
	freezeOK := tx.First(&freeze, link.HoldTransactionID).Error == nil && freeze.WalletID == hold.WalletID && freeze.UserID == owner.UserID && freeze.Type == "freeze" && freeze.Direction == "out" && freeze.Amount.Equal(hold.HoldAmount)
	unfreezeOK := link.ReleaseTransactionID != nil && tx.First(&unfreeze, *link.ReleaseTransactionID).Error == nil && unfreeze.WalletID == hold.WalletID && unfreeze.UserID == owner.UserID && unfreeze.Type == "unfreeze" && unfreeze.Direction == "in" && unfreeze.Amount.Equal(hold.HoldAmount) && unfreeze.ID > freeze.ID
	consumeOK := released && link.SettleTransactionID == nil && price.SettledAmount.IsZero()
	if settled {
		consumeOK = link.SettleTransactionID != nil && tx.First(&consume, *link.SettleTransactionID).Error == nil && consume.WalletID == hold.WalletID && consume.UserID == owner.UserID && consume.Type == "consume" && consume.Direction == "out" && consume.Amount.Equal(price.SettledAmount) && consume.ID > unfreeze.ID && consume.BalanceAfter.Equal(unfreeze.BalanceAfter.Sub(consume.Amount))
	}
	check("freeze", freezeOK)
	check("unfreeze", unfreezeOK)
	check("consume", consumeOK)
	var facts []model.VideoUsageItem
	if err := tx.Where("request_id=?", task.RequestID).Find(&facts).Error; err != nil {
		return nil, err
	}
	metadataOK := true
	adjustments := 0
	gatewayUsage, gatewaySale := 0, 0
	userQtyOK, saleOK := true, true
	for _, f := range facts {
		if f.RecordKind == model.AIUsageAdjustment {
			adjustments++
			continue
		}
		if f.TaskID != task.ID || f.QuoteID != q.ID || f.UserID != owner.UserID || f.ProjectID != owner.ProjectID || !equalOptionalUint64(f.APIKeyID, owner.APIKeyID) || f.LogicalModelCode != task.LogicalModelCode || f.Capability != model.AIVideoCapability || f.Operation == nil || *f.Operation != *task.Operation || f.PriceVersionID == nil || *f.PriceVersionID != q.PriceVersionID || f.VariantHash != q.RequestVariantHash || !equalVideoFinancialJSON(f.VariantJSON, task.InputJSON) || f.MeterType != VideoMeterSeconds || f.UsageUnit != "seconds" || f.Currency == nil || *f.Currency != "CNY" || f.Amount == nil || f.UnitPrice == nil || f.SequenceNo != 0 {
			metadataOK = false
			continue
		}
		if f.Source == "gateway" && f.RecordKind == model.AIUsageFact {
			gatewayUsage++
			userQtyOK = userQtyOK && f.Quantity.Equal(quantity) && f.Amount.IsZero() && f.UnitPrice.IsZero() && f.UnitSize.Equal(price.UsageFact.UnitSize)
		}
		if f.Source == "gateway" && f.RecordKind == model.AIUsageSaleLine {
			gatewaySale++
			saleOK = saleOK && f.Quantity.Equal(quantity) && f.Amount.Equal(price.SettledAmount) && f.UnitPrice.Equal(*price.SaleLine.UnitPrice) && f.UnitSize.Equal(price.SaleLine.UnitSize)
		}
	}
	check("usage_fact", metadataOK && gatewayUsage == 1 && userQtyOK)
	check("sale_line", metadataOK && gatewaySale == 1 && saleOK)
	check("adjustment", validateVideoAdjustmentsTx(tx, task) == nil)
	costOK := false
	if settled {
		cost, e := loadVideoConfirmedCostTx(tx, task, q)
		costOK = e == nil && cost.Quantity.Equal(quantity)
	} else if submittedRelease {
		_, e := loadVideoProviderReleaseProofTx(tx, task, q)
		costOK = e == nil && len(facts)-adjustments == 4
	} else if released {
		costOK = verifyVideoNeverSubmittedTx(tx, task) == nil && len(facts)-adjustments == 3
		for _, f := range facts {
			if f.RecordKind == model.AIUsageCostLine {
				costOK = costOK && f.Source == "gateway" && f.Quantity.IsZero() && f.Amount != nil && f.Amount.IsZero()
			}
		}
	}
	check("cost_line", metadataOK && costOK)
	var inputs []model.AIGatewayTaskInput
	if err := tx.Where("task_id=?", task.ID).Find(&inputs).Error; err != nil {
		return nil, err
	}
	inputOK := (*task.Operation == model.AIVideoOperationTextToVideo && len(inputs) == 0) || (*task.Operation == model.AIVideoOperationImageToVideo && len(inputs) == 1)
	for _, i := range inputs {
		var asset model.AIGatewayInputAsset
		if i.UserID != owner.UserID || i.ProjectID != owner.ProjectID || i.Role != model.AITaskInputReferenceImage || i.Ordinal != 0 || !lowerHex64.MatchString(i.NormalizedSHA256) || i.InputVersion == 0 || i.LeaseReleasedAt == nil || tx.First(&asset, i.InputAssetID).Error != nil || asset.UserID != owner.UserID || asset.ProjectID != owner.ProjectID || asset.NormalizedSHA256 == nil || *asset.NormalizedSHA256 != i.NormalizedSHA256 {
			inputOK = false
		}
	}
	check("task_input", inputOK)
	wantDelivery := model.AIDeliveryAvailable
	if preDelivery {
		wantDelivery = model.AIDeliveryPending
	}
	if released {
		wantDelivery = model.AIDeliveryRejected
	}
	var extraAttempts int64
	if err := tx.Model(&model.AIExecutionAttempt{}).Where("request_id=?", task.RequestID).Count(&extraAttempts).Error; err != nil {
		return nil, err
	}
	// 当前Fake原生执行以共享Task的绑定与事件为唯一执行事实，不允许另有未闭合的旧驱动Attempt。
	check("task", (settled || released) && task.CompletedAt != nil && task.DeliveryStatus == wantDelivery && extraAttempts == 0)
	eventOK, callbackTransitions := videoExecutionEventsConsistent(tx, task, settled || submittedRelease)
	check("task_event", eventOK && videoCancelIntentConsistent(tx, task))
	check("provider_callback_event", videoCallbacksConsistent(tx, task, callbackTransitions))
	compRepo := repository.NewVideoCompensationRepository(tx).WithClock(func() time.Time { return now })
	job, jobErr := compRepo.FindRequestTx(tx, task.RequestID)
	compOK := errors.Is(jobErr, gorm.ErrRecordNotFound)
	if jobErr == nil {
		compOK = job.Status == "completed" && job.CompletedAt != nil
		if preDelivery && lease != nil && lease.RequestID == task.RequestID && lease.ID == job.ID {
			_, e := compRepo.CheckLeaseTx(tx, *lease)
			compOK = e == nil
		}
	}
	check("compensation", compOK)
	financeErr := ErrVideoBillingState
	if settled {
		financeErr = validateVideoSettledFinanceTx(tx, task, *r, *link, *hold, price)
	} else if released {
		financeErr = validateVideoCancelledFactsTx(tx, task, *r, *link, *hold)
	}
	check("outbox", financeErr == nil)
	for _, ok := range report.Checks {
		if !ok {
			report.Passed = false
		}
	}
	return report, nil
}

func equalVideoFinancialJSON(a, b json.RawMessage) bool {
	var x, y interface{}
	dx, dy := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	dx.UseNumber()
	dy.UseNumber()
	return json.Valid(a) && json.Valid(b) && dx.Decode(&x) == nil && dy.Decode(&y) == nil && reflect.DeepEqual(x, y)
}

// videoWalletHistoryConsistent 锁住钱包再串联已有流水，避免把全额解冻误认为净释放；冻结额与所有未终结Hold相等。
func videoWalletHistoryConsistent(tx *gorm.DB, walletID, userID uint64) bool {
	var wallet billingmodel.Wallet
	if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", walletID, userID).First(&wallet).Error != nil || wallet.Currency != "CNY" {
		return false
	}
	var rows []billingmodel.WalletTransaction
	if tx.Where("wallet_id=?", walletID).Order("id ASC").Find(&rows).Error != nil || len(rows) == 0 {
		return false
	}
	var balance decimal.Decimal
	for index, row := range rows {
		if row.UserID != userID || row.Amount.IsNegative() || row.BalanceAfter.IsNegative() {
			return false
		}
		in := row.Type == "recharge" || row.Type == "refund" || row.Type == "unfreeze"
		out := row.Type == "consume" || row.Type == "freeze"
		if (!in && !out) || (in && row.Direction != "in") || (out && row.Direction != "out") {
			return false
		}
		delta := row.Amount
		if out {
			delta = delta.Neg()
		}
		if index == 0 {
			balance = row.BalanceAfter.Sub(delta)
			if balance.IsNegative() {
				return false
			}
		}
		balance = balance.Add(delta)
		if !balance.Equal(row.BalanceAfter) {
			return false
		}
	}
	var holding []billingmodel.WalletHold
	if tx.Where("wallet_id=? AND status='holding'", walletID).Find(&holding).Error != nil {
		return false
	}
	frozen := decimal.Zero
	for _, h := range holding {
		if h.UserID != userID {
			return false
		}
		frozen = frozen.Add(h.HoldAmount)
	}
	return wallet.BalanceAmount.Equal(balance) && wallet.FrozenAmount.Equal(frozen)
}

func videoExecutionEventsConsistent(tx *gorm.DB, task *repository.VideoTaskRecord, submitted bool) (bool, int) {
	var events []model.AIGatewayTaskEvent
	if tx.Where("task_id=?", task.ID).Order("id ASC").Find(&events).Error != nil {
		return false, 0
	}
	state := model.AIImageTaskCreated
	billing, delivery := model.AIBillingUnquoted, model.AIDeliveryPending
	holds, bindings, submits, callbacks := 0, 0, 0, 0
	for _, e := range events {
		if e.UserID != task.UserID || e.ProjectID != task.ProjectID {
			return false, 0
		}
		if e.EventType == "provider_task_bound_pending" {
			if e.FromStatus != nil || e.ToStatus != nil || e.Source != "worker" || state != model.AIImageTaskPendingReconcile {
				return false, 0
			}
			bindings++
			continue
		}
		if e.EventType == "billing_status_changed" {
			if e.FromStatus == nil || e.ToStatus == nil || *e.FromStatus != billing || !repository.VideoBillingTransitionAllowed(billing, *e.ToStatus) {
				return false, 0
			}
			billing = *e.ToStatus
			continue
		}
		if e.EventType == "delivery_status_changed" {
			if e.FromStatus == nil || e.ToStatus == nil || *e.FromStatus != delivery || !repository.VideoDeliveryTransitionAllowed(delivery, *e.ToStatus) {
				return false, 0
			}
			delivery = *e.ToStatus
			continue
		}
		if e.EventType != "video_billing_held" && e.EventType != "execution_status_changed" && e.EventType != "provider_task_bound" && e.EventType != "provider_callback_status_changed" {
			continue
		}
		if e.FromStatus == nil || e.ToStatus == nil || *e.FromStatus != state || !repository.VideoExecutionTransitionAllowed(state, *e.ToStatus) {
			return false, 0
		}
		state = *e.ToStatus
		if e.EventType == "video_billing_held" {
			holds++
			billing = model.AIBillingHeld
		}
		if e.EventType == "provider_task_bound" {
			bindings++
		}
		if state == model.AIImageTaskSubmitting {
			submits++
		}
		if e.EventType == "provider_callback_status_changed" {
			callbacks++
		}
	}
	want := 0
	if submitted {
		want = 1
	}
	return state == task.Status && billing == task.BillingStatus && delivery == task.DeliveryStatus && holds == 1 && bindings == want && submits == want && int(task.AttemptCount) == want, callbacks
}

func videoCallbacksConsistent(tx *gorm.DB, task *repository.VideoTaskRecord, wantApplied int) bool {
	query := tx.Where("task_id=?", task.ID)
	if task.ProviderCode != nil && task.ProviderTaskID != nil {
		query = tx.Where("task_id=? OR (provider_code=? AND provider_task_id=?)", task.ID, *task.ProviderCode, *task.ProviderTaskID)
	}
	var rows []model.AIGatewayProviderCallbackEvent
	if query.Find(&rows).Error != nil {
		return false
	}
	applied := 0
	for _, e := range rows {
		if !lowerHex64.MatchString(e.BodySHA256) || e.ProcessedAt == nil || e.ProcessedAt.Before(e.ReceivedAt) {
			return false
		}
		if e.TaskID != nil && (*e.TaskID != task.ID || e.UserID == nil || *e.UserID != task.UserID || e.ProjectID == nil || *e.ProjectID != task.ProjectID) {
			return false
		}
		if e.TaskID != nil && (task.ProviderCode == nil || task.ProviderTaskID == nil || e.ProviderCode != *task.ProviderCode || e.ProviderTaskID != *task.ProviderTaskID) {
			return false
		}
		var detail map[string]string
		if json.Unmarshal(e.ApplicationResultJSON, &detail) != nil || len(detail) > 2 {
			return false
		}
		switch e.ProcessStatus {
		case model.AIProviderCallbackApplied:
			if e.SignatureStatus != model.AIProviderCallbackSignatureValid || e.TaskID == nil || detail["result"] != "applied" {
				return false
			}
			applied++
		case model.AIProviderCallbackIgnored:
			if e.SignatureStatus != model.AIProviderCallbackSignatureValid || detail["result"] != "ignored" {
				return false
			}
		case model.AIProviderCallbackFailed:
			if e.SignatureStatus == model.AIProviderCallbackSignatureValid || detail["reason"] != "signature_invalid" {
				return false
			}
		default:
			return false
		}
	}
	return applied == wantApplied
}

// 取消意图不属于执行/计费/交付三轴，仍需与唯一追加事件及原时间完全对应。
func videoCancelIntentConsistent(tx *gorm.DB, task *repository.VideoTaskRecord) bool {
	var events []model.AIGatewayTaskEvent
	if tx.Where("task_id=? AND event_type='cancel_requested'", task.ID).Find(&events).Error != nil {
		return false
	}
	if task.CancelRequestedAt == nil {
		return len(events) == 0
	}
	if len(events) != 1 {
		return false
	}
	e := events[0]
	return e.EventID == "vid_cancel_"+strconv.FormatUint(task.ID, 10) && e.UserID == task.UserID && e.ProjectID == task.ProjectID && e.Source == "api" && e.FromStatus == nil && e.ToStatus == nil && e.CreatedAt.Equal(*task.CancelRequestedAt)
}
