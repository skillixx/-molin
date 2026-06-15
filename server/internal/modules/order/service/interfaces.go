package service

import (
	"context"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// BillingService 定义订单支付所需的钱包扣费接口，由 billing.WalletService 实现。
// 支付编排（PayService）需要在「同一事务」内完成「钱包扣费」与「订单 pending→paid」，
// 因此使用支持外部事务的 DeductTx，并返回新建钱包流水（wallet_transaction）的 ID
// 供 O3 支付响应透传 wallet_transaction_id。
type BillingService interface {
	// DeductTx 在外部事务 tx 内扣费，返回钱包流水 ID。
	// 余额不足返回 ErrInsufficientBalance（对应 code=60001）；
	// 乐观锁冲突返回 ErrConcurrentUpdate，由调用方重试。
	DeductTx(tx *gorm.DB, userID uint64, amount decimal.Decimal, orderID uint64, remark string) (uint64, error)
}

// ProvisionService 定义商品开通接口，由 provision 模块实现；nil 时由 stub 降级为空操作。
// 支付成功后异步触发，幂等且可重放。
type ProvisionService interface {
	Provision(ctx context.Context, orderID, productID, planID, userID uint64) error
}
