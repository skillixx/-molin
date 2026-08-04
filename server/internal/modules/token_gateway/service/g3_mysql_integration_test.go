package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	authmodel "molin/server/internal/modules/auth/model"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type startFailingG2Store struct {
	*repository.G2Repository
	err error
}

func (s *startFailingG2Store) StartRequest(context.Context, string, *model.AIExecutionAttempt) error {
	return s.err
}

// TestG3MySQLBillingIntegration 只在隔离 MySQL 8 验证脚本显式注入 DSN 时运行。
func TestG3MySQLBillingIntegration(t *testing.T) {
	dsn := os.Getenv("G3_MYSQL_DSN")
	if dsn == "" {
		t.Skip("G3_MYSQL_DSN 未配置，跳过隔离 MySQL 集成测试")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		// 并发用例会主动制造锁竞争，静默日志便于测试报告聚焦最终断言。
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("连接隔离 MySQL 失败: %v", err)
	}
	pricingRepo := repository.NewG3PricingRepository(db)
	pricing := NewPricingService(pricingRepo)
	walletHolds := billingservice.NewWalletHoldService(
		db, billingrepo.NewWalletRepository(db), billingrepo.NewTransactionRepository(db), billingrepo.NewWalletHoldRepository(db),
	)
	billing := NewAIBillingService(db, pricing, pricingRepo, walletHolds)
	g2 := repository.NewG2Repository(db)
	ctx := context.Background()

	t.Run("非法 n 在创建请求和预占钱包前拒绝", func(t *testing.T) {
		request := integrationRequest("g3-invalid-n-before-hold", 1)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100", "n": "1"}); !errors.Is(err, ErrUnquotableRequest) {
			t.Fatalf("字符串 n 必须在财务与上游链路前拒绝: %v", err)
		}
		assertCount(t, db, "ai_requests", "request_id = ?", request.RequestID, 0)
		assertCount(t, db, "wallet_holds", "idempotency_key = ?", request.RequestID+":hold", 0)
	})

	t.Run("执行启动失败前原子释放钱包预占", func(t *testing.T) {
		request := integrationRequest("g3-start-failed-release", 13)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": 10}); err != nil {
			t.Fatal(err)
		}
		upstreamModel := "qwen-plus"
		orchestrator := NewRequestOrchestrator(&startFailingG2Store{G2Repository: g2, err: errors.New("模拟启动事务失败")}, nil, nil).
			WithBillingService(billing)
		orchestrator.prepared.Store(request.RequestID, &PreparedRequest{
			RequestID:    request.RequestID,
			command:      PrepareCommand{LogicalModel: request.LogicalModelCode, Body: map[string]interface{}{"max_tokens": 10}},
			tokenModel:   model.TokenModel{UpstreamModel: &upstreamModel},
			providerCode: "bailian", endpointCode: "bailian", driver: &fakeOrchestratorDriver{},
		})
		if err := orchestrator.Execute(ctx, request.RequestID, &memorySink{}); err == nil {
			t.Fatal("StartRequest 失败必须从真实编排链返回错误")
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingReleased, "released")
		var executionStatus, errorCode string
		if err := db.Model(&model.AIRequest{}).Select("execution_status, error_code").Where("request_id = ?", request.RequestID).Row().Scan(&executionStatus, &errorCode); err != nil {
			t.Fatal(err)
		}
		if executionStatus != model.AIExecutionFailed || errorCode != "request_not_sent" {
			t.Fatalf("启动失败终态不正确: execution=%s error=%s", executionStatus, errorCode)
		}
	})

	t.Run("价格发布拒绝重叠区间和已发布版本", func(t *testing.T) {
		now := time.Now()
		approvedBy := uint64(1)
		version := &model.AIPriceVersion{
			ID: 2, LogicalModelCode: "qwen-plus", VersionNo: 2, Currency: "CNY", ExchangeRate: decimal.NewFromInt(1),
			Status: model.AIPriceApproved, MinMarginRate: decimal.RequireFromString("0.2"), MaxInputTokens: 1000, MaxOutputTokens: 100,
			FailureChargePolicy: "confirmed_usage", RoundingMode: "ceil_8", CostUpdatedAt: now.Add(-time.Hour),
			CostExpiresAt: now.Add(time.Hour), EffectiveAt: now, CreatedBy: 1, ApprovedBy: &approvedBy, ApprovedAt: &now,
		}
		if err := db.Create(version).Error; err != nil {
			t.Fatal(err)
		}
		for _, meter := range []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens"} {
			if err := db.Create(&model.AIPriceSKU{
				PriceVersionID: version.ID, MeterType: meter, VariantHash: fmt.Sprintf("%064s", meter),
				CostUnitPrice: decimal.NewFromInt(1), SaleUnitPrice: decimal.NewFromInt(2),
				Scale: decimal.NewFromInt(1_000_000), Currency: "CNY",
			}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Model(&model.AIPriceVersion{}).Where("id = ?", version.ID).Update("exchange_rate", decimal.NewFromInt(2)).Error; err == nil {
			t.Fatal("数据库约束必须直接拒绝人民币价格版本写入非 1 汇率")
		}
		if err := db.Model(&model.AIPriceSKU{}).Where("price_version_id = ? AND meter_type = ?", version.ID, "input_tokens").
			Update("sale_unit_price", decimal.NewFromInt(1)).Error; err != nil {
			t.Fatal(err)
		}
		if err := pricingRepo.PublishApprovedVersion(ctx, version.ID, now); !errors.Is(err, repository.ErrPriceVersionNotPublishable) {
			t.Fatalf("低于最低毛利的 SKU 不得发布: %v", err)
		}
		if err := db.Model(&model.AIPriceSKU{}).Where("price_version_id = ? AND meter_type = ?", version.ID, "input_tokens").
			Update("sale_unit_price", decimal.NewFromInt(2)).Error; err != nil {
			t.Fatal(err)
		}
		if err := pricingRepo.PublishApprovedVersion(ctx, version.ID, now); !errors.Is(err, repository.ErrPriceWindowOverlap) {
			t.Fatalf("重叠价格区间必须拒绝发布: %v", err)
		}
		if err := pricingRepo.PublishApprovedVersion(ctx, 1, now); !errors.Is(err, repository.ErrPriceVersionNotPublishable) {
			t.Fatalf("已发布版本不得再次原地发布: %v", err)
		}
		if err := db.Where("price_version_id = ?", version.ID).Delete(&model.AIPriceSKU{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Delete(version).Error; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("同模型重叠价格并发发布只能一个成功", func(t *testing.T) {
		now := time.Now()
		approvedBy := uint64(1)
		versions := []*model.AIPriceVersion{
			{ID: 20, LogicalModelCode: "qwen-concurrent", VersionNo: 1},
			{ID: 21, LogicalModelCode: "qwen-concurrent", VersionNo: 2},
		}
		for _, version := range versions {
			version.Currency = "CNY"
			version.ExchangeRate = decimal.NewFromInt(1)
			version.Status = model.AIPriceApproved
			version.MinMarginRate = decimal.RequireFromString("0.2")
			version.MaxInputTokens, version.MaxOutputTokens = 1000, 100
			version.FailureChargePolicy, version.RoundingMode = "confirmed_usage", "ceil_8"
			version.CostUpdatedAt, version.CostExpiresAt = now.Add(-time.Hour), now.Add(time.Hour)
			version.EffectiveAt, version.CreatedBy, version.ApprovedBy, version.ApprovedAt = now, 1, &approvedBy, &now
			if err := db.Create(version).Error; err != nil {
				t.Fatal(err)
			}
			createIntegrationPriceSKUs(t, db, version.ID)
		}
		var success, overlap atomic.Int64
		var wait sync.WaitGroup
		for _, version := range versions {
			wait.Add(1)
			go func(id uint64) {
				defer wait.Done()
				err := pricingRepo.PublishApprovedVersion(ctx, id, now)
				if err == nil {
					success.Add(1)
				} else if errors.Is(err, repository.ErrPriceWindowOverlap) {
					overlap.Add(1)
				}
			}(version.ID)
		}
		wait.Wait()
		if success.Load() != 1 || overlap.Load() != 1 {
			t.Fatalf("并发发布结果异常: success=%d overlap=%d", success.Load(), overlap.Load())
		}
		assertCount(t, db, "ai_price_versions", "logical_model_code = ? AND status = 'active'", "qwen-concurrent", 1)
	})

	t.Run("Outbox 同聚合有序发布并支持 dead 受控重入", func(t *testing.T) {
		now := time.Now().Truncate(time.Second)
		events := []model.AIOutboxEvent{
			{EventID: "g3-order:held", AggregateType: "ai_request", AggregateID: "g3-order", EventType: "billing_held", PayloadJSON: []byte(`{"status":"held"}`), Status: model.AIOutboxPending, NextRetryAt: now},
			{EventID: "g3-order:settled", AggregateType: "ai_request", AggregateID: "g3-order", EventType: "billing_settled", PayloadJSON: []byte(`{"status":"settled"}`), Status: model.AIOutboxPending, NextRetryAt: now},
		}
		if err := db.Create(&events).Error; err != nil {
			t.Fatal(err)
		}
		outbox := repository.NewG3OutboxRepository(db)
		claimed, err := outbox.ClaimBatch(ctx, now, now.Add(-time.Minute), 10)
		claimedForOrder := filterIntegrationOutboxByAggregate(claimed, "g3-order")
		if err != nil || len(claimedForOrder) != 1 || claimedForOrder[0].EventID != events[0].EventID {
			t.Fatalf("只能认领同聚合最早事件: claimed=%+v err=%v", claimed, err)
		}
		wrongLease := now.Add(-time.Second)
		if err := outbox.MarkPublished(ctx, claimedForOrder[0].ID, wrongLease, now); !errors.Is(err, repository.ErrOutboxLeaseLost) {
			t.Fatalf("旧租约不得覆盖当前拥有者: %v", err)
		}
		if err := outbox.MarkRetry(ctx, claimedForOrder[0].ID, *claimedForOrder[0].LockedAt, now, "test_dead", true); err != nil {
			t.Fatal(err)
		}
		blocked, err := outbox.ClaimBatch(ctx, now, now.Add(-time.Minute), 10)
		if err != nil || len(filterIntegrationOutboxByAggregate(blocked, "g3-order")) != 0 {
			t.Fatalf("前序 dead 时后续事件必须阻塞: claimed=%+v err=%v", blocked, err)
		}
		if err := outbox.RequeueDead(ctx, events[0].EventID, now); err != nil {
			t.Fatal(err)
		}
		for _, expected := range events {
			claimed, err = outbox.ClaimBatch(ctx, now, now.Add(-time.Minute), 10)
			claimedForOrder = filterIntegrationOutboxByAggregate(claimed, "g3-order")
			if err != nil || len(claimedForOrder) != 1 || claimedForOrder[0].EventID != expected.EventID {
				t.Fatalf("重入后必须按顺序认领: want=%s claimed=%+v err=%v", expected.EventID, claimed, err)
			}
			if err := outbox.MarkPublished(ctx, claimedForOrder[0].ID, *claimedForOrder[0].LockedAt, now); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("余额不足事务完整回滚", func(t *testing.T) {
		request := integrationRequest("g3-insufficient", 1)
		_, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"})
		if !errors.Is(err, ErrWalletInsufficient) {
			t.Fatalf("应返回余额不足: %v", err)
		}
		assertCount(t, db, "ai_requests", "request_id = ?", request.RequestID, 0)
		assertCount(t, db, "wallet_holds", "user_id = ?", 1, 0)
	})

	t.Run("一百请求争抢钱包不产生负余额", func(t *testing.T) {
		var success atomic.Int64
		var unexpected atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				request := integrationRequest(fmt.Sprintf("g3-race-%03d", index), 2)
				_, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"})
				if err == nil {
					success.Add(1)
					return
				}
				if !errors.Is(err, ErrWalletInsufficient) {
					unexpected.Add(1)
				}
			}(i)
		}
		wg.Wait()
		if unexpected.Load() != 0 || success.Load() != 10 {
			t.Fatalf("并发占额结果异常: success=%d unexpected=%d", success.Load(), unexpected.Load())
		}
		var balance, frozen decimal.Decimal
		if err := db.Raw("SELECT balance_amount, frozen_amount FROM wallets WHERE user_id = 2").Row().Scan(&balance, &frozen); err != nil {
			t.Fatal(err)
		}
		if balance.IsNegative() || frozen.IsNegative() || !balance.IsZero() || !frozen.Equal(decimal.RequireFromString("0.14")) {
			t.Fatalf("钱包余额不一致: balance=%s frozen=%s", balance, frozen)
		}
		assertCount(t, db, "ai_request_wallet_links", "request_id LIKE ?", "g3-race-%", success.Load())
	})

	t.Run("相同请求并发只形成一个 hold", func(t *testing.T) {
		var success atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				request := integrationRequest("g3-same-request", 3)
				if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err == nil {
					success.Add(1)
				}
			}()
		}
		wg.Wait()
		if success.Load() != 1 {
			t.Fatalf("相同请求只能有一个事务赢家: %d", success.Load())
		}
		assertCount(t, db, "ai_requests", "request_id = ?", "g3-same-request", 1)
		assertCount(t, db, "ai_request_wallet_links", "request_id = ?", "g3-same-request", 1)
		assertCount(t, db, "wallet_holds", "idempotency_key = ?", "g3-same-request:ai-hold", 1)
	})

	t.Run("明确未发出请求可原子复用幂等键", func(t *testing.T) {
		idem, fingerprint := "g3-retry-idem", "g3-retry-fingerprint"
		oldRequest := integrationRequest("g3-retry-old", 12)
		oldRequest.IdempotencyKey, oldRequest.RequestFingerprint = &idem, &fingerprint
		if _, err := billing.PrepareRequest(ctx, oldRequest, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, oldRequest.RequestID)
		failed := successfulIntegrationAttempt(started)
		failed.Outcome, failed.ErrorClass, failed.ResultUnknown = "failed", "request_not_sent", false
		if err := billing.FinalizeRequest(ctx, oldRequest.RequestID, ExecutionResult{Attempt: failed, ErrorCode: "request_not_sent"}); err != nil {
			t.Fatal(err)
		}
		newRequest := integrationRequest("g3-retry-new", 12)
		newRequest.IdempotencyKey, newRequest.RequestFingerprint = &idem, &fingerprint
		if _, err := billing.PrepareRetryRequest(ctx, oldRequest.RequestID, newRequest, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		assertRequestAndHoldStatus(t, db, oldRequest.RequestID, model.AIBillingReleased, "released")
		assertRequestAndHoldStatus(t, db, newRequest.RequestID, model.AIBillingHeld, "holding")
		assertCount(t, db, "ai_requests", "user_id = ? AND idempotency_key = 'g3-retry-idem'", 12, 1)
	})

	t.Run("正式编排链允许明确未发出请求安全重试", func(t *testing.T) {
		projectID, channelID := uint64(15), uint64(1)
		upstreamModel := "qwen-plus"
		store := &g3IntegrationOrchestratorStore{
			G2Repository: g2,
			key:          authmodel.APIKey{ID: 15, UserID: 15, ProjectID: &projectID, Status: "active", ScopeMode: ScopeModeAll},
			snapshot: repository.G2AccessSnapshot{
				UserStatus: "active", RealNameStatus: "verified", ProjectStatus: "active", KeyStatus: "active", ScopeMode: ScopeModeAll, ModelAllowed: true,
				TokenModel: model.TokenModel{LogicalModelCode: "qwen-plus", Modality: "chat", Status: "active", ChannelID: &channelID, UpstreamModel: &upstreamModel},
			},
		}
		orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: channelID, Code: "test", Status: "active"}}, nil).
			WithBillingService(billing).WithVisibilityChecker(fakeVisibilityChecker{visible: true})
		orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{executeErr: true, requestNotSent: true}})
		body := map[string]interface{}{"model": "qwen-plus", "max_tokens": "100"}
		first, err := orchestrator.Prepare(ctx, PrepareCommand{RequestID: "g3-orchestrator-retry-old", IdempotencyKey: "g3-orchestrator-retry", UserID: 15, APIKeyID: 15, LogicalModel: "qwen-plus", Body: body})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Execute(ctx, first.RequestID, &memorySink{}); !errors.Is(err, ErrUpstream) {
			t.Fatalf("首次连接前失败应返回统一上游错误: %v", err)
		}
		second, err := orchestrator.Prepare(ctx, PrepareCommand{RequestID: "g3-orchestrator-retry-new", IdempotencyKey: "g3-orchestrator-retry", UserID: 15, APIKeyID: 15, LogicalModel: "qwen-plus", Body: body})
		if err != nil || second.Existing {
			t.Fatalf("第二次公开 Prepare 应创建新请求: prepared=%+v err=%v", second, err)
		}
		assertRequestAndHoldStatus(t, db, first.RequestID, model.AIBillingReleased, "released")
		assertRequestAndHoldStatus(t, db, second.RequestID, model.AIBillingHeld, "holding")
		assertCount(t, db, "ai_requests", "user_id = ? AND idempotency_key = 'g3-orchestrator-retry'", 15, 1)
	})

	t.Run("失败响应携带可信 Usage 仍按快照结算", func(t *testing.T) {
		request := integrationRequest("g3-failed-with-usage", 13)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		attempt := successfulIntegrationAttempt(started)
		attempt.Outcome, attempt.ErrorClass = "failed", "provider_rejected"
		if err := billing.FinalizeRequest(ctx, request.RequestID, ExecutionResult{Attempt: attempt, Usage: ExecutionUsage{PromptTokens: 10, CompletionTokens: 5, Present: true}}); err != nil {
			t.Fatal(err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettled, "settled")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 13, 1)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 0", request.RequestID, 3)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 1 AND unit_price IS NOT NULL AND amount IS NOT NULL", request.RequestID, 4)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 2 AND unit_price IS NOT NULL AND amount IS NOT NULL", request.RequestID, 4)
	})

	t.Run("输出审核拦截保留上游成本但不向用户扣费", func(t *testing.T) {
		if !supportsProviderCostSource(t, db) {
			t.Skip("仅在 000063 已应用的 G4 隔离数据库验证平台成本事实")
		}
		request := integrationRequest("g4-content-policy-waived", 16)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		if err := billing.FinalizeRequest(ctx, request.RequestID, ExecutionResult{
			Attempt:              successfulIntegrationAttempt(started),
			Usage:                ExecutionUsage{PromptTokens: 10, CompletionTokens: 5, Present: true},
			CustomerChargeWaived: true,
			ErrorCode:            "output_moderation_blocked",
		}); err != nil {
			t.Fatal(err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingReleased, "released")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 16, 0)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 0", request.RequestID, 3)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 1", request.RequestID, 0)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider_cost' AND sequence_no = 0 AND unit_price IS NOT NULL AND amount IS NOT NULL", request.RequestID, 4)
		var providerCost decimal.Decimal
		if err := db.Raw("SELECT COALESCE(SUM(amount),0) FROM ai_usage_items WHERE request_id = ? AND source = 'provider_cost'", request.RequestID).Row().Scan(&providerCost); err != nil {
			t.Fatal(err)
		}
		if !providerCost.Equal(decimal.RequireFromString("0.000015")) {
			t.Fatalf("平台成本金额必须按冻结成本价保存: %s", providerCost)
		}
		assertCount(t, db, "ai_outbox_events", "aggregate_id = ? AND event_type = 'billing_content_policy_waived'", request.RequestID, 1)
	})

	t.Run("输出审核拦截缺失 Usage 时保持预占并在补录后零扣费收敛", func(t *testing.T) {
		if !supportsProviderCostSource(t, db) {
			t.Skip("仅在 000063 已应用的 G4 隔离数据库验证平台成本对账")
		}
		request := integrationRequest("g4-content-policy-reconcile", 17)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		if err := billing.FinalizeRequest(ctx, request.RequestID, ExecutionResult{
			Attempt: successfulIntegrationAttempt(started), CustomerChargeWaived: true,
			ErrorCode: "output_moderation_blocked",
		}); !errors.Is(err, ErrSettlementPending) {
			t.Fatalf("缺失 Usage 时必须进入待对账: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettlementPending, "holding")
		confirmed := ExecutionUsage{PromptTokens: 10, CompletionTokens: 5, Present: true, TotalTokens: 15}
		if err := billing.ResolveContentPolicyWaiver(ctx, request.RequestID, confirmed); err != nil {
			t.Fatalf("受控补录入口应完成平台成本核算和用户零扣费收敛: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingReleased, "released")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 17, 0)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 0", request.RequestID, 3)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider_cost' AND amount IS NOT NULL", request.RequestID, 4)
		if err := billing.ResolveContentPolicyWaiver(ctx, request.RequestID, confirmed); err != nil {
			t.Fatalf("相同 Usage 重复补录必须幂等成功: %v", err)
		}
		conflicting := ExecutionUsage{PromptTokens: 11, CompletionTokens: 5, Present: true, TotalTokens: 16}
		if err := billing.ResolveContentPolicyWaiver(ctx, request.RequestID, conflicting); !errors.Is(err, repository.ErrRequestStateConflict) {
			t.Fatalf("冲突 Usage 重复补录必须返回状态冲突: %v", err)
		}
		assertCount(t, db, "ai_outbox_events", "aggregate_id = ? AND event_type = 'billing_content_policy_waived'", request.RequestID, 1)
	})

	t.Run("输出审核缺失 Usage 超期转人工异常后仍可受控补录", func(t *testing.T) {
		if !supportsProviderCostSource(t, db) {
			t.Skip("仅在 000063 已应用的 G4 隔离数据库验证人工异常收敛")
		}
		request := integrationRequest("g4-content-policy-exception-reconcile", 18)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		if err := billing.FinalizeRequest(ctx, request.RequestID, ExecutionResult{
			Attempt: successfulIntegrationAttempt(started), CustomerChargeWaived: true,
			ErrorCode: "output_moderation_blocked",
		}); !errors.Is(err, ErrSettlementPending) {
			t.Fatalf("缺失 Usage 时必须先进入待对账: %v", err)
		}
		if err := db.Model(&model.AIRequest{}).Where("request_id = ?", request.RequestID).
			Update("updated_at", time.Now().Add(-manualReconcileDeadline-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
		changed, err := billing.ReconcileInterrupted(ctx, 20)
		if err != nil || changed == 0 {
			t.Fatalf("超过对账期限后应进入人工异常: changed=%d err=%v", changed, err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingException, "holding")
		confirmed := ExecutionUsage{PromptTokens: 20, CompletionTokens: 6, Present: true, TotalTokens: 26}
		if err := billing.ResolveContentPolicyWaiver(ctx, request.RequestID, confirmed); err != nil {
			t.Fatalf("内容安全人工异常应通过受控入口零扣费收敛: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingReleased, "released")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 18, 0)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider_cost' AND amount IS NOT NULL", request.RequestID, 4)
	})

	t.Run("缺省 max_tokens 同时用于报价和实际上游请求", func(t *testing.T) {
		projectID, channelID := uint64(13), uint64(1)
		upstreamModel := "qwen-plus"
		store := &g3IntegrationOrchestratorStore{G2Repository: g2,
			key: authmodel.APIKey{ID: 13, UserID: 13, ProjectID: &projectID, Status: "active", ScopeMode: ScopeModeAll},
			snapshot: repository.G2AccessSnapshot{UserStatus: "active", RealNameStatus: "verified", ProjectStatus: "active", KeyStatus: "active", ScopeMode: ScopeModeAll, ModelAllowed: true,
				TokenModel: model.TokenModel{LogicalModelCode: "qwen-plus", Modality: "chat", Status: "active", ChannelID: &channelID, UpstreamModel: &upstreamModel}}}
		driver := &fakeOrchestratorDriver{}
		orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: channelID, Code: "test", Status: "active"}}, nil).
			WithBillingService(billing).WithVisibilityChecker(fakeVisibilityChecker{visible: true})
		orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
		prepared, err := orchestrator.Prepare(ctx, PrepareCommand{RequestID: "g3-default-max-tokens", UserID: 13, APIKeyID: 13, LogicalModel: "qwen-plus",
			Body: map[string]interface{}{"model": "qwen-plus"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Execute(ctx, prepared.RequestID, &memorySink{}); err != nil {
			t.Fatal(err)
		}
		if got, ok := driver.lastRequest.Body["max_tokens"].(uint64); !ok || got != 100 {
			t.Fatalf("缺省上限必须写入上游请求并受模型上限约束: value=%v", driver.lastRequest.Body["max_tokens"])
		}
	})

	t.Run("Bifrost 映射缺失确认未发送并立即释放", func(t *testing.T) {
		projectID, channelID := uint64(13), uint64(1)
		upstreamModel := "qwen-plus"
		store := &g3IntegrationOrchestratorStore{G2Repository: g2,
			key: authmodel.APIKey{ID: 13, UserID: 13, ProjectID: &projectID, Status: "active", ScopeMode: ScopeModeAll},
			snapshot: repository.G2AccessSnapshot{UserStatus: "active", RealNameStatus: "verified", ProjectStatus: "active", KeyStatus: "active", ScopeMode: ScopeModeAll, ModelAllowed: true,
				TokenModel: model.TokenModel{LogicalModelCode: "qwen-plus", Modality: "chat", Status: "active", ChannelID: &channelID, UpstreamModel: &upstreamModel}}}
		orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: channelID, Code: "test", Status: "active"}}, nil).
			WithBillingService(billing).WithVisibilityChecker(fakeVisibilityChecker{visible: true})
		orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: NewBifrostDriver(BifrostDriverConfig{
			BaseURL: "http://127.0.0.1:1", InternalToken: "test", ModelMapping: map[string]string{"other-model": "test/other"},
		})})
		prepared, err := orchestrator.Prepare(ctx, PrepareCommand{RequestID: "g3-bifrost-mapping-not-sent", UserID: 13, APIKeyID: 13, LogicalModel: "qwen-plus",
			Body: map[string]interface{}{"model": "qwen-plus", "max_tokens": "10"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Execute(ctx, prepared.RequestID, &memorySink{}); !errors.Is(err, ErrUpstream) {
			t.Fatalf("映射缺失应返回统一上游错误: %v", err)
		}
		assertRequestAndHoldStatus(t, db, prepared.RequestID, model.AIBillingReleased, "released")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 13, 2)
	})

	t.Run("失败 attempt 对账发现可信 Usage 后结算并补齐计费行", func(t *testing.T) {
		request := integrationRequest("g3-reconcile-failed-usage", 14)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		finished := time.Now()
		if err := db.Model(&model.AIExecutionAttempt{}).Where("request_id = ?", request.RequestID).Updates(map[string]interface{}{
			"status": "failed", "result_unknown": false, "error_class": "provider_rejected", "finished_at": finished,
		}).Error; err != nil {
			t.Fatal(err)
		}
		usage := ExecutionUsage{PromptTokens: 10, CompletionTokens: 5, Present: true}
		if err := db.Create(usageModels(request.RequestID, usage)).Error; err != nil {
			t.Fatal(err)
		}
		staleAt := started.Add(-48 * time.Hour)
		if err := db.Model(&model.AIRequest{}).Where("request_id = ?", request.RequestID).Updates(map[string]interface{}{
			"execution_status": model.AIExecutionFailed, "billing_status": model.AIBillingSettlementPending, "updated_at": staleAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
		changed, err := billing.ReconcileInterrupted(ctx, 100)
		if err != nil || changed < 1 {
			t.Fatalf("对账必须按可信 Usage 结算失败 attempt: changed=%d err=%v", changed, err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettled, "settled")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 14, 1)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 1 AND meter_type IN ('input_tokens','output_tokens','cached_tokens','reasoning_tokens') AND unit_price IS NOT NULL AND amount IS NOT NULL", request.RequestID, 4)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 2 AND amount IS NOT NULL", request.RequestID, 4)
		var settledAmount, itemAmount decimal.Decimal
		if err := db.Raw("SELECT settled_amount FROM ai_requests WHERE request_id = ?", request.RequestID).Row().Scan(&settledAmount); err != nil {
			t.Fatal(err)
		}
		if err := db.Raw("SELECT COALESCE(SUM(amount),0) FROM ai_usage_items WHERE request_id = ? AND source = 'provider' AND sequence_no = 1 AND meter_type IN ('input_tokens','output_tokens','cached_tokens','reasoning_tokens')", request.RequestID).Row().Scan(&itemAmount); err != nil {
			t.Fatal(err)
		}
		if !settledAmount.Equal(itemAmount) {
			t.Fatalf("计费行金额必须可还原请求实扣: settled=%s items=%s", settledAmount, itemAmount)
		}
	})

	t.Run("补价数量不一致时拒绝且不改写原始 Usage", func(t *testing.T) {
		requestID := "g3-reconcile-failed-usage"
		original := model.AIUsageItem{RequestID: requestID, MeterType: "input_tokens", Source: "gateway", SequenceNo: 9, Quantity: decimal.NewFromInt(10)}
		if err := db.Create(&original).Error; err != nil {
			t.Fatal(err)
		}
		unitPrice, amount := decimal.NewFromInt(2), decimal.NewFromInt(18)
		priced := model.AIUsageItem{RequestID: requestID, MeterType: "input_tokens", Source: "gateway", SequenceNo: 9,
			Quantity: decimal.NewFromInt(9), UnitPrice: &unitPrice, Amount: &amount}
		if err := createUsageTx(db, []model.AIUsageItem{priced}); !errors.Is(err, repository.ErrRequestStateConflict) {
			t.Fatalf("数量不一致必须拒绝补价: %v", err)
		}
		var stored model.AIUsageItem
		if err := db.Where("request_id = ? AND meter_type = ? AND source = ? AND sequence_no = ?", requestID, "input_tokens", "gateway", 9).First(&stored).Error; err != nil {
			t.Fatal(err)
		}
		if !stored.Quantity.Equal(decimal.NewFromInt(10)) || stored.UnitPrice != nil || stored.Amount != nil {
			t.Fatalf("补价冲突不得改写原始事实: %+v", stored)
		}
	})

	t.Run("正式错误响应携带 Usage 时结算且无 Usage 时待对账", func(t *testing.T) {
		withUsage := true
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			if withUsage {
				_, _ = io.WriteString(w, `{"error":{"message":"provider rejected"},"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
				return
			}
			_, _ = io.WriteString(w, `{"error":{"message":"provider rejected"}}`)
		}))
		defer upstream.Close()
		for _, test := range []struct {
			requestID string
			userID    uint64
			usage     bool
			billing   string
			hold      string
		}{
			{requestID: "g3-driver-error-usage", userID: 16, usage: true, billing: model.AIBillingSettled, hold: "settled"},
			{requestID: "g3-driver-error-no-usage", userID: 17, usage: false, billing: model.AIBillingSettlementPending, hold: "holding"},
		} {
			withUsage = test.usage
			projectID, channelID := test.userID, uint64(1)
			upstreamModel := "qwen-plus"
			store := &g3IntegrationOrchestratorStore{
				G2Repository: g2,
				key:          authmodel.APIKey{ID: test.userID, UserID: test.userID, ProjectID: &projectID, Status: "active", ScopeMode: ScopeModeAll},
				snapshot: repository.G2AccessSnapshot{UserStatus: "active", RealNameStatus: "verified", ProjectStatus: "active", KeyStatus: "active", ScopeMode: ScopeModeAll, ModelAllowed: true,
					TokenModel: model.TokenModel{LogicalModelCode: "qwen-plus", Modality: "chat", Status: "active", ChannelID: &channelID, UpstreamModel: &upstreamModel}},
			}
			orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: channelID, Code: "test", Status: "active"}}, nil).
				WithBillingService(billing).WithVisibilityChecker(fakeVisibilityChecker{visible: true})
			orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: NewBifrostDriver(BifrostDriverConfig{
				BaseURL: upstream.URL, InternalToken: "test-internal-token", ModelMapping: map[string]string{"qwen-plus": "test/qwen-plus"}, HTTPClient: upstream.Client(),
			})})
			prepared, err := orchestrator.Prepare(ctx, PrepareCommand{RequestID: test.requestID, UserID: test.userID, APIKeyID: test.userID, LogicalModel: "qwen-plus", Body: map[string]interface{}{"model": "qwen-plus", "max_tokens": "100"}})
			if err != nil {
				t.Fatal(err)
			}
			executeErr := orchestrator.Execute(ctx, prepared.RequestID, &memorySink{})
			if test.usage && executeErr != nil {
				t.Fatalf("携带 Usage 的失败响应应完成结算: %v", executeErr)
			}
			if !test.usage && !errors.Is(executeErr, ErrSettlementPending) {
				t.Fatalf("请求已发出但无 Usage 必须待对账: %v", executeErr)
			}
			assertRequestAndHoldStatus(t, db, test.requestID, test.billing, test.hold)
		}
	})

	t.Run("SSE 错误事件虽携带 Usage 但结果未知时仍待对账", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"error\":{\"message\":\"provider rejected\"},\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5}}\n\n")
		}))
		defer upstream.Close()
		projectID, channelID := uint64(18), uint64(1)
		upstreamModel := "qwen-plus"
		store := &g3IntegrationOrchestratorStore{G2Repository: g2,
			key: authmodel.APIKey{ID: 18, UserID: 18, ProjectID: &projectID, Status: "active", ScopeMode: ScopeModeAll},
			snapshot: repository.G2AccessSnapshot{UserStatus: "active", RealNameStatus: "verified", ProjectStatus: "active", KeyStatus: "active", ScopeMode: ScopeModeAll, ModelAllowed: true,
				TokenModel: model.TokenModel{LogicalModelCode: "qwen-plus", Modality: "chat", Status: "active", ChannelID: &channelID, UpstreamModel: &upstreamModel}}}
		orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: channelID, Code: "test", Status: "active"}}, nil).
			WithBillingService(billing).WithVisibilityChecker(fakeVisibilityChecker{visible: true})
		orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: NewBifrostDriver(BifrostDriverConfig{
			BaseURL: upstream.URL, InternalToken: "test-internal-token", ModelMapping: map[string]string{"qwen-plus": "test/qwen-plus"},
			HTTPClient: upstream.Client(), StreamClient: upstream.Client(),
		})})
		prepared, err := orchestrator.Prepare(ctx, PrepareCommand{RequestID: "g3-sse-error-usage", UserID: 18, APIKeyID: 18, LogicalModel: "qwen-plus", Stream: true,
			Body: map[string]interface{}{"model": "qwen-plus", "max_tokens": "100", "stream": true}})
		if err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.Execute(ctx, prepared.RequestID, &memorySink{}); !errors.Is(err, ErrSettlementPending) {
			t.Fatalf("SSE 未正常终止时即使看到 Usage 也必须待对账: %v", err)
		}
		assertRequestAndHoldStatus(t, db, prepared.RequestID, model.AIBillingSettlementPending, "holding")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 18, 0)
	})

	t.Run("重复结算和结算释放竞争只有一个终态", func(t *testing.T) {
		request := integrationRequest("g3-terminal-race", 4)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		attempt := &model.AIExecutionAttempt{
			RequestID: request.RequestID, AttemptNo: 1, ExecutionDriver: "native", ProviderCode: "test",
			ExecutionModelCode: "test-model", Status: "running", StartedAt: started, CreatedAt: started,
		}
		if err := g2.StartRequest(ctx, request.RequestID, attempt); err != nil {
			t.Fatal(err)
		}
		successResult := ExecutionResult{Attempt: ExecutionAttempt{
			AttemptNo: 1, Driver: "native", ProviderCode: "test", ProviderModel: "test-model",
			StartedAt: started, FinishedAt: time.Now(), Outcome: "success",
		}, Usage: ExecutionUsage{PromptTokens: 100, CompletionTokens: 50, CachedTokens: 20, ReasoningTokens: 10, Present: true}}
		failedResult := ExecutionResult{Attempt: ExecutionAttempt{
			AttemptNo: 1, Driver: "native", ProviderCode: "test", ProviderModel: "test-model",
			StartedAt: started, FinishedAt: time.Now(), Outcome: "failed", ErrorClass: "request_not_sent",
		}, ErrorCode: "known_failure"}
		var wg sync.WaitGroup
		for _, result := range []ExecutionResult{successResult, failedResult, successResult} {
			wg.Add(1)
			go func(current ExecutionResult) {
				defer wg.Done()
				_ = billing.FinalizeRequest(ctx, request.RequestID, current)
			}(result)
		}
		wg.Wait()
		var status string
		if err := db.Raw("SELECT billing_status FROM ai_requests WHERE request_id = ?", request.RequestID).Row().Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != model.AIBillingSettled && status != model.AIBillingReleased {
			t.Fatalf("必须形成唯一终态: %s", status)
		}
		if err := billing.FinalizeRequest(ctx, request.RequestID, successResult); err != nil {
			t.Fatal(err)
		}
		assertAtMostOne(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 4)
		assertCount(t, db, "wallet_holds", "user_id = ? AND status IN ('settled','released')", 4, 1)
	})

	t.Run("Usage 缺失保留预占并进入待对账", func(t *testing.T) {
		request := integrationRequest("g3-usage-missing", 6)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		result := ExecutionResult{Attempt: successfulIntegrationAttempt(started), Usage: ExecutionUsage{Present: false}}
		if err := billing.FinalizeRequest(ctx, request.RequestID, result); !errors.Is(err, ErrSettlementPending) {
			t.Fatalf("Usage 缺失应返回待结算状态: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettlementPending, "holding")
		assertCount(t, db, "ai_outbox_events", "event_id = ?", request.RequestID+":billing_reconcile_required", 1)
		assertCount(t, db, "ai_usage_items", "request_id = ?", request.RequestID, 0)
		changed, err := billing.reconcileOne(ctx, request.RequestID, time.Now())
		if err != nil || changed {
			t.Fatalf("Usage 缺失且未超期时 Worker 必须保持待对账: changed=%t err=%v", changed, err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettlementPending, "holding")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 6, 0)
	})

	t.Run("客户端断连后仍按可信 Usage 结算", func(t *testing.T) {
		request := integrationRequest("g3-client-disconnected", 7)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		result := ExecutionResult{
			Attempt:            successfulIntegrationAttempt(started),
			Usage:              ExecutionUsage{PromptTokens: 100, CompletionTokens: 50, Present: true},
			ClientDisconnected: true,
		}
		if err := billing.FinalizeRequest(context.WithoutCancel(ctx), request.RequestID, result); err != nil {
			t.Fatal(err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettled, "settled")
		var disconnected bool
		if err := db.Raw("SELECT client_disconnected FROM ai_requests WHERE request_id = ?", request.RequestID).Row().Scan(&disconnected); err != nil {
			t.Fatal(err)
		}
		if !disconnected {
			t.Fatal("客户端断连事实未写入请求账本")
		}
		assertWalletTransactionContinuity(t, db, 7)
	})

	t.Run("可信全零 Usage 形成零金额结算终态", func(t *testing.T) {
		request := integrationRequest("g3-zero-usage", 7)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		if err := billing.FinalizeRequest(ctx, request.RequestID, ExecutionResult{
			Attempt: successfulIntegrationAttempt(started), Usage: ExecutionUsage{Present: true},
		}); err != nil {
			t.Fatalf("可信全零 Usage 应完成零金额结算: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettled, "settled")
		// 原始零 Usage 保留三项，销售拆分和成本拆分各保留四个 SKU，共十一条事实。
		assertCount(t, db, "ai_usage_items", "request_id = ?", request.RequestID, 11)
		assertCount(t, db, "ai_usage_items", "request_id = ? AND source = 'provider' AND sequence_no = 2 AND amount IS NOT NULL", request.RequestID, 4)
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 7, 1)
	})

	t.Run("结算最后一步失败时整笔事务回滚", func(t *testing.T) {
		request := integrationRequest("g3-write-rollback", 8)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		result := ExecutionResult{
			Attempt: successfulIntegrationAttempt(started),
			Usage:   ExecutionUsage{PromptTokens: 100, CompletionTokens: 50, Present: true},
		}
		const triggerName = "g3_test_fail_outbox_insert"
		if err := db.Exec("DROP TRIGGER IF EXISTS " + triggerName).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("CREATE TRIGGER " + triggerName + " BEFORE INSERT ON ai_outbox_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'g3 forced outbox failure'").Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Exec("DROP TRIGGER IF EXISTS " + triggerName).Error })
		if err := billing.FinalizeRequest(ctx, request.RequestID, result); err == nil {
			t.Fatal("强制 Outbox 写失败时结算事务必须返回错误")
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingHeld, "holding")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 8, 0)
		assertCount(t, db, "ai_usage_items", "request_id = ?", request.RequestID, 0)
		if err := db.Exec("DROP TRIGGER IF EXISTS " + triggerName).Error; err != nil {
			t.Fatal(err)
		}
		if err := billing.FinalizeRequest(ctx, request.RequestID, result); err != nil {
			t.Fatalf("故障移除后应使用同一入口完成结算: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettled, "settled")
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 8, 1)
	})

	t.Run("待对账超过期限转人工异常且保留 hold", func(t *testing.T) {
		request := integrationRequest("g3-manual-reconcile", 9)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		if err := billing.FinalizeRequest(ctx, request.RequestID, ExecutionResult{
			Attempt: successfulIntegrationAttempt(started), Usage: ExecutionUsage{Present: false},
		}); !errors.Is(err, ErrSettlementPending) {
			t.Fatalf("缺失 Usage 应先进入待对账: %v", err)
		}
		if err := db.Model(&model.AIRequest{}).Where("request_id = ?", request.RequestID).
			Update("updated_at", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)).Error; err != nil {
			t.Fatal(err)
		}
		changed, err := billing.reconcileOne(ctx, request.RequestID, time.Now())
		if err != nil || !changed {
			t.Fatalf("超期请求应收敛到人工异常: changed=%t err=%v", changed, err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingException, "holding")
		assertCount(t, db, "ai_outbox_events", "event_id = ?", request.RequestID+":billing_manual_review_required", 1)
		assertCount(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", 9, 0)
		if err := billing.ResolveException(ctx, request.RequestID, ManualResolutionRelease, ExecutionUsage{PromptTokens: 1, Present: true}); !errors.Is(err, ErrBillingAmountException) {
			t.Fatalf("release 携带正用量必须拒绝: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingException, "holding")
		if err := billing.ResolveException(ctx, request.RequestID, ManualResolutionRelease, ExecutionUsage{}); err != nil {
			t.Fatalf("人工确认未产生成本后应可受控释放: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingReleased, "released")
		assertCount(t, db, "ai_outbox_events", "event_id = ?", request.RequestID+":billing_manual_released", 1)
		if err := billing.ResolveException(ctx, request.RequestID, ManualResolutionRelease, ExecutionUsage{PromptTokens: 1, Present: true}); !errors.Is(err, ErrBillingAmountException) {
			t.Fatalf("已释放请求也不得吞掉携带 Usage 的矛盾 release: %v", err)
		}
		if err := billing.ResolveException(ctx, request.RequestID, ManualResolutionSettle, ExecutionUsage{Present: true}); !errors.Is(err, ErrBillingAmountException) {
			t.Fatalf("已释放请求也必须先拒绝人工零用量结算: %v", err)
		}
	})

	t.Run("待对账缺少执行记录时超期仍转人工异常", func(t *testing.T) {
		request := integrationRequest("g3-manual-reconcile-missing-attempt", 9)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
			t.Fatal(err)
		}
		started := startIntegrationAttempt(t, ctx, g2, request.RequestID)
		if err := billing.FinalizeRequest(ctx, request.RequestID, ExecutionResult{
			Attempt: successfulIntegrationAttempt(started), Usage: ExecutionUsage{Present: false},
		}); !errors.Is(err, ErrSettlementPending) {
			t.Fatalf("缺失 Usage 应先进入待对账: %v", err)
		}
		if err := db.Where("request_id = ?", request.RequestID).Delete(&model.AIExecutionAttempt{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIRequest{}).Where("request_id = ?", request.RequestID).
			Update("updated_at", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)).Error; err != nil {
			t.Fatal(err)
		}
		changed, err := billing.reconcileOne(ctx, request.RequestID, time.Now())
		if err != nil || !changed {
			t.Fatalf("缺少执行记录的超期请求也应转人工异常: changed=%t err=%v", changed, err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingException, "holding")
		assertCount(t, db, "ai_outbox_events", "event_id = ?", request.RequestID+":billing_manual_review_required", 1)
	})

	t.Run("单条损坏记录不阻塞后续对账", func(t *testing.T) {
		bad := integrationRequest("g3-reconcile-bad", 10)
		good := integrationRequest("g3-reconcile-good", 11)
		for _, request := range []*model.AIRequest{bad, good} {
			if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "100"}); err != nil {
				t.Fatal(err)
			}
		}
		// 删除坏请求的关联以模拟历史损坏数据；该记录必须报错，但不能形成队头阻塞。
		if err := db.Where("request_id = ?", bad.RequestID).Delete(&model.AIRequestWalletLink{}).Error; err != nil {
			t.Fatal(err)
		}
		staleAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		if err := db.Model(&model.AIRequest{}).Where("request_id IN ?", []string{bad.RequestID, good.RequestID}).
			Update("updated_at", staleAt).Error; err != nil {
			t.Fatal(err)
		}
		changed, err := billing.ReconcileInterrupted(ctx, 100)
		if err == nil || changed < 1 {
			t.Fatalf("对账应报告坏记录且继续处理好记录: changed=%d err=%v", changed, err)
		}
		assertRequestAndHoldStatus(t, db, good.RequestID, model.AIBillingReleased, "released")
		var badBillingStatus, badHoldStatus string
		if err := db.Raw("SELECT billing_status FROM ai_requests WHERE request_id = ?", bad.RequestID).Row().Scan(&badBillingStatus); err != nil {
			t.Fatal(err)
		}
		if err := db.Raw("SELECT status FROM wallet_holds WHERE user_id = ?", bad.UserID).Row().Scan(&badHoldStatus); err != nil {
			t.Fatal(err)
		}
		if badBillingStatus != model.AIBillingHeld || badHoldStatus != "holding" {
			t.Fatalf("坏记录必须保留财务证据: billing=%s hold=%s", badBillingStatus, badHoldStatus)
		}
	})

	t.Run("实际金额超过预占进入异常并暂停价格", func(t *testing.T) {
		request := integrationRequest("g3-over-hold", 5)
		if _, err := billing.PrepareRequest(ctx, request, map[string]interface{}{"max_tokens": "1"}); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if err := g2.StartRequest(ctx, request.RequestID, &model.AIExecutionAttempt{
			RequestID: request.RequestID, AttemptNo: 1, ExecutionDriver: "native", ProviderCode: "test",
			ExecutionModelCode: "test-model", Status: "running", StartedAt: started, CreatedAt: started,
		}); err != nil {
			t.Fatal(err)
		}
		result := ExecutionResult{Attempt: ExecutionAttempt{
			AttemptNo: 1, Driver: "native", ProviderCode: "test", ProviderModel: "test-model",
			StartedAt: started, FinishedAt: time.Now(), Outcome: "success",
		}, Usage: ExecutionUsage{PromptTokens: 1000, CompletionTokens: 1000, ReasoningTokens: 1000, Present: true}}
		if err := billing.FinalizeRequest(ctx, request.RequestID, result); !errors.Is(err, ErrBillingException) {
			t.Fatalf("超额费用应返回计费异常: %v", err)
		}
		var billingStatus, priceStatus, holdStatus string
		if err := db.Raw("SELECT billing_status FROM ai_requests WHERE request_id = ?", request.RequestID).Row().Scan(&billingStatus); err != nil {
			t.Fatal(err)
		}
		if err := db.Raw("SELECT status FROM ai_price_versions WHERE id = 1").Row().Scan(&priceStatus); err != nil {
			t.Fatal(err)
		}
		if err := db.Raw("SELECT h.status FROM wallet_holds h JOIN ai_request_wallet_links l ON l.wallet_hold_id=h.id WHERE l.request_id=?", request.RequestID).Row().Scan(&holdStatus); err != nil {
			t.Fatal(err)
		}
		if billingStatus != model.AIBillingException || priceStatus != model.AIPriceSuspended || holdStatus != "holding" {
			t.Fatalf("超额异常未正确隔离: billing=%s price=%s hold=%s", billingStatus, priceStatus, holdStatus)
		}
		assertCount(t, db, "ai_outbox_events", "event_id = ?", request.RequestID+":billing_p0_exception", 1)
		if err := billing.ResolveException(ctx, request.RequestID, ManualResolutionSettle, ExecutionUsage{Present: true}); !errors.Is(err, ErrBillingAmountException) {
			t.Fatalf("人工零用量不得形成 settled 请求与 released hold: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingException, "holding")
		if err := billing.ResolveException(ctx, request.RequestID, ManualResolutionSettle, ExecutionUsage{CompletionTokens: 1, Present: true}); err != nil {
			t.Fatalf("人工核定 Usage 后应可结算: %v", err)
		}
		assertRequestAndHoldStatus(t, db, request.RequestID, model.AIBillingSettled, "settled")
		if err := billing.ResolveException(ctx, request.RequestID, ManualResolutionSettle, ExecutionUsage{CompletionTokens: 1, Present: true}); err != nil {
			t.Fatalf("与真实终态一致的人工结算重试应幂等成功: %v", err)
		}
		if err := billing.ResolveException(ctx, request.RequestID, ManualResolutionSettle, ExecutionUsage{CompletionTokens: 2, Present: true}); !errors.Is(err, repository.ErrRequestStateConflict) {
			t.Fatalf("不同 Usage 的人工结算重试必须冲突: %v", err)
		}
		var providerRawItems, providerBilledItems, reconciledItems int64
		if err := db.Model(&model.AIUsageItem{}).Where("request_id = ? AND source = 'provider' AND sequence_no = 0", request.RequestID).Count(&providerRawItems).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIUsageItem{}).Where("request_id = ? AND source = 'provider' AND sequence_no = 1", request.RequestID).Count(&providerBilledItems).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIUsageItem{}).Where("request_id = ? AND source = 'reconciled'", request.RequestID).Count(&reconciledItems).Error; err != nil {
			t.Fatal(err)
		}
		if providerRawItems != 4 || providerBilledItems != 4 || reconciledItems != 4 {
			t.Fatalf("原始、异常计费拆分与人工核定 Usage 必须分别保留: raw=%d billed=%d reconciled=%d", providerRawItems, providerBilledItems, reconciledItems)
		}
	})

	assertCount(t, db, "token_usage_logs", "1 = ?", 1, 0)
}

func assertWalletTransactionContinuity(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	type transactionRow struct {
		Type         string
		Amount       decimal.Decimal
		BalanceAfter decimal.Decimal
	}
	var rows []transactionRow
	if err := db.Raw("SELECT type, amount, balance_after FROM wallet_transactions WHERE user_id = ? ORDER BY id", userID).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Type != "freeze" || rows[1].Type != "unfreeze" || rows[2].Type != "consume" {
		t.Fatalf("钱包流水顺序不完整: %+v", rows)
	}
	if !rows[0].BalanceAfter.Add(rows[1].Amount).Equal(rows[1].BalanceAfter) ||
		!rows[1].BalanceAfter.Sub(rows[2].Amount).Equal(rows[2].BalanceAfter) {
		t.Fatalf("钱包流水 balance_after 无法连续还原: %+v", rows)
	}
}

func createIntegrationPriceSKUs(t *testing.T, db *gorm.DB, versionID uint64) {
	t.Helper()
	for index, meter := range []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens"} {
		if err := db.Create(&model.AIPriceSKU{
			PriceVersionID: versionID, MeterType: meter, VariantHash: fmt.Sprintf("%064x", versionID*10+uint64(index)),
			CostUnitPrice: decimal.NewFromInt(1), SaleUnitPrice: decimal.NewFromInt(2),
			Scale: decimal.NewFromInt(1_000_000), Currency: "CNY",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func startIntegrationAttempt(t *testing.T, ctx context.Context, g2 *repository.G2Repository, requestID string) time.Time {
	t.Helper()
	started := time.Now()
	if err := g2.StartRequest(ctx, requestID, &model.AIExecutionAttempt{
		RequestID: requestID, AttemptNo: 1, ExecutionDriver: "native", ProviderCode: "test",
		ExecutionModelCode: "test-model", Status: "running", StartedAt: started, CreatedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	return started
}

func successfulIntegrationAttempt(started time.Time) ExecutionAttempt {
	return ExecutionAttempt{
		AttemptNo: 1, Driver: "native", ProviderCode: "test", ProviderModel: "test-model",
		StartedAt: started, FinishedAt: time.Now(), Outcome: "success",
	}
}

func assertRequestAndHoldStatus(t *testing.T, db *gorm.DB, requestID, wantBilling, wantHold string) {
	t.Helper()
	var billingStatus, holdStatus string
	if err := db.Raw("SELECT billing_status FROM ai_requests WHERE request_id = ?", requestID).Row().Scan(&billingStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT h.status FROM wallet_holds h JOIN ai_request_wallet_links l ON l.wallet_hold_id=h.id WHERE l.request_id=?", requestID).Row().Scan(&holdStatus); err != nil {
		t.Fatal(err)
	}
	if billingStatus != wantBilling || holdStatus != wantHold {
		t.Fatalf("请求与 hold 状态不一致: billing=%s hold=%s", billingStatus, holdStatus)
	}
}

func integrationRequest(requestID string, userID uint64) *model.AIRequest {
	projectID, keyID := userID, userID
	return &model.AIRequest{
		RequestID: requestID, UserID: userID, ProjectID: &projectID, APIKeyID: &keyID,
		LogicalModelCode: "qwen-plus", Modality: "chat", ModerationStatus: model.AIModerationPending,
		ExecutionStatus: model.AIExecutionPending, BillingStatus: model.AIBillingUnquoted, VersionNo: 1,
	}
}

type g3IntegrationOrchestratorStore struct {
	*repository.G2Repository
	key      authmodel.APIKey
	snapshot repository.G2AccessSnapshot
}

func (s *g3IntegrationOrchestratorStore) FindProjectKeyByID(_ context.Context, userID, keyID uint64) (*authmodel.APIKey, error) {
	if s.key.UserID != userID || s.key.ID != keyID {
		return nil, repository.ErrProjectKeyNotFound
	}
	key := s.key
	return &key, nil
}

func (s *g3IntegrationOrchestratorStore) LoadAccessSnapshot(_ context.Context, userID, projectID, keyID uint64, modelCode string) (*repository.G2AccessSnapshot, error) {
	if s.key.UserID != userID || s.key.ID != keyID || s.key.ProjectID == nil || *s.key.ProjectID != projectID || s.snapshot.TokenModel.LogicalModelCode != modelCode {
		return nil, repository.ErrProjectKeyNotFound
	}
	snapshot := s.snapshot
	return &snapshot, nil
}

func filterIntegrationOutboxByAggregate(events []model.AIOutboxEvent, aggregateID string) []model.AIOutboxEvent {
	filtered := make([]model.AIOutboxEvent, 0, len(events))
	for _, event := range events {
		if event.AggregateID == aggregateID {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func assertCount(t *testing.T, db *gorm.DB, table, where string, arg interface{}, want int64) {
	t.Helper()
	var count int64
	if err := db.Table(table).Where(where, arg).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s 计数不符: got=%d want=%d", table, count, want)
	}
}

func supportsProviderCostSource(t *testing.T, db *gorm.DB) bool {
	t.Helper()
	var count int64
	if err := db.Raw(`SELECT COUNT(*) FROM information_schema.check_constraints
		WHERE constraint_schema = DATABASE() AND constraint_name = 'chk_ai_usage_source'
		AND check_clause LIKE '%provider_cost%'`).Row().Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

func assertAtMostOne(t *testing.T, db *gorm.DB, table, where string, arg interface{}) {
	t.Helper()
	var count int64
	if err := db.Table(table).Where(where, arg).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count > 1 {
		t.Fatalf("%s 出现重复财务终态: count=%d", table, count)
	}
}
