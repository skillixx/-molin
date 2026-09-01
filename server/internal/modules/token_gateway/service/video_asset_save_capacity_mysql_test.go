package service

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

// 三层字节上限与权益余额分别拒绝，不允许先复制再发现超额，也不能遗留无主预占。
func TestVideoG6AssetSaveCapacityMySQL(t *testing.T) {
	for _, scope := range []string{"user", "project", "global", "entitlement"} {
		t.Run(scope, func(t *testing.T) {
			f := NewVideoContentHTTPFixture(t)
			entID := f.EnableAssetSaving()
			id := f.CreateCompletedForKey(f.ProjectID)
			var root model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
				t.Fatal(err)
			}
			switch scope {
			case "user":
				f.App.savePolicy.MaxUserBytes = 1
			case "project":
				f.App.savePolicy.MaxProjectBytes = 1
			case "global":
				f.App.savePolicy.MaxGlobalBytes = 1
				f.App.savePolicy.GlobalAlertBytes = 1
			case "entitlement":
				if err := f.DB.Exec("UPDATE user_entitlements SET quota_total=0 WHERE id=?", entID).Error; err != nil {
					t.Fatal(err)
				}
			}
			before := f.FinancialSnapshot()
			caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
			if _, err := f.App.SaveVideoAsset(context.Background(), caller, root.PublicID, "g6-save-capacity-denied"); !errors.Is(err, ErrVideoSaveCapacity) {
				t.Fatalf("对应容量门禁必须拒绝：%v", err)
			}
			for _, table := range []string{"ai_video_asset_saves", "ai_video_asset_save_commands"} {
				var n int64
				if err := f.DB.Table(table).Where("task_id=?", root.TaskID).Count(&n).Error; err != nil || n != 0 {
					t.Fatal("拒绝不能遗留保存计划或命令")
				}
			}
			if f.App.saveStore.(*videoContentHTTPStore).saveAttempts.Load() != 0 {
				t.Fatal("容量不足不能触发复制")
			}
			if !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("容量失败不得触碰模型财务")
			}
		})
	}
}
