package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
)

// 对齐发布前最后一次权益查询到半秒之后，复现DATETIME(0)向下一秒舍入导致的即时读取拒绝。
func TestVideoG6SavedReadPublicationClockMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	f.EnableAssetSaving()
	f.EnableAssetDownloads()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	var aligned time.Time
	const callback = "g6_saved_publication_clock"
	if err := f.DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*assetmodel.UserEntitlement); !ok || tx.Error != nil {
			return
		}
		if reads.Add(1) != 3 {
			return
		}
		// 只控制真实查询返回时序，不修改已发布数据、校验逻辑或数据库精度。
		now := time.Now()
		aligned = now.Truncate(time.Second).Add(650 * time.Millisecond)
		if !aligned.After(now) {
			aligned = aligned.Add(time.Second)
		}
		time.Sleep(time.Until(aligned))
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.DB.Callback().Query().Remove(callback) })
	ctx := context.Background()
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	saved, err := f.App.SaveVideoAsset(ctx, caller, root.PublicID, "g6-long-publication-clock")
	if err != nil {
		t.Fatal(err)
	}
	returned := time.Now()
	if aligned.IsZero() || returned.Sub(aligned) < 0 || returned.Sub(aligned) > 300*time.Millisecond {
		t.Fatal("未在限定的舍入窗口内完成发布，不能用错过时序当作通过")
	}
	var asset assetmodel.UserAsset
	if err := f.DB.First(&asset, saved.UserAssetID).Error; err != nil || asset.StartedAt == nil {
		t.Fatal("必须检查真实发布资产的开始时间")
	}
	if asset.StartedAt.After(returned) {
		t.Fatalf("刚发布资产开始时间被舍入到未来：ahead_ms=%d", asset.StartedAt.Sub(returned).Milliseconds())
	}
	if _, err := f.App.SavedVideoDownloadURL(ctx, caller, saved.UserAssetID, "content"); err != nil {
		t.Fatalf("发布完成必须可立即按当前资格签发：%v", err)
	}
	// 真正未来生效的资产仍须拒绝，不能通过删掉时间校验来修复精度问题。
	if err := f.DB.Model(&assetmodel.UserAsset{}).Where("id=?", saved.UserAssetID).Update("started_at", time.Now().Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.App.SavedVideoDownloadURL(ctx, caller, saved.UserAssetID, "content"); err == nil {
		t.Fatal("未来开始的长期资产不能提前下载")
	}
}
