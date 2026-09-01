package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"gorm.io/gorm"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

type videoQuarantineCommitPool struct {
	gorm.ConnPool
	lost atomic.Bool
}

func (p *videoQuarantineCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoQuarantineCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoQuarantineCommitTx struct {
	gorm.ConnPool
	tx          *sql.Tx
	pool        *videoQuarantineCommitPool
	resultWrite bool
}

func (t *videoQuarantineCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	lower := strings.ToLower(query)
	if strings.Contains(lower, "ai_video_admin_output_quarantines") && strings.Contains(lower, "update") {
		t.resultWrite = true
	}
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *videoQuarantineCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.resultWrite && t.pool.lost.CompareAndSwap(false, true) {
		return errors.New("合成输出隔离COMMIT确认丢失")
	}
	return nil
}
func (t *videoQuarantineCommitTx) Rollback() error { return t.tx.Rollback() }

func TestVideoG6AdminOutputQuarantineHTTPMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	f.f.EnableAssetSaving()
	f.f.EnableAssetDownloads()
	id := f.f.CreateCompletedForKey(f.f.ProjectID)
	var assets []model.AIImageAsset
	if err := f.f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", id).Order("id").Find(&assets).Error; err != nil || len(assets) != 6 {
		t.Fatal("必须有真实六角色资产")
	}
	var root, cover model.AIImageAsset
	for _, a := range assets {
		if a.AssetRole == "content" {
			root = a
		}
		if a.AssetRole == "cover" {
			cover = a
		}
	}
	if root.ID == 0 || cover.ID == 0 {
		t.Fatal("缺少真实内容或封面")
	}
	owner := service.VideoCaller{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: f.f.ProjectID}
	saved, err := f.f.App.SaveVideoAsset(context.Background(), owner, root.PublicID, "g6-admin-output-save-before")
	if err != nil || saved.UserAssetID == 0 {
		t.Fatalf("必须先形成原长期副本：%v", err)
	}
	short, err := f.f.App.AssetDownloadURL(context.Background(), owner, root.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	long, err := f.f.App.SavedVideoDownloadURL(context.Background(), owner, saved.UserAssetID, "content")
	if err != nil {
		t.Fatal(err)
	}
	p, err := service.NewVideoAdminReasonProtector("g6-output-quarantine-v1", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: p})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, f.f.JWT, true)
	gateway.RegisterVideoUserRoutes(mux, f.f.App, f.f.Keys, true, f.f.JWT)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 35 * time.Second
	read := func(path string, want int) {
		t.Helper()
		r, _ := http.NewRequest("GET", srv.URL+path, nil)
		r.Header.Set("Authorization", "Bearer "+f.f.Key)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("内容读取应%d实际%d", want, resp.StatusCode)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
	}
	read(short.DownloadURL, 200)
	read(long.DownloadURL, 200)
	const reason = "合成已交付视频的行政隔离"
	call := func(asset, key string, version uint64, token string, want, code int) *service.VideoAdminOutputQuarantineReply {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"reason": reason, "version_no": version})
		r, _ := http.NewRequest("POST", srv.URL+"/api/admin/token/video-assets/"+asset+"/quarantine", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
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
		if json.Unmarshal(raw, &e) != nil || resp.StatusCode != want || e.Code != code {
			t.Fatalf("输出隔离应%d/%d实际%d/%d", want, code, resp.StatusCode, e.Code)
		}
		if bytes.Contains(raw, []byte(reason)) || bytes.Contains(raw, []byte("admin_quarantine_command_id")) {
			t.Fatal("不能公开原因或内部隔离指针")
		}
		if want != 200 {
			if string(e.Data) != "null" {
				t.Fatal("错误不得返回部分结果")
			}
			return nil
		}
		var reply service.VideoAdminOutputQuarantineReply
		var fields map[string]json.RawMessage
		if json.Unmarshal(e.Data, &reply) != nil || json.Unmarshal(e.Data, &fields) != nil || len(fields) != 29 || reply.VideoAdminOutputDetails == nil || reply.AssetID != asset || reply.RequestID != root.RequestID {
			t.Fatal("必须返回原28字段管理资产及幂等标记")
		}
		return &reply
	}
	key := "g6-admin-output-quarantine"
	call(cover.PublicID, key, cover.VersionNo, "", 401, 40001)
	call(cover.PublicID, key, cover.VersionNo, f.f.Key, 401, 40001)
	call(cover.PublicID, key, cover.VersionNo, f.token, 403, 40003)
	if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:safety_review'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	call("vasset_unknown_output", key, 1, f.token, 404, 40400)
	call(cover.PublicID, key, cover.VersionNo+1, f.token, 409, 40900)
	// 没有行政凭据时，原通过审核/标识的资产仍必须被数据库拒绝直接隔离。
	if err := f.f.DB.Table("ai_gateway_assets").Where("id=?", cover.ID).Updates(map[string]any{"lifecycle_state": "quarantined", "version_no": cover.VersionNo + 1}).Error; err == nil {
		t.Fatal("不允许删除原隔离CHECK后裸改状态")
	}
	if err := f.f.DB.Table("ai_gateway_assets").Where("id=?", cover.ID).Updates(map[string]any{"lifecycle_state": "quarantined", "version_no": cover.VersionNo + 1, "admin_quarantine_command_id": uint64(999999999)}).Error; err == nil {
		t.Fatal("无效行政凭据不能授权隔离")
	}
	// 第二次父资产查询属于写入后的最终详情；让已获准权限在这里自然到期，检查整笔回滚。
	expiry := time.Now().UTC().Add(4 * time.Second).Truncate(time.Second)
	if err := f.f.DB.Table("user_permission_overrides").Where("user_id=? AND permission_code='ai_gateway:safety_review'", f.actor).Update("expires_at", expiry).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Table("user_permission_overrides").Select("expires_at").Where("user_id=? AND permission_code='ai_gateway:safety_review'", f.actor).Scan(&expiry).Error; err != nil {
		t.Fatal(err)
	}
	var parents atomic.Int32
	var crossedValidDeadline atomic.Bool
	hook := "g6_output_quarantine_final_details_expiry"
	if err := f.f.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == "ai_gateway_assets" && strings.Contains(tx.Statement.SQL.String(), "parent_asset_id IS NULL") && parents.Add(1) == 2 {
			enteredValid := tx.RowsAffected == 1 && time.Now().UTC().Before(expiry)
			if remaining := time.Until(expiry.Add(100 * time.Millisecond)); remaining > 0 {
				time.Sleep(remaining)
			}
			crossedValidDeadline.Store(enteredValid && !time.Now().UTC().Before(expiry))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.f.DB.Callback().Query().Remove(hook) })
	beforeExpiry := f.f.FinancialSnapshot()
	call(cover.PublicID, "g6-admin-output-expired-final", cover.VersionNo, f.token, 403, 40003)
	f.f.DB.Callback().Query().Remove(hook)
	if parents.Load() != 2 || !crossedValidDeadline.Load() {
		t.Fatal("必须在写后详情命中一行并由有效跨越数据库读回期限，不能用入口拒绝冒充末尾撤权回滚")
	}
	var rollbackAssets []model.AIImageAsset
	if err := f.f.DB.Where("task_id=?", root.TaskID).Order("id").Find(&rollbackAssets).Error; err != nil || !reflect.DeepEqual(assets, rollbackAssets) || !bytes.Equal(beforeExpiry, f.f.FinancialSnapshot()) {
		t.Fatal("最终权限到期必须回滚隔离CAS且保留原资产与财务")
	}
	for _, table := range []string{"ai_video_admin_output_quarantines", "audit_logs"} {
		column := "actor_user_id"
		if table == "audit_logs" {
			column = "operator_id"
		}
		var count int64
		if err := f.f.DB.Table(table).Where(column+"=?", f.actor).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("最终权限到期不能留下prepared、completed或前后审计")
		}
	}
	if err := f.f.DB.Table("user_permission_overrides").Where("user_id=? AND permission_code='ai_gateway:safety_review'", f.actor).Update("expires_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	finance := f.f.FinancialSnapshot()
	heads, deletes, submits := f.f.HeadCalls(), f.f.MediaDeleteCalls(), f.f.SubmitCalls()
	actorCaller, err := f.f.JWT.Authenticate(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	quarantineCommand := service.VideoAdminOutputQuarantineCommand{Caller: actorCaller, AssetID: cover.PublicID, VersionNo: cover.VersionNo, IdempotencyKey: key, Reason: reason}
	expiredMFA := time.Now().UTC().Add(-25 * time.Hour)
	if err := f.f.DB.Exec("UPDATE users SET admin_phone_verified_at=?,admin_email_verified_at=? WHERE id=?", expiredMFA, expiredMFA, f.actor).Error; err != nil {
		t.Fatal(err)
	}
	if reply, err := app.QuarantineOutput(context.Background(), quarantineCommand); reply != nil || !errors.Is(err, service.ErrVideoAdminMFA) {
		t.Fatalf("隔离管理员MFA过期必须在写入前拒绝：reply=%+v err=%v", reply, err)
	}
	validMFA := time.Now().UTC().Add(-time.Minute)
	if err := f.f.DB.Exec("UPDATE users SET admin_phone_verified_at=?,admin_email_verified_at=? WHERE id=?", validMFA, validMFA, f.actor).Error; err != nil {
		t.Fatal(err)
	}
	pool := &videoQuarantineCommitPool{ConnPool: f.f.DB.ConnPool}
	wrappedDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	restoreDB := f.f.UseApplicationDB(wrappedDB)
	start := make(chan struct{})
	type answer struct {
		reply *service.VideoAdminOutputQuarantineReply
		err   error
	}
	answers := make(chan answer, 100)
	var wg sync.WaitGroup
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reply, err := app.QuarantineOutput(context.Background(), quarantineCommand)
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
				t.Fatal("隔离确认丢失不能返回部分结果")
			}
			commitErrors++
			continue
		}
		if answer.reply == nil || answer.reply.LifecycleState != "quarantined" || answer.reply.VersionNo != cover.VersionNo+1 || answer.reply.ModerationStatus != "passed" || answer.reply.ExplicitLabelStatus != "applied" || answer.reply.ImplicitLabelStatus != "applied" {
			t.Fatalf("并发隔离不得篡改原安全事实：reply=%+v", answer.reply)
		}
		if !answer.reply.Idempotent {
			firstExecution++
		}
	}
	if firstExecution+commitErrors != 1 || commitErrors != 1 || !pool.lost.Load() {
		t.Fatalf("100并发隔离必须一次执行或确认丢失：first=%d commit_errors=%d lost=%t", firstExecution, commitErrors, pool.lost.Load())
	}
	first := call(cover.PublicID, key, cover.VersionNo, f.token, 200, 0)
	restoreDB()
	if !first.Idempotent || first.LifecycleState != "quarantined" || first.VersionNo != cover.VersionNo+1 || first.ModerationStatus != "passed" || first.ExplicitLabelStatus != "applied" || first.ImplicitLabelStatus != "applied" {
		t.Fatal("并发与确认丢失后的HTTP重放必须恢复原隔离事实")
	}
	var after []model.AIImageAsset
	if err := f.f.DB.Where("task_id=?", root.TaskID).Order("id").Find(&after).Error; err != nil || len(after) != 6 {
		t.Fatal("资产组不可缺项")
	}
	for i, a := range after {
		if a.ID == cover.ID {
			a.LifecycleState, a.VersionNo, a.UpdatedAt = assets[i].LifecycleState, assets[i].VersionNo, assets[i].UpdatedAt
		}
		if !reflect.DeepEqual(a, assets[i]) {
			t.Fatal("只允许选定资产状态/版本/更新时间变化，兄弟及所有原安全事实不变")
		}
	}
	if !bytes.Equal(finance, f.f.FinancialSnapshot()) || heads != f.f.HeadCalls() || deletes != f.f.MediaDeleteCalls() || submits != f.f.SubmitCalls() {
		t.Fatal("行政隔离不得写财务或访问Provider/Store")
	}
	replay := call(cover.PublicID, key, cover.VersionNo, f.token, 200, 0)
	if !replay.Idempotent || replay.VersionNo != first.VersionNo {
		t.Fatal("重放不能重复隔离")
	}
	call(root.PublicID, key, root.VersionNo, f.token, 409, 40900)
	read(short.DownloadURL, 404)
	read(long.DownloadURL, 409)
	read("/v1/videos/"+id+"/content", 404)
	// 生成财务快照不覆盖长期保存额度；单独比较保存、用户资产/权益与容量锁事实的完整内容。
	saveFacts := func() []byte {
		t.Helper()
		facts := map[string]any{}
		for _, table := range []string{"ai_video_asset_saves", "ai_video_asset_save_commands", "user_assets", "user_entitlements", "asset_events"} {
			var rows []map[string]any
			order := "id"
			if table == "ai_video_asset_saves" {
				order = "public_id"
			}
			if table == "ai_video_asset_save_commands" {
				order = "command_key_hash"
			}
			if err := f.f.DB.Table(table).Where("user_id=?", f.f.ProjectID).Order(order).Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			facts[table] = rows
		}
		var scopes []map[string]any
		if err := f.f.DB.Table("ai_video_asset_save_scopes").Where("scope_type='global' OR scope_id=?", f.f.ProjectID).Order("scope_type,scope_id").Find(&scopes).Error; err != nil {
			t.Fatal(err)
		}
		facts["scopes"] = scopes
		raw, err := json.Marshal(facts)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	beforeSaveFacts := saveFacts()
	beforeRejectedSave := f.f.HeadCalls()
	for _, saveKey := range []string{"g6-admin-output-save-before", "g6-admin-output-save-after"} {
		if _, err := f.f.App.SaveVideoAsset(context.Background(), owner, root.PublicID, saveKey); err == nil {
			t.Fatal("隔离后同键重放和新键均不能凭旧保存结果越过当前安全门禁")
		}
	}
	if f.f.HeadCalls() != beforeRejectedSave {
		t.Fatal("保存拒绝必须先于长期对象存储访问")
	}
	if !bytes.Equal(beforeSaveFacts, saveFacts()) {
		t.Fatal("保存拒绝不得追加新键、复制命令、用户资产、权益额度或容量事实")
	}
	if _, err := f.f.App.AssetDownloadURL(context.Background(), owner, root.PublicID); err == nil {
		t.Fatal("不能签发新下载URL")
	}
	if err := f.f.DB.Table("ai_gateway_assets").Where("id=?", cover.ID).Update("admin_quarantine_command_id", nil).Error; err == nil {
		t.Fatal("不能绕过独立解除隔离流程清除指针")
	}
	if err := f.f.DB.Table("ai_gateway_assets").Where("id=?", cover.ID).Updates(map[string]any{"lifecycle_state": "available", "version_no": first.VersionNo + 1}).Error; err == nil {
		t.Fatal("不能由原通用状态迁移解除行政隔离")
	}
	var commands, audits int64
	if err := f.f.DB.Table("ai_video_admin_output_quarantines").Where("actor_user_id=? AND status='completed'", f.actor).Count(&commands).Error; err != nil || commands != 1 {
		t.Fatal("必须只有一份完成命令")
	}
	if err := f.f.DB.Table("audit_logs").Where("operator_id=?", f.actor).Count(&audits).Error; err != nil || audits != 2 {
		t.Fatal("必须只有两份真实前后审计")
	}
	if err := f.f.DB.Table("ai_video_admin_output_quarantines").Where("actor_user_id=?", f.actor).Update("initial_state", "temporary").Error; err == nil {
		t.Fatal("完成后命令不可篡改")
	}
	if err := f.f.DB.Table("users").Where("id=?", f.f.ProjectID).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Table("api_keys").Where("id=?", f.f.ProjectID).Update("status", "revoked").Error; err != nil {
		t.Fatal(err)
	}
	if r := call(cover.PublicID, key, cover.VersionNo, f.token, 200, 0); !r.Idempotent {
		t.Fatal("目标停用不能伪装新隔离")
	}
	if !bytes.Equal(finance, f.f.FinancialSnapshot()) {
		t.Fatal("阻断读取不能改写已结算财务")
	}
}
