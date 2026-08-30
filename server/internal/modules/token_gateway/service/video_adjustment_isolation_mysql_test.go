package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 同一金额不代表同一资金动作，外部钱包流水与已关联流水都不能被再次使用。
func TestVideoG5AdjustmentMySQLWalletReferenceAndOwnerIsolation(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, cmd := videoG5ClosedAdjustmentFixture(t, db, "10")
	other, otherCmd := videoG5ClosedAdjustmentFixture(t, db, "10")
	// 此流水尚未被引用，排除唯一键提前拒绝而掩盖跨钱包校验失效的假阳性。
	rollback := errors.New("撤销未引用外部资金夹具")
	err := db.Transaction(func(tx *gorm.DB) error {
		var wallet billingmodel.Wallet
		if err := tx.Where("user_id=?", other.owner.UserID).First(&wallet).Error; err != nil {
			return err
		}
		balance := decimal.RequireFromString("10.25")
		if err := tx.Model(&wallet).Updates(map[string]interface{}{"balance_amount": balance, "version": gorm.Expr("version+1")}).Error; err != nil {
			return err
		}
		movement := billingmodel.WalletTransaction{WalletID: wallet.ID, UserID: other.owner.UserID, Type: "refund", Direction: "in", Amount: cmd.Amount, BalanceAfter: balance, Remark: videoAdjustmentWalletRemark(f.command.RequestID, cmd.SequenceNo), CreatedAt: time.Now().UTC()}
		if err := tx.Create(&movement).Error; err != nil {
			return err
		}
		var references int64
		if err := tx.Model(&model.VideoUsageItem{}).Where("adjustment_wallet_transaction_id=?", movement.ID).Count(&references).Error; err != nil {
			return err
		}
		if references != 0 {
			t.Fatal("跨钱包夹具不得已经被引用")
		}
		zero, currency := decimal.Zero, "CNY"
		fact := model.AIUsageItem{RecordKind: model.AIUsageAdjustment, Source: "reconciled", SequenceNo: 1, Quantity: zero, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &cmd.Amount, Currency: &currency, AdjustmentDirection: &cmd.Direction, AdjustmentReason: &cmd.Reason, AdjustmentOperatorID: &cmd.MakerID, AdjustmentReviewedBy: &cmd.CheckerID}
		if _, _, err := repository.NewVideoUsageRepository(tx).AppendAdjustmentTx(tx, f.command.TaskID, f.owner, fact, time.Now().UTC(), &movement.ID); err == nil {
			t.Error("未引用的外部钱包新流水也必须拒绝")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	foreign, err := other.service.ApplyAdjustment(context.Background(), other.command.TaskID, other.owner, otherCmd)
	if err != nil {
		t.Fatal(err)
	}
	zero, currency := decimal.Zero, "CNY"
	fact := model.AIUsageItem{RecordKind: model.AIUsageAdjustment, Source: "reconciled", SequenceNo: 1, Quantity: zero, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &cmd.Amount, Currency: &currency, AdjustmentDirection: &cmd.Direction, AdjustmentReason: &cmd.Reason, AdjustmentOperatorID: &cmd.MakerID, AdjustmentReviewedBy: &cmd.CheckerID}
	if _, _, err := repository.NewVideoUsageRepository(db).AppendAdjustmentTx(db, f.command.TaskID, f.owner, fact, time.Now().UTC(), &foreign.WalletTransactionID); err == nil {
		t.Fatal("不能引用另一用户钱包的同金额流水")
	}
	valid, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd)
	if err != nil {
		t.Fatal(err)
	}
	fact.SequenceNo = 2
	if _, _, err := repository.NewVideoUsageRepository(db).AppendAdjustmentTx(db, f.command.TaskID, f.owner, fact, time.Now().UTC(), &valid.WalletTransactionID); err == nil {
		t.Fatal("第二条调整不能复用已关联资金动作")
	}
	owners := []repository.VideoOwner{f.owner, f.owner, f.owner}
	owners[0].UserID = other.owner.UserID
	owners[1].ProjectID = other.owner.ProjectID
	owners[2].APIKeyID = other.owner.APIKeyID
	cmd.SequenceNo = 2
	for _, owner := range owners {
		if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, owner, cmd); !errors.Is(err, repository.ErrVideoTaskNotFound) {
			t.Fatalf("调账越权统一不存在: %v", err)
		}
	}
	for _, fixture := range []videoG5ReservationFixture{f, other} {
		var wallet billingmodel.Wallet
		if err := db.Where("user_id=?", fixture.owner.UserID).First(&wallet).Error; err != nil || wallet.BalanceAmount.StringFixed(8) != "10.25000000" || !wallet.FrozenAmount.IsZero() {
			t.Fatal("越权/重复引用不能改变两个钱包")
		}
		report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), fixture.command.TaskID, fixture.owner)
		if err != nil || !report.Passed {
			t.Fatalf("拒绝后原账仍须闭合: %+v %v", report, err)
		}
	}
}

func TestVideoG5AdjustmentMySQLInvalidCommandNoFacts(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, base := videoG5ClosedAdjustmentFixture(t, db, "10")
	for _, mode := range []string{"sequence", "direction", "reason", "same_actor", "missing_actor"} {
		cmd := base
		switch mode {
		case "sequence":
			cmd.SequenceNo = 0
		case "direction":
			cmd.Direction = "unknown"
		case "reason":
			cmd.Reason = "arbitrary"
		case "same_actor":
			cmd.CheckerID = cmd.MakerID
		case "missing_actor":
			cmd.CheckerID = 999999999
		}
		if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); err == nil {
			t.Fatalf("无效调整命令必须拒绝: %s", mode)
		}
	}
	var n int64
	if err := db.Model(&model.VideoUsageItem{}).Where("request_id=? AND record_kind='adjustment'", f.command.RequestID).Count(&n).Error; err != nil || n != 0 {
		t.Fatal("无效命令不能追加事实")
	}
	report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
	if err != nil || !report.Passed {
		t.Fatal("无效命令不能破坏原账")
	}
}
