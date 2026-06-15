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
)

// BillingRuleService 商品计费规则管理服务（管理端 P15/P16/P17）。
type BillingRuleService struct {
	ruleRepo    *repository.BillingRuleRepository
	productRepo *repository.ProductRepository
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
	return s.ruleRepo.Create(ctx, rule)
}

// Update 部分更新计费规则（P17）。
func (s *BillingRuleService) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	// 规则存在性校验
	if _, err := s.ruleRepo.FindByID(ctx, id); err != nil {
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
