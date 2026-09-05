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
	requestTypes           = map[string]struct{}{"json": {}, "stream": {}, "image": {}}
	requestOutcomes        = map[string]struct{}{"success": {}, "failure": {}, "rejected": {}}
	metricDrivers          = map[string]struct{}{"native": {}, "bifrost": {}, "fake": {}, "openrouter-images": {}}
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
	imageTaskStatuses    = []string{"created", "reserved", "submitted", "processing", "storing", "moderating", "succeeded", "failed", "cancelled", "expired", "pending_reconcile"}
	imageAssetStates     = []string{"temporary", "available", "quarantined", "expiring", "deleting", "deleted", "delete_failed"}
	videoTaskStatuses    = []string{"created", "reserved", "queued", "submitting", "submitted", "processing", "fetching", "storing", "moderating", "labeling", "succeeded", "failed", "cancelled", "expired", "pending_reconcile"}
	billingAnomalyKinds  = []string{"duplicate_settlement", "unbilled_execution", "missing_price_snapshot", "missing_wallet_transaction", "missing_usage", "completed_pending", "billing_exception"}
)

// AIGatewayAmountGauge 表示一个只读事实集合的数量、人民币金额和最老事实年龄。
type AIGatewayAmountGauge struct {
	Count            uint64
	Amount           decimal.Decimal
	OldestAgeSeconds uint64
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
	BillingAmounts      map[string]decimal.Decimal
	SecurityFindings    map[string]uint64
	ImageTasks          map[string]AIGatewayBacklogGauge
	ImageAssets         map[string]uint64
	ImageDifference     decimal.Decimal
}

// AIGatewayGaugeCollector 只允许执行 SELECT 聚合，不拥有任何修复、补偿或钱包写入能力。
type AIGatewayGaugeCollector interface {
	CollectAIGatewayGauges(ctx context.Context, now time.Time) (AIGatewayGaugeSnapshot, error)
}

// AIGatewayConcurrencyGaugeCollector 从 Redis 共享租约事实读取四层当前值，避免多实例各自维护 Gauge 后发生漂移。
type AIGatewayConcurrencyGaugeCollector interface {
	CollectConcurrencyLeases(ctx context.Context) (map[string]uint64, error)
}

type ImageQueueGaugeCollector interface {
	CollectImageQueueDepths(ctx context.Context) (map[string]uint64, error)
}

type VideoGaugeSnapshot struct {
	Tasks                          map[string]uint64
	TaskOldestAgeSeconds           map[string]uint64
	Queues                         map[string]uint64
	Capacity                       map[string]uint64
	UnsettledHolds                 AIGatewayAmountGauge
	ObjectObservations             map[string]uint64
	ObjectCompensations            map[string]uint64
	ComponentUp                    map[string]uint64
	ComponentFailures              map[string]uint64
	ComponentLastSuccessAgeSeconds map[string]uint64
	ObjectBytes                    map[string]uint64
	CleanupFailures                map[string]uint64
}

type VideoGaugeCollector interface {
	CollectVideoGauges(context.Context, time.Time) (VideoGaugeSnapshot, error)
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

	collector            AIGatewayGaugeCollector
	concurrencyCollector AIGatewayConcurrencyGaugeCollector
	imageQueueCollector  ImageQueueGaugeCollector
	videoGaugeCollector  VideoGaugeCollector
	models               map[string]struct{}

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

func (m *AIGatewayMetrics) WithVideoGaugeCollector(collector VideoGaugeCollector) *AIGatewayMetrics {
	if m == nil {
		return m
	}
	m.mu.Lock()
	m.videoGaugeCollector = collector
	m.mu.Unlock()
	return m
}

func (m *AIGatewayMetrics) WithImageQueueGaugeCollector(collector ImageQueueGaugeCollector) *AIGatewayMetrics {
	if m == nil {
		return m
	}
	m.mu.Lock()
	m.imageQueueCollector = collector
	m.mu.Unlock()
	return m
}

// WithConcurrencyGaugeCollector 注入 Redis 权威租约采集器；只应在模块装配阶段调用一次。
func (m *AIGatewayMetrics) WithConcurrencyGaugeCollector(collector AIGatewayConcurrencyGaugeCollector) *AIGatewayMetrics {
	if m == nil {
		return m
	}
	m.mu.Lock()
	m.concurrencyCollector = collector
	m.mu.Unlock()
	return m
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
	concurrencyCollector := m.concurrencyCollector
	imageQueueCollector := m.imageQueueCollector
	videoGaugeCollector := m.videoGaugeCollector
	m.mu.RUnlock()
	var sharedConcurrencyLeases map[string]uint64
	if concurrencyCollector != nil {
		var err error
		sharedConcurrencyLeases, err = concurrencyCollector.CollectConcurrencyLeases(ctx)
		if err != nil {
			return "", err
		}
	}
	imageQueueDepths := map[string]uint64{}
	if imageQueueCollector != nil {
		var err error
		imageQueueDepths, err = imageQueueCollector.CollectImageQueueDepths(ctx)
		if err != nil {
			return "", err
		}
	}
	videoGauges := VideoGaugeSnapshot{}
	if videoGaugeCollector != nil {
		var err error
		videoGauges, err = videoGaugeCollector.CollectVideoGauges(ctx, time.Now().UTC())
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
	writeGaugeHeader(&out, "molin_ai_gateway_unreleased_holds_oldest_age_seconds", "AI 网关当前最老未释放钱包预占年龄，单位秒。")
	fmt.Fprintf(&out, "molin_ai_gateway_unreleased_holds_oldest_age_seconds %d\n", gauges.UnreleasedHolds.OldestAgeSeconds)
	writeBacklog(&out, "outbox", outboxStatuses, gauges.OutboxBacklog)
	writeBacklog(&out, "compensation", compensationStatuses, gauges.CompensationBacklog)
	writeBacklog(&out, "image_tasks", imageTaskStatuses, gauges.ImageTasks)
	writeGaugeHeader(&out, "molin_ai_gateway_image_assets", "图片网关当前资产生命周期数量。")
	for _, state := range imageAssetStates {
		fmt.Fprintf(&out, "molin_ai_gateway_image_assets{lifecycle_state=%q} %d\n", state, gauges.ImageAssets[state])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_image_reconciliation_difference", "图片请求、销售、钱包和可交付资产的聚合差异。")
	fmt.Fprintf(&out, "molin_ai_gateway_image_reconciliation_difference %s\n", gauges.ImageDifference.Abs().StringFixed(8))
	writeGaugeHeader(&out, "molin_ai_gateway_image_queue_depth", "图片网关RabbitMQ主队列和死信队列深度。")
	for _, queueName := range []string{"main", "dead"} {
		fmt.Fprintf(&out, "molin_ai_gateway_image_queue_depth{queue=%q} %d\n", queueName, imageQueueDepths[queueName])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_video_tasks", "视频网关当前任务状态数量。")
	writeGaugeHeader(&out, "molin_ai_gateway_video_task_oldest_age_seconds", "视频网关当前状态最老任务年龄秒数。")
	for _, operation := range []string{"text_to_video", "image_to_video"} {
		for _, status := range videoTaskStatuses {
			key := operation + ":" + status
			fmt.Fprintf(&out, "molin_ai_gateway_video_tasks{operation=%q,status=%q} %d\n", operation, status, videoGauges.Tasks[key])
			fmt.Fprintf(&out, "molin_ai_gateway_video_task_oldest_age_seconds{operation=%q,status=%q} %d\n", operation, status, videoGauges.TaskOldestAgeSeconds[key])
		}
	}
	writeGaugeHeader(&out, "molin_ai_gateway_video_queue_depth", "视频网关RabbitMQ工作、延迟和死信队列深度。")
	for _, stage := range []string{"submit", "poll", "fetch"} {
		for _, kind := range []string{"work", "delay", "dead"} {
			fmt.Fprintf(&out, "molin_ai_gateway_video_queue_depth{stage=%q,kind=%q} %d\n", stage, kind, videoGauges.Queues[stage+":"+kind])
		}
	}
	writeGaugeHeader(&out, "molin_ai_gateway_video_capacity_leases", "视频网关Redis容量租约数量。")
	for _, phase := range []string{"queued", "promoting", "running"} {
		fmt.Fprintf(&out, "molin_ai_gateway_video_capacity_leases{phase=%q} %d\n", phase, videoGauges.Capacity[phase])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_video_unsettled_holds", "视频任务未结算冻结数量。")
	fmt.Fprintf(&out, "molin_ai_gateway_video_unsettled_holds %d\n", videoGauges.UnsettledHolds.Count)
	writeGaugeHeader(&out, "molin_ai_gateway_video_unsettled_holds_amount_cny", "视频任务未结算冻结金额。")
	fmt.Fprintf(&out, "molin_ai_gateway_video_unsettled_holds_amount_cny %s\n", videoGauges.UnsettledHolds.Amount.StringFixed(8))
	writeGaugeHeader(&out, "molin_ai_gateway_video_unsettled_holds_oldest_age_seconds", "视频任务最老未结算冻结年龄秒数。")
	fmt.Fprintf(&out, "molin_ai_gateway_video_unsettled_holds_oldest_age_seconds %d\n", videoGauges.UnsettledHolds.OldestAgeSeconds)
	writeGaugeHeader(&out, "molin_ai_gateway_video_object_observations", "视频对象双向对账观察数量。")
	for _, direction := range []string{"db_missing_object", "storage_unreferenced_object"} {
		for _, status := range []string{"observing", "confirmed"} {
			fmt.Fprintf(&out, "molin_ai_gateway_video_object_observations{direction=%q,status=%q} %d\n", direction, status, videoGauges.ObjectObservations[direction+":"+status])
		}
	}
	writeGaugeHeader(&out, "molin_ai_gateway_video_object_compensations", "视频对象缺失与孤儿清理补偿数量。")
	for _, taskType := range []string{"video_object_missing_reconcile", "video_orphan_cleanup"} {
		for _, status := range []string{"pending", "running", "retry", "dead", "manual_review"} {
			fmt.Fprintf(&out, "molin_ai_gateway_video_object_compensations{task_type=%q,status=%q} %d\n", taskType, status, videoGauges.ObjectCompensations[taskType+":"+status])
		}
	}
	writeGaugeHeader(&out, "molin_ai_gateway_video_component_up", "视频运行组件最近一次状态，1为可用、0为失败或尚未成功。")
	writeCounterHeader(&out, "molin_ai_gateway_video_component_failures_total", "视频运行组件累计失败次数。")
	writeGaugeHeader(&out, "molin_ai_gateway_video_component_last_success_age_seconds", "视频运行组件距离最近成功的秒数。")
	for _, component := range []string{"mysql", "redis", "rabbitmq", "outbox", "orphan_cleanup", "missing_repair", "input_retention", "output_retention", "object_scanner", "consumer_submit", "consumer_poll", "consumer_fetch"} {
		fmt.Fprintf(&out, "molin_ai_gateway_video_component_up{component=%q} %d\n", component, videoGauges.ComponentUp[component])
		fmt.Fprintf(&out, "molin_ai_gateway_video_component_failures_total{component=%q} %d\n", component, videoGauges.ComponentFailures[component])
		fmt.Fprintf(&out, "molin_ai_gateway_video_component_last_success_age_seconds{component=%q} %d\n", component, videoGauges.ComponentLastSuccessAgeSeconds[component])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_video_object_bytes", "视频网关数据库仍引用的对象字节数。")
	for _, bucket := range []string{"ai-upload-temp", "ai-result", "ai-quarantine", "ai-user-assets"} {
		fmt.Fprintf(&out, "molin_ai_gateway_video_object_bytes{bucket=%q} %d\n", bucket, videoGauges.ObjectBytes[bucket])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_video_cleanup_failures", "视频对象、输入和资产当前清理失败数量。")
	for _, kind := range []string{"object_compensation", "input_cleanup", "asset_delete"} {
		fmt.Fprintf(&out, "molin_ai_gateway_video_cleanup_failures{kind=%q} %d\n", kind, videoGauges.CleanupFailures[kind])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_billing_difference_cny", "AI 网关账单聚合差额。")
	for _, kind := range differenceKinds {
		fmt.Fprintf(&out, "molin_ai_gateway_billing_difference_cny{kind=%q} %s\n", kind, gauges.BillingDifferences[kind].Abs().StringFixed(8))
	}
	writeGaugeHeader(&out, "molin_ai_gateway_billing_anomalies", "AI 网关账单异常事实数量。")
	for _, kind := range billingAnomalyKinds {
		fmt.Fprintf(&out, "molin_ai_gateway_billing_anomalies{kind=%q} %d\n", kind, gauges.BillingAnomalies[kind])
	}
	writeGaugeHeader(&out, "molin_ai_gateway_billing_amount_cny", "AI 网关账本、模型 Usage 与钱包消费聚合金额。")
	for _, kind := range []string{"request_settled", "model_usage", "wallet_consumed"} {
		fmt.Fprintf(&out, "molin_ai_gateway_billing_amount_cny{kind=%q} %s\n", kind, gauges.BillingAmounts[kind].StringFixed(8))
	}
	writeGaugeHeader(&out, "molin_ai_gateway_security_findings", "AI 网关已进入安全审计的高危发现数量。")
	fmt.Fprintf(&out, "molin_ai_gateway_security_findings{kind=%q} %d\n", "secret_leak", gauges.SecurityFindings["secret_leak"])
	writeGaugeHeader(&out, "molin_ai_gateway_concurrency_leases", "Redis 中当前有效的 AI 网关共享并发租约数量。")
	writeCounterHeader(&out, "molin_ai_gateway_concurrency_rejections_total", "AI 网关并发租约拒绝总数。")
	for _, scope := range metricScopes {
		leaseCount := m.concurrencyLeases[scope]
		if sharedConcurrencyLeases != nil {
			leaseCount = int64(sharedConcurrencyLeases[scope])
		}
		fmt.Fprintf(&out, "molin_ai_gateway_concurrency_leases{scope=%q} %d\n", scope, leaseCount)
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
