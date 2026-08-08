package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/shopspring/decimal"
)

const maxObservableModels = 32

var (
	requestDurationBuckets = []float64{0.005, 0.01, 0.02, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
	ttftBuckets            = []float64{0.01, 0.03, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
	requestTypes           = map[string]struct{}{"json": {}, "stream": {}}
	requestOutcomes        = map[string]struct{}{"success": {}, "failure": {}, "rejected": {}}
	metricDrivers          = map[string]struct{}{"native": {}, "bifrost": {}, "fake": {}}
	upstreamOutcomes       = map[string]struct{}{"success": {}, "timeout": {}, "rate_limited": {}, "client_error": {}, "server_error": {}, "malformed": {}, "unknown": {}}
	billingStates          = []string{"unquoted", "held", "settlement_pending", "settled", "released", "exception"}
	rejectionReasons       = map[string]struct{}{
		"content_policy": {}, "classifier_timeout": {}, "fail_closed": {}, "model_disabled": {},
		"budget_limit": {}, "permission_denied": {}, "api_key_frozen": {}, "concurrency_limit": {},
		"rpm_limit": {}, "tpm_limit": {},
	}
	metricScopes         = []string{"user", "project", "api_key", "model"}
	outboxStatuses       = []string{"pending", "publishing", "dead"}
	compensationStatuses = []string{"pending", "retry", "dead", "manual_review"}
	differenceKinds      = []string{"request_usage", "request_hold", "request_wallet"}
	billingAnomalyKinds  = []string{"duplicate_settlement", "unbilled_execution", "missing_price_snapshot", "missing_wallet_transaction"}
)

// AIGatewayAmountGauge 表示一个只读事实集合的数量和人民币金额。
type AIGatewayAmountGauge struct {
	Count  uint64
	Amount decimal.Decimal
}

// AIGatewayBacklogGauge 表示积压数量与最老任务年龄，单位为秒。
type AIGatewayBacklogGauge struct {
	Count            uint64
	OldestAgeSeconds uint64
}

// AIGatewayGaugeSnapshot 是每次抓取时从 MySQL 只读聚合得到的财务和任务事实。
type AIGatewayGaugeSnapshot struct {
	BillingRequests     map[string]uint64
	BillingOldestAge    map[string]uint64
	UnreleasedHolds     AIGatewayAmountGauge
	OutboxBacklog       map[string]AIGatewayBacklogGauge
	CompensationBacklog map[string]AIGatewayBacklogGauge
	BillingDifferences  map[string]decimal.Decimal
	BillingAnomalies    map[string]uint64
}

// AIGatewayGaugeCollector 只允许执行 SELECT 聚合，不拥有任何修复、补偿或钱包写入能力。
type AIGatewayGaugeCollector interface {
	CollectAIGatewayGauges(ctx context.Context, now time.Time) (AIGatewayGaugeSnapshot, error)
}

type requestMetricKey struct {
	Model       string
	RequestType string
	Outcome     string
}

type modelDriverKey struct {
	Model  string
	Driver string
}

type upstreamMetricKey struct {
	Model   string
	Driver  string
	Outcome string
}

type histogramValue struct {
	Buckets []uint64
	Count   uint64
	Sum     float64
}

// AIGatewayMetrics 保存进程内单调事件计数，并在抓取时合并数据库只读 Gauge。
// 所有标签都先经过封闭枚举或受控模型注册，禁止使用 request_id、用户、Project、SK 和正文。
type AIGatewayMetrics struct {
	mu sync.RWMutex

	collector AIGatewayGaugeCollector
	models    map[string]struct{}

	requests              map[requestMetricKey]uint64
	requestDurations      map[string]*histogramValue
	ttft                  map[modelDriverKey]*histogramValue
	streamInterruptions   map[modelDriverKey]uint64
	upstreamRequests      map[upstreamMetricKey]uint64
	upstreamRetries       map[modelDriverKey]uint64
	usageMissing          map[modelDriverKey]uint64
	billingTransitions    map[string]uint64
	rejections            map[string]uint64
	concurrencyLeases     map[string]int64
	concurrencyRejections map[string]uint64
	heartbeatFailures     uint64
	ghostLeases           uint64
}

func NewAIGatewayMetrics(collector AIGatewayGaugeCollector) *AIGatewayMetrics {
	return &AIGatewayMetrics{
		collector: collector,
		models:    map[string]struct{}{"other": {}},
		requests:  make(map[requestMetricKey]uint64), requestDurations: make(map[string]*histogramValue),
		ttft: make(map[modelDriverKey]*histogramValue), streamInterruptions: make(map[modelDriverKey]uint64),
		upstreamRequests: make(map[upstreamMetricKey]uint64), upstreamRetries: make(map[modelDriverKey]uint64),
		usageMissing: make(map[modelDriverKey]uint64), billingTransitions: make(map[string]uint64),
		rejections: make(map[string]uint64), concurrencyLeases: make(map[string]int64),
		concurrencyRejections: make(map[string]uint64),
	}
}

// AllowLogicalModel 只接纳数据库已经验证过的逻辑模型编码，并设置硬上限防止时间序列无限增长。
func (m *AIGatewayMetrics) AllowLogicalModel(code string) {
	code = strings.TrimSpace(code)
	if m == nil || !validMetricModelCode(code) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.models) >= maxObservableModels+1 {
		return
	}
	m.models[code] = struct{}{}
}

func validMetricModelCode(code string) bool {
	if code == "" || len(code) > 128 {
		return false
	}
	for _, value := range code {
		if unicode.IsLetter(value) || unicode.IsDigit(value) || strings.ContainsRune("._/-", value) {
			continue
		}
		return false
	}
	return true
}

func (m *AIGatewayMetrics) model(code string) string {
	if m == nil {
		return "other"
	}
	if _, ok := m.models[strings.TrimSpace(code)]; ok {
		return strings.TrimSpace(code)
	}
	return "other"
}

func fixedMetricValue(value string, allowed map[string]struct{}) string {
	value = strings.TrimSpace(value)
	if _, ok := allowed[value]; ok {
		return value
	}
	return "other"
}

func (m *AIGatewayMetrics) RecordRequest(modelCode, requestType, outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	requestType = fixedMetricValue(requestType, requestTypes)
	outcome = fixedMetricValue(outcome, requestOutcomes)
	m.requests[requestMetricKey{Model: m.model(modelCode), RequestType: requestType, Outcome: outcome}]++
	observeHistogram(m.requestDurations, requestType, requestDurationBuckets, duration)
}

func (m *AIGatewayMetrics) RecordTTFT(modelCode, driver string, duration time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := modelDriverKey{Model: m.model(modelCode), Driver: fixedMetricValue(driver, metricDrivers)}
	observeHistogram(m.ttft, key, ttftBuckets, duration)
}

func (m *AIGatewayMetrics) RecordStreamInterruption(modelCode, driver string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamInterruptions[modelDriverKey{Model: m.model(modelCode), Driver: fixedMetricValue(driver, metricDrivers)}]++
}

func (m *AIGatewayMetrics) RecordUpstream(modelCode, driver, outcome string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := upstreamMetricKey{Model: m.model(modelCode), Driver: fixedMetricValue(driver, metricDrivers), Outcome: fixedMetricValue(outcome, upstreamOutcomes)}
	m.upstreamRequests[key]++
}

func (m *AIGatewayMetrics) RecordUpstreamRetry(modelCode, driver string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upstreamRetries[modelDriverKey{Model: m.model(modelCode), Driver: fixedMetricValue(driver, metricDrivers)}]++
}

func (m *AIGatewayMetrics) RecordUsageMissing(modelCode, requestType string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageMissing[modelDriverKey{Model: m.model(modelCode), Driver: fixedMetricValue(requestType, requestTypes)}]++
}

func (m *AIGatewayMetrics) RecordBillingTransition(state string) {
	if m == nil || !containsString(billingStates, state) {
		return
	}
	m.mu.Lock()
	m.billingTransitions[state]++
	m.mu.Unlock()
}

func (m *AIGatewayMetrics) RecordRejection(reason string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.rejections[fixedMetricValue(reason, rejectionReasons)]++
	m.mu.Unlock()
}

func (m *AIGatewayMetrics) RecordConcurrencyLease(scope string, delta int64) {
	if m == nil || !containsString(metricScopes, scope) {
		return
	}
	m.mu.Lock()
	m.concurrencyLeases[scope] += delta
	if m.concurrencyLeases[scope] < 0 {
		m.concurrencyLeases[scope] = 0
	}
	m.mu.Unlock()
}

func (m *AIGatewayMetrics) RecordConcurrencyRejection(scope string) {
	if m == nil || !containsString(metricScopes, scope) {
		return
	}
	m.mu.Lock()
	m.concurrencyRejections[scope]++
	m.mu.Unlock()
}

func (m *AIGatewayMetrics) RecordHeartbeatFailure() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.heartbeatFailures++
	m.mu.Unlock()
}

func (m *AIGatewayMetrics) RecordGhostLease(count uint64) {
	if m == nil || count == 0 {
		return
	}
	m.mu.Lock()
	m.ghostLeases += count
	m.mu.Unlock()
}

func observeHistogram[K comparable](target map[K]*histogramValue, key K, buckets []float64, duration time.Duration) {
	value := target[key]
	if value == nil {
		value = &histogramValue{Buckets: make([]uint64, len(buckets)+1)}
		target[key] = value
	}
	seconds := duration.Seconds()
	for index, boundary := range buckets {
		if seconds <= boundary {
			value.Buckets[index]++
		}
	}
	value.Buckets[len(buckets)]++
	value.Count++
	value.Sum += seconds
}

// AIGatewayPrometheus 返回可以直接追加到统一 text exposition 端点的稳定指标族。
func (m *AIGatewayMetrics) AIGatewayPrometheus(ctx context.Context) (string, error) {
	if m == nil {
		return "", nil
	}
	gauges := AIGatewayGaugeSnapshot{}
	if m.collector != nil {
		var err error
		gauges, err = m.collector.CollectAIGatewayGauges(ctx, time.Now().UTC())
		if err != nil {
			return "", err
		}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out strings.Builder
	writeCounterHeader(&out, "molin_ai_gateway_requests_total", "AI 网关逻辑请求总数。")
	for _, key := range sortedRequestKeys(m.requests) {
		fmt.Fprintf(&out, "molin_ai_gateway_requests_total{logical_model_code=%q,request_type=%q,outcome=%q} %d\n", key.Model, key.RequestType, key.Outcome, m.requests[key])
	}
	writeHistogram(&out, "molin_ai_gateway_request_duration_seconds", "AI 网关端到端处理耗时秒数。", m.requestDurations, requestDurationBuckets, func(key string) string { return fmt.Sprintf("request_type=%q", key) })
	writeHistogram(&out, "molin_ai_gateway_ttft_seconds", "AI 网关流式首个可公开 Token 耗时秒数。", m.ttft, ttftBuckets, func(key modelDriverKey) string {
		return fmt.Sprintf("logical_model_code=%q,driver=%q", key.Model, key.Driver)
	})
	writeModelDriverCounter(&out, "molin_ai_gateway_stream_interruptions_total", "AI 网关流式客户端中断总数。", m.streamInterruptions)
	writeCounterHeader(&out, "molin_ai_gateway_upstream_requests_total", "AI 网关上游请求结果总数。")
	for _, key := range sortedUpstreamKeys(m.upstreamRequests) {
		fmt.Fprintf(&out, "molin_ai_gateway_upstream_requests_total{logical_model_code=%q,driver=%q,outcome=%q} %d\n", key.Model, key.Driver, key.Outcome, m.upstreamRequests[key])
	}
	writeModelDriverCounter(&out, "molin_ai_gateway_upstream_retries_total", "AI 网关上游安全重试总数。", m.upstreamRetries)
	writeCounterHeader(&out, "molin_ai_gateway_usage_missing_total", "AI 网关可信 Usage 缺失总数。")
	for _, key := range sortedModelDriverKeys(m.usageMissing) {
		fmt.Fprintf(&out, "molin_ai_gateway_usage_missing_total{logical_model_code=%q,request_type=%q} %d\n", key.Model, key.Driver, m.usageMissing[key])
	}
	writeSimpleCounter(&out, "molin_ai_gateway_billing_transitions_total", "AI 网关账务状态进入次数。", "billing_state", billingStates, m.billingTransitions)
	writeSimpleCounter(&out, "molin_ai_gateway_rejections_total", "AI 网关前置安全和治理拒绝总数。", "rejection_reason", sortedSet(rejectionReasons), m.rejections)
	writeGaugeHeader(&out, "molin_ai_gateway_billing_requests", "AI 网关当前账务状态请求数。")
	for _, state := range billingStates {
		fmt.Fprintf(&out, "molin_ai_gateway_billing_requests{billing_state=%q} %d\n", state, gauges.BillingRequests[state])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_billing_oldest_age_seconds", "AI 网关当前账务状态最老请求年龄秒数。")
	for _, state := range billingStates {
		fmt.Fprintf(&out, "molin_ai_gateway_billing_oldest_age_seconds{billing_state=%q} %d\n", state, gauges.BillingOldestAge[state])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_unreleased_holds", "AI 网关当前未释放钱包预占数量。")
	fmt.Fprintf(&out, "molin_ai_gateway_unreleased_holds %d\n", gauges.UnreleasedHolds.Count)
	writeGaugeHeader(&out, "molin_ai_gateway_unreleased_holds_amount_cny", "AI 网关当前未释放钱包预占金额。")
	fmt.Fprintf(&out, "molin_ai_gateway_unreleased_holds_amount_cny %s\n", gauges.UnreleasedHolds.Amount.StringFixed(8))
	writeBacklog(&out, "outbox", outboxStatuses, gauges.OutboxBacklog)
	writeBacklog(&out, "compensation", compensationStatuses, gauges.CompensationBacklog)
	writeGaugeHeader(&out, "molin_ai_gateway_billing_difference_cny", "AI 网关账单聚合差额。")
	for _, kind := range differenceKinds {
		fmt.Fprintf(&out, "molin_ai_gateway_billing_difference_cny{kind=%q} %s\n", kind, gauges.BillingDifferences[kind].Abs().StringFixed(8))
	}
	writeGaugeHeader(&out, "molin_ai_gateway_billing_anomalies", "AI 网关账单异常事实数量。")
	for _, kind := range billingAnomalyKinds {
		fmt.Fprintf(&out, "molin_ai_gateway_billing_anomalies{kind=%q} %d\n", kind, gauges.BillingAnomalies[kind])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_concurrency_leases", "当前进程持有的 AI 网关并发租约数量。")
	writeCounterHeader(&out, "molin_ai_gateway_concurrency_rejections_total", "AI 网关并发租约拒绝总数。")
	for _, scope := range metricScopes {
		fmt.Fprintf(&out, "molin_ai_gateway_concurrency_leases{scope=%q} %d\n", scope, m.concurrencyLeases[scope])
		fmt.Fprintf(&out, "molin_ai_gateway_concurrency_rejections_total{scope=%q} %d\n", scope, m.concurrencyRejections[scope])
	}
	writeCounterHeader(&out, "molin_ai_gateway_heartbeat_failures_total", "AI 网关并发租约心跳失败总数。")
	fmt.Fprintf(&out, "molin_ai_gateway_heartbeat_failures_total %d\n", m.heartbeatFailures)
	writeCounterHeader(&out, "molin_ai_gateway_ghost_leases_total", "AI 网关发现并清理的幽灵租约总数。")
	fmt.Fprintf(&out, "molin_ai_gateway_ghost_leases_total %d\n", m.ghostLeases)
	return out.String(), nil
}

func writeCounterHeader(out *strings.Builder, name, help string) {
	fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
}

func writeGaugeHeader(out *strings.Builder, name, help string) {
	fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
}

func writeSimpleCounter(out *strings.Builder, name, help, label string, values []string, counts map[string]uint64) {
	writeCounterHeader(out, name, help)
	for _, value := range values {
		fmt.Fprintf(out, "%s{%s=%q} %d\n", name, label, value, counts[value])
	}
}

func writeModelDriverCounter(out *strings.Builder, name, help string, values map[modelDriverKey]uint64) {
	writeCounterHeader(out, name, help)
	for _, key := range sortedModelDriverKeys(values) {
		fmt.Fprintf(out, "%s{logical_model_code=%q,driver=%q} %d\n", name, key.Model, key.Driver, values[key])
	}
}

func writeBacklog(out *strings.Builder, prefix string, statuses []string, values map[string]AIGatewayBacklogGauge) {
	countName := "molin_ai_gateway_" + prefix + "_backlog"
	ageName := "molin_ai_gateway_" + prefix + "_oldest_age_seconds"
	writeGaugeHeader(out, countName, "AI 网关任务积压数量。")
	writeGaugeHeader(out, ageName, "AI 网关最老积压任务年龄秒数。")
	for _, status := range statuses {
		value := values[status]
		fmt.Fprintf(out, "%s{status=%q} %d\n", countName, status, value.Count)
		fmt.Fprintf(out, "%s{status=%q} %d\n", ageName, status, value.OldestAgeSeconds)
	}
}

func writeHistogram[K comparable](out *strings.Builder, name, help string, values map[K]*histogramValue, buckets []float64, labels func(K) string) {
	fmt.Fprintf(out, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	keys := make([]K, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	for _, key := range keys {
		value := values[key]
		base := labels(key)
		for index, boundary := range buckets {
			fmt.Fprintf(out, "%s_bucket{%s,le=%q} %d\n", name, base, strconv.FormatFloat(boundary, 'g', -1, 64), value.Buckets[index])
		}
		fmt.Fprintf(out, "%s_bucket{%s,le=\"+Inf\"} %d\n", name, base, value.Buckets[len(buckets)])
		fmt.Fprintf(out, "%s_sum{%s} %.9f\n", name, base, value.Sum)
		fmt.Fprintf(out, "%s_count{%s} %d\n", name, base, value.Count)
	}
}

func sortedRequestKeys(values map[requestMetricKey]uint64) []requestMetricKey {
	keys := make([]requestMetricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Model+"\x00"+keys[i].RequestType+"\x00"+keys[i].Outcome < keys[j].Model+"\x00"+keys[j].RequestType+"\x00"+keys[j].Outcome
	})
	return keys
}

func sortedModelDriverKeys(values map[modelDriverKey]uint64) []modelDriverKey {
	keys := make([]modelDriverKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Model+"\x00"+keys[i].Driver < keys[j].Model+"\x00"+keys[j].Driver })
	return keys
}

func sortedUpstreamKeys(values map[upstreamMetricKey]uint64) []upstreamMetricKey {
	keys := make([]upstreamMetricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].Model+"\x00"+keys[i].Driver+"\x00"+keys[i].Outcome < keys[j].Model+"\x00"+keys[j].Driver+"\x00"+keys[j].Outcome
	})
	return keys
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values)+1)
	for value := range values {
		out = append(out, value)
	}
	out = append(out, "other")
	sort.Strings(out)
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
