package service

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/product/repository"
)

// ErrNoPriceConfigured 未找到任何价格配置。
var ErrNoPriceConfigured = errors.New("该套餐未配置价格")

// PricingService 负责按优先级计算用户实际购买价格。
// 价格优先级（严格按此顺序）：
//  1. 会员专属价（membership_level_id IS NOT NULL AND role_id IS NULL）
//  2. 角色价（role_id IS NOT NULL AND membership_level_id IS NULL），多角色取最低价
//  3. 默认价（role_id IS NULL AND membership_level_id IS NULL）
type PricingService struct {
	priceRepo     *repository.PriceRepository
	iamSvc        IAMService
	membershipSvc MembershipService
}

// NewPricingService 创建定价服务实例。
func NewPricingService(
	priceRepo *repository.PriceRepository,
	iamSvc IAMService,
	membershipSvc MembershipService,
) *PricingService {
	return &PricingService{
		priceRepo:     priceRepo,
		iamSvc:        iamSvc,
		membershipSvc: membershipSvc,
	}
}

// CheckMembershipGate 校验"会员专属"购买门槛（修复 P1 缺陷：非会员可绕过会员专属门槛下单）。
//
// 判定规则：
//   - 若该套餐未配置任何会员专属价（membership_level_id IS NOT NULL AND role_id IS NULL），
//     则不构成"会员专属"门槛，直接放行；
//   - 若已配置会员专属价，则只有当前用户持有有效会员资格、且其等级命中配置的等级集合之一时，
//     才允许购买；否则视为无购买权限（对应 ErrNoAccess / 40003）。
//
// 返回 true 表示允许购买（放行），false 表示应拦截。
func (s *PricingService) CheckMembershipGate(ctx context.Context, planID, userID uint64) (bool, error) {
	levelIDs, err := s.priceRepo.ListMembershipLevelIDs(ctx, planID)
	if err != nil {
		return false, err
	}
	// 未配置会员专属价：非会员专属商品，不设购买门槛
	if len(levelIDs) == 0 {
		return true, nil
	}
	// 已配置会员专属价：要求用户持有命中等级之一的有效会员资格
	ok, err := s.membershipSvc.HasActiveLevelIn(ctx, userID, levelIDs)
	if err != nil {
		return false, err
	}
	return ok, nil
}

// GetPrice 按优先级返回用户对指定套餐的实际购买价格。
func (s *PricingService) GetPrice(ctx context.Context, planID, userID uint64) (decimal.Decimal, error) {
	// 1. 查用户活跃会员等级，有则优先使用会员价
	membership, _ := s.membershipSvc.GetActive(ctx, userID)
	if membership != nil {
		price, err := s.priceRepo.FindMembershipPrice(ctx, planID, membership.LevelID)
		if err == nil {
			return price.PriceAmount, nil
		}
	}

	// 2. 查用户角色，遍历所有角色取最低角色价
	roleIDs, _ := s.iamSvc.GetUserRoleIDs(ctx, userID)
	var minPrice *decimal.Decimal
	for _, roleID := range roleIDs {
		price, err := s.priceRepo.FindRolePrice(ctx, planID, roleID)
		if err == nil {
			if minPrice == nil || price.PriceAmount.LessThan(*minPrice) {
				amt := price.PriceAmount
				minPrice = &amt
			}
		}
	}
	if minPrice != nil {
		return *minPrice, nil
	}

	// 3. 默认价（role_id IS NULL AND membership_level_id IS NULL）
	price, err := s.priceRepo.FindDefaultPrice(ctx, planID)
	if err == nil {
		return price.PriceAmount, nil
	}

	return decimal.Zero, ErrNoPriceConfigured
}
