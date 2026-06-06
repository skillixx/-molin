package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/billing/repository"
	orderrepo "molin/server/internal/modules/order/repository"
	ordersvc "molin/server/internal/modules/order/service"
)

// ErrInvalidSignature 签名校验失败。
var ErrInvalidSignature = errors.New("支付签名校验失败")

// ErrUnsupportedProvider 不支持的支付渠道。
var ErrUnsupportedProvider = errors.New("不支持的支付渠道")

// notifyBody 解析支付回调报文的通用字段（不同渠道有差异，此处简化）。
type notifyBody struct {
	OutTradeNo      string `json:"out_trade_no"`       // 商户订单号（即 order_no）
	TransactionID   string `json:"transaction_id"`     // 微信流水号（wechat）
	TradeNo         string `json:"trade_no"`           // 支付宝流水号（alipay）
	TotalFee        int64  `json:"total_fee"`          // 微信：分
	TotalAmount     string `json:"total_amount"`       // 支付宝：元
}

// PaymentService 负责支付回调处理（签名校验 + 幂等入账）。
type PaymentService struct {
	db              *gorm.DB
	paymentRepo     *repository.PaymentRepository
	walletSvc       *WalletService
	orderRepo       *orderrepo.OrderRepository
	orderSvc        *ordersvc.OrderService
	wechatVerifier  *WechatVerifier
	alipayVerifier  *AlipayVerifier
}

// NewPaymentService 创建支付服务实例。
func NewPaymentService(
	db *gorm.DB,
	paymentRepo *repository.PaymentRepository,
	walletSvc *WalletService,
	orderRepo *orderrepo.OrderRepository,
	orderSvc *ordersvc.OrderService,
	wechatVerifier *WechatVerifier,
	alipayVerifier *AlipayVerifier,
) *PaymentService {
	return &PaymentService{
		db:             db,
		paymentRepo:    paymentRepo,
		walletSvc:      walletSvc,
		orderRepo:      orderRepo,
		orderSvc:       orderSvc,
		wechatVerifier: wechatVerifier,
		alipayVerifier: alipayVerifier,
	}
}

// HandleNotify 处理支付平台回调（签名校验 + 幂等处理）。
// 安全规范：
//  1. 必须先校验签名，校验失败立即拒绝
//  2. 同一 provider_trade_no 幂等处理，不重复入账
//  3. notify_body 存储原始回调报文
func (s *PaymentService) HandleNotify(ctx context.Context, provider string, rawBody []byte) error {
	// 1. 签名校验（必须在任何业务逻辑之前）
	if err := s.verify(provider, rawBody); err != nil {
		return err
	}

	// 2. 解析回调报文
	orderNo, providerTradeNo, amount, err := s.parse(provider, rawBody)
	if err != nil {
		return fmt.Errorf("解析回调报文失败: %w", err)
	}

	// 3. 查询对应订单
	order, err := s.orderRepo.FindByOrderNo(ctx, orderNo)
	if err != nil {
		return fmt.Errorf("订单不存在: %s", orderNo)
	}

	// 4. 幂等写入回调记录（received 状态）
	callback := &model.PaymentCallback{
		OrderID:         order.ID,
		Provider:        provider,
		ProviderTradeNo: providerTradeNo,
		NotifyBody:      string(rawBody),
		Status:          "received",
	}
	_ = s.paymentRepo.Upsert(ctx, callback)

	// 5. 幂等检查：若已处理则直接返回
	if s.paymentRepo.IsProcessed(ctx, provider, providerTradeNo) {
		return nil
	}

	// 6. 事务：更新订单状态 → 充值入账 → 写流水 → 标记已处理
	return s.db.Transaction(func(tx *gorm.DB) error {
		// 更新订单为 paid（状态机：pending → paid）
		paidOrder, err := s.orderSvc.MarkPaidByOrderNo(tx, orderNo)
		if err != nil {
			return err
		}
		// 若订单已经不是 pending（之前已处理），幂等返回
		if paidOrder.Status == "paid" && paidOrder.PaidAt != nil {
			// 已是 paid 状态，可能是并发重复回调，检查是否需要入账
		}

		// 充值入账（Recharge 内部也使用乐观锁，此处直接调用 walletSvc 内部方法）
		// 注意：这里不能调用外层的 walletSvc.Recharge（会开新事务），
		// 而是直接调用事务版 recharge 逻辑
		if err := s.rechargeTx(tx, order.UserID, amount, order.ID, "支付充值"); err != nil {
			return err
		}

		// 标记回调已处理
		return s.paymentRepo.MarkProcessed(tx, provider, providerTradeNo)
	})
}

// rechargeTx 在事务内执行充值（供 HandleNotify 调用）。
func (s *PaymentService) rechargeTx(tx *gorm.DB, userID uint64, amount decimal.Decimal, orderID uint64, remark string) error {
	walletRepo := s.walletSvc.walletRepo
	txRepo := s.walletSvc.txRepo

	wallet, err := walletRepo.GetForUpdate(tx, userID)
	if err != nil {
		// 用户无钱包，自动创建
		newWallet := &model.Wallet{UserID: userID, Currency: "CNY"}
		if createErr := tx.Create(newWallet).Error; createErr != nil {
			return createErr
		}
		wallet = newWallet
	}

	rows, err := walletRepo.UpdateWithOptimisticLock(tx, int64(wallet.ID), wallet.Version, map[string]interface{}{
		"balance_amount": gorm.Expr("balance_amount + ?", amount),
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrConcurrentUpdate
	}

	balanceAfter := wallet.BalanceAmount.Add(amount)
	return txRepo.Create(tx, &model.WalletTransaction{
		WalletID:       wallet.ID,
		UserID:         userID,
		Type:           "recharge",
		Direction:      "in",
		Amount:         amount,
		BalanceAfter:   balanceAfter,
		RelatedOrderID: &orderID,
		Remark:         remark,
	})
}

// verify 按渠道调用对应签名校验器。
func (s *PaymentService) verify(provider string, rawBody []byte) error {
	switch provider {
	case "wechat":
		return s.wechatVerifier.Verify(rawBody)
	case "alipay":
		return s.alipayVerifier.Verify(rawBody)
	default:
		return ErrUnsupportedProvider
	}
}

// parse 解析支付回调报文，提取 orderNo、providerTradeNo、amount。
func (s *PaymentService) parse(provider string, rawBody []byte) (orderNo, providerTradeNo string, amount decimal.Decimal, err error) {
	var body notifyBody
	if err = json.Unmarshal(rawBody, &body); err != nil {
		return
	}
	orderNo = body.OutTradeNo
	switch provider {
	case "wechat":
		providerTradeNo = body.TransactionID
		// 微信金额单位为分，转换为元
		amount = decimal.NewFromInt(body.TotalFee).Div(decimal.NewFromInt(100))
	case "alipay":
		providerTradeNo = body.TradeNo
		amount, err = decimal.NewFromString(body.TotalAmount)
	default:
		err = ErrUnsupportedProvider
	}
	return
}

// CreateRechargeOrder 创建充值订单（调用 order 模块创建 recharge 类型订单）。
// 返回模拟的支付 URL（Week 2 阶段不对接真实支付，仅创建订单）。
func (s *PaymentService) CreateRechargeOrder(ctx context.Context, userID uint64, amount decimal.Decimal, idempotencyKey string) (string, uint64, error) {
	order, err := s.orderSvc.Create(ctx, userID, 0, 0, amount, "recharge", idempotencyKey)
	if err != nil {
		return "", 0, err
	}
	// 模拟支付 URL（实际需对接微信/支付宝 API）
	payURL := fmt.Sprintf("/api/simulate-pay?order_no=%s&amount=%s", order.OrderNo, amount.String())
	return payURL, order.ID, nil
}
