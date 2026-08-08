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
