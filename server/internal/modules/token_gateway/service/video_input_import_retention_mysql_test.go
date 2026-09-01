package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
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
}
