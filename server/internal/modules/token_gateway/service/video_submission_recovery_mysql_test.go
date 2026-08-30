package service

import (
	"context"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 原始提交事件提供恢复时限；未过期不得抢占，过期只安排核对，不能重提或释放。
func TestVideoG5SubmissionMySQLExpiryRecovery(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
	task, err := l.Load(context.Background(), f.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []videogateway.TaskStatus{videogateway.TaskQueued, videogateway.TaskSubmitting} {
		task, err = l.Advance(context.Background(), task.TaskID, task.Version, to, "worker", "state_advanced", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	var e model.AIGatewayTaskEvent
	if err := db.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND to_status='submitting'", task.TaskID).First(&e).Error; err != nil {
		t.Fatal(err)
	}
	now := e.CreatedAt.Add(time.Minute)
	f.service.now = func() time.Time { return now }
	if d, err := f.service.RecoverExpiredSubmission(context.Background(), task.TaskID, f.owner); err != nil || d != "inflight" {
		t.Fatalf("未过期只读: %s %v", d, err)
	}
	now = e.CreatedAt.Add(2 * time.Minute)
	if d, err := f.service.RecoverExpiredSubmission(context.Background(), task.TaskID, f.owner); err != nil || d != "created" {
		t.Fatalf("过期应原子安排核对: %s %v", d, err)
	}
	after, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), task.TaskID, f.owner)
	if err != nil || after.Status != model.AIImageTaskPendingReconcile || after.BillingStatus != model.AIBillingSettlementPending || after.AttemptCount != 0 {
		t.Fatal("未知原RPC结果不应猜测绑定/消费")
	}
}

func TestVideoG5SubmissionMySQLLateReceiptKeepsPending(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
	task, err := l.Load(context.Background(), f.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []videogateway.TaskStatus{videogateway.TaskQueued, videogateway.TaskSubmitting} {
		task, err = l.Advance(context.Background(), task.TaskID, task.Version, to, "worker", "state_advanced", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	a := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	receipt, err := a.Submit(context.Background(), videogateway.SubmitRequest{RequestID: task.RequestID, Operation: task.Operation, Prompt: task.Prompt, Input: task.Input, Spec: task.Spec})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(3 * time.Minute)
	l.now = func() time.Time { return now }
	f.service.now = l.now
	if _, err := f.service.RecoverExpiredSubmission(context.Background(), task.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	r, err := l.RecordSubmissionReceipt(context.Background(), task.TaskID, task.Version, receipt)
	if err != nil || r.Status != videogateway.TaskPendingReconcile || r.ProviderTaskID != receipt.ProviderTaskID {
		t.Fatalf("迟到原ID应保留而不回退: %+v %v", r, err)
	}
	if _, err := l.RecordSubmissionReceipt(context.Background(), task.TaskID, task.Version, receipt); err != nil {
		t.Fatal(err)
	}
	if a.SubmitCalls() != 1 {
		t.Fatal("补记不得重调Provider")
	}
}
