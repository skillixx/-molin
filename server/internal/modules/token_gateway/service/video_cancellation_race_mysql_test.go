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

type videoG5SubmitCounter struct {
	videogateway.VideoProviderAdapter
	entries atomic.Int32
}

func (a *videoG5SubmitCounter) Submit(ctx context.Context, r videogateway.SubmitRequest) (videogateway.SubmitResult, error) {
	a.entries.Add(1)
	return a.VideoProviderAdapter.Submit(ctx, r)
}

// 让真实财务取消在线性化提交CAS之前获胜，而非只测试两个Repository方法。
type videoG5CancelAtSubmission struct {
	videogateway.VideoTaskLedger
	cancel func() error
}

func (l *videoG5CancelAtSubmission) Advance(ctx context.Context, id string, v uint64, to videogateway.TaskStatus, source, reason string, m videogateway.TaskMutation) (videogateway.GatewayTask, error) {
	if to == videogateway.TaskSubmitting {
		if err := l.cancel(); err != nil {
			return videogateway.GatewayTask{}, err
		}
	}
	return l.VideoTaskLedger.Advance(ctx, id, v, to, source, reason, m)
}

func TestVideoG5CancelMySQLRefundWinnerPreventsGatewaySubmit(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	prepareVideoG5I2V(t, &f)
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	l := &videoG5CancelAtSubmission{VideoTaskLedger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader), cancel: func() error {
		_, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner)
		return err
	}}
	a := &videoG5SubmitCounter{VideoProviderAdapter: videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)}
	g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: l, Provider: a, Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess))})
	task, err := g.Submit(context.Background(), f.command.TaskID)
	if err != nil || task.Status != videogateway.TaskCancelled || a.entries.Load() != 0 {
		t.Fatalf("退款胜出后不得进入Provider: state=%s entries=%d err=%v", task.Status, a.entries.Load(), err)
	}
	r, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
	if err != nil || !r.Passed {
		t.Fatalf("未提交取消应保持17项零差异: %+v %v", r, err)
	}
}

func TestVideoG5CancelMySQLIntentCASAndIsolation(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	r := repository.NewVideoTaskRepository(db)
	var failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x, err := r.RequestCancellation(context.Background(), f.command.TaskID, f.owner, time.Now())
			if err != nil || x.CancelRequestedAt == nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("100并发取消意图失败: %d", failures.Load())
	}
	task, err := r.FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || task.VersionNo != 2 || task.Status != model.AIImageTaskReserved || task.BillingStatus != model.AIBillingHeld {
		t.Fatalf("意图应仅CAS一次且不改变三轴: %v", err)
	}
	var n int64
	if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND event_type='cancel_requested'", task.ID).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("取消意图事件必须唯一: %d %v", n, err)
	}
	owners := []repository.VideoOwner{f.owner, f.owner, f.owner}
	owners[0].UserID++
	owners[1].ProjectID++
	wrongKey := *f.owner.APIKeyID + 1
	owners[2].APIKeyID = &wrongKey
	for _, owner := range owners {
		if _, err := r.RequestCancellation(context.Background(), f.command.TaskID, owner, time.Now()); !errors.Is(err, repository.ErrVideoTaskNotFound) {
			t.Fatalf("越权取消统一不存在: %v", err)
		}
	}
	if err := db.Model(&model.AIImageTask{}).Where("id=?", task.ID).Updates(map[string]interface{}{"cancel_requested_at": nil, "version_no": task.VersionNo + 1}).Error; err == nil {
		t.Fatal("取消意图不能清除")
	}
	if _, err := r.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: task.PublicID, Owner: f.owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: f.command.RequestID + "_after_cancel", Source: "worker", Now: time.Now()}); err == nil {
		t.Fatal("意图先落库不得再排队提交")
	}
	if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
	if err != nil || !report.Passed {
		t.Fatalf("随后原子取消须闭合: %+v %v", report, err)
	}
}

type videoG5InflightSubmit struct {
	videogateway.VideoProviderAdapter
	entered chan struct{}
	release chan struct{}
	entries atomic.Int32
}

func (a *videoG5InflightSubmit) Submit(ctx context.Context, r videogateway.SubmitRequest) (videogateway.SubmitResult, error) {
	if a.entries.Add(1) == 1 {
		close(a.entered)
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return videogateway.SubmitResult{}, ctx.Err()
	}
	return a.VideoProviderAdapter.Submit(ctx, r)
}

// 实际SQL Ledger中在途Submit遇到重试和取消意图，仍必须保存原RPC返回的任务ID。
func TestVideoG5CancelMySQLInflightSubmitKeepsBinding(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	a := &videoG5InflightSubmit{VideoProviderAdapter: videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess), entered: make(chan struct{}), release: make(chan struct{})}
	g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil), Provider: a, Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess))})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type outcome struct {
		task videogateway.GatewayTask
		err  error
	}
	done := make(chan outcome, 1)
	go func() { r, err := g.Submit(ctx, f.command.TaskID); done <- outcome{r, err} }()
	select {
	case <-a.entered:
	case <-ctx.Done():
		t.Fatal("提交未进入受控Provider")
	}
	if r, err := g.Submit(ctx, f.command.TaskID); err != nil || r.Status != videogateway.TaskSubmitting {
		t.Fatalf("第二次Submit只读原在途状态: %s %v", r.Status, err)
	}
	if r, err := g.Cancel(ctx, f.command.TaskID); !errors.Is(err, videogateway.ErrProviderResultUnknown) || r.Status != videogateway.TaskSubmitting || r.CancelRequestedAt == nil {
		t.Fatalf("在途取消只留意图: %s %v", r.Status, err)
	}
	close(a.release)
	first := <-done
	if first.err != nil || first.task.Status != videogateway.TaskSubmitted || first.task.ProviderTaskID == "" || first.task.CancelRequestedAt == nil || a.entries.Load() != 1 {
		t.Fatalf("原请求应成功绑定: status=%s bound=%t calls=%d err=%v", first.task.Status, first.task.ProviderTaskID != "", a.entries.Load(), first.err)
	}
	if r, err := g.Cancel(ctx, f.command.TaskID); err != nil || r.Status != videogateway.TaskCancelled {
		t.Fatalf("绑定后才可确认同一任务取消: %s %v", r.Status, err)
	}
	if _, err := f.service.ReleaseUnserviceable(ctx, f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	r, err := NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner)
	if err != nil || !r.Passed {
		t.Fatalf("在途取消恢复应最终零差异: %+v %v", r, err)
	}
}
