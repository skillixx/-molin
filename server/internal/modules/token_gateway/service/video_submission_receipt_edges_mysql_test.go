package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

func videoG5ClaimFixture(t *testing.T, db *gorm.DB) (videoG5ReservationFixture, *VideoRepositoryTaskLedger, videogateway.GatewayTask, videogateway.SubmitResult, time.Time) {
	t.Helper()
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
	x, err := l.Load(context.Background(), f.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []videogateway.TaskStatus{videogateway.TaskQueued, videogateway.TaskSubmitting} {
		x, err = l.Advance(context.Background(), x.TaskID, x.Version, to, "worker", "state_advanced", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	deadline, err := l.ValidateSubmissionClaim(context.Background(), x.TaskID, x.Version)
	if err != nil {
		t.Fatal(err)
	}
	a := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	r, err := a.Submit(context.Background(), videogateway.SubmitRequest{RequestID: x.RequestID, Operation: x.Operation, Prompt: x.Prompt, Input: x.Input, Spec: x.Spec})
	if err != nil {
		t.Fatal(err)
	}
	return f, l, x, r, deadline
}

func TestVideoG5SubmissionMySQLReceiptDeadlineAndReplay(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, offset := range []time.Duration{-time.Second, 0, time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			f, l, claim, r, deadline := videoG5ClaimFixture(t, db)
			now := deadline.Add(offset)
			l.now = func() time.Time { return now }
			f.service.now = l.now
			if _, err := repository.NewVideoTaskRepository(db).RequestCancellation(context.Background(), claim.TaskID, f.owner, now); err != nil {
				t.Fatal(err)
			}
			var bad atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, r)
					want := videogateway.TaskSubmitted
					if offset >= 0 {
						want = videogateway.TaskPendingReconcile
					}
					if err != nil || got.Status != want || got.ProviderTaskID != r.ProviderTaskID {
						bad.Add(1)
					}
				}()
			}
			wg.Wait()
			if bad.Load() != 0 {
				t.Fatalf("同一原回执应幂等: %d", bad.Load())
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), claim.TaskID, f.owner)
			if err != nil || task.AttemptCount != 1 {
				t.Fatal("不能重复增加提交次数")
			}
			var n int64
			if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND event_type IN ('provider_task_bound','provider_task_bound_pending')", task.ID).Count(&n).Error; err != nil || n != 1 {
				t.Fatal("绑定事件应唯一")
			}
			if offset >= 0 {
				if task.BillingStatus != model.AIBillingSettlementPending {
					t.Fatal("过期绑定不能提前结算")
				}
			} else if task.BillingStatus != model.AIBillingHeld {
				t.Fatal("正常绑定不改变财务")
			}
		})
	}
}

func TestVideoG5SubmissionMySQLRejectsWrongReceiptAndRollsBack(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, l, claim, r, deadline := videoG5ClaimFixture(t, db)
	for _, v := range []uint64{0, 1} {
		if _, err := l.ValidateSubmissionClaim(context.Background(), claim.TaskID, v); err == nil {
			t.Fatal("公开入口不能使用内部零版本模式")
		}
		if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, v, r); err == nil {
			t.Fatal("缺原claim不能补记回执")
		}
	}
	if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version+1, r); err == nil {
		t.Fatal("当前版本不能冒充原claim")
	}
	wrong := r
	wrong.RequestID = "wrong-request"
	if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, wrong); err == nil {
		t.Fatal("错请求不能绑定")
	}
	wrong = r
	wrong.ProviderCode = "wrong-provider"
	if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, wrong); err == nil {
		t.Fatal("错Provider不能绑定")
	}
	l.now = func() time.Time { return deadline }
	l.financialFault = func(at string) error {
		if at == "submission_receipt" {
			return errors.New("合成回执写入故障")
		}
		return nil
	}
	if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, r); err == nil {
		t.Fatal("故障应撤销过期状态、绑定和补偿")
	}
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), claim.TaskID, f.owner)
	if err != nil || task.Status != model.AIImageTaskSubmitting || task.ProviderTaskID != nil || task.BillingStatus != model.AIBillingHeld {
		t.Fatal("不得保留半个回执")
	}
	l.financialFault = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := l.RecordSubmissionReceipt(ctx, claim.TaskID, claim.Version, r); err != nil || got.Status != videogateway.TaskPendingReconcile {
		t.Fatalf("断连仍应补原身份: %v", err)
	}
	wrong = r
	wrong.ProviderTaskID += "-other"
	if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, wrong); err == nil {
		t.Fatal("已绑定的原ID不能被覆盖")
	}
}

type videoG5MissingSubmitID struct {
	videogateway.VideoProviderAdapter
}

func (a videoG5MissingSubmitID) Submit(ctx context.Context, r videogateway.SubmitRequest) (videogateway.SubmitResult, error) {
	x, err := a.VideoProviderAdapter.Submit(ctx, r)
	x.ProviderTaskID = ""
	return x, err
}

func TestVideoG5SubmissionMySQLSuccessfulResponseWithoutIDIsUnknown(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	a := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil), Provider: videoG5MissingSubmitID{a}, Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess))})
	r, err := g.Submit(context.Background(), f.command.TaskID)
	if err == nil || r.Status != videogateway.TaskPendingReconcile {
		t.Fatalf("空ID不能被写成submitted: %s %v", r.Status, err)
	}
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || task.BillingStatus != model.AIBillingSettlementPending || task.ProviderTaskID != nil {
		t.Fatal("需要HPC而不是伪绑定")
	}
	if a.SubmitCalls() != 1 {
		t.Fatal("不能重新提交")
	}
}
