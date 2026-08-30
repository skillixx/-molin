package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func videoG5PendingFixture(t *testing.T, db *gorm.DB) videoG5ReservationFixture {
	t.Helper()
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	runVideoG5ReadyFixture(t, f)
	f.service.fault = func(at string) error {
		if at == "settle_hold" {
			return errors.New("合成结算故障")
		}
		return nil
	}
	if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err == nil {
		t.Fatal("应形成待补偿")
	}
	f.service.fault = nil
	return f
}

// TestVideoG5CompensationMySQLLeaseCannotCrossRequest 有效租约也只能恢复它自己的请求，不能绕过另一活跃Worker。
func TestVideoG5CompensationMySQLLeaseCannotCrossRequest(t *testing.T) {
	db := openVideoG5MySQL(t)
	a, b := videoG5PendingFixture(t, db), videoG5PendingFixture(t, db)
	r := repository.NewVideoCompensationRepository(db)
	if _, err := r.Claim(context.Background(), a.command.RequestID, "worker-a"); err != nil {
		t.Fatal(err)
	}
	lease, err := r.Claim(context.Background(), b.command.RequestID, "worker-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.service.RecoverSettlement(context.Background(), a.command.TaskID, a.owner, *lease); !errors.Is(err, repository.ErrVideoCompensationLeaseLost) {
		t.Fatalf("B的租约不能用于A的结算: %v", err)
	}
	var w billingmodel.Wallet
	if err := db.Where("user_id=?", a.owner.UserID).First(&w).Error; err != nil {
		t.Fatal(err)
	}
	if w.FrozenAmount.StringFixed(8) != "0.50000000" {
		t.Fatal("跨请求租约不得解冻或消费A")
	}
}

// TestVideoG5CompensationMySQLExpiredDuringSettlement 租约在财务事务中途过期，全部财务写入回滚，后继围栏可恢复。
func TestVideoG5CompensationMySQLExpiredDuringSettlement(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := videoG5PendingFixture(t, db)
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	f.service.now = func() time.Time { return now }
	f.service.fault = func(at string) error {
		if at == "settle_outbox" {
			now = now.Add(repository.VideoCompensationLeaseDuration + time.Second)
		}
		return nil
	}
	w, err := NewVideoCompensationWorker(f.service, "expiry-worker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.RunOne(context.Background(), f.command.RequestID); !errors.Is(err, repository.ErrVideoCompensationLeaseLost) {
		t.Fatalf("过期围栏必须回滚: %v", err)
	}
	var wallet billingmodel.Wallet
	if err := db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil {
		t.Fatal(err)
	}
	if wallet.FrozenAmount.StringFixed(8) != "0.50000000" {
		t.Fatal("过期执行器不能消费")
	}
	f.service.fault = nil
	result, err := w.RunOne(context.Background(), f.command.RequestID)
	if err != nil || result.Financial == nil || result.Financial.BillingStatus != model.AIBillingSettled {
		t.Fatalf("后继租约应恢复: %v", err)
	}
}

// TestVideoG5CompensationMySQLMarkerAtomicity pending标记和两条事件任一失败均回滚，返回错误但不丢失原Hold。
func TestVideoG5CompensationMySQLMarkerAtomicity(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, point := range []string{"compensation_pending_outbox", "compensation_required_outbox"} {
		t.Run(point, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			runVideoG5ReadyFixture(t, f)
			f.service.fault = func(at string) error {
				if at == "settle_hold" || at == point {
					return errors.New("合成标记故障")
				}
				return nil
			}
			if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("标记失败不得吞掉错误")
			}
			var jobs int64
			if err := db.Model(&model.VideoCompensationTask{}).Where("aggregate_id=?", f.command.RequestID).Count(&jobs).Error; err != nil || jobs != 0 {
				t.Fatalf("标记失败不应留半个job: %v", err)
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || task.BillingStatus != model.AIBillingHeld {
				t.Fatalf("pending应一并回滚: %v", err)
			}
			var events int64
			if err := db.Model(&model.AIOutboxEvent{}).Where("aggregate_id=?", f.command.RequestID).Count(&events).Error; err != nil || events != 1 {
				t.Fatalf("只应保留held事件: %v", err)
			}
			f.service.fault = nil
			if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestVideoG5CompensationMySQLRecoversFinancialFailure 结算失败落唯一补偿，Worker只凭已存事实恢复，不再次提交Provider。
func TestVideoG5CompensationMySQLRecoversFinancialFailure(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	prepareVideoG5I2V(t, &f)
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	_, adapter := runVideoG5ReadyFixture(t, f)
	f.service.fault = func(at string) error {
		if at == "settle_outbox" {
			return errors.New("合成结算故障")
		}
		return nil
	}
	if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err == nil {
		t.Fatal("首次故障应返回错误")
	}
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || task.BillingStatus != model.AIBillingSettlementPending {
		t.Fatalf("失败应持久化待结算: %v", err)
	}
	r := repository.NewVideoCompensationRepository(db)
	job, err := r.GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || job.Status != "pending" || job.AttemptCount != 0 {
		t.Fatalf("缺少唯一pending补偿: %v", err)
	}
	f.service.fault = nil
	worker, err := NewVideoCompensationWorker(f.service, "comp-fixture")
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.RunOne(context.Background(), f.command.RequestID)
	if err != nil || result.Financial == nil || result.Financial.BillingStatus != model.AIBillingSettled {
		t.Fatalf("补偿应恢复财务结算: %v", err)
	}
	job, err = r.GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || job.Status != "completed" || job.LastSafeErrorCode != nil || job.CompletedAt == nil {
		t.Fatalf("财务与交付完成后应原子completed: %v", err)
	}
	if adapter.SubmitCalls() != 1 {
		t.Fatal("补偿不能重新提交Provider")
	}
	var available int64
	if err := db.Model(&model.AIImageAsset{}).Where("request_id=? AND lifecycle_state='available'", f.command.RequestID).Count(&available).Error; err != nil || available != 6 {
		t.Fatalf("必须在统一门禁后原子交付六资产: %v", err)
	}
}

// TestVideoG5CompensationMySQLCrashLeaseFencingAndFinalReap 同worker重启也不能复用旧围栏，第8次崩溃后只能回收为dead。
func TestVideoG5CompensationMySQLCrashLeaseFencingAndFinalReap(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	r := repository.NewVideoCompensationRepository(db).WithClock(func() time.Time { return now })
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, _, err := r.EnsureTx(tx, f.command.TaskID, f.owner, "settlement_failed")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var first *repository.VideoCompensationLease
	for i := 0; i < 8; i++ {
		lease, err := r.Claim(context.Background(), f.command.RequestID, "same-worker")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = lease
		} else {
			if err := db.Transaction(func(tx *gorm.DB) error { _, err := r.CheckLeaseTx(tx, *first); return err }); !errors.Is(err, repository.ErrVideoCompensationLeaseLost) {
				t.Fatalf("旧围栏不得复活: %v", err)
			}
		}
		now = now.Add(repository.VideoCompensationLeaseDuration)
	}
	if _, err := r.Claim(context.Background(), f.command.RequestID, "same-worker"); !errors.Is(err, repository.ErrVideoCompensationNotReady) {
		t.Errorf("第8次崩溃后应回收并停止: %v", err)
	}
	item, err := r.GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "dead" || item.AttemptCount != 8 || item.LockedAt != nil {
		t.Fatal("第8次崩溃不能永远running或进入第9次")
	}
}

// TestVideoG5CompensationMySQLManualAudit 人工核对需双主体且事件只追加，直接SQL不得绕过审核历史。
func TestVideoG5CompensationMySQLManualAudit(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	checker := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	r := repository.NewVideoCompensationRepository(db).WithClock(func() time.Time { return now })
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, _, err := r.EnsureTx(tx, f.command.TaskID, f.owner, "facts_missing")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][2]uint64{{99999991, 99999992}, {f.owner.UserID, checker.owner.UserID}} {
		var updateErr error
		rollback := errors.New("只回滚SQL反例")
		if err := db.Transaction(func(tx *gorm.DB) error {
			updateErr = tx.Exec("UPDATE ai_compensation_tasks SET status='running',version_no=version_no+1,locked_at=?,locked_by='manual-sql',lease_mode='manual',review_maker_id=?,review_checker_id=? WHERE task_key=?", now, ids[0], ids[1], "video:"+f.command.RequestID).Error
			return rollback
		}); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		if updateErr == nil {
			t.Error("没有审核事件的manual租约不得由SQL建立")
		}
	}
	if _, err := r.ClaimManual(context.Background(), f.command.RequestID, "manual", f.owner.UserID, f.owner.UserID); !errors.Is(err, repository.ErrVideoCompensationReview) {
		t.Fatalf("同主体必须拒绝: %v", err)
	}
	for i := 0; i < 2; i++ {
		lease, err := r.ClaimManual(context.Background(), f.command.RequestID, "manual", f.owner.UserID, checker.owner.UserID)
		if err != nil {
			t.Fatal(err)
		}
		status := "manual_review"
		code := "facts_missing"
		if i == 1 {
			var sqlErr error
			rollback := errors.New("回滚未闭合completed反例")
			if err := db.Transaction(func(tx *gorm.DB) error {
				sqlErr = tx.Exec("UPDATE ai_compensation_tasks SET status='completed',completed_at=?,updated_at=?,locked_at=NULL,locked_by=NULL,lease_mode=NULL,last_error_class=NULL,last_safe_error_code=NULL,version_no=version_no+1 WHERE id=?", now, now, lease.ID).Error
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if sqlErr == nil {
				t.Fatal("直接SQL也不得提前completed")
			}
			if err := db.Transaction(func(tx *gorm.DB) error { return r.FinishTx(tx, *lease, "completed", "") }); !errors.Is(err, repository.ErrVideoCompensationNotReady) {
				t.Fatalf("财务交付未闭合不得completed: %v", err)
			}
		}
		if err := db.Transaction(func(tx *gorm.DB) error { return r.FinishTx(tx, *lease, status, code) }); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	var events []model.VideoCompensationReviewEvent
	if err := db.Where("user_id=? AND event_type='video_compensation_manual_claimed'", f.owner.UserID).Order("id ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ReviewMakerID != f.owner.UserID || events[0].ReviewCheckerID != checker.owner.UserID {
		t.Fatal("人工核对历史必须追加保留")
	}
	if _, err := r.Claim(context.Background(), f.command.RequestID, "worker"); !errors.Is(err, repository.ErrVideoCompensationNotReady) {
		t.Fatalf("人工待核对不能自动重入: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, _, err := r.EnsureTx(tx, f.command.TaskID, f.owner, "settlement_failed")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	item, err := r.GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || item.Status != "manual_review" || item.CompletedAt != nil || item.AttemptCount != 0 {
		t.Fatalf("Ensure不能重置人工待核对事实: %v", err)
	}
}

// TestVideoG5CompensationMySQLCompletedRequiresClosedFacts 已安全取消并完成释放的任务才能形成completed，重放不重置。
func TestVideoG5CompensationMySQLCompletedRequiresClosedFacts(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	r := repository.NewVideoCompensationRepository(db)
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, _, err := r.EnsureTx(tx, f.command.TaskID, f.owner, "facts_missing")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := r.Claim(context.Background(), f.command.RequestID, "closed-check")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return r.FinishTx(tx, *lease, "completed", "") }); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, _, err := r.EnsureTx(tx, f.command.TaskID, f.owner, "settlement_failed")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	job, err := r.GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || job.Status != "completed" || job.CompletedAt == nil || job.AttemptCount != 1 {
		t.Fatalf("完成事实不能重置: %v", err)
	}
}

// TestVideoG5CompensationMySQLLeaseAndEightAttemptLimit 同一补偿100次认领只能一个租约，失败第8次后停止自动执行。
func TestVideoG5CompensationMySQLLeaseAndEightAttemptLimit(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	r := repository.NewVideoCompensationRepository(db).WithClock(func() time.Time { return now })
	for i := 0; i < 2; i++ {
		if err := db.Transaction(func(tx *gorm.DB) error {
			_, _, err := r.EnsureTx(tx, f.command.TaskID, f.owner, "settlement_failed")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	var winners atomic.Int64
	var wg sync.WaitGroup
	var winner *repository.VideoCompensationLease
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := r.Claim(context.Background(), f.command.RequestID, "worker-fixture")
			if err == nil {
				winners.Add(1)
				mu.Lock()
				winner = lease
				mu.Unlock()
			} else if !errors.Is(err, repository.ErrVideoCompensationBusy) {
				t.Errorf("租约竞争异常: %v", err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("只允许一个补偿租约: %d", winners.Load())
	}
	if _, err := r.ClaimManual(context.Background(), f.command.RequestID, "manual-fixture", f.owner.UserID, f.owner.UserID+1); !errors.Is(err, repository.ErrVideoCompensationBusy) {
		t.Fatalf("人工不能抢占活跃租约: %v", err)
	}
	for attempt := uint32(1); attempt <= 8; attempt++ {
		if attempt > 1 {
			var err error
			winner, err = r.Claim(context.Background(), f.command.RequestID, "worker-fixture")
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Transaction(func(tx *gorm.DB) error { return r.FinishTx(tx, *winner, "retry", "facts_missing") }); err != nil {
			t.Fatal(err)
		}
		item, err := r.GetForTask(context.Background(), f.command.TaskID, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		if item.AttemptCount != attempt || item.LockedAt != nil || item.LockedBy != nil {
			t.Fatal("补偿计数或租约释放错误")
		}
		if attempt < 8 {
			if item.Status != "retry" || !item.NextRetryAt.After(now) {
				t.Fatal("应有有界退避")
			}
			now = item.NextRetryAt
		} else if item.Status != "dead" {
			t.Fatal("第8次必须dead")
		}
	}
	if _, err := r.Claim(context.Background(), f.command.RequestID, "worker-fixture"); !errors.Is(err, repository.ErrVideoCompensationNotReady) {
		t.Fatalf("dead不能自动第9次: %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return r.FinishTx(tx, *winner, "completed", "") }); !errors.Is(err, repository.ErrVideoCompensationLeaseLost) {
		t.Fatalf("旧租约不能完成已结束尝试: %v", err)
	}
}
