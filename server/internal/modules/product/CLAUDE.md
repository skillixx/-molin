# Product 模块 — 后端 B 负责

## 职责边界

只负责：商品 CRUD、套餐配置、价格计算、角色访问规则、商品购买入口（调用 billing + provision）。

不负责：钱包扣费（billing 模块）、资产生成（provision/asset 模块）。

---

## Week 2 任务清单

```text
□ model/product.go        — products, product_plans, product_prices, product_role_access, product_billing_rules
□ repository/product_repo.go
□ repository/plan_repo.go
□ repository/price_repo.go
□ repository/access_repo.go
□ service/product_service.go  — 商品 CRUD，可见性过滤
□ service/pricing_service.go  — 价格优先级计算
□ service/purchase_service.go — 购买入口
□ handler/product_handler.go  — 商品列表/详情/购买
□ handler/admin_product_handler.go — 管理员 CRUD
□ dto/product_dto.go
□ route.go

Migration：
□ server/migrations/000004_create_product_tables.up.sql
```

---

## 价格优先级计算（核心逻辑）

```go
// service/pricing_service.go
// GetPrice 按优先级返回用户的实际购买价格：
// 会员价 > 角色价 > 默认价（最低优先级）
func (s *PricingService) GetPrice(ctx context.Context, planID, userID uint64) (decimal.Decimal, error) {
    // 1. 查用户活跃会员等级
    membership, _ := s.membershipRepo.GetActive(ctx, userID)
    if membership != nil {
        if price, ok := s.findPrice(ctx, planID, 0, membership.LevelID); ok {
            return price, nil
        }
    }
    // 2. 查用户角色，遍历所有角色取最低价
    roleIDs, _ := s.iamSvc.GetUserRoleIDs(ctx, userID)
    var minPrice *decimal.Decimal
    for _, roleID := range roleIDs {
        if price, ok := s.findPrice(ctx, planID, roleID, 0); ok {
            if minPrice == nil || price.LessThan(*minPrice) {
                minPrice = &price
            }
        }
    }
    if minPrice != nil {
        return *minPrice, nil
    }
    // 3. 默认价格（role_id IS NULL AND membership_level_id IS NULL）
    if price, ok := s.findPrice(ctx, planID, 0, 0); ok {
        return price, nil
    }
    return decimal.Zero, ErrNoPriceConfigured
}
```

## 购买入口调用链

```go
// service/purchase_service.go
func (s *PurchaseService) Purchase(ctx context.Context, userID, productID, planID uint64, idempotencyKey string) (*dto.PurchaseResult, error) {
    // 1. 校验实名制
    user, _ := s.userRepo.FindByID(ctx, userID)
    if user.RealNameStatus != "verified" {
        return nil, ErrRealNameRequired // code=70001
    }
    // 2. 校验购买权限（product_role_access.can_buy）
    if !s.accessRepo.CanBuy(ctx, productID, s.iamSvc.GetUserRoleIDs(ctx, userID)) {
        return nil, ErrNoAccess // code=40003
    }
    // 3. 计算价格
    price, _ := s.pricingSvc.GetPrice(ctx, planID, userID)
    // 4. 幂等检查（Idempotency-Key 唯一索引）
    if existing := s.orderRepo.FindByIdempotencyKey(ctx, idempotencyKey); existing != nil {
        return &dto.PurchaseResult{OrderID: existing.ID, Idempotent: true}, nil
    }
    // 5. 创建订单
    order, _ := s.orderSvc.Create(ctx, userID, productID, planID, price, idempotencyKey)
    // 6. 扣费（调用 billing.wallet_service.Deduct）
    if err := s.billingSvc.Deduct(ctx, userID, price, order.ID, "购买商品"); err != nil {
        s.orderSvc.MarkFailed(ctx, order.ID)
        return nil, err
    }
    // 7. 更新订单为已支付
    s.orderSvc.MarkPaid(ctx, order.ID)
    // 8. 触发开通（调用 provision 模块）
    go s.provisionSvc.Provision(ctx, order.ID, productID, planID, userID)
    return &dto.PurchaseResult{OrderID: order.ID}, nil
}
```

---

## Migration

### server/migrations/000004_create_product_tables.up.sql

```sql
CREATE TABLE IF NOT EXISTS products (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_type VARCHAR(64) NOT NULL,
  product_code VARCHAR(128) NOT NULL,
  name VARCHAR(191) NOT NULL,
  description TEXT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  business_ref_id BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_products_code (product_code),
  KEY idx_products_type_status (product_type, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_plans (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  plan_code VARCHAR(128) NOT NULL,
  name VARCHAR(191) NOT NULL,
  billing_type VARCHAR(64) NOT NULL,
  duration_days INT NULL,
  quota_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_product_plans_code (product_id, plan_code),
  KEY idx_product_plans_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_prices (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_plan_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NULL,
  membership_level_id BIGINT UNSIGNED NULL,
  price_amount DECIMAL(18,6) NOT NULL,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_product_prices_plan_id (product_plan_id),
  KEY idx_product_prices_role_id (role_id),
  KEY idx_product_prices_membership_level_id (membership_level_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_role_access (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  can_view TINYINT(1) NOT NULL DEFAULT 0,
  can_buy TINYINT(1) NOT NULL DEFAULT 0,
  can_use TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_product_role_access (product_id, role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS product_billing_rules (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  product_id BIGINT UNSIGNED NOT NULL,
  product_plan_id BIGINT UNSIGNED NULL,
  usage_type VARCHAR(64) NOT NULL,
  usage_unit VARCHAR(32) NOT NULL,
  price_amount DECIMAL(18,6) NOT NULL,
  currency VARCHAR(16) NOT NULL DEFAULT 'CNY',
  billing_mode VARCHAR(64) NOT NULL,
  free_quota DECIMAL(18,6) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_billing_rules_product_id (product_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 接口清单

```text
GET    /api/products               -- 用户端商品市场（按角色过滤可见商品）
GET    /api/products/:id           -- 商品详情 + 套餐 + 价格预览
POST   /api/products/:id/purchase  -- 购买（需 Idempotency-Key 请求头）
GET    /api/admin/products
POST   /api/admin/products
PUT    /api/admin/products/:id
GET    /api/admin/products/:id/plans
POST   /api/admin/products/:id/plans
PUT    /api/admin/products/:id/plans/:plan_id/prices
PUT    /api/admin/products/:id/access
```

## 依赖关系

- 依赖 `modules/billing` — 扣费
- 依赖 `modules/order` — 创建订单
- 依赖 `modules/provision` — 商品开通
- 依赖 `modules/iam` — 获取用户角色 ID
- 依赖 `modules/membership` — 获取用户活跃会员等级
