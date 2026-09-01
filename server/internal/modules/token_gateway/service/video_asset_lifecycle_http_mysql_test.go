package service_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 生命周期必须在真实授权和金融事实下读取，删除后保留元数据而不能继续授予下载。
func TestVideoG6AssetLifecycleHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	var assets []model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", id).Find(&assets).Error; err != nil {
		t.Fatal(err)
	}
	byRole := map[string]model.AIImageAsset{}
	for _, a := range assets {
		byRole[a.AssetRole] = a
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	s := httptest.NewServer(mux)
	defer s.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	get := func(assetID, key string, want int) *service.VideoAssetLifecycle {
		t.Helper()
		before := videoLifecycleFacts(t, f.DB, f.ProjectID)
		heads, ranges, deletes := f.HeadCalls(), f.RangeCalls(), f.MediaDeleteCalls()
		r, err := http.NewRequest("GET", s.URL+"/api/token/video-assets/"+assetID+"/lifecycle", nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.NewDecoder(resp.Body).Decode(&envelope) != nil || resp.StatusCode != want {
			t.Fatalf("生命周期应%d，实际%d", want, resp.StatusCode)
		}
		if !bytes.Equal(before, videoLifecycleFacts(t, f.DB, f.ProjectID)) || heads != f.HeadCalls() || ranges != f.RangeCalls() || deletes != f.MediaDeleteCalls() {
			t.Fatal("元数据查询不得改变财务、事件或下载租约，也不得读取/删除媒体")
		}
		if want != 200 {
			return nil
		}
		var fields map[string]json.RawMessage
		var value service.VideoAssetLifecycle
		if envelope.Code != 0 || json.Unmarshal(envelope.Data, &fields) != nil || len(fields) != 21 || json.Unmarshal(envelope.Data, &value) != nil {
			t.Fatal("生命周期必须只返回21个低敏字段")
		}
		for _, name := range strings.Fields("asset_id video_id request_id role parent_asset_id version_no lifecycle_state expires_at media_deleted media_deleted_at task_media_deleted deletion_status moderation_status explicit_label_status implicit_label_status legal_hold dispute_status execution_status billing_status delivery_status can_download") {
			if _, ok := fields[name]; !ok {
				t.Fatalf("生命周期缺少冻结字段%s", name)
			}
		}
		return &value
	}
	root := byRole["content"]
	life := get(root.PublicID, f.Key, 200)
	if life.VideoID != id || life.AssetID != root.PublicID || life.ParentAssetID != nil || life.DeletionStatus != nil || life.MediaDeletedAt != nil || !life.CanDownload || !life.ExpiresAt.Equal(root.ExpiresAt) {
		t.Fatal("根资产原归属、期限或下载判断错误")
	}
	thumb := get(byRole["thumbnail"].PublicID, f.Key, 200)
	if thumb.ParentAssetID == nil || *thumb.ParentAssetID != root.PublicID || thumb.CanDownload {
		t.Fatal("当前缩略图仅元数据，不冒充未实现的下载入口")
	}
	get(root.PublicID, f.OtherKey, 404)
	get(root.PublicID, f.Token, 404)
	get(f.SourceID, f.Key, 404)
	get(byRole["moderation_copy"].PublicID, f.Key, 404)
	if err := f.DB.Model(&model.AIImageAsset{}).Where("id=?", root.ID).Update("legal_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	if held := get(root.PublicID, f.Key, 200); !held.LegalHold || held.CanDownload {
		t.Fatal("保全元数据可查但不能下载")
	}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("id=?", root.ID).Update("legal_hold", false).Error; err != nil {
		t.Fatal(err)
	}
	// 争议必须同时保全并记录开启时间；解决时保留争议历史，不改回从未发生。
	opened := time.Now().UTC().Truncate(time.Second)
	if err := f.DB.Model(&model.AIImageAsset{}).Where("id=?", root.ID).Updates(map[string]any{"dispute_status": "open", "legal_hold": true, "dispute_opened_at": opened}).Error; err != nil {
		t.Fatal(err)
	}
	if value := get(root.PublicID, f.Key, 200); value.CanDownload || !value.LegalHold || value.DisputeStatus != "open" {
		t.Fatal("争议元数据可查，但不能授权正文")
	}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("id=?", root.ID).Updates(map[string]any{"dispute_status": "resolved", "legal_hold": false, "dispute_resolved_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if value := get(root.PublicID, f.Key, 200); !value.CanDownload || value.DisputeStatus != "resolved" {
		t.Fatal("争议解决后须保留历史并重新判断当前下载资格")
	}
	quarantinedID := f.CreateQuarantinedForKey(f.ProjectID)
	var quarantined model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", quarantinedID).Take(&quarantined).Error; err != nil {
		t.Fatal(err)
	}
	if value := get(quarantined.PublicID, f.Key, 200); value.CanDownload || value.LifecycleState != "quarantined" || value.ModerationStatus != "rejected" {
		t.Fatal("真实审核拒绝产生的隔离资产仅可查元数据，不能下载")
	}
	if err := f.DB.Exec("UPDATE api_keys SET video_generate_allowed=0 WHERE id=?", f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	get(root.PublicID, f.Key, 403)
	if err := f.DB.Exec("UPDATE api_keys SET video_generate_allowed=1 WHERE id=?", f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if !get(root.PublicID, f.Key, 200).CanDownload {
		t.Fatal("恢复当前权限和安全状态后，应重新评估可下载")
	}
	r, err := http.NewRequest("DELETE", s.URL+"/v1/videos/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+f.Key)
	r.Header.Set("Idempotency-Key", "g6-lifecycle-delete")
	f.FailMediaDelete(true)
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatal("必须真实注入外部删除失败")
	}
	failed := get(root.PublicID, f.Key, 200)
	if failed.CanDownload || failed.MediaDeleted || failed.TaskMediaDeleted || failed.DeletionStatus == nil || *failed.DeletionStatus != "delete_failed" || failed.LifecycleState != "delete_failed" {
		t.Fatal("删除失败只能显示待恢复，不能提前宣称正文删除或可下载")
	}
	f.FailMediaDelete(false)
	resp, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("测试必须实际完成媒体删除")
	}
	deleted := get(root.PublicID, f.Key, 200)
	if deleted.CanDownload || !deleted.MediaDeleted || !deleted.TaskMediaDeleted || deleted.MediaDeletedAt == nil || deleted.DeletionStatus == nil || *deleted.DeletionStatus != "completed" || deleted.LifecycleState != "deleted" {
		t.Fatal("删除事实与当前读取权限不一致")
	}
}

// 数据库是Goal指定的验收边界；快照仅在内存比较，不把任何原始财务行输出为测试日志。
func videoLifecycleFacts(t *testing.T, db *gorm.DB, userID uint64) []byte {
	t.Helper()
	facts := map[string][]string{}
	for _, table := range []string{"wallets", "wallet_holds", "wallet_transactions", "ai_requests", "ai_gateway_quotes", "ai_usage_items", "ai_request_wallet_links", "ai_outbox_events", "ai_gateway_task_events", "ai_gateway_provider_callback_events", "ai_video_download_leases", "ai_video_download_scopes", "ai_gateway_tasks", "ai_gateway_assets", "ai_video_media_deletions", "ai_video_media_delete_commands"} {
		predicate := "user_id=?"
		switch table {
		case "ai_usage_items", "ai_request_wallet_links":
			predicate = "request_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
		case "ai_outbox_events":
			predicate = "aggregate_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
		case "ai_video_download_scopes":
			// 合成夹具的user与Project同ID，覆盖这两种范围锁记录。
			predicate = "scope_id=?"
		}
		var rows []map[string]any
		if err := db.Table(table).Where(predicate, userID).Find(&rows).Error; err != nil {
			t.Fatal("无法读取无副作用验收事实")
		}
		for _, row := range rows {
			raw, err := json.Marshal(row)
			if err != nil {
				t.Fatal("无法编码验收事实")
			}
			facts[table] = append(facts[table], string(raw))
		}
		sort.Strings(facts[table])
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal("无法编码验收快照")
	}
	return raw
}

// 在真实查询末段注入一次数据库延迟，证明子资产跨期限时不能沿用早先的交付判断。
func TestVideoG6AssetLifecycleExpiryMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	var root model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(3 * time.Second).Truncate(time.Millisecond)
	updated := f.DB.Model(&model.AIImageAsset{}).Where("task_id=? AND asset_role='thumbnail'", root.TaskID).Update("expires_at", expires)
	if updated.Error != nil || updated.RowsAffected != 1 {
		t.Fatal("必须实际更新唯一缩略图期限")
	}
	// 数据库期限可能按列精度截断，屏障只能使用读回的持久化时刻，不能沿用内存毫秒值。
	var thumbnail model.AIImageAsset
	if err := f.DB.Where("task_id=? AND asset_role='thumbnail'", root.TaskID).Take(&thumbnail).Error; err != nil {
		t.Fatal(err)
	}
	expires = thumbnail.ExpiresAt
	var delayed, reachedBeforeExpiry atomic.Bool
	name := "g6_lifecycle_expiry_delay"
	if err := f.DB.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		// 只延迟生命周期的末段状态读取，不替换查询结果或认证/计费实现。
		if tx.Statement.Table == "ai_video_media_deletions" && strings.Contains(tx.Statement.SQL.String(), "SELECT `status`") && delayed.CompareAndSwap(false, true) {
			reachedBeforeExpiry.Store(time.Now().Before(expires))
			if wait := time.Until(expires.Add(50 * time.Millisecond)); wait > 0 {
				time.Sleep(wait)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer f.DB.Callback().Query().Remove(name)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	s := httptest.NewServer(mux)
	defer s.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 15 * time.Second}
	r, err := http.NewRequest("GET", s.URL+"/api/token/video-assets/"+root.PublicID+"/lifecycle", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+f.Key)
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var envelope struct {
		Code int                         `json:"code"`
		Data service.VideoAssetLifecycle `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if !delayed.Load() || !reachedBeforeExpiry.Load() {
		t.Fatal("必须实际跨越子资产期限，不能以未触发的时序算通过")
	}
	if resp.StatusCode != 200 || envelope.Code != 0 || envelope.Data.AssetID != root.PublicID || !envelope.Data.ExpiresAt.After(time.Now()) {
		t.Fatal("根资产仍有效，低敏生命周期必须可查询")
	}
	if envelope.Data.CanDownload {
		t.Fatal("子资产在查询期间过期后，根资产不得继续显示can_download=true")
	}
}
