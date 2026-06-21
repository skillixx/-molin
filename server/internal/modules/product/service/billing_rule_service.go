package service

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/product/model"
	"molin/server/internal/modules/product/repository"
)

// 计费规则业务错误。
var (
	// ErrBillingRuleNotFound 计费规则不存在。
	ErrBillingRuleNotFound = errors.New("计费规则不存在")
	// ErrBillingProductNotFound 计费规则关联的商品不存在。
	ErrBillingProductNotFound = errors.New("商品不存在")
	// ErrInvalidPriceAmount 单价非法（必须 > 0）。
	ErrInvalidPriceAmount = errors.New("price_amount 必须大于 0")
	// ErrMissingBillingField 必填字段缺失。
	ErrMissingBillingField = errors.New("usage_type / usage_unit / billing_mode 为必填项")
	// ErrMeteredRuleExists 已存在生效的按量规则，不能再加按次规则（二选一）。
	ErrMeteredRuleExists = errors.New("该商品已配置按量计费规则，不能再加按次规则（二选一）")
	// ErrCallRuleExists 已存在生效的按次规则，不能再加按量规则（二选一）。
	ErrCallRuleExists = errors.New("该商品已配置按次计费规则，不能再加按量规则（二选一）")
)

// 按量/按次计费方式分类（契约 docs/backend-token-billing-contract.md §1：
// 同一商品的「按量」与「按次」二选一，避免重复收费）。
var (
	// meteredUsageTypes 按量类 usage_type（按 token 用量计费）。
	meteredUsageTypes = []string{"input_tokens", "output_tokens"}
	// callUsageTypes 按次类 usage_type（按调用次数计费）。
	callUsageTypes = []string{"calls"}
)

// isCallUsage 判断是否为按次类 usage_type。
func isCallUsage(usageType string) bool {
	return usageType == "calls"
}

// isMeteredUsage 判断是否为按量类 usage_type。
func isMeteredUsage(usageType string) bool {
	return usageType == "input_tokens" || usageType == "output_tokens"
}

// billingRuleRepo 计费规则仓库依赖接口（便于单测注入 fake，*repository.BillingRuleRepository 已实现）。
type billingRuleRepo interface {
	ListPaged(ctx context.Context, filter repository.BillingRuleFilter, offset, limit int) ([]model.ProductBillingRule, int64, error)
	FindByID(ctx context.Context, id uint64) (*model.ProductBillingRule, error)
	Create(ctx context.Context, rule *model.ProductBillingRule) error
	Update(ctx context.Context, id uint64, updates map[string]interface{}) error
	CountActiveByUsageTypes(ctx context.Context, productID uint64, usageTypes []string, excludeID uint64) (int64, error)
}

// billingProductRepo 商品仓库依赖接口（便于单测注入 fake）。
type billingProductRepo interface {
	FindByID(ctx context.Context, id uint64) (*model.Product, error)
}

// BillingRuleService 商品计费规则管理服务（管理端 P15/P16/P17）。
type BillingRuleService struct {
	ruleRepo    billingRuleRepo
	productRepo billingProductRepo
}

// NewBillingRuleService 创建计费规则服务实例。
func NewBillingRuleService(
	ruleRepo *repository.BillingRuleRepository,
	productRepo *repository.ProductRepository,
) *BillingRuleService {
	return &BillingRuleService{ruleRepo: ruleRepo, productRepo: productRepo}
}

// List 分页查询计费规则（P15）。
func (s *BillingRuleService) List(ctx context.Context, filter repository.BillingRuleFilter, offset, limit int) ([]model.ProductBillingRule, int64, error) {
	return s.ruleRepo.ListPaged(ctx, filter, offset, limit)
}

// Create 新增计费规则（P16），完成参数校验与商品存在性校验。
func (s *BillingRuleService) Create(ctx context.Context, rule *model.ProductBillingRule) error {
	// 必填项校验
	if rule.UsageType == "" || rule.UsageUnit == "" || rule.BillingMode == "" {
		return ErrMissingBillingField
	}
	// 金额校验：必须 > 0
	if rule.PriceAmount.LessThanOrEqual(decimal.Zero) {
		return ErrInvalidPriceAmount
	}
	// 免费额度若提供，不得为负
	if rule.FreeQuota != nil && rule.FreeQuota.IsNegative() {
		return errors.New("free_quota 不能为负数")
	}
	// 商品存在性校验
	if _, err := s.productRepo.FindByID(ctx, rule.ProductID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrBillingProductNotFound
		}
		return err
	}
	// 默认值兜底
	if rule.Currency == "" {
		rule.Currency = "CNY"
	}
	if rule.Status == "" {
		rule.Status = "active"
	}
	// 按量/按次互斥强校验（仅对将要生效 active 的规则做拦截）。
	if rule.Status == "active" {
		if err := s.checkUsageExclusive(ctx, rule.ProductID, rule.UsageType, 0); err != nil {
			return err
		}
	}
	return s.ruleRepo.Create(ctx, rule)
}

// checkUsageExclusive 校验同一商品下「按量」与「按次」计费方式互斥。
// usageType 为本次将生效的计费类型；excludeID 为编辑时排除的规则自身 ID（创建传 0）。
// 仅在同一 product_id 维度判断，不同商品互不影响。
func (s *BillingRuleService) checkUsageExclusive(ctx context.Context, productID uint64, usageType string, excludeID uint64) error {
	switch {
	case isCallUsage(usageType):
		// 新规则为按次：若已存在生效的按量规则则拦截。
		cnt, err := s.ruleRepo.CountActiveByUsageTypes(ctx, productID, meteredUsageTypes, excludeID)
		if err != nil {
			return err
		}
		return exclusiveDecision(usageType, cnt)
	case isMeteredUsage(usageType):
		// 新规则为按量：若已存在生效的按次规则则拦截。
		cnt, err := s.ruleRepo.CountActiveByUsageTypes(ctx, productID, callUsageTypes, excludeID)
		if err != nil {
			return err
		}
		return exclusiveDecision(usageType, cnt)
	}
	// 其他 usage_type（非按量/按次）不参与互斥校验。
	return nil
}

// exclusiveDecision 纯函数：给定新规则 usageType 与「对立类」已生效规则数量 oppositeCount，
// 判定是否违反按量/按次互斥。oppositeCount>0 表示对立类已存在生效规则 → 拦截。
// 抽成纯函数便于单测，不依赖数据库。
func exclusiveDecision(usageType string, oppositeCount int64) error {
	if oppositeCount <= 0 {
		return nil
	}
	if isCallUsage(usageType) {
		// 新增按次，但已存在按量 → 拒绝
		return ErrMeteredRuleExists
	}
	if isMeteredUsage(usageType) {
		// 新增按量，但已存在按次 → 拒绝
		return ErrCallRuleExists
	}
	return nil
}

// Update 部分更新计费规则（P17）。
func (s *BillingRuleService) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	// 规则存在性校验
	existing, err := s.ruleRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrBillingRuleNotFound
		}
		return err
	}
	// 若更新单价，校验 > 0
	if v, ok := updates["price_amount"]; ok {
		if amount, ok := v.(decimal.Decimal); ok && amount.LessThanOrEqual(decimal.Zero) {
			return ErrInvalidPriceAmount
		}
	}
	if len(updates) == 0 {
		return nil
	}
	// 计算更新后的最终 usage_type 与 status（部分更新需结合现值）。
	finalUsageType := existing.UsageType
	if v, ok := updates["usage_type"].(string); ok && v != "" {
		finalUsageType = v
	}
	finalStatus := existing.Status
	if v, ok := updates["status"].(string); ok && v != "" {
		finalStatus = v
	}
	// 编辑/启用（status active 化或改 usage_type）后仍要满足按量/按次互斥。
	// 排除规则自身，避免把自身计入冲突。
	if finalStatus == "active" {
		if err := s.checkUsageExclusive(ctx, existing.ProductID, finalUsageType, id); err != nil {
			return err
		}
	}
	return s.ruleRepo.Update(ctx, id, updates)
}

// GetByID 查询单条计费规则（用于返回详情）。
func (s *BillingRuleService) GetByID(ctx context.Context, id uint64) (*model.ProductBillingRule, error) {
	rule, err := s.ruleRepo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBillingRuleNotFound
		}
		return nil, err
	}
	return rule, nil
}
