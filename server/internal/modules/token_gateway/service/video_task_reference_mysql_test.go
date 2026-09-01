package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoTaskReferenceCountingStore struct {
	VideoUploadStore
	reads      atomic.Int64
	beforeRead func(context.Context) error
}

func (s *videoTaskReferenceCountingStore) ReadNormalized(ctx context.Context, bucket, key string, max int64) ([]byte, error) {
	s.reads.Add(1)
	if s.beforeRead != nil {
		if err := s.beforeRead(ctx); err != nil {
			return nil, err
		}
	}
	return s.VideoUploadStore.ReadNormalized(ctx, bucket, key, max)
}

// 已有任务申请删除参考图后，专用读取仍须完成原Fake异步任务，不改变冻结输入或重复提交。
func TestVideoG6TaskReferenceAfterDeleteMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	ctx := context.Background()
	store := &videoUploadMemoryStore{entries: map[string]*videoUploadMemoryEntry{}}
	target := VideoUploadTarget{SessionID: "g6-task-reference-fixture", NormalizedBucket: *f.asset.Bucket, NormalizedKey: *f.asset.ObjectKey}
	if _, err := store.Issue(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := store.PutNormalized(ctx, target, f.reference.Bytes, f.reference.NormalizedSHA256); err != nil {
		t.Fatal(err)
	}
	countingStore := &videoTaskReferenceCountingStore{VideoUploadStore: store}
	uploads := VideoUploadOptions{Store: countingStore, Safety: f.legacy.service.safety, SourceBucket: "g6-task-reference-source", NormalizedBucket: *f.asset.Bucket, ModerationPolicyVersion: "g6-fixture", MaxUserReservedBytes: 128 << 20}
	app, err := NewVideoHTTPService(f.legacy.db, VideoBillingOptions{QuoteSecret: f.legacy.service.quoteSecret, PromptSecret: f.legacy.service.promptSecret, IntentSecret: f.legacy.service.intentSecret, Protector: f.legacy.service.protector, Safety: f.legacy.service.safety, ReferenceLoader: f.app.billing.referenceLoader}, VideoHTTPOptions{Uploads: &uploads})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.AcceptProjectRights(ctx, VideoRightsAcceptCommand{Caller: VideoCaller{UserID: f.legacy.owner.UserID, ProjectID: f.legacy.owner.ProjectID}, PolicyVersion: f.policyVersion, Confirmed: true, IdempotencyKey: "g6-task-reference-rights", RequestID: "g6-task-reference-rights-request"}); err != nil {
		t.Fatal(err)
	}
	c := f.command
	c.IdempotencyKey = "g6-task-reference-create"
	created, err := app.Create(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.NewVideoInputAssetRepository(f.legacy.db).RequestDeferredDelete(ctx, f.asset.PublicID, f.legacy.owner, f.asset.VersionNo, videoBillingDigest("g6-task-reference-delete"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	builder, ok := any(app).(interface {
		NewTaskLedger(repository.VideoOwner, repository.VideoObjectLocationFactory) *VideoRepositoryTaskLedger
	})
	if !ok {
		t.Fatal("缺少按TaskInput授权读取的G6账本装配入口")
	}
	ledger := builder.NewTaskLedger(f.legacy.owner, videoG4TestLocationFactory{})
	loaded, err := ledger.Load(ctx, created.Job.ID)
	if err != nil || loaded.Reference == nil || loaded.Input == nil || loaded.Input.Version != f.asset.VersionNo || loaded.Input.SHA256 != f.reference.NormalizedSHA256 || videoPayloadSHA256(loaded.Reference.Bytes) != f.reference.NormalizedSHA256 {
		t.Fatalf("删除后仍应读取原冻结参考图：err=%v", err)
	}
	t.Run("发布移除I2V后不得读取正文", func(t *testing.T) {
		publish := func(version int, operations string) error {
			return f.legacy.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("UPDATE ai_model_release_versions SET status='retired' WHERE model_id=? AND status='active'", f.legacy.owner.UserID).Error; err != nil {
					return err
				}
				if err := tx.Exec("INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) SELECT model_id,?,'active',JSON_SET(snapshot_json,'$.video_contract.supported_operations',CAST(? AS JSON)),'合成任务读取操作撤销',created_by,UTC_TIMESTAMP() FROM ai_model_release_versions WHERE model_id=? AND version_no=1", version, operations, f.legacy.owner.UserID).Error; err != nil {
					return err
				}
				return tx.Exec("UPDATE token_models SET release_version_no=? WHERE id=?", version, f.legacy.owner.UserID).Error
			})
		}
		if err := publish(2, `["text_to_video"]`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := publish(3, `["text_to_video","image_to_video"]`); err != nil {
				t.Error(err)
			}
		}()
		beforeReads := countingStore.reads.Load()
		_, err := ledger.Load(ctx, created.Job.ID)
		if !errors.Is(err, ErrVideoOptionUnsupported) || countingStore.reads.Load() != beforeReads {
			t.Errorf("撤下操作必须在读取正文前拒绝：err=%v reads_delta=%d", err, countingStore.reads.Load()-beforeReads)
		}
	})
	t.Run("读取中取消不改变原任务资金及租约", func(t *testing.T) {
		before, err := app.GetPlatformTask(ctx, c.Caller, created.Job.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{})
		countingStore.beforeRead = func(readCtx context.Context) error { close(entered); <-readCtx.Done(); return readCtx.Err() }
		readCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := ledger.Load(readCtx, created.Job.ID); done <- err }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("未进入受控对象读取窗口")
		}
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Error("已取消读取不得返回成功")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("协作式取消未结束读取")
		}
		countingStore.beforeRead = nil
		after, err := app.GetPlatformTask(ctx, c.Caller, created.Job.ID, false)
		if err != nil || after.VersionNo != before.VersionNo || after.RequestVersionNo != before.RequestVersionNo || after.BillingStatus != "held" || after.HeldAmount == nil || *after.HeldAmount != "0.75000000" || after.CurrentFrozenAmount == nil || *after.CurrentFrozenAmount != "0.75000000" {
			t.Fatalf("取消读取不能推进任务或动用冻结资金：%+v err=%v", after, err)
		}
		bindings, err := repository.NewVideoTaskInputRepository(f.legacy.db).ListForOwner(ctx, created.Job.ID, f.legacy.owner)
		if err != nil || len(bindings) != 1 || bindings[0].LeaseReleasedAt != nil {
			t.Fatal("读取取消不能释放执行租约")
		}
	})
	// 整段实际Submit/Poll/归档/结算/交付只允许一个连接，嵌套Advance必须复用原事务。
	pool, err := f.legacy.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	previousLimit := pool.Stats().MaxOpenConnections
	pool.SetMaxOpenConns(1)
	defer pool.SetMaxOpenConns(previousLimit)
	singleCtx, singleCancel := context.WithTimeout(ctx, 20*time.Second)
	defer singleCancel()
	ctx = singleCtx
	adapter := video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess)
	gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: video.NewFakeVideoObjectStore()})
	if _, err := gateway.Submit(ctx, created.Job.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := gateway.Poll(ctx, created.Job.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gateway.FetchAndFinalize(ctx, created.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.billing.SettleReady(ctx, created.Job.ID, f.legacy.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := app.billing.DeliverReady(ctx, created.Job.ID, f.legacy.owner); err != nil {
		t.Fatal(err)
	}
	job, err := app.GetVideo(ctx, c.Caller, created.Job.ID)
	if err != nil || job.Status != "completed" || adapter.SubmitCalls() != 1 {
		t.Fatalf("删除申请不得阻断原任务，也不得重Submit：%+v err=%v", job, err)
	}
	bindings, err := repository.NewVideoTaskInputRepository(f.legacy.db).ListForOwner(ctx, created.Job.ID, f.legacy.owner)
	if err != nil || len(bindings) != 1 || bindings[0].InputVersion != f.asset.VersionNo || bindings[0].NormalizedSHA256 != f.reference.NormalizedSHA256 || bindings[0].LeaseReleasedAt == nil {
		t.Fatal("只有安全财务终态才能释放原冻结绑定")
	}
	if _, err := store.ReadNormalized(ctx, target.NormalizedBucket, target.NormalizedKey, videoUploadMaxBytes); err != nil {
		t.Fatal("执行完成不代表留存窗口已过，不应提前清理正文")
	}
}
