package service

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

func videoG5CancellationFixture(t *testing.T, db *gorm.DB, op string, mode videogateway.FakeVideoMode) (videoG5ReservationFixture, *videogateway.VideoGateway, *videogateway.FakeAsyncVideoAdapter) {
	t.Helper()
	f := newVideoG5ReservationFixture(t, db, "10")
	if op == model.AIVideoOperationImageToVideo {
		prepareVideoG5I2V(t, &f)
	}
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	a := videogateway.NewFakeAsyncVideoAdapter(mode)
	l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
	g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: l, Provider: a, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: videogateway.NewFakeVideoObjectStore()})
	if _, err := g.Submit(context.Background(), f.command.TaskID); err != nil {
		t.Fatal(err)
	}
	return f, g, a
}

// 接受取消、拒绝/不支持取消及取消时迟到成功，必须复用原Provider任务和原财务链。
func TestVideoG5CancelMySQLSubmittedOutcomes(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, outcome := range []string{"accepted", "rejected", "unsupported", "late_success"} {
			t.Run(op+"/"+outcome, func(t *testing.T) {
				mode := videogateway.FakeVideoSuccess
				if outcome == "rejected" {
					mode = videogateway.FakeVideoCancelRejected
				}
				if outcome == "unsupported" {
					mode = videogateway.FakeVideoCancelUnsupported
				}
				f, g, a := videoG5CancellationFixture(t, db, op, mode)
				original, err := g.Query(context.Background(), f.command.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "late_success" {
					// 模拟Provider已完成但网关尚未轮询到结果，取消响应成为首个成功确认。
					for i := 0; i < 2; i++ {
						if _, err := a.Query(context.Background(), videogateway.QueryRequest{ProviderTaskID: original.ProviderTaskID}); err != nil {
							t.Fatal(err)
						}
					}
				}
				cancelled, err := g.Cancel(context.Background(), f.command.TaskID)
				if outcome == "rejected" {
					if !errors.Is(err, videogateway.ErrProviderCancelRejected) {
						t.Fatalf("应明确拒绝取消: %v", err)
					}
				} else if outcome == "unsupported" {
					if !errors.Is(err, videogateway.ErrProviderCancelUnsupported) {
						t.Fatalf("应不支持取消: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if cancelled.CancelRequestedAt == nil || cancelled.ProviderTaskID != original.ProviderTaskID {
					t.Fatal("取消意图和原Provider绑定必须保留")
				}
				stored, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
				if err != nil || stored.CancelRequestedAt == nil || stored.BillingStatus != model.AIBillingHeld {
					t.Fatalf("取消响应不能提前改变财务轴: %v", err)
				}
				if outcome == "accepted" {
					if cancelled.Status != videogateway.TaskCancelled {
						t.Fatal("明确确认取消才可进入cancelled")
					}
					if _, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner); err != nil {
						t.Fatalf("取消确认成本应支持释放: %v", err)
					}
				} else {
					if outcome != "late_success" {
						for i := 0; i < 2; i++ {
							if _, err := g.Poll(context.Background(), f.command.TaskID); err != nil {
								t.Fatal(err)
							}
						}
					}
					if _, err := g.FetchAndFinalize(context.Background(), f.command.TaskID); err != nil {
						t.Fatal(err)
					}
					if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
					if _, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := g.Cancel(context.Background(), f.command.TaskID); err != nil {
					t.Fatalf("终态重放应安全: %v", err)
				}
				report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
				if err != nil || !report.Passed {
					t.Fatalf("取消结果最终应17项零差异: %+v %v", report, err)
				}
				if a.SubmitCalls() != 1 {
					t.Fatal("取消不得创建第二个Provider任务")
				}
			})
		}
	}
}
