package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func videoG5ClosedAdjustmentFixture(t *testing.T, db *gorm.DB, balance string) (videoG5ReservationFixture, VideoAdjustmentCommand) {
	t.Helper()
	f := newVideoG5ReservationFixture(t, db, balance)
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
	return f, VideoAdjustmentCommand{Direction: "credit", Reason: "billing_correction", Amount: decimal.RequireFromString("0.25"), MakerID: f.owner.UserID, CheckerID: checker, SequenceNo: 1}
}

// 金额边界由DECIMAL(20,8)给出，错误命令不能留下新的资金或调整事实。
func TestVideoG5AdjustmentMySQLAmountBoundaries(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, cmd := videoG5ClosedAdjustmentFixture(t, db, "999999999999.75")
	for _, amount := range []string{"0.25", "0", "-0.01", "0.000000001", "1000000000000", "100000000000000000000"} {
		cmd.Amount = decimal.RequireFromString(amount)
		if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); err == nil {
			t.Fatalf("应拒绝精度/范围/余额溢出: %s", amount)
		}
		var wallet billingmodel.Wallet
		if err := db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil || wallet.BalanceAmount.StringFixed(8) != "999999999999.75000000" || !wallet.FrozenAmount.IsZero() {
			t.Fatal("拒绝金额必须保留余额")
		}
		var n int64
		if err := db.Model(&model.VideoUsageItem{}).Where("request_id=? AND record_kind='adjustment'", f.command.RequestID).Count(&n).Error; err != nil || n != 0 {
			t.Fatal("拒绝金额不得追加调整")
		}
	}
	cmd.Amount = decimal.RequireFromString("0.24999999")
	if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); err != nil {
		t.Fatalf("最大可表示余额应成功: %v", err)
	}
	report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
	if err != nil || !report.Passed {
		t.Fatal("最大合法金额应保持对账闭合")
	}
}

// 归属正确也不能忽略同request_id下类型错误的额外财务Outbox。
func TestVideoG5AdjustmentMySQLRejectsForeignAggregateOutbox(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, cmd := videoG5ClosedAdjustmentFixture(t, db, "10")
	if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); err != nil {
		t.Fatal(err)
	}
	var event model.AIOutboxEvent
	if err := db.Where("aggregate_id=? AND event_type='video_adjustment_recorded'", f.command.RequestID).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	event.ID = 0
	event.EventID += "_foreign"
	event.AggregateType = "foreign_request"
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
	if err != nil || report.Passed || report.Checks["adjustment"] {
		t.Fatalf("不能过滤掉错误聚合类型的额外调整事实: %+v %v", report, err)
	}
}

// 视频发布器未获授权时，错误聚合类型也不能让视频财务事件进入共享领取队列。
func TestVideoG5AdjustmentMySQLInvalidAggregateNeverClaimed(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, cmd := videoG5ClosedAdjustmentFixture(t, db, "10")
	if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); err != nil {
		t.Fatal(err)
	}
	var event model.AIOutboxEvent
	if err := db.Where("aggregate_id=? AND event_type='video_adjustment_recorded'", f.command.RequestID).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	event.ID = 0
	event.EventID += "_claim"
	event.AggregateType = "foreign_request"
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	oldLease := now.Add(-time.Hour)
	reclaim := event
	reclaim.ID = 0
	reclaim.EventID += "_reclaim"
	reclaim.AggregateType = "foreign_reclaim"
	reclaim.Status = model.AIOutboxPublishing
	reclaim.LockedAt = &oldLease
	if err := db.Create(&reclaim).Error; err != nil {
		t.Fatal(err)
	}
	control := model.AIOutboxEvent{EventID: f.command.RequestID + "_control", AggregateID: f.command.RequestID + "_control", AggregateType: "ai_request", EventType: "request.held", PayloadJSON: json.RawMessage(`{}`), Status: model.AIOutboxPending, NextRetryAt: now}
	if err := db.Create(&control).Error; err != nil {
		t.Fatal(err)
	}
	imageControl := control
	imageControl.ID = 0
	imageControl.EventID += "_image"
	imageControl.AggregateID += "_image"
	imageControl.AggregateType = "image_request"
	imageControl.EventType = "image_billing_held"
	if err := db.Create(&imageControl).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := repository.NewG3OutboxRepository(db).ClaimBatch(context.Background(), now.Add(time.Second), now.Add(-time.Minute), 10000)
	if err != nil {
		t.Fatal(err)
	}
	foundControl := false
	foundImage := false
	for _, row := range claimed {
		if row.ID == event.ID || row.ID == reclaim.ID {
			t.Error("错误聚合类型不能绕过视频发布器关闭边界")
		}
		if row.ID == control.ID {
			foundControl = true
		}
		if row.ID == imageControl.ID {
			foundImage = true
		}
	}
	if !foundControl || !foundImage {
		t.Fatal("排除视频不能阻断旧事件领取")
	}
	var pendingAfter, reclaimAfter model.AIOutboxEvent
	if err := db.First(&pendingAfter, event.ID).Error; err != nil || pendingAfter.Status != model.AIOutboxPending || pendingAfter.LockedAt != nil {
		t.Fatal("被排除pending事件必须保持未领取")
	}
	if err := db.First(&reclaimAfter, reclaim.ID).Error; err != nil || reclaimAfter.Status != model.AIOutboxPublishing || reclaimAfter.LockedAt == nil || !reclaimAfter.LockedAt.Equal(oldLease) {
		t.Fatal("被排除publishing事件不得取得新租约")
	}
}

func TestVideoG5AdjustmentMySQLOutboxPayloadCorruption(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, cmd := videoG5ClosedAdjustmentFixture(t, db, "10")
	if _, err := f.service.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); err != nil {
		t.Fatal(err)
	}
	var event model.AIOutboxEvent
	if err := db.Where("aggregate_id=? AND event_type='video_adjustment_recorded'", f.command.RequestID).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"request_id", "status", "amount", "currency", "operation", "version", "sequence_no", "extra", "version_number", "sequence_number", "amount_value"} {
		var body map[string]interface{}
		if err := json.Unmarshal(event.PayloadJSON, &body); err != nil {
			t.Fatal(err)
		}
		switch field {
		case "version_number":
			body["version"] = 2
		case "sequence_number":
			body["sequence_no"] = 2
		case "amount_value":
			body["amount"] = "0.26000000"
		default:
			body[field] = "incorrect"
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rollback := errors.New("撤销合成Outbox故障")
		err = db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.AIOutboxEvent{}).Where("id=?", event.ID).Update("payload_json", payload).Error; err != nil {
				return err
			}
			report, err := NewVideoReconciliationService(tx).Reconcile(context.Background(), f.command.TaskID, f.owner)
			if err != nil {
				return err
			}
			if report.Passed || report.Checks["adjustment"] {
				t.Errorf("错误Outbox字段未阻断: %s", field)
			}
			local := *f.service
			local.db = tx
			if _, err := local.ApplyAdjustment(context.Background(), f.command.TaskID, f.owner, cmd); err == nil {
				t.Errorf("错误Outbox不能借幂等重放通过: %s", field)
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
}

// 三种行数都为1且余额链正确，缺失明确资金关联仍必须失败，不能只比较计数。
func TestVideoG5AdjustmentMySQLNullMovementWithEqualCounts(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, cmd := videoG5ClosedAdjustmentFixture(t, db, "10")
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var wallet billingmodel.Wallet
		if err := tx.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil {
			return err
		}
		balance := decimal.RequireFromString("10.25")
		if err := tx.Model(&wallet).Updates(map[string]interface{}{"balance_amount": balance, "version": gorm.Expr("version+1")}).Error; err != nil {
			return err
		}
		movement := billingmodel.WalletTransaction{WalletID: wallet.ID, UserID: f.owner.UserID, Type: "refund", Direction: "in", Amount: cmd.Amount, BalanceAfter: balance, Remark: videoAdjustmentWalletRemark(task.RequestID, 1), CreatedAt: time.Now().UTC()}
		if err := tx.Create(&movement).Error; err != nil {
			return err
		}
		zero, currency := decimal.Zero, "CNY"
		fact := model.AIUsageItem{RecordKind: model.AIUsageAdjustment, Source: "reconciled", SequenceNo: 1, Quantity: zero, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &cmd.Amount, Currency: &currency, AdjustmentDirection: &cmd.Direction, AdjustmentReason: &cmd.Reason, AdjustmentOperatorID: &cmd.MakerID, AdjustmentReviewedBy: &cmd.CheckerID}
		item, _, err := repository.NewVideoUsageRepository(tx).AppendAdjustmentTx(tx, task.PublicID, f.owner, fact, time.Now().UTC(), nil)
		if err != nil {
			return err
		}
		payload, err := videoAdjustmentPayload(task, item)
		if err != nil {
			return err
		}
		return tx.Create(&model.AIOutboxEvent{EventID: videoAdjustmentEventID(task.RequestID, 1), AggregateType: "video_request", AggregateID: task.RequestID, EventType: "video_adjustment_recorded", PayloadJSON: payload, Status: model.AIOutboxPending, NextRetryAt: time.Now().UTC()}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []*gorm.DB{
		db.Model(&model.VideoUsageItem{}).Where("request_id=? AND record_kind='adjustment'", task.RequestID),
		db.Model(&model.AIOutboxEvent{}).Where("aggregate_id=? AND event_type='video_adjustment_recorded'", task.RequestID),
		db.Model(&billingmodel.WalletTransaction{}).Where("remark=?", videoAdjustmentWalletRemark(task.RequestID, 1)),
	} {
		var n int64
		if err := query.Count(&n).Error; err != nil || n != 1 {
			t.Fatalf("等行数夹具必须各有一条: %d %v", n, err)
		}
	}
	report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), task.PublicID, f.owner)
	if err != nil || report.Passed || report.Checks["adjustment"] || !report.Checks["hold"] {
		t.Fatalf("合法余额链不能代替资金关联: %+v %v", report, err)
	}
}
