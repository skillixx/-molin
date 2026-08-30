package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 结果未知必须落到完整的持久化待核对链，不仅是一个禁止交付的执行状态。
func TestVideoG5UnknownMySQLCreatesPendingAndUniqueCompensation(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, g, a := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, videogateway.FakeVideoResultUnknown)
	if _, err := g.Poll(context.Background(), f.command.TaskID); err != nil {
		t.Fatal(err)
	}
	_, _ = g.Poll(context.Background(), f.command.TaskID)
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || task.Status != model.AIImageTaskPendingReconcile || task.BillingStatus != model.AIBillingSettlementPending {
		t.Fatalf("未知结果需要执行与计费待核对: %v", err)
	}
	job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || job.Status != "pending" || job.OriginErrorCode != "provider_unknown" {
		t.Fatalf("缺少唯一待核对任务: %v", err)
	}
	var events []model.AIOutboxEvent
	if err := db.Where("aggregate_id=? AND aggregate_type='video_request'", f.command.RequestID).Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("应只有H/P/C三条事件: %d", len(events))
	}
	facts, err := repository.NewVideoUsageRepository(db).ListForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || len(facts) != 0 {
		t.Fatalf("未知成本不得伪记为0: %v", err)
	}
	w, err := NewVideoCompensationWorker(f.service, "unknown-worker")
	if err != nil {
		t.Fatal(err)
	}
	r, err := w.RunOne(context.Background(), f.command.RequestID)
	if err != nil || r.Status != "retry" {
		t.Fatalf("事实不足应有界重试而不猜测收费: %+v %v", r, err)
	}
	if a.SubmitCalls() != 1 {
		t.Fatal("补偿不得重提Provider")
	}
}

// 正常未提交取消没有Provider证明，也不需要该证明；已闭合的请求不能被误建未知补偿。
func TestVideoG5UnknownMySQLClosedUnsubmittedCancelIsNoop(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if d, err := f.service.ReconcileExecution(context.Background(), f.command.TaskID, f.owner); err != nil || d != "not_required" {
			t.Fatalf("已闭合未提交取消不应安排Provider核对: %s %v", d, err)
		}
	}
	var n int64
	if err := db.Model(&model.VideoCompensationTask{}).Where("aggregate_id=?", f.command.RequestID).Count(&n).Error; err != nil || n != 0 {
		t.Fatal("不能新增补偿")
	}
	if err := db.Model(&model.AIOutboxEvent{}).Where("aggregate_id=?", f.command.RequestID).Count(&n).Error; err != nil || n != 3 {
		t.Fatal("只保留原H/R/J")
	}
	if r, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner); err != nil || !r.Passed {
		t.Fatalf("正常取消应仍然17项通过: %+v %v", r, err)
	}
}

func TestVideoG5UnknownMySQLAtomicTransitionAndReplay(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, point := range []string{"execution_compensation", "execution_pending", "execution_pending_outbox", "execution_required_outbox"} {
		t.Run(point, func(t *testing.T) {
			f, _, a := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, videogateway.FakeVideoSuccess)
			l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
			before, err := l.Load(context.Background(), f.command.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			l.financialFault = func(at string) error {
				if at == point {
					return errors.New("合成待核对故障")
				}
				return nil
			}
			if _, err := l.Advance(context.Background(), f.command.TaskID, before.Version, videogateway.TaskPendingReconcile, "worker", "query_unknown", nil); err == nil {
				t.Fatal("注入故障应整体回滚")
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || task.Status != model.AIImageTaskSubmitted || task.BillingStatus != model.AIBillingHeld {
				t.Fatalf("执行和计费必须一起回滚: %v", err)
			}
			var count int64
			if err := db.Model(&model.VideoCompensationTask{}).Where("aggregate_id=?", f.command.RequestID).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("不能留下半个补偿")
			}
			if err := db.Model(&model.AIOutboxEvent{}).Where("aggregate_id=?", f.command.RequestID).Count(&count).Error; err != nil || count != 1 {
				t.Fatal("只保留原H事件")
			}
			l.financialFault = nil
			if _, err := l.Advance(context.Background(), f.command.TaskID, before.Version, videogateway.TaskPendingReconcile, "worker", "query_unknown", nil); err != nil {
				t.Fatal(err)
			}
			var failures atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					d, err := f.service.ReconcileExecution(context.Background(), f.command.TaskID, f.owner)
					if err != nil || d != "existing_active" {
						failures.Add(1)
					}
				}()
			}
			wg.Wait()
			if failures.Load() != 0 {
				t.Fatalf("重复编排不得失败或重建: %d", failures.Load())
			}
			job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
			if err != nil || job.InitialBillingStatus != model.AIBillingHeld || job.AttemptCount != 0 || job.VersionNo != 1 {
				t.Fatalf("首次状态及次数必须冻结: %v", err)
			}
			if a.SubmitCalls() != 1 {
				t.Fatal("重试数据库编排不能重新Submit")
			}
		})
	}
}

func TestVideoG5UnknownMySQLMatrixAndEightAttempts(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, mode := range []videogateway.FakeVideoMode{videogateway.FakeVideoSubmitTimeout, videogateway.FakeVideoAckLostUnknownTask, videogateway.FakeVideoQueryTimeout, videogateway.FakeVideoResultUnknown, videogateway.FakeVideoFetchTimeout, videogateway.FakeVideoCorruptResult} {
			t.Run(op+"/"+string(mode), func(t *testing.T) {
				f := newVideoG5ReservationFixture(t, db, "10")
				if op == model.AIVideoOperationImageToVideo {
					prepareVideoG5I2V(t, &f)
				}
				if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
					t.Fatal(err)
				}
				a := videogateway.NewFakeAsyncVideoAdapter(mode)
				g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader), Provider: a, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: videogateway.NewFakeVideoObjectStore()})
				_, _ = g.Submit(context.Background(), f.command.TaskID)
				for i := 0; i < 2; i++ {
					_, _ = g.Poll(context.Background(), f.command.TaskID)
				}
				_, _ = g.FetchAndFinalize(context.Background(), f.command.TaskID)
				task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
				if err != nil || task.BillingStatus != model.AIBillingSettlementPending || task.DeliveryStatus != model.AIDeliveryPending {
					t.Fatalf("异常必须完整待核对: %v", err)
				}
				facts, err := repository.NewVideoUsageRepository(db).ListForTask(context.Background(), f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				wantFacts := 0
				if mode == videogateway.FakeVideoFetchTimeout || mode == videogateway.FakeVideoCorruptResult {
					wantFacts = 2
				}
				if len(facts) != wantFacts {
					t.Fatalf("确认成本保留、未知成本不能伪0: %d", len(facts))
				}
				now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
				f.service.now = func() time.Time { return now }
				w, err := NewVideoCompensationWorker(f.service, "bounded-unknown")
				if err != nil {
					t.Fatal(err)
				}
				for i := 1; i <= 8; i++ {
					job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
					if err != nil {
						t.Fatal(err)
					}
					if now.Before(job.NextRetryAt) {
						now = job.NextRetryAt.Add(time.Second)
					}
					r, err := w.RunOne(context.Background(), f.command.RequestID)
					if err != nil {
						t.Fatal(err)
					}
					want := "retry"
					if i == 8 {
						want = "dead"
					}
					if r.Status != want || r.Financial != nil {
						t.Fatalf("第%d次不能猜测财务终态: %+v", i, r)
					}
				}
				if _, err := w.RunOne(context.Background(), f.command.RequestID); !errors.Is(err, repository.ErrVideoCompensationNotReady) {
					t.Fatalf("不能自动第9次: %v", err)
				}
				inputs, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				for _, input := range inputs {
					if input.LeaseReleasedAt != nil {
						t.Fatal("8次耗尽仍不能释放输入租约")
					}
				}
				if a.SubmitCalls() != 1 {
					t.Fatal("未知补偿不得再次Submit")
				}
			})
		}
	}
}

func TestVideoG5UnknownMySQLCompletedJobNeedsNewReviewFact(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := videoG5PendingFixture(t, db)
	w, err := NewVideoCompensationWorker(f.service, "completed-before-conflict")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := w.RunOne(context.Background(), f.command.RequestID); err != nil || r.Status != "completed" {
		t.Fatalf("原补偿先完成: %v", err)
	}
	before, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
	c := videogateway.ProviderCostConfirmation{ProviderCode: *task.ProviderCode, ProviderTaskID: *task.ProviderTaskID, Operation: *task.Operation, ExternalEventID: "after-completed", Outcome: videogateway.ProviderTaskCancelled, Currency: "CNY"}
	if err := l.RecordProviderResultConflict(context.Background(), f.command.TaskID, c); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if d, err := f.service.ReconcileExecution(context.Background(), f.command.TaskID, f.owner); err != nil || d != "review_required" {
			t.Fatalf("必须说明需要新的人工核对: %s %v", d, err)
		}
	}
	after, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || after.Status != "completed" || after.VersionNo != before.VersionNo || after.AttemptCount != before.AttemptCount {
		t.Fatal("不能重开或重置旧补偿")
	}
	var n int64
	if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND event_type='video_reconciliation_review_required'", task.ID).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("追加请求应幂等: %d %v", n, err)
	}
}
