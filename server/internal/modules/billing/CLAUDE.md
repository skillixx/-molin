# Billing 模块 — 后端 B 负责

## 职责边界

只负责：钱包余额、钱包流水、充值订单、支付回调、乐观锁扣费。

不负责：商品购买订单（order 模块）、资产生成（asset 模块）、消费事件（finance_consumer 模块）。

## 需要创建的文件

```text
model/
  wallet.go         -- wallets, wallet_transactions 结构体
  payment.go        -- payment_callbacks 结构体

repository/
  wallet_repo.go        -- 钱包 CRUD，含 SELECT FOR UPDATE
  transaction_repo.go   -- 流水写入，只追加不修改
  payment_repo.go       -- 支付回调记录 CRUD

service/
  wallet_service.go     -- Deduct（乐观锁扣费）、Recharge（充值）、Freeze/Unfreeze
  payment_service.go    -- 支付回调处理（签名校验 + 幂等）
  wechat_verifier.go    -- 微信支付 APIv3 签名校验
  alipay_verifier.go    -- 支付宝 RSA2 签名校验

handler/
  billing_handler.go    -- 钱包查询、流水查询、充值创建
  payment_handler.go    -- POST /api/payments/notify/:provider

dto/
  billing_dto.go

route.go
```

## 关键类型

```go
type Wallet struct {
    ID            uint64
    UserID        uint64
    BalanceAmount decimal.Decimal   // 使用 shopspring/decimal
    FrozenAmount  decimal.Decimal
    Currency      string            // CNY
    Version       int64             // 乐观锁版本号
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

type WalletTransaction struct {
    ID             uint64
    WalletID       uint64
    UserID         uint64
    Type           string   // recharge / consume / refund / freeze / unfreeze
    Direction      string   // in / out
    Amount         decimal.Decimal
    BalanceAfter   decimal.Decimal  // 交易后余额快照
    RelatedOrderID *uint64
    Remark         string
    CreatedAt      time.Time
}

type PaymentCallback struct {
    ID              uint64
    OrderID         uint64
    Provider        string   // wechat / alipay
    ProviderTradeNo string   // 第三方流水号，唯一索引
    NotifyBody      string   // 原始回调报文（加密存储）
    Status          string   // received / processed / ignored
    ProcessedAt     *time.Time
    CreatedAt       time.Time
}
```

## 扣费事务模板（必须严格遵守）

```go
func (s *WalletService) Deduct(ctx context.Context, userID uint64, amount decimal.Decimal, orderID uint64, remark string) error {
    return s.db.Transaction(func(tx *gorm.DB) error {
        // 1. 加行锁查询钱包
        var wallet Wallet
        if err := tx.Set("gorm:query_option", "FOR UPDATE").
            Where("user_id = ?", userID).First(&wallet).Error; err != nil {
            return err
        }
        // 2. 校验余额
        if wallet.BalanceAmount.LessThan(amount) {
            return ErrInsufficientBalance
        }
        // 3. 乐观锁更新
        result := tx.Model(&Wallet{}).
            Where("id = ? AND version = ?", wallet.ID, wallet.Version).
            Updates(map[string]interface{}{
                "balance_amount": gorm.Expr("balance_amount - ?", amount),
                "version":        gorm.Expr("version + 1"),
            })
        if result.RowsAffected == 0 {
            return ErrConcurrentUpdate  // 调用方重试最多 3 次
        }
        // 4. 写流水
        balanceAfter := wallet.BalanceAmount.Sub(amount)
        return tx.Create(&WalletTransaction{
            WalletID:       wallet.ID,
            UserID:         userID,
            Type:           "consume",
            Direction:      "out",
            Amount:         amount,
            BalanceAfter:   balanceAfter,
            RelatedOrderID: &orderID,
            Remark:         remark,
        }).Error
    })
}
```

## 支付回调幂等模板

```go
func (s *PaymentService) HandleNotify(ctx context.Context, provider string, rawBody []byte) error {
    // 1. 校验签名（调用对应 verifier）
    if err := s.verify(provider, rawBody); err != nil {
        return ErrInvalidSignature
    }
    // 2. 解析 provider_trade_no 和 order_no
    tradeNo, orderNo := s.parse(provider, rawBody)
    // 3. 记录回调（可重复，status = received）
    s.paymentRepo.Upsert(provider, tradeNo, rawBody)
    // 4. 幂等检查
    if s.paymentRepo.IsProcessed(provider, tradeNo) {
        return nil
    }
    // 5. 事务：更新 order → 入账 → 写流水 → 标记 processed
    return s.db.Transaction(func(tx *gorm.DB) error {
        order := s.orderRepo.FindByOrderNo(orderNo)
        if order.Status != "pending" { return nil }
        s.orderRepo.UpdateStatus(tx, order.ID, "paid")
        s.walletRepo.Recharge(tx, order.UserID, order.Amount)
        s.paymentRepo.MarkProcessed(tx, provider, tradeNo)
        return nil
    })
}
```

## 接口清单

> 权威设计见 `docs/backend-dev-plan-backend-b.md` §3.3。列表统一 **D-95 扁平分页** `{items,page,page_size,total}`。

```text
GET   /api/wallet                              -- 钱包余额 {wallet_id,balance_amount,frozen_amount,currency}
GET   /api/wallet/transactions                 -- 本人流水 [扁平分页] query: type,direction,created_from,created_to
POST  /api/recharge/orders                     -- 创建充值订单 返回 {order_id,order_no,amount,status,pay_url}
POST  /api/payments/notify/:provider           -- 无需登录，需验签 + 幂等
GET   /api/admin/wallet-transactions           -- [wallet:view] 全量流水 [扁平分页]
GET   /api/admin/users/:id/wallet              -- [wallet:view] 指定用户钱包
PATCH /api/admin/users/:id/wallet/freeze       -- [wallet:manage] body {action:"freeze"/"unfreeze",amount,reason}
GET   /api/admin/payment-callbacks             -- [wallet:view] 回调记录 [扁平分页]（禁回传明文 notify_body）
```

> 待办（详见设计文档 §7）：充值响应补 order_no/amount/status（C-3）；冻结 body 由 `{amount,action,remark}` 改 `{action,amount,reason}`（C-4）；冻结权限码 wallet:view→wallet:manage（C-10，需 seed migration）；列表扁平化（C-1）。

## 依赖关系

- 依赖 `modules/order/repository` — 查询和更新充值订单状态
- 被 `modules/finance_consumer/service` 依赖 — 消费事件最终调用 Deduct
- 不依赖 `modules/asset`（资产生成由 provision 模块负责）
