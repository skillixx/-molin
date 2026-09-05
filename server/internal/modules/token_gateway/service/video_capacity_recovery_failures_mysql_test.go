package service

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	auditmodel "molin/server/internal/modules/audit/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type videoCapacityDBSnapshot struct {
	Guard  map[string]any
	Audits []auditmodel.AuditLog
}

func captureVideoCapacityDB(t *testing.T, db *gorm.DB) videoCapacityDBSnapshot {
	t.Helper()
	var result videoCapacityDBSnapshot
	if err := db.Table("ai_video_queue_admission_guard").Where("id=1").Take(&result.Guard).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("module='token_gateway' AND target_type='video_capacity_domain'").Order("id").Find(&result.Audits).Error; err != nil {
		t.Fatal(err)
	}
	return result
}
func currentVideoCapacity(t *testing.T, repo *repository.VideoCapacityRecoveryRepository) *repository.VideoCapacityRecoveryState {
	t.Helper()
	state, err := repo.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func verifyVideoG7CapacityRecoverySQL(t *testing.T, db *gorm.DB, repo *repository.VideoCapacityRecoveryRepository, hash, redisID string, f videoG5ReservationFixture) {
	ctx := context.Background()
	state := currentVideoCapacity(t, repo)
	proof, err := repo.Begin(ctx, state.Epoch, "sql-recovery", hash, redisID)
	if err != nil {
		t.Fatal(err)
	}
	before := captureVideoCapacityDB(t, db)
	statements := []string{
		"UPDATE ai_video_queue_admission_guard SET capacity_epoch=capacity_epoch+1,version_no=version_no+1 WHERE id=1",
		"UPDATE ai_video_queue_admission_guard SET capacity_epoch=0,version_no=version_no+1 WHERE id=1",
		"UPDATE ai_video_queue_admission_guard SET capacity_recovery_owner='wrong-owner',version_no=version_no+1 WHERE id=1",
		"UPDATE ai_video_queue_admission_guard SET capacity_token_sha256=REPEAT('b',64),version_no=version_no+1 WHERE id=1",
		"UPDATE ai_video_queue_admission_guard SET capacity_redis_run_id=REPEAT('b',40),version_no=version_no+1 WHERE id=1",
		"UPDATE ai_video_queue_admission_guard SET capacity_state='ready',version_no=version_no+1 WHERE id=1",
		"UPDATE ai_video_queue_admission_guard SET capacity_lease_until=capacity_lease_until+INTERVAL 1 SECOND,version_no=version_no+1 WHERE id=1",
		"UPDATE ai_video_queue_admission_guard SET capacity_state='blocked' WHERE id=1",
		"DELETE FROM ai_video_queue_admission_guard WHERE id=1",
		"INSERT INTO ai_video_queue_admission_guard(id,version_no,updated_at,capacity_epoch) VALUES(2,1,UTC_TIMESTAMP(6),1)",
	}
	rollback := errors.New("仅回滚本轮SQL守卫负例")
	for index, statement := range statements {
		var observed error
		err := db.Transaction(func(tx *gorm.DB) error { observed = tx.Exec(statement).Error; return rollback })
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		var mysqlError *drivermysql.MySQLError
		if !errors.As(observed, &mysqlError) || (mysqlError.Number != 1644 && mysqlError.Number != 3819) {
			t.Fatalf("SQL守卫负例%d没有实际拒绝: %v", index, observed)
		}
	}
	if !reflect.DeepEqual(before, captureVideoCapacityDB(t, db)) {
		t.Fatal("SQL拒绝不能改写门闩和审计")
	}
	// uninitialized时旧G6仍兼容；进入recovering后必须由持久cutoff整笔拒绝，不能绕过新快照。
	if err := db.Transaction(func(tx *gorm.DB) error { return NewMySQLVideoQueueAdmission().AdmitTx(tx, f.owner, f.command.TaskID) }); !errors.Is(err, ErrVideoGovernanceUnavailable) {
		t.Fatalf("恢复期间旧G6准入必须失败关闭: %v", err)
	}
	if err := repo.Block(ctx, proof); err != nil {
		t.Fatal(err)
	}
}

func verifyVideoG7CapacityRecoveryFailures(t *testing.T, db *gorm.DB, repo *repository.VideoCapacityRecoveryRepository, hash, redisID string) {
	ctx := context.Background()
	for _, action := range []string{"claimed", "blocked"} {
		state := currentVideoCapacity(t, repo)
		var proof *repository.VideoCapacityRecoveryLease
		if action == "blocked" {
			var err error
			proof, err = repo.Begin(ctx, state.Epoch, "audit-block", hash, redisID)
			if err != nil {
				t.Fatal(err)
			}
		}
		before := captureVideoCapacityDB(t, db)
		var hits atomic.Int64
		hook := "g7:capacity_audit_failure"
		if err := db.Callback().Create().Before("gorm:create").Register(hook, func(tx *gorm.DB) {
			entry, ok := tx.Statement.Dest.(*auditmodel.AuditLog)
			if ok && tx.Statement.Table == "audit_logs" && entry.Action == "video_capacity_recovery_"+action {
				hits.Add(1)
				tx.AddError(errors.New("合成恢复审计写入失败"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := db.Callback().Create().Remove(hook); err != nil {
				t.Error(err)
			}
		})
		var err error
		if action == "claimed" {
			proof, err = repo.Begin(ctx, state.Epoch, "audit-claim", hash, redisID)
			if proof != nil {
				t.Fatal("失败事务不得返回证明")
			}
		} else {
			err = repo.Block(ctx, proof)
		}
		if removeErr := db.Callback().Create().Remove(hook); removeErr != nil {
			t.Fatal(removeErr)
		}
		if !errors.Is(err, repository.ErrVideoCapacityRecoveryUnavailable) || hits.Load() != 1 {
			t.Fatalf("必须实际命中审计失败: hits=%d err=%v", hits.Load(), err)
		}
		if !reflect.DeepEqual(before, captureVideoCapacityDB(t, db)) {
			t.Fatal("审计失败须回滚全部门闩和审计写入")
		}
		if action == "blocked" {
			if err := repo.Validate(ctx, proof); err != nil {
				t.Fatal("阻断失败后原证明应仍有效")
			}
			if err := repo.Block(ctx, proof); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := captureVideoCapacityDB(t, db)
	state := currentVideoCapacity(t, repo)
	for _, variant := range []string{"plain", "context", "new_session", "prepared"} {
		if err := db.Transaction(func(tx *gorm.DB) error {
			switch variant {
			case "context":
				tx = tx.WithContext(ctx)
			case "new_session":
				tx = tx.Session(&gorm.Session{NewDB: true})
			case "prepared":
				tx = tx.Session(&gorm.Session{PrepareStmt: true})
			}
			proof, err := repository.NewVideoCapacityRecoveryRepository(tx).Begin(ctx, state.Epoch, "nested", hash, redisID)
			if proof != nil || !errors.Is(err, repository.ErrVideoCapacityRecoveryUnavailable) {
				t.Fatalf("%s事务包装不能借出未提交证明: %v", variant, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, captureVideoCapacityDB(t, db)) {
		t.Fatal("嵌套事务拒绝必须零写入")
	}
	prepared := repository.NewVideoCapacityRecoveryRepository(db.Session(&gorm.Session{PrepareStmt: true}))
	proof, err := prepared.Begin(ctx, state.Epoch, "prepared-root", hash, redisID)
	if err != nil {
		t.Fatalf("合法根连接的PreparedStmt应允许: %v", err)
	}
	if err := prepared.Block(ctx, proof); err != nil {
		t.Fatal(err)
	}
}

func verifyVideoG7CapacityRecoveryTail(t *testing.T, db *gorm.DB, repo *repository.VideoCapacityRecoveryRepository, hash, redisID string) {
	ctx := context.Background()
	state := currentVideoCapacity(t, repo)
	proof, err := repo.Begin(ctx, state.Epoch, "tail-recovery", hash, redisID)
	if err != nil {
		t.Fatal(err)
	}
	before := captureVideoCapacityDB(t, db)
	deadline := proof.Deadline()
	if time.Until(deadline) < 3*time.Second {
		t.Fatal("准备阶段耗尽观察窗")
	}
	time.Sleep(time.Until(deadline) - 2*time.Second)
	var hits atomic.Int64
	hook := "g7:capacity_audit_tail"
	if err := db.Callback().Create().After("gorm:create").Register(hook, func(tx *gorm.DB) {
		entry, ok := tx.Statement.Dest.(*auditmodel.AuditLog)
		if tx.Error == nil && ok && entry.Action == "video_capacity_recovery_blocked" {
			hits.Add(1)
			time.Sleep(time.Until(deadline) + 100*time.Millisecond)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Create().Remove(hook); err != nil {
			t.Error(err)
		}
	})
	err = repo.Block(ctx, proof)
	if removeErr := db.Callback().Create().Remove(hook); removeErr != nil {
		t.Fatal(removeErr)
	}
	if !errors.Is(err, repository.ErrVideoCapacityRecoveryLost) || errors.Is(err, context.DeadlineExceeded) || hits.Load() != 1 {
		t.Fatalf("实际写入后尾部失权必须回滚: hits=%d err=%v", hits.Load(), err)
	}
	if !reflect.DeepEqual(before, captureVideoCapacityDB(t, db)) {
		t.Fatal("到期回滚必须保留原元数据和审计全集")
	}
	next, err := repo.Begin(ctx, proof.Epoch(), "tail-takeover", hash, redisID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Block(ctx, next); err != nil {
		t.Fatal(err)
	}
}

// 复用已验收的真实COMMIT后丢确认连接包装，只改变故障命中标记，不替换仓储/事务逻辑。
type videoCapacityUnknownCommitPool struct{ *videoBudgetCommitPool }

func (p *videoCapacityUnknownCommitPool) BeginTx(ctx context.Context, options *sql.TxOptions) (gorm.ConnPool, error) {
	transaction, err := p.videoBudgetCommitPool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &videoCapacityUnknownCommitTx{videoBudgetCommitTx: transaction.(*videoBudgetCommitTx)}, nil
}

type videoCapacityUnknownCommitTx struct{ *videoBudgetCommitTx }

func (t *videoCapacityUnknownCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	// 只在真实DML事务提交后丢回执；只读Current/Validate不能提前消耗故障。
	lower := strings.ToLower(strings.TrimSpace(query))
	if strings.HasPrefix(lower, "insert ") || strings.HasPrefix(lower, "update ") || strings.HasPrefix(lower, "delete ") {
		t.budgetWrite = true
	}
	return t.videoBudgetCommitTx.ExecContext(ctx, query, args...)
}
func verifyVideoG7CapacityRecoveryCommitUnknown(t *testing.T, db *gorm.DB, repo *repository.VideoCapacityRecoveryRepository, hash, redisID string) {
	ctx := context.Background()
	state := currentVideoCapacity(t, repo)
	pool := &videoCapacityUnknownCommitPool{&videoBudgetCommitPool{ConnPool: db.ConnPool}}
	faultDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: db.Logger})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := repository.NewVideoCapacityRecoveryRepository(faultDB).Begin(ctx, state.Epoch, "commit-unknown", hash, redisID)
	if proof != nil || !errors.Is(err, repository.ErrVideoCapacityRecoveryUnavailable) || !pool.lost.Load() {
		t.Fatalf("真实提交后丢确认不能返回证明: lost=%v err=%v", pool.lost.Load(), err)
	}
	committed := currentVideoCapacity(t, repo)
	if committed.Epoch != state.Epoch+1 || committed.State != "recovering" {
		t.Fatal("COMMIT未知不得清除已经持久化的恢复占用")
	}
	if _, err := repo.Begin(ctx, committed.Epoch, "unknown-retry", hash, redisID); !errors.Is(err, repository.ErrVideoCapacityRecoveryBusy) {
		t.Fatalf("结果未知必须保留恢复租约，不立即另开: %v", err)
	}
}
