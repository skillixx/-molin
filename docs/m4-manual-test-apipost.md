# M4 接口手动测试文档（apiPost 用）

> 范围：第二阶段 **M4 整合验收**——把 M1（postpaid 按量/按次）+ M2（prepaid 套餐预付）+ M3（工作台 tool-use 编排）**串起来**手测，验「合在一起」的跨切面红线。
> **M4 不引入新接口**：用的还是 M1/M2/M3 的接口，单接口细节请回看 `m1-manual-test-apipost.md` / `m2-manual-test-apipost.md` / `m3-manual-test-apipost.md`；本文只写**整合场景**与**怎么断言**。
> 用途：apiPost / Postman 手动测试。JSON 请求统一返回 `{code,message,data}`，`code=0` 成功；编排端点 `stream=true` 走 SSE。
> 对应：自动化报告 `docs/backend-stage2-m4-test-report.md`（B/C/D/E 四段），本文是其手测版。
> 日期：2026-06-23 ｜ 测试服 main `8c77b9c`（DB schema 000044）

---

## 0. M4 在测什么（先读这段）

M1/M2/M3 各自已单测通过。M4 验的是「**同一套系统里三种计费 + 编排同时跑，会不会互相串扣 / 并发会不会出错**」。四条主线：

| 段 | 验什么 | 核心红线 |
|---|---|---|
| **B 三计费并存** | 同一用户同时持 postpaid sk / prepaid sk / 登录态编排 | **互不串扣**：postpaid 只扣钱包、prepaid 只扣额度、编排走钱包 |
| **C 编排↔计费整合** | 一次编排多轮对话的计费 | 每轮各计 token、整次 **calls 只计 1**、净扣==各轮和、Agent/skill/插件零计费 |
| **D 并发硬验收** | 卡余额/卡额度下并发打 | **钱包绝不为负、prepaid 绝不超扣、保证金/预占无泄漏**、60001/60005/50301 三态不混淆 |
| **E 跨切面安全** | 编排端点鉴权 + 越权 + 凭证 | sk 不可调编排(401)、越权 40003、凭证不外泄 |

> 本文重点是**断言怎么看**（钱包 / 权益额度 / 用量流水三方对账）。准备数据的步骤复用 M1/M2/M3 文档。

---

## 1. 环境与前置

### apiPost 环境变量（汇总 M1/M2/M3）

| 变量 | 来源 | 说明 |
|---|---|---|
| `base_url` | — | `http://localhost:8080`（连接方式见 m1 文档 §0） |
| `token` | 用户 U 登录 | **同一用户 U** 的登录态 JWT（B/C/D/E 主角） |
| `token_b` | 用户 B 登录 | 第二个用户（验越权 §5） |
| `admin_token` | 管理员登录 + 二次验证 | 管理端用（建官方 Agent / 查管理端验越权） |
| `sk_postpaid` | M1 §2 签发 | 用户 U 的 **postpaid** sk |
| `sk_prepaid` | M2 §3.1 签发 | 用户 U 的 **prepaid** sk（绑 `source_id`） |
| `entitlement_id` | M2 §2.3 | 用户 U 的 token_quota 权益 ID（= prepaid sk 的 source_id） |
| `agent_id` | M3 §2.3 | 官方 Agent（**挂 doc_read skill**），用于编排 |
| `internal_token` | 运维 | `INTERNAL_API_TOKEN`，仅观测额度账本 §4 用 |

### 前置：把「同一用户 U」备成三载体齐全（关键）

按顺序做完，使 **用户 U 同时**具备：
1. **门禁 + 钱包**：U 已开通 token 服务资产（active），钱包有余额（如充值到 100）——见 M1 §2 前置。
2. **postpaid sk**：M1 §2.1 签 `billing_mode=postpaid` 的 sk → `sk_postpaid`。
3. **prepaid 权益 + prepaid sk**：M2 §2 买套餐（或 DBA seed 一条 `token_quota` 权益）→ `entitlement_id`；M2 §3.1 签 `billing_mode=prepaid, source_id={{entitlement_id}}` → `sk_prepaid`。
4. **官方 Agent**：M3 §2.1 建 doc_read skill → M3 §2.3 建官方 Agent 绑该 skill → `agent_id`（`default_model_code` 用测试服可用模型如 `DeepSeek`）。

> 测试服 DeepSeek 渠道/模型已配置可用。基线建议：开测前记一次 `GET /api/wallet`（余额/frozen）+ `GET /api/my/entitlements`（quota_used/total），作为后续对账锚点。

---

## 2. B 段：三计费并存互斥（核心）🔑🆔

> 目标：在**同一用户 U**上，分别用三条路径各打一次，**每打一次都查"对方载体没动"**。
> 图例：🔑 `Bearer {{token}}`（登录态）｜ 🆔 `Bearer {{sk_xxx}}`（sk）

### B1 postpaid sk → 只扣钱包 🆔
- 基线：`GET /api/wallet`（记余额 W0）、`GET /api/my/entitlements`（记 quota_used Q0）。
- **POST** `{{base_url}}/api/token/chat/completions` Header `Bearer {{sk_postpaid}}`
  ```json
  { "model": "DeepSeek", "messages": [{"role":"user","content":"一句话自我介绍"}], "stream": false }
  ```
- **断言**：
  - `GET /api/wallet` 余额 **W0 → 减少**（扣了钱包）；
  - `GET /api/my/entitlements` 的 `quota_used` **仍 = Q0**（entitlement 纹丝不动）；
  - `GET /api/token/usage` 末条 `sale_amount` = 本次钱包实扣（CNY 量纲）。

### B2 prepaid sk → 只扣额度 🆔
- 基线：再记一次钱包余额 W1、frozen F1、quota_used Q1。
- **POST** `{{base_url}}/api/token/chat/completions` Header `Bearer {{sk_prepaid}}`（Body 同上）
- **断言**：
  - `GET /api/my/entitlements` `quota_used` **Q1 → 增加**（扣了额度）；
  - `GET /api/wallet` **余额 = W1、frozen = F1**（钱包/冻结都没动）；
  - 不变量：`quota_used + quota_reserved ≤ quota_total`，结算后 `quota_reserved` 归 0（可由 §4 内部余额接口看）。

### B3 登录态编排 → 走钱包，不碰额度 🔑
- 基线：记钱包余额 W2、quota_used Q2。
- **POST** `{{base_url}}/api/agents/{{agent_id}}/chat` Header `Bearer {{token}}`
  ```json
  { "messages": [{"role":"user","content":"你好"}], "stream": false }
  ```
- **断言**：
  - `GET /api/wallet` 余额 **W2 → 减少**（编排登录态走 postpaid 钱包）；
  - `GET /api/my/entitlements` `quota_used` **仍 = Q2**（编排不碰 prepaid 额度）。

> **B 段总红线**：postpaid 不扣 entitlement、prepaid 不扣钱包/不动 frozen、编排走钱包不碰 entitlement。三方账实（usage.sale_amount / 钱包流水 / quota_used）各自自洽、互不串扣。

---

## 3. C 段：编排 ↔ 计费整合 🔑

> 目标：跑一次**会触发工具的多轮**编排，核对「每轮计 token、整次 calls 计 1、净扣==各轮和、零额外计费」。

- 基线：`GET /api/wallet` 余额 WC0。
- **POST** `{{base_url}}/api/agents/{{agent_id}}/chat` Header `Bearer {{token}}`（apiPost 关闭"格式化"以看原始 SSE）
  ```json
  { "messages": [{"role":"user","content":"读取 https://example.com 并用一句话总结"}], "stream": true }
  ```
- 预期 SSE：`event: tool_call`（doc_read）→ `event: tool_result` → `event: message` → `data: [DONE]`。
- **断言（编排计费红线）**：
  1. `GET /api/token/usage`：本次提问对应**多条**记录，`request_id` 形如 `<reqid>:r1`、`:r2`（每轮各计 token）。
  2. **calls 只 1 次**：整次提问按次计 1（非每轮）。token 用量按 usage 看；calls 维度需 DBA 查 `product_consumption_records`（`usage_type=calls` 仅 1 条 quantity=1）确认。
  3. **净扣 == 各轮 sale_amount 之和**：`GET /api/wallet`，WC0 − 现余额 = 各轮 usage.sale_amount 累加。
  4. **零额外计费**：净扣完全等于 token sale 之和，Agent/skill/插件本身不产生任何扣费。
  5. **不碰 prepaid 额度**：`GET /api/my/entitlements` quota_used 不变。

---

## 4. D 段：并发硬验收（最关键）🆔

> 目标：在**卡余额/卡额度**下并发打，验**绝不为负 / 绝不超扣 / 无泄漏 / 错误码不混淆**。
> ⚠️ 严格并发用 apiPost 较难，推荐两条路：
> - **apiPost 内置「自动化测试 / 批量循环」**：把同一请求设 N 次循环并发执行（apiPost 测试套件支持），看结果分布。
> - **退化手测（推荐）**：先把可用量卡到只够 K 次（DBA 设小余额/小 quota），然后**快速连发 N 次（N>K）**，**最终态断言**——并发安全的核心是终态不变量（余额≥0 / used+reserved≤total），不强依赖严格同时刻。
> 严格并发压力建议直接用脚本 `tests/test_s2_m4_integration.py`（D 段），手测以"卡量 + 连发 + 终态对账"为主。

### D1 postpaid 并发无负余额 🆔
- 前置：DBA 把用户 U 钱包余额设到**只够约 3 次**（如按单价×max_tokens 估到 `0.001` 级别）。
- 操作：用 `{{sk_postpaid}}` 对 `/api/token/chat/completions`（Body 带 `"max_tokens":16`）**连发 10 次**（apiPost 循环/连点）。
- **断言（终态）**：
  1. **钱包余额 ≥ 0**（绝不为负）——`GET /api/wallet`。
  2. 成功次数 ≤ 余额可负担次数；失败均为 **`60001`**（HTTP 402），**无** 60005/50301 误用。
  3. `frozen = 0`、无 holding 残留（保证金不锁死）——`GET /api/wallet` 的 frozen 应归零。
  4. 账实：初始余额 − 最终余额 == 这批成功调用的 consume 流水之和。

### D2 prepaid 并发无超扣 🆔
- 前置：DBA seed/调一条 `token_quota` 权益，`quota_total` 只够 **K 次**预占（如 `quota_total=48`、每次 max_tokens=16 → K=3），`quota_used=0`、`quota_reserved=0`、`status=active`、未过期。用它签 `sk_prepaid`。
- 操作：用 `{{sk_prepaid}}` 连发 10 次（Body 带 `"max_tokens":16`）。
- **断言（终态）**：
  1. **成功次数 = K（精确放行，无白嫖）**；其余为 **`60005`**（HTTP 402）。
  2. **`quota_used ≤ quota_total`（绝不超扣）**；不变量 `quota_used + quota_reserved ≤ quota_total` 始终成立。
  3. `quota_reserved = 0`（在途预占无泄漏）——见 §4.5 内部余额接口。
  4. **无** 60001（不与"余额不足"混淆）。

### D3 错误码三态区分（汇总断言）
| 场景 | 期望 code / HTTP | 不应出现 |
|---|---|---|
| postpaid 余额不足 | `60001` / 402 | 60005、50301 |
| prepaid 额度耗尽 | `60005` / 402 | 60001、50301 |
| 系统繁忙（乐观锁冲突重试耗尽，偶发） | `50301` / 503（可重试） | 不可伪装成 60001 |

> 观测额度账本（可选）🔒：`GET {{base_url}}/api/internal/entitlement-balance?entitlement_id={{entitlement_id}}&user_id=<U的id>`（Header `X-Internal-Token: {{internal_token}}`）→ 看 `quota_total/quota_used/quota_reserved/remaining/usable`，验 D2 终态 `quota_reserved=0`。

---

## 5. E 段：跨切面安全 🔑🆔

| 用例 | 操作 | 期望 |
|---|---|---|
| **E1 postpaid sk 调编排** | `POST /api/agents/{{agent_id}}/chat` Header `Bearer {{sk_postpaid}}` | **401**（D2：sk 不可调编排端点） |
| **E2 prepaid sk 调编排** | 同上 Header `Bearer {{sk_prepaid}}` | **401** |
| **E3 未登录调编排** | 同上 不带 Authorization | **401** |
| **E4 普通用户访问管理端** | `GET /api/admin/agents` Header `Bearer {{token}}` | **403**（40003，无权限码） |
| **E5 凭证不外泄** | `GET /api/agents/{{agent_id}}`（绑了带凭证的插件时） | 响应无 `api_key`/`secret`/`auth_config`/`credential` 字段；插件仅 `has_auth` |
| **E6 越权改官方 Agent** | `PATCH /api/agents/{{agent_id}}` Header `Bearer {{token}}` | **403**（官方对用户只读） |
| **E7 越权操作他人自建** | 用 `{{token_b}}` GET/PATCH/DELETE 用户 U 自建的 Agent | **403** |

> SSRF（doc_read 内网 URL / 插件 endpoint 内网）已在 M3 文档 §2.2/§5 覆盖，M4 不重复。

---

## 6. 推荐测试顺序（整合串一遍）

```
前置：用户 U 备齐三载体（门禁+钱包 / sk_postpaid / 权益+sk_prepaid / 官方 Agent 挂 doc_read）；记基线
2  B 三计费并存：B1 postpaid→只扣钱包 / B2 prepaid→只扣额度 / B3 编排→走钱包不碰额度（每步都查对方载体没动）
3  C 编排计费整合：多轮编排 → usage 每轮各计、calls=1、净扣==各轮和、零额外计费
4  D 并发硬验收：DBA 卡余额/卡额度 → 连发 N 次 → 终态断言（余额≥0 / used+reserved≤total / frozen=reserved=0 / 错误码三态）
5  E 跨切面安全：sk 调编排 401(E1/E2) / 未登录 401(E3) / 越权 403(E4/E6/E7) / 凭证不外泄(E5)
```

---

## 7. 错误码对照（M4 整合相关）

| code | HTTP | 含义 | 易混淆点 |
|---|---|---|---|
| 0 | 200/201 | 成功 | — |
| 40001 | 401 | 未登录 / sk 调编排端点被拒（D2） | E1/E2/E3 |
| 40003 | 403 | 越权（管理端无权限码 / 改官方或他人 Agent） | E4/E6/E7 |
| 60001 | 402 | **钱包余额不足**（postpaid） | 勿与 60005 混 |
| 60005 | 402 | **权益额度不足**（prepaid 套餐耗尽，复用此码禁用 60002） | 勿与 60001 混 |
| 50301 | 503 | 系统繁忙、可重试（乐观锁冲突） | **绝不**伪装成 60001 |
| 50200 / 50300 | 502 / 503 | 上游失败 / 渠道不可用 | 编排中途出错走 SSE `event: error` |

---

## 8. 整合红线速查（必过 checklist）

- [ ] **B** postpaid 调用：钱包减、entitlement 不动
- [ ] **B** prepaid 调用：额度减、钱包/frozen 不动
- [ ] **B** 编排调用：钱包减、entitlement 不动
- [ ] **C** 编排多轮：usage 每轮各一条（:r1/:r2），calls 整次只 1
- [ ] **C** 钱包净扣 == 各轮 sale_amount 之和（零额外计费）
- [ ] **D** postpaid 并发：余额恒 ≥0、失败全 60001、frozen 归零、无 hold 残留
- [ ] **D** prepaid 并发：成功精确 = 可负担次数、其余 60005、used≤total、reserved 归零
- [ ] **D** 60001 / 60005 / 50301 三态不混淆
- [ ] **E** postpaid/prepaid sk 调编排 → 401；未登录 → 401
- [ ] **E** 普通用户访问管理端 / 改官方 Agent / 操作他人自建 → 403
- [ ] **E** Agent 详情 / 插件响应无任何凭证明文

> 字段/错误码若与实现不符，以代码为准并回写本文 + `frontend-api-reference.md` §14（接口字段变更未同步为本项目反复出现根因）。
