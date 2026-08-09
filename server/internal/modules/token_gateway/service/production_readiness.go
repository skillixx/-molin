package service

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

const productionMinimumMarginRate = 0.15

// ProductionReadinessService 在生产流量开启前只读核对模型、渠道、价格、路由和审核事实。
// 它不发布模型、不修改价格、不修复数据，只负责失败关闭。
type ProductionReadinessService struct {
	db *gorm.DB
}

// ProductionReadinessSnapshot 保存启动门禁的低敏聚合结果，不包含模型名、渠道地址或密钥。
type ProductionReadinessSnapshot struct {
	PublishedModels      int64
	HealthyChannels      int64
	InvalidModels        int64
	MissingPrices        int64
	InvalidPrices        int64
	MissingRoutes        int64
	LowMarginModels      int64
	ActiveSafetyPolicies int64
}

func NewProductionReadinessService(db *gorm.DB) *ProductionReadinessService {
	return &ProductionReadinessService{db: db}
}

// Validate 要求首批 5 至 8 个文字模型、至少两个健康渠道，以及逐模型有效价格和路由。
// 价格还必须具备审批事实、未过期成本和不低于 15% 的毛利；请求重试语义由执行链独立失败关闭。
func (s *ProductionReadinessService) Validate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("生产发布门禁数据库未装配")
	}
	var snapshot ProductionReadinessSnapshot
	err := s.db.WithContext(ctx).Raw(`
SELECT
  COUNT(DISTINCT m.logical_model_code) AS published_models,
  (SELECT COUNT(DISTINCT c.id) FROM ai_model_routes r
    JOIN token_channels c ON c.id=r.channel_id
    JOIN token_models routed_model ON routed_model.logical_model_code=r.logical_model_code
    LEFT JOIN ai_model_route_runtime_states runtime ON runtime.route_id=r.id
    WHERE r.status='active' AND c.status='active' AND c.health_status='healthy'
      AND c.last_health_check_at IS NOT NULL AND c.last_health_check_at>=UTC_TIMESTAMP()-INTERVAL 5 MINUTE
      AND (runtime.circuit_open_until IS NULL OR runtime.circuit_open_until<=UTC_TIMESTAMP())
      AND routed_model.status='active' AND routed_model.modality='chat'
      AND routed_model.release_version_no>0 AND routed_model.published_at IS NOT NULL) AS healthy_channels,
  SUM(CASE WHEN m.context_window=0 OR m.capabilities_json IS NULL
    OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(m.capabilities_json, '$.stream')), 'false') <> 'true'
    THEN 1 ELSE 0 END) AS invalid_models,
  SUM(CASE WHEN (SELECT COUNT(*) FROM ai_price_versions p
    WHERE p.logical_model_code=m.logical_model_code AND p.status='active'
      AND p.approved_by IS NOT NULL AND p.approved_at IS NOT NULL AND p.published_at IS NOT NULL
      AND p.effective_at<=UTC_TIMESTAMP() AND (p.expires_at IS NULL OR p.expires_at>UTC_TIMESTAMP())
      AND p.cost_expires_at>UTC_TIMESTAMP()) <> 1
    THEN 1 ELSE 0 END) AS missing_prices,
  SUM(CASE WHEN EXISTS (
    SELECT 1 FROM ai_price_versions p
    WHERE p.logical_model_code=m.logical_model_code AND p.status='active'
      AND p.approved_by IS NOT NULL AND p.approved_at IS NOT NULL AND p.published_at IS NOT NULL
      AND p.effective_at<=UTC_TIMESTAMP() AND (p.expires_at IS NULL OR p.expires_at>UTC_TIMESTAMP())
      AND p.cost_expires_at>UTC_TIMESTAMP()
      AND (p.currency<>'CNY' OR p.exchange_rate<>1 OR p.max_input_tokens=0 OR p.max_output_tokens=0
        OR p.failure_charge_policy<>'confirmed_usage' OR p.rounding_mode<>'ceil_8'
        OR (SELECT COUNT(*) FROM ai_price_skus sku WHERE sku.price_version_id=p.id)<>4
        OR (SELECT COUNT(DISTINCT sku.meter_type) FROM ai_price_skus sku
            WHERE sku.price_version_id=p.id
              AND sku.meter_type IN ('input_tokens','output_tokens','cached_tokens','reasoning_tokens')
              AND sku.cost_unit_price>=0 AND sku.sale_unit_price>0 AND sku.scale>0 AND sku.currency='CNY')<>4)
  ) THEN 1 ELSE 0 END) AS invalid_prices,
  SUM(CASE WHEN NOT EXISTS (
    SELECT 1 FROM ai_model_routes r JOIN token_channels c ON c.id=r.channel_id
    LEFT JOIN ai_model_route_runtime_states runtime ON runtime.route_id=r.id
    WHERE r.logical_model_code=m.logical_model_code AND r.status='active'
      AND c.status='active' AND c.health_status='healthy'
      AND c.last_health_check_at IS NOT NULL AND c.last_health_check_at>=UTC_TIMESTAMP()-INTERVAL 5 MINUTE
      AND (runtime.circuit_open_until IS NULL OR runtime.circuit_open_until<=UTC_TIMESTAMP())
  ) THEN 1 ELSE 0 END) AS missing_routes,
  SUM(CASE WHEN EXISTS (
    SELECT 1 FROM ai_price_versions p JOIN ai_price_skus sku ON sku.price_version_id=p.id
    WHERE p.logical_model_code=m.logical_model_code AND p.status='active'
      AND p.effective_at<=UTC_TIMESTAMP() AND (p.expires_at IS NULL OR p.expires_at>UTC_TIMESTAMP())
      AND (p.min_margin_rate < ? OR p.min_margin_rate>=1
        OR sku.sale_unit_price < sku.cost_unit_price/(1-p.min_margin_rate))
  ) THEN 1 ELSE 0 END) AS low_margin_models,
  (SELECT COUNT(*) FROM ai_safety_policy_versions sp
    WHERE sp.status='active' AND sp.approved_by IS NOT NULL AND sp.effective_at<=UTC_TIMESTAMP()
      AND JSON_VALID(sp.rules_json)
      AND (SELECT COUNT(DISTINCT rules.category)
        FROM JSON_TABLE(sp.rules_json, '$[*]' COLUMNS(category VARCHAR(32) PATH '$.category')) AS rules
        WHERE rules.category IN ('illegal','sexual','gambling','drugs','terror','hate','self_harm'))=7) AS active_safety_policies
FROM token_models m
WHERE m.status='active' AND m.modality='chat' AND m.release_version_no>0 AND m.published_at IS NOT NULL`,
		productionMinimumMarginRate).Scan(&snapshot).Error
	if err != nil {
		return fmt.Errorf("生产发布事实只读核对失败")
	}
	if snapshot.PublishedModels < 5 || snapshot.PublishedModels > 8 {
		return fmt.Errorf("已发布文字模型数量必须为 5 至 8 个")
	}
	if snapshot.HealthyChannels < 2 {
		return fmt.Errorf("健康上游渠道少于两个")
	}
	if snapshot.InvalidModels != 0 {
		return fmt.Errorf("存在上下文长度或流式能力不完整的模型")
	}
	if snapshot.MissingPrices != 0 || snapshot.InvalidPrices != 0 || snapshot.MissingRoutes != 0 || snapshot.ActiveSafetyPolicies != 1 {
		return fmt.Errorf("模型价格、路由或内容审核发布事实不完整")
	}
	if snapshot.LowMarginModels != 0 {
		return fmt.Errorf("存在低于批准毛利底线的模型")
	}
	return nil
}
