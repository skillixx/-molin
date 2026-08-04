package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// G5AdminService 编排模型、价格和路由的受控管理动作。
type G5AdminService struct {
	repo    *repository.G5AdminRepository
	pricing *repository.G3PricingRepository
}

func NewG5AdminService(repo *repository.G5AdminRepository, pricing *repository.G3PricingRepository) *G5AdminService {
	return &G5AdminService{repo: repo, pricing: pricing}
}

func (s *G5AdminService) Dashboard(ctx context.Context, query dto.G5DashboardQuery) (dto.G5DashboardResp, error) {
	query.Model, query.Status = strings.TrimSpace(query.Model), strings.TrimSpace(query.Status)
	if query.From.IsZero() || query.To.IsZero() || !query.To.After(query.From) || query.To.Sub(query.From) > 90*24*time.Hour {
		return dto.G5DashboardResp{}, newValidation("概览时间范围必须有效且不超过 90 天")
	}
	if query.Status != "" && !map[string]bool{model.AIExecutionPending: true, model.AIExecutionRunning: true, model.AIExecutionSucceeded: true, model.AIExecutionFailed: true, model.AIExecutionCancelled: true, model.AIExecutionUnknown: true}[query.Status] {
		return dto.G5DashboardResp{}, newValidation("执行状态筛选不合法")
	}
	counts, err := s.repo.Dashboard(ctx)
	if err != nil {
		return dto.G5DashboardResp{}, err
	}
	metrics, err := s.repo.DashboardMetrics(ctx, repository.G5DashboardFilter{From: query.From, To: query.To, Model: query.Model, ChannelID: query.ChannelID, Status: query.Status})
	if err != nil {
		return dto.G5DashboardResp{}, err
	}
	successRate := decimal.Zero
	if metrics.TotalRequests > 0 {
		successRate = decimal.NewFromInt(metrics.SuccessfulRequests).Div(decimal.NewFromInt(metrics.TotalRequests))
	}
	return dto.G5DashboardResp{
		From: query.From.UTC(), To: query.To.UTC(), TotalRequests: metrics.TotalRequests, SuccessfulRequests: metrics.SuccessfulRequests,
		SuccessRate: successRate.StringFixed(4), TotalTokens: metrics.TotalTokens.StringFixed(0), SaleAmount: metrics.SaleAmount.StringFixed(8),
		UpstreamCost: metrics.UpstreamCost.StringFixed(8), GrossProfit: metrics.SaleAmount.Sub(metrics.UpstreamCost).StringFixed(8),
		SafetyRejections: metrics.SafetyRejections, RateLimitRejections: metrics.RateLimitRejections, BudgetRejections: metrics.BudgetRejections,
		ActiveModels: counts["active_models"], ActiveChannels: counts["active_channels"],
		UnhealthyChannels: counts["unhealthy_channels"], ActivePrices: counts["active_prices"],
		ActiveRoutes: counts["active_routes"], PendingExceptions: counts["pending_exceptions"],
		OpenBudgetAlerts: counts["open_budget_alerts"], OpenCompensations: counts["open_compensations"],
	}, nil
}

func (s *G5AdminService) ListModelReleases(ctx context.Context, modelID uint64) ([]model.AIModelReleaseVersion, error) {
	return s.repo.ListModelReleases(ctx, modelID)
}

func (s *G5AdminService) PublishModel(ctx context.Context, modelID, operatorID uint64, reason string) (*model.AIModelReleaseVersion, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 255 {
		return nil, newValidation("发布原因长度必须为 1 到 255 个字符")
	}
	return s.repo.PublishModel(ctx, modelID, operatorID, reason)
}

func (s *G5AdminService) UnpublishModel(ctx context.Context, modelID, operatorID uint64) error {
	return s.repo.UnpublishModel(ctx, modelID, operatorID)
}

func (s *G5AdminService) RollbackModel(ctx context.Context, modelID, operatorID uint64, req dto.RollbackModelReq) (*model.AIModelReleaseVersion, error) {
	req.Reason = strings.TrimSpace(req.Reason)
	if req.TargetVersionNo == 0 || req.Reason == "" || len(req.Reason) > 255 {
		return nil, newValidation("目标版本和 1 至 255 个字符的回滚原因不能为空")
	}
	return s.repo.RollbackModel(ctx, modelID, req.TargetVersionNo, operatorID, req.Reason)
}

func (s *G5AdminService) ListRoutes(ctx context.Context, modelCode, status string, page, pageSize int) ([]model.AIModelRoute, int64, error) {
	return s.repo.ListRoutes(ctx, strings.TrimSpace(modelCode), strings.TrimSpace(status), (page-1)*pageSize, pageSize)
}

func validateRoute(req dto.RouteWriteReq) error {
	if strings.TrimSpace(req.LogicalModelCode) == "" || req.ChannelID == 0 || strings.TrimSpace(req.ProviderModel) == "" {
		return newValidation("逻辑模型、渠道和 Bifrost provider/model 不能为空")
	}
	if !strings.Contains(req.ProviderModel, "/") {
		return newValidation("provider_model 必须采用 provider/model 格式")
	}
	if req.Weight == 0 || req.TimeoutMS < 1000 || req.TimeoutMS > 300000 || req.MaxRetries > 3 || req.CircuitBreakerThreshold == 0 {
		return newValidation("路由权重、超时、重试或熔断阈值不合法")
	}
	if req.Status != "active" && req.Status != "disabled" {
		return newValidation("路由状态不合法")
	}
	return nil
}

func routeFromReq(req dto.RouteWriteReq, operatorID uint64) model.AIModelRoute {
	return model.AIModelRoute{LogicalModelCode: strings.TrimSpace(req.LogicalModelCode), ChannelID: req.ChannelID,
		ProviderModel: strings.TrimSpace(req.ProviderModel), Priority: req.Priority, Weight: req.Weight,
		TimeoutMS: req.TimeoutMS, MaxRetries: req.MaxRetries, CircuitBreakerThreshold: req.CircuitBreakerThreshold,
		FallbackOrder: req.FallbackOrder, Status: req.Status, VersionNo: 1, UpdatedBy: operatorID}
}

func (s *G5AdminService) CreateRoute(ctx context.Context, operatorID uint64, req dto.RouteWriteReq) (*model.AIModelRoute, error) {
	if err := validateRoute(req); err != nil {
		return nil, err
	}
	route := routeFromReq(req, operatorID)
	if err := s.repo.CreateRoute(ctx, &route); err != nil {
		return nil, err
	}
	return &route, nil
}

func (s *G5AdminService) UpdateRoute(ctx context.Context, id, operatorID uint64, req dto.RouteWriteReq) error {
	if req.VersionNo == 0 {
		return newValidation("version_no 不能为空")
	}
	if err := validateRoute(req); err != nil {
		return err
	}
	route := routeFromReq(req, operatorID)
	route.ID = id
	return s.repo.UpdateRoute(ctx, &route, req.VersionNo)
}

func (s *G5AdminService) ListPrices(ctx context.Context, modelCode, status string, page, pageSize int) ([]model.AIPriceVersion, int64, error) {
	return s.repo.ListPrices(ctx, strings.TrimSpace(modelCode), strings.TrimSpace(status), (page-1)*pageSize, pageSize)
}

func (s *G5AdminService) PriceDetail(ctx context.Context, id uint64) (*dto.PriceDetailResp, error) {
	version, skus, err := s.repo.PriceDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	return &dto.PriceDetailResp{Version: version, SKUs: skus}, nil
}

func (s *G5AdminService) CreatePrice(ctx context.Context, operatorID uint64, req dto.CreatePriceReq) (*dto.PriceDetailResp, error) {
	modelCode := strings.TrimSpace(req.LogicalModelCode)
	margin, err := decimal.NewFromString(req.MinMarginRate)
	if err != nil || margin.IsNegative() || margin.GreaterThanOrEqual(decimal.NewFromInt(1)) {
		return nil, newValidation("最低毛利率不合法")
	}
	if modelCode == "" || req.MaxInputTokens == 0 || req.MaxOutputTokens == 0 || len(req.SKUs) != 4 {
		return nil, newValidation("模型、Token 上限和四项计量价格不能为空")
	}
	if req.CostUpdatedAt.IsZero() || !req.CostExpiresAt.After(req.CostUpdatedAt) || req.EffectiveAt.IsZero() || (req.ExpiresAt != nil && !req.ExpiresAt.After(req.EffectiveAt)) {
		return nil, newValidation("成本或价格生效时间不合法")
	}
	required := map[string]bool{"input_tokens": false, "output_tokens": false, "cached_tokens": false, "reasoning_tokens": false}
	skus := make([]model.AIPriceSKU, 0, 4)
	for _, item := range req.SKUs {
		if _, ok := required[item.MeterType]; !ok || required[item.MeterType] {
			return nil, newValidation("四项计量类型必须完整且不能重复")
		}
		cost, costErr := decimal.NewFromString(item.CostUnitPrice)
		sale, saleErr := decimal.NewFromString(item.SaleUnitPrice)
		scale, scaleErr := decimal.NewFromString(item.Scale)
		if costErr != nil || saleErr != nil || scaleErr != nil || cost.IsNegative() || sale.LessThanOrEqual(decimal.Zero) || scale.LessThanOrEqual(decimal.Zero) {
			return nil, newValidation("价格必须是合法的非负十进制定点数")
		}
		if sale.Sub(cost).Div(sale).LessThan(margin) {
			return nil, newValidation("销售价未达到最低毛利率")
		}
		variant := item.Variant
		if len(variant) == 0 {
			variant = json.RawMessage(`{}`)
		}
		var normalized interface{}
		if json.Unmarshal(variant, &normalized) != nil {
			return nil, newValidation("价格变体不是合法 JSON")
		}
		canonical, _ := json.Marshal(normalized)
		hash := sha256.Sum256(canonical)
		skus = append(skus, model.AIPriceSKU{MeterType: item.MeterType, VariantJSON: canonical, VariantHash: hex.EncodeToString(hash[:]), CostUnitPrice: cost, SaleUnitPrice: sale, Scale: scale, Currency: "CNY"})
		required[item.MeterType] = true
	}
	version := &model.AIPriceVersion{LogicalModelCode: modelCode, Currency: "CNY", ExchangeRate: decimal.NewFromInt(1), Status: model.AIPriceDraft,
		MinMarginRate: margin, MaxInputTokens: req.MaxInputTokens, MaxOutputTokens: req.MaxOutputTokens,
		FailureChargePolicy: "confirmed_usage", RoundingMode: "ceil_8", CostUpdatedAt: req.CostUpdatedAt.UTC(), CostExpiresAt: req.CostExpiresAt.UTC(),
		EffectiveAt: req.EffectiveAt.UTC(), ExpiresAt: req.ExpiresAt, CreatedBy: operatorID}
	if err := s.repo.CreatePrice(ctx, version, skus); err != nil {
		return nil, err
	}
	return &dto.PriceDetailResp{Version: version, SKUs: skus}, nil
}

func (s *G5AdminService) ApprovePrice(ctx context.Context, id, operatorID uint64) error {
	return s.repo.ApprovePrice(ctx, id, operatorID)
}

func (s *G5AdminService) PublishPrice(ctx context.Context, id uint64) error {
	return s.pricing.PublishApprovedVersion(ctx, id, time.Now().UTC())
}

func (s *G5AdminService) SuspendPrice(ctx context.Context, id uint64, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 191 {
		return newValidation("暂停原因长度必须为 1 到 191 个字符")
	}
	return s.repo.SetPriceStatus(ctx, id, []string{model.AIPriceActive}, model.AIPriceSuspended, reason)
}

func (s *G5AdminService) RetirePrice(ctx context.Context, id uint64) error {
	return s.repo.SetPriceStatus(ctx, id, []string{model.AIPriceDraft, model.AIPriceApproved, model.AIPriceActive, model.AIPriceSuspended}, model.AIPriceRetired, "")
}

func (s *G5AdminService) RollbackPrice(ctx context.Context, id, operatorID uint64, req dto.RollbackPriceReq) (*dto.PriceDetailResp, error) {
	req.Reason = strings.TrimSpace(req.Reason)
	if id == 0 || req.Reason == "" || len(req.Reason) > 255 || req.EffectiveAt.IsZero() || req.CostExpiresAt.IsZero() || !req.CostExpiresAt.After(time.Now().UTC()) {
		return nil, newValidation("回滚原因、生效时间和未来的成本失效时间不能为空")
	}
	version, skus, err := s.repo.ClonePriceAsDraft(ctx, id, operatorID, req.EffectiveAt, req.CostExpiresAt)
	if err != nil {
		return nil, err
	}
	return &dto.PriceDetailResp{Version: version, SKUs: skus}, nil
}

func IsG5Conflict(err error) bool {
	return errors.Is(err, repository.ErrModelReleaseConflict) || errors.Is(err, repository.ErrRouteVersionConflict) || errors.Is(err, repository.ErrPriceStateConflict) || errors.Is(err, repository.ErrPriceVersionNotPublishable) || errors.Is(err, repository.ErrPriceWindowOverlap)
}
