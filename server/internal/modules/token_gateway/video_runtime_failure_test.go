package token_gateway

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	video "molin/server/internal/modules/token_gateway/video"
)

type videoRuntimeTestRunner struct {
	run func(context.Context, video.TaskStage, int, video.TaskMessageHandler) error
}

func (r videoRuntimeTestRunner) RunWorkers(ctx context.Context, stage video.TaskStage, workers int, handler video.TaskMessageHandler) error {
	return r.run(ctx, stage, workers, handler)
}

type videoRuntimeInvalidMessage struct{ digest string }

func (e videoRuntimeInvalidMessage) Error() string { return video.ErrTaskMessageInvalid.Error() }
func (e videoRuntimeInvalidMessage) Is(target error) bool {
	return target == video.ErrTaskMessageInvalid
}
func (e videoRuntimeInvalidMessage) TaskMessageBodySHA256() string { return e.digest }

func TestVideoG7RuntimePoisonStopsWithoutHotRestart(t *testing.T) {
	var calls, blocks atomic.Int32
	runtime := &VideoRuntime{
		components: map[string]VideoRuntimeComponentHealth{},
		workerRunner: videoRuntimeTestRunner{run: func(context.Context, video.TaskStage, int, video.TaskMessageHandler) error {
			calls.Add(1)
			return videoRuntimeInvalidMessage{digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		}},
		poisonState: func(context.Context, video.TaskStage) (bool, error) { return false, nil },
		poisonBlock: func(context.Context, video.TaskStage, string) error { blocks.Add(1); return nil },
		retryDelay:  time.Millisecond,
	}
	runtime.runConsumerGroup(context.Background(), video.TaskSubmit, 2)
	if calls.Load() != 1 || blocks.Load() != 1 {
		t.Fatalf("毒消息必须只执行一次并持久熔断: calls=%d blocks=%d", calls.Load(), blocks.Load())
	}
	state := runtime.HealthSnapshot()["consumer_submit"]
	if state.Up || state.FailureCount != 1 || state.LastFailureAt.IsZero() {
		t.Fatalf("熔断必须留下失败健康事实: %+v", state)
	}
}

func TestVideoG7RuntimePersistentPoisonFenceBlocksStartup(t *testing.T) {
	var calls atomic.Int32
	runtime := &VideoRuntime{
		components: map[string]VideoRuntimeComponentHealth{},
		workerRunner: videoRuntimeTestRunner{run: func(context.Context, video.TaskStage, int, video.TaskMessageHandler) error {
			calls.Add(1)
			return nil
		}},
		poisonState: func(context.Context, video.TaskStage) (bool, error) { return true, nil },
		poisonBlock: func(context.Context, video.TaskStage, string) error { return nil },
		retryDelay:  time.Millisecond,
	}
	runtime.runConsumerGroup(context.Background(), video.TaskPoll, 2)
	if calls.Load() != 0 {
		t.Fatal("进程重启后持久熔断未解除前不得重新消费")
	}
	if state := runtime.HealthSnapshot()["consumer_poll"]; state.Up || state.FailureCount != 1 {
		t.Fatalf("持久熔断必须明确降级健康状态: %+v", state)
	}
}

func TestVideoG7RuntimeTransientFailureRetriesAndRecordsHealth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	runtime := &VideoRuntime{
		components: map[string]VideoRuntimeComponentHealth{},
		workerRunner: videoRuntimeTestRunner{run: func(ctx context.Context, _ video.TaskStage, _ int, _ video.TaskMessageHandler) error {
			if calls.Add(1) == 1 {
				return video.ErrTaskBrokerUnavailable
			}
			<-ctx.Done()
			return ctx.Err()
		}},
		poisonState: func(context.Context, video.TaskStage) (bool, error) { return false, nil },
		poisonBlock: func(context.Context, video.TaskStage, string) error { return nil },
		retryDelay:  time.Millisecond,
	}
	done := make(chan struct{})
	go func() { runtime.runConsumerGroup(ctx, video.TaskFetch, 2); close(done) }()
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("瞬态失败重试未响应取消")
	}
	state := runtime.HealthSnapshot()["consumer_fetch"]
	if calls.Load() != 2 || state.FailureCount != 1 || state.LastFailureAt.IsZero() || state.LastSuccessAt.IsZero() {
		t.Fatalf("瞬态失败必须记录降级后重连: calls=%d state=%+v", calls.Load(), state)
	}
}

func TestVideoG7RuntimeShutdownTimeoutPreservesLifecycle(t *testing.T) {
	runtime := &VideoRuntime{started: true, cancel: func() {}, done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runtime.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("收口超时必须返回调用方期限: %v", err)
	}
	if !runtime.started || runtime.done == nil || runtime.cancel == nil {
		t.Fatal("超时后必须保留运行生命周期，供调用方继续等待或下实例接管")
	}
}
