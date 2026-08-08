package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysqldsn "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	authmodel "molin/server/internal/modules/auth/model"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	pkgcrypto "molin/server/pkg/crypto"
)

const (
	g7FirstLoadUserID = uint64(701)
	g7IdempotencyUser = uint64(801)
	g7StreamUser      = uint64(802)
	g7ChaosUser       = uint64(803)
	g7ModelCode       = "qwen-plus"
)

// g7MeasuredBifrostDriver 包装生产 BifrostDriver，只增加测试时延记录，不改变驱动身份和协议行为。
// 驱动访问 httptest 的 Fake Bifrost 协议端点，既满足数据库 driver 约束，也不会接触真实节点或付费上游。
type g7MeasuredBifrostDriver struct {
	driver            *BifrostDriver
	timingMu          sync.Mutex
	upstreamStarted   map[string]time.Time
	upstreamFirstByte map[string]time.Time
}

// g7FirstByteReadCloser 在生产驱动返回的响应体第一次读到字节时打点，用于排除 Fake 上游等待时间。
type g7FirstByteReadCloser struct {
	io.ReadCloser
	once        sync.Once
	onFirstByte func(time.Time)
}

func (r *g7FirstByteReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.ReadCloser.Read(buffer)
	if count > 0 {
		r.once.Do(func() { r.onFirstByte(time.Now()) })
	}
	return count, err
}

// g7FirstWriteSink 记录 JSON 响应或公开 SSE 数据帧第一次成功写入客户端 Sink 的时刻。
type g7FirstWriteSink struct {
	memorySink
	timingMu     sync.Mutex
	firstWriteAt time.Time
}

func (s *g7FirstWriteSink) Write(data []byte) error {
	if err := s.memorySink.Write(data); err != nil {
		return err
	}
	if len(data) > 0 {
		s.timingMu.Lock()
		if s.firstWriteAt.IsZero() {
			s.firstWriteAt = time.Now()
		}
		s.timingMu.Unlock()
	}
	return nil
}

func (s *g7FirstWriteSink) firstWrite() (time.Time, bool) {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	return s.firstWriteAt, !s.firstWriteAt.IsZero()
}

func newG7MeasuredBifrostDriver(server *httptest.Server) *g7MeasuredBifrostDriver {
	modelMapping := DefaultBifrostModelMapping()
	modelMapping[g7ModelCode] = "g7-fake/qwen-plus"
	driver := NewBifrostDriver(BifrostDriverConfig{
		BaseURL: server.URL, InternalToken: "g7-internal-test-token",
		// 隔离门禁仍走生产 BifrostDriver；仅把 G7 逻辑模型显式映射到本地 Fake Bifrost provider/model，避免访问付费上游。
		ModelMapping: modelMapping,
		HTTPClient:   server.Client(), StreamClient: server.Client(),
	})
	return &g7MeasuredBifrostDriver{driver: driver}
}

func (d *g7MeasuredBifrostDriver) Name() string { return d.driver.Name() }

func (d *g7MeasuredBifrostDriver) ChatCompletion(ctx context.Context, request ExecutionRequest) (*ExecutionResponse, error) {
	d.recordUpstreamStarted(request.RequestID, time.Now())
	result, err := d.driver.ChatCompletion(ctx, request)
	d.observeFirstByte(request.RequestID, result, err)
	return result, err
}

func (d *g7MeasuredBifrostDriver) ChatCompletionStream(ctx context.Context, request ExecutionRequest) (*ExecutionResponse, error) {
	d.recordUpstreamStarted(request.RequestID, time.Now())
	result, err := d.driver.ChatCompletionStream(ctx, request)
	d.observeFirstByte(request.RequestID, result, err)
	return result, err
}

func (d *g7MeasuredBifrostDriver) observeFirstByte(requestID string, result *ExecutionResponse, err error) {
	if err == nil && result != nil && result.Response != nil && result.Response.Body != nil {
		// 仅包装读取观察点，不预读、不缓存也不改变生产 BifrostDriver 的流式协议行为。
		result.Response.Body = &g7FirstByteReadCloser{ReadCloser: result.Response.Body, onFirstByte: func(observedAt time.Time) {
			d.recordUpstreamFirstByte(requestID, observedAt)
		}}
	}
}

func (d *g7MeasuredBifrostDriver) recordUpstreamStarted(requestID string, observedAt time.Time) {
	d.timingMu.Lock()
	defer d.timingMu.Unlock()
	if d.upstreamStarted == nil {
		d.upstreamStarted = make(map[string]time.Time)
	}
	d.upstreamStarted[requestID] = observedAt
}

func (d *g7MeasuredBifrostDriver) started(requestID string) (time.Time, bool) {
	d.timingMu.Lock()
	defer d.timingMu.Unlock()
	observedAt, ok := d.upstreamStarted[requestID]
	return observedAt, ok
}

func (d *g7MeasuredBifrostDriver) recordUpstreamFirstByte(requestID string, observedAt time.Time) {
	d.timingMu.Lock()
	defer d.timingMu.Unlock()
	if d.upstreamFirstByte == nil {
		d.upstreamFirstByte = make(map[string]time.Time)
	}
	d.upstreamFirstByte[requestID] = observedAt
}

func (d *g7MeasuredBifrostDriver) firstByte(requestID string) (time.Time, bool) {
	d.timingMu.Lock()
	defer d.timingMu.Unlock()
	observedAt, ok := d.upstreamFirstByte[requestID]
	return observedAt, ok
}

func (d *g7MeasuredBifrostDriver) NormalizeStreamLine(line []byte, logicalModel string) (ExecutionStreamChunk, error) {
	return d.driver.NormalizeStreamLine(line, logicalModel)
}

func newG7FakeHTTPUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
			http.NotFound(response, request)
			return
		}
		expectedAuthorization := "Bearer " + "g7-internal-test-token"
		if request.Header.Get("Authorization") != expectedAuthorization {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		var requestBody struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if requestBody.Stream {
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: {\"id\":\"g7-stream\",\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n"+
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"total_tokens\":150}}\n\n"+
				"data: [DONE]\n\n")
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"id":"g7-fake","choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`)
	}))
}

func TestG7GatewayAddedOverhead(t *testing.T) {
	if testingEnvironment("G7_PERFORMANCE_TEST") != "YES" {
		t.Skip("G7_PERFORMANCE_TEST 未显式设置为 YES，跳过高并发时延门禁")
	}
	for _, testCase := range []struct {
		name   string
		stream bool
		limit  time.Duration
	}{
		{name: "JSON", limit: 20 * time.Millisecond},
		{name: "SSE", stream: true, limit: 30 * time.Millisecond},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := newG7FakeHTTPUpstream()
			defer server.Close()
			driver := newG7MeasuredBifrostDriver(server)
			const totalRequests = 1000
			durations := make([]time.Duration, totalRequests)
			var succeeded atomic.Int64
			for wave := 0; wave < 10; wave++ {
				errorsFound := make(chan error, 100)
				var wait sync.WaitGroup
				for index := 0; index < 100; index++ {
					position := wave*100 + index
					wait.Add(1)
					go func() {
						defer wait.Done()
						requestID := fmt.Sprintf("g7-perf-%s-%04d", strings.ToLower(testCase.name), position)
						// 每个请求使用独立内存仓储，排除测试替身的全局互斥等待；数据库完整性由同脚本的 MySQL 千请求门禁单独验证。
						store := newMemoryOrchestratorStore()
						orchestrator := newTestOrchestrator(store).WithMetrics(NewAIGatewayMetrics(nil))
						orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
						sink := &g7FirstWriteSink{}
						prepareStartedAt := time.Now()
						prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{
							RequestID: requestID, UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: testCase.stream,
							Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": testCase.stream},
						})
						prepareDuration := time.Since(prepareStartedAt)
						executeStartedAt := time.Now()
						if err == nil {
							err = orchestrator.Execute(context.Background(), prepared.RequestID, sink)
						}
						if err != nil {
							errorsFound <- fmt.Errorf("%s 失败: %w", requestID, err)
							return
						}
						upstreamStartedAt, upstreamStartedObserved := driver.started(requestID)
						upstreamFirstByteAt, upstreamObserved := driver.firstByte(requestID)
						clientFirstWriteAt, clientObserved := sink.firstWrite()
						if !upstreamStartedObserved || !upstreamObserved || !clientObserved {
							errorsFound <- fmt.Errorf("%s 缺少性能时间戳: started=%t upstream_first_byte=%t client_first_write=%t", requestID, upstreamStartedObserved, upstreamObserved, clientObserved)
							return
						}
						// JSON 门禁直接累加 Prepare、Execute 到调用上游前、上游首字节到客户端写出三个本地阶段，避免并发下用大区间相减引入调度误差。
						overhead := prepareDuration + upstreamStartedAt.Sub(executeStartedAt) + clientFirstWriteAt.Sub(upstreamFirstByteAt)
						if testCase.stream {
							// SSE 门禁只计算上游首字节到首个公开数据帧写入之间的网关附加开销，不混入整条流或结算耗时。
							overhead = clientFirstWriteAt.Sub(upstreamFirstByteAt)
						}
						if overhead < 0 {
							overhead = 0
						}
						durations[position] = overhead
						succeeded.Add(1)
					}()
				}
				wait.Wait()
				close(errorsFound)
				for testErr := range errorsFound {
					t.Error(testErr)
				}
			}
			if succeeded.Load() < 990 {
				t.Fatalf("Fake HTTP 上游成功率必须不低于 99%%: succeeded=%d total=%d", succeeded.Load(), totalRequests)
			}
			sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
			p95 := durations[(totalRequests*95+99)/100-1]
			metricName := "gateway_added_overhead_p95"
			if testCase.stream {
				metricName = "gateway_first_byte_overhead_p95"
			}
			t.Logf("G7_PERFORMANCE request_type=%s total=%d concurrency=100 success=%d %s=%s limit=%s", strings.ToLower(testCase.name), totalRequests, succeeded.Load(), metricName, p95, testCase.limit)
			if p95 > testCase.limit {
				t.Fatalf("%s 网关附加开销 P95 超标: p95=%s limit=%s", testCase.name, p95, testCase.limit)
			}
		})
	}
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
	if err := validateG7IsolatedDSN(dsn, testingEnvironment("G7_ISOLATED_DATABASE")); err != nil {
		t.Fatalf("拒绝在未证明隔离的 MySQL 执行 G7 写入验收: %v", err)
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
	fakeUpstream := newG7FakeHTTPUpstream()
	t.Cleanup(fakeUpstream.Close)
	fakeDriver := newG7MeasuredBifrostDriver(fakeUpstream)

	t.Run("一千请求按一百并发完整结算不超扣且不丢账", func(t *testing.T) {
		var succeeded atomic.Int64
		for wave := 0; wave < 10; wave++ {
			errorsFound := make(chan error, 100)
			var wait sync.WaitGroup
			for index := 0; index < 100; index++ {
				wait.Add(1)
				go func(current, currentWave int) {
					defer wait.Done()
					userID := g7FirstLoadUserID + uint64(current)
					orchestrator := newG7Orchestrator(g2, billing, metrics, fakeDriver, userID)
					requestID := fmt.Sprintf("g7-load-%04d", currentWave*100+current)
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
				}(index, wave)
			}
			wait.Wait()
			close(errorsFound)
			for testErr := range errorsFound {
				t.Error(testErr)
			}
		}
		if succeeded.Load() < 990 {
			t.Fatalf("Fake HTTP 上游成功率必须不低于 99%%: succeeded=%d total=1000", succeeded.Load())
		}
		assertG7Count(t, db, "ai_requests", "request_id LIKE 'g7-load-%' AND billing_status = 'settled'", 1000)
		assertG7Count(t, db, "wallet_holds", "idempotency_key LIKE 'g7-load-%:ai-hold' AND status = 'settled'", 1000)
		assertG7Count(t, db, "wallet_transactions", "user_id BETWEEN ? AND ? AND type = 'consume'", g7FirstLoadUserID, g7FirstLoadUserID+99, 1000)
	})

	t.Run("一百路同幂等键竞争只执行和结算一次", func(t *testing.T) {
		orchestrator := newG7Orchestrator(g2, billing, metrics, fakeDriver, g7IdempotencyUser)
		var created atomic.Int64
		var existing atomic.Int64
		var winner sync.Once
		winnerID := ""
		errorsFound := make(chan error, 100)
		var wait sync.WaitGroup
		for index := 0; index < 100; index++ {
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
		if created.Load() != 1 || existing.Load() != 99 || winnerID == "" {
			t.Fatalf("幂等竞争结果不正确: created=%d existing=%d winner=%q", created.Load(), existing.Load(), winnerID)
		}
		if executeErr := orchestrator.Execute(ctx, winnerID, &memorySink{}); executeErr != nil {
			t.Fatal(executeErr)
		}
		assertG7Count(t, db, "ai_requests", "idempotency_key = ?", "g7-idempotency-key", 1)
		assertG7Count(t, db, "wallet_holds", "idempotency_key = ?", winnerID+":ai-hold", 1)
		assertG7Count(t, db, "wallet_transactions", "user_id = ? AND type = 'consume'", g7IdempotencyUser, 1)
	})

	t.Run("只有本人请求中的真实有效平台密钥进入P0数据源", func(t *testing.T) {
		const hmacSecret = "g7-isolated-api-key-hmac-secret"
		// 使用真实签发路径可能产生的 Base64URL 特殊字符，防止测试只覆盖字母数字而掩盖漏匹配。
		const plaintext = "sk-molin-G7Leak_Validation-Key0123456789"
		updateResult := db.Model(&authmodel.APIKey{}).Where("id = ? AND user_id = ? AND status = 'active'", g7FirstLoadUserID, g7FirstLoadUserID).
			Updates(map[string]any{"key_prefix": "sk-molin-G7Le", "key_hash": pkgcrypto.HMAC256(plaintext, hmacSecret)})
		if updateResult.Error != nil {
			t.Fatal(updateResult.Error)
		}
		if updateResult.RowsAffected != 1 {
			t.Fatalf("更新 G7 隔离密钥夹具失败：期望影响 1 行，实际影响 %d 行", updateResult.RowsAffected)
		}
		userService := NewG6UserService(repository.NewG6UserRepository(db), nil).WithAPIKeyHMACSecret(hmacSecret)
		reason := "账单异常，请核查凭据 " + plaintext + " 是否发生泄漏"
		if _, err := userService.CreateDispute(ctx, g7FirstLoadUserID+1, "g7-load-0000", reason); !errors.Is(err, ErrUserRequestNotFound) {
			t.Fatalf("非请求所有人不得触发密钥发现: %v", err)
		}
		if _, err := userService.CreateDispute(ctx, g7FirstLoadUserID, "g7-load-0000", "账单异常，api_key=fabricatedvalue，请协助核查"); !errors.Is(err, ErrDisputeContainsUnverifiedSecret) {
			t.Fatalf("伪造密钥文本必须拒绝但不得升级 P0: %v", err)
		}
		expiredAt := time.Now().Add(-time.Hour)
		expireResult := db.Model(&authmodel.APIKey{}).Where("id = ? AND user_id = ?", g7FirstLoadUserID, g7FirstLoadUserID).Update("expires_at", expiredAt)
		if expireResult.Error != nil || expireResult.RowsAffected != 1 {
			t.Fatalf("设置 G7 密钥过期夹具失败: rows=%d err=%v", expireResult.RowsAffected, expireResult.Error)
		}
		if _, err := userService.CreateDispute(ctx, g7FirstLoadUserID, "g7-load-0000", reason); !errors.Is(err, ErrDisputeContainsUnverifiedSecret) {
			t.Fatalf("已过期但状态仍为 active 的 SK 不得升级 P0: %v", err)
		}
		restoreResult := db.Model(&authmodel.APIKey{}).Where("id = ? AND user_id = ?", g7FirstLoadUserID, g7FirstLoadUserID).Update("expires_at", nil)
		if restoreResult.Error != nil || restoreResult.RowsAffected != 1 {
			t.Fatalf("恢复 G7 密钥有效期夹具失败: rows=%d err=%v", restoreResult.RowsAffected, restoreResult.Error)
		}
		_, err := userService.CreateDispute(ctx, g7FirstLoadUserID, "g7-load-0000", reason)
		var confirmedLeak *ConfirmedCredentialLeakError
		if !errors.As(err, &confirmedLeak) || confirmedLeak.APIKeyID != g7FirstLoadUserID {
			t.Fatalf("本人请求中的真实有效 SK 必须返回确认泄漏事实: %+v %v", confirmedLeak, err)
		}
	})

	t.Run("流式客户端断连后继续读取可信 Usage 并结算", func(t *testing.T) {
		orchestrator := newG7Orchestrator(g2, billing, metrics, fakeDriver, g7StreamUser)
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

	t.Run("Fake HTTP 上游真实停止释放预占且恢复后可安全重试", func(t *testing.T) {
		stoppedServer := newG7FakeHTTPUpstream()
		stoppedDriver := newG7MeasuredBifrostDriver(stoppedServer)
		stopped := newG7Orchestrator(g2, billing, metrics, stoppedDriver, g7ChaosUser)
		first, prepareErr := stopped.Prepare(ctx, g7PrepareCommand("g7-chaos-stopped", "g7-chaos-idempotency", false, g7ChaosUser))
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		stoppedServer.Close()
		if executeErr := stopped.Execute(ctx, first.RequestID, &memorySink{}); !errors.Is(executeErr, ErrUpstream) {
			t.Fatalf("Fake 上游停止应返回统一上游错误: %v", executeErr)
		}
		assertRequestAndHoldStatus(t, db, first.RequestID, model.AIBillingReleased, "released")

		recoveredServer := newG7FakeHTTPUpstream()
		defer recoveredServer.Close()
		recovered := newG7Orchestrator(g2, billing, metrics, newG7MeasuredBifrostDriver(recoveredServer), g7ChaosUser)
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
		drained := false
		for round := 0; round < 100; round++ {
			published, publishErr := worker.RunOnce(ctx, 100)
			if publishErr != nil {
				t.Fatal(publishErr)
			}
			if published == 0 {
				drained = true
				break
			}
		}
		if !drained {
			t.Fatal("Outbox 在 100 轮内仍未排空，拒绝继续零差额核对")
		}
		for _, releaseCase := range []struct {
			name        string
			errorCode   string
			addRawUsage bool
		}{
			{name: "人工核定零成本释放", errorCode: "manual_reconciled"},
			{name: "输出审核免单释放", errorCode: "output_moderation_blocked", addRawUsage: true},
		} {
			t.Run(releaseCase.name+"不得误报P0或Usage缺失", func(t *testing.T) {
				positiveTx := db.Begin()
				if positiveTx.Error != nil {
					t.Fatal(positiveTx.Error)
				}
				defer func() {
					if rollbackErr := positiveTx.Rollback().Error; rollbackErr != nil {
						t.Errorf("回滚合法 release 夹具失败: %v", rollbackErr)
					}
				}()
				updated := positiveTx.Exec("UPDATE ai_requests SET execution_status = 'succeeded', error_code = ?, updated_at = ? WHERE request_id = ?", releaseCase.errorCode, time.Now().Add(-10*time.Minute), "g7-chaos-stopped")
				if updated.Error != nil || updated.RowsAffected != 1 {
					t.Fatalf("准备合法 release 夹具失败: rows=%d err=%v", updated.RowsAffected, updated.Error)
				}
				if releaseCase.addRawUsage {
					inserted := positiveTx.Exec(`INSERT INTO ai_usage_items (request_id,meter_type,source,sequence_no,quantity,unit_price,amount)
VALUES (?, 'input_tokens', 'provider', 0, 2, NULL, NULL),
	   (?, 'output_tokens', 'provider', 0, 1, NULL, NULL),
	   (?, 'total_tokens', 'provider', 0, 3, NULL, NULL),
	   (?, 'input_tokens', 'provider_cost', 0, 2, 0, 0),
	   (?, 'output_tokens', 'provider_cost', 0, 1, 0, 0),
	   (?, 'cached_tokens', 'provider_cost', 0, 0, 0, 0),
	   (?, 'reasoning_tokens', 'provider_cost', 0, 0, 0, 0)`, "g7-chaos-stopped", "g7-chaos-stopped", "g7-chaos-stopped",
						"g7-chaos-stopped", "g7-chaos-stopped", "g7-chaos-stopped", "g7-chaos-stopped")
					if inserted.Error != nil || inserted.RowsAffected != 7 {
						t.Fatalf("准备审核免单原始 Usage 失败: rows=%d err=%v", inserted.RowsAffected, inserted.Error)
					}
				}
				snapshot, collectErr := NewAIGatewayDBGaugeCollector(positiveTx).CollectAIGatewayGauges(ctx, time.Now())
				if collectErr != nil {
					t.Fatal(collectErr)
				}
				if snapshot.BillingAnomalies["unbilled_execution"] != 0 || snapshot.BillingAnomalies["missing_usage"] != 0 {
					t.Fatalf("合法 release 不得误报 P0 或 Usage 缺失: %+v", snapshot.BillingAnomalies)
				}
			})
		}
		t.Run("真实MySQL损坏夹具必须被聚合和明细同时识别", func(t *testing.T) {
			// 所有损坏都限制在事务内并在子测试结束时回滚，既验证真实 MySQL 三值逻辑，也不污染最终零差额验收。
			negativeTx := db.Begin()
			if negativeTx.Error != nil {
				t.Fatal(negativeTx.Error)
			}
			defer func() {
				if rollbackErr := negativeTx.Rollback().Error; rollbackErr != nil {
					t.Errorf("回滚 G7 损坏夹具失败: %v", rollbackErr)
				}
			}()

			holdDamage := negativeTx.Exec(`UPDATE wallet_holds AS holds
JOIN ai_request_wallet_links AS links ON links.wallet_hold_id = holds.id
SET holds.settled_amount = NULL WHERE links.request_id = ?`, "g7-load-0000")
			if holdDamage.Error != nil || holdDamage.RowsAffected != 1 {
				t.Fatalf("注入 hold 结算金额缺失失败: rows=%d err=%v", holdDamage.RowsAffected, holdDamage.Error)
			}
			linkDamage := negativeTx.Exec("UPDATE ai_request_wallet_links SET held_amount = held_amount + 0.01000000 WHERE request_id = ?", "g7-load-0001")
			if linkDamage.Error != nil || linkDamage.RowsAffected != 1 {
				t.Fatalf("注入 link 预占差额失败: rows=%d err=%v", linkDamage.RowsAffected, linkDamage.Error)
			}
			freezeDamage := negativeTx.Exec(`UPDATE wallet_holds AS holds
JOIN ai_request_wallet_links AS links ON links.wallet_hold_id = holds.id
SET holds.freeze_txn_id = NULL WHERE links.request_id = ?`, "g7-load-0002")
			if freezeDamage.Error != nil || freezeDamage.RowsAffected != 1 {
				t.Fatalf("注入 freeze 流水关联缺失失败: rows=%d err=%v", freezeDamage.RowsAffected, freezeDamage.Error)
			}
			releaseDamage := negativeTx.Exec("UPDATE ai_request_wallet_links SET release_transaction_id = NULL WHERE request_id = ?", "g7-chaos-stopped")
			if releaseDamage.Error != nil || releaseDamage.RowsAffected != 1 {
				t.Fatalf("注入 released 请求释放流水缺失失败: rows=%d err=%v", releaseDamage.RowsAffected, releaseDamage.Error)
			}
			priceDamage := negativeTx.Exec(`UPDATE ai_requests SET price_snapshot_json = JSON_SET(
				price_snapshot_json, '$.logical_model_code', JSON_OBJECT(), '$.skus', 'scalar-sku') WHERE request_id = ?`, "g7-chaos-recovered")
			if priceDamage.Error != nil || priceDamage.RowsAffected != 1 {
				t.Fatalf("注入伪结构价格快照失败: rows=%d err=%v", priceDamage.RowsAffected, priceDamage.Error)
			}
			versionDamage := negativeTx.Exec(`UPDATE ai_requests SET price_snapshot_json = JSON_SET(
				price_snapshot_json, '$.version_no', CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.version_no')) AS UNSIGNED) + 1)
				WHERE request_id = ?`, "g7-load-0011")
			if versionDamage.Error != nil || versionDamage.RowsAffected != 1 {
				t.Fatalf("注入结构完整但版本号错误的价格快照失败: rows=%d err=%v", versionDamage.RowsAffected, versionDamage.Error)
			}
			skuPriceDamage := negativeTx.Exec(`UPDATE ai_requests SET price_snapshot_json = JSON_SET(
				price_snapshot_json, '$.skus.input_tokens.cost_unit_price', '999.00000000',
				'$.skus.input_tokens.sale_unit_price', '999.00000000') WHERE request_id = ?`, "g7-load-0012")
			if skuPriceDamage.Error != nil || skuPriceDamage.RowsAffected != 1 {
				t.Fatalf("注入结构完整但 SKU 价格错误的快照失败: rows=%d err=%v", skuPriceDamage.RowsAffected, skuPriceDamage.Error)
			}
			usageDamage := negativeTx.Exec(`UPDATE ai_usage_items SET unit_price = NULL, amount = NULL
				WHERE request_id = ? AND sequence_no = 1 AND meter_type = 'cached_tokens'`, "g7-load-0010")
			if usageDamage.Error != nil || usageDamage.RowsAffected != 1 {
				t.Fatalf("注入失败请求销售 Usage 空单价金额失败: rows=%d err=%v", usageDamage.RowsAffected, usageDamage.Error)
			}
			if result := negativeTx.Exec("UPDATE ai_requests SET execution_status = 'failed' WHERE request_id = ?", "g7-load-0010"); result.Error != nil || result.RowsAffected != 1 {
				t.Fatalf("注入失败但已结算请求失败: rows=%d err=%v", result.RowsAffected, result.Error)
			}
			rawSourceDamage := negativeTx.Exec("UPDATE ai_usage_items SET source = 'reconciled' WHERE request_id = ? AND sequence_no = 0", "g7-load-0005")
			if rawSourceDamage.Error != nil || rawSourceDamage.RowsAffected != 3 {
				t.Fatalf("注入 reconciled 冒充原始 Provider Usage 失败: rows=%d err=%v", rawSourceDamage.RowsAffected, rawSourceDamage.Error)
			}
			extraSalesDamage := negativeTx.Exec(`INSERT INTO ai_usage_items (request_id,meter_type,source,sequence_no,quantity,unit_price,amount)
				VALUES (?, 'unexpected_tokens', 'provider', 1, 0, 0, 0)`, "g7-load-0006")
			if extraSalesDamage.Error != nil || extraSalesDamage.RowsAffected != 1 {
				t.Fatalf("注入额外销售 Usage 计量项失败: rows=%d err=%v", extraSalesDamage.RowsAffected, extraSalesDamage.Error)
			}
			quantityDamage := negativeTx.Exec(`UPDATE ai_usage_items SET quantity = quantity + 1
				WHERE request_id = ? AND source = 'provider' AND sequence_no = 1 AND meter_type = 'input_tokens'`, "g7-load-0013")
			if quantityDamage.Error != nil || quantityDamage.RowsAffected != 1 {
				t.Fatalf("注入 raw 与销售 Usage 数量不守恒失败: rows=%d err=%v", quantityDamage.RowsAffected, quantityDamage.Error)
			}
			unitPriceDamage := negativeTx.Exec(`UPDATE ai_usage_items SET unit_price = unit_price + 1
				WHERE request_id = ? AND source = 'provider' AND sequence_no = 1 AND meter_type = 'input_tokens'`, "g7-load-0014")
			if unitPriceDamage.Error != nil || unitPriceDamage.RowsAffected != 1 {
				t.Fatalf("注入销售 Usage 单价与快照不一致失败: rows=%d err=%v", unitPriceDamage.RowsAffected, unitPriceDamage.Error)
			}
			amountDamage := negativeTx.Exec(`UPDATE ai_usage_items SET amount = amount + 0.01000000
				WHERE request_id = ? AND source = 'provider' AND sequence_no = 1 AND meter_type = 'input_tokens'`, "g7-load-0015")
			if amountDamage.Error != nil || amountDamage.RowsAffected != 1 {
				t.Fatalf("注入销售 Usage 重算金额不一致失败: rows=%d err=%v", amountDamage.RowsAffected, amountDamage.Error)
			}
			identityDamage := negativeTx.Exec(`UPDATE wallet_transactions AS transactions
JOIN ai_request_wallet_links AS links ON links.settle_transaction_id = transactions.id
SET transactions.wallet_id = (SELECT wallets.id FROM wallets WHERE wallets.user_id = ? LIMIT 1), transactions.user_id = ?
WHERE links.request_id = ?`, g7FirstLoadUserID+5, g7FirstLoadUserID+5, "g7-load-0004")
			if identityDamage.Error != nil || identityDamage.RowsAffected != 1 {
				t.Fatalf("注入跨钱包同金额流水失败: rows=%d err=%v", identityDamage.RowsAffected, identityDamage.Error)
			}
			walletOwnerDamage := negativeTx.Exec(`UPDATE ai_request_wallet_links AS links
			SET links.wallet_id = (SELECT wallets.id FROM wallets WHERE wallets.user_id = ? LIMIT 1)
			WHERE links.request_id = ?`, g7FirstLoadUserID+6, "g7-load-0016")
			if walletOwnerDamage.Error != nil || walletOwnerDamage.RowsAffected != 1 {
				t.Fatalf("注入 link 指向他人钱包失败: rows=%d err=%v", walletOwnerDamage.RowsAffected, walletOwnerDamage.Error)
			}
			overHeldDamage := negativeTx.Exec("UPDATE ai_requests SET settled_amount = held_amount + 1 WHERE request_id = ?", "g7-load-0017")
			if overHeldDamage.Error != nil || overHeldDamage.RowsAffected != 1 {
				t.Fatalf("注入结算金额超过预占失败: rows=%d err=%v", overHeldDamage.RowsAffected, overHeldDamage.Error)
			}
			prematureDamage := negativeTx.Exec(`UPDATE ai_requests AS requests
			JOIN ai_request_wallet_links AS links ON links.request_id = requests.request_id
			SET requests.billing_status = 'settlement_pending' WHERE requests.request_id = ?`, "g7-load-0008")
			if prematureDamage.Error != nil || prematureDamage.RowsAffected != 1 {
				t.Fatalf("注入待结算请求提前挂接终态流水失败: rows=%d err=%v", prematureDamage.RowsAffected, prematureDamage.Error)
			}
			if result := negativeTx.Exec("UPDATE ai_requests SET billing_status = 'exception', settled_amount = NULL WHERE request_id = ?", "g7-load-0009"); result.Error != nil || result.RowsAffected != 1 {
				t.Fatalf("注入 exception 请求失败: rows=%d err=%v", result.RowsAffected, result.Error)
			}
			missingExceptionLink := negativeTx.Exec("DELETE FROM ai_request_wallet_links WHERE request_id = ?", "g7-load-0009")
			if missingExceptionLink.Error != nil || missingExceptionLink.RowsAffected != 1 {
				t.Fatalf("注入 exception 钱包关联缺失失败: rows=%d err=%v", missingExceptionLink.RowsAffected, missingExceptionLink.Error)
			}

			collector := NewAIGatewayDBGaugeCollector(negativeTx)
			negativeSnapshot, collectErr := collector.CollectAIGatewayGauges(ctx, time.Now())
			if collectErr != nil {
				t.Fatal(collectErr)
			}
			if negativeSnapshot.BillingAnomalies["missing_wallet_transaction"] == 0 {
				t.Fatal("hold.settled_amount=NULL 必须让聚合对账失败")
			}
			if negativeSnapshot.BillingDifferences["request_hold"].IsZero() {
				t.Fatal("request→link→hold 任一段损坏必须产生预占差额")
			}
			if negativeSnapshot.BillingAnomalies["missing_price_snapshot"] == 0 || negativeSnapshot.BillingAnomalies["missing_usage"] == 0 {
				t.Fatalf("空价格快照和销售计费 Usage 缺失都必须让聚合对账失败: %+v", negativeSnapshot.BillingAnomalies)
			}

			issues, issueErr := collector.CollectAIGatewayReconciliationIssues(ctx, time.Now(), 501)
			if issueErr != nil {
				t.Fatal(issueErr)
			}
			missingHoldSettlement := false
			missingFreezeTransaction := false
			missingReleaseTransaction := false
			missingPriceSnapshot := false
			missingBilledUsage := false
			spoofedRawUsage := false
			extraSalesUsage := false
			crossWalletTransaction := false
			prematureSettlement := false
			missingExceptionWalletLink := false
			wrongPriceVersion := false
			wrongSnapshotSKUPrice := false
			usageQuantityMismatch := false
			usageUnitPriceMismatch := false
			usageAmountMismatch := false
			wrongWalletOwner := false
			overHeldSettlement := false
			holdLegIssues := 0
			for _, issue := range issues {
				if issue.RequestID == "g7-load-0000" && issue.IssueCode == "missing_wallet_transaction" {
					missingHoldSettlement = true
				}
				if issue.RequestID == "g7-load-0001" && issue.IssueCode == "request_hold_difference" {
					holdLegIssues++
					if issue.ExpectedValue == issue.ActualValue {
						t.Fatalf("预占差额明细不得输出相同的期望值与实际值: %+v", issue)
					}
				}
				if issue.RequestID == "g7-load-0002" && issue.IssueCode == "missing_wallet_transaction" {
					missingFreezeTransaction = true
				}
				if issue.RequestID == "g7-chaos-stopped" && issue.IssueCode == "missing_wallet_transaction" {
					missingReleaseTransaction = true
				}
				if issue.RequestID == "g7-chaos-recovered" && issue.IssueCode == "missing_price_snapshot" {
					missingPriceSnapshot = true
				}
				if issue.RequestID == "g7-load-0010" && issue.IssueCode == "missing_usage" {
					missingBilledUsage = true
				}
				if issue.RequestID == "g7-load-0005" && issue.IssueCode == "missing_usage" {
					spoofedRawUsage = true
				}
				if issue.RequestID == "g7-load-0006" && issue.IssueCode == "missing_usage" {
					extraSalesUsage = true
				}
				if issue.RequestID == "g7-load-0004" && issue.IssueCode == "missing_wallet_transaction" {
					crossWalletTransaction = true
				}
				if issue.RequestID == "g7-load-0008" && issue.IssueCode == "missing_wallet_transaction" {
					prematureSettlement = true
				}
				if issue.RequestID == "g7-load-0009" && issue.IssueCode == "missing_wallet_transaction" {
					missingExceptionWalletLink = true
				}
				if issue.RequestID == "g7-load-0011" && issue.IssueCode == "missing_price_snapshot" {
					wrongPriceVersion = true
				}
				if issue.RequestID == "g7-load-0012" && issue.IssueCode == "missing_price_snapshot" {
					wrongSnapshotSKUPrice = true
				}
				if issue.RequestID == "g7-load-0013" && issue.IssueCode == "missing_usage" {
					usageQuantityMismatch = true
				}
				if issue.RequestID == "g7-load-0014" && issue.IssueCode == "missing_usage" {
					usageUnitPriceMismatch = true
				}
				if issue.RequestID == "g7-load-0015" && issue.IssueCode == "missing_usage" {
					usageAmountMismatch = true
				}
				if issue.RequestID == "g7-load-0016" && issue.IssueCode == "missing_wallet_transaction" {
					wrongWalletOwner = true
				}
				if issue.RequestID == "g7-load-0017" && issue.IssueCode == "missing_wallet_transaction" {
					overHeldSettlement = true
				}
			}
			if !missingHoldSettlement || !missingFreezeTransaction || !missingReleaseTransaction || !missingPriceSnapshot ||
				!missingBilledUsage || !spoofedRawUsage || !extraSalesUsage || !crossWalletTransaction || !prematureSettlement ||
				!missingExceptionWalletLink || !wrongPriceVersion || !wrongSnapshotSKUPrice || !usageQuantityMismatch ||
				!usageUnitPriceMismatch || !usageAmountMismatch || !wrongWalletOwner || !overHeldSettlement || holdLegIssues != 3 {
				t.Fatalf("损坏夹具必须输出价格版本/SKU、Usage 数量/单价/重算金额、钱包 owner/金额域及既有全链证据: missing_hold=%t missing_freeze=%t missing_release=%t missing_price=%t missing_usage=%t spoofed_raw=%t extra_sales=%t cross_wallet=%t premature=%t missing_exception_link=%t wrong_version=%t wrong_sku=%t usage_quantity=%t usage_unit_price=%t usage_amount=%t wallet_owner=%t over_held=%t hold_legs=%d issues=%+v", missingHoldSettlement, missingFreezeTransaction, missingReleaseTransaction, missingPriceSnapshot, missingBilledUsage, spoofedRawUsage, extraSalesUsage, crossWalletTransaction, prematureSettlement, missingExceptionWalletLink, wrongPriceVersion, wrongSnapshotSKUPrice, usageQuantityMismatch, usageUnitPriceMismatch, usageAmountMismatch, wrongWalletOwner, overHeldSettlement, holdLegIssues, issues)
			}
		})
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

func validateG7IsolatedDSN(dsn, expectedDatabase string) error {
	config, err := mysqldsn.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("DSN 格式错误: %w", err)
	}
	if expectedDatabase == "" || config.DBName != expectedDatabase || !strings.HasPrefix(expectedDatabase, "molin_g7_reliability_") {
		return fmt.Errorf("数据库名必须与随机隔离库一致")
	}
	host, _, err := net.SplitHostPort(config.Addr)
	if err != nil {
		return fmt.Errorf("MySQL 地址必须显式包含主机和端口: %w", err)
	}
	allowedHosts := map[string]bool{"mysql": true, "localhost": true, "127.0.0.1": true, "::1": true}
	if !allowedHosts[strings.ToLower(host)] {
		return fmt.Errorf("MySQL 主机 %q 不属于隔离容器或本机回环地址", host)
	}
	return nil
}

func g7Uint64Pointer(value uint64) *uint64 { return &value }
