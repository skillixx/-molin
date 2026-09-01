package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/shopspring/decimal"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
)

// 仅在外部Query边界计数/注入超时，实际异步Provider状态与全部数据库服务保持真实实现。
type adminPollProvider struct {
	service.VideoAdminPollProvider
	calls           atomic.Int32
	timeout         atomic.Bool
	explicitFailure atomic.Bool
	afterQuery      func()
	afterOnce       sync.Once
}

func (p *adminPollProvider) Query(ctx context.Context, r video.QueryRequest) (video.QueryResult, error) {
	p.calls.Add(1)
	if p.timeout.Swap(false) {
		return video.QueryResult{}, video.ErrProviderTimeout
	}
	result, err := p.VideoAdminPollProvider.Query(ctx, r)
	if p.afterQuery != nil {
		p.afterOnce.Do(p.afterQuery)
	}
	if p.explicitFailure.Swap(false) && err == nil && result.Confirmation != nil {
		// 在受控Provider边界构造晚到的明确失败观察，保持原绑定，不伪造业务成功。
		c := *result.Confirmation
		c.Outcome = video.ProviderTaskFailed
		c.Quantity, c.UnitPrice, c.Amount = decimal.Zero, decimal.Zero, decimal.Zero
		c.ExternalEventID = "failure-" + r.ProviderTaskID
		result.Status, result.Content, result.Confirmation = video.ProviderTaskFailed, nil, &c
		return result, video.ErrProviderExplicitFailure
	}
	return result, err
}

type videoPollCommitPool struct {
	gorm.ConnPool
	lost atomic.Bool
}

func (p *videoPollCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoPollCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoPollCommitTx struct {
	gorm.ConnPool
	tx          *sql.Tx
	pool        *videoPollCommitPool
	resultWrite bool
}

func (t *videoPollCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	lower := strings.ToLower(query)
	if strings.Contains(lower, "ai_video_admin_poll_commands") && strings.Contains(lower, "update") {
		t.resultWrite = true
	}
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *videoPollCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.resultWrite && t.pool.lost.CompareAndSwap(false, true) {
		return errors.New("合成poll结果COMMIT确认丢失")
	}
	return nil
}
func (t *videoPollCommitTx) Rollback() error { return t.tx.Rollback() }

// G3执行推进会同步原Request粗执行态/版本/更新时间；其余请求及全部资金字段必须逐字保持。
func adminPollOnlyExecutionChanged(t *testing.T, before, after []byte, requestID string) bool {
	return adminOnlyExecutionChanged(t, before, after, requestID, "running", 1)
}

func adminOnlyExecutionChanged(t *testing.T, before, after []byte, requestID, expectedStatus string, versionDelta uint64) bool {
	t.Helper()
	var a, b map[string][]string
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil {
		t.Fatal("财务快照格式异常")
	}
	var original map[string]json.RawMessage
	for _, raw := range a["ai_requests"] {
		var row map[string]json.RawMessage
		json.Unmarshal([]byte(raw), &row)
		var id string
		json.Unmarshal(row["request_id"], &id)
		if id == requestID {
			original = row
		}
	}
	if original == nil {
		t.Fatal("缺少原请求")
	}
	found := false
	for i, raw := range b["ai_requests"] {
		var row map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &row) != nil {
			t.Fatal("请求快照无效")
		}
		var id string
		json.Unmarshal(row["request_id"], &id)
		if id != requestID {
			continue
		}
		found = true
		var oldVersion, newVersion uint64
		var execution string
		if json.Unmarshal(original["version_no"], &oldVersion) != nil || json.Unmarshal(row["version_no"], &newVersion) != nil || newVersion != oldVersion+versionDelta || json.Unmarshal(row["execution_status"], &execution) != nil || execution != expectedStatus {
			t.Fatal("必须只有本次G3推进对应的原Request版本和粗执行态")
		}
		for _, key := range []string{"execution_status", "version_no", "updated_at"} {
			row[key] = original[key]
		}
		normalized, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		b["ai_requests"][i] = string(normalized)
	}
	if !found {
		t.Fatal("原请求不得丢失")
	}
	sort.Strings(b["ai_requests"])
	normalized, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(before, normalized)
}

func TestVideoG6AdminPollHTTPMySQL(t *testing.T) {
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
			f.f.Submit(f.task)
			if err := f.f.DB.Table("ai_gateway_tasks").Select("version_no").Where("public_id=?", f.task).Scan(&f.version).Error; err != nil {
				t.Fatal(err)
			}
			p, err := service.NewVideoAdminReasonProtector("g6-poll-test-v1", f.secret)
			if err != nil {
				t.Fatal(err)
			}
			provider := &adminPollProvider{VideoAdminPollProvider: f.f.AdminPollProvider}
			app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p, PollProvider: provider})
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			gateway.RegisterVideoAdminRoutes(mux, app, f.f.JWT, true)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			call := func(key, token string, version uint64, want int) *service.VideoAdminPollReply {
				t.Helper()
				body, _ := json.Marshal(map[string]any{"reason": "合成原任务管理查询", "version_no": version})
				r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-tasks/"+f.task+"/poll", bytes.NewReader(body))
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
					t.Fatalf("管理轮询应%d实际%d", want, resp.StatusCode)
				}
				var e struct {
					Code int             `json:"code"`
					Data json.RawMessage `json:"data"`
				}
				if json.Unmarshal(raw, &e) != nil {
					t.Fatal("响应无效")
				}
				if want != 200 {
					return nil
				}
				var reply service.VideoAdminPollReply
				var fields map[string]json.RawMessage
				if e.Code != 0 || json.Unmarshal(e.Data, &reply) != nil || json.Unmarshal(e.Data, &fields) != nil || len(fields) != 7 || reply.TaskID != f.task || reply.RequestID != f.requestID || resp.Header.Get("X-Molin-Request-ID") != f.requestID {
					t.Fatal("必须返回原任务七字段操作回执")
				}
				if bytes.Contains(raw, []byte("合成原任务管理查询")) || bytes.Contains(raw, []byte("provider_task_id")) || bytes.Contains(raw, []byte("prompt")) {
					t.Fatal("不得泄露原因或敏感执行载荷")
				}
				return &reply
			}
			call("g6-admin-poll-first", "", f.version, 401)
			call("g6-admin-poll-first", f.f.Key, f.version, 401)
			// 原主体失去生成资格后仍有管理追踪义务；I2V不能因此重新读取参考图或冒充原用户。
			if err := f.f.DB.Table("users").Where("id=?", f.f.ProjectID).Update("status", "disabled").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.f.DB.Table("api_keys").Where("id=?", f.f.ProjectID).Update("status", "revoked").Error; err != nil {
				t.Fatal(err)
			}
			submits := f.f.SubmitCalls()
			before := f.f.FinancialSnapshot()
			first := call("g6-admin-poll-first", f.token, f.version, 200)
			if first.ExecutionStatus != "processing" || first.Idempotent || provider.calls.Load() != 1 {
				t.Fatal("应只查询一次原Provider并推进processing")
			}
			if !adminPollOnlyExecutionChanged(t, before, f.f.FinancialSnapshot(), f.requestID) {
				t.Fatal("处理中观察不得提前结算或收费")
			}
			replay := call("g6-admin-poll-first", f.token, f.version, 200)
			if !replay.Idempotent || replay.CommandID != first.CommandID || provider.calls.Load() != 1 {
				t.Fatal("同键重放不得再次外部Query")
			}
			provider.timeout.Store(true)
			unknown := call("g6-admin-poll-timeout", f.token, first.VersionNo, 200)
			if unknown.ExecutionStatus != "pending_reconcile" {
				t.Fatal("未知查询应保护待核对状态")
			}
			observed := call("g6-admin-poll-pending", f.token, unknown.VersionNo, 200)
			if observed.ExecutionStatus != "pending_reconcile" || provider.calls.Load() != 3 {
				t.Fatal("待对账仍须查询原任务，不能noop或回退fetching")
			}
			if f.f.SubmitCalls() != submits {
				t.Fatal("管理恢复不得重新Submit")
			}
			provider.explicitFailure.Store(true)
			late := call("g6-poll-explicit-failure", f.token, observed.VersionNo, 200)
			if late.ExecutionStatus != "pending_reconcile" || late.Status != "unknown" {
				t.Fatal("明确失败观察不得回退待核对状态")
			}
			var conflicts int64
			if err := f.f.DB.Table("ai_gateway_task_events").Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND event_type='provider_result_conflict'", f.task).Count(&conflicts).Error; err != nil || conflicts != 1 {
				t.Fatal("必须保存晚到明确失败的低敏冲突观察，不能因error丢弃")
			}
			if r := call("g6-poll-explicit-failure", f.token, observed.VersionNo, 200); !r.Idempotent || provider.calls.Load() != 4 {
				t.Fatal("明确失败重放不能重复外部Query")
			}
			var commands, audits int64
			if err := f.f.DB.Table("ai_video_admin_poll_commands").Where("actor_user_id=?", f.actor).Count(&commands).Error; err != nil || commands != 4 {
				t.Fatal("应只有四个主动命令")
			}
			if err := f.f.DB.Table("audit_logs").Where("operator_id=?", f.actor).Count(&audits).Error; err != nil || audits != 8 {
				t.Fatal("每个命令须唯一前后审计")
			}
		})
	}
}

type adminPollRecoveryFixture struct {
	base     adminCancelErrorFixture
	app      *service.VideoAdminService
	provider *adminPollProvider
	caller   service.VideoCaller
}

func newAdminPollRecoveryFixture(t *testing.T) adminPollRecoveryFixture {
	t.Helper()
	f := newAdminCancelErrorFixture(t)
	f.f.Submit(f.task)
	if err := f.f.DB.Table("ai_gateway_tasks").Select("version_no").Where("public_id=?", f.task).Scan(&f.version).Error; err != nil {
		t.Fatal(err)
	}
	protector, err := service.NewVideoAdminReasonProtector("g6-poll-recovery-v1", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	provider := &adminPollProvider{VideoAdminPollProvider: f.f.AdminPollProvider}
	app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: protector, PollProvider: provider})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := f.f.JWT.Authenticate(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	return adminPollRecoveryFixture{base: f, app: app, provider: provider, caller: caller}
}

func (f adminPollRecoveryFixture) command(key string) service.VideoAdminPollCommand {
	return service.VideoAdminPollCommand{Caller: f.caller, TaskID: f.base.task, VersionNo: f.base.version, IdempotencyKey: key, Reason: "合成管理轮询恢复"}
}

func (f adminPollRecoveryFixture) snapshot(t *testing.T) []byte {
	t.Helper()
	facts := map[string]any{"finance": json.RawMessage(f.base.f.FinancialSnapshot())}
	for _, table := range []string{"ai_gateway_tasks", "ai_gateway_task_events", "ai_video_admin_poll_commands", "audit_logs"} {
		query := f.base.f.DB.Table(table)
		switch table {
		case "ai_gateway_tasks", "ai_gateway_task_events":
			query = query.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", f.base.task)
			if table == "ai_gateway_tasks" {
				query = f.base.f.DB.Table(table).Where("public_id=?", f.base.task)
			}
		case "ai_video_admin_poll_commands":
			query = query.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", f.base.task)
		case "audit_logs":
			query = query.Where("operator_id=? AND action LIKE 'video_admin_poll_%'", f.base.actor)
		}
		var rows []map[string]any
		if err := query.Order("id").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		facts[table] = rows
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVideoG6AdminPollConcurrentMySQL(t *testing.T) {
	f := newAdminPollRecoveryFixture(t)
	command := f.command("g6-admin-poll-concurrent")
	start := make(chan struct{})
	type answer struct {
		reply *service.VideoAdminPollReply
		err   error
	}
	answers := make(chan answer, 100)
	var wg sync.WaitGroup
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reply, err := f.app.PollTask(context.Background(), command)
			answers <- answer{reply: reply, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(answers)
	commandID := ""
	var first int
	for answer := range answers {
		if answer.err != nil || answer.reply == nil || (answer.reply.Status != "running" && answer.reply.Status != "completed") {
			t.Fatalf("同键并发轮询失败：reply=%+v err=%v", answer.reply, answer.err)
		}
		if commandID == "" {
			commandID = answer.reply.CommandID
		} else if commandID != answer.reply.CommandID {
			t.Fatal("同键并发产生不同轮询命令")
		}
		if !answer.reply.Idempotent {
			first++
		}
	}
	if first != 1 || f.provider.calls.Load() != 1 {
		t.Fatalf("100同键必须一次命令和一次Query：first=%d query=%d", first, f.provider.calls.Load())
	}
	var commands, audits int64
	_ = f.base.f.DB.Table("ai_video_admin_poll_commands").Where("actor_user_id=?", f.base.actor).Count(&commands).Error
	_ = f.base.f.DB.Table("audit_logs").Where("operator_id=? AND action LIKE 'video_admin_poll_%'", f.base.actor).Count(&audits).Error
	if commands != 1 || audits != 2 {
		t.Fatalf("并发轮询事实必须唯一：commands=%d audits=%d", commands, audits)
	}
}

func TestVideoG6AdminPollCommitUnknownMySQL(t *testing.T) {
	f := newAdminPollRecoveryFixture(t)
	pool := &videoPollCommitPool{ConnPool: f.base.f.DB.ConnPool}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.base.f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	restore := f.base.f.UseApplicationDB(db)
	command := f.command("g6-admin-poll-commit-unknown")
	result, commitErr := f.app.PollTask(context.Background(), command)
	if result != nil || commitErr == nil || !pool.lost.Load() || f.provider.calls.Load() != 1 {
		t.Fatalf("轮询结果必须真实提交后丢失确认：result=%+v err=%v lost=%t query=%d", result, commitErr, pool.lost.Load(), f.provider.calls.Load())
	}
	afterUnknown := f.snapshot(t)
	replay, err := f.app.PollTask(context.Background(), command)
	restore()
	if err != nil || replay == nil || !replay.Idempotent || replay.Status != "completed" || f.provider.calls.Load() != 1 {
		t.Fatalf("轮询提交未知重放必须恢复原回执：reply=%+v err=%v query=%d", replay, err, f.provider.calls.Load())
	}
	if !bytes.Equal(afterUnknown, f.snapshot(t)) {
		t.Fatal("轮询提交未知重放不得重复观察、事件或审计")
	}
}

func TestVideoG6AdminPollPostQueryRevocationMySQL(t *testing.T) {
	f := newAdminPollRecoveryFixture(t)
	f.provider.afterQuery = func() {
		_ = f.base.f.DB.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='ai_gateway:task_manage'", f.base.actor).Error
	}
	command := f.command("g6-admin-poll-post-query-revoke")
	result, err := f.app.PollTask(context.Background(), command)
	if result != nil || !errors.Is(err, service.ErrVideoAdminForbidden) || f.provider.calls.Load() != 1 {
		t.Fatalf("Query后撤权必须失败且不重复Query：result=%+v err=%v calls=%d", result, err, f.provider.calls.Load())
	}
	var taskStatus, commandStatus string
	_ = f.base.f.DB.Table("ai_gateway_tasks").Select("status").Where("public_id=?", f.base.task).Scan(&taskStatus).Error
	_ = f.base.f.DB.Table("ai_video_admin_poll_commands").Select("status").Where("actor_user_id=?", f.base.actor).Scan(&commandStatus).Error
	if taskStatus != "submitted" || commandStatus != "unknown" {
		t.Fatalf("撤权后只能善后命令，不推进Task：task=%s command=%s", taskStatus, commandStatus)
	}
	if err := f.base.f.DB.Exec("UPDATE user_permission_overrides SET effect='allow' WHERE user_id=? AND permission_code='ai_gateway:task_manage'", f.base.actor).Error; err != nil {
		t.Fatal(err)
	}
	replay, err := f.app.PollTask(context.Background(), command)
	if err != nil || replay == nil || !replay.Idempotent || replay.Status != "unknown" || f.provider.calls.Load() != 1 {
		t.Fatalf("恢复权限后只读原unknown回执：reply=%+v err=%v calls=%d", replay, err, f.provider.calls.Load())
	}
}

func TestVideoG6AdminPollMFAExpiryMySQL(t *testing.T) {
	f := newAdminPollRecoveryFixture(t)
	expired := time.Now().UTC().Add(-25 * time.Hour)
	if err := f.base.f.DB.Exec("UPDATE users SET admin_phone_verified_at=?,admin_email_verified_at=? WHERE id=?", expired, expired, f.base.actor).Error; err != nil {
		t.Fatal(err)
	}
	result, err := f.app.PollTask(context.Background(), f.command("g6-admin-poll-mfa-expired"))
	if result != nil || !errors.Is(err, service.ErrVideoAdminMFA) || f.provider.calls.Load() != 0 {
		t.Fatalf("MFA过期必须在命令和Query前拒绝：result=%+v err=%v calls=%d", result, err, f.provider.calls.Load())
	}
	var commands, audits int64
	_ = f.base.f.DB.Table("ai_video_admin_poll_commands").Where("actor_user_id=?", f.base.actor).Count(&commands).Error
	_ = f.base.f.DB.Table("audit_logs").Where("operator_id=? AND action LIKE 'video_admin_poll_%'", f.base.actor).Count(&audits).Error
	if commands != 0 || audits != 0 {
		t.Fatalf("MFA过期不得写命令或审计：commands=%d audits=%d", commands, audits)
	}
}
