# Order 模块 — 后端 B 负责

## 职责边界

只负责：商品购买订单和充值订单的创建、状态流转、查询。

不负责：钱包扣费（billing 模块）、资产生成（asset 模块）。

---

## Week 2 任务清单

```text
□ model/order.go              — orders, order_items
□ repository/order_repo.go    — 创建、按单号/幂等键查询、状态更新
□ service/order_service.go    — Create / MarkPaid / MarkCancelled / MarkFailed
□ handler/order_handler.go    — 用户查订单、管理员查订单
□ dto/order_dto.go
□ route.go

Migration：
□ 包含在 000005_create_billing_tables.up.sql（后端 B 统一写）
```

---

## 订单状态机（严格遵守，不允许其他状态跳转）

```text
pending  →  paid        （支付成功）
pending  →  cancelled   （用户取消或超时）
pending  →  failed      （扣费失败）
paid     →  refunded    （退款，第一阶段暂不实现）
```

## 订单号格式

```go
// pkg/idgen/order_no.go
// 格式：ORD + YYYYMMDD + 8 位随机大写字母数字
// 示例：ORD202406041A3B9C2F
func GenerateOrderNo() string {
    date := time.Now().Format("20060102")
    const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    b := make([]byte, 8)
    for i := range b {
        n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
        b[i] = chars[n.Int64()]
    }
    return "ORD" + date + string(b)
}
```

## 核心代码模板

```go
// service/order_service.go

func (s *OrderService) Create(ctx context.Context, userID, productID, planID uint64, amount decimal.Decimal, idempotencyKey string) (*model.Order, error) {
    order := &model.Order{
        OrderNo:        idgen.GenerateOrderNo(),
        UserID:         userID,
        OrderType:      "product",
        Status:         "pending",
        Amount:         amount,
        Currency:       "CNY",
        IdempotencyKey: idempotencyKey,
    }
    if err := s.repo.Create(ctx, order); err != nil {
        return nil, err
    }
    return order, nil
}

// MarkPaid 将订单标记为已支付，只允许从 pending 流转。
func (s *OrderService) MarkPaid(ctx context.Context, orderID uint64) error {
    result := s.db.Model(&model.Order{}).
        Where("id = ? AND status = ?", orderID, "pending").
        Updates(map[string]interface{}{
            "status":  "paid",
            "paid_at": time.Now(),
        })
    if result.RowsAffected == 0 {
        return ErrInvalidStatusTransition
    }
    return result.Error
}
```

---

## 接口清单

```text
GET  /api/orders           -- 用户查自己的订单列表
GET  /api/orders/:id       -- 用户查单个订单
GET  /api/admin/orders     -- 管理员查所有订单
GET  /api/admin/orders/:id -- 管理员查订单详情
```

## 注意：充值订单

充值订单（recharge_order）也放在 orders 表，order_type = 'recharge'，由 billing 模块的 RechargeService 调用 OrderService.Create 创建。支付回调处理完成后通过 OrderService.MarkPaid 更新状态。
