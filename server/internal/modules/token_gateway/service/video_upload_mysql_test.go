package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// Fake仅替换对象存储边界：封存返回不可变副本，丢弃产生不可复活墓碑。
type videoUploadMemoryEntry struct {
	target                  VideoUploadTarget
	raw, frozen, normalized []byte
	mime                    string
	sealed, discarded       bool
}
type videoUploadMemoryStore struct {
	sync.Mutex
	entries     map[string]*videoUploadMemoryEntry
	seals, puts int
	afterSeal   func()
	beforePut   func()
}

func (s *videoUploadMemoryStore) Issue(_ context.Context, t VideoUploadTarget) (*VideoUploadGrant, error) {
	s.Lock()
	defer s.Unlock()
	e := s.entries[t.SessionID]
	if e == nil {
		e = &videoUploadMemoryEntry{target: t}
		s.entries[t.SessionID] = e
	}
	if e.discarded || e.sealed {
		return nil, ErrVideoUploadConflict
	}
	return &VideoUploadGrant{Method: "PUT", URL: "http://127.0.0.1/isolated-upload/" + t.SessionID, Headers: map[string]string{"Content-Type": t.MIMEType}, ExpiresAt: t.UploadExpiresAt}, nil
}
func (s *videoUploadMemoryStore) write(id string, data []byte, mime string) error {
	s.Lock()
	defer s.Unlock()
	e := s.entries[id]
	if e == nil || e.discarded || e.sealed {
		return ErrVideoUploadConflict
	}
	e.raw = append([]byte(nil), data...)
	e.mime = mime
	return nil
}
func (s *videoUploadMemoryStore) Seal(_ context.Context, t VideoUploadTarget, max int64) (*VideoSealedUpload, error) {
	s.Lock()
	e := s.entries[t.SessionID]
	if e == nil || e.discarded || len(e.raw) == 0 || int64(len(e.raw)) > max {
		s.Unlock()
		return nil, ErrVideoUploadUnavailable
	}
	if !e.sealed {
		e.frozen = append([]byte(nil), e.raw...)
		e.sealed = true
		s.seals++
	}
	r := &VideoSealedUpload{Bytes: append([]byte(nil), e.frozen...), MIMEType: e.mime, ETag: videoBillingDigest(string(e.frozen)), VersionID: "immutable-fixture"}
	hook := s.afterSeal
	s.Unlock()
	if hook != nil {
		hook()
	}
	return r, nil
}
func (s *videoUploadMemoryStore) PutNormalized(_ context.Context, t VideoUploadTarget, data []byte, hash string) error {
	if s.beforePut != nil {
		s.beforePut()
	}
	s.Lock()
	defer s.Unlock()
	e := s.entries[t.SessionID]
	if e == nil || e.discarded {
		return ErrVideoUploadConflict
	}
	if len(e.normalized) != 0 && videoPayloadSHA256(e.normalized) != hash {
		return ErrVideoUploadConflict
	}
	if len(e.normalized) == 0 {
		s.puts++
		e.normalized = append([]byte(nil), data...)
	}
	return nil
}
func (s *videoUploadMemoryStore) ReadNormalized(_ context.Context, bucket, key string, max int64) ([]byte, error) {
	s.Lock()
	defer s.Unlock()
	for _, e := range s.entries {
		if !e.discarded && e.target.NormalizedBucket == bucket && e.target.NormalizedKey == key && len(e.normalized) > 0 && int64(len(e.normalized)) <= max {
			return append([]byte(nil), e.normalized...), nil
		}
	}
	return nil, ErrVideoUploadUnavailable
}
func (s *videoUploadMemoryStore) Discard(ctx context.Context, t VideoUploadTarget) error {
	s.Lock()
	defer s.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	e := s.entries[t.SessionID]
	if e == nil {
		e = &videoUploadMemoryEntry{target: t}
		s.entries[t.SessionID] = e
	}
	e.discarded = true
	e.raw = nil
	e.frozen = nil
	e.normalized = nil
	return nil
}

func (s *videoUploadMemoryStore) SupportsSynchronousDeletion() bool { return true }
func (s *videoUploadMemoryStore) VerifyDiscarded(ctx context.Context, t VideoUploadTarget) (bool, error) {
	s.Lock()
	defer s.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	e := s.entries[t.SessionID]
	return e != nil && e.target.SourceBucket == t.SourceBucket && e.target.SourceKey == t.SourceKey && e.target.NormalizedBucket == t.NormalizedBucket && e.target.NormalizedKey == t.NormalizedKey && e.discarded && len(e.raw) == 0 && len(e.frozen) == 0 && len(e.normalized) == 0, nil
}

var videoUploadFixtureSequence atomic.Uint64

func newVideoUploadFixture(t *testing.T) (*gorm.DB, *VideoUploadService, *videoUploadMemoryStore, VideoUploadCreateCommand, []byte) {
	t.Helper()
	db := openVideoG6MySQL(t)
	id := uint64(996700) + videoUploadFixtureSequence.Add(1)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO users(id,password_hash,status,real_name_status) VALUES(?,'fixture','active','verified')", []any{id}},
		{"INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,'受控上传测试','active','disabled','UTC')", []any{id, id}},
		{"INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(?,?,?,'g6',?,'合成上传Key','postpaid','allowlist','active',1)", []any{id, id, id, fmt.Sprintf("fixture-upload-%d", id)}},
		{"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='video:generate'", []any{id}},
	} {
		if err := db.Exec(stmt.sql, stmt.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	store := &videoUploadMemoryStore{entries: map[string]*videoUploadMemoryEntry{}}
	s, err := NewVideoUploadService(db, VideoUploadOptions{Store: store, Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), nil), SourceBucket: "g6-input-source", NormalizedBucket: "g6-input-normalized", ModerationPolicyVersion: "g6-fixture-input-v1", MaxUserReservedBytes: 128 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 640, 640))); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()
	c := VideoUploadCreateCommand{Caller: VideoCaller{UserID: id, ProjectID: id, APIKeyID: id}, IdempotencyKey: "g6-upload-create-0001", Filename: "reference.png", MIMEType: "image/png", SizeBytes: uint64(len(data)), SHA256: videoPayloadSHA256(data)}
	return db, s, store, c, data
}

func TestVideoG7UploadSessionRetentionMySQL(t *testing.T) {
	db, uploads, store, command, _ := newVideoUploadFixture(t)
	ctx := context.Background()
	createdAt := time.Now().UTC().Truncate(time.Second).Add(-25 * time.Hour)
	uploads.now = func() time.Time { return createdAt }
	created, err := uploads.Create(ctx, command)
	if err != nil || created.Upload == nil || created.Status != model.AIUploadSessionUploading {
		t.Fatalf("准备未完成上传会话失败: reply=%+v err=%v", created, err)
	}
	app := &VideoHTTPService{db: db, uploads: uploads}
	worker, err := NewVideoInputRetentionWorker(app, "upload-session-retention")
	if err != nil {
		t.Fatal(err)
	}
	completedAt := createdAt.Add(25 * time.Hour)
	worker.now = func() time.Time { return completedAt }
	if count, err := worker.RunOnce(ctx, 10); err != nil || count != 1 {
		t.Fatalf("24小时未完成会话必须墓碑化并收口: count=%d err=%v", count, err)
	}
	var session model.AIUploadSession
	if err := db.Where("public_id=?", created.SessionID).Take(&session).Error; err != nil || session.Status != model.AIUploadSessionExpired || session.ExpiredAt == nil || !session.ExpiredAt.Equal(completedAt) || session.FinalInputAssetID != nil {
		t.Fatalf("会话必须保留并进入expired: session=%+v err=%v", session, err)
	}
	var fact videoUploadSessionRetentionFact
	if err := db.Where("session_id=?", session.ID).Take(&fact).Error; err != nil || fact.PolicyVersion != "vid-g7-upload-session-retention-v1" || !fact.EligibleAt.Equal(createdAt.Add(24*time.Hour)) || !fact.CompletedAt.Equal(completedAt) {
		t.Fatalf("必须追加可追溯清理事实: fact=%+v err=%v", fact, err)
	}
	store.Lock()
	discarded := store.entries[created.SessionID] != nil && store.entries[created.SessionID].discarded
	store.Unlock()
	if !discarded {
		t.Fatal("数据库终态前必须完成不可复活墓碑")
	}
	if count, err := worker.RunOnce(ctx, 10); err != nil || count != 0 {
		t.Fatalf("已收口会话重跑必须零写: count=%d err=%v", count, err)
	}
}

func TestVideoG6UploadMySQLSealCompleteReplay(t *testing.T) {
	db, s, store, c, data := newVideoUploadFixture(t)
	ctx := context.Background()
	created, err := s.Create(ctx, c)
	if err != nil || created.Status != "uploading" || created.Upload == nil {
		t.Fatalf("创建受控会话失败：%v", err)
	}
	replayed, err := s.Create(ctx, c)
	if err != nil || !replayed.Idempotent || replayed.SessionID != created.SessionID || !replayed.Upload.ExpiresAt.Equal(created.Upload.ExpiresAt) {
		t.Fatal("创建重放不能续期或另建会话")
	}
	if err := store.write(created.SessionID, data, "image/png"); err != nil {
		t.Fatal(err)
	}
	// 模拟底层原对象在封存后被覆写；完成只能读取已经复制的不可变版本。
	store.afterSeal = func() {
		store.Lock()
		store.entries[created.SessionID].raw = []byte("malicious-after-seal")
		store.Unlock()
	}
	completed, err := s.Complete(ctx, c.Caller, created.SessionID, "g6-upload-complete-0001")
	if err != nil || completed.Status != "completed" || completed.InputAssetID == nil {
		t.Fatalf("封存完成失败：%v", err)
	}
	if err := store.write(created.SessionID, []byte("replacement"), "image/png"); !errors.Is(err, ErrVideoUploadConflict) {
		t.Fatal("旧上传能力必须失效")
	}
	again, err := s.Complete(ctx, c.Caller, created.SessionID, "g6-upload-complete-0001")
	if err != nil || !again.Idempotent || again.InputAssetID == nil || *again.InputAssetID != *completed.InputAssetID {
		t.Fatal("重复完成不能创建第二个输入")
	}
	owner := repository.VideoOwner{UserID: c.Caller.UserID, ProjectID: c.Caller.ProjectID, APIKeyID: &c.Caller.APIKeyID}
	asset, err := repository.NewVideoInputAssetRepository(db).FindReadyForBinding(ctx, *completed.InputAssetID, owner, time.Now())
	if err != nil || asset.OriginalSHA256 != c.SHA256 {
		t.Fatalf("只有校验过的原始字节可生成ready输入：%v", err)
	}
	ref, err := s.LoadReference(ctx, *asset)
	if err != nil || ref.NormalizedSHA256 != *asset.NormalizedSHA256 || ref.Width != 640 || ref.Height != 640 {
		t.Fatal("规范化读取不一致")
	}
	var n int64
	if err := db.Model(&model.AIGatewayInputAsset{}).Where("user_id=?", owner.UserID).Count(&n).Error; err != nil || n != 1 {
		t.Fatal("输入资产必须唯一")
	}
	if _, err := s.Cancel(ctx, c.Caller, created.SessionID, "g6-upload-cancel-0001"); !errors.Is(err, ErrVideoUploadConflict) {
		t.Fatal("已完成会话不能取消或删除发布资产")
	}
	foreign := c.Caller
	foreign.APIKeyID = 0
	if _, err := s.Get(ctx, foreign, created.SessionID); !errors.Is(err, repository.ErrVideoUploadNotFound) {
		t.Fatalf("跨Key会话查询必须统一404：%v", err)
	}
}

func TestVideoG6UploadMySQLRejectAndCancelRace(t *testing.T) {
	t.Run("原始hash不一致失败关闭", func(t *testing.T) {
		db, s, store, c, _ := newVideoUploadFixture(t)
		created, err := s.Create(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.write(created.SessionID, []byte("bad"), "image/png"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Complete(context.Background(), c.Caller, created.SessionID, "g6-upload-bad-complete-0001"); err == nil {
			t.Fatal("不允许提交被替换的输入")
		}
		state, err := s.Get(context.Background(), c.Caller, created.SessionID)
		if err != nil || state.Status != "rejected" || state.CleanupPending {
			t.Fatalf("拒绝后必须保留事实并清理对象：%v", err)
		}
		var n int64
		if err := db.Model(&model.AIGatewayInputAsset{}).Where("user_id=?", c.Caller.UserID).Count(&n).Error; err != nil || n != 0 {
			t.Fatal("拒绝不能创建输入资产")
		}
	})
	t.Run("取消与规范化写入竞争", func(t *testing.T) {
		db, s, store, c, data := newVideoUploadFixture(t)
		created, err := s.Create(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.write(created.SessionID, data, "image/png"); err != nil {
			t.Fatal(err)
		}
		store.beforePut = func() {
			if _, err := s.Cancel(context.Background(), c.Caller, created.SessionID, "g6-upload-race-cancel-0001"); err != nil {
				t.Error(err)
			}
		}
		if _, err := s.Complete(context.Background(), c.Caller, created.SessionID, "g6-upload-race-complete-0001"); err == nil {
			t.Fatal("取消获胜后完成不能发布")
		}
		state, err := s.Get(context.Background(), c.Caller, created.SessionID)
		if err != nil || state.Status != "cancelled" || state.InputAssetID != nil || state.CleanupPending {
			t.Fatal("取消必须保留终态并封堵迟到写入")
		}
		var n int64
		if err := db.Model(&model.AIGatewayInputAsset{}).Where("user_id=?", c.Caller.UserID).Count(&n).Error; err != nil || n != 0 {
			t.Fatal("不得留下悬空输入资产")
		}
	})
}

func TestVideoG6UploadMySQLInterruptedRetryAndConcurrency(t *testing.T) {
	t.Run("对象已写入后发布数据库暂时故障可同键恢复", func(t *testing.T) {
		db, s, store, c, data := newVideoUploadFixture(t)
		ctx := context.Background()
		created, err := s.Create(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.write(created.SessionID, data, "image/png"); err != nil {
			t.Fatal(err)
		}
		// 仅注入发布事务首次鉴权读的驱动故障；认领、对象写入及其余SQL真实执行。
		var armed atomic.Bool
		var injected atomic.Bool
		const callback = "g6_upload_publication_transient"
		if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
			if armed.Load() && tx.Statement.Table == "api_keys" && injected.CompareAndSwap(false, true) {
				tx.AddError(&mysqlDriver.MySQLError{Number: 1213, Message: "isolated publication deadlock"})
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
		store.beforePut = func() { armed.Store(true) }
		const key = "g6-upload-publication-retry-0001"
		if _, err := s.Complete(ctx, c.Caller, created.SessionID, key); !errors.Is(err, ErrVideoUploadUnavailable) || !injected.Load() {
			t.Fatalf("必须实际命中发布数据库故障并返回可重试错误：%v", err)
		}
		state, err := s.Get(ctx, c.Caller, created.SessionID)
		if err != nil || state.Status != "verifying" || state.InputAssetID != nil || state.CleanupPending {
			t.Fatalf("数据库暂时故障不得拒绝或清理封存输入：state=%+v err=%v", state, err)
		}
		store.Lock()
		entry := store.entries[created.SessionID]
		retained := !entry.discarded && len(entry.normalized) > 0 && store.puts == 1
		store.Unlock()
		if !retained {
			t.Fatal("发布失败必须保留已写入的不可变规范化对象")
		}
		completed, err := s.Complete(ctx, c.Caller, created.SessionID, key)
		if err != nil || completed.Status != "completed" || completed.InputAssetID == nil {
			t.Fatalf("数据库恢复后同键必须完成原会话：%v", err)
		}
		var count int64
		if err := db.Model(&model.AIGatewayInputAsset{}).Where("user_id=?", c.Caller.UserID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("恢复只能发布一个输入：count=%d err=%v", count, err)
		}
		store.Lock()
		defer store.Unlock()
		if store.puts != 1 || store.seals != 1 {
			t.Fatal("恢复不能另建规范化对象或重新封存可变源")
		}
	})
	t.Run("解码前取消不是内容错误且可同键恢复", func(t *testing.T) {
		_, s, store, c, data := newVideoUploadFixture(t)
		created, err := s.Create(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.write(created.SessionID, data, "image/png"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store.afterSeal = cancel
		if _, err := s.Complete(ctx, c.Caller, created.SessionID, "g6-upload-interrupted-0001"); !errors.Is(err, ErrVideoUploadUnavailable) {
			t.Fatalf("请求取消不能谎报图片无效：%v", err)
		}
		state, err := s.Get(context.Background(), c.Caller, created.SessionID)
		if err != nil || state.Status != "verifying" || state.InputAssetID != nil {
			t.Fatalf("暂时失败应保留可恢复的受控会话：%v", err)
		}
		store.afterSeal = nil
		result, err := s.Complete(context.Background(), c.Caller, created.SessionID, "g6-upload-interrupted-0001")
		if err != nil || result.Status != "completed" || result.InputAssetID == nil {
			t.Fatalf("原完成命令必须可恢复：%v", err)
		}
	})
	t.Run("首次完成100并发唯一发布", func(t *testing.T) {
		db, s, store, c, data := newVideoUploadFixture(t)
		created, err := s.Create(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.write(created.SessionID, data, "image/png"); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				r, err := s.Complete(context.Background(), c.Caller, created.SessionID, "g6-upload-concurrent-0001")
				if err != nil || r == nil || (r.Status != "verifying" && r.Status != "completed") {
					t.Errorf("并发完成应返回处理中或原完成：%v", err)
				}
			}()
		}
		close(start)
		wg.Wait()
		done, err := s.Complete(context.Background(), c.Caller, created.SessionID, "g6-upload-concurrent-0001")
		if err != nil || done.InputAssetID == nil || !done.Idempotent {
			t.Fatalf("最终应重放唯一完成结果：%v", err)
		}
		var n int64
		if err := db.Model(&model.AIGatewayInputAsset{}).Where("user_id=?", c.Caller.UserID).Count(&n).Error; err != nil || n != 1 {
			t.Fatalf("必须唯一输入：%d %v", n, err)
		}
		store.Lock()
		defer store.Unlock()
		if store.seals != 1 || store.puts != 1 {
			t.Fatalf("正常租约内只能一次封存和规范化写入：%d/%d", store.seals, store.puts)
		}
	})
}
