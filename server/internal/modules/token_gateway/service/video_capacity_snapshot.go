package service

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"

	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type VideoCapacitySnapshotSummary struct {
	Epoch                  uint64
	Total, Queued, Running int
}

// VideoCapacitySnapshotBuilder把完整账本校验隐藏在单一接口后，不暴露可修改记录或内部nonce。
type VideoCapacitySnapshotBuilder struct {
	db       *gorm.DB
	recovery *repository.VideoCapacityRecoveryRepository
	nonceKey *VideoCapacityNonceKey
}

func NewVideoCapacitySnapshotBuilder(db *gorm.DB, recovery *repository.VideoCapacityRecoveryRepository, nonceKey *VideoCapacityNonceKey) *VideoCapacitySnapshotBuilder {
	return &VideoCapacitySnapshotBuilder{db: db, recovery: recovery, nonceKey: nonceKey}
}

func (b *VideoCapacitySnapshotBuilder) BuildSnapshot(ctx context.Context, proof *repository.VideoCapacityRecoveryLease, policy *video.VideoCapacityPolicy) (*VideoCapacityRecoverySnapshot, VideoCapacitySnapshotSummary, error) {
	var empty VideoCapacitySnapshotSummary
	if b == nil || b.db == nil || b.recovery == nil || b.nonceKey == nil || ctx == nil || proof == nil || b.db.Statement == nil {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	if _, nested := b.db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	state, err := b.recovery.Current(ctx)
	if err != nil || state.State != "recovering" || state.Epoch != proof.Epoch() || state.PolicyHash != hash {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	if err := b.recovery.Validate(ctx, proof); err != nil {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	if _, err := b.recovery.Renew(ctx, proof); err != nil {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	records := []VideoCapacityRecoveryRecord{}
	summary := VideoCapacitySnapshotSummary{Epoch: proof.Epoch()}
	err = b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 在同一RR视图内按主键分页；历史终态可以很多，只有最终活动记录受102上限约束。
		var lastID uint64
		for {
			var tasks []repository.VideoTaskRecord
			if err := tx.Table("ai_gateway_tasks AS tasks").Select(`tasks.*,requests.execution_status AS request_execution_status,requests.billing_status,requests.delivery_status,requests.version_no AS request_version_no`).Joins("JOIN ai_requests AS requests ON requests.request_id=tasks.request_id AND requests.user_id=tasks.user_id AND requests.project_id=tasks.project_id").Where("tasks.id>? AND tasks.capability=? AND tasks.operation IN ?", lastID, model.AIVideoCapability, []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo}).Where("requests.modality='video' AND requests.capability=?", model.AIVideoCapability).Order("tasks.id").Limit(50).Find(&tasks).Error; err != nil {
				return err
			}
			if len(tasks) == 0 {
				break
			}
			for i := range tasks {
				task := &tasks[i]
				owner := repository.VideoOwner{UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID}
				request, quote, link, hold, err := loadVideoFinancialSnapshotTx(tx, task, owner)
				if err != nil {
					return err
				}
				if !videoOutboxExecutionMatches(task) || request.BillingStatus != task.BillingStatus || request.DeliveryStatus != task.DeliveryStatus {
					return ErrVideoGovernanceUnavailable
				}
				price, err := DecodeVideoPriceSnapshot(quote.PriceSnapshotJSON)
				if err != nil || price.PriceVersionID != quote.PriceVersionID || price.LogicalModelCode != task.LogicalModelCode || price.Operation != *task.Operation || price.SelectedLines[0].VariantHash != quote.RequestVariantHash || !equalVideoFinancialJSON(price.SelectedLines[0].VariantJSON, task.InputJSON) {
					return ErrVideoGovernanceUnavailable
				}
				if err := validateVideoCapacityOutboxTx(tx, task, request, link, hold); err != nil {
					return err
				}
				if _, err := videoOutboxInputIdentityTx(tx, task); err != nil {
					return err
				}
				var bindings []model.AIGatewayTaskInput
				if err := tx.Where("task_id=?", task.ID).Find(&bindings).Error; err != nil {
					return err
				}
				if videoCapacityTerminal(task.Status) {
					if err := validateVideoCapacityTerminalTx(tx, task, request, quote, link, hold, bindings); err != nil {
						return err
					}
					continue
				}
				if (request.BillingStatus != model.AIBillingHeld && request.BillingStatus != model.AIBillingSettlementPending) || request.DeliveryStatus != model.AIDeliveryPending || hold.Status != billingmodel.HoldStatusHolding {
					return ErrVideoGovernanceUnavailable
				}
				for _, binding := range bindings {
					if binding.LeaseReleasedAt != nil {
						return ErrVideoGovernanceUnavailable
					}
				}
				phase, provider, err := videoCapacitySnapshotPhaseTx(tx, task, proof.Epoch())
				if err != nil {
					return err
				}
				attempt, err := b.nonceKey.Attempt(proof.Epoch(), VideoCapacityIdentity{TaskID: task.PublicID, RequestID: task.RequestID, UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID, Model: task.LogicalModelCode, Provider: provider, Operation: *task.Operation})
				if err != nil {
					return ErrVideoGovernanceUnavailable
				}
				if !lowerHex64.MatchString(attempt.nonce) {
					return ErrVideoGovernanceUnavailable
				}
				records = append(records, VideoCapacityRecoveryRecord{Attempt: attempt, Phase: phase})
				summary.Total++
				if summary.Total > 102 {
					return ErrVideoGovernanceUnavailable
				}
				if phase == "queued" {
					summary.Queued++
				} else {
					summary.Running++
				}
			}
			lastID = tasks[len(tasks)-1].ID
			// 每页完成后续同一恢复proof；失败立即丢弃尚未返回的快照，不延长Task或Provider期限。
			if _, err := b.recovery.Renew(ctx, proof); err != nil {
				return ErrVideoGovernanceUnavailable
			}
			if len(tasks) < 50 {
				break
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	if err := b.recovery.Validate(ctx, proof); err != nil {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	snapshot, err := newVideoCapacityRecoverySnapshot(proof.Epoch(), policy, records)
	if err != nil {
		return nil, empty, ErrVideoGovernanceUnavailable
	}
	return snapshot, summary, nil
}

func loadVideoFinancialSnapshotTx(tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner) (*model.VideoBillingRequest, *model.AIGatewayQuote, *model.AIRequestWalletLink, *billingmodel.WalletHold, error) {
	var request model.VideoBillingRequest
	var quote model.AIGatewayQuote
	var link model.AIRequestWalletLink
	var hold billingmodel.WalletHold
	if tx.Where("request_id=? AND command_kind='create_video'", task.RequestID).First(&request).Error != nil || tx.First(&quote, task.QuoteID).Error != nil || tx.Where("request_id=?", task.RequestID).First(&link).Error != nil || tx.First(&hold, link.WalletHoldID).Error != nil {
		return nil, nil, nil, nil, ErrVideoGovernanceUnavailable
	}
	if request.UserID != owner.UserID || request.ProjectID == nil || *request.ProjectID != owner.ProjectID || !equalOptionalUint64(request.APIKeyID, owner.APIKeyID) || request.Operation == nil || task.Operation == nil || quote.Operation == nil || *request.Operation != *task.Operation || *quote.Operation != *task.Operation || request.LogicalModelCode != task.LogicalModelCode || quote.LogicalModelCode != task.LogicalModelCode || quote.UserID != owner.UserID || quote.ProjectID != owner.ProjectID || !equalOptionalUint64(quote.APIKeyID, owner.APIKeyID) || quote.ConsumedRequestID == nil || *quote.ConsumedRequestID != task.RequestID || request.HeldAmount == nil || request.QuotedAmount == nil || !request.HeldAmount.Equal(quote.QuotedAmount) || !request.QuotedAmount.Equal(quote.QuotedAmount) || !link.HeldAmount.Equal(quote.QuotedAmount) || !link.QuotedAmount.Equal(quote.QuotedAmount) || !hold.HoldAmount.Equal(quote.QuotedAmount) || hold.WalletID != link.WalletID || hold.UserID != owner.UserID || hold.FreezeTxnID == nil || *hold.FreezeTxnID != link.HoldTransactionID || hold.IdempotencyKey != task.RequestID+":video-hold" || quote.Currency != "CNY" || quote.Capability != model.AIVideoCapability {
		return nil, nil, nil, nil, ErrVideoGovernanceUnavailable
	}
	return &request, &quote, &link, &hold, nil
}

func validateVideoCapacityOutboxTx(tx *gorm.DB, task *repository.VideoTaskRecord, request *model.VideoBillingRequest, link *model.AIRequestWalletLink, hold *billingmodel.WalletHold) error {
	var events []model.AIOutboxEvent
	if err := tx.Where("aggregate_id=?", task.RequestID).Find(&events).Error; err != nil {
		return err
	}
	if len(events) == 0 {
		return ErrVideoGovernanceUnavailable
	}
	seen := map[string]bool{}
	seenAdjustments := map[string]bool{}
	held := false
	for _, event := range events {
		if event.AggregateType != "video_request" || !validVideoOutboxTransportState(event) {
			return ErrVideoGovernanceUnavailable
		}
		if event.EventType == "video_dlq_recovery_dispatch" {
			if validateVideoCapacityDLQDispatchTx(tx, event, task) != nil {
				return ErrVideoGovernanceUnavailable
			}
			continue
		}
		if event.EventType == "video_adjustment_recorded" {
			if seenAdjustments[event.EventID] {
				return ErrVideoGovernanceUnavailable
			}
			seenAdjustments[event.EventID] = true
		} else {
			if seen[event.EventType] {
				return ErrVideoGovernanceUnavailable
			}
			seen[event.EventType] = true
		}
		if event.EventType == "video_billing_held" {
			held = true
		}
		if err := validateVideoOutboxGroundTx(tx, event.EventType, task, request, link, hold); err != nil {
			return ErrVideoGovernanceUnavailable
		}
		if err := validateVideoOutboxPayloadSnapshotTx(tx, event, task, request, hold.HoldAmount); err != nil {
			return ErrVideoGovernanceUnavailable
		}
	}
	if !held {
		return ErrVideoGovernanceUnavailable
	}
	return nil
}

// 恢复dispatch是运维运输事实，不参与金额状态枚举；容量恢复只接受已确认发布且完整绑定的事件。
func validateVideoCapacityDLQDispatchTx(tx *gorm.DB, event model.AIOutboxEvent, task *repository.VideoTaskRecord) error {
	if event.Status != model.AIOutboxPublished || event.AggregateID != task.RequestID {
		return ErrVideoGovernanceUnavailable
	}
	payload, err := decodeVideoDLQDispatchPayload(event.PayloadJSON)
	if err != nil || payload.TaskID != task.PublicID || payload.RequestID != task.RequestID {
		return ErrVideoGovernanceUnavailable
	}
	inputID, err := videoOutboxInputIdentityTx(tx, task)
	if err != nil || inputID != payload.InputAssetID {
		return ErrVideoGovernanceUnavailable
	}
	identity := fmt.Sprintf("%s|%s|%s|%d|%d", payload.TaskID, payload.RequestID, payload.Stage, payload.Attempt, payload.TaskVersion)
	if event.EventID != "vg7_dlq_dispatch_"+videoBillingDigest(identity) || payload.RecoveryEventID != "vg7_dlq_request_"+videoBillingDigest(identity) {
		return ErrVideoGovernanceUnavailable
	}
	var requested int64
	if err := tx.Model(&model.AIGatewayTaskEvent{}).Where("event_id=? AND task_id=? AND event_type='video_dlq_recovery_requested' AND source='reconciler'", payload.RecoveryEventID, task.ID).Count(&requested).Error; err != nil || requested != 1 {
		return ErrVideoGovernanceUnavailable
	}
	return nil
}

func videoCapacitySnapshotPhaseTx(tx *gorm.DB, task *repository.VideoTaskRecord, recoveryEpoch uint64) (string, string, error) {
	queued := task.Status == model.AIImageTaskCreated || task.Status == model.AIImageTaskReserved || task.Status == model.AIImageTaskQueued
	if queued {
		if task.PlannedProviderCode != nil || task.ProviderCode != nil || task.ProviderTaskID != nil || task.AttemptCount != 0 || verifyVideoNeverSubmittedTx(tx, task) != nil {
			return "", "", ErrVideoGovernanceUnavailable
		}
		return "queued", "fake-native-async", nil
	}
	if _, err := videoSubmissionClaimTx(tx, task, 0); err != nil {
		return "", "", ErrVideoGovernanceUnavailable
	}
	provider := ""
	if task.PlannedProviderCode != nil {
		provider = *task.PlannedProviderCode
		if validateVideoCapacityPlanTx(tx, task, recoveryEpoch) != nil {
			return "", "", ErrVideoGovernanceUnavailable
		}
	}
	if (task.ProviderCode == nil) != (task.ProviderTaskID == nil) {
		return "", "", ErrVideoGovernanceUnavailable
	}
	if task.ProviderCode != nil {
		if *task.ProviderCode != "fake-native-async" || !stringsHasTaskUUID(*task.ProviderTaskID) || task.AttemptCount != 1 || (provider != "" && provider != *task.ProviderCode) {
			return "", "", ErrVideoGovernanceUnavailable
		}
		provider = *task.ProviderCode
		var accepted []model.VideoFinancialEvent
		if err := tx.Where("task_id=? AND event_type='submission_receipt_accepted'", task.ID).Find(&accepted).Error; err != nil || len(accepted) != 1 {
			return "", "", ErrVideoGovernanceUnavailable
		}
		e := accepted[0]
		if e.EventID != "vg5_"+videoBillingDigest(task.RequestID+":submission_accepted") || e.UserID != task.UserID || e.ProjectID != task.ProjectID || e.Source != "worker" || e.FromStatus != nil || e.ToStatus != nil || !lowerHex64.MatchString(e.FactSHA256) {
			return "", "", ErrVideoGovernanceUnavailable
		}
	}
	if provider == "" {
		return "", "", ErrVideoGovernanceUnavailable
	}
	return "running", provider, nil
}

func videoCapacityTerminal(status string) bool {
	return status == model.AIImageTaskSucceeded || status == model.AIImageTaskFailed || status == model.AIImageTaskCancelled || status == model.AIImageTaskExpired
}

// 终态只有财务、输入和Provider结束事实全部稳定时才能排除；pending或expired不能凭状态字符串释放债务。
func validateVideoCapacityTerminalTx(tx *gorm.DB, task *repository.VideoTaskRecord, request *model.VideoBillingRequest, quote *model.AIGatewayQuote, link *model.AIRequestWalletLink, hold *billingmodel.WalletHold, bindings []model.AIGatewayTaskInput) error {
	for _, binding := range bindings {
		if binding.LeaseReleasedAt == nil {
			return ErrVideoGovernanceUnavailable
		}
	}
	if task.ProviderCode == nil && task.ProviderTaskID == nil {
		if task.Status != model.AIImageTaskCancelled || verifyVideoNeverSubmittedTx(tx, task) != nil || validateVideoCancelledFactsSnapshotTx(tx, task, *request, *link, *hold) != nil {
			return ErrVideoGovernanceUnavailable
		}
		return nil
	}
	if task.ProviderCode == nil || task.ProviderTaskID == nil || task.AttemptCount != 1 || *task.ProviderCode != "fake-native-async" || !stringsHasTaskUUID(*task.ProviderTaskID) {
		return ErrVideoGovernanceUnavailable
	}
	switch task.Status {
	case model.AIImageTaskSucceeded:
		if request.BillingStatus != model.AIBillingSettled || hold.Status != billingmodel.HoldStatusSettled || (request.DeliveryStatus != model.AIDeliveryAvailable && request.DeliveryStatus != model.AIDeliveryExpired) {
			return ErrVideoGovernanceUnavailable
		}
		media, err := loadVideoSettlementMediaSnapshotTx(tx, task, task.UpdatedAt)
		if err != nil {
			return ErrVideoGovernanceUnavailable
		}
		price, err := NewVideoPricingService(nil).CalculateVideoFinal(task.RequestID, quote.PriceSnapshotJSON, *media.DurationSeconds)
		if err != nil || validateVideoSettledFinanceSnapshotTx(tx, task, *request, *link, *hold, price) != nil {
			return ErrVideoGovernanceUnavailable
		}
	case model.AIImageTaskFailed:
		if request.BillingStatus != model.AIBillingReleased || hold.Status != billingmodel.HoldStatusReleased || request.DeliveryStatus != model.AIDeliveryRejected {
			return ErrVideoGovernanceUnavailable
		}
		if validateVideoCancelledFactsSnapshotTx(tx, task, *request, *link, *hold) != nil {
			return ErrVideoGovernanceUnavailable
		}
	case model.AIImageTaskCancelled:
		if validateVideoCancelledFactsSnapshotTx(tx, task, *request, *link, *hold) != nil {
			return ErrVideoGovernanceUnavailable
		}
	default:
		return ErrVideoGovernanceUnavailable
	}
	return nil
}

func stringsHasTaskUUID(value string) bool {
	return regexp.MustCompile(`^taskUUID-[A-Za-z0-9_.-]+$`).MatchString(value) && len(value) <= 191
}
func validateVideoCapacityPlanTx(tx *gorm.DB, task *repository.VideoTaskRecord, recoveryEpoch uint64) error {
	if recoveryEpoch == 0 || task.SubmissionIntentID == nil || !videoProviderTaskUUIDPattern.MatchString(*task.SubmissionIntentID) || task.SubmissionClaimVersion == nil || *task.SubmissionClaimVersion < 2 || task.SubmissionWorkerVersion == nil || *task.SubmissionWorkerVersion == 0 || *task.PlannedProviderCode != "fake-native-async" {
		return ErrVideoGovernanceUnavailable
	}
	var events []model.AIGatewayTaskEvent
	if err := tx.Where("task_id=? AND event_type='video_submission_planned'", task.ID).Find(&events).Error; err != nil || len(events) != 1 {
		return ErrVideoGovernanceUnavailable
	}
	e := events[0]
	if e.EventID != "vg7_plan_"+videoBillingDigest(task.PublicID) || e.UserID != task.UserID || e.ProjectID != task.ProjectID || e.Source != "worker" || e.FromStatus != nil || e.ToStatus != nil || string(e.SafeDetailJSON) != "{}" {
		return ErrVideoGovernanceUnavailable
	}
	if task.SubmissionCapacityEpoch == nil {
		// 升级前计划尚未绑定容量epoch，仍保守计入running；业务继续提交前必须由执行协调器补齐。
		if task.SubmissionSendTokenHash != nil || task.SubmissionSendWorker != nil || task.SubmissionSendStartedAt != nil {
			return ErrVideoGovernanceUnavailable
		}
		return nil
	}
	if *task.SubmissionCapacityEpoch == 0 || *task.SubmissionCapacityEpoch > recoveryEpoch {
		return ErrVideoGovernanceUnavailable
	}
	var capacity []model.AIGatewayTaskEvent
	if err := tx.Where("task_id=? AND event_type='video_submission_capacity_bound'", task.ID).Find(&capacity).Error; err != nil || len(capacity) != 1 {
		return ErrVideoGovernanceUnavailable
	}
	bound := capacity[0]
	want := "vg7_capacity_" + videoBillingDigest(task.PublicID+":"+strconv.FormatUint(*task.SubmissionCapacityEpoch, 10))
	if bound.EventID != want || bound.UserID != task.UserID || bound.ProjectID != task.ProjectID || bound.Source != "worker" || bound.FromStatus != nil || bound.ToStatus != nil || string(bound.SafeDetailJSON) != "{}" {
		return ErrVideoGovernanceUnavailable
	}
	if task.SubmissionSendTokenHash == nil && task.SubmissionSendWorker == nil && task.SubmissionSendStartedAt == nil {
		// 114已绑定容量但115发送权尚未领取的升级窗口仍保守占用，不能据此调用Provider。
		return nil
	}
	if task.SubmissionSendTokenHash == nil || !lowerHex64.MatchString(*task.SubmissionSendTokenHash) || task.SubmissionSendWorker == nil || *task.SubmissionSendWorker == 0 || task.SubmissionSendStartedAt == nil {
		return ErrVideoGovernanceUnavailable
	}
	var sends []model.AIGatewayTaskEvent
	if err := tx.Where("task_id=? AND event_type='video_submission_send_claimed'", task.ID).Find(&sends).Error; err != nil || len(sends) != 1 {
		return ErrVideoGovernanceUnavailable
	}
	send := sends[0]
	if send.EventID != "vg7_send_"+videoBillingDigest(task.PublicID) || send.UserID != task.UserID || send.ProjectID != task.ProjectID || send.Source != "worker" || send.FromStatus != nil || send.ToStatus != nil || string(send.SafeDetailJSON) != "{}" {
		return ErrVideoGovernanceUnavailable
	}
	return nil
}
