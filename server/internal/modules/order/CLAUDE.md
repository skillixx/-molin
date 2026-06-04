# Order 模块 — 后端 B 负责

## 职责边界

只负责：商品购买订单和充值订单的创建、状态流转、查询。

不负责：钱包扣费（billing 模块）、资产生成（asset 模块）、具体业务开通（provision 模块）。

## 需要创建的文件

```text
model/
  order.go          -- orders, order_items

repository/
  order_repo.go     -- 订单 CRUD，含按 order_no 查询

service/
  order_service.go  -- Create / Pay / Cancel / Refund

handler/
  order_handler.go

dto/
  order_dto.go

route.go
```

## 关键类型

```go
type Order struct {
    ID           uint64
    OrderNo      string    // 唯一，格式：ORD + YYYYMMDD + 随机8位大写字母数字
    UserID       uint64
    OrderType    string    // purchase / recharge / refund
    Status       string    // pending / paid / cancelled / refunded / failed
    Amount       decimal.Decimal
    Currency     string
    PaidAt       *time.Time
    CancelledAt  *time.Time
    RefundAmount decimal.Decimal
    RefundedAt   *time.Time
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

## 订单状态机（必须严格遵守，不允许跳跃）

```text
pending  --[扣费成功]-->  paid
pending  --[超时/取消]--> cancelled
pending  --[系统错误]-->  failed
paid     --[退款]------> refunded
```

禁止的流转：
- paid → cancelled（必须先退款）
- refunded → 任何状态
- failed → 任何状态

## 订单号生成规则

```go
// server/pkg/idgen/order_no.go
// 格式：ORD + YYYYMMDD + 8位随机大写字母数字
// 例如：ORD202606040A3B7F2E
// 生成后在数据库创建时加唯一约束，冲突则重试
```

## 接口清单

```text
GET  /api/orders
GET  /api/orders/:id
POST /api/orders/:id/pay       -- 钱包支付（调用 billing.Deduct）
POST /api/orders/:id/cancel
GET  /api/admin/orders
GET  /api/admin/orders/:id
```

## 依赖关系

- 被 `modules/product/handler` 依赖 — 购买接口调用 OrderService.Create
- 依赖 `modules/billing/service` — 支付时调用 WalletService.Deduct
- 依赖 `server/pkg/idgen` — 生成订单号
