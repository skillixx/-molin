# Token 计费收口对接契约（按量 + 按次 + 套餐）

> 状态：对接契约 v1（2026-06-21）
> 阶段：第二阶段 M1（按次）+ M2（套餐预付）
> 实现方：后端乙（计费规则/商品）、后端丙（套餐额度扣减）、后端丁（门面计费编排）
> 关联：`docs/backend-stage2-architecture-roadmap.md` §4、`docs/backend-sk-auth-contract.md`、`docs/frontend-api-reference.md` §14
> 复用现状（已核对代码）：
> - 门面 `forward_service` 已有解耦上报接口 `UsageReporter.Report(UsageEvent{UserID,ProductID,UsageType,UsageAmount,IdempotencyKey})`
> - `finance_consumer` `POST /api/internal/product-usage-events` 按 `product_billing_rules(product_id,plan_id,usage_type)` 匹配 → 扣钱包，**幂等键去重**
> - `user_entitlements` 含 `quota_total/quota_used/quota_unit` + `ConsumeQuota`（`SELECT FOR UPDATE`）；`ProvisionService` 已能按 `plan.quota_json` 生成 entitlement

---

## 1. 三种计费总览

| 方式 | 付费 | usage_type / unit | 结算路径 | 状态 |
|---|---|---|---|---|
| 按量（token） | 后付·钱包 | `input_tokens`/`output_tokens` `tokens` | 门面上报 → finance_consumer 扣钱包 | ✅ 已实现（000033） |
| 按次（调用次数） | 后付·钱包 | `calls` `count` | 门面每次提问额外上报 1 条 calls 事件 | 🔜 乙+丁 |
| 套餐（预付额度） | 预付·entitlement | token 额度 | 门面扣 entitlement（丙），不走钱包 | 🔜 乙+丙+丁 |

**计费模式选择**（铁律）：由 sk / 调用上下文的 `billing_mode` 决定。
- `postpaid` → 钱包路径（按量 / 按次）
- `prepaid` → 套餐 entitlement 路径

**计费口径（PM 已确认 2026-06-21）**
- 一次用户提问触发 tool-use 多轮上游调用时：**按量** 累加所有轮 token；**按次** 仅计 **1 次**（按用户提问，不按上游轮数）。
- 同一商品可同时配「按量」或「按次」规则；**按量与按次二选一**配置在商品上，避免重复收费。**管理端必须做强校验**：保存按次规则时若已存在生效的按量规则（反之亦然）则拦截并提示，不依赖运营自觉；门面按存在的规则上报。

---

## 2. 按量计费（基线，已实现）

门面读上游 usage → 上报两条事件（`input_tokens` / `output_tokens`），幂等键 `request_id:input_tokens` / `request_id:output_tokens` → finance_consumer 扣钱包。无需改动，仅作为对照。

---

## 3. 按次计费（M1）

### 3.1 后端乙：计费规则 seed

新增计费规则（迁移 `000036_seed_token_call_billing_rule.up.sql`，序号以实际合并顺序为准——000034 api_keys、000035 wallet_holds 已合并，calls seed 顺延 000036），挂在现有 `token-api` 商品上：

```sql
-- 按次计费规则：usage_type=calls, usage_unit=count, price_amount=每次售价（占位，运营调整）
INSERT INTO product_billing_rules
  (product_id, product_plan_id, usage_type, usage_unit, price_amount, currency, billing_mode, status)
SELECT p.id, NULL, 'calls', 'count', 0.010000, 'CNY', 'postpaid', 'active'
FROM products p
WHERE p.product_code = 'token-api'
  AND NOT EXISTS (
    SELECT 1 FROM product_billing_rules r
    WHERE r.product_id = p.id AND r.usage_type = 'calls' AND r.product_plan_id IS NULL
  );
```
- 幂等：`INSERT ... NOT EXISTS` 锚点 `(product_id, usage_type, plan_id IS NULL)`，可重复执行。
- 是否启用按次由运营决定：建了 calls 规则即按次生效；若同时不想按量，运营把 input/output 规则置 `inactive`。

### 3.2 后端丁：门面上报次数事件

`forward_service` 在一次**用户提问**成功结算时，除按量两条外（或替代），额外上报一条：

```go
UsageEvent{
    UserID:         in.UserID,
    ProductID:      tm.ProductID,        // token_models.product_id
    UsageType:      "calls",
    UsageAmount:    decimal.NewFromInt(1),
    IdempotencyKey: requestID + ":calls", // 与 token 事件同源 request_id，保证幂等
}
```
- **tool-use 编排**下：一次提问只上报 **1 条 calls**（在编排结束、产出最终答案后），不随上游轮数累加。
- **计次条件（PM 决策 2026-06-21）**：只要**已发起至少一次上游调用并产生有效结果**（含因超 `MAX_ROUNDS` 终止但已出"超上限"提示、已消耗 token 的情况）即计 **1 次**；**纯前置失败不计次**（鉴权失败、`40300` 未开通/模型越界、余额闸拒绝等——尚未发起任何上游调用）。
- finance_consumer 无 calls 规则时返回「无匹配规则」→ 门面按「未配置按次」静默跳过（不报错、不重复扣量）。

---

## 4. 套餐预付（M2）

### 4.1 后端乙：token 套餐商品 + plan + 额度

新增 token 套餐 plan（与现有 `token-api-payg` 并列），`billing_type=usage`、`quota_json` 声明额度，供 `ProvisionService` 生成 entitlement：

```sql
-- 套餐 plan：例「100万 token 套餐」，quota_json 声明额度总量、单位与有效期
INSERT IGNORE INTO product_plans (product_id, plan_code, name, billing_type, quota_json, status)
SELECT p.id, 'token-pkg-1m', '100万 Token 套餐', 'usage',
       JSON_OBJECT('entitlement_type','token_quota','quota_total',1000000,'quota_unit','tokens','valid_days',365),
       'active'
FROM products p WHERE p.product_code = 'token-api';
-- 套餐售价配 product_prices（一次性预付价）；购买走现有 POST /orders + 钱包支付。
```
> **额度单位 = token 数**（PM 决策 2026-06-21，与计费同维度、余额耗尽即拒；不用金额，prepaid 不走钱包无需折算汇率）。
> **套餐有效期（PM 决策）**：`quota_json.valid_days` 声明有效天数，开通时 `user_entitlements.expires_at = started_at + valid_days`；**到期未用完额度清零**（entitlement 置 `expired`，门面前置闸据 `status`/`expires_at` 拒绝）。有效期到期由现有资产过期机制处理，不另起。

### 4.2 后端丙：套餐生成 + 额度扣减接口

**A. 开通生成 entitlement**：`ProvisionService` 已按 `plan.quota_json` 生成 `user_entitlements`（`entitlement_type=token_quota`, `quota_total=1000000`, `quota_unit=tokens`, `quota_used=0`, `status=active`）。`TokenProvisioner` 当前按量分支不建额度——**套餐分支需放行**：当 plan 带 `quota_json` 时正常生成 entitlement（确认 ProvisionService 已据 QuotaConfig 处理，则 TokenProvisioner 无需改）。

**B. 额度扣减内部接口**（新增）：

```
POST /api/internal/entitlement-consume        （内部调用，门面 → 丙）
```
请求体：
```json
{
  "entitlement_id": 123,
  "amount": "232",                     // 本次消耗额度（token 数，decimal）
  "idempotency_key": "req_xxx:quota",  // 幂等键，request_id:quota
  "user_id": 45                        // 校验归属
}
```
响应 `data`：
```json
{ "entitlement_id": 123, "quota_total": "1000000", "quota_used": "5232", "remaining": "994768", "status": "active" }
```
实现要点：
- 事务内 `FindByIDForUpdate` 锁行 → 校验 `status=active`、未过期（`expires_at`）且 `quota_used + amount <= quota_total` → `ConsumeQuota`（已有 `SELECT FOR UPDATE`）。
- **幂等（D5 已拍板）**：**必须新建幂等表 `entitlement_consume_logs`**（不复用钱包消费流水，二者域不同），与 postpaid 钱包幂等对称：
  ```sql
  CREATE TABLE entitlement_consume_logs (
    id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    entitlement_id  BIGINT UNSIGNED NOT NULL,
    user_id         BIGINT UNSIGNED NOT NULL,
    amount          DECIMAL(18,6) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,          -- request_id:quota
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_entitlement_consume_idem (idempotency_key)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
  ```
  事务内：先 `INSERT` 幂等日志（唯一键冲突 = 重复请求，直接返回首次结果，不二次扣减）→ 再 `ConsumeQuota`。迁移序号紧随套餐相关迁移（M2，丙）。
- 余额/额度不足：复用现有错误码 **`60005` 权益额度不足**（`full-api-design.md` 已定义；**不新造 60002**——60002 已是「重复支付」），门面据此拒绝/降级。
- 归属校验：`entitlement.user_id == req.user_id`，否则 40003。

**C. 余额查询**（门面前置闸用，可复用现有 §10.3「我的权益额度」或加内部查询）：返回 `remaining = quota_total - quota_used`。

### 4.3 后端丁：门面计费路由（postpaid vs prepaid）

`forward_service` 按 `billing_mode`（来自 sk/上下文，见 sk 契约 §5）分流。**postpaid 引入预扣保证金（D1），prepaid 引入预占额度（方案 B，PR #232/#233），两者结构对称**：

```
转发前（预扣 / 预占）：
  if billing_mode == "postpaid":
      hold = 模型单价 × max_tokens(请求带则用之，否则模型档位默认上限)
      freeze(hold) 冻结保证金；余额不足 → 拒绝(60001)；
      乐观锁冲突 → 503/50301（可重试，非 60001）
  if billing_mode == "prepaid":
      reserve_amount = max_tokens（与单次预估消耗同量纲）
      POST /api/internal/entitlements/reserve(entitlement_id, amount=reserve_amount, idempotency_key)
      available = quota_total - quota_used - quota_reserved < reserve_amount → 拒绝(60005)
      // reserve 成功后 quota_reserved += reserve_amount，不变量 used+reserved ≤ total 全程成立

转发上游 → 读 usage（SSE 断开仍读完再结算，R5）→ 写 token_usage_logs

结算阶段（拿到 usage 后）：
  if billing_mode == "postpaid":
      actual = 按 product_billing_rules 计算(input/output tokens [+ calls])
      unfreeze(hold) 解冻保证金 → 实扣 actual(多退少补)     // 净额 = 实扣，杜绝并发负余额
  if billing_mode == "prepaid":
      actual_tokens = input_tokens + output_tokens
      POST /api/internal/entitlements/{hold_id}/settle(actual_amount=min(actual_tokens, reserve_amount))
      // settle: quota_used += settled, quota_reserved -= reserve_amount（多退少补，不超收）

转发失败（上游 5xx / 超时）：
  if billing_mode == "postpaid":
      unfreeze(hold) 归还保证金（defer 在 panic/error 路径也执行）
  if billing_mode == "prepaid":
      POST /api/internal/entitlements/{hold_id}/release
      // release: quota_reserved -= reserve_amount（quota_used 不增），hold.status=released
```

- **预扣保证金（D1）postpaid**：并发请求各自占住钱包额度，结算时解冻按实际 usage 实扣（多退少补）；乐观锁冲突 → ErrSystemBusy → 503/50301，不误报 60001。
- **预占额度（方案 B）prepaid**：与 postpaid freeze/settle/release 结构对称，DB 层 `entitlement_holds` 表 + `quota_reserved` 列；FOR UPDATE 行锁保证 reserve 原子性，`available = total - used - reserved` 不变量全程成立。根治了「0 < remaining < 单次消耗」区间的串行/并发白嫖漏洞（D-M2-01 已验收闭环）。
- `source_id`（= entitlement_id）来自 sk 的 `ResolveKey` 结果。
- prepaid 模式下**不走钱包**、不预扣、不上报 product-usage-events。
- **SSE 断开兜底（R5）**：客户端中断后服务端继续读完上游流拿到 usage 再结算；确无 usage 的成功调用按 `max_tokens` 兜底（settle 封顶于 reserve_amount，避免漏计）。
- 写 `token_usage_logs` 不变（两种模式都写，`sale_amount` 记本次实扣金额/额度）。

---

## 5. 任务拆分

**后端乙**
1. `000036` 按次计费规则 seed（`calls/count`）
2. token 套餐 plan（`quota_json` 声明 token 额度）+ 套餐售价
3. 校验 finance_consumer 对 `calls` 事件正常匹配扣费
4. **（D1 前置，W5 第一天）确认钱包 `freeze/unfreeze` 对门面暴露可调内部接口，无则补**

**后端丙**
1. `POST /api/internal/entitlement-consume`（锁行 + 余额校验 + 归属 + **D5 新建幂等表 `entitlement_consume_logs` 及其迁移**）
2. 确认 `ProvisionService`/`TokenProvisioner` 套餐分支正常生成 `token_quota` entitlement
3. （可选）内部余额查询接口供门面前置闸

**后端丁**
1. 门面上报 `calls` 次数事件（一次提问 1 条，tool-use 不累加）
2. 计费路由：`postpaid`→**预扣保证金(freeze)** + 结算解冻实扣(钱包)；`prepaid`→调丙 entitlement-consume
3. 前置余额闸（钱包/额度）+ 余额不足拒绝；**SSE 断开仍读完上游再结算（R5）**
4. `token_usage_logs.sale_amount` 记本次实扣

---

## 6. 验收（测试/PM）

- 按量：调用 → input/output 扣钱包（基线回归）
- 按次：配 calls 规则 → 一次提问扣 1 次；tool-use 多轮仍只扣 1 次
- 套餐：买套餐 → 生成 token_quota entitlement → prepaid sk 调用扣额度、不扣钱包 → `remaining` 递减 → 耗尽后拒绝
- 幂等：同 `request_id` 重复上报/扣减不二次扣费
- 并发：同一 entitlement 并发调用无超扣（`SELECT FOR UPDATE` 生效）

---

## 7. 红线

- 计费事件必须带幂等键（`request_id:类型`），杜绝重复扣费。
- prepaid 与 postpaid 互斥结算，严禁同一次调用既扣钱包又扣额度。
- 额度扣减事务内锁行，余额校验在事务内完成，防并发透支。
- **错误码复用现有 `60005` 权益额度不足**（套餐额度不足场景）；钱包余额不足仍用 `60001`。**禁止新造 60002**（已占用=重复支付）。
- **插件外部成本归属**：官方上架插件若调用付费第三方 API，其成本由**平台承担**（运营选型时自行评估），用户侧不额外计费——与「唯一收费点 = 模型 token」自洽。
