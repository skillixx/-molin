package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// TestVideoG7WorkerLeaseRollbackMySQL 在真实MySQL事件写入边界注入错误，证明租约及追加事实一起回滚。
func TestVideoG7WorkerLeaseRollbackMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, phase := range []string{"claim", "release"} {
		t.Run(phase, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
				t.Fatal(err)
			}
			leases := repository.NewVideoWorkerLeaseRepository(db)
			tasks := repository.NewVideoTaskRepository(db)
			events := repository.NewVideoTaskEventRepository(db)
			var proof *repository.VideoWorkerLease
			kind := "video_worker_lease_claimed"
			if phase == "release" {
				var err error
				proof, err = leases.Claim(ctx, f.command.TaskID, f.owner, "rollback-worker", "submit")
				if err != nil {
					t.Fatal(err)
				}
				kind = "video_worker_lease_released"
			}
			before, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			beforeEvents, err := events.ListForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
			// 只针对本次合成Task安装独立故障触发器；标识来自本地数值ID和固定类型，非客户输入。
			trigger := fmt.Sprintf("trg_vidg7_lease_fault_%d", before.ID)
			ddl := fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON ai_gateway_task_events FOR EACH ROW BEGIN IF NEW.task_id=%d AND BINARY NEW.event_type='%s' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic_worker_event_failure'; END IF; END", trigger, before.ID, kind)
			if err := db.Exec(ddl).Error; err != nil {
				t.Fatal(err)
			}
			installed := true
			t.Cleanup(func() {
				if installed {
					if err := db.Exec("DROP TRIGGER " + trigger).Error; err != nil {
						t.Errorf("清理本轮事件故障触发器失败: %v", err)
					}
				}
			})
			if phase == "claim" {
				if result, err := leases.Claim(ctx, f.command.TaskID, f.owner, "rollback-worker", "submit"); result != nil || !errors.Is(err, repository.ErrVideoWorkerLeaseUnavailable) {
					t.Fatalf("事件失败不能返回成功租约: %v", err)
				}
			} else if err := leases.Release(ctx, proof); !errors.Is(err, repository.ErrVideoWorkerLeaseUnavailable) {
				t.Fatalf("释放事件失败必须撤销释放: %v", err)
			}
			after, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("事件失败后Task包括租约字段必须原样保留: %v", err)
			}
			afterEvents, err := events.ListForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil || !reflect.DeepEqual(beforeEvents, afterEvents) {
				t.Fatalf("失败不能留下部分租约事件: %v", err)
			}
			if phase == "release" {
				if err := leases.Validate(ctx, proof); err != nil {
					t.Fatalf("回滚后原持有者仍须有效: %v", err)
				}
			}
			if err := db.Exec("DROP TRIGGER " + trigger).Error; err != nil {
				t.Fatal(err)
			}
			installed = false
			if phase == "claim" {
				proof, err = leases.Claim(ctx, f.command.TaskID, f.owner, "rollback-worker", "submit")
				if err != nil || proof.Version() != 1 {
					t.Fatalf("失败认领不能消耗代次: %v", err)
				}
			}
			if err := leases.Release(ctx, proof); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
				t.Fatal("技术租约失败与恢复不得改动八张财务/Quote/Outbox表")
			}
		})
	}
}

// TestVideoG7WorkerLeaseIsolationMySQL 错身份和取消context不能产生租约；通用事件入口不能制造证明。
func TestVideoG7WorkerLeaseIsolationMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVideoWorkerLeaseRepository(db)
	wrongKey := *f.owner.APIKeyID + 1
	for _, wrong := range []repository.VideoOwner{
		{UserID: f.owner.UserID + 1, ProjectID: f.owner.ProjectID, APIKeyID: f.owner.APIKeyID},
		{UserID: f.owner.UserID, ProjectID: f.owner.ProjectID + 1, APIKeyID: f.owner.APIKeyID},
		{UserID: f.owner.UserID, ProjectID: f.owner.ProjectID, APIKeyID: &wrongKey},
		{UserID: f.owner.UserID, ProjectID: f.owner.ProjectID},
	} {
		if proof, err := repo.Claim(ctx, f.command.TaskID, wrong, "wrong-owner", "submit"); proof != nil || !errors.Is(err, repository.ErrVideoTaskNotFound) {
			t.Fatalf("跨User/Project/Key或省略Key统一不存在: %v", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if proof, err := repo.Claim(cancelled, f.command.TaskID, f.owner, "cancelled-worker", "submit"); proof != nil || !errors.Is(err, repository.ErrVideoWorkerLeaseUnavailable) {
		t.Fatalf("已取消context不能认领: %v", err)
	}
	for _, kind := range []string{"video_worker_lease_claimed", "video_worker_lease_released", " VIDEO_WORKER_LEASE_CLAIMED "} {
		err := repository.NewVideoTaskEventRepository(db).Append(ctx, f.command.TaskID, f.owner, model.AIGatewayTaskEvent{EventID: "forged_" + f.command.RequestID, EventType: kind, Source: "worker", CreatedAt: time.Now().UTC()})
		if !errors.Is(err, repository.ErrVideoUnsafeDetail) {
			t.Fatalf("普通追加入口不能伪造租约历史: %v", err)
		}
	}
	proof, err := repo.Claim(ctx, f.command.TaskID, f.owner, "correct-owner", "submit")
	if err != nil || proof.Version() != 1 {
		t.Fatalf("所有拒绝不得占用或递增真实任务租约: %v", err)
	}
	if err := repo.Release(ctx, proof); err != nil {
		t.Fatal(err)
	}
}

// TestVideoG7WorkerLeasePendingInputMySQL 待对账时退出技术阶段，不等于释放参考图或用户冻结款。
func TestVideoG7WorkerLeasePendingInputMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	f, gateway, fake := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, video.FakeVideoResultUnknown)
	if _, err := gateway.Poll(ctx, f.command.TaskID); err != nil {
		t.Fatal(err)
	}
	_, _ = gateway.Poll(ctx, f.command.TaskID)
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
	if err != nil || task.Status != model.AIImageTaskPendingReconcile || task.BillingStatus != model.AIBillingSettlementPending {
		t.Fatalf("测试前必须形成原完整待对账链: %v", err)
	}
	inputs := repository.NewVideoTaskInputRepository(db)
	before, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
	if err != nil || len(before) != 1 || before[0].LeaseReleasedAt != nil {
		t.Fatalf("待对账输入应受保护: %v", err)
	}
	finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
	leases := repository.NewVideoWorkerLeaseRepository(db)
	proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "reconcile-observer", "poll")
	if err != nil {
		t.Fatal(err)
	}
	if err := leases.Release(ctx, proof); err != nil {
		t.Fatal(err)
	}
	after, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
	if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
		t.Fatalf("退出技术阶段不得释放输入、冻结款或改变补偿Outbox: %v", err)
	}
	if fake.SubmitCalls() != 1 {
		t.Fatal("租约管理不得再次提交Fake Provider")
	}
}
