# 测试报告 — S2-测5 + S2-测6 M4 整合端到端验收 (2026-06-22)

> 测试环境：测试服 `8.130.9.163:8080`，main @ `6219fdc`（PR #238），DB schema_migrations **version=44, dirty=0**。
> 上游真实可用（DeepSeek active）。env 已注入 `PLUGIN_SECRET_KEY` / `MAX_ROUNDS=5` / `PLUGIN_DOMAIN_WHITELIST`（空，仅按网段拦内网）。
> 整合测试脚本：`tests/test_s2_m4_integration.py`（仅标准库 + 命令行 mysql；账号/双重认证/seed 沿用 M1/M2/M3 套路）。
> 子里程碑回归脚本：`tests/test_s2_m1_token_sk.py` / `tests/test_s2_m2_prepaid_billing.py` / `tests/test_s2_m3_workbench.py`。
> 验收基准：`docs/backend-token-billing-contract.md`、`docs/backend-chat-workbench-contract.md`、`docs/backend-sk-auth-contract.md`、`docs/frontend-api-reference.md` §14、`docs/backend-stage2-master-tracking.md` §2.7/§3.3。

## 本轮验收定位

M1（postpaid 按量/按次）+ M2（prepaid 套餐预付）+ M3（工作台 tool-use 编排）三子里程碑各自单测已通过（M1 44/44、M2 75/75、M3 85/85）。本轮（S2-测5）验「**合在一起**」的跨切面正确性与硬红线，不重复逐里程碑深测——那部分由三个既有脚本回归覆盖。

## 测试结论：**通过（整合验收通过，建议允许第二阶段验收上线）**

| 阶段 | 用例数 | 结果 |
|---|---|---|
| M1 回归（既有脚本，确认合并无破坏） | 44 | **44 / 0** |
| M2 回归（含并发硬验收，方案 B） | 75 | **75 / 0** |
| M3 回归（工作台 tool-use 编排） | 85 | **85 / 0** |
| **M4 整合（本轮新增，B/C/D/E 跨切面）** | **62** | **62 / 0** |
| **合计** | **266** | **266 / 0** |

**无 P0 / P1 / P2 / P3 缺陷。** 三计费模式并存互斥红线、编排↔计费整合、并发无负余额/无超扣、跨切面安全均满足契约。详见 §S2-测6 缺陷跟踪表。

---

## A. 子里程碑回归（合并后无相互破坏）

main @ `6219fdc` 上三脚本全绿，确认 M1/M2/M3 合并到一处后无相互破坏：

| 脚本 | 结果 | 关键覆盖 |
|---|---|---|
| `test_s2_m1_token_sk.py` | **44/44** | sk 生命周期、双模式鉴权、model_scope 越界、门禁、postpaid 按量+按次扣钱包、用量查询（用户端/管理端）、封禁联动 |
| `test_s2_m2_prepaid_billing.py` | **75/75** | prepaid 扣 entitlement（不扣钱包）、方案 B reserve→settle 多退少补、postpaid D1 预扣保证金、prepaid+postpaid 并发硬验收 |
| `test_s2_m3_workbench.py` | **85/85** | 三模块 CRUD/绑定/越权、SSE 编排、D2 边界、多轮+工具失败降级、MAX_ROUNDS、运行时 SSRF、付费插件日上限、计费正确性 |

---

## B. 三计费模式并存正确性（核心，互斥红线）

**关键设计**：同一用户 U 同时持有三种计费载体——postpaid sk（扣钱包）、prepaid sk（绑 entitlement 扣额度）、登录态编排（postpaid 走钱包）。在一个用户上下文中验证三载体互不串扣。

| 用例 | 结论 | 关键实测 |
|---|---|---|
| 准备 | ✅ | 同一用户开门禁 + 钱包(100) + prepaid 权益(quota=100万)；签 postpaid sk(mode=postpaid) + prepaid sk(mode=prepaid, source_id=ent) |
| **B1 postpaid → 钱包** | ✅ | 余额 `100.0 → 99.98963`；**entitlement quota_used 不变(0→0)、reserved 不变(0→0)**；账实：钱包净扣 0.01037 == consume 流水 0.01037 |
| **B2 prepaid → entitlement** | ✅ | quota_used `0 → 32`；**钱包余额不变、frozen 不变、无 wallet_holds holding**；不变量 used+reserved≤total；settle 后 reserved 归 0 |
| **B3 编排（登录态 postpaid）→ 钱包** | ✅ | 余额 `99.98963 → 99.97609`；**entitlement quota_used 不变(32→32)** |

**互斥红线全部成立**：postpaid 不扣 entitlement；prepaid 不扣钱包/不动 freeze；编排走钱包不碰 entitlement。账实三方对账（token_usage_logs.sale_amount / wallet_transactions consume / entitlement quota）一致。

---

## C. tool-use 编排 ↔ 计费整合

| 用例 | 结论 | 关键实测 |
|---|---|---|
| 编排请求 | ✅ | 真实触发 `tool_call→tool_result→message→[DONE]`（DeepSeek 实际调用 doc_read 抓 https://example.com） |
| **每轮计 token** | ✅ | token_usage_logs **2 条**：`req_…:r1`、`req_…:r2`（多轮各独立计 token） |
| **整次 calls=1** | ✅ | product_consumption_records usage_type=calls **quantity=1**（多轮只计 1 次，非每轮） |
| **净扣 == 各轮和** | ✅ | 钱包净扣 0.02106 == 各轮 sale_amount 之和 0.02106 == consume 流水 0.02106 |
| **Agent/skill/插件零计费** | ✅ | 唯一扣费载体 = token；净扣完全等于 sale_sum，无额外扣费 |
| **编排不碰 prepaid entitlement** | ✅ | 同一用户另持 prepaid 权益，编排后 quota_used 不变(0→0) |

> D2 边界、MAX_ROUNDS、工具失败降级由 M3 脚本（85/85）覆盖，本轮 E 段复验 D2。

---

## D. 并发硬验收（最关键）

### D1 postpaid 并发无负余额

钱包余额 `0.00112`（约够 3 次），10 并发各调用 max_tokens=16：

| 断言 | 结论 | 实测 |
|---|---|---|
| **钱包余额绝不为负** | ✅ | 最终余额 `0.00097`（≥0） |
| 成功受余额约束 | ✅ | 成功 **3** / 失败 7 |
| 余额不足组 = 60001 | ✅ | **60001 × 7**，无 60002/60005/50301 误用 |
| 冻结额归零（无锁死保证金） | ✅ | frozen=0 |
| 无 holding 残留 | ✅ | wallet_holds holding=0 |
| 账实相符 | ✅ | 初始-最终余额 0.00015 == consume 流水 0.00015 |

### D2 prepaid 并发无超扣

entitlement quota_total=48（只够 K=3 次预占×16），10 并发：

| 断言 | 结论 | 实测 |
|---|---|---|
| **成功 ≤ K（精确放行，无白嫖）** | ✅ | 成功 **3** = K |
| 超出额度 = 60005 | ✅ | **60005 × 7**，60001=0（不混淆余额不足） |
| **quota_used+reserved ≤ total 始终成立** | ✅ | used=48 reserved=0 total=48 |
| **quota_used ≤ total（绝不超扣）** | ✅ | used=48 = total=48 |
| reserved 归零（在途无泄漏） | ✅ | reserved=0 |
| 无 holding 残留 entitlement_holds | ✅ | holding=0 |

### D3 错误码三态区分

| 断言 | 结论 | 实测 |
|---|---|---|
| postpaid 余额不足 → 60001（非 60005/50301） | ✅ | D1 全为 60001 |
| prepaid 额度耗尽 → 60005（非 60001/50301） | ✅ | 额度耗尽后续调用 st=402 code=**60005** |

**60001（真余额不足）/ 60005（额度不足）/ 50301（系统繁忙可重试）三态语义清晰、并发下不混淆。** 本轮所有并发均无 50301（系统未触限流降级），全部为确定性的额度/余额拒绝。

---

## E. 跨切面安全

| 用例 | 结论 | 实测 |
|---|---|---|
| E1 postpaid sk 调编排端点 | ✅ | → **401**(40001)（D2：sk 不可调编排） |
| E2 prepaid sk 调编排端点 | ✅ | → **401**(40001)（任何 sk 都不可调编排） |
| E3 未登录调编排端点 | ✅ | → **401** |
| E4 普通用户访问管理端 | ✅ | `/api/admin/agents`、`/api/admin/token/usage` → **403**(40003) |
| E5 Agent 详情凭证不外泄 | ✅ | 响应无 api_key/secret/credential/auth_config 等字段 |
| E6 普通用户改官方 Agent | ✅ | → **403**(40003)（官方只读） |

> 配置时 + 运行时 SSRF（doc_read 内网/元数据 URL、插件 endpoint 网段拦截）由 M3 脚本用例 2/11 覆盖，已通过。

---

## 跨切面对账小结（账实一致）

| 计费路径 | 扣费载体 | 凭证 | 账实核对方式 | 结果 |
|---|---|---|---|---|
| postpaid sk | wallets（钱包） | token_usage_logs.sale_amount / wallet_transactions(consume) | 净扣==consume==sale_amount | ✅ |
| prepaid sk | user_entitlements.quota_used | entitlement_holds（方案 B） | reserve→settle，used+reserved≤total | ✅ |
| 登录态编排 | wallets（postpaid 语义） | token_usage_logs(每轮) + product_consumption_records(calls=1) | 净扣==各轮 sale 之和==consume，calls=1 | ✅ |

**三载体在同一用户上下文中互不串扣**，是本次整合验收的核心结论。

---

## 卡点说明

无。环境/账号/上游/配置齐备，所有计划用例均实测执行，无静默跳过。
（注：M3 脚本中 MAX_ROUNDS 与 SSRF doc_read 个别项依赖真实模型是否触发工具，属「软判定」，但安全拦截逻辑均有代码侧兜底，且配置侧网段拦截已硬覆盖——见 M3 报告。）

---

## 建议

是否允许第二阶段（M4 整合）验收上线：**是**

依据：266/266 全绿，无 P0/P1/P2/P3 缺陷；三计费并存互斥红线、编排计费整合、并发无负余额/无超扣、跨切面安全全部满足契约。

---

# S2-测6 缺陷跟踪表

| 编号 | 等级 | 模块 | 复现步骤 | 期望 vs 实际 | 状态 |
|---|---|---|---|---|---|
| — | — | — | 本轮 266 项用例全部通过，**未发现产品缺陷** | — | — |

**P0/P1 缺陷数：0。** 第二阶段 M4 整合验收通过，**无需回 S2-各1 修复**。

> 过程记录（均为测试脚本自身适配，非产品缺陷）：
> - 整合脚本初版 `get_official_doc_agent` 误用 `agent_skills`/`skills.skill_key`（旧设想表名/列名）；实际 schema 为 `agent_skill_bindings` + `skills.handler_key`，修正后官方 Agent 复用查询正常。属测试脚本编写适配，未改业务代码。
