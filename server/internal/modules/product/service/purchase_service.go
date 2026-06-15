package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	billingsvc "molin/server/internal/modules/billing/service"
	orderrepo "molin/server/internal/modules/order/repository"
	ordersvc "molin/server/internal/modules/order/service"
	"molin/server/internal/modules/product/dto"
	"molin/server/internal/modules/product/repository"
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
//  5. 创建订单（pending 状态，remark 写入订单备注）
//  6. 钱包扣费（乐观锁，最多重试 3 次）
//  7. 更新订单为 paid
//  8. 触发商品开通（异步，不阻塞响应）
//
// quantity 暂时不影响计价逻辑（TODO: Week 3 按数量计算总价）。
// remark 写入订单备注字段。
func (s *PurchaseService) Purchase(ctx context.Context, userID, productID, planID uint64, idempotencyKey string, quantity int, remark string) (*dto.PurchaseResult, error) {
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

	// 2.1 会员专属购买门槛校验（修复 P1 缺陷：非会员可绕过会员专属门槛以默认价下单）。
	// 若该套餐配置了会员专属价（product_prices.membership_level_id），
	// 则要求用户持有命中等级之一的有效会员资格，否则拒绝购买（与 CanBuy 同属访问控制环节）。
	memberOK, err := s.pricingSvc.CheckMembershipGate(ctx, planID, userID)
	if err != nil {
		return nil, err
	}
	if !memberOK {
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

	// 5. 创建订单（pending 状态），写入 remark 备注。
	// 若 idempotency_key 并发竞争导致唯一索引冲突，Create 内部会重查返回已有订单。
	// TODO: Week 3 按 quantity 计算总价（当前 quantity 暂不参与计价）
	order, err := s.orderSvc.Create(ctx, userID, productID, planID, price, "product", idempotencyKey, remark)
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

	// 6. 钱包扣费。
	// WalletService.Deduct 内部已实现「FOR UPDATE + 乐观锁 + 指数退避抖动重试」，
	// 这里不再叠加外层重试循环（旧实现的外层 3 次重试会放大 failed 脏单且无意义）。
	deductErr := s.billingSvc.Deduct(ctx, userID, price, order.ID, "购买商品")
	if deductErr != nil {
		// 区分失败类型，决定订单去向（F6 并发健壮性）：
		if errors.Is(deductErr, billingsvc.ErrConcurrentUpdate) {
			// 6a. 纯瞬时锁冲突（重试耗尽）：本质是并发瞬时冲突，并非真实业务失败，
			//     订单从未发生任何资金变动。删除该 pending 订单，避免遗留 failed 垃圾订单。
			//     幂等保持正确：FindByIdempotencyKey + idempotency_key 唯一键时序未变，
			//     客户端凭同一 Idempotency-Key 重试将重新建单并扣费；删除带 status='pending'
			//     守卫，绝不会误删已支付订单。
			if deleted, delErr := s.orderSvc.DeletePendingTransient(ctx, order.ID); delErr != nil || !deleted {
				// 删除失败或订单已非 pending（极端情况）：退化为 MarkFailed 兜底，保证一致性。
				_ = s.orderSvc.MarkFailed(ctx, order.ID)
			}
			return nil, deductErr
		}
		// 6b. 真实业务失败（如余额不足 60001）：订单置 failed 合理，保留供用户/对账查看。
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
