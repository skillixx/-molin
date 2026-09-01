package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 两类任务的取消只形成一次G5释放；并发重放不能再次建Usage、Outbox或解除其他任务的预占。
func TestVideoG6TaskCancelMySQL(t *testing.T) {
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := newVideoG6I2VFixture(t)
			c := f.command
			if operation == model.AIVideoOperationTextToVideo {
				c.Operation = operation
				c.InputAssetID = ""
			}
			if operation == model.AIVideoOperationImageToVideo {
				if _, err := f.app.AcceptProjectRights(context.Background(), VideoRightsAcceptCommand{Caller: VideoCaller{UserID: c.Caller.UserID, ProjectID: c.Caller.ProjectID}, PolicyVersion: f.policyVersion, Confirmed: true, IdempotencyKey: "g6-cancel-rights-accept", RequestID: "g6-cancel-rights-trace"}); err != nil {
					t.Fatal(err)
				}
			}
			job, err := f.app.Create(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			var mu sync.Mutex
			fresh, replays := 0, 0
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, err := f.app.CancelTask(context.Background(), c.Caller, job.Job.ID, "g6-cancel-same-key")
					if err != nil || r == nil {
						t.Errorf("取消失败：%v", err)
						return
					}
					if r.CancellationResult != "cancelled" || r.ExecutionStatus != "cancelled" || r.BillingStatus != "released" || r.SettledAmount == nil || *r.SettledAmount != "0.00000000" || r.CancelRequestedAt == nil {
						t.Error("取消必须由原G5财务链闭合")
					}
					mu.Lock()
					if r.Idempotent {
						replays++
					} else {
						fresh++
					}
					mu.Unlock()
				}()
			}
			wg.Wait()
			if fresh != 1 || replays != 99 {
				t.Fatalf("应1首次/99重放，实际%d/%d", fresh, replays)
			}
			if report, err := NewVideoReconciliationService(f.legacy.db).Reconcile(context.Background(), job.Job.ID, f.legacy.owner); err != nil || !report.Passed {
				t.Fatalf("取消后必须完整对账：%v", err)
			}
			other := c
			other.IdempotencyKey = "g6-cancel-other-create"
			second, err := f.app.Create(context.Background(), other)
			if err != nil {
				t.Fatal(err)
			}
			if r, err := f.app.CancelTask(context.Background(), c.Caller, second.Job.ID, "g6-cancel-same-key"); !errors.Is(err, ErrVideoCancelConflict) || r != nil {
				t.Fatal("同键异任务必须冲突")
			}
			task, err := repository.NewVideoTaskRepository(f.legacy.db).FindForOwner(context.Background(), second.Job.ID, f.legacy.owner)
			if err != nil || task.Status != "reserved" || task.BillingStatus != "held" || task.CancelRequestedAt != nil {
				t.Fatal("冲突不得取消第二个任务")
			}
		})
	}
}
