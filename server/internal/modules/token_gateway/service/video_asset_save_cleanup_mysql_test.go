package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 部分保存失败后只清理已登记的长期目标；全部目标围栏确认后才精确释放一次容量。
func TestVideoG6AssetSaveCleanupMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	entID := f.EnableAssetSaving()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root, thumb model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=? AND asset_role='thumbnail'", root.TaskID).Update("expires_at", time.Now().UTC().Add(3*time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Where("task_id=? AND asset_role='thumbnail'", root.TaskID).Take(&thumb).Error; err != nil {
		t.Fatal(err)
	}
	f.FailSaveAfterOne(true)
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	if _, err := f.App.SaveVideoAsset(context.Background(), caller, root.PublicID, "g6-cleanup-partial-save"); !errors.Is(err, ErrVideoSaveUnavailable) {
		t.Fatalf("必须先形成部分复制故障：%v", err)
	}
	f.FailSaveAfterOne(false)
	var op videoAssetSave
	if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
		t.Fatal(err)
	}
	owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &f.ProjectID}
	policy := VideoSaveCleanupPolicy{Purpose: "non_commercial_test_fixture", Version: "fixture-cleanup-v1"}
	if _, err := f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy); !errors.Is(err, ErrVideoSaveConflict) {
		t.Fatal("未到期的可恢复保存不能被清理")
	}
	if wait := time.Until(thumb.ExpiresAt.Add(50 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	before := f.FinancialSnapshot()
	result, err := f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy)
	if err != nil || result == nil || !result.Aborted || result.Idempotent {
		t.Fatalf("到期后的未发布副本应清理完成：%v", err)
	}
	var ent assetmodel.UserEntitlement
	if err := f.DB.First(&ent, entID).Error; err != nil {
		t.Fatal(err)
	}
	if !ent.QuotaUsed.IsZero() || !ent.QuotaReserved.IsZero() {
		t.Fatal("只释放未发布的原预占，不计入已用")
	}
	plan, err := decodeVideoSavePlan(&op)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan {
		ref := video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}
		gone, err := f.App.saveStore.VerifyDeleted(context.Background(), ref)
		if err != nil || !gone {
			t.Fatal("包括从未创建的目标在内，五份计划都必须建立删除围栏")
		}
		if _, err := f.App.saveStore.CopyImmutable(context.Background(), video.VideoObjectRef{Bucket: p.SourceBucket, ObjectKey: p.SourceKey}, ref, p.SHA256, p.Size); !errors.Is(err, video.ErrVideoObjectConflict) {
			t.Fatal("迟到复制不能复活清理目标")
		}
	}
	if !f.InspectMedia(id)["content"].Present || !f.InspectMedia(id)["moderation_copy"].Present {
		t.Fatal("不得删除原视频或审核副本")
	}
	result, err = f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy)
	if err != nil || !result.Idempotent {
		t.Fatalf("清理重放应幂等：%v", err)
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("清理不能改变原模型生成财务")
	}
}
