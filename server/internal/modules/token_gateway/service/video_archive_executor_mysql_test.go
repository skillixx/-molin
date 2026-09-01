package service_test

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6ArchiveExecutorMySQL(t *testing.T) {
	for _, i2v := range []bool{false, true} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("i2v_%t_pending_%t", i2v, pending), func(t *testing.T) {
				f := newAdminCancelErrorFixture(t)
				if i2v {
					adminCancelI2VFixture(t, &f)
				}
				f.f.PrepareArchive(f.task)
				owner := repository.VideoOwner{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: &f.f.ProjectID}
				repo := repository.NewVideoTaskRepository(f.f.DB)
				task, err := repo.FindForOwner(context.Background(), f.task, owner)
				if err != nil {
					t.Fatal(err)
				}
				if pending {
					task, err = repo.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: f.task, Owner: owner, ExpectedVersion: task.VersionNo, ToStatus: "pending_reconcile", Progress: task.Progress, EventID: fmt.Sprintf("vg6_archive_executor_pending_%d", f.f.ProjectID), Source: "worker", Now: time.Now().UTC()})
					if err != nil {
						t.Fatal(err)
					}
				}
				proof, _, err := repo.ClaimArchiveFence(context.Background(), repository.VideoArchiveFenceClaim{TaskPublicID: f.task, Owner: owner, ExpectedVersion: task.VersionNo, InitialPhase: "fetching", Now: time.Now().UTC()})
				if err != nil {
					t.Fatal(err)
				}
				caller, err := f.f.JWT.Authenticate(context.Background(), f.token)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.f.DB.Table("users").Where("id=?", f.f.ProjectID).Update("status", "disabled").Error; err != nil {
					t.Fatal(err)
				}
				if err := f.f.DB.Table("api_keys").Where("id=?", f.f.ProjectID).Update("status", "revoked").Error; err != nil {
					t.Fatal(err)
				}
				before := f.f.FinancialSnapshot()
				submits := f.f.SubmitCalls()
				if err := f.f.RunArchiveRecovery(context.Background(), caller, f.task, proof); err != nil {
					t.Fatal(err)
				}
				after, err := repo.FindForOwner(context.Background(), f.task, owner)
				if err != nil {
					t.Fatal(err)
				}
				if after.Status != "succeeded" || after.BillingStatus != "held" || after.DeliveryStatus != "pending" || after.ArchiveTokenHash != nil {
					t.Fatal("只允许执行成功并退让围栏，未结算不得交付")
				}
				delta := uint64(4)
				if pending {
					delta = 1
				}
				if !adminOnlyExecutionChanged(t, before, f.f.FinancialSnapshot(), f.requestID, "succeeded", delta) {
					t.Fatal("归档不得结算、释放或修改原Quote/Usage/Outbox")
				}
				facts := f.f.InspectMedia(f.task)
				if len(facts) != 6 {
					t.Fatal("必须形成原六角色资产")
				}
				for _, fact := range facts {
					if !fact.Present || fact.Deleted || !fact.HashMatches {
						t.Fatal("原对象及hash必须实际可核验")
					}
				}
				if f.f.SubmitCalls() != submits {
					t.Fatal("恢复不得重新生成")
				}
				var leases int64
				if err := f.f.DB.Table("ai_gateway_task_inputs").Where("task_id=? AND lease_released_at IS NULL", after.ID).Count(&leases).Error; err != nil {
					t.Fatal(err)
				}
				if (i2v && leases != 1) || (!i2v && leases != 0) {
					t.Fatal("未结算仍须保护原I2V输入租约")
				}
			})
		}
	}
}

func TestVideoG6ArchiveExecutorFinalAuthorizationMySQL(t *testing.T) {
	f := newAdminCancelErrorFixture(t)
	f.f.PrepareArchive(f.task)
	owner := repository.VideoOwner{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: &f.f.ProjectID}
	repo := repository.NewVideoTaskRepository(f.f.DB)
	task, err := repo.FindForOwner(context.Background(), f.task, owner)
	if err != nil {
		t.Fatal(err)
	}
	proof, _, err := repo.ClaimArchiveFence(context.Background(), repository.VideoArchiveFenceClaim{TaskPublicID: f.task, Owner: owner, ExpectedVersion: task.VersionNo, InitialPhase: "fetching", Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := f.f.JWT.Authenticate(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(4 * time.Second).Truncate(time.Second)
	if err := f.f.DB.Table("user_permission_overrides").Where("user_id=? AND permission_code='ai_gateway:task_manage'", f.actor).Update("expires_at", expiry).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.DB.Table("user_permission_overrides").Select("expires_at").Where("user_id=? AND permission_code='ai_gateway:task_manage'", f.actor).Scan(&expiry).Error; err != nil {
		t.Fatal(err)
	}
	var hit atomic.Bool
	var crossed atomic.Bool
	const hook = "g6_archive_final_release_wait"
	if err := f.f.DB.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		row, ok := tx.Statement.Dest.(*repository.VideoTaskRecord)
		if !ok || tx.Error != nil || tx.RowsAffected != 1 || row.PublicID != f.task || row.Status != "succeeded" || row.ArchiveTokenHash != nil || hit.Swap(true) {
			return
		}
		valid := time.Now().UTC().Before(expiry)
		if delay := time.Until(expiry.Add(100 * time.Millisecond)); delay > 0 {
			time.Sleep(delay)
		}
		crossed.Store(valid && !time.Now().UTC().Before(expiry))
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.f.DB.Callback().Query().Remove(hook) })
	before := f.f.FinancialSnapshot()
	err = f.f.RunArchiveRecovery(context.Background(), caller, f.task, proof)
	f.f.DB.Callback().Query().Remove(hook)
	if !errors.Is(err, service.ErrVideoAdminForbidden) || !hit.Load() || !crossed.Load() {
		t.Fatal("必须在成功事务释放围栏之后跨权限期限并拒绝提交")
	}
	after, err := repo.FindForOwner(context.Background(), f.task, owner)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "labeling" || after.ArchiveTokenHash == nil || after.ArchivePhase == nil || *after.ArchivePhase != "labeling" {
		t.Fatal("最终成功及围栏释放必须整体回滚")
	}
	var finalized int64
	if err := f.f.DB.Table("ai_gateway_task_events").Where("task_id=? AND (event_type='archive_fence_released' OR to_status='succeeded')", after.ID).Count(&finalized).Error; err != nil || finalized != 0 {
		t.Fatal("回滚不能留下成功或围栏释放事件")
	}
	if !adminOnlyExecutionChanged(t, before, f.f.FinancialSnapshot(), f.requestID, "running", 3) {
		t.Fatal("只能保留前三个已提交技术步骤，不得提前结算或丢失资金事实")
	}
}
