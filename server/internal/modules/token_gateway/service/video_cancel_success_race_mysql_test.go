package service

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 模拟先前轮询已获得成功回执但尚未返回，期间取消接口却声称无产物；两份真实入口结果必须留下冲突。
type videoG5LateSuccessPoll struct {
	videogateway.VideoProviderAdapter
	entered, release chan struct{}
	sameEvent        bool
	operation        string
}

func (a *videoG5LateSuccessPoll) Query(ctx context.Context, r videogateway.QueryRequest) (videogateway.QueryResult, error) {
	q, err := a.VideoProviderAdapter.Query(ctx, r)
	if err == nil && q.Status == videogateway.ProviderTaskProcessing {
		q, err = a.VideoProviderAdapter.Query(ctx, r)
	}
	if err != nil {
		return q, err
	}
	close(a.entered)
	select {
	case <-a.release:
		return q, nil
	case <-ctx.Done():
		return videogateway.QueryResult{}, ctx.Err()
	}
}
func (a *videoG5LateSuccessPoll) Cancel(_ context.Context, r videogateway.CancelRequest) (videogateway.QueryResult, error) {
	event := "cancel-" + r.ProviderTaskID
	if a.sameEvent {
		event = "final-" + r.ProviderTaskID
	}
	c := &videogateway.ProviderCostConfirmation{ProviderCode: a.Name(), ProviderTaskID: r.ProviderTaskID, ExternalEventID: event, Operation: a.operation, Outcome: videogateway.ProviderTaskCancelled, Quantity: decimal.Zero, UnitPrice: decimal.Zero, Amount: decimal.Zero, Currency: "CNY"}
	return videogateway.QueryResult{ProviderTaskID: r.ProviderTaskID, Status: videogateway.ProviderTaskCancelled, Confirmation: c}, nil
}

func TestVideoG5CancelMySQLLatePollSuccessCannotHideConflict(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, sameEvent := range []bool{true, false} {
		for _, releasedFirst := range []bool{false, true} {
			name := map[bool]string{true: "same_event", false: "new_event"}[sameEvent] + "/" + map[bool]string{true: "already_released", false: "holding"}[releasedFirst]
			t.Run(name, func(t *testing.T) {
				f, _, base := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, videogateway.FakeVideoSuccess)
				a := &videoG5LateSuccessPoll{VideoProviderAdapter: base, entered: make(chan struct{}), release: make(chan struct{}), sameEvent: sameEvent, operation: model.AIVideoOperationImageToVideo}
				g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader), Provider: a})
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := g.Poll(ctx, f.command.TaskID); done <- err }()
				select {
				case <-a.entered:
				case <-ctx.Done():
					t.Fatal("成功轮询必须先在途")
				}
				if r, err := g.Cancel(ctx, f.command.TaskID); err != nil || r.Status != videogateway.TaskCancelled {
					t.Fatalf("先收到明确取消: %s %v", r.Status, err)
				}
				if releasedFirst {
					if _, err := f.service.ReleaseUnserviceable(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
				}
				close(a.release)
				if err := <-done; err == nil {
					t.Fatal("晚到相反成功确认必须拒绝")
				}
				if _, err := f.service.ReleaseUnserviceable(ctx, f.command.TaskID, f.owner); err == nil {
					t.Fatal("晚到成功冲突不能被旧无产物证明掩盖")
				}
				task, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
				wantBilling := model.AIBillingSettlementPending
				if releasedFirst {
					wantBilling = model.AIBillingReleased
				} else {
					assertVideoG5ReleaseStillHeld(t, f)
				}
				if err != nil || task.Status != model.AIImageTaskCancelled || task.BillingStatus != wantBilling {
					t.Fatal("相反回执不能改写原终态")
				}
				var n int64
				if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND event_type='provider_result_conflict'", task.ID).Count(&n).Error; err != nil || n != 1 {
					t.Fatalf("必须持久化相反确认观察: %d %v", n, err)
				}
				if r, err := NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner); err != nil || r.Passed {
					t.Fatalf("冲突不能仍然对账PASS: %+v %v", r, err)
				}
				if base.SubmitCalls() != 1 {
					t.Fatal("晚到成功不能再次Submit")
				}
			})
		}
	}
}
