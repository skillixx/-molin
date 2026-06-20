# 第二阶段设计风险与修订建议

> 状态：架构审查 v1（2026-06-21）
> 阶段：第二阶段（Token 转发售卖 + 多模型聊天工作台）
> 视角：资深架构师对已定稿规划（7 份文档）的批判性复盘，聚焦会导致线上事故/成本失控/体验硬伤的缺陷。
> 读者：后端甲/乙/丙/丁、测试、PM、运维
> 关联：`backend-stage2-architecture-roadmap.md`、`backend-sk-auth-contract.md`、`backend-token-billing-contract.md`、`backend-chat-workbench-contract.md`、`backend-stage2-task-schedule.md`、`frontend-api-reference.md` §14
> 性质：本文是风险登记 + 修订建议，不直接改既有契约；经 PM/团队拍板后，由对应契约文档吸收。

---

## 0. 结论

主线（复用底座 / 薄转发 / 三层免费 / 唯一 token 收费）清晰自洽。审查识别 12 项风险（🔴4 / 🟡6 / 🟢2），其中 4 个「不定就会出事」+ 渠道单点共 6 个决策点 **已由 PM 拍板**（§4 D1–D6，2026-06-21）。**不阻塞规划合并；D1 阻塞 M1、D5 阻塞 M2 开工**——决议由架构师吸收进对应契约后方可进入实现（§5/§6）。

---

## 1. 🔴 严重（M1 前必须定方案）

### R1. 并发透支只有「硬验收」口号，无落地解法
- **现状**：`backend-sk-auth-contract.md` §9 把「并发扣费无负余额」列为 M1/M4 硬验收，但解法只写「必要时串行/预扣估算」，未拍板。
- **根因**：LLM 流式调用持续数十秒、token 量调用前不可知，只能「调用前查余额>阈值」。余额刚好的用户并发开 N 个流式请求，全部过前置闸，结算时累计扣费 → 负余额。
- **影响**：S2-丁5（计费路由）、S2-测1/测2/测5（并发验收）。验收标准已立，实现路径为空。
- **建议（择一，推荐 a）**：
  - **(a) 预扣保证金**：按 model 的 `max_tokens × 单价` 预冻结钱包/额度，结算后多退少补。复用底座已有 `wallet_transaction.type=freeze/unfreeze`。最干净。
  - (b) 单用户并发流式上限 + 要求余额 ≥ 最坏单次成本。
- **落点**：定案后写入 billing 契约 §4.3 + sk 契约 §9。

### R2. 渠道单点，无故障转移（相对第一阶段计划倒退）
- **现状**：已核实 `forward_service` 用 `channelRepo.FindByID(tm.ChannelID)` 绑**单一渠道**，渠道停用即 `ErrChannelUnavailable`；`token_channels.priority` 字段建了从未使用。`development-execution-plan.md` 明确要求「weight + priority + 断路器熔断切换备用路由」。
- **影响**：任一上游（OpenAI/DeepSeek/Kimi）抖动 → 对应逻辑模型整体不可用，无降级。
- **建议**：`token_models` → 渠道改为**一对多**（按 priority/weight），转发失败按序重试下一渠道 + 简单熔断（连续失败拉黑 N 秒）。工作量较大。
- **落点**：若本期做 → 新增任务卡（丁，约 W6–W7）+ 改 chat-workbench/网关数据模型；若本期不做 → **必须在 roadmap §9 风险表显式登记为「已知单点 + 延后」**，不留隐含。

### R3. 插件外部成本 = 不设防的成本黑洞
- **范围澄清**：成本归属「平台承担」**已是既定红线**（billing §7 / roadmap §9 #15），本风险**不推翻成本归属**，只针对「无配额 + sk 可触发」这一**不设防**问题。
- **现状**：「插件外部 API 成本平台承担」+「Agent/插件免费」+「sk 可调 `/agents/{id}/chat` 编排端点」（§14.8）三者叠加，且插件调用无配额。
- **根因**：用户用 sk 狂调绑了付费插件的 agent，token 自付，但每轮 tool 调用的第三方付费 API 全由平台买单。
- **影响**：可被单用户刷爆平台外部账单。
- **建议**：
  - (a) plugin 调用计入限流配额（按 user/sk）；
  - (b) 付费插件设「每用户每日调用上限」；
  - (c) 重评 sk 是否开放编排端点 —— 建议**开发者直连只给透传端点 `/chat/completions`，编排端点 `/agents/{id}/chat` 限登录态**。
- **落点**：chat-workbench 契约 §5 + §14.8 鉴权列。

### R4. skill `code_exec` 把任意代码执行当普通内置函数，无沙箱
- **现状**：chat-workbench 契约把 `code_exec` 与 `web_search` 并列为 skill 示例，沙箱只字未提。
- **根因**：在服务端执行**模型生成的代码** = RCE 级风险。
- **建议**：本期**明确不做 code_exec**；示例 skill 改为无副作用的（web_search / doc_read）。若未来要做，必须独立沙箱（容器/gVisor/WASM）+ 单列安全评审。
- **落点**：chat-workbench 契约 §4 要点 + §5。

---

## 2. 🟡 中等（影响体验/正确性/可维护性）

### R5. SSE 在 usage chunk 前断开 → 漏计费
- **现状订正**：现有 `forwardStream` 在客户端断开（`w.Write` 失败）后**仍执行 logUsage + 计费**，并非完全丢失。真正漏计的是「客户端断开发生在上游尚未吐出含 usage 的末尾 chunk 之前」——此时无 usage 可记。编排多轮放大该窗口。
- **建议**：服务端始终读完上游流再结算（即使客户端断开，后端继续消费上游到拿到 usage）；仍无 usage 的成功调用按 max_tokens 兜底计费。
- **落点**：billing 契约 §4.3 + 网关转发实现规范。

### R6. 套餐耗尽不回退钱包 → 付费用户被硬卡
- prepaid sk 绑单一 entitlement，套餐耗尽/到期即失败，钱包有钱也不能用。
- **建议**：sk 增「套餐耗尽是否回退 postpaid」开关，或计费源支持「主套餐 + 兜底钱包」。
- **落点**：sk 契约 §4（IssueKey/ResolveKey 增回退标记）+ billing 契约 §4.3。

### R7. ResolveKey 热路径 DB 查询，与实时吊销矛盾，无缓存设计
- 每次 chat 都 `ResolveKey` 查 api_keys(by hash) + `IsUserBlocked`，高频打 DB；加缓存又与「封禁/吊销实时生效」冲突。
- **建议**：sk 解析结果短 TTL（如 30s）缓存 + 吊销/封禁时主动失效缓存键（复用现有封禁黑名单 Redis 机制）。
- **落点**：sk 契约 §5/§6。

### R8. entitlement 扣减幂等表仍是「建议」，未拍板
- billing 契约写「建议新增 `entitlement_consume_logs` 或复用消费流水」。不定 = prepaid 重试会重复扣额度。
- **建议**：明确建 `entitlement_consume_logs(idempotency_key uniq)`，与 postpaid 幂等对称。
- **落点**：billing 契约 §4.2 + 新增迁移（丙）。

### R9. 两层限流缺鉴权前一层
- 仅设计「按 sk 限流」（鉴权后）。无效/恶意 sk 在 ResolveKey 阶段已打 DB。
- **建议**：网关层 IP 粗粒度限流（挡未鉴权洪水）+ sk 维度限流（挡已鉴权刷量），两层。
- **落点**：S2-甲8 任务扩展 + sk 契约 §9。

### R10. token_usage_logs 高写入，无分表/归档/TTL
- 每次调用 ×(input+output[+calls]) 行，编排多轮多倍；`data-scale-sharding-plan.md` 未对接。
- **建议**：按月分表或归档 + 明细 TTL，纳入上线检查单。
- **落点**：S2-运3 上线检查单 + 运维排期。

---

## 3. 🟢 值得复议的产品/口径

### R11. 「会话历史一律前端自持」可能过激
- 后端不存对话 → 长对话请求体随历史线性膨胀（用户 token 成本不透明增长）、刷新/多端丢失，对「Gemini 式产品」是体验硬伤。
- **建议**：复议为「后端存会话元数据 + 消息（可选加密）」，隐私红线仅约束**日志不落明文**，而非禁止会话存储；或至少定长对话历史截断策略。
- **落点**：roadmap §9 #12 复议 + chat-workbench 契约 §4。

### R12. 「按次」在两个端点语义不一致
- 透传端点按次 = 每次 API 调用；编排端点按次 = 一次提问（含多轮）。同一 `calls` 规则挂同一商品，两端语义不同易混乱。
- **建议**：文档显式区分两端「次」的定义，或两类调用用不同 usage_type。
- **落点**：billing 契约 §1/§3。

---

## 4. 决策点（D1–D6，PM 已拍板 2026-06-21）

| # | 决策点 | PM 决定 | 阻塞 |
|---|---|---|---|
| D1 | 并发透支方案（R1） | ✅ **预扣保证金**（按 `model 单价 × max_tokens` 预冻结钱包，结算多退少补，复用底座 `wallet freeze/unfreeze`）+ 前置阈值闸；prepaid 侧靠 entitlement 锁行已防透支，预扣只覆盖 postpaid | 🔴 **阻塞 M1** |
| D2 | sk 是否开放编排端点（R3） | ✅ **编排端点 `/api/agents/{id}/chat` 仅登录态**；sk 只给透传端点 `/chat/completions`（外部程序=纯透传自付 token，平台编排=站内登录态） | M3（趁早定，避免回退） |
| D3 | 付费插件成本与配额（R3） | ✅ 成本归属维持 **平台承担**（已是 billing 红线，不推翻）；新增 **付费插件「每用户每日调用上限」**（保守默认如 50 次/人/日，运营可调）+ 插件调用**计入 sk/user 限流**；`plugins` 表加 `is_paid`/`daily_limit` 字段 | M3 |
| D4 | code_exec 是否本期做（R4） | ✅ **本期不做**；skill 示例改为无副作用的 web_search / doc_read。若未来做须独立沙箱 + 安全评审，第三阶段 | M3（减法） |
| D5 | entitlement 幂等表（R8） | ✅ **新建 `entitlement_consume_logs(idempotency_key UNIQUE)`**，与 postpaid 钱包幂等对称，不复用钱包流水 | 🔴 **阻塞 M2** |
| D6 | 渠道故障转移（R2） | ✅ 本期**不做**完整一对多+熔断；但**立即在 roadmap §9 显式登记「渠道单点 + priority 字段建而未用 + 延后第三阶段」**，运营/测试知情，验收不当 bug；完整方案新建第三阶段任务卡 | 登记动作本期做 |

> 决议落点见 §5 修订影响表。D1（M1）、D5（M2）阻塞开工，须在对应里程碑前由契约吸收；D2/D3/D4 属 M3，趁早定避免返工。

---

## 5. 对现有文档/任务的修订影响（拍板后执行）

| 风险 | 待改文档 | 待改/新增任务 |
|---|---|---|
| R1 | billing §4.3、sk §9 | S2-丁5 扩展（预扣/解冻逻辑）、S2-测* 验收明确；**前置依赖：后端乙 W5 第一天确认底座 `wallet freeze/unfreeze` 是否对门面暴露可调内部接口，若无需补一个（增加 S2-丁5/乙 工作量）** |
| R2 | 网关数据模型、roadmap §9 | 新增「多渠道路由+熔断」任务（丁）或登记延后 |
| R3 | chat-workbench §5、§14.8 | S2-甲8/丁10 增插件配额；§14.8 鉴权改登录态 |
| R4 | chat-workbench §4/§5 | S2-丁11 示例 skill 调整，移除 code_exec |
| R5 | billing §4.3、网关实现规范 | S2-丁5/丁13 兜底结算 |
| R6 | sk §4、billing §4.3 | S2-甲6/丁5 回退逻辑 |
| R7 | sk §5/§6 | S2-甲2/甲3 加缓存+失效 |
| R8 | billing §4.2 | 新增迁移（丙）+ S2-丙2 幂等 |
| R9 | sk §9 | S2-甲8 扩两层限流 |
| R10 | 上线检查单 | S2-运3 + 运维排期 |
| R11 | roadmap §9 #12、chat-workbench §4 | 视复议结果，可能新增会话存储任务 |
| R12 | billing §1/§3 | 文档澄清，无新代码 |

---

## 6. 下一步

1. ✅ D1–D6 已由 PM 拍板（2026-06-21，见 §4）。
2. 由架构师将决议吸收进对应契约：
   - D1 → `backend-token-billing-contract.md` §4.3 + `backend-sk-auth-contract.md` §9（预扣保证金 + 钱包 freeze 依赖）
   - D2 → `backend-chat-workbench-contract.md` §3.3 + `frontend-api-reference.md` §14.8（编排端点改「仅登录态」）
   - D3 → `backend-chat-workbench-contract.md` §5（付费插件每日上限 + 限流；`plugins` 表加 `is_paid`/`daily_limit`）
   - D4 → `backend-chat-workbench-contract.md` §4/§5（移除 code_exec 示例）
   - D5 → `backend-token-billing-contract.md` §4.2 + 新增迁移（丙，`entitlement_consume_logs`）
   - D6 → `backend-stage2-architecture-roadmap.md` §9（新增渠道单点风险行 + 第三阶段任务卡）
3. 据此调整 `backend-stage2-task-schedule.md`（扩展 S2-丁5/甲8/丁10/丁11 + 新增 D5 迁移、D6 三阶段卡），再进入实现。
4. 待办（PM 提示）：后端乙 W5 第一天确认钱包 `freeze/unfreeze` 接口可被门面调用（D1 落地前置）。
