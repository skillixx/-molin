package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 故障只注入对象存储边界；数据库、事务及完成事实仍走真实实现。
type videoCleanupFaultStore struct {
	*videoUploadMemoryStore
	discardFailure   error
	verifyFailure    error
	denyVerification bool
	unsupported      bool
}

func (s *videoCleanupFaultStore) SupportsSynchronousDeletion() bool { return !s.unsupported }
func (s *videoCleanupFaultStore) Discard(ctx context.Context, target VideoUploadTarget) error {
	if s.discardFailure != nil {
		return s.discardFailure
	}
	return s.videoUploadMemoryStore.Discard(ctx, target)
}
func (s *videoCleanupFaultStore) VerifyDiscarded(ctx context.Context, target VideoUploadTarget) (bool, error) {
	if s.verifyFailure != nil {
		return false, s.verifyFailure
	}
	if s.denyVerification {
		return false, nil
	}
	return s.videoUploadMemoryStore.VerifyDiscarded(ctx, target)
}

// 清理实际删除Fake对象正文并验证围栏，不能只写deleted标志或把读取错误当不存在。
func TestVideoG6InputCleanupMySQLUpload(t *testing.T) {
	v := newVideoG6I2VFixture(t)
	db, upload, store, c, data := newVideoUploadFixture(t)
	ctx := context.Background()
	created, err := upload.Create(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.write(created.SessionID, data, "image/png"); err != nil {
		t.Fatal(err)
	}
	completed, err := upload.Complete(ctx, c.Caller, created.SessionID, "g6-cleanup-upload-complete")
	if err != nil || completed.InputAssetID == nil {
		t.Fatalf("上传必须实际完成：%v", err)
	}
	faultStore := &videoCleanupFaultStore{videoUploadMemoryStore: store}
	uploadOptions := upload.options
	uploadOptions.Store = faultStore
	app, err := NewVideoHTTPService(db, VideoBillingOptions{QuoteSecret: v.legacy.service.quoteSecret, PromptSecret: v.legacy.service.promptSecret, IntentSecret: v.legacy.service.intentSecret, Protector: v.legacy.service.protector, Safety: v.legacy.service.safety}, VideoHTTPOptions{Uploads: &uploadOptions})
	if err != nil {
		t.Fatal(err)
	}
	var asset model.AIGatewayInputAsset
	if err := db.Where("public_id=?", *completed.InputAssetID).Take(&asset).Error; err != nil {
		t.Fatal(err)
	}
	requested, err := app.RequestInputDeletion(ctx, c.Caller, asset.PublicID, asset.VersionNo, "g6-input-cleanup-delete")
	if err != nil {
		t.Fatal(err)
	}
	cleaner, ok := any(app).(interface {
		CleanupInput(context.Context, string, repository.VideoOwner, VideoInputCleanupPolicy) (*VideoInputDeletionReply, error)
	})
	if !ok {
		t.Fatal("缺少实际输入清理与独立删除确认入口")
	}
	clock := time.Now().UTC()
	policy := VideoInputCleanupPolicy{Purpose: "non_commercial_test_fixture", Version: "g6-input-retention-fixture", BoundRetention: 7 * 24 * time.Hour, Now: func() time.Time { return clock }}
	owner := repository.VideoOwner{UserID: c.Caller.UserID, ProjectID: c.Caller.ProjectID, APIKeyID: &c.Caller.APIKeyID}
	waiting, err := cleaner.CleanupInput(ctx, asset.PublicID, owner, policy)
	if err != nil || waiting.MediaDeleted || waiting.LifecycleState != "pending_delete" {
		t.Fatalf("留存期未结束不能清理：%+v err=%v", waiting, err)
	}
	store.Lock()
	e := store.entries[created.SessionID]
	preserved := e != nil && !e.discarded && len(e.raw) > 0 && len(e.frozen) > 0 && len(e.normalized) > 0
	store.Unlock()
	if !preserved {
		t.Fatal("留存期间必须保留实际三类对象")
	}
	clock = asset.ExpiresAt.Add(time.Second)
	assertPending := func(t *testing.T) {
		t.Helper()
		var current model.AIGatewayInputAsset
		if err := db.First(&current, asset.ID).Error; err != nil || current.LifecycleState != "pending_delete" || current.VersionNo != requested.VersionNo || current.DeletedAt != nil {
			t.Fatal("失败不能伪造数据库删除完成")
		}
		var control videoUploadControl
		if err := db.Where("session_id=?", *asset.UploadSessionID).Take(&control).Error; err != nil || control.CleanedAt != nil {
			t.Fatal("未确认删除不得释放容量预留")
		}
		var facts int64
		if err := db.Table("ai_video_input_cleanup_facts").Where("input_asset_id=?", asset.ID).Count(&facts).Error; err != nil || facts != 0 {
			t.Fatal("未完成不得产生清理成功事实")
		}
	}
	for _, name := range []string{"不支持可靠同步删除", "删除失败"} {
		t.Run(name, func(t *testing.T) {
			failure := errors.New("合成存储清理故障")
			faultStore.unsupported = name == "不支持可靠同步删除"
			if name == "删除失败" {
				faultStore.discardFailure = failure
			}
			result, err := cleaner.CleanupInput(ctx, asset.PublicID, owner, policy)
			faultStore.unsupported, faultStore.discardFailure, faultStore.verifyFailure, faultStore.denyVerification = false, nil, nil, false
			if err == nil || result != nil {
				t.Fatalf("失败不能返回完成回执：%+v %v", result, err)
			}
			assertPending(t)
		})
	}
	t.Run("对象已删但确认写入失败", func(t *testing.T) {
		store.Lock()
		original := store.entries[created.SessionID]
		presentBefore := original != nil && !original.discarded && len(original.raw) > 0 && len(original.frozen) > 0 && len(original.normalized) > 0
		store.Unlock()
		if !presentBefore {
			t.Fatal("首次删除失败窗口必须从真实三类正文存在开始")
		}
		failure := errors.New("合成清理事实INSERT失败")
		const hook = "g6_cleanup_fact_insert_failure"
		deletedBeforeInsert := false
		if err := db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
			if tx.Statement.Table == "ai_video_input_cleanup_facts" {
				store.Lock()
				current := store.entries[created.SessionID]
				deletedBeforeInsert = current != nil && current.discarded && len(current.raw) == 0 && len(current.frozen) == 0 && len(current.normalized) == 0
				store.Unlock()
				tx.AddError(failure)
			}
		}); err != nil {
			t.Fatal(err)
		}
		result, err := cleaner.CleanupInput(ctx, asset.PublicID, owner, policy)
		db.Callback().Create().Remove(hook)
		if !errors.Is(err, failure) || result != nil || !deletedBeforeInsert {
			t.Fatalf("确认写入失败必须保留未知/未完成：%+v %v", result, err)
		}
		assertPending(t)
	})
	// 原目标已删除但数据库仍待确认；确认失败或报告未清理时，重试也不得伪造成功。
	for _, name := range []string{"删除确认读取失败", "确认报告未清理"} {
		t.Run(name, func(t *testing.T) {
			if name == "删除确认读取失败" {
				faultStore.verifyFailure = errors.New("合成确认读取故障")
			}
			faultStore.denyVerification = name == "确认报告未清理"
			result, err := cleaner.CleanupInput(ctx, asset.PublicID, owner, policy)
			faultStore.verifyFailure, faultStore.denyVerification = nil, false
			if err == nil || result != nil {
				t.Fatalf("确认失败不能返回完成：%+v %v", result, err)
			}
			assertPending(t)
		})
	}
	done, err := cleaner.CleanupInput(ctx, asset.PublicID, owner, policy)
	if err != nil || !done.MediaDeleted || done.LifecycleState != "deleted" || done.VersionNo != requested.VersionNo+2 {
		t.Fatalf("到期后应确认真实删除：%+v err=%v", done, err)
	}
	store.Lock()
	e = store.entries[created.SessionID]
	removed := e != nil && e.discarded && len(e.raw) == 0 && len(e.frozen) == 0 && len(e.normalized) == 0
	store.Unlock()
	if !removed {
		t.Fatal("不能用数据库标志代替原件/封存/规范化正文删除")
	}
	if err := store.write(created.SessionID, data, "image/png"); err == nil {
		t.Fatal("迟到上传不能复活已清理对象")
	}
	again, err := cleaner.CleanupInput(ctx, asset.PublicID, owner, policy)
	if err != nil || !again.MediaDeleted || !again.Idempotent || again.VersionNo != done.VersionNo {
		t.Fatalf("重复清理必须复用唯一完成事实：%v", err)
	}
	history, err := app.RequestInputDeletion(ctx, c.Caller, asset.PublicID, asset.VersionNo, "g6-input-cleanup-delete")
	if err != nil || !history.MediaDeleted || history.LifecycleState != "deleted" || !history.Idempotent || !history.DeleteRequestedAt.Equal(requested.DeleteRequestedAt) {
		t.Fatalf("已确认清理后必须返回原归属的完成回执：%+v %v", history, err)
	}
	var facts int64
	if err := db.Table("ai_video_input_cleanup_facts").Where("input_asset_id=?", asset.ID).Count(&facts).Error; err != nil || facts != 1 {
		t.Fatalf("必须存在唯一不可变清理事实：%d %v", facts, err)
	}
	var control videoUploadControl
	if err := db.Where("session_id=?", *asset.UploadSessionID).Take(&control).Error; err != nil || control.CleanedAt == nil || control.CleanupPending {
		t.Fatal("对象确认与原容量预留释放必须一起落库")
	}
	var retained model.AIGatewayInputAsset
	if err := db.First(&retained, asset.ID).Error; err != nil || retained.NormalizedSHA256 == nil || *retained.NormalizedSHA256 != *asset.NormalizedSHA256 || retained.OriginalSHA256 != asset.OriginalSHA256 || retained.DeletedAt == nil {
		t.Fatal("清理正文仍保留原输入hash及生命周期事实")
	}
}
