package video

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 在公共Ledger边界控制两个Worker同时竞争，计数放在Fake去重之前。
type videoSubmissionBarrier struct {
	VideoTaskLedger
	arrivals atomic.Int32
	gate     chan struct{}
}

func (l *videoSubmissionBarrier) Advance(ctx context.Context, id string, v uint64, to TaskStatus, source, reason string, m TaskMutation) (GatewayTask, error) {
	if to == TaskSubmitting {
		if l.arrivals.Add(1) == 2 {
			close(l.gate)
		}
		select {
		case <-l.gate:
		case <-ctx.Done():
			return GatewayTask{}, ctx.Err()
		}
	}
	return l.VideoTaskLedger.Advance(ctx, id, v, to, source, reason, m)
}

type videoSubmitEntryCounter struct {
	VideoProviderAdapter
	entries atomic.Int32
}

func (a *videoSubmitEntryCounter) Submit(ctx context.Context, r SubmitRequest) (SubmitResult, error) {
	a.entries.Add(1)
	return a.VideoProviderAdapter.Submit(ctx, r)
}

func TestVideoSubmittingCASLoserNeverCallsProvider(t *testing.T) {
	f := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	queued, err := f.gateway.Query(context.Background(), f.taskID)
	if err != nil {
		t.Fatal(err)
	}
	queued.Status = TaskQueued
	base := NewInMemoryVideoTaskLedger()
	if err := base.Seed(queued); err != nil {
		t.Fatal(err)
	}
	l := &videoSubmissionBarrier{VideoTaskLedger: base, gate: make(chan struct{})}
	a := &videoSubmitEntryCounter{VideoProviderAdapter: f.adapter}
	f.gateway.deps.Ledger, f.gateway.deps.Provider = l, a
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = f.gateway.Submit(ctx, f.taskID) }()
	}
	wg.Wait()
	if l.arrivals.Load() != 2 {
		t.Fatal("两个Worker必须确实到达提交CAS竞争点")
	}
	if a.entries.Load() != 1 {
		t.Fatalf("只有赢得提交CAS的Worker可进入Provider，实际=%d", a.entries.Load())
	}
}

type videoBlockingSubmit struct {
	VideoProviderAdapter
	entered chan struct{}
	release chan struct{}
	entries atomic.Int32
}

func (a *videoBlockingSubmit) Submit(ctx context.Context, r SubmitRequest) (SubmitResult, error) {
	if a.entries.Add(1) == 1 {
		close(a.entered)
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return SubmitResult{}, ctx.Err()
	}
	return a.VideoProviderAdapter.Submit(ctx, r)
}

// 后到的Submit重试不能抢先判定原RPC已失联，否则原返回任务ID将无法绑定。
func TestVideoSubmitRetryPreservesInflightProviderBinding(t *testing.T) {
	f := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	a := &videoBlockingSubmit{VideoProviderAdapter: f.adapter, entered: make(chan struct{}), release: make(chan struct{})}
	f.gateway.deps.Provider = a
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan GatewayTask, 1)
	go func() { r, _ := f.gateway.Submit(ctx, f.taskID); done <- r }()
	select {
	case <-a.entered:
	case <-ctx.Done():
		t.Fatal("原提交未到达Provider")
	}
	_, _ = f.gateway.Submit(ctx, f.taskID)
	close(a.release)
	r := <-done
	if a.entries.Load() != 1 || r.Status != TaskSubmitted || r.ProviderTaskID == "" {
		t.Fatalf("原RPC返回后仍应绑定同一任务: entries=%d status=%s bound=%t", a.entries.Load(), r.Status, r.ProviderTaskID != "")
	}
}
