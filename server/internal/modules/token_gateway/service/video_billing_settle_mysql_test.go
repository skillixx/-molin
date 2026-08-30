package service

import (
	"context"
	"errors"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// TestVideoG5SettleMySQLRejectsStatusOnlyShortcut 公开状态仓储不能代替真实钱包结算，防止免费交付。
func TestVideoG5SettleMySQLRejectsStatusOnlyShortcut(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	runVideoG5ReadyFixture(t, f)
	var invalid model.VideoBillingRequest
	if err := db.Where("request_id=?", f.command.RequestID).First(&invalid).Error; err != nil {
		t.Fatal(err)
	}
	invalid.ID = 0
	invalid.RequestID += "_invalid"
	invalid.IntentKeyHash = strings.Repeat("f", 64)
	invalid.BillingStatus = model.AIBillingSettled
	invalid.DeliveryStatus = model.AIDeliveryAvailable
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("不得直接INSERT已结算可交付的G5请求")
	}
	tasks := repository.NewVideoTaskRepository(db)
	task, err := tasks.FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	task, err = tasks.TransitionBilling(context.Background(), videoCancelTransition(task, f.owner, model.AIBillingSettlementPending, "shortcut_pending", time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.TransitionBilling(context.Background(), videoCancelTransition(task, f.owner, model.AIBillingSettled, "shortcut_settled", time.Now())); err == nil {
		t.Fatal("没有真实消费流水不得仅推进settled状态")
	}
}

// TestVideoG5SettleMySQLRollbackRetainsConfirmation 结算写入故障只回滚财务变更，已经确认的Provider成本仍可追溯。
func TestVideoG5SettleMySQLRollbackRetainsConfirmation(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, step := range []string{"settle_pending", "settle_hold", "settle_link", "settle_usage_fact", "settle_sale_line", "settle_state", "settle_outbox", "settle_lease"} {
		t.Run(step, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			prepareVideoG5I2V(t, &f)
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			_, adapter := runVideoG5ReadyFixture(t, f)
			f.service.fault = func(at string) error {
				if at == step {
					return errors.New("合成结算故障")
				}
				return nil
			}
			if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("故障必须返回失败")
			}
			var w billingmodel.Wallet
			if err := db.Where("user_id=?", f.owner.UserID).First(&w).Error; err != nil {
				t.Fatal(err)
			}
			if w.BalanceAmount.StringFixed(8) != "9.25000000" || w.FrozenAmount.StringFixed(8) != "0.75000000" {
				t.Fatal("失败结算不得改变余额/冻结额")
			}
			facts, err := repository.NewVideoUsageRepository(db).ListForTask(context.Background(), f.command.TaskID, f.owner)
			if err != nil || len(facts) != 2 {
				t.Fatalf("确认计量/成本保留但用户计量/销售应回滚: %v", err)
			}
			var n int64
			if err := db.Model(&billingmodel.WalletTransaction{}).Where("user_id=?", f.owner.UserID).Count(&n).Error; err != nil || n != 1 {
				t.Fatalf("只应有原冻结流水: %d %v", n, err)
			}
			bindings, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || len(bindings) != 1 || bindings[0].LeaseReleasedAt != nil {
				t.Fatalf("未结算必须保护输入租约: %v", err)
			}
			f.service.fault = nil
			worker, err := NewVideoCompensationWorker(f.service, "rollback-worker")
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := worker.RunOne(context.Background(), f.command.RequestID)
			if err != nil || recovered.Financial == nil || recovered.Financial.BillingStatus != model.AIBillingSettled {
				t.Fatalf("应由补偿Worker恢复财务: %v", err)
			}
			if adapter.SubmitCalls() != 1 {
				t.Fatal("恢复财务不得重复提交Provider")
			}
		})
	}
}

// TestVideoG5SettleMySQLMediaExpiryDuringWrites 媒体等待锁或钱包写入期间过期，不得收款后才发现不可交付。
func TestVideoG5SettleMySQLMediaExpiryDuringWrites(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, step := range []string{"settle_pending", "settle_outbox"} {
		t.Run(step, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			runVideoG5ReadyFixture(t, f)
			var asset model.AIImageAsset
			if err := db.Where("request_id=?", f.command.RequestID).First(&asset).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			f.service.now = func() time.Time { return now }
			f.service.fault = func(at string) error {
				if at == step {
					now = asset.ExpiresAt.Add(time.Hour)
				}
				return nil
			}
			if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("媒体已过期仍成功扣费")
			}
			var w billingmodel.Wallet
			if err := db.Where("user_id=?", f.owner.UserID).First(&w).Error; err != nil {
				t.Fatal(err)
			}
			if w.BalanceAmount.StringFixed(8) != "9.50000000" || w.FrozenAmount.StringFixed(8) != "0.50000000" {
				t.Fatal("过期失败必须完整回滚结算")
			}
		})
	}
}

// TestVideoG5SettleMySQLReplayChecksAllOutbox 幂等不能掩盖原held事实、相反终态或payload版本异常。
func TestVideoG5SettleMySQLReplayChecksAllOutbox(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"held_amount", "settled_version", "opposite_event"} {
		t.Run(mode, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			runVideoG5ReadyFixture(t, f)
			if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "held_amount":
				err = db.Exec("UPDATE ai_outbox_events SET payload_json=JSON_SET(payload_json,'$.amount','999.00000000') WHERE aggregate_id=? AND event_type='video_billing_held'", f.command.RequestID).Error
			case "settled_version":
				err = db.Exec("UPDATE ai_outbox_events SET payload_json=JSON_SET(payload_json,'$.version',99) WHERE aggregate_id=? AND event_type='video_billing_settled'", f.command.RequestID).Error
			case "opposite_event":
				err = db.Transaction(func(tx *gorm.DB) error {
					return createVideoBillingOutboxTx(tx, f.command.RequestID, "video_billing_released", model.AIBillingReleased, model.AIVideoOperationTextToVideo, f.quote.QuotedAmount, time.Now())
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); !errors.Is(err, ErrVideoBillingState) {
				t.Fatalf("Outbox不一致应拒绝重放: %v", err)
			}
		})
	}
}

// TestVideoG5SettleMySQLConfirmationEvidenceRejectsWrongScale 摘要事件不能被用来追加错误分母或不匹配金额的成本。
func TestVideoG5SettleMySQLConfirmationEvidenceRejectsWrongScale(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	tasks := repository.NewVideoTaskRepository(db)
	task, err := tasks.FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{model.AIImageTaskQueued, model.AIImageTaskSubmitting} {
		task, err = tasks.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: task.PublicID, Owner: f.owner, ExpectedVersion: task.VersionNo, ToStatus: status, Progress: task.Progress, EventID: f.command.RequestID + status, Source: "worker", Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
	}
	task, err = tasks.BindProviderTask(context.Background(), repository.VideoProviderBinding{TaskPublicID: task.PublicID, Owner: f.owner, ExpectedVersion: task.VersionNo, ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-evidence-scale", EventID: f.command.RequestID + "bind", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	price, amount, currency := decimal.RequireFromString("0.04"), decimal.RequireFromString("0.20"), "CNY"
	event := model.VideoFinancialEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: f.command.RequestID + "evidence", TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "provider_cost_succeeded", Source: "worker", CreatedAt: time.Now()}, FactSHA256: strings.Repeat("1", 64)}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	correct := event
	correct.ID = 0
	correct.EventID += "_correct"
	correct.FactSHA256 = videogateway.ProviderCostFactSHA256(task.RequestID, videogateway.ProviderCostConfirmation{ProviderCode: *task.ProviderCode, ProviderTaskID: *task.ProviderTaskID, Operation: *task.Operation, Outcome: videogateway.ProviderTaskSucceeded, Quantity: decimal.NewFromInt(5), UnitPrice: price, Amount: amount, Currency: currency})
	if err := db.Create(&correct).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		event   uint64
		scale   int64
		allowed bool
	}{{event.ID, 1, false}, {correct.ID, 100, false}, {correct.ID, 1, true}} {
		fact := model.AIUsageItem{RecordKind: model.AIUsageCostLine, Source: "provider_cost", Quantity: decimal.NewFromInt(5), UnitSize: decimal.NewFromInt(tc.scale), UnitPrice: &price, Amount: &amount, Currency: &currency}
		var appendErr error
		rollback := errors.New("回滚合成反例")
		if err := db.Transaction(func(tx *gorm.DB) error {
			_, _, appendErr = repository.NewVideoUsageRepository(tx).AppendEvidenceTx(tx, task.PublicID, f.owner, fact, time.Now(), tc.event)
			return rollback
		}); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		if (appendErr == nil) != tc.allowed {
			t.Errorf("仓储摘要/分母门禁不符: scale=%d allowed=%v err=%v", tc.scale, tc.allowed, appendErr)
		}
		fact.RequestID, fact.Operation, fact.PriceVersionID = task.RequestID, task.Operation, &f.quote.PriceVersionID
		fact.MeterType, fact.UsageUnit, fact.VariantHash, fact.VariantJSON = VideoMeterSeconds, "seconds", f.quote.RequestVariantHash, task.InputJSON
		direct := model.VideoUsageItem{AIUsageItem: fact, TaskID: task.ID, QuoteID: task.QuoteID, UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID, LogicalModelCode: task.LogicalModelCode, Capability: model.AIVideoCapability, EvidenceEventID: &tc.event}
		var sqlErr error
		if err := db.Transaction(func(tx *gorm.DB) error { sqlErr = tx.Create(&direct).Error; return rollback }); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		if (sqlErr == nil) != tc.allowed {
			t.Errorf("SQL摘要/分母门禁不符: scale=%d allowed=%v err=%v", tc.scale, tc.allowed, sqlErr)
		}
	}
}

func runVideoG5ReadyFixture(t *testing.T, f videoG5ReservationFixture) (*videogateway.VideoGateway, *videogateway.FakeAsyncVideoAdapter) {
	t.Helper()
	ledger := NewVideoBillingTaskLedger(f.db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
	adapter := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	gateway := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: videogateway.NewFakeVideoObjectStore()})
	if _, err := gateway.Submit(context.Background(), f.command.TaskID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := gateway.Poll(context.Background(), f.command.TaskID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gateway.FetchAndFinalize(context.Background(), f.command.TaskID); err != nil {
		t.Fatal(err)
	}
	return gateway, adapter
}

// TestVideoG5SettleMySQLConfirmedCostAndSingleConsumption 正常Fake完成后100并发只消费一次，成本独立于销售。
func TestVideoG5SettleMySQLConfirmedCostAndSingleConsumption(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(op, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if op == model.AIVideoOperationImageToVideo {
				prepareVideoG5I2V(t, &f)
			}
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			_, adapter := runVideoG5ReadyFixture(t, f)
			var wg sync.WaitGroup
			var applied, replay atomic.Int64
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					result, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner)
					if err != nil {
						t.Errorf("结算失败: %v", err)
						return
					}
					if result.Existing {
						replay.Add(1)
					} else {
						applied.Add(1)
					}
					if result.BillingStatus != model.AIBillingSettled || !result.SettledAmount.Equal(f.quote.QuotedAmount) || !result.ReleasedAmount.IsZero() || result.DeliveryStatus != model.AIDeliveryPending {
						t.Error("结算或交付状态不一致")
					}
				}()
			}
			wg.Wait()
			if applied.Load() != 1 || replay.Load() != 99 {
				t.Fatalf("重复结算: %d/%d", applied.Load(), replay.Load())
			}
			var wallet billingmodel.Wallet
			if err := db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil {
				t.Fatal(err)
			}
			wantBalance, wantCost := "9.50000000", "0.20000000"
			if op == model.AIVideoOperationImageToVideo {
				wantBalance, wantCost = "9.25000000", "0.30000000"
			}
			if wallet.BalanceAmount.StringFixed(8) != wantBalance || !wallet.FrozenAmount.IsZero() {
				t.Fatal("钱包不守恒")
			}
			usage, err := repository.NewVideoUsageRepository(db).ListForTask(context.Background(), f.command.TaskID, f.owner)
			if err != nil || len(usage) != 4 {
				t.Fatalf("应保留Provider计量、确认成本、用户计量与销售四条事实: %v", err)
			}
			var costs, sales int
			for _, u := range usage {
				if u.RecordKind == model.AIUsageCostLine {
					costs++
					if u.Source != "provider_cost" || u.Amount == nil || u.Amount.StringFixed(8) != wantCost {
						t.Error("成本必须来自Provider已确认事实")
					}
				}
				if u.RecordKind == model.AIUsageSaleLine {
					sales++
					if u.Amount == nil || !u.Amount.Equal(f.quote.QuotedAmount) {
						t.Error("销售错价")
					}
				}
			}
			if costs != 1 || sales != 1 || adapter.SubmitCalls() != 1 {
				t.Fatal("重复财务事实或Provider提交")
			}
			var available int64
			if err := db.Model(&model.AIImageAsset{}).Where("request_id=? AND lifecycle_state='available'", f.command.RequestID).Count(&available).Error; err != nil || available != 0 {
				t.Fatalf("仅结算成功仍不能绕过独立交付门禁: %v", err)
			}
			var consume int64
			if err := db.Model(&billingmodel.WalletTransaction{}).Where("user_id=? AND type='consume'", f.owner.UserID).Count(&consume).Error; err != nil || consume != 1 {
				t.Fatalf("消费流水必须唯一: %d %v", consume, err)
			}
		})
	}
}
