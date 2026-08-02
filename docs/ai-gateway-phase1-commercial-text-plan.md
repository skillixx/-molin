# 墨灵 AI 网关 Phase 1：文字模型商业闭环开发计划

> 文档状态：Phase 0 工程参数已冻结，商业发布仍待产品、财务与合规审批
>
> 更新日期：2026-08-03
>
> 权威范围：本文是 Phase 1 唯一执行依据；`multimodal-ai-gateway-implementation-plan.md` 仅作为长期多模态蓝图。两者冲突时，Phase 1 以本文为准。
>
> 证据声明：本文描述目标设计，不代表 Bifrost、真实支付、生产上游或商业计费已经启用。

> 2026-08-03 执行层增量：已实现 `ExecutionDriver`、Native 默认驱动和 Bifrost 文字驱动，并使用 Fake Bifrost 完成契约测试。该增量只替换鉴权后上游执行层，不代表商业账本、价格引擎、内容审核、限流或生产 Bifrost 已完成；详见 `docs/bifrost-driver-development.md`。
>
> Phase 0 冻结依据：[`ai-gateway-phase0-freeze-record.md`](./ai-gateway-phase0-freeze-record.md)。

## 1. 产品结论

墨灵 Phase 1 不建设一个“大而全的多模态中转站”，而是先交付一个可向小型企业收费、可逐请求对账、可控制预算的文字模型网关。

客户使用人民币充值钱包，通过归属于 Project 的平台 SK 调用 5～8 个精选文字模型。墨灵统一完成鉴权、内容安全、价格快照、最坏成本预占、上游执行、一次终态结算、消费记录和账单解释。Bifrost 是受控的内部执行驱动，不是用户系统、预算系统或财务事实源。

```text
客户结果
  = 一个 SK 可以调用多个精选文字模型
  + 每次请求都能看到 Token、价格版本和人民币费用
  + Project 与 SK 可以设置预算和并发限制
  + 平台能够解释、追踪和修正每一笔异常账单
```

## 2. 阶段目标与商业门槛

### 2.1 工程目标

1. 对外提供 OpenAI 兼容的文字非流式和 SSE 流式接口。
2. 首批发布 5～8 个文字模型，覆盖通用对话、低价高速、复杂推理、代码和长文本场景。
3. 接入两个经过契约和计量验收的上游，每个模型配置主执行端点和受控备用端点。
4. 通过 Project 级 SK 完成模型授权、并发、RPM/TPM、预算和消费归集。
5. 建立 `ai_requests + ai_usage_items` 请求账本和不可变价格快照。
6. 复用墨灵钱包完成最坏成本预占、一次终态结算和多退，禁止产生负余额。
7. 提供模型市场、模型详情、Quick Start、用量概览、请求账本和 CSV 导出。
8. 提供模型发布、价格版本、上游路由、内容安全、并发预算、异常结算和对账后台。

### 2.2 设计客户门槛

工程开发与客户验证并行：

- 至少确认 3 家小型企业设计客户。
- 至少 2 家完成真实 API 集成，不以控制台演示代替。
- 至少 1 家使用自有资金完成真实充值，并连续使用 4 周。
- 正常请求账单差异为 0；所有异常结算均可追踪。
- 网关请求成功率不低于 99.5%。
- 单客户和单模型毛利不低于批准的最低值，Phase 1 建议底线为 15%。
- 至少 2 家设计客户明确提出图片 API 需求后，才允许评审图片阶段。

赠送余额、Mock 请求、沙箱上游成功和供应商控制台测试均不能计入真实付费证据。

### 2.3 充值开放门槛

采用两阶段方案：

1. 设计客户试点期：客户真实对公转账后，可由财务和管理员双人复核入账；必须生成充值单、钱包流水和审计记录。
2. 公开自助运营前：必须完成真实支付渠道、回调验签、幂等入账、每日对账、退款和异常补单演练。

后台直接赠送的测试金额必须标记 `fund_source=grant`，不能伪装为客户实付。

## 3. Phase 1 范围

### 3.1 必须交付

- 文字 Chat Completions，非流式与 SSE 流式。
- 可选 Responses 兼容层，但仅覆盖已验收的文字能力。
- 5～8 个公开文字模型和两个上游。
- Bifrost POC、受控主路径和原生 Go 回退驱动。
- Project、Project 预算和 Project 级 SK。
- 模型目录、能力、销售价格、Quick Start 和静态文档链接。
- 请求前文字安全检查、流式输出缓冲审核、违规累计、SK/用户暂停和申诉入口。
- 请求账本、用量项、价格快照、钱包预占、结算、释放、Outbox 和异常对账。
- 用户端消费概览、按模型/Project/SK 聚合、逐请求明细和导出。
- 管理端模型发布、价格、路由、安全、并发、预算、结算异常和监控入口。

### 3.2 NOT in scope

以下内容保留在长期蓝图，但不属于 Phase 1 开发、估时或验收：

- 图片、音频和视频生成接口：必须等待文字商业门槛通过。
- 媒体转码、抽帧、OCR、ASR 和媒体审核：随对应模态单独立项。
- 媒体对象永久资产与复杂存储套餐：Phase 1 只保留生命周期模型，不实现媒体业务。
- Embedding、Rerank：可作为后续同步能力扩展，不进入首批商业验收。
- 多地域部署和 99.99% SLA：当前客户规模不支持对应成本。
- 完整企业组织、部门、成员和复杂 RBAC：首期使用用户钱包加轻量 Project。
- 企业后付费和授信额度：首期只允许预充值，禁止负余额。
- 自动采购、上游自动充值和未经审核的自动调价：财务风险过高。
- 自建 Markdown/CMS 文档系统：使用外部静态文档注册中心。
- 开放式模型接入市场和用户自定义路由：首期模型由墨灵运营发布。
- 任意跨供应商自动故障切换：只允许能力和计量契约等价的端点切换。
- 模型排行榜、Playground、模型对比：不阻塞核心付费闭环。
- 发票自动化和复杂财务报表：先保证充值、钱包和请求账单可核对。

## 4. What already exists

| 现有能力 | 代码/数据 | Phase 1 处理 |
|---|---|---|
| 平台 SK | `auth` 模块、`api_keys`、HMAC 哈希、仅创建时显示明文 | 复用鉴权和吊销；扩展 `project_id`、有效期、预算和限制，不重建密钥系统 |
| 公开模型目录 | `token_gateway`、`token_models` | 复用模型 ID 和兼容入口；扩展能力、生命周期和执行驱动 |
| 上游渠道 | `token_channels` 与当前直连逻辑 | 迁移为版本化执行端点；密钥继续加密，不暴露给 Bifrost 用户侧 |
| 用量日志 | `token_usage_logs` | 转为只读兼容数据源；新请求不得再将其作为财务事实 |
| 钱包 | `billing`、`wallets`、`wallet_transactions` | 继续作为余额和流水事实源 |
| 钱包预占 | `wallet_holds`、`WalletHoldService` | 复用事务与锁模型；补充请求关联、严格终态和对账能力 |
| 商品与准入 | `product`、资产和实名状态 | 继续决定购买资格和服务开通，不承载动态模型计价 |
| 通用按量消费 | `finance_consumer` | 保留给其他商品；AI 新请求禁止逐指标调用该路径扣费 |
| 管理端 Token 页面 | 模型、渠道、用量页面 | 迁入统一“AI 网关”菜单，旧路由作为过渡入口 |
| 用户端 Token 页面 | SK、套餐、用量页面 | 升级为 Project、模型市场、Quick Start 和请求账本 |

### 4.1 必须先修正的现有风险

当前 `ForwardService` 存在“结果已经返回，计费失败只记录日志”的 best-effort 路径；旧消费上报还可能按多个 usage 指标分别扣费。Phase 1 不允许在该路径上继续叠加模型类型，必须先完成新的请求账本和单次终态结算。

`WalletHoldService` 当前会将 `actual > hold_amount` 封顶。Phase 1 只有在报价器能够证明最坏成本覆盖所有可计费指标时才允许调用上游；封顶不能作为隐藏亏损的常规策略，超额必须触发 P0 财务异常和模型熔断。

## 5. Dream state delta

```text
当前状态
  单渠道文字转发 + 简单 Token 日志 + best-effort 计费
        ↓ Phase 1
精选文字模型商业网关
  Project/SK + 请求账本 + 价格快照 + 可靠结算 + 可解释账单
        ↓ 12 个月目标
受控多模态 AI 商业平台
  文字/图片/音频/视频 + 数字资产 + 多上游治理 + 企业运营
```

Phase 1 建立可复用的请求、计量、价格、执行和结算抽象，但不提前实现媒体任务和资产逻辑。

## 6. 总体架构

### 6.1 组件边界

```text
用户 SDK / 墨灵用户端
          |
          v
Nginx -> 墨灵 Go API
          |
          +-- APIKey/Project/Auth
          +-- ModerationPolicy
          +-- Concurrency/Budget
          +-- RequestOrchestrator
                |
                +-- PricingQuote ---- ai_price_versions
                +-- WalletHold ------ wallets/wallet_holds
                +-- RequestLedger --- ai_requests/ai_usage_items/outbox
                +-- ProviderAdapter
                       |-- BifrostAdapter -> 私网 Bifrost -> 上游 A/B
                       `-- NativeOpenAIAdapter ----------> 上游 A/B

Outbox -> RabbitMQ -> Settlement/Reconcile Worker
Redis  -> 并发租约、速率限制、短期配置缓存
MySQL  -> 请求、价格、账单、钱包和审计事实
日志平台 -> 脱敏运行日志、指标与 Trace
```

### 6.2 唯一编排原则

`RequestOrchestrator` 是唯一允许推动请求状态的应用服务。HTTP Handler、SSE Handler 和后台补偿 Worker 必须调用同一套方法，不得复制预占、结算或释放逻辑。

建议内部接口：

```go
type RequestOrchestrator interface {
    Prepare(ctx context.Context, cmd PrepareCommand) (*PreparedRequest, error)
    Execute(ctx context.Context, requestID string, sink StreamSink) error
    Finalize(ctx context.Context, requestID string, result ExecutionResult) error
    Reconcile(ctx context.Context, requestID string) error
}
```

实现代码时所有注释使用中文。Handler 只负责协议解析和响应，业务状态迁移放在应用服务中。

### 6.3 四条数据路径

```text
Happy：鉴权 -> 审核 -> 限流 -> 报价 -> 预占 -> 执行 -> usage -> 结算 -> 返回/更新账本
Nil：  缺 SK/模型/消息 -> 参数错误 -> 不占并发、不预占、不调用上游
Empty：空 messages/空文本 -> 参数错误或安全拒绝 -> 不收费
Error：任一步失败 -> 写明确状态 -> 释放或待结算 -> Outbox/补偿 -> 用户可见错误
```

## 7. 请求与结算状态机

### 7.1 正交状态

`ai_requests` 不使用一个无限膨胀的综合状态，而是保存三个维度：

```text
moderation_status: pending -> passed | rejected | error
execution_status:  pending -> running -> succeeded | failed | cancelled | unknown
billing_status:    unquoted -> held -> settlement_pending -> settled | released | exception
```

约束：

- 未通过审核不能执行。
- 未成功预占不能调用收费上游。
- `settled` 与 `released` 互斥且只能发生一次。
- 客户端断开只改变连接事实，不直接决定账单终态。
- 终态账单不可原地改金额；更正必须写冲正流水和调整记录。
- 每次状态更新必须带旧状态条件或 `version_no`，并发失败后重新读取。

### 7.2 流式断连

客户端断开后尝试取消上游，但保留请求和预占：

```text
client_disconnected
  -> upstream_cancel_confirmed -> 按已确认 usage 结算或释放
  -> upstream_still_running    -> settlement_pending
  -> provider_usage_confirmed  -> 一次终态结算
  -> deadline_exceeded         -> billing_exception + 人工队列
```

用户端显示“结算中”，不得显示为免费或已退款。

### 7.3 上游执行结果未知

- 连接建立前失败可以安全重试。
- 上游明确未执行可以使用同一幂等键重试。
- 请求已经发送但结果未知时标记 `execution_status=unknown`。
- 上游支持幂等键或查询时，优先查询原执行结果。
- 不支持确认时不得自动跨供应商重试，进入对账队列。

## 8. 数据模型与迁移

### 8.1 复用与新增策略

采用绞杀式迁移：

- 复用 `users`、`api_keys`、`wallets`、`wallet_transactions`、`wallet_holds`、`token_models`。
- 新建请求、用量、价格、端点、Project 和 Outbox 表。
- `token_usage_logs` 只读兼容；新链路停止写入后再迁移查询。
- 不回填无法证明准确的历史价格和上游成本；历史记录明确标记 `legacy`。

### 8.2 核心表

| 表 | 关键字段 | 关键约束/索引 |
|---|---|---|
| `ai_projects` | `id,user_id,name,status,monthly_budget,budget_mode,timezone` | `(user_id,status)`；名称按用户唯一 |
| `api_keys` 扩展 | `project_id,scope_mode,model_scope,expires_at,daily_limit,monthly_limit,rpm,tpm,concurrency_limit,ip_allowlist_json` | `(project_id,status)`；密钥仍只存哈希 |
| `ai_models` 或扩展 `token_models` | 公开代码、能力、状态、`execution_driver`、文档版本 | 公开模型代码唯一；发布版本不可变 |
| `ai_provider_endpoints` | 驱动、上游、真实模型、能力、成本版本、健康状态 | 内部别名唯一；密钥仅存密文引用 |
| `ai_price_versions` | 币种、有效期、审核状态、最低毛利 | 已发布版本不可修改；生效区间不得重叠 |
| `ai_price_skus` | `price_version_id,meter_type,variant_json,unit_price,scale` | `(price_version_id,meter_type,variant_hash)` 唯一 |
| `ai_requests` | 请求身份、三类状态、价格快照、预占、结算、错误分类 | `request_id` 唯一；用户/Project/SK/时间复合索引 |
| `ai_usage_items` | 来源、计量类型、数量、单价、金额 | `(request_id,meter_type,source,sequence)` 唯一 |
| `ai_execution_attempts` | 驱动、端点、上游请求 ID、状态、耗时、usage 摘要 | `(request_id,attempt_no)` 唯一 |
| `ai_request_wallet_links` | 请求、hold、freeze/settle/release 流水 | `request_id` 唯一；钱包流水 ID 唯一 |
| `ai_outbox_events` | 聚合 ID、事件类型、payload、状态、重试 | `event_id` 唯一；`(status,next_retry_at)` 索引 |
| `ai_billing_adjustments` | 原账单、调整金额、原因、审批、流水 | 只能追加，不覆盖原记录 |

金额统一使用 `DECIMAL(20,8)` 或项目批准的更高精度；API 返回字符串。计量数量使用 Decimal，不使用 `float64` 参与财务计算。

### 8.3 迁移顺序

1. Expand：创建新表和可空字段，不改旧读写行为。
2. Shadow：新链路写新账本，旧日志只做对照，不产生第二笔钱包流水。
3. Read switch：用户端和后台查询切换到新账本。
4. Stop legacy writes：确认对账后停止新请求写 `token_usage_logs`。
5. Contract：至少跨一个稳定版本后，再评审删除旧字段或代码；Phase 1 不物理删除历史表。

现有 `api_keys.model_scope` 使用“空字符串代表不限模型”。Project Key 不得继续复用这个危险默认值，应增加显式 `scope_mode=all|allowlist`：新建 Key 默认 `allowlist`，空列表表示禁止全部模型；只有用户主动选择“全部已授权模型”时才保存 `all`。迁移旧 Key 时先标记 `legacy_all` 并要求用户确认或轮换，不能静默改变权限。

## 9. 价格与计费规范

### 9.1 价格事实

- 上游成本价和用户销售价分离。
- 每次请求锁定不可变 `price_version_id`、计价 SKU、币种和汇率快照。
- 新价格经过草稿、校验、审核、定时生效和回滚；回滚本质是发布新版本。
- 成本缺失、成本过期或预计毛利低于下限时，模型自动停止新请求。
- 上游/Bifrost 返回的 `response_cost` 只能用于成本对账，不能直接扣用户钱包。

### 9.2 分层计量

每笔请求保存：

- `provider_usage`：上游原始确认用量。
- `gateway_usage`：Bifrost或网关归一化用量。
- `estimated_usage`：本地预估用量。
- `billable_usage`：最终计费用量。
- `usage_source`：最终采用来源。

默认使用上游确认用量。只有模型和 Tokenizer 契约已经验证时，才允许在上游缺失 Usage 时使用预估计费。差异超过阈值进入异常队列，不自动掩盖。

### 9.3 最坏成本预占

Phase 1 坚持 `actual_amount <= held_amount`：

```text
held_amount = 对所有可计费输入和最大可能输出按当前价格快照求和
```

- 对无法计算最大金额的请求拒绝执行，并提示降低 `max_tokens` 或改用已支持参数。
- 不允许增量补扣、负余额或企业授信。
- 如果实际金额超过预占，停止该模型新请求，账单进入 P0 异常；不得静默封顶后继续运营。

### 9.4 单一结算入口

AI 请求不能再通过 `finance_consumer` 对 input/output 等指标逐条扣款。正确流程：

```text
价格快照 -> 钱包 hold -> 上游执行 -> 汇总 usage items
           -> 计算一个 final_amount
           -> WalletHoldService 一次 settle 或 release
           -> 同事务写请求终态、钱包关联和 Outbox
```

Outbox 消费者负责聚合、通知和对账，不再创建第二笔用户扣费。

现有 `WalletHoldService.SettleHold` 会自行开启事务。实现时必须增加受控的事务内能力，例如内部 `SettleHoldTx(tx, ...)`，再由 AI 结算应用服务使用一个 MySQL 事务完成：锁定 `ai_requests`、锁定 hold 与钱包、写钱包流水、写请求终态、写钱包关联、写 Outbox。锁顺序固定为“请求 -> hold -> 钱包”，所有同步和 Worker 路径一致，避免死锁。不得采用“先结算钱包，再 best-effort 回填请求”的双事务方案。

### 9.5 不可用结果收费分类

| 结果类型 | 用户收费 | 处理 |
|---|---|---|
| 墨灵/Bifrost 协议转换错误 | 否 | 平台成本，内部故障 |
| 上游空响应、损坏 JSON、流缺少终帧 | 否 | 供应商对账 |
| 请求前内容安全拒绝 | 否 | 不调用上游 |
| 输出后被墨灵安全拦截 | Phase 1 否 | 平台记录成本与安全事件 |
| 模型正常政策拒答且返回有效内容/Usage | 按公开规则 | 模型详情提前说明 |
| 普通提示词要求 JSON 但模型未满足 | 是 | 仅承诺结构化输出的接口例外 |
| 客户端断开 | 按最终确认用量 | 后台结算 |

## 10. Bifrost 与上游执行

### 10.1 定位

Bifrost 负责协议转换、统一响应、流式转发和受控路由。墨灵负责用户、SK、Project、价格、预算、钱包、内容策略、请求账本和审计。

生产要求：

- Bifrost 只部署在私网，用户 SK 永不传入。
- 锁定版本和镜像摘要，不使用 `latest`。
- POC 核对许可证、社区/企业能力边界、漏洞和配置持久化要求。
- 每个模型配置 `execution_driver=bifrost|native_openai|fake`。
- Bifrost POC 失败不阻塞文字商业闭环，原生 Go Adapter 是正式回退路径。

### 10.2 首批路由规则

- 只接入两个已经通过合同、计量和稳定性验收的上游。
- 每个模型配置主端点和至多一个备用端点。
- 只有能力、参数、内容安全、Usage 和计费语义等价时，才允许自动切换。
- `execution_unknown` 时禁止自动跨供应商重试。
- 每次尝试写 `ai_execution_attempts`，用户只产生一个逻辑请求和一个账单终态。

### 10.3 POC 验收

- 原生 Go 与 Bifrost 的响应字段和错误分类一致。
- 非流式附加延迟 P95 目标不超过 20ms；流式 TTFT 增量目标不超过 30ms。
- 成功率差异不超过 0.1 个百分点。
- Usage 完整率、断连行为、超时、429 和取消语义通过契约测试。
- 单实例退出时不重复请求、不重复结算；可按模型回切原生驱动。

这些指标是墨灵 POC 门槛，不是第三方性能承诺。

## 11. Project、SK、预算与并发

### 11.1 轻量 Project

- 钱包仍归企业负责人用户所有。
- Project 只承担 SK、预算、请求和账单归集，不引入共享钱包和组织成员系统。
- 所有 Project 查询必须同时校验 `user_id`，禁止通过 ID 越权访问。

### 11.2 Project 级 SK

每个 SK 支持模型白名单、最大 Token、超时、RPM、TPM、并发、日/月限额、环境标签、有效期、IP 白名单和吊销。

安全规则：

- 完整密钥只在创建时显示一次，数据库只保存 HMAC 哈希。
- Key 前缀仅用于识别，不得作为鉴权凭据。
- 新建 Project Key 默认使用模型白名单；空白名单表示禁止全部模型，不表示无限授权。
- 轮换时允许短期双 Key，并要求明确过渡截止时间。
- 吊销、预算超限和内容违规可以只暂停单个 SK。
- 创建、修改、轮换和吊销全部写审计日志。

### 11.3 预算

- 80%：站内提醒。
- 90%：站内加已配置的短信或邮件提醒。
- 100%：默认停止新的收费请求。
- 预算判断包含已结算金额和当前预占金额。
- 支持仅提醒或硬停止；临时超额必须填写原因和失效时间。
- 达到硬上限时不调用上游、不预占，返回稳定错误码。

### 11.4 四层并发

Phase 1 实现用户、Project、API Key 和模型四层限制，实际限制取最小值。Redis Lua 原子完成清理过期租约、检查全部维度和写入租约。

建议初始值需通过压测确认：普通文字模型默认 10～20，高成本推理模型默认 2～5。触发限制返回 429、`Retry-After` 和限制范围，不收费。

Redis 无法原子判断时公网收费请求 fail-closed；不降级为每个实例的本地限额，也不把高峰压力转移为 MySQL 每请求写锁。

## 12. 内容安全与日志隐私

### 12.1 Phase 1 文字安全闭环

```text
输入规范化
  -> 关键词/规则
  -> 上下文分类器
  -> 策略决策
  -> 允许执行或统一拒绝
  -> 流式分段缓冲
  -> 输出复检
  -> 返回、截断或拒绝
```

覆盖违法、色情、赌博、毒品、暴力恐怖、仇恨、自残和其他经合规负责人确认的分类。默认拒绝文案为：“请求内容违反中国大陆相关法律法规或平台安全规范，无法继续处理。”接口同时返回稳定错误码和请求 ID。

规则、分类器和处置动作必须版本化。审核服务不可用时公开收费模型默认 fail-closed。违规累计支持告警、SK 暂停、用户暂停和人工申诉，所有处置写审计。

法律适用、词库范围、留存时间、算法备案和生成内容标识由法务/合规负责人确认；工程文档不能替代法律意见。

### 12.2 日志最小化

默认只记录请求 ID、用户、Project、SK 脱敏标识、模型、上游、Token、金额、状态、耗时和安全命中分类，不保存完整提示词或输出。

- 企业可主动开启加密内容日志并选择批准的留存档位。
- 违规内容进入独立隔离区，普通管理员无权查看。
- 指定请求排障需要客户授权，并记录访问审计。
- 运行日志、内容日志、财务流水和安全证据分别设置生命周期。
- Bifrost 禁止开启记录完整 prompt、响应或密钥的生产 debug 模式。

## 13. 外部静态文档注册中心

墨灵不建设 Markdown/CMS 编辑器，只管理：

- 模型介绍 URL、Quick Start URL、API 参考 URL、SDK 示例 URL、更新日志 URL。
- 文档语言、版本、发布状态和适用模型版本。
- HTTPS、域名白名单、DNS/IP、重定向和内网地址校验。
- 定时检测状态码、响应时间和页面标题。
- 链接失效时告警并隐藏入口，保留上一已发布版本。

模型详情页的简短 Quick Start 由后端根据公开模型代码和 Base URL 生成；完整文章打开外部静态网页。外部网页不得暴露 Bifrost 地址、内部模型别名、上游密钥或内网信息。

## 14. API 契约

### 14.1 对外兼容 API

```text
GET  /v1/models
POST /v1/chat/completions
```

认证使用 `Authorization: Bearer sk-molin-...`。响应保留 OpenAI 兼容字段；墨灵账单详情通过独立接口查询，避免破坏第三方 SDK。

### 14.2 用户端 API

```text
GET/POST/PATCH /api/ai/projects
GET/POST/DELETE /api/ai/projects/{project_id}/keys
POST /api/ai/keys/{id}/rotate
GET /api/ai/catalog/models
GET /api/ai/catalog/models/{model_code}
GET /api/ai/catalog/models/{model_code}/prices
GET /api/ai/catalog/models/{model_code}/quick-start
GET /api/ai/usage/summary
GET /api/ai/requests
GET /api/ai/requests/{request_id}
POST /api/ai/requests/{request_id}/disputes
POST /api/ai/usage/exports
```

### 14.3 管理端 API

```text
/api/admin/ai-gateway/models
/api/admin/ai-gateway/endpoints
/api/admin/ai-gateway/price-versions
/api/admin/ai-gateway/releases
/api/admin/ai-gateway/moderation-policies
/api/admin/ai-gateway/concurrency-policies
/api/admin/ai-gateway/billing-exceptions
/api/admin/ai-gateway/reconciliations
/api/admin/ai-gateway/document-links
```

列表统一返回 `{items,page,page_size,total}`；金额返回字符串；时间使用 RFC3339。所有错误包含 `code`、`message` 和 `request_id`。

### 14.4 关键错误码

| HTTP | code | 用户含义 | 是否可重试 |
|---:|---|---|---|
| 400 | `invalid_request` | 参数错误 | 修改后重试 |
| 401 | `invalid_api_key` | SK 无效或已吊销 | 否 |
| 403 | `model_not_allowed` | SK 无模型权限 | 否 |
| 403 | `content_policy_violation` | 违反安全规范 | 否，可申诉 |
| 402 | `insufficient_balance` | 钱包余额不足 | 充值后重试 |
| 429 | `concurrency_limit_exceeded` | 并发达到上限 | 按 Retry-After |
| 429 | `budget_limit_exceeded` | Project/SK 预算已满 | 调整预算后 |
| 503 | `pricing_unavailable` | 无有效价格或毛利熔断 | 稍后重试 |
| 503 | `upstream_unavailable` | 上游暂不可用 | 可控重试 |
| 202 | `settlement_pending` | 执行或结算确认中 | 查询原 request_id |

## 15. 管理后台 UI

菜单统一收口为“AI 网关”：

1. 总览：请求、销售额、成本、毛利、成功率、P95、待结算和安全拒绝。
2. 模型中心：草稿、内测、待审核、已发布、暂停、下线。
3. 上游与路由：驱动、端点、健康、熔断和配置版本。
4. 价格中心：成本、销售 SKU、汇率、毛利、审核、生效和回滚。
5. Project 与 SK：预算、并发、限额、状态和审计。
6. 内容安全：规则、分类器、测试、发布、违规与申诉。
7. 请求与结算：请求详情、执行尝试、钱包关联、异常和冲正。
8. 文档链接：URL 校验、版本、发布和失效巡检。

模型发布状态：

```text
草稿 -> 配置检查 -> 内测 -> 待审核 -> 已发布 -> 已暂停 -> 已下线
```

发布门禁必须校验双上游、价格、最坏成本、毛利、安全策略、文档、监控和内测账单。价格版本和执行配置可关联到同一发布单，但分别生效和回滚。

## 16. 用户端 UI

### 16.1 信息架构

```text
模型市场 -> 模型详情 -> Quick Start -> 创建/选择 Project SK
    -> 发起请求 -> 获得 request_id -> 用量中心 -> 请求账单/申诉
```

Phase 1 只展示“全部”和“文本”。图片、音频和视频未发布前不显示空分类或不可用按钮。

### 16.2 页面

- 模型市场：搜索、场景筛选、上下文、主要价格和状态。
- 模型详情：能力、上下文、限制、价格表、失败收费规则、文档链接和服务状态。
- Quick Start：cURL、Python、JavaScript、OpenAI SDK，复制按钮必须有反馈。
- Project/SK：密钥创建、一次展示、轮换、吊销、模型范围、预算和并发。
- 用量概览：今日、本月、预算进度、请求量、Token 和费用趋势。
- 请求账本：按 Project、SK、模型、状态和时间查询，展示价格版本、计量来源和钱包流水。
- 账单申诉：使用 request_id 提交，不要求用户提供原始密钥。

### 16.3 响应式与状态

- 桌面端使用紧凑表格和详情抽屉；移动端将筛选放入抽屉，模型行收敛为关键信息。
- 所有页面覆盖 loading、empty、error、success 和 partial 状态。
- 所有按钮必须有真实导航、复制、提交、下载、重试或状态反馈。
- 支持键盘操作、可见焦点、屏幕阅读器标签、足够对比度和不小于 44px 的移动触控目标。
- 金额、长模型代码和 request_id 不得溢出容器。

## 17. Error & Rescue Registry

| 方法/路径 | 失败 | 错误分类 | 补偿动作 | 用户看到 |
|---|---|---|---|---|
| APIKeyAuth | 缺失、吊销、过期 | `AuthError` | 拒绝，不创建请求 | 401 |
| ModerationCheck | 规则命中 | `PolicyViolation` | 写安全事件，不调用上游 | 403 统一拒绝 |
| ModerationCheck | 服务超时 | `ModerationUnavailable` | fail-closed，告警 | 503 |
| ConcurrencyAcquire | 达到限制 | `ConcurrencyExceeded` | 不预占、不执行 | 429 + Retry-After |
| ConcurrencyAcquire | Redis 不可用 | `ConcurrencyUnavailable` | fail-closed | 503 |
| PricingQuote | 无价格/成本过期 | `PricingUnavailable` | 暂停模型 | 503 |
| PricingQuote | 无法计算最坏成本 | `UnquotableRequest` | 拒绝并提示参数 | 400 |
| WalletHold | 余额不足 | `InsufficientBalance` | 不执行 | 402 |
| WalletHold | 并发冲突 | `WalletBusy` | 有界重试后失败 | 503 |
| ProviderExecute | 建连前失败 | `SafeRetryableError` | 同幂等键受控重试 | 临时等待/503 |
| ProviderExecute | 发送后超时 | `ExecutionUnknown` | 查询、保留预占、对账 | 202 处理中 |
| ProviderExecute | 429 | `ProviderRateLimited` | 有抖动退避；受路由策略限制 | 429/503 |
| ResponseNormalize | 空响应/损坏 JSON | `MalformedProviderResponse` | 不向用户收费，供应商对账 | 502 |
| StreamForward | 客户端断开 | `ClientDisconnected` | 尝试取消，后台结算 | 查询账单状态 |
| UsageResolve | Usage 缺失 | `UsageUnavailable` | 已验证估算或异常队列 | 结算中 |
| WalletSettle | DB/锁失败 | `SettlementPending` | Outbox 重试，禁止重复终态 | 结果可用，账单结算中 |
| OutboxPublish | RabbitMQ 不可用 | `OutboxPending` | 保留 DB 事件，后台重投 | 无额外影响 |
| Reconcile | 多次失败 | `ManualReviewRequired` | 人工异常队列、模型熔断 | 账单异常处理中 |
| DocumentCheck | SSRF/重定向内网 | `UnsafeDocumentURL` | 拒绝发布 | 后台显示校验错误 |

禁止使用“捕获所有错误后只打印日志并继续”的方式处理财务状态。每个错误必须重试、降级、转入补偿，或带上下文重新抛出。

## 18. Failure Modes Registry

| CODEPATH | FAILURE MODE | RESCUED? | TEST? | USER SEES? | LOGGED? |
|---|---|---:|---:|---|---:|
| 报价 | 成本版本缺失 | 是 | 是 | 模型暂不可用 | 是 |
| 预占 | 100 请求并发争抢余额 | 是 | 是 | 余额不足或系统繁忙 | 是 |
| 执行 | Bifrost 单实例退出 | 是 | 是 | 受控重试或 503 | 是 |
| 执行 | 已发送但结果未知 | 是 | 是 | 处理中 | 是 |
| 流式 | 客户端断开 | 是 | 是 | 请求账单可查询 | 是 |
| 计量 | 上游 Usage 缺失 | 是 | 是 | 结算中或已验证估算 | 是 |
| 结算 | Worker 重复处理 | 是 | 是 | 无重复扣费 | 是 |
| 结算 | MySQL 提交成功但响应丢失 | 是 | 是 | 查询到同一终态 | 是 |
| Outbox | RabbitMQ 停止 2 小时 | 是 | 是 | 账单可用，聚合延迟 | 是 |
| 并发 | Redis 故障 | 是 | 是 | 503，不收费 | 是 |
| 安全 | 审核服务超时 | 是 | 是 | 503，不收费 | 是 |
| 文档 | URL 变为内网地址 | 是 | 是 | 链接隐藏 | 是 |
| 钱包 | actual 大于 hold | 是 | 是 | 账单异常处理中 | 是 |
| 日志 | 日志平台不可用 | 是 | 是 | 主请求不依赖日志平台 | 本地受控缓冲 |

不存在 `RESCUED=N + TEST=N + Silent` 的允许项；出现即阻止上线。

## 19. 测试计划

### 19.1 测试分层

- 单元测试：价格、Decimal、最坏成本、状态迁移、错误映射、预算、权限和安全规则。
- 契约测试：Bifrost/原生 Adapter、两个上游、SSE、Usage、429、超时和拒答。
- 集成测试：MySQL 事务、钱包 hold、Outbox、Redis Lua、RabbitMQ 重投和对账。
- E2E：创建 Project/SK、充值、请求、结算、账单、导出、吊销和申诉。
- 浏览器测试：桌面、平板、手机布局及所有按钮交互。

### 19.2 必测场景

1. 同一幂等键重复 100 次，只创建一个请求、一个 hold 和一个终态钱包消费。
2. 100 个并发请求竞争用户上限 20，最多 20 个调用上游，其余稳定返回 429。
3. 多 SK 仍共享用户和 Project 总预算，不能绕过。
4. 预占后进程退出，后台能够继续结算或释放。
5. 流式中途断开、缺 Usage、缺 `[DONE]`、损坏 chunk 均不丢账。
6. Bifrost 和原生 Go 对同一请求的字段、错误和 Usage 契约一致。
7. 上游超时发生在建连前和发送后时采取不同策略。
8. 价格发布与请求并发时，每个请求使用唯一、完整的价格快照。
9. 成本过期和低毛利触发停售，不调用上游。
10. 用户 A 无法读取用户 B 的 Project、SK、请求、账单和导出文件。
11. 新 Project Key 的空模型白名单拒绝全部模型；旧 Key 迁移不会静默扩大或缩小权限。
12. 文档 URL 拒绝 localhost、私网、重绑定和恶意重定向。
13. 安全命中、审核超时、规则回滚和申诉都有审计证据。
14. MySQL、Redis、RabbitMQ、Bifrost 短时故障恢复后，账本与钱包一致。
15. 钱包结算事务在任意写入点失败时全部回滚，不出现钱包已扣但请求仍待结算。
16. CSV 大导出走异步任务，不占用 API 进程大量内存。

### 19.3 混沌与压力测试

- 在 2、4、8 个 Go 实例下测试并发租约和钱包竞争。
- 随机终止 Bifrost、Go 实例和结算 Worker。
- 注入上游 429、500、慢响应、空响应、半截 SSE 和 Usage 差异。
- 停止 RabbitMQ 2 小时后恢复，验证 Outbox 有序收敛。
- Redis 主从切换期间验证 fail-closed 和租约回收。
- MySQL 提交后丢失客户端响应，重试仍返回同一财务终态。

## 20. 性能设计

- 网关自身附加延迟 P95 目标不超过 150ms；Bifrost 单独按 POC 门槛评估。
- SSE 使用流式解析和有界安全缓冲，不在内存保存完整长响应。
- HTTP 客户端启用连接池、连接/首字节/整体超时和上游级并发限制。
- 模型发布配置和价格只缓存已发布版本，缓存键包含版本号。
- 请求账本按用户/Project/SK/模型和时间建立复合索引，避免 N+1 查询。
- 用量看板使用异步日聚合表；逐请求账本保持财务事实，不用聚合覆盖原记录。
- 大导出和对账使用分页游标与后台任务，禁止一次加载全部数据。
- Outbox Worker 设置批次、锁超时、指数退避、死信和最大尝试次数。

## 21. 可观测性与运营

### 21.1 统一关联标识

`request_id` 必须贯穿 Nginx、Go、Bifrost、上游尝试、钱包、Outbox、日志和用户账单。Bifrost/上游请求 ID 作为内部字段保存，不向普通用户暴露内部拓扑。

### 21.2 核心指标

- 请求：QPS、成功率、错误分类、P50/P95/P99、TTFT、流式断连。
- 计费：hold、settled、released、pending、异常、金额差异和处理时长。
- 价格：成本过期、低毛利、发布失败、模型停售。
- 上游：429、5xx、超时、Usage 缺失、端点熔断和驱动差异。
- 安全：规则命中、分类器超时、SK/用户暂停和申诉积压。
- 并发：租约占用、429、heartbeat 失败和幽灵租约。
- 商业：真实充值、活跃设计客户、收入、成本、毛利和留存。

### 21.3 告警与 Runbook

P0：重复扣费、免费穿透、钱包与请求账本不平、密钥泄露、越权读取。立即停止放量并暂停相关模型。

P1：Usage 大面积缺失、结算积压、价格过期、毛利低于底线、审核不可用、Bifrost 集群异常。按模型或驱动熔断。

每种告警必须链接 Runbook，说明确认范围、停止流量、恢复、对账、用户通知和复盘步骤。提供公开状态页，但不泄露内部供应商和安全细节。

## 22. 部署、灰度与回滚

### 22.1 分支与环境

AI 网关固定在 `D:\molingproject\molin-gateway-worktree` 的 `feature/bifrost-ai-gateway-v2` 开发，不切换短信或邮件工作区。该分支已从 2026-08-03 的最新 `origin/main` 建立，并只迁移纯网关、Node 24 CI 和阶段0规划提交；基线证据见 [`ai-gateway-phase0-freeze-record.md`](./ai-gateway-phase0-freeze-record.md)。在开始第一笔核心网关代码前，仍须确认前置核心商业闭环已经通过阶段验收。

环境分为本地、测试、预发布和生产。Fake/Mock、沙箱上游、供应商接受、生产调用和客户验收必须分别记录，不能互相替代。

### 22.2 发布顺序

1. Expand migration。
2. 部署兼容新旧结构的 Go 代码，功能开关默认关闭。
3. 部署私网 Bifrost 锁定版本和监控。
4. 启用 Shadow 对照，不产生第二笔扣费。
5. 内部 SK 小流量。
6. 设计客户按模型和 Project 灰度。
7. 达到 SLO 和账单门槛后扩大流量。

### 22.3 回滚

- 执行问题：按模型将 `execution_driver` 切回原生 Go。
- Bifrost 配置问题：恢复上一已验证配置版本。
- 价格问题：停止新请求，发布复制自旧版本的新价格版本。
- 应用问题：关闭 Phase 1 功能开关，旧查询保持只读。
- 数据库：采用 expand/contract；应用回滚不删除请求、价格快照、执行尝试和钱包流水。

回滚后必须对灰度时间段执行请求与钱包对账，不能只验证 HTTP 恢复。

## 23. 八周开发计划

| 周 | 内容 | 主要产物 | 阶段门槛 |
|---|---|---|---|
| 1 | 契约、数据模型、Bifrost POC、双上游计量 | POC 报告、Migration 草案、接口契约 | G0：核心商业闭环验收；G1：POC 通过或确认原生路径 |
| 2 | RequestOrchestrator、Adapter、SK/Project、SSE | 文字调用链路、契约测试 | G2：无收费的端到端文字请求稳定 |
| 3 | 价格版本、最坏成本、钱包 hold、结算和 Outbox | 金额金样、状态机、对账 | G3：并发和故障下无重复扣费/免费穿透 |
| 4 | 内容安全、并发、预算、异常补偿 | Redis Lua、安全策略、异常队列 | G4：fail-closed 和恢复演练通过 |
| 5 | 管理后台模型发布、价格、路由和安全 | 后台工作台、权限和审计 | G5：模型发布门禁通过 |
| 6 | 用户端模型市场、Quick Start、SK、用量和账单 | 响应式页面、导出 API | G6：完整客户旅程 E2E 通过 |
| 7 | 集成、并发、混沌、安全、性能和账单核对 | 测试报告、Runbook、SLO 面板 | G7：0 个 P0/P1，计费差异 0 |
| 8 | 设计客户灰度、修复、上线演练和文档收口 | 灰度报告、上线/回滚清单 | G8：测试与产品确认后进入商业观察 |

工程完成后继续至少 4 周商业观察。图片阶段必须另行立项，不能与 Phase 1 尾期并行偷跑。

## 24. 阶段验收

### 24.1 工程验收

- 5～8 个文字模型和两个上游通过契约测试。
- 100 并发竞争、重复请求、断连和进程退出不会重复扣费或遗失 hold。
- 正常请求的请求账本、Usage、价格快照和钱包流水金额完全一致。
- 所有 `billing_exception` 可查询、可补偿、可审计。
- SK、Project、预算、并发和模型权限无法绕过。
- 内容安全失败默认不调用上游；违规处置与申诉可追踪。
- 桌面、平板和手机端关键流程通过浏览器验收，按钮均有有效交互。
- `docs/full-api-design.md`、`docs/frontend-api-reference.md`、`docs/database-schema-design.md` 和 `docs/test-plan.md` 与实现同步。
- 测试工程师和产品经理分别验收，0 个 P0/P1 后才允许生产灰度。

### 24.2 商业验收

- 3 家设计客户确认，2 家真实集成。
- 1 家真实付费并连续使用 4 周。
- 网关成功率不低于 99.5%。
- 正常账单差异为 0，异常账单全部在 SLA 内处理。
- 毛利不低于批准底线。
- 公开自助充值前，真实支付、回调、对账和退款流程有生产证据。

## 25. 开发前人工确认项

以下是实施门槛，不允许由开发者自行猜测：

1. 首批 7 个公开模型、两个上游和模型场景定位已在 Phase 0 冻结记录中形成工程基线；发布前仍需逐模型验收。
2. 每个模型的最大参数、Usage 来源、失败收费规则和最坏成本公式已形成工程基线；Usage 契约仍需 POC 证据。
3. 人民币销售价、上游成本、保护汇率和最低毛利率已冻结为 `phase1-text-cny-v0.1`；发布前必须重新核价并由产品、财务批准。
4. Bifrost 锁定版本、镜像摘要、许可证与社区/企业能力边界。
5. 内容分类、默认拒绝文案、留存和申诉 SLA 的合规确认。
6. 设计客户名单、试点入账审批人和真实支付开放责任人。
7. 生产 SLO、告警值、值班责任和事件通知渠道。

## 26. 产品决策记录

本计划已经锁定以下决策：

- 分阶段统一平台，Phase 1 只交付文字商业闭环。
- 长期蓝图与 Phase 1 执行计划拆分为两份权威文档。
- 数据迁移采用绞杀模式，`token_usage_logs` 不再作为新财务事实。
- Bifrost 是受控主路径，原生 Go 是可上线回退路径。
- AI Billing 单一结算入口，使用钱包事务与 Outbox 收敛。
- 最坏成本全额预占，Phase 1 不允许负余额和增量补扣。
- 设计客户验证与工程并行。
- 使用用户钱包加轻量 Project，不建设完整组织系统。
- Phase 1 只交付文字内容安全闭环。
- 充值采用设计客户人工实款入账与公开支付渠道两阶段方案。
- 首批 5～8 个文字模型、两个上游。
- 价格版本化并设置毛利熔断。
- 用量采用分层计量和差异审计。
- 流式断连转后台持久化结算。
- 用户、Project、SK、模型四层并发。
- Project 分级预警和可配置硬上限。
- 外部静态文档注册中心，不建设 CMS。
- 界面只展示已发布能力。
- 默认日志最小化，内容按授权加密留存。
- 未来媒体对象采用分级生命周期。
- Project 级可控 SK 和逐请求消费账本。
- 模型通过统一发布工作台上线。
- Phase 1 商业 SLO 与八周工程计划。
- 建立明确的 Phase 1 非目标清单。
- `RequestOrchestrator` 唯一编排，三类正交状态。
- 结果未知不自动跨供应商重试；不可用结果按责任分类收费。
- 首批 7 个文字逻辑模型和 `phase1-text-cny-v0.1` 价格参数以 Phase 0 冻结记录为工程基线。
- 成功请求最低收费 `0.000001 CNY`；Usage 缺失进入待对账，不再直接按 `max_tokens` 结算。

## 27. 后续评审建议

本文通过 CEO 范围收敛后，还需要执行：

1. `/plan-eng-review`：锁定 migration、事务边界、接口签名、任务拆分和回滚细节。
2. `/plan-design-review`：对管理后台和用户端信息架构、响应式页面及完整状态进行深度设计评审。
3. `/security-threat-model`：对 SK、Project 越权、文档 SSRF、上游密钥、内容日志和财务调整建立正式威胁模型。

这些评审不能替代本项目既有的测试工程师与产品经理阶段验收。
