package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG5CancelMySQLRejectsInconsistentFacts 不一致Outbox和取消前已有额外Usage不能被原子取消或重放掩盖。
func TestVideoG5CancelMySQLRejectsInconsistentFacts(t *testing.T) {
	db := openVideoG5MySQL(t)
	t.Run("outbox_payload", func(t *testing.T) {
		f := newVideoG5ReservationFixture(t, db, "10")
		if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("UPDATE ai_outbox_events SET payload_json=JSON_SET(payload_json,'$.amount','999.00000000') WHERE aggregate_type='video_request' AND aggregate_id=? AND event_type='video_billing_released'", f.command.RequestID).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); !errors.Is(err, ErrVideoBillingState) {
			t.Fatalf("错误Outbox金额应阻断重放: %v", err)
		}
	})
	t.Run("extra_usage_before_cancel", func(t *testing.T) {
		f := newVideoG5ReservationFixture(t, db, "10")
		if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
			t.Fatal(err)
		}
		zero, currency := decimal.Zero, "CNY"
		fact := model.AIUsageItem{RecordKind: model.AIUsageFact, Source: "provider", Quantity: decimal.NewFromInt(1), UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &zero, Currency: &currency, SequenceNo: 1}
		if err := db.Transaction(func(tx *gorm.DB) error {
			_, _, err := repository.NewVideoUsageRepository(db).AppendTx(tx, f.command.TaskID, f.owner, fact, time.Now())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); !errors.Is(err, ErrVideoBillingState) {
			t.Fatalf("已有冲突计量不得直接闭合取消: %v", err)
		}
		var w billingmodel.Wallet
		if err := db.Where("user_id=?", f.owner.UserID).First(&w).Error; err != nil {
			t.Fatal(err)
		}
		if w.BalanceAmount.StringFixed(8) != "9.50000000" || w.FrozenAmount.StringFixed(8) != "0.50000000" {
			t.Fatal("一致性检查失败必须整体回滚释放")
		}
	})
}

// TestVideoG5CancelMySQLRacesSubmitting 取消和Worker取得提交权竞争，获胜者唯一；进入submitting后不能释放。
func TestVideoG5CancelMySQLRacesSubmitting(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	tasks := repository.NewVideoTaskRepository(db)
	queued, err := tasks.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: f.command.TaskID, Owner: f.owner, ExpectedVersion: 1, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: f.command.RequestID + "_queued", Source: "worker", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var cancelled, submitting atomic.Int64
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err == nil {
			cancelled.Add(1)
		} else if !errors.Is(err, ErrVideoBillingState) {
			t.Errorf("取消竞争异常: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if _, err := tasks.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: f.command.TaskID, Owner: f.owner, ExpectedVersion: queued.VersionNo, ToStatus: model.AIImageTaskSubmitting, Progress: 15, EventID: f.command.RequestID + "_submitting", Source: "worker", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), Now: time.Now()}); err == nil {
			submitting.Add(1)
		} else if !errors.Is(err, repository.ErrVideoTaskConflict) {
			t.Errorf("提交权竞争异常: %v", err)
		}
	}()
	close(start)
	wg.Wait()
	if cancelled.Load()+submitting.Load() != 1 {
		t.Fatalf("取消和提交权必须互斥: %d/%d", cancelled.Load(), submitting.Load())
	}
	if submitting.Load() == 1 {
		if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); !errors.Is(err, ErrVideoBillingState) {
			t.Fatalf("取得提交权后禁止即时释放: %v", err)
		}
	}
}

// TestVideoG5CancelMySQLReplayRejectsExtraUsage 迟到计量事实不能被只数三条零金额行的重放检查掩盖。
func TestVideoG5CancelMySQLReplayRejectsExtraUsage(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	zero, currency := decimal.Zero, "CNY"
	late := model.AIUsageItem{RecordKind: model.AIUsageFact, Source: "provider", Quantity: decimal.NewFromInt(1), UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &zero, Currency: &currency, SequenceNo: 1}
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, _, err := repository.NewVideoUsageRepository(db).AppendTx(tx, f.command.TaskID, f.owner, late, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); !errors.Is(err, ErrVideoBillingState) {
		t.Fatalf("存在迟到冲突事实时不能冒报完整取消重放成功: %v", err)
	}
}

// TestVideoG5CancelMySQLRollbackEveryWrite 释放任一步失败都保留原Hold与输入租约，重试后只能释放一次。
func TestVideoG5CancelMySQLRollbackEveryWrite(t *testing.T) {
	db := openVideoG5MySQL(t)
	steps := []string{"cancel_task", "cancel_pending", "cancel_hold", "cancel_link", "cancel_usage_fact", "cancel_sale_line", "cancel_cost_line", "cancel_final_state", "cancel_lease", "cancel_released_outbox", "cancel_rejected_outbox"}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			prepareVideoG5I2V(t, &f)
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			f.service.fault = func(at string) error {
				if at == step {
					return errors.New("合成释放故障")
				}
				return nil
			}
			if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("注入故障必须返回错误")
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || task.Status != model.AIImageTaskReserved || task.BillingStatus != model.AIBillingHeld || task.DeliveryStatus != model.AIDeliveryPending {
				t.Fatalf("取消状态未回滚: %v", err)
			}
			var w billingmodel.Wallet
			if err := db.Where("user_id=?", f.owner.UserID).First(&w).Error; err != nil {
				t.Fatal(err)
			}
			if w.BalanceAmount.StringFixed(8) != "9.25000000" || w.FrozenAmount.StringFixed(8) != "0.75000000" {
				t.Fatal("失败释放改变了钱包余额")
			}
			for _, table := range []string{"ai_usage_items", "wallet_transactions", "ai_outbox_events"} {
				var n int64
				query := db.Table(table)
				want := int64(0)
				switch table {
				case "wallet_transactions":
					query = query.Where("user_id=?", f.owner.UserID)
					want = 1
				case "ai_outbox_events":
					query = query.Where("aggregate_type='video_request' AND aggregate_id=?", f.command.RequestID)
					want = 1
				default:
					query = query.Where("request_id=?", f.command.RequestID)
				}
				if err := query.Count(&n).Error; err != nil || n != want {
					t.Fatalf("%s事实未回滚: %d/%d %v", table, n, want, err)
				}
			}
			bindings, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || len(bindings) != 1 || bindings[0].LeaseReleasedAt != nil {
				t.Fatalf("失败释放不能丢失输入租约: %v", err)
			}
			f.service.fault = nil
			if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err != nil {
				t.Fatalf("失败后的重试应闭合释放: %v", err)
			}
		})
	}
}

// TestVideoG5CancelMySQLQueuedOneRelease 金样F07：同一未提交任务100次取消只解冻一次，保留全部原始事实。
func TestVideoG5CancelMySQLQueuedOneRelease(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if operation == model.AIVideoOperationImageToVideo {
				prepareVideoG5I2V(t, &f)
			}
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			tasks := repository.NewVideoTaskRepository(db)
			if _, err := tasks.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: f.command.TaskID, Owner: f.owner, ExpectedVersion: 1, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: "queued_" + f.command.RequestID, Source: "worker", Now: time.Now()}); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			var created, replayed atomic.Int64
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner)
					if err != nil {
						t.Errorf("取消失败: %v", err)
						return
					}
					if r.Existing {
						replayed.Add(1)
					} else {
						created.Add(1)
					}
					if r.BillingStatus != model.AIBillingReleased || r.DeliveryStatus != model.AIDeliveryRejected || !r.SettledAmount.IsZero() || !r.ReleasedAmount.Equal(f.quote.QuotedAmount) {
						t.Error("取消金额或交付状态错误")
					}
				}()
			}
			wg.Wait()
			if created.Load() != 1 || replayed.Load() != 99 {
				t.Fatalf("取消只能一次生效: %d/%d", created.Load(), replayed.Load())
			}
			var wallet billingmodel.Wallet
			if err := db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil {
				t.Fatal(err)
			}
			if !wallet.BalanceAmount.Equal(decimal.NewFromInt(10)) || !wallet.FrozenAmount.IsZero() {
				t.Fatal("取消后钱包未恢复10/0")
			}
			var txs []billingmodel.WalletTransaction
			if err := db.Where("user_id=?", f.owner.UserID).Order("id ASC").Find(&txs).Error; err != nil {
				t.Fatal(err)
			}
			if len(txs) != 2 || txs[0].Type != "freeze" || txs[1].Type != "unfreeze" || txs[0].Direction != "out" || txs[1].Direction != "in" || !txs[0].Amount.Equal(f.quote.QuotedAmount) || !txs[1].Amount.Equal(f.quote.QuotedAmount) || !txs[1].BalanceAfter.Equal(decimal.NewFromInt(10)) {
				t.Fatal("冻结/解冻方向、数量或金额不守恒")
			}
			if err := db.Model(&billingmodel.WalletTransaction{}).Where("id=?", txs[0].ID).Update("amount", 0).Error; err == nil {
				t.Fatal("已关联视频的原流水禁止覆盖")
			}
			if err := db.Delete(&billingmodel.WalletTransaction{}, txs[1].ID).Error; err == nil {
				t.Fatal("已关联视频的解冻流水禁止删除")
			}
			var hold billingmodel.WalletHold
			if err := db.Where("user_id=?", f.owner.UserID).First(&hold).Error; err != nil {
				t.Fatal(err)
			}
			if hold.Status != billingmodel.HoldStatusReleased {
				t.Fatal("Hold没有形成released终态")
			}
			if err := db.Model(&hold).Update("status", billingmodel.HoldStatusHolding).Error; err == nil {
				t.Fatal("财务终态不得回退")
			}
			usage, err := repository.NewVideoUsageRepository(db).ListForTask(context.Background(), f.command.TaskID, f.owner)
			if err != nil || len(usage) != 3 {
				t.Fatalf("缺少零数量、销售或未提交零成本事实: %v", err)
			}
			for _, u := range usage {
				if !u.Quantity.IsZero() || u.Amount == nil || !u.Amount.IsZero() || u.Source != "gateway" {
					t.Fatal("未提交取消不得伪造Provider成本")
				}
			}
			var events []model.AIOutboxEvent
			if err := db.Where("aggregate_type='video_request' AND aggregate_id=?", f.command.RequestID).Find(&events).Error; err != nil {
				t.Fatal(err)
			}
			if len(events) != 3 {
				t.Fatalf("held/released/rejected应各一条: %d", len(events))
			}
			task, err := tasks.FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || task.Status != model.AIImageTaskCancelled || task.AttemptCount != 0 || task.ProviderTaskID != nil {
				t.Fatalf("取消任务不得有Provider提交: %v", err)
			}
			bindings, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range bindings {
				if b.LeaseReleasedAt == nil {
					t.Fatal("安全取消后必须释放输入执行租约")
				}
			}
		})
	}
}
