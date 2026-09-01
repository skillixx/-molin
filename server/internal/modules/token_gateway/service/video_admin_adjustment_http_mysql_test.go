package service_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	billingmodel "molin/server/internal/modules/billing/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

type videoAdjustmentCommitPool struct {
	gorm.ConnPool
	lost atomic.Bool
}

func (p *videoAdjustmentCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoAdjustmentCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoAdjustmentCommitTx struct {
	gorm.ConnPool
	tx             *sql.Tx
	pool           *videoAdjustmentCommitPool
	executionWrite bool
}

func (t *videoAdjustmentCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	lower := strings.ToLower(query)
	if strings.Contains(lower, "ai_video_adjustment_approval_executions") && (strings.Contains(lower, "insert") || strings.Contains(lower, "update")) {
		t.executionWrite = true
	}
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *videoAdjustmentCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.executionWrite && t.pool.lost.CompareAndSwap(false, true) {
		return errors.New("合成调账复核COMMIT确认丢失")
	}
	return nil
}
func (t *videoAdjustmentCommitTx) Rollback() error { return t.tx.Rollback() }

func TestVideoG6AdminAdjustmentHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	id := f.f.CreateCompletedForKey(f.f.ProjectID)
	owner := repository.VideoOwner{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: &f.f.ProjectID}
	task, err := repository.NewVideoTaskRepository(f.f.DB).FindForOwner(context.Background(), id, owner)
	if err != nil {
		t.Fatal(err)
	}
	verified := time.Now().UTC().Add(-time.Minute)
	checker := authmodel.User{ID: service.NextVideoFixtureUserID(), Status: "active", PasswordHash: "synthetic-only", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := f.f.DB.Create(&checker).Error; err != nil {
		t.Fatal(err)
	}
	for _, actor := range []uint64{f.actor, checker.ID} {
		if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:reconcile_manage'", actor).Error; err != nil {
			t.Fatal(err)
		}
	}
	protector, err := service.NewVideoAdminReasonProtector("g6-adjustment-http-v1", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: protector, AdjustmentsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, f.f.JWT, true)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 35 * time.Second
	call := func(body map[string]any, key, token string, want int) (*service.VideoAdminAdjustmentReply, error) {
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-adjustments", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(r)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		var e struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &e) != nil || resp.StatusCode != want {
			return nil, errAdjustmentTestHTTP{want: want, got: resp.StatusCode}
		}
		if bytes.Contains(data, []byte("合成调账说明")) || bytes.Contains(data, []byte("checker_id")) {
			return nil, errAdjustmentTestHTTP{want: 0, got: 1}
		}
		if want >= 400 {
			if string(e.Data) != "null" {
				return nil, errAdjustmentTestHTTP{want: 0, got: 2}
			}
			return nil, nil
		}
		var result service.VideoAdminAdjustmentReply
		var fields map[string]json.RawMessage
		if e.Code != 0 || json.Unmarshal(e.Data, &result) != nil || json.Unmarshal(e.Data, &fields) != nil || len(fields) != 14 || result.TaskID != id || result.RequestID != task.RequestID || resp.Header.Get("X-Molin-Request-ID") != task.RequestID {
			return nil, errAdjustmentTestHTTP{want: 14, got: len(fields)}
		}
		return &result, nil
	}
	request := map[string]any{"action": "request", "task_id": id, "version_no": task.VersionNo, "amount": "0.25", "direction": "credit", "adjustment_reason": "billing_correction", "reason": "合成调账说明"}
	if _, err := call(request, "g6-adjustment-request", "", 401); err != nil {
		t.Fatal(err)
	}
	if _, err := call(request, "g6-adjustment-request", f.f.Key, 401); err != nil {
		t.Fatal(err)
	}
	before := f.f.FinancialSnapshot()
	var walletBefore billingmodel.Wallet
	if err := f.f.DB.Where("user_id=?", f.f.ProjectID).Take(&walletBefore).Error; err != nil {
		t.Fatal(err)
	}
	a, err := call(request, "g6-adjustment-request", f.token, 202)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "pending" || a.Amount != "0.25000000" || a.SequenceNo != 1 || a.VersionNo != 1 || a.UsageID != nil || a.WalletTransactionID != nil || !bytes.Equal(before, f.f.FinancialSnapshot()) {
		t.Fatal("申请仅冻结计划，不得产生资金动作")
	}
	second, err := call(request, "g6-adjustment-request-second", f.token, 202)
	if err != nil {
		t.Fatal(err)
	}
	if second.SequenceNo != 2 {
		t.Fatal("待审批也必须占用独立序号")
	}
	// 受保护原因密钥不可用不能被误判为客户提交了不同意图；原审批与资金均不得变化。
	otherSecret := make([]byte, 32)
	if _, err := rand.Read(otherSecret); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct {
		version string
		secret  []byte
	}{{"g6-adjustment-http-v2", f.secret}, {"g6-adjustment-http-v2", otherSecret}, {"g6-adjustment-http-v1", otherSecret}} {
		p, err := service.NewVideoAdminReasonProtector(variant.version, variant.secret)
		if err != nil {
			t.Fatal(err)
		}
		a, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p, AdjustmentsEnabled: true})
		if err != nil {
			t.Fatal(err)
		}
		m := http.NewServeMux()
		gateway.RegisterVideoAdminRoutes(m, a, f.f.JWT, true)
		other := httptest.NewServer(m)
		raw, _ := json.Marshal(request)
		r, _ := http.NewRequest("POST", other.URL+"/api/admin/token/video-adjustments", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "g6-adjustment-request")
		r.Header.Set("Authorization", "Bearer "+f.token)
		resp, err := other.Client().Do(r)
		if err != nil {
			other.Close()
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		other.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("原因密钥不可用应503实际%d", resp.StatusCode)
		}
	}
	var pendingCount int64
	if err := f.f.DB.Table("ai_video_adjustment_approvals").Where("maker_user_id=?", f.actor).Count(&pendingCount).Error; err != nil || pendingCount != 2 || !bytes.Equal(before, f.f.FinancialSnapshot()) {
		t.Fatal("密钥失败不能重复申请或写资金")
	}
	approve := map[string]any{"action": "approve", "approval_id": a.ApprovalID, "version_no": 1, "reason": "合成调账说明"}
	if _, err := call(approve, "g6-adjustment-self-approve", f.token, 403); err != nil {
		t.Fatal(err)
	}
	forged := map[string]any{"action": "approve", "approval_id": a.ApprovalID, "version_no": 1, "reason": "合成调账说明", "amount": "9.00"}
	checkerToken := f.f.TokenForUser(checker.ID)
	if _, err := call(forged, "g6-adjustment-forged-amount", checkerToken, 400); err != nil {
		t.Fatal(err)
	}
	var first, bad atomic.Int32
	var lock sync.Mutex
	var usageID, movementID uint64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := call(approve, "g6-adjustment-approve", checkerToken, 200)
			if e != nil || r == nil || r.Status != "executed" || r.UsageID == nil || r.WalletTransactionID == nil {
				bad.Add(1)
				return
			}
			if !r.Idempotent {
				first.Add(1)
			}
			lock.Lock()
			defer lock.Unlock()
			if usageID == 0 {
				usageID, movementID = *r.UsageID, *r.WalletTransactionID
			} else if usageID != *r.UsageID || movementID != *r.WalletTransactionID {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 || first.Load() != 1 {
		t.Fatalf("100复核必须恰好一次资金动作：bad=%d first=%d", bad.Load(), first.Load())
	}
	var walletAfter billingmodel.Wallet
	if err := f.f.DB.Where("user_id=?", f.f.ProjectID).Take(&walletAfter).Error; err != nil {
		t.Fatal(err)
	}
	if !walletAfter.BalanceAmount.Equal(walletBefore.BalanceAmount.Add(decimal.RequireFromString("0.25"))) || !walletAfter.FrozenAmount.Equal(walletBefore.FrozenAmount) {
		t.Fatal("只能追加0.25合成修正，原预占不变")
	}
	debitRequest := map[string]any{"action": "request", "task_id": id, "version_no": task.VersionNo, "amount": "0.10", "direction": "debit", "adjustment_reason": "billing_correction", "reason": "合成调账说明"}
	debit, err := call(debitRequest, "g6-adjustment-debit-request", f.token, 202)
	if err != nil || debit.Status != "pending" || debit.SequenceNo != 3 {
		t.Fatalf("debit申请失败：reply=%+v err=%v", debit, err)
	}
	var debitApproval struct{ ID uint64 }
	if err := f.f.DB.Table("ai_video_adjustment_approvals").Select("id").Where("public_id=?", debit.ApprovalID).Take(&debitApproval).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Table("ai_video_adjustment_approvals").Where("id=?", debitApproval.ID).Update("amount", "9.00").Error; err == nil {
		t.Fatal("不可变审批不得被SQL替换金额")
	}
	if err := f.f.DB.Exec("DELETE FROM ai_video_adjustment_approvals WHERE id=?", debitApproval.ID).Error; err == nil {
		t.Fatal("不可变审批不得被SQL删除")
	}
	debitApprove := map[string]any{"action": "approve", "approval_id": debit.ApprovalID, "version_no": 1, "reason": "合成调账说明"}
	beforeDebit := f.f.FinancialSnapshot()
	if err := f.f.DB.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='ai_gateway:reconcile_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := call(debitApprove, "g6-adjustment-debit-approve", checkerToken, 403); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDebit, f.f.FinancialSnapshot()) {
		t.Fatal("maker撤权后复核不得形成资金动作")
	}
	if err := f.f.DB.Exec("UPDATE user_permission_overrides SET effect='allow' WHERE user_id=? AND permission_code='ai_gateway:reconcile_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	debitExecuted, err := call(debitApprove, "g6-adjustment-debit-approve", checkerToken, 200)
	if err != nil || debitExecuted.Status != "executed" || debitExecuted.Direction != "debit" || debitExecuted.UsageID == nil || debitExecuted.WalletTransactionID == nil {
		t.Fatalf("debit复核失败：reply=%+v err=%v", debitExecuted, err)
	}
	if err := f.f.DB.Where("user_id=?", f.f.ProjectID).Take(&walletAfter).Error; err != nil {
		t.Fatal(err)
	}
	if !walletAfter.BalanceAmount.Equal(walletBefore.BalanceAmount.Add(decimal.RequireFromString("0.15"))) || !walletAfter.FrozenAmount.Equal(walletBefore.FrozenAmount) {
		t.Fatal("credit 0.25与debit 0.10后余额或原冻结额错误")
	}
	insufficientRequest := map[string]any{"action": "request", "task_id": id, "version_no": task.VersionNo, "amount": "999.00", "direction": "debit", "adjustment_reason": "billing_correction", "reason": "合成调账说明"}
	insufficient, err := call(insufficientRequest, "g6-adjustment-insufficient-request", f.token, 202)
	if err != nil || insufficient.Status != "pending" || insufficient.SequenceNo != 4 {
		t.Fatalf("余额不足计划仍应只形成待审批：reply=%+v err=%v", insufficient, err)
	}
	beforeInsufficient := f.f.FinancialSnapshot()
	insufficientApprove := map[string]any{"action": "approve", "approval_id": insufficient.ApprovalID, "version_no": 1, "reason": "合成调账说明"}
	if _, err := call(insufficientApprove, "g6-adjustment-insufficient-approve", checkerToken, 402); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeInsufficient, f.f.FinancialSnapshot()) {
		t.Fatal("余额不足复核必须回滚执行、审计和全部资金事实")
	}
	var expiryInjected atomic.Bool
	const expiryHook = "g6_adjustment_expired_insert"
	expiredCreated := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if err := f.f.DB.Callback().Create().Before("gorm:create").Register(expiryHook, func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "ai_video_adjustment_approvals" || !expiryInjected.CompareAndSwap(false, true) {
			return
		}
		tx.Statement.SetColumn("CreatedAt", expiredCreated)
		tx.Statement.SetColumn("ExpiresAt", expiredCreated.Add(15*time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	expiredRequest := map[string]any{"action": "request", "task_id": id, "version_no": task.VersionNo, "amount": "0.05", "direction": "credit", "adjustment_reason": "service_credit", "reason": "合成调账说明"}
	expiredApproval, err := call(expiredRequest, "g6-adjustment-expired-request", f.token, 200)
	_ = f.f.DB.Callback().Create().Remove(expiryHook)
	if err != nil || !expiryInjected.Load() || expiredApproval.Status != "expired" || expiredApproval.SequenceNo != 5 {
		t.Fatalf("过期审批必须只返回expired计划：reply=%+v err=%v", expiredApproval, err)
	}
	beforeExpiredApproval := f.f.FinancialSnapshot()
	expiredApprove := map[string]any{"action": "approve", "approval_id": expiredApproval.ApprovalID, "version_no": 1, "reason": "合成调账说明"}
	if _, err := call(expiredApprove, "g6-adjustment-expired-approve", checkerToken, 409); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeExpiredApproval, f.f.FinancialSnapshot()) {
		t.Fatal("过期审批复核不得形成执行、审计或资金动作")
	}
	commitRequest := map[string]any{"action": "request", "task_id": id, "version_no": task.VersionNo, "amount": "0.05", "direction": "credit", "adjustment_reason": "service_credit", "reason": "合成调账说明"}
	commitApproval, err := call(commitRequest, "g6-adjustment-commit-request", f.token, 202)
	if err != nil || commitApproval.Status != "pending" || commitApproval.SequenceNo != 6 {
		t.Fatalf("提交未知审批计划创建失败：reply=%+v err=%v", commitApproval, err)
	}
	pool := &videoAdjustmentCommitPool{ConnPool: f.f.DB.ConnPool}
	wrappedDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	restoreDB := f.f.UseApplicationDB(wrappedDB)
	commitApprove := map[string]any{"action": "approve", "approval_id": commitApproval.ApprovalID, "version_no": 1, "reason": "合成调账说明"}
	if _, err := call(commitApprove, "g6-adjustment-commit-approve", checkerToken, 503); err != nil || !pool.lost.Load() {
		t.Fatalf("复核必须真实提交后丢失确认：err=%v lost=%t", err, pool.lost.Load())
	}
	afterUnknown := f.f.FinancialSnapshot()
	commitReplay, err := call(commitApprove, "g6-adjustment-commit-approve", checkerToken, 200)
	restoreDB()
	if err != nil || commitReplay == nil || !commitReplay.Idempotent || commitReplay.Status != "executed" || commitReplay.UsageID == nil || commitReplay.WalletTransactionID == nil {
		t.Fatalf("复核提交未知重放必须恢复原结果：reply=%+v err=%v", commitReplay, err)
	}
	if !bytes.Equal(afterUnknown, f.f.FinancialSnapshot()) {
		t.Fatal("复核提交未知重放不得重复资金、Usage、Outbox或审计")
	}
	if err := f.f.DB.Where("user_id=?", f.f.ProjectID).Take(&walletAfter).Error; err != nil || !walletAfter.BalanceAmount.Equal(walletBefore.BalanceAmount.Add(decimal.RequireFromString("0.20"))) || !walletAfter.FrozenAmount.Equal(walletBefore.FrozenAmount) {
		t.Fatal("三次成功调整后的余额或冻结额错误")
	}
	var oldFacts, newFacts map[string][]string
	if json.Unmarshal(before, &oldFacts) != nil || json.Unmarshal(f.f.FinancialSnapshot(), &newFacts) != nil {
		t.Fatal("快照无效")
	}
	for _, table := range []string{"ai_requests", "ai_gateway_quotes", "wallet_holds", "ai_request_wallet_links"} {
		if !reflect.DeepEqual(oldFacts[table], newFacts[table]) {
			t.Fatal("调账不得覆盖原结算、报价、Hold和关联")
		}
	}
	report, err := service.NewVideoReconciliationService(f.f.DB).Reconcile(context.Background(), id, owner)
	if err != nil || !report.Passed {
		t.Fatal("调账后原账和调整账必须同时零差异")
	}
	var executions, adjustments, audits int64
	if err := f.f.DB.Table("ai_video_adjustment_approval_executions").Where("status='executed' AND checker_user_id=?", checker.ID).Count(&executions).Error; err != nil || executions != 3 {
		t.Fatal("credit、debit和提交未知恢复只能各消费一个审批")
	}
	if err := f.f.DB.Table("ai_usage_items").Where("request_id=? AND record_kind='adjustment'", task.RequestID).Count(&adjustments).Error; err != nil || adjustments != 3 {
		t.Fatal("只能追加三次成功调整Usage")
	}
	if err := f.f.DB.Table("audit_logs").Where("operator_id IN ?", []uint64{f.actor, checker.ID}).Count(&audits).Error; err != nil || audits != 18 {
		t.Fatal("六个申请和三次成功复核各有前后审计")
	}
}

type errAdjustmentTestHTTP struct{ want, got int }

func (e errAdjustmentTestHTTP) Error() string {
	return fmt.Sprintf("调账HTTP/字段应%d实际%d", e.want, e.got)
}
