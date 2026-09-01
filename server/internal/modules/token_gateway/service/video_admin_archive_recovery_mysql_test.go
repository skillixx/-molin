package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

type videoArchiveCommitPool struct {
	gorm.ConnPool
	lost atomic.Bool
}

func (p *videoArchiveCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoArchiveCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoArchiveCommitTx struct {
	gorm.ConnPool
	tx          *sql.Tx
	pool        *videoArchiveCommitPool
	resultWrite bool
}

func (t *videoArchiveCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	lower := strings.ToLower(query)
	if strings.Contains(lower, "ai_video_admin_archive_commands") && strings.Contains(lower, "update") {
		t.resultWrite = true
	}
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *videoArchiveCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.resultWrite && t.pool.lost.CompareAndSwap(false, true) {
		return errors.New("合成归档结果COMMIT确认丢失")
	}
	return nil
}
func (t *videoArchiveCommitTx) Rollback() error { return t.tx.Rollback() }

type adminArchiveRecoveryFixture struct {
	base    adminCancelErrorFixture
	app     *service.VideoAdminService
	counter *archiveCountingContent
	caller  service.VideoCaller
	version uint64
}

func newAdminArchiveRecoveryFixture(t *testing.T) adminArchiveRecoveryFixture {
	t.Helper()
	f := newAdminCancelErrorFixture(t)
	f.f.PrepareArchive(f.task)
	owner := repository.VideoOwner{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: &f.f.ProjectID}
	task, err := repository.NewVideoTaskRepository(f.f.DB).FindForOwner(context.Background(), f.task, owner)
	if err != nil {
		t.Fatal(err)
	}
	options := f.f.ArchiveOptions()
	counter := &archiveCountingContent{VideoAdminArchiveContent: options.Content}
	options.Content = counter
	protector, err := service.NewVideoAdminReasonProtector("g6-archive-recovery-v1", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: protector, Archive: &options})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := f.f.JWT.Authenticate(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	return adminArchiveRecoveryFixture{base: f, app: app, counter: counter, caller: caller, version: task.VersionNo}
}

func (f adminArchiveRecoveryFixture) command(key string) service.VideoAdminArchiveCommand {
	return service.VideoAdminArchiveCommand{Caller: f.caller, TaskID: f.base.task, VersionNo: f.version, IdempotencyKey: key, Reason: "合成归档恢复"}
}

func (f adminArchiveRecoveryFixture) snapshot(t *testing.T) []byte {
	t.Helper()
	facts := map[string]any{"finance": json.RawMessage(f.base.f.FinancialSnapshot())}
	for _, table := range []string{"ai_gateway_tasks", "ai_gateway_task_events", "ai_gateway_assets", "ai_video_admin_archive_commands", "audit_logs"} {
		query := f.base.f.DB.Table(table)
		switch table {
		case "ai_gateway_tasks":
			query = query.Where("public_id=?", f.base.task)
		case "ai_gateway_task_events", "ai_gateway_assets", "ai_video_admin_archive_commands":
			query = query.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", f.base.task)
		case "audit_logs":
			query = query.Where("operator_id=? AND action LIKE 'video_admin_archive_%'", f.base.actor)
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

func TestVideoG6AdminArchiveConcurrentMySQL(t *testing.T) {
	f := newAdminArchiveRecoveryFixture(t)
	command := f.command("g6-admin-archive-concurrent")
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
			reply, err := f.app.RetryArchive(context.Background(), command)
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
			t.Fatalf("同键并发归档失败：reply=%+v err=%v", answer.reply, answer.err)
		}
		if commandID == "" {
			commandID = answer.reply.CommandID
		} else if commandID != answer.reply.CommandID {
			t.Fatal("同键并发产生不同归档命令")
		}
		if !answer.reply.Idempotent {
			first++
		}
	}
	if first != 1 || f.counter.opens.Load() != 3 {
		t.Fatalf("100同键必须一次命令和一套三阶段媒体读取：first=%d opens=%d", first, f.counter.opens.Load())
	}
	var commands, audits int64
	_ = f.base.f.DB.Table("ai_video_admin_archive_commands").Where("actor_user_id=?", f.base.actor).Count(&commands).Error
	_ = f.base.f.DB.Table("audit_logs").Where("operator_id=? AND action LIKE 'video_admin_archive_%'", f.base.actor).Count(&audits).Error
	if commands != 1 || audits != 2 || f.base.f.SubmitCalls() != 1 {
		t.Fatalf("并发归档事实必须唯一且不重Submit：commands=%d audits=%d submits=%d", commands, audits, f.base.f.SubmitCalls())
	}
}

func TestVideoG6AdminArchiveCommitUnknownMySQL(t *testing.T) {
	f := newAdminArchiveRecoveryFixture(t)
	pool := &videoArchiveCommitPool{ConnPool: f.base.f.DB.ConnPool}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.base.f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	restore := f.base.f.UseApplicationDB(db)
	command := f.command("g6-admin-archive-commit-unknown")
	result, commitErr := f.app.RetryArchive(context.Background(), command)
	if commitErr != nil || result == nil || result.Status != "completed" || !pool.lost.Load() || f.counter.opens.Load() != 3 {
		t.Fatalf("归档结果必须真实提交后由最外层恢复：result=%+v err=%v lost=%t opens=%d", result, commitErr, pool.lost.Load(), f.counter.opens.Load())
	}
	afterUnknown := f.snapshot(t)
	replay, err := f.app.RetryArchive(context.Background(), command)
	restore()
	if err != nil || replay == nil || !replay.Idempotent || replay.Status != "completed" || f.counter.opens.Load() != 3 {
		t.Fatalf("归档提交未知重放必须恢复原回执：reply=%+v err=%v opens=%d", replay, err, f.counter.opens.Load())
	}
	if !bytes.Equal(afterUnknown, f.snapshot(t)) || f.base.f.SubmitCalls() != 1 {
		t.Fatal("归档提交未知重放不得重复媒体IO、审计、事件或Submit")
	}
}
