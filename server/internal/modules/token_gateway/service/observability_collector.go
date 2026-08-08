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
	Count  uint64
	Amount string
}

type aiGatewayBacklogRow struct {
	Status           string
	Count            uint64
	OldestAgeSeconds uint64
}

type aiGatewayReconciliationRow struct {
	RequestUsageDifference   string
	RequestHoldDifference    string
	RequestWalletDifference  string
	DuplicateSettlement      uint64
	UnbilledExecution        uint64
	MissingPriceSnapshot     uint64
	MissingWalletTransaction uint64
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
		SELECT COUNT(*) AS count, CAST(COALESCE(SUM(holds.hold_amount), 0) AS CHAR) AS amount
		FROM wallet_holds AS holds
		JOIN ai_request_wallet_links AS links ON links.wallet_hold_id = holds.id
		WHERE holds.status = ?`, "holding").Scan(&holdRow).Error; err != nil {
		return snapshot, err
	}
	holdAmount, err := decimal.NewFromString(holdRow.Amount)
	if err != nil {
		return snapshot, err
	}
	snapshot.UnreleasedHolds = AIGatewayAmountGauge{Count: holdRow.Count, Amount: holdAmount}

	if err := c.collectBacklog(ctx, "ai_outbox_events", outboxStatuses, now, snapshot.OutboxBacklog); err != nil {
		return snapshot, err
	}
	if err := c.collectBacklog(ctx, "ai_compensation_tasks", compensationStatuses, now, snapshot.CompensationBacklog); err != nil {
		return snapshot, err
	}

	var reconciliation aiGatewayReconciliationRow
	if err := c.db.WithContext(ctx).Raw(aiGatewayReconciliationSQL, now.Add(-unbilledExecutionGracePeriod)).Scan(&reconciliation).Error; err != nil {
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
	return snapshot, nil
}

func emptyAIGatewayGaugeSnapshot() AIGatewayGaugeSnapshot {
	return AIGatewayGaugeSnapshot{
		BillingRequests:     make(map[string]uint64),
		BillingOldestAge:    make(map[string]uint64),
		OutboxBacklog:       make(map[string]AIGatewayBacklogGauge),
		CompensationBacklog: make(map[string]AIGatewayBacklogGauge),
		BillingDifferences:  make(map[string]decimal.Decimal),
		BillingAnomalies:    make(map[string]uint64),
	}
}

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
	SELECT usage_items.request_id, COALESCE(SUM(usage_items.amount), 0) AS sale_amount
	FROM ai_usage_items AS usage_items
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
		THEN ABS(COALESCE(requests.held_amount, 0) - links.held_amount) + ABS(links.held_amount - COALESCE(holds.hold_amount, 0)) ELSE 0 END), 0) AS CHAR) AS request_hold_difference,
	CAST(COALESCE(SUM(CASE WHEN requests.billing_status = 'settled'
		THEN ABS(COALESCE(requests.settled_amount, 0) - COALESCE(transactions.amount, 0)) ELSE 0 END), 0) AS CHAR) AS request_wallet_difference,
	MAX(duplicate_settlements.count) AS duplicate_settlement,
	CAST(COALESCE(SUM(CASE WHEN requests.execution_status = 'succeeded' AND requests.billing_status <> 'settled' AND requests.updated_at < ? THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS unbilled_execution,
	CAST(COALESCE(SUM(CASE WHEN requests.billing_status IN ('held', 'settlement_pending', 'settled') AND (requests.price_snapshot_json IS NULL OR JSON_VALID(requests.price_snapshot_json) = 0) THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS missing_price_snapshot,
	CAST(COALESCE(SUM(CASE WHEN requests.billing_status = 'settled' AND (
		links.id IS NULL OR links.settle_transaction_id IS NULL OR transactions.id IS NULL
		OR transactions.type <> 'consume' OR transactions.direction <> 'out'
		OR transactions.amount <> COALESCE(requests.settled_amount, 0)
		OR links.settled_amount <> COALESCE(requests.settled_amount, 0)
		OR holds.status <> 'settled' OR holds.settle_txn_id <> links.settle_transaction_id
	) THEN 1 ELSE 0 END), 0) AS UNSIGNED) AS missing_wallet_transaction
FROM ai_requests AS requests
LEFT JOIN selected_usage ON selected_usage.request_id = requests.request_id
LEFT JOIN ai_request_wallet_links AS links ON links.request_id = requests.request_id
LEFT JOIN wallet_holds AS holds ON holds.id = links.wallet_hold_id
LEFT JOIN wallet_transactions AS transactions ON transactions.id = links.settle_transaction_id
CROSS JOIN duplicate_settlements`
