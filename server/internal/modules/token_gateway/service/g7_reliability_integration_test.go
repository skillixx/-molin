package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	authmodel "molin/server/internal/modules/auth/model"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

const (
	g7FirstLoadUserID = uint64(701)
	g7IdempotencyUser = uint64(801)
	g7StreamUser      = uint64(802)
	g7ChaosUser       = uint64(803)
	g7ModelCode       = "qwen-plus"
)

// g7FakeDriver 是完全本地、无付费上游依赖的确定性执行器。
// 普通和流式响应都携带可信 Usage，便于验证账本、预占、结算和钱包流水的闭环一致性。
type g7FakeDriver struct {
	requestNotSent bool
}

func (d *g7FakeDriver) Name() string { return "bifrost" }

func (d *g7FakeDriver) ChatCompletion(_ context.Context, request ExecutionRequest) (*ExecutionResponse, error) {
	startedAt := time.Now().Add(-time.Millisecond)
	if d.requestNotSent {
		return &ExecutionResponse{Attempt: ExecutionAttempt{
			AttemptNo: request.AttemptNo, Driver: d.Name(), ProviderCode: "fake", EndpointCode: "fake",
			ProviderModel: "fake/qwen-plus", StartedAt: startedAt, FinishedAt: time.Now(),
			Outcome: "failed", ErrorClass: "request_not_sent", ResultUnknown: false,
		}}, errors.New("模拟 Fake 上游停止，确认请求尚未发出")
	}
	return &ExecutionResponse{
		Response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"id":"g7-fake","choices":[{"message":{"content":"OK"}}]}`)),
		},
		Usage: ExecutionUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, Present: true},
		Attempt: ExecutionAttempt{
			AttemptNo: request.AttemptNo, Driver: d.Name(), ProviderCode: "fake", EndpointCode: "fake",
			ProviderModel: "fake/qwen-plus", StartedAt: startedAt, FinishedAt: time.Now(), Outcome: "success",
		},
	}, nil
}

func (d *g7FakeDriver) ChatCompletionStream(_ context.Context, request ExecutionRequest) (*ExecutionResponse, error) {
	startedAt := time.Now().Add(-time.Millisecond)
	body := "data: {\"id\":\"g7-stream\",\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150}}\n\n" +
		"data: [DONE]\n\n"
	return &ExecutionResponse{
		Response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(bytes.NewBufferString(body))},
		Attempt: ExecutionAttempt{
			AttemptNo: request.AttemptNo, Driver: d.Name(), ProviderCode: "fake", EndpointCode: "fake",
			ProviderModel: "fake/qwen-plus", StartedAt: startedAt, Outcome: "success",
		},
	}, nil
}

func (d *g7FakeDriver) NormalizeStreamLine(line []byte, logicalModel string) (ExecutionStreamChunk, error) {
	return normalizeExecutionStreamLine(line, logicalModel)
}

type g7OutboxPublisher struct{}

func (g7OutboxPublisher) Publish(context.Context, model.AIOutboxEvent) error { return nil }

// TestG7MySQLReliabilityIntegration 只允许在脚本创建的临时 MySQL 中执行。
// 测试完成时除历史已发布 Outbox 外，不允许留下未释放预占、差额或账单异常。
func TestG7MySQLReliabilityIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("短测试模式跳过 G7 隔离 MySQL 可靠性验收")
	}
	if testingEnv := testingEnvironment("G7_ISOLATED_TEST"); testingEnv != "YES" {
		t.Skip("G7_ISOLATED_TEST 未显式设置为 YES，跳过隔离 MySQL 验收")
	}
	dsn := testingEnvironment("G7_MYSQL_DSN")
	if dsn == "" {
		t.Fatal("G7_MYSQL_DSN 未配置")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("连接 G7 隔离 MySQL 失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(120)
	sqlDB.SetMaxIdleConns(120)

	metrics := NewAIGatewayMetrics(nil)
	pricingRepo := repository.NewG3PricingRepository(db)
	pricing := NewPricingService(pricingRepo)
	walletHolds := billingservice.NewWalletHoldService(
		db, billingrepo.NewWalletRepository(db), billingrepo.NewTransactionRepository(db), billingrepo.NewWalletHoldRepository(db),
	)
	billing := NewAIBillingService(db, pricing, pricingRepo, walletHolds).WithMetrics(metrics)
	g2 := repository.NewG2Repository(db)
	ctx := context.Background()

	t.Run("一百并发完整结算不超扣且不丢账", func(t *testing.T) {
		var succeeded atomic.Int64
		errorsFound := make(chan error, 100)
		var wait sync.WaitGroup
		for index := 0; index < 100; index++ {
			wait.Add(1)
			go func(current int) {
				defer wait.Done()
				userID := g7FirstLoadUserID + uint64(current)
				orchestrator := newG7Orchestrator(g2, billing, metrics, &g7FakeDriver{}, userID)
				requestID := fmt.Sprintf("g7-load-%03d", current)
				var prepared *PreparedRequest
				var prepareErr error
				// 服务层已经处理单事务死锁；负载工具继续模拟客户端对 409/503 可重试响应的有界退避。
				for attempt := 0; attempt < 10; attempt++ {
					prepared, prepareErr = orchestrator.Prepare(ctx, g7PrepareCommand(requestID, requestID, false, userID))
					if prepareErr == nil || !errors.Is(prepareErr, billingservice.ErrConcurrentUpdate) {
						break
					}
					time.Sleep(time.Duration(10+current%7+attempt*10) * time.Millisecond)
				}
				if prepareErr != nil {
					errorsFound <- fmt.Errorf("%s Prepare 有界重试后失败: %w", requestID, prepareErr)
					return
				}
				if prepared.Existing {
					errorsFound <- fmt.Errorf("%s 在全新隔离库意外命中历史请求", requestID)
					return
				}
				if executeErr := orchestrator.Execute(ctx, prepared.RequestID, &memorySink{}); executeErr != nil {
					errorsFound <- fmt.Errorf("%s Execute 失败: %w", requestID, executeErr)
					return
				}
				succeeded.Add(1)
			}(index)
		}
		wait.Wait()
		close(errorsFound)
		for testErr := range errorsFound {
			t.Error(testErr)
		}
		if succeeded.Load() != 100 {
			t.Fatalf("100 并发完整结算未全部成功: got=%d", succeeded.Load())
		}
		assertG7Count(t, db, "ai_requests", "request_id LIKE 'g7-load-%' AND billing_status = 'settled'", 100)
		assertG7Count(t, db, "wallet_holds", "idempotency_key LIKE 'g7-load-%:ai-hold' AND status = 'settled'", 100)
		assertG7Count(t, db, "wallet_transactions", "user_id BETWEEN ? AND ? AND type = 'consume'", g7FirstLoadUserID, g7FirstLoadUserID+99, 100)
	})

	t.Run("二十路幂等竞争只执行和结算一次", func(t *testing.T) {
		orchestrator := newG7Orchestrator(g2, billing, metrics, &g7FakeDriver{}, g7IdempotencyUser)
		var created atomic.Int64
		var existing atomic.Int64
		var winner sync.Once
		winnerID := ""
		errorsFound := make(chan error, 20)
		var wait sync.WaitGroup
		for index := 0; index < 20; index++ {
			wait.Add(1)
			go func(current int) {
				defer wait.Done()
				requestID := fmt.Sprintf("g7-idem-%02d", current)
				prepared, prepareErr := orchestrator.Prepare(ctx, g7PrepareCommand(requestID, "g7-idempotency-key", false, g7IdempotencyUser))
				if prepareErr != nil {
					errorsFound <- prepareErr
					return
				}
				if prepared.Existing {
					existing.Add(1)
					return
				}
				created.Add(1)
				winner.Do(func() { winnerID = prepared.RequestID })
			}(index)
		}
		wait.Wait()
		close(errorsFound)
		for testErr := range errorsFound {
			t.Error(testErr)
		}
		if created.Load() != 1 || existing.Load() != 19 || winnerID == "" {
			t.Fatalf("幂等竞争结果不正确: created=%d existing=%d winner=%q", created.Load(), existing.Load(), winnerID)
		}
		if executeErr := orchestrator.Execute(ctx, winnerID, &memorySink{}); executeErr != nil {
			t.Fatal(executeErr)
		}
		assertG7Count(t, db, "ai_requests", "idempotency_key = ?", "g7-idempotency-key", 1)
		assertG7Count(t, db, "wallet_holds", "idempotency_key = ?", winnerID+":ai-hold", 1)
		assertG7Count(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", g7IdempotencyUser, 1)
	})

	t.Run("流式客户端断连后继续读取可信 Usage 并结算", func(t *testing.T) {
		orchestrator := newG7Orchestrator(g2, billing, metrics, &g7FakeDriver{}, g7StreamUser)
		prepared, prepareErr := orchestrator.Prepare(ctx, g7PrepareCommand("g7-stream-disconnect", "g7-stream-disconnect", true, g7StreamUser))
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		if executeErr := orchestrator.Execute(ctx, prepared.RequestID, &memorySink{failWrite: true}); executeErr != nil {
			t.Fatal(executeErr)
		}
		var disconnected bool
		var billingStatus string
		if queryErr := db.Raw("SELECT client_disconnected, billing_status FROM ai_requests WHERE request_id = ?", prepared.RequestID).
			Row().Scan(&disconnected, &billingStatus); queryErr != nil {
			t.Fatal(queryErr)
		}
		if !disconnected || billingStatus != model.AIBillingSettled {
			t.Fatalf("流式断连事实或结算终态不正确: disconnected=%t billing=%s", disconnected, billingStatus)
		}
	})

	t.Run("Fake 上游停止释放预占且恢复后可安全重试", func(t *testing.T) {
		stopped := newG7Orchestrator(g2, billing, metrics, &g7FakeDriver{requestNotSent: true}, g7ChaosUser)
		first, prepareErr := stopped.Prepare(ctx, g7PrepareCommand("g7-chaos-stopped", "g7-chaos-idempotency", false, g7ChaosUser))
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		if executeErr := stopped.Execute(ctx, first.RequestID, &memorySink{}); !errors.Is(executeErr, ErrUpstream) {
			t.Fatalf("Fake 上游停止应返回统一上游错误: %v", executeErr)
		}
		assertRequestAndHoldStatus(t, db, first.RequestID, model.AIBillingReleased, "released")

		recovered := newG7Orchestrator(g2, billing, metrics, &g7FakeDriver{}, g7ChaosUser)
		second, retryErr := recovered.Prepare(ctx, g7PrepareCommand("g7-chaos-recovered", "g7-chaos-idempotency", false, g7ChaosUser))
		if retryErr != nil || second.Existing {
			t.Fatalf("Fake 上游恢复后应创建安全重试请求: prepared=%+v err=%v", second, retryErr)
		}
		if executeErr := recovered.Execute(ctx, second.RequestID, &memorySink{}); executeErr != nil {
			t.Fatal(executeErr)
		}
		assertRequestAndHoldStatus(t, db, second.RequestID, model.AIBillingSettled, "settled")
	})

	t.Run("发布 Outbox 后自动核对差额与异常均为零", func(t *testing.T) {
		worker := NewOutboxWorker(repository.NewG3OutboxRepository(db), g7OutboxPublisher{})
		for round := 0; round < 20; round++ {
			published, publishErr := worker.RunOnce(ctx, 100)
			if publishErr != nil {
				t.Fatal(publishErr)
			}
			if published == 0 {
				break
			}
		}
		snapshot, collectErr := NewAIGatewayDBGaugeCollector(db).CollectAIGatewayGauges(ctx, time.Now())
		if collectErr != nil {
			t.Fatal(collectErr)
		}
		for code, difference := range snapshot.BillingDifferences {
			if !difference.IsZero() {
				t.Errorf("账单差额必须为 0: code=%s difference=%s", code, difference)
			}
		}
		for code, count := range snapshot.BillingAnomalies {
			if count != 0 {
				t.Errorf("账单异常必须为 0: code=%s count=%d", code, count)
			}
		}
		if snapshot.UnreleasedHolds.Count != 0 || !snapshot.UnreleasedHolds.Amount.IsZero() {
			t.Errorf("不允许遗留钱包预占: count=%d amount=%s", snapshot.UnreleasedHolds.Count, snapshot.UnreleasedHolds.Amount)
		}
		for status, backlog := range snapshot.OutboxBacklog {
			if backlog.Count != 0 {
				t.Errorf("Outbox 活跃积压必须为 0: status=%s count=%d", status, backlog.Count)
			}
		}
		for status, backlog := range snapshot.CompensationBacklog {
			if backlog.Count != 0 {
				t.Errorf("补偿任务活跃积压必须为 0: status=%s count=%d", status, backlog.Count)
			}
		}
		assertG7WalletInvariant(t, db)
		assertG7Count(t, db, "ai_requests", "request_id LIKE 'g7-%' AND price_snapshot_json IS NULL", 0)
	})
}

func newG7Orchestrator(g2 *repository.G2Repository, billing *AIBillingService, metrics *AIGatewayMetrics, driver ExecutionDriver, userID uint64) *RequestOrchestratorService {
	upstreamModel := g7ModelCode
	channelID := uint64(701)
	store := &g3IntegrationOrchestratorStore{
		G2Repository: g2,
		key: authmodel.APIKey{
			ID: userID, UserID: userID, ProjectID: g7Uint64Pointer(userID), Status: "active", ScopeMode: ScopeModeAll,
		},
		snapshot: repository.G2AccessSnapshot{
			UserStatus: "active", RealNameStatus: "verified", ProjectStatus: "active", KeyStatus: "active",
			ScopeMode: ScopeModeAll, ModelAllowed: true,
			TokenModel: model.TokenModel{LogicalModelCode: g7ModelCode, Modality: "chat", Status: "active", ChannelID: &channelID, UpstreamModel: &upstreamModel},
		},
	}
	orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 701, Code: "fake", Status: "active"}}, nil).
		WithBillingService(billing).WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithMetrics(metrics)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	return orchestrator
}

func g7PrepareCommand(requestID, idempotencyKey string, stream bool, userID uint64) PrepareCommand {
	return PrepareCommand{
		RequestID: requestID, IdempotencyKey: idempotencyKey, UserID: userID, APIKeyID: userID,
		LogicalModel: g7ModelCode, Stream: stream,
		Body: map[string]interface{}{"model": g7ModelCode, "max_tokens": 100},
	}
}

func assertG7Count(t *testing.T, db *gorm.DB, table, where string, args ...interface{}) {
	t.Helper()
	want := args[len(args)-1].(int)
	queryArgs := args[:len(args)-1]
	var count int64
	if err := db.Table(table).Where(where, queryArgs...).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != int64(want) {
		t.Fatalf("%s 计数不正确: got=%d want=%d", table, count, want)
	}
}

func assertG7WalletInvariant(t *testing.T, db *gorm.DB) {
	t.Helper()
	var invalidWallets int64
	if err := db.Raw("SELECT COUNT(*) FROM wallets WHERE user_id BETWEEN ? AND ? AND (balance_amount < 0 OR frozen_amount <> 0)", g7FirstLoadUserID, g7ChaosUser).
		Row().Scan(&invalidWallets); err != nil {
		t.Fatal(err)
	}
	if invalidWallets != 0 {
		t.Fatalf("存在余额为负数或冻结金额未归零的钱包: count=%d", invalidWallets)
	}
}

func testingEnvironment(key string) string {
	return strings.TrimSpace(os.Getenv(key))
}

func g7Uint64Pointer(value uint64) *uint64 { return &value }
