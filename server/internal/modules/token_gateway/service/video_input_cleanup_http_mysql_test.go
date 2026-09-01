package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

// 导入副本到期清理后，真实HTTP仍能按原命令返回200；来源图片过期不等于来源正文被删除。
func TestVideoG6InputCleanupMySQLImportHTTP(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	financialSnapshot := func() string {
		t.Helper()
		// 比较合成主体的完整财务行而非仅数量；排序消除查询顺序影响，原文只存在测试内存。
		snapshots := map[string][]string{}
		for _, table := range []string{"wallets", "ai_requests", "ai_gateway_quotes", "wallet_holds", "wallet_transactions", "ai_usage_items", "ai_request_wallet_links", "ai_outbox_events"} {
			predicate := "user_id=?"
			if table == "ai_usage_items" || table == "ai_request_wallet_links" {
				predicate = "request_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
			} else if table == "ai_outbox_events" {
				predicate = "aggregate_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)"
			}
			var rows []map[string]any
			if err := f.DB.Table(table).Where(predicate, f.ProjectID).Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			snapshots[table] = []string{}
			for _, row := range rows {
				raw, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				snapshots[table] = append(snapshots[table], string(raw))
			}
			sort.Strings(snapshots[table])
		}
		raw, err := json.Marshal(snapshots)
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	beforeFinance := financialSnapshot()
	call := func(method, path, key, credential, body string, want int) json.RawMessage {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want || (want < 300 && envelope.Code != 0) || (want == 404 && envelope.Code != 40400) {
			t.Fatalf("HTTP合同错误：status=%d code=%d", resp.StatusCode, envelope.Code)
		}
		return envelope.Data
	}
	imported := call("POST", "/api/token/video-inputs/from-image-asset", "g6-cleanup-import-create", f.Key, fmt.Sprintf(`{"source_asset_id":%q}`, f.SourceID), 201)
	var result service.VideoInputImportReply
	if json.Unmarshal(imported, &result) != nil || result.InputAssetID == nil {
		t.Fatal("导入未创建独立输入")
	}
	var input model.AIGatewayInputAsset
	if err := f.DB.Where("public_id=?", *result.InputAssetID).Take(&input).Error; err != nil {
		t.Fatal(err)
	}
	path := "/api/token/video-inputs/" + input.PublicID
	key := "g6-cleanup-import-delete"
	body := fmt.Sprintf(`{"version_no":%d}`, input.VersionNo)
	var requested service.VideoInputDeletionReply
	if json.Unmarshal(call("DELETE", path, key, f.Key, body, 202), &requested) != nil || requested.MediaDeleted {
		t.Fatal("申请不能提前声称已删")
	}
	if !f.InputPresent(input.PublicID) || !f.SourcePresent() {
		t.Fatal("清理前必须有独立输入和来源正文，且hash各自匹配")
	}
	// 当前保全必须优先于清理；仅变更合成来源，解除后再执行到期路径。
	clock := input.ExpiresAt.Add(time.Second)
	policy := service.VideoInputCleanupPolicy{Purpose: "non_commercial_test_fixture", Version: "g6-import-cleanup-fixture", BoundRetention: 7 * 24 * time.Hour, Now: func() time.Time { return clock }}
	owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &f.ProjectID}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("public_id=?", f.SourceID).Update("legal_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	if value, err := f.App.CleanupInput(context.Background(), input.PublicID, owner, policy); err == nil || value != nil {
		t.Fatal("来源保全必须阻止清理")
	}
	if !f.InputPresent(input.PublicID) || !f.SourcePresent() {
		t.Fatal("保全拒绝后独立输入和来源正文必须仍存在且hash匹配")
	}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("public_id=?", f.SourceID).Updates(map[string]any{"legal_hold": false, "expires_at": time.Now().UTC().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	completed, err := f.App.CleanupInput(context.Background(), input.PublicID, owner, policy)
	if err != nil || completed == nil || !completed.MediaDeleted {
		t.Fatalf("独立导入目标应可确认清理：%v", err)
	}
	if !f.SourcePresent() || !f.InputDeleted(input.PublicID) || f.InputPresent(input.PublicID) {
		t.Fatal("只能清理独立输入，不得删除来源图片或伪造清理")
	}
	var history service.VideoInputDeletionReply
	historyBody := call("DELETE", path, key, f.Key, body, 200)
	var fields map[string]json.RawMessage
	if json.Unmarshal(historyBody, &fields) != nil || len(fields) != 6 {
		t.Fatal("完成回执必须严格返回六个低敏字段")
	}
	for _, name := range []string{"input_asset_id", "lifecycle_state", "version_no", "delete_requested_at", "media_deleted", "idempotent"} {
		if value, ok := fields[name]; !ok || string(value) == "null" {
			t.Fatalf("完成回执字段缺失或为空：%s", name)
		}
	}
	if json.Unmarshal(historyBody, &history) != nil || !history.MediaDeleted || !history.Idempotent || !history.DeleteRequestedAt.Equal(requested.DeleteRequestedAt) || history.InputAssetID != input.PublicID || history.LifecycleState != "deleted" || history.VersionNo != requested.VersionNo+2 || history.VersionNo != completed.VersionNo {
		t.Fatal("来源过期后仍应返回原归属的完成事实")
	}
	call("DELETE", path, key, f.OtherKey, body, 404)
	call("DELETE", path, key, f.Token, body, 404)
	if f.ProviderCalls() != 1 {
		t.Fatal("导入及清理不得重新生成来源图片")
	}
	if financialSnapshot() != beforeFinance {
		t.Fatal("导入清理与回执不能改写或新建财务、用量、Outbox事实")
	}
}
