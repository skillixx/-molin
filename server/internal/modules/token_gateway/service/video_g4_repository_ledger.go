package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// VideoReferenceLoader 从受控私有对象读取已规范化参考图，禁止实现方接受用户URL。
type VideoReferenceLoader func(ctx context.Context, asset model.AIGatewayInputAsset) (*videogateway.NormalizedReferenceImage, error)

// 专用任务读取在同一事务内校验已有绑定；不接受客户端资产对象或放宽状态的布尔参数。
type videoTaskReferenceLoader func(context.Context, *gorm.DB, string, repository.VideoOwner) (*videogateway.ControlledInputRef, *videogateway.NormalizedReferenceImage, error)

// VideoRepositoryTaskLedger 把G4 Worker桥接到VID-G3共享Task、Input、Asset、Event、Callback和Payload Repository。
type VideoRepositoryTaskLedger struct {
	db                  *gorm.DB
	owner               repository.VideoOwner
	taskRepo            *repository.VideoTaskRepository
	inputRepo           *repository.VideoInputAssetRepository
	eventRepo           *repository.VideoTaskEventRepository
	callbackRepo        *repository.VideoProviderCallbackEventRepository
	payloadRepo         *repository.VideoTaskPayloadRepository
	assetRepo           *repository.VideoOutputAssetRepository
	protector           *VideoTaskPayloadProtector
	locator             repository.VideoObjectLocationFactory
	referenceLoader     VideoReferenceLoader
	taskReferenceLoader videoTaskReferenceLoader
	now                 func() time.Time
	deferDelivery       bool
	financialFault      func(string) error
	runningAdmission    bool
	runningLimits       videoRunningLimits
	// 仅管理轮询装配的原任务标识；此路径不提供Prompt/参考图，不能用于Submit。
	recoveryTaskID string
}

// NewVideoBillingTaskLedger 在同一G3/G4仓储上启用G5财务交付门禁，不创建第二套任务或资产账本。
func NewVideoBillingTaskLedger(db *gorm.DB, owner repository.VideoOwner, protector *VideoTaskPayloadProtector, locator repository.VideoObjectLocationFactory, loader VideoReferenceLoader) *VideoRepositoryTaskLedger {
	ledger := NewVideoRepositoryTaskLedger(db, owner, protector, locator, loader)
	ledger.deferDelivery = true
	return ledger
}

func NewVideoRepositoryTaskLedger(db *gorm.DB, owner repository.VideoOwner, protector *VideoTaskPayloadProtector, locator repository.VideoObjectLocationFactory, referenceLoader VideoReferenceLoader) *VideoRepositoryTaskLedger {
	return &VideoRepositoryTaskLedger{
		db: db, owner: owner, taskRepo: repository.NewVideoTaskRepository(db),
		inputRepo: repository.NewVideoInputAssetRepository(db), eventRepo: repository.NewVideoTaskEventRepository(db),
		callbackRepo: repository.NewVideoProviderCallbackEventRepository(db),
		payloadRepo:  repository.NewVideoTaskPayloadRepository(db, protector),
		assetRepo:    repository.NewVideoOutputAssetRepository(db, locator),
		protector:    protector, locator: locator, referenceLoader: referenceLoader, now: time.Now,
	}
}

func (l *VideoRepositoryTaskLedger) Load(ctx context.Context, taskID string) (videogateway.GatewayTask, error) {
	if l == nil || l.db == nil || l.protector == nil {
		return videogateway.GatewayTask{}, videogateway.ErrGatewayTaskNotFound
	}
	if l.recoveryTaskID != "" {
		return l.loadRecoveryMetadata(ctx, taskID)
	}
	record, err := l.taskRepo.FindForOwner(ctx, taskID, l.owner)
	if err != nil {
		return videogateway.GatewayTask{}, mapVideoRepositoryError(err)
	}
	// 已被管理归档接管时，普通Worker连新的媒体IO也不能启动；先前IO的写回另由共享CAS拒绝。
	if record.ArchiveTokenHash != nil {
		return videogateway.GatewayTask{}, videogateway.ErrGatewayTaskConflict
	}
	if !l.deferDelivery {
		// 持久化G5身份不能由换构造器降级。显式SELECT *兼容旧库没有command_kind列的情况。
		var identity struct {
			CommandKind *string `gorm:"column:command_kind"`
		}
		if err := l.db.WithContext(ctx).Table("ai_requests").Select("*").Where("request_id=?", record.RequestID).Take(&identity).Error; err != nil {
			return videogateway.GatewayTask{}, err
		}
		// 非NULL命令代表新协议身份；不以Go大小写比较复刻数据库排序规则，避免别名降级。
		if identity.CommandKind != nil {
			return videogateway.GatewayTask{}, ErrVideoReconciliation
		}
	}
	if l.deferDelivery && (record.Status == model.AIImageTaskCreated || record.Status == model.AIImageTaskReserved || record.Status == model.AIImageTaskQueued || record.Status == model.AIImageTaskSubmitting) {
		var attempts int64
		if err := l.db.WithContext(ctx).Model(&model.AIExecutionAttempt{}).Where("request_id=?", record.RequestID).Count(&attempts).Error; err != nil {
			return videogateway.GatewayTask{}, err
		}
		if attempts != 0 {
			return videogateway.GatewayTask{}, ErrVideoReconciliation
		}
	}
	operation := ""
	if record.Operation != nil {
		operation = *record.Operation
	}
	spec, err := parseVideoG4TaskSpec(record.InputJSON)
	if err != nil {
		return videogateway.GatewayTask{}, err
	}
	payload, err := l.payloadRepo.FindForOwner(ctx, taskID, model.AITaskPayloadPrompt, l.owner)
	if err != nil {
		return videogateway.GatewayTask{}, err
	}
	plaintext, err := l.protector.Open(payload)
	if err != nil {
		return videogateway.GatewayTask{}, err
	}
	prompt := string(append([]byte(nil), plaintext...))
	for index := range plaintext {
		plaintext[index] = 0
	}
	task := videogateway.GatewayTask{
		DeferDelivery: l.deferDelivery,
		TaskID:        record.PublicID, RequestID: record.RequestID, Operation: operation, Prompt: prompt,
		Spec: spec, Status: videogateway.TaskStatus(record.Status), Version: record.VersionNo,
		CancelRequestedAt: record.CancelRequestedAt,
	}
	if record.ProviderCode != nil {
		task.ProviderCode = *record.ProviderCode
	}
	if record.ProviderTaskID != nil {
		task.ProviderTaskID = *record.ProviderTaskID
	}
	if record.SubmissionClaimVersion != nil {
		task.SubmissionClaimVersion = *record.SubmissionClaimVersion
	}
	if record.SubmissionIntentID != nil {
		task.PlannedProviderTaskID = *record.SubmissionIntentID
	}
	if (task.Status == videogateway.TaskFetching || task.Status == videogateway.TaskStoring || task.Status == videogateway.TaskModerating || task.Status == videogateway.TaskLabeling) && task.ProviderTaskID != "" {
		task.Content = &videogateway.ControlledContentRef{
			ProviderTaskID: task.ProviderTaskID,
			ContentID:      "content-" + task.ProviderTaskID,
			MediaType:      "video/mp4",
		}
	}
	if operation == model.AIVideoOperationImageToVideo && !videoG4TerminalStatus(record.Status) && l.taskReferenceLoader != nil {
		task.Input, task.Reference, err = l.taskReferenceLoader(ctx, l.db, taskID, l.owner)
		if err != nil {
			return videogateway.GatewayTask{}, err
		}
		if task.Input == nil || task.Reference == nil {
			return videogateway.GatewayTask{}, repository.ErrVideoInputSnapshotDrift
		}
	} else if operation == model.AIVideoOperationImageToVideo {
		var binding *model.AIGatewayTaskInput
		if videoG4NeedsProviderInputValidation(record.Status) {
			binding, err = l.inputRepo.ValidateTaskInputForProvider(ctx, taskID, l.owner, l.now().UTC())
			if err != nil {
				return videogateway.GatewayTask{}, err
			}
			if binding == nil {
				return videogateway.GatewayTask{}, repository.ErrVideoInputSnapshotDrift
			}
		} else {
			var bindings []model.AIGatewayTaskInput
			bindings, err = repository.NewVideoTaskInputRepository(l.db).ListForOwner(ctx, taskID, l.owner)
			if err != nil || len(bindings) != 1 {
				return videogateway.GatewayTask{}, repository.ErrVideoInputSnapshotDrift
			}
			binding = &bindings[0]
		}
		var asset model.AIGatewayInputAsset
		if err := l.db.WithContext(ctx).
			Where("id=? AND user_id=? AND project_id=?", binding.InputAssetID, l.owner.UserID, l.owner.ProjectID).
			First(&asset).Error; err != nil {
			return videogateway.GatewayTask{}, repository.ErrVideoInputSnapshotDrift
		}
		task.Input = &videogateway.ControlledInputRef{AssetID: asset.PublicID, SHA256: binding.NormalizedSHA256, Version: binding.InputVersion}
		if !videoG4TerminalStatus(record.Status) {
			// 普通读取器没有TaskInput授权能力；延迟删除输入必须等待专用执行读取装配，不能静默放宽为ready。
			if asset.LifecycleState == model.AIInputAssetPendingDelete {
				return videogateway.GatewayTask{}, repository.ErrVideoInputSnapshotDrift
			}
			if l.referenceLoader == nil {
				return videogateway.GatewayTask{}, repository.ErrVideoInputSnapshotDrift
			}
			task.Reference, err = l.referenceLoader(ctx, asset)
			if err != nil {
				return videogateway.GatewayTask{}, err
			}
		}
	}
	assetPublicID := videoG4AssetPublicID(taskID)
	if asset, findErr := l.assetRepo.FindOwnedForInternal(ctx, assetPublicID, l.owner); findErr == nil {
		task.Asset = mapVideoG4Asset(asset)
		var children []model.AIImageAsset
		if err := l.db.WithContext(ctx).Where("parent_asset_id=? AND user_id=? AND project_id=? AND modality='video'", asset.ID, l.owner.UserID, l.owner.ProjectID).
			Order("id ASC").Find(&children).Error; err == nil {
			for index := range children {
				child := mapVideoG4Asset(&children[index])
				if child != nil {
					child.ParentAssetID = asset.PublicID
					task.Asset.Children = append(task.Asset.Children, *child)
				}
			}
		}
	}
	events, err := l.eventRepo.ListForOwner(ctx, taskID, l.owner)
	if err == nil {
		for _, event := range events {
			from, to := "", ""
			if event.FromStatus != nil {
				from = *event.FromStatus
			}
			if event.ToStatus != nil {
				to = *event.ToStatus
			}
			task.Events = append(task.Events, videogateway.GatewayTaskEvent{EventID: event.EventID, FromStatus: videogateway.TaskStatus(from), ToStatus: videogateway.TaskStatus(to), Source: event.Source, CreatedAt: event.CreatedAt})
		}
	}
	if l.deferDelivery && task.Asset != nil && task.Asset.Lifecycle == videogateway.AssetAvailable {
		reconciler := NewVideoReconciliationService(l.db)
		reconciler.now = l.now
		report, err := reconciler.Reconcile(ctx, taskID, l.owner)
		if err != nil {
			return videogateway.GatewayTask{}, err
		}
		if !report.Passed {
			return videogateway.GatewayTask{}, ErrVideoReconciliation
		}
	}
	return task, nil
}

func (l *VideoRepositoryTaskLedger) Advance(ctx context.Context, taskID string, expectedVersion uint64, to videogateway.TaskStatus, source, reason string, mutate videogateway.TaskMutation) (videogateway.GatewayTask, error) {
	if l.deferDelivery && to == videogateway.TaskPendingReconcile && mutate == nil {
		return l.advanceUncertainMetadata(ctx, taskID, expectedVersion, source)
	}
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, advanceErr := l.withDB(tx).advanceOnce(ctx, taskID, expectedVersion, to, source, reason, mutate)
		return advanceErr
	})
	if err != nil {
		return videogateway.GatewayTask{}, mapVideoRepositoryError(err)
	}
	return l.Load(ctx, taskID)
}

func (l *VideoRepositoryTaskLedger) advanceOnce(ctx context.Context, taskID string, expectedVersion uint64, to videogateway.TaskStatus, source, reason string, mutate videogateway.TaskMutation) (videogateway.GatewayTask, error) {
	current, err := l.Load(ctx, taskID)
	if err != nil {
		return videogateway.GatewayTask{}, err
	}
	if current.Version != expectedVersion {
		return videogateway.GatewayTask{}, videogateway.ErrGatewayTaskConflict
	}
	next := current
	if current.Asset != nil {
		assetCopy := *current.Asset
		assetCopy.Children = append([]videogateway.GatewayAsset(nil), current.Asset.Children...)
		next.Asset = &assetCopy
	}
	if mutate != nil {
		if err := mutate(&next); err != nil {
			return videogateway.GatewayTask{}, err
		}
	}
	now := l.now().UTC()
	if l.deferDelivery && to == videogateway.TaskSucceeded {
		// 成功前核对Provider计量与已探测媒体，冲突时仍保存完整安全资产，但不先进入不可回退的成功态。
		record, e := l.taskRepo.LockForOwnerTx(l.db, taskID, l.owner)
		if e != nil {
			return videogateway.GatewayTask{}, e
		}
		var quote model.AIGatewayQuote
		if e := l.db.First(&quote, record.QuoteID).Error; e != nil {
			return videogateway.GatewayTask{}, e
		}
		cost, e := loadVideoConfirmedCostTx(l.db, record, &quote)
		if e != nil || next.Asset == nil || !cost.Quantity.Equal(decimal.NewFromInt(int64(next.Asset.DurationMillis)).Div(decimal.NewFromInt(1000))) {
			to = videogateway.TaskPendingReconcile
		}
	}
	eventID := fmt.Sprintf("vid_g4_%s_%s_%d", taskID, to, expectedVersion)
	if to == videogateway.TaskSubmitted && next.ProviderTaskID != "" {
		_, err = l.taskRepo.BindProviderTask(ctx, repository.VideoProviderBinding{
			TaskPublicID: taskID, Owner: l.owner, ExpectedVersion: expectedVersion,
			ProviderCode: next.ProviderCode, ProviderTaskID: next.ProviderTaskID, EventID: eventID, Now: now,
		})
	} else {
		failureOrigin := ""
		if l.deferDelivery && to == videogateway.TaskFailed {
			failureOrigin = "other_failed"
			switch reason {
			case "provider_failed":
				failureOrigin = "provider_failed"
			case "moderation_failed":
				failureOrigin = "moderation_unknown"
				if next.Asset != nil && next.Asset.ModerationStatus == videogateway.AssetModerationRejected {
					failureOrigin = "moderation_rejected"
				}
			case "label_failed", "label_unknown":
				failureOrigin = reason
			case "derived_failed", "derived_source_failed":
				failureOrigin = "derived_failed"
			}
		}
		_, err = l.taskRepo.TransitionExecution(ctx, repository.VideoStateTransition{
			TaskPublicID: taskID, Owner: l.owner, ExpectedVersion: expectedVersion,
			ToStatus: string(to), Progress: videoG4Progress(to), EventID: eventID,
			Source: normalizeVideoG4EventSource(source), SafeDetailJSON: json.RawMessage("{\"reason\":\"state_advanced\"}"), Now: now, FailureOrigin: failureOrigin,
		})
	}
	if err != nil {
		return videogateway.GatewayTask{}, mapVideoRepositoryError(err)
	}
	if err := l.persistAssetMutation(ctx, current, next, now); err != nil {
		return videogateway.GatewayTask{}, err
	}
	if l.deferDelivery && (to == videogateway.TaskPendingReconcile || to == videogateway.TaskFailed) {
		record, err := l.taskRepo.LockForOwnerTx(l.db, taskID, l.owner)
		if err != nil {
			return videogateway.GatewayTask{}, err
		}
		billing := &VideoBillingService{db: l.db, now: l.now, fault: l.financialFault}
		if _, err := billing.reconcileVideoExecutionTx(ctx, l.db, record, l.owner); err != nil {
			return videogateway.GatewayTask{}, err
		}
	}
	return l.Load(ctx, taskID)
}

func (l *VideoRepositoryTaskLedger) withDB(db *gorm.DB) *VideoRepositoryTaskLedger {
	scoped := NewVideoRepositoryTaskLedger(db, l.owner, l.protector, l.locator, l.referenceLoader)
	scoped.now = l.now
	scoped.deferDelivery = l.deferDelivery
	scoped.financialFault = l.financialFault
	scoped.runningAdmission = l.runningAdmission
	scoped.runningLimits = l.runningLimits
	scoped.taskReferenceLoader = l.taskReferenceLoader
	scoped.recoveryTaskID = l.recoveryTaskID
	return scoped
}

func (l *VideoRepositoryTaskLedger) RequestCancellation(ctx context.Context, taskID string) (videogateway.GatewayTask, error) {
	if _, err := l.Load(ctx, taskID); err != nil {
		return videogateway.GatewayTask{}, err
	}
	err := retryVideoBillingTransaction(ctx, func() error {
		_, err := l.taskRepo.RequestCancellation(ctx, taskID, l.owner, l.now().UTC())
		return err
	})
	if err != nil {
		return videogateway.GatewayTask{}, mapVideoRepositoryError(err)
	}
	return l.Load(ctx, taskID)
}

func (l *VideoRepositoryTaskLedger) ReleaseLeaseOnce(ctx context.Context, taskID string) (videogateway.GatewayTask, error) {
	if l.deferDelivery {
		record, err := l.taskRepo.FindForOwner(ctx, taskID, l.owner)
		if err != nil {
			return videogateway.GatewayTask{}, err
		}
		// 执行终态与财务终态相互独立，held/pending期间仍保护输入，不把等待结算当作执行错误。
		if record.BillingStatus != model.AIBillingSettled && record.BillingStatus != model.AIBillingReleased && record.BillingStatus != model.AIBillingAdjusted {
			return l.Load(ctx, taskID)
		}
	}
	if err := l.inputRepo.ReleaseTaskLeases(ctx, taskID, l.owner, l.now().UTC()); err != nil {
		return videogateway.GatewayTask{}, err
	}
	return l.Load(ctx, taskID)
}

func (l *VideoRepositoryTaskLedger) RecordCallback(ctx context.Context, taskID string, callback videogateway.VerifiedCallback) (bool, error) {
	toStatus := mapVideoG4ProviderStatus(callback.Status)
	// 外部event_id只在Provider任务内唯一；内部全局事件键使用完整三元组摘要，长度不随外部ID增长。
	// 新版本域与旧裸ID域分离，避免历史事件号恰好是摘要文本时碰撞，旧记录保持不变。
	identity := fmt.Sprintf("%d:%s%d:%s%d:%s", len(callback.ProviderCode), callback.ProviderCode, len(callback.ProviderTaskID), callback.ProviderTaskID, len(callback.ExternalEventID), callback.ExternalEventID)
	eventDigest := videoPayloadSHA256([]byte(identity))
	var replayed bool
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 回调只推进Provider执行段。归档/审核后的失败属于Worker，待对账终结属于对账流程；
		// 不能等Gateway读取旧快照后再保护，因为原Callback Repository已经可能先写入数据库。
		current, loadErr := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, l.owner)
		if loadErr != nil && !errors.Is(loadErr, repository.ErrVideoTaskNotFound) {
			return loadErr
		}
		if loadErr == nil && current.Status == model.AIImageTaskSubmitted && callback.Status == videogateway.ProviderTaskSucceeded && current.ProviderCode != nil && current.ProviderTaskID != nil && *current.ProviderCode == callback.ProviderCode && *current.ProviderTaskID == callback.ProviderTaskID {
			// Provider可以只发送最终成功。沿原单步矩阵在同一事务补齐processing，不能直接跳过节点；
			// 已存在的事件（包括历史ignored）仅重放，不能借重试重新解释或改写原处理事实。
			// 共享账本可能位于已有RR快照；必须当前读事件实体，不能用旧COUNT漏掉新提交的ignored。
			var existing model.AIGatewayProviderCallbackEvent
			existingErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("provider_code=? AND provider_task_id=? AND external_event_id=?", callback.ProviderCode, callback.ProviderTaskID, callback.ExternalEventID).Take(&existing).Error
			if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
				return existingErr
			}
			if errors.Is(existingErr, gorm.ErrRecordNotFound) {
				updated, err := repository.NewVideoTaskRepository(tx).TransitionExecution(ctx, repository.VideoStateTransition{TaskPublicID: current.PublicID, Owner: l.owner, ExpectedVersion: current.VersionNo, ToStatus: model.AIImageTaskProcessing, Progress: current.Progress, EventID: "vid_g4_cb_v2_pre_" + eventDigest, Source: "provider_callback", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), Now: l.now().UTC()})
				if err != nil {
					return err
				}
				current = updated
			}
		}
		if loadErr == nil && current.Status != model.AIImageTaskSubmitted && current.Status != model.AIImageTaskProcessing {
			// 同态不是允许迁移，原Repository仍追加该回调为ignored并保留三元去重事实。
			toStatus = videogateway.TaskStatus(current.Status)
		}
		outcome, err := repository.NewVideoProviderCallbackEventRepository(tx).RecordAndApply(ctx, repository.VideoProviderCallbackCommand{
			ProviderCode: callback.ProviderCode, ProviderTaskID: callback.ProviderTaskID,
			ExternalEventID: callback.ExternalEventID, BodySHA256: callback.BodySHA256,
			ExpectedTaskPublicID: taskID, ExpectedOwner: l.owner,
			SignatureStatus: model.AIProviderCallbackSignatureValid, ToStatus: string(toStatus),
			EventID:        "vid_g4_cb_v2_" + eventDigest,
			SafeResultJSON: json.RawMessage("{\"result\":\"applied\"}"), ReceivedAt: l.now().UTC(),
		})
		if err != nil {
			return err
		}
		replayed = outcome.Replayed
		// Applied描述原事件的历史结果；重放只能恢复ACK，不能按当前任务再次安排补偿或人工核对。
		if l.deferDelivery && outcome.Applied && !outcome.Replayed {
			record, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, l.owner)
			if err != nil {
				return err
			}
			billing := &VideoBillingService{db: tx, now: l.now, fault: l.financialFault}
			if _, err := billing.reconcileVideoExecutionTx(ctx, tx, record, l.owner); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, repository.ErrVideoCallbackBodyConflict) {
		return false, videogateway.ErrCallbackBodyConflict
	}
	if err != nil {
		return false, err
	}
	return replayed, nil
}

// PrepareMediaDelete 在删除对象正文前原子推进全部父子资产到deleting。
// 任何资产处于法律保全或争议中都会让事务失败，从而保证对象存储尚未被触碰。
func (l *VideoRepositoryTaskLedger) PrepareMediaDelete(ctx context.Context, taskID string) (videogateway.GatewayTask, error) {
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scoped := l.withDB(tx)
		assets, loadErr := scoped.loadDeleteAssets(ctx, taskID)
		if loadErr != nil {
			return loadErr
		}
		now := scoped.now().UTC()
		for index := range assets {
			asset := &assets[index]
			if asset.MediaDeletedAt != nil || asset.LifecycleState == model.AIImageAssetDeleted {
				continue
			}
			if asset.LegalHold || asset.DisputeStatus == model.AIImageDisputeOpen {
				return repository.ErrVideoAssetTransition
			}
			for asset.LifecycleState != model.AIImageAssetDeleting {
				var next string
				switch asset.LifecycleState {
				case model.AIImageAssetAvailable:
					next = model.AIImageAssetExpiring
				case model.AIImageAssetTemporary, model.AIImageAssetQuarantined, model.AIImageAssetExpiring, model.AIImageAssetDeleteFailed:
					next = model.AIImageAssetDeleting
				default:
					return repository.ErrVideoAssetTransition
				}
				updated, transitionErr := scoped.assetRepo.TransitionLifecycle(ctx, asset.PublicID, scoped.owner, asset.VersionNo, next, now)
				if transitionErr != nil {
					return transitionErr
				}
				*asset = *updated
			}
		}
		return nil
	})
	if err != nil {
		return videogateway.GatewayTask{}, mapVideoRepositoryError(err)
	}
	return l.Load(ctx, taskID)
}

// CompleteMediaDelete 在对象存储操作结束后原子记录deleted或delete_failed。
// delete_failed可由下一次PrepareMediaDelete恢复，已删除资产保持幂等。
func (l *VideoRepositoryTaskLedger) CompleteMediaDelete(ctx context.Context, taskID string, succeeded bool) (videogateway.GatewayTask, error) {
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scoped := l.withDB(tx)
		assets, loadErr := scoped.loadDeleteAssets(ctx, taskID)
		if loadErr != nil {
			return loadErr
		}
		target := model.AIImageAssetDeleteFailed
		if succeeded {
			target = model.AIImageAssetDeleted
		}
		now := scoped.now().UTC()
		for index := range assets {
			asset := &assets[index]
			if succeeded && asset.MediaDeletedAt != nil && asset.LifecycleState == model.AIImageAssetDeleted {
				continue
			}
			if asset.LifecycleState != model.AIImageAssetDeleting {
				return repository.ErrVideoAssetTransition
			}
			if _, transitionErr := scoped.assetRepo.TransitionLifecycle(ctx, asset.PublicID, scoped.owner, asset.VersionNo, target, now); transitionErr != nil {
				return transitionErr
			}
		}
		return nil
	})
	if err != nil {
		return videogateway.GatewayTask{}, mapVideoRepositoryError(err)
	}
	return l.Load(ctx, taskID)
}

func (l *VideoRepositoryTaskLedger) loadDeleteAssets(ctx context.Context, taskID string) ([]model.AIImageAsset, error) {
	asset, err := l.assetRepo.FindOwnedForInternal(ctx, videoG4AssetPublicID(taskID), l.owner)
	if err != nil {
		return nil, err
	}
	assets := []model.AIImageAsset{*asset}
	var children []model.AIImageAsset
	if err := l.db.WithContext(ctx).Where("parent_asset_id=? AND user_id=? AND project_id=? AND modality='video'", asset.ID, l.owner.UserID, l.owner.ProjectID).Order("id ASC").Find(&children).Error; err != nil {
		return nil, err
	}
	return append(assets, children...), nil
}

func (l *VideoRepositoryTaskLedger) persistAssetMutation(ctx context.Context, current, next videogateway.GatewayTask, now time.Time) error {
	if current.Asset == nil && next.Asset != nil {
		hasAudio := next.Asset.HasAudio
		duration := decimal.NewFromInt(int64(next.Asset.DurationMillis)).Div(decimal.NewFromInt(1000))
		_, err := l.assetRepo.Create(ctx, repository.VideoOutputAssetDraft{
			PublicID: next.Asset.AssetID, TaskPublicID: next.TaskID, Owner: l.owner,
			AssetRole: model.AIImageAssetContent, IsBillableOutput: true, MIMEType: next.Asset.MIMEType,
			SizeBytes: next.Asset.SizeBytes, SHA256: next.Asset.SHA256, Width: next.Asset.Width, Height: next.Asset.Height,
			DurationSeconds: duration, FrameRate: decimal.NewFromInt(int64(next.Asset.FrameRate)),
			Container: "mp4", VideoCodec: next.Asset.VideoCodec, AudioCodec: next.Asset.AudioCodec, HasAudio: &hasAudio,
			Source: "fake_object_store", RetentionPolicyID: "video-g4-fake", ExpiresAt: now.Add(24 * time.Hour), Now: now,
		})
		return err
	}
	if current.Asset == nil || next.Asset == nil {
		return nil
	}
	asset, err := l.assetRepo.FindOwnedForInternal(ctx, next.Asset.AssetID, l.owner)
	if err != nil {
		return err
	}
	if current.Asset.ModerationStatus != next.Asset.ModerationStatus && next.Asset.ModerationStatus != videogateway.AssetModerationPending {
		asset, err = l.assetRepo.ApplyModerationResult(ctx, asset.PublicID, l.owner, asset.VersionNo, string(next.Asset.ModerationStatus), "fake-moderation-v1", now)
		if err != nil {
			return err
		}
	}
	if current.Asset.ExplicitLabelStatus != next.Asset.ExplicitLabelStatus || current.Asset.ImplicitLabelStatus != next.Asset.ImplicitLabelStatus {
		if next.Asset.ExplicitLabelStatus != videogateway.LabelPending && next.Asset.ImplicitLabelStatus != videogateway.LabelPending {
			asset, err = l.assetRepo.ApplyLabelResult(ctx, asset.PublicID, l.owner, asset.VersionNo, string(next.Asset.ExplicitLabelStatus), string(next.Asset.ImplicitLabelStatus), next.Asset.LabelVersion, now)
		}
		if err != nil {
			return err
		}
	}
	if current.Asset.Object.Ref != next.Asset.Object.Ref {
		asset, err = l.assetRepo.MoveObjectLocation(ctx, asset.PublicID, l.owner, asset.VersionNo,
			repository.VideoObjectLocation{Bucket: current.Asset.Object.Ref.Bucket, ObjectKey: current.Asset.Object.Ref.ObjectKey},
			repository.VideoObjectLocation{Bucket: next.Asset.Object.Ref.Bucket, ObjectKey: next.Asset.Object.Ref.ObjectKey}, now)
		if err != nil {
			return err
		}
	}
	if len(current.Asset.Children) == 0 && len(next.Asset.Children) > 0 {
		for index := range next.Asset.Children {
			if err := l.persistDerivedAsset(ctx, next.TaskID, asset.PublicID, next.Asset.Children[index], now); err != nil {
				return err
			}
		}
	}
	if current.Asset.Lifecycle != next.Asset.Lifecycle && (next.Asset.Lifecycle == videogateway.AssetAvailable || next.Asset.Lifecycle == videogateway.AssetQuarantined) {
		_, err = l.assetRepo.TransitionLifecycle(ctx, asset.PublicID, l.owner, asset.VersionNo, string(next.Asset.Lifecycle), now)
		if err != nil {
			return err
		}
	}
	return err
}

func (l *VideoRepositoryTaskLedger) persistDerivedAsset(ctx context.Context, taskID, parentPublicID string, child videogateway.GatewayAsset, now time.Time) error {
	draft := repository.VideoOutputAssetDraft{
		PublicID: child.AssetID, TaskPublicID: taskID, ParentPublicID: parentPublicID, Owner: l.owner,
		AssetRole: child.Role, MIMEType: child.MIMEType, SizeBytes: child.SizeBytes, SHA256: child.SHA256,
		Width: child.Width, Height: child.Height, Source: "derived", RetentionPolicyID: "video-g4-fake",
		ExpiresAt: now.Add(24 * time.Hour), Now: now,
	}
	if child.MIMEType == "video/mp4" {
		hasAudio := child.HasAudio
		draft.DurationSeconds = decimal.NewFromInt(int64(child.DurationMillis)).Div(decimal.NewFromInt(1000))
		draft.FrameRate = decimal.NewFromInt(int64(child.FrameRate))
		draft.Container, draft.VideoCodec, draft.AudioCodec, draft.HasAudio = "mp4", child.VideoCodec, child.AudioCodec, &hasAudio
	}
	created, err := l.assetRepo.Create(ctx, draft)
	if err != nil {
		return err
	}
	created, err = l.assetRepo.ApplyModerationResult(ctx, created.PublicID, l.owner, created.VersionNo, string(child.ModerationStatus), "fake-moderation-v1", now)
	if err != nil {
		return err
	}
	created, err = l.assetRepo.ApplyLabelResult(ctx, created.PublicID, l.owner, created.VersionNo, string(child.ExplicitLabelStatus), string(child.ImplicitLabelStatus), child.LabelVersion, now)
	if err != nil {
		return err
	}
	if l.deferDelivery {
		return nil
	}
	_, err = l.assetRepo.TransitionLifecycle(ctx, created.PublicID, l.owner, created.VersionNo, model.AIImageAssetAvailable, now)
	return err
}

func parseVideoG4TaskSpec(raw json.RawMessage) (videogateway.VideoSpec, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 6 {
		return videogateway.VideoSpec{}, videogateway.ErrVideoRequestInvalid
	}
	for _, key := range []string{"operation", "resolution", "duration_seconds", "aspect_ratio", "frame_rate", "audio"} {
		if _, ok := fields[key]; !ok {
			return videogateway.VideoSpec{}, videogateway.ErrVideoRequestInvalid
		}
	}
	var operation, resolution, aspectRatio string
	var duration, frameRate uint32
	var audio bool
	if json.Unmarshal(fields["operation"], &operation) != nil ||
		json.Unmarshal(fields["resolution"], &resolution) != nil ||
		json.Unmarshal(fields["duration_seconds"], &duration) != nil ||
		json.Unmarshal(fields["aspect_ratio"], &aspectRatio) != nil ||
		json.Unmarshal(fields["frame_rate"], &frameRate) != nil ||
		json.Unmarshal(fields["audio"], &audio) != nil ||
		(operation != model.AIVideoOperationTextToVideo && operation != model.AIVideoOperationImageToVideo) ||
		aspectRatio == "" || strings.Contains(resolution, "://") {
		return videogateway.VideoSpec{}, videogateway.ErrVideoRequestInvalid
	}
	parts := strings.Split(resolution, "x")
	if len(parts) != 2 {
		return videogateway.VideoSpec{}, videogateway.ErrVideoRequestInvalid
	}
	width, widthErr := strconv.ParseUint(parts[0], 10, 32)
	height, heightErr := strconv.ParseUint(parts[1], 10, 32)
	if widthErr != nil || heightErr != nil {
		return videogateway.VideoSpec{}, videogateway.ErrVideoRequestInvalid
	}
	if width == 0 || height == 0 || duration == 0 || frameRate == 0 {
		return videogateway.VideoSpec{}, videogateway.ErrVideoRequestInvalid
	}
	return videogateway.VideoSpec{Width: uint32(width), Height: uint32(height), DurationSeconds: duration, FrameRate: frameRate, Audio: audio}, nil
}

func mapVideoG4Asset(asset *model.AIImageAsset) *videogateway.GatewayAsset {
	if asset == nil || asset.Bucket == nil || asset.ObjectKey == nil || asset.MIMEType == nil || asset.SizeBytes == nil || asset.SHA256 == nil {
		return nil
	}
	result := &videogateway.GatewayAsset{
		AssetID: asset.PublicID, Role: asset.AssetRole,
		Object:   videogateway.StoredVideoObject{Ref: videogateway.VideoObjectRef{Bucket: *asset.Bucket, ObjectKey: *asset.ObjectKey}, SizeBytes: *asset.SizeBytes, SHA256: *asset.SHA256, CreatedAt: asset.CreatedAt},
		MIMEType: *asset.MIMEType, SizeBytes: *asset.SizeBytes, SHA256: *asset.SHA256,
		ModerationPassed:    asset.ModerationStatus == model.AIModerationPassed,
		ModerationStatus:    videogateway.AssetModerationStatus(asset.ModerationStatus),
		ExplicitLabelStatus: videogateway.LabelStatus(asset.ExplicitLabelStatus),
		ImplicitLabelStatus: videogateway.LabelStatus(asset.ImplicitLabelStatus),
		Lifecycle:           videogateway.AssetLifecycle(asset.LifecycleState), MediaDeleted: asset.MediaDeletedAt != nil,
	}
	if asset.Width != nil {
		result.Width = *asset.Width
	}
	if asset.Height != nil {
		result.Height = *asset.Height
	}
	if asset.FrameRate != nil {
		frameRate, _ := asset.FrameRate.Float64()
		result.FrameRate = uint32(frameRate)
	}
	if asset.DurationSeconds != nil {
		duration, _ := asset.DurationSeconds.Mul(decimal.NewFromInt(1000)).Float64()
		result.DurationMillis = uint64(duration)
	}
	if asset.VideoCodec != nil {
		result.VideoCodec = *asset.VideoCodec
	}
	if asset.AudioCodec != nil {
		result.AudioCodec = *asset.AudioCodec
	}
	if asset.HasAudio != nil {
		result.HasAudio = *asset.HasAudio
	}
	if asset.ExplicitLabelVersion != nil {
		result.LabelVersion = *asset.ExplicitLabelVersion
	}
	return result
}

func videoG4AssetPublicID(taskID string) string { return "vasset-" + taskID }

func videoG4Progress(status videogateway.TaskStatus) uint8 {
	values := map[videogateway.TaskStatus]uint8{
		videogateway.TaskReserved: 5, videogateway.TaskQueued: 10, videogateway.TaskSubmitting: 15,
		videogateway.TaskSubmitted: 20, videogateway.TaskProcessing: 50, videogateway.TaskFetching: 65,
		videogateway.TaskStoring: 75, videogateway.TaskModerating: 85, videogateway.TaskLabeling: 95,
		videogateway.TaskSucceeded: 100, videogateway.TaskFailed: 100, videogateway.TaskCancelled: 100,
		videogateway.TaskPendingReconcile: 95,
	}
	return values[status]
}

func normalizeVideoG4EventSource(source string) string {
	switch source {
	case "worker", "provider_callback", "reconciler", "system", "api":
		return source
	default:
		return "worker"
	}
}

func mapVideoG4ProviderStatus(status videogateway.ProviderTaskStatus) videogateway.TaskStatus {
	switch status {
	case videogateway.ProviderTaskProcessing:
		return videogateway.TaskProcessing
	case videogateway.ProviderTaskSucceeded:
		return videogateway.TaskFetching
	case videogateway.ProviderTaskFailed:
		return videogateway.TaskFailed
	case videogateway.ProviderTaskCancelled:
		return videogateway.TaskCancelled
	default:
		return videogateway.TaskPendingReconcile
	}
}

func videoG4NeedsProviderInputValidation(status string) bool {
	return status == model.AIImageTaskCreated || status == model.AIImageTaskReserved ||
		status == model.AIImageTaskQueued || status == model.AIImageTaskSubmitting
}

func videoG4TerminalStatus(status string) bool {
	return status == model.AIImageTaskSucceeded || status == model.AIImageTaskFailed ||
		status == model.AIImageTaskCancelled || status == model.AIImageTaskExpired
}

func mapVideoRepositoryError(err error) error {
	switch {
	case errors.Is(err, repository.ErrVideoTaskNotFound):
		return videogateway.ErrGatewayTaskNotFound
	case errors.Is(err, repository.ErrVideoTaskConflict):
		return videogateway.ErrGatewayTaskConflict
	case errors.Is(err, repository.ErrVideoTaskTransition), errors.Is(err, repository.ErrVideoAssetTransition):
		return videogateway.ErrGatewayTaskTransition
	case errors.Is(err, repository.ErrVideoAssetConflict):
		return videogateway.ErrGatewayTaskConflict
	default:
		return err
	}
}
