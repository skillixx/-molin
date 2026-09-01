package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

// 权益合法续期后新键可重新保存；旧键、旧计划、围栏和已释放容量永远属于旧尝试。
func TestVideoG6AssetSaveReattemptMySQL(t *testing.T) {
	f, old, owner, policy := expiredVideoSaveFixture(t)
	ctx := context.Background()
	if _, err := f.App.CleanupVideoAssetSave(ctx, old.PublicID, owner, policy); err != nil {
		t.Fatal(err)
	}
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=? AND asset_role='content'", old.TaskID).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	caller := VideoCaller{UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: *owner.APIKeyID}
	oldKey := "g6-cleanup-expired-entitlement"
	before := f.FinancialSnapshot()
	oldFacts := func() []byte {
		t.Helper()
		var row map[string]any
		if err := f.DB.Table("ai_video_asset_saves").Where("public_id=?", old.PublicID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal("无法编码旧尝试事实")
		}
		return data
	}
	frozenOld := oldFacts()
	checkOldKey := func() {
		t.Helper()
		copies := f.App.saveStore.(*videoContentHTTPStore).saveAttempts.Load()
		var beforeEnt assetmodel.UserEntitlement
		if err := f.DB.First(&beforeEnt, old.StorageEntitlementID).Error; err != nil {
			t.Fatal(err)
		}
		reply, err := f.App.SaveVideoAsset(ctx, caller, root.PublicID, oldKey)
		if err != nil || reply == nil || reply.Status != "aborted" || reply.UserAssetID != 0 || !reply.Idempotent {
			t.Fatalf("旧键必须返回原aborted而非新保存：%v", err)
		}
		if !bytes.Equal(frozenOld, oldFacts()) {
			t.Fatal("旧键重放不得改写旧尝试任何字段")
		}
		var afterEnt assetmodel.UserEntitlement
		if err := f.DB.First(&afterEnt, old.StorageEntitlementID).Error; err != nil {
			t.Fatal(err)
		}
		if copies != f.App.saveStore.(*videoContentHTTPStore).saveAttempts.Load() || !beforeEnt.QuotaUsed.Equal(afterEnt.QuotaUsed) || !beforeEnt.QuotaReserved.Equal(afterEnt.QuotaReserved) {
			t.Fatal("旧键不得复制任何对象或改变容量预占和结转")
		}
	}
	checkOldKey()
	if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", old.StorageEntitlementID).Update("expires_at", time.Now().UTC().Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	created, err := f.App.SaveVideoAsset(ctx, caller, root.PublicID, "g6-save-renewed-new-attempt")
	if err != nil || created == nil || created.UserAssetID == 0 || created.Status != "completed" || created.Idempotent {
		t.Fatalf("有效源及续期权益的新键必须产生独立保存：%v", err)
	}
	var all []videoAssetSave
	if err := f.DB.Where("task_id=?", old.TaskID).Order("created_at").Find(&all).Error; err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].PublicID != old.PublicID || all[0].Status != "aborted" || all[1].Status != "completed" || all[1].PublicID == old.PublicID {
		t.Fatal("必须保留原aborted行并新增唯一completed尝试")
	}
	oldPlan, err := decodeVideoSavePlan(&all[0])
	if err != nil {
		t.Fatal(err)
	}
	newPlan, err := decodeVideoSavePlan(&all[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, oldItem := range oldPlan {
		ref := video.VideoObjectRef{Bucket: oldItem.TargetBucket, ObjectKey: oldItem.TargetKey}
		if gone, err := f.App.saveStore.VerifyDeleted(ctx, ref); err != nil || !gone {
			t.Fatal("新保存不得复活旧目标")
		}
		for _, newItem := range newPlan {
			if newItem.TargetBucket == oldItem.TargetBucket && newItem.TargetKey == oldItem.TargetKey {
				t.Fatal("新旧尝试不能复用任何目标位置")
			}
		}
	}
	checkOldKey()
	if reply, err := f.App.CleanupVideoAssetSave(ctx, old.PublicID, owner, policy); err != nil || reply == nil || !reply.Idempotent {
		t.Fatalf("旧清理只能重放原尝试：%v", err)
	}
	again, err := f.App.SaveVideoAsset(ctx, caller, root.PublicID, "g6-save-renewed-another-key")
	if err != nil || again == nil || !again.Idempotent || again.UserAssetID != created.UserAssetID {
		t.Fatal("已有completed时另一个新键不能产生第二个长期资产")
	}
	var ent assetmodel.UserEntitlement
	if err := f.DB.First(&ent, old.StorageEntitlementID).Error; err != nil {
		t.Fatal(err)
	}
	if !ent.QuotaReserved.IsZero() || !ent.QuotaUsed.Equal(all[1].QuotaAmount) || !bytes.Equal(frozenOld, oldFacts()) || !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("新旧重放只能保留一次当前保存容量，旧尝试与生成财务不能改写")
	}
}

// 同一应用上的100个并发新键只能共享一个后继尝试，旧终止记录和旧命令仍然保留。
func TestVideoG6AssetSaveReattemptConcurrentMySQL(t *testing.T) {
	f, old, owner, policy := expiredVideoSaveFixture(t)
	ctx := context.Background()
	if _, err := f.App.CleanupVideoAssetSave(ctx, old.PublicID, owner, policy); err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", old.StorageEntitlementID).Update("expires_at", time.Now().UTC().Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=? AND asset_role='content'", old.TaskID).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	before := f.FinancialSnapshot()
	caller := VideoCaller{UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: *owner.APIKeyID}
	results := make([]*VideoAssetSaveReply, 100)
	errs := make([]error, 100)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index], errs[index] = f.App.SaveVideoAsset(ctx, caller, root.PublicID, fmt.Sprintf("g6-save-new-attempt-race-%03d", index))
		}(i)
	}
	close(start)
	wg.Wait()
	created := 0
	var assetID uint64
	for i, result := range results {
		if errs[i] != nil || result == nil || result.Status != "completed" || result.UserAssetID == 0 {
			t.Fatalf("新尝试100并发必须全部汇合：index=%d err=%v", i, errs[i])
		}
		if assetID != 0 && result.UserAssetID != assetID {
			t.Fatal("并发不能产生多个长期资产")
		}
		assetID = result.UserAssetID
		if !result.Idempotent {
			created++
		}
	}
	var attempts []videoAssetSave
	if err := f.DB.Where("task_id=?", old.TaskID).Order("attempt_no").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if created != 1 || len(attempts) != 2 || attempts[0].PublicID != old.PublicID || attempts[0].Status != "aborted" || attempts[1].Status != "completed" || attempts[1].AttemptNo != 2 || attempts[1].PreviousSaveID == nil || *attempts[1].PreviousSaveID != old.PublicID {
		t.Fatal("必须恰好一个旧终止和一个新完成尝试，前驱不变")
	}
	var commands int64
	if err := f.DB.Model(&videoAssetSaveCommand{}).Where("task_id=? AND save_public_id=?", old.TaskID, attempts[1].PublicID).Count(&commands).Error; err != nil || commands != 100 {
		t.Fatal("100个新命令必须精确绑定唯一新尝试")
	}
	var ent assetmodel.UserEntitlement
	if err := f.DB.First(&ent, old.StorageEntitlementID).Error; err != nil {
		t.Fatal(err)
	}
	if !ent.QuotaReserved.IsZero() || !ent.QuotaUsed.Equal(attempts[1].QuotaAmount) || !bytes.Equal(before, f.FinancialSnapshot()) || f.App.saveStore.(*videoContentHTTPStore).saveAttempts.Load() != 5 {
		t.Fatal("新尝试只复制五目标、结转一次容量，生成财务不变")
	}
}
