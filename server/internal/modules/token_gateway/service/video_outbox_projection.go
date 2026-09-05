package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoDLQDispatchPayload struct {
	Version         int
	TaskID          string
	RequestID       string
	InputAssetID    string
	Attempt         uint32
	Stage           video.TaskStage
	TaskVersion     uint64
	RecoveryEventID string
}

var ErrVideoOutboxProjection = errors.New("视频事件引用或发布租约无效")

// VideoOutboxProjector 将原G5事件投影为任务唤醒引用；不拥有执行、上传、签名或资金写入能力。
type VideoOutboxProjector struct{ db *gorm.DB }

func NewVideoOutboxProjector(db *gorm.DB) *VideoOutboxProjector { return &VideoOutboxProjector{db: db} }

// Project 先核原事件租约，再沿Task/Request/Quote/Hold/Input身份重建消息，绝不直接转发PayloadJSON。
// 终态、撤权或已删除输入仍可能有迟到事件；这里只解析历史归属，真正使用资格由任务处理器重新检查。
func (p *VideoOutboxProjector) Project(ctx context.Context, claimed model.AIOutboxEvent) (video.TaskMessage, error) {
	if p == nil || p.db == nil || claimed.ID == 0 || claimed.AggregateType != "video_request" || claimed.Status != model.AIOutboxPublishing || claimed.LockedAt == nil {
		return video.TaskMessage{}, ErrVideoOutboxProjection
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var message video.TaskMessage
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event model.AIOutboxEvent
		if err := tx.Where("id=?", claimed.ID).Take(&event).Error; err != nil {
			return err
		}
		if !sameVideoOutboxClaim(event, claimed) {
			return ErrVideoOutboxProjection
		}
		var identity model.AIImageTask
		if err := tx.Where("request_id=? AND capability=?", event.AggregateID, model.AIVideoCapability).Take(&identity).Error; err != nil {
			return err
		}
		if identity.RequestID != event.AggregateID {
			return ErrVideoOutboxProjection
		}
		owner := repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
		task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, identity.PublicID, owner)
		if err != nil {
			return err
		}
		request, quote, link, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
		if err != nil {
			return err
		}
		if task.RequestID != event.AggregateID || request.RequestID != task.RequestID || quote.Currency != "CNY" || quote.Capability != model.AIVideoCapability || !equalVideoFinancialJSON(request.PriceSnapshotJSON, quote.PriceSnapshotJSON) || !videoOutboxExecutionMatches(task) {
			return ErrVideoOutboxProjection
		}
		snapshot, err := DecodeVideoPriceSnapshot(quote.PriceSnapshotJSON)
		if err != nil || snapshot.PriceVersionID != quote.PriceVersionID || snapshot.LogicalModelCode != task.LogicalModelCode || snapshot.Operation != *task.Operation || snapshot.SelectedLines[0].VariantHash != quote.RequestVariantHash || !equalVideoFinancialJSON(snapshot.SelectedLines[0].VariantJSON, task.InputJSON) {
			return ErrVideoOutboxProjection
		}
		if err := validateVideoOutboxGroundTx(tx, event.EventType, task, request, link, hold); err != nil {
			return err
		}
		if err := validateVideoOutboxPayloadTx(tx, event, task, request, hold.HoldAmount); err != nil {
			return err
		}
		inputID, err := videoOutboxInputIdentityTx(tx, task)
		if err != nil {
			return err
		}
		message = video.TaskMessage{TaskID: task.PublicID, RequestID: task.RequestID, InputAssetID: inputID, Attempt: 0}
		if _, err := video.EncodeTaskMessage(message); err != nil {
			return err
		}
		// 钱包或Task行锁等待后再读原Outbox，旧持有者不能用入口快照跨过已发生的接管。
		var latest model.AIOutboxEvent
		if err := tx.Where("id=?", claimed.ID).Take(&latest).Error; err != nil {
			return err
		}
		if !sameVideoOutboxClaim(latest, claimed) {
			return ErrVideoOutboxProjection
		}
		return ctx.Err()
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return video.TaskMessage{}, ErrVideoOutboxProjection
	}
	return message, nil
}

// ProjectDLQRecovery只投影管理事务写入的恢复dispatch，不接受任意Payload直接指定队列。
func (p *VideoOutboxProjector) ProjectDLQRecovery(ctx context.Context, claimed model.AIOutboxEvent) (video.TaskStage, video.TaskMessage, error) {
	if p == nil || p.db == nil || claimed.ID == 0 || claimed.AggregateType != "video_request" || claimed.EventType != "video_dlq_recovery_dispatch" || claimed.Status != model.AIOutboxPublishing || claimed.LockedAt == nil {
		return "", video.TaskMessage{}, ErrVideoOutboxProjection
	}
	payload, err := decodeVideoDLQDispatchPayload(claimed.PayloadJSON)
	if err != nil {
		return "", video.TaskMessage{}, ErrVideoOutboxProjection
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	message := video.TaskMessage{TaskID: payload.TaskID, RequestID: payload.RequestID, InputAssetID: payload.InputAssetID, Attempt: payload.Attempt}
	err = p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var event model.AIOutboxEvent
		if err := tx.Where("id=?", claimed.ID).Take(&event).Error; err != nil || !sameVideoOutboxClaim(event, claimed) || event.AggregateID != payload.RequestID {
			return ErrVideoOutboxProjection
		}
		var identity model.AIImageTask
		if err := tx.Where("public_id=? AND request_id=? AND capability='video.generate' AND operation IN ('text_to_video','image_to_video')", payload.TaskID, payload.RequestID).Take(&identity).Error; err != nil {
			return ErrVideoOutboxProjection
		}
		owner := repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
		task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, identity.PublicID, owner)
		if err != nil {
			return ErrVideoOutboxProjection
		}
		inputID, err := videoOutboxInputIdentityTx(tx, task)
		if err != nil || inputID != payload.InputAssetID {
			return ErrVideoOutboxProjection
		}
		identityText := fmt.Sprintf("%s|%s|%s|%d|%d", payload.TaskID, payload.RequestID, payload.Stage, payload.Attempt, payload.TaskVersion)
		if event.EventID != "vg7_dlq_dispatch_"+videoBillingDigest(identityText) || payload.RecoveryEventID != "vg7_dlq_request_"+videoBillingDigest(identityText) {
			return ErrVideoOutboxProjection
		}
		var requestEvent int64
		if err := tx.Model(&model.AIGatewayTaskEvent{}).Where("event_id=? AND task_id=? AND event_type='video_dlq_recovery_requested' AND source='reconciler'", payload.RecoveryEventID, task.ID).Count(&requestEvent).Error; err != nil || requestEvent != 1 {
			return ErrVideoOutboxProjection
		}
		if _, err := video.EncodeTaskMessage(message); err != nil {
			return ErrVideoOutboxProjection
		}
		var latest model.AIOutboxEvent
		if err := tx.Where("id=?", claimed.ID).Take(&latest).Error; err != nil || !sameVideoOutboxClaim(latest, claimed) {
			return ErrVideoOutboxProjection
		}
		return ctx.Err()
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", video.TaskMessage{}, ErrVideoOutboxProjection
	}
	return payload.Stage, message, nil
}

func decodeVideoDLQDispatchPayload(raw json.RawMessage) (videoDLQDispatchPayload, error) {
	var result videoDLQDispatchPayload
	if len(raw) == 0 || len(raw) > 2048 {
		return result, ErrVideoOutboxProjection
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return result, ErrVideoOutboxProjection
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return result, ErrVideoOutboxProjection
		}
		seen[key] = true
		switch key {
		case "version":
			err = decoder.Decode(&result.Version)
		case "task_id":
			err = decoder.Decode(&result.TaskID)
		case "request_id":
			err = decoder.Decode(&result.RequestID)
		case "input_asset_id":
			err = decoder.Decode(&result.InputAssetID)
		case "attempt":
			err = decoder.Decode(&result.Attempt)
		case "stage":
			err = decoder.Decode(&result.Stage)
		case "task_version":
			err = decoder.Decode(&result.TaskVersion)
		case "recovery_event_id":
			err = decoder.Decode(&result.RecoveryEventID)
		default:
			return result, ErrVideoOutboxProjection
		}
		if err != nil {
			return result, ErrVideoOutboxProjection
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || decoder.Decode(&struct{}{}) != io.EOF || len(seen) != 8 || result.Version != 1 || result.TaskVersion == 0 ||
		(result.Stage != video.TaskSubmit && result.Stage != video.TaskPoll && result.Stage != video.TaskFetch) {
		return videoDLQDispatchPayload{}, ErrVideoOutboxProjection
	}
	return result, nil
}

// 格式正确的S/R/P等事件也必须有原事务留下的依据。这里只读稳定资金事实，不做交付可用性判断或资金补写。
func validateVideoOutboxGroundTx(tx *gorm.DB, kind string, task *repository.VideoTaskRecord, request *model.VideoBillingRequest, link *model.AIRequestWalletLink, hold *billingmodel.WalletHold) error {
	var freeze billingmodel.WalletTransaction
	if tx.First(&freeze, link.HoldTransactionID).Error != nil || freeze.UserID != task.UserID || freeze.WalletID != hold.WalletID || freeze.Type != "freeze" || freeze.Direction != "out" || !freeze.Amount.Equal(hold.HoldAmount) {
		return ErrVideoOutboxProjection
	}
	if kind == "video_settlement_pending" || kind == "video_compensation_required" {
		var jobs []model.VideoCompensationTask
		if err := tx.Where("task_type='video_reconcile' AND aggregate_id=?", task.RequestID).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) != 1 || jobs[0].TaskType != "video_reconcile" || jobs[0].AggregateID != task.RequestID || jobs[0].TaskKey != "video:"+task.RequestID || jobs[0].VersionNo == 0 || (kind == "video_settlement_pending" && !videoCompensationNeedsPending(&jobs[0])) {
			return ErrVideoOutboxProjection
		}
		return nil
	}
	settled := kind == "video_billing_settled" || kind == "video_delivery_available"
	released := kind == "video_billing_released" || kind == "video_delivery_rejected"
	if !settled && !released {
		return nil
	}
	if request.SettledAmount == nil || hold.SettledAmount == nil || link.SettledAmount == nil || !request.SettledAmount.Equal(*hold.SettledAmount) || !request.SettledAmount.Equal(*link.SettledAmount) || link.ReleaseTransactionID == nil {
		return ErrVideoOutboxProjection
	}
	var unfreeze billingmodel.WalletTransaction
	if tx.First(&unfreeze, *link.ReleaseTransactionID).Error != nil || unfreeze.ID <= freeze.ID || unfreeze.UserID != task.UserID || unfreeze.WalletID != hold.WalletID || unfreeze.Type != "unfreeze" || unfreeze.Direction != "in" || !unfreeze.Amount.Equal(hold.HoldAmount) {
		return ErrVideoOutboxProjection
	}
	if released {
		if request.BillingStatus != model.AIBillingReleased || hold.Status != billingmodel.HoldStatusReleased || !request.SettledAmount.IsZero() || link.SettleTransactionID != nil || request.DeliveryStatus != model.AIDeliveryRejected {
			return ErrVideoOutboxProjection
		}
		return nil
	}
	if request.BillingStatus != model.AIBillingSettled || hold.Status != billingmodel.HoldStatusSettled || !request.SettledAmount.IsPositive() || task.Status != model.AIImageTaskSucceeded || link.SettleTransactionID == nil {
		return ErrVideoOutboxProjection
	}
	var consume billingmodel.WalletTransaction
	if tx.First(&consume, *link.SettleTransactionID).Error != nil || consume.ID <= unfreeze.ID || consume.UserID != task.UserID || consume.WalletID != hold.WalletID || consume.Type != "consume" || consume.Direction != "out" || !consume.Amount.Equal(*request.SettledAmount) {
		return ErrVideoOutboxProjection
	}
	if kind == "video_delivery_available" && request.DeliveryStatus != model.AIDeliveryAvailable && request.DeliveryStatus != model.AIDeliveryExpired {
		return ErrVideoOutboxProjection
	}
	return nil
}

// Task是细粒度执行轴，Request保留旧协议的粗粒度投影；不能把两者直接作字符串等值判断。
func videoOutboxExecutionMatches(task *repository.VideoTaskRecord) bool {
	switch task.Status {
	case model.AIImageTaskReserved:
		return task.RequestExecutionStatus == model.AIExecutionPending
	case model.AIImageTaskQueued, model.AIImageTaskSubmitting, model.AIImageTaskSubmitted, model.AIImageTaskProcessing,
		model.AIImageTaskFetching, model.AIImageTaskStoring, model.AIImageTaskModerating, model.AIImageTaskLabeling,
		model.AIImageTaskSucceeded, model.AIImageTaskFailed, model.AIImageTaskCancelled, model.AIImageTaskExpired, model.AIImageTaskPendingReconcile:
		return task.RequestExecutionStatus == repository.VideoRequestExecutionStatus(task.Status)
	default:
		return false
	}
}

// 令牌可略晚于墙钟，但到期、已确认、身份变化或载荷变化一律拒绝；不回显数据库错误或正文。
func sameVideoOutboxClaim(current, claimed model.AIOutboxEvent) bool {
	return current.ID == claimed.ID && current.EventID == claimed.EventID && current.AggregateType == "video_request" && current.AggregateID == claimed.AggregateID && current.EventType == claimed.EventType && current.Status == model.AIOutboxPublishing && current.LockedAt != nil && claimed.LockedAt != nil && current.LockedAt.Equal(*claimed.LockedAt) && current.LockedAt.Add(2*time.Minute).After(time.Now().UTC()) && current.RetryCount == claimed.RetryCount && validVideoOutboxTransportState(current) && bytes.Equal(current.PayloadJSON, claimed.PayloadJSON)
}

// 普通事件金额来自原Hold或已结算请求；调账必须匹配原Usage及独立资金动作，不接受调用者金额。
func validateVideoOutboxPayloadTx(tx *gorm.DB, event model.AIOutboxEvent, task *repository.VideoTaskRecord, request *model.VideoBillingRequest, held decimal.Decimal) error {
	return validateVideoOutboxPayload(tx, event, task, request, held, false)
}

func validateVideoOutboxPayloadSnapshotTx(tx *gorm.DB, event model.AIOutboxEvent, task *repository.VideoTaskRecord, request *model.VideoBillingRequest, held decimal.Decimal) error {
	return validateVideoOutboxPayload(tx, event, task, request, held, true)
}

func validateVideoOutboxPayload(tx *gorm.DB, event model.AIOutboxEvent, task *repository.VideoTaskRecord, request *model.VideoBillingRequest, held decimal.Decimal, readOnly bool) error {
	var fields map[string]json.RawMessage
	if len(event.PayloadJSON) > 4096 || json.Unmarshal(event.PayloadJSON, &fields) != nil {
		return ErrVideoOutboxProjection
	}
	var version int
	if json.Unmarshal(fields["version"], &version) != nil || version != 1 {
		return ErrVideoOutboxProjection
	}
	status := ""
	amount := held
	expectedID := "vg5_" + videoBillingDigest(task.RequestID+":"+event.EventType)
	switch event.EventType {
	case "video_billing_held":
		status = model.AIBillingHeld
	case "video_settlement_pending":
		status = model.AIBillingSettlementPending
	case "video_compensation_required":
		status = "pending"
	case "video_billing_released":
		status = model.AIBillingReleased
	case "video_billing_settled", "video_delivery_available":
		if request.SettledAmount == nil {
			return ErrVideoOutboxProjection
		}
		amount = *request.SettledAmount
		status = model.AIBillingSettled
		if event.EventType == "video_delivery_available" {
			status = model.AIDeliveryAvailable
		}
	case "video_delivery_rejected":
		status = model.AIDeliveryRejected
		amount = decimal.Zero
	case "video_adjustment_recorded":
		var sequence uint32
		adjustmentErr := validateVideoAdjustmentsTx(tx, task)
		if readOnly {
			adjustmentErr = validateVideoAdjustmentsSnapshotTx(tx, task)
		}
		if len(fields) != 7 || json.Unmarshal(fields["sequence_no"], &sequence) != nil || sequence == 0 || adjustmentErr != nil {
			return ErrVideoOutboxProjection
		}
		var item model.VideoUsageItem
		if err := tx.Where("request_id=? AND record_kind='adjustment' AND sequence_no=?", task.RequestID, sequence).Take(&item).Error; err != nil || item.Amount == nil {
			return ErrVideoOutboxProjection
		}
		expectedID = videoAdjustmentEventID(task.RequestID, sequence)
		payload, err := videoAdjustmentPayload(task, &item)
		if err != nil || event.EventID != expectedID || !equalVideoFinancialJSON(event.PayloadJSON, payload) {
			return ErrVideoOutboxProjection
		}
		return nil
	default:
		return ErrVideoOutboxProjection
	}
	if len(fields) != 6 || event.EventID != expectedID {
		return ErrVideoOutboxProjection
	}
	for key, want := range map[string]string{"request_id": task.RequestID, "status": status, "amount": amount.StringFixed(8), "currency": "CNY", "operation": *task.Operation} {
		var got string
		if json.Unmarshal(fields[key], &got) != nil || got != want {
			return ErrVideoOutboxProjection
		}
	}
	return nil
}

// 历史输入身份解析不能复用仅允许ready/来源可用的读取器，否则迟到财务事件会在媒体清理后永久卡住。
// 这里不返回对象或使用许可，只核原绑定、规范化摘要及来源Key；执行前仍须复验当前输入状态和租约。
func videoOutboxInputIdentityTx(tx *gorm.DB, task *repository.VideoTaskRecord) (string, error) {
	var bindings []model.AIGatewayTaskInput
	if err := tx.Where("task_id=?", task.ID).Find(&bindings).Error; err != nil {
		return "", err
	}
	if *task.Operation == model.AIVideoOperationTextToVideo {
		if len(bindings) != 0 {
			return "", ErrVideoOutboxProjection
		}
		return "", nil
	}
	if *task.Operation != model.AIVideoOperationImageToVideo || len(bindings) != 1 {
		return "", ErrVideoOutboxProjection
	}
	b := bindings[0]
	if b.UserID != task.UserID || b.ProjectID != task.ProjectID || b.Role != model.AITaskInputReferenceImage || b.Ordinal != 0 || b.InputVersion == 0 || !lowerHex64.MatchString(b.NormalizedSHA256) {
		return "", ErrVideoOutboxProjection
	}
	var input model.AIGatewayInputAsset
	if err := tx.Where("id=?", b.InputAssetID).Take(&input).Error; err != nil {
		return "", err
	}
	if input.UserID != task.UserID || input.ProjectID != task.ProjectID || input.NormalizedSHA256 == nil || *input.NormalizedSHA256 != b.NormalizedSHA256 {
		return "", ErrVideoOutboxProjection
	}
	if input.UploadSessionID != nil && input.SourceGatewayAssetID == nil {
		var session model.AIUploadSession
		if err := tx.Where("id=?", *input.UploadSessionID).Take(&session).Error; err != nil {
			return "", err
		}
		if session.UserID != task.UserID || session.ProjectID != task.ProjectID || !equalOptionalUint64(session.APIKeyID, task.APIKeyID) || session.Status != model.AIUploadSessionCompleted || session.FinalInputAssetID == nil || *session.FinalInputAssetID != input.ID || session.Purpose != model.AIUploadPurposeVideoReferenceImage {
			return "", ErrVideoOutboxProjection
		}
	} else if input.UploadSessionID == nil && input.SourceGatewayAssetID != nil {
		var source struct {
			UserID, ProjectID uint64
			APIKeyID          *uint64
		}
		q := tx.Table("ai_gateway_assets a").Select("a.user_id,a.project_id,t.api_key_id").Joins("JOIN ai_gateway_tasks t ON t.id=a.task_id AND t.request_id=a.request_id AND t.user_id=a.user_id AND t.project_id=a.project_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND (r.api_key_id <=> t.api_key_id)").Where("a.id=? AND a.modality='image' AND t.capability='image.generate' AND t.operation IS NULL AND r.modality='image' AND r.capability='image.generate'", *input.SourceGatewayAssetID).Take(&source)
		if q.Error != nil {
			return "", q.Error
		}
		if source.UserID != task.UserID || source.ProjectID != task.ProjectID || !equalOptionalUint64(source.APIKeyID, task.APIKeyID) {
			return "", ErrVideoOutboxProjection
		}
	} else {
		return "", ErrVideoOutboxProjection
	}
	return input.PublicID, nil
}
