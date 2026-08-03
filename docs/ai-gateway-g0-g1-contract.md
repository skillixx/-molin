# 墨灵 AI 网关 G0/G1 契约

> 状态：G0 已由产品经理采纳 QA 全链路证据并签收；G1 工程契约冻结，真实双上游 POC 结果单独记录。

> 范围：只覆盖文字 Chat Completions、商业请求账本 Expand Schema、Native/Bifrost 执行契约和双上游 POC。本文不启用 G2 RequestOrchestrator、正式计费、内容审核、并发限流、管理端、用户端或多模态能力。

## 1. G0 前置门禁

平台第一阶段核心闭环已有以下证据：

- `tests/audit-stage1-final.md`：测试工程师 37/37 端到端断言通过。
- `tests/audit-stage1-closing-confirm.md`：权限与收尾确认通过。
- `docs/frontend-acceptance-stage1-pm-review.md`：产品经理已完成前端业务验收；它不等同于完整核心闭环产品签收。

产品经理已在 `docs/ai-gateway-g0-g1-acceptance.md` 明确采纳上述证据，G0 产品验收通过；该结论不包含真实支付机构回调、生产部署或 AI 网关商业能力验收。

## 2. 公开 HTTP 契约

G1 不新增公开路由：

```text
GET  /api/token/models
POST /api/token/chat/completions
GET  /v1/models
POST /v1/chat/completions
```

- JWT 与平台 SK 最终都必须解析出 `user_id`，并经过模型可见性、SK 模型范围和资产门禁。
- 请求的 `model` 是墨灵逻辑模型代码，不允许客户端指定 Provider、Bifrost 地址或上游密钥。
- `stream=true` 使用 SSE，网关向执行驱动补充 `stream_options.include_usage=true`。
- `request_id` 贯穿公开 `X-Request-ID`、Go、Bifrost、上游尝试和新账本；它同时是账本全局唯一键，必须由墨灵中间件生成，Handler 必须复用且禁止二次生成。客户端传入的同名 Header 不作为账本身份，统一忽略。
- Bifrost 的 `extra_fields`、`routing_info`、供应商响应头、内部 Key 名称和错误正文不得返回客户端。
- 内部入口缺失或错误 Token 必须返回 401；重复 Authorization 必须在代理上游前拒绝，允许由 Nginx 协议层返回 400 或鉴权层返回 401。
- HTTP 200 携带业务错误、非法 JSON 或缺少 `choices` 时仍按上游失败处理。
- 收到完整 Usage 和 `[DONE]` 才确认流式成功；缺少 `[DONE]` 或执行结果未知时进入 `pending_reconcile`。
- G1 禁止按 `max_tokens` 猜测实际用量，也禁止结果未知时自动切换供应商。

## 3. 执行驱动契约

`ExecutionDriver` 是 Native 与 Bifrost 的唯一执行边界：

```text
ChatCompletion(ctx, request)       -> response + usage + attempt
ChatCompletionStream(ctx, request) -> response + attempt
NormalizeStreamLine(line, logical_model) -> public line + usage + done
```

驱动必须返回独立的 `ExecutionAttempt`，即使网络错误或超时也不能丢失本次尝试身份。Bifrost 与 Native 对标准 OpenAI 响应的公开字段、错误分类和 Usage 语义必须等价，公开 JSON/SSE 的 `model` 必须改写为墨灵逻辑模型。一次请求只选择一个驱动；G1 不实现自动 fallback。

运行态与持久化状态固定映射如下：

| 运行态 Outcome | `ai_execution_attempts.status` | `ai_requests.execution_status` | 计费含义 |
|---|---|---|---|
| `success` | `succeeded` | `succeeded` | Usage 完整时 G1 仍为 `unquoted`；G3 才报价/预占/结算 |
| `failed` | `failed` | `failed` | 未预占为 `unquoted`；已预占后的释放属于 G3 |
| `failed` 且结果未知 | `failed` | `unknown` | 请求可能已到达上游，禁止自动 fallback，等待对账 |
| `timeout` 且结果未知 | `timeout` | `unknown` | 已有 hold 时必须 `settlement_pending`，禁止自动 fallback |
| `pending_reconcile` | `unknown` | `unknown` | 已有 hold 时必须 `settlement_pending`，等待对账 |
| `running` | `running` | `running` | 不得形成终态账单 |

`ExecutionAttempt.ToLedgerModel` 冻结 `attempt_no`、Driver、Provider、内部端点、执行模型、状态、耗时和 Usage 摘要的转换；G2 只能调用该映射，不得自行发明另一套状态字符串。

## 4. 标准 Usage 契约

G1 归一化以下文字计量项：

| meter_type | 来源字段 | 说明 |
|---|---|---|
| `input_tokens` | `usage.prompt_tokens` | 输入 Token |
| `output_tokens` | `usage.completion_tokens` | 输出 Token |
| `cached_tokens` | `prompt_tokens_details.cached_tokens` | 缓存读取 Token |
| `reasoning_tokens` | `completion_tokens_details.reasoning_tokens` | 推理 Token |
| `total_tokens` | `usage.total_tokens` | 校验项，不单独重复收费 |

Usage 缺失或不完整时不得形成已知结算金额。运行态整数 Token 可使用 `int64`；进入用量账本前转换为 `decimal.Decimal` 并写入 `DECIMAL(30,10)`，单价和金额使用 `DECIMAL(20,8)`。禁止 `float64` 参与财务计算。

## 5. 商业请求账本

Migration `000058_create_ai_gateway_ledger_expand` 新建：

| 表 | G1 责任 |
|---|---|
| `ai_projects` | 冻结用户、Project、预算模式、月预算和时区；管理接口留 G2 |
| `ai_requests` | 保存请求身份、逻辑/执行模型和三类正交状态 |
| `ai_usage_items` | 保存标准化 Usage；价格字段在 G3 前保持为空 |
| `ai_execution_attempts` | 保存驱动、Provider、内部端点、执行模型、上游请求 ID、耗时和 Usage 摘要 |

正交状态：

```text
moderation_status: pending -> passed | rejected | error
execution_status:  pending -> running -> succeeded | failed | cancelled | unknown
billing_status:    unquoted -> held -> settlement_pending -> settled | released | exception
```

`request_id` 全局唯一；`(user_id,idempotency_key)`、`(request_id,meter_type,source,sequence_no)` 和 `(request_id,attempt_no)` 分别保证请求、计量行和执行尝试幂等。账本不保存提示词、响应正文或明文密钥。

`ai_requests` 使用 `(project_id,user_id)` 和 `(api_key_id,user_id)` 复合外键强制租户归属一致，禁止仅依赖 G2 应用校验。G1 不给现有 SK 增加 Project 语义；`api_keys.project_id`、`scope_mode=all|allowlist` 和新建 Key 默认拒绝全部模型属于 G2，必须通过后续 Expand Migration 和接口实现落地。

Project 预算组合冻结如下：`disabled` 必须对应 `monthly_budget=NULL`；`soft` 和 `hard` 必须配置大于 0 的人民币月预算。`soft` 超限只告警并允许继续请求，`hard` 在预计消费超过剩余额度时于上游调用前拒绝。月周期按 Project 的 IANA 时区计算为当地每月首日 00:00 至下月首日 00:00；并发下的准确预占与拒绝由 G3/G4 实现，G1 只冻结 Schema 和行为契约。

## 6. Expand 与回滚

G1 创建四张新表，并为现有 `api_keys` 增加 `(id,user_id)` 非业务语义唯一索引，以支持数据库级租户归属外键；不修改旧 `token_usage_logs` 读写，不产生钱包流水，不触发上游调用。应用回滚时保留新表、索引和审计数据；物理删除必须在备份、零引用证明和产品/财务审批后通过独立 Contract Migration 执行。

## 7. G1 出口

只有以下全部成立，才允许宣布 G1 通过：

1. `000058` 静态契约、Go 模型、全仓测试和敏感扫描通过。
2. Native/Bifrost 普通响应、SSE、五类 Usage、错误和脱敏契约通过。
3. 测试 Linux 的固定 Bifrost 版本、配置摘要、双节点健康和内部鉴权通过。
4. 百炼与 OpenRouter 各完成最小普通请求和 SSE，取得 Usage，不使用真实用户流量。
5. 测试工程师与产品经理分别给出无 P0/P1 的阶段结论。

完成 G1 不代表进入 G2；RequestOrchestrator 和新账本写入必须由新的阶段 Goal 开发。
