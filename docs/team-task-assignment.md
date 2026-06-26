# 团队任务分配表

## 1. 分工原则

每人负责明确模块边界，不横跨他人模块写代码。跨模块调用只通过 `service` 层接口，不直接访问他人 `repository`。

## 2. 后端代码结构约定

每个 Go 模块统一目录结构：

```text
server/internal/modules/{module}/
  model/          -- Go 结构体，对应数据库表
  repository/     -- 数据库访问层（CRUD）
  service/        -- 业务逻辑层
  handler/        -- HTTP Handler
  dto/            -- 请求 / 响应 DTO
  route.go        -- 注册路由
```

## 3. 前端代码结构约定

每个 Vue 工程统一目录结构：

```text
web/{console}/src/
  api/            -- Axios 请求封装
  views/          -- 页面组件
  components/     -- 公共组件
  stores/         -- Pinia 状态
  router/         -- Vue Router
  types/          -- TypeScript 类型
```

---

## 4. 后端 A：账号 + 权限 + 实名 + 审计

**负责人：后端 A**

**负责模块：**

```text
server/internal/modules/auth/
server/internal/modules/iam/
server/internal/modules/identity/
server/internal/modules/audit/
server/internal/middleware/   （auth.go、permission.go、rate_limit.go）
```

### 4.1 Week 1 任务与代码位置

| 任务 | 代码文件 |
|---|---|
| users 表结构体 | `modules/auth/model/user.go` |
| user_sessions 表结构体 | `modules/auth/model/session.go` |
| verification_codes 表结构体 | `modules/auth/model/verification.go` |
| user_login_logs 表结构体 | `modules/auth/model/login_log.go` |
| 用户 CRUD | `modules/auth/repository/user_repo.go` |
| 会话 CRUD | `modules/auth/repository/session_repo.go` |
| 验证码 CRUD | `modules/auth/repository/verification_repo.go` |
| 登录日志写入 | `modules/auth/repository/login_log_repo.go` |
| 注册 / 登录 / 退出 / 刷新业务逻辑 | `modules/auth/service/auth_service.go` |
| 验证码发送 / 校验 | `modules/auth/service/verification_service.go` |
| 注册 / 登录接口 Handler | `modules/auth/handler/auth_handler.go` |
| 注册 / 登录 DTO | `modules/auth/dto/auth_dto.go` |
| auth 路由注册 | `modules/auth/route.go` |
| JWT 生成 / 校验工具 | `server/pkg/jwt/jwt.go` |
| 密码 hash 工具 | `server/pkg/crypto/password.go` |
| HMAC 工具（用于 id_card_no / refresh_token） | `server/pkg/crypto/hmac.go` |
| JWT 鉴权中间件 | `server/internal/middleware/auth.go` |

**安全要求：**

- Refresh Token 明文只在响应中返回一次，数据库存 `HMAC-SHA256(token, REFRESH_TOKEN_SECRET)`。
- 退出登录写入 `user_sessions.revoked_at`，刷新时先检查 `revoked_at IS NULL`。
- 验证码 10 分钟过期，使用后写入 `used_at`，不可二次使用。
- 登录限流：10 次/分钟/IP，超限返回 429。

### 4.2 Week 1–2 任务与代码位置（IAM）

| 任务 | 代码文件 |
|---|---|
| roles / permissions / user_roles / role_permissions / user_permission_overrides 结构体 | `modules/iam/model/role.go` |
| role_change_logs 结构体 | `modules/iam/model/change_log.go` |
| 角色 CRUD | `modules/iam/repository/role_repo.go` |
| 权限 CRUD | `modules/iam/repository/permission_repo.go` |
| 用户角色 CRUD | `modules/iam/repository/user_role_repo.go` |
| 用户权限覆盖 CRUD | `modules/iam/repository/override_repo.go` |
| RBAC 权限计算（汇总角色权限 + 覆盖） | `modules/iam/service/iam_service.go` |
| Redis 权限缓存（key: perm:user:{id}，TTL 5min） | `modules/iam/service/cache_service.go` |
| 角色 / 权限 / 动态授权 Admin Handler | `modules/iam/handler/iam_handler.go` |
| IAM DTO | `modules/iam/dto/iam_dto.go` |
| IAM 路由注册 | `modules/iam/route.go` |
| 权限校验中间件（读缓存，走 IAM service） | `server/internal/middleware/permission.go` |

**权限优先级（代码必须按此顺序判定）：**

```text
1. user_permission_overrides effect=deny  → 直接拒绝
2. user_permission_overrides effect=allow → 直接通过
3. 角色聚合权限                           → 通过 / 拒绝
4. 默认拒绝
```

### 4.3 Week 1 任务（实名认证）

| 任务 | 代码文件 |
|---|---|
| identity_verifications / identity_verification_logs 结构体 | `modules/identity/model/identity.go` |
| 实名 CRUD | `modules/identity/repository/identity_repo.go` |
| 提交实名 / 审核逻辑（HMAC 处理） | `modules/identity/service/identity_service.go` |
| 实名接口 Handler | `modules/identity/handler/identity_handler.go` |
| 实名 DTO | `modules/identity/dto/identity_dto.go` |
| 实名路由注册 | `modules/identity/route.go` |

**安全要求：**

- `id_card_no` 不入库，只存 `HMAC-SHA256(id_card_no, ID_CARD_HMAC_SECRET)` 和 `masked` 值。
- HMAC 工具复用 `server/pkg/crypto/hmac.go`。

### 4.4 审计日志

| 任务 | 代码文件 |
|---|---|
| audit_logs / role_change_logs 结构体 | `modules/audit/model/audit.go` |
| 审计日志写入 | `modules/audit/repository/audit_repo.go` |
| 审计日志 service（供其他模块调用） | `modules/audit/service/audit_service.go` |
| 审计日志 Admin 查询 Handler | `modules/audit/handler/audit_handler.go` |
| 审计路由注册 | `modules/audit/route.go` |

---

## 5. 后端 B：商品 + 订单 + 钱包 + 计费

**负责人：后端 B**

**负责模块：**

```text
server/internal/modules/product/
server/internal/modules/order/
server/internal/modules/billing/
server/internal/modules/finance_consumer/
```

### 5.1 Week 2 任务（商品）

| 任务 | 代码文件 |
|---|---|
| products / product_plans / product_prices / product_role_access 结构体 | `modules/product/model/product.go` |
| product_billing_rules / product_consumption_records 结构体 | `modules/product/model/billing_rule.go` |
| product_membership_rules 结构体 | `modules/product/model/membership_rule.go` |
| product_provision_handlers / application_adapters 结构体 | `modules/product/model/adapter.go` |
| 商品 CRUD | `modules/product/repository/product_repo.go` |
| 套餐 CRUD | `modules/product/repository/plan_repo.go` |
| 价格 CRUD | `modules/product/repository/price_repo.go` |
| 商品角色权限 CRUD | `modules/product/repository/access_repo.go` |
| 计费规则 CRUD | `modules/product/repository/billing_rule_repo.go` |
| 消费记录 CRUD | `modules/product/repository/consumption_repo.go` |
| 商品管理业务逻辑 | `modules/product/service/product_service.go` |
| 价格计算（角色价 / 会员价 / 默认价）| `modules/product/service/pricing_service.go` |
| 权限校验（can_view / can_buy / can_use）| `modules/product/service/access_service.go` |
| 商品 Handler（用户端 + 管理端） | `modules/product/handler/product_handler.go` |
| 商品 DTO | `modules/product/dto/product_dto.go` |
| product 路由注册 | `modules/product/route.go` |

**价格优先级（代码必须按此顺序匹配）：**

```text
1. 会员专属价（product_prices.membership_level_id IS NOT NULL AND role_id IS NULL）
2. 角色价（product_prices.role_id IS NOT NULL AND membership_level_id IS NULL）
3. 默认价（product_prices.role_id IS NULL AND membership_level_id IS NULL）
```

### 5.2 Week 3 任务（订单）

| 任务 | 代码文件 |
|---|---|
| orders / order_items 结构体 | `modules/order/model/order.go` |
| 订单 CRUD | `modules/order/repository/order_repo.go` |
| 创建订单 / 订单状态流转 | `modules/order/service/order_service.go` |
| 订单号生成（格式：ORD + 日期 + 随机8位） | `server/pkg/idgen/order_no.go` |
| 订单 Handler | `modules/order/handler/order_handler.go` |
| 订单 DTO | `modules/order/dto/order_dto.go` |
| order 路由注册 | `modules/order/route.go` |

**订单状态机（代码必须严格按此流转）：**

```text
pending → paid（扣费成功）
pending → cancelled（超时或用户取消）
paid    → refunded（退款）
pending/paid → failed（系统错误）
```

### 5.3 Week 3 任务（钱包 + 支付回调）

| 任务 | 代码文件 |
|---|---|
| wallets / wallet_transactions 结构体 | `modules/billing/model/wallet.go` |
| payment_callbacks 结构体 | `modules/billing/model/payment.go` |
| 钱包 CRUD（含乐观锁查询） | `modules/billing/repository/wallet_repo.go` |
| 钱包流水写入 | `modules/billing/repository/transaction_repo.go` |
| 支付回调记录 CRUD | `modules/billing/repository/payment_repo.go` |
| 扣费逻辑（乐观锁事务） | `modules/billing/service/wallet_service.go` |
| 支付回调处理（签名校验 + 幂等） | `modules/billing/service/payment_service.go` |
| 微信支付签名校验 | `modules/billing/service/wechat_verifier.go` |
| 支付宝签名校验 | `modules/billing/service/alipay_verifier.go` |
| 钱包 Handler（用户端 + 管理端） | `modules/billing/handler/billing_handler.go` |
| 支付回调 Handler（POST /api/payments/notify/:provider） | `modules/billing/handler/payment_handler.go` |
| billing DTO | `modules/billing/dto/billing_dto.go` |
| billing 路由注册 | `modules/billing/route.go` |

**扣费事务模板（必须用乐观锁）：**

```go
// 开启事务
// SELECT * FROM wallets WHERE user_id = ? FOR UPDATE
// 校验 balance_amount >= 扣款金额
// UPDATE wallets SET balance_amount = balance_amount - ?, version = version + 1
//   WHERE id = ? AND version = ?
// 检查影响行数：0 则乐观锁冲突，重试最多 3 次
// INSERT wallet_transactions
// 提交事务
```

### 5.4 Week 3 任务（财务消费路由）

| 任务 | 代码文件 |
|---|---|
| ProductUsageEvent 结构体 | `modules/finance_consumer/model/event.go` |
| 消费事件处理（幂等 + 计费规则匹配 + 扣费） | `modules/finance_consumer/service/consumer_service.go` |
| 内部消费事件 Handler（POST /api/internal/product-usage-events） | `modules/finance_consumer/handler/consumer_handler.go` |
| finance_consumer 路由注册 | `modules/finance_consumer/route.go` |

**消费幂等模板：**

```go
// SELECT id FROM product_consumption_records WHERE idempotency_key = ?
// 如已存在：直接返回原结果
// 否则：匹配计费规则 → 计算金额 → 事务扣费 → 写消费记录
```

---

## 6. 后端 C：资产 + 会员 + 应用 + 内容 + 开通路由

**负责人：后端 C**

**负责模块：**

```text
server/internal/modules/asset/
server/internal/modules/membership/
server/internal/modules/app/
server/internal/modules/provision/
server/internal/modules/content/
```

### 6.1 Week 3 任务（用户资产）

| 任务 | 代码文件 |
|---|---|
| user_assets / user_entitlements / asset_events 结构体 | `modules/asset/model/asset.go` |
| 资产 CRUD | `modules/asset/repository/asset_repo.go` |
| 权益额度 CRUD | `modules/asset/repository/entitlement_repo.go` |
| 资产事件写入 | `modules/asset/repository/event_repo.go` |
| 创建资产 / 更新状态 / 到期检查 | `modules/asset/service/asset_service.go` |
| 权益额度消耗 / 补充 | `modules/asset/service/entitlement_service.go` |
| 资产 Handler（用户端 + 管理端） | `modules/asset/handler/asset_handler.go` |
| 资产 DTO | `modules/asset/dto/asset_dto.go` |
| asset 路由注册 | `modules/asset/route.go` |

### 6.2 Week 2–3 任务（会员）

| 任务 | 代码文件 |
|---|---|
| membership_levels / membership_benefits / user_memberships 结构体 | `modules/membership/model/membership.go` |
| 会员等级 CRUD | `modules/membership/repository/level_repo.go` |
| 会员权益 CRUD | `modules/membership/repository/benefit_repo.go` |
| 用户会员 CRUD | `modules/membership/repository/user_membership_repo.go` |
| 会员状态校验 / 权益查询 | `modules/membership/service/membership_service.go` |
| 会员 Handler（用户端 + 管理端） | `modules/membership/handler/membership_handler.go` |
| 会员 DTO | `modules/membership/dto/membership_dto.go` |
| membership 路由注册 | `modules/membership/route.go` |

### 6.3 Week 2 任务（应用 + 开通路由）

| 任务 | 代码文件 |
|---|---|
| applications / application_adapters 结构体 | `modules/app/model/app.go` |
| 应用 CRUD | `modules/app/repository/app_repo.go` |
| 适配器注册 CRUD | `modules/app/repository/adapter_repo.go` |
| 应用业务逻辑 | `modules/app/service/app_service.go` |
| 应用适配器注册 | `modules/app/service/adapter_service.go` |
| 应用 Handler（用户端 + 管理端） | `modules/app/handler/app_handler.go` |
| app 路由注册 | `modules/app/route.go` |
| 开通处理器接口定义 | `modules/provision/interface.go` |
| 开通路由（按 product_type 分发） | `modules/provision/service/provision_service.go` |
| 应用类型开通处理器 | `modules/provision/service/app_provisioner.go` |
| provision 路由注册 | `modules/provision/route.go` |

**开通处理器接口（所有业务模块必须实现）：**

```go
type ProvisionHandler interface {
    Provision(ctx context.Context, order Order, product Product, plan Plan) (*ProvisionResult, error)
    Renew(ctx context.Context, instance Asset, order Order) error
    Suspend(ctx context.Context, instance Asset, reason string) error
    Resume(ctx context.Context, instance Asset) error
    Cancel(ctx context.Context, instance Asset) error
}
```

### 6.4 Week 4 任务（内容管理）

| 任务 | 代码文件 |
|---|---|
| announcements / help_categories / help_articles 结构体 | `modules/content/model/content.go` |
| 公告 CRUD | `modules/content/repository/announcement_repo.go` |
| 帮助文档 CRUD | `modules/content/repository/help_repo.go` |
| 内容发布 / 可见范围过滤 | `modules/content/service/content_service.go` |
| 内容 Handler（用户端 + 管理端） | `modules/content/handler/content_handler.go` |
| content 路由注册 | `modules/content/route.go` |

---

## 6.5 后端 D：Token 网关 + Agent + Skill（第二阶段 AI 业务）

**负责人：后端工程师丁**

**Git 分支前缀：`feature/backend-d-{描述}`**（与 A/B/C 平级，禁止直接 push `main`）

**负责模块：**

```text
server/internal/modules/token_gateway/   -- Token 上游聚合网关、模型路由转发、OpenAI 兼容对话、用量计费
server/internal/modules/workbench/       -- Agent 定制市场、Skills 技能市场、多模型聊天工作台
```

### 6.5.1 Token 网关（token_gateway）

| 任务 | 代码位置 |
|---|---|
| 上游渠道管理（AES-256-GCM 加密 API Key） | `modules/token_gateway/service/channel_service.go` |
| 对外模型目录（logical_model_code + 定向可见性） | `modules/token_gateway/service/catalog_service.go` |
| 对话转发门面（选渠道 + 透传上游 + 计费编排，含 SSE） | `modules/token_gateway/service/forward_service.go` |
| 用量流水记录与查询 | `modules/token_gateway/service/usage_service.go` |
| 管理端 / 用户端路由注册 | `modules/token_gateway/route.go` |

**对外开放接口（凭平台 sk 密钥，OpenAI 兼容）：**

- `POST /api/token/chat/completions`、`GET /api/token/models`、`GET /api/token/usage`（平台原生路径）
- `POST /v1/chat/completions`、`GET /v1/models`（OpenAI 兼容别名，供 Cline / Cherry Studio 等客户端直接接入，详见 `docs/token-gateway-openai-compat.md`）

**鉴权与安全约定：**

- 双模式鉴权统一走 `middleware.RequireUserAuth`：`Authorization: Bearer sk-...` 走 API Key 解析，否则走登录态 JWT。
- 平台 API Key（sk）由 `auth` 模块签发与管理（`POST /api/keys`），DB 只存 `HMAC-SHA256`，明文仅签发时返回一次。
- 上游渠道 API Key 必须 AES-256-GCM 加密存储（`TOKEN_PROVIDER_KEY`），响应禁止返回。

### 6.5.2 AI 工作台（workbench：Agent / Skill / Chat）

| 任务 | 代码位置 |
|---|---|
| Agent 定制市场（含分类、定向可见性） | `modules/workbench/service/agent_service.go` |
| Skills 技能市场 | `modules/workbench/service/`（skill 相关） |
| 多模型聊天编排 | `modules/workbench/service/chat_service.go` |
| workbench 路由注册 | `modules/workbench/route.go` |

> 计费红线：Agent / Skill / 插件全部免费，唯一收费项为模型 token 消耗，计费一律经 token_gateway 门面统一编排。

---

## 7. 前端 A：管理后台

**负责人：前端 A**

**负责工程：`web/admin-console`**

### 7.1 代码文件规划

```text
web/admin-console/src/
  api/
    http.ts               -- Axios 实例，统一 baseURL / token / 响应拦截
    auth.ts               -- 登录、刷新令牌
    user.ts               -- 用户管理接口
    role.ts               -- 角色、权限、动态授权接口
    identity.ts           -- 实名认证审核接口
    product.ts            -- 商品、套餐、价格接口
    order.ts              -- 订单管理接口
    wallet.ts             -- 钱包流水接口
    asset.ts              -- 用户资产、权益接口
    membership.ts         -- 会员等级、权益接口
    content.ts            -- 公告、帮助文档接口
    audit.ts              -- 审计日志接口
  types/
    user.ts               -- 用户、角色、权限 TS 类型
    product.ts            -- 商品、套餐、价格 TS 类型
    order.ts              -- 订单、流水 TS 类型
    asset.ts              -- 资产、权益 TS 类型
  stores/
    auth.ts               -- 登录状态、当前用户、角色
    app.ts                -- 全局配置、菜单状态
  views/
    auth/
      LoginView.vue
    dashboard/
      DashboardView.vue
    user/
      UserListView.vue
      UserDetailView.vue
      UserRoleView.vue
      UserPermOverrideView.vue
    identity/
      VerificationListView.vue
      VerificationDetailView.vue
    iam/
      RoleListView.vue
      PermissionListView.vue
    product/
      ProductListView.vue
      ProductFormView.vue
      PlanFormView.vue
      PriceFormView.vue
      AccessFormView.vue
    order/
      OrderListView.vue
      OrderDetailView.vue
    wallet/
      TransactionListView.vue
    asset/
      AssetListView.vue
      EntitlementListView.vue
    membership/
      LevelListView.vue
      BenefitListView.vue
      MembershipRuleView.vue
    content/
      AnnouncementListView.vue
      AnnouncementFormView.vue
      HelpCategoryView.vue
      HelpArticleView.vue
    audit/
      AuditLogView.vue
  components/
    layout/
      AdminLayout.vue     -- 整体布局（侧边栏 + 顶栏 + 内容区）
      SideMenu.vue        -- 导航菜单
      TopBar.vue          -- 顶部栏（用户名、退出）
    common/
      DataTable.vue       -- 通用表格（分页、列配置）
      SearchForm.vue      -- 通用搜索表单
      StatusTag.vue       -- 状态标签（active/disabled 等）
      ConfirmDialog.vue   -- 通用确认弹窗
      PageHeader.vue      -- 页头（标题 + 操作按钮）
  router/
    index.ts              -- 路由配置，含权限守卫
```

### 7.2 Week 1–2 优先页面

| 周次 | 页面 | 文件 |
|---|---|---|
| Week 1 | 登录 | `views/auth/LoginView.vue` |
| Week 1 | 后台布局骨架 | `components/layout/AdminLayout.vue` |
| Week 1 | 用户列表 | `views/user/UserListView.vue` |
| Week 1 | 角色管理 | `views/iam/RoleListView.vue` |
| Week 2 | 权限管理 | `views/iam/PermissionListView.vue` |
| Week 2 | 商品管理 | `views/product/ProductListView.vue` |
| Week 2 | 套餐配置 | `views/product/PlanFormView.vue` |
| Week 3 | 价格配置 | `views/product/PriceFormView.vue` |
| Week 3 | 订单管理 | `views/order/OrderListView.vue` |
| Week 3 | 钱包流水 | `views/wallet/TransactionListView.vue` |
| Week 3 | 用户资产 | `views/asset/AssetListView.vue` |
| Week 4 | 实名认证审核 | `views/identity/VerificationListView.vue` |
| Week 4 | 公告管理 | `views/content/AnnouncementListView.vue` |

---

## 8. 前端 B：用户控制台

**负责人：前端 B**

**负责工程：`web/user-console`**

### 8.1 代码文件规划

```text
web/user-console/src/
  api/
    http.ts               -- Axios 实例，统一 baseURL / token / 响应拦截
    auth.ts               -- 注册、登录、刷新、退出
    identity.ts           -- 提交实名认证
    product.ts            -- 商品市场、商品详情
    order.ts              -- 订单查询
    wallet.ts             -- 余额、流水
    recharge.ts           -- 充值订单创建
    asset.ts              -- 我的资产、权益额度
    membership.ts         -- 会员中心
    content.ts            -- 公告、帮助中心
  types/
    auth.ts
    product.ts
    order.ts
    asset.ts
    wallet.ts
  stores/
    auth.ts               -- 登录态、用户信息、实名状态
    wallet.ts             -- 钱包余额（实时）
  views/
    auth/
      LoginView.vue
      RegisterView.vue
    identity/
      VerificationView.vue
    overview/
      OverviewView.vue    -- 总览（余额、资产摘要、公告）
    marketplace/
      MarketplaceView.vue
      ProductDetailView.vue
      PurchaseView.vue    -- 购买确认页（选套餐、显示价格、扣费预览）
    assets/
      AssetListView.vue
      EntitlementView.vue
    wallet/
      WalletView.vue      -- 余额 + 充值入口
      RechargeView.vue    -- 充值页（选金额、选支付方式）
      TransactionView.vue
    membership/
      MembershipView.vue
    content/
      AnnouncementView.vue
      HelpCenterView.vue
      HelpArticleView.vue
  components/
    layout/
      UserLayout.vue
      TopNav.vue
      BottomBar.vue       -- 移动端底部导航（可选）
    common/
      ProductCard.vue     -- 商品卡片
      AssetCard.vue       -- 资产卡片
      WalletBalance.vue   -- 余额展示组件
      StepProgress.vue    -- 购买步骤
      EmptyState.vue      -- 空状态提示
  router/
    index.ts              -- 路由，未登录跳转 login，未实名跳转实名页
```

### 8.2 Week 1–2 优先页面

| 周次 | 页面 | 文件 |
|---|---|---|
| Week 1 | 注册 | `views/auth/RegisterView.vue` |
| Week 1 | 登录 | `views/auth/LoginView.vue` |
| Week 1 | 实名认证 | `views/identity/VerificationView.vue` |
| Week 1 | 用户布局骨架 | `components/layout/UserLayout.vue` |
| Week 2 | 总览页 | `views/overview/OverviewView.vue` |
| Week 2 | 商品市场 | `views/marketplace/MarketplaceView.vue` |
| Week 2 | 商品详情 | `views/marketplace/ProductDetailView.vue` |
| Week 3 | 购买确认 | `views/marketplace/PurchaseView.vue` |
| Week 3 | 我的资产 | `views/assets/AssetListView.vue` |
| Week 3 | 钱包 / 充值 | `views/wallet/WalletView.vue` |
| Week 3 | 账单流水 | `views/wallet/TransactionView.vue` |
| Week 4 | 会员中心 | `views/membership/MembershipView.vue` |
| Week 4 | 公告 / 帮助中心 | `views/content/AnnouncementView.vue` |

---

## 9. 前端共享代码

**负责人：前端 A 主导，前端 B 协同**

**工程：`web/shared`**

```text
web/shared/
  utils/
    format.ts             -- 金额格式化、时间格式化、状态文字映射
    validator.ts          -- 邮箱/手机号/密码校验规则
  types/
    api.ts                -- 通用分页响应、统一响应结构 TS 类型
    enums.ts              -- 全局枚举（用户状态、订单状态、资产状态等）
  constants/
    status.ts             -- 状态常量
```

---

## 10. Migration 文件分配

**负责人：后端 A（核心表）/ 后端 B（商品交易表）/ 后端 C（资产会员内容表）**

```text
server/migrations/
  000001_create_auth_tables.up.sql          -- 后端 A：users, user_sessions, verification_codes, user_login_logs
  000002_create_iam_tables.up.sql           -- 后端 A：roles, permissions, user_roles, role_permissions, user_permission_overrides, role_change_logs, audit_logs
  000003_create_identity_tables.up.sql      -- 后端 A：identity_verifications, identity_verification_logs
  000004_create_product_tables.up.sql       -- 后端 B：products, product_plans, product_prices, product_role_access, product_provision_handlers, product_billing_rules
  000005_create_billing_tables.up.sql       -- 后端 B：wallets, wallet_transactions, orders, order_items, payment_callbacks, product_consumption_records, application_adapters
  000006_create_asset_tables.up.sql         -- 后端 C：user_assets, user_entitlements, asset_events
  000007_create_membership_tables.up.sql    -- 后端 C：membership_levels, membership_benefits, user_memberships, product_membership_rules
  000008_create_app_tables.up.sql           -- 后端 C：applications
  000009_create_content_tables.up.sql       -- 后端 C：announcements, help_categories, help_articles
```

对应 down.sql 每个文件一份。

---

## 11. 公共 pkg 分配

**负责人：后端 A 为主，其他人按需使用**

```text
server/pkg/
  jwt/
    jwt.go                -- Access Token 生成 / 校验 / Claims 解析
  crypto/
    password.go           -- bcrypt hash / verify
    hmac.go               -- HMAC-SHA256（用于身份证号 / refresh token）
    aes.go                -- AES-256-GCM 加解密（用于 Token 供应商密钥）
  idgen/
    order_no.go           -- 订单号生成
    snowflake.go          -- 分布式 ID（可选）
  response/
    response.go           -- 统一 JSON 响应（已存在）
  pagination/
    pagination.go         -- 分页参数解析
  validator/
    validator.go          -- 请求参数校验
```

---

## 12. 环境变量约定（运维提供）

```text
APP_ENV
APP_NAME
API_HOST
API_PORT

MYSQL_HOST
MYSQL_PORT
MYSQL_DATABASE
MYSQL_USER
MYSQL_PASSWORD

REDIS_ADDR
REDIS_PASSWORD
REDIS_DB

RABBITMQ_URL

MINIO_ENDPOINT
MINIO_ACCESS_KEY
MINIO_SECRET_KEY
MINIO_BUCKET

JWT_SECRET
JWT_EXPIRE_SECONDS           -- Access Token 有效期，建议 7200
REFRESH_TOKEN_SECRET         -- Refresh Token HMAC 密钥
REFRESH_TOKEN_EXPIRE_DAYS    -- Refresh Token 有效天数，建议 30

ID_CARD_HMAC_SECRET          -- 身份证号 HMAC 密钥

TOKEN_PROVIDER_KEY            -- Token 供应商 API Key AES-256-GCM 加密密钥（第二阶段使用）
WECHAT_PAY_API_V3_KEY        -- 微信支付 APIv3 密钥
ALIPAY_PUBLIC_KEY             -- 支付宝公钥
```

---

## 13. 运维：环境与部署

**负责人：运维**

**负责目录：`infra/`、`.github/workflows/`、`scripts/`**

**Agent 文件：`infra/CLAUDE.md`**

### 13.1 Week 1 任务

| 任务 | 文件 |
|---|---|
| 本地开发 Docker Compose（已有，确认可用） | `infra/docker-compose.yml` |
| 环境变量模板（已创建） | `infra/.env.example` |
| 提供每位开发者的 `.env.local` 初始值 | 手动分发，不入库 |
| Go API Dockerfile（已创建） | `infra/Dockerfile.server` |
| 管理后台 Dockerfile（已创建） | `infra/Dockerfile.admin-console` |
| 用户控制台 Dockerfile | `infra/Dockerfile.user-console` |
| Nginx 配置（已创建） | `infra/nginx/admin.conf`、`infra/nginx/user.conf` |
| CI 流水线（已创建） | `.github/workflows/ci.yml` |
| 等待服务就绪脚本 | `scripts/wait-for-it.sh` |
| Migration 执行脚本 | `scripts/migrate.sh` |
| 测试数据初始化脚本 | `scripts/seed_test_data.sh` |

### 13.2 Week 2 任务

| 任务 | 文件 |
|---|---|
| 生产 Docker Compose | `infra/docker-compose.prod.yml` |
| 测试环境部署脚本 | `scripts/deploy-test.sh` |
| 配置 CI 自动部署测试环境 | `.github/workflows/deploy-test.yml` |

### 13.3 运维每周工作

```text
每周五：
  -> 拉取 main 最新代码
  -> 执行 migration（scripts/migrate.sh）
  -> 重启服务（docker-compose.prod.yml）
  -> 验证健康检查 /api/health

开发者需要新环境变量时：
  -> 更新 infra/.env.example（入库）
  -> 分发实际值给需要的开发者（不入库）
  -> 更新测试和生产环境配置
```

---

## 14. 产品经理：代码评审与合并

**负责人：产品经理**

**参考文档：`docs/git-workflow.md`**

### 14.1 每周工作

| 时间 | 工作 |
|---|---|
| 周一 | 确认本周功能范围，向开发者澄清需求细节 |
| 周三 | 对已提交的 PR 进行业务逻辑评审 |
| 周五 | 主持当周验收，确认合并范围，通知运维部署 |
| 持续 | 维护 docs/ 下需求和规则文档 |

### 14.2 PR 评审 Checklist（产品经理视角）

```text
□ 接口响应字段与 docs/full-api-design.md 一致
□ 错误文案用中文，用户友好
□ 业务规则正确：实名拦截、权限控制、价格优先级
□ 购买成功后资产正确生成
□ 余额不足、未实名等错误码正确（60001 / 70001）
□ 幂等处理：重复请求不重复扣费
□ 管理后台操作写了审计日志
□ 代码注释使用中文
□ Commit message 使用中文
```

### 14.3 必须拒绝合并的情况

```text
✗ CI 未通过（构建失败、测试失败、lint 失败）
✗ 身份证号明文或 SHA-256 直接 hash 存储
✗ 钱包扣费没有事务保护
✗ 购买接口没有幂等处理
✗ 支付回调没有签名校验
✗ 英文注释或英文 commit message
✗ 未经至少 1 名开发者 Code Review
✗ PR 描述不完整（缺少功能说明、测试情况）
```

---

## 15. 测试：功能测试与验收

**负责人：测试/产品**

**参考文档：`docs/test-plan.md`**

### 15.1 每周工作

| 时间 | 工作 |
|---|---|
| 周一 | 根据本周功能范围准备测试用例 |
| 周三 | 开始接口测试和功能测试 |
| 周五上午 | 完成全部测试，提交测试报告 |
| 周五下午 | 参与验收会议 |

### 15.2 测试脚本位置

```text
tests/
  api/                          -- 接口测试（curl / Bruno 集合）
    auth.http                   -- 认证接口测试
    product.http                -- 商品购买接口测试
    billing.http                -- 钱包充值接口测试
    payment-callback.http       -- 支付回调幂等测试
  load/
    concurrent-deduct.sh        -- 并发扣费压力测试脚本
  seed/
    init-roles.sql              -- 初始化角色数据
    init-admin.sql              -- 初始化管理员账号
```

### 15.3 每周验收门槛

Week 1–2 门槛（必须全部通过才能进入 Week 3）：
```text
□ 邮箱注册、手机号注册、邮箱登录、手机号登录正常
□ 退出登录后原 Token 不可用
□ 未实名用户购买商品返回 70001
□ 管理员可配置角色和权限
□ 权限变更后立即生效（不需重新登录）
```

Week 3 门槛（必须全部通过才能进入 Week 4）：
```text
□ 购买闭环：创建商品 → 充值 → 购买 → 生成资产，全链路通过
□ 余额不足正确拦截
□ 10 并发扣费不出现负余额
□ 同一 Idempotency-Key 重复购买不重复扣费
□ 支付回调重复不重复入账
□ 管理员后台可查订单和钱包流水
```

---

## 16. 模块边界约定

| 规则 | 说明 |
|---|---|
| 不跨模块访问 repository | 只能通过对方 service 接口调用 |
| auth 不依赖 billing | 登录不需要钱包信息 |
| billing 不依赖 asset | 扣费后通知 asset，不直接操作资产 |
| provision 依赖 asset | 开通成功后调用 asset service 创建资产 |
| finance_consumer 依赖 billing | 消费事件最终调用 billing.wallet_service 扣费 |
| 审计日志在各模块写，不跨模块读 | audit_service 只提供 Write 接口 |
