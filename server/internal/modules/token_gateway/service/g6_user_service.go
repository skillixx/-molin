package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	pkgcrypto "molin/server/pkg/crypto"
)

var (
	ErrPublicModelNotFound             = errors.New("模型不存在、未发布或当前不可用")
	ErrUserRequestNotFound             = errors.New("请求记录不存在")
	ErrDisputeReasonInvalid            = errors.New("申诉说明长度应为 10 至 1000 个字符")
	ErrDisputeContainsSecret           = errors.New("申诉说明不能包含 API Key、Token 或其他密钥")
	ErrDisputeContainsUnverifiedSecret = errors.New("申诉说明不能包含 API Key、Token 或其他密钥")
)

var credentialLikePattern = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|bearer\s+[a-z0-9._~+/=-]{8,}|(?:api[_ -]?key|access[_ -]?token|secret)\s*[:=]\s*\S{6,}|eyJ[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}\.[a-z0-9_-]{8,})`)

// 平台 SK 的随机段使用 Base64URL 无填充编码，因此除字母数字外还必须允许连字符和下划线。
var platformAPIKeyPattern = regexp.MustCompile(`sk-molin-[0-9A-Za-z_-]{16,128}`)

// ConfirmedCredentialLeakError 只表示 HMAC 已精确匹配请求所属有效平台 SK 的确认泄漏，不携带明文或 Hash。
type ConfirmedCredentialLeakError struct {
	APIKeyID uint64
}

func (e *ConfirmedCredentialLeakError) Error() string { return ErrDisputeContainsSecret.Error() }
func (e *ConfirmedCredentialLeakError) Unwrap() error { return ErrDisputeContainsSecret }

const publicMinimumCharge = "0.000001"

// PublicModelFilter 是模型市场搜索、筛选和排序条件。
type PublicModelFilter struct {
	Keyword       string
	Provider      string
	Capability    string
	ServiceStatus string
	ContextMin    uint64
	ContextMax    uint64
	Sort          string
}

// G6UserService 组合已发布目录、价格和用户请求账本，不参与执行或结算状态写入。
type G6UserService struct {
	repo             *repository.G6UserRepository
	catalog          *CatalogService
	now              func() time.Time
	resourceDefaults ResourceDefaults
	apiKeyHMACSecret string
}

func (s *G6UserService) WithResourceDefaults(defaults ResourceDefaults) *G6UserService {
	s.resourceDefaults = defaults
	return s
}

// WithAPIKeyHMACSecret 注入平台 SK 的 HMAC 密钥，仅用于确认命中，不保存或输出用户提交的凭据。
func (s *G6UserService) WithAPIKeyHMACSecret(secret string) *G6UserService {
	s.apiKeyHMACSecret = secret
	return s
}

func NewG6UserService(repo *repository.G6UserRepository, catalog *CatalogService) *G6UserService {
	return &G6UserService{repo: repo, catalog: catalog, now: time.Now}
}

func (s *G6UserService) ListModels(ctx context.Context, userID uint64, filter PublicModelFilter, offset, limit int) ([]dto.PublicModelCatalogItem, int64, error) {
	rows, err := s.repo.ListPublishedCatalog(ctx, s.now())
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.PublicModelCatalogItem, 0, len(rows))
	keyword := strings.ToLower(strings.TrimSpace(filter.Keyword))
	provider := strings.ToLower(strings.TrimSpace(filter.Provider))
	capability := strings.ToLower(strings.TrimSpace(filter.Capability))
	for i := range rows {
		// 可见性必须按不可变发布快照判断，防止尚未发布的后台编辑提前影响客户目录。
		snapshotModel := model.TokenModel{VisibleScope: rows[i].Snapshot.VisibleScope, TargetAudience: rows[i].Snapshot.TargetAudience}
		if !modelVisibleTo(ctx, &snapshotModel, userID, s.catalog.groupResolver, s.catalog.roleResolver) {
			continue
		}
		item := publicCatalogDTO(rows[i])
		searchable := strings.ToLower(item.DisplayName + " " + item.LogicalModelCode + " " + item.ProviderName)
		if keyword != "" && !strings.Contains(searchable, keyword) {
			continue
		}
		if provider != "" && strings.ToLower(item.ProviderName) != provider {
			continue
		}
		if filter.ContextMin > 0 && item.ContextWindow < filter.ContextMin {
			continue
		}
		if filter.ContextMax > 0 && item.ContextWindow > filter.ContextMax {
			continue
		}
		if capability != "" && !capabilityEnabled(item.Capabilities, capability) {
			continue
		}
		if filter.ServiceStatus != "" && !strings.EqualFold(strings.TrimSpace(filter.ServiceStatus), item.ServiceStatus) {
			continue
		}
		items = append(items, item)
	}
	sortPublicModels(items, filter.Sort)
	total := int64(len(items))
	start := offset
	if start < 0 || start > len(items) {
		start = len(items)
	}
	end := start + limit
	if limit <= 0 || end > len(items) {
		end = len(items)
	}
	return items[start:end], total, nil
}

func capabilityEnabled(raw json.RawMessage, capability string) bool {
	var value interface{}
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(text), capability) {
				return true
			}
		}
	case map[string]interface{}:
		var enabled interface{}
		var exists bool
		for key, value := range typed {
			if strings.EqualFold(strings.TrimSpace(key), capability) {
				enabled, exists = value, true
				break
			}
		}
		if !exists {
			return false
		}
		switch flag := enabled.(type) {
		case bool:
			return flag
		case string:
			return strings.EqualFold(flag, "true")
		}
	}
	return false
}

func (s *G6UserService) GetModel(ctx context.Context, userID uint64, modelCode string) (*dto.PublicModelCatalogItem, error) {
	items, _, err := s.ListModels(ctx, userID, PublicModelFilter{}, 0, 0)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].LogicalModelCode == strings.TrimSpace(modelCode) {
			return &items[i], nil
		}
	}
	return nil, ErrPublicModelNotFound
}

func publicCatalogDTO(row repository.PublishedCatalogRow) dto.PublicModelCatalogItem {
	prices := make([]dto.PublicPriceSKU, 0, len(row.PriceSKUs))
	for i := range row.PriceSKUs {
		prices = append(prices, dto.PublicPriceSKU{
			MeterType: row.PriceSKUs[i].MeterType, SaleUnitPrice: row.PriceSKUs[i].SaleUnitPrice,
			Scale: row.PriceSKUs[i].Scale, Currency: row.PriceSKUs[i].Currency,
		})
	}
	snapshot := row.Snapshot
	minimumCharge := publicMinimumCharge
	if snapshot.Modality == "image" {
		minimumCharge = row.PriceVersion.MinimumCharge.StringFixed(8)
	}
	return dto.PublicModelCatalogItem{
		LogicalModelCode: snapshot.LogicalModelCode, DisplayName: snapshot.DisplayName,
		ProviderName: snapshot.ProviderName, Description: snapshot.Description,
		Capabilities: snapshot.Capabilities, ContextWindow: snapshot.ContextWindow, Modality: snapshot.Modality,
		IntroURL: snapshot.IntroURL, IntroURLHealthStatus: publishedDocumentHealth(snapshot.IntroURL, row.Model.IntroURL, snapshot.IntroURLHealthStatus, row.Model.IntroURLHealthStatus),
		DocsURL: snapshot.DocsURL, DocsURLHealthStatus: publishedDocumentHealth(snapshot.DocsURL, row.Model.DocsURL, snapshot.DocsURLHealthStatus, row.Model.DocsURLHealthStatus),
		QuickStartURL: snapshot.QuickStartURL, QuickStartURLHealthStatus: publishedDocumentHealth(snapshot.QuickStartURL, row.Model.QuickStartURL, snapshot.QuickStartURLHealthStatus, row.Model.QuickStartURLHealthStatus),
		ReleaseVersionNo: row.Release.VersionNo, PublishedAt: row.Release.PublishedAt.UTC(),
		PriceVersionNo: row.PriceVersion.VersionNo, PriceEffectiveAt: row.PriceVersion.EffectiveAt,
		FailureChargePolicy: row.PriceVersion.FailureChargePolicy, RoundingMode: row.PriceVersion.RoundingMode,
		MinimumCharge: minimumCharge, ServiceStatus: "available", Prices: prices,
	}
}

// G5 已存在的历史快照没有文档健康字段；仅当工作副本 URL 与发布 URL 完全一致时兼容读取迁移后的状态。
func publishedDocumentHealth(publishedURL, currentURL *string, publishedStatus, currentStatus string) string {
	if publishedURL == nil || strings.TrimSpace(*publishedURL) == "" {
		return "unpublished"
	}
	if publishedStatus != "" {
		return publishedStatus
	}
	if currentURL != nil && strings.TrimSpace(*currentURL) == strings.TrimSpace(*publishedURL) && currentStatus != "" {
		return currentStatus
	}
	return "unknown"
}

func sortPublicModels(items []dto.PublicModelCatalogItem, value string) {
	switch strings.TrimSpace(value) {
	case "price_asc":
		sort.SliceStable(items, func(i, j int) bool { return primaryPrice(items[i]).LessThan(primaryPrice(items[j])) })
	case "context_desc":
		sort.SliceStable(items, func(i, j int) bool { return items[i].ContextWindow > items[j].ContextWindow })
	case "latest":
		sort.SliceStable(items, func(i, j int) bool { return items[i].PublishedAt.After(items[j].PublishedAt) })
	default:
		sort.SliceStable(items, func(i, j int) bool { return items[i].DisplayName < items[j].DisplayName })
	}
}

func primaryPrice(item dto.PublicModelCatalogItem) decimal.Decimal {
	for _, meter := range []string{"input_tokens", "output_tokens"} {
		for i := range item.Prices {
			if item.Prices[i].MeterType == meter {
				return item.Prices[i].SaleUnitPrice.Div(item.Prices[i].Scale)
			}
		}
	}
	return decimal.NewFromInt(1 << 30)
}

func (s *G6UserService) ListRequests(ctx context.Context, filter repository.G6RequestFilter, offset, limit int) ([]dto.UserRequestLedgerItem, int64, error) {
	rows, total, err := s.repo.ListRequests(ctx, filter, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	items := make([]dto.UserRequestLedgerItem, len(rows))
	for i := range rows {
		items[i] = requestRowDTO(rows[i])
	}
	return items, total, nil
}

func (s *G6UserService) Overview(ctx context.Context, userID uint64, timezone string) (*dto.UsageOverview, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location, _ = time.LoadLocation("Asia/Shanghai")
	}
	now := s.now().In(location)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
	today, err := s.repo.AggregateUsage(ctx, userID, dayStart.UTC(), dayStart.AddDate(0, 0, 1).UTC())
	if err != nil {
		return nil, err
	}
	month, err := s.repo.AggregateUsage(ctx, userID, monthStart.UTC(), monthStart.AddDate(0, 1, 0).UTC())
	if err != nil {
		return nil, err
	}
	budget, err := s.repo.SumMonthlyBudget(ctx, userID, s.now())
	if err != nil {
		return nil, err
	}
	var percent *decimal.Decimal
	if budget != nil && budget.GreaterThan(decimal.Zero) {
		budgetUsage, usageErr := s.repo.SumCurrentProjectBudgetUsage(ctx, userID, s.now())
		if usageErr != nil {
			return nil, usageErr
		}
		value := budgetUsage.Div(*budget).Mul(decimal.NewFromInt(100)).Round(2)
		percent = &value
	}
	return &dto.UsageOverview{
		TodayRequests: today.Requests, TodayInputTokens: today.InputTokens, TodayOutputTokens: today.OutputTokens, TodayAmount: today.Amount,
		MonthRequests: month.Requests, MonthInputTokens: month.InputTokens, MonthOutputTokens: month.OutputTokens, MonthAmount: month.Amount,
		MonthlyBudget: budget, MonthlyBudgetUsage: percent, Currency: "CNY",
	}, nil
}

func (s *G6UserService) ResourceLimits(ctx context.Context, userID uint64) (*dto.UserResourceLimits, error) {
	scopes, err := s.repo.ListOwnedResourceScopes(ctx, userID)
	if err != nil {
		return nil, err
	}
	policies, err := s.repo.FindResourcePolicies(ctx, userID, scopes)
	if err != nil {
		return nil, err
	}
	budgets, err := s.repo.FindBudgetPolicies(ctx, scopes)
	if err != nil {
		return nil, err
	}
	overrides, err := s.repo.FindActiveBudgetOverrides(ctx, scopes, s.now())
	if err != nil {
		return nil, err
	}
	userLimit := effectiveLimit("user", userID, "本人总限制", s.resourceDefaults.User, policies)
	result := &dto.UserResourceLimits{User: userLimit}
	projectLimits := make(map[uint64]dto.EffectiveResourceLimit)
	for i := range scopes {
		defaults := s.resourceDefaults.Project
		if scopes[i].ScopeType == "api_key" {
			defaults = s.resourceDefaults.APIKey
		}
		limit := effectiveLimit(scopes[i].ScopeType, scopes[i].ScopeID, scopes[i].Name, defaults, policies)
		if scopes[i].ScopeType == "project" {
			limit = clampLimit(limit, userLimit)
			key := "project:" + strconv.FormatUint(scopes[i].ScopeID, 10)
			applyBudget(&limit, budgets[key], overrides[key])
			projectLimits[scopes[i].ScopeID] = limit
			result.Projects = append(result.Projects, limit)
		} else {
			limit = clampLimit(limit, userLimit)
			if parent, ok := projectLimits[scopes[i].ProjectID]; ok {
				limit = clampLimit(limit, parent)
			}
			key := "api_key:" + strconv.FormatUint(scopes[i].ScopeID, 10)
			applyBudget(&limit, budgets[key], overrides[key])
			result.APIKeys = append(result.APIKeys, limit)
		}
	}
	return result, nil
}

func clampLimit(child, parent dto.EffectiveResourceLimit) dto.EffectiveResourceLimit {
	before := child
	child.Concurrency = minPositive(child.Concurrency, parent.Concurrency)
	child.RPM = minPositive(child.RPM, parent.RPM)
	child.TPM = minPositive(child.TPM, parent.TPM)
	if child.Concurrency != before.Concurrency || child.RPM != before.RPM || child.TPM != before.TPM {
		child.Source = "inherited_parent"
	}
	return child
}

func minPositive(left, right uint64) uint64 {
	if left == 0 {
		return right
	}
	if right == 0 || left < right {
		return left
	}
	return right
}

func applyBudget(limit *dto.EffectiveResourceLimit, policy model.AIBudgetPolicy, override decimal.Decimal) {
	if policy.ID == 0 {
		limit.BudgetMode = model.AIBudgetDisabled
		return
	}
	limit.BudgetMode, limit.DailyBudget, limit.MonthlyBudget = policy.Mode, policy.DailyLimit, policy.MonthlyLimit
	if override.GreaterThan(decimal.Zero) {
		limit.BudgetOverride = &override
		if limit.DailyBudget != nil {
			value := limit.DailyBudget.Add(override)
			limit.DailyBudget = &value
		}
		if limit.MonthlyBudget != nil {
			value := limit.MonthlyBudget.Add(override)
			limit.MonthlyBudget = &value
		}
	}
}

func effectiveLimit(scopeType string, scopeID uint64, name string, defaults ResourceLimits, policies map[string]model.AIResourcePolicy) dto.EffectiveResourceLimit {
	result := dto.EffectiveResourceLimit{ScopeType: scopeType, ScopeID: scopeID, Name: name, Concurrency: defaults.Concurrency, RPM: defaults.RPM, TPM: defaults.TPM, Source: "platform_default"}
	if policy, ok := policies[scopeType+":"+strconv.FormatUint(scopeID, 10)]; ok {
		result.Concurrency, result.RPM, result.TPM, result.Source = policy.ConcurrencyLimit, policy.RPMLimit, policy.TPMLimit, "policy_override"
	}
	return result
}

func (s *G6UserService) RequestDetail(ctx context.Context, userID uint64, requestID string) (*dto.UserRequestDetail, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || len(requestID) > 128 {
		return nil, ErrUserRequestNotFound
	}
	row, err := s.repo.FindRequestRow(ctx, userID, requestID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUserRequestNotFound
	}
	if err != nil {
		return nil, err
	}
	fact, err := s.repo.FindRequestFact(ctx, userID, requestID)
	if err != nil {
		return nil, err
	}
	var snapshot PriceSnapshot
	if len(fact.PriceSnapshotJSON) > 0 {
		if err := json.Unmarshal(fact.PriceSnapshotJSON, &snapshot); err != nil {
			return nil, err
		}
	}
	billed, err := s.repo.ListBilledUsage(ctx, requestID)
	if err != nil {
		return nil, err
	}
	lines := make([]dto.UserRequestPriceLine, 0, len(billed))
	for i := range billed {
		sku, ok := snapshot.SKUs[billed[i].MeterType]
		if !ok || billed[i].UnitPrice == nil || billed[i].Amount == nil {
			continue
		}
		scale, parseErr := decimal.NewFromString(sku.Scale)
		if parseErr != nil {
			continue
		}
		lines = append(lines, dto.UserRequestPriceLine{
			MeterType: billed[i].MeterType, MeterSource: publicMeterSource(billed[i].Source), Quantity: billed[i].Quantity, SaleUnitPrice: *billed[i].UnitPrice,
			Scale: scale, Amount: *billed[i].Amount, Currency: snapshot.Currency,
		})
	}
	detail := &dto.UserRequestDetail{UserRequestLedgerItem: requestRowDTO(*row), PriceVersionID: snapshot.PriceVersionID,
		PriceVersionNo: snapshot.VersionNo, FailureChargePolicy: snapshot.FailureChargePolicy,
		RoundingMode: snapshot.RoundingMode, MinimumCharge: snapshot.MinimumCharge, PriceLines: lines}
	link, err := s.repo.FindWalletLink(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if link != nil {
		detail.WalletHoldID = &link.WalletHoldID
		detail.SettleTransactionID = link.SettleTransactionID
		detail.ReleaseTransactionID = link.ReleaseTransactionID
	}
	dispute, err := s.repo.FindDispute(ctx, userID, requestID)
	if err != nil {
		return nil, err
	}
	if dispute != nil {
		resp := disputeDTO(dispute)
		detail.Dispute = &resp
	}
	return detail, nil
}

// publicMeterSource 将内部计量来源收敛为稳定的客户接口枚举，避免泄漏仓储实现值。
func publicMeterSource(source string) string {
	if source == "reconciled" {
		return "reconciled"
	}
	return "provider_confirmed"
}

func (s *G6UserService) CreateDispute(ctx context.Context, userID uint64, requestID, reason string) (*dto.BillingDisputeResp, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || len(requestID) > 128 {
		return nil, ErrUserRequestNotFound
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 10 || len([]rune(reason)) > 1000 {
		return nil, ErrDisputeReasonInvalid
	}
	request, err := s.repo.FindRequestFact(ctx, userID, requestID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUserRequestNotFound
	} else if err != nil {
		return nil, err
	}
	if credentialLikePattern.MatchString(reason) {
		confirmed, confirmErr := s.confirmedRequestCredential(ctx, userID, request, reason)
		if confirmErr != nil {
			return nil, confirmErr
		}
		if confirmed {
			return nil, &ConfirmedCredentialLeakError{APIKeyID: *request.APIKeyID}
		}
		// 疑似但无法确认的文本同样禁止入库，但不能污染需要 P0 处置的确认泄漏数据源。
		return nil, ErrDisputeContainsUnverifiedSecret
	}
	disputeNo, err := newDisputeNo()
	if err != nil {
		return nil, err
	}
	dispute := &model.AIBillingDispute{DisputeNo: disputeNo, RequestID: requestID, UserID: userID, Reason: reason, Status: "submitted"}
	if err := s.repo.CreateDispute(ctx, dispute); err != nil {
		return nil, err
	}
	resp := disputeDTO(dispute)
	return &resp, nil
}

func (s *G6UserService) confirmedRequestCredential(ctx context.Context, userID uint64, request *model.AIRequest, reason string) (bool, error) {
	if s == nil || s.repo == nil || request == nil || request.APIKeyID == nil || *request.APIKeyID == 0 || s.apiKeyHMACSecret == "" {
		return false, nil
	}
	storedHash, err := s.repo.FindActiveAPIKeyHash(ctx, userID, *request.APIKeyID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return matchesPlatformCredential(reason, storedHash, s.apiKeyHMACSecret), nil
}

func matchesPlatformCredential(reason, storedHash, hmacSecret string) bool {
	if storedHash == "" || hmacSecret == "" {
		return false
	}
	for _, candidate := range platformAPIKeyPattern.FindAllString(reason, -1) {
		candidateHash := pkgcrypto.HMAC256(candidate, hmacSecret)
		if subtle.ConstantTimeCompare([]byte(candidateHash), []byte(storedHash)) == 1 {
			return true
		}
	}
	return false
}

func requestRowDTO(row repository.RequestLedgerRow) dto.UserRequestLedgerItem {
	return dto.UserRequestLedgerItem{
		RequestID: row.RequestID, ProjectID: row.ProjectID, ProjectName: row.ProjectName,
		APIKeyID: row.APIKeyID, APIKeyName: row.APIKeyName, APIKeyPrefix: row.APIKeyPrefix,
		LogicalModelCode: row.LogicalModelCode, ModerationStatus: row.ModerationStatus,
		ExecutionStatus: row.ExecutionStatus, BillingStatus: row.BillingStatus,
		InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, ReasoningTokens: row.ReasoningTokens,
		CachedTokens: row.CachedTokens, QuotedAmount: row.QuotedAmount, SettledAmount: row.SettledAmount,
		ErrorCode: row.ErrorCode, CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt,
	}
}

func disputeDTO(dispute *model.AIBillingDispute) dto.BillingDisputeResp {
	return dto.BillingDisputeResp{DisputeNo: dispute.DisputeNo, RequestID: dispute.RequestID, Reason: dispute.Reason,
		Status: dispute.Status, Resolution: dispute.Resolution, ResolvedAt: dispute.ResolvedAt, CreatedAt: dispute.CreatedAt}
}

func newDisputeNo() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "DSP-" + strings.ToUpper(hex.EncodeToString(buffer)), nil
}
