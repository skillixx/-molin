# 回归测试报告 — 后端乙 fix-plan（F1~F6）/ 缺陷 B-01~B-06 + F5

- 日期：2026-06-15
- 测试环境：测试服 `http://8.130.9.163:8080`，测试库 v24（`schema_migrations` MAX=24），DB `8.130.9.163:13306`
- 被测版本：origin/main `5e72b81`（含 F1/F2/F5/F6 + B-06；fix-plan PR #120~#123）
- 部署 commit 与运维生成的 test-only 密钥说明一致（`/tmp/molin-test-secrets/README.md`）
- 测试者：QA（测试工程师）

## 测试账号 / 夹具（沿用上轮 QA 夹具，仅重置密码与价格）

| 用途 | user_id | 说明 |
|---|---|---|
| 管理员 | 262 (`qa_b_...@molin.io`) | admin 角色(5)，重置密码 `Test@123456`，含 `wallet:view` |
| 买家 | 263 (`qa_user_...@molin.io`) | qa_buyer 角色(30)，已实名 verified，重置密码 `Test@123456`，钱包 id=4 |
| 商品 | product 8 / plan 6 | 本轮新增 plan 6 默认价 `product_prices=10`（role/membership 均 NULL）供 B-05 |
| 计费规则 | rule id=1 | product 8 / plan 6 / `cpu_qa` / price=3 / free_quota=10（B-03 用，上轮已建） |
| 商品权限 | `product_role_access` id=6 | product 8 + role 30，can_buy=1 |

> 仅做了：重置上述两用户密码、`email_verified=1`、给 plan 6 加默认价 10、B-05 前把 263 钱包置 100。未改任何表结构、未动前端、未动主工作树/记忆/分支。

---

## 总览结论

| 缺陷 | 修复序 | 回归结论 |
|---|---|---|
| **B-01** 回调金额校验 | F1 #1a | ✅ 已闭环 |
| **B-02** O3 充值订单可被钱包支付 | F1 #2 | ✅ 已闭环 |
| **B-03** free_quota 未扣减 | F2 #3 | ✅ 已闭环 |
| **B-04** notify_body 明文/回传 | F1 #1（存储侧） | ✅ 已闭环（新数据；历史明文为遗留，见残留项） |
| **B-05** 并发裸 500 + failed 垃圾单 | F6 | ✅ 已闭环 |
| **B-06** 内部上报接口无鉴权 | B-06（X-Internal-Token） | ✅ 已闭环 |
| **F5** 真实验签（微信/支付宝） | F5 #1b | ✅ 已闭环 |
| （附）F4 钱包健壮性 unfreeze 无钱包 | F4 #5 | ✅ 通过（轻微文案瑕疵） |

**最终回归结论：通过，可上线。** 7 项全部闭环，无 P0/P1 残留。仅 2 个非阻断的轻微残留（历史明文回调数据、两处响应文案），见末尾。

---

## B-01 — 支付回调金额校验（F1 #1a）✅ 已闭环

夹具：买家 263 充值订单 id=52 `ORD20260615W6D1012D` 金额 88，钱包初始 0。

### ① 金额不符（验签通过、金额≠订单金额 → 不入账）
- 用 `alipay_private.pem` 对 `total_amount=999.00` 的报文正确签名（验签必过），订单金额为 88。
- 请求 `POST /api/payments/notify/alipay` → **HTTP 200 `success`**（按渠道协议返回成功停止重试）
- DB 证据：
  - 钱包余额 **不变**：`balance_amount=0.000000`，version 仍为 28
  - 回调记录 `payment_callbacks.status = ignored`（trade_no=`QA_B01_MISMATCH_*`）
  - 订单 52 仍 `pending`（未被错误置 paid）

### ② 金额一致（正确签名 + 金额=订单金额 → 正常入账，兼验 F5 happy-path）
- `total_amount=88` 正确签名 → **HTTP 200 `success`**
- 钱包 `0 → 88.000000`（version 28→29）；订单 52 `paid`，`paid_at` 落地；回调 `processed`；写入 recharge 流水（id=33，in 88，balance_after=88）

**结论**：验签放行 + 金额护栏精确生效，金额不符绝不入账。与原报告「按回调 amount 入账、无金额比对、可超额入账」相比已彻底修复。

---

## B-02 — O3 钱包支付限制 order_type（F1 #2）✅ 已闭环

- 新建 pending **充值订单** id=54（type=recharge）；买家钱包 138。
- `POST /api/orders/54/pay` body `{"pay_method":"wallet"}` →
  - **HTTP 400 `{"code":40000,"message":"该订单不支持钱包支付"}`**
  - 钱包**不变**（138，version 30），订单 54 仍 `recharge/pending`（未扣款、未错置 paid）

**结论**：仅 product 订单可走钱包支付；充值订单被拒绝。与原报告「充值订单也能被 /pay 扣款且置 paid、导致真实回调永久无法入账」相比已修复。

---

## B-03 — 消费计费扣减 free_quota（F2 #3）✅ 已闭环

规则：price=3、free_quota=10。买家 263 钱包入测前 138。

| 场景 | 上报用量 | 期望 amount | 实际 amount | 扣费 | 钱包变化 |
|---|---|---|---|---|---|
| 额度内 | 5 | 0 | **0** | 不扣（wallet_tx_id=0） | 不变；写 record id=3（留痕+幂等） |
| 超额度 | 15 | (15-10)×3=15 | **15** | 扣 15 | 138 → 123（version 30→31），consume 流水 id=35 amount=15 |

附（F2 #6 幂等）：同 idempotency_key 重发超额度事件 → 返回原记录（record id=4，amount=15），钱包**仍为 123**（不重复扣）。
> 小瑕疵：幂等重发响应里 `wallet_transaction_id` 返回 0（返回的是原记录 ToResult，未回填原流水 ID）。金额与 record_id 一致、无重复扣费，属非阻断的展示瑕疵。

**结论**：免费额度内 0 扣费且留痕，超额仅对超出部分计费。与原报告「完全忽略 free_quota、免费额度内被全额扣费」相比已修复。

---

## B-04 — notify_body 加密 + 不回传（F1 存储侧 / NOTIFY_BODY_KEY）✅ 已闭环（新数据）

- `GET /api/admin/payment-callbacks` 响应项**不含 `notify_body`**：`items[0] | has("notify_body") = false`（字段为 id/order_id/provider/provider_trade_no/status/processed_at/...）
- DB：本轮新产生的回调（id 4/5/7）`notify_body` 为**密文**（`WbBSINza...`/`h+V0VfLP...`/`1y5dqyy...`，`LIKE '{%'`=0），NOTIFY_BODY_KEY 已在 `.env.test` 配置（AES-256-GCM）。

**结论**：API 不回传明文，DB 新数据为密文。与原报告问题相比已修复。
> 残留（非回归）：DB 中 id 1/2 为上轮 QA 未配密钥时产生的**历史明文** JSON 回调。属遗留测试数据，非当前构建缺陷；上线生产库不含此类历史数据，无需阻断。

---

## B-05 — 并发购买健壮性（F6）✅ 已闭环

设置：买家钱包 = 100，plan 6 单价 = 10，**20 并发购买**（各自独立 Idempotency-Key），基线 max(order.id)=54。

实测 HTTP 分布：
```
 10  200   （购买成功，order paid）
  4  409   {"code":50000,"message":"系统繁忙，请稍后重试"}   ← 瞬时锁冲突（非裸 500）
  6  400   {"code":60001,"message":"余额不足"}              ← 真实业务失败
```

DB 证据（id>54 的新建 product 订单）：

| 状态 | 数量 | 对应 |
|---|---|---|
| paid | 10 | 10 次成功 |
| failed | 6 | 6 次余额不足（合理保留供对账） |
| （无 pending/无 failed 残留来自 409） | 0 | **4 次瞬时冲突订单已被删除（DeletePendingTransient）** |

- 最终钱包 `balance_amount = 0.000000`（version 42）——**精确归 0，无超扣、无负余额**。
- 新建订单总数 16 = 20 − 4（瞬时冲突删除），数目自洽。
- 无任何 HTTP 500；瞬时冲突统一返回 409。

**结论**：成功次数精确等于 100/10=10；瞬时锁冲突返回 409（非裸 500）且不残留 failed 垃圾订单；真实余额不足保留 failed 订单；余额无超扣/负值。与原报告 F6 目标完全一致。

---

## B-06 — 内部上报接口鉴权（X-Internal-Token）✅ 已闭环

`POST /api/internal/product-usage-events`（直接扣钱包，必须 fail-closed）：

| 请求 | 结果 |
|---|---|
| 无 `X-Internal-Token` | **HTTP 403 `{"code":40003,"内部接口鉴权失败"}`** |
| 错误 token | **HTTP 403 `{"code":40003}`** |
| 正确 token + 合法事件 | **HTTP 200**，正常处理（见 B-03） |

> 说明：测试服经边缘 NAT，外部请求源 IP 被改写为回环（`.env.test` 已注明），故 IP 白名单（127.0.0.1）对外部请求亦放行；本接口的主闸是共享密钥 token，token 校验为常量时间比较且 fail-closed（未配置则全拒），符合设计。

**结论**：缺/错 token 一律 403，正确 token 放行。与原报告「内部接口无鉴权、任意扣款风险」相比已修复。

---

## F5 — 真实支付验签（微信 APIv3 / 支付宝 RSA2）✅ 已闭环

用 test-only 私钥模拟渠道签名，服务端用已注入 env 的对应公钥验签。

支付宝（RSA2，待签名串=按 key 字母序 `k=v&k=v`，排除 sign/sign_type）：
- 无 `sign` → **HTTP 400 `签名校验失败`**
- 垃圾 `sign`（含 sign 字段但非法签名）→ **HTTP 400**（桩实现会误放行，此处证明已真实验签）
- 正确签名后篡改金额（sign 针对 88，body 改 5000）→ **HTTP 400**（先验签即拒，钱包不变）
- 正确签名 + 金额一致 → **HTTP 200** 正常入账（见 B-01 ②）

微信（APIv3，签名串 `timestamp\nnonce\nbody\n`，RSA-SHA256，±5min 时间窗）：
- 缺签名头 → **HTTP 400**
- 错误签名 → **HTTP 400**
- 正确私钥签名（`--data-binary` 字节级一致）+ total_fee=5000 分 → **HTTP 200**，钱包 `88 → 138`（50 元 = 5000 分换算正确）

附（回调幂等）：对已 processed 的支付宝回调原样重发 → HTTP 200，钱包**仍 88**（不重复入账）。

**结论**：非法/伪造/篡改签名一律 400；仅合法签名回调入账。配合 B-01 金额护栏，「免费充值」风险已彻底关闭。与原报告「verifier 仅检查 sign 字段是否存在、无真实验签」相比已修复。

---

## 附：F4 钱包健壮性（#5）✅ 通过

- 对**无钱包**用户（user_id=1）`PATCH /api/admin/users/1/wallet/freeze` action=unfreeze →
  **HTTP 400 `{"code":60001,"message":"record not found"}`**（业务错误，非裸 500）。
- 健壮性目标达成。
> 小瑕疵：错误文案为 `record not found`（gorm 原文），建议改为中文业务文案（如「钱包不存在」），非阻断。

---

## 与原报告对比小结

原 6 个缺陷的根因（按回调 amount 入账无比对、充值订单可被钱包支付、忽略 free_quota、notify_body 明文回传、并发裸 500+failed 垃圾单、内部接口无鉴权）+ F5 验签桩，本轮全部复测**均已修复且行为符合 fix-plan 验收标准**。

## 残留问题（均非阻断，不影响上线）

1. **[P3][数据] 历史明文回调**：测试库 `payment_callbacks` id 1/2 为上轮未配 NOTIFY_BODY_KEY 时产生的明文 JSON。属遗留测试数据，非当前构建缺陷。建议测试库清理或忽略；生产无此历史。
2. **[P3][文案] F4 unfreeze 无钱包**：返回 `record not found`（gorm 原文），建议改中文业务文案。
3. **[P3][展示] B-03/F2 幂等重发**：响应 `wallet_transaction_id` 返回 0（未回填原流水 ID）。无重复扣费、金额/record_id 正确，仅展示瑕疵。

## 上线建议

**允许本周合并上线：是。** B-01~B-06 + F5 共 7 项全部闭环，资金硬护栏（金额校验、验签、并发归 0、内部鉴权 fail-closed）均验证有效，无 P0/P1 残留。3 项 P3 残留可作为后续清理/打磨项跟踪，不阻断上线。

---

### 阻塞 / 说明
- 内部上报接口（B-06/B-03）原计划需在测试服 localhost 执行（IP 白名单），但测试服边缘 NAT 将外部源 IP 改写为回环，故从构建机直连公网 IP 即可通过 IP 白名单，未受阻。
- 全程未改任何代码；未动主工作树前端在制内容、`.claude/memory`、stash、agent worktree。报告仅写入 `/tmp/molin-qa/`。
