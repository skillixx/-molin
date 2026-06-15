# Finance Consumer 模块 — 后端 B 负责

## 职责边界

只负责：接收业务模块上报的消费事件、匹配计费规则、调用 billing 扣费、记录消费流水。

不负责：钱包扣费实现（billing 模块）、资产额度更新（asset 模块）。

## 需要创建的文件

```text
model/
  event.go          -- ProductUsageEvent（消费事件结构体）

service/
  consumer_service.go   -- 幂等校验 → 匹配计费规则 → 扣费 → 写消费记录

handler/
  consumer_handler.go   -- POST /api/internal/product-usage-events（内部接口，不对外）

route.go
```

## 关键类型

```go
type ProductUsageEvent struct {
    EventID        string    // UUID，用于幂等
    UserID         uint64
    ProductType    string    // app / gpu / agent / skill / token
    ProductCode    string
    ProductPlanID  uint64
    InstanceID     uint64
    UsageType      string    // 例如 input_tokens / output_tokens / storage_gb / hours
    UsageAmount    decimal.Decimal
    UsageUnit      string
    OccurredAt     time.Time
    IdempotencyKey string    // 必须全局唯一，推荐 event_id + usage_type
}
```

## 处理流程（必须严格按此执行）

```go
func (s *ConsumerService) Handle(ctx context.Context, event ProductUsageEvent) (*ConsumptionResult, error) {
    // 1. 幂等检查（按 idempotency_key 查 product_consumption_records）
    if existing := s.consumptionRepo.FindByIdempotencyKey(event.IdempotencyKey); existing != nil {
        return existing.ToResult(), nil  // 直接返回原结果
    }

    // 2. 匹配计费规则
    rule := s.ruleRepo.FindRule(event.ProductID, event.ProductPlanID, event.UsageType)
    if rule == nil { return nil, ErrNoBillingRule }

    // 3. 计算金额
    amount := rule.PriceAmount.Mul(event.UsageAmount)
    if amount.LessThanOrEqual(decimal.Zero) { return nil, ErrInvalidAmount }

    // 4. 事务：扣费 + 写消费记录
    var result *ConsumptionResult
    err := s.db.Transaction(func(tx *gorm.DB) error {
        // 调用 billing.WalletService.Deduct（注意：这里传 tx）
        if err := s.walletService.DeductTx(tx, event.UserID, amount, 0, "消费扣费"); err != nil {
            return err
        }
        record := &ProductConsumptionRecord{
            EventID:         event.EventID,
            UserID:          event.UserID,
            ProductID:       event.ProductID,
            ProductPlanID:   event.ProductPlanID,
            InstanceID:      event.InstanceID,
            UsageType:       event.UsageType,
            UsageAmount:     event.UsageAmount,
            UsageUnit:       event.UsageUnit,
            Amount:          amount,
            IdempotencyKey:  event.IdempotencyKey,
        }
        if err := s.consumptionRepo.Create(tx, record); err != nil {
            return err
        }
        result = record.ToResult()
        return nil
    })
    return result, err
}
```

## 接口清单

> 权威设计见 `docs/backend-dev-plan-backend-b.md` §3.4。列表统一 **D-95 扁平分页** `{items,page,page_size,total}`。

```text
POST  /api/internal/product-usage-events       -- 内部上报（IP 白名单 + Idempotency-Key）
                                                  返回 {consumption_record_id,wallet_transaction_id,amount,idempotency_key}
GET   /api/product-consumption-records         -- 用户查本人消费记录 [扁平分页]（待新增）
                                                  query: product_id,usage_type,created_from,created_to
GET   /api/admin/product-consumption-records   -- [wallet:view] 全量消费记录 [扁平分页]（待新增）query 同上 + user_id
```

> 待办（详见设计文档 §7）：消费记录查询 F2/F3 尚未实现（C-7）；上报响应字段由 `{record_id,amount,idempotency_key}` 改为含 `consumption_record_id`/`wallet_transaction_id`（C-5）。

## 依赖关系

- 依赖 `modules/billing/service` — 调用 WalletService.Deduct / DeductTx
- 依赖 `modules/product/repository` — 查询 product_billing_rules
- 被业务模块（token_gateway、resource 等）调用
