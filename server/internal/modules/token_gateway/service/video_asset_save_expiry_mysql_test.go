package service

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
)

// 最后一个写点跨越真实到期时间时，不得把较早的授权和对账结果当作提交许可。
func TestVideoG6AssetSaveCommitExpiryMySQL(t *testing.T) {
	for _, kind := range []string{"source", "entitlement", "jwt"} {
		t.Run(kind, func(t *testing.T) {
			f := NewVideoContentHTTPFixture(t)
			entID := f.EnableAssetSaving()
			keyID := f.ProjectID
			if kind == "jwt" {
				keyID = 0
			}
			id := f.CreateCompletedForKey(keyID)
			var root model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().UTC().Add(5 * time.Second)
			caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: keyID}
			switch kind {
			case "source":
				if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=? AND asset_role='thumbnail'", root.TaskID).Update("expires_at", deadline).Error; err != nil {
					t.Fatal(err)
				}
				var thumb model.AIImageAsset
				if err := f.DB.Where("task_id=? AND asset_role='thumbnail'", root.TaskID).Take(&thumb).Error; err != nil {
					t.Fatal(err)
				}
				deadline = thumb.ExpiresAt
			case "entitlement":
				if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("expires_at", deadline).Error; err != nil {
					t.Fatal(err)
				}
				var ent assetmodel.UserEntitlement
				if err := f.DB.First(&ent, entID).Error; err != nil {
					t.Fatal(err)
				}
				deadline = *ent.ExpiresAt
			case "jwt":
				var token string
				token, deadline = f.ShortJWT(5)
				var err error
				caller, err = f.JWT.Authenticate(context.Background(), token)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := f.FinancialSnapshot()
			var entered, validAtEntry atomic.Bool
			name := "g6-save-final-expiry"
			if err := f.DB.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
				if tx.Statement.Table == "asset_events" && entered.CompareAndSwap(false, true) {
					validAtEntry.Store(time.Now().Before(deadline))
					if wait := time.Until(deadline.Add(50 * time.Millisecond)); wait > 0 {
						time.Sleep(wait)
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer f.DB.Callback().Create().Remove(name)
			result, err := f.App.SaveVideoAsset(context.Background(), caller, root.PublicID, "g6-save-cross-final-expiry")
			if !entered.Load() || !validAtEntry.Load() {
				t.Fatal("必须真实到达有效期内的最终写入点再跨期")
			}
			if err == nil || result != nil {
				t.Fatal("最后写入跨过有效期必须拒绝发布长期资产")
			}
			var op videoAssetSave
			if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
				t.Fatal(err)
			}
			if op.Status != "copying" || op.SavedUserAssetID != nil {
				t.Fatal("只保留首阶段计划，完成事务应全部回滚")
			}
			var n int64
			if err := f.DB.Table("user_assets").Where("user_id=? AND asset_type='video_file'", f.ProjectID).Count(&n).Error; err != nil || n != 0 {
				t.Fatal("不得遗留已发布用户资产")
			}
			if err := f.DB.Table("asset_events").Where("user_id=? AND remark='视频独立副本保存完成'", f.ProjectID).Count(&n).Error; err != nil || n != 0 {
				t.Fatal("不得遗留完成事件")
			}
			var ent assetmodel.UserEntitlement
			if err := f.DB.First(&ent, entID).Error; err != nil {
				t.Fatal(err)
			}
			if !ent.QuotaUsed.IsZero() || !ent.QuotaReserved.Equal(op.QuotaAmount) {
				t.Fatal("失败复制仍有计划归属并占预留，不能结转或偷放容量")
			}
			if !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("跨期回滚不能改变生成财务事实")
			}
		})
	}
}
