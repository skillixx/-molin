# 前端开发规范与任务规划（基于后端乙接口）

> **版本**：v1.4 规划稿，2026-06-16（v1.1 契约勘误；v1.2 补齐 F2/管理端签名+分页上限/冻结/404 边界；v1.3 商品状态仅 active⇄inactive、user_price 未配置统一 -1、Product 类型补全；v1.4 order_type 值订正为 product、O3 仅 product 订单、SSOT frontend-api-reference 同步 #144）
> **作者**：架构（senior-architect 方法论）
> **范围**：仅覆盖**后端工程师乙**负责的四个模块对接 —— `product`（商品/套餐/价格/访问规则/计费规则）、`order`（订单状态机/支付/取消）、`billing`（钱包/流水/充值/回调）、`finance_consumer`（消费记录）。
> **对接基线**：main `4779eb2`（2026-06-16，全量回归 88/88 PASS）。
> **状态**：**本文件为规划与归属划分**，不含落地页面代码（页面由前端团队实现）；编码前需经产品经理确认任务边界与里程碑。
> **配套文档**：接口字段细节以 `docs/frontend-api-reference.md`（第五～八章）为唯一事实来源（SSOT）；与文档冲突时以 `server/internal/modules/{product,order,billing,finance_consumer}/route.go` 现行代码为准。本文不重复字段定义，只做**归属、分层、拆解、流程与风险**约定。
> **姊妹文档**：`docs/frontend-dev-plan-backend-a.md`（后端甲对接规划）。两份规划共用同一套分层与协作约定。

---

## 0. 阅读对象与协作边界

| 角色 | 仓库目录 | 本规划中的职责 |
|---|---|---|
| 前端工程师甲 | `web/admin-console/`（墨灵管理后台） | 对接后端乙**全部 `/api/admin/*` 管理接口** —— 商品/套餐/价格/访问规则/计费规则 CRUD、订单管理、钱包管理（含冻结/解冻、回调记录）、全量消费记录 |
| 前端工程师乙 | `web/user-console/`（墨灵用户控制台） | 对接后端乙**面向终端用户的接口** —— 商品市场与详情、购买（钱包扣费）、我的订单（支付/取消）、钱包（余额/流水/充值）、我的消费记录 |

> **划分原则（与后端甲规划一致）：按调用者身份切分，而非按后端模块切分。** 同一个 `product` 模块里，用户态市场接口归乙、管理态 CRUD 归甲；`billing` 模块里用户钱包归乙、管理端钱包/回调归甲。这样每个前端工程师对接的接口在**鉴权链路、错误码语义、UI 形态**上是同质的（用户态走登录 + 业务错误引导；管理态走登录 + 权限码 + 数据范围），降低跨页面认知负担，也与既有 `frontend-task-{admin,user}-console.md` 任务单的 Section 6 一一对应。

> **不对接的接口（无前端动作）**：
> - `POST /api/payments/notify/{provider}` —— 第三方支付回调，无登录、需验签，由支付渠道服务端回调，前端不调用。
> - `POST /api/internal/product-usage-events` —— 内部消费上报，IP 白名单 + `X-Internal-Token` 共享密钥，由后端业务模块（token 网关/资源服务等）调用，前端不可达。

---

## 1. 架构分层规范（沿用两端统一四层）

与 `frontend-dev-plan-backend-a.md` §1 完全一致，不再重复。要点：**页面只依赖 `api/*.ts`，禁止组件内直接 `import axios`**；类型层字段保持 snake_case 不转驼峰；破坏性操作（取消订单、冻结钱包、删除规则、商品下架）必须 `ElMessageBox.confirm` 二次确认。

### 1.1 ⚠️ 后端乙分页结构已统一为 D-95 扁平（重要更正）

**后端乙的全部列表接口已于 Round 7 完成 D-95 扁平化**，返回 `data` 顶层即 `{ items, page, page_size, total }`，**无** `pagination` 子对象、**无** `list` 字段。甲、乙两端可**复用同一个** `PageResult<T>`：

```typescript
export interface PageResult<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}
```

> 历史提示：`frontend-dev-plan-backend-a.md` 早期稿曾注明「乙模块仍嵌套 pagination」，**该说法已过期**，以本节为准。两端不再需要为乙模块写差异化的分页解析。

**分页入参契约**（`server/pkg/pagination/pagination.go`）：query 参数为 `page` / `page_size`；`page` 缺省/<1 取 1；`page_size` 缺省/<1 取 **20**，**上限 100（超过静默截断为 100）**。前端按惯例固定 `page_size=20` 即可，如需大页长不要超过 100。

### 1.2 后端乙专属约定（区别于后端甲）

| 约定 | 说明 |
|---|---|
| 金额一律字符串 | `amount`/`balance_amount`/`price_amount` 等用 `string`（后端 `decimal.Decimal` 序列化为字符串），前端**禁止** `parseFloat` 后做加减展示，避免浮点精度丢失；如需计算用 decimal 库或后端返回值 |
| 幂等键前端生成 | 购买 `POST /api/products/{id}/purchase` 与支付 `POST /api/orders/{id}/pay` 必须由前端生成 `Idempotency-Key`（UUID v4）放请求头；同一业务动作重试须复用同一 key |
| 批量写入统一 `items` 键 | 价格/访问规则覆盖写 body 顶层键为 `items`（缺失返回 400），见 §3 D-009/D-011 |
| 业务错误引导（用户端） | `60001` 余额不足 → 引导充值；`70001` 未实名 → 引导实名；`40003` 无购买权限 → 提示无权限。统一在 `http.ts` 之上由购买/支付流程兜底处理 |
| 安全红线（管理端） | 支付回调记录列表**不返回也不渲染** `notify_body`（明文/密文均不回传，B-04）；前端不得新增该字段展示 |

---

## 2. 接口归属矩阵（后端乙全部接口）

> 来源：`server/internal/modules/{product,order,billing,finance_consumer}/route.go`。编号沿用既有任务单口径（P=商品 / O=订单 / B=钱包计费 / F=消费）。✅=该端负责，—=不涉及。

### 2.1 商品 product

| 编号 | 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|---|:--:|:--:|
| P1 | `GET /api/products` | 登录（按角色 can_view 过滤） | — | ✅ |
| P2 | `GET /api/products/{id}` | 登录（含套餐 + 用户实际价格） | — | ✅ |
| P3 | `GET /api/products/{id}/plans` | 登录 | — | ✅ |
| P4 | `POST /api/products/{id}/purchase` | 登录 + 实名 + can_buy（需 Idempotency-Key） | — | ✅ |
| P5 | `GET /api/admin/products` | `product:view` | ✅ | — |
| P6 | `POST /api/admin/products` | `product:create` | ✅ | — |
| P7 | `GET /api/admin/products/{id}` | `product:view` | ✅ | — |
| P8 | `PATCH /api/admin/products/{id}` | `product:edit` | ✅ | — |
| P9 | `PATCH /api/admin/products/{id}/status` | `product:edit`（上下架） | ✅ | — |
| P10 | `GET /api/admin/products/{id}/plans` | `product:view` | ✅ | — |
| P11 | `POST /api/admin/products/{id}/plans` | `product:create` | ✅ | — |
| P12 | `PATCH /api/admin/products/{id}/plans/{plan_id}` | `product:edit` | ✅ | — |
| P13 | `PATCH /api/admin/products/{id}/access` | `product:edit`（D-011 `items` 必填） | ✅ | — |
| P14 | `PATCH /api/admin/products/{id}/prices` | `product:edit`（D-009 每项含 product_plan_id） | ✅ | — |
| P15 | `GET /api/admin/product-billing-rules` | `product:view` | ✅ | — |
| P16 | `POST /api/admin/product-billing-rules` | `product:create` | ✅ | — |
| P17 | `PATCH /api/admin/product-billing-rules/{id}` | `product:edit` | ✅ | — |

### 2.2 订单 order

| 编号 | 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|---|:--:|:--:|
| O1 | `GET /api/orders` | 登录（仅本人） | — | ✅ |
| O2 | `GET /api/orders/{id}` | 登录（仅本人） | — | ✅ |
| O3 | `POST /api/orders/{id}/pay` | 登录（钱包支付 pending 订单，需 Idempotency-Key） | — | ✅ |
| O4 | `POST /api/orders/{id}/cancel` | 登录（取消 pending 订单） | — | ✅ |
| O5 | `GET /api/admin/orders` | `order:list` | ✅ | — |
| O6 | `GET /api/admin/orders/{id}` | `order:list` | ✅ | — |

### 2.3 钱包 / 支付 billing

| 编号 | 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|---|:--:|:--:|
| B1 | `GET /api/wallet` | 登录（响应 `wallet_id`，D-008） | — | ✅ |
| B2 | `GET /api/wallet/transactions` | 登录（仅本人流水） | — | ✅ |
| B3 | `POST /api/recharge/orders` | 登录（返回 `pay_url`，HTTP 201） | — | ✅ |
| — | `POST /api/payments/notify/{provider}` | 公开 + 验签 | — | —（渠道回调，无前端） |
| B5 | `GET /api/admin/users/{id}/wallet` | `wallet:view` | ✅ | — |
| B6 | `GET /api/admin/wallet-transactions` | `wallet:view` | ✅ | — |
| B7 | `PATCH /api/admin/users/{id}/wallet/freeze` | `wallet:manage`（body `{action,amount,reason}`） | ✅ | — |
| B8 | `GET /api/admin/payment-callbacks` | `wallet:view`（**不含 notify_body**） | ✅ | — |

### 2.4 消费记录 finance_consumer

| 编号 | 端点 | 鉴权 | 前端甲 | 前端乙 |
|---|---|---|:--:|:--:|
| F1 | `POST /api/internal/product-usage-events` | 内部（IP 白名单 + X-Internal-Token） | —（内部，无前端） | — |
| F2 | `GET /api/product-consumption-records` | 登录（强制本人过滤） | — | ✅ |
| F3 | `GET /api/admin/product-consumption-records` | `wallet:view`（支持 user_id 过滤） | ✅ | — |

**统计**：前端可对接端点共 **31** 个 —— 前端甲（管理端）**19** 个（P5–P17、O5–O6、B5–B8、F3），前端乙（用户端）**12** 个（P1–P4、O1–O4、B1–B3、F2）。回调与内部上报 2 个端点无前端对接。

---

## 3. Round 7 / 最新契约变更（前端必须对齐）

> 历史反复出现「后端改字段未同步前端」根因，以下为本期后端乙落地变更，前端对接时**逐条核对**。

| 编号 | 端点 | 变更内容 | 影响端 | PR |
|---|---|---|---|---|
| D-008 | `GET /api/wallet` | 钱包响应字段 `id` → **`wallet_id`** | 乙 | #135 |
| D-009 | `PATCH /api/admin/products/{id}/prices` | body 移除顶层 `plan_id`，改为每项内含 `product_plan_id`；支持单次配置多套餐 | 甲 | #135 |
| D-011 | `PATCH /api/admin/products/{id}/access` | `items` 为必填键，缺失返回 400；`"items": []` 合法（清空所有规则） | 甲 | #137 |
| BUG-A | `POST /api/products/{id}/purchase` | 购买为单事务（创建+扣费+MarkPaid）；响应 `status` 直接 `paid`（无 pending 中间态）；新增 `idempotent` 字段 | 乙 | #136 |
| BUG-B | `GET/PATCH /api/admin/products/{id}` 等 | 商品/套餐不存在返回 **404/40400**（原 200/500） | 甲 | #136 |
| BUG-C | `POST /api/admin/products` `.../plans` | 重复 product_code/plan_code 返回 **400/40000** 友好提示 | 甲 | #136 |
| BUG-D | `PATCH /api/admin/products/{id}/prices` | 多套餐价格覆盖写改为单事务原子操作（前端可一次提交多套餐） | 甲 | #136 |
| C-3 | `POST /api/recharge/orders` | 响应补 `order_no`/`amount`/`status` | 乙 | — |
| C-4 | `PATCH /api/admin/users/{id}/wallet/freeze` | body 统一为 `{action, amount, reason}`（原 `remark` → `reason`） | 甲 | — |
| C-5 | 消费记录 | 上报响应 `record_id` → `consumption_record_id`，新增 `wallet_transaction_id` | （内部） | — |

### 3.1 契约勘误（v1.1，据源码逐接口复核）

> 以下为 v1.0 规划稿与后端乙现行代码（`route.go` + DTO + handler）核对后发现的偏差，已同步修订两端任务单。**前端对接以本节为准**。

| # | 接口 / 类型 | 勘误 | 源码依据 |
|---|---|---|---|
| 1 | `GET /api/products/{id}/plans` | 响应是**扁平分页** `{items,page,page_size,total}`，**非** `{plans:[]}`；与商品详情里的 `plans` 字段结构不同，勿混用 | `product/handler/product_handler.go:126` |
| 2 | 购买响应 `PurchaseResult` | 含 `asset_id`（`number\|null`，异步开通时为 null），v1.0 类型漏列 | `product/dto/product_dto.go:91` |
| 3 | 购买 `POST /products/{id}/purchase` 错误码 | 除 70001/60001/40003 外，还有 **409/50000「系统繁忙请重试」**（并发锁耗尽）、40000「该套餐未配置价格」 | `product/handler/product_handler.go:174-179` |
| 4 | 支付 `POST /orders/{id}/pay` 错误码 | 除 60001 外，还有 **60002「订单已支付」(D-007)**、40900「状态不可支付/冲突」、40000「不支持的支付方式」、40004「订单不存在」 | `order/handler/order_handler.go:146-158` |
| 5 | 价格 `PATCH .../prices` 空数组 | prices 的 `items` **不可为空**（空→400「items 不能为空」）；与 access（空→清空规则，合法）**行为相反** | `product/handler/admin_product_handler.go:345` |
| 6 | 订单类型 `Order` / `OrderItem` | 列表/详情返回完整 `model.Order`（含 `user_id/cancelled_at/failed_at/remark/updated_at/items[]`）；`order_type` 取值为 **`product`**（非 `purchase`）；明细类型 `OrderItem` 需单独定义 | `order/model/order.go:16` |
| 7 | "不存在"错误码边界 | **40400 仅用于管理端商品/套餐**（BUG-B / D-006）；其余一律 **40004**：用户端 `GetProduct`/`GetOrder`、管理端订单（O6）、管理端计费规则（P17）均为 40004。即"管理端=40400"不成立，按 404 通用处理 | `product_handler.go:86`、`order_handler.go:108`、`admin_billing_rule_handler.go:169`、`admin_product_handler.go:101` |
| 8 | `WalletTransaction` 类型 | 实际含 `wallet_id/user_id/related_order_id`，v1.0 类型漏列 | `billing/model` + `billing_handler.go:114` |
| 9 | `user_price` 未配置取值 | **已定**：未配置统一返回 `-1`（区别于合法免费价 `0`）；前端以 `user_price === '-1'`（或 <0）判定"未配置/暂不可购买"。后端统一由 `feature/backend-product-userprice-unify` 落地（见对应后端 PR） | `product/dto/product_dto.go:144`、`product_handler.go` enrichPlansWithPrice |
| 10 | 订单 JSON 含 `idempotency_key` | 列表/详情会原样返回 `idempotency_key`，前端忽略即可（可提请后端评估是否隐藏） | `order/model/order.go:25` |
| 11 | `order_type` 取值 | 购买订单 `order_type` 实际为 **`product`**（非 `purchase`）；充值为 `recharge`。过滤/展示按 `product` | `order/service/pay_service.go:92`、`order/CLAUDE.md` |
| 12 | O3 钱包支付适用范围 | `POST /api/orders/{id}/pay` **仅支持 `order_type=product` 的 pending 订单**；recharge 订单不可钱包支付（返回 40000「该订单不支持钱包支付」），走第三方 pay_url。原文档"充值单续付"表述有误已订正 | `order/service/pay_service.go:92` |

---

## 4. 前端甲（管理后台）任务分解 —— 后端乙管理端

> 落地任务单详见 `docs/frontend-task-admin-console.md` §6（D1–D6）。本节给出阶段、归属端点与工期初估。
> 统一前置：全部需登录 + 对应权限码 + 管理员双重认证；列表走扁平 `PageResult<T>`。

### 阶段 D1 — 商品管理（CRUD + 状态切换）

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| D1-1 | 商品列表（keyword/status/type 过滤、分页） | P5 | 扁平分页 | 1d |
| D1-2 | 商品详情 | P7 | 不存在 → 404/40400 提示页（BUG-B） | 0.5d |
| D1-3 | 创建/编辑商品 | P6 / P8 | 重复 product_code → 400/40000 友好提示（BUG-C） | 1d |
| D1-4 | 上架/下架（active⇄inactive 切换） | P9 | status 仅接受 active/inactive，**draft 仅初始态不可设置（传 draft→400）**；切换二次确认 | 0.5d |

### 阶段 D2 — 套餐 + 访问权限 + 价格

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| D2-1 | 套餐列表 + 创建/编辑 | P10 / P11 / P12 | 重复 plan_code → 400（BUG-C） | 1.5d |
| D2-2 | 访问规则覆盖写（多角色 can_view/can_buy/can_use） | P13 | body `{items:[...]}`；`items:[]` 清空所有规则；**缺 items 键报 400**（D-011） | 1d |
| D2-3 | 价格配置（默认价/角色价/会员价三档，多套餐批量） | P14 | 每项含 `product_plan_id`，**无顶层 plan_id**（D-009）；可一次提交多套餐（BUG-D）；**items 不可为空（空→400，与 access 相反）** | 1.5d |

### 阶段 D3 — 计费规则 CRUD

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| D3-1 | 计费规则列表 | P15 | 扁平分页；按 product 过滤 | 0.5d |
| D3-2 | 新增/修改计费规则 | P16 / P17 | usage_type/usage_unit/billing_mode/free_quota 字段 | 1d |

### 阶段 D4 — 订单管理

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| D4-1 | 订单列表（user_id/status/order_type/时间过滤） | O5 | 扁平分页 | 1d |
| D4-2 | 订单详情 | O6 | 含 order_items | 0.5d |

### 阶段 D5 — 钱包管理

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| D5-1 | 按用户查钱包 | B5 | 输入/选择 user_id | 0.5d |
| D5-2 | 全量流水（user_id/type/direction/时间过滤） | B6 | 扁平分页 | 1d |
| D5-3 | 冻结/解冻 | B7 | body `{action:'freeze'\|'unfreeze', amount, reason}`；**amount 必填且>0**（两动作都要）；操作失败（如余额不足以冻结）→60001；需 `wallet:manage`，无权限 403；二次确认 | 1d |
| D5-4 | 回调记录列表（provider/status 过滤） | B8 | **不渲染 notify_body**（安全红线 B-04） | 0.5d |

### 阶段 D6 — 全量消费记录（F3）

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| D6-1 | 全量消费记录（user_id/product_id/usage_type/时间过滤） | F3 | `wallet:view`；扁平分页；列表无 wallet_transaction_id（以 event_id 对账） | 1d |

**前端甲合计初估**：约 **15 人日**（不含已有基础设施复用）。

---

## 5. 前端乙（用户控制台）任务分解 —— 后端乙用户端

> 落地任务单详见 `docs/frontend-task-user-console.md` §6（C1–C5）。统一前置：仅需登录；列表走扁平 `PageResult<T>`；金额字符串展示。

### 阶段 C1 — 商品市场

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| C1-1 | 商品列表（product_type/keyword 过滤） | P1 | 后端按角色 can_view 过滤，非 active 不展示；扁平分页 | 1d |
| C1-2 | 商品详情（套餐 + 用户实际价格） | P2 / P3 | user_price 为后端按会员/角色/默认优先级算出 | 1d |

### 阶段 C2 — 购买（钱包扣费，含幂等）

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| C2-1 | 购买弹窗 | P4 | 前端生成 Idempotency-Key；`status` 直接 `paid`（BUG-A）；`idempotent=true` 提示「已购买，未重复扣费」；70001→引导实名、60001→引导充值、40003→无权限提示 | 1.5d |

### 阶段 C3 — 我的订单（列表 + 详情 + 支付 + 取消）

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| C3-1 | 订单列表（status/order_type/时间过滤） | O1 | 扁平分页 | 1d |
| C3-2 | 订单详情 | O2 | 含 order_items | 0.5d |
| C3-3 | **支付存量 pending 购买订单（钱包）** | O3 | body `{pay_method:'wallet'}`，需 Idempotency-Key；响应 `{order_id,status,wallet_transaction_id,asset_id}`；余额不足 60001→引导充值。**仅 `order_type=product` 的 pending 订单可用；recharge 订单不支持钱包支付（40000）。场景：购买订单存量 pending 的续付** | 1d |
| C3-4 | 取消 pending 订单 | O4 | body `{reason}`；仅 pending 显示取消按钮；二次确认 | 0.5d |

### 阶段 C4 — 钱包（余额 + 流水 + 充值）

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| C4-1 | 钱包余额 | B1 | 取 `wallet_id`（**非 id**，D-008）；balance/frozen 字符串精确显示 | 0.5d |
| C4-2 | 流水（type/direction/时间过滤） | B2 | 扁平分页 | 0.5d |
| C4-3 | 充值 | B3 | 选 wechat/alipay；金额字符串；返回 HTTP 201，`pay_url` 展示二维码/跳转 | 1d |

### 阶段 C5 — 我的消费记录

| # | 任务 | 对接端点 | 关键点 | 工期 |
|---|---|---|---|---|
| C5-1 | 我的消费记录（product_id/usage_type/时间过滤） | F2 | 强制本人过滤；扁平分页 | 1d |

**前端乙合计初估**：约 **10.5 人日**（不含已有基础设施复用）。

---

## 6. 关键交互流程（用户端）

### 6.1 购买流程（P4，含前置校验与错误引导）
```
点「购买」→ 生成 Idempotency-Key(UUID) →
  POST /api/products/{id}/purchase  {plan_id, quantity, remark?}  header: Idempotency-Key
    ├ 70001 未实名         → 弹窗引导去实名（/identity 提交页）
    ├ 60001 余额不足       → 弹窗引导去充值（/wallet/recharge）
    ├ 40003 无购买权限     → 提示「当前角色无购买权限」
    ├ 40000 套餐未配置价格 → 提示「该套餐暂不可购买」
    ├ 409/50000 系统繁忙   → 提示「系统繁忙，请重试」（并发锁耗尽，复用同一 key 重试）
    ├ 成功 status=paid     → 提示「购买成功」，读取 asset_id（可能 null），刷新订单/资产
    └ 成功 idempotent=true → 提示「该订单已购买，未重复扣费」
```

### 6.2 充值流程（B3）+ 购买订单续付（O3）
```
充值（recharge）：POST /api/recharge/orders {amount, payment_method, return_url?}
  → 返回 {order_id, order_no, amount, status:'pending', pay_url}（HTTP 201）
  → 展示 pay_url 二维码 / 跳转第三方
  → 第三方回调（POST /api/payments/notify/{provider}，前端不参与）→ 订单转 paid、钱包入账
  → 前端轮询/手动刷新订单状态
  ⚠️ 充值订单（order_type=recharge）只能由第三方支付，不能用 O3 钱包支付

续付（O3，钱包支付存量 pending 的购买订单，仅 order_type=product）：
  POST /api/orders/{id}/pay {pay_method:'wallet'}  header: Idempotency-Key
    ├ 60001 余额不足          → 引导充值
    ├ 60002 订单已支付(D-007) → 刷新订单状态
    ├ 40900 状态不可支付/冲突 → 刷新后重试
    ├ 40000 不支持的支付方式  → 提示
    ├ 40004 订单不存在        → 提示
    └ 成功 → {order_id, status:'paid', wallet_transaction_id, asset_id}
```

### 6.3 金额展示约定
所有金额字段（`amount`/`balance_amount`/`frozen_amount`/`price_amount`/`user_price`）均为字符串，**直接展示**或交由 decimal 库计算，禁止 `Number()`/`parseFloat` 后参与求和展示（精度红线）。

---

## 7. 里程碑与验收

| 里程碑 | 内容 | 验收口径 |
|---|---|---|
| N1 用户购买闭环 | C1 + C2 + C4 | 商品市场→详情→购买→扣费→钱包余额变化全通；幂等/余额不足/未实名引导正确 |
| N2 用户订单与消费 | C3 + C5 | 订单列表/详情/支付(O3)/取消全通；消费记录展示正确 |
| N3 商品后台 | D1 + D2 + D3 | 商品/套餐/价格/访问/计费规则 CRUD 全覆盖；D-009/D-011/BUG-B/C 边界正确 |
| N4 交易后台 | D4 + D5 + D6 | 订单管理、钱包管理（冻结鉴权）、回调记录（无 notify_body）、消费记录全通 |

> 每个里程碑须经**测试工程师**回归 + **产品经理**确认，方可进入下一阶段（遵循阶段门禁原则）。两端可并行：前端乙推 N1→N2，前端甲推 N3→N4。

---

## 8. 风险与注意事项

1. **契约变更逐条对齐**（§3 表）：D-008 钱包 `wallet_id`、D-009 价格 `product_plan_id`、D-011 access `items` 必填、BUG-A 购买直达 paid + idempotent。
2. **幂等键由前端负责**：购买（P4）与续付（O3）必须带 `Idempotency-Key`，重试复用同一 key；缺失会被后端拒绝或产生重复请求风险。
3. **金额精度红线**：全程字符串，禁止浮点运算后展示。
4. **安全红线**：回调记录（B8）不渲染 `notify_body`；钱包冻结（B7）需 `wallet:manage`，无权限 403 要给明确提示而非笼统报错。
5. **权限码依赖**：管理端依赖 `product:view/create/edit`、`order:list`、`wallet:view/manage`。`wallet:manage` 为 Round 7 新增（seed migration 000023），前端菜单/按钮可见性按权限码控制；如测试环境 403，先确认后端已迁移至 000025。
6. **O3 与 P4 的区别**：P4（购买）是「创建即支付」的单事务，正常路径不产生 pending；O3（支付）用于**已存在的 pending 购买订单**（`order_type=product`，异常中断后的存量单）的钱包续付，**不适用于充值订单**（recharge 走第三方 pay_url）。前端不要把两者混用。
7. **消费记录无 wallet_transaction_id**：F2/F3 列表项不含扣费流水 ID（后端刻意不返回恒 null 字段），对账以 `event_id` 追溯。

---

## 9. 待确认事项（编码前请产品经理拍板）

- [ ] N1–N4 里程碑排期顺序，是否甲乙并行（管理端 N3/N4 与用户端 N1/N2 互不阻塞）。
- [ ] 用户端订单详情（O2）是否需要展示 `order_items` 明细，还是仅摘要字段。
- [ ] 充值后订单状态刷新策略：前端轮询 vs 手动刷新 vs 回跳 `return_url` 后查询。
- [ ] 计费规则（D3）管理页是否本期纳入，还是随商品后台延后。
- [ ] 工期为初估，需结合两位前端实际排期校准。
</content>
</invoke>
