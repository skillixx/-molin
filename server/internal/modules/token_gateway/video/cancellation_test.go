package video

import (
	"context"
	"errors"
	"testing"
)

// 仅替换外部取消响应，保留真实Fake提交和网关状态机。
type videoCancelMissingConfirmation struct{ VideoProviderAdapter }

type videoQueuedQuery struct{ VideoProviderAdapter }

func (a videoQueuedQuery) Query(_ context.Context, r QueryRequest) (QueryResult, error) {
	return QueryResult{ProviderTaskID: r.ProviderTaskID, Status: ProviderTaskQueued}, nil
}

func TestVideoPollQueuedRetainsSubmittedTask(t *testing.T) {
	f := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	if _, err := f.gateway.Submit(context.Background(), f.taskID); err != nil {
		t.Fatal(err)
	}
	f.gateway.deps.Provider = videoQueuedQuery{f.adapter}
	r, err := f.gateway.Poll(context.Background(), f.taskID)
	if err != nil || r.Status != TaskSubmitted {
		t.Fatalf("Provider仍排队不能误判失败: %s %v", r.Status, err)
	}
}

func (a videoCancelMissingConfirmation) Cancel(ctx context.Context, r CancelRequest) (QueryResult, error) {
	q, err := a.VideoProviderAdapter.Cancel(ctx, r)
	q.Confirmation = nil
	return q, err
}

func TestVideoCancellationWithoutCostConfirmationStaysHeld(t *testing.T) {
	f := newGatewayFixture(t, OperationTextToVideo, FakeVideoSuccess, FakeVideoModerationAllow, FakeVideoLabelSuccess)
	// 该用例只验证取消回执门禁，先完成原Fake提交；持久化提交租约由独立MySQL测试覆盖。
	if _, err := f.gateway.Submit(context.Background(), f.taskID); err != nil {
		t.Fatal(err)
	}
	task, err := f.gateway.Query(context.Background(), f.taskID)
	if err != nil {
		t.Fatal(err)
	}
	task.DeferDelivery = true
	l := NewInMemoryVideoTaskLedger()
	if err := l.Seed(task); err != nil {
		t.Fatal(err)
	}
	f.gateway.deps.Ledger = l
	f.gateway.deps.Provider = videoCancelMissingConfirmation{f.adapter}
	r, err := f.gateway.Cancel(context.Background(), f.taskID)
	if err == nil || r.Status != TaskPendingReconcile || r.LeaseReleased {
		t.Fatalf("缺少确认不得形成取消终态或释放租约: status=%s err=%v", r.Status, err)
	}
}

// Provider拒绝或不支持取消并不等于任务免费终止；原任务继续完成且Submit只有一次。
func TestVideoCancellationRefusalRetainsOriginalTask(t *testing.T) {
	for _, mode := range []FakeVideoMode{FakeVideoCancelRejected, FakeVideoCancelUnsupported} {
		t.Run(string(mode), func(t *testing.T) {
			f := newGatewayFixture(t, OperationTextToVideo, mode, FakeVideoModerationAllow, FakeVideoLabelSuccess)
			if _, err := f.gateway.Submit(context.Background(), f.taskID); err != nil {
				t.Fatal(err)
			}
			before, err := f.gateway.Query(context.Background(), f.taskID)
			if err != nil {
				t.Fatal(err)
			}
			r, err := f.gateway.Cancel(context.Background(), f.taskID)
			if (!errors.Is(err, ErrProviderCancelRejected) && !errors.Is(err, ErrProviderCancelUnsupported)) || r.Status != TaskSubmitted || r.CancelRequestedAt == nil {
				t.Fatal("取消拒绝必须保留意图与执行状态")
			}
			for i := 0; i < 2; i++ {
				if _, err := f.gateway.Poll(context.Background(), f.taskID); err != nil {
					t.Fatal(err)
				}
			}
			r, err = f.gateway.FetchAndFinalize(context.Background(), f.taskID)
			if err != nil || r.Status != TaskSucceeded || r.ProviderTaskID != before.ProviderTaskID || r.CancelRequestedAt == nil {
				t.Fatalf("取消拒绝后应继续原任务: %v", err)
			}
		})
	}
}
