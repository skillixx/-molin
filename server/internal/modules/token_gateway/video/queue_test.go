package video

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDeterministicTaskQueueIsIdempotentAndRecoversExpiredLease(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	queue := NewDeterministicTaskQueue(func() time.Time { return now })
	for index := 0; index < 100; index++ {
		if err := queue.Enqueue("vid_task_queue"); err != nil {
			t.Fatal(err)
		}
	}
	if queue.Pending() != 1 {
		t.Fatalf("重复投递只能保留一个待办: %d", queue.Pending())
	}
	first, ok := queue.Claim(context.Background(), "worker-a", time.Minute)
	if !ok || first.TaskID != "vid_task_queue" {
		t.Fatalf("首次领取失败: %+v", first)
	}
	if _, ok := queue.Claim(context.Background(), "worker-b", time.Minute); ok {
		t.Fatal("租约有效期内不得重复领取")
	}
	now = now.Add(2 * time.Minute)
	recovered := queue.RecoverExpired()
	if recovered != 1 {
		t.Fatalf("崩溃租约必须恢复: %d", recovered)
	}
	second, ok := queue.Claim(context.Background(), "worker-b", time.Minute)
	if !ok || second.Attempt != 2 {
		t.Fatalf("恢复后必须递增尝试次数: %+v", second)
	}
	if err := queue.Ack(second); err != nil {
		t.Fatal(err)
	}
	if queue.Pending() != 0 {
		t.Fatalf("ACK后不得再次投递: %d", queue.Pending())
	}
}

func TestDeterministicTaskQueueCrashRecoveryRunsGatewayWorker(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	queue := NewDeterministicTaskQueue(func() time.Time { return now })
	fixture := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	if err := queue.Enqueue(fixture.taskID); err != nil {
		t.Fatal(err)
	}
	crashed, ok := queue.Claim(context.Background(), "worker-crashed", time.Second)
	if !ok {
		t.Fatal("崩溃Worker必须先获得租约")
	}
	// 模拟领取后进程退出：既不执行任务，也不ACK/NACK。
	now = crashed.ExpiresAt.Add(time.Nanosecond)
	if queue.RecoverExpired() != 1 {
		t.Fatal("过期租约必须被恢复")
	}
	recovered, ok := queue.Claim(context.Background(), "worker-recovered", time.Second)
	if !ok || recovered.Attempt != 2 {
		t.Fatalf("恢复Worker必须获得第二次租约: %+v", recovered)
	}
	result, err := fixture.submit.Run(context.Background(), recovered.TaskID)
	if err != nil || result.Status != TaskSubmitted {
		t.Fatalf("恢复后必须实际推进共享任务: task=%+v err=%v", result, err)
	}
	if err := queue.Ack(recovered); err != nil || queue.Pending() != 0 {
		t.Fatalf("执行成功后必须ACK: pending=%d err=%v", queue.Pending(), err)
	}
}

func TestDeterministicTaskQueueAllowsOnlyOneConcurrentClaim(t *testing.T) {
	queue := NewDeterministicTaskQueue(time.Now)
	if err := queue.Enqueue("vid_task_concurrent"); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	var lock sync.Mutex
	claimed := 0
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			if _, ok := queue.Claim(context.Background(), "worker", time.Minute); ok {
				lock.Lock()
				claimed++
				lock.Unlock()
			}
		}(index)
	}
	wait.Wait()
	if claimed != 1 {
		t.Fatalf("100并发只能一个消费者领取: %d", claimed)
	}
}
