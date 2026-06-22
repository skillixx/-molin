# M2 接口手动测试文档（apiPost 用）

> 范围：第二阶段 M2（套餐预付闭环）已完成接口。
> 用途：在 apiPost / Postman 里手动测试。所有请求统一返回 `{code,message,data}`，`code=0` 为成功。
> 关系：本文是 `m1-manual-test-apipost.md` 的续篇——M1（按量/按次 postpaid + sk 生命周期）请先按 M1 文档测；本文聚焦 **M2 新增能力**。
> 日期：2026-06-22 ｜ 对应测试服 main `c8dee4e`（方案 B，DB schema 000042）

---

## 0. M2 在测什么（先读这段）

M2 = **预付套餐（prepaid）**，相对 M1（postpaid 按量/按次扣钱包）的核心差异：

| 维度 | M1 postpaid（按量/按次） | M2 prepaid（套餐预付） |
|---|---|---|
| 计费载体 | 钱包余额（CNY） | 套餐额度 entitlement（token 数） |
| sk 类型 | `billing_mode=postpaid` | `billing_mode=prepaid` + `source_id`（绑权益） |
| 扣费时机 | 调用后扣钱包 | 转发前**预占**(reserve)，结算时**多退少补**(settle)，失败**释放**(release) |
| 额度耗尽 | 余额不足 `60001` | 权益额度不足 `60005`（**绝不返回免费答案**） |
| 红线 | 不扣 entitlement | **绝不扣钱包** |

**本文要验的 4 条主线**：
1. 买套餐 → 拿到 `token_quota` 权益 → 签发 prepaid sk（绑 `source_id`）
2. prepaid sk 对话：**扣额度、不扣钱包**，`quota_used` 递增
3. 额度耗尽：返回 `60005`、**不给答案**、`quota_used` 不再增长（方案 B 根治白嫖）
4. 越权 / 互斥 / postpaid 预扣保证金（`freeze` 不泄漏，余额不足 `60001`）

---

## 1. 环境与连接

### apiPost 环境变量（在 M1 那套基础上新增）

| 变量 | 示例值 | 说明 |
|---|---|---|
| `base_url` | `http://localhost:8080` | API 地址（连接方式同 M1 文档 §0） |
| `token` | （登录后填） | 登录态 JWT |
| `sk_prepaid` | （签发后填） | **prepaid** sk 明文（仅创建时返回一次） |
| `entitlement_id` | （查权益后填） | `token_quota` 权益 ID，= prepaid sk 的 `source_id` |
| `pkg_plan_id` | （查套餐后填） | token 套餐 plan_id（`token-pkg-1m`） |
| `token_product_id` | （查商品后填） | token-api 商品 ID（购买路径用） |
| `internal_token` | （运维提供） | `INTERNAL_API_TOKEN`，仅测内部接口 §4 用 |

> 连接方式（本地起后端 / SSH 隧道连测试服）与请求头约定见 `m1-manual-test-apipost.md` §0，不再重复。

### 拿测试数据的两条路

- **完整购买路径**（贴近真实，§2）：查套餐 → 购买（扣钱包 99 元）→ 自动开通生成权益。
  - ⚠️ 购买要求**已实名**（否则 `70001`）且**钱包余额 ≥ 售价**（否则 `60001`）。
- **DBA seed 最快路**（推荐做并发/耗尽用例）：请运维直接插一条 `user_entitlements`
  （`entitlement_type=token_quota`、`status=active`、`quota_total` 设小一点如 20、`quota_used=0`、`expires_at` 给未来时间），
  拿到 `id` 当 `entitlement_id`，跳过 §2 直接到 §3 签发 prepaid sk。S2-测2 并发脚本即用此法控量。

---

## 2. 套餐购买路径（拿到 token_quota 权益）

> 图例：🔑 需 `Bearer {{token}}`（登录态）｜ 🆔 需 `Bearer {{sk_prepaid}}`（prepaid sk）｜ 🔒 内部接口（X-Internal-Token）

### 2.1 找到 token 套餐的 plan_id 🔑
- **GET** `{{base_url}}/api/products?page=1&page_size=50`
- 在 `data.items` 里找 `product_code=token-api` 的商品 → 记其 `id` 填 `token_product_id`。
- **GET** `{{base_url}}/api/products/{{token_product_id}}/plans`
- 在套餐列表里找 `plan_code=token-pkg-1m`（100万 Token 套餐）→ 记其 `id` 填 `pkg_plan_id`。
- **断言**：该 plan 的 `quota_json` 含 `entitlement_type=token_quota`、`quota_total=1000000`、`quota_unit=tokens`、`valid_days=365`；`user_price` 应为 99.00 CNY（占位价）。

### 2.2 购买套餐 🔑
- **POST** `{{base_url}}/api/products/{{token_product_id}}/purchase`
- Headers：`Authorization: Bearer {{token}}`，`Content-Type: application/json`，**`Idempotency-Key: <自取一个唯一串，如 m2-buy-001>`**（缺则 `40000`）
- Body：
  ```json
  { "plan_id": {{pkg_plan_id}}, "quantity": 1, "remark": "M2手测购买套餐" }
  ```
- 预期：`code=0`，`data` 含 `order_id`（已支付）。钱包被扣 99 元。
- **断言**：再查 `GET /api/wallet` 余额减少 99；`GET /api/wallet/transactions` 有一条消费流水。
- 失败排查：`70001`=未实名；`60001`=余额不足（先 `POST /api/recharge/orders` 充值）；`40003`=无购买权限；`40000`=缺 Idempotency-Key / plan_id。

### 2.3 查我的权益，拿 entitlement_id 🔑
- **GET** `{{base_url}}/api/my/entitlements`
- 预期 `data.items[]`（扁平分页）含刚开通的权益：
  ```json
  { "id": 88, "entitlement_type": "token_quota", "product_id": 5,
    "quota_total": "1000000", "quota_used": "0", "quota_unit": "tokens",
    "status": "active", "expires_at": "2027-06-22T..." }
  ```
- **断言**：存在一条 `entitlement_type=token_quota`、`status=active` 的权益 → **把它的 `id` 填入环境变量 `entitlement_id`**（下一步当 `source_id` 用）。

---

## 3. M2 核心：prepaid sk 全链路

### 3.1 签发 prepaid sk（绑套餐权益）🔑
- **POST** `{{base_url}}/api/keys`
- Headers：`Authorization: Bearer {{token}}`，`Content-Type: application/json`
- Body：
  ```json
  { "name": "我的预付Key", "model_scope": [], "billing_mode": "prepaid", "source_id": {{entitlement_id}} }
  ```
- 预期 `data`：
  ```json
  { "id": 21, "name": "我的预付Key", "key_prefix": "sk-molin-XxYy",
    "secret_key": "sk-molin-XxYy....（完整明文，只此一次）",
    "billing_mode": "prepaid", "source_id": 88, "model_scope": [], "status": "active", "created_at": "..." }
  ```
- **断言**：`billing_mode=prepaid` 且回显 `source_id` == `entitlement_id` → **把 `secret_key` 填入 `sk_prepaid`**。
- 失败排查：`40000`「prepaid 模式必须提供 source_id」=漏传 source_id；`40000`「billing_mode 非法」=拼写错；`40003`「套餐权益不存在、已失效或不属于当前用户」=见 3.6 越权用例。

### 3.2 prepaid 对话：扣额度、不扣钱包（核心）🆔
- 先记录基线：`GET /api/my/entitlements` 看 `quota_used`（应为 0），`GET /api/wallet` 看余额。
- **POST** `{{base_url}}/api/token/chat/completions`
- Headers：`Authorization: Bearer {{sk_prepaid}}`，`Content-Type: application/json`
- Body：
  ```json
  { "model": "DeepSeek", "messages": [ { "role": "user", "content": "你好，一句话自我介绍" } ], "stream": false }
  ```
- 预期：HTTP 200，透传上游 OpenAI 格式响应（含 `choices`、`usage`）。
- **断言（M2 红线）**：
  1. 调用后 `GET /api/my/entitlements` 的 `quota_used` **增加**（增量 = 本次净扣额度，方案 B 封顶于预占额 max_tokens）。
  2. `GET /api/wallet` 余额**完全不变**（prepaid 绝不扣钱包）。
  3. `GET /api/token/usage` 能看到本次记录，`sale_amount` = 实际扣减的 **token 数**（注意 prepaid 下 sale_amount 是 token 数量纲，非 CNY）。

### 3.3 查权益余额递减 🔑
- 重复几次 3.2，每次后 `GET /api/my/entitlements`，确认 `quota_used` 单调递增、`quota_total - quota_used` 单调递减。

### 3.4 额度耗尽 → 60005、不给答案（方案 B 根治白嫖）🆔
- 前置：用一个 **`quota_total` 很小**的权益（DBA seed `quota_total=20`，或把上面套餐打到接近耗尽）。
- 连续调用 3.2 直到额度不足：
- 预期：HTTP 402（业务码 **`60005`**「权益额度不足」），**响应体不含 `choices`/答案**。
- **断言（最关键）**：
  1. 失败返回 `60005`，**没有任何模型回答**（对比方案 A 的白嫖 bug：余额>0 仍放行返回 200 答案）。
  2. 失败后 `quota_used` **不再增长**（预占失败在转发前被拒，未消耗上游、未计费）。
  3. `quota_reserved` 回到 0（无在途残留，可由 §4.5 内部余额接口观测）。

### 3.5 失败释放路径（可选）🆔
- 触发上游失败（如 Body 加 `"temperature": 9.9` 让上游返回 400）：
- 预期：返回上游错误（`50200`/相应错误），但 **`quota_used` 不增长**（reserve 后失败 → release 回滚预占）。

### 3.6 越权签发（负向）🔑
- 用 **用户 A 的 token**，`source_id` 填 **用户 B 名下**（或不存在 / 已 cancelled）的 entitlement_id 调 3.1：
- 预期：`40003`「套餐权益不存在、已失效或不属于当前用户」（归属/有效性校验，复用 40003 不用 40004）。

### 3.7 postpaid / prepaid 互斥 & 预扣保证金（回归）
- **postpaid 预扣保证金**：用 postpaid sk（M1 §2.1 那把）对话，余额充足时正常扣费；**余额不足**时返回 `60001`，且**不留冻结**（`GET /api/wallet` 的 frozen 应归零，无 hold 泄漏）。
- **互斥红线**：postpaid 调用**不扣** entitlement；prepaid 调用**不扣**钱包（两者各查一次确认对方载体未动）。
- **50301**（系统繁忙/可重试，HTTP 503）：高并发乐观锁冲突时可能出现，属可重试，非余额不足——不要和 `60001` 混淆。

---

## 4. 内部接口（仅供门面/运维联调，需 X-Internal-Token）🔒

> 这些是 `/api/internal/*`，**不对外公开**：`X-Internal-Token` 主闸（fail-closed）+ IP 白名单辅助。
> 普通业务测试**不需要**碰这些——门面（token 网关）会在 prepaid 链路自动调用。仅当你要单独验额度账本时用。
> 统一 Header：`X-Internal-Token: {{internal_token}}`，`Content-Type: application/json`。鉴权失败一律 `40003`。

### 4.1 预占 `POST /api/internal/entitlement-reserve`
- Body：`{ "entitlement_id": 88, "user_id": 12, "amount": 16, "idempotency_key": "req_demo:reserve" }`
- 预期 `data`：`{ "hold_id": .., "reserved": "16", "available": "...", "status": "holding" }`
- 错误码：`60005` 额度不足/权益不可用；`40003` 归属不符；`40400` 权益不存在；`40000` 参数错。

### 4.2 结算 `POST /api/internal/entitlement-settle`（多退少补）
- Body：`{ "hold_id": 已占的hold, "actual_amount": 21 }`（`hold_id` 与 `idempotency_key` 二选一，优先 hold_id）
- 预期 `data`：`{ "status": "settled", "settled_amount": "16", "quota_used": "16", "quota_reserved": "0", "available": "..." }`
  - 注意：`settled_amount = min(actual_amount, reserved)`（封顶预占额，不超收）。

### 4.3 释放 `POST /api/internal/entitlement-release`（失败路径，不计 used）
- Body：`{ "hold_id": 已占的hold }`
- 预期 `data`：`{ "status": "released", "settled_amount": "0", "quota_used": 未增, "quota_reserved": "0" }`

### 4.4 直接消耗 `POST /api/internal/entitlement-consume`（M2 早期接口，幂等）
- Body：`{ "entitlement_id": 88, "amount": 10, "idempotency_key": "req_demo:10", "user_id": 12 }`
- 预期：`{ "quota_used": .., "remaining": .., "status": "active" }`；同 `idempotency_key` 重放不重复扣（幂等）。

### 4.5 余额查询 `GET /api/internal/entitlement-balance?entitlement_id=88&user_id=12`
- 预期 `data`：`{ "quota_total":"...", "quota_used":"...", "quota_reserved":"...", "remaining":"...", "status":"active", "usable":true }`
  - `remaining = quota_total - quota_used - quota_reserved`；`usable` 综合 active + 未过期 + remaining>0。
- 用途：验证 3.4 耗尽后 `quota_reserved` 归零、`usable=false`。

---

## 5. 推荐测试顺序（端到端串一遍）

```
1.  登录拿 token（必要时先实名 + 充钱包 ≥99）
2.1 查 token-api 商品 + token-pkg-1m 套餐 → 记 token_product_id / pkg_plan_id
2.2 购买套餐（带 Idempotency-Key）→ 钱包扣 99
2.3 查我的权益 → 记 entitlement_id（token_quota / active）
3.1 签发 prepaid sk（绑 source_id）→ 存 sk_prepaid
3.2 prepaid 对话 → 验「扣额度、不扣钱包」
3.3 重复几次 → 验 quota_used 递增
3.4 额度耗尽 → 60005、无答案、quota_used 不增（方案 B 根治，最关键）
3.6 越权签发 → 40003
3.7 postpaid 余额不足 → 60001 无冻结泄漏；互斥红线
4.* （可选）内部接口单独验额度账本
```

---

## 6. 错误码对照（M2 相关）

| code | HTTP | 含义 |
|---|---|---|
| 0 | 200/201 | 成功 |
| 40000 | 400 | 参数错误（缺 source_id / 缺 Idempotency-Key / billing_mode 非法 / plan_id 必填） |
| 40003 | 403 | 越权（prepaid 绑非己权益）/ 无购买权限 / 内部接口鉴权失败 |
| 40400 | 404 | 权益 / 预占记录不存在 |
| 50200 | 502 | 上游模型失败（失败路径触发 release） |
| 50301 | 503 | 系统繁忙、可重试（乐观锁冲突，**勿与 60001 混淆**） |
| 60001 | 400（购买路径）/ 402（对话路径） | 钱包余额不足：购买套餐 `POST /purchase` 返 400；postpaid 对话 `POST /chat/completions` 返 402（验 §3.7 时按 402 断言） |
| 60005 | 402 | **权益额度不足**（prepaid 套餐额度耗尽/失效，复用此码，**禁用 60002**） |
| 70001 | 400 | 需先完成实名认证（购买前置） |

---

## 7. M2 接口清单速查

| 接口 | 方法 | 鉴权 | M2 关注点 |
|---|---|---|---|
| `/api/products`、`/api/products/{id}/plans` | GET | 登录态 | 找 token-pkg-1m 套餐 |
| `/api/products/{id}/purchase` | POST | 登录态 | 买套餐（需 Idempotency-Key + 实名 + 余额） |
| `/api/my/entitlements` | GET | 登录态 | 拿 token_quota 权益 id（= source_id） |
| `/api/keys` | POST | 登录态 | **新增 billing_mode/source_id**，签发 prepaid sk |
| `/api/token/chat/completions` | POST | sk | prepaid：扣额度不扣钱包；耗尽 60005 |
| `/api/token/usage` | GET | 登录态/sk | prepaid 下 sale_amount=token 数量纲 |
| `/api/internal/entitlement-reserve\|settle\|release\|consume\|balance` | POST/GET | X-Internal-Token | 额度账本（门面自动调用，单独验账用） |

> 字段/错误码若与实现不符，以代码为准并回写本文 + `frontend-api-reference.md` §14（接口字段变更未同步为本项目反复出现根因）。
