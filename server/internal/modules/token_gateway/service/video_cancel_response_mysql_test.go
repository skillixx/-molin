package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 仅修改外部取消回执，数据库与确认/财务逻辑保持真实实现。
type videoG5CancelResponse struct {
	videogateway.VideoProviderAdapter
	mode string
}

func (a videoG5CancelResponse) Cancel(ctx context.Context, r videogateway.CancelRequest) (videogateway.QueryResult, error) {
	q, err := a.VideoProviderAdapter.Cancel(ctx, r)
	if err != nil {
		return q, err
	}
	return a.mutate(q), nil
}

func (a videoG5CancelResponse) Query(ctx context.Context, r videogateway.QueryRequest) (videogateway.QueryResult, error) {
	q, err := a.VideoProviderAdapter.Query(ctx, r)
	if err != nil || q.Status == videogateway.ProviderTaskProcessing {
		return q, err
	}
	return a.mutate(q), nil
}

func (a videoG5CancelResponse) mutate(q videogateway.QueryResult) videogateway.QueryResult {
	switch a.mode {
	case "nonzero":
		q.Confirmation.Quantity = decimal.NewFromInt(1)
		q.Confirmation.UnitPrice = decimal.RequireFromString("0.06")
		q.Confirmation.Amount = decimal.RequireFromString("0.06")
	case "missing":
		q.Confirmation = nil
	case "has_content":
		q.Content = &videogateway.ControlledContentRef{ProviderTaskID: q.ProviderTaskID, ContentID: "unexpected-content", MediaType: "video/mp4"}
	case "wrong_provider":
		q.Confirmation.ProviderCode = "wrong-provider"
	case "wrong_operation":
		q.Confirmation.Operation = model.AIVideoOperationTextToVideo
	case "wrong_task":
		q.ProviderTaskID = "wrong-task"
	case "empty":
		q.Status = ""
	}
	return q
}

func TestVideoG5CancelMySQLInvalidOrNonzeroConfirmation(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, entry := range []string{"cancel", "poll"} {
		for _, mode := range []string{"nonzero", "has_content", "missing", "wrong_provider", "wrong_operation", "wrong_task", "empty"} {
			t.Run(entry+"/"+mode, func(t *testing.T) {
				providerMode := videogateway.FakeVideoSuccess
				if entry == "poll" {
					providerMode = videogateway.FakeVideoProviderCancelled
				}
				f, _, a := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, providerMode)
				g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader), Provider: videoG5CancelResponse{VideoProviderAdapter: a, mode: mode}})
				var result videogateway.GatewayTask
				var err error
				if entry == "poll" {
					if _, err := g.Poll(context.Background(), f.command.TaskID); err != nil {
						t.Fatal(err)
					}
					result, err = g.Poll(context.Background(), f.command.TaskID)
				} else {
					result, err = g.Cancel(context.Background(), f.command.TaskID)
				}
				if err == nil || result.Status != videogateway.TaskPendingReconcile || (entry == "cancel" && result.CancelRequestedAt == nil) {
					t.Fatalf("不能确认免费无产物取消时应待核对: state=%s err=%v", result.Status, err)
				}
				task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
				if err != nil || task.BillingStatus != model.AIBillingSettlementPending || task.DeliveryStatus != model.AIDeliveryPending {
					t.Fatalf("未知取消不得提前终结财务: %v", err)
				}
				if _, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner); err == nil {
					t.Fatal("待核对不能释放")
				}
				facts, err := repository.NewVideoUsageRepository(db).ListForTask(context.Background(), f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "nonzero" || mode == "has_content" {
					if len(facts) != 2 {
						t.Fatal("非零成本确认必须保留")
					}
					for _, f := range facts {
						want := "0.06000000"
						if mode == "has_content" {
							want = "0.00000000"
						}
						if f.RecordKind == model.AIUsageCostLine && (f.Amount == nil || f.Amount.StringFixed(8) != want) {
							t.Fatal("成本不能被伪记为0")
						}
					}
				} else if len(facts) != 0 {
					t.Fatal("无效确认不得产生Provider成本事实")
				}
				if a.SubmitCalls() != 1 {
					t.Fatal("未知取消不能重新提交")
				}
			})
		}
	}
}

// 零成本不是“无产物”证明：有产物冲突后即便收到取消终态，也不能仅凭尚未归档资产而退款。
func TestVideoG5CancelMySQLZeroCostAloneCannotAuthorizeRelease(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, _, a := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, videogateway.FakeVideoSuccess)
	g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader), Provider: videoG5CancelResponse{VideoProviderAdapter: a, mode: "has_content"}})
	if _, err := g.Cancel(context.Background(), f.command.TaskID); err == nil {
		t.Fatal("有产物取消需先进入待核对")
	}
	repo := repository.NewVideoTaskRepository(db)
	task, err := repo.FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: task.PublicID, Owner: f.owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskCancelled, Progress: 100, EventID: f.command.RequestID + "_late_cancelled", Source: "reconciler", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner); err == nil {
		t.Fatal("零成本加后补终态不能证明无产物")
	}
	e := model.AIGatewayTaskEvent{EventID: "vg5_" + videoBillingDigest(f.command.RequestID+":no_product"), EventType: "provider_no_product_confirmed", Source: "worker", CreatedAt: time.Now()}
	if err := repository.NewVideoTaskEventRepository(db).Append(context.Background(), f.command.TaskID, f.owner, e); err == nil {
		t.Fatal("普通事件追加不能伪造无产物证据")
	}
	var cost model.VideoUsageItem
	if err := db.Where("request_id=? AND source='provider_cost'", f.command.RequestID).First(&cost).Error; err != nil {
		t.Fatal(err)
	}
	var confirmation model.VideoFinancialEvent
	if cost.EvidenceEventID == nil || db.First(&confirmation, *cost.EvidenceEventID).Error != nil {
		t.Fatal("缺少原确认")
	}
	e.TaskID, e.UserID, e.ProjectID = task.ID, task.UserID, task.ProjectID
	if err := db.Create(&model.VideoFinancialEvent{AIGatewayTaskEvent: e, FactSHA256: confirmation.FactSHA256}).Error; err == nil {
		t.Fatal("有冲突时，即使摘要正确也不能通过SQL补造无产物证明")
	}
}

type videoG5DualCancel struct {
	videogateway.VideoProviderAdapter
	entries         atomic.Int32
	entered         chan struct{}
	first, second   chan struct{}
	firstHasContent bool
}

func (a *videoG5DualCancel) Cancel(ctx context.Context, r videogateway.CancelRequest) (videogateway.QueryResult, error) {
	n := a.entries.Add(1)
	q, err := a.VideoProviderAdapter.Cancel(ctx, r)
	if err != nil {
		return q, err
	}
	if (n == 1) == a.firstHasContent {
		q.Content = &videogateway.ControlledContentRef{ProviderTaskID: r.ProviderTaskID, ContentID: "conflicting-content", MediaType: "video/mp4"}
	}
	a.entered <- struct{}{}
	gate := a.first
	if n != 1 {
		gate = a.second
	}
	select {
	case <-gate:
		return q, nil
	case <-ctx.Done():
		return videogateway.QueryResult{}, ctx.Err()
	}
}

// 相同成本摘要的两个在途响应也可能对产物存在性矛盾；无论到达顺序都不能自动退款。
func TestVideoG5CancelMySQLContradictoryInflightReplies(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, firstHasContent := range []bool{true, false} {
		t.Run(map[bool]string{true: "content_first", false: "no_product_first"}[firstHasContent], func(t *testing.T) {
			f, _, base := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, videogateway.FakeVideoSuccess)
			a := &videoG5DualCancel{VideoProviderAdapter: base, entered: make(chan struct{}, 2), first: make(chan struct{}), second: make(chan struct{}), firstHasContent: firstHasContent}
			g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader), Provider: a})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			done := make(chan struct{}, 2)
			for i := 0; i < 2; i++ {
				go func() { _, _ = g.Cancel(ctx, f.command.TaskID); done <- struct{}{} }()
				select {
				case <-a.entered:
				case <-ctx.Done():
					t.Fatal("两个取消必须确实同时在途")
				}
			}
			close(a.first)
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("首个响应未完成")
			}
			close(a.second)
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("第二响应未完成")
			}
			if _, err := f.service.ReleaseUnserviceable(ctx, f.command.TaskID, f.owner); err == nil {
				t.Fatal("相同成本但产物矛盾的在途回复不能授权退款")
			}
			assertVideoG5ReleaseStillHeld(t, f)
			if base.SubmitCalls() != 1 {
				t.Fatal("矛盾响应不能重新Submit")
			}
		})
	}
}

// 已观察到矛盾后既不能新扣费，也不能在已交付状态下继续通过读取对账；原财务终态不回退。
func TestVideoG5CancelMySQLLateConflictBlocksSettlementAndRead(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, phase := range []string{"before_settle", "after_delivery"} {
		t.Run(phase, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			prepareVideoG5I2V(t, &f)
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			g, a := runVideoG5ReadyFixture(t, f)
			if phase == "after_delivery" {
				if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
					t.Fatal(err)
				}
				if _, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner); err != nil {
					t.Fatal(err)
				}
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			c := videogateway.ProviderCostConfirmation{ProviderCode: *task.ProviderCode, ProviderTaskID: *task.ProviderTaskID, Operation: *task.Operation, ExternalEventID: "late-conflicting-cancel", Outcome: videogateway.ProviderTaskCancelled, Quantity: decimal.Zero, UnitPrice: decimal.Zero, Amount: decimal.Zero, Currency: "CNY"}
			ledger := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
			if err := ledger.RecordProviderResultConflict(context.Background(), f.command.TaskID, c); err != nil {
				t.Fatal(err)
			}
			if phase == "before_settle" {
				if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err == nil {
					t.Fatal("已存在Provider冲突不能继续扣费")
				}
				assertVideoG5ReleaseStillHeld(t, f)
			} else {
				if _, err := g.ReadContent(context.Background(), f.command.TaskID, 0, 1); err == nil {
					t.Fatal("晚到Provider冲突必须阻断继续读取")
				}
				current, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
				if err != nil || current.BillingStatus != model.AIBillingSettled || current.Status != model.AIImageTaskSucceeded {
					t.Fatal("不能因晚到相反回执覆写原财务终态")
				}
			}
			if a.SubmitCalls() != 1 {
				t.Fatal("冲突不得重提Provider")
			}
		})
	}
}
