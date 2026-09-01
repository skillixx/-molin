package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 同一输入的多个真实任务分别完成后，清理只认最晚的安全租约释放时间，不按首个任务提前删除。
func TestVideoG6InputCleanupMySQLBoundTasks(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	ctx := context.Background()
	store := &videoUploadMemoryStore{entries: map[string]*videoUploadMemoryEntry{}}
	options := VideoUploadOptions{Store: store, SourceBucket: "g6-bound-source", NormalizedBucket: "g6-bound-normalized", ModerationPolicyVersion: "g6-bound-fixture", MaxUserReservedBytes: 128 << 20}
	app, err := NewVideoHTTPService(f.legacy.db, VideoBillingOptions{QuoteSecret: f.legacy.service.quoteSecret, PromptSecret: f.legacy.service.promptSecret, IntentSecret: f.legacy.service.intentSecret, Protector: f.legacy.service.protector, Safety: f.legacy.service.safety}, VideoHTTPOptions{Uploads: &options})
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.CreateUpload(ctx, VideoUploadCreateCommand{Caller: f.command.Caller, IdempotencyKey: "g6-bound-upload-create", Filename: "reference.png", MIMEType: "image/png", SizeBytes: uint64(len(f.reference.Bytes)), SHA256: videoPayloadSHA256(f.reference.Bytes)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.write(created.SessionID, f.reference.Bytes, "image/png"); err != nil {
		t.Fatal(err)
	}
	uploaded, err := app.CompleteUpload(ctx, f.command.Caller, created.SessionID, "g6-bound-upload-complete")
	if err != nil || uploaded.InputAssetID == nil {
		t.Fatalf("必须真实完成上传：%v", err)
	}
	if _, err := app.AcceptProjectRights(ctx, VideoRightsAcceptCommand{Caller: VideoCaller{UserID: f.legacy.owner.UserID, ProjectID: f.legacy.owner.ProjectID}, PolicyVersion: f.policyVersion, Confirmed: true, IdempotencyKey: "g6-bound-rights-accept", RequestID: "g6-bound-rights-request"}); err != nil {
		t.Fatal(err)
	}
	var jobs []*VideoHTTPGeneration
	for i := 0; i < 2; i++ {
		command := f.command
		command.InputAssetID = *uploaded.InputAssetID
		command.IdempotencyKey = fmt.Sprintf("g6-bound-generation-%04d", i)
		job, err := app.Create(ctx, command)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, job)
	}
	var input model.AIGatewayInputAsset
	if err := f.legacy.db.Where("public_id=?", *uploaded.InputAssetID).Take(&input).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := app.RequestInputDeletion(ctx, f.command.Caller, input.PublicID, input.VersionNo, "g6-bound-delete-request"); err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC().Add(30 * 24 * time.Hour)
	policy := VideoInputCleanupPolicy{Purpose: "non_commercial_test_fixture", Version: "g6-bound-retention-fixture", BoundRetention: 7 * 24 * time.Hour, Now: func() time.Time { return clock }}
	assertHeld := func() {
		t.Helper()
		r, err := app.CleanupInput(ctx, input.PublicID, f.legacy.owner, policy)
		if err != nil || r == nil || r.MediaDeleted || r.LifecycleState != "pending_delete" {
			t.Fatalf("只要有一个任务在执行就不能清理：%+v %v", r, err)
		}
	}
	assertHeld()
	for i, job := range jobs {
		adapter := video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess)
		gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: app.NewTaskLedger(f.legacy.owner, videoG4TestLocationFactory{}), Provider: adapter, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: video.NewFakeVideoObjectStore()})
		if _, err := gateway.Submit(ctx, job.Job.ID); err != nil {
			t.Fatal(err)
		}
		for step := 0; step < 2; step++ {
			if _, err := gateway.Poll(ctx, job.Job.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := gateway.FetchAndFinalize(ctx, job.Job.ID); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			// 合成时钟只用于第二笔真实结算/租约释放，确保最晚安全终态严格晚于第一笔。
			financialTime := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
			app.billing.now = func() time.Time { return financialTime }
		}
		if _, err := app.billing.SettleReady(ctx, job.Job.ID, f.legacy.owner); err != nil {
			t.Fatal(err)
		}
		if _, err := app.billing.DeliverReady(ctx, job.Job.ID, f.legacy.owner); err != nil {
			t.Fatal(err)
		}
		if adapter.SubmitCalls() != 1 {
			t.Fatal("每个原任务只能提交一次Fake Provider")
		}
		if i == 0 {
			assertHeld()
		}
	}
	var bindings []model.AIGatewayTaskInput
	if err := f.legacy.db.Where("input_asset_id=?", input.ID).Order("task_id").Find(&bindings).Error; err != nil || len(bindings) != 2 || bindings[0].LeaseReleasedAt == nil || bindings[1].LeaseReleasedAt == nil {
		t.Fatal("两个绑定均须真实释放")
	}
	if !bindings[1].LeaseReleasedAt.After(*bindings[0].LeaseReleasedAt) {
		t.Fatal("必须构造不同安全终态时间，不能用同一时刻掩盖最晚截止")
	}
	deadline := bindings[1].LeaseReleasedAt.Add(7 * 24 * time.Hour)
	if !deadline.After(input.ExpiresAt) {
		t.Fatal("本例的绑定保护必须晚于原输入期限")
	}
	clock = deadline.Add(-time.Second)
	assertHeld()
	var before billingmodel.Wallet
	if err := f.legacy.db.Where("user_id=?", f.legacy.owner.UserID).Take(&before).Error; err != nil || before.BalanceAmount.StringFixed(8) != "8.50000000" || !before.FrozenAmount.IsZero() {
		t.Fatal("两笔原I2V须先按0.75完成真实合成结算")
	}
	clock = deadline
	result, err := app.CleanupInput(ctx, input.PublicID, f.legacy.owner, policy)
	if err != nil || result == nil || !result.MediaDeleted {
		t.Fatalf("最晚安全留存期结束才可清理：%+v %v", result, err)
	}
	var after billingmodel.Wallet
	if err := f.legacy.db.Where("user_id=?", f.legacy.owner.UserID).Take(&after).Error; err != nil || !after.BalanceAmount.Equal(before.BalanceAmount) || !after.FrozenAmount.Equal(before.FrozenAmount) || after.Version != before.Version {
		t.Fatal("清理不得改变原钱包事实")
	}
	for _, job := range jobs {
		view, err := app.GetVideo(ctx, f.command.Caller, job.Job.ID)
		if err != nil || view.Status != "completed" {
			t.Fatalf("输入正文清理不应破坏已完成视频与财务交付：%v", err)
		}
	}
	remaining, err := repository.NewVideoTaskInputRepository(f.legacy.db).ListForOwner(ctx, jobs[0].Job.ID, f.legacy.owner)
	if err != nil || len(remaining) != 1 || remaining[0].InputVersion != input.VersionNo || remaining[0].NormalizedSHA256 != *input.NormalizedSHA256 {
		t.Fatal("清理仍须保留原冻结TaskInput")
	}
}
