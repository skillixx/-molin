package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type fakeOutboxStore struct {
	events       []model.AIOutboxEvent
	publishedIDs []uint64
	retriedIDs   []uint64
	dead         bool
	markErr      error
}

func (f *fakeOutboxStore) ClaimBatch(context.Context, time.Time, time.Time, int) ([]model.AIOutboxEvent, error) {
	return append([]model.AIOutboxEvent(nil), f.events...), nil
}
func (f *fakeOutboxStore) MarkPublished(_ context.Context, id uint64, _ time.Time, _ time.Time) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.publishedIDs = append(f.publishedIDs, id)
	return nil
}
func (f *fakeOutboxStore) MarkRetry(_ context.Context, id uint64, _ time.Time, _ time.Time, _ string, dead bool) error {
	f.retriedIDs = append(f.retriedIDs, id)
	f.dead = dead
	return nil
}

type fakeOutboxPublisher struct{ err error }

func (f fakeOutboxPublisher) Publish(context.Context, model.AIOutboxEvent) error { return f.err }

type blockingOutboxPublisher struct{}

func (blockingOutboxPublisher) Publish(ctx context.Context, _ model.AIOutboxEvent) error {
	<-ctx.Done()
	return ctx.Err()
}

type nonCooperativeOutboxPublisher struct{ release <-chan struct{} }

func (p nonCooperativeOutboxPublisher) Publish(context.Context, model.AIOutboxEvent) error {
	<-p.release
	return nil
}

func TestOutboxWorkerRetainsFailedEventAndRecovers(t *testing.T) {
	lease := time.Now().Truncate(time.Second)
	store := &fakeOutboxStore{events: []model.AIOutboxEvent{{ID: 7, EventID: "evt-7", RetryCount: 0, LockedAt: &lease}}}
	worker := NewOutboxWorker(store, fakeOutboxPublisher{err: errors.New("rabbit down")})
	if count, err := worker.RunOnce(context.Background(), 10); err != nil || count != 0 {
		t.Fatalf("RabbitMQ 不可用时不得伪造发布成功: count=%d err=%v", count, err)
	}
	if len(store.retriedIDs) != 1 || store.retriedIDs[0] != 7 || store.dead {
		t.Fatalf("事件应保留并重试: %+v", store)
	}
	store.retriedIDs = nil
	worker.publisher = fakeOutboxPublisher{}
	if count, err := worker.RunOnce(context.Background(), 10); err != nil || count != 1 {
		t.Fatalf("RabbitMQ 恢复后应发布成功: count=%d err=%v", count, err)
	}
	if len(store.publishedIDs) != 1 || store.publishedIDs[0] != 7 {
		t.Fatalf("发布标记不正确: %+v", store.publishedIDs)
	}
}

func TestOutboxWorkerMovesExhaustedEventToDead(t *testing.T) {
	lease := time.Now().Truncate(time.Second)
	store := &fakeOutboxStore{events: []model.AIOutboxEvent{{ID: 9, EventID: "evt-9", RetryCount: 17, LockedAt: &lease}}}
	worker := NewOutboxWorker(store, fakeOutboxPublisher{err: errors.New("still down")})
	if _, err := worker.RunOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if !store.dead {
		t.Fatal("达到最大重试次数后应进入 dead 状态")
	}
}

func TestOutboxWorkerKeepsRetryingBeyondTwoHours(t *testing.T) {
	var accumulated time.Duration
	for attempt := uint32(1); attempt < 18; attempt++ {
		accumulated += outboxBackoff(attempt)
	}
	if accumulated < 150*time.Minute {
		t.Fatalf("自动重试窗口不足两小时三十分钟: %s", accumulated)
	}
	lease := time.Now().Truncate(time.Second)
	store := &fakeOutboxStore{events: []model.AIOutboxEvent{{ID: 10, EventID: "evt-10", RetryCount: 16, LockedAt: &lease}}}
	worker := NewOutboxWorker(store, fakeOutboxPublisher{err: errors.New("rabbit still down")})
	if _, err := worker.RunOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if store.dead {
		t.Fatal("覆盖两小时停机窗口前不得进入 dead")
	}
}

func TestOutboxWorkerTimesOutBlockedPublisher(t *testing.T) {
	lease := time.Now().Truncate(time.Second)
	store := &fakeOutboxStore{events: []model.AIOutboxEvent{{ID: 12, EventID: "evt-12", LockedAt: &lease}}}
	worker := NewOutboxWorker(store, blockingOutboxPublisher{})
	worker.publishTimeout = 10 * time.Millisecond
	started := time.Now()
	if _, err := worker.RunOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second || len(store.retriedIDs) != 1 {
		t.Fatalf("发布卡死必须在有限时间内进入重试: elapsed=%s retried=%v", time.Since(started), store.retriedIDs)
	}
}

func TestOutboxWorkerTimesOutNonCooperativePublisher(t *testing.T) {
	lease := time.Now().Truncate(time.Second)
	release := make(chan struct{})
	store := &fakeOutboxStore{events: []model.AIOutboxEvent{{ID: 15, EventID: "evt-15", LockedAt: &lease}}}
	worker := NewOutboxWorker(store, nonCooperativeOutboxPublisher{release: release})
	worker.publishTimeout = 10 * time.Millisecond
	started := time.Now()
	if _, err := worker.RunOnce(context.Background(), 10); err != nil {
		close(release)
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(context.Background(), 10); err != nil {
		close(release)
		t.Fatal(err)
	}
	if len(worker.publishSlots) != 1 {
		close(release)
		t.Fatalf("连续超时不得累积不受控发布 goroutine: slots=%d", len(worker.publishSlots))
	}
	close(release)
	if time.Since(started) > time.Second || len(store.retriedIDs) != 2 {
		t.Fatalf("不响应 context 的发布器也不能阻塞 Worker: elapsed=%s retried=%v", time.Since(started), store.retriedIDs)
	}
}

func TestOutboxWorkerBlocksLaterEventOfFailedAggregate(t *testing.T) {
	lease := time.Now().Truncate(time.Second)
	store := &fakeOutboxStore{events: []model.AIOutboxEvent{
		{ID: 13, EventID: "evt-held", AggregateType: "ai_request", AggregateID: "req-order", LockedAt: &lease},
		{ID: 14, EventID: "evt-settled", AggregateType: "ai_request", AggregateID: "req-order", LockedAt: &lease},
	}}
	worker := NewOutboxWorker(store, fakeOutboxPublisher{err: errors.New("rabbit down")})
	if _, err := worker.RunOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if len(store.retriedIDs) != 1 || store.retriedIDs[0] != 13 {
		t.Fatalf("前序事件失败后不得尝试同聚合后续事件: %v", store.retriedIDs)
	}
}

func TestOutboxWorkerRejectsLostLeaseResult(t *testing.T) {
	lease := time.Now().Truncate(time.Second)
	store := &fakeOutboxStore{
		events:  []model.AIOutboxEvent{{ID: 11, EventID: "evt-11", LockedAt: &lease}},
		markErr: repository.ErrOutboxLeaseLost,
	}
	worker := NewOutboxWorker(store, fakeOutboxPublisher{})
	if _, err := worker.RunOnce(context.Background(), 10); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatalf("旧 Worker 丢失租约后不得覆盖新拥有者: %v", err)
	}
}
