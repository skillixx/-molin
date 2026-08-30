package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 通用事件追加不应生成专用财务释放依据，即使调用者知道确定性事件ID。
func TestVideoG5ReleaseMySQLRejectsGenericReleaseEvidence(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	event := model.AIGatewayTaskEvent{EventID: "vg5_" + videoBillingDigest(f.command.RequestID+":video_release_label_failed"), EventType: "video_release_label_failed", Source: "worker", CreatedAt: time.Now()}
	if err := repository.NewVideoTaskEventRepository(db).Append(context.Background(), f.command.TaskID, f.owner, event); err == nil {
		t.Fatal("通用追加接口允许伪造专用释放依据")
	}
}

// 通过真实预占、Fake执行、释放和对账入口验证销售归零与平台已确认安全成本保留。
func TestVideoG5ReleaseMySQLDefiniteFailureMatrix(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, outcome := range []string{"provider_failed", "moderation_rejected", "explicit_label_failed", "implicit_label_failed"} {
			t.Run(op+"/"+outcome, func(t *testing.T) {
				f := newVideoG5ReservationFixture(t, db, "10")
				if op == model.AIVideoOperationImageToVideo {
					prepareVideoG5I2V(t, &f)
				}
				if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
					t.Fatal(err)
				}
				pm, mm, lm := videogateway.FakeVideoSuccess, videogateway.FakeVideoModerationAllow, videogateway.FakeVideoLabelSuccess
				switch outcome {
				case "provider_failed":
					pm = videogateway.FakeVideoExplicitFailure
				case "moderation_rejected":
					mm = videogateway.FakeVideoModerationRejectFrames
				case "explicit_label_failed":
					lm = videogateway.FakeVideoLabelExplicitFailure
				case "implicit_label_failed":
					lm = videogateway.FakeVideoLabelImplicitFailure
				}
				adapter := videogateway.NewFakeAsyncVideoAdapter(pm)
				ledger := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
				g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(mm), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(lm, "fake-label-v1"), Store: videogateway.NewFakeVideoObjectStore()})
				if _, err := g.Submit(context.Background(), f.command.TaskID); err != nil {
					t.Fatal(err)
				}
				if _, err := g.Poll(context.Background(), f.command.TaskID); err != nil {
					t.Fatal(err)
				}
				_, _ = g.Poll(context.Background(), f.command.TaskID)
				_, fetchErr := g.FetchAndFinalize(context.Background(), f.command.TaskID)
				before, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
				if err != nil || before.Status != model.AIImageTaskFailed || before.BillingStatus != model.AIBillingHeld {
					t.Fatalf("失败执行不应先释放财务: err=%v fetch=%v", err, fetchErr)
				}
				var failures, first atomic.Int32
				var wg sync.WaitGroup
				for i := 0; i < 100; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						r, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner)
						if err != nil || r == nil || r.BillingStatus != model.AIBillingReleased || !r.SettledAmount.IsZero() {
							failures.Add(1)
							return
						}
						if !r.Existing {
							first.Add(1)
						}
					}()
				}
				wg.Wait()
				if failures.Load() != 0 || first.Load() != 1 {
					t.Fatalf("释放100并发只允许一次: failures=%d first=%d", failures.Load(), first.Load())
				}
				r, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
				if err != nil || !r.Passed || len(r.Checks) != 17 {
					t.Fatalf("释放终态须17项零差异: report=%+v err=%v", r, err)
				}
				facts, err := repository.NewVideoUsageRepository(db).ListForTask(context.Background(), f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				wantCost := decimal.RequireFromString("0.20")
				if op == model.AIVideoOperationImageToVideo {
					wantCost = decimal.RequireFromString("0.30")
				}
				if outcome == "provider_failed" {
					wantCost = decimal.Zero
				}
				if len(facts) != 4 {
					t.Fatalf("用户与Provider各两条事实: %d", len(facts))
				}
				for _, item := range facts {
					if item.RecordKind == model.AIUsageCostLine && (item.Amount == nil || !item.Amount.Equal(wantCost)) {
						t.Fatal("已确认成本不得被销售额归零覆盖")
					}
				}
				if _, err := g.ReadContent(context.Background(), f.command.TaskID, 0, 1); err == nil {
					t.Fatal("拒绝交付不得读取媒体")
				}
				if adapter.SubmitCalls() != 1 {
					t.Fatal("财务释放不能重新提交Provider")
				}
			})
		}
	}
}
