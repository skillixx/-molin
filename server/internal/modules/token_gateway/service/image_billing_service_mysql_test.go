package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	billingmodel "molin/server/internal/modules/billing/model"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingservice "molin/server/internal/modules/billing/service"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

const (
	imageG5ModelCode      = "molin/image-g5-mysql"
	imageG5PriceVersionID = uint64(97001)
	imageG5OperatorID     = uint64(97001)
	imageG5ReviewerID     = uint64(97002)
)

type imageBillingFixture struct {
	service     *ImageBillingService
	adapter     *imagegateway.FakeImageAdapter
	store       imagegateway.ObjectStore
	owner       repository.ImageOwner
	requestID   string
	quoteID     string
	fingerprint string
	command     imagegateway.GenerateImageCommand
}

func TestImageBillingServiceMySQLClosedLoop(t *testing.T) {
	if os.Getenv("MOLIN_IMAGE_G5_ISOLATED") != "YES" {
		t.Skip("IMG-G5 只允许隔离MySQL门禁执行")
	}
	dsn := os.Getenv("MOLIN_IMAGE_G5_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置 IMG-G5 隔离 MySQL DSN")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := db.Raw("SELECT DATABASE()").Scan(&databaseName).Error; err != nil || databaseName != "molin_image_g5_contract" {
		t.Fatalf("IMG-G5拒绝连接非隔离数据库: database=%s err=%v", databaseName, err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(140)
	sqlDB.SetMaxIdleConns(140)
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	setupImageG5Base(t, db, now)

	t.Run("成功与同请求100并发预占", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97101, "g5-success", 2, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		var successes atomic.Int64
		var wg sync.WaitGroup
		for index := 0; index < 100; index++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := fixture.service.Reserve(context.Background(), fixture.reserveCommand()); err != nil {
					t.Errorf("同请求并发预占失败: %v", err)
					return
				}
				successes.Add(1)
			}()
		}
		wg.Wait()
		if successes.Load() != 100 {
			t.Fatalf("同请求预占应全部幂等成功: %d", successes.Load())
		}
		assertImageG5Count(t, db, "wallet_holds", "user_id = ?", fixture.owner.UserID, 1)
		assertImageG5Count(t, db, "ai_request_wallet_links", "request_id = ?", fixture.requestID, 1)
		assertImageG5Count(t, db, "ai_outbox_events", "aggregate_id = ? AND event_type = 'image_billing_held'", fixture.requestID, 1)
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.00000000", "1.00000000")

		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if err != nil || execution.BillingStatus != model.AIBillingSettled || execution.DeliveryStatus != model.AIDeliveryAvailable {
			diagnosticErr := fixture.service.finalizeSuccess(context.Background(), fixture.requestID, execution.GatewayResult.ProviderResultCount)
			t.Fatalf("成功闭环失败: execution=%+v err=%v diagnostic=%v snapshot=%s", execution, err, diagnosticErr, imageG5PricingDiagnostic(db, fixture.requestID))
		}
		if fixture.adapter.Calls() != 1 {
			t.Fatalf("成功请求Provider调用必须为1: %d", fixture.adapter.Calls())
		}
		replayedReservation, err := fixture.service.Reserve(context.Background(), fixture.reserveCommand())
		if err != nil || replayedReservation.HoldID == 0 {
			t.Fatalf("终态后的预占重放必须返回原hold: reservation=%+v err=%v", replayedReservation, err)
		}
		assertImageG5Count(t, db, "wallet_transactions", "user_id = ? AND type = 'freeze'", fixture.owner.UserID, 1)
		if _, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command); !errors.Is(err, ErrImageExecutionStarted) || fixture.adapter.Calls() != 1 {
			t.Fatalf("执行重放不得再次调用Provider: calls=%d err=%v", fixture.adapter.Calls(), err)
		}
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.00000000", "0.00000000")
		assertImageG5Count(t, db, "ai_gateway_assets", "request_id = ? AND lifecycle_state = 'available'", fixture.requestID, 4)
		assertImageG5Count(t, db, "ai_usage_items", "request_id = ? AND record_kind IN ('usage_fact','sale_line','cost_line')", fixture.requestID, 3)
		report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID)
		if err != nil || !report.ZeroDifference() {
			t.Fatalf("成功请求必须零差异: report=%+v err=%v", report, err)
		}
		if _, err := fixture.service.assets.FindDeliverable(context.Background(), imageAssetPublicID(fixture.requestID, 0, model.AIImageAssetPrimaryOutput), fixture.owner); err != nil {
			t.Fatalf("结算提交后主图应可交付: %v", err)
		}
		if err := fixture.service.AppendAdjustment(context.Background(), fixture.requestID, "credit", "测试调账审计", decimal.RequireFromString("0.1"), imageG5OperatorID, imageG5OperatorID, 9); !errors.Is(err, ErrImageAdjustmentInvalid) {
			t.Fatalf("maker/checker相同必须拒绝: %v", err)
		}
		if err := fixture.service.AppendAdjustment(context.Background(), fixture.requestID, "credit", "测试调账审计", decimal.RequireFromString("0.1"), imageG5OperatorID, imageG5ReviewerID, 9); err != nil {
			t.Fatal(err)
		}
		if report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID); !errors.Is(err, ErrImageReconcileMismatch) || report.AdjustmentCount != 1 {
			t.Fatalf("未配套钱包动作的调账必须让对账失败关闭: report=%+v err=%v", report, err)
		}
	})

	t.Run("部分成功按可交付数量结算", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97102, "g5-partial", 2, imagegateway.FakeImagePartial, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if err != nil || execution.GatewayResult.Outcome != imagegateway.GatewayPartial || execution.GatewayResult.DeliverableCount != 1 || execution.GatewayResult.ProviderResultCount != 2 {
			diagnosticErr := fixture.service.finalizeSuccess(context.Background(), fixture.requestID, execution.GatewayResult.ProviderResultCount)
			t.Fatalf("部分成功执行错误: %+v err=%v diagnostic=%v snapshot=%s", execution, err, diagnosticErr, imageG5PricingDiagnostic(db, fixture.requestID))
		}
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.00000000")
		var sale, cost decimal.Decimal
		db.Raw("SELECT amount FROM ai_usage_items WHERE request_id=? AND record_kind='sale_line'", fixture.requestID).Scan(&sale)
		db.Raw("SELECT amount FROM ai_usage_items WHERE request_id=? AND record_kind='cost_line'", fixture.requestID).Scan(&cost)
		if sale.StringFixed(8) != "0.50000000" || cost.StringFixed(8) != "0.60000000" {
			t.Fatalf("部分成功销售/成本错误: sale=%s cost=%s", sale, cost)
		}
		report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID)
		if err != nil || !report.ZeroDifference() {
			t.Fatalf("部分成功必须零差异: %+v %v", report, err)
		}
	})

	t.Run("输出安全拒绝释放用户预占并记录平台成本", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97103, "g5-rejected", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationRejectImage, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, imagegateway.ErrImageResultInvalid) || execution.BillingStatus != model.AIBillingReleased || execution.GatewayResult.RejectedCount != 1 {
			diagnosticErr := fixture.service.finalizeRelease(context.Background(), fixture.requestID, imagegateway.GatewayResult{ProviderResultCount: 1, ErrorClass: "output_rejected"})
			t.Fatalf("输出拒绝处理错误: %+v err=%v diagnostic=%v", execution, err, diagnosticErr)
		}
		assertWalletAmounts(t, db, fixture.owner.UserID, "10.00000000", "0.00000000")
		var sale, cost decimal.Decimal
		db.Raw("SELECT amount FROM ai_usage_items WHERE request_id=? AND record_kind='sale_line'", fixture.requestID).Scan(&sale)
		db.Raw("SELECT amount FROM ai_usage_items WHERE request_id=? AND record_kind='cost_line'", fixture.requestID).Scan(&cost)
		if !sale.IsZero() || cost.StringFixed(8) != "0.30000000" {
			t.Fatalf("输出拒绝必须用户0元、平台记成本: sale=%s cost=%s", sale, cost)
		}
		assertImageG5Count(t, db, "ai_gateway_assets", "request_id=? AND lifecycle_state='quarantined'", fixture.requestID, 1)
		report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID)
		if err != nil || !report.ZeroDifference() {
			t.Fatalf("输出拒绝必须零差异: %+v %v", report, err)
		}
	})

	t.Run("明确失败释放且超时断连保持待核对", func(t *testing.T) {
		failed := seedImageBillingFixture(t, db, 97107, "g5-failed", 1, imagegateway.FakeImageFailed, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, failed)
		execution, err := failed.service.Execute(context.Background(), failed.requestID, failed.command)
		if !errors.Is(err, imagegateway.ErrProviderFailed) || execution.BillingStatus != model.AIBillingReleased || failed.adapter.Calls() != 1 {
			t.Fatalf("明确失败必须释放: execution=%+v calls=%d err=%v", execution, failed.adapter.Calls(), err)
		}
		assertWalletAmounts(t, db, failed.owner.UserID, "10.00000000", "0.00000000")
		if report, err := failed.service.ReconcileRequest(context.Background(), failed.requestID); err != nil || !report.ZeroDifference() {
			t.Fatalf("明确失败必须零差异: report=%+v err=%v", report, err)
		}

		pendingModes := []struct {
			userID uint64
			suffix string
			mode   imagegateway.FakeImageMode
		}{
			{userID: 97108, suffix: "g5-timeout", mode: imagegateway.FakeImageTimeout},
			{userID: 97109, suffix: "g5-disconnected", mode: imagegateway.FakeImageDisconnected},
		}
		for _, item := range pendingModes {
			fixture := seedImageBillingFixture(t, db, item.userID, item.suffix, 1, item.mode, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
			mustReserveImageG5(t, fixture)
			execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
			if !errors.Is(err, ErrImagePendingReconcile) || execution.BillingStatus != model.AIBillingSettlementPending || fixture.adapter.Calls() != 1 {
				t.Fatalf("超时/断连必须待核对: mode=%s execution=%+v calls=%d err=%v", item.mode, execution, fixture.adapter.Calls(), err)
			}
			assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.50000000")
		}
	})

	t.Run("Provider返回后上下文取消仍原子进入待补偿", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97112, "g5-cancelled-finalize", 1, imagegateway.FakeImageTimeout, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		requestCtx, cancelRequest := context.WithCancel(context.Background())
		fixture.service.gateway = &g5CancelAfterGateway{imageGatewayRunner: fixture.service.gateway, cancel: cancelRequest}
		execution, err := fixture.service.Execute(requestCtx, fixture.requestID, fixture.command)
		cancelRequest()
		if !errors.Is(err, ErrImagePendingReconcile) || execution == nil || execution.BillingStatus != model.AIBillingSettlementPending || fixture.adapter.Calls() != 1 {
			t.Fatalf("取消上下文不得阻断本地待补偿事务: execution=%+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.50000000")
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_key=? AND status='pending'", "image:"+fixture.requestID, 1)
		assertImageG5Count(t, db, "ai_outbox_events", "aggregate_id=? AND event_type='image_settlement_pending'", fixture.requestID, 1)
	})

	t.Run("明确失败释放首败后由补偿唯一完成", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97110, "g5-release-compensate", 1, imagegateway.FakeImageFailed, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		// 只在钱包模块边界注入第一次释放失败，后续补偿仍走真实MySQL钱包事务。
		failingHolds := &g5FailFirstReleaseHoldService{imageWalletHoldService: fixture.service.holds}
		fixture.service.holds = failingHolds

		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, ErrImagePendingReconcile) || execution == nil || execution.BillingStatus != model.AIBillingSettlementPending {
			t.Fatalf("释放首败必须原子进入待补偿: execution=%+v err=%v", execution, err)
		}
		if fixture.adapter.Calls() != 1 || failingHolds.releaseAttempts.Load() != 1 {
			t.Fatalf("首次执行必须只调用一次Provider和一次释放: provider=%d release=%d", fixture.adapter.Calls(), failingHolds.releaseAttempts.Load())
		}
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.50000000")
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_key=? AND status='pending'", "image:"+fixture.requestID, 1)
		assertImageG5Count(t, db, "ai_outbox_events", "aggregate_id=? AND event_type='image_settlement_pending'", fixture.requestID, 1)
		var pendingRequest model.AIRequest
		if err := db.Where("request_id=?", fixture.requestID).First(&pendingRequest).Error; err != nil ||
			pendingRequest.ExecutionStatus != model.AIExecutionFailed || pendingRequest.BillingStatus != model.AIBillingSettlementPending || pendingRequest.DeliveryStatus != model.AIDeliveryRejected {
			t.Fatalf("待补偿请求必须保留Provider明确失败事实: request=%+v err=%v", pendingRequest, err)
		}
		var pendingTask model.AIImageTask
		var recoveryFacts imageRecoveryFacts
		if err := db.Where("request_id=?", fixture.requestID).First(&pendingTask).Error; err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(pendingTask.ResultJSON, &recoveryFacts); err != nil || recoveryFacts.RecoveryAction != imageRecoveryRelease ||
			recoveryFacts.ProviderResultCount != 0 || recoveryFacts.FinalErrorClass != "provider_failed" {
			t.Fatalf("补偿必须持久化release确定事实: facts=%+v err=%v", recoveryFacts, err)
		}

		worker := NewImageCompensationWorker(repository.NewImageCompensationRepository(db), fixture.service)
		worker.now = func() time.Time { return now.Add(time.Minute) }
		completed, err := worker.RunBatch(context.Background(), 10)
		if err != nil || completed != 1 {
			t.Fatalf("释放补偿必须完成一次: completed=%d err=%v", completed, err)
		}
		completed, err = worker.RunBatch(context.Background(), 10)
		if err != nil || completed != 0 {
			t.Fatalf("终态重放不得产生第二次补偿: completed=%d err=%v", completed, err)
		}
		if fixture.adapter.Calls() != 1 || failingHolds.releaseAttempts.Load() != 2 {
			t.Fatalf("补偿只能重试本地释放且不得重调Provider: provider=%d release=%d", fixture.adapter.Calls(), failingHolds.releaseAttempts.Load())
		}
		assertWalletAmounts(t, db, fixture.owner.UserID, "10.00000000", "0.00000000")
		assertImageG5Count(t, db, "wallet_transactions", "user_id=? AND type='unfreeze'", fixture.owner.UserID, 1)
		assertImageG5Count(t, db, "ai_outbox_events", "aggregate_id=? AND event_type='image_billing_released'", fixture.requestID, 1)
		var compensation model.AICompensationTask
		if err := db.Where("task_key=?", "image:"+fixture.requestID).First(&compensation).Error; err != nil || compensation.Status != "completed" {
			t.Fatalf("补偿任务必须唯一完成: task=%+v err=%v", compensation, err)
		}
		var releasedRequest model.AIRequest
		if err := db.Where("request_id=?", fixture.requestID).First(&releasedRequest).Error; err != nil ||
			releasedRequest.ExecutionStatus != model.AIExecutionFailed || releasedRequest.BillingStatus != model.AIBillingReleased || releasedRequest.DeliveryStatus != model.AIDeliveryRejected {
			t.Fatalf("释放补偿后必须收敛到唯一终态: request=%+v err=%v", releasedRequest, err)
		}
		report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID)
		if err != nil || !report.ZeroDifference() {
			t.Fatalf("释放补偿完成后必须零差异: report=%+v err=%v", report, err)
		}
	})

	t.Run("人工Reconcile以CAS关闭补偿任务", func(t *testing.T) {
		t.Run("pending settle成功后补偿完成且零差异", func(t *testing.T) {
			fixture := seedImageBillingFixture(t, db, 97130, "g5-manual-settle", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
			mustReserveImageG5(t, fixture)
			fixture.service.beforeFinalize = func() error { return errors.New("注入首次结算失败") }
			execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
			if !errors.Is(err, ErrImagePendingReconcile) || execution == nil || execution.BillingStatus != model.AIBillingSettlementPending {
				t.Fatalf("结算首败必须进入待补偿: execution=%+v err=%v", execution, err)
			}
			fixture.service.beforeFinalize = nil
			if err := fixture.service.ReconcilePendingAndCompleteCompensation(context.Background(), fixture.requestID); err != nil {
				t.Fatalf("人工结算补偿失败: %v", err)
			}
			assertImageG5Count(t, db, "ai_compensation_tasks", "task_key=? AND status='completed'", "image:"+fixture.requestID, 1)
			if report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID); err != nil || !report.ZeroDifference() {
				t.Fatalf("人工结算关闭补偿后必须零差异: report=%+v err=%v", report, err)
			}
			if fixture.adapter.Calls() != 1 {
				t.Fatalf("人工结算不得重调Provider: %d", fixture.adapter.Calls())
			}
		})

		t.Run("pending release成功后补偿完成且零差异", func(t *testing.T) {
			fixture := seedImageBillingFixture(t, db, 97131, "g5-manual-release", 1, imagegateway.FakeImageFailed, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
			mustReserveImageG5(t, fixture)
			failingHolds := &g5FailFirstReleaseHoldService{imageWalletHoldService: fixture.service.holds}
			fixture.service.holds = failingHolds
			execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
			if !errors.Is(err, ErrImagePendingReconcile) || execution == nil || execution.BillingStatus != model.AIBillingSettlementPending {
				t.Fatalf("释放首败必须进入待补偿: execution=%+v err=%v", execution, err)
			}
			if err := fixture.service.ReconcilePendingAndCompleteCompensation(context.Background(), fixture.requestID); err != nil {
				t.Fatalf("人工释放补偿失败: %v", err)
			}
			assertImageG5Count(t, db, "ai_compensation_tasks", "task_key=? AND status='completed'", "image:"+fixture.requestID, 1)
			if report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID); err != nil || !report.ZeroDifference() {
				t.Fatalf("人工释放关闭补偿后必须零差异: report=%+v err=%v", report, err)
			}
			if fixture.adapter.Calls() != 1 || failingHolds.releaseAttempts.Load() != 2 {
				t.Fatalf("人工释放只能重试钱包且不得重调Provider: provider=%d release=%d", fixture.adapter.Calls(), failingHolds.releaseAttempts.Load())
			}
		})

		for index, status := range []string{"dead", "manual_review"} {
			t.Run(status+"在财务已终态后可关闭", func(t *testing.T) {
				userID := uint64(97132 + index)
				fixture := seedImageBillingFixture(t, db, userID, "g5-terminal-"+status, 1, imagegateway.FakeImageFailed, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
				mustReserveImageG5(t, fixture)
				if execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command); !errors.Is(err, imagegateway.ErrProviderFailed) || execution.BillingStatus != model.AIBillingReleased {
					t.Fatalf("前置财务终态错误: execution=%+v err=%v", execution, err)
				}
				errorClass := "人工核对前遗留任务"
				if err := db.Create(&model.AICompensationTask{
					TaskKey: "image:" + fixture.requestID, TaskType: "image_reconcile", AggregateID: fixture.requestID,
					Status: status, RetryCount: 8, NextRetryAt: now, LastErrorClass: &errorClass,
				}).Error; err != nil {
					t.Fatal(err)
				}
				if err := fixture.service.ReconcilePendingAndCompleteCompensation(context.Background(), fixture.requestID); err != nil {
					t.Fatalf("财务终态后的%s补偿必须关闭: %v", status, err)
				}
				assertImageG5Count(t, db, "ai_compensation_tasks", "task_key=? AND status='completed'", "image:"+fixture.requestID, 1)
				if report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID); err != nil || !report.ZeroDifference() {
					t.Fatalf("关闭%s补偿后必须零差异: report=%+v err=%v", status, report, err)
				}
			})
		}

		t.Run("活跃worker租约不得被人工核对抢占", func(t *testing.T) {
			fixture := seedImageBillingFixture(t, db, 97134, "g5-manual-busy", 1, imagegateway.FakeImageUnknown, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
			mustReserveImageG5(t, fixture)
			if execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command); !errors.Is(err, ErrImagePendingReconcile) || execution.BillingStatus != model.AIBillingSettlementPending {
				t.Fatalf("结果未知前置状态错误: execution=%+v err=%v", execution, err)
			}
			lockedAt := now.UTC().Truncate(time.Second)
			if err := db.Model(&model.AICompensationTask{}).Where("task_key=?", "image:"+fixture.requestID).
				Updates(map[string]interface{}{"status": "running", "locked_at": lockedAt}).Error; err != nil {
				t.Fatal(err)
			}
			if err := fixture.service.ReconcilePendingAndCompleteCompensation(context.Background(), fixture.requestID); !errors.Is(err, repository.ErrImageCompensationBusy) {
				t.Fatalf("活跃worker期间人工核对必须busy: %v", err)
			}
			var task model.AICompensationTask
			if err := db.Where("task_key=?", "image:"+fixture.requestID).First(&task).Error; err != nil || task.Status != "running" || task.LockedAt == nil || !task.LockedAt.Equal(lockedAt) {
				t.Fatalf("人工核对不得改写活跃worker租约: task=%+v err=%v", task, err)
			}
			if fixture.adapter.Calls() != 1 {
				t.Fatalf("busy人工核对不得重调Provider: %d", fixture.adapter.Calls())
			}
		})
	})

	t.Run("finalize与pending双失败由陈旧执行扫描恢复", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97140, "g5-stale-processing-recovery", 1, imagegateway.FakeImageFailed, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		failingHolds := &g5FailFirstReleaseHoldService{imageWalletHoldService: fixture.service.holds}
		fixture.service.holds = failingHolds
		fixture.service.beforeMarkPending = func() error { return errors.New("注入pending数据库不可用") }
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if execution != nil || err == nil || fixture.adapter.Calls() != 1 || failingHolds.releaseAttempts.Load() != 1 {
			t.Fatalf("双失败必须留下可扫描执行事实且Provider只调用一次: execution=%+v provider=%d release=%d err=%v", execution, fixture.adapter.Calls(), failingHolds.releaseAttempts.Load(), err)
		}
		fixture.service.beforeMarkPending = nil
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_key=?", "image:"+fixture.requestID, 0)
		assertImageG5Count(t, db, "ai_outbox_events", "aggregate_id=? AND event_type='image_settlement_pending'", fixture.requestID, 0)
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.50000000")

		activeAt := now.Add(-4 * time.Minute)
		if err := db.Model(&model.AIImageTask{}).Where("request_id=?", fixture.requestID).UpdateColumn("updated_at", activeAt).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIRequest{}).Where("request_id=?", fixture.requestID).UpdateColumn("updated_at", activeAt).Error; err != nil {
			t.Fatal(err)
		}
		if recovered, err := fixture.service.RecoverStaleActiveExecutions(context.Background(), now.Add(-5*time.Minute), 100); err != nil || recovered != 0 {
			t.Fatalf("未过五分钟安全窗不得恢复: recovered=%d err=%v", recovered, err)
		}
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_key=?", "image:"+fixture.requestID, 0)

		staleAt := now.Add(-6 * time.Minute)
		if err := db.Model(&model.AIImageTask{}).Where("request_id=?", fixture.requestID).UpdateColumn("updated_at", staleAt).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIRequest{}).Where("request_id=?", fixture.requestID).UpdateColumn("updated_at", staleAt).Error; err != nil {
			t.Fatal(err)
		}
		if recovered, err := fixture.service.RecoverStaleActiveExecutions(context.Background(), now.Add(-5*time.Minute), 100); err != nil || recovered != 1 {
			t.Fatalf("超过安全窗必须恢复一次: recovered=%d err=%v", recovered, err)
		}
		if recovered, err := fixture.service.RecoverStaleActiveExecutions(context.Background(), now.Add(-5*time.Minute), 100); err != nil || recovered != 0 {
			t.Fatalf("恢复重放必须幂等: recovered=%d err=%v", recovered, err)
		}
		var request model.AIRequest
		if err := db.Where("request_id=?", fixture.requestID).First(&request).Error; err != nil || request.ExecutionStatus != model.AIExecutionUnknown ||
			request.BillingStatus != model.AIBillingSettlementPending || request.DeliveryStatus != model.AIDeliveryPending || request.ErrorClass == nil || *request.ErrorClass != "result_unknown" {
			t.Fatalf("陈旧执行必须原子进入结果未知: request=%+v err=%v", request, err)
		}
		var task model.AIImageTask
		if err := db.Where("request_id=?", fixture.requestID).First(&task).Error; err != nil || task.Status != model.AIImageTaskPendingReconcile || task.ErrorCode == nil || *task.ErrorCode != "result_unknown" {
			t.Fatalf("陈旧任务必须进入pending_reconcile: task=%+v err=%v", task, err)
		}
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_key=? AND status='pending'", "image:"+fixture.requestID, 1)
		assertImageG5Count(t, db, "ai_outbox_events", "aggregate_id=? AND event_type='image_settlement_pending'", fixture.requestID, 1)
		assertImageG5Count(t, db, "ai_usage_items", "request_id=?", fixture.requestID, 0)
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.50000000")
		if state, err := fixture.service.ImageRequestQueueState(context.Background(), fixture.requestID); err != nil || state != imageQueueStateInactive {
			t.Fatalf("恢复后重投消息必须幂等Ack: state=%d err=%v", state, err)
		}
		if err := fixture.service.ReconcilePending(context.Background(), fixture.requestID); !errors.Is(err, ErrImagePendingReconcile) {
			t.Fatalf("结果未知不得猜测结算或释放: %v", err)
		}
		if fixture.adapter.Calls() != 1 || failingHolds.releaseAttempts.Load() != 1 {
			t.Fatalf("恢复不得重调Provider或再次释放: provider=%d release=%d", fixture.adapter.Calls(), failingHolds.releaseAttempts.Load())
		}
	})

	t.Run("结果未知保留hold且八次补偿不重调Provider", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97104, "g5-unknown", 1, imagegateway.FakeImageUnknown, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, ErrImagePendingReconcile) || execution.BillingStatus != model.AIBillingSettlementPending || fixture.adapter.Calls() != 1 {
			t.Fatalf("结果未知状态错误: %+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.50000000")
		current := now
		worker := NewImageCompensationWorker(repository.NewImageCompensationRepository(db), fixture.service)
		worker.now = func() time.Time { return current }
		for attempt := 0; attempt < 8; attempt++ {
			if completed, err := worker.RunBatch(context.Background(), 10); err != nil || completed != 0 {
				t.Fatalf("unknown补偿不得自动完成: attempt=%d completed=%d err=%v", attempt, completed, err)
			}
			current = current.Add(2 * time.Minute)
		}
		var task model.AICompensationTask
		if err := db.Where("task_key=?", "image:"+fixture.requestID).First(&task).Error; err != nil || task.Status != "dead" || task.RetryCount != 8 {
			t.Fatalf("第8次失败必须进入dead: task=%+v err=%v", task, err)
		}
		if fixture.adapter.Calls() != 1 {
			t.Fatalf("补偿不得重调Provider: %d", fixture.adapter.Calls())
		}
		if _, err := fixture.service.assets.FindDeliverable(context.Background(), imageAssetPublicID(fixture.requestID, 0, model.AIImageAssetPrimaryOutput), fixture.owner); !errors.Is(err, repository.ErrImageAssetAccess) {
			t.Fatalf("结果未知不得交付: %v", err)
		}
	})

	t.Run("结算失败补偿成功后只交付一次", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97105, "g5-compensate", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		injected := true
		fixture.service.beforeFinalize = func() error {
			if injected {
				injected = false
				return errors.New("注入结算失败")
			}
			return nil
		}
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, ErrImagePendingReconcile) || execution.BillingStatus != model.AIBillingSettlementPending || fixture.adapter.Calls() != 1 {
			t.Fatalf("注入结算失败状态错误: %+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		fixture.service.beforeFinalize = nil
		worker := NewImageCompensationWorker(repository.NewImageCompensationRepository(db), fixture.service)
		worker.now = func() time.Time { return now.Add(time.Minute) }
		completed, err := worker.RunBatch(context.Background(), 10)
		if err != nil || completed != 1 {
			t.Fatalf("补偿应完成一次: completed=%d err=%v", completed, err)
		}
		completed, err = worker.RunBatch(context.Background(), 10)
		if err != nil || completed != 0 || fixture.adapter.Calls() != 1 {
			t.Fatalf("完成后重放不得再次交付或调用Provider: completed=%d calls=%d err=%v", completed, fixture.adapter.Calls(), err)
		}
		assertImageG5Count(t, db, "ai_gateway_assets", "request_id=? AND asset_role='primary_output' AND lifecycle_state='available'", fixture.requestID, 1)
		report, err := fixture.service.ReconcileRequest(context.Background(), fixture.requestID)
		if err != nil || !report.ZeroDifference() {
			t.Fatalf("补偿成功后必须零差异: %+v %v", report, err)
		}
	})

	t.Run("存储失败保持待补偿且不重调Provider", func(t *testing.T) {
		baseStore := imagegateway.NewFakeObjectStore()
		fixture := seedImageBillingFixture(t, db, 97106, "g5-store-failed", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now,
			&g5FailStore{ObjectStore: baseStore, failBucket: "ai-result"})
		mustReserveImageG5(t, fixture)
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, ErrImagePendingReconcile) || execution.GatewayResult.ErrorClass != "asset_storage_failed" || fixture.adapter.Calls() != 1 {
			t.Fatalf("存储失败状态错误: %+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.50000000")
		worker := NewImageCompensationWorker(repository.NewImageCompensationRepository(db), fixture.service)
		worker.now = func() time.Time { return now.Add(time.Minute) }
		if completed, err := worker.RunBatch(context.Background(), 10); err != nil || completed != 0 || fixture.adapter.Calls() != 1 {
			t.Fatalf("无持久资产的存储失败不得伪完成或重调Provider: completed=%d calls=%d err=%v", completed, fixture.adapter.Calls(), err)
		}
	})

	t.Run("资产元数据事务回滚后逐对象持久清理", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97111, "g5-metadata-cleanup", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		// 重复主图会触发资产唯一约束，使元数据事务在对象已经写入后整体回滚。
		fixture.service.gateway = &g5DuplicatePrimaryGateway{imageGatewayRunner: fixture.service.gateway}
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, ErrImagePendingReconcile) || execution == nil || execution.BillingStatus != model.AIBillingSettlementPending ||
			execution.GatewayResult.ErrorClass != "asset_metadata_failed" || fixture.adapter.Calls() != 1 {
			t.Fatalf("元数据事务失败必须待补偿且Provider只调用一次: execution=%+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		assertImageG5Count(t, db, "ai_gateway_assets", "request_id=?", fixture.requestID, 0)
		requestHash := sha256.Sum256([]byte(fixture.requestID))
		cleanupPrefix := "result:" + hex.EncodeToString(requestHash[:16]) + ":%"
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_type='image_object_cleanup' AND aggregate_id LIKE ?", cleanupPrefix, 2)
		assertImageG5Count(t, db, "ai_outbox_events", "aggregate_id=? AND event_type='image_settlement_pending'", fixture.requestID, 1)
		assertWalletAmounts(t, db, fixture.owner.UserID, "9.50000000", "0.50000000")

		cleanupWorker, err := NewImageObjectCleanupWorker(repository.NewImageObjectCleanupRepository(db), fixture.store)
		if err != nil {
			t.Fatal(err)
		}
		cleanupAt := time.Now().UTC().Add(2 * time.Minute)
		cleanupWorker.now = func() time.Time { return cleanupAt }
		cleanupResult, err := cleanupWorker.RunBatch(context.Background(), 10)
		if err != nil || cleanupResult.Scanned < 2 || cleanupResult.Completed != 2 || cleanupResult.Retried != 0 {
			t.Fatalf("元数据回滚对象必须由持久任务全部清理: result=%+v err=%v", cleanupResult, err)
		}
		seen := make(map[imagegateway.ObjectRef]struct{})
		for _, asset := range execution.GatewayResult.Assets {
			ref := asset.StoredObject.Ref
			if _, exists := seen[ref]; exists {
				continue
			}
			seen[ref] = struct{}{}
			if _, err := fixture.store.Head(context.Background(), ref); !errors.Is(err, imagegateway.ErrObjectNotFound) {
				t.Fatalf("清理完成后对象必须不存在: bucket=%s err=%v", ref.Bucket, err)
			}
		}
		if fixture.adapter.Calls() != 1 {
			t.Fatalf("对象清理不得重调Provider: %d", fixture.adapter.Calls())
		}
	})

	t.Run("资产提交响应未知但完整事实可见时继续结算", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97113, "g5-asset-commit-unknown", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		fixture.service.afterAssetCommit = func() error { return errors.New("注入资产事务提交响应丢失") }
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if err != nil || execution == nil || execution.BillingStatus != model.AIBillingSettled || execution.DeliveryStatus != model.AIDeliveryAvailable || fixture.adapter.Calls() != 1 {
			t.Fatalf("完整提交事实必须继续结算且不得重调Provider: execution=%+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		assertImageG5Count(t, db, "ai_gateway_assets", "request_id=?", fixture.requestID, 2)
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_type='image_object_cleanup' AND aggregate_id LIKE ?", "%"+hex.EncodeToString(sha256Sum16(fixture.requestID))+"%", 0)
	})

	t.Run("资产部分落库时保守pending且不登记删除", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97114, "g5-asset-partial", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		fixture.service.gateway = &g5PersistOneAssetGateway{
			imageGatewayRunner: fixture.service.gateway, db: db, requestID: fixture.requestID,
			owner: fixture.owner, now: now,
		}
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, ErrImagePendingReconcile) || execution == nil || execution.GatewayResult.ErrorClass != "asset_metadata_partial" || fixture.adapter.Calls() != 1 {
			t.Fatalf("部分资产事实必须待人工核对: execution=%+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		assertImageG5Count(t, db, "ai_gateway_assets", "request_id=?", fixture.requestID, 1)
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_type='image_object_cleanup' AND aggregate_id LIKE ?", "%"+hex.EncodeToString(sha256Sum16(fixture.requestID))+"%", 0)
	})

	t.Run("资产复查未知时保守pending且不登记删除", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97115, "g5-asset-inspect-unknown", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		fixture.service.afterAssetCommit = func() error { return errors.New("注入资产事务提交响应丢失") }
		fixture.service.inspectAssets = func(context.Context, string, repository.ImageOwner, imagegateway.GatewayResult) (imageAssetPersistenceState, error) {
			return imageAssetsPersistedPartial, errors.New("注入独立资产复查失败")
		}
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, ErrImagePendingReconcile) || execution == nil || execution.GatewayResult.ErrorClass != "asset_metadata_unknown" || fixture.adapter.Calls() != 1 {
			t.Fatalf("资产复查未知必须待人工核对: execution=%+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_type='image_object_cleanup' AND aggregate_id LIKE ?", "%"+hex.EncodeToString(sha256Sum16(fixture.requestID))+"%", 0)
	})

	t.Run("清理tombstone阻止后到资产引用", func(t *testing.T) {
		fixture := seedImageBillingFixture(t, db, 97116, "g5-cleanup-tombstone", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
		mustReserveImageG5(t, fixture)
		namespace := hex.EncodeToString(sha256Sum16(fixture.requestID))
		recorder := repository.NewImageObjectCleanupRepository(db)
		for _, ref := range []imagegateway.ObjectRef{
			{Bucket: imagegateway.ResultObjectBucket, Key: namespace + "/0/primary.png"},
			{Bucket: imagegateway.ResultObjectBucket, Key: namespace + "/0/thumbnail.png"},
		} {
			if err := recorder.RecordObjectCleanup(context.Background(), imagegateway.ObjectCleanupTask{
				RequestID: fixture.requestID, Ref: ref, Reason: imagegateway.ObjectCleanupAfterMetadataPersistFailure,
			}); err != nil {
				t.Fatal(err)
			}
		}
		execution, err := fixture.service.Execute(context.Background(), fixture.requestID, fixture.command)
		if !errors.Is(err, ErrImagePendingReconcile) || execution == nil || execution.GatewayResult.ErrorClass != "asset_metadata_failed" || fixture.adapter.Calls() != 1 {
			t.Fatalf("tombstone之后的资产持久化必须失败关闭: execution=%+v calls=%d err=%v", execution, fixture.adapter.Calls(), err)
		}
		assertImageG5Count(t, db, "ai_gateway_assets", "request_id=?", fixture.requestID, 0)
		assertImageG5Count(t, db, "ai_compensation_tasks", "task_type='image_object_cleanup' AND aggregate_id LIKE ?", "%"+namespace+"%", 2)
	})

	t.Run("100个不同请求不超额预占且钱包不为负", func(t *testing.T) {
		userID := uint64(97200)
		setupImageG5Owner(t, db, userID, decimal.RequireFromString("25"))
		fixtures := make([]*imageBillingFixture, 100)
		for index := range fixtures {
			fixtures[index] = seedImageBillingRequestForExistingOwner(t, db, userID, fmt.Sprintf("g5-wallet-%03d", index), 1, now)
		}
		var success atomic.Int64
		var insufficient atomic.Int64
		var wg sync.WaitGroup
		for _, fixture := range fixtures {
			wg.Add(1)
			go func(item *imageBillingFixture) {
				defer wg.Done()
				_, reserveErr := item.service.Reserve(context.Background(), item.reserveCommand())
				switch {
				case reserveErr == nil:
					success.Add(1)
				case errors.Is(reserveErr, billingservice.ErrInsufficientBalance):
					insufficient.Add(1)
				default:
					t.Errorf("不同请求并发预占异常: %v", reserveErr)
				}
			}(fixture)
		}
		wg.Wait()
		if success.Load() != 50 || insufficient.Load() != 50 {
			t.Fatalf("100请求×0.5、余额25应恰好50成功: success=%d insufficient=%d", success.Load(), insufficient.Load())
		}
		assertWalletAmounts(t, db, userID, "0.00000000", "25.00000000")
		var negatives int64
		db.Model(&billingmodel.Wallet{}).Where("user_id=? AND (balance_amount<0 OR frozen_amount<0)", userID).Count(&negatives)
		if negatives != 0 {
			t.Fatal("并发预占后钱包不得出现负数")
		}
	})
}

func setupImageG5Base(t *testing.T, db *gorm.DB, now time.Time) {
	t.Helper()
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?, 'fixture','verified','active'),(?, 'fixture','verified','active')", imageG5OperatorID, imageG5ReviewerID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,?,?,?,?)", imageG5PriceVersionID, imageG5ModelCode, "图片G5隔离模型", "image", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	variant := ImagePriceVariant{Resolution: "2K", AspectRatio: "1:1", Quality: "standard", OutputFormat: "provider_default", Delivery: "url"}
	variantJSON, variantHash, err := canonicalImageVariant(variant)
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := json.Marshal(imagePricingLimits{MaxCount: 2, Variants: []ImagePriceVariant{variant}})
	if err := db.Exec(`INSERT INTO ai_price_versions(
id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,
max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,
failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(?,?, 'image.generate','image_variant',1,'CNY',1,'active',0.2,NULL,NULL,?,0.01,'test_fixture','g5-mysql','test_fixture','confirmed_usage','ceil_8',?,?,?,?)`,
		imageG5PriceVersionID, imageG5ModelCode, limits, now.Add(-time.Hour), now.Add(24*time.Hour), now.Add(-time.Hour), imageG5OperatorID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_price_skus(price_version_id,meter_type,variant_json,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES(?,?,?,?,0.3,0.5,1,'CNY')",
		imageG5PriceVersionID, "image_count", variantJSON, variantHash).Error; err != nil {
		t.Fatal(err)
	}
}

func setupImageG5Owner(t *testing.T, db *gorm.DB, userID uint64, balance decimal.Decimal) {
	t.Helper()
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?, 'fixture','verified','active')", userID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,monthly_budget,timezone) VALUES(?,?,?,'active','disabled',NULL,'Asia/Shanghai')", userID, userID, fmt.Sprintf("G5项目-%d", userID)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES(?,?,?,'g5',?,'G5密钥','postpaid','','allowlist','active')",
		userID, userID, userID, fmt.Sprintf("g5-hash-%d", userID)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&billingmodel.Wallet{ID: userID, UserID: userID, BalanceAmount: balance, FrozenAmount: decimal.Zero, Currency: "CNY"}).Error; err != nil {
		t.Fatal(err)
	}
}

func seedImageBillingFixture(t *testing.T, db *gorm.DB, userID uint64, suffix string, count uint64, providerMode imagegateway.FakeImageMode,
	moderationMode imagegateway.FakeModerationMode, balance decimal.Decimal, now time.Time, overrideStore imagegateway.ObjectStore) *imageBillingFixture {
	t.Helper()
	setupImageG5Owner(t, db, userID, balance)
	fixture := seedImageBillingRequestForExistingOwner(t, db, userID, suffix, count, now)
	adapter := imagegateway.NewFakeImageAdapter(providerMode)
	store := overrideStore
	if store == nil {
		store = imagegateway.NewFakeObjectStore()
	}
	fixture.adapter = adapter
	fixture.store = store
	fixture.service = newImageG5Service(t, db, adapter, imagegateway.NewFakeModerationAdapter(moderationMode), store, now)
	return fixture
}

func seedImageBillingRequestForExistingOwner(t *testing.T, db *gorm.DB, userID uint64, suffix string, count uint64, now time.Time) *imageBillingFixture {
	t.Helper()
	requestID := "img-" + suffix
	quoteID := "quote-" + suffix
	promptHashRaw := sha256.Sum256([]byte("prompt-" + suffix))
	variant := ImagePriceVariant{Resolution: "2K", AspectRatio: "1:1", Quality: "standard", OutputFormat: "provider_default", Delivery: "url"}
	fingerprintInput := ImageQuoteFingerprintInput{
		UserID: userID, ProjectID: userID, APIKeyID: userID, LogicalModelCode: imageG5ModelCode,
		PromptHash: hex.EncodeToString(promptHashRaw[:]), Count: count, Variant: variant,
	}
	fingerprint, err := BuildImageQuoteFingerprint([]byte("0123456789abcdef0123456789abcdef"), fingerprintInput)
	if err != nil {
		t.Fatal(err)
	}
	pricing := NewImagePricingService(repository.NewG3PricingRepository(db))
	pricing.now = func() time.Time { return now }
	priceQuote, err := pricing.QuoteImage(context.Background(), ImageQuoteCommand{LogicalModelCode: imageG5ModelCode, Count: count, Variant: variant})
	if err != nil {
		t.Fatal(err)
	}
	request := model.AIRequest{
		RequestID: requestID, RequestFingerprint: &fingerprint, UserID: userID, ProjectID: uint64TestPtr(userID), APIKeyID: uint64TestPtr(userID),
		LogicalModelCode: imageG5ModelCode, Modality: "image", Capability: model.AIImageCapability,
		ModerationStatus: model.AIModerationPending, ExecutionStatus: model.AIExecutionPending,
		BillingStatus: model.AIBillingUnquoted, DeliveryStatus: model.AIDeliveryPending,
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatal(err)
	}
	quote := model.AIGatewayQuote{
		PublicID: quoteID, UserID: userID, ProjectID: userID, APIKeyID: uint64TestPtr(userID), LogicalModelCode: imageG5ModelCode,
		Capability: model.AIImageCapability, RequestFingerprint: fingerprint, RequestVariantHash: priceQuote.VariantHash,
		PriceVersionID: imageG5PriceVersionID, PriceSnapshotJSON: priceQuote.SnapshotJSON,
		QuotedAmount: priceQuote.QuotedAmount, Currency: "CNY", ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now,
	}
	if err := db.Create(&quote).Error; err != nil {
		t.Fatal(err)
	}
	task := model.AIImageTask{
		PublicID: "task-" + suffix, RequestID: requestID, QuoteID: quote.ID, UserID: userID, ProjectID: userID, APIKeyID: uint64TestPtr(userID),
		LogicalModelCode: imageG5ModelCode, Capability: model.AIImageCapability, Status: model.AIImageTaskCreated,
		InputJSON: json.RawMessage(`{"resolution":"2K"}`),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	adapter := imagegateway.NewFakeImageAdapter(imagegateway.FakeImageSuccess)
	store := imagegateway.NewFakeObjectStore()
	service := newImageG5Service(t, db, adapter, imagegateway.NewFakeModerationAdapter(imagegateway.FakeModerationAllow), store, now)
	return &imageBillingFixture{
		service: service, adapter: adapter, store: store, owner: repository.ImageOwner{UserID: userID, ProjectID: userID, APIKeyID: uint64TestPtr(userID)},
		requestID: requestID, quoteID: quoteID, fingerprint: fingerprint,
		command: imagegateway.GenerateImageCommand{RequestID: requestID, ModelCode: imageG5ModelCode, Prompt: "隔离测试图片", Count: count, Resolution: "2K", AspectRatio: "1:1", Quality: "standard", OutputFormat: "provider_default"},
	}
}

func newImageG5Service(t *testing.T, db *gorm.DB, adapter imagegateway.ImageProviderAdapter, moderation imagegateway.ImageModerationAdapter, store imagegateway.ObjectStore, now time.Time) *ImageBillingService {
	t.Helper()
	processor, err := imagegateway.NewImageProcessor(imagegateway.ImageProcessingLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 2 << 20, MaxPixels: 10000,
		MaxWidth: 100, MaxHeight: 100, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := repository.NewImageObjectCleanupRepository(db)
	gateway, err := imagegateway.NewImageGateway(adapter, moderation, processor, store, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	walletRepo := billingrepo.NewWalletRepository(db)
	txRepo := billingrepo.NewTransactionRepository(db)
	holdRepo := billingrepo.NewWalletHoldRepository(db)
	holds := billingservice.NewWalletHoldService(db, walletRepo, txRepo, holdRepo)
	pricing := NewImagePricingService(repository.NewG3PricingRepository(db))
	pricing.now = func() time.Time { return now }
	service, err := NewImageBillingService(db, holds, pricing, gateway, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	return service
}

func (f *imageBillingFixture) reserveCommand() ImageReserveCommand {
	return ImageReserveCommand{RequestID: f.requestID, QuotePublicID: f.quoteID, Owner: f.owner, RequestFingerprint: f.fingerprint}
}

func mustReserveImageG5(t *testing.T, fixture *imageBillingFixture) {
	t.Helper()
	if _, err := fixture.service.Reserve(context.Background(), fixture.reserveCommand()); err != nil {
		t.Fatal(err)
	}
}

func assertWalletAmounts(t *testing.T, db *gorm.DB, userID uint64, balance, frozen string) {
	t.Helper()
	var wallet billingmodel.Wallet
	if err := db.Where("user_id=?", userID).First(&wallet).Error; err != nil {
		t.Fatal(err)
	}
	if wallet.BalanceAmount.StringFixed(8) != balance || wallet.FrozenAmount.StringFixed(8) != frozen {
		t.Fatalf("钱包金额错误: user=%d balance=%s frozen=%s", userID, wallet.BalanceAmount, wallet.FrozenAmount)
	}
}

func assertImageG5Count(t *testing.T, db *gorm.DB, table, where string, arg interface{}, expected int64) {
	t.Helper()
	var count int64
	if err := db.Table(table).Where(where, arg).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("数量不符: table=%s where=%s count=%d expected=%d", table, where, count, expected)
	}
}

func imageG5PricingDiagnostic(db *gorm.DB, requestID string) string {
	var request model.AIRequest
	if err := db.Where("request_id=?", requestID).First(&request).Error; err != nil {
		return fmt.Sprintf("request_error=%v", err)
	}
	var snapshot MetricPriceSnapshotV2
	decodeErr := json.Unmarshal(request.PriceSnapshotJSON, &snapshot)
	lineState := "none"
	if len(snapshot.SelectedLines) == 1 {
		line := snapshot.SelectedLines[0]
		canonical, canonicalHash, canonicalErr := canonicalizeStoredVariant(line.VariantJSON)
		lineState = fmt.Sprintf("meter=%s unit=%s unit_size=%s sale=%s cost=%s quoted=%s variant_bytes=%d canonical_err=%v hash_match=%t bytes_match=%t",
			line.MeterType, line.UsageUnit, line.UnitSize, line.SaleUnitPrice, line.CostUnitPrice, line.QuotedUsage,
			len(line.VariantJSON), canonicalErr, canonicalHash == line.VariantHash, string(canonical) == string(line.VariantJSON))
	}
	return fmt.Sprintf("bytes=%d decode=%v schema=%d capability=%s template=%s purpose=%s currency=%s exchange=%s rounding=%s failure=%s minimum=%s quoted=%s held=%s lines=%d line={%s}",
		len(request.PriceSnapshotJSON), decodeErr, snapshot.SchemaVersion, snapshot.Capability, snapshot.PricingTemplate,
		snapshot.PricePurpose, snapshot.Currency, snapshot.ExchangeRate, snapshot.RoundingMode, snapshot.FailureChargePolicy,
		snapshot.MinimumCharge, snapshot.QuotedAmount, snapshot.HeldAmount, len(snapshot.SelectedLines), lineState)
}

func uint64TestPtr(value uint64) *uint64 { return &value }

type g5FailStore struct {
	imagegateway.ObjectStore
	failBucket string
}

type g5FailFirstReleaseHoldService struct {
	imageWalletHoldService
	releaseAttempts atomic.Int64
}

type g5DuplicatePrimaryGateway struct {
	imageGatewayRunner
}

type g5CancelAfterGateway struct {
	imageGatewayRunner
	cancel context.CancelFunc
}

type g5PersistOneAssetGateway struct {
	imageGatewayRunner
	db        *gorm.DB
	requestID string
	owner     repository.ImageOwner
	now       time.Time
}

// Generate 在正式元数据事务前仅落一条主图，稳定复现部分资产事实与清理任务并发交错。
func (g *g5PersistOneAssetGateway) Generate(ctx context.Context, command imagegateway.GenerateImageCommand) (imagegateway.GatewayResult, error) {
	result, err := g.imageGatewayRunner.Generate(ctx, command)
	if err != nil {
		return result, err
	}
	var task model.AIImageTask
	if err := g.db.Where("request_id = ?", g.requestID).First(&task).Error; err != nil {
		return result, err
	}
	for _, gatewayAsset := range result.Assets {
		if gatewayAsset.AssetRole != model.AIImageAssetPrimaryOutput {
			continue
		}
		asset, buildErr := buildImageAsset(g.requestID, task.ID, g.owner, gatewayAsset, nil, g.now)
		if buildErr != nil {
			return result, buildErr
		}
		if createErr := g.db.Create(asset).Error; createErr != nil {
			return result, createErr
		}
		break
	}
	return result, nil
}

// Generate 在Provider结果返回后取消调用上下文，复现客户端断开导致本地终态事务被连带取消的问题。
func (g *g5CancelAfterGateway) Generate(ctx context.Context, command imagegateway.GenerateImageCommand) (imagegateway.GatewayResult, error) {
	result, err := g.imageGatewayRunner.Generate(ctx, command)
	g.cancel()
	return result, err
}

// Generate 只复制一次已持久主图引用，用唯一约束稳定注入元数据事务失败。
func (g *g5DuplicatePrimaryGateway) Generate(ctx context.Context, command imagegateway.GenerateImageCommand) (imagegateway.GatewayResult, error) {
	result, err := g.imageGatewayRunner.Generate(ctx, command)
	if err != nil {
		return result, err
	}
	for _, asset := range result.Assets {
		if asset.AssetRole == model.AIImageAssetPrimaryOutput {
			result.Assets = append(result.Assets, asset)
			break
		}
	}
	return result, nil
}

// ReleaseHoldTx 仅让第一次释放失败，用于证明补偿不重调Provider且复用真实钱包事务。
func (s *g5FailFirstReleaseHoldService) ReleaseHoldTx(tx *gorm.DB, holdID uint64, idempotencyKey string) (*billingservice.SettleTxResult, error) {
	attempt := s.releaseAttempts.Add(1)
	result, err := s.imageWalletHoldService.ReleaseHoldTx(tx, holdID, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if attempt == 1 {
		// 真实释放SQL已经写入当前事务，此处返回错误可验证整笔事务回滚后仍能安全补偿。
		return nil, errors.New("注入首次释放事务回滚")
	}
	return result, nil
}

func (s *g5FailStore) Put(ctx context.Context, ref imagegateway.ObjectRef, body io.Reader, maxBytes int64) (imagegateway.StoredObject, error) {
	if ref.Bucket == s.failBucket {
		return imagegateway.StoredObject{}, errors.New("注入结果区存储失败")
	}
	return s.ObjectStore.Put(ctx, ref, body, maxBytes)
}

func sha256Sum16(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return append([]byte(nil), digest[:16]...)
}
