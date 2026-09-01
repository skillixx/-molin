package service

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
)

// 不同幂等键的100个请求也共享同一原视频保存执行权，不能重复占用长期容量。
func TestVideoG6AssetSaveConcurrentMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	entID := f.EnableAssetSaving()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	before := f.FinancialSnapshot()
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	type outcome struct {
		value *VideoAssetSaveReply
		err   error
	}
	out := make(chan outcome, 100)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, err := f.App.SaveVideoAsset(context.Background(), caller, root.PublicID, fmt.Sprintf("g6-concurrent-save-%03d", i))
			out <- outcome{v, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(out)
	var savedID uint64
	var fresh int
	for r := range out {
		if r.err != nil {
			t.Errorf("并发保存失败：%v", r.err)
			continue
		}
		if r.value == nil {
			t.Error("并发回复不能为空")
			continue
		}
		if savedID == 0 {
			savedID = r.value.UserAssetID
		}
		if r.value.UserAssetID != savedID {
			t.Error("同一原视频形成多个长期资产")
		}
		if !r.value.Idempotent {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("只能有一个首次资产创建，实际%d", fresh)
	}
	var count int64
	if err := f.DB.Table("ai_video_asset_save_commands").Where("task_id=?", root.TaskID).Count(&count).Error; err != nil || count != 100 {
		t.Fatal("每个成功命令必须有唯一持久化回执")
	}
	if err := f.DB.Table("user_assets").Where("user_id=? AND asset_type='video_file'", f.ProjectID).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("只能持久化一个长期用户资产")
	}
	var op videoAssetSave
	if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
		t.Fatal(err)
	}
	var ent assetmodel.UserEntitlement
	if err := f.DB.First(&ent, entID).Error; err != nil {
		t.Fatal(err)
	}
	if !ent.QuotaReserved.IsZero() || !ent.QuotaUsed.Equal(op.QuotaAmount) {
		t.Fatal("并发保存不能重复结转容量")
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("保存并发不得改变原生成财务事实")
	}
}
