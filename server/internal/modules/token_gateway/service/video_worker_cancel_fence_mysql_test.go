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
)

// 私有结构用DeepEqual保留所有内部字段；不使用会隐藏租约字段的JSON序列化作Task对照。
type videoG7TaskWriteSnapshot struct {
	task    *repository.VideoTaskRecord
	events  []model.AIGatewayTaskEvent
	inputs  []model.AIGatewayTaskInput
	finance []byte
}

func captureVideoG7TaskWrite(t *testing.T, db *gorm.DB, id string, owner repository.VideoOwner) videoG7TaskWriteSnapshot {
	t.Helper()
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), id, owner)
	if err != nil {
		t.Fatal(err)
	}
	events, err := repository.NewVideoTaskEventRepository(db).ListForOwner(context.Background(), id, owner)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), id, owner)
	if err != nil {
		t.Fatal(err)
	}
	return videoG7TaskWriteSnapshot{task: task, events: events, inputs: inputs, finance: mediaDeleteFinanceSnapshot(t, db, owner.UserID)}
}

// TestVideoG7WorkerCancelFenceMySQL 取消兼容原用户准入，但携带Worker证明的资金事务必须拒绝尾部失权。
func TestVideoG7WorkerCancelFenceMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, mode := range []string{"current_worker", "authorized_user", "worker_tail_expiry"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				f := newVideoG5ReservationFixture(t, db, "10")
				if operation == model.AIVideoOperationImageToVideo {
					prepareVideoG5I2V(t, &f)
				}
				if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
					t.Fatal(err)
				}
				leases := repository.NewVideoWorkerLeaseRepository(db)
				proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "cancel-worker", "submit")
				if err != nil {
					t.Fatal(err)
				}
				before := captureVideoG7TaskWrite(t, db, f.command.TaskID, f.owner)
				if before.task.Status != model.AIImageTaskReserved || before.task.BillingStatus != model.AIBillingHeld || before.task.CancelRequestedAt != nil {
					t.Fatal("须从尚未提交、未取消的原预占开始")
				}
				if mode == "authorized_user" {
					// 无普通证明不代表跳过原准入；真实Key状态被撤销时仍须拒绝取消。
					if err := db.Table("api_keys").Where("id=?", *f.owner.APIKeyID).Update("status", "revoked").Error; err != nil {
						t.Fatal(err)
					}
					if _, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); !errors.Is(err, ErrVideoBillingAccess) {
						t.Fatalf("无Worker证明仍必须通过原Key准入: %v", err)
					}
					if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, f.command.TaskID, f.owner)) {
						t.Fatal("准入拒绝不能改变任务或资金")
					}
					if err := db.Table("api_keys").Where("id=?", *f.owner.APIKeyID).Update("status", "active").Error; err != nil {
						t.Fatal(err)
					}
				}
				if mode == "worker_tail_expiry" {
					deadline := proof.Deadline()
					if time.Until(deadline) < 3*time.Second {
						t.Fatal("准备阶段耗尽取消尾部观察窗")
					}
					time.Sleep(time.Until(deadline) - 2*time.Second)
					hits := 0
					f.service.fault = func(at string) error {
						if at == "cancel_rejected_outbox" {
							hits++
							time.Sleep(time.Until(deadline) + 200*time.Millisecond)
						}
						return nil
					}
					owned, cancel := context.WithTimeout(repository.WithVideoWorkerLease(ctx, proof), 8*time.Second)
					defer cancel()
					result, err := f.service.CancelBeforeSubmit(owned, f.command.TaskID, f.owner)
					if result != nil || !errors.Is(err, repository.ErrVideoWorkerLeaseLost) || errors.Is(err, context.DeadlineExceeded) || hits != 1 {
						t.Fatalf("必须命中真实取消写入后的租约失效: hits=%d err=%v", hits, err)
					}
					if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, f.command.TaskID, f.owner)) {
						t.Fatal("失权取消必须回滚意图、Task、资金、事件与输入释放")
					}
					f.service.fault = nil
				}
				caller := ctx
				if mode == "current_worker" {
					caller = repository.WithVideoWorkerLease(ctx, proof)
				}
				result, err := f.service.CancelBeforeSubmit(caller, f.command.TaskID, f.owner)
				if err != nil || result == nil || result.Existing || result.BillingStatus != model.AIBillingReleased || result.DeliveryStatus != model.AIDeliveryRejected || !result.ReleasedAmount.Equal(f.quote.QuotedAmount) {
					t.Fatalf("当前Worker或原授权用户应能取消并全额释放: %v", err)
				}
				if mode != "worker_tail_expiry" {
					if err := leases.Release(ctx, proof); err != nil {
						t.Fatal(err)
					}
				}
				replayBefore := captureVideoG7TaskWrite(t, db, f.command.TaskID, f.owner)
				if replay, err := f.service.CancelBeforeSubmit(repository.WithVideoWorkerLease(ctx, proof), f.command.TaskID, f.owner); err != nil || replay == nil || !replay.Existing {
					t.Fatalf("已取消的只读重放允许旧证明，不再次写钱: %v", err)
				}
				if !reflect.DeepEqual(replayBefore, captureVideoG7TaskWrite(t, db, f.command.TaskID, f.owner)) {
					t.Fatal("取消重放应零写入")
				}
			})
		}
	}
}
