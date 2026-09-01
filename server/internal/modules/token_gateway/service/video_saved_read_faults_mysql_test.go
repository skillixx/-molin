package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
)

// 数据库暂时故障只能表示依赖不可用，不能冒称当前用户已失去存储权益。
func TestVideoG6SavedReadDatabaseFaultMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	f.EnableAssetSaving()
	f.EnableAssetDownloads()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	saved, err := f.App.SaveVideoAsset(ctx, caller, root.PublicID, "g6-long-db-fault-save")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"saved_asset", "entitlement_link", "entitlement"} {
		t.Run(target, func(t *testing.T) {
			var injected atomic.Bool
			name := "g6_saved_read_fault_" + target
			if err := f.DB.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
				matches := false
				switch target {
				case "saved_asset":
					_, matches = tx.Statement.Dest.(*assetmodel.UserAsset)
				case "entitlement":
					_, matches = tx.Statement.Dest.(*assetmodel.UserEntitlement)
				case "entitlement_link":
					matches = tx.Statement.Table == "user_entitlements" && len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "asset_id"
				}
				if matches && injected.CompareAndSwap(false, true) {
					tx.AddError(&mysqlDriver.MySQLError{Number: 2006, Message: "合成数据库连接故障"})
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer f.DB.Callback().Query().Remove(name)
			heads, ranges := f.HeadCalls(), f.RangeCalls()
			_, err := f.App.SavedVideoDownloadURL(ctx, caller, saved.UserAssetID, "content")
			if !injected.Load() || !errors.Is(err, ErrVideoAccessUnavailable) {
				t.Fatalf("读取依赖故障应明确不可用且必须触发注入：injected=%t err=%v", injected.Load(), err)
			}
			if heads != f.HeadCalls() || ranges != f.RangeCalls() {
				t.Fatal("数据库证明不可用时不能访问媒体存储")
			}
		})
	}
}
