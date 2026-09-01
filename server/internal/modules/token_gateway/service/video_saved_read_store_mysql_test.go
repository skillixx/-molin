package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

// 长期后端与临时后端分离；临时后端的同名影子不得替代已丢失的长期副本。
func TestVideoG6SavedReadSeparateStoreMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	f.EnableAssetSaving()
	f.EnableAssetDownloads()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	original := f.App.saveStore
	destination := &videoSeparateSaveStore{FakeVideoObjectStore: video.NewFakeVideoObjectStore(), source: f.App.contentStore}
	f.App.saveStore = destination
	ctx := context.Background()
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	saved, err := f.App.SaveVideoAsset(ctx, caller, root.PublicID, "g6-long-separate-save")
	if err != nil {
		t.Fatal(err)
	}
	// 新保存策略改变不改写旧保存的原资格；读取仍按原商品、权益、类型与计划校验。
	policy := *f.App.savePolicy
	policy.Version = "fixture-save-v2"
	policy.AllowedModels = []string{"molin/new-save-fixture-model"}
	if err := policy.validate(); err != nil {
		t.Fatal(err)
	}
	f.App.savePolicy = &policy
	address, err := f.App.SavedVideoDownloadURL(ctx, caller, saved.UserAssetID, "content")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(address.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	content, err := f.App.GetSavedVideoContent(ctx, caller, saved.UserAssetID, "content", q.Get("expires"), q.Get("signature"))
	if err != nil {
		t.Fatal(err)
	}
	defer content.Close()
	r, err := content.OpenRange(ctx, 0, content.Size)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	r.Close()
	hash := sha256.Sum256(body)
	if err != nil || hex.EncodeToString(hash[:]) != *root.SHA256 {
		t.Fatal("读取必须来自独立长期后端且匹配保存字节")
	}
	var op videoAssetSave
	if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
		t.Fatal(err)
	}
	plan, err := decodeVideoSavePlan(&op)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range plan {
		if _, err := original.CopyImmutable(ctx, video.VideoObjectRef{Bucket: p.SourceBucket, ObjectKey: p.SourceKey}, video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}, p.SHA256, p.Size); err != nil {
			t.Fatal(err)
		}
	}
	// 丢失非请求角色同样破坏整份保存证明，已经取得的读取能力也不能继续交付。
	for _, p := range plan {
		if p.Role == "thumbnail" {
			if err := destination.Delete(ctx, video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := content.OpenRange(ctx, 0, content.Size); !errors.Is(err, ErrVideoMediaProtected) {
		t.Fatalf("长期缩略图丢失后不得被临时影子掩盖：%v", err)
	}
	if _, err := f.App.SavedVideoDownloadURL(ctx, caller, saved.UserAssetID, "content"); !errors.Is(err, ErrVideoMediaProtected) {
		t.Fatalf("保存证明不完整时不能签发新地址：%v", err)
	}
}
