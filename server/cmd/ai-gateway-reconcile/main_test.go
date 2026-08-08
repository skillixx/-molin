package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/service"
)

func TestBuildReportPassesOnlyWhenEveryFinancialCheckIsZero(t *testing.T) {
	snapshot := service.AIGatewayGaugeSnapshot{
		BillingRequests:     map[string]uint64{"settled": 3},
		BillingOldestAge:    map[string]uint64{"settled": 1},
		UnreleasedHolds:     service.AIGatewayAmountGauge{},
		OutboxBacklog:       map[string]service.AIGatewayBacklogGauge{},
		CompensationBacklog: map[string]service.AIGatewayBacklogGauge{},
		BillingDifferences: map[string]decimal.Decimal{
			"request_usage": decimal.Zero, "request_hold": decimal.Zero, "request_wallet": decimal.Zero,
		},
		BillingAnomalies: map[string]uint64{
			"duplicate_settlement": 0, "unbilled_execution": 0, "missing_price_snapshot": 0, "missing_wallet_transaction": 0,
		},
	}
	report := buildReport(snapshot, nil, time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC))
	if report.Status != "PASS" || report.HasMismatch {
		t.Fatalf("零差额、零异常必须通过: %+v", report)
	}
	if len(report.Checks) != 13 {
		t.Fatalf("必须覆盖三段差额、七类账务异常、预占及两类积压，实际 %d 项", len(report.Checks))
	}
	var out bytes.Buffer
	if err := renderReport(&out, report, "json"); err != nil {
		t.Fatal(err)
	}
	var decoded reconciliationReport
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.Mode != "read_only" || decoded.DifferencesCNY["request_wallet"] != "0.00000000" {
		t.Fatalf("机器可读 JSON 契约错误: decoded=%+v err=%v output=%s", decoded, err, out.String())
	}
}

func TestBuildReportFailsOnAnyDifferenceOrAnomaly(t *testing.T) {
	snapshot := service.AIGatewayGaugeSnapshot{
		BillingRequests:     map[string]uint64{},
		BillingOldestAge:    map[string]uint64{},
		OutboxBacklog:       map[string]service.AIGatewayBacklogGauge{},
		CompensationBacklog: map[string]service.AIGatewayBacklogGauge{},
		BillingDifferences:  map[string]decimal.Decimal{"request_usage": decimal.RequireFromString("0.01000000")},
		BillingAnomalies:    map[string]uint64{"missing_wallet_transaction": 1},
	}
	report := buildReport(snapshot, []service.AIGatewayReconciliationIssue{{RequestID: "req-bad", IssueCode: "missing_wallet_transaction"}}, time.Now().UTC())
	if report.Status != "FAIL" || !report.HasMismatch {
		t.Fatalf("任一非零差额或异常必须失败: %+v", report)
	}
	var out bytes.Buffer
	if err := renderReport(&out, report, "summary"); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"只读对账", "FAIL", "账本↔Usage", "缺失钱包结算流水"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("中文摘要缺少 %q:\n%s", expected, out.String())
		}
	}
}

func TestBuildReportFailsOnHoldsAndBacklogs(t *testing.T) {
	snapshot := service.AIGatewayGaugeSnapshot{
		BillingRequests: map[string]uint64{}, BillingOldestAge: map[string]uint64{},
		UnreleasedHolds:     service.AIGatewayAmountGauge{Count: 1, Amount: decimal.RequireFromString("0.5")},
		OutboxBacklog:       map[string]service.AIGatewayBacklogGauge{"pending": {Count: 2}},
		CompensationBacklog: map[string]service.AIGatewayBacklogGauge{"retry": {Count: 3}},
		BillingDifferences:  map[string]decimal.Decimal{}, BillingAnomalies: map[string]uint64{},
	}
	report := buildReport(snapshot, nil, time.Now().UTC())
	if report.Status != "FAIL" || !report.HasMismatch {
		t.Fatalf("未释放 hold、Outbox 或补偿积压均不得误报 PASS: %+v", report)
	}
}

func TestValidateSafetyGateRequiresExplicitNonProductionApproval(t *testing.T) {
	for _, test := range []struct {
		name, appEnv, approved string
		wantErr                bool
	}{
		{name: "测试环境显式批准", appEnv: "test", approved: "YES"},
		{name: "开发环境显式批准", appEnv: " development ", approved: "YES"},
		{name: "缺少批准", appEnv: "test", approved: "", wantErr: true},
		{name: "批准值区分大小写", appEnv: "test", approved: "yes", wantErr: true},
		{name: "批准值拒绝空白", appEnv: "test", approved: " YES ", wantErr: true},
		{name: "生产环境拒绝", appEnv: "production", approved: "YES", wantErr: true},
		{name: "生产缩写拒绝", appEnv: "prod", approved: "YES", wantErr: true},
		{name: "预发布环境拒绝", appEnv: "staging", approved: "YES", wantErr: true},
		{name: "未知环境拒绝", appEnv: "sandbox", approved: "YES", wantErr: true},
		{name: "缺少环境", appEnv: "", approved: "YES", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateSafetyGate(test.appEnv, test.approved); (err != nil) != test.wantErr {
				t.Fatalf("安全门结果错误: err=%v wantErr=%v", err, test.wantErr)
			}
		})
	}
}
