package service_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	assetmodel "molin/server/internal/modules/asset/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6SavedReadHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	entID := f.EnableAssetSaving()
	f.EnableAssetDownloads()
	id := f.CreateCompletedForKey(f.ProjectID)
	var assets []model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", id).Find(&assets).Error; err != nil {
		t.Fatal(err)
	}
	byRole := map[string]model.AIImageAsset{}
	for _, a := range assets {
		byRole[a.AssetRole] = a
	}
	root := byRole["content"]
	// 在保存之前冻结真实源期限，之后只等待自然到期，不能事后篡改已签名的元数据。
	if err := f.DB.Model(&model.AIImageAsset{}).Where("id=?", byRole["thumbnail"].ID).Update("expires_at", time.Now().UTC().Add(5*time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	var timed model.AIImageAsset
	if err := f.DB.First(&timed, byRole["thumbnail"].ID).Error; err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	s := httptest.NewServer(mux)
	defer s.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	call := func(method, path, key, idem string, want int) ([]byte, http.Header) {
		t.Helper()
		heads, ranges := f.HeadCalls(), f.RangeCalls()
		var before []byte
		if method == http.MethodGet {
			before = videoSavedReadFacts(t, f.DB, f.ProjectID)
		}
		r, err := http.NewRequest(method, s.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if idem != "" {
			r.Header.Set("Idempotency-Key", idem)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(body, &failure)
			t.Fatalf("长期读取应%d实际%d，低敏分类=%s", want, resp.StatusCode, failure.Error.Code)
		}
		if method == http.MethodGet && want != http.StatusOK && (f.HeadCalls() != heads || f.RangeCalls() != ranges) {
			t.Fatal("被准入、签名或归属拒绝的读取不得访问对象存储")
		}
		if method == http.MethodGet && !bytes.Equal(before, videoSavedReadFacts(t, f.DB, f.ProjectID)) {
			t.Fatal("长期查询不能改写原业务、保存事实或存储容量")
		}
		return body, resp.Header
	}
	decode := func(body []byte, value any) {
		t.Helper()
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Code != 0 || json.Unmarshal(envelope.Data, value) != nil {
			t.Fatal("平台成功响应无效")
		}
	}
	body, _ := call("POST", "/api/token/video-assets/"+root.PublicID+"/save", f.Key, "g6-saved-read-save", 201)
	var saved service.VideoAssetSaveReply
	decode(body, &saved)
	base := fmt.Sprintf("/api/token/video-saved-assets/%d/", saved.UserAssetID)
	issue := func(role, key string) string {
		t.Helper()
		body, _ := call("GET", base+role+"/download-url", key, "", 200)
		var value service.VideoAssetDownloadURL
		decode(body, &value)
		if value.AssetID != fmt.Sprint(saved.UserAssetID) || !strings.HasPrefix(value.DownloadURL, base+role+"/content?") {
			t.Fatal("签名必须绑定当前长期资产和角色")
		}
		return value.DownloadURL
	}
	read := func(role string) {
		t.Helper()
		url := issue(role, f.Key)
		body, headers := call("GET", url, f.Key, "", 200)
		hash := sha256.Sum256(body)
		source := byRole[role]
		if hex.EncodeToString(hash[:]) != *source.SHA256 || headers.Get("Content-Type") != *source.MIMEType {
			t.Fatal("长期目标字节和MIME必须与原保存事实一致")
		}
	}
	before := f.FinancialSnapshot()
	for _, role := range []string{"content", "cover", "preview", "thumbnail", "derived"} {
		read(role)
	}
	if wait := time.Until(timed.ExpiresAt.Add(50 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	read("content")
	call("DELETE", "/v1/videos/"+id, f.Key, "g6-saved-read-delete-original", 200)
	call("GET", "/v1/videos/"+id, f.Key, "", 404)
	call("GET", "/v1/videos/"+id+"/content", f.Key, "", 404)
	for _, role := range []string{"content", "cover", "preview", "thumbnail", "derived"} {
		read(role)
	}
	call("GET", base+"moderation_copy/download-url", f.Key, "", 404)
	call("GET", base+"content/download-url", f.OtherKey, "", 404)
	call("GET", base+"content/download-url", f.Token, "", 404)
	url := issue("content", f.Key)
	call("GET", url, "", "", 401)
	call("GET", url[:len(url)-64]+strings.Repeat("0", 64), f.Key, "", 404)
	call("GET", strings.Replace(url, "/content/content?", "/thumbnail/content?", 1), f.Key, "", 404)
	if err := f.DB.Model(&assetmodel.UserAsset{}).Where("id=?", saved.UserAssetID).Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	call("GET", url, f.Key, "", 409)
	if err := f.DB.Model(&assetmodel.UserAsset{}).Where("id=?", saved.UserAssetID).Update("status", "active").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("expires_at", time.Now().UTC().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	call("GET", url, f.Key, "", 403)
	if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("expires_at", time.Now().UTC().Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("entitlement_type", "api_calls").Error; err != nil {
		t.Fatal(err)
	}
	call("GET", url, f.Key, "", 403)
	if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("entitlement_type", "storage_bytes").Error; err != nil {
		t.Fatal(err)
	}
	var license assetmodel.UserEntitlement
	if err := f.DB.First(&license, entID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Table("products").Where("id=?", license.ProductID).Update("product_type", "token").Error; err != nil {
		t.Fatal(err)
	}
	call("GET", url, f.Key, "", 403)
	if err := f.DB.Table("products").Where("id=?", license.ProductID).Update("product_type", "storage").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("id=?", root.ID).Update("legal_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	call("GET", url, f.Key, "", 409)
	if !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("长期读取与临时删除不能改变原生成财务")
	}
}

// 逐行核对业务事实，排除允许变化的认证使用时间、下载租约和运行范围锁；原始行只在内存比较。
func videoSavedReadFacts(t *testing.T, db *gorm.DB, userID uint64) []byte {
	t.Helper()
	facts := map[string][]string{}
	for _, table := range []string{"wallets", "wallet_holds", "wallet_transactions", "ai_requests", "ai_gateway_quotes", "ai_usage_items", "ai_request_wallet_links", "ai_outbox_events", "ai_gateway_task_events", "ai_gateway_provider_callback_events", "ai_gateway_tasks", "ai_gateway_assets", "ai_video_media_deletions", "ai_video_media_delete_commands", "ai_video_asset_saves", "ai_video_asset_save_commands", "user_assets", "user_entitlements", "asset_events"} {
		predicate := "user_id=?"
		switch table {
		case "ai_usage_items", "ai_request_wallet_links":
			predicate = "request_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
		case "ai_outbox_events":
			predicate = "aggregate_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
		}
		var rows []map[string]any
		if err := db.Table(table).Where(predicate, userID).Find(&rows).Error; err != nil {
			t.Fatal("无法读取长期资产业务快照")
		}
		for _, row := range rows {
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal("无法编码长期资产业务快照")
			}
			facts[table] = append(facts[table], string(raw))
		}
		sort.Strings(facts[table])
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal("无法编码长期资产快照集合")
	}
	return raw
}
