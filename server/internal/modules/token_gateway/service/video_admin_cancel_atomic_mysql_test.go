package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/service"
)

func adminCancelI2VFixture(t *testing.T, f *adminCancelErrorFixture) {
	t.Helper()
	ctx := context.Background()
	owner := service.VideoCaller{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: f.f.ProjectID}
	if _, err := f.f.App.AcceptProjectRights(ctx, service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID}, PolicyVersion: f.f.Policy, Confirmed: true, IdempotencyKey: "g6-admin-atomic-rights", RequestID: "g6-admin-atomic-rights-trace"}); err != nil {
		t.Fatal(err)
	}
	input, err := f.f.App.ImportImageInput(ctx, service.VideoInputImportCommand{Caller: owner, IdempotencyKey: "g6-admin-atomic-import", SourceAssetID: f.f.SourceID})
	if err != nil || input.InputAssetID == nil {
		t.Fatalf("真实输入导入失败：%v", err)
	}
	job, err := f.f.App.Create(ctx, service.VideoCommand{Caller: owner, IdempotencyKey: "g6-admin-atomic-create", Model: f.f.Model, Prompt: "合成图生取消事务回滚", Operation: "image_to_video", InputAssetID: *input.InputAssetID, RightsAttestation: true})
	if err != nil {
		t.Fatal(err)
	}
	f.task = job.Job.ID
	f.requestID = job.RequestID
	if err := f.f.DB.Table("ai_gateway_tasks").Select("version_no").Where("public_id=?", f.task).Scan(&f.version).Error; err != nil {
		t.Fatal(err)
	}
}

func adminCancelAtomicSnapshot(t *testing.T, f adminCancelErrorFixture) []byte {
	t.Helper()
	facts := map[string]any{"finance": json.RawMessage(f.f.FinancialSnapshot())}
	for _, table := range []string{"ai_gateway_tasks", "ai_gateway_task_events", "ai_gateway_task_inputs", "ai_gateway_input_assets"} {
		var rows []map[string]any
		if err := f.f.DB.Table(table).Where("user_id=?", f.f.ProjectID).Order("id").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		facts[table] = rows
	}
	// 不只比较数量；原因密文、命令版本、审计摘要及原引用内容也必须保持不变。
	var commands, audits []map[string]any
	if err := f.f.DB.Table("ai_video_admin_cancellation_commands").Where("actor_user_id=?", f.actor).Order("actor_user_id,command_key_hash").Find(&commands).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Table("audit_logs").Where("operator_id=?", f.actor).Order("id").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	facts["commands"], facts["audits"] = commands, audits
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVideoG6AdminCancelAtomicWritesMySQL(t *testing.T) {
	for _, tc := range []struct {
		point string
		i2v   bool
	}{{"before", false}, {"after", false}, {"command", false}, {"command", true}} {
		t.Run(fmt.Sprintf("%s-i2v-%t", tc.point, tc.i2v), func(t *testing.T) {
			f := newAdminCancelErrorFixture(t)
			if tc.i2v {
				adminCancelI2VFixture(t, &f)
			}
			srv := f.server(t, "g6-admin-atomic-v1", f.secret)
			before := adminCancelAtomicSnapshot(t, f)
			var hits atomic.Int64
			const hook = "g6-admin-atomic-create-failure"
			if err := f.f.DB.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
				fail := tc.point == "command" && tx.Statement.Table == "ai_video_admin_cancellation_commands"
				if tc.point != "command" && tx.Statement.Table == "audit_logs" {
					raw, _ := json.Marshal(tx.Statement.Dest)
					fail = bytes.Contains(raw, []byte("video_admin_cancel_"+tc.point))
				}
				if fail {
					hits.Add(1)
					tx.AddError(errors.New("合成取消写入失败"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { f.f.DB.Callback().Create().Remove(hook) })
			body := []byte(fmt.Sprintf(`{"reason":"合成取消原子性","version_no":%d}`, f.version))
			f.call(t, srv, body, 503)
			f.f.DB.Callback().Create().Remove(hook)
			commands, audits := f.counts(t)
			if hits.Load() != 1 || commands != 0 || audits != 0 || !bytes.Equal(before, adminCancelAtomicSnapshot(t, f)) {
				t.Fatal("任一写点失败必须回滚前后审计、任务/租约/资金和命令")
			}
			f.call(t, srv, body, 200)
			commands, audits = f.counts(t)
			if commands != 1 || audits != 2 || f.f.SubmitCalls() != 0 {
				t.Fatal("修复边界后同命令只能成功一次且不提交Provider")
			}
		})
	}
}

// 只模拟数据库真实提交成功之后确认丢失，事务内容、原服务和后续重放均执行真实逻辑。
type adminCancelAckPool struct {
	gorm.ConnPool
	lost atomic.Bool
}

func (p *adminCancelAckPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &adminCancelAckTx{ConnPool: tx, tx: tx, p: p}, nil
}

type adminCancelAckTx struct {
	gorm.ConnPool
	tx *sql.Tx
	p  *adminCancelAckPool
}

func (t *adminCancelAckTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.p.lost.CompareAndSwap(false, true) {
		return errors.New("合成已提交但确认丢失")
	}
	return nil
}
func (t *adminCancelAckTx) Rollback() error { return t.tx.Rollback() }

func TestVideoG6AdminCancelCommitUnknownMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	srv := f.server(t, "g6-admin-commit-v1", f.secret)
	pool := &adminCancelAckPool{ConnPool: f.f.DB.ConnPool}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.f.UseApplicationDB(db))
	body := []byte(fmt.Sprintf(`{"reason":"合成提交确认未知","version_no":%d}`, f.version))
	f.call(t, srv, body, 503)
	commands, audits := f.counts(t)
	if !pool.lost.Load() || commands != 1 || audits != 2 {
		t.Fatal("必须实际提交原取消与审计后才丢失确认")
	}
	before := adminCancelAtomicSnapshot(t, f)
	reply := f.call(t, srv, body, 200)
	if !reply.Idempotent || reply.CancellationResult != "cancelled" || reply.RequestID != f.requestID {
		t.Fatal("提交未知后必须返回原业务的幂等取消结果")
	}
	commands, audits = f.counts(t)
	if commands != 1 || audits != 2 || !bytes.Equal(before, adminCancelAtomicSnapshot(t, f)) || f.f.SubmitCalls() != 0 {
		t.Fatal("提交未知恢复必须读原回执，不能重复退款、审计或创建任务")
	}
}
