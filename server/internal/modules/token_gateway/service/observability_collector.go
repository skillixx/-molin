package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const unbilledExecutionGracePeriod = 5 * time.Minute

// AIGatewayDBGaugeCollector 只从 MySQL 读取 AI 网关持久化事实，不执行修复、补偿或钱包写入。
// 指标抓取失败时由统一 metrics 端点 fail-closed，避免输出看似正常但实际过期的零值。
type AIGatewayDBGaugeCollector struct {
	db *gorm.DB
}

// NewAIGatewayDBGaugeCollector 创建数据库 Gauge 采集器。
func NewAIGatewayDBGaugeCollector(db *gorm.DB) *AIGatewayDBGaugeCollector {
	return &AIGatewayDBGaugeCollector{db: db}
}

type aiGatewayStateCountRow struct {
	BillingStatus    string
	Count            uint64
	OldestAgeSeconds uint64
}

type aiGatewayAmountRow struct {
	Count            uint64
	Amount           string
	OldestAgeSeconds uint64
}

type aiGatewayBacklogRow struct {
	Status           string
	Count            uint64
	OldestAgeSeconds uint64
}

type aiGatewayBillingAmountRow struct {
	RequestSettled string
	ModelUsage     string
	WalletConsumed string
}

type aiGatewayReconciliationRow struct {
	RequestUsageDifference   string
	RequestHoldDifference    string
	RequestWalletDifference  string
	DuplicateSettlement      uint64
	UnbilledExecution        uint64
	MissingPriceSnapshot     uint64
	MissingWalletTransaction uint64
	MissingUsage             uint64
	CompletedPending         uint64
	BillingException         uint64
}

// AIGatewayReconciliationIssue 是只读对账返回的 request_id 级异常证据，不包含手机号、密钥或请求正文。
type AIGatewayReconciliationIssue struct {
	RequestID     string `json:"request_id"`
	IssueCode     string `json:"issue_code"`
	BillingStatus string `json:"billing_status"`
	ExpectedValue string `json:"expected_value,omitempty"`
	ActualValue   string `json:"actual_value,omitempty"`
}

// CollectAIGatewayGauges 在一次抓取中依次读取状态、积压和账单差额。
// 所有金额以 DECIMAL 在 MySQL 中计算，再按字符串解析，禁止经过 float64 丢失财务精度。
func (c *AIGatewayDBGaugeCollector) CollectAIGatewayGauges(ctx context.Context, now time.Time) (AIGatewayGaugeSnapshot, error) {
	snapshot := emptyAIGatewayGaugeSnapshot()
	if c == nil || c.db == nil {
		return snapshot, errors.New("AI 网关指标数据库未配置")
	}
	now = now.UTC()

	var billingRows []aiGatewayStateCountRow
	if err := c.db.WithContext(ctx).Raw(
		"SELECT billing_status, COUNT(*) AS count, CAST(COALESCE(GREATEST(TIMESTAMPDIFF(SECOND, MIN(updated_at), ?), 0), 0) AS UNSIGNED) AS oldest_age_seconds FROM ai_requests GROUP BY billing_status", now,
	).Scan(&billingRows).Error; err != nil {
		return snapshot, err
	}
	for _, row := range billingRows {
		if containsString(billingStates, row.BillingStatus) {
			snapshot.BillingRequests[row.BillingStatus] = row.Count
			snapshot.BillingOldestAge[row.BillingStatus] = row.OldestAgeSeconds
		}
	}

	var holdRow aiGatewayAmountRow
	if err := c.db.WithContext(ctx).Raw(`
		SELECT COUNT(*) AS count, CAST(COALESCE(SUM(holds.hold_amount), 0) AS CHAR) AS amount,
			CAST(COALESCE(GREATEST(TIMESTAMPDIFF(SECOND, MIN(holds.updated_at), ?), 0), 0) AS UNSIGNED) AS oldest_age_seconds
		FROM wallet_holds AS holds
		WHERE holds.status = ? AND (holds.idempotency_key LIKE '%:ai-hold'
			OR EXISTS (SELECT 1 FROM ai_request_wallet_links AS links WHERE links.wallet_hold_id = holds.id))`, now, "holding").Scan(&holdRow).Error; err != nil {
		return snapshot, err
	}
	holdAmount, err := decimal.NewFromString(holdRow.Amount)
	if err != nil {
		return snapshot, err
	}
	snapshot.UnreleasedHolds = AIGatewayAmountGauge{Count: holdRow.Count, Amount: holdAmount, OldestAgeSeconds: holdRow.OldestAgeSeconds}

	if err := c.collectBacklog(ctx, "ai_outbox_events", outboxStatuses, now, snapshot.OutboxBacklog); err != nil {
		return snapshot, err
	}
	if err := c.collectBacklog(ctx, "ai_compensation_tasks", compensationStatuses, now, snapshot.CompensationBacklog); err != nil {
		return snapshot, err
	}
	var amounts aiGatewayBillingAmountRow
	if err := c.db.WithContext(ctx).Raw(aiGatewayBillingAmountsSQL).Scan(&amounts).Error; err != nil {
		return snapshot, err
	}
	for code, raw := range map[string]string{"request_settled": amounts.RequestSettled, "model_usage": amounts.ModelUsage, "wallet_consumed": amounts.WalletConsumed} {
		value, parseErr := decimal.NewFromString(raw)
		if parseErr != nil {
			return snapshot, parseErr
		}
		snapshot.BillingAmounts[code] = value
	}
	var secretLeakFindings uint64
	if err := c.db.WithContext(ctx).Raw(
		"SELECT COUNT(DISTINCT target_id) FROM audit_logs WHERE module = ? AND action = ? AND target_type = ? AND target_id IS NOT NULL AND created_at >= ?",
		"token_gateway", "secret_leak_detected", "api_key", now.Add(-5*time.Minute),
	).Row().Scan(&secretLeakFindings); err != nil {
		return snapshot, err
	}
	snapshot.SecurityFindings["secret_leak"] = secretLeakFindings

	var reconciliation aiGatewayReconciliationRow
	cutoff := now.Add(-unbilledExecutionGracePeriod)
	if err := c.db.WithContext(ctx).Raw(aiGatewayReconciliationSQL, cutoff, cutoff).Scan(&reconciliation).Error; err != nil {
		return snapshot, err
	}
	requestUsage, err := decimal.NewFromString(reconciliation.RequestUsageDifference)
	if err != nil {
		return snapshot, err
	}
	requestHold, err := decimal.NewFromString(reconciliation.RequestHoldDifference)
	if err != nil {
		return snapshot, err
	}
	requestWallet, err := decimal.NewFromString(reconciliation.RequestWalletDifference)
	if err != nil {
		return snapshot, err
	}
	snapshot.BillingDifferences["request_usage"] = requestUsage
	snapshot.BillingDifferences["request_hold"] = requestHold
	snapshot.BillingDifferences["request_wallet"] = requestWallet
	snapshot.BillingAnomalies["duplicate_settlement"] = reconciliation.DuplicateSettlement
	snapshot.BillingAnomalies["unbilled_execution"] = reconciliation.UnbilledExecution
	snapshot.BillingAnomalies["missing_price_snapshot"] = reconciliation.MissingPriceSnapshot
	snapshot.BillingAnomalies["missing_wallet_transaction"] = reconciliation.MissingWalletTransaction
	snapshot.BillingAnomalies["missing_usage"] = reconciliation.MissingUsage
	snapshot.BillingAnomalies["completed_pending"] = reconciliation.CompletedPending
	snapshot.BillingAnomalies["billing_exception"] = reconciliation.BillingException
	return snapshot, nil
}

// CollectAIGatewayReconciliationIssues 返回有限条 request_id 级证据，供只读 CLI 定位，不进入 Prometheus 标签。
func (c *AIGatewayDBGaugeCollector) CollectAIGatewayReconciliationIssues(ctx context.Context, now time.Time, limit int) ([]AIGatewayReconciliationIssue, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("AI 网关指标数据库未配置")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("对账明细上限必须在 1 到 1000 之间")
	}
	var issues []AIGatewayReconciliationIssue
	cutoff := now.UTC().Add(-unbilledExecutionGracePeriod)
	if err := c.db.WithContext(ctx).Raw(aiGatewayReconciliationIssuesSQL, cutoff, cutoff, cutoff, limit).Scan(&issues).Error; err != nil {
		return nil, err
	}
	return issues, nil
}

func emptyAIGatewayGaugeSnapshot() AIGatewayGaugeSnapshot {
	return AIGatewayGaugeSnapshot{
		BillingRequests:     make(map[string]uint64),
		BillingOldestAge:    make(map[string]uint64),
		OutboxBacklog:       make(map[string]AIGatewayBacklogGauge),
		CompensationBacklog: make(map[string]AIGatewayBacklogGauge),
		BillingDifferences:  make(map[string]decimal.Decimal),
		BillingAnomalies:    make(map[string]uint64),
		BillingAmounts:      make(map[string]decimal.Decimal),
		SecurityFindings:    make(map[string]uint64),
	}
}

const aiGatewayBillingAmountsSQL = `WITH selected_usage AS (
	SELECT usage_items.request_id, COALESCE(SUM(usage_items.amount), 0) AS sale_amount
	FROM ai_usage_items AS usage_items
	WHERE usage_items.sequence_no = 1
	  AND (usage_items.source = 'reconciled' OR (usage_items.source = 'provider' AND NOT EXISTS (
		SELECT 1 FROM ai_usage_items AS reconciled
		WHERE reconciled.request_id = usage_items.request_id AND reconciled.source = 'reconciled' AND reconciled.sequence_no = 1
	  )))
	GROUP BY usage_items.request_id
)
SELECT
	CAST(COALESCE((SELECT SUM(settled_amount) FROM ai_requests WHERE billing_status = 'settled'), 0) AS CHAR) AS request_settled,
	CAST(COALESCE((SELECT SUM(selected_usage.sale_amount) FROM selected_usage JOIN ai_requests ON ai_requests.request_id = selected_usage.request_id WHERE ai_requests.billing_status = 'settled'), 0) AS CHAR) AS model_usage,
	CAST(COALESCE((SELECT SUM(transactions.amount) FROM ai_request_wallet_links AS links JOIN ai_requests AS requests ON requests.request_id = links.request_id JOIN wallet_transactions AS transactions ON transactions.id = links.settle_transaction_id WHERE requests.billing_status = 'settled' AND transactions.type = 'consume' AND transactions.direction = 'out'), 0) AS CHAR) AS wallet_consumed`

func (c *AIGatewayDBGaugeCollector) collectBacklog(ctx context.Context, table string, statuses []string, now time.Time, target map[string]AIGatewayBacklogGauge) error {
	// 表名只能来自本文件内的固定调用点，不能接受 HTTP 参数或外部配置。
	query := "SELECT status, COUNT(*) AS count, CAST(COALESCE(GREATEST(TIMESTAMPDIFF(SECOND, MIN(created_at), ?), 0), 0) AS UNSIGNED) AS oldest_age_seconds FROM " + table + " WHERE status IN ? GROUP BY status"
	var rows []aiGatewayBacklogRow
	if err := c.db.WithContext(ctx).Raw(query, now, statuses).Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if containsString(statuses, row.Status) {
			target[row.Status] = AIGatewayBacklogGauge{Count: row.Count, OldestAgeSeconds: row.OldestAgeSeconds}
		}
	}
	return nil
}

// aiGatewayReconciliationSQL 用一组只读 CTE 固化 G7 在线对账口径：
// 1. 人工核对 Usage 存在时优先使用 reconciled 销售价，否则使用 provider 销售价；
// 2. settled_amount 必须同时等于选定 Usage、钱包 hold/link 和唯一 consume 流水金额；
// 3. 已执行成功但超过宽限期仍未结算、缺失价格快照或缺失钱包流水均计为异常。
const aiGatewayReconciliationSQL = `WITH selected_usage AS (
	SELECT usage_items.request_id, COALESCE(SUM(usage_items.amount), 0) AS sale_amount, MAX(usage_items.source) AS selected_source,
		COUNT(*) AS row_count,
		COUNT(DISTINCT CASE WHEN usage_items.meter_type IN ('input_tokens','output_tokens','cached_tokens','reasoning_tokens') THEN usage_items.meter_type END) AS meter_count,
		SUM(CASE WHEN usage_items.quantity < 0 OR usage_items.unit_price IS NULL OR usage_items.unit_price <= 0
			OR usage_items.amount IS NULL OR usage_items.amount < 0 THEN 1 ELSE 0 END) AS incomplete_count,
		SUM(CASE WHEN usage_items.meter_type = 'input_tokens' THEN usage_items.quantity ELSE 0 END) AS input_quantity,
		SUM(CASE WHEN usage_items.meter_type = 'output_tokens' THEN usage_items.quantity ELSE 0 END) AS output_quantity,
		SUM(CASE WHEN usage_items.meter_type = 'cached_tokens' THEN usage_items.quantity ELSE 0 END) AS cached_quantity,
		SUM(CASE WHEN usage_items.meter_type = 'reasoning_tokens' THEN usage_items.quantity ELSE 0 END) AS reasoning_quantity,
		COALESCE(SUM(CEIL(usage_items.quantity * usage_items.unit_price /
			NULLIF(CASE usage_items.meter_type
				WHEN 'input_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.input_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'output_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.output_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'cached_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.cached_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'reasoning_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.reasoning_tokens.scale')) AS DECIMAL(30,10))
			END, 0) * 100000000) / 100000000), 0) AS computed_base_amount,
		SUM(CASE WHEN usage_items.meter_type <> 'output_tokens' AND NOT (usage_items.amount <=>
			CEIL(usage_items.quantity * usage_items.unit_price /
			NULLIF(CASE usage_items.meter_type
				WHEN 'input_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.input_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'cached_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.cached_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'reasoning_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.reasoning_tokens.scale')) AS DECIMAL(30,10))
			END, 0) * 100000000) / 100000000)
			THEN 1 ELSE 0 END) AS non_output_amount_mismatch_count
	FROM ai_usage_items AS usage_items
	JOIN ai_requests AS usage_requests ON usage_requests.request_id = usage_items.request_id
	WHERE usage_items.sequence_no = 1
	  AND (
		usage_items.source = 'reconciled'
		OR (
			usage_items.source = 'provider'
			AND NOT EXISTS (
				SELECT 1 FROM ai_usage_items AS reconciled
				WHERE reconciled.request_id = usage_items.request_id
				  AND reconciled.source = 'reconciled'
				  AND reconciled.sequence_no = 1
			)
		)
	  )
	GROUP BY usage_items.request_id
), duplicate_settlements AS (
	SELECT COUNT(*) AS count FROM (
		SELECT links.request_id AS duplicate_key
		FROM ai_request_wallet_links AS links
		WHERE links.settle_transaction_id IS NOT NULL
		GROUP BY links.request_id HAVING COUNT(*) > 1
		UNION ALL
		SELECT CAST(links.settle_transaction_id AS CHAR) AS duplicate_key
		FROM ai_request_wallet_links AS links
		WHERE links.settle_transaction_id IS NOT NULL
		GROUP BY links.settle_transaction_id HAVING COUNT(*) > 1
	) AS duplicate_rows
)
SELECT
	CAST(COALESCE(SUM(CASE WHEN requests.billing_status = 'settled'
		THEN ABS(COALESCE(requests.settled_amount, 0) - COALESCE(selected_usage.sale_amount, 0)) ELSE 0 END), 0) AS CHAR) AS request_usage_difference,
	CAST(COALESCE(SUM(CASE WHEN links.id IS NOT NULL
		THEN ABS(COALESCE(requests.held_amount, 0) - links.held_amount)
			+ ABS(links.held_amount - COALESCE(holds.hold_amount, 0))
			+ CASE WHEN requests.billing_status IN ('settled', 'released')
				THEN ABS(links.held_amount - COALESCE(release_transactions.amount, 0)) ELSE 0 END
		ELSE 0 END), 0) AS CHAR) AS request_hold_difference,
	CAST(COALESCE(SUM(CASE WHEN requests.billing_status = 'settled'
		THEN ABS(COALESCE(requests.settled_amount, 0) - COALESCE(transactions.amount, 0)) ELSE 0 END), 0) AS CHAR) AS request_wallet_difference,
	MAX(duplicate_settlements.count) AS duplicate_settlement,
	CAST(COALESCE(SUM(CASE WHEN requests.execution_status = 'succeeded'
		AND (requests.billing_status = 'unquoted' OR (requests.billing_status = 'released'
			AND NOT (requests.error_code <=> 'output_moderation_blocked') AND NOT (requests.error_code <=> 'manual_reconciled')))
		AND requests.updated_at < ? THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS unbilled_execution,
	CAST(COALESCE(SUM(CASE WHEN requests.billing_status IN ('held', 'settlement_pending', 'settled', 'released', 'exception') AND (
		requests.price_snapshot_json IS NULL OR JSON_VALID(requests.price_snapshot_json) = 0
		OR price_versions.id IS NULL OR price_versions.logical_model_code <> requests.logical_model_code OR price_versions.currency <> 'CNY'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.price_version_id')), '') <> 'INTEGER'
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.price_version_id')) AS SIGNED), 0) <= 0
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.version_no')), '') <> 'INTEGER'
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.version_no')) AS UNSIGNED), 0) <> price_versions.version_no
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.logical_model_code')), '') <> 'STRING'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.logical_model_code')), '') <> requests.logical_model_code
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.currency')), '') <> 'STRING'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.currency')), '') <> 'CNY'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.exchange_rate')), '') <> 'STRING'
		OR NOT (CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.exchange_rate')) AS DECIMAL(20,8)) <=> price_versions.exchange_rate)
		OR NOT (price_versions.exchange_rate <=> 1)
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.rounding_mode')), '') <> 'STRING'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.rounding_mode')), '') <> price_versions.rounding_mode
		OR price_versions.rounding_mode <> 'ceil_8'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.failure_charge_policy')), '') <> 'STRING'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.failure_charge_policy')), '') <> price_versions.failure_charge_policy
		OR price_versions.failure_charge_policy <> 'confirmed_usage'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.minimum_charge')), '') <> 'STRING'
		OR NOT (CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.minimum_charge')) AS DECIMAL(20,8)) <=> 0.00000100)
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.max_input_tokens')), '') <> 'INTEGER'
		OR CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.max_input_tokens')) AS UNSIGNED) <> price_versions.max_input_tokens
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.max_output_tokens')), '') <> 'INTEGER'
		OR CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.max_output_tokens')) AS UNSIGNED) <> price_versions.max_output_tokens
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus')), '') <> 'OBJECT'
		OR COALESCE(JSON_LENGTH(JSON_EXTRACT(requests.price_snapshot_json, '$.skus')), 0) <> 4
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens')), '') <> 'OBJECT'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens')), '') <> 'OBJECT'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens')), '') <> 'OBJECT'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens')), '') <> 'OBJECT'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.meter_type')), '') <> 'input_tokens'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.meter_type')), '') <> 'output_tokens'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.meter_type')), '') <> 'cached_tokens'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.meter_type')), '') <> 'reasoning_tokens'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.sale_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.sale_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.sale_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.sale_unit_price')), '') <> 'STRING'
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.sale_unit_price')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.sale_unit_price')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.sale_unit_price')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.sale_unit_price')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.scale')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.scale')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.scale')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.scale')), '') <> 'STRING'
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.scale')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.scale')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.scale')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.scale')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.currency')), '') <> 'CNY'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.currency')), '') <> 'CNY'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.currency')), '') <> 'CNY'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.currency')), '') <> 'CNY'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.variant_hash')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.variant_hash')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.variant_hash')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.variant_hash')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.cost_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.cost_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.cost_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.cost_unit_price')), '') <> 'STRING'
		OR NOT EXISTS (SELECT 1 FROM ai_price_skus AS snapshot_sku WHERE snapshot_sku.price_version_id = price_versions.id
			AND snapshot_sku.meter_type = 'input_tokens'
			AND snapshot_sku.variant_hash = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.variant_hash')), '')
			AND snapshot_sku.cost_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.cost_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.sale_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.sale_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.scale = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.scale')) AS DECIMAL(30,10))
			AND snapshot_sku.currency = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.currency')), ''))
		OR NOT EXISTS (SELECT 1 FROM ai_price_skus AS snapshot_sku WHERE snapshot_sku.price_version_id = price_versions.id
			AND snapshot_sku.meter_type = 'output_tokens'
			AND snapshot_sku.variant_hash = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.variant_hash')), '')
			AND snapshot_sku.cost_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.cost_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.sale_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.sale_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.scale = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.scale')) AS DECIMAL(30,10))
			AND snapshot_sku.currency = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.currency')), ''))
		OR NOT EXISTS (SELECT 1 FROM ai_price_skus AS snapshot_sku WHERE snapshot_sku.price_version_id = price_versions.id
			AND snapshot_sku.meter_type = 'cached_tokens'
			AND snapshot_sku.variant_hash = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.variant_hash')), '')
			AND snapshot_sku.cost_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.cost_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.sale_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.sale_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.scale = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.scale')) AS DECIMAL(30,10))
			AND snapshot_sku.currency = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.currency')), ''))
		OR NOT EXISTS (SELECT 1 FROM ai_price_skus AS snapshot_sku WHERE snapshot_sku.price_version_id = price_versions.id
			AND snapshot_sku.meter_type = 'reasoning_tokens'
			AND snapshot_sku.variant_hash = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.variant_hash')), '')
			AND snapshot_sku.cost_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.cost_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.sale_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.sale_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.scale = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.scale')) AS DECIMAL(30,10))
			AND snapshot_sku.currency = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.currency')), ''))
	) THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS missing_price_snapshot,
	CAST(COALESCE(SUM(CASE WHEN
		(links.id IS NOT NULL AND (
			wallets.id IS NULL OR wallets.user_id <> requests.user_id
			OR requests.quoted_amount IS NULL OR requests.quoted_amount <= 0
			OR requests.held_amount IS NULL OR requests.held_amount < requests.quoted_amount
			OR links.quoted_amount <= 0 OR links.held_amount < links.quoted_amount
			OR NOT (links.quoted_amount <=> requests.quoted_amount)
			OR freeze_transactions.id IS NULL OR NOT (holds.freeze_txn_id <=> links.hold_transaction_id)
			OR holds.wallet_id <> links.wallet_id OR holds.user_id <> requests.user_id
			OR freeze_transactions.wallet_id <> links.wallet_id OR freeze_transactions.user_id <> requests.user_id
			OR freeze_transactions.type <> 'freeze' OR freeze_transactions.direction <> 'out'
			OR NOT (freeze_transactions.amount <=> links.held_amount)
		))
		OR (requests.billing_status IN ('held', 'settlement_pending', 'settled', 'released', 'exception') AND links.id IS NULL)
		OR (requests.billing_status IN ('held', 'settlement_pending', 'exception') AND (
			holds.status <> 'holding' OR requests.settled_amount IS NOT NULL OR links.settled_amount IS NOT NULL
			OR holds.settled_amount IS NOT NULL OR links.settle_transaction_id IS NOT NULL OR holds.settle_txn_id IS NOT NULL
			OR links.release_transaction_id IS NOT NULL
		))
		OR (requests.billing_status IN ('settled', 'released') AND (
			requests.settled_amount IS NULL
			OR requests.settled_amount < 0 OR requests.settled_amount > requests.held_amount
			OR links.settled_amount IS NULL OR NOT (links.settled_amount <=> requests.settled_amount)
			OR holds.settled_amount IS NULL OR NOT (holds.settled_amount <=> requests.settled_amount)
			OR links.release_transaction_id IS NULL OR release_transactions.id IS NULL
			OR release_transactions.wallet_id <> links.wallet_id OR release_transactions.user_id <> requests.user_id
			OR release_transactions.type <> 'unfreeze' OR release_transactions.direction <> 'in'
			OR NOT (release_transactions.amount <=> links.held_amount)
			OR (requests.billing_status = 'settled' AND (
				holds.status <> 'settled'
				OR (requests.settled_amount > 0 AND (
					links.settle_transaction_id IS NULL OR transactions.id IS NULL
					OR transactions.wallet_id <> links.wallet_id OR transactions.user_id <> requests.user_id
					OR transactions.type <> 'consume' OR transactions.direction <> 'out'
					OR NOT (transactions.amount <=> requests.settled_amount)
					OR NOT (holds.settle_txn_id <=> links.settle_transaction_id)
				))
				OR (requests.settled_amount = 0 AND (links.settle_transaction_id IS NOT NULL OR holds.settle_txn_id IS NOT NULL))
			))
			OR (requests.billing_status = 'released' AND (
				holds.status <> 'released' OR NOT (requests.settled_amount <=> 0)
				OR links.settle_transaction_id IS NOT NULL OR holds.settle_txn_id IS NOT NULL
			))
		))
		THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS missing_wallet_transaction,
	CAST(COALESCE(SUM(CASE WHEN (
		((requests.execution_status = 'succeeded' OR requests.billing_status = 'settled')
			AND NOT (requests.billing_status = 'settled' AND selected_usage.selected_source = 'reconciled') AND (
			NOT (requests.billing_status = 'released' AND requests.error_code <=> 'manual_reconciled') AND (
		(SELECT COUNT(DISTINCT raw_usage.meter_type) FROM ai_usage_items AS raw_usage
		 WHERE raw_usage.request_id = requests.request_id AND raw_usage.sequence_no = 0
		   AND raw_usage.source = 'provider'
		   AND raw_usage.meter_type IN ('input_tokens','output_tokens','total_tokens')) <> 3
		OR EXISTS (SELECT 1 FROM ai_usage_items AS raw_usage
			WHERE raw_usage.request_id = requests.request_id AND raw_usage.sequence_no = 0
			  AND (raw_usage.source NOT IN ('provider','provider_cost') OR (raw_usage.source = 'provider'
				AND raw_usage.meter_type NOT IN ('input_tokens','output_tokens','total_tokens','cached_tokens','reasoning_tokens'))))
		OR EXISTS (SELECT 1 FROM ai_usage_items AS raw_usage
			WHERE raw_usage.request_id = requests.request_id AND raw_usage.sequence_no = 0
			  AND raw_usage.source = 'provider' AND raw_usage.quantity < 0)
		OR (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'total_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = requests.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider') <>
		   (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type IN ('input_tokens','output_tokens') THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = requests.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
		OR (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'cached_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = requests.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider') >
		   (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'input_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = requests.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
		OR (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'reasoning_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = requests.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider') >
		   (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'output_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = requests.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
		)))
		OR (requests.billing_status = 'settled' AND (
			COALESCE(selected_usage.meter_count, 0) <> 4 OR COALESCE(selected_usage.row_count, 0) <> 4
			OR COALESCE(selected_usage.incomplete_count, 0) <> 0
			OR COALESCE(selected_usage.non_output_amount_mismatch_count, 0) <> 0
			OR NOT (selected_usage.sale_amount <=> CASE
				WHEN requests.execution_status = 'succeeded'
					AND selected_usage.input_quantity + selected_usage.output_quantity + selected_usage.cached_quantity + selected_usage.reasoning_quantity > 0
				THEN GREATEST(selected_usage.computed_base_amount,
					CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.minimum_charge')) AS DECIMAL(20,8)))
				ELSE selected_usage.computed_base_amount END)
			OR EXISTS (SELECT 1 FROM ai_usage_items AS sale_usage
				WHERE sale_usage.request_id = requests.request_id AND sale_usage.sequence_no = 1
				  AND sale_usage.source = selected_usage.selected_source
				  AND NOT (sale_usage.unit_price <=> CASE sale_usage.meter_type
					WHEN 'input_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.input_tokens.sale_unit_price')) AS DECIMAL(20,8))
					WHEN 'output_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.output_tokens.sale_unit_price')) AS DECIMAL(20,8))
					WHEN 'cached_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.cached_tokens.sale_unit_price')) AS DECIMAL(20,8))
					WHEN 'reasoning_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.skus.reasoning_tokens.sale_unit_price')) AS DECIMAL(20,8))
				  END))
			OR (selected_usage.selected_source = 'provider' AND (
				selected_usage.input_quantity + selected_usage.cached_quantity <>
					(SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'input_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
					 FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = requests.request_id
					   AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
				OR selected_usage.output_quantity + selected_usage.reasoning_quantity <>
					(SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'output_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
					 FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = requests.request_id
					   AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
			))
		))
	) THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS missing_usage,
	CAST(COALESCE(SUM(CASE WHEN requests.execution_status IN ('succeeded', 'failed', 'unknown')
		AND requests.billing_status IN ('unquoted', 'held', 'settlement_pending') AND requests.updated_at < ?
		THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS completed_pending,
	CAST(COALESCE(SUM(CASE WHEN requests.billing_status = 'exception' THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS billing_exception
FROM ai_requests AS requests
	LEFT JOIN selected_usage ON selected_usage.request_id = requests.request_id
	LEFT JOIN ai_price_versions AS price_versions ON price_versions.id = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.price_version_id')) AS UNSIGNED)
	LEFT JOIN ai_request_wallet_links AS links ON links.request_id = requests.request_id
	LEFT JOIN wallets AS wallets ON wallets.id = links.wallet_id
	LEFT JOIN wallet_holds AS holds ON holds.id = links.wallet_hold_id
	LEFT JOIN wallet_transactions AS freeze_transactions ON freeze_transactions.id = links.hold_transaction_id
	LEFT JOIN wallet_transactions AS transactions ON transactions.id = links.settle_transaction_id
	LEFT JOIN wallet_transactions AS release_transactions ON release_transactions.id = links.release_transaction_id
	CROSS JOIN duplicate_settlements`

// 明细 SQL 与聚合口径使用同一 selected_usage 规则；LIMIT 只限制输出，不影响上面的聚合失败判定。
const aiGatewayReconciliationIssuesSQL = `WITH selected_usage AS (
	SELECT usage_items.request_id, COALESCE(SUM(usage_items.amount), 0) AS sale_amount, MAX(usage_items.source) AS selected_source,
		COUNT(*) AS row_count,
		COUNT(DISTINCT CASE WHEN usage_items.meter_type IN ('input_tokens','output_tokens','cached_tokens','reasoning_tokens') THEN usage_items.meter_type END) AS meter_count,
		SUM(CASE WHEN usage_items.quantity < 0 OR usage_items.unit_price IS NULL OR usage_items.unit_price <= 0
			OR usage_items.amount IS NULL OR usage_items.amount < 0 THEN 1 ELSE 0 END) AS incomplete_count,
		SUM(CASE WHEN usage_items.meter_type = 'input_tokens' THEN usage_items.quantity ELSE 0 END) AS input_quantity,
		SUM(CASE WHEN usage_items.meter_type = 'output_tokens' THEN usage_items.quantity ELSE 0 END) AS output_quantity,
		SUM(CASE WHEN usage_items.meter_type = 'cached_tokens' THEN usage_items.quantity ELSE 0 END) AS cached_quantity,
		SUM(CASE WHEN usage_items.meter_type = 'reasoning_tokens' THEN usage_items.quantity ELSE 0 END) AS reasoning_quantity,
		COALESCE(SUM(CEIL(usage_items.quantity * usage_items.unit_price /
			NULLIF(CASE usage_items.meter_type
				WHEN 'input_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.input_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'output_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.output_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'cached_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.cached_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'reasoning_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.reasoning_tokens.scale')) AS DECIMAL(30,10))
			END, 0) * 100000000) / 100000000), 0) AS computed_base_amount,
		SUM(CASE WHEN usage_items.meter_type <> 'output_tokens' AND NOT (usage_items.amount <=>
			CEIL(usage_items.quantity * usage_items.unit_price /
			NULLIF(CASE usage_items.meter_type
				WHEN 'input_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.input_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'cached_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.cached_tokens.scale')) AS DECIMAL(30,10))
				WHEN 'reasoning_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(usage_requests.price_snapshot_json, '$.skus.reasoning_tokens.scale')) AS DECIMAL(30,10))
			END, 0) * 100000000) / 100000000)
			THEN 1 ELSE 0 END) AS non_output_amount_mismatch_count
	FROM ai_usage_items AS usage_items
	JOIN ai_requests AS usage_requests ON usage_requests.request_id = usage_items.request_id
	WHERE usage_items.sequence_no = 1
	  AND (usage_items.source = 'reconciled' OR (usage_items.source = 'provider' AND NOT EXISTS (
		SELECT 1 FROM ai_usage_items AS reconciled
		WHERE reconciled.request_id = usage_items.request_id AND reconciled.source = 'reconciled' AND reconciled.sequence_no = 1
	  )))
	GROUP BY usage_items.request_id
), request_facts AS (
	SELECT requests.request_id, requests.user_id AS request_user_id, requests.logical_model_code, requests.billing_status, requests.execution_status,
		requests.error_code, requests.updated_at,
		requests.quoted_amount, requests.settled_amount, requests.held_amount, requests.price_snapshot_json,
		price_versions.id AS price_version_fact_id, price_versions.version_no AS price_version_fact_version_no,
		price_versions.logical_model_code AS price_version_model_code, price_versions.currency AS price_version_currency,
		price_versions.exchange_rate AS price_version_exchange_rate, price_versions.rounding_mode AS price_version_rounding_mode,
		price_versions.failure_charge_policy AS price_version_failure_charge_policy,
		price_versions.max_input_tokens AS price_version_max_input_tokens, price_versions.max_output_tokens AS price_version_max_output_tokens,
		selected_usage.sale_amount, selected_usage.selected_source, selected_usage.row_count AS selected_usage_row_count,
		selected_usage.meter_count AS selected_usage_meter_count, selected_usage.incomplete_count AS selected_usage_incomplete_count,
		selected_usage.input_quantity AS selected_input_quantity, selected_usage.output_quantity AS selected_output_quantity,
		selected_usage.cached_quantity AS selected_cached_quantity, selected_usage.reasoning_quantity AS selected_reasoning_quantity,
		selected_usage.computed_base_amount AS selected_computed_base_amount,
		selected_usage.non_output_amount_mismatch_count AS selected_non_output_amount_mismatch_count,
		links.id AS link_id, links.wallet_id AS link_wallet_id, links.quoted_amount AS link_quoted_amount, links.held_amount AS link_held_amount,
		links.settled_amount AS link_settled_amount, links.hold_transaction_id, links.settle_transaction_id, links.release_transaction_id,
		wallets.id AS wallet_fact_id, wallets.user_id AS wallet_user_id,
		holds.wallet_id AS hold_wallet_id, holds.user_id AS hold_user_id,
		holds.hold_amount, holds.settled_amount AS hold_settled_amount, holds.status AS hold_status,
		holds.freeze_txn_id AS hold_freeze_transaction_id, holds.settle_txn_id AS hold_settle_transaction_id, holds.updated_at AS hold_updated_at,
		freeze_transactions.id AS freeze_transaction_id, freeze_transactions.wallet_id AS freeze_transaction_wallet_id,
		freeze_transactions.user_id AS freeze_transaction_user_id, freeze_transactions.amount AS freeze_transaction_amount,
		freeze_transactions.type AS freeze_transaction_type, freeze_transactions.direction AS freeze_transaction_direction,
		transactions.id AS transaction_id, transactions.wallet_id AS transaction_wallet_id,
		transactions.user_id AS transaction_user_id, transactions.amount AS transaction_amount,
		transactions.type AS transaction_type, transactions.direction AS transaction_direction,
		release_transactions.id AS release_transaction_fact_id, release_transactions.wallet_id AS release_transaction_wallet_id,
		release_transactions.user_id AS release_transaction_user_id, release_transactions.amount AS release_transaction_amount,
		release_transactions.type AS release_transaction_type, release_transactions.direction AS release_transaction_direction
	FROM ai_requests AS requests
	LEFT JOIN selected_usage ON selected_usage.request_id = requests.request_id
	LEFT JOIN ai_price_versions AS price_versions ON price_versions.id = CAST(JSON_UNQUOTE(JSON_EXTRACT(requests.price_snapshot_json, '$.price_version_id')) AS UNSIGNED)
	LEFT JOIN ai_request_wallet_links AS links ON links.request_id = requests.request_id
	LEFT JOIN wallets AS wallets ON wallets.id = links.wallet_id
	LEFT JOIN wallet_holds AS holds ON holds.id = links.wallet_hold_id
	LEFT JOIN wallet_transactions AS freeze_transactions ON freeze_transactions.id = links.hold_transaction_id
	LEFT JOIN wallet_transactions AS transactions ON transactions.id = links.settle_transaction_id
	LEFT JOIN wallet_transactions AS release_transactions ON release_transactions.id = links.release_transaction_id
), issues AS (
	SELECT request_id, 'duplicate_settlement' AS issue_code, 'settled' AS billing_status,
		'single_settlement' AS expected_value, CAST(COUNT(*) AS CHAR) AS actual_value
	FROM ai_request_wallet_links WHERE settle_transaction_id IS NOT NULL
	GROUP BY request_id HAVING COUNT(*) > 1
	UNION ALL
	SELECT links.request_id, 'duplicate_settlement', 'settled', 'unique_settlement_transaction', CAST(links.settle_transaction_id AS CHAR)
	FROM ai_request_wallet_links AS links
	JOIN (SELECT settle_transaction_id FROM ai_request_wallet_links WHERE settle_transaction_id IS NOT NULL GROUP BY settle_transaction_id HAVING COUNT(*) > 1) AS duplicates
	  ON duplicates.settle_transaction_id = links.settle_transaction_id
	UNION ALL
	SELECT request_id, 'request_usage_difference' AS issue_code, billing_status,
		CAST(COALESCE(settled_amount, 0) AS CHAR) AS expected_value, CAST(COALESCE(sale_amount, 0) AS CHAR) AS actual_value
	FROM request_facts WHERE billing_status = 'settled' AND COALESCE(settled_amount, 0) <> COALESCE(sale_amount, 0)
	UNION ALL
	SELECT request_id, 'request_hold_difference', billing_status,
		CAST(COALESCE(held_amount, 0) AS CHAR), CAST(COALESCE(link_held_amount, 0) AS CHAR)
	FROM request_facts WHERE link_id IS NOT NULL AND COALESCE(held_amount, 0) <> COALESCE(link_held_amount, 0)
	UNION ALL
	SELECT request_id, 'request_hold_difference', billing_status,
		CAST(COALESCE(link_held_amount, 0) AS CHAR), CAST(COALESCE(hold_amount, 0) AS CHAR)
	FROM request_facts WHERE link_id IS NOT NULL AND COALESCE(link_held_amount, 0) <> COALESCE(hold_amount, 0)
	UNION ALL
	SELECT request_id, 'request_hold_difference', billing_status,
		CAST(COALESCE(link_held_amount, 0) AS CHAR), CAST(COALESCE(release_transaction_amount, 0) AS CHAR)
	FROM request_facts WHERE billing_status IN ('settled', 'released')
		AND NOT (link_held_amount <=> release_transaction_amount)
	UNION ALL
	SELECT request_id, 'request_wallet_difference', billing_status,
		CAST(COALESCE(settled_amount, 0) AS CHAR), CAST(COALESCE(transaction_amount, 0) AS CHAR)
	FROM request_facts WHERE billing_status = 'settled' AND COALESCE(settled_amount, 0) <> COALESCE(transaction_amount, 0)
	UNION ALL
	SELECT request_id, 'missing_usage', billing_status, 'complete_provider_and_sales_usage', ''
	FROM request_facts WHERE (
		((execution_status = 'succeeded' OR billing_status = 'settled')
			AND NOT (billing_status = 'settled' AND selected_source = 'reconciled') AND (
			NOT (billing_status = 'released' AND error_code <=> 'manual_reconciled') AND (
		(SELECT COUNT(DISTINCT raw_usage.meter_type) FROM ai_usage_items AS raw_usage
		 WHERE raw_usage.request_id = request_facts.request_id AND raw_usage.sequence_no = 0
		   AND raw_usage.source = 'provider'
		   AND raw_usage.meter_type IN ('input_tokens','output_tokens','total_tokens')) <> 3
		OR EXISTS (SELECT 1 FROM ai_usage_items AS raw_usage
			WHERE raw_usage.request_id = request_facts.request_id AND raw_usage.sequence_no = 0
			  AND (raw_usage.source NOT IN ('provider','provider_cost') OR (raw_usage.source = 'provider'
				AND raw_usage.meter_type NOT IN ('input_tokens','output_tokens','total_tokens','cached_tokens','reasoning_tokens'))))
		OR EXISTS (SELECT 1 FROM ai_usage_items AS raw_usage
			WHERE raw_usage.request_id = request_facts.request_id AND raw_usage.sequence_no = 0
			  AND raw_usage.source = 'provider' AND raw_usage.quantity < 0)
		OR (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'total_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = request_facts.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider') <>
		   (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type IN ('input_tokens','output_tokens') THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = request_facts.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
		OR (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'cached_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = request_facts.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider') >
		   (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'input_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = request_facts.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
		OR (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'reasoning_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = request_facts.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider') >
		   (SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'output_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
			FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = request_facts.request_id
			  AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
		)))
		OR (billing_status = 'settled' AND (
			COALESCE(selected_usage_meter_count, 0) <> 4 OR COALESCE(selected_usage_row_count, 0) <> 4
			OR COALESCE(selected_usage_incomplete_count, 0) <> 0
			OR COALESCE(selected_non_output_amount_mismatch_count, 0) <> 0
			OR NOT (sale_amount <=> CASE
				WHEN execution_status = 'succeeded'
					AND selected_input_quantity + selected_output_quantity + selected_cached_quantity + selected_reasoning_quantity > 0
				THEN GREATEST(selected_computed_base_amount,
					CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.minimum_charge')) AS DECIMAL(20,8)))
				ELSE selected_computed_base_amount END)
			OR EXISTS (SELECT 1 FROM ai_usage_items AS sale_usage
				WHERE sale_usage.request_id = request_facts.request_id AND sale_usage.sequence_no = 1
				  AND sale_usage.source = selected_source
				  AND NOT (sale_usage.unit_price <=> CASE sale_usage.meter_type
					WHEN 'input_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.sale_unit_price')) AS DECIMAL(20,8))
					WHEN 'output_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.sale_unit_price')) AS DECIMAL(20,8))
					WHEN 'cached_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.sale_unit_price')) AS DECIMAL(20,8))
					WHEN 'reasoning_tokens' THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.sale_unit_price')) AS DECIMAL(20,8))
				  END))
			OR (selected_source = 'provider' AND (
				selected_input_quantity + selected_cached_quantity <>
					(SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'input_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
					 FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = request_facts.request_id
					   AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
				OR selected_output_quantity + selected_reasoning_quantity <>
					(SELECT COALESCE(SUM(CASE WHEN raw_usage.meter_type = 'output_tokens' THEN raw_usage.quantity ELSE 0 END), 0)
					 FROM ai_usage_items AS raw_usage WHERE raw_usage.request_id = request_facts.request_id
					   AND raw_usage.sequence_no = 0 AND raw_usage.source = 'provider')
			))
		)))
	UNION ALL
	SELECT request_id, 'unbilled_execution', billing_status, 'settled', billing_status
	FROM request_facts WHERE execution_status = 'succeeded'
		AND (billing_status = 'unquoted' OR (billing_status = 'released'
			AND NOT (error_code <=> 'output_moderation_blocked') AND NOT (error_code <=> 'manual_reconciled')))
		AND updated_at < ?
	UNION ALL
	SELECT request_id, 'missing_price_snapshot', billing_status, 'valid_json', ''
	FROM request_facts WHERE billing_status IN ('held', 'settlement_pending', 'settled', 'released', 'exception') AND (
		price_snapshot_json IS NULL OR JSON_VALID(price_snapshot_json) = 0
		OR price_version_fact_id IS NULL OR price_version_model_code <> logical_model_code OR price_version_currency <> 'CNY'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.price_version_id')), '') <> 'INTEGER'
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.price_version_id')) AS SIGNED), 0) <= 0
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.version_no')), '') <> 'INTEGER'
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.version_no')) AS UNSIGNED), 0) <> price_version_fact_version_no
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.logical_model_code')), '') <> 'STRING'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.logical_model_code')), '') <> logical_model_code
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.currency')), '') <> 'STRING'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.currency')), '') <> 'CNY'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.exchange_rate')), '') <> 'STRING'
		OR NOT (CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.exchange_rate')) AS DECIMAL(20,8)) <=> price_version_exchange_rate)
		OR NOT (price_version_exchange_rate <=> 1)
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.rounding_mode')), '') <> 'STRING'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.rounding_mode')), '') <> price_version_rounding_mode
		OR price_version_rounding_mode <> 'ceil_8'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.failure_charge_policy')), '') <> 'STRING'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.failure_charge_policy')), '') <> price_version_failure_charge_policy
		OR price_version_failure_charge_policy <> 'confirmed_usage'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.minimum_charge')), '') <> 'STRING'
		OR NOT (CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.minimum_charge')) AS DECIMAL(20,8)) <=> 0.00000100)
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.max_input_tokens')), '') <> 'INTEGER'
		OR CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.max_input_tokens')) AS UNSIGNED) <> price_version_max_input_tokens
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.max_output_tokens')), '') <> 'INTEGER'
		OR CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.max_output_tokens')) AS UNSIGNED) <> price_version_max_output_tokens
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus')), '') <> 'OBJECT'
		OR COALESCE(JSON_LENGTH(JSON_EXTRACT(price_snapshot_json, '$.skus')), 0) <> 4
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens')), '') <> 'OBJECT'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens')), '') <> 'OBJECT'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens')), '') <> 'OBJECT'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens')), '') <> 'OBJECT'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.meter_type')), '') <> 'input_tokens'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.meter_type')), '') <> 'output_tokens'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.meter_type')), '') <> 'cached_tokens'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.meter_type')), '') <> 'reasoning_tokens'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.sale_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.sale_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.sale_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.sale_unit_price')), '') <> 'STRING'
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.sale_unit_price')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.sale_unit_price')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.sale_unit_price')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.sale_unit_price')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.scale')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.scale')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.scale')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.scale')), '') <> 'STRING'
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.scale')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.scale')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.scale')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.scale')) AS DECIMAL(20,8)), 0) <= 0
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.currency')), '') <> 'CNY'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.currency')), '') <> 'CNY'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.currency')), '') <> 'CNY'
		OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.currency')), '') <> 'CNY'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.variant_hash')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.variant_hash')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.variant_hash')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.variant_hash')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.cost_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.cost_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.cost_unit_price')), '') <> 'STRING'
		OR COALESCE(JSON_TYPE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.cost_unit_price')), '') <> 'STRING'
		OR NOT EXISTS (SELECT 1 FROM ai_price_skus AS snapshot_sku WHERE snapshot_sku.price_version_id = price_version_fact_id
			AND snapshot_sku.meter_type = 'input_tokens'
			AND snapshot_sku.variant_hash = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.variant_hash')), '')
			AND snapshot_sku.cost_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.cost_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.sale_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.sale_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.scale = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.scale')) AS DECIMAL(30,10))
			AND snapshot_sku.currency = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.input_tokens.currency')), ''))
		OR NOT EXISTS (SELECT 1 FROM ai_price_skus AS snapshot_sku WHERE snapshot_sku.price_version_id = price_version_fact_id
			AND snapshot_sku.meter_type = 'output_tokens'
			AND snapshot_sku.variant_hash = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.variant_hash')), '')
			AND snapshot_sku.cost_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.cost_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.sale_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.sale_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.scale = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.scale')) AS DECIMAL(30,10))
			AND snapshot_sku.currency = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.output_tokens.currency')), ''))
		OR NOT EXISTS (SELECT 1 FROM ai_price_skus AS snapshot_sku WHERE snapshot_sku.price_version_id = price_version_fact_id
			AND snapshot_sku.meter_type = 'cached_tokens'
			AND snapshot_sku.variant_hash = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.variant_hash')), '')
			AND snapshot_sku.cost_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.cost_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.sale_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.sale_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.scale = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.scale')) AS DECIMAL(30,10))
			AND snapshot_sku.currency = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.cached_tokens.currency')), ''))
		OR NOT EXISTS (SELECT 1 FROM ai_price_skus AS snapshot_sku WHERE snapshot_sku.price_version_id = price_version_fact_id
			AND snapshot_sku.meter_type = 'reasoning_tokens'
			AND snapshot_sku.variant_hash = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.variant_hash')), '')
			AND snapshot_sku.cost_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.cost_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.sale_unit_price = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.sale_unit_price')) AS DECIMAL(20,8))
			AND snapshot_sku.scale = CAST(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.scale')) AS DECIMAL(30,10))
			AND snapshot_sku.currency = COALESCE(JSON_UNQUOTE(JSON_EXTRACT(price_snapshot_json, '$.skus.reasoning_tokens.currency')), '')))
	UNION ALL
	SELECT request_id, 'missing_wallet_transaction', billing_status, 'request=link=hold=transaction', 'inconsistent_or_missing'
	FROM request_facts WHERE
		(link_id IS NOT NULL AND (
			wallet_fact_id IS NULL OR wallet_user_id <> request_user_id
			OR quoted_amount IS NULL OR quoted_amount <= 0
			OR held_amount IS NULL OR held_amount < quoted_amount
			OR link_quoted_amount <= 0 OR link_held_amount < link_quoted_amount
			OR NOT (link_quoted_amount <=> quoted_amount)
			OR freeze_transaction_id IS NULL OR NOT (hold_freeze_transaction_id <=> hold_transaction_id)
			OR hold_wallet_id <> link_wallet_id OR hold_user_id <> request_user_id
			OR freeze_transaction_wallet_id <> link_wallet_id OR freeze_transaction_user_id <> request_user_id
			OR freeze_transaction_type <> 'freeze' OR freeze_transaction_direction <> 'out'
			OR NOT (freeze_transaction_amount <=> link_held_amount)
		))
		OR (billing_status IN ('held', 'settlement_pending', 'settled', 'released', 'exception') AND link_id IS NULL)
		OR (billing_status IN ('held', 'settlement_pending', 'exception') AND (
			hold_status <> 'holding' OR settled_amount IS NOT NULL OR link_settled_amount IS NOT NULL
			OR hold_settled_amount IS NOT NULL OR settle_transaction_id IS NOT NULL OR hold_settle_transaction_id IS NOT NULL
			OR release_transaction_id IS NOT NULL
		))
		OR (billing_status IN ('settled', 'released') AND (
			settled_amount IS NULL
			OR settled_amount < 0 OR settled_amount > held_amount
			OR link_settled_amount IS NULL OR NOT (link_settled_amount <=> settled_amount)
			OR hold_settled_amount IS NULL OR NOT (hold_settled_amount <=> settled_amount)
			OR release_transaction_id IS NULL OR release_transaction_fact_id IS NULL
			OR release_transaction_wallet_id <> link_wallet_id OR release_transaction_user_id <> request_user_id
			OR release_transaction_type <> 'unfreeze' OR release_transaction_direction <> 'in'
			OR NOT (release_transaction_amount <=> link_held_amount)
			OR (billing_status = 'settled' AND (
				hold_status <> 'settled'
				OR (settled_amount > 0 AND (
					settle_transaction_id IS NULL OR transaction_id IS NULL
					OR transaction_wallet_id <> link_wallet_id OR transaction_user_id <> request_user_id
					OR transaction_type <> 'consume' OR transaction_direction <> 'out'
					OR NOT (transaction_amount <=> settled_amount)
					OR NOT (hold_settle_transaction_id <=> settle_transaction_id)
				))
				OR (settled_amount = 0 AND (settle_transaction_id IS NOT NULL OR hold_settle_transaction_id IS NOT NULL))
			))
			OR (billing_status = 'released' AND (
				hold_status <> 'released' OR NOT (settled_amount <=> 0)
				OR settle_transaction_id IS NOT NULL OR hold_settle_transaction_id IS NOT NULL
			))
		))
	UNION ALL
	SELECT request_id, 'unreleased_hold', billing_status, 'released_or_settled', hold_status
	FROM request_facts WHERE hold_status = 'holding' AND hold_updated_at < ?
	UNION ALL
	SELECT request_id, 'completed_pending', billing_status, 'settled_or_released', billing_status
	FROM request_facts WHERE execution_status IN ('succeeded', 'failed', 'unknown')
		AND billing_status IN ('unquoted', 'held', 'settlement_pending') AND updated_at < ?
	UNION ALL
	SELECT request_id, 'billing_exception', billing_status, 'settled_or_released', billing_status
	FROM request_facts WHERE billing_status = 'exception'
	UNION ALL
	SELECT aggregate_id, 'outbox_unsettled', status, 'published', status
	FROM ai_outbox_events WHERE status IN ('pending', 'publishing', 'dead')
	UNION ALL
	SELECT aggregate_id, 'compensation_unsettled', status, 'done', status
	FROM ai_compensation_tasks WHERE status IN ('pending', 'retry', 'dead', 'manual_review')
)
SELECT request_id, issue_code, billing_status, expected_value, actual_value
FROM issues ORDER BY issue_code, request_id LIMIT ?`
