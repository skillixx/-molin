package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// completed写入或最终核对后租约过期，释放、输入租约和完成标记仍必须整体回滚。
func TestVideoG5ReleaseMySQLCompletionLeaseExpiry(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, step := range []string{"release_completed", "release_checked"} {
		t.Run(step, func(t *testing.T) {
			f, a := videoG5ReleaseFailureFixture(t, db, "label_failed")
			f.service.fault = func(at string) error {
				if at == "release_hold" {
					return errors.New("合成释放故障")
				}
				return nil
			}
			if _, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("需先形成待恢复的释放")
			}
			now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
			f.service.now = func() time.Time { return now }
			f.service.fault = func(at string) error {
				if at == step {
					now = now.Add(repository.VideoCompensationLeaseDuration + time.Second)
				}
				return nil
			}
			worker, err := NewVideoCompensationWorker(f.service, "release-deadline")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := worker.RunOne(context.Background(), f.command.RequestID); !errors.Is(err, repository.ErrVideoCompensationLeaseLost) {
				t.Fatalf("尾部过期必须撤销释放: %v", err)
			}
			assertVideoG5ReleaseStillHeld(t, f)
			job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
			if err != nil || job.Status != "running" || job.CompletedAt != nil {
				t.Fatalf("完成标记不能越过过期租约: %v", err)
			}
			f.service.fault = nil
			result, err := worker.RunOne(context.Background(), f.command.RequestID)
			if err != nil || result.Status != "completed" || result.Financial == nil || result.Financial.BillingStatus != model.AIBillingReleased {
				t.Fatalf("新围栏应从原事实恢复一次释放: %+v %v", result, err)
			}
			if a.SubmitCalls() != 1 {
				t.Fatal("回收租约不能再次调用Provider")
			}
		})
	}
}
