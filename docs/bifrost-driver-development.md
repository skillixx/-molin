# Bifrost 文字模型执行驱动开发说明

## 1. 当前范围

本阶段只把现有文字 `Chat Completions` 上游调用抽象为统一执行驱动。它不新增商业请求账本、价格引擎、钱包计费、Project、内容审核、并发限流或图片/音视频能力，也不会自动启用 Bifrost。

后续 G0/G1 增量已通过 `000059_create_ai_gateway_ledger_expand` 新增商业请求账本 Schema 和 Go 模型，但仍未把现有调用链切换为新账本写入。执行驱动契约见本文，账本与阶段出口见 `docs/ai-gateway-g0-g1-contract.md`；两者都不代表 G2 RequestOrchestrator 已实现。

```text
现有 Handler
  -> 平台 SK/JWT 鉴权
  -> 模型可见范围、SK 模型范围、用户资产门禁
  -> ForwardService / ChatOnce
  -> ExecutionDriver
  -> NativeOpenAICompatibleDriver 或 BifrostDriver
  -> 上游模型
```

## 2. 代码目录与核心接口

- `server/internal/modules/token_gateway/service/execution_driver.go`：统一接口、请求、Usage、Attempt、Native 与 Bifrost 驱动。
- `server/internal/modules/token_gateway/service/forward_service.go`：保留原有门禁、预占、日志和结算编排。
- `server/internal/modules/token_gateway/service/chat_once.go`：工作台单轮调用复用同一非流式驱动。
- `server/internal/modules/token_gateway/service/execution_driver_test.go`：Fake Bifrost 契约测试。
- `server/internal/modules/token_gateway/service/forward_execution_gate_test.go`：验证 JWT/SK、模型范围和资产门禁始终早于执行驱动。

核心接口为 `ExecutionDriver.ChatCompletion`、`ExecutionDriver.ChatCompletionStream` 和 SSE 行归一化方法。`ExecutionDriverSelector` 为后续按模型路由和 `RequestOrchestrator` 预留入口，当前一次请求只选择一个驱动。

## 3. 配置项

```env
TOKEN_EXECUTION_DRIVER=native
BIFROST_BASE_URL=http://127.0.0.1:18080
BIFROST_INTERNAL_TOKEN=<通过安全环境变量注入>
```

默认必须为 `native`。只有显式设置 `TOKEN_EXECUTION_DRIVER=bifrost` 且地址、内部 Token 都存在时才启用 Bifrost；显式启用但配置不完整或 Token 不符合安全格式时，整个 Molin API 拒绝启动，避免其他模块正常而 AI 网关处于误配置状态。真实 Token 不得写入源码、文档、日志或测试。

## 4. 模型映射

| 墨灵逻辑模型 | Bifrost 模型 |
|---|---|
| `molin/qwen-turbo` | `bailian/qwen-turbo` |
| `molin/qwen-3.7-flash` | `bailian/qwen3.7-flash-2026-07-15` |
| `molin/deepseek-v4-flash` | `openrouter/deepseek/deepseek-v4-flash-0731` |

目标模型必须显式包含 Provider。未映射模型直接失败，不依赖 Bifrost 自动识别，也不自动切换上游。

## 5. 响应、错误与脱敏

非流式成功响应保持 OpenAI 兼容结构。Bifrost 驱动同时检查 HTTP 状态、`error`、`is_bifrost_error`、`choices` 和 JSON 合法性；HTTP 200 携带业务错误仍按失败处理。对外错误统一为受控错误，不透传供应商堆栈；响应中的 `model` 统一改写为墨灵逻辑模型。

以下字段会递归删除：`extra_fields`、`routing_info`、`provider_response_headers`、内部 Key 名称和 Bifrost 错误标识。上游真实密钥只用于 Native 渠道请求；Bifrost 请求只发送内部 Token。

## 6. SSE 处理

流式请求自动补充 `stream_options.include_usage=true`。每个 `data:` JSON 事件经过检查与清洗后再写给客户端，非 `data:` 扩展元数据默认丢弃。只有收到 `[DONE]` 才确认流正常结束；仅收到 Usage 后 EOF 仍属于结果未知并进入 `pending_reconcile`。中途出现 Bifrost 业务错误或非法数据时记录失败。流一旦开始输出，禁止透明切换供应商。

客户端写入失败后停止向客户端写，但继续尽力读取上游尾部 Usage。请求上下文取消或结果未知时同样不得自动 fallback。

## 7. Usage 标准化与结算边界

统一字段包括 `prompt_tokens`、`completion_tokens`、`total_tokens`、`reasoning_tokens` 和 `cached_tokens`。后两项分别读取 `completion_tokens_details.reasoning_tokens` 与 `prompt_tokens_details.cached_tokens`。

Usage 缺失时记录 `pending_reconcile / usage_missing`，并由原有 defer 释放钱包保证金或套餐预占。本阶段禁止按 `max_tokens` 猜测扣费。新的商业账本和对账 Worker 不在本阶段实现，因此驱动层不会开启新的商业计费能力。

## 8. 回退规则

- 默认 Native；切换 Bifrost 必须通过环境变量和部署变更完成。
- 当前不实现请求内自动重试或跨供应商 fallback。
- 已发送请求但结果未知、SSE 已输出、SSE 中途错误时均禁止自动切换。
- 每次调用生成独立 `ExecutionAttempt` 元数据；G0/G1 的 `000059` 已冻结 `ai_execution_attempts` 持久化表，但当前驱动尚不写入该表，正式落库由 G2 RequestOrchestrator 负责。

## 9. 测试方式

```powershell
cd D:\molingproject\molin-gateway-worktree\server
go test -count=1 ./internal/modules/token_gateway/...
go test -count=1 ./...
```

测试使用 `httptest` Fake Bifrost，不依赖真实付费上游。覆盖非流式、完整 HTTP SSE、`include_usage`、模型映射、内部鉴权、401/429/500、HTTP 200 业务错误、缺少 choices、非法 JSON、Usage 完整/空对象/缺字段、reasoning/cached、脱敏、缺少 `[DONE]`、中途错误、客户端断开、超时、Native 非流式/SSE、JWT/SK 与资产门禁，以及禁止自动 fallback。Windows 常规测试不等同于 `-race` 验收，并发安全需由 Linux CI 补跑。

## 10. 当前未实现

未实现商业请求账本、价格快照、最终对账 Worker、自动重试、模型级后台驱动配置、内容审核、Redis RPM/TPM、图片/音频/视频和生产部署。真实 Bifrost、上游验收及 QA/产品经理签署仍是后续人工门禁。
