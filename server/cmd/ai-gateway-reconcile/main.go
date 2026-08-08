// 命令 ai-gateway-reconcile 在显式非生产环境中执行 AI 网关只读财务核对。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/config"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/db"
)

var errReconciliationMismatch = errors.New("AI 网关只读对账发现非零差额或异常事实")

type reconciliationCheck struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Passed bool   `json:"passed"`
}

type reportAmountGauge struct {
	Count  uint64 `json:"count"`
	Amount string `json:"amount_cny"`
}

type reportBacklogGauge struct {
	Count            uint64 `json:"count"`
	OldestAgeSeconds uint64 `json:"oldest_age_seconds"`
}

type reconciliationReport struct {
	GeneratedAt         time.Time                     `json:"generated_at"`
	Mode                string                        `json:"mode"`
	Status              string                        `json:"status"`
	HasMismatch         bool                          `json:"has_mismatch"`
	DifferencesCNY      map[string]string             `json:"differences_cny"`
	Anomalies           map[string]uint64             `json:"anomalies"`
	BillingRequests     map[string]uint64             `json:"billing_requests"`
	BillingOldestAge    map[string]uint64             `json:"billing_oldest_age_seconds"`
	UnreleasedHolds     reportAmountGauge             `json:"unreleased_holds"`
	OutboxBacklog       map[string]reportBacklogGauge `json:"outbox_backlog"`
	CompensationBacklog map[string]reportBacklogGauge `json:"compensation_backlog"`
	Checks              []reconciliationCheck         `json:"checks"`
	SafetyStatement     string                        `json:"safety_statement"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "AI 网关只读对账失败：%v\n", err)
		if errors.Is(err, errReconciliationMismatch) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("ai-gateway-reconcile", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	format := flags.String("format", "summary", "输出格式：summary 或 json")
	timeout := flags.Duration("timeout", 30*time.Second, "只读对账超时")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 || *timeout > 5*time.Minute {
		return errors.New("timeout 必须大于 0 且不超过 5 分钟")
	}
	rawAppEnv, appEnvSet := os.LookupEnv("APP_ENV")
	if !appEnvSet {
		rawAppEnv = ""
	}
	if err := validateSafetyGate(rawAppEnv, os.Getenv("AI_GATEWAY_RECONCILE_READ_ONLY")); err != nil {
		return err
	}
	cfg := config.Load()
	gormDB, err := db.New(cfg.MySQLHost, cfg.MySQLPort, cfg.MySQLUser, cfg.MySQLPassword, cfg.MySQLDatabase)
	if err != nil {
		return fmt.Errorf("连接测试数据库失败：%w", err)
	}
	if sqlDB, dbErr := gormDB.DB(); dbErr == nil {
		defer func() { _ = sqlDB.Close() }()
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	now := time.Now().UTC()
	var snapshot service.AIGatewayGaugeSnapshot
	// MySQL READ ONLY 事务从数据库侧阻止 INSERT、UPDATE、DELETE 和 DDL，命令本身不具备修账路径。
	if err := gormDB.WithContext(ctx).Transaction(func(txDB *gorm.DB) error {
		var collectErr error
		snapshot, collectErr = service.NewAIGatewayDBGaugeCollector(txDB).CollectAIGatewayGauges(ctx, now)
		return collectErr
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}); err != nil {
		return fmt.Errorf("读取对账事实失败：%w", err)
	}
	report := buildReport(snapshot, now)
	if err := renderReport(output, report, strings.ToLower(strings.TrimSpace(*format))); err != nil {
		return err
	}
	if report.HasMismatch {
		return errReconciliationMismatch
	}
	return nil
}

func validateSafetyGate(appEnv, approved string) error {
	// 批准值必须是精确原始字节，不能容忍大小写或首尾空白，避免宽松解析掩盖错误的运维注入。
	if approved != "YES" {
		return errors.New("必须显式设置 AI_GATEWAY_RECONCILE_READ_ONLY=YES")
	}
	if strings.TrimSpace(appEnv) == "" {
		return errors.New("必须显式设置非生产 APP_ENV")
	}
	if !(config.Config{AppEnv: appEnv}).IsSafeNonProduction() {
		return errors.New("该命令只允许在明确的 local、development、dev、test 或 testing 环境运行")
	}
	return nil
}

func buildReport(snapshot service.AIGatewayGaugeSnapshot, generatedAt time.Time) reconciliationReport {
	differenceNames := map[string]string{
		"request_usage":  "账本↔Usage",
		"request_hold":   "账本↔钱包预占",
		"request_wallet": "账本↔钱包消费流水",
	}
	anomalyNames := map[string]string{
		"duplicate_settlement":       "重复结算",
		"unbilled_execution":         "执行成功但未结算",
		"missing_price_snapshot":     "缺失冻结价格快照",
		"missing_wallet_transaction": "缺失钱包结算流水",
	}
	differenceOrder := []string{"request_usage", "request_hold", "request_wallet"}
	anomalyOrder := []string{"duplicate_settlement", "unbilled_execution", "missing_price_snapshot", "missing_wallet_transaction"}
	report := reconciliationReport{
		GeneratedAt: generatedAt.UTC(), Mode: "read_only", Status: "PASS",
		DifferencesCNY:   make(map[string]string, len(differenceOrder)),
		Anomalies:        cloneUintMap(snapshot.BillingAnomalies),
		BillingRequests:  cloneUintMap(snapshot.BillingRequests),
		BillingOldestAge: cloneUintMap(snapshot.BillingOldestAge),
		UnreleasedHolds:  reportAmountGauge{Count: snapshot.UnreleasedHolds.Count, Amount: snapshot.UnreleasedHolds.Amount.StringFixed(8)},
		OutboxBacklog:    convertBacklog(snapshot.OutboxBacklog), CompensationBacklog: convertBacklog(snapshot.CompensationBacklog),
		SafetyStatement: "本报告来自 MySQL READ ONLY 事务；命令不会修账、退款、补扣、释放预占、重排任务或修改任何业务状态。",
	}
	for _, code := range differenceOrder {
		value := snapshot.BillingDifferences[code].Abs()
		report.DifferencesCNY[code] = value.StringFixed(8)
		passed := value.IsZero()
		report.Checks = append(report.Checks, reconciliationCheck{Code: code, Name: differenceNames[code], Kind: "difference_cny", Value: value.StringFixed(8), Passed: passed})
		if !passed {
			report.HasMismatch = true
		}
	}
	for _, code := range anomalyOrder {
		value := snapshot.BillingAnomalies[code]
		report.Anomalies[code] = value
		passed := value == 0
		report.Checks = append(report.Checks, reconciliationCheck{Code: code, Name: anomalyNames[code], Kind: "anomaly_count", Value: fmt.Sprintf("%d", value), Passed: passed})
		if !passed {
			report.HasMismatch = true
		}
	}
	if report.HasMismatch {
		report.Status = "FAIL"
	}
	return report
}

func renderReport(output io.Writer, report reconciliationReport, format string) error {
	switch format {
	case "json":
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	case "summary":
		if _, err := fmt.Fprintf(output, "墨灵 AI 网关 G7 只读对账：%s\n生成时间：%s\n", report.Status, report.GeneratedAt.Format(time.RFC3339)); err != nil {
			return err
		}
		for _, check := range report.Checks {
			state := "PASS"
			if !check.Passed {
				state = "FAIL"
			}
			unit := "项"
			if check.Kind == "difference_cny" {
				unit = "CNY"
			}
			if _, err := fmt.Fprintf(output, "[%s] %s：%s %s\n", state, check.Name, check.Value, unit); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(output, "未释放预占：%d 笔 / %s CNY\n安全声明：%s\n", report.UnreleasedHolds.Count, report.UnreleasedHolds.Amount, report.SafetyStatement)
		return err
	default:
		return errors.New("format 只支持 summary 或 json")
	}
}

func cloneUintMap(source map[string]uint64) map[string]uint64 {
	result := make(map[string]uint64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func convertBacklog(source map[string]service.AIGatewayBacklogGauge) map[string]reportBacklogGauge {
	result := make(map[string]reportBacklogGauge, len(source))
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := source[key]
		result[key] = reportBacklogGauge{Count: value.Count, OldestAgeSeconds: value.OldestAgeSeconds}
	}
	return result
}
