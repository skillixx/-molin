package service_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	assetmodel "molin/server/internal/modules/asset/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 真实HTTP保存走原G5已交付任务和独立存储权益，跨键重放不能形成第二个长期资产或额外生成费用。
func TestVideoG6AssetSaveHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	entID := f.EnableAssetSaving()
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	s := httptest.NewServer(mux)
	defer s.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	call := func(method, path, token, key, body string, want int) json.RawMessage {
		t.Helper()
		r, err := http.NewRequest(method, s.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("保存流程应%d实际%d", want, resp.StatusCode)
		}
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &envelope) != nil {
			t.Fatal("平台返回必须有效JSON")
		}
		return envelope.Data
	}
	path := "/api/token/video-assets/" + root.PublicID + "/save"
	call("POST", path, f.OtherKey, "g6-save-http-command", "", 404)
	call("POST", path, f.Token, "g6-save-http-command", "", 404)
	call("POST", path, f.Key, "g6-save-http-command", `{"bucket":"forged"}`, 400)
	call("POST", path+"?capacity=0", f.Key, "g6-save-http-command", "", 400)
	before := f.FinancialSnapshot()
	var eventsBefore int64
	if err := f.DB.Table("ai_gateway_task_events").Where("task_id=?", root.TaskID).Count(&eventsBefore).Error; err != nil {
		t.Fatal(err)
	}
	var first service.VideoAssetSaveReply
	if json.Unmarshal(call("POST", path, f.Key, "g6-save-http-command", "", 201), &first) != nil {
		t.Fatal("保存回复无效")
	}
	if first.AssetID != root.PublicID || first.VideoID != id || first.RequestID != root.RequestID || first.UserAssetID == 0 || first.Status != "completed" || first.Idempotent || first.SizeBytes == 0 {
		t.Fatal("首次保存必须关联原请求和新的长期资产")
	}
	var replay service.VideoAssetSaveReply
	if json.Unmarshal(call("POST", path, f.Key, "g6-save-http-second-key", "", 200), &replay) != nil || !replay.Idempotent || replay.UserAssetID != first.UserAssetID {
		t.Fatal("不同幂等键也只能形成同一长期资产")
	}
	var ent assetmodel.UserEntitlement
	if err := f.DB.First(&ent, entID).Error; err != nil {
		t.Fatal(err)
	}
	if !ent.QuotaReserved.IsZero() || !ent.QuotaUsed.Equal(decimal.NewFromUint64(first.SizeBytes)) {
		t.Fatal("复制完成应一次性将预留结转已用")
	}
	var saved assetmodel.UserAsset
	if err := f.DB.First(&saved, first.UserAssetID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.UserID != f.ProjectID || saved.AssetType != "video_file" || saved.ExpiresAt != nil || saved.Status != "active" {
		t.Fatal("必须形成无自动过期的同用户长期资产")
	}
	var count int64
	if err := f.DB.Table("asset_events").Where("asset_id=? AND event_type='created'", first.UserAssetID).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("用户资产必须且仅有一个创建事件")
	}
	call("DELETE", "/v1/videos/"+id, f.Key, "g6-save-original-delete", "", 200)
	// 原结果已删除后仍能复验独立副本，不能依赖原临时对象假装长期保存。
	call("POST", path, f.Key, "g6-save-after-original-delete", "", 200)
	var eventsAfter int64
	if err := f.DB.Table("ai_gateway_task_events").Where("task_id=?", root.TaskID).Count(&eventsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) || eventsBefore != eventsAfter {
		t.Fatal("保存和原媒体删除不能新增生成计费或任务事件")
	}
}
