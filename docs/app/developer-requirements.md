# 应用开发需求与注意事项 —— 开发规范 + 开发案例

> 📚 本文属于 [业务与计费总览](../business-billing-overview.md) 文档体系；面向**应用开发者**，规定开发一个计费应用的硬性需求、设计要点（商品单价设计、会员设计）、易错注意事项与完整开发案例。
> 配套：[接入对接规范（字段级）](./billing-integration-spec.md)、[扣费集成设计](./billing-integration-design.md)、[商品计费](../product-and-billing-guide.md)、[会员管理](../membership-management-guide.md)。
> 统一信封 `{code,message,data}`；金额一律字符串 decimal。

---

## 一、开发需求（硬性要求，必须满足）

| # | 需求 | 验收口径 |
|---|---|---|
| R1 | 应用必须挂成商品才有计费能力 | 存在 `product_type=application` 且 `business_ref_id=应用ID` 的 active 商品 |
| R2 | 购买必须幂等 | 调购买接口带唯一 `Idempotency-Key` 头，重复请求不重复扣款 |
| R3 | 使用扣费必须幂等 | 每条用量事件带全局唯一 `idempotency_key`，重复上报不二次扣费 |
| R4 | `usage_type` 三处一致 | 适配器声明 / 计费规则 / 上报事件用同一字符串 |
| R5 | 金额用 decimal 字符串 | 收发金额一律字符串，禁止 float 解析 |
| R6 | 内部接口鉴权 | `/api/internal/*` 带 `X-Internal-Token`，部署在 IP 白名单内网，不暴露公网 |
| R7 | `billing_mode` 只用 `postpaid`/`prepaid` | 全小写，不自创（规则层不校验，写错是静默坑） |
| R8 | 按量/按次二选一 | 同一商品不同时配按量(`*_tokens`)与按次(`calls`)规则 |
| R9 | 计费失败要分类处理 | "无匹配规则"静默跳过；"余额不足"业务降级；勿无脑重试扣款 |

---

## 二、设计要点与注意事项

### 2.1 商品单价设计（最需要想清楚）

**A. 两层价格，别混淆**

| 价格 | 配在哪 | 字段 | 决定 |
|---|---|---|---|
| 购买价（一次性） | `product_prices` | `price_amount` | 用户买套餐付多少 |
| 使用单价（按量/按次） | `product_billing_rules` | `price_amount` | 用户用一次/一单位扣多少 |

两者都是 `DECIMAL(18,6)` —— **整数 12 位、小数 6 位**。

**B. 单价精度与粒度**

- 小数最多 **6 位**：可表达极小单价，如每 token `0.000002` 元。
- 超过 6 位小数会被截断，**定单价时自己控制在 6 位内**。
- 计费金额公式（平台执行，无额外四舍五入）：
  ```
  amount = price_amount × max(0, usage_amount − free_quota)
  ```
- `amount ≤ 0` 时**不扣费**，但仍写一条 `amount=0` 的消费记录留痕（免费额度内的用量可对账）。

**C. 单价怎么定（按计费类型）**

| 计费类型 | usage_type | 单价含义 | 示例 |
|---|---|---|---|
| 按次 | `calls` | 每次调用价 | `0.010000`（1 分/次） |
| 按量·token | `input_tokens`/`output_tokens` | 每 token 价 | 输入 `0.000002`、输出 `0.000006` |
| 按量·存储 | 自定义如 `storage_used` | 每单位价 | `0.010000`/GB |

> 输入/输出 token 通常**分别配两条规则**，单价不同。

**D. 免费额度 `free_quota` 的语义坑（务必看）**

⚠️ `free_quota` 是**对"每一条上报事件"的 `usage_amount` 做扣减**，**不是周期累计免费额度**。

- 例：规则 `free_quota=10`(GB)，你每小时上报一次 `usage_amount=25` → **每次**都按 `25−10=15` 计费，一天扣 24 次 15GB，而不是"当月前 10GB 免费"。
- 想做"**每月前 N 单位免费**"：由你的应用**自己累计**已用量，只把**超出免费额度的增量**作为 `usage_amount` 上报（或 `free_quota=0`，免费逻辑全在应用侧算）。
- `free_quota` 适合"**单次调用内的免抵**"（如每次请求前 X token 不计费），不适合周期免费额度。

**E. 币种**：`currency` 默认 `CNY`，钱包同币种结算，第一阶段统一人民币。

### 2.2 会员设计

**A. 三张表的职责**

| 表 | 设计什么 | 关键字段 |
|---|---|---|
| `membership_levels` | 会员档（普通/黄金/钻石） | `level_code`(唯一)、`sort_order`(排序)、`status` |
| `membership_benefits` | 每档享什么权益（展示用） | `benefit_type`、`benefit_value`(JSON 字符串) |
| `user_memberships` | 谁是会员、到期没 | `status`、`expires_at`(null=永久) |

**B. 会员价怎么设计 —— 不在会员模块，在商品价格里**

会员折扣**不在会员模块配**，而是给套餐价格配"会员档"（`product_prices.membership_level_id`）。取价优先级（命中即停）：

```
会员价（命中用户会员等级）> 角色价（多角色取最低）> 默认价（兜底）
```

- **设计要求**：凡是要卖的套餐，**默认价必配**（用户没命中会员/角色价时兜底，否则取价失败无法购买）。
- 会员价**自动生效**：用户是有效会员且配了对应会员档价，购买时自动按会员价扣——应用和运营都无需在扣费时判断会员身份。

**C. 会员专属购买门槛**

若某套餐**只配了会员档价、没配默认价**，则构成"会员专属"：非该等级会员购买会被拦截（`40003 无购买权限`）。用这个特性做"仅会员可购"的商品。

**D. 有效会员判定口径（平台保证，应用查询时同口径）**

```
status = active AND (expires_at IS NULL OR expires_at > NOW())
```

应用要按会员等级做**功能门禁**（如"黄金会员才能用高级功能"）时，查 `GET /api/my/membership`，按上面口径判断。

**E. 续期是叠加不是覆盖**：同一 `(用户, 等级)` 再次开通，在原有效期上叠加天数，不新增记录；`duration_days=null` = 永久会员。

**F. ⚠️ 现状**：会员"购买商品自动开通"链路**尚未接线**（provision 未注册 membership 处理器）。现阶段会员开通以**管理端手动开通**为准（`POST /api/admin/user-memberships`）。详见 [会员文档·现状必读](../membership-management-guide.md)。

### 2.3 计费模式 `billing_mode`（固定枚举 + 静默坑）

- 只有两个值：`postpaid`（后付·扣钱包）/ `prepaid`（预付·扣额度）。**全小写，不自创。**
- ⚠️ **`product_billing_rules` 这层不校验枚举（只校验非空）**，写成 `Postpaid`/`post_paid` 能存进去但行为错——务必精确。
- ⚠️ **想做 prepaid 扣额度，别用上报用量接口**：`finance_consumer` 的 `product-usage-events` 一律扣钱包、不读 `billing_mode`；prepaid 必须走 `asset` 额度接口（`entitlement-reserve/settle/consume`）。
- 绝大多数应用用 `postpaid` 即可。

### 2.4 其它通用注意

- **幂等键格式**：建议 `业务请求ID:类型`（`evt1:calls`、`req9:storage_used`、`req9:quota`），唯一且可复算。
- **上报时机由你定**：按次（每次调用上报）、按周期（定时巡检用量）、按阈值，皆可。
- **"无匹配规则"不是错误**：说明该商品没配该 `usage_type` 的规则，静默跳过即可，别重试。
- **下架要连动**：应用下架时同步下架其关联商品，否则用户端可见性与可购买性不一致。

### 2.5 额度（积分）查询与扣减的注意事项

> 适用 prepaid 场景（如"积分制"应用：买积分包→花积分）。平台的 `user_entitlements`（权益额度）就是你的"积分账户"。

**A. 平台已现成提供，不用自己写额度系统**

查询和消费额度的能力平台已实现、已接入、有数据库测试，直接调即可：

| 能力 | 接口 | 说明 |
|---|---|---|
| 用户查自己额度 | `GET /api/my/entitlements` | 用户 JWT；返回 `quota_total`/`quota_used` |
| 服务端查额度 | `GET /api/internal/entitlement-balance` | X-Internal-Token；直接返回 `remaining`/`usable` |
| 一步扣减 | `POST /api/internal/entitlement-consume` | 用量已知（如修改扣 2 积分） |
| 预占→结算/释放 | `entitlement-reserve`/`settle`/`release` | 用量未定或贵动作（如生成扣 6 积分，防并发/失败回滚） |

**B. 查余额：两个接口的差别（容易踩）**

- `GET /api/my/entitlements`：**没有 `remaining` 字段**，需自己算 `剩余 = quota_total − quota_used`；适合给用户看余额。
- `GET /api/internal/entitlement-balance`：**直接给 `remaining`（已扣预占）+ `usable`**；适合服务端前置判断够不够。
- 不限量额度（`quota_total` 为 NULL）时 `quota_total`/`remaining` 为 `null`，接入方需容许缺省。

**C. ⚠️ 防超用靠平台原子扣减，不要应用"查了再扣"的 if 把关**

限制"额度用没用完"是**分层**的，别搞错谁兜底：
- **平台 = 硬兜底**：扣减时若不足直接拒——prepaid 额度不足 `consume`/`reserve` 返回 `60005`；postpaid 钱包余额不足在 product-usage-events 上报时返回 `60001`。`FOR UPDATE` 行锁保证并发不透支——**绕不过**。
- **应用 = 软前置**：操作前查 `entitlement-balance`，不够就提前拦、提示充值——**只为体验和省资源**，不负责防透支。
- ❌ **错误写法**：`查余额 → if ≥6 → 生成 → 扣6`。并发下两个请求都"查到够"、都通过 if、都扣 → 超用变负余额。
- ✅ **正确**：够不够的**最终判定交给平台的扣减调用**（consume/reserve 原子完成校验+扣减）；应用的查余额仅用于 UX 提示。

**D. consume vs reserve/settle 怎么选**

- **轻、便宜、用量已知**（如修改单页扣 2）→ 直接 `entitlement-consume`。
- **贵、耗时、可能失败**（如生成整套 PPT 扣 6）→ `reserve` 预占 → 成功 `settle` 实扣 / 失败 `release` 归还，防"白做一次"和并发白嫖。

**E. 概念澄清：售卖与扣费是"买"和"用"两个阶段，不是两个套餐**

- **买**（售卖）：用户花钱买"积分包套餐"→ 钱在这里扣一次 → 得到额度（entitlement）。套餐属于这一侧，可有多档（100/500/1000 积分包）。
- **用**（扣费）：生成/修改时扣积分 → 扣的是**额度不是钱**。这不是套餐，是对已买额度的消耗。
- prepaid（积分）下钱只在"买"时动；postpaid（按量付费）下钱在"用"时动——**两种模型二选一**，"积分扣费" ≠ "按量付费(postpaid)"。

**F. 前提**：① 这些 `/api/internal/*` 需 `X-Internal-Token` + IP 白名单，不暴露公网；② 额度来自用户购买套餐后开通生成的 entitlement，没买就没额度可查/可扣。

---

## 三、开发规范（编码与流程）

1. **配置先行**：先把应用配成商品、配套餐/价格/规则并上架，再写应用上报代码——否则上报全部"无匹配规则"。
2. **密钥从环境变量读**：`INTERNAL_API_TOKEN` 等绝不硬编码、绝不入库（`.env.local` 已在 `.gitignore`）。
3. **金额全程 decimal**：用 decimal 库运算与序列化，单价定义不超过 6 位小数。
4. **每个计费动作生成唯一幂等键并落库**，便于失败重发与对账。
5. **错误分类处理**：余额不足（`60001`）/额度不足（`60005`）→ 业务降级或提示；鉴权失败（`40003`）→ 检查 token/IP；无规则 → 跳过。
6. **上报与业务解耦**：扣费上报失败不应阻断核心业务（除非是"先扣后用"的强一致场景，那应走 prepaid 预占）。
7. **自测三件套**：① 配置后能购买并生成资产；② 上报用量能扣到钱、写消费记录；③ 重复上报金额不变（幂等）。
8. **遵守分工**：应用开发者只写"何时上报/调额度"的业务代码，不改 product/billing/finance_consumer/asset 等平台模块。

---

## 四、开发案例

> 管理端 `{{ADMIN}}`、用户 `{{USER}}`、内部密钥 `{{INTERNAL_API_TOKEN}}`。

### 案例 A：按次计费的 AI 小工具（postpaid，最常见）

**需求**：开通免费，用户每问一次扣 0.01 元。

**配置（运营，零应用代码）**
```bash
# 1. 应用 → 商品（business_ref_id 指向应用）→ 上架（略，见集成设计案例 1-2、4）
# 2. 套餐：开通免费（默认价 0）
curl -X POST /api/admin/products/100/plans -H "Authorization: Bearer {{ADMIN}}" \
  -d '{"plan_code":"free","name":"免费开通","billing_type":"one_time","duration_days":null}'
curl -X PATCH /api/admin/products/100/prices -d '{"items":[
  {"product_plan_id":51,"role_id":null,"membership_level_id":null,"price_amount":"0","currency":"CNY"}]}'
# 3. 按次计费规则：每次 0.01 元
curl -X POST /api/admin/product-billing-rules -d '{
  "product_id":100,"product_plan_id":null,
  "usage_type":"calls","usage_unit":"count",
  "price_amount":"0.010000","billing_mode":"postpaid","free_quota":"0","status":"active"}'
```

**应用代码（每次提问结束后上报 1 次）**
```go
reportUsage := map[string]any{
  "event_id":        reqID,
  "user_id":         userID,
  "product_id":      100,
  "usage_type":      "calls",          // 与规则一致
  "usage_amount":    "1",
  "usage_unit":      "count",
  "idempotency_key": reqID + ":calls", // 唯一
}
httpPost("/api/internal/product-usage-events", reportUsage,
  header("X-Internal-Token", env("INTERNAL_API_TOKEN")))
// code==0 → 扣 0.01；余额不足(60001) → 提示充值；无规则 → 跳过
```

**作用**：用户问 100 次扣 1 元，幂等键保证重试不重复扣。

---

### 案例 B：按 token 量计费 + 单次免抵（postpaid）

**需求**：输入每 token 0.000002 元、输出 0.000006 元；每次请求前 100 token 免费。

**配置**
```bash
curl -X POST /api/admin/product-billing-rules -d '{
  "product_id":100,"usage_type":"input_tokens","usage_unit":"tokens",
  "price_amount":"0.000002","billing_mode":"postpaid","free_quota":"100","status":"active"}'
curl -X POST /api/admin/product-billing-rules -d '{
  "product_id":100,"usage_type":"output_tokens","usage_unit":"tokens",
  "price_amount":"0.000006","billing_mode":"postpaid","free_quota":"0","status":"active"}'
```

**应用代码（一次请求结束上报两条）**
```go
for _, u := range []struct{ t string; n int }{{"input_tokens", inN}, {"output_tokens", outN}} {
  httpPost("/api/internal/product-usage-events", map[string]any{
    "event_id": reqID, "user_id": userID, "product_id": 100,
    "usage_type": u.t, "usage_amount": itoa(u.n), "usage_unit": "tokens",
    "idempotency_key": reqID + ":" + u.t,   // 每类型独立幂等键
  }, header("X-Internal-Token", env("INTERNAL_API_TOKEN")))
}
```

**注意**：`free_quota=100` 是**每条事件**免抵 100 token（单次免抵），非当月累计；要"每月前 1 万 token 免费"得应用侧自己累计后只报增量。

---

### 案例 C：会员折扣应用（购买价享会员价）

**需求**：网盘月付 19.9 元；黄金会员（等级 2）14.9 元；钻石会员（等级 3）仅会员可买。

**配置（默认价必配 + 会员档价）**
```bash
# 普通套餐：默认价 + 黄金会员价
curl -X PATCH /api/admin/products/100/prices -d '{"items":[
  {"product_plan_id":50,"role_id":null,"membership_level_id":null,"price_amount":"19.90","currency":"CNY"},
  {"product_plan_id":50,"role_id":null,"membership_level_id":2,"price_amount":"14.90","currency":"CNY"}]}'

# 钻石专属套餐：只配会员档价、不配默认价 → 非钻石会员买不了（会员门槛）
curl -X PATCH /api/admin/products/100/prices -d '{"items":[
  {"product_plan_id":52,"role_id":null,"membership_level_id":3,"price_amount":"9.90","currency":"CNY"}]}'
```

**应用代码**：购买时**无需任何会员判断**，平台自动按身份取价。仅当要做"会员专属功能"时查会员：
```go
m := httpGet("/api/my/membership", bearer(userToken))   // data.membership=null 即非会员
if m.membership != nil && m.membership.level_code == "diamond" {
   enableAdvancedExport()                                 // 按等级开放功能
}
```

**作用**：会员自动享折扣；钻石套餐构成会员专属门槛，非会员购买返回 `40003`。

---

### 案例 D：预付额度包（prepaid，不扣钱包）

**需求**：卖"100 万 token 套餐"，用户先付钱买额度，用时扣额度。

**配置**
```bash
# 套餐声明额度（quota_json）
curl -X POST /api/admin/products/100/plans -d '{
  "plan_code":"pkg-1m","name":"100万Token套餐","billing_type":"usage",
  "quota_json":"{\"entitlement_type\":\"token_quota\",\"quota_total\":1000000,\"quota_unit\":\"tokens\",\"valid_days\":365}"}'
# 套餐售价（一次性预付价，走购买扣钱包）
curl -X PATCH /api/admin/products/100/prices -d '{"items":[
  {"product_plan_id":53,"role_id":null,"membership_level_id":null,"price_amount":"99.00","currency":"CNY"}]}'
# 计费规则声明 prepaid（声明用途；扣减走 asset 接口，不走上报）
curl -X POST /api/admin/product-billing-rules -d '{
  "product_id":100,"usage_type":"input_tokens","usage_unit":"tokens",
  "price_amount":"0.000002","billing_mode":"prepaid","status":"active"}'
```

**应用代码（用时扣额度，不走 product-usage-events）**
```go
// 购买后拿 entitlement_id：GET /api/my/entitlements
// 不确定实际用量时：先预占，拿到实际再结算（多退少补）
h := httpPost("/api/internal/entitlement-reserve", map[string]any{
  "entitlement_id": entID, "user_id": userID,
  "amount": "2000", "idempotency_key": reqID + ":quota"},  // 预估上限
  header("X-Internal-Token", env("INTERNAL_API_TOKEN")))   // available 不足 → 60005 拒绝
// ... 调用上游，得到 actual ...
httpPost("/api/internal/entitlement-settle", map[string]any{
  "hold_id": h.hold_id, "actual_amount": itoa(actual)},     // 实扣 min(actual, 预占)
  header("X-Internal-Token", env("INTERNAL_API_TOKEN")))
// 失败路径：entitlement-release 归还预占
```

**注意**：prepaid **不扣钱包、不上报 product-usage-events**；额度耗尽返回 `60005`；到期未用清零。

---

## 五、自检清单（提测前过一遍）

```
□ 应用已配成 active 商品（product_type=application + business_ref_id）
□ 套餐默认价已配（否则取价失败无法购买）
□ 会员价/角色价（如有）已配，优先级符合预期
□ 计费规则 usage_type 与上报、适配器声明三处一致
□ billing_mode 全小写且为 postpaid/prepaid 之一
□ 按量/按次未同时配（互斥）
□ free_quota 语义确认（每事件免抵 vs 周期累计——后者需应用自算）
□ 每条计费请求带唯一 idempotency_key，重复上报金额不变
□ 内部接口带 X-Internal-Token，部署在 IP 白名单内网
□ 余额不足/额度不足/无规则 三类返回均有处理分支
□ 单价小数 ≤ 6 位，金额按 decimal 字符串收发
□ （prepaid）查余额用对接口：用户端 my/entitlements 自己算剩余，服务端 entitlement-balance 取 remaining
□ （prepaid）防超用交给 consume/reserve 原子扣减，未用"查了再扣"的 if 把关
□ （prepaid）贵动作用 reserve→settle、轻动作用 consume
```
