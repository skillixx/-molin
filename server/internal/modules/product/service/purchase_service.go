package service

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/shopspring/decimal"
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
//  4. 按 quantity 计算总价（total = price × quantity）
//  5. 幂等检查（Idempotency-Key 唯一索引，重复请求返回已有订单）
//  6. 创建订单（pending 状态，remark 写入订单备注，total 作为订单金额）
//  7. 钱包扣费（乐观锁，最多重试 3 次）
//  8. 更新订单为 paid
//  9. 同步触发商品开通（失败仅记 warn，不回滚已 paid 订单）
//
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

	// 3. 计算单价
	price, err := s.pricingSvc.GetPrice(ctx, planID, userID)
	if err != nil {
		return nil, err
	}

	// 3.1 按 quantity 计算总价（D-003：quantity 参与计价）
	// quantity 兜底：purchase handler 已校验 ≥1，此处再次兜底防御性保护
	if quantity <= 0 {
		quantity = 1
	}
	total := price.Mul(decimal.NewFromInt(int64(quantity)))

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

	// 5. 创建订单（pending 状态），传入 total 作为订单金额，quantity 用于写入 order_items。
	// 若 idempotency_key 并发竞争导致唯一索引冲突，Create 内部会重查返回已有订单。
	order, err := s.orderSvc.Create(ctx, userID, productID, planID, total, "product", idempotencyKey, remark, quantity)
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

	// 6+7. 单事务：扣费 + pending→paid（最多重试 3 次应对乐观锁冲突）。
	// BUG-A 修复：原先扣费与 MarkPaid 分属两个独立事务，进程在两步之间崩溃会导致
	// 钱包已扣但订单永远 pending（用户丢钱）。现合并为 purchasePayTx 确保原子性。
	var txErr error
	for i := 0; i < 3; i++ {
		txErr = s.purchasePayTx(ctx, order.ID, userID, total, order.OrderNo)
		if txErr == nil || !errors.Is(txErr, billingsvc.ErrConcurrentUpdate) {
			break
		}
	}
	if txErr != nil {
		if errors.Is(txErr, billingsvc.ErrConcurrentUpdate) {
			// 瞬时锁冲突重试耗尽：删除 pending 订单，避免遗留垃圾单。
			// 客户端凭同一 Idempotency-Key 重试将重新建单并扣费。
			if deleted, delErr := s.orderSvc.DeletePendingTransient(ctx, order.ID); delErr != nil || !deleted {
				_ = s.orderSvc.MarkFailed(ctx, order.ID)
			}
			return nil, txErr
		}
		// 真实业务失败（余额不足等）：订单置 failed，保留供用户/对账查看。
		_ = s.orderSvc.MarkFailed(ctx, order.ID)
		return nil, txErr
	}

	// 8. 同步触发商品开通（D-004：改异步 goroutine 为同步调用）。
	// provision 失败不回滚已 paid 订单（钱已扣、订单已付），仅记 warn 供运维补偿。
	// 当前 ProvisionService.Provision 接口只返回 error（不返回 asset_id），
	// asset_id 留 nil，前端通过 /api/my/products 轮询资产状态。
	if provErr := s.provisionSvc.Provision(ctx, order.ID, productID, planID, userID); provErr != nil {
		log.Printf("[WARN] provision 失败，orderID=%d: %v", order.ID, provErr)
	}

	return &dto.PurchaseResult{
		OrderID:    order.ID,
		OrderNo:    order.OrderNo,
		Status:     "paid",
		Amount:     total,   // D-003：返回总价（price × quantity）
		AssetID:    nil,     // D-004：provision 接口当前只返回 error，asset_id 由前端通过 /api/my/products 查询
		Idempotent: false,
	}, nil
}

// purchasePayTx 单次扣费+状态流转尝试（同一事务）。
// BUG-A 修复：将扣费（DeductTx）与 pending→paid 状态变更包进同一数据库事务，
// 确保进程崩溃时两者同时回滚，消除「钱扣了但订单永远 pending」的数据不一致风险。
func (s *PurchaseService) purchasePayTx(ctx context.Context, orderID, userID uint64, amount decimal.Decimal, remark string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 在外部事务内扣费（FOR UPDATE + 乐观锁，不重试，由外层循环负责重试）
		if _, err := s.billingSvc.DeductTx(tx, userID, amount, orderID, "购买商品："+remark); err != nil {
			return err
		}
		// 2. pending→paid（RowsAffected 守卫，==0 说明状态已变，回滚事务）
		now := time.Now()
		rows, err := s.orderRepo.UpdateStatusTx(tx, orderID, "pending", "paid", map[string]interface{}{
			"paid_at": now,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ordersvc.ErrInvalidStatusTransition
		}
		return nil
	})
}
