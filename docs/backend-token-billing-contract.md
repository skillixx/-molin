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

**计费口径（待 PM 终确认，文档按建议默认）**
- 一次用户提问触发 tool-use 多轮上游调用时：**按量** 累加所有轮 token；**按次** 仅计 **1 次**（按用户提问，不按上游轮数）。
- 同一商品可同时配「按量」或「按次」规则；**按量与按次二选一**配置在商品上，避免重复收费（运营在管理端控制，门面按存在的规则上报）。

---

## 2. 按量计费（基线，已实现）

门面读上游 usage → 上报两条事件（`input_tokens` / `output_tokens`），幂等键 `request_id:input_tokens` / `request_id:output_tokens` → finance_consumer 扣钱包。无需改动，仅作为对照。

---

## 3. 按次计费（M1）

### 3.1 后端乙：计费规则 seed

新增计费规则（迁移 `000035_seed_token_call_billing_rule.up.sql`，序号紧随 sk 000034，以实际合并顺序为准），挂在现有 `token-api` 商品上：

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
- finance_consumer 无 calls 规则时返回「无匹配规则」→ 门面按「未配置按次」静默跳过（不报错、不重复扣量）。

---

## 4. 套餐预付（M2）

### 4.1 后端乙：token 套餐商品 + plan + 额度

新增 token 套餐 plan（与现有 `token-api-payg` 并列），`billing_type=usage`、`quota_json` 声明额度，供 `ProvisionService` 生成 entitlement：

```sql
-- 套餐 plan：例「100万 token 套餐」，quota_json 声明额度总量与单位
INSERT IGNORE INTO product_plans (product_id, plan_code, name, billing_type, quota_json, status)
SELECT p.id, 'token-pkg-1m', '100万 Token 套餐', 'usage',
       JSON_OBJECT('entitlement_type','token_quota','quota_total',1000000,'quota_unit','tokens'),
       'active'
FROM products p WHERE p.product_code = 'token-api';
-- 套餐售价配 product_prices（一次性预付价）；购买走现有 POST /orders + 钱包支付。
```
> 额度单位建议 **token 数**（与计费同维度，余额耗尽即拒）；金额(CNY)为备选，待 PM 确认（roadmap §9 #9）。

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
- 事务内 `FindByIDForUpdate` 锁行 → 校验 `status=active` 且 `quota_used + amount <= quota_total` → `ConsumeQuota`（已有 `SELECT FOR UPDATE`）。
- **幂等**：以 `idempotency_key` 去重（建议新增 `entitlement_consume_logs` 表或复用消费流水），重复请求返回首次结果，不二次扣减。
- 余额不足：返回业务错误（如 `code=60002 套餐额度不足`），门面据此拒绝/降级。
- 归属校验：`entitlement.user_id == req.user_id`，否则 40003。

**C. 余额查询**（门面前置闸用，可复用现有 §10.3「我的权益额度」或加内部查询）：返回 `remaining = quota_total - quota_used`。

### 4.3 后端丁：门面计费路由（postpaid vs prepaid）

`forward_service` 按 `billing_mode`（来自 sk/上下文，见 sk 契约 §5）分流：

```
结算阶段（读到 usage 后）：
  if billing_mode == "postpaid":
      reporter.Report(input_tokens 事件) / (output_tokens 事件)   // 现状·钱包
      reporter.Report(calls 事件)                                // 若配按次
  if billing_mode == "prepaid":
      amount = 按 token_models.product 的套餐折算规则计算消耗额度（token 数：input+output 或加权）
      POST /api/internal/entitlement-consume(entitlement_id=source_id, amount, key=request_id:quota)
```
- `source_id`（= entitlement_id）来自 sk 的 `ResolveKey` 结果。
- **前置余额闸**：转发前查 entitlement remaining > 阈值，不足直接拒（防透支，与 sk 契约 §9 一致）。
- prepaid 模式下**不走钱包**，不上报 product-usage-events。
- 写 `token_usage_logs` 不变（两种模式都写，`sale_amount` 记本次折算金额/额度）。

---

## 5. 任务拆分

**后端乙**
1. `000035` 按次计费规则 seed（`calls/count`）
2. token 套餐 plan（`quota_json` 声明 token 额度）+ 套餐售价
3. 校验 finance_consumer 对 `calls` 事件正常匹配扣费

**后端丙**
1. `POST /api/internal/entitlement-consume`（锁行 + 余额校验 + 幂等 + 归属）
2. 确认 `ProvisionService`/`TokenProvisioner` 套餐分支正常生成 `token_quota` entitlement
3. （可选）内部余额查询接口供门面前置闸

**后端丁**
1. 门面上报 `calls` 次数事件（一次提问 1 条，tool-use 不累加）
2. 计费路由：`postpaid`→钱包上报；`prepaid`→调丙 entitlement-consume
3. 前置余额闸（钱包/额度）+ 余额不足拒绝
4. `token_usage_logs.sale_amount` 记本次扣费

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
- 新增错误码（建议）：`60002` 套餐额度不足（与 `60001` 钱包余额不足并列）。
