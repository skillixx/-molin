package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 处理耗时不能侵占ready后的完整留存期，且不能借发布或重放延长原处理命令期限。
func TestVideoG6ImportReadyRetentionMySQL(t *testing.T) {
	f := NewVideoImportHTTPFixture(t)
	importer := f.App.imports
	store := importer.options.Store.(*videoImportMemoryStore)
	start := time.Now().UTC().Truncate(time.Second)
	clock := start
	importer.now = func() time.Time { return clock }
	var once sync.Once
	store.afterRead = func() { once.Do(func() { clock = clock.Add(90 * time.Second) }) }
	c := VideoInputImportCommand{Caller: VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}, SourceAssetID: f.SourceID, IdempotencyKey: "g6-import-ready-retention"}
	result, err := importer.Import(context.Background(), c)
	if err != nil || result.InputAssetID == nil {
		t.Fatalf("导入必须实际发布：%v", err)
	}
	var input model.AIGatewayInputAsset
	if err := f.DB.Where("public_id=?", *result.InputAssetID).Take(&input).Error; err != nil {
		t.Fatal(err)
	}
	if !input.ExpiresAt.Equal(clock.Add(7 * 24 * time.Hour)) {
		t.Errorf("输入期限必须从ready发布起算7天：got=%s want=%s", input.ExpiresAt, clock.Add(7*24*time.Hour))
	}
	if !result.ProcessingExpiresAt.Equal(start.Add(24 * time.Hour)) {
		t.Fatal("处理命令不能因发布而续期")
	}
	clock = clock.Add(time.Minute)
	replayed, err := importer.Import(context.Background(), c)
	if err != nil || !replayed.Idempotent || !replayed.ProcessingExpiresAt.Equal(result.ProcessingExpiresAt) {
		t.Fatalf("完成重放保持处理期限：%v", err)
	}
	var after model.AIGatewayInputAsset
	if err := f.DB.First(&after, input.ID).Error; err != nil || !after.ExpiresAt.Equal(input.ExpiresAt) {
		t.Fatal("完成重放不能延长已发布输入期限")
	}
	// G7后台留存必须同样覆盖导入来源，并且只删除独立规范化副本，不删除来源图片。
	expired := clock.Add(-time.Minute)
	if err := f.DB.Model(&model.AIGatewayInputAsset{}).Where("id=?", input.ID).Update("expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	worker, err := NewVideoInputRetentionWorker(f.App, "import-retention-worker")
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return clock }
	if count, err := worker.RunOnce(context.Background(), 10); err != nil || count != 1 {
		t.Fatalf("导入输入到期后必须自动清理: count=%d err=%v", count, err)
	}
	var deleted model.AIGatewayInputAsset
	if err := f.DB.First(&deleted, input.ID).Error; err != nil || deleted.LifecycleState != model.AIInputAssetDeleted || deleted.DeletedAt == nil || f.InputPresent(input.PublicID) || !f.SourcePresent() {
		t.Fatal("后台清理只能删除独立导入副本并保留来源")
	}
	var request repository.VideoInputDeletionRequest
	if err := f.DB.Where("input_asset_id=?", input.ID).Take(&request).Error; err != nil || request.RequestKind != "retention" {
		t.Fatal("导入来源后台清理必须保留retention请求")
	}
}

func TestVideoG7ImportRetentionCursorSkipsProtectedPrefixMySQL(t *testing.T) {
	f := NewVideoImportHTTPFixture(t)
	ctx := context.Background()
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	first, err := f.App.imports.Import(ctx, VideoInputImportCommand{Caller: caller, SourceAssetID: f.SourceID, IdempotencyKey: "g7-retention-protected-first"})
	if err != nil || first.InputAssetID == nil {
		t.Fatalf("准备受保护输入失败: %v", err)
	}
	second, err := f.App.imports.Import(ctx, VideoInputImportCommand{Caller: caller, SourceAssetID: f.SourceID, IdempotencyKey: "g7-retention-tail-second"})
	if err != nil || second.InputAssetID == nil {
		t.Fatalf("准备尾部输入失败: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second).Add(time.Minute)
	if err := f.DB.Model(&model.AIGatewayInputAsset{}).Where("public_id IN ?", []string{*first.InputAssetID, *second.InputAssetID}).Update("expires_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	assets := []model.AIGatewayInputAsset{}
	if err := f.DB.Where("public_id IN ?", []string{*first.InputAssetID, *second.InputAssetID}).Order("id").Find(&assets).Error; err != nil || len(assets) != 2 {
		t.Fatalf("读取输入失败: count=%d err=%v", len(assets), err)
	}
	repo := repository.NewVideoInputAssetRepository(f.DB)
	for _, asset := range assets {
		if _, _, err := repo.RequestRetentionDelete(ctx, asset.PublicID, repository.VideoOwner{UserID: asset.UserID, ProjectID: asset.ProjectID, APIKeyID: &asset.UserID}, asset.VersionNo, now); err != nil {
			t.Fatalf("准备retention凭据失败: id=%s err=%v", asset.PublicID, err)
		}
	}
	// 删除凭据形成后审核状态漂移，首页清理必须失败关闭；该冲突不得让尾页永久饥饿。
	if err := f.DB.Model(&model.AIGatewayInputAsset{}).Where("id=?", assets[0].ID).Update("moderation_status", model.AIModerationRejected).Error; err != nil {
		t.Fatalf("准备首页保护冲突失败: %v", err)
	}
	worker, err := NewVideoInputRetentionWorker(f.App, "retention-fair-worker")
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	if count, err := worker.RunOnce(ctx, 1); err != nil || count != 0 {
		t.Fatalf("首页审核漂移必须被保护且不阻断游标推进: count=%d err=%v", count, err)
	}
	// 新Worker实例必须从MySQL游标继续尾页，不能因进程重启重复卡在第一条。
	restarted, err := NewVideoInputRetentionWorker(f.App, "retention-fair-restart")
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = worker.now
	if count, err := restarted.RunOnce(ctx, 1); err != nil || count != 1 {
		t.Fatalf("重启后尾部可清理输入必须完成: count=%d err=%v", count, err)
	}
	var protected, cleaned model.AIGatewayInputAsset
	if err := f.DB.Where("public_id=?", *first.InputAssetID).Take(&protected).Error; err != nil || protected.LifecycleState != model.AIInputAssetPendingDelete {
		t.Fatalf("冲突输入必须继续受保护: state=%s err=%v", protected.LifecycleState, err)
	}
	if err := f.DB.Where("public_id=?", *second.InputAssetID).Take(&cleaned).Error; err != nil || cleaned.LifecycleState != model.AIInputAssetDeleted {
		t.Fatalf("尾部输入必须完成清理: state=%s err=%v", cleaned.LifecycleState, err)
	}
}
