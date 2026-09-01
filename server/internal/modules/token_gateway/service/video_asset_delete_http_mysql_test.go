package service_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	assetmodel "molin/server/internal/modules/asset/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 真正HTTP删除缩略图只影响自身，之后根删除可收敛余下对象，长期副本及原财务始终保留。
func TestVideoG6MediaDeletePlatformAssetHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	entID := f.EnableAssetSaving()
	f.EnableAssetDownloads()
	id := f.CreateCompletedForKey(f.ProjectID)
	var assets []model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", id).Find(&assets).Error; err != nil {
		t.Fatal(err)
	}
	roles := map[string]model.AIImageAsset{}
	for _, a := range assets {
		roles[a.AssetRole] = a
	}
	root, thumb := roles["content"], roles["thumbnail"]
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	s := httptest.NewServer(mux)
	defer s.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	call := func(method, path, key, idem, body string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, s.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		if idem != "" {
			req.Header.Set("Idempotency-Key", idem)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s应%d实际%d", method, want, resp.StatusCode)
		}
		return raw
	}
	decode := func(raw []byte, value any) {
		t.Helper()
		var env struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Code != 0 || json.Unmarshal(env.Data, value) != nil {
			t.Fatal("平台响应无效")
		}
	}
	var saved service.VideoAssetSaveReply
	decode(call("POST", "/api/token/video-assets/"+root.PublicID+"/save", f.Key, "g6-delete-platform-save", "", 201), &saved)
	var entBefore assetmodel.UserEntitlement
	if err := f.DB.First(&entBefore, entID).Error; err != nil {
		t.Fatal(err)
	}
	before := f.FinancialSnapshot()
	path := "/api/token/video-assets/" + thumb.PublicID
	body := fmt.Sprintf(`{"version_no":%d}`, thumb.VersionNo)
	call("DELETE", path, f.OtherKey, "g6-delete-platform-wrong-key", body, 404)
	call("DELETE", path, f.Key, "g6-delete-platform-wrong-version", fmt.Sprintf(`{"version_no":%d}`, thumb.VersionNo+1), 409)
	call("DELETE", path, f.Key, "g6-delete-platform-forged", fmt.Sprintf(`{"version_no":%d,"bucket":"forged"}`, thumb.VersionNo), 400)
	call("DELETE", "/api/token/video-assets/"+roles["moderation_copy"].PublicID, f.Key, "g6-delete-platform-audit", fmt.Sprintf(`{"version_no":%d}`, roles["moderation_copy"].VersionNo), 404)
	if f.MediaDeleteCalls() != 0 {
		t.Fatal("被拒绝的删除不得访问Delete边界")
	}
	var reply service.VideoAssetDeleted
	decode(call("DELETE", path, f.Key, "g6-delete-platform-thumb", body, 200), &reply)
	if reply.AssetID != thumb.PublicID || reply.Scope != "asset" || !reply.MediaDeleted || reply.LifecycleState != "deleted" || reply.Idempotent {
		t.Fatal("单资产删除响应不准确")
	}
	for role, fact := range f.InspectMedia(id) {
		if role == "thumbnail" {
			if fact.Present || !fact.Deleted {
				t.Fatal("缩略图必须已确认删除")
			}
		} else if !fact.Present || !fact.HashMatches {
			t.Fatal("单删不能改变父、兄弟或审核副本")
		}
	}
	call("GET", "/v1/videos/"+id, f.Key, "", "", 404)
	call("GET", "/v1/videos/"+id+"/content", f.Key, "", "", 404)
	var list struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(call("GET", "/v1/videos", f.Key, "", "", 200), &list) != nil || len(list.Data) != 0 {
		t.Fatal("部分删除的兼容Job不再公开")
	}
	var details map[string]any
	decode(call("GET", "/api/token/videos/requests/by-video/"+id, f.Key, "", "", 200), &details)
	if details["media_deleted"] != false || details["media_partially_deleted"] != true || details["can_deliver"] != false || details["execution_status"] != "succeeded" || details["billing_status"] != "settled" {
		t.Fatal("部分删除不能伪造整个视频删除或生成状态")
	}
	var address service.VideoAssetDownloadURL
	decode(call("GET", fmt.Sprintf("/api/token/video-saved-assets/%d/content/download-url", saved.UserAssetID), f.Key, "", "", 200), &address)
	call("GET", address.DownloadURL, f.Key, "", "", 200)
	decode(call("DELETE", path, f.Key, "g6-delete-platform-thumb", body, 200), &reply)
	if !reply.Idempotent || f.MediaDeleteCalls() != 1 {
		t.Fatal("原子资产删除重放不得重复删除")
	}
	rootPath := "/api/token/video-assets/" + root.PublicID
	rootBody := fmt.Sprintf(`{"version_no":%d}`, root.VersionNo)
	call("DELETE", rootPath, f.Key, "g6-delete-platform-thumb", rootBody, 409)
	decode(call("DELETE", rootPath, f.Key, "g6-delete-platform-root", rootBody, 200), &reply)
	if reply.Scope != "video" || !reply.MediaDeleted {
		t.Fatal("根删除应联动普通派生物")
	}
	for role, fact := range f.InspectMedia(id) {
		if role == "moderation_copy" {
			if !fact.Present || !fact.HashMatches {
				t.Fatal("审核副本必须保留")
			}
		} else if fact.Present || !fact.Deleted {
			t.Fatal("根删除后五个交付对象均须清除")
		}
	}
	if f.MediaDeleteCalls() != 5 {
		t.Fatal("已单删缩略图不能再次删除或扩张目标")
	}
	decode(call("DELETE", path, f.Key, "g6-delete-platform-thumb", body, 200), &reply)
	if !reply.Idempotent {
		t.Fatal("根删除不能破坏原子资产删除重放")
	}
	var entAfter assetmodel.UserEntitlement
	if err := f.DB.First(&entAfter, entID).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) || !entBefore.QuotaUsed.Equal(entAfter.QuotaUsed) || !entBefore.QuotaReserved.Equal(entAfter.QuotaReserved) {
		t.Fatal("删除临时媒体不能改生成财务或释放长期容量")
	}
}
