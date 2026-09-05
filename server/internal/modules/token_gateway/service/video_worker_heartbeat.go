package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// VideoWorkerExecution由可信任务解析器从原账本建立，不能直接把HTTP/MQ字段当作归属证明。
type VideoWorkerExecution struct {
	TaskID   string
	Owner    repository.VideoOwner
	WorkerID string
	Stage    video.TaskStage
}

// VideoWorkerLeaseRunner只管理一个阶段的执行租约；不替代Redis容量、输入租约或财务门禁。
type VideoWorkerLeaseRunner struct {
	leases *repository.VideoWorkerLeaseRepository
}

func NewVideoWorkerLeaseRunner(db *gorm.DB) (*VideoWorkerLeaseRunner, error) {
	if db == nil {
		return nil, repository.ErrVideoWorkerLeaseUnavailable
	}
	return &VideoWorkerLeaseRunner{leases: repository.NewVideoWorkerLeaseRepository(db)}, nil
}

// Execute在工作开始前认领，固定10秒续期；必须等工作退出和心跳停止后才尝试释放。
// 工作必须遵守context并在持久化/外部IO边界复核围栏，不能把取消视作能强制杀死任意Go代码。
func (r *VideoWorkerLeaseRunner) Execute(ctx context.Context, command VideoWorkerExecution, work func(context.Context) error) (result error) {
	if r == nil || r.leases == nil || ctx == nil || work == nil {
		return repository.ErrVideoWorkerLeaseUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	proof, err := r.leases.Claim(ctx, command.TaskID, command.Owner, command.WorkerID, string(command.Stage))
	if err != nil {
		return err
	}
	execution, cancelWork := context.WithCancel(ctx)
	heartbeat, stopHeartbeat := context.WithCancel(execution)
	finished := make(chan error, 1)
	go func() { finished <- r.heartbeat(heartbeat, proof, cancelWork) }()
	defer func() {
		// 不传播panic正文，避免Provider正文或Prompt经上层恢复日志泄露；失败不能形成成功ACK。
		if recover() != nil {
			result = video.ErrTaskHandlerUncertain
		}
		stopHeartbeat()
		heartbeatErr := <-finished
		// 在自己的清理cancel之前读取实际取消原因，正常完成不能被误判为context失败。
		executionErr := execution.Err()
		cancelWork()
		// 仅解除技术占用；原工作已经退出，且Release仍在数据库中核对原代次与有效期。
		// 若租约已过期/被接管或数据库不可用，保留原事实并返回失败，不能释放新持有者。
		cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelCleanup()
		releaseErr := r.leases.Release(cleanup, proof)
		result = errors.Join(result, executionErr, heartbeatErr, releaseErr)
	}()
	if err := execution.Err(); err != nil {
		return err
	}
	return work(repository.WithVideoWorkerLease(execution, proof))
}

// 同一持有者的心跳不换代，原私有证明仍代表同一执行身份；有效期限始终以数据库当前值为准。
func (r *VideoWorkerLeaseRunner) heartbeat(ctx context.Context, proof *repository.VideoWorkerLease, cancelWork context.CancelFunc) (result error) {
	defer func() {
		if recover() != nil {
			cancelWork()
			result = video.ErrTaskHandlerUncertain
		}
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if ctx.Err() != nil {
				return nil
			}
			if _, err := r.leases.Renew(ctx, proof); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				// 续期结果未知也不能继续工作；不盲目重试心跳来掩盖租约已经失效。
				cancelWork()
				return err
			}
		}
	}
}
