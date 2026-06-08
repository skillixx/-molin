# 全量接口地毯式覆盖测试报告（2026-06-08）

## 测试目标

不局限于 Stage1 端到端核心购买闭环（37 用例）已覆盖范围，对后端甲/乙/丙三位工程师当前
**已实现的全部 API 接口**做一次地毯式测试，重点核对：

1. `docs/full-api-design.md` 文档声明的接口与各模块 `route.go` 实际实现是否一致；
2. Stage1 未触及的边角接口（实名认证详情、审计日志、内容公告/帮助文档、应用适配器、
   会员、资产权益、消费计费等）正常路径与异常路径是否正确；
3. 越权访问 / 参数校验 / 资源不存在 等通用边界场景。

测试环境：测试服务器 `8.130.9.163:8080`（API），MySQL `8.130.9.163:13306`。
测试账号方案：自包含账号 `qa_fullapi_admin_*@molin.io` / `qa_fullapi_user_*@molin.io`，
固定密码 `Test1234!`，管理员权限通过 `INSERT IGNORE INTO user_roles` 绑定系统已有 `admin` 角色获得，
未触碰任何已存在的管理员/测试账号，未新增任何权限/角色记录到共享 RBAC 数据。

测试脚本：`tests/test_full_api_coverage.py`（在测试服务器上执行，107→111 项断言，105 通过 / 6 失败）。

---

## 一、接口清单与覆盖范围（按模块分类）

标注：**[S1]** = Stage1 已覆盖；**[新]** = 本次新增覆盖；**[缺]** = 文档声明但未实现/路径不一致。

### 1. 后端甲：auth（认证）

| 接口 | 覆盖 |
|---|---|
| POST /api/auth/verification-codes/email、/phone | [S1] |
| POST /api/auth/register、/register/email、/register/phone | [S1] |
| POST /api/auth/login/email、/login/phone | [S1] |
| POST /api/auth/logout、/refresh、/password/reset | [S1] |
| GET /api/me；PATCH /api/me/profile、/password、/username、/phone、/email | [S1][新]（username/phone/email 边角路径本次新增覆盖伪造 JWT/越权场景） |
| POST /api/admin/auth/verify-phone、/verify-email | [S1] |
| PATCH /api/admin/users/:id/status | [S1] |

### 2. 后端甲：iam（角色权限）

| 接口 | 覆盖 |
|---|---|
| GET/POST /api/admin/roles，PUT/DELETE /api/admin/roles/:id | [S1][新]（本次新增：重复 code、删除已删除角色、详情查询） |
| GET /api/admin/roles/:id | **[缺]** 文档 §3.10 声明但未实现，见问题 #3 |
| PATCH /api/admin/roles/:id/permissions | **[缺]** 文档 §3.12 声明但未实现，见问题 #4 |
| GET /api/admin/permissions | [S1] |
| GET/POST/DELETE /api/admin/users/:id/roles[/:role_id] | [S1] |
| GET/POST/DELETE /api/admin/users/:id/permission-overrides[/:id] | [S1] |

### 3. 后端甲：identity（实名认证）

| 接口 | 覆盖 |
|---|---|
| POST /api/identity/verifications | [S1][新]（重复提交场景） |
| GET /api/identity/verifications/me（文档命名为 /latest，见问题 #14） | [新] |
| GET /api/admin/identity-verifications[/:id] | [新] |
| PATCH /api/admin/identity-verifications/:id/review | [新]（含非法 action、缺 reject_reason、重复审核场景） |

### 4. 后端甲：audit（审计日志）

| 接口 | 覆盖 |
|---|---|
| GET /api/admin/audit-logs | **[缺]** 文档 §3.16 声明、`audit_logs` 表存在数据，但路由未实现，见问题 #1 |

### 5. 后端乙：product（商品）

| 接口 | 覆盖 |
|---|---|
| GET /api/products[/:id][/:id/plans] | [S1][新]（不存在商品 404 场景） |
| POST /api/products/:id/purchase | [S1] |
| GET/POST /api/admin/products，GET/PATCH /api/admin/products/:id[/status] | [新]（**发现 P1 阻断缺陷**，见问题 #5） |
| GET/POST/PATCH /api/admin/products/:id/plans[/:plan_id] | [新] |
| PATCH /api/admin/products/:id/access、/prices | [新] |
| GET /api/admin/product-handlers | **[缺]** 文档 §4.12 声明但未实现，见问题 #11 |

### 6. 后端乙：order（订单）

| 接口 | 覆盖 |
|---|---|
| GET /api/orders[/:id]，POST /api/orders/:id/pay、/cancel | [S1][新]（不存在订单 404、取消不存在订单场景） |
| GET /api/admin/orders[/:id] | [新]（**发现 P1 阻断缺陷**，见问题 #6） |

### 7. 后端乙：billing（钱包/充值/支付回调）

| 接口 | 覆盖 |
|---|---|
| GET /api/wallet、/wallet/transactions | [S1][新] |
| POST /api/recharge/orders | [S1][新]（**发现支付方式枚举校验缺失**，见问题 #7） |
| POST /api/payments/notify/:provider | [S1][新]（签名错误回调、未知渠道回调场景） |
| GET /api/admin/wallet-transactions、/payment-callbacks | [新] |
| GET /api/admin/users/:id/wallet，PATCH .../wallet/freeze | [新]（含 0 元/负数冻结边界） |

### 8. 后端乙：finance_consumer（消费计费）

| 接口 | 覆盖 |
|---|---|
| POST /api/internal/product-usage-events | [新]（IP 白名单逻辑核对，见说明） |
| GET /api/product-consumption-records | **[缺]** 文档 §4.21 声明但未实现，见问题 #8 |
| GET/POST/PATCH /api/admin/product-billing-rules[/:id] | **[缺]** 文档 §4.21 声明但未实现，见问题 #8 |
| GET /api/admin/product-consumption-records | **[缺]** 文档 §4.21 声明但未实现，见问题 #8 |

### 9. 后端丙：asset（资产/权益）

| 接口 | 覆盖 |
|---|---|
| GET /api/my/assets[/:id]、/my/entitlements | [S1][新]（不存在资产 404 场景） |
| GET /api/admin/assets、/admin/users/:id/assets | [新] |
| PATCH /api/admin/assets/:id | [新] |
| GET /api/admin/asset-events | **[缺]** 文档 §5.1 声明但未实现，见问题 #9 |
| GET /api/admin/users/:id/entitlements | **[缺]** 文档 §5.1 声明但未实现，见问题 #9 |

### 10. 后端丙：membership（会员）

| 接口 | 覆盖 |
|---|---|
| GET /api/memberships、/my/membership | [S1][新] |
| POST /api/memberships/:id/purchase | **[缺]** 文档 §5.2 声明，实测返回 404（路由疑似未注册），见问题 #15 |
| GET/POST/PATCH /api/admin/membership-levels[/:id] | [新] |
| GET/POST/PATCH /api/admin/membership-benefits[/:id] | [新] |
| GET /api/admin/user-memberships | [新] |
| GET/POST/PATCH /api/admin/product-membership-rules[/:id] | **[缺]** 文档 §5.2 声明但未实现，见问题 #9 |

> 注：文档 §5.2 字段命名（`code`/`membership_level_id`/`benefit_config_json`）与实现 DTO
> （`level_code`/`level_id`/`benefit_value`）不一致，详见问题 #16。

### 11. 后端丙：app（应用市场/适配器）

| 接口 | 覆盖 |
|---|---|
| GET /api/apps[/:id]、POST /api/apps/:id/purchase、GET /api/my/apps | **[缺]** 文档 §5.3 声明，实现仅有 `GET /api/marketplace/apps/:id`，见问题 #10 |
| GET /api/marketplace/apps/:id | [新] |
| GET/POST/PATCH /api/admin/apps[/:id] | [新] |
| PATCH /api/admin/apps/:id/access、/prices | **[缺]** 文档 §5.3 声明但未实现，见问题 #12 |
| GET/POST/PATCH /api/admin/application-adapters[/:id]（实现路径为 app-adapters） | [新]（命名不一致，已记录于问题 #14） |

### 12. 后端丙：content（公告/帮助文档）

| 接口 | 覆盖 |
|---|---|
| GET /api/announcements（用户端，含可见性范围过滤） | [新] |
| GET /api/help/categories、/help/articles[/:id]（公开） | [新]（含下线文章过滤、不存在文章 404 场景） |
| GET/POST/PATCH /api/admin/announcements[/:id] | [新] |
| GET/POST/PATCH /api/admin/help/categories[/:id]、/help/articles[/:id] | [新]（含发布后公开可见的端到端验证） |

### 13. provision（开通路由）

provision 模块**无对外 HTTP 路由**（route.go 中 `RegisterRoutes` 为空实现，仅供 product 模块内部调用），
符合设计预期，无需单独接口测试。

### 14. 通用安全场景（补充于 Stage1 范围之外）

| 场景 | 结果 |
|---|---|
| 伪造 JWT（篡改 payload，签名不更新）访问受保护接口 | 通过，返回 401 |
| 完全无效 Token | 通过，返回 401 |
| 缺少 Bearer 前缀的 Authorization 头 | 通过，返回 401 |
| 普通用户访问各模块管理端接口（iam/identity/product/order/billing/asset/membership/app/content 共 13 处） | 全部正确返回 403 |

---

## 二、测试结果统计

- 本次执行断言总数：**111**
- 通过：**105**（94.6%）
- 失败：**6**（对应 6 个不同根因的缺陷，详见下文问题清单）
- 另有 10 项以"接口未实现/文档不一致"形式记录（未计入失败断言，但已作为缺陷登记）

合计本次发现/登记问题 **16 项**：P0 = 0，P1 = 3，P2 = 7，P3 = 6。

---

## 三、发现的问题（按优先级排序）

### 🔴 P1（核心功能不可用，3 项 — 需要尽快处理）

#### 问题 #5【product 模块】管理员（admin 角色）无法访问 `GET /api/admin/products`

**复现步骤：**
1. 注册全新账号并通过 SQL 绑定系统已有 `admin` 角色
2. 使用该账号登录，携带 Token 访问 `GET /api/admin/products`

**期望结果**：返回 200，商品列表数据

**实际结果**：返回 `HTTP 403 {"code":40003,"message":"无操作权限"}`

**根因**：`server/internal/modules/product/route.go` 中
`GET /api/admin/products`、`GET /api/admin/products/:id` 要求权限码 `product:view`，
但数据库 `permissions` 表中**不存在 `product:view` 这条权限记录**（实际存在的是
`product:create`、`product:edit`），因此该权限码无法被分配给任何角色——**包括系统内置的
`admin` 超级管理员角色**。结果是管理后台商品列表/详情功能对所有人不可访问。

**影响范围**：管理后台「商品管理」整个列表/详情页面不可用，属于功能阻断级问题。

**建议**：后端乙在权限迁移/seed 数据中补充 `product:view` 权限并赋予 `admin` 角色；
或者直接复用已存在的 `product:create`/`product:edit` 权限码，避免引入"声明了但从未播种"的权限码。

---

#### 问题 #6【order 模块】管理员（admin 角色）无法访问 `GET /api/admin/orders`

**复现步骤**：同上，使用绑定 `admin` 角色的账号访问 `GET /api/admin/orders`

**期望结果**：返回 200，订单列表数据

**实际结果**：返回 `HTTP 403 {"code":40003,"message":"无操作权限"}`

**根因**：`server/internal/modules/order/route.go` 中 `GET /api/admin/orders`、
`GET /api/admin/orders/:id` 要求权限码 `order:list`，但数据库 `permissions` 表中
**不存在 `order:list` 这条权限记录**，与问题 #5 同根因——权限码声明在路由中却从未播种到
`permissions` 表，导致无法分配给 `admin` 角色。

**影响范围**：管理后台「订单管理」整个列表/详情功能对所有人不可访问，属于功能阻断级问题。

**建议**：后端乙补充 `order:list` 权限并赋予 `admin` 角色，并 review 是否还有其他模块存在
"路由声明权限码、但 seed 数据未播种"的同类问题（建议做一次全量交叉核对：
`grep RequirePerm` 输出的全部权限码 vs `permissions` 表实际记录）。

---

#### 问题 #7【billing 模块】创建充值订单未校验 `payment_method` 枚举值

**复现步骤**：
1. 普通用户登录
2. `POST /api/recharge/orders` 携带 `{"amount": "10.00", "payment_method": "bitcoin"}`

**期望结果**：返回 `HTTP 400`，因为接口文档（§4.18）声明 `payment_method` 仅支持 `wechat`/`alipay`

**实际结果**：返回 `HTTP 201`，正常创建充值订单：
```json
{"code":0,"message":"ok","data":{"order_id":70,"pay_url":"/api/simulate-pay?order_no=ORD20260608YP6UZ7JB&amount=10"}}
```

**根因**：`billing/handler/billing_handler.go` 的 `CreateRechargeOrder` 只校验了
`amount > 0`，未对 `payment_method` 做枚举校验，任意字符串都会被接受并生成订单。

**影响范围**：
- 用任意非法支付渠道创建的充值订单会进入 `pending` 状态但永远无法收到对应渠道的支付回调，
  导致僵尸订单堆积；
- 若后续支付回调路由 `/api/payments/notify/:provider` 与创建时记录的渠道存在关联校验逻辑，
  可能引发状态不一致或被恶意构造异常数据；
- 属于资金链路上的输入校验缺口，建议尽快修复。

**建议**：后端乙在 `CreateRechargeOrder` 中增加 `payment_method in (wechat, alipay)` 的枚举校验，
非法值返回 `40000`。

---

### 🟡 P2（有临时方案，但应尽快修复，7 项）

#### 问题 #1：`GET /api/admin/audit-logs` 接口未实现
文档 §3.16 声明了审计日志查询接口，数据库中 `audit_logs` 表也已存在并有数据，
但 `server/internal/modules/audit` 目录下只有 `README.md`，无 `route.go`，`bootstrap/app.go`
也未挂载该模块路由。管理后台无法查询审计日志，影响安全审计能力。**建议后端甲尽快补齐。**

#### 问题 #2：创建重复 `code` 角色返回 `HTTP 500` 而非 `400/409`
**复现**：先创建角色 `code=qa_dup_role_xxx`，再用同样的 `code` 再次创建。
**期望**：返回 `400`（参数冲突）或 `409`（数据冲突，对应错误码 `40900`）
**实际**：返回 `HTTP 500 {"code":50000,"message":"创建失败"}`
**根因**：`iam/handler/iam_handler.go` 的 `CreateRole` 把 `Service.CreateRole` 返回的任何
错误（包括数据库唯一索引冲突 `Error 1062: Duplicate entry`）一律映射为 `50000` 内部错误，
未区分业务校验错误与真正的系统异常。**建议后端甲识别 DB 唯一约束冲突并返回 `40900`。**

#### 问题 #3：`GET /api/admin/roles/:id` 接口未实现
文档 §3.10 声明了角色详情查询接口，但 `iam/route.go` 只注册了
`PUT /api/admin/roles/{id}`、`DELETE /api/admin/roles/{id}`，未注册 `GET`，
请求返回 `HTTP 405 Method Not Allowed`。管理后台无法单独查询角色详情（含权限列表）。
**建议后端甲补齐该路由。**

#### 问题 #4：`PATCH /api/admin/roles/:id/permissions` 接口未实现
文档 §3.12 声明了"配置角色权限"接口，但 `iam/route.go` 中无对应路由/Handler。
管理后台只能靠"创建角色时一次性指定"或没有专门接口为已存在角色调整权限列表。
**建议后端甲确认是否计划补齐，或更新文档说明权限配置走其它接口。**

#### 问题 #8：消费计费查询类接口（3个）均未实现
文档 §4.21 声明的以下接口均未在路由中找到对应实现：
- `GET /api/product-consumption-records`（用户端，本次实测返回 `404`）
- `GET/POST/PATCH /api/admin/product-billing-rules[/:id]`（管理端计费规则，实测返回 `404`）
- `GET /api/admin/product-consumption-records`（管理端消费记录，实测返回 `404`）

`finance_consumer/route.go` 仅注册了内部接口 `POST /api/internal/product-usage-events`，
计费规则的 CRUD 与消费记录的查询能力完全缺失，意味着**管理员无法配置按量计费规则**，
也**无法核对计费是否正确**，用户端也看不到自己的消费明细。
此项虽非阻断购买闭环，但属于按量计费业务闭环的关键缺口，**建议后端乙尽快排期补齐**。

#### 问题 #10：用户端应用市场接口路径与文档严重不一致
文档 §5.3 声明用户端应该有：
```
GET   /api/apps
GET   /api/apps/:id
POST  /api/apps/:id/purchase
GET   /api/my/apps
```
但实现（`app/route.go`）中用户端**只有一个** `GET /api/marketplace/apps/{id}`，
应用列表、应用购买、"我的应用"均未实现或路径完全不同。
**影响**：前端用户控制台若按文档对接应用市场列表/购买/我的应用，将全部失败（404）。
**建议后端丙与产品/前端确认应用市场的实际交付路径，并同步更新设计文档**，
避免前后端联调时产生大量"接口不存在"的误报。

#### 问题 #15：`POST /api/memberships/:id/purchase` 返回 404
文档 §5.2 声明了会员购买接口 `POST /api/memberships/:id/purchase`，但实测
对存在的会员等级 ID 发起购买请求返回 `HTTP 404`（空响应体），怀疑该路由未注册
（`membership/route.go` 中确未发现该路由）。**建议后端丙确认该接口的交付状态**——
若已规划在后续版本交付，应在文档中标注"待实现"，避免误导联调。

---

### 🔵 P3（体验/规范问题，6 项 — 可延后处理）

#### 问题 #9：资产/会员相关 3 个查询接口未实现
- `GET /api/admin/asset-events`（文档 §5.1，资产事件审计）
- `GET /api/admin/users/:id/entitlements`（文档 §5.1，管理端查询用户权益）
- `GET/POST/PATCH /api/admin/product-membership-rules[/:id]`（文档 §5.2，商品会员规则配置）

均为辅助查询/配置类接口，不影响核心链路，建议后端丙列入后续迭代排期。

#### 问题 #11：`GET /api/admin/product-handlers`（商品处理器列表）未实现
文档 §4.12 声明，实现路由中未发现，属辅助查询功能，优先级不高。

#### 问题 #12：`PATCH /api/admin/apps/:id/access`、`PATCH /api/admin/apps/:id/prices` 未实现
文档 §5.3 声明了应用访问规则与价格配置接口，实现路由中未发现对应 Handler。

#### 问题 #13：`DELETE /api/admin/roles/:id` 对不存在的角色仍返回 `200 OK`
**复现**：先删除一个角色成功（200），再用同一 ID 重复调用 DELETE
**期望**：返回 404（资源不存在）
**实际**：仍返回 `200 {"code":0,"message":"ok"}`
**根因**：`DeleteRole` 直接调用 GORM 的 `Delete`，未检查 `RowsAffected`，对不存在的记录
GORM 不会报错，因此始终返回成功。属于幂等性设计选择 vs 资源存在性校验的权衡问题，
不影响安全性，**建议低优先级修复以符合 RESTful 语义**。

#### 问题 #14：多处接口命名与文档不一致（命名漂移，建议统一/更新文档）
| 文档命名 | 实现命名 | 模块 |
|---|---|---|
| `GET /api/identity/verifications/latest` | `GET /api/identity/verifications/me` | identity |
| `GET/POST/PATCH /api/admin/application-adapters[/:id]` | `GET/POST/PATCH /api/admin/app-adapters[/:id]` | app |

两组接口实现均存在且工作正常，仅路径命名与文档不一致，建议后端甲/丙与文档维护者
对齐后统一更新 `docs/full-api-design.md`，避免前端联调时按文档路径请求出现 404。

#### 问题 #16：会员模块 Body 字段命名与文档不一致
文档 §5.2 声明会员等级/权益创建接口字段为：
- 等级：`code`、`name`、`level_order`、`status`
- 权益：`membership_level_id`、`benefit_type`、`target_product_id`、
  `target_product_type`、`benefit_config_json`、`status`

实现 DTO（`membership/dto/membership_dto.go`）实际字段为：
- 等级：`level_code`、`name`、`description`、`sort_order`、`status`
- 权益：`level_id`、`benefit_type`、`benefit_value`（JSON 字符串）、`status`
  （未见 `target_product_id`/`target_product_type` 字段）

**影响**：前端管理后台若按文档传参将全部命中"缺少必填字段"的 400 校验，
**建议后端丙更新文档以匹配实现，或反向调整实现以符合已定稿的设计文档**（取决于哪个是权威版本）。

---

## 四、未发现 P0 级问题

本轮测试**未发现 P0（阻断级）缺陷**。重点验证的资金安全相关场景均表现正常：

- 支付回调签名错误时正确返回 `400`，余额未被篡改增加；
- 普通用户访问他人钱包详情、用户资产管理接口均正确返回 `403`；
- 伪造 JWT（篡改 payload 但不更新签名）正确返回 `401`；
- 内部计费事件接口 `POST /api/internal/product-usage-events` 的 IP 白名单逻辑核实：
  当 `INTERNAL_ALLOWED_IPS` 未显式配置时，`finance_consumer/handler.isAllowedIP` 默认仅放行
  `127.0.0.1`/`::1`（本机），外部来源会被正确拒绝并返回 `403`，符合"内部接口默认拒绝外部访问"
  的安全预期。**强烈建议生产环境务必显式配置 `INTERNAL_ALLOWED_IPS`，否则部署在容器/反代后面时
  默认仅放行本机的策略可能导致内部服务间调用也被拒绝，需要与运维确认实际网络拓扑下的白名单取值。**

---

## 五、建议

1. **立即处理（P1，影响管理后台基本可用性）**：
   - 问题 #5、#6 ——为 `admin` 角色补齐 `product:view`、`order:list` 权限播种数据
     （或修改路由复用已有权限码），否则管理后台商品/订单管理功能对任何人都不可用；
   - 问题 #7 ——为充值订单创建接口补充 `payment_method` 枚举校验。

2. **本周内处理（P2）**：补齐审计日志查询接口（#1）、修复重复角色创建的 500 错误（#2）、
   补齐角色详情接口（#3）、明确会员购买/应用市场接口的交付状态并对齐文档（#10、#15）。

3. **排期处理（P3）**：消费计费查询接口、资产事件/权益查询、应用访问规则与价格配置等
   辅助查询/配置接口；统一接口命名和字段命名与文档的一致性（#14、#16）。

4. **流程建议**：建议团队建立"路由声明的权限码 ↔ permissions 表播种数据"的自动化交叉校验
   （例如 CI 中跑一个脚本 `grep RequirePerm` 提取全部权限码，与 seed SQL/迁移文件中的
   `INSERT INTO permissions` 做 diff），从根本上避免 #5、#6 这类"代码可编译运行、
   但功能因缺失种子数据而不可用"的问题再次出现。

## 测试结论：**部分通过**

核心链路（认证、购买、支付回调幂等、越权防护）依旧稳定，但本次"地毯式"测试发现
**3 个 P1 级别的功能性缺陷**（管理后台商品/订单列表因权限码缺失而完全不可访问、
充值支付方式校验缺失），需要相关后端工程师尽快修复并复测后方可视为完整可上线状态。
是否影响 Stage1 已验收的核心购买闭环：**否**（购买闭环本身依赖的权限码与本次发现的
`product:view`/`order:list` 不同，闭环测试链路未受影响）。
