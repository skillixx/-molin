# 商品 · 会员 · 应用 · 扣费 —— 业务与计费总览（文档导航）

> 本文是「商品售卖 + 会员 + 应用 + 扣费」四份对接文档的总入口，串起整条业务主线。
> 读者：后端、运营、产品、测试、前端对接。建议先读本文建立全局认知，再按需进入子文档。

---

## 一、四份文档与各自定位

| # | 文档 | 主题 | 一句话定位 |
|---|---|---|---|
| 1 | [商品与商品计费](./product-and-billing-guide.md) | `product` / `order` / `billing` / `finance_consumer` | 怎么卖、怎么定价、怎么扣钱（**底座**） |
| 2 | [会员管理](./membership-management-guide.md) | `membership` | 会员等级/权益，决定“享不享会员价” |
| 3 | [应用管理](./app-management-guide.md) | `app` | 应用元数据（图标/描述/适配器），“是什么、怎么对接” |
| 4 | [应用×财务商品 扣费集成设计](./app/billing-integration-design.md) | 跨模块集成 | 把应用挂成商品，实现购买扣费 + 使用扣费（**总装**） |
| 4b | [应用接入会员/商品计费 开发对接规范](./app/billing-integration-spec.md) | 开发者接入（字段级） | 开发应用如何对接计费：接口字段、上报/扣减流程、各功能分工 |
| 4c | [应用开发需求与注意事项](./app/developer-requirements.md) | 开发者需求/设计 | 硬性需求、单价设计、会员设计、易错坑、开发规范与案例 |

**阅读顺序建议**：1 → 2 → 3 → 4。文档 1 是底座（所有售卖与扣费的统一机制），2、3 是两类可售对象（会员、应用），4 把 3 接到 1 上完成扣费闭环。

---

## 二、一张图看清整体关系

```
                          ┌─────────────────────────────────────────────┐
                          │  财务商品底座（文档1：product/order/billing）   │
                          │                                             │
                          │  products ── plans ── prices（购买扣费）       │
                          │     │         │         access（谁能买/用）    │
                          │     │         └── billing_rules（使用扣费）     │
                          │     │                                        │
   product_type 决定开通  │     ├ product_type=membership（开通待接线）►文档2 会员│
   走哪个 Provisioner     │     ├ product_type=application ─► 文档3 应用    │
                          │     └ product_type=token/...                  │
                          └───────────────────────┬─────────────────────┘
                                                  │ business_ref_id 关联
            ┌─────────────────────────┐          │          ┌──────────────────────────┐
            │ 文档2 会员（membership）   │          │          │ 文档3 应用（app）           │
            │ levels / benefits        │◄─────────┴─────────►│ applications / adapters    │
            │ user_memberships         │  会员价由 prices      │ 仅元数据，不含交易字段       │
            │ → 决定会员价是否命中        │  的会员档承载         │ business_ref_id=app.id     │
            └─────────────────────────┘                     └──────────────────────────┘
                                                  │
                                                  ▼ 购买成功后
            ┌──────────────────────────────────────────────────────────────────────┐
            │  下单扣费(billing钱包) → 订单paid → provision开通 → asset资产/entitlement额度 │
            │  使用时 → 上报用量 → finance_consumer 按 billing_rules 扣钱包/扣额度（文档4）  │
            └──────────────────────────────────────────────────────────────────────┘
```

**核心枢纽**：
- **`products` 是唯一售卖入口**：会员、应用都通过“做成一个商品”进入售卖与扣费，不另起购买接口。
- **`product_type` 是路由键**：决定购买后由哪个 Provisioner 开通（`membership`/`application`/`token`…）。
- **`business_ref_id` 是关联键**：商品指向具体业务对象（应用 ID 等）。
- **会员价不在会员模块配**：由商品 `product_prices` 的会员档承载，会员模块只回答“是不是有效会员”。

---

## 三、两条贯穿全局的主线

### 主线 A：怎么把一个东西卖出去并扣到钱（购买扣费）

```
配置（运营）：建业务对象(会员等级/应用) → 做成商品(product_type+business_ref_id)
            → 配套餐 → 配价格(会员价/角色价/默认价) → 配访问权限 → 上架
用户（购买）：浏览市场 → 下单(带Idempotency-Key) → 扣钱包 → 订单paid → 开通 → 拿到资产/会员/额度
```
涉及文档：1（机制）+ 2 或 3（对象）+ 4（应用落地范例）。

### 主线 B：怎么按使用量持续扣钱（使用扣费）

```
配置（运营）：给商品配 product_billing_rules（按量/按次/套餐额度，postpaid或prepaid）
使用（运行）：业务上报用量事件 → finance_consumer 匹配规则 → 扣钱包(postpaid)/扣额度(prepaid) → 写消费记录
```
涉及文档：1（计费规则 + finance_consumer）+ 4（应用上报用量的端到端范例）。

---

## 四、跨文档高频问题速查

| 问题 | 答案 | 看哪份 |
|---|---|---|
| 会员怎么收费？ | 现阶段以**管理端手动开通**为准（`POST /api/admin/user-memberships`）；「做成 membership 商品购买后自动开通」是设计目标，provision 尚未注册 membership 处理器，未接线 | 1 + 2 |
| 应用怎么收费？ | 做成 `product_type=application` 的商品，`business_ref_id=应用ID` | 3 + 4 |
| 会员价在哪配？ | 商品 `product_prices` 的会员档（不在会员模块） | 1 案例4 / 2 |
| 购买时扣费 vs 使用时扣费？ | 价格=买的时候付；计费规则=用的时候扣 | 1 / 4 |
| 按量和按次能同时配吗？ | 不能，二选一，管理端强校验 | 1 案例9 / 4 |
| postpaid 和 prepaid 区别？ | 后付扣钱包 / 预付扣额度，互斥不双扣 | 1 / 4 案例8 |
| 防重复扣费靠什么？ | 购买靠 `Idempotency-Key`，用量靠事件 `idempotency_key` | 1 案例7 / 4 |
| 应用建好了为什么买不到？ | 还没做成商品（缺 `product_type=application` 的 product） | 3 / 4 案例2 |

---

## 五、统一约定（四份文档通用）

- **响应信封**：`{ "code": 0, "message": "ok", "data": ... }`；出错 `code != 0` 且 `data = null`。
- **列表分页**：商品/订单类用扁平分页 `{ items, page, page_size, total }`。
- **金额**：一律字符串 decimal（如 `"19.900000"`），禁止转 float。
- **幂等**：购买必带 `Idempotency-Key` 请求头；用量事件必带全局唯一 `idempotency_key`。
- **权限码**：`product:*`（商品）、`membership:view/manage`（会员）、`app:manage`（应用）。
- **状态可见性**：用户端只见 `active`；草稿/下架对用户隐藏。

---

## 六、文档清单（按模块）

- 商品/计费底座 → [`product-and-billing-guide.md`](./product-and-billing-guide.md)
- 会员 → [`membership-management-guide.md`](./membership-management-guide.md)
- 应用 → [`app-management-guide.md`](./app-management-guide.md)
- 应用扣费集成（总装） → [`app/billing-integration-design.md`](./app/billing-integration-design.md)
- 计费深度契约（按量/按次/套餐预付） → [`backend-token-billing-contract.md`](./backend-token-billing-contract.md)
</content>
