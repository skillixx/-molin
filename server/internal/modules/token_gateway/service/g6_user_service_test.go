package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestPublicCatalogDTOOnlyContainsSalePrice(t *testing.T) {
	now := time.Now().UTC()
	description := "公开模型"
	row := repository.PublishedCatalogRow{
		Model:   model.TokenModel{DisplayName: "未发布草稿名称", IntroURLHealthStatus: "unpublished", DocsURLHealthStatus: "healthy", QuickStartURLHealthStatus: "healthy"},
		Release: model.AIModelReleaseVersion{VersionNo: 2, PublishedAt: now},
		Snapshot: model.TokenModelReleaseSnapshot{LogicalModelCode: "molin/test", DisplayName: "测试模型", ProviderName: "测试厂商", Description: &description,
			Capabilities: json.RawMessage(`{"stream":true}`), ContextWindow: 128000, Modality: "chat", VisibleScope: "all", IntroURLHealthStatus: "unpublished", DocsURLHealthStatus: "healthy", QuickStartURLHealthStatus: "healthy"},
		PriceVersion: model.AIPriceVersion{VersionNo: 3, Currency: "CNY", EffectiveAt: now, FailureChargePolicy: "confirmed_usage", RoundingMode: "ceil_8"},
		PriceSKUs:    []model.AIPriceSKU{{MeterType: "input_tokens", CostUnitPrice: decimal.RequireFromString("1.25"), SaleUnitPrice: decimal.RequireFromString("2.50"), Scale: decimal.NewFromInt(1000000), Currency: "CNY"}},
	}
	got := publicCatalogDTO(row)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || containsAny(string(raw), "cost_unit_price", "1.25", "upstream_model", "channel_id") {
		t.Fatalf("用户目录泄漏成本或内部路由: %s", raw)
	}
	if !containsAny(string(raw), "sale_unit_price") {
		t.Fatalf("用户目录缺少销售价格: %s", raw)
	}
	if containsAny(string(raw), "未发布草稿名称") {
		t.Fatalf("用户目录泄漏尚未发布的工作副本: %s", raw)
	}
}

func TestEffectiveLimitUsesOwnedPolicyOverride(t *testing.T) {
	defaults := ResourceLimits{Concurrency: 5, RPM: 60, TPM: 100000}
	policies := map[string]model.AIResourcePolicy{"api_key:9": {ConcurrencyLimit: 2, RPMLimit: 20, TPMLimit: 30000}}
	got := effectiveLimit("api_key", 9, "生产密钥", defaults, policies)
	if got.Concurrency != 2 || got.RPM != 20 || got.TPM != 30000 || got.Source != "policy_override" {
		t.Fatalf("有效限制未应用覆盖策略: %+v", got)
	}
}

func TestEffectiveLimitIsClampedByParent(t *testing.T) {
	parent := effectiveLimit("user", 5, "本人", ResourceLimits{Concurrency: 2, RPM: 30, TPM: 50000}, nil)
	child := effectiveLimit("project", 9, "生产", ResourceLimits{Concurrency: 10, RPM: 100, TPM: 200000}, nil)
	got := clampLimit(child, parent)
	if got.Concurrency != 2 || got.RPM != 30 || got.TPM != 50000 || got.Source != "inherited_parent" {
		t.Fatalf("子级展示必须反映父级实际门禁: %+v", got)
	}
}

func TestCapabilityFilterRequiresEnabledStructuredValue(t *testing.T) {
	if capabilityEnabled(json.RawMessage(`{"reasoning":false}`), "reasoning") {
		t.Fatal("显式关闭的能力不得被子串筛选命中")
	}
	if !capabilityEnabled(json.RawMessage(`{"reasoning":true}`), "reasoning") || !capabilityEnabled(json.RawMessage(`["stream","tool"]`), "tool") {
		t.Fatal("已启用的对象或数组能力应被命中")
	}
}

func TestDisplayedBudgetIncludesActiveOverride(t *testing.T) {
	daily := decimal.NewFromInt(10)
	monthly := decimal.NewFromInt(100)
	limit := effectiveLimit("project", 9, "生产", ResourceLimits{}, nil)
	applyBudget(&limit, model.AIBudgetPolicy{ID: 1, Mode: model.AIBudgetHard, DailyLimit: &daily, MonthlyLimit: &monthly}, decimal.NewFromInt(5))
	if limit.DailyBudget == nil || !limit.DailyBudget.Equal(decimal.NewFromInt(15)) || limit.MonthlyBudget == nil || !limit.MonthlyBudget.Equal(decimal.NewFromInt(105)) {
		t.Fatalf("用户展示预算必须与执行链路的临时增额一致: %+v", limit)
	}
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if len(candidate) > 0 && len(value) >= len(candidate) {
			for i := 0; i+len(candidate) <= len(value); i++ {
				if value[i:i+len(candidate)] == candidate {
					return true
				}
			}
		}
	}
	return false
}
