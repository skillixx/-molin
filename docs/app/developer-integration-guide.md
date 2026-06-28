# 应用开发者对接指南（给应用开发者）

> 用途：你要把自己的应用接入本平台，让平台用户能**购买并使用**你的应用、由平台**自动计费**。本文讲你需要了解什么、怎么对接。
> 配套（细节）：字段级接口契约 `./billing-integration-spec.md`；单价/会员/避坑/案例 `./developer-requirements.md`；平台方准备 [平台方应用接入任务清单](./platform-integration-tasks.md)。
> 统一响应信封 `{code,message,data}`；金额一律字符串 decimal。

---

## 1. 先建立认知：你和平台的边界

> **平台负责"卖 + 收钱 + 发凭证 + 对账"；你负责"应用功能本身 + 在合适时机调计费接口"。**

```
平台做的：商品售卖、扣钱包/扣积分、生成资产/权益凭证、消费流水对账
你做的：  应用功能（上传/生成/计算…）+ 用前校验凭证 + 用时上报计费
```

**你几乎不写"扣费逻辑"本身**——扣钱、扣额度、防并发透支平台都做好了，你只在"用户用了多少/能不能用"这两件事上和平台交互。

---

## 2. 你需要了解的核心概念

| 概念 | 是什么 | 你怎么用 |
|---|---|---|
| **商品 product** | 你的应用在平台里被卖的形态（`product_type=application`，靠 `business_ref_id` 指向你的应用） | 平台方配，给你 `product_id` |
| **套餐 plan** | 购买规格（月付/积分包等） | 给你 `plan_id` |
| **价格 price** | 购买时一次性收费（含会员价，自动命中） | 你不管，平台配 |
| **资产 user_asset** | 用户买了之后的"凭证"（active/expired） | 用前查它确认用户有权用 |
| **权益额度 entitlement** | 预付额度（"积分账户"，quota_total/quota_used） | 积分制下查余额、扣积分 |
| **计费规则 billing_rule** | 用一次扣多少 | 平台配；你按约定上报 |
| **计费模型** | postpaid 用时扣钱包 / prepaid 用时扣额度 | 决定你调哪个接口 |

> 关键区分：**"购买"和"使用"是两个阶段**。购买=花钱得凭证（平台做）；使用=你提供功能并按量计费。**售卖和计费不是两个套餐，是一个商品的"买"和"用"。**

---

## 3. 对接前你会从平台方拿到什么

开工前向平台方要齐（见对方的任务清单）：

```
□ 标识：app_id、product_id、plan_id（积分制还需知道 entitlement 怎么取）
□ 计费约定：用哪种模型(postpaid/prepaid)、usage_type 命名、单价、积分单位
□ 内部密钥：INTERNAL_API_TOKEN（做使用扣费/额度才需要），且你的服务器 IP 已加白名单
□ 身份方案：SSO 一次性票据（推荐，收 `?ticket=` → 调 verify 换 user_id）；自有账号体系可用 `/api/my/assets` 兜底
□ 测试账号：一个带余额/积分的普通用户账号，供你联调
```

---

## 4. 你需要实现的三件事（核心工作量）

### ① 身份：确认"是哪个用户"
**推荐（SSO 一次性票据）**：用户从平台点「进入应用」时，会带 `?ticket=lt_xxx` 跳到你的入口地址。你的后端收到票据后调
`POST /api/internal/app-launch/verify`（带 `X-Internal-Token`，body `{"launch_ticket":"lt_xxx"}`）→ 平台**校验并消费**票据（一次性、60s 过期、防重放），返回 `{user_id, app_id, product_id}`。据此为该用户建立你自己的会话，完成免登。

- 票据无效/过期/已用 → 返回 `40003`，按"重新从平台进入"处理，**不要重试同一张票据**。
- 票据只走 URL query，**不在 URL 里放平台长期 JWT**；拿到 `user_id` 后用你自己的会话机制维持登录态。

> 备选：若你的应用已有自有账号体系，也可用 `GET /api/my/assets`（带用户 JWT）自行核对身份与使用权；但需可信免登交接时，一律走上面的票据方案。

### ② 用前校验：确认"用户有没有权用 / 额度够不够"
- 通用：`GET /api/my/assets`（带用户 JWT）→ 有 `product_id=你的商品` 且 `status=active` 的资产才放行。
- 积分制(prepaid)：票据只给了 `user_id`/`product_id`、没有 `entitlement_id`，先调 `GET /api/internal/user-entitlements?user_id=&product_id=`（带 `X-Internal-Token`）解析出可用权益的 `entitlement_id`；需要前置看余量再调 `GET /api/internal/entitlement-balance` 看 `usable=true` 且 `remaining` 够不够。

### ③ 用时计费：把"用了多少"告诉平台
- **postpaid（按量扣钱包）**：动作完成后调
  `POST /api/internal/product-usage-events`（带 `X-Internal-Token` + 唯一 `idempotency_key`）
- **prepaid（扣积分/额度）**：
  - 轻动作直扣：`POST /api/internal/entitlement-consume`
  - 贵动作防并发：`reserve` 预占 → 成功 `settle` / 失败 `release`

> ⚠️ **防超用靠平台原子扣减，不要自己"查余额→if 够→再扣"**（并发会超扣）。够不够的最终判定交给扣减调用，前置查余额只为体验。

---

## 5. 你会用到的接口清单（速查）

| 用途 | 接口 | 鉴权 |
|---|---|---|
| 票据换身份（免登 SSO） | `POST /api/internal/app-launch/verify` | X-Internal-Token |
| 查用户资产（用前校验） | `GET /api/my/assets` | 用户 JWT |
| 查用户额度（给用户看） | `GET /api/my/entitlements` | 用户 JWT（无 remaining，自己算 total−used） |
| **解析 entitlement_id**（第三方应用） | `GET /api/internal/user-entitlements?user_id=&product_id=` | X-Internal-Token（票据无 entitlement_id 时用它定位） |
| 查额度余量（服务端判断） | `GET /api/internal/entitlement-balance` | X-Internal-Token |
| 上报用量（postpaid） | `POST /api/internal/product-usage-events` | X-Internal-Token |
| 扣额度·一步（prepaid） | `POST /api/internal/entitlement-consume` | X-Internal-Token |
| 扣额度·预占/结算/释放 | `entitlement-reserve` / `settle` / `release` | X-Internal-Token |
| 用户消费明细（对账） | `GET /api/product-consumption-records` | 用户 JWT |

> 字段级请求/响应见 `./billing-integration-spec.md`。

---

## 6. 计费模型怎么选

```
只在购买时收一次（买断/月卡） → 不写计费代码，平台购买时扣钱即可
按使用量收钱（用一次扣一次）   → postpaid，做"上报用量"（最常见）
积分/额度包（先买后扣额度）     → prepaid，做"额度扣减"，用户看剩余积分
```

要点：
- `usage_type` 必须和平台计费规则约定的**一字不差**（如 `ppt_generate`/`storage_overage`）。
- 单价/积分消耗数：postpaid 配在平台规则里；prepaid 由你调用时指定 amount。
- postpaid 与 prepaid **互斥**，一次使用只走一条。

---

## 7. 开发步骤

```
阶段1 对齐：拿到第 3 节的交付清单，确认计费模型
阶段2 编码：实现第 4 节三件事（身份 / 用前校验 / 用时计费）
阶段3 联调：
  □ 用测试账号在市场看到并购买你的应用
  □ 购买后 GET /api/my/assets 有资产
  □ 用功能 → 计费/扣额度生效、金额对
  □ 重复上报金额不变（幂等）
  □ 余额/额度不足正确拒绝
  □ 对账能查到消费流水
```

---

## 8. 注意事项（高频踩坑）

- **内部接口**：`/api/internal/*` 必带 `X-Internal-Token`，部署在白名单内网，**不暴露公网**；密钥从环境变量读，不硬编码。
- **同机部署调内部接口走 `127.0.0.1`**：应用与平台服务部署在同一台服务器时（当前测试服拓扑），应用后端调内部接口请用 **`http://127.0.0.1:8080/...`（IPv4 字面量），不要用 `http://localhost:8080/...`**。`localhost` 在多数系统优先解析为 IPv6 `::1`，会被 IP 白名单辅助闸误拒；用 `127.0.0.1` 走 IPv4 即命中白名单（平台默认 `INTERNAL_ALLOWED_IPS=127.0.0.1,::1`）。应用若**跨机**部署，需把应用后端的出口 IP 加进平台的 `INTERNAL_ALLOWED_IPS`。
- **幂等键**：每条计费请求带全局唯一、可复算的 `idempotency_key`（如 `任务ID:类型`），防重复扣。
- **金额**：一律字符串 decimal，单价不超 6 位小数。
- **错误分类**：余额不足 `60001` / 额度不足 `60005` → 提示充值；无匹配规则（`40000`+特定 message）→ 静默跳过别重试；鉴权失败 `40003` → 查 token/IP。
- **用前校验是必须的**：别假设"买了就一直能用"，资产会到期（平台自动置 expired），每次用都要校验。
- **遵守边界**：你只写"何时校验/何时上报"的业务代码，不碰平台的 product/billing/asset 等模块。

---

## 9. 详细文档索引

| 你要查 | 看哪份 |
|---|---|
| 整体认知（商品/会员/应用/扣费） | `../business-billing-overview.md` |
| 字段级接口契约 | `./billing-integration-spec.md` |
| 单价设计/会员设计/避坑/案例 | `./developer-requirements.md` |
| 应用怎么挂成商品（设计） | `./billing-integration-design.md` |
| 平台方要为你准备什么 | [./platform-integration-tasks.md](./platform-integration-tasks.md) |

---

## 一句话总结

> **平台给你"卖+计费+凭证"，你给用户"功能"。**
> 你的活：接 JWT 认人 → 用前查资产/额度放行 → 用时调计费接口上报（带 token+幂等键）。三件事做好，计费就通了。
</content>
