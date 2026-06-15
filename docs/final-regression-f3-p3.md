# 最终回归报告 — 后端乙 F1~F6（新增/小修项）

- 日期：2026-06-15
- 测试环境：测试服 `http://8.130.9.163:8080`，测试库 `8.130.9.163:13306`
- 被测版本：main（部署声明 f280752；含 F1~F6 + migration 000025）
- DB 校验：`schema_migrations` MAX=**25**；`product_consumption_records.wallet_transaction_id` 列**已存在**（bigint unsigned, NULL）
- 本轮聚焦：F3 订单列表过滤(#126)、F4 unfreeze 中文文案(#125)、B-03 幂等 txid 持久化(#125 + 000025)
- 前一轮已确认 B-01~B-06 + F5 闭环（见 `docs/regression-backend-b-fixes.md`），本轮不重复。

## 总览结论

| 项 | Issue | 结论 |
|---|---|---|
| F3 订单列表过滤（O1 / O5） | #126 | ✅ 闭环 |
| F4 unfreeze 无钱包中文文案 | #125 | ✅ 闭环 |
| B-03 幂等 txid 持久化 | #125 + 000025 | ✅ 闭环 |

**最终回归结论：通过，可上线。** 本轮 3 项全部闭环；叠加上轮 B-01~B-06 + F5，**F1~F6 全量可上线**，无 P0/P1 残留。

## 测试夹具
- 管理员 user 262（admin 角色，含 `wallet:view`，token 见 `/tmp/molin-qa/admin.token`）
- 买家 user 263（qa_buyer，已实名，钱包 id=4，token 见 `/tmp/molin-qa/buyer.token`）
- 计费规则 rule id=1：product 8 / plan 6 / usage_type=`cpu_qa` / price_amount=3 / free_quota=10 / billing_mode=per_unit
- 仅修改测试数据：B-03 前把 263 钱包余额置 1000（扣费夹具）。未改表结构、未动前端/记忆/分支。

---

## F3 — 订单列表过滤（#126）✅ 闭环

### O1 用户端 `GET /api/orders`（buyer token，强制本人）
- 扁平分页：响应为 `{items,page,page_size,total}`（data 顶层），符合 D-95。
- `?order_type=recharge`：返回 total=7，items 全部 `order_type=recharge`（无 product 订单混入）。
- `?status=pending`：返回 total=1，仅 id=54（唯一 pending 单）。
- `?created_from=2026-06-01&created_to=2026-12-31`（`2006-01-02` 格式）：total=65。
- `?created_from=2026-06-01&created_to=2026-06-14`（窄区间）：total=0（06-15 单全被排除 → 上界过滤生效）。
- `?created_from=2026-06-15T21:09:00+08:00`（RFC3339）：total=17，所有 items created_at ≥ 21:09（含 54 @21:09:50）→ 下界过滤生效。
- `?created_from=abc`（非法时间）：HTTP 200、code=0、total=65（被忽略、返回全量，**未报 400**）。
- **强制本人**：buyer 带 `?user_id=262`（他人）→ total=65，items 内 distinct user_id={263}，他人 user_id 被忽略，无越权。

### O5 管理端 `GET /api/admin/orders`（admin token，order:list）
- 全量 total=65。
- `?created_from=2026-06-01&created_to=2026-06-14`：total=0（上界过滤生效）。
- `?created_from=2026-06-15T21:11:00+08:00`（RFC3339）：total=16，min created_at=21:11:46（下界过滤生效）。
- `?created_from=abc`（非法时间）：code=0、total=65（忽略、返全量、不报 400）。

**结论**：RFC3339 与 `2006-01-02` 两种格式均生效；非法时间被静默忽略而非 400；O1 强制按登录用户过滤，`user_id` query 越权无效。闭环。

---

## F4 — unfreeze 无钱包用户中文文案（#125）✅ 闭环

- 用例：对**无钱包**用户 261（DB 校验 `wallets` 无该 user_id 记录）调
  `PATCH /api/admin/users/261/wallet/freeze` body `{"action":"unfreeze","amount":"1","reason":"t"}`（admin token）。
- 实际响应：**HTTP 400** `{"code":60001,"message":"钱包不存在","data":null}`
- 验证：消息为**中文「钱包不存在」**，非 gorm 原文 `record not found`，非 500。

**结论**：闭环。

---

## B-03 — 幂等 txid 持久化（#125 + migration 000025）✅ 闭环

端点：`POST /api/internal/product-usage-events`（内部上报，IP 白名单；请求头带 `X-Internal-Token`）。

### ① 超免费额度（产生扣费）首发
- body：user 263 / product 8 / plan 6 / usage_type=cpu_qa / usage_amount=15 / idempotency_key=`qa-b03-final-probe`
- 计费：amount = price 3 ×（15 − free_quota 10）= **15**
- 响应：HTTP 200 `consumption_record_id:5, amount:"15", wallet_transaction_id:46`（txid **>0**）
- DB：record id=5 `wallet_transaction_id=46`（已落库，非 NULL）；wallet_transactions id=46（type=consume,out,amount=15,balance_after=985）

### ② 相同 idempotency_key 重发（核心断言）
- 同 key `qa-b03-final-probe` 重发 2 次：
  - 第 2 次：`consumption_record_id:5, amount:"15", wallet_transaction_id:46`
  - 第 3 次：同上，`wallet_transaction_id:46`
- **返回相同的非 0 txid（46），不再返回 0** → 验证 txid 已持久化、幂等读回正确。
- **无重复扣费**：钱包 263 余额 = 1000 − 15（probe）− 15（probe2，独立 key）= **970**，version=44；
  仅 2 笔扣费流水（46 balance_after 985 / 47 balance_after 970），多次重发未新增流水。

### ③ 免费额度内（amount=0）txid 为 0
- usage_amount=5（< free_quota 10）→ amount=0
- 响应：`consumption_record_id:7, amount:"0", wallet_transaction_id:0`
- DB：record id=7 `wallet_transaction_id=NULL`，钱包余额不变（970，version 44）
- 重发同 key 仍返回 amount=0 / txid=0（幂等一致）

**结论**：闭环。扣费事件 txid 持久化且重发幂等读回非 0；免费额度事件 txid=0、不扣费。migration 000025 列已就位并被正确写入。

---

## 备注 / 观察（非阻断）

- **内部上报接口可经公网 8080 直接访问并成功（HTTP 200）**：本环境 API 前置代理向上游注入了 `X-Real-IP=127.0.0.1`（或上游以 loopback 直连），故 IP 白名单（`INTERNAL_ALLOWED_IPS=127.0.0.1,::1`）放行了来自公网的回归请求。功能正确，但**生产部署须确认 nginx/网关已剥离客户端伪造的 X-Real-IP 且内部接口未对公网暴露**，否则白名单可被绕过。建议运维在生产侧核对。属部署/网关配置项，非后端代码缺陷，不阻断本轮上线。
- 历史消费记录（id 1~4，000025 之前产生）`wallet_transaction_id` 仍为 NULL，属遗留数据，新逻辑不回填，不影响新数据正确性。

## 测试数据残留
- 买家 263 钱包余额被置为 970（夹具充值 1000 后扣费 30）。
- 新增消费记录 id=5/6/7 与钱包流水 46/47（QA 夹具，event_id 以 `qa-b03-final-` 前缀标记）。
