package repository

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"reflect"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrModelDocumentsNotReady = errors.New("模型发布文档未就绪")
	ErrModelPriceNotReady     = errors.New("模型发布价格未就绪")
	ErrModelRouteNotReady     = errors.New("模型发布路由未就绪")
	ErrModelReleaseConflict   = errors.New("模型发布版本冲突")
	ErrRouteVersionConflict   = errors.New("路由版本冲突")
	ErrRouteUnavailable       = errors.New("已配置路由暂时不可用")
	ErrPriceStateConflict     = errors.New("价格版本状态冲突")
)

// G5AdminRepository 保存管理工作台产生的发布事实，所有状态变更均使用事务和版本条件。
type G5AdminRepository struct {
	db  *gorm.DB
	now func() time.Time
}

type G5DashboardMetrics struct {
	TotalRequests       int64
	SuccessfulRequests  int64
	TotalTokens         decimal.Decimal
	SaleAmount          decimal.Decimal
	UpstreamCost        decimal.Decimal
	SafetyRejections    int64
	RateLimitRejections int64
	BudgetRejections    int64
}

type G5DashboardFilter struct {
	From      time.Time
	To        time.Time
	Model     string
	ChannelID uint64
	Status    string
}

func NewG5AdminRepository(db *gorm.DB) *G5AdminRepository {
	return &G5AdminRepository{db: db, now: time.Now}
}

// Dashboard 返回网关关键运行事实的聚合计数，不读取请求或响应正文。
func (r *G5AdminRepository) Dashboard(ctx context.Context) (map[string]int64, error) {
	queries := map[string]*gorm.DB{
		"active_models":      r.db.Table("token_models").Where("status = ?", "active"),
		"active_channels":    r.db.Table("token_channels").Where("status = ?", "active"),
		"unhealthy_channels": r.db.Table("token_channels").Where("health_status IN ?", []string{"degraded", "down"}),
		"active_prices":      r.db.Table("ai_price_versions").Where("status = ?", model.AIPriceActive),
		"active_routes":      r.db.Table("ai_model_routes").Where("status = ?", "active"),
		"pending_exceptions": r.db.Table("ai_requests").Where("billing_status IN ?", []string{model.AIBillingSettlementPending, model.AIBillingException}),
		"open_budget_alerts": r.db.Table("ai_budget_alerts").Where("created_at >= ?", r.now().UTC().Add(-24*time.Hour)),
		"open_compensations": r.db.Table("ai_compensation_tasks").Where("status IN ?", []string{"pending", "retry", "dead"}),
	}
	result := make(map[string]int64, len(queries))
	for key, query := range queries {
		var count int64
		if err := query.WithContext(ctx).Count(&count).Error; err != nil {
			return nil, err
		}
		result[key] = count
	}
	return result, nil
}

// DashboardMetrics 从请求账本、已定价 Usage 和冻结价格快照聚合经营指标，避免使用可变的当前价格反算历史成本。
func (r *G5AdminRepository) DashboardMetrics(ctx context.Context, query G5DashboardFilter) (G5DashboardMetrics, error) {
	base := r.db.WithContext(ctx).Table("ai_requests AS requests").
		Where("requests.created_at >= ? AND requests.created_at < ?", query.From.UTC(), query.To.UTC())
	if query.Model != "" {
		base = base.Where("requests.logical_model_code = ?", query.Model)
	}
	if query.Status != "" {
		base = base.Where("requests.execution_status = ?", query.Status)
	}
	if query.ChannelID != 0 {
		base = base.Where("EXISTS (SELECT 1 FROM ai_execution_attempts AS attempts JOIN ai_model_routes AS routes ON attempts.endpoint_code = CONCAT('route:', routes.id) WHERE attempts.request_id = requests.request_id AND routes.channel_id = ?)", query.ChannelID)
	}
	var requestTotals struct {
		TotalRequests      int64  `gorm:"column:total_requests"`
		SuccessfulRequests int64  `gorm:"column:successful_requests"`
		SaleAmount         string `gorm:"column:sale_amount"`
	}
	if err := base.Select("COUNT(DISTINCT requests.request_id) AS total_requests, COUNT(DISTINCT CASE WHEN requests.execution_status = 'succeeded' THEN requests.request_id END) AS successful_requests, CAST(COALESCE(SUM(CASE WHEN requests.billing_status = 'settled' THEN requests.settled_amount ELSE 0 END),0) AS CHAR) AS sale_amount").Scan(&requestTotals).Error; err != nil {
		return G5DashboardMetrics{}, err
	}
	usageQuery := r.db.WithContext(ctx).Table("ai_usage_items AS usage_items").
		Joins("JOIN ai_requests AS requests ON requests.request_id = usage_items.request_id").
		Where("requests.created_at >= ? AND requests.created_at < ?", query.From.UTC(), query.To.UTC())
	if query.Model != "" {
		usageQuery = usageQuery.Where("requests.logical_model_code = ?", query.Model)
	}
	if query.Status != "" {
		usageQuery = usageQuery.Where("requests.execution_status = ?", query.Status)
	}
	if query.ChannelID != 0 {
		usageQuery = usageQuery.Where("EXISTS (SELECT 1 FROM ai_execution_attempts AS attempts JOIN ai_model_routes AS routes ON attempts.endpoint_code = CONCAT('route:', routes.id) WHERE attempts.request_id = requests.request_id AND routes.channel_id = ?)", query.ChannelID)
	}
	var usageTotals struct {
		TotalTokens  string `gorm:"column:total_tokens"`
		UpstreamCost string `gorm:"column:upstream_cost"`
	}
	if err := usageQuery.Select("CAST(COALESCE(SUM(CASE WHEN usage_items.source = 'provider' AND usage_items.sequence_no = 0 AND usage_items.meter_type = 'total_tokens' THEN usage_items.quantity ELSE 0 END),0) AS CHAR) AS total_tokens, CAST(COALESCE(SUM(CASE WHEN (usage_items.source = 'provider_cost' AND usage_items.sequence_no = 0) OR (usage_items.source = 'provider' AND usage_items.sequence_no = 2) THEN usage_items.amount ELSE 0 END),0) AS CHAR) AS upstream_cost").Scan(&usageTotals).Error; err != nil {
		return G5DashboardMetrics{}, err
	}
	sale, saleErr := decimal.NewFromString(requestTotals.SaleAmount)
	tokens, tokenErr := decimal.NewFromString(usageTotals.TotalTokens)
	cost, costErr := decimal.NewFromString(usageTotals.UpstreamCost)
	if saleErr != nil || tokenErr != nil || costErr != nil {
		return G5DashboardMetrics{}, errors.New("概览聚合金额格式异常")
	}
	metrics := G5DashboardMetrics{TotalRequests: requestTotals.TotalRequests, SuccessfulRequests: requestTotals.SuccessfulRequests, TotalTokens: tokens, SaleAmount: sale, UpstreamCost: cost}
	if query.ChannelID == 0 && query.Status == "" {
		rejections := r.db.WithContext(ctx).Table("ai_gateway_rejection_events").Where("created_at >= ? AND created_at < ?", query.From.UTC(), query.To.UTC())
		if query.Model != "" {
			rejections = rejections.Where("logical_model_code = ?", query.Model)
		}
		var totals struct {
			Safety int64 `gorm:"column:safety"`
			Rate   int64 `gorm:"column:rate"`
			Budget int64 `gorm:"column:budget"`
		}
		if err := rejections.Select("COALESCE(SUM(reason_code = 'content_policy_violation'),0) AS safety, COALESCE(SUM(reason_code IN ('concurrency_limit_exceeded','rpm_limit_exceeded','tpm_limit_exceeded')),0) AS rate, COALESCE(SUM(reason_code = 'budget_limit_exceeded'),0) AS budget").Scan(&totals).Error; err != nil {
			return G5DashboardMetrics{}, err
		}
		metrics.SafetyRejections, metrics.RateLimitRejections, metrics.BudgetRejections = totals.Safety, totals.Rate, totals.Budget
	}
	return metrics, nil
}

func (r *G5AdminRepository) ListModelReleases(ctx context.Context, modelID uint64) ([]model.AIModelReleaseVersion, error) {
	var items []model.AIModelReleaseVersion
	err := r.db.WithContext(ctx).Where("model_id = ?", modelID).Order("version_no DESC").Find(&items).Error
	return items, err
}

// PublishModel 锁定模型行后创建新快照，保证多节点并发发布只产生连续且唯一的版本号。
func (r *G5AdminRepository) PublishModel(ctx context.Context, modelID, operatorID uint64, reason string) (*model.AIModelReleaseVersion, error) {
	var release model.AIModelReleaseVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.TokenModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&item, modelID).Error; err != nil {
			return err
		}
		if err := validateModelPublishMetadata(item); err != nil {
			return err
		}
		if item.ChannelID == nil || item.UpstreamModel == nil || strings.TrimSpace(*item.UpstreamModel) == "" {
			return ErrModelRouteNotReady
		}
		var priceCount int64
		if err := tx.Model(&model.AIPriceVersion{}).Where("logical_model_code = ? AND status = ? AND effective_at <= ? AND (expires_at IS NULL OR expires_at > ?)", item.LogicalModelCode, model.AIPriceActive, r.now().UTC(), r.now().UTC()).Count(&priceCount).Error; err != nil {
			return err
		}
		if priceCount != 1 {
			return ErrModelPriceNotReady
		}
		if err := validatePublishRoute(tx, item.LogicalModelCode); err != nil {
			return err
		}
		snapshot, err := item.MarshalReleaseSnapshot()
		if err != nil {
			return err
		}
		// 同一份目录快照重复发布时直接返回既有活动版本，使重复请求和并发请求收敛到同一结果。
		var current model.AIModelReleaseVersion
		// 这里必须使用锁定当前读；普通快照读在 REPEATABLE READ 下可能看不到等待期间刚提交的活动版本。
		currentErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("model_id = ? AND status = ?", item.ID, "active").First(&current).Error
		if currentErr == nil && releaseSnapshotsEqual(current.SnapshotJSON, snapshot) {
			release = current
			return nil
		}
		if currentErr != nil && !errors.Is(currentErr, gorm.ErrRecordNotFound) {
			return currentErr
		}
		now := r.now().UTC()
		release = model.AIModelReleaseVersion{ModelID: item.ID, VersionNo: item.ReleaseVersionNo + 1, Status: "active", SnapshotJSON: snapshot, Reason: reason, CreatedBy: operatorID, PublishedAt: now}
		// 在写入前检查目标版本号是否被孤儿记录占用，避免把数据库唯一键原文暴露给调用方。
		var occupied model.AIModelReleaseVersion
		occupiedErr := tx.Where("model_id = ? AND version_no = ?", item.ID, release.VersionNo).First(&occupied).Error
		if occupiedErr == nil {
			if occupied.Status == "active" && releaseSnapshotsEqual(occupied.SnapshotJSON, snapshot) {
				release = occupied
				return nil
			}
			return ErrModelReleaseConflict
		}
		if !errors.Is(occupiedErr, gorm.ErrRecordNotFound) {
			return occupiedErr
		}
		if err := tx.Model(&model.AIModelReleaseVersion{}).Where("model_id = ? AND status = ?", item.ID, "active").Updates(map[string]interface{}{"status": "retired", "retired_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&release).Error; err != nil {
			return err
		}
		result := tx.Model(&model.TokenModel{}).Where("id = ? AND release_version_no = ?", item.ID, item.ReleaseVersionNo).Updates(map[string]interface{}{
			"status": "active", "release_version_no": release.VersionNo, "published_at": now, "updated_by": operatorID,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrModelReleaseConflict
		}
		return nil
	})
	return &release, err
}

// validateModelPublishMetadata 在任何发布写入前验证两份必需文档均已配置且检查健康。
func validateModelPublishMetadata(item model.TokenModel) error {
	if item.DocsURL == nil || strings.TrimSpace(*item.DocsURL) == "" || item.QuickStartURL == nil || strings.TrimSpace(*item.QuickStartURL) == "" ||
		item.DocsURLHealthStatus != "healthy" || item.QuickStartURLHealthStatus != "healthy" {
		return ErrModelDocumentsNotReady
	}
	return nil
}

// releaseSnapshotsEqual 按 JSON 语义比较快照，避免 MySQL JSON 列规范化对象键顺序后误判为配置变化。
func releaseSnapshotsEqual(left, right json.RawMessage) bool {
	var leftValue, rightValue interface{}
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func (r *G5AdminRepository) UnpublishModel(ctx context.Context, modelID, operatorID uint64) error {
	now := r.now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.TokenModel{}).Where("id = ? AND status = ?", modelID, "active").Updates(map[string]interface{}{"status": "inactive", "updated_by": operatorID})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrModelReleaseConflict
		}
		return tx.Model(&model.AIModelReleaseVersion{}).Where("model_id = ? AND status = ?", modelID, "active").Updates(map[string]interface{}{"status": "retired", "retired_at": now}).Error
	})
}

// RollbackModel 将历史快照恢复到模型目录，并生成新的不可变发布版本，历史版本本身始终保持只读。
func (r *G5AdminRepository) RollbackModel(ctx context.Context, modelID, targetVersionNo, operatorID uint64, reason string) (*model.AIModelReleaseVersion, error) {
	var release model.AIModelReleaseVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.TokenModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&item, modelID).Error; err != nil {
			return err
		}
		var target model.AIModelReleaseVersion
		if err := tx.Where("model_id = ? AND version_no = ?", modelID, targetVersionNo).First(&target).Error; err != nil {
			return err
		}
		var snapshot model.TokenModelReleaseSnapshot
		if err := json.Unmarshal(target.SnapshotJSON, &snapshot); err != nil || snapshot.LogicalModelCode == "" || snapshot.DisplayName == "" || snapshot.ChannelID == nil || snapshot.UpstreamModel == nil || snapshot.DocsURL == nil || snapshot.QuickStartURL == nil {
			return ErrModelReleaseConflict
		}
		now := r.now().UTC()
		var priceCount int64
		if err := tx.Model(&model.AIPriceVersion{}).Where("logical_model_code = ? AND status = ? AND effective_at <= ? AND (expires_at IS NULL OR expires_at > ?)", snapshot.LogicalModelCode, model.AIPriceActive, now, now).Count(&priceCount).Error; err != nil {
			return err
		}
		if priceCount != 1 {
			return ErrModelPriceNotReady
		}
		if err := validatePublishRoute(tx, snapshot.LogicalModelCode); err != nil {
			return err
		}
		release = model.AIModelReleaseVersion{ModelID: modelID, VersionNo: item.ReleaseVersionNo + 1, Status: "active", SnapshotJSON: target.SnapshotJSON, Reason: reason, CreatedBy: operatorID, PublishedAt: now}
		if err := tx.Model(&model.AIModelReleaseVersion{}).Where("model_id = ? AND status = ?", modelID, "active").Updates(map[string]interface{}{"status": "retired", "retired_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&release).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{
			"logical_model_code": snapshot.LogicalModelCode, "display_name": snapshot.DisplayName, "provider_name": snapshot.ProviderName,
			"description": snapshot.Description, "capabilities_json": snapshot.Capabilities, "context_window": snapshot.ContextWindow,
			"intro_url": snapshot.IntroURL, "docs_url": snapshot.DocsURL, "quick_start_url": snapshot.QuickStartURL,
			"intro_url_health_status":       rollbackDocumentHealth(snapshot.IntroURL, snapshot.IntroURLHealthStatus),
			"docs_url_health_status":        rollbackDocumentHealth(snapshot.DocsURL, snapshot.DocsURLHealthStatus),
			"quick_start_url_health_status": rollbackDocumentHealth(snapshot.QuickStartURL, snapshot.QuickStartURLHealthStatus),
			"modality":                      snapshot.Modality, "product_id": snapshot.ProductID, "channel_id": snapshot.ChannelID,
			"upstream_model": snapshot.UpstreamModel, "visible_scope": snapshot.VisibleScope, "target_audience_json": snapshot.TargetAudience,
			"status": "active", "release_version_no": release.VersionNo, "published_at": now, "updated_by": operatorID,
		}
		result := tx.Model(&model.TokenModel{}).Where("id = ? AND release_version_no = ?", modelID, item.ReleaseVersionNo).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrModelReleaseConflict
		}
		return nil
	})
	return &release, err
}

func rollbackDocumentHealth(value *string, status string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "unpublished"
	}
	if status == "" {
		return "unknown"
	}
	return status
}

// validatePublishRoute 保证上架目录至少有一条指向可用渠道的生效 Bifrost 路由。
func validatePublishRoute(tx *gorm.DB, logicalModelCode string) error {
	var routeCount int64
	err := tx.Table("ai_model_routes AS routes").Joins("JOIN token_channels AS channels ON channels.id = routes.channel_id").
		Where("routes.logical_model_code = ? AND routes.status = 'active' AND channels.status = 'active' AND channels.health_status = 'healthy'", logicalModelCode).Count(&routeCount).Error
	if err != nil {
		return err
	}
	if routeCount == 0 {
		return ErrModelRouteNotReady
	}
	return nil
}

func (r *G5AdminRepository) ListRoutes(ctx context.Context, modelCode, status string, offset, limit int) ([]model.AIModelRoute, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.AIModelRoute{})
	if modelCode != "" {
		query = query.Where("logical_model_code = ?", modelCode)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.AIModelRoute
	err := query.Order("logical_model_code, priority DESC, fallback_order, id").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

// ResolveActiveRoute 从最低回退层级和最高优先级中按稳定权重选择健康路由。
// 同一 request_id 始终落到同一路由，避免重放时产生随机路由漂移。
func (r *G5AdminRepository) ResolveActiveRoute(ctx context.Context, modelCode, requestID string) (*model.AIModelRoute, error) {
	var candidates []model.AIModelRoute
	err := r.db.WithContext(ctx).Table("ai_model_routes AS routes").
		Select("routes.*").
		Joins("JOIN token_channels AS channels ON channels.id = routes.channel_id").
		Joins("LEFT JOIN ai_model_route_runtime_states AS runtime ON runtime.route_id = routes.id").
		Where("routes.logical_model_code = ? AND routes.status = 'active' AND channels.status = 'active' AND channels.health_status = 'healthy' AND (runtime.circuit_open_until IS NULL OR runtime.circuit_open_until <= ?)", modelCode, r.now().UTC()).
		Order("routes.fallback_order ASC, routes.priority DESC, routes.id ASC").Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		// 只有从未配置过 G5 路由的旧模型才允许回退 token_models；已配置但熔断、停用或渠道 down 时必须失败关闭。
		var configured int64
		if err := r.db.WithContext(ctx).Model(&model.AIModelRoute{}).Where("logical_model_code = ?", modelCode).Count(&configured).Error; err != nil {
			return nil, err
		}
		if configured > 0 {
			return nil, ErrRouteUnavailable
		}
		return nil, gorm.ErrRecordNotFound
	}
	selected := chooseWeightedPrimaryRoute(candidates, requestID)
	if selected == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return selected, nil
}

// RecordRouteTransportFailure 只统计确认未发送的传输失败；达到阈值后短暂熔断 30 秒，让下一请求进入回退层级。
func (r *G5AdminRepository) RecordRouteTransportFailure(ctx context.Context, routeID, threshold uint64) error {
	if routeID == 0 || threshold == 0 {
		return ErrRouteVersionConflict
	}
	now := r.now().UTC()
	openUntil := now.Add(30 * time.Second)
	return r.db.WithContext(ctx).Exec(`INSERT INTO ai_model_route_runtime_states (route_id, consecutive_failures, circuit_open_until, updated_at)
VALUES (?, 1, IF(1 >= ?, ?, NULL), ?)
ON DUPLICATE KEY UPDATE consecutive_failures = consecutive_failures + 1,
circuit_open_until = IF(consecutive_failures >= ?, ?, circuit_open_until), updated_at = VALUES(updated_at)`, routeID, threshold, openUntil, now, threshold, openUntil).Error
}

// ResetRouteTransportFailures 在路由成功建立请求后关闭熔断并清零连续失败。
func (r *G5AdminRepository) ResetRouteTransportFailures(ctx context.Context, routeID uint64) error {
	if routeID == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.AIModelRouteRuntimeState{}).Where("route_id = ?", routeID).Updates(map[string]interface{}{"consecutive_failures": 0, "circuit_open_until": nil}).Error
}

func chooseWeightedPrimaryRoute(candidates []model.AIModelRoute, requestID string) *model.AIModelRoute {
	if len(candidates) == 0 {
		return nil
	}
	minFallback, maxPriority := candidates[0].FallbackOrder, candidates[0].Priority
	pool := make([]model.AIModelRoute, 0, len(candidates))
	var total uint64
	for _, route := range candidates {
		if route.FallbackOrder != minFallback {
			break
		}
		if route.Priority > maxPriority {
			maxPriority = route.Priority
		}
	}
	for _, route := range candidates {
		if route.FallbackOrder == minFallback && route.Priority == maxPriority {
			pool = append(pool, route)
			total += route.Weight
		}
	}
	if len(pool) == 0 || total == 0 {
		return nil
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(requestID))
	point := hasher.Sum64() % total
	var current uint64
	for i := range pool {
		current += pool[i].Weight
		if point < current {
			chosen := pool[i]
			return &chosen
		}
	}
	chosen := pool[len(pool)-1]
	return &chosen
}

func (r *G5AdminRepository) CreateRoute(ctx context.Context, route *model.AIModelRoute) error {
	return r.db.WithContext(ctx).Create(route).Error
}

func (r *G5AdminRepository) UpdateRoute(ctx context.Context, route *model.AIModelRoute, expectedVersion uint64) error {
	updates := map[string]interface{}{
		"channel_id": route.ChannelID, "provider_model": route.ProviderModel, "priority": route.Priority,
		"weight": route.Weight, "timeout_ms": route.TimeoutMS, "max_retries": route.MaxRetries,
		"circuit_breaker_threshold": route.CircuitBreakerThreshold, "fallback_order": route.FallbackOrder,
		"status": route.Status, "updated_by": route.UpdatedBy, "version_no": expectedVersion + 1,
	}
	result := r.db.WithContext(ctx).Model(&model.AIModelRoute{}).Where("id = ? AND version_no = ?", route.ID, expectedVersion).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRouteVersionConflict
	}
	return nil
}

func (r *G5AdminRepository) ListPrices(ctx context.Context, modelCode, status string, offset, limit int) ([]model.AIPriceVersion, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.AIPriceVersion{})
	if modelCode != "" {
		query = query.Where("logical_model_code = ?", modelCode)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.AIPriceVersion
	err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (r *G5AdminRepository) PriceDetail(ctx context.Context, id uint64) (*model.AIPriceVersion, []model.AIPriceSKU, error) {
	var version model.AIPriceVersion
	if err := r.db.WithContext(ctx).First(&version, id).Error; err != nil {
		return nil, nil, err
	}
	var skus []model.AIPriceSKU
	if err := r.db.WithContext(ctx).Where("price_version_id = ?", id).Order("meter_type").Find(&skus).Error; err != nil {
		return nil, nil, err
	}
	return &version, skus, nil
}

func (r *G5AdminRepository) CreatePrice(ctx context.Context, version *model.AIPriceVersion, skus []model.AIPriceSKU) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 锁定模型目录行，把同一逻辑模型的版本号分配串行化，防止并发创建得到相同版本号。
		var tokenModel model.TokenModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("logical_model_code = ?", version.LogicalModelCode).First(&tokenModel).Error; err != nil {
			return err
		}
		var next uint64
		if err := tx.Model(&model.AIPriceVersion{}).Where("logical_model_code = ?", version.LogicalModelCode).Select("COALESCE(MAX(version_no),0)+1").Scan(&next).Error; err != nil {
			return err
		}
		version.VersionNo = next
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		for i := range skus {
			skus[i].PriceVersionID = version.ID
		}
		return tx.Create(&skus).Error
	})
}

// ClonePriceAsDraft 从不可变历史价格复制新草稿，并要求操作者重新审批发布，避免回滚绕过价格门禁。
func (r *G5AdminRepository) ClonePriceAsDraft(ctx context.Context, sourceID, operatorID uint64, effectiveAt, costExpiresAt time.Time) (*model.AIPriceVersion, []model.AIPriceSKU, error) {
	var cloned model.AIPriceVersion
	var clonedSKUs []model.AIPriceSKU
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source model.AIPriceVersion
		if err := tx.First(&source, sourceID).Error; err != nil {
			return err
		}
		var tokenModel model.TokenModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("logical_model_code = ?", source.LogicalModelCode).First(&tokenModel).Error; err != nil {
			return err
		}
		var sourceSKUs []model.AIPriceSKU
		if err := tx.Where("price_version_id = ?", sourceID).Order("id").Find(&sourceSKUs).Error; err != nil {
			return err
		}
		if len(sourceSKUs) == 0 {
			return ErrPriceStateConflict
		}
		var next uint64
		if err := tx.Model(&model.AIPriceVersion{}).Where("logical_model_code = ?", source.LogicalModelCode).Select("COALESCE(MAX(version_no),0)+1").Scan(&next).Error; err != nil {
			return err
		}
		cloned = source
		cloned.ID, cloned.VersionNo, cloned.Status = 0, next, model.AIPriceDraft
		cloned.EffectiveAt, cloned.ExpiresAt, cloned.CostExpiresAt = effectiveAt.UTC(), nil, costExpiresAt.UTC()
		cloned.CreatedBy, cloned.ApprovedBy, cloned.ApprovedAt, cloned.PublishedAt, cloned.SuspendedReason = operatorID, nil, nil, nil, nil
		cloned.CreatedAt, cloned.UpdatedAt = time.Time{}, time.Time{}
		if err := tx.Create(&cloned).Error; err != nil {
			return err
		}
		clonedSKUs = make([]model.AIPriceSKU, len(sourceSKUs))
		for i := range sourceSKUs {
			clonedSKUs[i] = sourceSKUs[i]
			clonedSKUs[i].ID, clonedSKUs[i].PriceVersionID, clonedSKUs[i].CreatedAt = 0, cloned.ID, time.Time{}
		}
		return tx.Create(&clonedSKUs).Error
	})
	return &cloned, clonedSKUs, err
}

func (r *G5AdminRepository) ApprovePrice(ctx context.Context, id, operatorID uint64) error {
	now := r.now().UTC()
	result := r.db.WithContext(ctx).Model(&model.AIPriceVersion{}).Where("id = ? AND status = ?", id, model.AIPriceDraft).Updates(map[string]interface{}{"status": model.AIPriceApproved, "approved_by": operatorID, "approved_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrPriceStateConflict
	}
	return nil
}

func (r *G5AdminRepository) SetPriceStatus(ctx context.Context, id uint64, from []string, to, reason string) error {
	updates := map[string]interface{}{"status": to}
	if reason != "" {
		updates["suspended_reason"] = reason
	}
	result := r.db.WithContext(ctx).Model(&model.AIPriceVersion{}).Where("id = ? AND status IN ?", id, from).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrPriceStateConflict
	}
	return nil
}
