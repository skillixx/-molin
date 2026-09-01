package service_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"molin/server/internal/middleware"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 真实首片写出后吊销JWT，不能让只在初次兑换验签的实现继续输出余下媒体。
type videoRevokeAfterChunk struct {
	http.ResponseWriter
	written int
	revoke  func()
}

func (w *videoRevokeAfterChunk) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *videoRevokeAfterChunk) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.written += n
	if w.written >= 1<<20 && w.revoke != nil {
		revoke := w.revoke
		w.revoke = nil
		revoke()
	}
	return n, err
}

func TestVideoG6AssetDownloadJWTRevocationMySQL(t *testing.T) {
	testVideoAssetCredentialExpiry(t, "revoked")
}

func TestVideoG6AssetDownloadJWTExpiryMySQL(t *testing.T) {
	testVideoAssetCredentialExpiry(t, "expired")
}

func TestVideoG6AssetDownloadJWTDependencyMySQL(t *testing.T) {
	testVideoAssetCredentialExpiry(t, "unavailable")
}

func TestVideoG6AssetDownloadURLExpiryMySQL(t *testing.T) {
	testVideoAssetCredentialExpiry(t, "url_expired")
}

// 真实首片屏障分别触发吊销、依赖故障、JWT自然到期或合法签名自然到期，不以篡改URL代替到期测试。
func testVideoAssetCredentialExpiry(t *testing.T, mode string) {
	f := service.NewVideoContentHTTPFixture(t, true)
	f.EnableAssetDownloads()
	keyID := uint64(0)
	if mode == "url_expired" {
		keyID = f.ProjectID
	}
	id := f.CreateCompletedForKey(keyID)
	var asset model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&asset).Error; err != nil {
		t.Fatal(err)
	}
	token := f.Token
	var expiry time.Time
	if mode == "expired" {
		token, expiry = f.ShortJWT(5)
	}
	if mode == "url_expired" {
		token = f.Key
		if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=? AND asset_role='thumbnail'", asset.TaskID).Update("expires_at", time.Now().UTC().Add(5*time.Second)).Error; err != nil {
			t.Fatal(err)
		}
		var thumb model.AIImageAsset
		if err := f.DB.Where("task_id=? AND asset_role='thumbnail'", asset.TaskID).Take(&thumb).Error; err != nil {
			t.Fatal(err)
		}
		expiry = thumb.ExpiresAt
	}
	reachedBeforeExpiry := false
	invalidate := func() {
		switch mode {
		case "revoked":
			f.RevokeToken()
		case "unavailable":
			f.FailJWTRevocations()
		default:
			reachedBeforeExpiry = time.Now().Before(expiry)
			if wait := time.Until(expiry.Add(50 * time.Millisecond)); wait > 0 {
				time.Sleep(wait)
			}
		}
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	done := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			defer func() { done <- struct{}{} }()
			w = &videoRevokeAfterChunk{ResponseWriter: w, revoke: invalidate}
		}
		middleware.Recovery(mux).ServeHTTP(w, r)
	}))
	defer s.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	get := func(path string) *http.Response {
		t.Helper()
		r, err := http.NewRequest("GET", s.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := get("/api/token/video-assets/" + asset.PublicID + "/download-url")
	var envelope struct {
		Code int                           `json:"code"`
		Data service.VideoAssetDownloadURL `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&envelope) != nil || resp.StatusCode != 200 || envelope.Code != 0 {
		t.Fatal("必须成功签发JWT媒体地址")
	}
	resp.Body.Close()
	if !expiry.IsZero() && envelope.Data.ExpiresAt.After(expiry) {
		t.Fatal("签发期限不得超过JWT或六资产的最早期限")
	}
	resp = get(envelope.Data.DownloadURL)
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("服务端未回收连接")
	}
	if !expiry.IsZero() && !reachedBeforeExpiry {
		t.Fatal("自然到期测试必须先以有效身份写出首片，再跨实际到期时间")
	}
	if resp.StatusCode != 200 || !errors.Is(readErr, io.ErrUnexpectedEOF) || !bytes.Equal(body, service.VideoPlayableFixtureBytes()[:1<<20]) {
		t.Fatalf("JWT首片后吊销必须断流且无后续字节，实际length=%d err=%v", len(body), readErr)
	}
	var count int64
	if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL", f.ProjectID).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("吊销断流必须回收下载租约")
	}
}
