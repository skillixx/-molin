package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 真实Gateway、SQL Ledger与内存Fake组合：原RPC尚未返回时，恢复扫描不能制造第二次提交。
func TestVideoG5SubmissionMySQLGatewayRecoveryOrdering(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, i2v := range []bool{false, true} {
		for _, order := range []string{"receipt_before_expiry", "recovery_before_receipt", "expired_receipt_before_recovery"} {
			name := "t2v/" + order
			if i2v {
				name = "i2v/" + order
			}
			t.Run(name, func(t *testing.T) {
				f := newVideoG5ReservationFixture(t, db, "10")
				if i2v {
					prepareVideoG5I2V(t, &f)
				}
				if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
					t.Fatal(err)
				}
				var clock atomic.Int64
				clock.Store(time.Now().UTC().UnixNano())
				now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
				l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
				l.now, f.service.now = now, now
				a := &videoG5InflightSubmit{VideoProviderAdapter: videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess), entered: make(chan struct{}), release: make(chan struct{})}
				g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: l, Provider: a, Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess))})
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
					t.Fatal("原RPC未进入Fake")
				}
				claim, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				deadline, err := l.ValidateSubmissionClaim(ctx, claim.PublicID, claim.VersionNo)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := repository.NewVideoTaskRepository(db).RequestCancellation(ctx, claim.PublicID, f.owner, now()); err != nil {
					t.Fatal(err)
				}
				if order == "receipt_before_expiry" {
					clock.Store(deadline.Add(-time.Second).UnixNano())
				} else {
					clock.Store(deadline.UnixNano())
				}
				if order == "recovery_before_receipt" {
					if d, err := f.service.RecoverExpiredSubmission(ctx, claim.PublicID, f.owner); err != nil || d != "created" {
						t.Fatalf("恢复先到须保留HPC: %s %v", d, err)
					}
				}
				close(a.release)
				first := <-done
				want := videogateway.TaskPendingReconcile
				if order == "receipt_before_expiry" {
					want = videogateway.TaskSubmitted
				}
				if first.err != nil || first.task.Status != want || first.task.ProviderTaskID == "" || first.task.CancelRequestedAt == nil {
					t.Fatalf("回执必须保留原ID与取消意图: %s %v", first.task.Status, first.err)
				}
				clock.Store(deadline.Add(time.Second).UnixNano())
				if _, err := f.service.RecoverExpiredSubmission(ctx, claim.PublicID, f.owner); err != nil {
					t.Fatal(err)
				}
				if r, err := g.Submit(ctx, claim.PublicID); err != nil || r.Status != want || r.ProviderTaskID != first.task.ProviderTaskID || a.entries.Load() != 1 {
					t.Fatalf("恢复后重放不能重调或丢失原身份: %s %v", r.Status, err)
				}
				stored, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, claim.PublicID, f.owner)
				if err != nil || stored.AttemptCount != 1 || stored.DeliveryStatus != model.AIDeliveryPending {
					t.Fatal("回执不能直接交付或重复记提交次数")
				}
				if i2v {
					var released int64
					if err := db.Model(&model.AIGatewayTaskInput{}).Where("task_id=? AND lease_released_at IS NOT NULL", claim.ID).Count(&released).Error; err != nil || released != 0 {
						t.Fatalf("在途和待核对均不得释放输入: %v", err)
					}
				}
			})
		}
	}
}
