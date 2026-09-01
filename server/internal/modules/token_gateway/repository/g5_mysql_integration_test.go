package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/model"
)

// TestG5MySQLIntegration 只允许在验收脚本创建的临时 MySQL 中运行，禁止误用项目数据库。
func TestG5MySQLIntegration(t *testing.T) {
	dsn := os.Getenv("G5_MYSQL_DSN")
	if dsn == "" || os.Getenv("G5_ISOLATED_TEST") != "YES" {
		t.Skip("仅在 G5 隔离 MySQL 验收脚本显式授权时运行")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("连接 G5 隔离 MySQL 失败: %v", err)
	}
	ctx := context.Background()
	repo := NewG5AdminRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	modelCode := "molin/g5-integration"

	cleanupG5IntegrationFacts(t, db, modelCode)
	channelID, modelID := createG5ChannelAndModel(t, db, modelCode, now)
	route := createG5Route(t, db, modelCode, channelID)
	createG5ActivePrice(t, db, modelCode, now)

	t.Run("发布前置条件返回稳定错误分类", func(t *testing.T) {
		if err := db.Model(&model.TokenModel{}).Where("id = ?", modelID).Updates(map[string]interface{}{"docs_url": nil, "docs_url_health_status": "unpublished"}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repo.PublishModel(ctx, modelID, 901, "缺少文档"); !errors.Is(err, ErrModelDocumentsNotReady) {
			t.Fatalf("缺少文档必须返回固定分类，实际 err=%v", err)
		}
		docsURL := "https://docs.invalid/api"
		if err := db.Model(&model.TokenModel{}).Where("id = ?", modelID).Updates(map[string]interface{}{"docs_url": docsURL, "docs_url_health_status": "healthy"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIPriceVersion{}).Where("logical_model_code = ?", modelCode).Update("status", model.AIPriceRetired).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repo.PublishModel(ctx, modelID, 901, "缺少价格"); !errors.Is(err, ErrModelPriceNotReady) {
			t.Fatalf("缺少唯一生效价格必须返回固定分类，实际 err=%v", err)
		}
		if err := db.Model(&model.AIPriceVersion{}).Where("logical_model_code = ?", modelCode).Update("status", model.AIPriceActive).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIModelRoute{}).Where("id = ?", route.ID).Update("status", "disabled").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := repo.PublishModel(ctx, modelID, 901, "缺少路由"); !errors.Is(err, ErrModelRouteNotReady) {
			t.Fatalf("缺少健康生效路由必须返回固定分类，实际 err=%v", err)
		}
		if err := db.Model(&model.AIModelRoute{}).Where("id = ?", route.ID).Update("status", "active").Error; err != nil {
			t.Fatal(err)
		}
		orphan := model.AIModelReleaseVersion{ModelID: modelID, VersionNo: 1, Status: "retired", SnapshotJSON: json.RawMessage(`{"orphan":true}`), Reason: "隔离测试孤儿版本", CreatedBy: 901, PublishedAt: now}
		if err := db.Create(&orphan).Error; err != nil {
			t.Fatal(err)
		}
		release, err := repo.PublishModel(ctx, modelID, 901, "跳过孤儿版本号")
		if err != nil {
			t.Fatalf("历史版本号被孤儿记录占用时必须自动分配下一版本，实际 err=%v", err)
		}
		if release.VersionNo != 2 {
			t.Fatalf("孤儿 v1 后首个有效发布必须使用 v2，实际 version=%d", release.VersionNo)
		}
	})

	t.Run("同一模型并发发布收敛到唯一版本", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan *model.AIModelReleaseVersion, 2)
		errorsCh := make(chan error, 2)
		var wg sync.WaitGroup
		for index := 0; index < 2; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				release, publishErr := repo.PublishModel(ctx, modelID, 901, fmt.Sprintf("并发发布-%d", index))
				if publishErr != nil {
					errorsCh <- publishErr
					return
				}
				results <- release
			}(index)
		}
		close(start)
		wg.Wait()
		close(results)
		close(errorsCh)
		for publishErr := range errorsCh {
			t.Fatalf("并发重复发布应幂等收敛，实际 err=%v", publishErr)
		}
		var releaseIDs []uint64
		for release := range results {
			releaseIDs = append(releaseIDs, release.ID)
		}
		var releaseCount int64
		if err := db.Model(&model.AIModelReleaseVersion{}).Where("model_id = ?", modelID).Count(&releaseCount).Error; err != nil {
			t.Fatal(err)
		}
		if len(releaseIDs) != 2 || releaseIDs[0] == 0 || releaseIDs[0] != releaseIDs[1] || releaseCount != 2 {
			t.Fatalf("模型并发发布必须返回同一有效版本且保留一条历史孤儿记录: ids=%v releases=%d", releaseIDs, releaseCount)
		}
	})

	t.Run("经营指标使用冻结价格快照并支持渠道筛选", func(t *testing.T) {
		requestID := "g5-dashboard-integration"
		settled := decimal.RequireFromString("1.50000000")
		completedAt := now
		priceSnapshot := json.RawMessage(`{"skus":{"input_tokens":{"cost_unit_price":"1","scale":"1000"},"output_tokens":{"cost_unit_price":"2","scale":"1000"},"cached_tokens":{"cost_unit_price":"0.5","scale":"1000"},"reasoning_tokens":{"cost_unit_price":"3","scale":"1000"}}}`)
		request := model.AIRequest{
			RequestID: requestID, UserID: 901, LogicalModelCode: modelCode, Modality: "chat",
			ModerationStatus: model.AIModerationPassed, ExecutionStatus: model.AIExecutionSucceeded,
			BillingStatus: model.AIBillingSettled, SettledAmount: &settled, PriceSnapshotJSON: priceSnapshot,
			CompletedAt: &completedAt, VersionNo: 1, CreatedAt: now,
		}
		if err := db.Create(&request).Error; err != nil {
			t.Fatal(err)
		}
		endpoint := fmt.Sprintf("route:%d", route.ID)
		attempt := model.AIExecutionAttempt{
			RequestID: requestID, AttemptNo: 1, ExecutionDriver: "bifrost", ProviderCode: "openrouter",
			EndpointCode: &endpoint, ExecutionModelCode: route.ProviderModel, Status: "succeeded", StartedAt: now,
		}
		if err := db.Create(&attempt).Error; err != nil {
			t.Fatal(err)
		}
		costInput := decimal.RequireFromString("0.09")
		costCached := decimal.RequireFromString("0.005")
		costOutput := decimal.RequireFromString("0.09")
		costReasoning := decimal.RequireFromString("0.015")
		usage := []model.AIUsageItem{
			{RequestID: requestID, MeterType: "total_tokens", Source: "provider", SequenceNo: 0, Quantity: decimal.NewFromInt(150)},
			{RequestID: requestID, MeterType: "input_tokens", Source: "provider", SequenceNo: 2, Quantity: decimal.NewFromInt(90), Amount: &costInput},
			{RequestID: requestID, MeterType: "cached_tokens", Source: "provider", SequenceNo: 2, Quantity: decimal.NewFromInt(10), Amount: &costCached},
			{RequestID: requestID, MeterType: "output_tokens", Source: "provider", SequenceNo: 2, Quantity: decimal.NewFromInt(45), Amount: &costOutput},
			{RequestID: requestID, MeterType: "reasoning_tokens", Source: "provider", SequenceNo: 2, Quantity: decimal.NewFromInt(5), Amount: &costReasoning},
		}
		if err := db.Create(&usage).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO ai_gateway_rejection_events(request_id, logical_model_code, reason_code, scope_type, scope_id, created_at) VALUES
			('g5-reject-safety', ?, 'content_policy_violation', 'user', '901', ?),
			('g5-reject-rate', ?, 'rpm_limit_exceeded', 'project', '901', ?),
			('g5-reject-budget', ?, 'budget_limit_exceeded', 'api_key', '901', ?)`, modelCode, now, modelCode, now, modelCode, now).Error; err != nil {
			t.Fatal(err)
		}

		metrics, err := repo.DashboardMetrics(ctx, G5DashboardFilter{From: now.Add(-time.Minute), To: now.Add(time.Minute), Model: modelCode})
		if err != nil {
			t.Fatal(err)
		}
		if metrics.TotalRequests != 1 || metrics.SuccessfulRequests != 1 || !metrics.TotalTokens.Equal(decimal.NewFromInt(150)) ||
			!metrics.SaleAmount.Equal(settled) || !metrics.UpstreamCost.Equal(decimal.RequireFromString("0.2")) ||
			metrics.SafetyRejections != 1 || metrics.RateLimitRejections != 1 || metrics.BudgetRejections != 1 {
			t.Fatalf("G5 经营指标聚合错误: %+v", metrics)
		}
		filtered, err := repo.DashboardMetrics(ctx, G5DashboardFilter{From: now.Add(-time.Minute), To: now.Add(time.Minute), Model: modelCode, ChannelID: channelID})
		if err != nil || filtered.TotalRequests != 1 || !filtered.UpstreamCost.Equal(decimal.RequireFromString("0.2")) {
			t.Fatalf("渠道筛选未命中真实执行路由: metrics=%+v err=%v", filtered, err)
		}
	})

	t.Run("熔断状态跨节点共享且成功后恢复", func(t *testing.T) {
		fallback := model.AIModelRoute{LogicalModelCode: modelCode, ChannelID: channelID, ProviderModel: "openrouter/fallback/model", Priority: 200, Weight: 100, TimeoutMS: 30000, MaxRetries: 0, CircuitBreakerThreshold: 2, FallbackOrder: 1, Status: "active", VersionNo: 1, UpdatedBy: 901}
		if err := db.Create(&fallback).Error; err != nil {
			t.Fatal(err)
		}
		if selected, err := repo.ResolveActiveRoute(ctx, modelCode, "before-circuit"); err != nil || selected.ID != route.ID {
			t.Fatalf("健康路由应可解析: %v", err)
		}
		if err := repo.RecordRouteTransportFailure(ctx, route.ID, 2); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordRouteTransportFailure(ctx, route.ID, 2); err != nil {
			t.Fatal(err)
		}
		if selected, err := repo.ResolveActiveRoute(ctx, modelCode, "opened-circuit"); err != nil || selected.ID != fallback.ID {
			t.Fatalf("达到阈值后下一请求必须从共享路由池选择备用路由: route=%+v err=%v", selected, err)
		}
		if err := repo.ResetRouteTransportFailures(ctx, route.ID); err != nil {
			t.Fatal(err)
		}
		if selected, err := repo.ResolveActiveRoute(ctx, modelCode, "after-reset"); err != nil || selected.ID != route.ID {
			t.Fatalf("成功后必须清除共享熔断状态: route=%+v err=%v", selected, err)
		}
	})

	t.Run("路由乐观锁只允许一个并发更新成功", func(t *testing.T) {
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for index := 0; index < 2; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				candidate := route
				candidate.Weight = uint64(101 + index)
				results <- repo.UpdateRoute(ctx, &candidate, 1)
			}(index)
		}
		close(start)
		wg.Wait()
		close(results)
		var succeeded, conflicted int
		for updateErr := range results {
			switch {
			case updateErr == nil:
				succeeded++
			case errors.Is(updateErr, ErrRouteVersionConflict):
				conflicted++
			default:
				t.Fatalf("并发路由更新出现未知错误: %v", updateErr)
			}
		}
		if succeeded != 1 || conflicted != 1 {
			t.Fatalf("路由乐观锁结果错误: succeeded=%d conflicted=%d", succeeded, conflicted)
		}
	})

	t.Run("并发价格创建分配连续且唯一版本", func(t *testing.T) {
		start := make(chan struct{})
		versions := make(chan uint64, 2)
		errorsCh := make(chan error, 2)
		var wg sync.WaitGroup
		for index := 0; index < 2; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				price := newG5Price(modelCode, model.AIPriceDraft, now.Add(time.Duration(index+1)*time.Hour))
				skus := newG5SKUs()
				if createErr := repo.CreatePrice(ctx, &price, skus); createErr != nil {
					errorsCh <- createErr
					return
				}
				versions <- price.VersionNo
			}(index)
		}
		close(start)
		wg.Wait()
		close(versions)
		close(errorsCh)
		for createErr := range errorsCh {
			t.Fatalf("并发价格创建失败: %v", createErr)
		}
		actual := make([]int, 0, 2)
		for version := range versions {
			actual = append(actual, int(version))
		}
		sort.Ints(actual)
		if fmt.Sprint(actual) != "[2 3]" {
			t.Fatalf("价格版本必须连续且唯一，实际 %v", actual)
		}
	})
}

func createG5ChannelAndModel(t *testing.T, db *gorm.DB, modelCode string, now time.Time) (uint64, uint64) {
	t.Helper()
	channel := model.TokenChannel{Code: "g5-integration", Name: "G5 Integration", Type: "openai_compatible", BaseURL: "http://bifrost.invalid", APIKeyEncrypted: "encrypted-test-only", Status: "active", Priority: 100, HealthStatus: "healthy"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	docsURL, quickURL, upstream := "https://docs.invalid/api", "https://docs.invalid/quick", "openrouter/test/model"
	operatorID := uint64(901)
	item := model.TokenModel{LogicalModelCode: modelCode, DisplayName: "G5 Integration", ProviderName: "Test", Modality: "chat", ChannelID: &channel.ID, UpstreamModel: &upstream, Status: "inactive", DocsURL: &docsURL, DocsURLHealthStatus: "healthy", QuickStartURL: &quickURL, QuickStartURLHealthStatus: "healthy", VisibleScope: "all", UpdatedBy: &operatorID, CreatedAt: now}
	// 旧Chat契约仍在截至77号迁移的专用库验收；夹具不插入后续视频专用列，不能升级旧库掩盖兼容问题。
	if err := db.Omit("VideoContractJSON").Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return channel.ID, item.ID
}

func createG5Route(t *testing.T, db *gorm.DB, modelCode string, channelID uint64) model.AIModelRoute {
	t.Helper()
	route := model.AIModelRoute{LogicalModelCode: modelCode, ChannelID: channelID, ProviderModel: "openrouter/test/model", Priority: 100, Weight: 100, TimeoutMS: 30000, MaxRetries: 0, CircuitBreakerThreshold: 2, FallbackOrder: 0, Status: "active", VersionNo: 1, UpdatedBy: 901}
	if err := db.Create(&route).Error; err != nil {
		t.Fatal(err)
	}
	return route
}

func createG5ActivePrice(t *testing.T, db *gorm.DB, modelCode string, now time.Time) {
	t.Helper()
	price := newG5Price(modelCode, model.AIPriceActive, now.Add(-time.Hour))
	price.VersionNo = 1
	if err := db.Create(&price).Error; err != nil {
		t.Fatal(err)
	}
	skus := newG5SKUs()
	for index := range skus {
		skus[index].PriceVersionID = price.ID
	}
	if err := db.Create(&skus).Error; err != nil {
		t.Fatal(err)
	}
}

func newG5Price(modelCode, status string, effectiveAt time.Time) model.AIPriceVersion {
	return model.AIPriceVersion{LogicalModelCode: modelCode, Currency: "CNY", ExchangeRate: decimal.NewFromInt(1), Status: status, MinMarginRate: decimal.RequireFromString("0.2"), MaxInputTokens: 100000, MaxOutputTokens: 10000, FailureChargePolicy: "confirmed_usage", RoundingMode: "ceil_8", CostUpdatedAt: effectiveAt.Add(-time.Hour), CostExpiresAt: effectiveAt.Add(48 * time.Hour), EffectiveAt: effectiveAt, CreatedBy: 901}
}

func newG5SKUs() []model.AIPriceSKU {
	meters := []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens"}
	items := make([]model.AIPriceSKU, 0, len(meters))
	for index, meter := range meters {
		items = append(items, model.AIPriceSKU{MeterType: meter, VariantHash: fmt.Sprintf("%064d", index+1), CostUnitPrice: decimal.NewFromInt(1), SaleUnitPrice: decimal.NewFromInt(2), Scale: decimal.NewFromInt(1000), Currency: "CNY"})
	}
	return items
}

func cleanupG5IntegrationFacts(t *testing.T, db *gorm.DB, modelCode string) {
	t.Helper()
	statements := []string{
		"DELETE FROM ai_gateway_rejection_events WHERE logical_model_code = ?",
		"DELETE FROM ai_usage_items WHERE request_id = 'g5-dashboard-integration'",
		"DELETE FROM ai_execution_attempts WHERE request_id = 'g5-dashboard-integration'",
		"DELETE FROM ai_requests WHERE request_id = 'g5-dashboard-integration'",
		"DELETE runtime FROM ai_model_route_runtime_states runtime JOIN ai_model_routes routes ON routes.id = runtime.route_id WHERE routes.logical_model_code = ?",
		"DELETE FROM ai_model_routes WHERE logical_model_code = ?",
		"DELETE skus FROM ai_price_skus skus JOIN ai_price_versions prices ON prices.id = skus.price_version_id WHERE prices.logical_model_code = ?",
		"DELETE FROM ai_price_versions WHERE logical_model_code = ?",
		"DELETE FROM token_models WHERE logical_model_code = ?",
		"DELETE FROM token_channels WHERE code = 'g5-integration'",
	}
	for _, statement := range statements {
		var query *gorm.DB
		if strings.Contains(statement, "?") {
			query = db.Exec(statement, modelCode)
		} else {
			query = db.Exec(statement)
		}
		if err := query.Error; err != nil {
			t.Fatalf("清理 G5 隔离测试事实失败: %v", err)
		}
	}
}
