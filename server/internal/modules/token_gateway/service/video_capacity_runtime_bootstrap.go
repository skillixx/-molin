package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type VideoCapacityRuntime struct {
	Recovery *repository.VideoCapacityRecoveryRepository
	Store    *RedisVideoCapacityStore
	Policy   *video.VideoCapacityPolicy
	Summary  VideoCapacitySnapshotSummary
}

// PrepareVideoCapacityRuntime由一个实例领导完整恢复，其余实例只在原子验证MySQL/Redis ready一致后加入。
func PrepareVideoCapacityRuntime(ctx context.Context, db *gorm.DB, client *redis.Client, key *VideoCapacityNonceKey, owner string) (*VideoCapacityRuntime, error) {
	if ctx == nil || db == nil || client == nil || key == nil || !videoBillingPublicID.MatchString(owner) || len(owner) > 64 {
		return nil, ErrVideoGovernanceUnavailable
	}
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		return nil, fmt.Errorf("capacity_policy: %w", ErrVideoGovernanceUnavailable)
	}
	policyHash, err := policy.Fingerprint()
	if err != nil {
		return nil, fmt.Errorf("capacity_policy_hash: %w", ErrVideoGovernanceUnavailable)
	}
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	for ctx.Err() == nil {
		runID, err := ReadVideoRedisRunID(ctx, client)
		if err != nil {
			return nil, fmt.Errorf("capacity_redis_run_id: %w", err)
		}
		state, err := recovery.Current(ctx)
		if err != nil {
			return nil, fmt.Errorf("capacity_mysql_state: %w", err)
		}
		if state.State == "ready" && state.Epoch > 0 && state.PolicyHash == policyHash && state.RedisRunID == runID {
			store, err := NewRedisVideoCapacityStore(client, state.Epoch, policy)
			if err != nil {
				return nil, err
			}
			count, readyErr := store.ValidateReadyState(ctx, runID)
			if readyErr == nil && count == int(state.SnapshotCount) && state.SnapshotHash != "" {
				return &VideoCapacityRuntime{Recovery: recovery, Store: store, Policy: policy, Summary: VideoCapacitySnapshotSummary{Epoch: state.Epoch, Total: count}}, nil
			}
			// MySQL ready但Redis事实缺失或漂移，不能继续启动；争取下一epoch完整重建。
		}
		if state.State == "recovering" && state.LeaseUntil.After(time.Now().UTC()) {
			if !waitVideoCapacityBootstrap(ctx, 250*time.Millisecond) {
				break
			}
			continue
		}
		proof, err := recovery.Begin(ctx, state.Epoch, owner, policyHash, runID)
		if errors.Is(err, repository.ErrVideoCapacityRecoveryBusy) || errors.Is(err, repository.ErrVideoCapacityRecoveryConflict) {
			if !waitVideoCapacityBootstrap(ctx, 250*time.Millisecond) {
				break
			}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("capacity_claim: %w", err)
		}
		store, err := NewRedisVideoCapacityStore(client, proof.Epoch(), policy)
		if err != nil {
			_ = recovery.Block(context.WithoutCancel(ctx), proof)
			return nil, fmt.Errorf("capacity_store: %w", err)
		}
		coordinator := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, store)
		prepared, err := coordinator.Prepare(ctx, proof, policy)
		if err == nil {
			var summary VideoCapacitySnapshotSummary
			summary, err = coordinator.Complete(ctx, proof, prepared)
			if err == nil {
				return &VideoCapacityRuntime{Recovery: recovery, Store: store, Policy: policy, Summary: summary}, nil
			}
		}
		blockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		_ = recovery.Block(blockCtx, proof)
		cancel()
		return nil, fmt.Errorf("capacity_leader_recovery: %w", err)
	}
	return nil, errors.Join(ErrVideoGovernanceUnavailable, ctx.Err())
}

func waitVideoCapacityBootstrap(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
