package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fakeAIGatewayGaugeCollector struct {
	snapshot AIGatewayGaugeSnapshot
	err      error
}

func TestAIGatewayMetricsCapsModelCardinalityAndSupportsConcurrentScrapes(t *testing.T) {
	metrics := NewAIGatewayMetrics(nil)
	for index := 0; index < 100; index++ {
		metrics.AllowLogicalModel(fmt.Sprintf("molin/g7-model-%03d", index))
	}

	// 记录和抓取会在真实 HTTP 请求与 Prometheus 抓取之间并发发生，必须由同一把读写锁保护。
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(current int) {
			defer wait.Done()
			modelCode := fmt.Sprintf("molin/g7-model-%03d", current)
			metrics.RecordRequest(modelCode, "json", "success", time.Millisecond)
			metrics.RecordUpstream(modelCode, "bifrost", "success")
			if _, err := metrics.AIGatewayPrometheus(context.Background()); err != nil {
				t.Errorf("并发抓取指标失败: %v", err)
			}
		}(index)
	}
	wait.Wait()

	text, err := metrics.AIGatewayPrometheus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 模型硬上限为 32，再加一个 other 兜底；攻击者不能通过任意模型名制造无限时序。
	if count := strings.Count(text, "\nmolin_ai_gateway_requests_total{"); count != maxObservableModels+1 {
		t.Fatalf("模型标签时序数量未受控: got=%d want=%d", count, maxObservableModels+1)
	}
	if !strings.Contains(text, `logical_model_code="other"`) || strings.Contains(text, "molin/g7-model-099") {
		t.Fatalf("超出硬上限的模型必须收敛到 other")
	}
}

func (f fakeAIGatewayGaugeCollector) CollectAIGatewayGauges(context.Context, time.Time) (AIGatewayGaugeSnapshot, error) {
	return f.snapshot, f.err
}

func TestAIGatewayMetricsExportsClosedSeriesAndReadOnlyGauges(t *testing.T) {
	collector := fakeAIGatewayGaugeCollector{snapshot: AIGatewayGaugeSnapshot{
		BillingRequests:     map[string]uint64{"held": 2, "settled": 7},
		BillingOldestAge:    map[string]uint64{"held": 75},
		UnreleasedHolds:     AIGatewayAmountGauge{Count: 2, Amount: decimal.RequireFromString("0.12000000"), OldestAgeSeconds: 360},
		OutboxBacklog:       map[string]AIGatewayBacklogGauge{"pending": {Count: 3, OldestAgeSeconds: 45}},
		CompensationBacklog: map[string]AIGatewayBacklogGauge{"retry": {Count: 1, OldestAgeSeconds: 90}},
		BillingDifferences:  map[string]decimal.Decimal{"request_usage": decimal.Zero, "request_wallet": decimal.Zero},
		BillingAnomalies:    map[string]uint64{"duplicate_settlement": 0, "unbilled_execution": 0, "missing_wallet_transaction": 0},
	}}
	metrics := NewAIGatewayMetrics(collector)
	metrics.AllowLogicalModel("molin/qwen-turbo")
	metrics.RecordRequest("molin/qwen-turbo", "json", "success", 18*time.Millisecond)
	metrics.RecordRequest("恶意\"model", "json", "unexpected", 5*time.Millisecond)
	metrics.RecordTTFT("molin/qwen-turbo", "bifrost", 24*time.Millisecond)
	metrics.RecordStreamInterruption("molin/qwen-turbo", "bifrost")
	metrics.RecordUpstream("molin/qwen-turbo", "bifrost", "rate_limited")
	metrics.RecordUpstreamRetry("molin/qwen-turbo", "bifrost")
	metrics.RecordUsageMissing("molin/qwen-turbo", "stream")
	metrics.RecordBillingTransition("settled")
	metrics.RecordRejection("budget_limit")
	metrics.RecordConcurrencyLease("project", 1)
	metrics.RecordConcurrencyRejection("project")
	metrics.RecordHeartbeatFailure()
	metrics.RecordGhostLease(2)

	text, err := metrics.AIGatewayPrometheus(context.Background())
	if err != nil {
		t.Fatalf("导出 AI 网关指标失败: %v", err)
	}
	checks := []string{
		`molin_ai_gateway_requests_total{logical_model_code="molin/qwen-turbo",request_type="json",outcome="success"} 1`,
		`molin_ai_gateway_requests_total{logical_model_code="other",request_type="json",outcome="other"} 1`,
		`molin_ai_gateway_request_duration_seconds_bucket{request_type="json",le="0.02"} 2`,
		`molin_ai_gateway_ttft_seconds_bucket{logical_model_code="molin/qwen-turbo",driver="bifrost",le="0.03"} 1`,
		`molin_ai_gateway_stream_interruptions_total{logical_model_code="molin/qwen-turbo",driver="bifrost"} 1`,
		`molin_ai_gateway_unreleased_holds_oldest_age_seconds 360`,
		`molin_ai_gateway_upstream_requests_total{logical_model_code="molin/qwen-turbo",driver="bifrost",outcome="rate_limited"} 1`,
		`molin_ai_gateway_billing_requests{billing_state="settled"} 7`,
		`molin_ai_gateway_billing_oldest_age_seconds{billing_state="held"} 75`,
		`molin_ai_gateway_unreleased_holds_amount_cny 0.12000000`,
		`molin_ai_gateway_billing_difference_cny{kind="request_wallet"} 0.00000000`,
		`molin_ai_gateway_concurrency_leases{scope="project"} 1`,
		`molin_ai_gateway_ghost_leases_total 2`,
	}
	for _, expected := range checks {
		if !strings.Contains(text, expected) {
			t.Fatalf("缺少指标序列 %q\n%s", expected, text)
		}
	}
	for _, forbidden := range []string{"恶意", "request_id=", "user_id=", "project_id=", "api_key=", "prompt=", "secret="} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("指标输出出现高基数或敏感内容 %q", forbidden)
		}
	}
}

func TestAIGatewayDBGaugeCollectorReadsFinancialFactsWithoutWrites(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("创建只读指标数据库桩失败: %v", err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("创建 GORM 连接失败: %v", err)
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery("SELECT billing_status, COUNT\\(\\*\\) AS count,.*FROM ai_requests GROUP BY billing_status").
		WithArgs(now).
		WillReturnRows(sqlmock.NewRows([]string{"billing_status", "count", "oldest_age_seconds"}).AddRow("held", 2, 75).AddRow("settled", 7, 10))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) AS count,.*FROM wallet_holds AS holds.*status = \\?").
		WithArgs(now, "holding").
		WillReturnRows(sqlmock.NewRows([]string{"count", "amount", "oldest_age_seconds"}).AddRow(2, "0.12000000", 320))
	mock.ExpectQuery("SELECT status, COUNT\\(\\*\\) AS count,.*FROM ai_outbox_events.*status IN \\(").
		WithArgs(now, "pending", "publishing", "dead").
		WillReturnRows(sqlmock.NewRows([]string{"status", "count", "oldest_age_seconds"}).AddRow("pending", 3, 45))
	mock.ExpectQuery("SELECT status, COUNT\\(\\*\\) AS count,.*FROM ai_compensation_tasks.*status IN \\(").
		WithArgs(now, "pending", "retry", "dead", "manual_review").
		WillReturnRows(sqlmock.NewRows([]string{"status", "count", "oldest_age_seconds"}).AddRow("retry", 1, 90))
	mock.ExpectQuery("WITH selected_usage AS.*request_settled").
		WillReturnRows(sqlmock.NewRows([]string{"request_settled", "model_usage", "wallet_consumed"}).AddRow("1.25000000", "1.25000000", "1.25000000"))
	mock.ExpectQuery("SELECT COUNT\\(DISTINCT target_id\\) FROM audit_logs.*target_type = \\?.*created_at >= \\?").
		WithArgs("token_gateway", "secret_leak_detected", "api_key", now.Add(-5*time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("WITH selected_usage AS.*duplicate_settlement").
		WithArgs(now.Add(-5*time.Minute), now.Add(-5*time.Minute)).
		WillReturnRows(sqlmock.NewRows([]string{
			"request_usage_difference", "request_hold_difference", "request_wallet_difference", "duplicate_settlement", "unbilled_execution", "missing_price_snapshot", "missing_wallet_transaction", "missing_usage", "completed_pending", "billing_exception",
		}).AddRow("0.00000000", "0.00000000", "0.00000000", 0, 0, 0, 0, 0, 0, 0))

	snapshot, err := NewAIGatewayDBGaugeCollector(db).CollectAIGatewayGauges(context.Background(), now)
	if err != nil {
		t.Fatalf("采集数据库指标失败: %v", err)
	}
	if snapshot.BillingRequests["settled"] != 7 || snapshot.BillingOldestAge["held"] != 75 || snapshot.UnreleasedHolds.Count != 2 ||
		!snapshot.UnreleasedHolds.Amount.Equal(decimal.RequireFromString("0.12000000")) || snapshot.UnreleasedHolds.OldestAgeSeconds != 320 {
		t.Fatalf("账务状态或预占快照错误: %+v", snapshot)
	}
	if snapshot.OutboxBacklog["pending"].OldestAgeSeconds != 45 || snapshot.CompensationBacklog["retry"].OldestAgeSeconds != 90 {
		t.Fatalf("任务积压快照错误: %+v", snapshot)
	}
	if !snapshot.BillingDifferences["request_usage"].IsZero() || !snapshot.BillingDifferences["request_wallet"].IsZero() {
		t.Fatalf("零差额账单被错误识别: %+v", snapshot.BillingDifferences)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("Gauge 采集器必须只执行约定的 SELECT 聚合: %v", err)
	}
}

func TestReconciliationIssuesExposeHoldSettlementLinkMismatch(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("创建只读对账数据库桩失败: %v", err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("创建 GORM 连接失败: %v", err)
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-unbilledExecutionGracePeriod)
	mock.ExpectQuery("WITH selected_usage AS.*hold_settle_transaction_id").
		WithArgs(cutoff, cutoff, cutoff, 10).
		WillReturnRows(sqlmock.NewRows([]string{"request_id", "issue_code", "billing_status", "expected_value", "actual_value"}).
			AddRow("req-hold-link-mismatch", "missing_wallet_transaction", "settled", "request=link=hold=transaction", "inconsistent_or_missing"))

	issues, err := NewAIGatewayDBGaugeCollector(db).CollectAIGatewayReconciliationIssues(context.Background(), now, 10)
	if err != nil {
		t.Fatalf("读取 request_id 级对账证据失败: %v", err)
	}
	if len(issues) != 1 || issues[0].RequestID != "req-hold-link-mismatch" || issues[0].IssueCode != "missing_wallet_transaction" {
		t.Fatalf("hold 与 link 结算流水不一致必须返回 request_id 证据: %+v", issues)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("对账明细 SQL 未覆盖 hold 结算流水一致性: %v", err)
	}
}

func TestReconciliationSQLUsesNullSafeSettlementAndCompleteHoldEvidence(t *testing.T) {
	for name, query := range map[string]string{"聚合": aiGatewayReconciliationSQL, "明细": aiGatewayReconciliationIssuesSQL} {
		if !strings.Contains(query, "NOT (links.settled_amount <=> requests.settled_amount)") &&
			!strings.Contains(query, "NOT (link_settled_amount <=> settled_amount)") {
			t.Fatalf("%s SQL 必须用 NULL-safe 比较 link 结算金额", name)
		}
		if !strings.Contains(query, "holds.settled_amount IS NULL") &&
			!strings.Contains(query, "hold_settled_amount IS NULL") {
			t.Fatalf("%s SQL 必须把 wallet_holds.settled_amount 缺失视为结算链异常", name)
		}
	}
	if !strings.Contains(aiGatewayReconciliationIssuesSQL, "CAST(COALESCE(held_amount, 0) AS CHAR), CAST(COALESCE(link_held_amount, 0) AS CHAR)") ||
		!strings.Contains(aiGatewayReconciliationIssuesSQL, "CAST(COALESCE(link_held_amount, 0) AS CHAR), CAST(COALESCE(hold_amount, 0) AS CHAR)") ||
		!strings.Contains(aiGatewayReconciliationIssuesSQL, "CAST(COALESCE(link_held_amount, 0) AS CHAR), CAST(COALESCE(release_transaction_amount, 0) AS CHAR)") {
		t.Fatal("预占差额明细必须分别展示 request→link、link→hold 与 link→release 三段实际值")
	}
	for name, query := range map[string]string{"聚合": aiGatewayReconciliationSQL, "明细": aiGatewayReconciliationIssuesSQL} {
		if !strings.Contains(query, "meter_type IN ('input_tokens','output_tokens','total_tokens')) <> 3") ||
			!strings.Contains(query, "raw_usage.source = 'provider'") || !strings.Contains(query, "raw_usage.source NOT IN ('provider','provider_cost')") ||
			!strings.Contains(query, "meter_count, 0) <> 4") || !strings.Contains(query, "row_count, 0) <> 4") ||
			!strings.Contains(query, "incomplete_count, 0) <> 0") || !strings.Contains(query, "selected_usage.input_quantity + selected_usage.cached_quantity") &&
			!strings.Contains(query, "selected_input_quantity + selected_cached_quantity") {
			t.Fatalf("%s SQL 必须分别验证三项原始 Usage 和四项销售计费 Usage", name)
		}
		if !strings.Contains(query, "'released', 'exception'") || !strings.Contains(query, "JSON_TYPE(JSON_EXTRACT") ||
			!strings.Contains(query, "price_versions") || !strings.Contains(query, "logical_model_code") ||
			!strings.Contains(query, "JSON_LENGTH(JSON_EXTRACT") || !strings.Contains(query, "<> 4") ||
			!strings.Contains(query, "price_versions.version_no") && !strings.Contains(query, "price_version_fact_version_no") ||
			!strings.Contains(query, "ai_price_skus AS snapshot_sku") ||
			!strings.Contains(query, "$.skus.reasoning_tokens.sale_unit_price") || !strings.Contains(query, "$.skus.reasoning_tokens.scale") {
			t.Fatalf("%s SQL 必须验证所有财务状态的完整价格快照结构", name)
		}
		if !strings.Contains(query, "output_moderation_blocked") || !strings.Contains(query, "manual_reconciled") {
			t.Fatalf("%s SQL 不得把合法输出审核免单或人工核定 release 误报为 P0", name)
		}
		if !strings.Contains(query, "'held', 'settlement_pending', 'exception'") ||
			!strings.Contains(query, "settle_transaction_id IS NOT NULL") || !strings.Contains(query, "release_transaction_id IS NOT NULL") {
			t.Fatalf("%s SQL 必须拒绝在途或异常状态提前挂接结算/释放流水", name)
		}
	}
}
