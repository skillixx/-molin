package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 模拟已经被远端接受、晚于调用取消才尝试落地的复制，证明目标围栏独立于客户端context生效。
type delayedVideoSaveCopy struct {
	VideoAssetSaveStore
	entered, release chan struct{}
	once             sync.Once
	lateError        chan error
}

func (s *delayedVideoSaveCopy) CopyImmutable(ctx context.Context, source, target video.VideoObjectRef, hash string, size uint64) (video.StoredVideoObject, error) {
	late := false
	s.once.Do(func() { late = true; close(s.entered); <-s.release })
	if late {
		value, err := s.VideoAssetSaveStore.CopyImmutable(context.Background(), source, target, hash, size)
		s.lateError <- err
		return value, err
	}
	return s.VideoAssetSaveStore.CopyImmutable(ctx, source, target, hash, size)
}

func TestVideoG6AssetSaveCleanupLateCopyMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	entID := f.EnableAssetSaving()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("expires_at", time.Now().UTC().Add(3*time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	var entitlement assetmodel.UserEntitlement
	if err := f.DB.First(&entitlement, entID).Error; err != nil {
		t.Fatal(err)
	}
	store := &delayedVideoSaveCopy{VideoAssetSaveStore: f.App.saveStore, entered: make(chan struct{}), release: make(chan struct{}), lateError: make(chan error, 1)}
	f.App.saveStore = store
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(store.release) }) }
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := f.App.SaveVideoAsset(ctx, VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}, root.PublicID, "g6-save-late-copy")
		done <- err
	}()
	select {
	case <-store.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("保存没有到达真实外部复制边界")
	}
	cancel()
	if wait := time.Until(entitlement.ExpiresAt.Add(50 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	var op videoAssetSave
	if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
		t.Fatal(err)
	}
	cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	result, err := f.App.CleanupVideoAssetSave(cleanupCtx, op.PublicID, repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &f.ProjectID}, VideoSaveCleanupPolicy{Purpose: "non_commercial_test_fixture", Version: "fixture-cleanup-v1"})
	if err != nil || result == nil || !result.Aborted {
		t.Fatalf("旧事务取消后必须能清理并建立围栏：%v", err)
	}
	release()
	select {
	case err := <-store.lateError:
		if !errors.Is(err, video.ErrVideoObjectConflict) {
			t.Fatalf("迟到写入必须由目标围栏拒绝，而非仅依赖取消context：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("迟到复制没有返回")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("旧保存执行者不能在清理完成后提交")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("旧保存执行者未结束")
	}
	var n int64
	if err := f.DB.Table("user_assets").Where("user_id=? AND asset_type='video_file'", f.ProjectID).Count(&n).Error; err != nil || n != 0 {
		t.Fatal("不得出现清理后的幽灵长期资产")
	}
	if err := f.DB.First(&entitlement, entID).Error; err != nil {
		t.Fatal(err)
	}
	if !entitlement.QuotaReserved.IsZero() || !entitlement.QuotaUsed.IsZero() {
		t.Fatal("迟到执行者不能再次占用或结转容量")
	}
}
