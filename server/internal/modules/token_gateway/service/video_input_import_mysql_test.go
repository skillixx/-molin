package service

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 原图来自真实IMG-G5测试归档；这里只替换受限对象读写，目标墓碑禁止迟到写入复活。
type videoImportMemoryStore struct {
	sync.Mutex
	source                imagegateway.ObjectStore
	objects               map[VideoImportObject][]byte
	tombstones            map[VideoImportObject]bool
	reads, puts, discards int
	putUnknown            bool
	afterRead             func()
	afterPut              func(VideoImportObject)
}

func (s *videoImportMemoryStore) Read(ctx context.Context, o VideoImportObject, max int64) ([]byte, error) {
	s.Lock()
	s.reads++
	data, ok := s.objects[o]
	hook := s.afterRead
	s.Unlock()
	if !ok {
		var err error
		data, err = s.source.Get(ctx, imagegateway.ObjectRef{Bucket: o.Bucket, Key: o.Key})
		if err != nil {
			return nil, err
		}
	}
	if int64(len(data)) > max {
		return nil, ErrVideoImportInvalid
	}
	copy := append([]byte(nil), data...)
	if hook != nil {
		hook()
	}
	return copy, nil
}
func (s *videoImportMemoryStore) Put(_ context.Context, o VideoImportObject, data []byte, hash string) error {
	s.Lock()
	if s.tombstones[o] {
		s.Unlock()
		return ErrVideoImportConflict
	}
	if old, ok := s.objects[o]; ok {
		s.Unlock()
		if videoPayloadSHA256(old) != hash {
			return ErrVideoImportConflict
		}
		return nil
	}
	s.objects[o] = append([]byte(nil), data...)
	s.puts++
	unknown, hook := s.putUnknown, s.afterPut
	s.putUnknown = false
	s.Unlock()
	if hook != nil {
		hook(o)
	}
	if unknown {
		return ErrVideoImportUnavailable
	}
	return nil
}
func (s *videoImportMemoryStore) Discard(ctx context.Context, o VideoImportObject) error {
	s.Lock()
	defer s.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.tombstones[o] = true
	delete(s.objects, o)
	s.discards++
	return nil
}

func (s *videoImportMemoryStore) SupportsSynchronousDeletion() bool { return true }
func (s *videoImportMemoryStore) VerifyDiscarded(ctx context.Context, o VideoImportObject) (bool, error) {
	s.Lock()
	defer s.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, exists := s.objects[o]
	return s.tombstones[o] && !exists, nil
}

func exerciseVideoInputImport(t *testing.T, db *gorm.DB, source imagegateway.ObjectStore, caller VideoCaller, assetID string) {
	t.Helper()
	ctx := context.Background()
	store := &videoImportMemoryStore{source: source, objects: map[VideoImportObject][]byte{}, tombstones: map[VideoImportObject]bool{}}
	importer, err := NewVideoInputImportService(db, VideoInputImportOptions{Store: store, Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), nil), NormalizedBucket: "g6-import-normalized", ModerationPolicyVersion: "g6-import-fixture-v1", MaxUserReservedBytes: 128 << 20})
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]int64{}
	for _, table := range []string{"ai_requests", "ai_gateway_quotes", "wallet_holds", "ai_gateway_tasks"} {
		var n int64
		if err := db.Table(table).Where("user_id=?", caller.UserID).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		before[table] = n
	}
	c := VideoInputImportCommand{Caller: caller, IdempotencyKey: "g6-import-concurrent-0001", SourceAssetID: assetID}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := importer.Import(ctx, c)
			if err != nil || r == nil || (r.Status != "processing" && r.Status != "completed") {
				t.Errorf("同键导入必须处理中或原完成：%v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	done, err := importer.Import(ctx, c)
	if err != nil || done.Status != "completed" || done.InputAssetID == nil || !done.Idempotent {
		t.Fatalf("导入最终必须重放唯一完成：%+v %v", done, err)
	}
	var asset model.AIGatewayInputAsset
	if err := db.Where("public_id=?", *done.InputAssetID).First(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if asset.SourceType != "gateway_asset_snapshot" || asset.UploadSessionID != nil || asset.SourceGatewayAssetID == nil || asset.LifecycleState != "ready" || asset.NormalizedSHA256 == nil {
		t.Fatal("必须为独立规范化输入，不能伪造UploadSession")
	}
	ref, err := importer.LoadReference(ctx, asset)
	if err != nil || ref.NormalizedSHA256 != *asset.NormalizedSHA256 || ref.Width != 640 {
		t.Fatal("导入输入必须可经同一参考图读取接口校验")
	}
	store.Lock()
	puts := store.puts
	store.Unlock()
	if puts != 1 {
		t.Fatalf("同键100并发只允许一份规范化对象：%d", puts)
	}
	var controls int64
	if err := db.Table("ai_video_input_imports").Where("user_id=?", caller.UserID).Count(&controls).Error; err != nil || controls != 1 {
		t.Fatal("导入回执必须唯一")
	}
	var reserved uint64
	if err := db.Table("ai_video_input_imports").Select("reserved_bytes").Where("input_asset_id=?", asset.ID).Scan(&reserved).Error; err != nil || reserved != *asset.SizeBytes {
		t.Fatal("完成后预留应转为实际输入字节占用")
	}
	wrong := c
	wrong.SourceAssetID = "img_other_public_fixture"
	if _, err := importer.Import(ctx, wrong); !errors.Is(err, ErrVideoImportConflict) {
		t.Fatalf("同键异源必须冲突：%v", err)
	}
	foreign := c
	foreign.Caller.APIKeyID = 0
	if _, err := importer.Import(ctx, foreign); !errors.Is(err, repository.ErrVideoInputNotFound) {
		t.Fatalf("跨Key不能探测导入命令：%v", err)
	}
	// 目标已经写成但响应未知时，不清理或重新分配输入，原命令可恢复。
	store.putUnknown = true
	c.IdempotencyKey = "g6-import-unknown-put-0001"
	if _, err := importer.Import(ctx, c); !errors.Is(err, ErrVideoImportUnavailable) {
		t.Fatalf("必须命中写入响应未知：%v", err)
	}
	var pending videoInputImportRecord
	if err := db.Where("user_id=? AND status='processing'", caller.UserID).Take(&pending).Error; err != nil {
		t.Fatal(err)
	}
	recovered, err := importer.Import(ctx, c)
	if err != nil || recovered.Status != "completed" || recovered.InputAssetID == nil {
		t.Fatalf("原命令应恢复已写对象：%v", err)
	}
	if recovered.ImportID != pending.PublicID || *recovered.InputAssetID != pending.InputPublicID {
		t.Fatal("未知写入恢复必须保持原命令与输入身份")
	}
	store.Lock()
	puts = store.puts
	store.Unlock()
	if puts != 2 {
		t.Fatal("写入未知恢复不能创建第二份目标")
	}
	// 源版本在读出后变化，发布必须失败并只清理新目标，原件保持可读。
	c.IdempotencyKey = "g6-import-source-drift-0001"
	var once sync.Once
	store.afterRead = func() {
		once.Do(func() {
			if err := db.Exec("UPDATE ai_gateway_assets SET version_no=version_no+1 WHERE public_id=?", assetID).Error; err != nil {
				t.Error(err)
			}
		})
	}
	if _, err := importer.Import(ctx, c); !errors.Is(err, ErrVideoImportConflict) {
		t.Fatalf("来源漂移必须失败关闭：%v", err)
	}
	var rejected videoInputImportRecord
	if err := db.Where("user_id=? AND status='rejected'", caller.UserID).Take(&rejected).Error; err != nil || rejected.CleanupPending || rejected.CleanedAt == nil {
		t.Fatal("源漂移必须登记目标清理和占额释放")
	}
	store.Lock()
	cleaned := store.tombstones[rejected.target()]
	_, exists := store.objects[rejected.target()]
	store.Unlock()
	if !cleaned || exists {
		t.Fatal("源漂移目标必须实际删除并建立墓碑")
	}
	// 保全期间保留目标；解除保全后仍用原命令清理，不建立新输入或恢复生成。
	store.afterRead = nil
	c.IdempotencyKey = "g6-import-held-target-0001"
	store.afterPut = func(target VideoImportObject) {
		if err := db.Exec("UPDATE ai_gateway_input_assets i JOIN ai_video_input_imports c ON c.input_asset_id=i.id SET i.legal_hold=1,i.version_no=i.version_no+1 WHERE c.normalized_bucket=? AND c.normalized_key=?", target.Bucket, target.Key).Error; err != nil {
			t.Error(err)
		}
	}
	if _, err := importer.Import(ctx, c); !errors.Is(err, ErrVideoImportUnavailable) {
		t.Fatalf("目标保全应阻止发布和清理：%v", err)
	}
	var held videoInputImportRecord
	if err := db.Where("user_id=? AND status='rejected' AND cleanup_pending=1", caller.UserID).Take(&held).Error; err != nil {
		t.Fatal(err)
	}
	store.Lock()
	_, preserved := store.objects[held.target()]
	tombstone := store.tombstones[held.target()]
	store.Unlock()
	if !preserved || tombstone {
		t.Fatal("保全期间不得删除目标")
	}
	store.afterPut = nil
	if err := db.Exec("UPDATE ai_gateway_input_assets SET legal_hold=0,version_no=version_no+1 WHERE id=?", held.InputAssetID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(ctx, c); !errors.Is(err, ErrVideoImportConflict) {
		t.Fatalf("解除保全后原拒绝命令应清理完并保持拒绝：%v", err)
	}
	var released videoInputImportRecord
	if err := db.Where("input_asset_id=?", held.InputAssetID).Take(&released).Error; err != nil || released.CleanupPending || released.CleanedAt == nil {
		t.Fatal("解除保全必须能恢复清理及释放占额")
	}
	store.Lock()
	_, exists = store.objects[held.target()]
	cleaned = store.tombstones[held.target()]
	store.Unlock()
	if exists || !cleaned {
		t.Fatal("解除保全后目标必须实际清理")
	}
	store.Lock()
	discardCount := store.discards
	store.Unlock()
	if _, err := importer.Import(ctx, c); !errors.Is(err, ErrVideoImportConflict) {
		t.Fatal("原拒绝命令重放不得恢复生成")
	}
	store.Lock()
	discardAfter := store.discards
	store.Unlock()
	if discardAfter != discardCount {
		t.Fatal("已清理命令重放不得重复Discard")
	}
	exerciseVideoImportScopeRevocation(t, db, importer, store, caller, assetID)
	var original model.AIImageAsset
	if err := db.Where("public_id=?", assetID).First(&original).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := source.Get(ctx, imagegateway.ObjectRef{Bucket: *original.Bucket, Key: *original.ObjectKey})
	if err != nil || !bytes.Equal([]byte(videoPayloadSHA256(raw)), []byte(*original.SHA256)) {
		t.Fatal("失败导入不得删除原图")
	}
	for table, n := range before {
		var after int64
		if err := db.Table(table).Where("user_id=?", caller.UserID).Count(&after).Error; err != nil || after != n {
			t.Fatalf("导入不得新建财务或任务：%s", table)
		}
	}
}

// 在发布事务已经建立RR快照后，由另一连接提交撤权；旧快照不能继续发布。
func exerciseVideoImportScopeRevocation(t *testing.T, db *gorm.DB, importer *VideoInputImportService, store *videoImportMemoryStore, caller VideoCaller, assetID string) {
	t.Helper()
	ctx := context.Background()
	var isolation string
	if err := db.Raw("SELECT @@transaction_isolation").Scan(&isolation).Error; err != nil || isolation != "REPEATABLE-READ" {
		t.Fatalf("撤权反例需要真实RR默认隔离：%s %v", isolation, err)
	}
	var armed, revoked atomic.Bool
	const callback = "g6_import_scope_revoke_after_snapshot"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "api_keys" || !armed.CompareAndSwap(true, false) {
			return
		}
		deadline, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		changed := db.WithContext(deadline).Exec("DELETE FROM api_key_model_scopes WHERE api_key_id=? AND user_id=? AND project_id=? AND logical_model_code=?", caller.APIKeyID, caller.UserID, caller.ProjectID, imageG5ModelCode)
		if changed.Error != nil {
			tx.AddError(changed.Error)
			return
		}
		if changed.RowsAffected != 1 {
			tx.AddError(errors.New("撤权反例未删除精确授权行"))
			return
		}
		revoked.Store(true)
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		store.afterPut = nil
		_ = db.Callback().Query().Remove(callback)
		if revoked.Load() {
			if err := db.Exec("INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(?,?,?,?)", caller.APIKeyID, caller.ProjectID, caller.UserID, imageG5ModelCode).Error; err != nil {
				t.Error(err)
			}
		}
	}()
	store.afterPut = func(VideoImportObject) { armed.Store(true) }
	c := VideoInputImportCommand{Caller: caller, SourceAssetID: assetID, IdempotencyKey: "g6-import-scope-rr-0001"}
	if _, err := importer.Import(ctx, c); !errors.Is(err, repository.ErrVideoInputNotFound) || !revoked.Load() {
		t.Fatalf("已提交撤权后必须404，不能使用旧RR授权：revoked=%v err=%v", revoked.Load(), err)
	}
	var record videoInputImportRecord
	if err := db.Where("user_id=? AND command_key_hash=?", caller.UserID, videoBillingDigest("input_import\x00"+c.IdempotencyKey)).Take(&record).Error; err != nil || record.Status != "rejected" || record.CleanupPending || record.CleanedAt == nil {
		t.Fatal("撤权必须拒绝未发布目标并收口清理")
	}
	store.Lock()
	_, exists := store.objects[record.target()]
	discarded := store.tombstones[record.target()]
	store.Unlock()
	if exists || !discarded {
		t.Fatal("撤权目标不得公开或悬留")
	}
}
