package service_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"molin/server/internal/middleware"
	assetmodel "molin/server/internal/modules/asset/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 首片真实HTTP写出后撤销凭据或存储资格；后续内容必须截断，不能追加JSON或继续读取长期存储。
func TestVideoG6SavedReadStreamRevocationMySQL(t *testing.T) {
	for _, mode := range []string{"jwt_revoked", "entitlement_revoked", "entitlement_expired"} {
		t.Run(mode, func(t *testing.T) {
			f := service.NewVideoContentHTTPFixture(t, true)
			entID := f.EnableAssetSaving()
			f.EnableAssetDownloads()
			id := f.CreateCompletedForKey(0)
			var root model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
				t.Fatal(err)
			}
			var expires time.Time
			var invalidationErr error
			reachedBeforeExpiry := false
			invalidate := func() {
				switch mode {
				case "jwt_revoked":
					f.RevokeToken()
				case "entitlement_revoked":
					invalidationErr = f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("status", "suspended").Error
				case "entitlement_expired":
					reachedBeforeExpiry = time.Now().Before(expires)
					if delay := time.Until(expires.Add(50 * time.Millisecond)); delay > 0 {
						time.Sleep(delay)
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
			transport := &http.Transport{Proxy: nil}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
			call := func(method, path, key string) *http.Response {
				t.Helper()
				r, err := http.NewRequest(method, s.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Authorization", "Bearer "+f.Token)
				if key != "" {
					r.Header.Set("Idempotency-Key", key)
				}
				resp, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				return resp
			}
			decode := func(resp *http.Response, want int, value any) {
				t.Helper()
				defer resp.Body.Close()
				var envelope struct {
					Code int             `json:"code"`
					Data json.RawMessage `json:"data"`
				}
				if resp.StatusCode != want || json.NewDecoder(resp.Body).Decode(&envelope) != nil || envelope.Code != 0 || json.Unmarshal(envelope.Data, value) != nil {
					t.Fatalf("长期HTTP夹具应%d实际%d", want, resp.StatusCode)
				}
			}
			var saved service.VideoAssetSaveReply
			decode(call("POST", "/api/token/video-assets/"+root.PublicID+"/save", "g6-long-stream-"+mode), 201, &saved)
			if mode == "entitlement_expired" {
				if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("expires_at", time.Now().UTC().Add(10*time.Second)).Error; err != nil {
					t.Fatal(err)
				}
				var entitlement assetmodel.UserEntitlement
				if err := f.DB.First(&entitlement, entID).Error; err != nil || entitlement.ExpiresAt == nil {
					t.Fatal("存储自然到期测试必须读取数据库实际期限")
				}
				expires = *entitlement.ExpiresAt
			}
			var address service.VideoAssetDownloadURL
			decode(call("GET", fmt.Sprintf("/api/token/video-saved-assets/%d/content/download-url", saved.UserAssetID), ""), 200, &address)
			before := f.FinancialSnapshot()
			ranges := f.RangeCalls()
			resp := call("GET", address.DownloadURL, "")
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("长期内容请求未结束")
			}
			if invalidationErr != nil {
				t.Fatal("合成存储撤权未实际提交")
			}
			if resp.StatusCode != 200 || readErr == nil || len(body) != 1<<20 || f.RangeCalls()-ranges != 1 {
				t.Fatalf("失效后必须恰好一片且截断：status=%d bytes=%d ranges=%d read_failed=%t", resp.StatusCode, len(body), f.RangeCalls()-ranges, readErr != nil)
			}
			if mode == "entitlement_expired" && !reachedBeforeExpiry {
				t.Fatal("必须先于实际权益期限写出第一片，不能用初次准入失败替代流中失效")
			}
			if !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("长期读取中断不能改变原生成财务")
			}
		})
	}
}
