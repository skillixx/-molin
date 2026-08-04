# AI 网关 G3 价格与钱包可靠结算功能文档

> 分支：`feature/bifrost-ai-gateway-g3`
>
> 基线：`64e35da46af2c386e74073ec4e903ed953c7aadf`
>
> 范围：文字模型价格版本、最坏成本报价、钱包预占、一次终态结算、释放、Outbox 和异常对账。

## 1. 功能说明

G3 在 G2 的 Project SK 和 RequestOrchestrator 链路上增加人民币按量计费。客户不购买积分；每次请求在调用上游前锁定价格快照和最大可能费用，从钱包可用余额转入冻结余额，执行结束后按可信 Usage 一次结算，多余冻结金额退回。

## 2. 使用角色

- 小型企业客户：使用 Project SK 调用已授权文字模型，钱包不足或请求无法报价时在上游调用前被拒绝。
- 运营与财务：通过受控数据流程准备已审核价格版本；使用受权限和审计保护的后台接口终结人工对账异常。
- 运维与测试：运行 Settlement Worker、Outbox Worker 和隔离验收脚本。

G3 不提供管理后台或用户端 UI；仅提供人工异常终结后台 API。价格发布 UI、消费记录页面和模型详情页属于后续阶段。

## 3. 核心业务规则

1. 币种固定为 CNY，金额使用 Decimal；最低收费写入逐请求快照，但只对成功且存在正用量的请求应用。
2. 一个价格版本必须包含输入、输出、缓存和推理四个 SKU；成本过期、毛利不足、SKU 缺失或生效区间重叠时失败关闭。
3. `max_tokens` 可选；缺省时采用 `TOKEN_HOLD_DEFAULT_MAX_TOKENS` 与模型输出上限中的较小值。G3 只允许单候选 `n=1`，显式值非法、超过模型上限或 `n>1` 时拒绝，无法证明最大费用时不调用上游。
4. 同一请求只创建一个 hold，只允许 `settled`、`released` 或 `exception` 中一个财务终态。
5. JSON 或正常结束 SSE 的可信 Usage 完整时，无论执行成功或明确失败都按快照结算；失败请求不套用成功最低收费。只有确认零成本且无 Usage 才释放。Usage 缺失、不一致、结果未知或 SSE 不完整时，即使已经看到中间 Usage 也保留 hold，进入 `settlement_pending`；禁止按 `max_tokens` 猜测收费。
6. `actual_amount > held_amount` 时进入 `exception`，暂停价格版本并产生 P0 财务事件，不补扣、不透支。
7. 客户端断连不改变已经发生的上游成本；可信 Usage 到达后使用后台上下文结算。
8. 请求、hold、钱包流水、请求钱包关联和 Outbox 在同一 MySQL 事务中提交。
9. Outbox 先写 MySQL，再由 Worker 发布到 RabbitMQ；单次发布有硬超时，同一请求严格按事件 ID 顺序投递，默认自动重试窗口超过 2.5 小时，dead 事件可受控重新入队。
10. 请求尚未发出时的网络失败明确释放 hold，并允许同一幂等键原子创建新请求；请求已发出但结果未知时保留 hold，禁止误判为零成本。
11. 对账批次隔离单条损坏记录，其他请求继续收敛；超过期限的异常可由人工确认无成本释放，或按核定 Usage 结算，原始与人工核定 Usage 分别留存。
12. Project/SK 越权统一返回 HTTP 403 + `40003`；人工 `settle` 必须包含正的输入或输出用量，确认零成本必须显式选择 `release`。
13. G3 正式链路不写旧 `token_usage_logs`，不按多个 Usage 项分别扣钱包。

## 4. 状态与用户反馈

| 场景 | HTTP | error | 财务行为 |
|---|---:|---|---|
| 无有效价格 | 503 | `pricing_unavailable` | 不预占、不调用上游 |
| 成本过期 | 503 | `price_expired` | 不预占、不调用上游 |
| 毛利不足 | 503 | `margin_below_minimum` | 不预占、不调用上游 |
| 请求无法报价 | 400 | `unquotable_request` | 不预占、不调用上游 |
| 钱包余额不足 | 402 | `insufficient_balance` | 不调用上游 |
| 钱包预占失败 | 503 | `wallet_hold_failed` | 整体回滚 |
| 结果或 Usage 待确认 | 202 | `settlement_pending` | 保留 hold，进入对账 |
| 计费异常 | 500 | `billing_exception` | 保留证据和 hold，人工复核 |

数字 `code` 继续兼容墨灵现有接口；新增 `error` 提供稳定机器可读分类。

## 5. 页面与接口入口

- 模型调用：`POST /api/token/chat/completions`、`POST /v1/chat/completions`。
- 请求状态：`GET /api/token/requests/{request_id}`、`GET /v1/requests/{request_id}`，只允许原 Project SK 查询。
- 人工异常终结：`POST /api/admin/token/billing/exceptions/{request_id}/resolve`，要求 `token:manage`、管理员二次认证和前置审计。
- 模型目录：`GET /api/token/models`、`GET /v1/models`。
- Project 与 SK 管理沿用 G2 接口。
- 同一 `Idempotency-Key` 重放返回已有请求及 `billing_status`，不产生第二次上游调用或扣费。
- SSE 已开始后不能修改 HTTP 状态；待结算或计费异常通过 `event: molin.status` 返回 `request_id` 和稳定 `error`。

## 6. 非本阶段能力

内容审核、关键词过滤、并发/RPM/TPM、Project 预算硬限制、管理和用户 UI、多模态、跨供应商 fallback、生产部署均不属于 G3。
