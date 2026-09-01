package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

type videoReleaseCommitPool struct {
	gorm.ConnPool
	lost atomic.Bool
}

func (p *videoReleaseCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoReleaseCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoReleaseCommitTx struct {
	gorm.ConnPool
	tx          *sql.Tx
	pool        *videoReleaseCommitPool
	resultWrite bool
}

func (t *videoReleaseCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	lower := strings.ToLower(query)
	if strings.Contains(lower, "ai_video_output_release_executions") && strings.Contains(lower, "update") {
		t.resultWrite = true
	}
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *videoReleaseCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.resultWrite && t.pool.lost.CompareAndSwap(false, true) {
		return errors.New("合成解除隔离COMMIT确认丢失")
	}
	return nil
}
func (t *videoReleaseCommitTx) Rollback() error { return t.tx.Rollback() }

func TestVideoG6AdminOutputReleaseHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	id := f.f.CreateCompletedForKey(f.f.ProjectID)
	var assets []model.AIImageAsset
	if err := f.f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", id).Order("id").Find(&assets).Error; err != nil || len(assets) != 6 {
		t.Fatal("缺少原六角色资产")
	}
	var cover model.AIImageAsset
	for _, a := range assets {
		if a.AssetRole == "cover" {
			cover = a
		}
	}
	verified := time.Now().UTC().Add(-time.Minute)
	checker := authmodel.User{ID: service.NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := f.f.DB.Create(&checker).Error; err != nil {
		t.Fatal(err)
	}
	for _, actor := range []uint64{f.actor, checker.ID} {
		if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:safety_review'", actor).Error; err != nil {
			t.Fatal(err)
		}
	}
	p, err := service.NewVideoAdminReasonProtector("g6-release-test-v1", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p})
	if err != nil {
		t.Fatal(err)
	}
	makerCaller, err := f.f.JWT.Authenticate(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	q, err := app.QuarantineOutput(context.Background(), service.VideoAdminOutputQuarantineCommand{Caller: makerCaller, AssetID: cover.PublicID, VersionNo: cover.VersionNo, IdempotencyKey: "g6-release-quarantine", Reason: "合成解除前隔离"})
	if err != nil {
		t.Fatal(err)
	}
	finance := f.f.FinancialSnapshot()
	heads, submits, deletes := f.f.HeadCalls(), f.f.SubmitCalls(), f.f.MediaDeleteCalls()
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, f.f.JWT, true)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(asset, key, token string, body map[string]any, want int) map[string]json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-assets/"+asset+"/release", bytes.NewReader(raw))
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
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("解除隔离应%d实际%d", want, resp.StatusCode)
		}
		var e struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &e) != nil {
			t.Fatal("错误响应")
		}
		if bytes.Contains(data, []byte("合成复核原因")) || bytes.Contains(data, []byte("checker_id")) || bytes.Contains(data, []byte("object_key")) {
			t.Fatal("不得泄露原因或内部位置身份")
		}
		if want >= 400 {
			wantCode := map[int]int{400: 40000, 401: 40001, 403: 40003, 404: 40400}[want]
			if e.Code != wantCode {
				t.Fatalf("错误码应%d实际%d", wantCode, e.Code)
			}
			if string(e.Data) != "null" {
				t.Fatal("失败不得返回部分结果")
			}
			return nil
		}
		var fields map[string]json.RawMessage
		if e.Code != 0 || json.Unmarshal(e.Data, &fields) != nil || len(fields) != 9 {
			t.Fatal("应返回九字段低敏审批回执")
		}
		for _, key := range []string{"approval_id", "asset_id", "video_id", "request_id", "status", "restore_state", "version_no", "expires_at", "idempotent"} {
			if value, ok := fields[key]; !ok || string(value) == "null" {
				t.Fatal("审批回执字段不可缺失或用null替代")
			}
		}
		for name, wantValue := range map[string]string{"asset_id": asset, "video_id": id, "request_id": cover.RequestID, "restore_state": cover.LifecycleState} {
			var value string
			if json.Unmarshal(fields[name], &value) != nil || value != wantValue {
				t.Fatalf("审批回执%s必须关联原事实", name)
			}
		}
		var version uint64
		wantVersion := q.VersionNo
		if want == 200 {
			wantVersion++
		}
		if json.Unmarshal(fields["version_no"], &version) != nil || version != wantVersion {
			t.Fatal("审批回执必须返回当前CAS版本")
		}
		var deadline time.Time
		if json.Unmarshal(fields["expires_at"], &deadline) != nil || !deadline.After(time.Now().UTC()) {
			t.Fatal("当前申请必须有真实有效期限")
		}
		if resp.Header.Get("X-Molin-Request-ID") != cover.RequestID {
			t.Fatal("必须使用原业务请求")
		}
		return fields
	}
	body := map[string]any{"action": "request", "reason": "合成复核原因", "version_no": q.VersionNo}
	call(cover.PublicID, "g6-release-request", "", body, 401)
	call(cover.PublicID, "g6-release-request", f.f.Key, body, 401)
	first := call(cover.PublicID, "g6-release-request", f.token, body, 202)
	var approval string
	if json.Unmarshal(first["approval_id"], &approval) != nil || approval == "" {
		t.Fatal("缺少公开审批号")
	}
	replay := call(cover.PublicID, "g6-release-request", f.token, body, 202)
	if string(replay["approval_id"]) != string(first["approval_id"]) || string(replay["idempotent"]) != "true" {
		t.Fatal("申请重放不得新增审批")
	}
	var still model.AIImageAsset
	if err := f.f.DB.Where("id=?", cover.ID).Take(&still).Error; err != nil {
		t.Fatal(err)
	}
	if still.LifecycleState != "quarantined" || still.VersionNo != q.VersionNo {
		t.Fatal("申请本身不能解除隔离")
	}
	body = map[string]any{"action": "approve", "approval_id": approval, "reason": "合成复核原因", "version_no": q.VersionNo}
	call(cover.PublicID, "g6-release-self-approve", f.token, body, 403)
	forged := map[string]any{"action": "approve", "approval_id": approval, "reason": "合成复核原因", "version_no": q.VersionNo, "checker_id": checker.ID}
	call(cover.PublicID, "g6-release-forged-checker", f.token, forged, 400)
	checkerToken := f.f.TokenForUser(checker.ID)
	call("vasset_wrong_target", "g6-release-wrong-target", checkerToken, body, 404)
	// 最终checker查询期间maker临时权限到期，不能因已完成maker复验而提交旧批准。
	expiry := time.Now().UTC().Add(4 * time.Second).Truncate(time.Second)
	if err := f.f.DB.Table("user_permission_overrides").Where("user_id=? AND permission_code='ai_gateway:safety_review'", f.actor).Update("expires_at", expiry).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Table("user_permission_overrides").Select("expires_at").Where("user_id=? AND permission_code='ai_gateway:safety_review'", f.actor).Scan(&expiry).Error; err != nil {
		t.Fatal(err)
	}
	var checkerReads atomic.Int32
	var crossed atomic.Bool
	const hook = "g6_release_final_checker_wait"
	if err := f.f.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "users" || !strings.Contains(tx.Statement.SQL.String(), "id,status,admin_phone_verified_at,admin_email_verified_at") {
			return
		}
		isChecker := false
		for _, v := range tx.Statement.Vars {
			if id, ok := v.(uint64); ok && id == checker.ID {
				isChecker = true
			}
		}
		if isChecker && checkerReads.Add(1) == 2 {
			valid := tx.RowsAffected == 1 && time.Now().UTC().Before(expiry)
			if delay := time.Until(expiry.Add(100 * time.Millisecond)); delay > 0 {
				time.Sleep(delay)
			}
			crossed.Store(valid && !time.Now().UTC().Before(expiry))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.f.DB.Callback().Query().Remove(hook) })
	call(cover.PublicID, "g6-release-expired-maker", checkerToken, body, 403)
	f.f.DB.Callback().Query().Remove(hook)
	if checkerReads.Load() != 2 || !crossed.Load() {
		t.Fatal("必须在最后checker读取时实际跨越maker有效期限")
	}
	var executions int64
	if err := f.f.DB.Table("ai_video_output_release_executions").Where("quarantine_id=(SELECT id FROM ai_video_admin_output_quarantines WHERE asset_id=?)", cover.ID).Count(&executions).Error; err != nil || executions != 0 {
		t.Fatal("maker末尾过期不能留下复核执行事实")
	}
	var rollback model.AIImageAsset
	if err := f.f.DB.Where("id=?", cover.ID).Take(&rollback).Error; err != nil || !reflect.DeepEqual(rollback, still) {
		t.Fatal("maker末尾过期必须回滚资产恢复")
	}
	var audits int64
	if err := f.f.DB.Table("audit_logs").Where("operator_id=?", checker.ID).Count(&audits).Error; err != nil || audits != 0 {
		t.Fatal("maker过期必须回滚复核前后审计")
	}
	if err := f.f.DB.Table("user_permission_overrides").Where("user_id=? AND permission_code='ai_gateway:safety_review'", f.actor).Update("expires_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	checkerCaller, err := f.f.JWT.Authenticate(context.Background(), checkerToken)
	if err != nil {
		t.Fatal(err)
	}
	releaseCommand := service.VideoAdminOutputReleaseCommand{Caller: checkerCaller, AssetID: cover.PublicID, Action: "approve", ApprovalID: approval, IdempotencyKey: "g6-release-checker-approve", Reason: "合成复核原因", VersionNo: q.VersionNo}
	expiredMFA := time.Now().UTC().Add(-25 * time.Hour)
	if err := f.f.DB.Exec("UPDATE users SET admin_phone_verified_at=?,admin_email_verified_at=? WHERE id=?", expiredMFA, expiredMFA, checker.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reply, err := app.ReleaseOutput(context.Background(), releaseCommand); reply != nil || !errors.Is(err, service.ErrVideoAdminMFA) {
		t.Fatalf("checker MFA过期必须在执行前拒绝：reply=%+v err=%v", reply, err)
	}
	if err := f.f.DB.Exec("UPDATE users SET admin_phone_verified_at=?,admin_email_verified_at=? WHERE id=?", verified, verified, checker.ID).Error; err != nil {
		t.Fatal(err)
	}
	var beforeConcurrentExecutions, beforeConcurrentAudits int64
	_ = f.f.DB.Table("ai_video_output_release_executions").Where("request_id=(SELECT id FROM ai_video_output_release_requests WHERE public_id=?)", approval).Count(&beforeConcurrentExecutions).Error
	_ = f.f.DB.Table("audit_logs").Where("operator_id=? AND action LIKE 'video_output_release_%'", checker.ID).Count(&beforeConcurrentAudits).Error
	if beforeConcurrentExecutions != 0 || beforeConcurrentAudits != 0 {
		t.Fatal("checker MFA过期不得留下执行或审计")
	}
	pool := &videoReleaseCommitPool{ConnPool: f.f.DB.ConnPool}
	wrappedDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	restoreDB := f.f.UseApplicationDB(wrappedDB)
	start := make(chan struct{})
	type answer struct {
		reply *service.VideoAdminOutputReleaseReply
		err   error
	}
	answers := make(chan answer, 100)
	var wg sync.WaitGroup
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reply, err := app.ReleaseOutput(context.Background(), releaseCommand)
			answers <- answer{reply: reply, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(answers)
	var firstExecution, commitErrors int
	for answer := range answers {
		if answer.err != nil {
			if answer.reply != nil {
				t.Fatalf("确认丢失不能同时返回部分结果：reply=%+v err=%v", answer.reply, answer.err)
			}
			commitErrors++
			continue
		}
		if answer.reply == nil || answer.reply.Status != "released" || answer.reply.ApprovalID != approval || answer.reply.VersionNo != q.VersionNo+1 {
			t.Fatalf("并发解除返回异常：reply=%+v err=%v", answer.reply, answer.err)
		}
		if !answer.reply.Idempotent {
			firstExecution++
		}
	}
	if firstExecution+commitErrors != 1 || commitErrors != 1 {
		t.Fatalf("100并发复核必须恰好一次实际执行或确认丢失：first=%d commit_errors=%d", firstExecution, commitErrors)
	}
	if !pool.lost.Load() {
		t.Fatal("100并发中必须实际提交后丢失一次确认并由最外层恢复")
	}
	done := call(cover.PublicID, "g6-release-checker-approve", checkerToken, body, 200)
	restoreDB()
	if string(done["status"]) != "\"released\"" || string(done["idempotent"]) != "true" {
		t.Fatal("并发完成后的HTTP重放必须读取原解除回执")
	}
	approvalFacts := func() []byte {
		t.Helper()
		var requests, executions, audits []map[string]any
		if err := f.f.DB.Table("ai_video_output_release_requests").Where("public_id=?", approval).Find(&requests).Error; err != nil || len(requests) != 1 {
			t.Fatal("必须唯一申请事实")
		}
		if err := f.f.DB.Table("ai_video_output_release_executions").Where("request_id=(SELECT id FROM ai_video_output_release_requests WHERE public_id=?) AND status='completed'", approval).Find(&executions).Error; err != nil || len(executions) != 1 {
			t.Fatal("必须唯一完成执行")
		}
		if err := f.f.DB.Table("audit_logs").Where("operator_id IN ? AND action LIKE 'video_output_release_%'", []uint64{f.actor, checker.ID}).Order("id").Find(&audits).Error; err != nil || len(audits) != 4 {
			t.Fatal("申请和复核各有两份审计")
		}
		for _, actor := range []uint64{f.actor, checker.ID} {
			var count int64
			if err := f.f.DB.Table("audit_logs").Where("operator_id=? AND action LIKE 'video_output_release_%'", actor).Count(&count).Error; err != nil || count != 2 {
				t.Fatal("不能以一人四份审计冒充双人记录")
			}
		}
		raw, err := json.Marshal(map[string]any{"requests": requests, "executions": executions, "audits": audits})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	firstFacts := approvalFacts()
	done = call(cover.PublicID, "g6-release-checker-approve", checkerToken, body, 200)
	if string(done["idempotent"]) != "true" {
		t.Fatal("完成重放不重复消费审批")
	}
	if !bytes.Equal(firstFacts, approvalFacts()) {
		t.Fatal("重放不能改变申请、执行、原因密文和四份审计")
	}
	var after []model.AIImageAsset
	if err := f.f.DB.Where("task_id=?", cover.TaskID).Order("id").Find(&after).Error; err != nil || len(after) != 6 {
		t.Fatal("原资产组不可缺失")
	}
	for i, a := range after {
		if a.ID == cover.ID {
			if a.VersionNo != cover.VersionNo+2 {
				t.Fatal("只允许一次隔离和一次解除CAS")
			}
			a.VersionNo, a.UpdatedAt = assets[i].VersionNo, assets[i].UpdatedAt
		}
		if !reflect.DeepEqual(a, assets[i]) {
			t.Fatal("解除仅恢复原状态，不改安全、期限、规格及兄弟资产")
		}
	}
	if !bytes.Equal(finance, f.f.FinancialSnapshot()) || heads != f.f.HeadCalls() || submits != f.f.SubmitCalls() || deletes != f.f.MediaDeleteCalls() {
		t.Fatal("解除不得调用Provider、Store或改财务")
	}
}
