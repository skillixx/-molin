# 开发者任务看板

> 本文档是各开发者 Week 1–4 的具体编码任务清单，按人分组。
> 每个任务对应具体文件路径，完成后在对应 CLAUDE.md 中打勾。

---

## 后端 A — 账号 + 权限 + 实名（Week 1 优先级最高）

**Agent 文件：**
- `server/internal/modules/auth/CLAUDE.md`
- `server/internal/modules/iam/CLAUDE.md`
- `server/internal/modules/identity/CLAUDE.md`
- `server/internal/bootstrap/CLAUDE.md`

### Week 1（必须全部完成，其他人依赖）

| # | 文件 | 说明 |
|---|---|---|
| 1 | `server/pkg/db/db.go` | GORM 连接池 |
| 2 | `server/pkg/cache/redis.go` | Redis 客户端 |
| 3 | `server/pkg/crypto/password.go` | bcrypt hash/verify |
| 4 | `server/pkg/crypto/hmac.go` | HMAC-SHA256 |
| 5 | `server/pkg/jwt/jwt.go` | JWT 生成/校验 |
| 6 | `server/internal/config/config.go` | 补充 DB/Redis/JWT 配置字段 |
| 7 | `server/migrations/000001_create_auth_tables.up.sql` | users/sessions/codes/logs 建表 |
| 8 | `server/migrations/000002_create_iam_tables.up.sql` | roles/permissions/user_roles 建表 |
| 9 | `server/migrations/000003_create_identity_tables.up.sql` | identity 建表 |
| 10 | `server/internal/modules/auth/model/user.go` | User 结构体 |
| 11 | `server/internal/modules/auth/model/session.go` | UserSession 结构体 |
| 12 | `server/internal/modules/auth/model/verification.go` | VerificationCode 结构体 |
| 13 | `server/internal/modules/auth/model/login_log.go` | UserLoginLog 结构体 |
| 14 | `server/internal/modules/auth/repository/user_repo.go` | 用户 CRUD |
| 15 | `server/internal/modules/auth/repository/session_repo.go` | 会话 CRUD |
| 16 | `server/internal/modules/auth/repository/verification_repo.go` | 验证码 CRUD |
| 17 | `server/internal/modules/auth/service/auth_service.go` | 注册/登录/退出/刷新 |
| 18 | `server/internal/modules/auth/service/verification_service.go` | 验证码发送/校验 |
| 19 | `server/internal/modules/auth/handler/auth_handler.go` | HTTP Handler |
| 20 | `server/internal/modules/auth/dto/auth_dto.go` | 请求/响应 DTO |
| 21 | `server/internal/modules/auth/route.go` | 注册路由 |
| 22 | `server/internal/middleware/auth.go` | JWT 中间件 RequireAuth |
| 23 | `server/internal/modules/iam/model/role.go` | 角色/权限模型 |
| 24 | `server/internal/modules/iam/repository/role_repo.go` | 角色 CRUD |
| 25 | `server/internal/modules/iam/repository/permission_repo.go` | 权限 CRUD |
| 26 | `server/internal/modules/iam/repository/user_role_repo.go` | 用户角色关联 |
| 27 | `server/internal/modules/iam/repository/override_repo.go` | 权限覆盖 CRUD |
| 28 | `server/internal/modules/iam/service/iam_service.go` | 权限计算（4 步优先级） |
| 29 | `server/internal/modules/iam/service/cache_service.go` | 权限 Redis 缓存 |
| 30 | `server/internal/modules/iam/handler/iam_handler.go` | Handler |
| 31 | `server/internal/modules/iam/route.go` | 注册路由 |
| 32 | `server/internal/middleware/permission.go` | RequirePerm 中间件 |
| 33 | `server/internal/modules/identity/model/identity.go` | 实名认证模型 |
| 34 | `server/internal/modules/identity/repository/identity_repo.go` | CRUD |
| 35 | `server/internal/modules/identity/service/identity_service.go` | 提交/审核（HMAC） |
| 36 | `server/internal/modules/identity/handler/identity_handler.go` | Handler |
| 37 | `server/internal/modules/identity/route.go` | 注册路由 |
| 38 | `server/internal/bootstrap/app.go` | 初始化 DB/Redis，接入 auth+iam+identity |

---

## 后端 B — 商品 + 订单 + 钱包 + 计费

**Agent 文件：**
- `server/internal/modules/product/CLAUDE.md`
- `server/internal/modules/order/CLAUDE.md`
- `server/internal/modules/billing/CLAUDE.md`
- `server/internal/modules/finance_consumer/CLAUDE.md`

### Week 2

| # | 文件 | 说明 |
|---|---|---|
| 1 | `server/migrations/000004_create_product_tables.up.sql` | products/plans/prices/access/billing_rules |
| 2 | `server/pkg/idgen/order_no.go` | 订单号生成（ORD+日期+8位随机） |
| 3 | `server/internal/modules/product/model/product.go` | Product/Plan/Price 模型 |
| 4 | `server/internal/modules/product/repository/product_repo.go` | 商品 CRUD |
| 5 | `server/internal/modules/product/repository/plan_repo.go` | 套餐 CRUD |
| 6 | `server/internal/modules/product/repository/price_repo.go` | 价格查询 |
| 7 | `server/internal/modules/product/repository/access_repo.go` | 角色访问规则 |
| 8 | `server/internal/modules/product/service/product_service.go` | 商品 CRUD 业务 |
| 9 | `server/internal/modules/product/service/pricing_service.go` | 价格优先级计算 |
| 10 | `server/internal/modules/product/handler/product_handler.go` | 用户端商品列表/详情 |
| 11 | `server/internal/modules/product/handler/admin_handler.go` | 管理员 CRUD |
| 12 | `server/internal/modules/product/route.go` | 注册路由 |
| 13 | `server/internal/modules/order/model/order.go` | Order/OrderItem 模型 |
| 14 | `server/internal/modules/order/repository/order_repo.go` | 按幂等键/订单号查询 |
| 15 | `server/internal/modules/order/service/order_service.go` | Create/MarkPaid/MarkFailed |
| 16 | `server/internal/modules/order/handler/order_handler.go` | 订单查询 Handler |
| 17 | `server/internal/modules/order/route.go` | 注册路由 |

### Week 3

| # | 文件 | 说明 |
|---|---|---|
| 1 | `server/migrations/000005_create_billing_tables.up.sql` | wallets/transactions/orders/items/payment_callbacks/consumption_records |
| 2 | `server/internal/modules/billing/model/wallet.go` | Wallet/WalletTransaction 模型 |
| 3 | `server/internal/modules/billing/model/payment.go` | PaymentCallback 模型 |
| 4 | `server/internal/modules/billing/repository/wallet_repo.go` | 钱包 CRUD + SELECT FOR UPDATE |
| 5 | `server/internal/modules/billing/repository/transaction_repo.go` | 流水追加写入 |
| 6 | `server/internal/modules/billing/repository/payment_repo.go` | 回调记录 CRUD |
| 7 | `server/internal/modules/billing/service/wallet_service.go` | **乐观锁扣费（核心，人工审查）** |
| 8 | `server/internal/modules/billing/service/payment_service.go` | **支付回调幂等（核心，人工审查）** |
| 9 | `server/internal/modules/billing/service/wechat_verifier.go` | 微信支付签名校验 |
| 10 | `server/internal/modules/billing/handler/billing_handler.go` | 钱包/流水查询 |
| 11 | `server/internal/modules/billing/handler/payment_handler.go` | POST /api/payments/notify/:provider |
| 12 | `server/internal/modules/billing/route.go` | 注册路由 |
| 13 | `server/internal/modules/product/service/purchase_service.go` | **购买入口（核心，人工审查）** |
| 14 | `server/internal/modules/finance_consumer/service/consumer_service.go` | 消费事件幂等处理 |

---

## 后端 C — 资产 + 会员 + 应用 + 内容 + 开通

**Agent 文件：**
- `server/internal/modules/asset/CLAUDE.md`
- `server/internal/modules/provision/CLAUDE.md`
- `server/internal/modules/membership/CLAUDE.md`
- `server/internal/modules/content/CLAUDE.md`

### Week 3

| # | 文件 | 说明 |
|---|---|---|
| 1 | `server/migrations/000006_create_asset_tables.up.sql` | user_assets/entitlements/events |
| 2 | `server/internal/modules/asset/model/asset.go` | UserAsset/UserEntitlement/AssetEvent 模型 |
| 3 | `server/internal/modules/asset/repository/asset_repo.go` | 资产 CRUD |
| 4 | `server/internal/modules/asset/repository/entitlement_repo.go` | 权益额度 CRUD + FOR UPDATE |
| 5 | `server/internal/modules/asset/service/asset_service.go` | CreateAsset/ExpireAsset/ConsumeEntitlement |
| 6 | `server/internal/modules/asset/handler/asset_handler.go` | 用户查资产/权益，管理员查资产 |
| 7 | `server/internal/modules/asset/route.go` | 注册路由 |
| 8 | `server/internal/modules/provision/service/provision_service.go` | ProvisionHandler 接口 + 路由 |
| 9 | `server/internal/modules/provision/handler/app_provisioner.go` | AppProvisioner 实现 |

### Week 4

| # | 文件 | 说明 |
|---|---|---|
| 1 | `server/migrations/000007_create_membership_tables.up.sql` | membership_levels/benefits/user_memberships/product_membership_rules |
| 2 | `server/migrations/000008_create_app_tables.up.sql` | applications, application_adapters |
| 3 | `server/migrations/000009_create_content_tables.up.sql` | announcements, help_categories, help_articles |
| 4 | `server/internal/modules/membership/model/membership.go` | 会员模型 |
| 5 | `server/internal/modules/membership/service/membership_service.go` | 查活跃会员 |
| 6 | `server/internal/modules/membership/handler/membership_handler.go` | 会员查询 |
| 7 | `server/internal/modules/membership/route.go` | 注册路由 |
| 8 | `server/internal/modules/app/model/app.go` | Application 模型 |
| 9 | `server/internal/modules/app/service/app_service.go` | 应用 CRUD |
| 10 | `server/internal/modules/app/handler/app_handler.go` | 应用管理 Handler |
| 11 | `server/internal/modules/app/route.go` | 注册路由 |
| 12 | `server/internal/modules/content/model/content.go` | 公告/帮助文档模型 |
| 13 | `server/internal/modules/content/service/content_service.go` | 公告/帮助文档 CRUD |
| 14 | `server/internal/modules/content/handler/content_handler.go` | 用户端 + 管理端 Handler |
| 15 | `server/internal/modules/content/route.go` | 注册路由 |
| 16 | `server/internal/jobs/expire_assets.go` | 资产到期定时任务 |

---

## 前端 A — 管理后台

**Agent 文件：`web/admin-console/CLAUDE.md`**

### Week 1

| # | 文件 | 说明 |
|---|---|---|
| 1 | `web/admin-console/src/api/http.ts` | Axios 实例 + 拦截器 |
| 2 | `web/admin-console/src/api/auth.ts` | 登录/退出/当前用户 |
| 3 | `web/admin-console/src/api/user.ts` | 用户管理 |
| 4 | `web/admin-console/src/api/role.ts` | 角色/权限 |
| 5 | `web/admin-console/src/stores/auth.ts` | 登录 Store |
| 6 | `web/admin-console/src/types/api.ts` | 通用类型 |
| 7 | `web/admin-console/src/types/user.ts` | User/Role/Permission 类型 |
| 8 | `web/admin-console/src/router/index.ts` | 路由 + 守卫 |
| 9 | `web/admin-console/src/views/auth/LoginView.vue` | 登录页 |
| 10 | `web/admin-console/src/components/layout/AdminLayout.vue` | 整体布局 |
| 11 | `web/admin-console/src/components/layout/SideMenu.vue` | 侧边导航 |
| 12 | `web/admin-console/src/components/layout/TopBar.vue` | 顶栏 |
| 13 | `web/admin-console/src/components/common/DataTable.vue` | 通用表格 |
| 14 | `web/admin-console/src/views/user/UserListView.vue` | 用户列表 |
| 15 | `web/admin-console/src/views/iam/RoleListView.vue` | 角色管理 |

### Week 2

| # | 文件 | 说明 |
|---|---|---|
| 1 | `web/admin-console/src/views/iam/PermissionListView.vue` | 权限管理 |
| 2 | `web/admin-console/src/views/identity/VerificationListView.vue` | 实名认证审核列表 |
| 3 | `web/admin-console/src/views/product/ProductListView.vue` | 商品列表 |
| 4 | `web/admin-console/src/views/product/ProductFormView.vue` | 商品新建/编辑 |
| 5 | `web/admin-console/src/views/product/PlanFormView.vue` | 套餐配置 |
| 6 | `web/admin-console/src/views/product/PriceFormView.vue` | 价格配置 |
| 7 | `web/admin-console/src/views/order/OrderListView.vue` | 订单管理 |
| 8 | `web/admin-console/src/views/wallet/TransactionListView.vue` | 钱包流水 |
| 9 | `web/admin-console/src/views/asset/AssetListView.vue` | 用户资产 |
| 10 | `web/admin-console/src/views/content/AnnouncementListView.vue` | 公告管理 |

---

## 前端 B — 用户控制台

**Agent 文件：`web/user-console/CLAUDE.md`**

### Week 1

| # | 文件 | 说明 |
|---|---|---|
| 1 | `web/user-console/src/api/http.ts` | Axios 实例 + Token 自动刷新拦截器 |
| 2 | `web/user-console/src/api/auth.ts` | 注册/登录/刷新/退出 |
| 3 | `web/user-console/src/api/identity.ts` | 提交/查询实名认证 |
| 4 | `web/user-console/src/api/product.ts` | 商品列表/详情/购买 |
| 5 | `web/user-console/src/stores/auth.ts` | 含实名状态 + refreshToken() |
| 6 | `web/user-console/src/router/index.ts` | requiresAuth + requiresRealName 守卫 |
| 7 | `web/user-console/src/views/auth/RegisterView.vue` | 注册页 |
| 8 | `web/user-console/src/views/auth/LoginView.vue` | 登录页 |
| 9 | `web/user-console/src/views/identity/VerificationView.vue` | 实名认证提交页 |
| 10 | `web/user-console/src/components/layout/UserLayout.vue` | 用户端布局 |
| 11 | `web/user-console/src/components/layout/TopNav.vue` | 顶部导航 |
| 12 | `web/user-console/src/views/marketplace/MarketplaceView.vue` | 商品市场 |
| 13 | `web/user-console/src/views/marketplace/ProductDetailView.vue` | 商品详情 |

### Week 2

| # | 文件 | 说明 |
|---|---|---|
| 1 | `web/user-console/src/views/marketplace/PurchaseView.vue` | 购买确认（含 Idempotency-Key） |
| 2 | `web/user-console/src/views/overview/OverviewView.vue` | 总览 |
| 3 | `web/user-console/src/views/assets/AssetListView.vue` | 我的资产 |
| 4 | `web/user-console/src/views/wallet/WalletView.vue` | 钱包余额 |
| 5 | `web/user-console/src/views/wallet/RechargeView.vue` | 充值页 |
| 6 | `web/user-console/src/views/wallet/TransactionView.vue` | 账单流水 |
| 7 | `web/user-console/src/views/membership/MembershipView.vue` | 会员中心 |
| 8 | `web/user-console/src/components/common/ProductCard.vue` | 商品卡片 |
| 9 | `web/user-console/src/components/common/WalletBalance.vue` | 余额展示 |

---

## 人工必须审查的代码（不允许直接用 AI 输出）

| 文件 | 原因 |
|---|---|
| `billing/service/wallet_service.go` 的 Deduct 方法 | 乐观锁扣费，涉及资金安全 |
| `billing/service/payment_service.go` 的 HandleNotify | 支付回调幂等，涉及资金安全 |
| `product/service/purchase_service.go` 的 Purchase | 完整购买链路，涉及多模块事务 |
| `iam/service/iam_service.go` 的 CheckPermission | 权限计算优先级，涉及安全 |
| `identity/service/identity_service.go` 的 HMAC 处理 | 身份证号隐私，涉及合规 |
| `asset/service/asset_service.go` 的 ConsumeEntitlement | 并发安全，涉及额度扣减 |

---

## 模块依赖顺序（开发必须按此顺序）

```text
Week 1 必须完成（其他模块的前提）：
  pkg/db → pkg/cache → pkg/crypto → pkg/jwt
  auth → iam → identity
  middleware/auth → middleware/permission

Week 2 可并行：
  后端 B：product + order
  前端 A：LoginView + 布局 + 用户管理 + 角色管理
  前端 B：RegisterView + LoginView + 实名页 + 商品市场

Week 3（依赖 Week 2 完成）：
  后端 B：billing（扣费） + purchase_service
  后端 C：provision + asset
  前端：购买确认 + 我的资产 + 钱包

Week 4：
  后端 C：membership + app + content + 定时任务
  前端：会员中心 + 公告 + 帮助中心
```
