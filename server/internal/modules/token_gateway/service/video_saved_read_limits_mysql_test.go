package service

import (
	"context"
	"errors"
	"io"
	"net/url"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

func TestVideoG6SavedReadSharedLimitsMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	f.EnableAssetSaving()
	f.EnableAssetDownloads()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	saved, err := f.App.SaveVideoAsset(ctx, caller, root.PublicID, "g6-long-shared-save")
	if err != nil {
		t.Fatal(err)
	}
	address, err := f.App.SavedVideoDownloadURL(ctx, caller, saved.UserAssetID, "content")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(address.DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	short, err := f.App.GetContent(ctx, caller, id)
	if err != nil {
		t.Fatal(err)
	}
	defer short.Close()
	long, err := f.App.GetSavedVideoContent(ctx, caller, saved.UserAssetID, "content", q.Get("expires"), q.Get("signature"))
	if err != nil {
		t.Fatal(err)
	}
	defer long.Close()
	heads := f.HeadCalls()
	if _, err := f.App.GetSavedVideoContent(ctx, caller, saved.UserAssetID, "content", q.Get("expires"), q.Get("signature")); !errors.Is(err, ErrVideoDownloadLimited) {
		t.Fatalf("长期与临时必须共享用户两路限制：%v", err)
	}
	if f.HeadCalls() != heads {
		t.Fatal("超出共享连接限额前不能访问对象存储")
	}
	if err := short.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := f.App.GetSavedVideoContent(ctx, caller, saved.UserAssetID, "content", q.Get("expires"), q.Get("signature"))
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
}

func TestVideoG6SavedReadJWTMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	f.EnableAssetSaving()
	f.EnableAssetDownloads()
	id := f.CreateCompletedForKey(0)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	caller, err := f.JWT.Authenticate(ctx, f.Token)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.App.SaveVideoAsset(ctx, caller, root.PublicID, "g6-long-jwt-save")
	if err != nil {
		t.Fatal(err)
	}
	address, err := f.App.SavedVideoDownloadURL(ctx, caller, saved.UserAssetID, "thumbnail")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(address.DownloadURL)
	q := u.Query()
	content, err := f.App.GetSavedVideoContent(ctx, caller, saved.UserAssetID, "thumbnail", q.Get("expires"), q.Get("signature"))
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
	if err != nil || int64(len(body)) != content.Size || content.MIMEType != "image/png" {
		t.Fatal("JWT长期缩略图必须实际可读")
	}
	f.RevokeToken()
	if _, err := content.OpenRange(ctx, 0, content.Size); !errors.Is(err, ErrVideoJWTInvalid) {
		t.Fatal("取得能力后撤销JWT也不能继续读取")
	}
}
