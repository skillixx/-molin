package service_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 经真实认证、G5结算和本机HTTP签发/兑换，签名不能替代身份，也不能授权另一版本媒体。
func TestVideoG6AssetDownloadHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	s := httptest.NewServer(mux)
	defer s.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	request := func(path, key, rangeValue string, want int) ([]byte, http.Header) {
		t.Helper()
		r, err := http.NewRequest("GET", s.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if rangeValue != "" {
			r.Header.Set("Range", rangeValue)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("平台下载应%d，实际%d", want, resp.StatusCode)
		}
		return data, resp.Header
	}
	var assets []model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", id).Find(&assets).Error; err != nil {
		t.Fatal(err)
	}
	byRole := map[string]model.AIImageAsset{}
	for _, a := range assets {
		byRole[a.AssetRole] = a
	}
	root := byRole["content"]
	issuePath := func(a string) string { return "/api/token/video-assets/" + a + "/download-url" }
	request(issuePath(root.PublicID), f.Key, "", 503)
	f.EnableAssetDownloads()
	issue := func(a, key string) service.VideoAssetDownloadURL {
		t.Helper()
		started := time.Now()
		data, _ := request(issuePath(a), key, "", 200)
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &envelope) != nil || envelope.Code != 0 {
			t.Fatal("签发响应无效")
		}
		var fields map[string]json.RawMessage
		var value service.VideoAssetDownloadURL
		if json.Unmarshal(envelope.Data, &fields) != nil || len(fields) != 3 || fields["asset_id"] == nil || fields["download_url"] == nil || fields["expires_at"] == nil || json.Unmarshal(envelope.Data, &value) != nil {
			t.Fatal("短效地址必须固定三键")
		}
		u, err := url.Parse(value.DownloadURL)
		// 服务端签发发生在请求与响应之间，15分钟上界不能错误锚定到客户端发起前。
		if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/api/token/video-assets/"+a+"/content" || len(u.Query()) != 2 || value.AssetID != a || !value.ExpiresAt.After(started) || value.ExpiresAt.After(time.Now().Add(15*time.Minute)) {
			t.Fatal("仅允许绑定资产的15分钟内同源地址")
		}
		return value
	}
	for _, role := range []string{"content", "cover", "preview", "thumbnail", "derived"} {
		a := byRole[role]
		signed := issue(a.PublicID, f.Key)
		data, h := request(signed.DownloadURL, f.Key, "", 200)
		sum := sha256.Sum256(data)
		if a.SHA256 == nil || hex.EncodeToString(sum[:]) != *a.SHA256 || a.MIMEType == nil || h.Get("Content-Type") != *a.MIMEType || h.Get("Cache-Control") != "private, no-store" {
			t.Fatal("兑换必须返回原资产字节和真实白名单MIME")
		}
	}
	request(issuePath(byRole["moderation_copy"].PublicID), f.Key, "", 404)
	signed := issue(root.PublicID, f.Key)
	request(strings.Replace(signed.DownloadURL, root.PublicID, byRole["thumbnail"].PublicID, 1), f.Key, "", 404)
	for _, path := range []string{issuePath(root.PublicID), signed.DownloadURL} {
		r, err := http.NewRequest("HEAD", s.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+f.Key)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 405 {
			t.Fatal("签名GET不能隐式授权HEAD")
		}
	}
	request(signed.DownloadURL, "", "", 401)
	request(signed.DownloadURL, f.OtherKey, "", 404)
	request(signed.DownloadURL, f.Token, "", 404)
	data, h := request(signed.DownloadURL, f.Key, "bytes=0-31", 206)
	if len(data) != 32 || h.Get("Content-Range") == "" {
		t.Fatal("平台单Range必须保留精确长度")
	}
	u, _ := url.Parse(signed.DownloadURL)
	q := u.Query()
	q.Set("expires", strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10))
	u.RawQuery = q.Encode()
	request(u.String(), f.Key, "", 404)
	request(signed.DownloadURL+"&signature=duplicate", f.Key, "", 400)
	u, _ = url.Parse(signed.DownloadURL)
	q = u.Query()
	q.Set("signature", "0000000000000000000000000000000000000000000000000000000000000000")
	u.RawQuery = q.Encode()
	request(u.String(), f.Key, "", 404)
	if err := f.DB.Model(&model.AIImageAsset{}).Where("id=?", root.ID).Update("version_no", root.VersionNo+1).Error; err != nil {
		t.Fatal(err)
	}
	request(signed.DownloadURL, f.Key, "", 404)
	// JWT使用真实无Key任务，不借用SK任务规避来源隔离。
	jwtID := f.CreateCompletedForKey(0)
	var jwtAsset model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", jwtID).Take(&jwtAsset).Error; err != nil {
		t.Fatal(err)
	}
	jwtURL := issue(jwtAsset.PublicID, f.Token)
	request(jwtURL.DownloadURL, f.Token, "bytes=0-15", 206)
	request(jwtURL.DownloadURL, f.Key, "", 404)
}
