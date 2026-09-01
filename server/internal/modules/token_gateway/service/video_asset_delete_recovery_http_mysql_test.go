package service_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

// 对象已消失但确认未提交时仍表达待确认；恢复只处理原子资产，不动父、兄弟或生成财务。
func TestVideoG6MediaDeleteAssetRecoveryHTTPMySQL(t *testing.T) {
	for _, mode := range []string{"delete", "confirmation", "database", "jwt"} {
		t.Run(mode, func(t *testing.T) {
			f := service.NewVideoContentHTTPFixture(t)
			keyID, token := f.ProjectID, f.Key
			if mode == "jwt" {
				keyID = 0
				token = f.Token
			}
			id := f.CreateCompletedForKey(keyID)
			var thumb model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='thumbnail'", id).Take(&thumb).Error; err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
			s := httptest.NewServer(mux)
			defer s.Close()
			transport := &http.Transport{Proxy: nil}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
			call := func(method, path, body string, want int) []byte {
				t.Helper()
				req, err := http.NewRequest(method, s.URL+path, bytes.NewBufferString(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
				if method == "DELETE" {
					req.Header.Set("Idempotency-Key", "g6-asset-recover-"+mode)
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
			details := func() service.VideoTaskDetails {
				var result service.VideoTaskDetails
				decode(call("GET", "/api/token/videos/requests/by-video/"+id, "", 200), &result)
				return result
			}
			beforeDetail := details()
			if beforeDetail.MediaDeleted || beforeDetail.MediaPartiallyDeleted || beforeDetail.MediaDeletionPending || !beforeDetail.CanDeliver {
				t.Fatal("夹具必须从完整交付状态开始")
			}
			before := f.FinancialSnapshot()
			var injected atomic.Bool
			const hook = "g6_asset_delete_commit_write_failure"
			failureStatus := 503
			switch mode {
			case "delete", "jwt":
				f.FailMediaDelete(true)
			case "confirmation":
				f.FailMediaConfirmation(true)
			case "database":
				failureStatus = 500
				if err := f.DB.Callback().Update().After("gorm:update").Register(hook, func(tx *gorm.DB) {
					if tx.Statement.Table != "ai_video_asset_deletions" {
						return
					}
					if values, ok := tx.Statement.Dest.(map[string]any); ok && values["status"] == "completed" && injected.CompareAndSwap(false, true) {
						tx.AddError(errors.New("合成单资产完成写入失败"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				defer f.DB.Callback().Update().Remove(hook)
			}
			path := "/api/token/video-assets/" + thumb.PublicID
			body := fmt.Sprintf(`{"version_no":%d}`, thumb.VersionNo)
			call("DELETE", path, body, failureStatus)
			if mode == "database" && !injected.Load() {
				t.Fatal("数据库故障必须实际命中完成写入")
			}
			pending := details()
			if pending.MediaDeleted || pending.MediaPartiallyDeleted || !pending.MediaDeletionPending || pending.CanDeliver || pending.ExecutionStatus != "succeeded" || pending.BillingStatus != "settled" {
				t.Fatal("失败或确认未知不能冒充已删除或重新生成")
			}
			var life service.VideoAssetLifecycle
			decode(call("GET", path+"/lifecycle", "", 200), &life)
			wantState := "delete_failed"
			if mode == "database" {
				wantState = "deleting"
			}
			if life.LifecycleState != wantState || life.MediaDeleted || life.TaskMediaDeleted || life.DeletionStatus == nil || *life.DeletionStatus != wantState {
				t.Fatal("资产元数据必须反映未确认状态")
			}
			facts := f.InspectMedia(id)
			for role, fact := range facts {
				if role == "thumbnail" {
					if (mode == "delete" || mode == "jwt") != fact.Present {
						t.Fatal("物理对象状态必须与注入故障一致")
					}
				} else if !fact.Present || !fact.HashMatches {
					t.Fatal("单删故障不能损坏父或兄弟")
				}
			}
			if mode != "jwt" {
				call("GET", "/v1/videos/"+id, "", 404)
				call("GET", "/v1/videos/"+id+"/content", "", 404)
			}
			f.FailMediaDelete(false)
			f.FailMediaConfirmation(false)
			if mode == "database" {
				if err := f.DB.Callback().Update().Remove(hook); err != nil {
					t.Fatal(err)
				}
			}
			var reply service.VideoAssetDeleted
			decode(call("DELETE", path, body, 200), &reply)
			if !reply.Idempotent || !reply.MediaDeleted || reply.Scope != "asset" || reply.LifecycleState != "deleted" {
				t.Fatal("恢复必须返回原资产的确认完成")
			}
			after := details()
			if after.MediaDeleted || !after.MediaPartiallyDeleted || after.MediaDeletionPending || after.CanDeliver {
				t.Fatal("确认后才能标记部分删除")
			}
			wantCalls := int64(1)
			if mode == "delete" || mode == "jwt" {
				wantCalls = 2
			}
			if f.MediaDeleteCalls() != wantCalls {
				t.Fatal("已消失对象恢复不能重复Delete")
			}
			decode(call("DELETE", path, body, 200), &reply)
			if !reply.Idempotent || f.MediaDeleteCalls() != wantCalls || !bytes.Equal(before, f.FinancialSnapshot()) {
				t.Fatal("重放不得再次删除或改生成财务")
			}
		})
	}
}
