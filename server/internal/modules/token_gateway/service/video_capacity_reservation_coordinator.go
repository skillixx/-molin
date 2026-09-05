package service

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// videoCapacityQueueAdmission只在原财务事务尾部、全部授权复核后预留Redis queued。
type videoCapacityQueueAdmission struct {
	store *RedisVideoCapacityStore
	key   *VideoCapacityNonceKey
	mu    sync.Mutex
	seen  map[string]*VideoCapacityAttempt
}

func (a *videoCapacityQueueAdmission) AdmitTx(tx *gorm.DB, owner repository.VideoOwner, taskID string) error {
	if a == nil || a.store == nil || a.key == nil || tx == nil || tx.Statement == nil || tx.Statement.Context == nil {
		return ErrVideoGovernanceUnavailable
	}
	ctx := tx.Statement.Context
	task, err := repository.NewVideoTaskRepository(tx).FindForOwner(ctx, taskID, owner)
	if err != nil || task.Status != model.AIImageTaskReserved || task.PlannedProviderCode != nil || task.ProviderCode != nil || task.AttemptCount != 0 {
		return ErrVideoGovernanceUnavailable
	}
	var guard videoCapacityReadyGuard
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Table("ai_video_queue_admission_guard").Where("id=1").Take(&guard).Error; err != nil || guard.CapacityState != "ready" || strconv.FormatUint(guard.CapacityEpoch, 10) != a.store.epoch || guard.CapacityPolicySHA256 == nil || *guard.CapacityPolicySHA256 != a.store.policy || guard.CapacityRedisRunID == nil {
		return ErrVideoGovernanceUnavailable
	}
	if err := a.store.ValidateRunID(ctx, *guard.CapacityRedisRunID); err != nil {
		return ErrVideoGovernanceUnavailable
	}
	identity, err := videoCapacityIdentityForTask(task)
	if err != nil {
		return err
	}
	attempt, err := a.key.Attempt(guard.CapacityEpoch, identity)
	if err != nil {
		return ErrVideoGovernanceUnavailable
	}
	// EVAL回执可能丢失；先登记确定性attempt，外层只有确认MySQL未提交后才允许用它清理。
	a.mu.Lock()
	a.seen[taskID] = attempt
	a.mu.Unlock()
	view, err := a.store.ReserveQueued(ctx, attempt)
	if err != nil {
		var limited *VideoCapacityLimitError
		if errors.As(err, &limited) {
			return &VideoQueueLimitError{Scope: limited.Scope}
		}
		return ErrVideoGovernanceUnavailable
	}
	if view.Phase != "queued" {
		return ErrVideoGovernanceUnavailable
	}
	return nil
}

func (a *videoCapacityQueueAdmission) take(taskID string) *VideoCapacityAttempt {
	a.mu.Lock()
	defer a.mu.Unlock()
	attempt := a.seen[taskID]
	delete(a.seen, taskID)
	return attempt
}

// VideoCapacityReservationCoordinator在原财务协调器外只管理Redis提交结果，不复制Quote、Hold或Task账本。
type VideoCapacityReservationCoordinator struct {
	base     *VideoBillingService
	recovery *repository.VideoCapacityRecoveryRepository
	store    *RedisVideoCapacityStore
	key      *VideoCapacityNonceKey
}

func NewVideoCapacityReservationCoordinator(base *VideoBillingService, recovery *repository.VideoCapacityRecoveryRepository, store *RedisVideoCapacityStore, key *VideoCapacityNonceKey) (*VideoCapacityReservationCoordinator, error) {
	if base == nil || base.db == nil || recovery == nil || store == nil || key == nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	copy := *base
	return &VideoCapacityReservationCoordinator{base: &copy, recovery: recovery, store: store, key: key}, nil
}

func (c *VideoCapacityReservationCoordinator) ReserveAndCreate(ctx context.Context, command VideoReservationCommand) (*VideoPreparedGeneration, error) {
	local, admission := c.localService()
	intent, err := local.prepareVideoReservationIntent(command)
	if err != nil {
		return nil, err
	}
	result, callErr := local.ReserveAndCreate(ctx, command)
	return c.resolve(ctx, local, intent, command.TaskID, command.RequestID, admission.take(command.TaskID), result, callErr)
}

func (c *VideoCapacityReservationCoordinator) CreateWithAutomaticQuote(ctx context.Context, request VideoFacadeRequest, quotes *VideoQuoteService) (*VideoPreparedGeneration, error) {
	local, admission := c.localService()
	command := VideoReservationCommand{Rights: request.Rights, Prompt: request.Prompt, RightsPolicyVersion: request.RightsPolicyVersion, IdempotencyKey: request.IdempotencyKey, RequestID: request.RequestID, TaskID: request.TaskID, FingerprintInput: request.FingerprintInput, QuoteCommandKind: VideoQuoteCommandKindCreate}
	intent, err := local.prepareVideoReservationIntent(command)
	if err != nil {
		return nil, err
	}
	result, callErr := local.CreateWithAutomaticQuote(ctx, request, quotes)
	return c.resolve(ctx, local, intent, command.TaskID, command.RequestID, admission.take(command.TaskID), result, callErr)
}

func (c *VideoCapacityReservationCoordinator) localService() (*VideoBillingService, *videoCapacityQueueAdmission) {
	admission := &videoCapacityQueueAdmission{store: c.store, key: c.key, seen: make(map[string]*VideoCapacityAttempt)}
	local := *c.base
	local.queue = admission
	return &local, admission
}

func (c *VideoCapacityReservationCoordinator) resolve(parent context.Context, local *VideoBillingService, intent *videoReservationIntent, taskID, requestID string, attempt *VideoCapacityAttempt, result *VideoPreparedGeneration, callErr error) (*VideoPreparedGeneration, error) {
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	record, findErr := repository.NewVideoTaskRepository(local.db).FindForOwner(checkCtx, taskID, intent.owner)
	if callErr != nil {
		if findErr == nil && record.RequestID == requestID {
			if existing, found, err := local.lookupVideoReservation(checkCtx, intent); err == nil && found {
				return c.verifyCommitted(checkCtx, record, attempt, existing)
			}
			return nil, errors.Join(callErr, ErrVideoGovernanceUnavailable)
		}
		if attempt != nil && errors.Is(findErr, repository.ErrVideoTaskNotFound) {
			if _, releaseErr := c.store.releaseCapacity(checkCtx, attempt); releaseErr != nil {
				if _, readErr := c.store.Read(checkCtx, attempt); !errors.Is(readErr, ErrVideoCapacityLeaseLost) {
					return nil, errors.Join(callErr, ErrVideoGovernanceUnavailable)
				}
			}
		}
		return nil, callErr
	}
	if result == nil || findErr != nil || record.RequestID != result.RequestID || record.PublicID != result.TaskID {
		return nil, ErrVideoGovernanceUnavailable
	}
	return c.verifyCommitted(checkCtx, record, attempt, result)
}

func (c *VideoCapacityReservationCoordinator) verifyCommitted(ctx context.Context, record *repository.VideoTaskRecord, attempt *VideoCapacityAttempt, result *VideoPreparedGeneration) (*VideoPreparedGeneration, error) {
	state, err := c.recovery.Current(ctx)
	if err != nil || state.State != "ready" || strconv.FormatUint(state.Epoch, 10) != c.store.epoch || state.PolicyHash != c.store.policy {
		return nil, ErrVideoGovernanceUnavailable
	}
	if attempt == nil {
		identity, err := videoCapacityIdentityForTask(record)
		if err != nil {
			return nil, ErrVideoGovernanceUnavailable
		}
		attempt, err = c.key.Attempt(state.Epoch, identity)
		if err != nil {
			return nil, ErrVideoGovernanceUnavailable
		}
	}
	view, err := c.store.Read(ctx, attempt)
	if videoCapacityTerminal(record.Status) && errors.Is(err, ErrVideoCapacityLeaseLost) {
		return result, nil
	}
	if err != nil || (view.Phase != "queued" && view.Phase != "promoting" && view.Phase != "running") {
		return nil, ErrVideoGovernanceUnavailable
	}
	return result, nil
}
