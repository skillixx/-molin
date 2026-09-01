package service

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func expiredVideoSaveFixture(t *testing.T) (VideoContentHTTPFixture, videoAssetSave, repository.VideoOwner, VideoSaveCleanupPolicy) {
	t.Helper()
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
	var ent assetmodel.UserEntitlement
	if err := f.DB.First(&ent, entID).Error; err != nil {
		t.Fatal(err)
	}
	f.FailSaveAfterOne(true)
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	if _, err := f.App.SaveVideoAsset(context.Background(), caller, root.PublicID, "g6-cleanup-expired-entitlement"); !errors.Is(err, ErrVideoSaveUnavailable) {
		t.Fatalf("必须先形成可追溯的部分复制失败：%v", err)
	}
	f.FailSaveAfterOne(false)
	var op videoAssetSave
	if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
		t.Fatal(err)
	}
	if wait := time.Until(ent.ExpiresAt.Add(50 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	return f, op, repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &f.ProjectID}, VideoSaveCleanupPolicy{Purpose: "non_commercial_test_fixture", Version: "fixture-cleanup-v1"}
}

func TestVideoG6AssetSaveCleanupRecoveryMySQL(t *testing.T) {
	for _, failure := range []string{"delete", "confirmation", "database"} {
		t.Run(failure, func(t *testing.T) {
			f, op, owner, policy := expiredVideoSaveFixture(t)
			before := f.FinancialSnapshot()
			var injected atomic.Bool
			switch failure {
			case "delete":
				f.FailMediaDelete(true)
			case "confirmation":
				f.FailMediaConfirmation(true)
			case "database":
				name := "g6-cleanup-aborted-failure"
				if err := f.DB.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == "ai_video_asset_saves" {
						if values, ok := tx.Statement.Dest.(map[string]any); ok && values["status"] == "aborted" && injected.CompareAndSwap(false, true) {
							tx.AddError(errors.New("合成清理完成数据库写失败"))
						}
					}
				}); err != nil {
					t.Fatal(err)
				}
				defer f.DB.Callback().Update().Remove(name)
			}
			if result, err := f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy); err == nil || result != nil {
				t.Fatal("故障不能伪装成清理成功")
			}
			var pending videoAssetSave
			if err := f.DB.Where("task_id=?", op.TaskID).Take(&pending).Error; err != nil {
				t.Fatal(err)
			}
			if pending.Status != "cleanup_pending" || pending.CleanupFinishedAt != nil {
				t.Fatal("失败必须保留原清理意图")
			}
			var ent assetmodel.UserEntitlement
			if err := f.DB.First(&ent, op.StorageEntitlementID).Error; err != nil {
				t.Fatal(err)
			}
			if !ent.QuotaReserved.Equal(op.QuotaAmount) || !ent.QuotaUsed.IsZero() {
				t.Fatal("未完成时不能释放或结转预占")
			}
			if failure == "database" && !injected.Load() {
				t.Fatal("必须实际触发最后数据库写失败")
			}
			f.FailMediaDelete(false)
			f.FailMediaConfirmation(false)
			result, err := f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy)
			if err != nil || result == nil || !result.Aborted {
				t.Fatalf("应从原目标事实恢复：%v", err)
			}
			result, err = f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy)
			if err != nil || !result.Idempotent {
				t.Fatal("完成重放不能再次释放容量")
			}
			if err := f.DB.First(&ent, op.StorageEntitlementID).Error; err != nil {
				t.Fatal(err)
			}
			if !ent.QuotaReserved.IsZero() || !ent.QuotaUsed.IsZero() {
				t.Fatal("过期权益也须精确释放原预占一次")
			}
			if !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("恢复不能改变生成账单、钱包或Outbox")
			}
		})
	}
}

func TestVideoG6AssetSaveCleanupProtectionMySQL(t *testing.T) {
	for _, protection := range []string{"legal_hold", "matching_asset", "wrong_owner", "completed"} {
		t.Run(protection, func(t *testing.T) {
			f, op, owner, policy := expiredVideoSaveFixture(t)
			switch protection {
			case "completed":
				if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", op.StorageEntitlementID).Update("expires_at", time.Now().UTC().Add(time.Hour)).Error; err != nil {
					t.Fatal(err)
				}
				plan, err := decodeVideoSavePlan(&op)
				if err != nil {
					t.Fatal(err)
				}
				rootID := ""
				for _, p := range plan {
					if p.Role == "content" {
						rootID = p.PublicID
					}
				}
				if _, err := f.App.SaveVideoAsset(context.Background(), VideoCaller{UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: *owner.APIKeyID}, rootID, "g6-cleanup-protect-completed"); err != nil {
					t.Fatal(err)
				}
			case "legal_hold":
				if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=? AND asset_role='content'", op.TaskID).Update("legal_hold", true).Error; err != nil {
					t.Fatal(err)
				}
			case "matching_asset":
				a := assetmodel.UserAsset{UserID: owner.UserID, ProductID: op.StorageProductID, AssetType: "video_file", BusinessInstanceID: &op.PublicID, Status: "active"}
				if err := f.DB.Create(&a).Error; err != nil {
					t.Fatal(err)
				}
			case "wrong_owner":
				owner.ProjectID++
			}
			before := f.MediaDeleteCalls()
			if result, err := f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy); err == nil || result != nil {
				t.Fatal("保护或归属不符必须拒绝清理")
			}
			if f.MediaDeleteCalls() != before {
				t.Fatal("拒绝发生在任何目标删除之前")
			}
		})
	}
}
