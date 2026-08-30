package service

import (
	"context"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// TestVideoG5MediaMySQLReadyIsNotDelivered 真实复用G4 Fake流水线，媒体安全完成不等于钱包已结算。
func TestVideoG5MediaMySQLReadyIsNotDelivered(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(op, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if op == model.AIVideoOperationImageToVideo {
				prepareVideoG5I2V(t, &f)
			}
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			ledger := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
			adapter := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
			gateway := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: videogateway.NewFakeVideoObjectStore()})
			if _, err := videogateway.NewSubmitWorker(gateway).Run(context.Background(), f.command.TaskID); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := videogateway.NewPollWorker(gateway).Run(context.Background(), f.command.TaskID); err != nil {
					t.Fatal(err)
				}
			}
			ready, err := videogateway.NewAssetFetchWorker(gateway).Run(context.Background(), f.command.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if ready.Status != videogateway.TaskSucceeded || ready.Asset == nil || ready.Asset.Lifecycle != videogateway.AssetTemporary || len(ready.Asset.Children) != 5 {
				t.Fatal("媒体就绪必须保留六类未交付资产")
			}
			for _, a := range ready.Asset.Children {
				if a.Lifecycle != videogateway.AssetTemporary {
					t.Fatal("派生资产不得提前交付")
				}
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || task.BillingStatus != model.AIBillingHeld || task.DeliveryStatus != model.AIDeliveryPending {
				t.Fatalf("媒体处理不得改变财务轴: %v", err)
			}
			bindings, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range bindings {
				if b.LeaseReleasedAt != nil {
					t.Fatal("尚未结算不得提前释放输入租约")
				}
			}
			var asset model.AIImageAsset
			if err := db.Where("task_id=? AND asset_role='content'", task.ID).First(&asset).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := repository.NewVideoOutputAssetRepository(db, videoG4TestLocationFactory{}).TransitionLifecycle(context.Background(), asset.PublicID, f.owner, asset.VersionNo, model.AIImageAssetAvailable, time.Now()); err == nil {
				t.Fatal("绕过协调器也不能把未结算资产标为available")
			}
		})
	}
}
