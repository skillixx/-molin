package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/dto"
)

func TestG5AdminImagePriceOnlyAcceptsFrozenMVPContract(t *testing.T) {
	service := NewG5AdminService(nil, nil)
	now := time.Now().UTC()
	frozenVariant := json.RawMessage(`{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}`)
	base := dto.CreatePriceReq{
		LogicalModelCode: "molin/image", Capability: "image.generate", PricingTemplate: "image_variant",
		MinMarginRate: "0.20", Limits: json.RawMessage(`{"max_count":1,"variants":[{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}]}`),
		MinimumCharge: "0.01", CostSource: "test_fixture", CostSourceVersion: "mvp-fixture", PricePurpose: "test_fixture",
		CostUpdatedAt: now, CostExpiresAt: now.Add(time.Hour), EffectiveAt: now,
		SKUs: []dto.PriceSKUReq{{MeterType: "image_count", Variant: frozenVariant, CostUnitPrice: "0.30", SaleUnitPrice: "0.50", Scale: "1"}},
	}
	tests := []struct {
		name   string
		limits json.RawMessage
	}{
		{name: "禁止max_count大于一", limits: json.RawMessage(`{"max_count":2,"variants":[{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}]}`)},
		{name: "禁止多个variant", limits: json.RawMessage(`{"max_count":1,"variants":[{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"},{"resolution":"1K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}]}`)},
		{name: "禁止其他尺寸", limits: json.RawMessage(`{"max_count":1,"variants":[{"resolution":"1K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}]}`)},
		{name: "禁止其他质量", limits: json.RawMessage(`{"max_count":1,"variants":[{"resolution":"2K","aspect_ratio":"1:1","quality":"hd","output_format":"provider_default","delivery":"url"}]}`)},
		{name: "禁止其他输出", limits: json.RawMessage(`{"max_count":1,"variants":[{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"png","delivery":"url"}]}`)},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			request := base
			request.Limits = item.limits
			if _, err := service.CreatePrice(context.Background(), 1, request); !IsValidation(err) {
				t.Fatalf("管理价格越出冻结规格必须拒绝: %v", err)
			}
		})
	}
}

func TestG5AdminRejectsRouteWithoutExplicitProvider(t *testing.T) {
	svc := NewG5AdminService(nil, nil)
	_, err := svc.CreateRoute(context.Background(), 1, dto.RouteWriteReq{LogicalModelCode: "molin/test", ChannelID: 1, ProviderModel: "missing-provider", Weight: 100, TimeoutMS: 30000, CircuitBreakerThreshold: 5, Status: "active"})
	if !IsValidation(err) {
		t.Fatalf("未显式 provider/model 必须被拒绝: %v", err)
	}
}

func TestG5AdminRejectsUnsafeRetryAndTimeout(t *testing.T) {
	svc := NewG5AdminService(nil, nil)
	_, err := svc.CreateRoute(context.Background(), 1, dto.RouteWriteReq{LogicalModelCode: "molin/test", ChannelID: 1, ProviderModel: "openrouter/test", Weight: 100, TimeoutMS: 500, MaxRetries: 4, CircuitBreakerThreshold: 5, Status: "active"})
	if !IsValidation(err) {
		t.Fatalf("越界超时和重试必须被拒绝: %v", err)
	}
}

func TestG5AdminPriceRequiresFourMetersAndMargin(t *testing.T) {
	svc := NewG5AdminService(nil, nil)
	now := time.Now().UTC()
	_, err := svc.CreatePrice(context.Background(), 1, dto.CreatePriceReq{LogicalModelCode: "molin/test", MinMarginRate: "0.20", MaxInputTokens: 1000, MaxOutputTokens: 100,
		CostUpdatedAt: now, CostExpiresAt: now.Add(time.Hour), EffectiveAt: now,
		SKUs: []dto.PriceSKUReq{{MeterType: "input_tokens", CostUnitPrice: "1.00000000", SaleUnitPrice: "1.10000000", Scale: "1000000"}}})
	if !IsValidation(err) {
		t.Fatalf("缺少四项计量必须被拒绝: %v", err)
	}
}

func TestG5AdminPublishReasonIsRequired(t *testing.T) {
	svc := NewG5AdminService(nil, nil)
	_, err := svc.PublishModel(context.Background(), 1, 1, "   ")
	if !IsValidation(err) {
		t.Fatalf("空发布原因必须被拒绝: %v", err)
	}
}

func TestG5AdminRollbackRequiresTargetAndReason(t *testing.T) {
	svc := NewG5AdminService(nil, nil)
	if _, err := svc.RollbackModel(context.Background(), 1, 1, dto.RollbackModelReq{TargetVersionNo: 0, Reason: "回滚"}); !IsValidation(err) {
		t.Fatalf("缺少目标版本必须被拒绝: %v", err)
	}
	if _, err := svc.RollbackModel(context.Background(), 1, 1, dto.RollbackModelReq{TargetVersionNo: 2, Reason: "   "}); !IsValidation(err) {
		t.Fatalf("缺少回滚原因必须被拒绝: %v", err)
	}
}

func TestG5AdminPriceRollbackRequiresFutureCostWindow(t *testing.T) {
	svc := NewG5AdminService(nil, nil)
	_, err := svc.RollbackPrice(context.Background(), 1, 1, dto.RollbackPriceReq{Reason: "恢复历史价格", EffectiveAt: time.Now().UTC(), CostExpiresAt: time.Now().UTC().Add(-time.Minute)})
	if !IsValidation(err) {
		t.Fatalf("成本有效期已经过期时必须拒绝回滚: %v", err)
	}
}

func TestG5DashboardRejectsInvalidWindowAndStatus(t *testing.T) {
	svc := NewG5AdminService(nil, nil)
	now := time.Now().UTC()
	if _, err := svc.Dashboard(context.Background(), dto.G5DashboardQuery{From: now, To: now.Add(91 * 24 * time.Hour)}); !IsValidation(err) {
		t.Fatalf("超过 90 天的概览查询必须拒绝: %v", err)
	}
	if _, err := svc.Dashboard(context.Background(), dto.G5DashboardQuery{From: now.Add(-time.Hour), To: now, Status: "success"}); !IsValidation(err) {
		t.Fatalf("非冻结执行状态必须拒绝: %v", err)
	}
}
