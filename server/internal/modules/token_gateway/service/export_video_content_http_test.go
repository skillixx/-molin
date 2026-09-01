package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	assetmodel "molin/server/internal/modules/asset/model"
	productmodel "molin/server/internal/modules/product/model"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 仅测试构建装配既有Fake异步执行边界；认证、创建、结算及交付仍执行真实服务。
type VideoContentHTTPFixture struct {
	FinancialSnapshot    func() []byte
	FailSaveAfterOne     func(bool)
	EnableAssetSaving    func() uint64
	EnableAssetDownloads func()
	VideoImportHTTPFixture
	Execute                 func(string)
	Submit                  func(string)
	SubmitCallbackFixture   func(string)
	TrySubmit               func(string) error
	SubmitCalls             func() int
	AdminPollProvider       VideoAdminPollProvider
	PrepareArchive          func(string)
	TryArchive              func(string) error
	RunArchiveRecovery      func(context.Context, VideoCaller, string, *repository.VideoArchiveFenceProof) error
	ArchiveOptions          func() VideoAdminArchiveOptions
	Settle                  func(string)
	Deliver                 func(string)
	FailHead                func(bool)
	HeadCalls               func() int64
	CreateCompletedForKey   func(uint64) string
	CreateQuarantinedForKey func(uint64) string
	ExpireLeaseOnRead       func()
	FailAfterFirstRange     func(bool)
	RangeCalls              func() int64
	MediaDeleteCalls        func() int64
	FailMediaDelete         func(bool)
	FailMediaConfirmation   func(bool)
	InspectMedia            func(string) map[string]VideoMediaFixtureFact
	WithInlineUploads       func(VideoUploadStore) *VideoHTTPService
}

type VideoMediaFixtureFact struct{ Present, Deleted, HashMatches bool }

// 仅测试构建允许包装外部删除存储；HTTP认证、准入和真实数据库协调器保持原实现。
func (f VideoContentHTTPFixture) WrapMediaDeleteStore(wrap func(VideoMediaDeleteStore) VideoMediaDeleteStore) {
	f.App.mediaDeleteStore = wrap(f.App.mediaDeleteStore)
}

// 故障只替换外部存储Head，不能替换鉴权或业务读取门禁。
type videoContentHTTPStore struct {
	*video.FakeVideoObjectStore
	fail             atomic.Bool
	heads            atomic.Int64
	expire           atomic.Bool
	onExpire         func() error
	failAfterFirst   atomic.Bool
	ranges           atomic.Int64
	deletes          atomic.Int64
	failDelete       atomic.Bool
	failConfirmation atomic.Bool
	failSave         atomic.Bool
	saveAttempts     atomic.Int64
}

// 只在外部复制边界注入部分成功，计划/额度/资产事务仍执行真实实现。
func (s *videoContentHTTPStore) CopyImmutable(ctx context.Context, source, target video.VideoObjectRef, hash string, size uint64) (video.StoredVideoObject, error) {
	n := s.saveAttempts.Add(1)
	if s.failSave.Load() && n > 1 {
		return video.StoredVideoObject{}, errors.New("合成复制边界失败")
	}
	return s.FakeVideoObjectStore.CopyImmutable(ctx, source, target, hash, size)
}

func (s *videoContentHTTPStore) Delete(ctx context.Context, ref video.VideoObjectRef) error {
	s.deletes.Add(1)
	if s.failDelete.Load() {
		return errors.New("合成媒体删除失败")
	}
	return s.FakeVideoObjectStore.Delete(ctx, ref)
}
func (s *videoContentHTTPStore) VerifyDeleted(ctx context.Context, ref video.VideoObjectRef) (bool, error) {
	ok, err := s.FakeVideoObjectStore.VerifyDeleted(ctx, ref)
	if ok && s.failConfirmation.Load() {
		return false, nil
	}
	return ok, err
}

// 仅测试在外部读取完成与HTTP写出之间跨过租约期限，不能由续约复活旧连接。
func (s *videoContentHTTPStore) GetRange(ctx context.Context, ref video.VideoObjectRef, offset, length int64) (io.ReadCloser, error) {
	s.ranges.Add(1)
	if s.failAfterFirst.Load() && offset >= 1<<20 {
		return nil, errors.New("合成第二片存储失败")
	}
	r, err := s.FakeVideoObjectStore.GetRange(ctx, ref, offset, length)
	if err != nil {
		return r, err
	}
	if s.expire.Swap(false) {
		if err := s.onExpire(); err != nil {
			_ = r.Close()
			return nil, err
		}
	}
	return r, nil
}

func (s *videoContentHTTPStore) Head(ctx context.Context, ref video.VideoObjectRef) (video.StoredVideoObject, error) {
	s.heads.Add(1)
	if s.fail.Load() {
		return video.StoredVideoObject{}, errors.New("仅测试的内部存储失败标记")
	}
	return s.FakeVideoObjectStore.Head(ctx, ref)
}

func NewVideoContentHTTPFixture(t *testing.T, playable ...bool) VideoContentHTTPFixture {
	t.Helper()
	f := NewVideoImportHTTPFixture(t)
	var media []byte
	if len(playable) > 1 {
		t.Fatal("测试媒体选择无效")
	}
	if len(playable) == 1 && playable[0] {
		media = videoG6PlayableFixture
	}
	store := &videoContentHTTPStore{FakeVideoObjectStore: video.NewFakeVideoObjectStore()}
	store.onExpire = func() error {
		return f.DB.Exec("UPDATE ai_video_download_leases SET lease_until=UTC_TIMESTAMP(6) WHERE user_id=? AND released_at IS NULL", f.ProjectID).Error
	}
	f.App.contentStore = store
	f.App.mediaDeleteStore = store
	inspect := func(id string) map[string]VideoMediaFixtureFact {
		t.Helper()
		var assets []model.AIImageAsset
		if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", id).Find(&assets).Error; err != nil {
			t.Fatal(err)
		}
		out := map[string]VideoMediaFixtureFact{}
		for _, a := range assets {
			if a.Bucket == nil || a.ObjectKey == nil || a.SHA256 == nil {
				t.Fatal("夹具必须有原对象位置/hash")
			}
			ref := video.VideoObjectRef{Bucket: *a.Bucket, ObjectKey: *a.ObjectKey}
			meta, err := store.FakeVideoObjectStore.Head(context.Background(), ref)
			deleted, e := store.FakeVideoObjectStore.VerifyDeleted(context.Background(), ref)
			if e != nil {
				t.Fatal(e)
			}
			out[a.AssetRole] = VideoMediaFixtureFact{Present: err == nil, Deleted: deleted, HashMatches: err == nil && meta.SHA256 == *a.SHA256}
		}
		return out
	}
	owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &f.ProjectID}
	adapter := newVideoContentFixtureProvider(media)
	gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: f.App.NewTaskLedger(owner, videoG4TestLocationFactory{}), Provider: adapter, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: store})
	var completedCounter atomic.Uint64
	return VideoContentHTTPFixture{VideoImportHTTPFixture: f, AdminPollProvider: adapter, PrepareArchive: func(id string) {
		t.Helper()
		if _, err := gateway.Submit(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := gateway.Poll(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}
	}, TryArchive: func(id string) error { _, err := gateway.FetchAndFinalize(context.Background(), id); return err }, RunArchiveRecovery: func(ctx context.Context, caller VideoCaller, id string, proof *repository.VideoArchiveFenceProof) error {
		admin, err := NewVideoAdminService(f.App, 24)
		if err != nil {
			return err
		}
		return runVideoArchiveRecovery(ctx, admin, caller, id, owner, proof, videoArchiveExecutionOptions{content: adapter, store: store, probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), locator: videoG4TestLocationFactory{}})
	}, ArchiveOptions: func() VideoAdminArchiveOptions {
		return VideoAdminArchiveOptions{Content: adapter, Store: store, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Locator: videoG4TestLocationFactory{}}
	}, FinancialSnapshot: func() []byte { return mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID) }, FailSaveAfterOne: func(fail bool) { store.saveAttempts.Store(0); store.failSave.Store(fail) }, EnableAssetSaving: func() uint64 {
		// 单独创建合成存储商品/权益，不借用模型商品或真实用户资产；许可仍经真实角色关联检查。
		p := productmodel.Product{ProductType: "storage", ProductCode: fmt.Sprintf("video-save-fixture-%d", f.ProjectID), Name: "合成存储商品", Status: "active"}
		if err := f.DB.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		role := struct {
			ID         uint64
			Code, Name string
		}{Code: fmt.Sprintf("video-save-role-%d", f.ProjectID), Name: "合成存储角色"}
		if err := f.DB.Table("roles").Create(&role).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.DB.Exec("INSERT INTO user_roles(user_id,role_id) VALUES(?,?)", f.ProjectID, role.ID).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.DB.Exec("INSERT INTO product_role_access(product_id,role_id,can_view,can_buy,can_use) VALUES(?,?,1,1,1)", p.ID, role.ID).Error; err != nil {
			t.Fatal(err)
		}
		started := time.Now().UTC().Add(-time.Hour)
		expires := time.Now().UTC().Add(time.Hour)
		a := assetmodel.UserAsset{UserID: f.ProjectID, ProductID: p.ID, AssetType: "storage", Status: "active", StartedAt: &started, ExpiresAt: &expires}
		if err := f.DB.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
		total := decimal.NewFromInt(1 << 30)
		unit := "bytes"
		ent := assetmodel.UserEntitlement{UserID: f.ProjectID, AssetID: a.ID, ProductID: p.ID, EntitlementType: "storage_bytes", QuotaUnit: &unit, QuotaTotal: &total, Status: "active", StartedAt: &started, ExpiresAt: &expires}
		if err := f.DB.Create(&ent).Error; err != nil {
			t.Fatal(err)
		}
		f.App.saveStore = store
		f.App.savePolicy = &VideoAssetSavePolicy{Version: "fixture-save-v1", StorageProductID: p.ID, EntitlementType: "storage_bytes", QuotaUnit: unit, AllowedModels: []string{f.Model}, MaxUserBytes: 1 << 30, MaxProjectBytes: 2 << 30, MaxGlobalBytes: 4 << 30, GlobalAlertBytes: 3 << 30}
		return ent.ID
	}, EnableAssetDownloads: func() {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			t.Fatal(err)
		}
		f.App.downloadSecret = secret
	}, MediaDeleteCalls: store.deletes.Load, FailMediaDelete: store.failDelete.Store, FailMediaConfirmation: store.failConfirmation.Store, InspectMedia: inspect, WithInlineUploads: func(uploadStore VideoUploadStore) *VideoHTTPService {
		inline := f.WithUploads(uploadStore)
		inline.contentStore = store
		inline.mediaDeleteStore = store
		return inline
	}, Submit: func(id string) {
		t.Helper()
		if _, err := gateway.Submit(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}, SubmitCallbackFixture: func(id string) {
		t.Helper()
		// 回调矩阵需要同时保留多个真实submitted任务；额外夹具仅绕开另有专测的G6容量裁决，
		// Task、Provider绑定、事件、回调与G5财务账本仍走真实实现，不能直接改写任务状态。
		ledger := NewVideoBillingTaskLedger(f.DB, owner, f.App.billing.protector, videoG4TestLocationFactory{}, f.App.billing.referenceLoader)
		ledger.taskReferenceLoader = f.App.loadTaskReference
		callbackGateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: store})
		if _, err := callbackGateway.Submit(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}, TrySubmit: func(id string) error { _, err := gateway.Submit(context.Background(), id); return err }, SubmitCalls: adapter.SubmitCalls, FailAfterFirstRange: store.failAfterFirst.Store, RangeCalls: store.ranges.Load, FailHead: store.fail.Store, HeadCalls: store.heads.Load, ExpireLeaseOnRead: func() { store.expire.Store(true) }, CreateCompletedForKey: func(keyID uint64) string {
		t.Helper()
		caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: keyID}
		job, err := f.App.Create(context.Background(), VideoCommand{Caller: caller, IdempotencyKey: fmt.Sprintf("g6-completed-key-%d-%d", keyID, completedCounter.Add(1)), Model: f.Model, Prompt: "仅用于跨Key下载限额的合成视频", Operation: model.AIVideoOperationTextToVideo})
		if err != nil {
			t.Fatal(err)
		}
		owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: optionalUint64(keyID)}
		adapter := newVideoContentFixtureProvider(media)
		g := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: f.App.NewTaskLedger(owner, videoG4TestLocationFactory{}), Provider: adapter, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: store})
		ctx := context.Background()
		if _, err := g.Submit(ctx, job.Job.ID); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := g.Poll(ctx, job.Job.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := g.FetchAndFinalize(ctx, job.Job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.App.billing.SettleReady(ctx, job.Job.ID, owner); err != nil {
			t.Fatal(err)
		}
		if _, err := f.App.billing.DeliverReady(ctx, job.Job.ID, owner); err != nil {
			t.Fatal(err)
		}
		return job.Job.ID
	}, CreateQuarantinedForKey: func(keyID uint64) string {
		t.Helper()
		// 通过真实G4隔离链形成拒绝事实，绝不改写已完成的不可变审核结论或关闭SQL守卫。
		caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: keyID}
		job, err := f.App.Create(context.Background(), VideoCommand{Caller: caller, IdempotencyKey: "g6-quarantined-fixture", Model: f.Model, Prompt: "仅用于合成隔离资产验收", Operation: model.AIVideoOperationTextToVideo})
		if err != nil {
			t.Fatal(err)
		}
		owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: optionalUint64(keyID)}
		g := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: f.App.NewTaskLedger(owner, videoG4TestLocationFactory{}), Provider: newVideoContentFixtureProvider(media), Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationRejectFrames), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: store})
		ctx := context.Background()
		if _, err := g.Submit(ctx, job.Job.ID); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := g.Poll(ctx, job.Job.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := g.FetchAndFinalize(ctx, job.Job.ID); !errors.Is(err, video.ErrVideoModerationRejected) {
			t.Fatal("隔离夹具必须实际命中视频帧审核拒绝")
		}
		return job.Job.ID
	}, Execute: func(id string) {
		t.Helper()
		ctx := context.Background()
		if _, err := gateway.Submit(ctx, id); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := gateway.Poll(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := gateway.FetchAndFinalize(ctx, id); err != nil {
			t.Fatal(err)
		}
	}, Settle: func(id string) {
		t.Helper()
		if _, err := f.App.billing.SettleReady(context.Background(), id, owner); err != nil {
			t.Fatal(err)
		}
	}, Deliver: func(id string) {
		t.Helper()
		if _, err := f.App.billing.DeliverReady(context.Background(), id, owner); err != nil {
			t.Fatal(err)
		}
	}}
}
