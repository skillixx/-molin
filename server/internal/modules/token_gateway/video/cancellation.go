package video

import "context"

// VideoCancellationLedger 先持久化取消意图，拒绝/不支持/未知的Provider响应不能清除此事实。
// 采用独立接口保持旧自定义Ledger兼容；财务模式缺少此能力时必须失败关闭。
type VideoCancellationLedger interface {
	RequestCancellation(context.Context, string) (GatewayTask, error)
}

func (l *InMemoryVideoTaskLedger) RequestCancellation(ctx context.Context, taskID string) (GatewayTask, error) {
	if err := ctx.Err(); err != nil {
		return GatewayTask{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	task, ok := l.tasks[taskID]
	if !ok {
		return GatewayTask{}, ErrGatewayTaskNotFound
	}
	if !taskSafeTerminal(task.Status) && task.CancelRequestedAt == nil {
		now := l.now().UTC()
		task.CancelRequestedAt = &now
		task.Version++
		l.tasks[taskID] = cloneGatewayTask(task)
	}
	return cloneGatewayTask(task), nil
}
