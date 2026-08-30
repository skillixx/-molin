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
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 原销售与成本已闭合后追加信用调整，T2V/I2V仍按各自原价核对，不重算旧账。
func TestVideoG5AdjustmentMySQLSettledOperationsAndOriginalMovement(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, i2v := range []bool{false, true} {
		f := newVideoG5ReservationFixture(t, db, "10")
		if i2v {
			prepareVideoG5I2V(t, &f)
		}
		if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
			t.Fatal(err)
		}
		runVideoG5ReadyFixture(t, f)
		if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner); err != nil {
			t.Fatal(err)
		}
		checker := f.owner.UserID + 900000
		if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", checker).Error; err != nil {
			t.Fatal(err)
		}
		var original model.AIRequestWalletLink
		if err := db.Where("request_id=?", f.command.RequestID).First(&original).Error; err != nil {
			t.Fatal(err)
		}
		// 伪造“新增扣款”却引用原消费，金额完全匹配仍必须拒绝。
		zero, currency, direction, reason := decimal.Zero, "CNY", "debit", "billing_correction"
		amount := *original.SettledAmount
		fact := model.AIUsageItem{RecordKind: model.AIUsageAdjustment, Source: "reconciled", SequenceNo: 1, Quantity: zero, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &amount, Currency: &currency, AdjustmentDirection: &direction, AdjustmentReason: &reason, AdjustmentOperatorID: &f.owner.UserID, AdjustmentReviewedBy: &checker}
		if _, _, err := repository.NewVideoUsageRepository(db).AppendAdjustmentTx(db, f.command.TaskID, f.owner, fact, time.Now().UTC(), original.SettleTransactionID); err == nil {
			t.Fatal("原消费不能冒充新增调账动作")
		}
		cmd := VideoAdjustmentCommand{Direction: "credit", Reason: "service_credit", Amount: decimal.RequireFromString("0.10"), MakerID: f.owner.UserID, CheckerID: checker, SequenceNo: 1}
		result, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd)
		if err != nil {
			t.Fatal(err)
		}
		want := "9.60"
		if i2v {
			want = "9.35"
		}
		var wallet billingmodel.Wallet
		if err := db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil || !wallet.BalanceAmount.Equal(decimal.RequireFromString(want)) {
			t.Fatal("调整后余额必须匹配独立金样")
		}
		if err := db.Model(&billingmodel.WalletTransaction{}).Where("id=?", result.WalletTransactionID).Update("remark", "overwrite").Error; err == nil {
			t.Fatal("调账资金动作不可覆盖")
		}
		if err := db.Delete(&billingmodel.WalletTransaction{}, result.WalletTransactionID).Error; err == nil {
			t.Fatal("调账资金动作不可删除")
		}
		if err := db.Model(&model.VideoUsageItem{}).Where("id=?", result.UsageID).Update("amount", "0.2").Error; err == nil {
			t.Fatal("原调整事实不可覆盖")
		}
		// 历史复核不会因为主体后来停用而失效；新调整仍须活跃双主体。
		if err := db.Exec("UPDATE users SET status='banned' WHERE id=?", checker).Error; err != nil {
			t.Fatal(err)
		}
		report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
		if err != nil || !report.Passed {
			t.Fatalf("合法调整应逐项闭合: %+v %v", report, err)
		}
		cmd.SequenceNo = 2
		if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); !errors.Is(err, ErrVideoBillingAccess) {
			t.Fatal("已停用主体不得发起新调整")
		}
	}
}

// 一百个不同序号竞争同一余额，合成10元最多支持50笔0.2元调整，不得触及冻结额。
func TestVideoG5AdjustmentMySQLConcurrentDebitsNoOverdraft(t *testing.T) {
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
	var wg sync.WaitGroup
	var ok, insufficient, bad atomic.Int32
	for i := 1; i <= 100; i++ {
		wg.Add(1)
		go func(sequence uint32) {
			defer wg.Done()
			cmd := VideoAdjustmentCommand{Direction: "debit", Reason: "billing_correction", Amount: decimal.RequireFromString("0.2"), MakerID: f.owner.UserID, CheckerID: checker, SequenceNo: sequence}
			_, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd)
			if err == nil {
				ok.Add(1)
			} else if errors.Is(err, billingservice.ErrInsufficientBalance) {
				insufficient.Add(1)
			} else {
				bad.Add(1)
			}
		}(uint32(i))
	}
	wg.Wait()
	if ok.Load() != 50 || insufficient.Load() != 50 || bad.Load() != 0 {
		t.Fatalf("余额竞争应50成功50拒绝: %d/%d/%d", ok.Load(), insufficient.Load(), bad.Load())
	}
	var wallet billingmodel.Wallet
	if err := db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil || !wallet.BalanceAmount.IsZero() || !wallet.FrozenAmount.IsZero() {
		t.Fatal("不得透支或改变冻结额")
	}
	report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
	if err != nil || !report.Passed {
		t.Fatalf("并发后必须闭合: %+v %v", report, err)
	}
}
