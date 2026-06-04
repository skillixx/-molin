# Product 模块 — 后端 B 负责

## 职责边界

只负责：商品 CRUD、套餐配置、价格计算、角色访问规则、计费规则配置、消费记录查询。

不负责：订单创建（order 模块）、钱包扣费（billing 模块）、资产生成（asset 模块）。

## 需要创建的文件

```text
model/
  product.go        -- products, product_plans, product_prices, product_role_access
  billing_rule.go   -- product_billing_rules, product_consumption_records
  membership_rule.go -- product_membership_rules
  adapter.go        -- product_provision_handlers, application_adapters

repository/
  product_repo.go
  plan_repo.go
  price_repo.go
  access_repo.go
  billing_rule_repo.go
  consumption_repo.go
  adapter_repo.go

service/
  product_service.go    -- 商品 CRUD，含状态校验
  pricing_service.go    -- 按角色/会员计算最终价格
  access_service.go     -- 校验 can_view / can_buy / can_use

handler/
  product_handler.go    -- 用户端 + 管理端商品接口

dto/
  product_dto.go

route.go
```

## 价格优先级（必须按此顺序匹配）

```go
func (s *PricingService) GetPrice(ctx context.Context, planID uint64, userID uint64) (decimal.Decimal, error) {
    membershipLevelID := s.getMembershipLevel(userID)  // 0 表示无会员
    roleIDs := s.getRoles(userID)

    // 1. 优先匹配用户会员级别的专属价
    if membershipLevelID > 0 {
        if price := s.priceRepo.FindByMembership(planID, membershipLevelID); price != nil {
            return price.PriceAmount, nil
        }
    }
    // 2. 匹配角色价（取最低价）
    if len(roleIDs) > 0 {
        if price := s.priceRepo.FindLowestByRoles(planID, roleIDs); price != nil {
            return price.PriceAmount, nil
        }
    }
    // 3. 默认价
    if price := s.priceRepo.FindDefault(planID); price != nil {
        return price.PriceAmount, nil
    }
    return decimal.Zero, ErrNoPriceFound
}
```

## 关键类型

```go
type Product struct {
    ID            uint64
    ProductType   string   // app / gpu / agent / skill / token / netdisk / membership
    ProductCode   string
    Name          string
    Description   string
    Status        string   // draft / active / inactive / archived
    BusinessRefID *uint64  // 指向 applications.id 或其他业务表
    CreatedAt     time.Time
    UpdatedAt     time.Time
}

type ProductPlan struct {
    ID           uint64
    ProductID    uint64
    PlanCode     string
    Name         string
    BillingType  string   // one_time / subscription / pay_as_you_go
    DurationDays int      // 订阅型有效天数，按量为 0
    QuotaJSON    string   // JSON，不同业务的额度参数
    Status       string
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

## 接口清单

```text
-- 用户端 --
GET  /api/products
GET  /api/products/:id
GET  /api/products/:id/plans
POST /api/products/:id/purchase
GET  /api/my/products

-- 管理端 --
GET    /api/admin/products
POST   /api/admin/products
GET    /api/admin/products/:id
PATCH  /api/admin/products/:id
POST   /api/admin/products/:id/plans
PATCH  /api/admin/products/:id/plans/:plan_id
PATCH  /api/admin/products/:id/access
PATCH  /api/admin/products/:id/prices
GET    /api/admin/product-handlers
GET    /api/admin/application-adapters
POST   /api/admin/application-adapters
PATCH  /api/admin/application-adapters/:id
GET    /api/admin/product-billing-rules
POST   /api/admin/product-billing-rules
PATCH  /api/admin/product-billing-rules/:id
GET    /api/admin/product-consumption-records
GET    /api/product-consumption-records
```

## 购买接口流程（handler 调用链）

```text
POST /api/products/:id/purchase
  -> 校验实名认证（调用 identity service）
  -> access_service.CanBuy(userID, productID)
  -> pricing_service.GetPrice(planID, userID)
  -> order_service.Create(userID, productID, planID, price)  [调用 order 模块]
  -> billing_service.Deduct(userID, price, orderID)           [调用 billing 模块]
  -> provision_service.Provision(order, product, plan)        [调用 provision 模块]
  -> 返回 order_id、asset_id
```

## 依赖关系

- 依赖 `modules/iam/service` — 获取用户角色
- 依赖 `modules/membership/service` — 获取用户会员级别
- 被 `modules/provision/service` 依赖 — 查询商品和套餐
