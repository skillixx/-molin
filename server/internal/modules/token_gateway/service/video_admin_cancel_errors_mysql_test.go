package service_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
)

type adminCancelErrorFixture struct {
	f           service.VideoContentHTTPFixture
	actor       uint64
	token, task string
	requestID   string
	version     uint64
	secret      []byte
}

func newAdminCancelErrorFixture(t *testing.T) adminCancelErrorFixture {
	t.Helper()
	f := service.NewVideoContentHTTPFixture(t)
	job, err := f.App.Create(context.Background(), service.VideoCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: "g6-admin-negative-create", Model: f.Model, Prompt: "仅用于管理错误恢复", Operation: "text_to_video"})
	if err != nil {
		t.Fatal(err)
	}
	var version uint64
	if err := f.DB.Table("ai_gateway_tasks").Select("version_no").Where("public_id=?", job.Job.ID).Scan(&version).Error; err != nil {
		t.Fatal(err)
	}
	verified := time.Now().UTC().Add(-time.Minute)
	actor := authmodel.User{ID: service.NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := f.DB.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:task_manage'", actor.ID).Error; err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	return adminCancelErrorFixture{f: f, actor: actor.ID, token: f.TokenForUser(actor.ID), task: job.Job.ID, requestID: job.RequestID, version: version, secret: secret}
}

func (f adminCancelErrorFixture) server(t *testing.T, version string, secret []byte) *httptest.Server {
	t.Helper()
	p, err := service.NewVideoAdminReasonProtector(version, secret)
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, f.f.JWT, true)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (f adminCancelErrorFixture) call(t *testing.T, srv *httptest.Server, body []byte, want int) *service.VideoAdminCancellationReply {
	t.Helper()
	r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-tasks/"+f.task+"/cancel", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Idempotency-Key", "g6-admin-negative-command")
	client := srv.Client()
	client.Timeout = 35 * time.Second
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
		t.Fatalf("负向合同应HTTP%d，实际%d", want, resp.StatusCode)
	}
	wantCode := 0
	if want == 400 {
		wantCode = 40000
	} else if want == 503 {
		wantCode = 50300
	}
	if envelope.Code != wantCode {
		t.Fatalf("平台错误码应%d实际%d", wantCode, envelope.Code)
	}
	if want >= 400 {
		if string(envelope.Data) != "null" {
			t.Fatal("错误不得返回部分结果")
		}
		return nil
	}
	var reply service.VideoAdminCancellationReply
	if json.Unmarshal(envelope.Data, &reply) != nil || reply.VideoAdminTaskDetails == nil || reply.VideoTaskDetails == nil || reply.TaskID != f.task || reply.RequestID != f.requestID {
		t.Fatal("取消回复必须关联原Task和Request")
	}
	return &reply
}

func (f adminCancelErrorFixture) counts(t *testing.T) (int64, int64) {
	t.Helper()
	var commands, audits int64
	if err := f.f.DB.Table("ai_video_admin_cancellation_commands").Where("actor_user_id=?", f.actor).Count(&commands).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Table("audit_logs").Where("operator_id=?", f.actor).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	return commands, audits
}

func TestVideoG6AdminCancelInvalidUTF8MySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	srv := f.server(t, "g6-admin-negative-v1", f.secret)
	before := f.f.FinancialSnapshot()
	body := append([]byte(`{"reason":"`), 0xff)
	body = append(body, []byte(fmt.Sprintf(`","version_no":%d}`, f.version))...)
	f.call(t, srv, body, 400)
	commands, audits := f.counts(t)
	if commands != 0 || audits != 0 || !bytes.Equal(before, f.f.FinancialSnapshot()) {
		t.Fatal("非法原始UTF-8必须在写入任何取消事实前拒绝")
	}
}

func TestVideoG6AdminCancelReasonKeyChangeMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	srv := f.server(t, "g6-admin-negative-v1", f.secret)
	body := []byte(fmt.Sprintf(`{"reason":"合成密钥轮换审阅","version_no":%d}`, f.version))
	f.call(t, srv, body, 200)
	before := f.f.FinancialSnapshot()
	other := make([]byte, 32)
	if _, err := rand.Read(other); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, version string
		key           []byte
	}{{"仅变版本", "g6-admin-negative-v2", f.secret}, {"版本与密钥均变", "g6-admin-negative-v2", other}, {"同版本错误密钥", "g6-admin-negative-v1", other}} {
		t.Run(tc.name, func(t *testing.T) { f.call(t, f.server(t, tc.version, tc.key), body, 503) })
	}
	commands, audits := f.counts(t)
	if commands != 1 || audits != 2 || !bytes.Equal(before, f.f.FinancialSnapshot()) {
		t.Fatal("原因密钥不可用不能建立新命令或二次退款")
	}
}

func TestVideoG6AdminCancelAuditReadRetryMySQL(t *testing.T) {
	for _, number := range []uint16{1213, 1205} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			f := newAdminCancelErrorFixture(t)
			srv := f.server(t, "g6-admin-negative-v1", f.secret)
			var mu sync.Mutex
			calls := 0
			transactions := map[gorm.ConnPool]bool{}
			const hook = "g6-admin-audit-read-error"
			// 只在数据库错误边界注入驱动错误；业务事务、回滚、退款和重试仍由真实MySQL与原服务执行。
			if err := f.f.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
				if tx.Statement.Table != "audit_logs" {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				calls++
				transactions[tx.Statement.ConnPool] = true
				if calls == 1 {
					tx.AddError(&mysqldriver.MySQLError{Number: number, Message: "合成数据库读取错误"})
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { f.f.DB.Callback().Query().Remove(hook) })
			body := []byte(fmt.Sprintf(`{"reason":"合成完整事务重试","version_no":%d}`, f.version))
			f.call(t, srv, body, 200)
			f.f.DB.Callback().Query().Remove(hook)
			mu.Lock()
			count, roots := calls, len(transactions)
			actualSQLTransactions := true
			for root := range transactions {
				if _, ok := root.(*sql.Tx); !ok {
					actualSQLTransactions = false
				}
			}
			mu.Unlock()
			if count != 3 || roots != 2 || !actualSQLTransactions {
				t.Fatalf("必须重启完整事务再核验两条审计：reads=%d roots=%d", count, roots)
			}
			commands, audits := f.counts(t)
			if commands != 1 || audits != 2 {
				t.Fatal("重试后只能一份回执与前后审计")
			}
			var unfreezes int64
			if err := f.f.DB.Table("wallet_transactions").Where("user_id=? AND type='unfreeze'", f.f.ProjectID).Count(&unfreezes).Error; err != nil || unfreezes != 2 {
				t.Fatal("含原图片结算和视频取消各一笔解冻，不得重复退款")
			}
		})
	}
}
