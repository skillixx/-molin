package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"molin/server/internal/modules/product/dto"
	"molin/server/internal/modules/product/repository"
	orderrepo "molin/server/internal/modules/order/repository"
	ordersvc "molin/server/internal/modules/order/service"
)

// 错误码定义（对应业务规范）。
var (
	// ErrRealNameRequired 未实名认证（code=70001）。
	ErrRealNameRequired = errors.New("需要先完成实名认证")
	// ErrNoAccess 无购买权限（code=40003）。
	ErrNoAccess = errors.New("无购买权限")
)

// PurchaseService 负责商品购买入口（实名校验→权限校验→定价→幂等→建单→扣费→开通）。
type PurchaseService struct {
	db           *gorm.DB
	accessRepo   *repository.AccessRepository
	orderRepo    *orderrepo.OrderRepository
	orderSvc     *ordersvc.OrderService
	pricingSvc   *PricingService
	billingSvc   BillingService
	provisionSvc ProvisionService
	iamSvc       IAMService
	userRepo     UserRepository
}

// NewPurchaseService 创建购买服务实例。
func NewPurchaseService(
	db *gorm.DB,
	accessRepo *repository.AccessRepository,
	orderRepo *orderrepo.OrderRepository,
	orderSvc *ordersvc.OrderService,
	pricingSvc *PricingService,
	billingSvc BillingService,
	provisionSvc ProvisionService,
	iamSvc IAMService,
	userRepo UserRepository,
) *PurchaseService {
	return &PurchaseService{
		db:           db,
		accessRepo:   accessRepo,
		orderRepo:    orderRepo,
		orderSvc:     orderSvc,
		pricingSvc:   pricingSvc,
		billingSvc:   billingSvc,
		provisionSvc: provisionSvc,
		iamSvc:       iamSvc,
		userRepo:     userRepo,
	}
}

// Purchase 执行商品购买流程：
//  1. 校验实名制（real_name_status = verified，code=70001）
//  2. 校验购买权限（product_role_access.can_buy，code=40003）
//  3. 计算用户实际价格（会员价 > 角色价 > 默认价）
//  4. 幂等检查（Idempotency-Key 唯一索引，重复请求返回已有订单）
//  5. 创建订单（pending 状态）
//  6. 钱包扣费（乐观锁，最多重试 3 次）
//  7. 更新订单为 paid
//  8. 触发商品开通（异步，不阻塞响应）
func (s *PurchaseService) Purchase(ctx context.Context, userID, productID, planID uint64, idempotencyKey string) (*dto.PurchaseResult, error) {
	// 1. 实名校验
	realNameStatus, err := s.userRepo.GetRealNameStatus(ctx, userID)
	if err != nil {
		return nil, err
	}
	if realNameStatus != "verified" {
		return nil, ErrRealNameRequired
	}

	// 2. 购买权限校验（product_role_access.can_buy）
	roleIDs, _ := s.iamSvc.GetUserRoleIDs(ctx, userID)
	if !s.accessRepo.CanBuy(ctx, productID, roleIDs) {
		return nil, ErrNoAccess
	}

	// 3. 计算价格
	price, err := s.pricingSvc.GetPrice(ctx, planID, userID)
	if err != nil {
		return nil, err
	}

	// 4. 幂等检查：先查是否已有同幂等键订单
	existing, queryErr := s.orderRepo.FindByIdempotencyKey(ctx, idempotencyKey)
	if queryErr == nil && existing != nil {
		return &dto.PurchaseResult{
			OrderID:    existing.ID,
			OrderNo:    existing.OrderNo,
			Status:     existing.Status,
			Amount:     existing.Amount,
			Idempotent: true,
		}, nil
	}

	// 5. 创建订单（pending 状态）。
	// 若 idempotency_key 并发竞争导致唯一索引冲突，Create 内部会重查返回已有订单。
	order, err := s.orderSvc.Create(ctx, userID, productID, planID, price, "product", idempotencyKey)
	if err != nil {
		return nil, err
	}

	// 若创建后发现是已有订单（并发幂等），直接返回
	if order.Status != "pending" {
		return &dto.PurchaseResult{
			OrderID:    order.ID,
			OrderNo:    order.OrderNo,
			Status:     order.Status,
			Amount:     order.Amount,
			Idempotent: true,
		}, nil
	}

	// 6. 钱包扣费（乐观锁，最多重试 3 次）
	var deductErr error
	for i := 0; i < 3; i++ {
		deductErr = s.billingSvc.Deduct(ctx, userID, price, order.ID, "购买商品")
		if deductErr == nil {
			break
		}
	}
	if deductErr != nil {
		// 扣费失败：将订单标记为 failed
		_ = s.orderSvc.MarkFailed(ctx, order.ID)
		return nil, deductErr
	}

	// 7. 更新订单为 paid
	if err := s.orderSvc.MarkPaid(ctx, order.ID); err != nil {
		return nil, err
	}

	// 8. 异步触发商品开通（不阻塞响应，失败不影响订单）
	go func() {
		bgCtx := context.Background()
		_ = s.provisionSvc.Provision(bgCtx, order.ID, productID, planID, userID)
	}()

	return &dto.PurchaseResult{
		OrderID:    order.ID,
		OrderNo:    order.OrderNo,
		Status:     "paid",
		Amount:     price,
		Idempotent: false,
	}, nil
}
