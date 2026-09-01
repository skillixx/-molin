package service_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 实际HTTP取消不调用Provider；两路径共用命令键，JWT与不同Key不能接管原任务。
func TestVideoG6TaskCancelHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	create := func(c service.VideoCaller, key string) *service.VideoHTTPGeneration {
		t.Helper()
		job, err := f.App.Create(context.Background(), service.VideoCommand{Caller: c, IdempotencyKey: key, Model: f.Model, Prompt: "仅用于本地取消HTTP验证", Operation: model.AIVideoOperationTextToVideo})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	job := create(caller, "g6-cancel-http-command")
	path := "/api/token/video-tasks/" + job.Job.ID
	alias := "/api/token/video-tasks/by-video/" + job.Job.ID
	call := func(path, credential, key, body string, want int) *service.VideoCancellationReply {
		t.Helper()
		r, err := http.NewRequest("DELETE", server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+credential)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil || resp.StatusCode != want {
			t.Fatalf("取消HTTP应为%d，实际%d", want, resp.StatusCode)
		}
		if want >= 400 {
			if envelope.Code == 0 {
				t.Fatal("错误不能返回成功code")
			}
			return nil
		}
		var fields map[string]json.RawMessage
		var result service.VideoCancellationReply
		if envelope.Code != 0 || json.Unmarshal(envelope.Data, &fields) != nil || len(fields) != 28 || json.Unmarshal(envelope.Data, &result) != nil {
			t.Fatal("取消DTO必须为28个低敏字段")
		}
		for _, key := range []string{"task_id", "video_id", "request_id", "quote_id", "model", "operation", "execution_status", "billing_status", "delivery_status", "progress", "version_no", "request_version_no", "quoted_amount", "held_amount", "current_frozen_amount", "settled_amount", "net_released_amount", "hold_status", "currency", "created_at", "completed_at", "media_deleted", "media_partially_deleted", "media_deletion_pending", "can_deliver", "cancel_requested_at", "cancellation_result", "idempotent"} {
			if _, ok := fields[key]; !ok {
				t.Fatalf("取消响应缺少必需字段：%s", key)
			}
		}
		if string(fields["media_partially_deleted"]) != "false" || string(fields["media_deletion_pending"]) != "false" {
			t.Fatal("取消不能伪造媒体删除意图或删除完成")
		}
		if resp.Header.Get("X-Molin-Request-ID") != result.RequestID || resp.Header.Get("X-Request-ID") == "" {
			t.Fatal("取消HTTP与原业务追踪不能混淆")
		}
		return &result
	}
	call(path, f.OtherKey, "g6-cancel-http-command", "", 404)
	call(path, f.Token, "g6-cancel-http-command", "", 404)
	for _, body := range []string{"{}", " ", `{"settled_amount":"0"}`, `{"user_id":1}`} {
		call(path, f.Key, "g6-cancel-http-command", body, 400)
	}
	call(path+"?project_id=1", f.Key, "g6-cancel-http-command", "", 400)
	call(path, f.Key, "", "", 400)
	first := call(path, f.Key, "g6-cancel-http-command", "", 200)
	if first.Idempotent || first.CancellationResult != "cancelled" || first.CancelRequestedAt == nil || first.BillingStatus != "released" || first.SettledAmount == nil || *first.SettledAmount != "0.00000000" {
		t.Fatal("未提交取消应真实释放且不标重放")
	}
	replay := call(alias, f.Key, "g6-cancel-http-command", "", 200)
	if !replay.Idempotent || replay.RequestID != job.RequestID || !replay.CancelRequestedAt.Equal(*first.CancelRequestedAt) {
		t.Fatal("别名重放必须保留原命令和取消时刻")
	}
	// 先真实执行并完成终态夹具，释放user=1运行名额；不能在后续submitted任务占位时绕过容量门禁。
	completed := f.CreateCompletedForKey(f.ProjectID)
	second := create(caller, "g6-cancel-http-second")
	call("/api/token/video-tasks/"+second.Job.ID, f.Key, "g6-cancel-http-command", "", 409)
	// 已提交任务只记取消意图，资金与三轴不得因为用户愿望而改成释放终态。
	f.Submit(second.Job.ID)
	beforeSubmits := f.SubmitCalls()
	pending := call("/api/token/video-tasks/by-video/"+second.Job.ID, f.Key, "g6-cancel-submitted-command", "", 202)
	if pending.CancellationResult != "cancel_requested" || pending.ExecutionStatus != "submitted" || pending.BillingStatus != "held" || pending.DeliveryStatus != "pending" || pending.CurrentFrozenAmount == nil || *pending.CurrentFrozenAmount != "0.50000000" || pending.SettledAmount != nil {
		t.Fatal("已提交取消不能假称已退款")
	}
	if f.SubmitCalls() != beforeSubmits {
		t.Fatal("HTTP取消不得提交或重提Provider")
	}
	if err := f.DB.Exec("UPDATE api_keys SET video_generate_allowed=0 WHERE id=?", f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	call(alias, f.Key, "g6-cancel-http-command", "", 403)
	if err := f.DB.Exec("UPDATE api_keys SET video_generate_allowed=1 WHERE id=?", f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	jwtJob := create(service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, "g6-cancel-jwt-create")
	jwt := call("/api/token/video-tasks/"+jwtJob.Job.ID, f.Token, "g6-cancel-jwt-command", "", 200)
	if jwt.CancellationResult != "cancelled" {
		t.Fatal("JWT应能取消自己的无Key任务")
	}
	terminal := call("/api/token/video-tasks/"+completed, f.Key, "g6-cancel-terminal-command", "", 200)
	if terminal.CancellationResult != "already_terminal" || terminal.CancelRequestedAt != nil || terminal.ExecutionStatus != "succeeded" || terminal.BillingStatus != "settled" {
		t.Fatal("已成功任务只能明确无操作，不能取消或退款")
	}
	var receipts int64
	if err := f.DB.Table("ai_video_cancellation_commands").Where("user_id=?", f.ProjectID).Count(&receipts).Error; err != nil || receipts != 4 {
		t.Fatalf("拒绝和重放不能新增命令：%d %v", receipts, err)
	}
	if f.ProviderCalls() != 1 {
		t.Fatal("取消不应重新生成来源图片")
	}
}
