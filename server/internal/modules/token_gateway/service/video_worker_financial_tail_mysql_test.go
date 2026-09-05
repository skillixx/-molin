package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG7WorkerFinancialTailMySQL 实际跨过30秒截止，资金主事务和失败后补记事务都须完整撤销。
func TestVideoG7WorkerFinancialTailMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"settle", "release", "mark_settle", "mark_release"} {
		t.Run(mode, func(t *testing.T) {
			// 原子分配的独立User/Task与独立故障函数允许并行等待，不修改全局DDL或GORM回调。
			t.Parallel()
			ctx := context.Background()
			kind := strings.TrimPrefix(mode, "mark_")
			mark := strings.HasPrefix(mode, "mark_")
			f, fake := newVideoG7FinancialFixture(t, db, kind)
			leases := repository.NewVideoWorkerLeaseRepository(db)
			proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "financial-tail-owner", "fetch")
			if err != nil {
				t.Fatal(err)
			}
			tasks := repository.NewVideoTaskRepository(db)
			events := repository.NewVideoTaskEventRepository(db)
			inputs := repository.NewVideoTaskInputRepository(db)
			comp := repository.NewVideoCompensationRepository(db)
			before, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
			wantExecution := model.AIImageTaskSucceeded
			if kind == "release" {
				wantExecution = model.AIImageTaskFailed
			}
			if err != nil || before.Status != wantExecution || before.BillingStatus != model.AIBillingHeld {
				t.Fatalf("必须从已执行但资金尚未终结的原任务开始: %v", err)
			}
			if _, err := comp.GetForTask(ctx, f.command.TaskID, f.owner); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("尾部测试开始时不得已有补偿任务: %v", err)
			}
			beforeEvents, err := events.ListForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			beforeInputs, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil || len(beforeInputs) != 1 || beforeInputs[0].LeaseReleasedAt != nil {
				t.Fatalf("尚未终结资金的I2V输入必须受保护: %v", err)
			}
			finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
			deadline := proof.Deadline()
			if time.Until(deadline) < 3*time.Second {
				t.Fatal("准备阶段耗尽尾部过期观察窗")
			}
			time.Sleep(time.Until(deadline) - 2*time.Second)
			mainHits, tailHits := 0, 0
			fault := errors.New("合成主资金事务故障")
			tailPoint := "settle_lease"
			if kind == "release" {
				tailPoint = "release_checked"
			}
			if mark {
				tailPoint = "execution_required_outbox"
			}
			f.service.fault = func(at string) error {
				if mark && at == kind+"_hold" {
					mainHits++
					return fault
				}
				if at == tailPoint {
					tailHits++
					// 已经写入但尚未提交；真实等待到期，不改变DB时钟、租期或代次。
					time.Sleep(time.Until(deadline) + 200*time.Millisecond)
				}
				return nil
			}
			owned, cancel := context.WithTimeout(repository.WithVideoWorkerLease(ctx, proof), 8*time.Second)
			defer cancel()
			var result *VideoFinancialResult
			if kind == "settle" {
				result, err = f.service.SettleReady(owned, f.command.TaskID, f.owner)
			} else {
				result, err = f.service.ReleaseUnserviceable(owned, f.command.TaskID, f.owner)
			}
			if result != nil || !errors.Is(err, repository.ErrVideoWorkerLeaseLost) || errors.Is(err, context.DeadlineExceeded) || tailHits != 1 {
				t.Fatalf("必须是已写入后的执行权到期，而非入口或context超时: main=%d tail=%d err=%v", mainHits, tailHits, err)
			}
			if (mark && (mainHits != 1 || !errors.Is(err, fault))) || (!mark && mainHits != 0) {
				t.Fatalf("主事务与补记阶段必须独立命中: main=%d err=%v", mainHits, err)
			}
			after, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
				t.Fatalf("资金事务或补记跨期必须保留Task及完整八表: %v", err)
			}
			afterEvents, err := events.ListForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil || !reflect.DeepEqual(beforeEvents, afterEvents) {
				t.Fatalf("不能遗留财务/P/C事件: %v", err)
			}
			afterInputs, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil || !reflect.DeepEqual(beforeInputs, afterInputs) {
				t.Fatalf("跨期回滚必须恢复输入保护: %v", err)
			}
			if _, err := comp.GetForTask(ctx, f.command.TaskID, f.owner); !errors.Is(err, gorm.ErrRecordNotFound) || fake.SubmitCalls() != 1 {
				t.Fatalf("旧执行权不得创建补偿或重提Provider: %v", err)
			}
			f.service.fault = nil
			next, err := leases.Claim(ctx, f.command.TaskID, f.owner, "financial-tail-recovery", "fetch")
			if err != nil || next.Version() != 2 {
				t.Fatalf("新持有者应可恢复原资金终结: %v", err)
			}
			current := repository.WithVideoWorkerLease(ctx, next)
			if kind == "settle" {
				result, err = f.service.SettleReady(current, f.command.TaskID, f.owner)
				if err != nil || result == nil || result.Existing || result.BillingStatus != model.AIBillingSettled || result.DeliveryStatus != model.AIDeliveryPending || !result.SettledAmount.Equal(f.quote.QuotedAmount) {
					t.Fatalf("新代次正常结算必须按原报价且不提前交付: %v", err)
				}
			} else {
				result, err = f.service.ReleaseUnserviceable(current, f.command.TaskID, f.owner)
				if err != nil || result == nil || result.Existing || result.BillingStatus != model.AIBillingReleased || result.DeliveryStatus != model.AIDeliveryRejected || !result.ReleasedAmount.Equal(f.quote.QuotedAmount) {
					t.Fatalf("新代次正常退款必须遵守原政策: %v", err)
				}
			}
			if err := leases.Release(ctx, next); err != nil {
				t.Fatal(err)
			}
			if fake.SubmitCalls() != 1 {
				t.Fatal("资金恢复不能再次调用Submit")
			}
		})
	}
}
