package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
)

type archiveCountingContent struct {
	service.VideoAdminArchiveContent
	opens atomic.Int32
}

func (p *archiveCountingContent) OpenContent(ctx context.Context, r video.ControlledContentRef) (video.StreamContent, error) {
	p.opens.Add(1)
	return p.VideoAdminArchiveContent.OpenContent(ctx, r)
}

func TestVideoG6AdminArchiveHTTPMySQL(t *testing.T) {
	for _, i2v := range []bool{false, true} {
		name := "t2v"
		if i2v {
			name = "i2v"
		}
		t.Run(name, func(t *testing.T) {
			f := newAdminCancelErrorFixture(t)
			if i2v {
				adminCancelI2VFixture(t, &f)
			}
			f.f.PrepareArchive(f.task)
			owner := repository.VideoOwner{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: &f.f.ProjectID}
			repo := repository.NewVideoTaskRepository(f.f.DB)
			task, err := repo.FindForOwner(context.Background(), f.task, owner)
			if err != nil {
				t.Fatal(err)
			}
			o := f.f.ArchiveOptions()
			counter := &archiveCountingContent{VideoAdminArchiveContent: o.Content}
			o.Content = counter
			p, err := service.NewVideoAdminReasonProtector("g6-archive-http-v1", f.secret)
			if err != nil {
				t.Fatal(err)
			}
			app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p, Archive: &o})
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			gateway.RegisterVideoAdminRoutes(mux, app, f.f.JWT, true)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			call := func(key, token string, version uint64, want int) *service.VideoAdminPollReply {
				t.Helper()
				body, _ := json.Marshal(map[string]any{"reason": "合成归档管理执行", "version_no": version})
				r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-tasks/"+f.task+"/archive-retry", bytes.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", key)
				if token != "" {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				resp, err := srv.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode != want {
					t.Fatalf("归档HTTP应%d实际%d", want, resp.StatusCode)
				}
				var e struct {
					Code int             `json:"code"`
					Data json.RawMessage `json:"data"`
				}
				if json.Unmarshal(raw, &e) != nil {
					t.Fatal("返回无效JSON")
				}
				if want != 200 {
					if string(e.Data) != "null" {
						t.Fatal("错误不得返回部分结果")
					}
					return nil
				}
				var reply service.VideoAdminPollReply
				var fields map[string]json.RawMessage
				if e.Code != 0 || json.Unmarshal(e.Data, &reply) != nil || json.Unmarshal(e.Data, &fields) != nil || len(fields) != 7 || reply.TaskID != f.task || reply.RequestID != f.requestID || resp.Header.Get("X-Molin-Request-ID") != f.requestID {
					t.Fatal("归档返回原Task七字段低敏回执")
				}
				if bytes.Contains(raw, []byte("合成归档管理执行")) || bytes.Contains(raw, []byte("provider_task_id")) || bytes.Contains(raw, []byte("archive_token")) {
					t.Fatal("不泄露原因/Provider/围栏令牌")
				}
				return &reply
			}
			call("g6-admin-archive-main", "", task.VersionNo, 401)
			call("g6-admin-archive-main", f.f.Key, task.VersionNo, 401)
			call("g6-admin-archive-stale", f.token, task.VersionNo+1, 409)
			if counter.opens.Load() != 0 {
				t.Fatal("未授权或过期版本不能读取Provider媒体")
			}
			if err := f.f.DB.Table("users").Where("id=?", f.f.ProjectID).Update("status", "disabled").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.f.DB.Table("api_keys").Where("id=?", f.f.ProjectID).Update("status", "revoked").Error; err != nil {
				t.Fatal(err)
			}
			versionDelta := uint64(4)
			wantCommands, wantAudits := int64(1), int64(2)
			if i2v {
				fundsBefore := f.f.FinancialSnapshot()
				f.f.FailHead(true)
				failed := call("g6-admin-archive-head-failure", f.token, task.VersionNo, 200)
				if failed.Status != "unknown" || failed.ExecutionStatus != "pending_reconcile" {
					t.Fatal("媒体确认失败须持久化待核对命令，不伪造业务失败或成功")
				}
				var oldFunds, newFunds map[string][]string
				if json.Unmarshal(fundsBefore, &oldFunds) != nil || json.Unmarshal(f.f.FinancialSnapshot(), &newFunds) != nil {
					t.Fatal("资金快照无效")
				}
				for _, table := range []string{"wallets", "wallet_holds", "wallet_transactions", "ai_gateway_quotes", "ai_usage_items", "ai_request_wallet_links"} {
					if !reflect.DeepEqual(oldFunds[table], newFunds[table]) {
						t.Fatal("失败收口不得修改资金、原报价或计量事实")
					}
				}
				opens := counter.opens.Load()
				if r := call("g6-admin-archive-head-failure", f.token, task.VersionNo, 200); !r.Idempotent || r.Status != "unknown" || counter.opens.Load() != opens {
					t.Fatal("失败回执重放也不能重新媒体IO")
				}
				f.f.FailHead(false)
				task.VersionNo = failed.VersionNo
				versionDelta, wantCommands, wantAudits = 1, 2, 4
			}
			before := f.f.FinancialSnapshot()
			submits := f.f.SubmitCalls()
			first := call("g6-admin-archive-main", f.token, task.VersionNo, 200)
			if first.Status != "completed" || first.ExecutionStatus != "succeeded" || first.Idempotent || counter.opens.Load() == 0 {
				t.Fatal("必须实际归档而非空操作返回成功")
			}
			if !adminOnlyExecutionChanged(t, before, f.f.FinancialSnapshot(), f.requestID, "succeeded", versionDelta) {
				t.Fatal("归档HTTP不得改变原资金事实")
			}
			calls := counter.opens.Load()
			replay := call("g6-admin-archive-main", f.token, task.VersionNo, 200)
			if !replay.Idempotent || replay.CommandID != first.CommandID || replay.VersionNo != first.VersionNo || counter.opens.Load() != calls {
				t.Fatal("同key只读取原完成事实，不重新做媒体IO")
			}
			var commands, audits int64
			if err := f.f.DB.Table("ai_video_admin_archive_commands").Where("actor_user_id=? AND status='completed'", f.actor).Count(&commands).Error; err != nil || commands != 1 {
				t.Fatal("只能有一个完成管理命令")
			}
			if err := f.f.DB.Table("ai_video_admin_archive_commands").Where("actor_user_id=?", f.actor).Count(&commands).Error; err != nil || commands != wantCommands {
				t.Fatal("历史未知命令必须保留，不得覆盖或删除")
			}
			if err := f.f.DB.Table("audit_logs").Where("operator_id=?", f.actor).Count(&audits).Error; err != nil || audits != wantAudits {
				t.Fatal("命令必须有唯一前后审计")
			}
			if f.f.SubmitCalls() != submits {
				t.Fatal("归档不能新增Provider提交")
			}
		})
	}
}
