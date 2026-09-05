package service

import (
	"context"
	"errors"
	"strconv"

	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// VideoCapacityPreparedRecovery是两阶段之间唯一保留对象，普通输出始终脱敏。
type VideoCapacityPreparedRecovery struct {
	snapshot      *VideoCapacityRecoverySnapshot
	summary       VideoCapacitySnapshotSummary
	policy, runID string
}

func (VideoCapacityPreparedRecovery) MarshalJSON() ([]byte, error) {
	return []byte(`{"redacted":true}`), nil
}
func (VideoCapacityPreparedRecovery) String() string   { return "[video capacity prepared recovery]" }
func (VideoCapacityPreparedRecovery) GoString() string { return "[video capacity prepared recovery]" }
func (p *VideoCapacityPreparedRecovery) Summary() VideoCapacitySnapshotSummary {
	if p == nil {
		return VideoCapacitySnapshotSummary{}
	}
	return p.summary
}

// VideoCapacityRecoveryCoordinator隐藏Build→Stage→DB ready→Activate顺序，调用方不能拆开逐桶写入。
type VideoCapacityRecoveryCoordinator struct {
	builder  *VideoCapacitySnapshotBuilder
	recovery *repository.VideoCapacityRecoveryRepository
	store    *RedisVideoCapacityStore
}

func NewVideoCapacityRecoveryCoordinator(builder *VideoCapacitySnapshotBuilder, recovery *repository.VideoCapacityRecoveryRepository, store *RedisVideoCapacityStore) *VideoCapacityRecoveryCoordinator {
	return &VideoCapacityRecoveryCoordinator{builder: builder, recovery: recovery, store: store}
}

func (c *VideoCapacityRecoveryCoordinator) Prepare(ctx context.Context, proof *repository.VideoCapacityRecoveryLease, policy *video.VideoCapacityPolicy) (*VideoCapacityPreparedRecovery, error) {
	if c == nil || c.builder == nil || c.recovery == nil || c.store == nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	snapshot, summary, err := c.builder.BuildSnapshot(ctx, proof, policy)
	if err != nil {
		return nil, err
	}
	state, err := c.recovery.Current(ctx)
	if err != nil || state.State != "recovering" || state.Epoch != summary.Epoch {
		return nil, ErrVideoGovernanceUnavailable
	}
	prepared := &VideoCapacityPreparedRecovery{snapshot: snapshot, summary: summary, policy: state.PolicyHash, runID: state.RedisRunID}
	if err := c.store.ValidateRunID(ctx, prepared.runID); err != nil {
		return prepared, err
	}
	view, err := c.store.StageRecovery(ctx, snapshot)
	if err != nil {
		view, err = c.store.InspectRecovery(ctx, snapshot)
		if err != nil || view.Status != "rebuilding" {
			return prepared, ErrVideoCapacityUnavailable
		}
	}
	if view.Status != "rebuilding" || view.Count != summary.Total {
		return prepared, ErrVideoCapacityUnavailable
	}
	return prepared, nil
}

func (c *VideoCapacityRecoveryCoordinator) Complete(ctx context.Context, proof *repository.VideoCapacityRecoveryLease, prepared *VideoCapacityPreparedRecovery) (VideoCapacitySnapshotSummary, error) {
	if c == nil || c.recovery == nil || c.store == nil || prepared == nil || prepared.snapshot == nil || prepared.summary.Epoch == 0 {
		return VideoCapacitySnapshotSummary{}, ErrVideoGovernanceUnavailable
	}
	// 发布MySQL ready前先把私有prepared、恢复证明、持久门闩与Redis完整快照绑定。
	// 该顺序阻止旧prepared借用新epoch，也阻止Prepare后被删除、替换或加TTL的Redis状态先污染MySQL。
	state, view, err := c.validatePrepared(ctx, proof, prepared)
	if err != nil {
		return VideoCapacitySnapshotSummary{}, err
	}
	if state.State == "ready" {
		// PublishReady在相同ready事实下是只读校验，并额外确认调用者仍持有原始私有证明。
		if err := c.recovery.PublishReady(ctx, proof, prepared.snapshot.Digest(), uint32(prepared.summary.Total)); err != nil {
			return VideoCapacitySnapshotSummary{}, err
		}
		if view.Status == "ready" {
			return prepared.summary, nil
		}
		// DB ready而Redis仍是同快照rebuilding，表示上次在Activate前退出；继续幂等激活。
	} else {
		err = c.recovery.PublishReady(ctx, proof, prepared.snapshot.Digest(), uint32(prepared.summary.Total))
		if err != nil {
			if check := c.recovery.ValidateReady(ctx, prepared.summary.Epoch, prepared.policy, prepared.runID, prepared.snapshot.Digest(), uint32(prepared.summary.Total)); check != nil {
				return VideoCapacitySnapshotSummary{}, errors.Join(err, check)
			}
		}
	}
	view, err = c.store.ActivateRecovery(ctx, prepared.snapshot)
	if err != nil {
		view, err = c.store.InspectRecovery(ctx, prepared.snapshot)
		if err != nil || view.Status != "ready" {
			return VideoCapacitySnapshotSummary{}, ErrVideoCapacityUnavailable
		}
	}
	if view.Status != "ready" || view.Count != prepared.summary.Total {
		return VideoCapacitySnapshotSummary{}, ErrVideoCapacityUnavailable
	}
	if err := c.store.ValidateRunID(ctx, prepared.runID); err != nil {
		return VideoCapacitySnapshotSummary{}, err
	}
	if err := c.recovery.ValidateReady(ctx, prepared.summary.Epoch, prepared.policy, prepared.runID, prepared.snapshot.Digest(), uint32(prepared.summary.Total)); err != nil {
		return VideoCapacitySnapshotSummary{}, err
	}
	return prepared.summary, nil
}

// validatePrepared只读取两侧状态，在任何ready写入前完成全量身份、摘要和阶段核验。
func (c *VideoCapacityRecoveryCoordinator) validatePrepared(ctx context.Context, proof *repository.VideoCapacityRecoveryLease, prepared *VideoCapacityPreparedRecovery) (*repository.VideoCapacityRecoveryState, *VideoCapacityRecoveryView, error) {
	if ctx == nil || proof == nil || prepared.summary.Total < 0 || prepared.summary.Total > 102 || prepared.summary.Queued < 0 || prepared.summary.Running < 0 || prepared.summary.Queued+prepared.summary.Running != prepared.summary.Total || prepared.snapshot.Count() != prepared.summary.Total || proof.Epoch() != prepared.summary.Epoch || prepared.snapshot.epoch != strconv.FormatUint(prepared.summary.Epoch, 10) || prepared.snapshot.epoch != c.store.epoch || prepared.snapshot.policy != prepared.policy || prepared.policy != c.store.policy {
		return nil, nil, ErrVideoGovernanceUnavailable
	}
	state, err := c.recovery.Current(ctx)
	if err != nil || state == nil || state.Epoch != prepared.summary.Epoch || state.PolicyHash != prepared.policy || state.RedisRunID != prepared.runID || (state.State != "recovering" && state.State != "ready") {
		return nil, nil, ErrVideoGovernanceUnavailable
	}
	if err := c.store.ValidateRunID(ctx, prepared.runID); err != nil {
		return nil, nil, err
	}
	view, err := c.store.InspectRecovery(ctx, prepared.snapshot)
	if err != nil || view == nil || view.Count != prepared.summary.Total {
		return nil, nil, ErrVideoCapacityUnavailable
	}
	if state.State == "recovering" {
		if view.Status != "rebuilding" {
			return nil, nil, ErrVideoCapacityUnavailable
		}
		if err := c.recovery.Validate(ctx, proof); err != nil {
			return nil, nil, err
		}
		return state, view, nil
	}
	if (view.Status != "ready" && view.Status != "rebuilding") || state.SnapshotHash != prepared.snapshot.Digest() || state.SnapshotCount != uint32(prepared.summary.Total) || state.ReadyAt.IsZero() {
		return nil, nil, ErrVideoCapacityUnavailable
	}
	if err := c.recovery.ValidateReady(ctx, prepared.summary.Epoch, prepared.policy, prepared.runID, prepared.snapshot.Digest(), uint32(prepared.summary.Total)); err != nil {
		return nil, nil, err
	}
	return state, view, nil
}
