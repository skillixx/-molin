# 开发执行计划

## 1. 开发优先级

第一阶段不要先做页面堆砌，也不要先做 GPU、Agent、Skills、Token 这些复杂业务。应该先把平台底座做稳。

三阶段交付计划：

```text
第一阶段（Week 1–4）：平台底座 + 应用售卖
  基础工程 + 认证（auth + user_sessions）
  -> 用户与权限（iam + identity）
  -> 统一商品中心（product）
  -> 订单与钱包（billing + 支付回调）
  -> 用户资产与权益（asset）
  -> 应用售卖闭环（app + provision）
  -> 会员制（membership）
  -> 公告和帮助文档（content）

第二阶段（Week 5–9）：Token 网关 + Agent / Skills
  -> Token 上游聚合网关（token_gateway）
  -> Agent 定制市场（agent）
  -> Skills 技能市场（skill）

第三阶段（Week 10–12）：GPU 服务器出售
  -> GPU 设备管理与状态机（resource）
  -> 租赁订单和按量计费
  -> 到期释放任务
```

最先开发的不是某个应用，而是这四个底座：

- `Auth`：用户、会话（user_sessions）、Refresh Token、登录/注册。
- `IAM`：角色、权限、动态授权、权限缓存。
- `Product`：统一商品、套餐、价格、会员规则。
- `Billing`：订单、钱包、流水、乐观锁扣费、支付回调。
- `Asset`：用户资产、权益额度、到期状态。

这五个底座决定后面所有应用能不能快速接入。

## 2. 第一阶段目标

第一阶段目标是做出一个可运行的管理平台骨架，并完成一个简单应用的完整售卖闭环。

闭环必须包含：

```text
管理员创建商品
  -> 配置套餐
  -> 配置角色权限
  -> 配置价格
  -> 用户购买
  -> 钱包扣费
  -> 生成订单
  -> 生成用户资产
  -> 用户可以访问已购买应用
  -> 后台可以查订单、流水、资产
```

只要这个闭环跑通，后面的 GPU、Agent、Skills、Token、网盘都可以按同一套架构接入。

## 3. 推荐团队分工

最低可执行团队：

- 后端 2 人。
- 前端 2 人。
- 产品 / 测试 1 人。
- 运维 / 全栈 1 人兼职。

更稳妥团队：

- 后端 3 人。
- 前端 2 人。
- 产品 1 人。
- 测试 1 人。
- 运维 1 人。

## 4. 后端任务分配

### 后端 A：平台底座

负责模块：

- `auth`
- `iam`
- `audit`

任务：

- 邮箱注册。
- 手机号注册。
- 邮箱登录。
- 手机号登录。
- 实名制认证。
- 邮箱验证码。
- 短信验证码。
- JWT / API Key。
- 用户管理。
- 角色管理。
- 权限管理。
- 用户动态授权。
- 权限缓存失效。
- 审计日志。

优先级最高，因为其他接口都依赖鉴权和权限。

### 后端 B：商品、订单、财务

负责模块：

- `product`
- `order`
- `billing`
- `finance_consumer`

任务：

- 统一商品。
- 商品套餐。
- 商品价格。
- 商品角色权限。
- 商品会员规则。
- 订单。
- 钱包。
- 充值。
- 扣费。
- 财务流水。
- 产品消费事件。

这是整个平台最关键的业务核心，必须优先完成。

### 后端 C：资产、会员、应用接入

负责模块：

- `asset`
- `membership`
- `application_adapter`
- `app`
- `content`

任务：

- 用户资产。
- 用户权益额度。
- 会员等级。
- 会员权益。
- 应用适配器。
- 应用管理。
- 系统公告。
- 帮助文档。

如果只有 2 个后端，后端 C 的任务由后端 A 和后端 B 分摊，但 `asset` 必须尽早做。

## 5. 前端任务分配

### 前端 A：管理后台

负责页面：

- 登录页。
- 仪表盘。
- 用户管理。
- 角色管理。
- 权限管理。
- 用户动态授权。
- 统一商品管理。
- 商品套餐管理。
- 商品价格配置。
- 商品会员规则配置。
- 订单管理。
- 财务流水。
- 用户资产管理。

后台优先级高于用户端，因为第一阶段需要运营人员能配置商品和查账。

### 前端 B：用户控制台

负责页面：

- 登录 / 注册。
- 实名认证。
- 总览。
- 商品市场。
- 商品详情。
- 购买页。
- 我的资产。
- 我的权益额度。
- 会员中心。
- 账户余额。
- 账单流水。
- 系统公告。
- 帮助中心。

第一阶段用户端只需要做出购买闭环，不要一开始追求复杂交互。

## 6. 产品 / 测试任务

产品和测试前期应该一起做，重点不是写大而全的 PRD，而是把业务规则写清楚。

必须提前确认：

- 角色列表。
- 权限点列表。
- 商品类型列表。
- 订单状态。
- 钱包流水类型。
- 资产状态。
- 会员等级。
- 会员权益。
- 计费规则。
- 退款规则。
- 到期规则。

第一阶段测试用例：

- 用户可以使用邮箱注册。
- 用户可以使用手机号注册。
- 用户可以使用邮箱登录。
- 用户可以使用手机号登录。
- 用户注册后可以提交实名认证。
- 未实名用户不能购买商品。
- 重复邮箱不能注册两个用户。
- 重复手机号不能注册两个用户。
- 普通用户购买普通应用。
- VIP 用户购买会员价应用。
- 非会员无法购买会员专属应用。
- 用户余额不足无法购买。
- 钱包扣费后生成流水。
- 订单支付成功后生成资产。
- 管理员修改用户角色后权限立即生效。
- 用户权限被禁用后无法访问应用。

## 7. 运维任务

第一阶段运维不要复杂化，先满足开发、测试、演示。

任务：

- Docker Compose。
- MySQL。
- Redis。
- RabbitMQ。
- MinIO。
- Go API 服务。
- Vue3 管理后台。
- Vue3 用户控制台。
- 基础 CI。
- 环境变量管理。

第一版先用单机或小型云服务器部署，后面再上 Kubernetes。

## 8. 第一个可验收版本

第一个可验收版本不应该等所有功能都做完。

建议第 2 到第 3 周验收：

```text
管理员登录后台
  -> 创建角色
  -> 创建用户
  -> 给用户分配角色
  -> 创建商品
  -> 创建套餐
  -> 配置价格
  -> 配置角色可购买
  -> 用户登录控制台
  -> 用户充值
  -> 用户购买商品
  -> 系统扣费
  -> 生成订单
  -> 生成资产
  -> 用户看到已购买资产
```

这个版本完成后，再进入 GPU、Agent、Skills、Token。

## 9. 开发节奏

建议按周推进：

### Week 1：基础工程 + 认证

- 建前后端工程骨架。
- 建数据库 migration（第一批：users、user_sessions、verification_codes、user_login_logs、roles、permissions、user_roles、role_permissions、wallets）。
- 邮箱注册、手机号注册、邮箱登录、手机号登录。
- 邮箱验证码、短信验证码（防刷限流：10 req/min / IP）。
- JWT Access Token + Refresh Token（user_sessions 持久化，hash 存储）。
- 退出登录（吊销 user_sessions）、刷新令牌。
- 实名制认证提交和审核流程（id_card_no_hmac，HMAC-SHA256）。
- 用户动态授权和权限缓存失效机制（Redis key: perm:user:{user_id}，TTL 5 min）。
- 后台基础布局（登录页、菜单骨架）。
- 用户端基础布局。

### Week 2：商品、权限与应用

- 完成 RBAC（角色、权限、配置角色权限）。
- 完成统一商品模型（products、product_plans、product_prices、product_role_access）。
- 完成商品购买路由和业务开通处理器接口（provision）。
- 完成会员等级、会员权益和商品会员规则模型。
- 完成应用适配器注册接口（application_adapters）。
- 完成应用 CRUD（applications 表，只含业务详情字段，不含套餐/价格/权限）。
- 用户端应用市场骨架。

### Week 3：订单、钱包与资产

- 完成钱包（乐观锁扣费：SELECT FOR UPDATE + version 字段）。
- 完成充值订单创建和支付回调接口（POST /api/payments/notify/:provider，幂等 + 签名校验）。
- 完成消费订单和钱包流水（wallet_transactions）。
- 完成统一商品购买。
- 完成产品消费事件接入和计费规则（finance_consumer，幂等键：idempotency_key 唯一索引）。
- 完成会员商品购买和会员权益校验。
- 完成用户资产和权益生成（user_assets、user_entitlements）。
- 完成应用购买（第一个应用售卖闭环）。
- 完成基础对账查询。

### Week 4：内容、会员与完善

- 完成系统公告和帮助文档（content）。
- 完成用户端会员中心页面。
- 完善管理后台（订单管理、财务流水、用户资产管理）。
- 完成并测试第一个完整应用售卖闭环。
- 第一阶段验收。

### Week 5–9：Token 网关 + Agent / Skills（第二阶段）

- Token 供应商管理（api_key AES-256-GCM 加密）、模型路由（weight + priority）、断路器（熔断切换备用路由）、流式调用（SSE，不缓冲 response body）、用量统计（token_gateway）。
- Token 接入统一商品中心（product_type=token）、按 input/output tokens 计费、token_quota 额度资产。
- Agent 模板管理、用户 Agent 创建和定制订单（agent）。
- Skills 管理、版本、购买、安装、Agent 绑定（skill）。

### Week 10–12：GPU 服务器出售（第三阶段）

- GPU 设备管理和状态机（available → reserved → deploying → running → releasing → released）。
- 租赁订单（按量计费接入 finance_consumer）。
- 到期释放定时任务。
- 用户端租赁页面和我的实例。
- 管理后台设备和租赁管理。

## 10. AI 开发使用方式

AI 不要先让它生成整个平台。正确用法是按模块拆小任务。

推荐提示词方式：

```text
请基于现有 Go + MySQL 项目，为 product 模块生成：
1. 数据库 migration
2. model
3. repository
4. service
5. handler
6. route
7. 基础单元测试

要求遵循现有目录结构，不要改其他模块。
```

适合 AI 优先做：

- CRUD。
- migration。
- 后台表格页面。
- 表单页面。
- 接口 DTO。
- 单元测试。
- 接口测试。

人工必须把关：

- 钱包扣费。
- 订单状态。
- 权限判定。
- 用户资产生成。
- 会员权益优先级。
- 按量计费。
- 幂等处理。

## 11. 当前最应该做的事情

现在最应该做的是创建项目骨架，不要继续扩展需求。

下一步建议直接开始：

```text
web/admin-console
web/user-console
server/api
server/internal/modules/auth
server/internal/modules/iam
server/internal/modules/product
server/internal/modules/order
server/internal/modules/billing
server/internal/modules/asset
infra/docker-compose.yml
```

第一批只开发：

- 登录。
- 用户管理。
- 角色管理。
- 权限管理。
- 商品管理。
- 钱包。
- 订单。
- 用户资产。

等这个闭环完成后，再继续开发 GPU、Agent、Skills、Token。
