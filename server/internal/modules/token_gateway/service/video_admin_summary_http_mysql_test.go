package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminSummaryHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	verified := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	admin := authmodel.User{ID: service.NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := f.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:view'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(f.App, 24)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, f.JWT, true)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 30 * time.Second
	token := f.TokenForUser(admin.ID)
	path := "/api/admin/token/video-reconciliation/summary"
	call := func(query, body, credential string, status, code int) *service.VideoAdminReconciliationSummary {
		t.Helper()
		r, _ := http.NewRequest("GET", srv.URL+path+query, strings.NewReader(body))
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
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
		var e struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil || resp.StatusCode != status || e.Code != code {
			t.Fatalf("汇总应%d/%d，实际%d/%d", status, code, resp.StatusCode, e.Code)
		}
		if status != 200 {
			if string(e.Data) != "null" {
				t.Fatal("故障不能返回半份汇总")
			}
			return nil
		}
		var value service.VideoAdminReconciliationSummary
		var keys map[string]json.RawMessage
		if json.Unmarshal(e.Data, &value) != nil || json.Unmarshal(e.Data, &keys) != nil || len(keys) != 6 {
			t.Fatal("运行汇总必须固定六字段，不是D-95或对账PASS")
		}
		for _, name := range []string{"settlement_pending", "active_compensations", "dead_compensations", "outbox_pending", "outbox_dead", "unreleased_hold_amount"} {
			if _, ok := keys[name]; !ok {
				t.Fatalf("缺汇总字段%s", name)
			}
		}
		amount, err := decimal.NewFromString(value.UnreleasedHoldAmount)
		if err != nil || amount.IsNegative() || amount.StringFixed(8) != value.UnreleasedHoldAmount {
			t.Fatal("冻结金额必须非负八位Decimal字符串")
		}
		if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Molin-Request-ID") != "" {
			t.Fatal("全局汇总不可缓存或冒称单个业务请求")
		}
		return &value
	}
	call("", "", "", 401, 40001)
	call("", "", f.Key, 401, 40001)
	call("", "", f.Token, 403, 40003)
	call("", "", token, 403, 40003)
	// view不隐式授予财务核查权限；仅给本测试合成管理员增加已存在的独立权限码。
	if err := f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:reconcile_manage'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	before := call("", "", token, 200, 0)
	for _, query := range []string{"?page=1", "?user_id=1", "?request_id=unknown", "?unexpected=x"} {
		call(query, "", token, 400, 40000)
	}
	call("", "{}", token, 400, 40000)
	caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	job, err := f.App.Create(context.Background(), service.VideoCommand{Caller: caller, IdempotencyKey: "g6-admin-summary-create", Model: f.Model, Prompt: "仅用于运行事实汇总验证", Operation: "text_to_video"})
	if err != nil {
		t.Fatal(err)
	}
	finance := f.FinancialSnapshot()
	heads, submits := f.HeadCalls(), f.SubmitCalls()
	held := call("", "", token, 200, 0)
	baseAmount, _ := decimal.NewFromString(before.UnreleasedHoldAmount)
	if held.UnreleasedHoldAmount != baseAmount.Add(decimal.RequireFromString("0.50")).StringFixed(8) || held.OutboxPending != before.OutboxPending+1 || held.SettlementPending != before.SettlementPending || held.ActiveCompensations != before.ActiveCompensations || held.DeadCompensations != before.DeadCompensations || held.OutboxDead != before.OutboxDead {
		t.Fatal("真实预占只增加0.50冻结额与一条Outbox，不应被汇总执行或修复")
	}
	if !bytes.Equal(finance, f.FinancialSnapshot()) || heads != f.HeadCalls() || submits != f.SubmitCalls() {
		t.Fatal("读取不能触发原任务、资金或外部调用")
	}
	// 仅污染本测试原预占行，模拟历史未知状态；汇总必须拒绝，不可因为不是holding就把金额消失当正常。
	restoreHold := func() {
		if err := f.DB.Exec("UPDATE wallet_holds SET status='holding' WHERE id=(SELECT wallet_hold_id FROM ai_request_wallet_links WHERE request_id=(SELECT request_id FROM ai_gateway_tasks WHERE public_id=?)) AND status='unknown'", job.Job.ID).Error; err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(restoreHold)
	poison := f.DB.Exec("UPDATE wallet_holds SET status='unknown' WHERE id=(SELECT wallet_hold_id FROM ai_request_wallet_links WHERE request_id=(SELECT request_id FROM ai_gateway_tasks WHERE public_id=?)) AND status='holding'", job.Job.ID)
	if poison.Error != nil || poison.RowsAffected != 1 {
		t.Fatalf("异常夹具必须恰好改变原Hold一行：rows=%d err=%v", poison.RowsAffected, poison.Error)
	}
	var poisonedStatus string
	if err := f.DB.Table("wallet_holds").Select("status").Where("id=(SELECT wallet_hold_id FROM ai_request_wallet_links WHERE request_id=(SELECT request_id FROM ai_gateway_tasks WHERE public_id=?))", job.Job.ID).Scan(&poisonedStatus).Error; err != nil || poisonedStatus != "unknown" {
		t.Fatal("必须读回真实unknown状态，不能用零行修改制造空反例")
	}
	poisonedFinance := f.FinancialSnapshot()
	call("", "", token, 503, 50300)
	if !bytes.Equal(poisonedFinance, f.FinancialSnapshot()) {
		t.Fatal("汇总拒绝异常不能偷偷修复Hold或改写其他财务事实")
	}
	restoreHold()
	if _, err := f.App.CancelTask(context.Background(), caller, job.Job.ID, "g6-admin-summary-cancel"); err != nil {
		t.Fatal(err)
	}
	finance = f.FinancialSnapshot()
	released := call("", "", token, 200, 0)
	if released.UnreleasedHoldAmount != before.UnreleasedHoldAmount || released.OutboxPending != before.OutboxPending+3 || released.SettlementPending != before.SettlementPending {
		t.Fatal("取消后冻结应归零增量，三条原Outbox仍待发，汇总不得派发")
	}
	if err := f.DB.Table("users").Where("id=?", f.ProjectID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if got := call("", "", token, 200, 0); *got != *released {
		t.Fatal("目标停用不能隐藏汇总历史")
	}
	// 只在数据库Row边界注入聚合读取失败，不能把已读取的五项计数当作完整成功结果。
	const hook = "g6-admin-summary-row-failure"
	if err := f.DB.Callback().Row().Before("gorm:row").Register(hook, func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "SUM(h.hold_amount)") {
			tx.AddError(fmt.Errorf("合成聚合读取失败"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.DB.Callback().Row().Remove(hook) })
	call("", "", token, 503, 50300)
	f.DB.Callback().Row().Remove(hook)
	if err := f.DB.Table("users").Where("id=?", admin.ID).Update("admin_email_verified_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	call("", "", token, 403, 40031)
	if !bytes.Equal(finance, f.FinancialSnapshot()) || heads != f.HeadCalls() || submits != f.SubmitCalls() {
		t.Fatal("故障和MFA拒绝也不能改变财务或触发Provider")
	}
}
