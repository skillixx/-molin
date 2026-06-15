# 后端乙接口功能验收测试报告（product / order / billing / finance_consumer）

- **测试对象**：已部署到测试服务器的后端乙新增/变更接口
- **环境**：`http://8.130.9.163:8080`（main `68ac4ee`，测试库 migration v24，健康检查通过）
- **测试库**：直连 `8.130.9.163:13306`（凭据见 `infra/.env*`，此处不记录）做数据准备与结果校验
- **测试时间**：2026-06-15
- **测试账号**：
  - 管理员：`qa_b_1781517210@molin.io`（user_id=262，DB 赋 `admin` 角色，已实名）
  - 普通用户：`qa_user_…@molin.io`（user_id=263，赋自建 `qa_buyer` 角色，测试中置实名）
  - 注：注册接口**不自动分配任何默认角色**（库内无 `user` 角色），新用户 0 角色——为完成可购买场景，测试中自建 `qa_buyer` 角色并配 `can_buy`。此为 iam/auth（后端甲）范畴，仅记录不计入乙缺陷。

## 测试结论

**部分通过**。R1/R2/R3/R4/R5 的常规契约与正向/负向用例全部通过；并发资金安全（无负余额、无超扣）通过；幂等通过。但 **fix-plan 已知问题 F1（#1a 回调金额未校验、#2 O3 未限制 order_type）、F2（#3 free_quota 未扣减）经复测确认真实存在**，且额外发现 **payment-callbacks 接口明文返回/明文存储 notify_body（违反安全红线）**。其中回调金额未校验为 **P0 资金风险**，建议修复后方可上线。

---

## 一、R1 扁平分页（全部通过）

7 个列表接口均返回扁平 `{items,page,page_size,total}`，无 `list`/`pagination` 嵌套，空列表为 `[]`。

| 用例 | 接口 | 结果 | 备注 |
|---|---|---|---|
| R1-1 | `GET /api/products` | 通过 | items/page/page_size/total |
| R1-2 | `GET /api/admin/products` | 通过 | |
| R1-3 | `GET /api/orders` | 通过 | |
| R1-4 | `GET /api/admin/orders` | 通过 | |
| R1-5 | `GET /api/wallet/transactions` | 通过 | |
| R1-6 | `GET /api/admin/wallet-transactions` | 通过 | |
| R1-7 | `GET /api/admin/payment-callbacks` | 通过 | 分页结构正确（但字段含明文 notify_body，见缺陷 B-04） |
| R1-8 | 空列表 `items` 为 `[]` | 通过 | 新钱包流水返回 `[]` 非 null |

---

## 二、R2 字段契约（通过）

| 用例 | 项 | 结果 | 实际 |
|---|---|---|---|
| R2-1 | 充值响应字段 | 通过 | `POST /api/recharge/orders`（body `amount`/`payment_method`/`return_url?`）→ HTTP **201**，data 含 `order_id,order_no,amount,status,pay_url`，status=`pending`。**注意返回码为 201 而非 200**（其余创建类接口亦为 201，前端需兼容）。 |
| R2-2a | 冻结接受 `{action,amount,reason}` | 通过 | `PATCH /api/admin/users/{id}/wallet/freeze` 200，余额→冻结正确（500→450 frozen=50），解冻还原，写 freeze/unfreeze 流水。 |
| R2-2b | 冻结需 `wallet:manage` 权限 | 通过 | 普通用户调用 → 403 / code=40003。 |
| R2-3 | 批量价格/访问用 `items` 键 | 通过 | `/prices`、`/access` 接受 `items`；提交旧键 `accesses` 时被忽略（按空 items 处理，返回 200 但不写入），前端务必使用 `items`。 |
| R2-4 | 套餐列表 `product:view` 可访问 | 通过 | `GET /api/admin/products/{id}/plans` 管理员（含 product:view）200。 |

**契约偏差（非缺陷，需同步前端文档）**：
- **D-01 `/prices` body 结构与 §3 文档不符**：文档 P14 称 `items:[{product_plan_id,...}]`，**实际**为顶层 `plan_id` + `items:[{role_id?,membership_level_id?,price_amount,currency}]`（item 内**无** `product_plan_id`）。即一次只能配置一个套餐的价格。建议以代码为准更新 `full-api-design.md`/`frontend-api-reference.md`，或反之修代码。
- **D-02 创建类接口返回 201 + 完整对象**：`POST /api/admin/products` 返回 `{id,...}`（非文档所述 `product_id`），HTTP 201。

---

## 三、R3 购买闭环 + 订单支付/取消（通过，含 1 项 P0 缺陷复现）

| 用例 | 项 | 结果 | 实际 |
|---|---|---|---|
| R3-1 | 未实名购买 → 70001 | 通过 | code=70001「需要先完成实名认证」 |
| R3-2 | 缺 `Idempotency-Key` → 400 | 通过 | code=40000「缺少 Idempotency-Key 请求头」 |
| R3-3 | 已实名+余额足 → 购买成功 | 通过 | 200，status=**paid**，amount=10，扣费正确 |
| R3-4 | 同 Idempotency-Key 幂等 | 通过 | 返回原 order_id，`idempotent=true`，不重复扣费 |
| R3-5 | 无购买权限 → 40003 | 通过 | admin（无 qa_buyer 角色，已实名隔离）→ code=40003「无购买权限」 |
| R3-6 | 余额不足 → 60001 | 通过 | 高价套餐 → code=60001「余额不足」 |
| R3-9 | 非本人订单 `/pay` → 404 | 通过 | code=40004「订单不存在」 |
| R3-10 | 非本人订单 `/cancel` → 404 | 通过 | code=40004「订单不存在」 |
| O4 | 取消 pending 订单 | 通过 | 充值 pending 订单 `/cancel` → 200 `{cancelled:true}`，状态 pending→cancelled；越界重复取消 → 400 code=40900「订单状态不可取消」 |

**O3/O4 端到端验证说明**：
- **购买 P4 直接置 `paid`**（步骤 6-7 创建订单后立即钱包扣费并 MarkPaid），**无法自然产生 pending 产品订单**。因此 O3 对「pending 产品订单」的正向支付无法端到端验证。
- 唯一能自然产生的 pending 订单是**充值订单**，用其验证 O3/O4：
  - O4 取消正常（上表）。
  - **O3 对充值订单 `/pay` 暴露缺陷 B-02（fix-plan #2），见下。**

---

## 四、R4 计费规则 CRUD（全部通过）

| 用例 | 项 | 结果 | 实际 |
|---|---|---|---|
| R4-1 | 列表（扁平 + product:view） | 通过 | 扁平分页 |
| R4-2 | 商品不存在 → 404/40004 | 通过 | code=40004「关联商品不存在」 |
| R4-3 | price_amount≤0 → 40000 | 通过 | code=40000「price_amount 必须大于 0」 |
| R4-4 | 正常创建 | 通过 | 201，data 返回完整规则（含 free_quota） |
| R4-5 | 修改 | 通过 | 200 `{updated:true}` |
| R4-6 | 权限（普通用户 → 403） | 通过 | code=40003 |

---

## 五、R5 消费记录（全部通过）

| 用例 | 项 | 结果 | 实际 |
|---|---|---|---|
| R5-1 | 用户消费记录（扁平） | 通过 | `GET /api/product-consumption-records` 扁平分页 |
| R5-2 | 强制本人过滤 | 通过 | 带 `?user_id=262`（他人）仍只返回本人（263）记录，items 无他人数据 |
| R5-3 | admin 全量（wallet:view + 扁平） | 通过 | 扁平分页，含 user_id 字段 |

消费记录响应字段（C-5）齐全：`consumption_record_id,wallet_transaction_id,amount,idempotency_key`。

---

## 六、认证安全（全部通过）

| 用例 | 项 | 结果 |
|---|---|---|
| SEC-1 | 无 Token → 401 | 通过（code=40001） |
| SEC-2 | 伪造 JWT → 401 | 通过（code=40001） |
| SEC-3 | 普通用户访问 admin → 403 | 通过（code=40003） |

---

## 七、并发安全（资金安全通过，健壮性有缺陷）

**场景**：余额 100，单价 10，20 并发购买（各独立 Idempotency-Key）。

- 结果码分布：**成功 10、HTTP 500「并发更新冲突，请重试」(code=50000) 10**
- 最终余额 = **0.000000**，新增 paid 订单 = 10，**扣费一致**（10×10 = 100）
- 全库无负余额（`balance_amount<0 OR frozen_amount<0` 计数 = 0）

**结论**：核心资金不变量正确——乐观锁 + `FOR UPDATE` + 余额校验下**无超扣、无负余额**，幂等正确。但并发冲突的健壮性有缺陷（见 B-05）：3 次重试在高竞争下耗尽，向客户端返回**裸 500**，且每次冲突已先建订单 → 扣费失败后置 `failed`，遗留大量「failed」垃圾订单（本质是瞬时锁冲突而非真实业务失败）。

支付回调幂等：相同 trade_no 第二次回调入账 0（已 processed），订单仍 paid——**幂等正确**。

---

## 八、fix-plan F1~F5 复测确认

| 编号 | 描述 | 复测结论 | 证据 |
|---|---|---|---|
| **F1 #1a** | 回调金额未与订单金额校验 | **确认存在（P0）** | 100 元订单，回调 `total_amount=9999`（带 sign 过桩验签）→ HTTP 200 success，钱包**入账 9999**（440→10439），订单置 paid。源码 `payment_service.go HandleNotify` 直接用回调 amount 入账，无 `amount.Equal(order.Amount)` 校验。 |
| **F1 #2** | O3 `/pay` 未限制 order_type | **确认存在（P0）** | 对 pending **充值订单**调 `/api/orders/{id}/pay`（pay_method=wallet）→ HTTP 200 ok，钱包**扣 100**（500→400），充值订单被置 **paid**。扣钱不入账，且后续真实回调因订单非 pending 被幂等跳过 → 永久无法入账。源码 `pay_service.go Pay` 无 `order.OrderType != "product"` 守卫。 |
| **F2 #3** | 消费计费未扣减 free_quota | **确认存在（P1）** | 规则 price=3、free_quota=10：用量=5（≤额度）→ 应 0，**实扣 15**；用量=15（>额度）→ 应 15（仅超出 5×3），**实扣 45**。free_quota 完全未生效。 |
| **F2 #6** | 消费幂等并发返原结果 | **已修复/正确** | 同 idempotency_key 重发 → 200，amount 一致，余额不变。 |
| **F3 #4** | 订单列表过滤参数补全 | **正确** | `/api/orders`、`/api/admin/orders` 扁平分页正常（过滤参数未逐一穷举，结构与基本查询通过）。 |
| **F4 #5** | GetForUpdate 错误区分 NotFound | 未单独构造 DB 异常验证（需注入 DB 故障，环境不具备），**未发现回归** | 充值/冻结/解冻正常路径均正确 |
| **F5 #1b** | 真实验签（微信/支付宝） | **桩未替换（上线前必须）** | alipay verifier 仍为桩：仅校验 body 含 `sign` 字段是否存在，不做真实 RSA2 验签。无 sign → 400「签名校验失败」；任意 `sign:"fakesign"` 即通过。叠加 F1 #1a 构成「免费充值」高危链路。 |

---

## 九、缺陷清单

### B-01 【billing][P0] 支付回调金额未校验，可超额/免费充值
- **复现**：① 创建 100 元充值订单；② POST `/api/payments/notify/alipay`，body `{out_trade_no:<order_no>, trade_no:<新>, total_amount:"9999", trade_status:"TRADE_SUCCESS", sign:"fakesign"}`。
- **期望**：回调金额 ≠ 订单金额时不入账，callback 记 `ignored`，余额不变。
- **实际**：HTTP 200 success，钱包入账 **9999**，订单置 paid，callback=processed。
- **根因**：`billing/service/payment_service.go HandleNotify` 用回调报文 amount 入账，缺 `amount.Equal(order.Amount)` 校验（即 fix-plan F1 #1a）。
- **环境**：测试服 main 68ac4ee。

### B-02 【order][P0] `/orders/{id}/pay` 未限制 order_type，可对充值订单钱包扣款且不入账
- **复现**：① 创建 pending 充值订单；② POST `/api/orders/{id}/pay` body `{pay_method:"wallet"}` + Idempotency-Key。
- **期望**：拒绝（仅产品订单可走钱包支付），不扣款。
- **实际**：HTTP 200 ok，钱包扣 100，充值订单置 paid（钱凭空消失且订单永久无法被真实回调入账）。
- **根因**：`order/service/pay_service.go Pay` 缺 order_type 守卫（fix-plan F1 #2）。

### B-03 【finance_consumer][P1] 计费未扣减 free_quota，免费额度内全额计费
- **复现**：建规则 usage_type=cpu_qa price=3 free_quota=10；上报 usage_amount=5 与 15。
- **期望**：用量 ≤ 额度不扣费（amount=0）；用量 > 额度仅对超出部分计费。
- **实际**：用量 5 扣 15；用量 15 扣 45（按全量 × 单价）。
- **根因**：`consumer_service.go Handle` 计费忽略 `rule.FreeQuota`（fix-plan F2 #3）。

### B-04 【billing][P1] admin payment-callbacks 接口明文返回 notify_body，且 DB 明文存储（违反安全红线）
- **复现**：`GET /api/admin/payment-callbacks`。
- **期望**：响应**禁止**返回明文 notify_body（设计 B8 / CLAUDE.md 安全约定）；DB 应 AES-256-GCM 加密存储。
- **实际**：响应直接返回完整回调 JSON（含 sign）；DB `payment_callbacks.notify_body` 亦为明文。
- **根因（两点）**：① 测试服未配置 `NOTIFY_BODY_KEY` → `encryptNotifyBody` 降级明文存储（属环境/运维配置）；② `admin_billing_handler.go` 在响应前**主动解密并回传 notify_body**（line 175-177），与 B8「禁止回传明文」契约直接冲突（属代码设计）。即使加密生效，接口仍会暴露明文。
- **建议**：管理端列表移除 notify_body 字段（或仅返回脱敏摘要）；上线环境必须注入 `NOTIFY_BODY_KEY`。

### B-05 【billing/order][P2] 并发购买锁冲突返回裸 500 并遗留 failed 垃圾订单
- **复现**：余额 100、单价 10、20 并发购买。
- **期望**：瞬时锁冲突应重试至成功或返回明确业务码（如 409/重试提示），不应建脏订单。
- **实际**：10 成功 + 10 个 HTTP 500「并发更新冲突，请重试」，且 10 次冲突各遗留一条 `failed` 订单。**资金本身安全（余额=0，无超扣、无负余额）**，但客户端体验与订单数据整洁度受损。
- **建议**：扩大乐观锁重试上限/加退避；或将订单创建移至扣费成功之后，避免锁冲突产生 failed 脏单。

### B-06 【finance_consumer][P3] 内部上报接口未被 IP 白名单拦截（需进一步确认）
- **现象**：从测试沙箱（非测试服本机）直接 POST `/api/internal/product-usage-events`，未返回 403「IP 未在白名单」，而是正常处理并扣费。
- **说明**：可能因测试服未配置 `INTERNAL_ALLOWED_IPS`（为空时按代码逻辑「仅允许本机」，但经反代后 RemoteAddr 可能被识别为本机），或请求经 8080 反代后源 IP 丢失。**无法在外部确凿区分配置缺失 vs 反代透传问题**，记录待运维确认 `INTERNAL_ALLOWED_IPS` 是否注入及反代是否透传真实源 IP。属配置/运维范畴。

---

## 十、契约/文档同步建议（非缺陷）
- D-01：`/prices` 实际 body 为顶层 `plan_id` + `items[]`（item 无 product_plan_id），与 §3/§5.4 文档不一致——以代码或文档为准统一。
- D-02：创建类接口返回 HTTP 201 + 完整对象（`id` 而非 `product_id`），前端需兼容 201。
- 注册不分配默认角色（库无 `user` 角色）——属 iam/auth，影响新用户开箱购买体验，建议后端甲确认是否预期。

---

## 十一、上线建议

**不建议直接上线**。B-01、B-02 为 P0 资金风险（超额/免费充值、扣款不入账），B-03、B-04 为 P1（计费错误、敏感数据泄露+安全红线），F5 真实验签未接入。建议后端乙按 fix-plan F1→F2→F5 完成修复、运维注入 `NOTIFY_BODY_KEY`/`INTERNAL_ALLOWED_IPS` 后，重新提测回归。R1/R2/R3 常规契约、R4/R5、并发资金安全与幂等已通过，修复后可较快通过回归。
