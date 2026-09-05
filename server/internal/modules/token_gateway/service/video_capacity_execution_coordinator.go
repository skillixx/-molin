package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// VideoCapacityExecutionCoordinator封装Redis running预留、MySQL确定提交和Redis确认，调用方不能拆开顺序。
type VideoCapacityExecutionCoordinator struct {
	ledger   *VideoRepositoryTaskLedger
	recovery *repository.VideoCapacityRecoveryRepository
	store    *RedisVideoCapacityStore
	nonceKey *VideoCapacityNonceKey
	fault    func(string) error
	permitMu sync.Mutex
	permits  map[string]*videoProviderSendPermit
}

func NewVideoCapacityExecutionCoordinator(ledger *VideoRepositoryTaskLedger, recovery *repository.VideoCapacityRecoveryRepository, store *RedisVideoCapacityStore, nonceKey *VideoCapacityNonceKey) *VideoCapacityExecutionCoordinator {
	return &VideoCapacityExecutionCoordinator{ledger: ledger, recovery: recovery, store: store, nonceKey: nonceKey, permits: make(map[string]*videoProviderSendPermit)}
}

type videoProviderSendPermit struct {
	task  string
	nonce [32]byte
	epoch uint64
}

func (videoProviderSendPermit) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }
func (videoProviderSendPermit) String() string               { return "[video provider send permit]" }
func (videoProviderSendPermit) GoString() string             { return "[video provider send permit]" }

func newVideoProviderSendPermit(task string, epoch uint64) (*videoProviderSendPermit, error) {
	if !videoBillingPublicID.MatchString(task) || epoch == 0 {
		return nil, ErrVideoGovernanceUnavailable
	}
	permit := &videoProviderSendPermit{task: task, epoch: epoch}
	if _, err := rand.Read(permit.nonce[:]); err != nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	return permit, nil
}

func (p *videoProviderSendPermit) hash() string {
	if p == nil {
		return ""
	}
	sum := sha256.Sum256(p.nonce[:])
	return hex.EncodeToString(sum[:])
}

// ValidateSubmission在Provider RPC紧前重新核对当前Task、资金、事件、Worker、ready门闩及Redis running。
func (c *VideoCapacityExecutionCoordinator) ValidateSubmission(ctx context.Context, taskID string, version uint64) error {
	_, err := c.validateSubmissionRecord(ctx, taskID, version)
	return err
}

// ReleaseTerminal只有完整持久终态证明通过后才清理Redis容量；pending_reconcile和证据缺失一律保守保留。
func (c *VideoCapacityExecutionCoordinator) ReleaseTerminal(ctx context.Context, taskID string) error {
	if c == nil || c.ledger == nil || c.recovery == nil || c.store == nil || c.nonceKey == nil || ctx == nil {
		return ErrVideoGovernanceUnavailable
	}
	state, err := c.recovery.Current(ctx)
	if err != nil || state == nil || state.State != "ready" || strconv.FormatUint(state.Epoch, 10) != c.store.epoch || state.PolicyHash != c.store.policy {
		return ErrVideoGovernanceUnavailable
	}
	if err := c.store.ValidateRunID(ctx, state.RedisRunID); err != nil {
		return ErrVideoGovernanceUnavailable
	}
	var task *repository.VideoTaskRecord
	err = c.ledger.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current repository.VideoTaskRecord
		if err := tx.Table("ai_gateway_tasks AS tasks").Select(`tasks.*,requests.execution_status AS request_execution_status,requests.billing_status,requests.delivery_status,requests.version_no AS request_version_no`).Joins("JOIN ai_requests AS requests ON requests.request_id=tasks.request_id AND requests.user_id=tasks.user_id AND requests.project_id=tasks.project_id").Where("tasks.public_id=? AND tasks.user_id=? AND tasks.project_id=? AND tasks.capability=?", taskID, c.ledger.owner.UserID, c.ledger.owner.ProjectID, model.AIVideoCapability).Take(&current).Error; err != nil || !equalOptionalUint64(current.APIKeyID, c.ledger.owner.APIKeyID) || !videoCapacityTerminal(current.Status) {
			return ErrVideoGovernanceUnavailable
		}
		request, quote, link, hold, err := loadVideoFinancialSnapshotTx(tx, &current, c.ledger.owner)
		if err != nil || !videoOutboxExecutionMatches(&current) || request.BillingStatus != current.BillingStatus || request.DeliveryStatus != current.DeliveryStatus {
			return ErrVideoGovernanceUnavailable
		}
		price, err := DecodeVideoPriceSnapshot(quote.PriceSnapshotJSON)
		if err != nil || current.Operation == nil || price.PriceVersionID != quote.PriceVersionID || price.LogicalModelCode != current.LogicalModelCode || price.Operation != *current.Operation || price.SelectedLines[0].VariantHash != quote.RequestVariantHash || !equalVideoFinancialJSON(price.SelectedLines[0].VariantJSON, current.InputJSON) {
			return ErrVideoGovernanceUnavailable
		}
		if err := validateVideoCapacityOutboxTx(tx, &current, request, link, hold); err != nil {
			return err
		}
		if _, err := videoOutboxInputIdentityTx(tx, &current); err != nil {
			return err
		}
		var bindings []model.AIGatewayTaskInput
		if err := tx.Where("task_id=?", current.ID).Find(&bindings).Error; err != nil {
			return err
		}
		if err := validateVideoCapacityTerminalTx(tx, &current, request, quote, link, hold, bindings); err != nil {
			return err
		}
		task = &current
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil || task == nil {
		return ErrVideoGovernanceUnavailable
	}
	identity, err := videoCapacityIdentityForTask(task)
	if err != nil {
		return ErrVideoGovernanceUnavailable
	}
	attempt, err := c.nonceKey.Attempt(state.Epoch, identity)
	if err != nil {
		return ErrVideoGovernanceUnavailable
	}
	if _, err := c.store.releaseCapacity(ctx, attempt); err != nil {
		// 删除响应未知时只读确认原记录确已不存在；任何仍存在或存储异常都保留失败。
		if _, readErr := c.store.Read(ctx, attempt); !errors.Is(readErr, ErrVideoCapacityLeaseLost) {
			return ErrVideoGovernanceUnavailable
		}
	}
	return nil
}

// ConsumeSendPermit只在全部持久与Redis门禁通过后原子取走本进程唯一发送权。
func (c *VideoCapacityExecutionCoordinator) ConsumeSendPermit(ctx context.Context, taskID string, version uint64) error {
	record, err := c.validateSubmissionRecord(ctx, taskID, version)
	if err != nil {
		return err
	}
	c.permitMu.Lock()
	defer c.permitMu.Unlock()
	permit, ok := c.permits[taskID]
	if !ok || permit.task != taskID || record.SubmissionCapacityEpoch == nil || permit.epoch < *record.SubmissionCapacityEpoch || strconv.FormatUint(permit.epoch, 10) != c.store.epoch || record.SubmissionSendTokenHash == nil || permit.hash() != *record.SubmissionSendTokenHash {
		return ErrVideoGovernanceUnavailable
	}
	clear(permit.nonce[:])
	delete(c.permits, taskID)
	return nil
}

func (c *VideoCapacityExecutionCoordinator) validateSubmissionRecord(ctx context.Context, taskID string, version uint64) (*repository.VideoTaskRecord, error) {
	if c == nil || c.ledger == nil || c.recovery == nil || c.store == nil || c.nonceKey == nil || ctx == nil || version == 0 {
		return nil, ErrVideoGovernanceUnavailable
	}
	state, err := c.recovery.Current(ctx)
	if err != nil || state == nil || state.State != "ready" || strconv.FormatUint(state.Epoch, 10) != c.store.epoch || state.PolicyHash != c.store.policy {
		return nil, ErrVideoGovernanceUnavailable
	}
	if err := c.store.ValidateRunID(ctx, state.RedisRunID); err != nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	record, err := c.validateCommittedPlan(ctx, taskID, state)
	if err != nil || record.VersionNo != version {
		return nil, ErrVideoGovernanceUnavailable
	}
	identity, err := videoCapacityIdentityForTask(record)
	if err != nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	attempt, err := c.nonceKey.Attempt(state.Epoch, identity)
	if err != nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	view, err := c.store.Read(ctx, attempt)
	if err != nil || view.Phase != "running" || view.Expired {
		return nil, ErrVideoGovernanceUnavailable
	}
	return record, nil
}

// PromoteAndPlan只返回已完成三段协调的submitting任务，不调用Provider，也不保存Provider回执。
func (c *VideoCapacityExecutionCoordinator) PromoteAndPlan(ctx context.Context, taskID string, expectedVersion uint64) (videogateway.GatewayTask, error) {
	if c == nil || c.ledger == nil || c.ledger.db == nil || c.recovery == nil || c.store == nil || c.nonceKey == nil || ctx == nil || expectedVersion == 0 {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	state, err := c.recovery.Current(ctx)
	if err != nil || state == nil || state.State != "ready" || state.Epoch == 0 || strconv.FormatUint(state.Epoch, 10) != c.store.epoch || state.PolicyHash != c.store.policy || state.RedisRunID == "" {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	if err := c.store.ValidateRunID(ctx, state.RedisRunID); err != nil {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	record, err := repository.NewVideoTaskRepository(c.ledger.db).FindForOwner(ctx, taskID, c.ledger.owner)
	if err != nil {
		return videogateway.GatewayTask{}, mapVideoRepositoryError(err)
	}
	identity, err := videoCapacityIdentityForTask(record)
	if err != nil {
		return videogateway.GatewayTask{}, err
	}
	attempt, err := c.nonceKey.Attempt(state.Epoch, identity)
	if err != nil {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	if record.Status == model.AIImageTaskSubmitting && videoCapacityPlanMatches(record, state.Epoch) {
		return c.finishCommittedPromotion(ctx, record, state, attempt)
	}
	if record.Status == model.AIImageTaskSubmitting && videoCapacityPlanNeedsSend(record, state.Epoch) {
		permit, permitErr := newVideoProviderSendPermit(taskID, state.Epoch)
		if permitErr != nil {
			return videogateway.GatewayTask{}, permitErr
		}
		bound, err := c.bindHistoricalPlan(ctx, record, state, permit)
		if err != nil {
			reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if resolved, resolveErr := c.validateCommittedPlan(reconcileCtx, taskID, state); resolveErr == nil {
				c.retainPermit(resolved, permit)
				return c.finishCommittedPromotion(reconcileCtx, resolved, state, attempt)
			}
			return videogateway.GatewayTask{}, err
		}
		c.retainPermit(bound, permit)
		return c.finishCommittedPromotion(ctx, bound, state, attempt)
	}
	if record.Status != model.AIImageTaskQueued || record.VersionNo != expectedVersion || record.PlannedProviderCode != nil || record.SubmissionCapacityEpoch != nil {
		return videogateway.GatewayTask{}, videogateway.ErrGatewayTaskConflict
	}
	prepared, err := c.store.PrepareRunning(ctx, attempt)
	if err != nil {
		var limited *VideoCapacityLimitError
		if errors.As(err, &limited) {
			return videogateway.GatewayTask{}, videogateway.ErrGatewayRunningCapacity
		}
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	if prepared.Phase != "promoting" {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	permit, err := newVideoProviderSendPermit(taskID, state.Epoch)
	if err != nil {
		return videogateway.GatewayTask{}, err
	}
	committed, err := c.commitPromotion(ctx, record, state, permit)
	if err != nil {
		// COMMIT回执未知时先查原Task；只有精确queued旧版本能证明本次没有提交，才允许撤销promoting。
		reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		resolved, safeAbort := c.resolvePromotion(reconcileCtx, taskID, expectedVersion, state)
		if resolved != nil {
			c.retainPermit(resolved, permit)
			return c.finishCommittedPromotion(reconcileCtx, resolved, state, attempt)
		}
		if safeAbort {
			if view, abortErr := c.store.abortPromotion(reconcileCtx, attempt); abortErr == nil && view.Phase == "queued" {
				return videogateway.GatewayTask{}, err
			}
		}
		return videogateway.GatewayTask{}, errors.Join(err, ErrVideoGovernanceUnavailable)
	}
	c.retainPermit(committed, permit)
	return c.finishCommittedPromotion(ctx, committed, state, attempt)
}

func (c *VideoCapacityExecutionCoordinator) commitPromotion(ctx context.Context, original *repository.VideoTaskRecord, state *repository.VideoCapacityRecoveryState, permit *videoProviderSendPermit) (*repository.VideoTaskRecord, error) {
	if permit == nil || permit.task != original.PublicID || permit.epoch != state.Epoch || !lowerHex64.MatchString(permit.hash()) {
		return nil, ErrVideoGovernanceUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var committed *repository.VideoTaskRecord
	err := c.ledger.db.WithContext(bounded).Transaction(func(tx *gorm.DB) error {
		tasks := repository.NewVideoTaskRepository(tx)
		current, err := tasks.LockForOwnerTx(tx, original.PublicID, c.ledger.owner)
		if err != nil {
			return err
		}
		if current.VersionNo != original.VersionNo || current.Status != model.AIImageTaskQueued || current.PlannedProviderCode != nil || current.SubmissionCapacityEpoch != nil || current.ProviderCode != nil || current.ProviderTaskID != nil || current.AttemptCount != 0 || current.CancelRequestedAt != nil || current.ArchiveTokenHash != nil {
			return videogateway.ErrGatewayTaskConflict
		}
		if err := repository.CheckVideoWorkerLeaseTx(bounded, tx, current); err != nil {
			return err
		}
		if err := validateVideoCapacityActiveFinancialTx(tx, current, c.ledger.owner, true); err != nil {
			return err
		}
		if err := verifyVideoNeverSubmittedTx(tx, current); err != nil {
			return err
		}
		var guard videoCapacityReadyGuard
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Table("ai_video_queue_admission_guard").Where("id=1").Take(&guard).Error; err != nil || !guard.matches(state) {
			return ErrVideoGovernanceUnavailable
		}
		advanced, err := c.ledger.withDB(tx).advanceOnce(bounded, current.PublicID, current.VersionNo, videogateway.TaskSubmitting, "worker", "state_advanced", nil)
		if err != nil {
			return err
		}
		current, err = tasks.LockForOwnerTx(tx, original.PublicID, c.ledger.owner)
		if err != nil || current.VersionNo != advanced.Version || current.Status != model.AIImageTaskSubmitting || current.VersionNo == math.MaxUint64 {
			return ErrVideoGovernanceUnavailable
		}
		if _, err := videoSubmissionClaimTx(tx, current, current.VersionNo); err != nil {
			return err
		}
		var clock struct{ Now time.Time }
		if err := tx.Raw("SELECT UTC_TIMESTAMP(6) AS now").Scan(&clock).Error; err != nil || clock.Now.IsZero() {
			return ErrVideoGovernanceUnavailable
		}
		now := clock.Now
		providerTaskID, err := newVideoProviderTaskUUID()
		if err != nil {
			return err
		}
		updated := tx.Model(&model.AIImageTask{}).Where("id=? AND version_no=? AND planned_provider_code IS NULL AND submission_capacity_epoch IS NULL", current.ID, current.VersionNo).Updates(map[string]any{
			"planned_provider_code": "fake-native-async", "submission_intent_id": providerTaskID,
			"submission_claim_version": current.VersionNo, "submission_worker_version": current.WorkerLeaseVersion,
			"submission_capacity_epoch": state.Epoch, "version_no": gorm.Expr("version_no+1"), "updated_at": now,
			"submission_send_token_sha256": permit.hash(), "submission_send_worker_version": current.WorkerLeaseVersion, "submission_send_started_at": now,
		})
		if err := videoBillingCASResult(updated); err != nil {
			return err
		}
		planEvent := model.AIGatewayTaskEvent{EventID: "vg7_plan_" + videoBillingDigest(current.PublicID), TaskID: current.ID, UserID: current.UserID, ProjectID: current.ProjectID, EventType: "video_submission_planned", Source: "worker", SafeDetailJSON: json.RawMessage(`{}`), CreatedAt: now}
		capacityEvent := model.AIGatewayTaskEvent{EventID: "vg7_capacity_" + videoBillingDigest(current.PublicID+":"+strconv.FormatUint(state.Epoch, 10)), TaskID: current.ID, UserID: current.UserID, ProjectID: current.ProjectID, EventType: "video_submission_capacity_bound", Source: "worker", SafeDetailJSON: json.RawMessage(`{}`), CreatedAt: now}
		sendEvent := model.AIGatewayTaskEvent{EventID: "vg7_send_" + videoBillingDigest(current.PublicID), TaskID: current.ID, UserID: current.UserID, ProjectID: current.ProjectID, EventType: "video_submission_send_claimed", Source: "worker", SafeDetailJSON: json.RawMessage(`{}`), CreatedAt: now}
		if err := tx.Create(&planEvent).Error; err != nil {
			return err
		}
		if err := tx.Create(&capacityEvent).Error; err != nil {
			return err
		}
		if err := tx.Create(&sendEvent).Error; err != nil {
			return err
		}
		if c.fault != nil {
			if err := c.fault("after_capacity_event"); err != nil {
				return err
			}
		}
		if err := repository.CheckVideoWorkerLeaseTx(bounded, tx, current); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Table("ai_video_queue_admission_guard").Where("id=1").Take(&guard).Error; err != nil || !guard.matches(state) {
			return ErrVideoGovernanceUnavailable
		}
		committed, err = tasks.LockForOwnerTx(tx, original.PublicID, c.ledger.owner)
		if err != nil || !videoCapacityPlanMatches(committed, state.Epoch) {
			return ErrVideoGovernanceUnavailable
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return committed, err
}

func (c *VideoCapacityExecutionCoordinator) finishCommittedPromotion(ctx context.Context, record *repository.VideoTaskRecord, state *repository.VideoCapacityRecoveryState, attempt *VideoCapacityAttempt) (videogateway.GatewayTask, error) {
	validated, err := c.validateCommittedPlan(ctx, record.PublicID, state)
	if err != nil {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	view, err := c.store.Read(ctx, attempt)
	if err != nil {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	if view.Phase == "queued" {
		view, err = c.store.PrepareRunning(ctx, attempt)
		if err != nil {
			return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
		}
	}
	if view.Phase == "promoting" {
		view, err = c.store.confirmRunning(ctx, attempt)
		if err != nil {
			// EVAL回执未知先查原尝试；只有实际running才可继续。
			view, err = c.store.Read(ctx, attempt)
		}
	}
	if err != nil || view.Phase != "running" {
		return videogateway.GatewayTask{}, ErrVideoGovernanceUnavailable
	}
	return c.ledger.Load(ctx, validated.PublicID)
}

func (c *VideoCapacityExecutionCoordinator) resolvePromotion(ctx context.Context, taskID string, expectedVersion uint64, state *repository.VideoCapacityRecoveryState) (*repository.VideoTaskRecord, bool) {
	record, err := repository.NewVideoTaskRepository(c.ledger.db).FindForOwner(ctx, taskID, c.ledger.owner)
	if err != nil {
		return nil, false
	}
	if videoCapacityPlanMatches(record, state.Epoch) {
		validated, err := c.validateCommittedPlan(ctx, taskID, state)
		if err == nil {
			return validated, false
		}
		return nil, false
	}
	return nil, record.Status == model.AIImageTaskQueued && record.VersionNo == expectedVersion && record.PlannedProviderCode == nil && record.SubmissionCapacityEpoch == nil
}

func (c *VideoCapacityExecutionCoordinator) validateCommittedPlan(ctx context.Context, taskID string, state *repository.VideoCapacityRecoveryState) (*repository.VideoTaskRecord, error) {
	var validated *repository.VideoTaskRecord
	err := c.ledger.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := repository.NewVideoTaskRepository(tx).FindForOwner(ctx, taskID, c.ledger.owner)
		if err != nil || !videoCapacityPlanMatches(task, state.Epoch) {
			return ErrVideoGovernanceUnavailable
		}
		if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
			return err
		}
		if err := validateVideoCapacityActiveFinancialTx(tx, task, c.ledger.owner, false); err != nil {
			return err
		}
		var guard videoCapacityReadyGuard
		if err := tx.Table("ai_video_queue_admission_guard").Where("id=1").Take(&guard).Error; err != nil || !guard.matches(state) {
			return ErrVideoGovernanceUnavailable
		}
		if err := validateVideoCapacityPlanEventsTx(tx, task); err != nil {
			return err
		}
		validated = task
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return validated, err
}

func (c *VideoCapacityExecutionCoordinator) bindHistoricalPlan(ctx context.Context, original *repository.VideoTaskRecord, state *repository.VideoCapacityRecoveryState, permit *videoProviderSendPermit) (*repository.VideoTaskRecord, error) {
	if permit == nil || permit.task != original.PublicID || permit.epoch != state.Epoch || !lowerHex64.MatchString(permit.hash()) {
		return nil, ErrVideoGovernanceUnavailable
	}
	var bound *repository.VideoTaskRecord
	err := c.ledger.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tasks := repository.NewVideoTaskRepository(tx)
		task, err := tasks.LockForOwnerTx(tx, original.PublicID, c.ledger.owner)
		if err != nil || !videoCapacityPlanNeedsSend(task, state.Epoch) || task.VersionNo != original.VersionNo || task.VersionNo == math.MaxUint64 {
			return ErrVideoGovernanceUnavailable
		}
		if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
			return err
		}
		if err := validateVideoCapacityActiveFinancialTx(tx, task, c.ledger.owner, true); err != nil {
			return err
		}
		if err := validateVideoCapacityPlanEventTx(tx, task); err != nil {
			return err
		}
		if task.SubmissionCapacityEpoch != nil {
			if err := validateVideoCapacityEventTx(tx, task); err != nil {
				return err
			}
		}
		var guard videoCapacityReadyGuard
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Table("ai_video_queue_admission_guard").Where("id=1").Take(&guard).Error; err != nil || !guard.matches(state) {
			return ErrVideoGovernanceUnavailable
		}
		var clock struct{ Now time.Time }
		if err := tx.Raw("SELECT UTC_TIMESTAMP(6) AS now").Scan(&clock).Error; err != nil || clock.Now.IsZero() {
			return ErrVideoGovernanceUnavailable
		}
		updates := map[string]any{"submission_send_token_sha256": permit.hash(), "submission_send_worker_version": task.WorkerLeaseVersion, "submission_send_started_at": clock.Now, "version_no": gorm.Expr("version_no+1"), "updated_at": clock.Now}
		if task.SubmissionCapacityEpoch == nil {
			updates["submission_capacity_epoch"] = state.Epoch
		}
		if err := videoBillingCASResult(tx.Model(&model.AIImageTask{}).Where("id=? AND version_no=? AND submission_send_token_sha256 IS NULL", task.ID, task.VersionNo).Updates(updates)); err != nil {
			return err
		}
		if task.SubmissionCapacityEpoch == nil {
			event := model.AIGatewayTaskEvent{EventID: "vg7_capacity_" + videoBillingDigest(task.PublicID+":"+strconv.FormatUint(state.Epoch, 10)), TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "video_submission_capacity_bound", Source: "worker", SafeDetailJSON: json.RawMessage(`{}`), CreatedAt: clock.Now}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
		}
		sendEvent := model.AIGatewayTaskEvent{EventID: "vg7_send_" + videoBillingDigest(task.PublicID), TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "video_submission_send_claimed", Source: "worker", SafeDetailJSON: json.RawMessage(`{}`), CreatedAt: clock.Now}
		if err := tx.Create(&sendEvent).Error; err != nil {
			return err
		}
		if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
			return err
		}
		bound, err = tasks.LockForOwnerTx(tx, task.PublicID, c.ledger.owner)
		if err != nil || !videoCapacityPlanMatches(bound, state.Epoch) {
			return ErrVideoGovernanceUnavailable
		}
		return validateVideoCapacityPlanEventsTx(tx, bound)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return bound, err
}

func validateVideoCapacityActiveFinancialTx(tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner, lockHold bool) error {
	var request *model.VideoBillingRequest
	var link *model.AIRequestWalletLink
	var hold *billingmodel.WalletHold
	var err error
	if lockHold {
		request, _, link, hold, err = loadVideoFinancialFactsTx(tx, task, owner)
	} else {
		request, _, link, hold, err = loadVideoFinancialFactsReadTx(tx, task, owner)
	}
	expectedExecution := repository.VideoRequestExecutionStatus(task.Status)
	if err != nil || request.ExecutionStatus != expectedExecution || request.BillingStatus != model.AIBillingHeld || request.DeliveryStatus != model.AIDeliveryPending || request.SettledAmount != nil || hold.Status != billingmodel.HoldStatusHolding || hold.SettledAmount != nil || hold.SettleTxnID != nil || hold.SettledAt != nil || link.SettledAmount != nil || link.SettleTransactionID != nil || link.ReleaseTransactionID != nil {
		return ErrVideoBillingState
	}
	for _, query := range []*gorm.DB{
		tx.Model(&model.AIUsageItem{}).Where("request_id=?", task.RequestID),
		tx.Model(&model.AIExecutionAttempt{}).Where("request_id=?", task.RequestID),
		tx.Model(&model.AIGatewayProviderCallbackEvent{}).Where("task_id=?", task.ID),
	} {
		var count int64
		if err := query.Count(&count).Error; err != nil || count != 0 {
			return ErrVideoBillingState
		}
	}
	return nil
}

func validateVideoCapacityPlanEventTx(tx *gorm.DB, task *repository.VideoTaskRecord) error {
	var events []model.AIGatewayTaskEvent
	if err := tx.Where("task_id=? AND event_type='video_submission_planned'", task.ID).Find(&events).Error; err != nil || len(events) != 1 {
		return ErrVideoGovernanceUnavailable
	}
	event := events[0]
	if event.EventID != "vg7_plan_"+videoBillingDigest(task.PublicID) || event.UserID != task.UserID || event.ProjectID != task.ProjectID || event.Source != "worker" || event.FromStatus != nil || event.ToStatus != nil || string(event.SafeDetailJSON) != "{}" {
		return ErrVideoGovernanceUnavailable
	}
	return nil
}

func validateVideoCapacityPlanEventsTx(tx *gorm.DB, task *repository.VideoTaskRecord) error {
	if err := validateVideoCapacityPlanEventTx(tx, task); err != nil || task.SubmissionCapacityEpoch == nil {
		return ErrVideoGovernanceUnavailable
	}
	if err := validateVideoCapacityEventTx(tx, task); err != nil {
		return err
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

func validateVideoCapacityEventTx(tx *gorm.DB, task *repository.VideoTaskRecord) error {
	if task == nil || task.SubmissionCapacityEpoch == nil {
		return ErrVideoGovernanceUnavailable
	}
	var events []model.AIGatewayTaskEvent
	if err := tx.Where("task_id=? AND event_type='video_submission_capacity_bound'", task.ID).Find(&events).Error; err != nil || len(events) != 1 {
		return ErrVideoGovernanceUnavailable
	}
	event := events[0]
	want := "vg7_capacity_" + videoBillingDigest(task.PublicID+":"+strconv.FormatUint(*task.SubmissionCapacityEpoch, 10))
	if event.EventID != want || event.UserID != task.UserID || event.ProjectID != task.ProjectID || event.Source != "worker" || event.FromStatus != nil || event.ToStatus != nil || string(event.SafeDetailJSON) != "{}" {
		return ErrVideoGovernanceUnavailable
	}
	return nil
}

func videoCapacityIdentityForTask(task *repository.VideoTaskRecord) (VideoCapacityIdentity, error) {
	if task == nil || task.Operation == nil {
		return VideoCapacityIdentity{}, ErrVideoGovernanceUnavailable
	}
	identity := VideoCapacityIdentity{TaskID: task.PublicID, RequestID: task.RequestID, UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID, Model: task.LogicalModelCode, Provider: "fake-native-async", Operation: *task.Operation}
	if _, err := canonicalVideoCapacityIdentity(identity); err != nil {
		return VideoCapacityIdentity{}, ErrVideoGovernanceUnavailable
	}
	return identity, nil
}

func videoCapacityPlanMatches(task *repository.VideoTaskRecord, epoch uint64) bool {
	return task != nil && task.Status == model.AIImageTaskSubmitting && task.PlannedProviderCode != nil && *task.PlannedProviderCode == "fake-native-async" && task.SubmissionIntentID != nil && videoProviderTaskUUIDPattern.MatchString(*task.SubmissionIntentID) && task.SubmissionClaimVersion != nil && *task.SubmissionClaimVersion >= 2 && task.SubmissionWorkerVersion != nil && *task.SubmissionWorkerVersion > 0 && task.SubmissionCapacityEpoch != nil && *task.SubmissionCapacityEpoch > 0 && *task.SubmissionCapacityEpoch <= epoch && task.SubmissionSendTokenHash != nil && lowerHex64.MatchString(*task.SubmissionSendTokenHash) && task.SubmissionSendWorker != nil && *task.SubmissionSendWorker > 0 && task.SubmissionSendStartedAt != nil && task.ProviderCode == nil && task.ProviderTaskID == nil && task.AttemptCount == 0
}

func videoCapacityPlanNeedsSend(task *repository.VideoTaskRecord, epoch uint64) bool {
	return task != nil && task.Status == model.AIImageTaskSubmitting && task.PlannedProviderCode != nil && *task.PlannedProviderCode == "fake-native-async" && task.SubmissionIntentID != nil && videoProviderTaskUUIDPattern.MatchString(*task.SubmissionIntentID) && task.SubmissionClaimVersion != nil && *task.SubmissionClaimVersion >= 2 && task.SubmissionWorkerVersion != nil && *task.SubmissionWorkerVersion > 0 && (task.SubmissionCapacityEpoch == nil || (*task.SubmissionCapacityEpoch > 0 && *task.SubmissionCapacityEpoch <= epoch)) && task.SubmissionSendTokenHash == nil && task.SubmissionSendWorker == nil && task.SubmissionSendStartedAt == nil && task.ProviderCode == nil && task.ProviderTaskID == nil && task.AttemptCount == 0
}

func (c *VideoCapacityExecutionCoordinator) retainPermit(task *repository.VideoTaskRecord, permit *videoProviderSendPermit) {
	if task == nil || permit == nil || task.PublicID != permit.task || task.SubmissionSendTokenHash == nil || *task.SubmissionSendTokenHash != permit.hash() || task.SubmissionSendWorker == nil || task.SubmissionSendStartedAt == nil {
		return
	}
	c.permitMu.Lock()
	c.permits[task.PublicID] = permit
	c.permitMu.Unlock()
}

type videoCapacityReadyGuard struct {
	ID                     uint8
	CapacityEpoch          uint64
	CapacityState          string
	CapacityPolicySHA256   *string
	CapacityRedisRunID     *string
	CapacitySnapshotSHA256 *string
	CapacitySnapshotCount  *uint32
	CapacityReadyAt        *time.Time
}

func (g videoCapacityReadyGuard) matches(state *repository.VideoCapacityRecoveryState) bool {
	return state != nil && g.ID == 1 && g.CapacityEpoch == state.Epoch && g.CapacityState == "ready" && g.CapacityPolicySHA256 != nil && *g.CapacityPolicySHA256 == state.PolicyHash && g.CapacityRedisRunID != nil && *g.CapacityRedisRunID == state.RedisRunID && g.CapacitySnapshotSHA256 != nil && *g.CapacitySnapshotSHA256 == state.SnapshotHash && g.CapacitySnapshotCount != nil && *g.CapacitySnapshotCount == state.SnapshotCount && g.CapacityReadyAt != nil && !g.CapacityReadyAt.IsZero()
}
