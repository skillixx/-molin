package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	assetmodel "molin/server/internal/modules/asset/model"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

// 独立目标后端模拟服务端跨区域复制，目标Head只能看到真正的长期副本。
type videoSeparateSaveStore struct {
	*video.FakeVideoObjectStore
	source VideoContentStore
}

func (s *videoSeparateSaveStore) CopyImmutable(ctx context.Context, source, target video.VideoObjectRef, hash string, size uint64) (video.StoredVideoObject, error) {
	meta, err := s.source.Head(ctx, source)
	if err != nil || meta.SHA256 != hash || meta.SizeBytes != size || size > 1<<20 {
		return video.StoredVideoObject{}, ErrVideoSaveUnavailable
	}
	r, err := s.source.GetRange(ctx, source, 0, int64(size))
	if err != nil {
		return video.StoredVideoObject{}, err
	}
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		return video.StoredVideoObject{}, err
	}
	parts := strings.Split(target.ObjectKey, "/")
	if target.Bucket != "ai-user-assets" || len(parts) != 3 {
		return video.StoredVideoObject{}, ErrVideoSaveUnavailable
	}
	return s.Put(ctx, video.PutVideoObjectRequest{Zone: video.VideoObjectSaved, TaskID: parts[0], AssetID: parts[1], Role: strings.TrimSuffix(parts[2], ".bin"), Body: bytes.NewReader(data), MaxBytes: int64(size)})
}

func TestVideoG6AssetSaveSeparateStoreMySQL(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "独立后端允许删除原结果", true: "源侧影子不能掩盖长期丢失"}[lost], func(t *testing.T) {
			f := NewVideoContentHTTPFixture(t)
			f.EnableAssetSaving()
			id := f.CreateCompletedForKey(f.ProjectID)
			var root model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
				t.Fatal(err)
			}
			original := f.App.saveStore
			destination := &videoSeparateSaveStore{FakeVideoObjectStore: video.NewFakeVideoObjectStore(), source: f.App.contentStore}
			f.App.saveStore = destination
			caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
			if _, err := f.App.SaveVideoAsset(context.Background(), caller, root.PublicID, "g6-save-separate-store"); err != nil {
				t.Fatal(err)
			}
			var op videoAssetSave
			if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
				t.Fatal(err)
			}
			plan, err := decodeVideoSavePlan(&op)
			if err != nil {
				t.Fatal(err)
			}
			if lost {
				for _, p := range plan {
					if _, err := original.CopyImmutable(context.Background(), video.VideoObjectRef{Bucket: p.SourceBucket, ObjectKey: p.SourceKey}, video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}, p.SHA256, p.Size); err != nil {
						t.Fatal(err)
					}
				}
				if err := destination.Delete(context.Background(), video.VideoObjectRef{Bucket: plan[0].TargetBucket, ObjectKey: plan[0].TargetKey}); err != nil {
					t.Fatal(err)
				}
			}
			_, err = f.App.DeleteMedia(context.Background(), caller, id, "g6-delete-after-separate-save")
			if lost {
				if !errors.Is(err, ErrVideoMediaProtected) {
					t.Fatalf("真正长期目标丢失必须保护原媒体：%v", err)
				}
				if !f.InspectMedia(id)["content"].Present {
					t.Fatal("不能被源侧影子欺骗而删除原媒体")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range plan {
					if _, err := destination.Head(context.Background(), video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}); err != nil {
						t.Fatal("原媒体删除不能触碰独立长期后端")
					}
				}
			}
		})
	}
}

func TestVideoG6AssetSavePartialCopyMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	entID := f.EnableAssetSaving()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	before := f.FinancialSnapshot()
	f.FailSaveAfterOne(true)
	if _, err := f.App.SaveVideoAsset(context.Background(), caller, root.PublicID, "g6-save-partial-copy"); !errors.Is(err, ErrVideoSaveUnavailable) {
		t.Fatalf("第二份复制故障必须失败关闭：%v", err)
	}
	var op videoAssetSave
	if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
		t.Fatal(err)
	}
	if op.Status != "copy_failed" || op.SavedUserAssetID != nil {
		t.Fatal("部分复制不能发布长期用户资产")
	}
	var ent assetmodel.UserEntitlement
	if err := f.DB.First(&ent, entID).Error; err != nil {
		t.Fatal(err)
	}
	if !ent.QuotaUsed.IsZero() || !ent.QuotaReserved.Equal(op.QuotaAmount) {
		t.Fatal("未清理部分对象必须保留原容量预占")
	}
	if _, err := f.App.DeleteMedia(context.Background(), caller, id, "g6-delete-during-partial-save"); !errors.Is(err, ErrVideoMediaProtected) {
		t.Fatal("部分转存时不得删除源")
	}
	f.FailSaveAfterOne(false)
	if result, err := f.App.SaveVideoAsset(context.Background(), caller, root.PublicID, "g6-save-partial-copy"); err != nil || result == nil || result.UserAssetID == 0 {
		t.Fatalf("恢复必须沿原计划完成：%v", err)
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("复制故障与恢复不得改变生成财务事实")
	}
}
