package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// TestVideoG7WorkerFinancialSettleMySQL 已就绪任务进入租约化结算后，旧执行者不能消费或伪建恢复任务。
func TestVideoG7WorkerFinancialSettleMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, mode := range []string{"missing", "stale"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				f := newVideoG5ReservationFixture(t, db, "10")
				if operation == model.AIVideoOperationImageToVideo {
					prepareVideoG5I2V(t, &f)
				}
				if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
					t.Fatal(err)
				}
				_, fake := runVideoG5ReadyFixture(t, f)
				leases := repository.NewVideoWorkerLeaseRepository(db)
				old, err := leases.Claim(ctx, f.command.TaskID, f.owner, "old-settler", "fetch")
				if err != nil {
					t.Fatal(err)
				}
				if err := leases.Release(ctx, old); err != nil {
					t.Fatal(err)
				}
				current, err := leases.Claim(ctx, f.command.TaskID, f.owner, "current-settler", "fetch")
				if err != nil || current.Version() != 2 {
					t.Fatalf("新结算持有者认领失败: %v", err)
				}
				tasks := repository.NewVideoTaskRepository(db)
				events := repository.NewVideoTaskEventRepository(db)
				inputs := repository.NewVideoTaskInputRepository(db)
				before, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || before.Status != model.AIImageTaskSucceeded || before.BillingStatus != model.AIBillingHeld {
					t.Fatalf("必须是原已执行成功且尚未结算的任务: %v", err)
				}
				if _, err := repository.NewVideoCompensationRepository(db).GetForTask(ctx, f.command.TaskID, f.owner); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("普通结算反例不能借已有补偿任务阻断: %v", err)
				}
				beforeEvents, err := events.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				beforeInputs, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
				attempt := ctx
				if mode == "stale" {
					attempt = repository.WithVideoWorkerLease(ctx, old)
				}
				if result, err := f.service.SettleReady(attempt, f.command.TaskID, f.owner); result != nil || !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
					t.Fatalf("缺失或旧租约不能结算: %v", err)
				}
				after, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
					t.Fatalf("被拒绝的结算必须零资金/Request/Quote/Usage/Outbox写入: %v", err)
				}
				afterEvents, err := events.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || !reflect.DeepEqual(beforeEvents, afterEvents) {
					t.Fatalf("被拒绝的结算不能追加财务状态事件: %v", err)
				}
				afterInputs, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || !reflect.DeepEqual(beforeInputs, afterInputs) {
					t.Fatalf("被拒绝的结算不能释放输入: %v", err)
				}
				if _, err := repository.NewVideoCompensationRepository(db).GetForTask(ctx, f.command.TaskID, f.owner); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("失去执行权不是创建财务补偿的授权: %v", err)
				}
				settled, err := f.service.SettleReady(repository.WithVideoWorkerLease(ctx, current), f.command.TaskID, f.owner)
				if err != nil || settled.Existing || settled.BillingStatus != model.AIBillingSettled || settled.DeliveryStatus != model.AIDeliveryPending || !settled.SettledAmount.Equal(f.quote.QuotedAmount) {
					t.Fatalf("当前持有者必须可按原报价结算且不提前交付: %v", err)
				}
				if err := leases.Release(ctx, current); err != nil {
					t.Fatal(err)
				}
				replayFinance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
				if replay, err := f.service.SettleReady(attempt, f.command.TaskID, f.owner); err != nil || !replay.Existing || !replay.SettledAmount.Equal(settled.SettledAmount) {
					t.Fatalf("已结算的只读重放无需重新认领: %v", err)
				}
				if !reflect.DeepEqual(replayFinance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) || fake.SubmitCalls() != 1 {
					t.Fatal("重放不得重复消费或重提Provider")
				}
			})
		}
	}
}

// 使用原G5服务建立两类I2V资金终结前置事实，不模拟钱包、确认成本或对账器。
func newVideoG7FinancialFixture(t *testing.T, db *gorm.DB, kind string) (videoG5ReservationFixture, *video.FakeAsyncVideoAdapter) {
	t.Helper()
	ctx := context.Background()
	if kind == "release" {
		f, gateway, fake := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, video.FakeVideoExplicitFailure)
		if _, err := gateway.Poll(ctx, f.command.TaskID); err != nil {
			t.Fatal(err)
		}
		_, _ = gateway.Poll(ctx, f.command.TaskID)
		task, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
		if err != nil || task.Status != model.AIImageTaskFailed || task.BillingStatus != model.AIBillingHeld {
			t.Fatalf("原失败资金事实未准备好: %v", err)
		}
		return f, fake
	}
	if kind != "settle" {
		t.Fatal("不支持的财务测试类型")
	}
	f := newVideoG5ReservationFixture(t, db, "10")
	prepareVideoG5I2V(t, &f)
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	_, fake := runVideoG5ReadyFixture(t, f)
	return f, fake
}

// TestVideoG7WorkerFinancialCompensationMySQL 有效补偿租约是独立授权，非nil但错请求的租约不能借此写钱。
func TestVideoG7WorkerFinancialCompensationMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, kind := range []string{"settle", "release"} {
		t.Run(kind, func(t *testing.T) {
			f, fake := newVideoG7FinancialFixture(t, db, kind)
			leases := repository.NewVideoWorkerLeaseRepository(db)
			proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "financial-fault-owner", "fetch")
			if err != nil {
				t.Fatal(err)
			}
			fault := errors.New("合成资金事务故障")
			f.service.fault = func(at string) error {
				if at == kind+"_outbox" {
					return fault
				}
				return nil
			}
			owned := repository.WithVideoWorkerLease(ctx, proof)
			if kind == "settle" {
				_, err = f.service.SettleReady(owned, f.command.TaskID, f.owner)
			} else {
				_, err = f.service.ReleaseUnserviceable(owned, f.command.TaskID, f.owner)
			}
			if !errors.Is(err, fault) {
				t.Fatalf("必须命中原资金事务故障: %v", err)
			}
			comp := repository.NewVideoCompensationRepository(db)
			job, err := comp.GetForTask(ctx, f.command.TaskID, f.owner)
			if err != nil || job.ID == 0 || job.Status != "pending" || job.AttemptCount != 0 {
				t.Fatalf("当前普通证明允许错误后原子建立恢复任务: %v", err)
			}
			if err := leases.Release(ctx, proof); err != nil {
				t.Fatal(err)
			}
			f.service.fault = nil
			grant, err := comp.Claim(ctx, f.command.RequestID, "independent-compensator")
			if err != nil {
				t.Fatal(err)
			}
			wrong := *grant
			wrong.RequestID += "_wrong"
			finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
			if kind == "settle" {
				_, err = f.service.RecoverSettlement(ctx, f.command.TaskID, f.owner, wrong)
			} else {
				_, err = f.service.RecoverRelease(ctx, f.command.TaskID, f.owner, wrong)
			}
			if !errors.Is(err, repository.ErrVideoCompensationLeaseLost) || !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
				t.Fatalf("非nil但错请求的补偿租约不能豁免授权: %v", err)
			}
			if kind == "settle" {
				result, err := f.service.RecoverSettlement(ctx, f.command.TaskID, f.owner, *grant)
				if err != nil || result.BillingStatus != model.AIBillingSettled || !result.SettledAmount.Equal(f.quote.QuotedAmount) {
					t.Fatalf("独立补偿证明应能恢复原结算: %v", err)
				}
				if _, err := f.service.RecoverDelivery(ctx, f.command.TaskID, f.owner, *grant); err != nil {
					t.Fatal(err)
				}
			} else {
				result, err := f.service.RecoverRelease(ctx, f.command.TaskID, f.owner, *grant)
				if err != nil || result.BillingStatus != model.AIBillingReleased || !result.ReleasedAmount.Equal(f.quote.QuotedAmount) {
					t.Fatalf("独立补偿证明应能恢复原退款: %v", err)
				}
			}
			job, err = comp.GetForTask(ctx, f.command.TaskID, f.owner)
			if err != nil || job.Status != "completed" || job.AttemptCount != 1 || job.CompletedAt == nil {
				t.Fatalf("合法恢复必须闭合原补偿任务: %v", err)
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
			if err != nil || task.WorkerLeaseActive || task.WorkerLeaseVersion != 1 || fake.SubmitCalls() != 1 {
				t.Fatalf("财务恢复不能重启执行租约或重提Provider: %v", err)
			}
		})
	}
}

// TestVideoG7WorkerFinancialReleaseMySQL 原Provider明确失败后，退款同样需要当前普通Worker证明。
func TestVideoG7WorkerFinancialReleaseMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, mode := range []string{"missing", "stale"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				f, gateway, fake := videoG5CancellationFixture(t, db, operation, video.FakeVideoExplicitFailure)
				if _, err := gateway.Poll(ctx, f.command.TaskID); err != nil {
					t.Fatal(err)
				}
				// 明确失败由后续持久状态和原资金事实验证，不把业务失败返回误判为测试准备失败。
				_, _ = gateway.Poll(ctx, f.command.TaskID)
				tasks := repository.NewVideoTaskRepository(db)
				events := repository.NewVideoTaskEventRepository(db)
				inputs := repository.NewVideoTaskInputRepository(db)
				leases := repository.NewVideoWorkerLeaseRepository(db)
				old, err := leases.Claim(ctx, f.command.TaskID, f.owner, "old-releaser", "poll")
				if err != nil {
					t.Fatal(err)
				}
				if err := leases.Release(ctx, old); err != nil {
					t.Fatal(err)
				}
				current, err := leases.Claim(ctx, f.command.TaskID, f.owner, "current-releaser", "poll")
				if err != nil || current.Version() != 2 {
					t.Fatalf("新退款持有者认领失败: %v", err)
				}
				before, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || before.Status != model.AIImageTaskFailed || before.BillingStatus != model.AIBillingHeld {
					t.Fatalf("必须是明确失败且尚未释放的原任务: %v", err)
				}
				if _, err := repository.NewVideoCompensationRepository(db).GetForTask(ctx, f.command.TaskID, f.owner); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("普通退款反例不能借已有补偿阻断: %v", err)
				}
				beforeEvents, err := events.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				beforeInputs, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil {
					t.Fatal(err)
				}
				finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
				attempt := ctx
				if mode == "stale" {
					attempt = repository.WithVideoWorkerLease(ctx, old)
				}
				if result, err := f.service.ReleaseUnserviceable(attempt, f.command.TaskID, f.owner); result != nil || !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
					t.Fatalf("无证明或旧Worker不得退款: %v", err)
				}
				after, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
					t.Fatalf("被拒绝的退款不得改Task或八表资金事实: %v", err)
				}
				afterEvents, err := events.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || !reflect.DeepEqual(beforeEvents, afterEvents) {
					t.Fatalf("拒绝退款不得追加财务事件: %v", err)
				}
				afterInputs, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || !reflect.DeepEqual(beforeInputs, afterInputs) {
					t.Fatalf("拒绝退款不得释放输入: %v", err)
				}
				if _, err := repository.NewVideoCompensationRepository(db).GetForTask(ctx, f.command.TaskID, f.owner); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("失权不得通过补记创建恢复任务: %v", err)
				}
				released, err := f.service.ReleaseUnserviceable(repository.WithVideoWorkerLease(ctx, current), f.command.TaskID, f.owner)
				if err != nil || released.Existing || released.BillingStatus != model.AIBillingReleased || released.DeliveryStatus != model.AIDeliveryRejected || !released.SettledAmount.IsZero() || !released.ReleasedAmount.Equal(f.quote.QuotedAmount) {
					t.Fatalf("当前证明应按原政策全额释放并拒绝交付: %v", err)
				}
				if err := leases.Release(ctx, current); err != nil {
					t.Fatal(err)
				}
				replayFinance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
				if replay, err := f.service.ReleaseUnserviceable(attempt, f.command.TaskID, f.owner); err != nil || !replay.Existing || !replay.ReleasedAmount.Equal(released.ReleasedAmount) {
					t.Fatalf("已释放只读重放不需要新租约: %v", err)
				}
				if !reflect.DeepEqual(replayFinance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) || fake.SubmitCalls() != 1 {
					t.Fatal("退款重放不得改变资金或重提Provider")
				}
			})
		}
	}
}
