# 后端甲/乙/丙接口人工测试清单（ApiPost）

> 用途：供人工使用 ApiPost 工具逐一测试三位后端工程师负责模块的接口。
> 本清单基于 `docs/full-api-design.md` 与各模块 `route.go` 实际实现整理，
> 并标注了已知缺陷（详见各模块末尾"已知问题"），测试时可重点验证这些点是否已修复/复现。

## 通用约定

- **测试环境**：测试服务器 API `http://8.130.9.163:8080`（或本地 `http://127.0.0.1:8080`）
- **认证方式**：除标注"无需登录"外，均需在请求头携带：
  ```
  Authorization: Bearer <access_token>
  ```
  `access_token` 通过 `POST /api/auth/login/email` 或 `/login/phone` 登录获取
- **管理端接口**（路径含 `/admin/`）：除需要登录外，还需要账号具备对应的权限码
  （如 `product:view`、`order:list`、`user:manage` 等），普通用户访问应返回 `403 {"code":40003}`
- **测试账号建议**：自行注册全新账号（建议邮箱格式 `manualtest_xxx_{时间戳}@molin.io`，
  密码 `Test1234!`），**不要使用任何已存在的管理员/测试账号登录**。如需管理员权限，
  请联系测试/运维同学协助绑定 `admin` 角色，不要自行写库操作共享数据
- **响应包格式**：统一为 `{"code": 0, "message": "ok", "data": {...}}`，`code=0` 表示成功，
  非 0 为业务错误码（如 `40003` 无权限、`40000` 参数错误、`40900` 资源冲突等）

---

## 一、后端工程师甲（auth / iam / identity / audit / middleware）

### 1.1 auth 模块（注册登录、账号管理）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| POST | `/api/auth/verification-codes/email` | 否 | 发送邮箱验证码（scene: register/login/reset_password/bind_email） |
| POST | `/api/auth/verification-codes/phone` | 否 | 发送短信验证码 |
| POST | `/api/auth/register/email` | 否 | 邮箱注册（body: email, password, code） |
| POST | `/api/auth/register/phone` | 否 | 手机号注册 |
| POST | `/api/auth/login/email` | 否 | 邮箱密码登录，返回 access_token + refresh_token |
| POST | `/api/auth/login/phone` | 否 | 手机号验证码登录 |
| POST | `/api/auth/logout` | 是 | 退出登录，注销当前 refresh token |
| POST | `/api/auth/refresh` | 否（携带 refresh_token） | 刷新 access_token |
| POST | `/api/auth/password/reset` | 否 | 找回密码（邮箱/手机号验证码 + 新密码） |
| GET | `/api/me` | 是 | 查询当前登录用户信息 |
| PATCH | `/api/me/profile` | 是 | 修改昵称/头像等基础资料 |
| PATCH | `/api/me/password` | 是 | 修改密码（需旧密码） |
| PATCH | `/api/me/username` | 是 | 修改用户名 |
| PATCH | `/api/me/phone` | 是 | 绑定/修改手机号（需先发验证码 scene=bind_phone） |
| PATCH | `/api/me/email` | 是 | 绑定/修改邮箱（需先发验证码 scene=bind_email） |
| POST | `/api/admin/auth/verify-phone` | 是（管理员） | 管理员代为标记用户手机号已验证 |
| POST | `/api/admin/auth/verify-email` | 是（管理员） | 管理员代为标记用户邮箱已验证 |
| PATCH | `/api/admin/users/:id/status` | 是（管理员，`user:manage`） | 封禁/解封用户（body: `{"status":"active"\|"disabled","reason":"..."}`） |

**重点测试点**：
- 注册/登录全流程（邮箱+手机号两条线）
- 验证码错误、过期、重复使用的拦截
- token 刷新、退出登录后旧 token 是否失效
- 封禁接口：封禁后该用户的 access/refresh token 应立即失效（401），解封后恢复正常

### 1.2 iam 模块（角色权限管理）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/admin/roles` | 是（管理员） | 角色列表 |
| POST | `/api/admin/roles` | 是（管理员） | 创建角色（body: code, name, description） |
| PUT | `/api/admin/roles/:id` | 是（管理员） | 更新角色 |
| DELETE | `/api/admin/roles/:id` | 是（管理员） | 删除角色 |
| GET | `/api/admin/permissions` | 是（管理员） | 权限码列表 |
| GET | `/api/admin/users/:id/roles` | 是（管理员） | 查询用户已绑定角色 |
| POST | `/api/admin/users/:id/roles` | 是（管理员） | 给用户绑定角色 |
| DELETE | `/api/admin/users/:id/roles/:role_id` | 是（管理员） | 解绑用户角色 |
| GET | `/api/admin/users/:id/permission-overrides` | 是（管理员） | 查询用户权限覆盖项 |
| POST | `/api/admin/users/:id/permission-overrides` | 是（管理员） | 新增权限覆盖（单独授予/收回某权限码） |
| DELETE | `/api/admin/users/:id/permission-overrides/:id` | 是（管理员） | 删除权限覆盖项 |

**已知问题（测试时重点验证是否已修复）**：
- 🟡 P2-#2：创建重复 `code` 的角色返回 `HTTP 500`（应为 `400/409`）
- 🟡 P2-#3：`GET /api/admin/roles/:id`（角色详情）**未实现**，请求会返回 `405`
- 🟡 P2-#4：`PATCH /api/admin/roles/:id/permissions`（配置角色权限）**未实现**
- 🔵 P3-#13：`DELETE /api/admin/roles/:id` 对不存在的角色仍返回 `200`（应为 `404`）

### 1.3 identity 模块（实名认证）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| POST | `/api/identity/verifications` | 是 | 提交实名认证申请（姓名、身份证号、证件照片） |
| GET | `/api/identity/verifications/me` | 是 | 查询自己的实名认证状态/记录（注意：文档写的是 `/latest`，实现是 `/me`，详见已知问题） |
| GET | `/api/admin/identity-verifications` | 是（管理员） | 实名认证申请列表（待审核/已通过/已拒绝） |
| GET | `/api/admin/identity-verifications/:id` | 是（管理员） | 实名认证申请详情 |
| PATCH | `/api/admin/identity-verifications/:id/review` | 是（管理员） | 审核通过/拒绝（body: `{"action":"approve"\|"reject","reject_reason":"..."}`） |

**重点测试点**：
- 身份证号格式校验、重复提交拦截
- 审核通过后，用户的实名认证状态应同步更新（影响"未实名用户禁止购买"等业务规则）
- 审核拒绝必须填写理由，重复审核同一申请应被拦截

**已知问题**：
- 🔵 P3-#14：路径命名与文档不一致（文档：`/latest`，实现：`/me`），功能本身正常

### 1.4 audit 模块（审计日志）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/admin/audit-logs` | 是（管理员） | 查询审计日志（按用户/操作类型/时间范围过滤） |

**已知问题**：
- 🟡 P2-#1：该接口**尚未实现**（`audit_logs` 表已存在并有数据，但路由未注册），
  请求会返回 `404`，测试时请重点确认是否已补齐

---

## 二、后端工程师乙（product / order / billing / finance_consumer）

### 2.1 product 模块（商品管理）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/products` | 否 | 用户端商品列表（按上架状态过滤） |
| GET | `/api/products/:id` | 否 | 商品详情 |
| GET | `/api/products/:id/plans` | 否 | 商品套餐列表 |
| POST | `/api/products/:id/purchase` | 是 | 下单购买（核心购买闭环入口） |
| GET | `/api/admin/products` | 是（管理员，`product:view`） | 管理端商品列表 |
| GET | `/api/admin/products/:id` | 是（管理员，`product:view`） | 管理端商品详情 |
| POST | `/api/admin/products` | 是（管理员） | 创建商品 |
| PATCH | `/api/admin/products/:id` | 是（管理员） | 更新商品 |
| PATCH | `/api/admin/products/:id/status` | 是（管理员） | 上架/下架商品 |
| GET | `/api/admin/products/:id/plans` | 是（管理员） | 套餐列表 |
| POST | `/api/admin/products/:id/plans` | 是（管理员） | 创建套餐 |
| PATCH | `/api/admin/products/:id/plans/:plan_id` | 是（管理员） | 更新套餐 |
| PATCH | `/api/admin/products/:id/access` | 是（管理员） | 配置商品的角色/会员访问规则 |
| PATCH | `/api/admin/products/:id/prices` | 是（管理员） | 配置商品定价（默认价/角色价/会员价） |

**重点测试点**：
- 用户端浏览商品 → 下单购买全流程（含余额不足、未实名、无购买权限等拦截场景）
- 价格优先级：会员价 > 角色价 > 默认价
- 商品上下架后，用户端可见性/可购买性是否正确联动

**已知问题**：
- 🔴 **P1-#5（核心问题，请重点验证是否已修复并部署生效）**：
  此前 `GET /api/admin/products`、`GET /api/admin/products/:id` 因权限码 `product:view`
  未在数据库中播种，导致**包括 admin 在内的任何账号访问都返回 403**。
  截至 2026-06-08，此问题已修复并部署到测试服务器（migration `000013` 已执行，
  `product:view` 权限码已 seed 并绑定 admin 角色），**请用具备 admin 权限的账号实测确认
  现在能正常返回 200 和商品列表数据**
- 🔵 P3-#11：`GET /api/admin/product-handlers`（商品处理器列表）未实现

### 2.2 order 模块（订单管理）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/orders` | 是 | 我的订单列表 |
| GET | `/api/orders/:id` | 是 | 订单详情 |
| POST | `/api/orders/:id/pay` | 是 | 发起支付 |
| POST | `/api/orders/:id/cancel` | 是 | 取消订单 |
| GET | `/api/admin/orders` | 是（管理员，`order:list`） | 管理端订单列表 |
| GET | `/api/admin/orders/:id` | 是（管理员，`order:list`） | 管理端订单详情 |

**重点测试点**：
- 订单状态机流转：待支付 → 已支付 → 已完成 / 已取消 / 已过期
- 取消已支付订单、重复取消、操作他人订单等边界场景应被正确拦截

**已知问题**：
- 🔴 **P1-#6（核心问题，请重点验证是否已修复并部署生效）**：
  此前 `GET /api/admin/orders`、`GET /api/admin/orders/:id` 因权限码 `order:list`
  未播种，**包括 admin 在内的任何账号访问都返回 403**。
  截至 2026-06-08 已修复并部署（migration `000013` 已执行，`order:list` 已 seed 并绑定
  admin 角色），**请用具备 admin 权限的账号实测确认现在能正常返回 200 和订单列表数据**

### 2.3 billing 模块（钱包/充值/支付回调）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/wallet` | 是 | 查询我的钱包余额（首次访问会自动创建钱包） |
| GET | `/api/wallet/transactions` | 是 | 钱包流水列表 |
| POST | `/api/recharge/orders` | 是 | 创建充值订单（body: `{"amount":"100.00","payment_method":"alipay"\|"wechat"}`） |
| POST | `/api/payments/notify/:provider` | 否（第三方回调，需签名校验） | 支付回调通知 |
| GET | `/api/admin/wallet-transactions` | 是（管理员） | 管理端查询全平台钱包流水 |
| GET | `/api/admin/payment-callbacks` | 是（管理员） | 管理端查询支付回调记录 |
| GET | `/api/admin/users/:id/wallet` | 是（管理员） | 查询指定用户钱包详情 |
| PATCH | `/api/admin/users/:id/wallet/freeze` | 是（管理员） | 冻结/解冻用户钱包余额 |

**重点测试点**：
- 充值 → 模拟支付回调 → 余额到账 → 购买扣费 → 流水记录，全链路验证
- 伪造/篡改支付回调签名应被正确拒绝（`400`），不能影响真实余额
- 跨用户访问他人钱包应返回 `403`

**已知问题**：
- 🔴 **P1-#7（请重点验证是否已修复并部署生效）**：
  此前 `POST /api/recharge/orders` 不校验 `payment_method` 枚举值，传入任意非法值
  （如 `"bitcoin"`）仍返回 `201` 并创建订单。截至 2026-06-08 已修复并部署：
  - 字段名已从 `provider` 改为 `payment_method`（**请注意使用新字段名传参**）
  - 现在传入 `alipay`/`wechat` 应返回 `201`；传入非法值/缺省/空字符串应返回 `400`
    并提示"不支持的支付方式: xxx，仅支持 wechat / alipay"

  **请实测以下用例**：
  ```
  {"amount":"100.00","payment_method":"alipay"}   → 期望 201
  {"amount":"100.00","payment_method":"wechat"}   → 期望 201
  {"amount":"100.00","payment_method":"bitcoin"}  → 期望 400
  {"amount":"100.00"}                              → 期望 400（缺省）
  {"amount":"100.00","payment_method":""}         → 期望 400（空字符串）
  ```

### 2.4 finance_consumer 模块（按量消费计费）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| POST | `/api/internal/product-usage-events` | 否（仅限内部 IP 白名单） | 内部上报使用事件（计费触发入口） |

**已知问题（这部分功能缺口较大，测试时只需确认现状，无需深入）**：
- 🟡 P2-#8：以下三个文档声明的接口**均未实现**（请求返回 `404`）：
  - `GET /api/product-consumption-records`（用户端查自己的消费记录）
  - `GET /api/admin/product-billing-rules`（管理端计费规则 CRUD）
  - `GET /api/admin/product-consumption-records`（管理端消费记录查询）
- 内部接口 `POST /api/internal/product-usage-events` 默认仅放行 `127.0.0.1`，
  生产环境需通过 `INTERNAL_ALLOWED_IPS` 环境变量显式配置允许调用的内部服务 IP

---

## 三、后端工程师丙（asset / membership / app / provision / content）

### 3.1 asset 模块（用户资产/权益）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/my/assets` | 是 | 我的资产列表（已购买的应用/服务实例） |
| GET | `/api/my/assets/:id` | 是 | 资产详情 |
| GET | `/api/my/entitlements` | 是 | 我的权益列表 |
| GET | `/api/admin/assets` | 是（管理员） | 管理端资产列表 |
| GET | `/api/admin/users/:id/assets` | 是（管理员） | 查询指定用户的资产 |
| PATCH | `/api/admin/assets/:id` | 是（管理员） | 调整资产状态/有效期 |

**重点测试点**：
- 购买后资产是否正确生成、到期后状态是否正确流转
- 跨用户访问他人资产应返回 `403`

**已知问题**：
- 🔵 P3-#9：`GET /api/admin/asset-events`（资产事件审计）、
  `GET /api/admin/users/:id/entitlements`（管理端查用户权益）**均未实现**

### 3.2 membership 模块（会员体系）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/memberships` | 否 | 会员等级列表（展示用） |
| GET | `/api/my/membership` | 是 | 我的会员状态 |
| POST | `/api/memberships/:id/purchase` | 是 | 购买/开通会员 |
| GET | `/api/admin/membership-levels` | 是（管理员） | 管理端会员等级列表 |
| POST | `/api/admin/membership-levels` | 是（管理员） | 创建会员等级 |
| PATCH | `/api/admin/membership-levels/:id` | 是（管理员） | 更新会员等级 |
| GET | `/api/admin/membership-benefits` | 是（管理员） | 会员权益列表 |
| POST | `/api/admin/membership-benefits` | 是（管理员） | 创建会员权益 |
| PATCH | `/api/admin/membership-benefits/:id` | 是（管理员） | 更新会员权益 |
| GET | `/api/admin/user-memberships` | 是（管理员） | 查询用户会员开通记录 |

**重点测试点**：
- 会员等级与会员价的联动（已在 Stage1 验收中验证过核心链路）

**已知问题**：
- 🟡 P2-#15：`POST /api/memberships/:id/purchase` 实测返回 `404`，疑似路由未注册，
  请确认该接口当前是否可用，若仍不可用请反馈给后端丙
- 🔵 P3-#16：会员等级/权益创建接口的请求体字段名与文档不一致：
  - 文档写 `code`/`membership_level_id`/`benefit_config_json`
  - 实现实际是 `level_code`/`level_id`/`benefit_value`
  - **测试时请按实现的实际字段名传参**（否则会命中"缺少必填字段"的 400 校验），
    并将差异反馈给后端丙核对
- 🔵 P3-#9：`GET/POST/PATCH /api/admin/product-membership-rules`（商品会员规则配置）未实现

### 3.3 app 模块（应用市场/适配器）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/marketplace/apps/:id` | 是 | 用户查应用业务详情（图标/描述等） |
| GET | `/api/admin/apps` | 是（管理员） | 管理端应用列表 |
| GET | `/api/admin/apps/:id` | 是（管理员） | 管理端应用详情 |
| POST | `/api/admin/apps` | 是（管理员） | 创建应用 |
| PATCH | `/api/admin/apps/:id` | 是（管理员） | 更新应用/上下架 |
| GET | `/api/admin/app-adapters` | 是（管理员） | 适配器列表（注意：文档写的是 `application-adapters`，实现是 `app-adapters`） |
| POST | `/api/admin/app-adapters` | 是（管理员） | 注册适配器 |
| PATCH | `/api/admin/app-adapters/:id` | 是（管理员） | 更新/启停适配器 |

**已知问题**：
- 🟡 P2-#10：文档声明用户端应有 `GET /api/apps`、`GET /api/apps/:id`、
  `POST /api/apps/:id/purchase`、`GET /api/my/apps`，但实现中**只有**
  `GET /api/marketplace/apps/:id` 一个用户端接口，路径与功能均严重不符。
  测试时请确认：用户查看/购买应用，目前实际走的是哪条路径
  （应用购买可能是通过统一的 `product` 模块 `POST /api/products/:id/purchase` 完成，
  而非 app 模块自己的接口）
- 🔵 P3-#12：`PATCH /api/admin/apps/:id/access`、`PATCH /api/admin/apps/:id/prices` 未实现
- 🔵 P3-#14：`app-adapters` 路径命名与文档（`application-adapters`）不一致，功能本身正常

### 3.4 content 模块（公告/帮助文档）

| 方法 | 路径 | 是否需要登录 | 说明 |
|---|---|---|---|
| GET | `/api/announcements` | 否 | 用户端公告列表（按可见性范围过滤） |
| GET | `/api/help/categories` | 否 | 帮助文档分类列表 |
| GET | `/api/help/articles` | 否 | 帮助文档列表 |
| GET | `/api/help/articles/:id` | 否 | 帮助文档详情 |
| GET | `/api/admin/announcements` | 是（管理员） | 管理端公告列表 |
| POST | `/api/admin/announcements` | 是（管理员） | 创建公告 |
| PATCH | `/api/admin/announcements/:id` | 是（管理员） | 更新/发布/下线公告 |
| GET | `/api/admin/help/categories` | 是（管理员） | 帮助分类管理 |
| POST | `/api/admin/help/categories` | 是（管理员） | 创建分类 |
| PATCH | `/api/admin/help/categories/:id` | 是（管理员） | 更新分类 |
| GET | `/api/admin/help/articles` | 是（管理员） | 帮助文档管理 |
| POST | `/api/admin/help/articles` | 是（管理员） | 创建文档 |
| PATCH | `/api/admin/help/articles/:id` | 是（管理员） | 更新/发布/下线文档 |

**重点测试点**：
- 管理端创建/发布后，用户端是否能正确看到（且下线后用户端不可见）
- 不存在的文档 ID 应返回 `404`

**已知问题**：暂无

### 3.5 provision 模块

无对外 HTTP 接口（仅供 `product` 模块内部调用应用开通逻辑），**无需测试**。

---

## 四、测试建议与反馈方式

1. **优先级建议**：先测三处标 🔴 的 P1 已修复项（product/order 管理端列表、充值方式校验），
   这是本轮最关心的"是否真的修复生效"；再按模块顺序测其余接口
2. **遇到问题如何反馈**：发现任何与本清单描述不符的现象（无论是已知问题"还没修复"，
   还是新发现的问题），请记录：
   - 请求方法 + 路径 + 请求体
   - 实际返回的状态码 + 响应内容
   - 期望的结果是什么
   并整理反馈给测试工程师登记，统一按 P0~P3 分级跟踪处理
3. **已知问题列表仅供参考**：标注的 P1/P2/P3 是测试工程师此前地毯式测试的结论快照
   （截至 2026-06-08），实际状态可能已有变化（尤其 P1 已修复部署），测试时请以
   **实际请求结果**为准，不要直接假设清单中的描述仍然成立
