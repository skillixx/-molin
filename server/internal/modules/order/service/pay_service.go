package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/order/repository"
)

// 支付/取消相关业务错误（order 模块对外语义，handler 据此映射错误码）。
var (
	// ErrOrderNotPending 订单非 pending 状态，无法支付/取消（状态机越界）。
	ErrOrderNotPending = errors.New("订单状态不可操作")
	// ErrInsufficientBalance 余额不足（code=60001）。
	// 由注入的 BillingService 实现（bootstrap 适配器）在扣费余额不足时返回本哨兵错误，
	// 使 order 模块无需反向依赖 billing 包即可识别该业务语义。
	ErrInsufficientBalance = errors.New("余额不足")
	// ErrConcurrentUpdate 钱包乐观锁冲突，需重试。
	// 同样由注入的 BillingService 实现在乐观锁冲突时返回本哨兵错误。
	ErrConcurrentUpdate = errors.New("并发更新冲突，请重试")
)

// PayResult O3 支付结果。
type PayResult struct {
	OrderID             uint64 `json:"order_id"`
	Status              string `json:"status"`
	WalletTransactionID uint64 `json:"wallet_transaction_id"`
	AssetID             uint64 `json:"asset_id"`
	// Idempotent 标识本次为重复支付已 paid 订单的幂等返回（不再扣费）。
	Idempotent bool `json:"-"`
}

// PayService 负责存量 pending 订单的钱包支付编排（O3）与取消（O4）。
// 设计要点：
//   - 「钱包扣费」与「订单 pending→paid」在同一事务内完成，避免扣费成功但订单未流转的中间态；
//   - pending→paid 用 WHERE id=? AND status='pending' 的 RowsAffected 守卫并发/越界，==0 回滚；
//   - 余额不足回滚事务并返回 60001，不产生流水；
//   - 乐观锁冲突由外层最多重试 3 次（与 WalletService.Deduct 一致）。
type PayService struct {
	db           *gorm.DB
	repo         *repository.OrderRepository
	billingSvc   BillingService
	provisionSvc ProvisionService
}

// NewPayService 创建支付编排服务。provisionSvc 为 nil 时降级为空操作 stub。
func NewPayService(db *gorm.DB, repo *repository.OrderRepository, billingSvc BillingService, provisionSvc ProvisionService) *PayService {
	if provisionSvc == nil {
		provisionSvc = NewProvisionStub()
	}
	return &PayService{
		db:           db,
		repo:         repo,
		billingSvc:   billingSvc,
		provisionSvc: provisionSvc,
	}
}

// Pay 以钱包余额支付指定 pending 订单（仅本人）。
//
// 流程：
//  1. 加载订单并校验归属（非本人 → ErrOrderNotFound，对外 404）。
//  2. 状态校验：已 paid 视为幂等成功直接返回；cancelled/failed/refunded 返回 ErrOrderNotPending。
//  3. 事务内：DeductTx 扣费（金额=order.Amount，余额不足 60001）→ pending→paid（RowsAffected 守卫）。
//  4. 扣费+流转成功后异步触发 provision 开通（幂等，可重放）。
//
// 返回 wallet_transaction_id（真实流水 ID）；asset_id 由 provision 异步开通，
// 同步阶段无法获知，统一返回 0（前端可后续查「我的资产」获取）。
func (s *PayService) Pay(ctx context.Context, orderID, userID uint64) (*PayResult, error) {
	// 1. 加载订单并校验归属
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if order.UserID != userID {
		// 非本人订单：按"不存在"处理，避免暴露他人订单存在性
		return nil, ErrOrderNotFound
	}

	// 2. 状态校验
	switch order.Status {
	case "pending":
		// 继续支付流程
	case "paid":
		// 幂等：已支付订单重复支付直接返回成功，不再扣费
		return &PayResult{
			OrderID:    order.ID,
			Status:     "paid",
			Idempotent: true,
		}, nil
	default:
		// cancelled / failed / refunded 等：状态机越界，拒绝
		return nil, ErrOrderNotPending
	}

	// 3. 事务内扣费 + 状态流转，最多重试 3 次（应对钱包乐观锁冲突）
	var walletTxID uint64
	var payErr error
	for i := 0; i < 3; i++ {
		walletTxID, payErr = s.payOnce(order.ID, userID, order.Amount, order.OrderNo)
		if payErr == nil || !errors.Is(payErr, ErrConcurrentUpdate) {
			break
		}
		// 乐观锁冲突，重试
	}
	if payErr != nil {
		return nil, payErr
	}

	// 4. 异步触发开通（不阻塞响应，失败不影响已成功的支付，靠 provision 幂等重放补偿）
	var productID, planID uint64
	if order.ProductID != nil {
		productID = *order.ProductID
	}
	if order.ProductPlanID != nil {
		planID = *order.ProductPlanID
	}
	go func() {
		bgCtx := context.Background()
		_ = s.provisionSvc.Provision(bgCtx, order.ID, productID, planID, userID)
	}()

	return &PayResult{
		OrderID:             order.ID,
		Status:              "paid",
		WalletTransactionID: walletTxID,
		AssetID:             0, // provision 异步开通，同步阶段不可知
	}, nil
}

// payOnce 单次支付尝试：单事务内扣费 + pending→paid。
func (s *PayService) payOnce(orderID, userID uint64, amount decimal.Decimal, remark string) (uint64, error) {
	var walletTxID uint64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 3.1 钱包扣费（外部事务内，乐观锁；返回流水 ID）
		// 余额不足/乐观锁冲突由 BillingService 实现归一化为 order 模块哨兵错误。
		txID, derr := s.billingSvc.DeductTx(tx, userID, amount, orderID, "支付订单："+remark)
		if derr != nil {
			return derr
		}
		// 3.2 pending → paid（RowsAffected 守卫，==0 回滚）
		now := time.Now()
		rows, uerr := s.repo.UpdateStatusTx(tx, orderID, "pending", "paid", map[string]interface{}{
			"paid_at": now,
		})
		if uerr != nil {
			return uerr
		}
		if rows == 0 {
			// 并发下订单已被流转（非 pending）：回滚扣费，按状态错误返回
			return ErrOrderNotPending
		}
		walletTxID = txID
		return nil
	})
	if err != nil {
		return 0, err
	}
	return walletTxID, nil
}

// Cancel 取消指定 pending 订单（仅本人）。pending→cancelled，RowsAffected 守卫。
// reason 落地到订单 Remark 字段（订单模型无独立 cancel_reason 列，复用现有字段，避免新增 migration）。
func (s *PayService) Cancel(ctx context.Context, orderID, userID uint64, reason string) error {
	// 校验归属
	order, err := s.repo.FindByID(ctx, orderID)
	if err != nil {
		return ErrOrderNotFound
	}
	if order.UserID != userID {
		return ErrOrderNotFound
	}
	if order.Status != "pending" {
		return ErrOrderNotPending
	}

	now := time.Now()
	updates := map[string]interface{}{
		"cancelled_at": now,
	}
	// reason 复用 Remark 字段落地（无独立 cancel_reason 列）。
	if reason != "" {
		updates["remark"] = reason
	}
	rows, err := s.repo.UpdateStatusTx(s.db, orderID, "pending", "cancelled", updates)
	if err != nil {
		return err
	}
	if rows == 0 {
		// 并发下订单已非 pending：无法取消
		return ErrOrderNotPending
	}
	return nil
}
