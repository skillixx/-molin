package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

// 外部故障边界：删除交付对象时错误清除审核副本，服务必须拒绝提交completed。
type mediaCollateralStore struct {
	VideoMediaDeleteStore
	retained video.VideoObjectRef
	once     sync.Once
}

func (s *mediaCollateralStore) Delete(ctx context.Context, ref video.VideoObjectRef) error {
	if err := s.VideoMediaDeleteStore.Delete(ctx, ref); err != nil {
		return err
	}
	var err error
	s.once.Do(func() { err = s.VideoMediaDeleteStore.Delete(ctx, s.retained) })
	return err
}

func mediaDeleteFinanceSnapshot(t *testing.T, db *gorm.DB, userID uint64) []byte {
	t.Helper()
	rowsByTable := map[string][]string{}
	for _, table := range []string{"wallets", "wallet_holds", "wallet_transactions", "ai_requests", "ai_gateway_quotes", "ai_usage_items", "ai_request_wallet_links", "ai_outbox_events"} {
		predicate := "user_id=?"
		if table == "ai_usage_items" || table == "ai_request_wallet_links" {
			predicate = "request_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
		}
		if table == "ai_outbox_events" {
			predicate = "aggregate_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
		}
		var rows []map[string]any
		if err := db.Table(table).Where(predicate, userID).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			rowsByTable[table] = append(rowsByTable[table], string(raw))
		}
		sort.Strings(rowsByTable[table])
	}
	raw, err := json.Marshal(rowsByTable)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVideoG6MediaDeleteRetainedCopyMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t, true)
	id := f.CreateCompletedForKey(f.ProjectID)
	var retained model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='moderation_copy'", id).Take(&retained).Error; err != nil {
		t.Fatal(err)
	}
	if !f.InspectMedia(id)["moderation_copy"].HashMatches {
		t.Fatal("故障前必须存在原审核副本")
	}
	before := mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID)
	f.App.mediaDeleteStore = &mediaCollateralStore{VideoMediaDeleteStore: f.App.mediaDeleteStore, retained: video.VideoObjectRef{Bucket: *retained.Bucket, ObjectKey: *retained.ObjectKey}}
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	if got, err := f.App.DeleteMedia(context.Background(), caller, id, "g6-media-retained-failure"); err == nil || got != nil {
		t.Fatal("保留副本丢失时不能返回删除完成")
	}
	var op videoMediaDeletion
	if err := f.DB.Where("task_id=?", retained.TaskID).Take(&op).Error; err != nil || op.Status != "delete_failed" || op.CompletedAt != nil {
		t.Fatal("必须保留失败状态，不得伪造完成")
	}
	if f.InspectMedia(id)["moderation_copy"].Present {
		t.Fatal("必须实际命中副本附带丢失故障")
	}
	if !bytes.Equal(before, mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID)) {
		t.Fatal("副本故障不得改写财务")
	}
}

func TestVideoG6MediaDeleteConfirmationRollbackMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t, true)
	id := f.CreateCompletedForKey(f.ProjectID)
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	before := mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID)
	var injected atomic.Bool
	name := "g6_media_confirm_rollback"
	if err := f.DB.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
		values, ok := tx.Statement.Dest.(map[string]interface{})
		if tx.Error == nil && ok && tx.Statement.Table == "ai_video_media_deletions" && values["status"] == "completed" && injected.CompareAndSwap(false, true) {
			tx.AddError(errors.New("合成确认提交失败"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer f.DB.Callback().Update().Remove(name)
	if got, err := f.App.DeleteMedia(context.Background(), caller, id, "g6-media-confirm-rollback"); err == nil || got != nil || !injected.Load() {
		t.Fatal("确认写失败不能返回成功")
	}
	if f.MediaDeleteCalls() != 5 {
		t.Fatal("故障须发生于实际五对象删除之后")
	}
	var op videoMediaDeletion
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", id).Take(&op).Error; err != nil || op.Status != "deleting" || op.CompletedAt != nil {
		t.Fatal("确认回滚后保留第一阶段隐藏意图")
	}
	if got, err := f.App.GetVideo(context.Background(), caller, id); err == nil || got != nil {
		t.Fatal("确认失败不能重新公开旧Job")
	}
	result, err := f.App.DeleteMedia(context.Background(), caller, id, "g6-media-confirm-rollback")
	if err != nil || result == nil || !result.Deleted || f.MediaDeleteCalls() != 5 {
		t.Fatalf("原命令恢复只能确认原墓碑，不能重删：%v", err)
	}
	if !f.InspectMedia(id)["moderation_copy"].HashMatches {
		t.Fatal("原副本必须仍存在")
	}
	if !bytes.Equal(before, mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID)) {
		t.Fatal("确认恢复不得新建或改写财务")
	}
}

type mediaBlockingHead struct {
	VideoMediaDeleteStore
	bounded atomic.Bool
}

func (s *mediaBlockingHead) Head(ctx context.Context, ref video.VideoObjectRef) (video.StoredVideoObject, error) {
	deadline, ok := ctx.Deadline()
	s.bounded.Store(ok && time.Until(deadline) <= 30*time.Second)
	<-ctx.Done()
	return video.StoredVideoObject{}, ctx.Err()
}

func TestVideoG6MediaDeletePrepareDeadlineMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t, true)
	id := f.CreateCompletedForKey(f.ProjectID)
	store := &mediaBlockingHead{VideoMediaDeleteStore: f.App.mediaDeleteStore}
	f.App.mediaDeleteStore = store
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	before := mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID)
	started := time.Now()
	// 45秒仅为测试保险丝，仍要求服务自己的30秒deadline先触发，避免回归时挂满测试总期限。
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	got, err := f.App.DeleteMedia(ctx, caller, id, "g6-media-prepare-timeout")
	if err == nil || got != nil || !store.bounded.Load() || time.Since(started) > 35*time.Second {
		t.Fatal("准备Head必须受内部期限约束")
	}
	for _, table := range []string{"ai_video_media_deletions", "ai_video_media_delete_commands"} {
		var n int64
		if err := f.DB.Table(table).Where("user_id=?", f.ProjectID).Count(&n).Error; err != nil || n != 0 {
			t.Fatal("准备到期必须回滚全部意图")
		}
	}
	if f.MediaDeleteCalls() != 0 {
		t.Fatal("准备失败不能删除对象")
	}
	for _, fact := range f.InspectMedia(id) {
		if !fact.Present || !fact.HashMatches || fact.Deleted {
			t.Fatal("准备超时必须保留原对象")
		}
	}
	if !bytes.Equal(before, mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID)) {
		t.Fatal("准备超时不得改变财务")
	}
}
