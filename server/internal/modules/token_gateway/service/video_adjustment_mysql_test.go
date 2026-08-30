package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 金额取自独立合成样例：取消返还后10元，调增0.25后10.25，调减0.10后10.15。
func TestVideoG5AdjustmentMySQLAppendWalletAndReconcile(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	checker := f.owner.UserID + 900000
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", checker).Error; err != nil {
		t.Fatal(err)
	}
	for i, x := range []struct{ direction, amount, balance string }{{"credit", "0.25", "10.25"}, {"debit", "0.10", "10.15"}} {
		cmd := VideoAdjustmentCommand{Direction: x.direction, Reason: "billing_correction", Amount: decimal.RequireFromString(x.amount), MakerID: f.owner.UserID, CheckerID: checker, SequenceNo: uint32(i + 1)}
		r, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd)
		if err != nil || r == nil || r.Existing || r.UsageID == 0 || r.WalletTransactionID == 0 {
			t.Fatalf("合法调账应原子追加: %+v %v", r, err)
		}
		replay, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd)
		if err != nil || !replay.Existing || replay.UsageID != r.UsageID || replay.WalletTransactionID != r.WalletTransactionID {
			t.Fatal("同一调整必须幂等")
		}
		var wallet billingmodel.Wallet
		if err := db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil || !wallet.BalanceAmount.Equal(decimal.RequireFromString(x.balance)) || !wallet.FrozenAmount.IsZero() {
			t.Fatal("调整钱包不守恒")
		}
		report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
		if err != nil || !report.Passed {
			t.Fatalf("闭合调整应逐项零差异: %+v %v", report, err)
		}
	}
	var request model.VideoBillingRequest
	if err := db.Where("request_id=?", f.command.RequestID).First(&request).Error; err != nil || request.BillingStatus != model.AIBillingReleased || request.SettledAmount == nil || !request.SettledAmount.IsZero() {
		t.Fatal("调整不得伪造原请求消费或覆盖原终态")
	}
}

// 对同一序号重复提交100次，只能产生一次余额修正；各写点故障必须完整撤销。
func TestVideoG5AdjustmentMySQLConcurrencyRollbackAndMissingMovement(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CancelBeforeSubmit(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	checker := f.owner.UserID + 900000
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", checker).Error; err != nil {
		t.Fatal(err)
	}
	cmd := VideoAdjustmentCommand{Direction: "credit", Reason: "billing_correction", Amount: decimal.RequireFromString("0.25"), MakerID: f.owner.UserID, CheckerID: checker, SequenceNo: 1}
	for _, point := range []string{"adjustment_wallet", "adjustment_movement", "adjustment_usage", "adjustment_outbox"} {
		f.service.fault = func(at string) error {
			if at == point {
				return errors.New("合成调账故障")
			}
			return nil
		}
		if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); err == nil {
			t.Fatal("故障必须拒绝")
		}
		var wallet billingmodel.Wallet
		if err := db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil || !wallet.BalanceAmount.Equal(decimal.NewFromInt(10)) {
			t.Fatal("故障不应改变钱包")
		}
		report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
		if err != nil || !report.Passed {
			t.Fatalf("故障应完整回滚: %+v %v", report, err)
		}
	}
	f.service.fault = nil
	var wg sync.WaitGroup
	var bad, first atomic.Int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd)
			if err != nil {
				bad.Add(1)
			} else if !r.Existing {
				first.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 || first.Load() != 1 {
		t.Fatalf("同序号100并发应单一修正: bad=%d first=%d", bad.Load(), first.Load())
	}
	changed := cmd
	changed.Amount = decimal.RequireFromString("0.30")
	if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, changed); !errors.Is(err, ErrVideoBillingConflict) {
		t.Fatal("同序号异金额必须冲突")
	}
	changed = cmd
	changed.SequenceNo = 2
	changed.CheckerID = cmd.MakerID
	if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, changed); err == nil {
		t.Fatal("同人复核必须拒绝")
	}
	wrong := f.owner
	wrong.ProjectID++
	if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, wrong, cmd); !errors.Is(err, repository.ErrVideoTaskNotFound) {
		t.Fatal("跨Project不得调整")
	}
	// 合成历史异常：只追加双主体Adjustment但无资金动作，不允许对账把NULL当已执行。
	zero, currency, direction, reason := decimal.Zero, "CNY", "credit", "billing_correction"
	amount := decimal.RequireFromString("0.10")
	fact := model.AIUsageItem{RecordKind: model.AIUsageAdjustment, Source: "reconciled", SequenceNo: 2, Quantity: zero, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &amount, Currency: &currency, AdjustmentDirection: &direction, AdjustmentReason: &reason, AdjustmentOperatorID: &cmd.MakerID, AdjustmentReviewedBy: &checker}
	if _, _, err := repository.NewVideoUsageRepository(db).AppendAdjustmentTx(db, f.command.TaskID, f.owner, fact, time.Now().UTC(), nil); err != nil {
		t.Fatal(err)
	}
	report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
	if err != nil || report.Passed || report.Checks["adjustment"] {
		t.Fatalf("缺资金动作必须保持差异: %+v %v", report, err)
	}
}
