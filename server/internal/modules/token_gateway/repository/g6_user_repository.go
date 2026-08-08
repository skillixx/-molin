package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

var ErrBillingDisputeExists = errors.New("该请求已提交账单申诉")

// G6UserRepository 为用户端模型市场和请求账本提供只读聚合，并承载账单申诉写入。
type G6UserRepository struct {
	db *gorm.DB
}

func NewG6UserRepository(db *gorm.DB) *G6UserRepository { return &G6UserRepository{db: db} }

// PublishedCatalogRow 组合已发布模型和当前活动人民币价格。
type PublishedCatalogRow struct {
	Model        model.TokenModel
	Release      model.AIModelReleaseVersion
	Snapshot     model.TokenModelReleaseSnapshot
	PriceVersion model.AIPriceVersion
	PriceSKUs    []model.AIPriceSKU
}

// ListPublishedCatalog 只返回可实际执行的已发布文字模型。
func (r *G6UserRepository) ListPublishedCatalog(ctx context.Context, at time.Time) ([]PublishedCatalogRow, error) {
	var models []model.TokenModel
	if err := r.db.WithContext(ctx).Where(
		"status = 'active' AND release_version_no > 0 AND published_at IS NOT NULL",
	).Order("sort_order ASC, id ASC").Find(&models).Error; err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return []PublishedCatalogRow{}, nil
	}
	modelIDs := make([]uint64, 0, len(models))
	for i := range models {
		modelIDs = append(modelIDs, models[i].ID)
	}
	var releases []model.AIModelReleaseVersion
	if err := r.db.WithContext(ctx).Where("model_id IN ? AND status = 'active'", modelIDs).Order("model_id ASC, version_no DESC").Find(&releases).Error; err != nil {
		return nil, err
	}
	releaseByModel := make(map[uint64]model.AIModelReleaseVersion, len(models))
	snapshotByModel := make(map[uint64]model.TokenModelReleaseSnapshot, len(models))
	modelCodes := make([]string, 0, len(models))
	for i := range releases {
		if _, exists := releaseByModel[releases[i].ModelID]; exists {
			continue
		}
		var snapshot model.TokenModelReleaseSnapshot
		if json.Unmarshal(releases[i].SnapshotJSON, &snapshot) != nil || snapshot.LogicalModelCode == "" || snapshot.Modality != "chat" {
			continue
		}
		releaseByModel[releases[i].ModelID], snapshotByModel[releases[i].ModelID] = releases[i], snapshot
		modelCodes = append(modelCodes, snapshot.LogicalModelCode)
	}
	var routableCodes []string
	if len(modelCodes) > 0 {
		if err := r.db.WithContext(ctx).Table("ai_model_routes AS routes").Distinct("routes.logical_model_code").
			Joins("JOIN token_channels AS channels ON channels.id = routes.channel_id").
			Joins("LEFT JOIN ai_model_route_runtime_states AS runtime ON runtime.route_id = routes.id").
			Where("routes.logical_model_code IN ? AND routes.status = 'active' AND channels.status = 'active' AND channels.health_status = 'healthy' AND (runtime.circuit_open_until IS NULL OR runtime.circuit_open_until <= ?)", modelCodes, at.UTC()).
			Pluck("routes.logical_model_code", &routableCodes).Error; err != nil {
			return nil, err
		}
	}
	routable := make(map[string]bool, len(routableCodes))
	for _, code := range routableCodes {
		routable[code] = true
	}
	var versions []model.AIPriceVersion
	if len(routableCodes) > 0 {
		if err := r.db.WithContext(ctx).Where("logical_model_code IN ? AND status = 'active' AND currency = 'CNY' AND effective_at <= ? AND (expires_at IS NULL OR expires_at > ?) AND cost_expires_at > ?", routableCodes, at, at, at).
			Order("logical_model_code ASC, version_no DESC").Find(&versions).Error; err != nil {
			return nil, err
		}
	}
	versionByCode := make(map[string]model.AIPriceVersion, len(versions))
	versionIDs := make([]uint64, 0, len(versions))
	for i := range versions {
		if _, exists := versionByCode[versions[i].LogicalModelCode]; !exists {
			versionByCode[versions[i].LogicalModelCode] = versions[i]
			versionIDs = append(versionIDs, versions[i].ID)
		}
	}
	var allSKUs []model.AIPriceSKU
	if len(versionIDs) > 0 {
		if err := r.db.WithContext(ctx).Where("price_version_id IN ?", versionIDs).Order("meter_type ASC, id ASC").Find(&allSKUs).Error; err != nil {
			return nil, err
		}
	}
	skusByVersion := make(map[uint64][]model.AIPriceSKU, len(versionIDs))
	for i := range allSKUs {
		skusByVersion[allSKUs[i].PriceVersionID] = append(skusByVersion[allSKUs[i].PriceVersionID], allSKUs[i])
	}
	rows := make([]PublishedCatalogRow, 0, len(models))
	for i := range models {
		release, ok := releaseByModel[models[i].ID]
		if !ok {
			continue
		}
		snapshot := snapshotByModel[models[i].ID]
		if !routable[snapshot.LogicalModelCode] {
			continue
		}
		version, ok := versionByCode[snapshot.LogicalModelCode]
		if !ok || len(skusByVersion[version.ID]) == 0 {
			continue
		}
		rows = append(rows, PublishedCatalogRow{Model: models[i], Release: release, Snapshot: snapshot, PriceVersion: version, PriceSKUs: skusByVersion[version.ID]})
	}
	return rows, nil
}

// G6RequestFilter 是用户请求账本过滤条件；UserID 必须由鉴权上下文提供。
type G6RequestFilter struct {
	UserID           uint64
	ProjectID        *uint64
	APIKeyID         *uint64
	LogicalModelCode string
	Status           string
	Start            *time.Time
	End              *time.Time
}

// RequestLedgerRow 是请求账本数据库投影视图。
type RequestLedgerRow struct {
	RequestID        string
	ProjectID        uint64
	ProjectName      string
	APIKeyID         uint64
	APIKeyName       string
	APIKeyPrefix     string
	LogicalModelCode string
	ModerationStatus string
	ExecutionStatus  string
	BillingStatus    string
	InputTokens      decimal.Decimal
	OutputTokens     decimal.Decimal
	ReasoningTokens  decimal.Decimal
	CachedTokens     decimal.Decimal
	QuotedAmount     *decimal.Decimal
	SettledAmount    *decimal.Decimal
	ErrorCode        *string
	CreatedAt        time.Time
	CompletedAt      *time.Time
}

func (r *G6UserRepository) requestLedgerQuery(ctx context.Context, filter G6RequestFilter) *gorm.DB {
	usage := r.db.Table("ai_usage_items").
		Select("request_id, SUM(CASE WHEN source = 'provider' AND sequence_no = 0 AND meter_type = 'input_tokens' THEN quantity ELSE 0 END) AS input_tokens, SUM(CASE WHEN source = 'provider' AND sequence_no = 0 AND meter_type = 'output_tokens' THEN quantity ELSE 0 END) AS output_tokens, SUM(CASE WHEN source = 'provider' AND sequence_no = 0 AND meter_type = 'reasoning_tokens' THEN quantity ELSE 0 END) AS reasoning_tokens, SUM(CASE WHEN source = 'provider' AND sequence_no = 0 AND meter_type = 'cached_tokens' THEN quantity ELSE 0 END) AS cached_tokens").
		Group("request_id")
	query := r.db.WithContext(ctx).Table("ai_requests AS requests").
		Joins("JOIN ai_projects AS projects ON projects.id = requests.project_id AND projects.user_id = requests.user_id").
		Joins("JOIN api_keys AS keys_table ON keys_table.id = requests.api_key_id AND keys_table.user_id = requests.user_id").
		Joins("LEFT JOIN (?) AS usage_totals ON usage_totals.request_id = requests.request_id", usage).
		Where("requests.user_id = ?", filter.UserID)
	if filter.ProjectID != nil {
		query = query.Where("requests.project_id = ?", *filter.ProjectID)
	}
	if filter.APIKeyID != nil {
		query = query.Where("requests.api_key_id = ?", *filter.APIKeyID)
	}
	if filter.LogicalModelCode != "" {
		query = query.Where("requests.logical_model_code = ?", filter.LogicalModelCode)
	}
	if filter.Status != "" {
		query = query.Where("(requests.execution_status = ? OR requests.billing_status = ? OR requests.moderation_status = ?)", filter.Status, filter.Status, filter.Status)
	}
	if filter.Start != nil {
		query = query.Where("requests.created_at >= ?", *filter.Start)
	}
	if filter.End != nil {
		query = query.Where("requests.created_at <= ?", *filter.End)
	}
	return query
}

func requestLedgerSelect() string {
	return "requests.request_id, requests.project_id, projects.name AS project_name, requests.api_key_id, keys_table.name AS api_key_name, keys_table.key_prefix AS api_key_prefix, requests.logical_model_code, requests.moderation_status, requests.execution_status, requests.billing_status, COALESCE(usage_totals.input_tokens, 0) AS input_tokens, COALESCE(usage_totals.output_tokens, 0) AS output_tokens, COALESCE(usage_totals.reasoning_tokens, 0) AS reasoning_tokens, COALESCE(usage_totals.cached_tokens, 0) AS cached_tokens, requests.quoted_amount, requests.settled_amount, requests.error_code, requests.created_at, requests.completed_at"
}

func (r *G6UserRepository) ListRequests(ctx context.Context, filter G6RequestFilter, offset, limit int) ([]RequestLedgerRow, int64, error) {
	query := r.requestLedgerQuery(ctx, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []RequestLedgerRow
	if err := query.Select(requestLedgerSelect()).Order("requests.created_at DESC, requests.id DESC").Offset(offset).Limit(limit).Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *G6UserRepository) FindRequestRow(ctx context.Context, userID uint64, requestID string) (*RequestLedgerRow, error) {
	query := r.requestLedgerQuery(ctx, G6RequestFilter{UserID: userID})
	var row RequestLedgerRow
	if err := query.Select(requestLedgerSelect()).Where("requests.request_id = ?", requestID).Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *G6UserRepository) FindRequestFact(ctx context.Context, userID uint64, requestID string) (*model.AIRequest, error) {
	var request model.AIRequest
	if err := r.db.WithContext(ctx).Where("user_id = ? AND request_id = ?", userID, requestID).Take(&request).Error; err != nil {
		return nil, err
	}
	return &request, nil
}

func (r *G6UserRepository) ListBilledUsage(ctx context.Context, requestID string) ([]model.AIUsageItem, error) {
	var items []model.AIUsageItem
	err := r.db.WithContext(ctx).Where("request_id = ? AND source = 'provider' AND sequence_no = 1", requestID).
		Order("meter_type ASC, id ASC").Find(&items).Error
	return items, err
}

func (r *G6UserRepository) FindWalletLink(ctx context.Context, requestID string) (*model.AIRequestWalletLink, error) {
	var link model.AIRequestWalletLink
	err := r.db.WithContext(ctx).Where("request_id = ?", requestID).Take(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &link, err
}

// UsageAggregate 是一段时间内的本人用量聚合。
type UsageAggregate struct {
	Requests     int64
	InputTokens  decimal.Decimal
	OutputTokens decimal.Decimal
	Amount       decimal.Decimal
}

// OwnedResourceScope 是用户拥有的 Project 或 Project SK，用于安全地绑定策略覆盖。
type OwnedResourceScope struct {
	ScopeType string
	ScopeID   uint64
	ProjectID uint64
	Name      string
}

func (r *G6UserRepository) ListOwnedResourceScopes(ctx context.Context, userID uint64) ([]OwnedResourceScope, error) {
	var projects []OwnedResourceScope
	if err := r.db.WithContext(ctx).Table("ai_projects").Select("'project' AS scope_type, id AS scope_id, id AS project_id, name").Where("user_id = ? AND status <> 'archived'", userID).Scan(&projects).Error; err != nil {
		return nil, err
	}
	var keys []OwnedResourceScope
	if err := r.db.WithContext(ctx).Table("api_keys").Select("'api_key' AS scope_type, id AS scope_id, project_id, name").Where("user_id = ? AND project_id IS NOT NULL AND status = 'active'", userID).Scan(&keys).Error; err != nil {
		return nil, err
	}
	return append(projects, keys...), nil
}

func (r *G6UserRepository) FindBudgetPolicies(ctx context.Context, scopes []OwnedResourceScope) (map[string]model.AIBudgetPolicy, error) {
	conditions := make([]string, 0, len(scopes))
	args := make([]interface{}, 0, len(scopes)*2)
	for i := range scopes {
		conditions = append(conditions, "(scope_type = ? AND scope_id = ?)")
		args = append(args, scopes[i].ScopeType, scopes[i].ScopeID)
	}
	if len(conditions) == 0 {
		return map[string]model.AIBudgetPolicy{}, nil
	}
	var policies []model.AIBudgetPolicy
	if err := r.db.WithContext(ctx).Where("("+strings.Join(conditions, " OR ")+")", args...).Find(&policies).Error; err != nil {
		return nil, err
	}
	result := make(map[string]model.AIBudgetPolicy, len(policies))
	for i := range policies {
		result[policies[i].ScopeType+":"+strconv.FormatUint(policies[i].ScopeID, 10)] = policies[i]
	}
	return result, nil
}

type activeBudgetOverride struct {
	ScopeType string
	ScopeID   uint64
	Amount    decimal.Decimal
}

func (r *G6UserRepository) FindActiveBudgetOverrides(ctx context.Context, scopes []OwnedResourceScope, at time.Time) (map[string]decimal.Decimal, error) {
	conditions := make([]string, 0, len(scopes))
	args := make([]interface{}, 0, len(scopes)*2)
	for i := range scopes {
		conditions = append(conditions, "(scope_type = ? AND scope_id = ?)")
		args = append(args, scopes[i].ScopeType, scopes[i].ScopeID)
	}
	if len(conditions) == 0 {
		return map[string]decimal.Decimal{}, nil
	}
	var rows []activeBudgetOverride
	err := r.db.WithContext(ctx).Model(&model.AIBudgetOverride{}).
		Select("scope_type, scope_id, SUM(extra_amount) AS amount").
		Where("revoked_at IS NULL AND expires_at > ?", at).
		Where("("+strings.Join(conditions, " OR ")+")", args...).
		Group("scope_type, scope_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[string]decimal.Decimal, len(rows))
	for i := range rows {
		result[rows[i].ScopeType+":"+strconv.FormatUint(rows[i].ScopeID, 10)] = rows[i].Amount
	}
	return result, nil
}

func (r *G6UserRepository) FindResourcePolicies(ctx context.Context, userID uint64, scopes []OwnedResourceScope) (map[string]model.AIResourcePolicy, error) {
	conditions := []string{"(scope_type = 'user' AND scope_key = ?)"}
	args := []interface{}{userID}
	for i := range scopes {
		conditions = append(conditions, "(scope_type = ? AND scope_key = ?)")
		args = append(args, scopes[i].ScopeType, scopes[i].ScopeID)
	}
	var policies []model.AIResourcePolicy
	if err := r.db.WithContext(ctx).Where("status = 'active'").Where("("+strings.Join(conditions, " OR ")+")", args...).Find(&policies).Error; err != nil {
		return nil, err
	}
	result := make(map[string]model.AIResourcePolicy, len(policies))
	for i := range policies {
		result[policies[i].ScopeType+":"+policies[i].ScopeKey] = policies[i]
	}
	return result, nil
}

func (r *G6UserRepository) AggregateUsage(ctx context.Context, userID uint64, start, end time.Time) (UsageAggregate, error) {
	usage := r.db.Table("ai_usage_items").
		Select("request_id, SUM(CASE WHEN source = 'provider' AND sequence_no = 0 AND meter_type = 'input_tokens' THEN quantity ELSE 0 END) AS input_tokens, SUM(CASE WHEN source = 'provider' AND sequence_no = 0 AND meter_type = 'output_tokens' THEN quantity ELSE 0 END) AS output_tokens").
		Group("request_id")
	var result UsageAggregate
	err := r.db.WithContext(ctx).Table("ai_requests AS requests").
		Joins("LEFT JOIN (?) AS usage_totals ON usage_totals.request_id = requests.request_id", usage).
		Where("requests.user_id = ? AND requests.created_at >= ? AND requests.created_at < ?", userID, start, end).
		Select("COUNT(*) AS requests, COALESCE(SUM(usage_totals.input_tokens), 0) AS input_tokens, COALESCE(SUM(usage_totals.output_tokens), 0) AS output_tokens, COALESCE(SUM(requests.settled_amount), 0) AS amount").Scan(&result).Error
	return result, err
}

func (r *G6UserRepository) SumMonthlyBudget(ctx context.Context, userID uint64) (*decimal.Decimal, error) {
	var value *decimal.Decimal
	err := r.db.WithContext(ctx).Table("ai_budget_policies AS policies").
		Joins("JOIN ai_projects AS projects ON projects.id = policies.scope_id AND policies.scope_type = 'project'").
		Where("projects.user_id = ? AND policies.mode IN ('soft','hard') AND policies.monthly_limit IS NOT NULL", userID).
		Select("SUM(policies.monthly_limit)").Scan(&value).Error
	return value, err
}

func (r *G6UserRepository) CreateDispute(ctx context.Context, dispute *model.AIBillingDispute) error {
	err := r.db.WithContext(ctx).Create(dispute).Error
	if isDuplicateKey(err) {
		return ErrBillingDisputeExists
	}
	return err
}

func (r *G6UserRepository) FindDispute(ctx context.Context, userID uint64, requestID string) (*model.AIBillingDispute, error) {
	var dispute model.AIBillingDispute
	err := r.db.WithContext(ctx).Where("user_id = ? AND request_id = ?", userID, requestID).Take(&dispute).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &dispute, err
}

// UserRealNameStatus 供 Project SK 签发和轮换前重新确认实名状态。
func (r *G2Repository) UserRealNameStatus(ctx context.Context, userID uint64) (string, error) {
	var row struct{ RealNameStatus string }
	err := r.db.WithContext(ctx).Table("users").Select("real_name_status").Where("id = ?", userID).Take(&row).Error
	return row.RealNameStatus, err
}
