package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoG7SubmissionFixture struct {
	fixture videoG5ReservationFixture
	ledger  *VideoRepositoryTaskLedger
	claim   video.GatewayTask
	receipt video.SubmitResult
	leases  *repository.VideoWorkerLeaseRepository
	proof   *repository.VideoWorkerLease
	fake    *video.FakeAsyncVideoAdapter
}

// 从原预占/输入/提交事件链建立正常或待对账任务；原Worker在Fake提交前已持有真实执行租约。
func newVideoG7Submission(t *testing.T, db *gorm.DB, operation string, enterPending bool) videoG7SubmissionFixture {
	t.Helper()
	ctx := context.Background()
	f := newVideoG5ReservationFixture(t, db, "10")
	if operation == model.AIVideoOperationImageToVideo {
		prepareVideoG5I2V(t, &f)
	}
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	leases := repository.NewVideoWorkerLeaseRepository(db)
	proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "original-submitter", "submit")
	if err != nil {
		t.Fatal(err)
	}
	owned := repository.WithVideoWorkerLease(ctx, proof)
	ledger := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
	claim, err := ledger.Load(owned, f.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []video.TaskStatus{video.TaskQueued, video.TaskSubmitting} {
		claim, err = ledger.Advance(owned, claim.TaskID, claim.Version, state, "worker", "state_advanced", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ledger.ValidateSubmissionClaim(owned, claim.TaskID, claim.Version); err != nil {
		t.Fatal(err)
	}
	fake := video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess)
	receipt, err := fake.Submit(owned, video.SubmitRequest{RequestID: claim.RequestID, Operation: claim.Operation, Prompt: claim.Prompt, Input: claim.Input, Spec: claim.Spec})
	if err != nil {
		t.Fatal(err)
	}
	if enterPending {
		// 原提交结果暂未被接收；只进入原有待对账/补偿链，不先绑定Provider身份。
		if pending, err := ledger.Advance(owned, claim.TaskID, claim.Version, video.TaskPendingReconcile, "worker", "submit_unknown", nil); err != nil || pending.Status != video.TaskPendingReconcile || pending.ProviderTaskID != "" {
			t.Fatalf("原待对账准备失败: %v", err)
		}
	}
	return videoG7SubmissionFixture{fixture: f, ledger: ledger, claim: claim, receipt: receipt, leases: leases, proof: proof, fake: fake}
}

// TestVideoG7SubmissionWorkerFenceMySQL 迟到身份写入须服从新代次；已接受的相同回执重放则仍为只读。
func TestVideoG7SubmissionWorkerFenceMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, mode := range []string{"missing_proof", "stale_cancelled_proof"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				f := newVideoG7Submission(t, db, operation, true)
				if err := f.leases.Release(ctx, f.proof); err != nil {
					t.Fatal(err)
				}
				next, err := f.leases.Claim(ctx, f.claim.TaskID, f.fixture.owner, "recovery-worker", "poll")
				if err != nil || next.Version() != 2 {
					t.Fatalf("新持有者认领失败: %v", err)
				}
				tasks := repository.NewVideoTaskRepository(db)
				events := repository.NewVideoTaskEventRepository(db)
				inputs := repository.NewVideoTaskInputRepository(db)
				before, err := tasks.FindForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil {
					t.Fatal(err)
				}
				beforeEvents, err := events.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil {
					t.Fatal(err)
				}
				beforeInputs, err := inputs.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil {
					t.Fatal(err)
				}
				finance := mediaDeleteFinanceSnapshot(t, db, f.fixture.owner.UserID)
				attempt := ctx
				if mode == "stale_cancelled_proof" {
					cancelled, cancel := context.WithCancel(repository.WithVideoWorkerLease(ctx, f.proof))
					cancel()
					attempt = cancelled
				}
				if _, err := f.ledger.RecordSubmissionReceipt(attempt, f.claim.TaskID, f.claim.Version, f.receipt); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
					t.Fatalf("缺少或过期证明不得绕过pending分支绑定Provider: %v", err)
				}
				after, err := tasks.FindForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("拒绝旧回执不得修改Task: %v", err)
				}
				afterEvents, err := events.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil || !reflect.DeepEqual(beforeEvents, afterEvents) || !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.fixture.owner.UserID)) {
					t.Fatalf("拒绝旧回执不得追加接受/绑定事件或改财务: %v", err)
				}
				current := repository.WithVideoWorkerLease(ctx, next)
				accepted, err := f.ledger.RecordSubmissionReceipt(current, f.claim.TaskID, f.claim.Version, f.receipt)
				if err != nil || accepted.Status != video.TaskPendingReconcile || accepted.ProviderTaskID != f.receipt.ProviderTaskID {
					t.Fatalf("当前持有者须能保存原身份且不回退执行状态: %v", err)
				}
				bound, err := tasks.FindForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil || bound.AttemptCount != 1 || bound.BillingStatus != model.AIBillingSettlementPending || bound.DeliveryStatus != model.AIDeliveryPending {
					t.Fatalf("身份绑定不能提前结算或交付: %v", err)
				}
				if err := f.leases.Release(ctx, next); err != nil {
					t.Fatal(err)
				}
				replayEvents, err := events.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil {
					t.Fatal(err)
				}
				replayFinance := mediaDeleteFinanceSnapshot(t, db, f.fixture.owner.UserID)
				if replay, err := f.ledger.RecordSubmissionReceipt(attempt, f.claim.TaskID, f.claim.Version, f.receipt); err != nil || !reflect.DeepEqual(replay, accepted) {
					t.Fatalf("已接受相同回执只读重放不需要新租约: %v", err)
				}
				lastEvents, err := events.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil || !reflect.DeepEqual(replayEvents, lastEvents) || !reflect.DeepEqual(replayFinance, mediaDeleteFinanceSnapshot(t, db, f.fixture.owner.UserID)) {
					t.Fatalf("相同回执重放应零写入: %v", err)
				}
				afterInputs, err := inputs.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
				if err != nil || !reflect.DeepEqual(beforeInputs, afterInputs) || f.fake.SubmitCalls() != 1 {
					t.Fatalf("绑定与重放不能释放在途输入或重复提交: %v", err)
				}
			})
		}
	}
}

// TestVideoG7SubmissionWorkerTailExpiryMySQL 在回执自身5秒事务内跨过真实Worker租约截止，必须整笔回滚。
func TestVideoG7SubmissionWorkerTailExpiryMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, state := range []video.TaskStatus{video.TaskSubmitting, video.TaskPendingReconcile} {
		t.Run(string(state), func(t *testing.T) {
			f := newVideoG7Submission(t, db, model.AIVideoOperationImageToVideo, state == video.TaskPendingReconcile)
			tasks := repository.NewVideoTaskRepository(db)
			events := repository.NewVideoTaskEventRepository(db)
			inputs := repository.NewVideoTaskInputRepository(db)
			recovery := repository.NewVideoCompensationRepository(db)
			before, err := tasks.FindForOwner(ctx, f.claim.TaskID, f.fixture.owner)
			if err != nil || before.Status != string(state) {
				t.Fatalf("原回执分支状态准备错误: %v", err)
			}
			beforeEvents, err := events.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
			if err != nil {
				t.Fatal(err)
			}
			beforeInputs, err := inputs.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
			if err != nil || len(beforeInputs) != 1 || beforeInputs[0].LeaseReleasedAt != nil {
				t.Fatalf("I2V必须保留唯一在途输入: %v", err)
			}
			beforeRecovery, beforeRecoveryErr := recovery.GetForTask(ctx, f.claim.TaskID, f.fixture.owner)
			if beforeRecoveryErr != nil && !errors.Is(beforeRecoveryErr, gorm.ErrRecordNotFound) {
				t.Fatal(beforeRecoveryErr)
			}
			if state == video.TaskSubmitting && !errors.Is(beforeRecoveryErr, gorm.ErrRecordNotFound) {
				t.Fatal("正常提交分支开始时不得已经存在补偿任务")
			}
			if state == video.TaskPendingReconcile && (beforeRecoveryErr != nil || beforeRecovery == nil || beforeRecovery.ID == 0) {
				t.Fatal("待对账分支开始时必须有原持久化补偿任务")
			}
			finance := mediaDeleteFinanceSnapshot(t, db, f.fixture.owner.UserID)
			deadline := f.proof.Deadline()
			if time.Until(deadline) < 3*time.Second {
				t.Fatal("夹具准备不能耗尽尾部过期观察窗")
			}
			// 大部分30秒等待放在事务外，避免误测为回执自身5秒context超时。
			time.Sleep(time.Until(deadline) - 2*time.Second)
			hits := 0
			f.ledger.financialFault = func(point string) error {
				if point == "submission_receipt" {
					hits++
					// 此时绑定和接受事件已写入尚未提交；不修改DB时间、租期或租约代次。
					time.Sleep(time.Until(deadline) + 200*time.Millisecond)
				}
				return nil
			}
			_, err = f.ledger.RecordSubmissionReceipt(repository.WithVideoWorkerLease(ctx, f.proof), f.claim.TaskID, f.claim.Version, f.receipt)
			if hits != 1 || !errors.Is(err, repository.ErrVideoWorkerLeaseLost) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("必须是成功写入之后的租约到期拒绝，而非入口拒绝或context超时: hits=%d err=%v", hits, err)
			}
			after, err := tasks.FindForOwner(ctx, f.claim.TaskID, f.fixture.owner)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("尾部过期必须撤销正常与pending分支的全部Task变化: %v", err)
			}
			afterEvents, err := events.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
			if err != nil || !reflect.DeepEqual(beforeEvents, afterEvents) || !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.fixture.owner.UserID)) {
				t.Fatalf("不能遗留绑定/接受事件或半份财务/Outbox: %v", err)
			}
			afterRecovery, afterRecoveryErr := recovery.GetForTask(ctx, f.claim.TaskID, f.fixture.owner)
			if (beforeRecoveryErr == nil) != (afterRecoveryErr == nil) || (afterRecoveryErr != nil && !errors.Is(afterRecoveryErr, gorm.ErrRecordNotFound)) || !reflect.DeepEqual(beforeRecovery, afterRecovery) {
				t.Fatal("回执回滚不得新增或改动原补偿任务")
			}
			f.ledger.financialFault = nil
			next, err := f.leases.Claim(ctx, f.claim.TaskID, f.fixture.owner, "tail-recovery", "poll")
			if err != nil || next.Version() != 2 {
				t.Fatalf("到期后应由新代次继续保存原回执: %v", err)
			}
			accepted, err := f.ledger.RecordSubmissionReceipt(repository.WithVideoWorkerLease(ctx, next), f.claim.TaskID, f.claim.Version, f.receipt)
			want := video.TaskSubmitted
			if state == video.TaskPendingReconcile {
				want = video.TaskPendingReconcile
			}
			if err != nil || accepted.Status != want || accepted.ProviderTaskID != f.receipt.ProviderTaskID || f.fake.SubmitCalls() != 1 {
				t.Fatalf("新持有者应保存同一原回执，不重新create或回退pending: %v", err)
			}
			if err := f.leases.Release(ctx, next); err != nil {
				t.Fatal(err)
			}
			afterInputs, err := inputs.ListForOwner(ctx, f.claim.TaskID, f.fixture.owner)
			if err != nil || !reflect.DeepEqual(beforeInputs, afterInputs) {
				t.Fatalf("回滚和新代次恢复都不能释放在途输入: %v", err)
			}
		})
	}
}
