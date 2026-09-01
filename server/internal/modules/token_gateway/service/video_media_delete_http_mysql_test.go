package service_test

import (
	"context"
	"encoding/json"
	"errors"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 兼容DELETE删除媒体而非取消任务，成功后Job隐藏但平台原请求和账单必须保留。
func TestVideoG6MediaDeleteHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t, true)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	call := func(method, path, key, credential string, want int) []byte {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, nil)
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
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("媒体接口应%d，实际%d", want, resp.StatusCode)
		}
		return body
	}
	caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	running, err := f.App.Create(context.Background(), service.VideoCommand{Caller: caller, IdempotencyKey: "g6-media-delete-running", Model: f.Model, Prompt: "仅用于媒体删除合同验证", Operation: model.AIVideoOperationTextToVideo})
	if err != nil {
		t.Fatal(err)
	}
	call("DELETE", "/v1/videos/"+running.Job.ID, "g6-media-delete-command", f.Key, 409)
	call("DELETE", "/api/token/video-tasks/"+running.Job.ID, "g6-media-cancel-command", f.Key, 200)
	// 已取消无产物也属于公开failed；仅缺少资产不能自动证明失败闭合，需原G5对账。
	deleted := call("DELETE", "/v1/videos/"+running.Job.ID, "g6-media-delete-command", f.Key, 200)
	assertVideoDeleted := func(raw []byte, id string) {
		t.Helper()
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil || len(value) != 3 || value["id"] != id || value["object"] != "video.deleted" || value["deleted"] != true {
			t.Fatal("必须精确返回冻结VideoDeleted")
		}
	}
	assertVideoDeleted(deleted, running.Job.ID)
	call("GET", "/v1/videos/"+running.Job.ID, "", f.Key, 404)
	assertVideoDeleted(call("DELETE", "/v1/videos/"+running.Job.ID, "g6-media-delete-command", f.Key, 200), running.Job.ID)
	completed := f.CreateCompletedForKey(f.ProjectID)
	err = f.DB.Exec("INSERT INTO ai_video_media_deletions(task_id,user_id,project_id,api_key_id,request_id,status,version_no,plan_json,plan_sha256,created_at,completed_at) SELECT id,user_id,project_id,api_key_id,request_id,'completed',1,JSON_ARRAY(),SHA2('[]',256),UTC_TIMESTAMP(),UTC_TIMESTAMP() FROM ai_gateway_tasks WHERE public_id=?", completed).Error
	var sqlFailure *mysqlDriver.MySQLError
	if !errors.As(err, &sqlFailure) || sqlFailure.Number != 1644 {
		t.Fatalf("成功任务不能用空计划伪造完成：%v", err)
	}
	call("DELETE", "/v1/videos/"+completed, "g6-media-delete-command", f.Key, 409)
	call("DELETE", "/v1/videos/"+completed, "g6-media-delete-completed", f.OtherKey, 404)
	call("DELETE", "/v1/videos/"+completed, "g6-media-delete-completed", f.Token, 401)
	for role, fact := range f.InspectMedia(completed) {
		if !fact.Present || !fact.HashMatches || fact.Deleted {
			t.Fatalf("删除前必须存在原对象：%s", role)
		}
	}
	f.FailMediaConfirmation(true)
	call("DELETE", "/v1/videos/"+completed, "g6-media-delete-completed", f.Key, 503)
	call("GET", "/v1/videos/"+completed, "", f.Key, 404)
	f.FailMediaConfirmation(false)
	assertVideoDeleted(call("DELETE", "/v1/videos/"+completed, "g6-media-delete-completed", f.Key, 200), completed)
	facts := f.InspectMedia(completed)
	if len(facts) != 6 {
		t.Fatal("必须逐一核对完整六角色")
	}
	for role, fact := range facts {
		if role == "moderation_copy" {
			if !fact.Present || !fact.HashMatches || fact.Deleted {
				t.Fatal("审核副本必须保留原正文")
			}
		} else if fact.Present || !fact.Deleted {
			t.Fatalf("交付角色必须有实际删除墓碑：%s", role)
		}
	}
	if f.MediaDeleteCalls() != 5 {
		t.Fatal("恢复必须只删除剩余目标，不再次删除已确认的原目标")
	}
	call("GET", "/v1/videos/"+completed, "", f.Key, 404)
	call("GET", "/v1/videos/"+completed+"/content", "", f.Key, 404)
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(call("GET", "/v1/videos", "", f.Key, 200), &list) != nil || len(list.Data) != 0 {
		t.Fatal("两个删除Job都必须隐藏")
	}
	var detail struct {
		Data service.VideoTaskDetails `json:"data"`
	}
	if json.Unmarshal(call("GET", "/api/token/videos/requests/by-video/"+completed, "", f.Key, 200), &detail) != nil || !detail.Data.MediaDeleted || detail.Data.BillingStatus != "settled" || detail.Data.SettledAmount == nil || *detail.Data.SettledAmount != "0.50000000" {
		t.Fatal("媒体删除不能清除或改写原账单")
	}
}
