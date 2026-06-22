# 测试报告 — S2-测2 回归（方案 B）(2026-06-22)

> 测试环境：测试服 main `c8dee4e`（含 #232/#233 方案 B），DB schema 000042，dirty=0，已重置。
> 上游真实可用（DeepSeek）。测试脚本：`tests/test_s2_m2_prepaid_billing.py`。
> 本轮为第三轮测试（前两轮分别针对方案 A 遗留白嫖、D-M2-02/03 修复验证）。

## 本轮测试范围

- D-M2-01/02/03 三缺陷方案 B 全量回归（最高优先级）
- 方案 B 四路径端到端验证（串行低余额/失败释放/fail-safe/并发超额）
- 并发硬验收（prepaid 无超扣、postpaid 无负余额 + 账实相符 + 无泄漏）
- M2 套餐预付（prepaid 扣额度、不扣钱包、hold 记账、sale_amount 回填）
- postpaid 预扣保证金（freeze/settle、余额不足 60001）
- M1 全链路回归（sk 生命周期、双模式鉴权、model_scope/门禁/越权、按量+按次扣费、用量查询）

## 测试结论：通过

总用例 **75 项：通过 75 / 失败 0**。三缺陷全部闭环，方案 B 核心路径全部通过，并发硬验收全部通过。

---

## 三缺陷修复验证结果（核心）

| 缺陷 | 本轮结论 | 实测数字 |
|---|---|---|
| **D-M2-01** prepaid 额度耗尽仍白嫖 | **✅ 方案 B 根治通过** | 串行低余额(quota=20)：第1次成功(used=16)，后5次全部 402/60005，不返回答案，used 永远卡 16（不增长）；并发(quota=48,N=10)：200=3,60005=7，精确等于配额允许的 K=3 次，无超扣 |
| **D-M2-02** 乐观锁冲突误报 60001 | **✅ 修复通过** | 余额充足(1000元)并发 12 次：60001=0（无误报），codes 仅 [-1]，错误码正确区分；余额不足并发：60001 正确触发，无 50301 混入 |
| **D-M2-03** 钱包并发漏扣 + hold 泄漏 | **✅ 修复通过** | N=10 并发(余额1000元)：净扣 0.1021 == consume 流水 0.1021（账实三方一致）；freeze 0.0032 == unfreeze 0.0032；frozen=0，holding 残留=0；余额非负 |

---

## 方案 B 核心断言（reserve/settle/release 三路径）

### 串行低余额根治（最高优先级）
- quota_total=20，max_tokens=16（单次预占=16）
- 第 1 次调用：reserve(16) 成功（available=20≥16），成功返回答案，quota_used=16，hold=settled
- 第 2~6 次调用：available=20-16-0=4 < 16，reserve 失败，**全部 402/60005，无答案**
- quota_used 轨迹：[16, 16, 16, 16, 16, 16]（稳定不增）
- quota_reserved=0（无在途残留），holding 残留=0
- **对比方案 A**：方案 A remaining=4>0 时 usable=true 仍放行，用户可无限免费调用；方案 B 根治。

### 预占可见性（reserve/settle 路径）
- prepaid chat 调用（max_tokens=16，上游返回 total=21）
- reserve：hold 创建 amount=16（与 max_tokens 一致）
- settle：settled_amount=16=min(actual=21, reserve=16)（封顶于预占额，不多收）
- quota_used 增量=16，quota_reserved=0（无残留），hold.status=settled
- sale_amount=16（实际净扣额度）

### 失败释放路径
- temperature=9.9 注入上游 400 失败
- release 触发：quota_used 不增（0→0），quota_reserved 归零，hold.status=released

### fail-safe（权益失效）
- cancelled entitlement → reserve 返回 60005，st=402，无答案
- 不扣额度、不占额、不扣钱包

---

## 并发硬验收实测数字

### prepaid 同一 entitlement 无白嫖窗口（方案 B 预占）
- quota_total=48（恰够 K=3 次预占，单次=16），并发 N=10
- 实测：HTTP200=3，返回答案=3，60005=7
- 成功次数精确等于配额允许的 K=3 次（无超扣），无白嫖答案
- 最终 quota_used=48（=3×16，精确耗尽），quota_reserved=0，holding 残留=0
- 不变量 used+reserved≤total 全程无违例（并发采样器未检出任何违例）
- 错误码：仅 [-1, 60005]，无 60001/60002 误用

### postpaid 无负余额 + 账实相符（预扣保证金）
- 余额 0.00112 元（约够 K=3 次 hold），并发 N=10
- 实测：成功=3，60001=7，最终余额=0.00097（非负），frozen=0.0
- 净扣 0.00015 == consume 流水 0.00015（账实相符）
- holding 残留=0（无泄漏）

### postpaid 余额充足 N=10 并发账实相符专项（D-M2-03）
- 余额 1000 元，并发 10 次全成功
- 净扣=0.1021 == consume 流水=0.1021（三方一致）
- freeze 0.0032 == unfreeze 0.0032（完整解冻）
- 结算 hold 数=10 == 成功数=10（无漏扣），frozen=0，holding=0，余额非负

---

## 红线核查

| 红线 | 结论 | 说明 |
|---|---|---|
| prepaid 绝不扣钱包 | ✅ | prepaid 所有路径钱包余额不变（50→50）；耗尽/失效/并发全部验证通过 |
| postpaid 绝不扣 entitlement（不双扣） | ✅ | postpaid 路径无 entitlement_holds 记录；prepaid 无 wallet_transactions consume 流水 |
| 错误码 60005/60001/40003/50301 正确、无 60002 误用 | ✅ | prepaid 耗尽 60005、余额不足 60001、越权 40003、可重试 50301 全部正确触发；无 60002 误用 |
| 账实相符（postpaid 并发 + hold 无泄漏） | ✅ | 两组并发净扣 == consume 流水；freeze == unfreeze；holding 残留=0，frozen 归零 |
| D-M2-01 串行/并发白嫖根治 | ✅ | available < 单次预占额时 reserve 失败，转发前被拒；不再出现"拿到答案却不扣费"的情况 |

---

## 脚本修正说明（方案 B 断言对齐，非缺陷）

脚本第三轮修正了 3 个方案 A 残留断言，对齐方案 B 设计语义：

1. **扣减额度断言**：从 `delta == actual_tokens(21)` 修正为 `delta == min(actual_tokens, reserve_amount)`（方案 B settle 封顶于预占额，不超收）。
2. **hold.settled_amount 断言**：从 `== actual_tokens(21)` 修正为 `> 0 且 <= reserve_amount`（封顶语义）。
3. **entitlement_consume_logs 断言**：方案 B 使用 `entitlement_holds` 记账，不再写 `entitlement_consume_logs`；替换为验证 entitlement_holds 新增 +1（已在 hold 新增断言中覆盖）。

---

## M2 套餐 + M1 回归结论

- **M1 回归 12/12 通过**：sk 生命周期（明文只一次、列表无 hash）、双模式鉴权（JWT/sk 通过、无凭证/无效 sk 401）、model_scope 越界 40300、门禁未开通 40300、越权吊销 40003、按量(input+output)+按次(calls)扣钱包、余额扣减、用量查询。
- **postpaid 预扣保证金 5/5 通过**：余额不足 60001 不留冻结、生成 wallet_holds、settle/released、sale_amount 回填(>0)。
- **M2 套餐核心全部通过**：prepaid sk 签发(billing_mode/source_id 归属校验)、越权签发 40003、reserve/settle 全路径验证、不扣钱包、entitlement_holds 记账、sale_amount 回填(=净扣额度)、remaining 递减。

---

## 建议

**是否允许 M2 验收通过 / 上线：是。**

- D-M2-01（方案 B 根治）、D-M2-02（50301 区分）、D-M2-03（账实相符 + 无泄漏）三缺陷全部修复并验证通过。
- 方案 B reserve/settle/release 三路径全部端到端验证，无超扣、无泄漏、无白嫖。
- 并发硬验收通过：prepaid 无超扣（精确 K 次），postpaid 无负余额，账实相符。
- M1 全链路回归无退化。
- **无新增缺陷，无遗留 P0/P1 未修复项。建议 M2 版本可进入上线审批。**
