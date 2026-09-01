package service_test

import (
	"bytes"
	"context"
	"errors"
	"gorm.io/gorm"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
)

func TestVideoG6ArchiveFenceMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	f.f.PrepareArchive(f.task)
	owner := repository.VideoOwner{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: &f.f.ProjectID}
	repo := repository.NewVideoTaskRepository(f.f.DB)
	task, err := repo.FindForOwner(context.Background(), f.task, owner)
	if err != nil || task.Status != "fetching" {
		t.Fatal("必须经原Fake链取得真实归档状态")
	}
	before := f.f.FinancialSnapshot()
	now := time.Now().UTC()
	var wins atomic.Int32
	var bad atomic.Int32
	var mutex sync.Mutex
	var proof *repository.VideoArchiveFenceProof
	var claimed *repository.VideoTaskRecord
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			grant, row, e := repo.ClaimArchiveFence(context.Background(), repository.VideoArchiveFenceClaim{TaskPublicID: f.task, Owner: owner, ExpectedVersion: task.VersionNo, InitialPhase: "fetching", Now: now})
			if e == nil {
				wins.Add(1)
				mutex.Lock()
				proof, claimed = grant, row
				mutex.Unlock()
			} else if !errors.Is(e, repository.ErrVideoTaskConflict) {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || bad.Load() != 0 || proof == nil || claimed == nil {
		t.Fatalf("100认领必须唯一且其余版本冲突：winner=%d unexpected=%d", wins.Load(), bad.Load())
	}
	if claimed.Status != "fetching" || claimed.VersionNo != task.VersionNo+1 || !bytes.Equal(before, f.f.FinancialSnapshot()) {
		t.Fatal("认领只加围栏，不改业务三轴或财务")
	}
	if err := f.f.TryArchive(f.task); err == nil {
		t.Fatal("普通旧Worker不能在管理围栏下继续归档")
	}
	if _, err := repo.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: f.task, Owner: owner, ExpectedVersion: claimed.VersionNo, ToStatus: "storing", Progress: claimed.Progress, EventID: "vg6_archive_unproven_writer", Source: "worker", Now: now}); !errors.Is(err, repository.ErrVideoTaskConflict) {
		t.Fatal("只知道最新Task版本也不能绕过围栏")
	}
	// 持有证明的管理协调器可安全停止到待核对，但不能用技术步骤回退主状态。
	pending, err := repo.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: f.task, Owner: owner, ExpectedVersion: claimed.VersionNo, ToStatus: "pending_reconcile", Progress: claimed.Progress, EventID: "vg6_archive_hold_pending", Source: "worker", Now: now, ArchiveFence: proof})
	if err != nil {
		t.Fatal(err)
	}
	phase, err := repo.AdvanceArchivePhase(context.Background(), f.task, owner, pending.VersionNo, proof, "storing", now)
	if err != nil {
		t.Fatal(err)
	}
	if phase.Status != "pending_reconcile" || phase.ArchivePhase == nil || *phase.ArchivePhase != "storing" {
		t.Fatal("技术phase推进不能回退原Task")
	}
	if _, err := repo.AdvanceArchivePhase(context.Background(), f.task, owner, phase.VersionNo, proof, "verified", now); !errors.Is(err, repository.ErrVideoTaskTransition) {
		t.Fatal("不得跳过审核/标识技术步骤")
	}
	// 使用显式夹具时钟验证过期代次，不能冒充实际等待两分钟或外部IO回收。
	later := now.Add(3 * time.Minute)
	repo = repo.WithArchiveClock(func() time.Time { return later })
	if err := repository.CheckVideoArchiveFence(phase, proof, later); err == nil {
		t.Fatal("过期令牌不得写回")
	}
	next, adopted, err := repo.ClaimArchiveFence(context.Background(), repository.VideoArchiveFenceClaim{TaskPublicID: f.task, Owner: owner, ExpectedVersion: phase.VersionNo, InitialPhase: "storing", Now: later})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AdvanceArchivePhase(context.Background(), f.task, owner, adopted.VersionNo, proof, "moderating", later); !errors.Is(err, repository.ErrVideoTaskConflict) {
		t.Fatal("旧代次不得借最新Task版本写回")
	}
	if _, err := repo.AdvanceArchivePhase(context.Background(), f.task, owner, adopted.VersionNo, next, "moderating", later); err != nil {
		t.Fatal(err)
	}
	current, err := repo.FindForOwner(context.Background(), f.task, owner)
	if err != nil {
		t.Fatal(err)
	}
	released, err := repo.ReleaseArchiveFence(context.Background(), f.task, owner, current.VersionNo, next, later)
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != "pending_reconcile" || released.ArchiveTokenHash != nil || released.ArchivePhase != nil {
		t.Fatal("退让只能保留真实待核对状态，不可复活旧执行")
	}
	if err := repository.CheckVideoArchiveFence(released, next, later); err == nil {
		t.Fatal("已退让证明不能再次使用")
	}
	if _, err := repo.ReleaseArchiveFence(context.Background(), f.task, owner, released.VersionNo, nil, later); !errors.Is(err, repository.ErrVideoTaskConflict) {
		t.Fatal("无围栏且无证明必须返回冲突，不得panic或写事件")
	}
	var events int64
	if err := f.f.DB.Table("ai_gateway_task_events").Where("task_id=? AND event_type LIKE 'archive_%'", task.ID).Count(&events).Error; err != nil || events != 5 {
		t.Fatal("认领/技术推进/退让必须追加唯一可追溯事件")
	}
}

func TestVideoG6ArchiveFenceExpiryMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	f.f.PrepareArchive(f.task)
	owner := repository.VideoOwner{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: &f.f.ProjectID}
	repo := repository.NewVideoTaskRepository(f.f.DB)
	task, err := repo.FindForOwner(context.Background(), f.task, owner)
	if err != nil {
		t.Fatal(err)
	}
	proof, before, err := repo.ClaimArchiveFence(context.Background(), repository.VideoArchiveFenceClaim{TaskPublicID: f.task, Owner: owner, ExpectedVersion: task.VersionNo, InitialPhase: "fetching", Now: time.Now().UTC(), LeaseDuration: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	block := f.f.DB.Begin()
	if block.Error != nil {
		t.Fatal(block.Error)
	}
	defer block.Rollback()
	if _, err := repo.LockForOwnerTx(block, f.task, owner); err != nil {
		t.Fatal(err)
	}
	type waitKey struct{}
	entered := make(chan struct{}, 1)
	const hook = "g6_archive_fence_wait_before_query"
	if err := f.f.DB.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tagged, _ := tx.Statement.Context.Value(waitKey{}).(bool); tagged && tx.Statement.Table == "tasks" {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.f.DB.Callback().Query().Remove(hook) })
	result := make(chan error, 1)
	callTime := time.Now().UTC()
	go func() {
		_, err := repo.AdvanceArchivePhase(context.WithValue(context.Background(), waitKey{}, true), f.task, owner, before.VersionNo, proof, "storing", callTime)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("等待方必须进入原Task锁查询")
	}
	if before.ArchiveLeaseUntil == nil || !time.Now().UTC().Before(*before.ArchiveLeaseUntil) {
		t.Fatal("必须从有效租约进入锁等待")
	}
	if delay := time.Until(before.ArchiveLeaseUntil.Add(30 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	if err := block.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, repository.ErrVideoTaskConflict) {
			t.Fatalf("跨期锁等待必须拒绝：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("锁释放后应有界结束")
	}
	f.f.DB.Callback().Query().Remove(hook)
	after, err := repo.FindForOwner(context.Background(), f.task, owner)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("过期写入不能改变原Task或技术phase")
	}
}
