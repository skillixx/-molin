# AI 网关 G2 请求编排与无收费文字链路开发文档

> 分支：`feature/bifrost-ai-gateway-g2`
>
> 基线：`6e7f6f6bddf1c1d40f4b20861ca4d240b31a91fc`
>
> 范围：Project、Project SK、显式模型权限、RequestOrchestrator、正式请求/执行/Usage 账本、JSON/SSE。

## 1. 阶段结论

G2 把公开文字调用从旧 `ForwardService + token_usage_logs + 钱包/套餐` 路径切换到独立的正式请求编排链。公开的 `/api/token/chat/completions` 和 `/v1/chat/completions` 共用同一个 `RequestOrchestrator`；旧 `ForwardService` 只保留给现有工作台内部调用。公开 Handler 未装配编排器时直接失败关闭，不回落旧转发链。

本阶段只形成可追踪的请求和用量事实，不形成价格或财务事实：

- 所有请求的 `billing_status` 保持 `unquoted`。
- `unit_price`、`amount`、`quoted_amount`、`held_amount`、`settled_amount` 保持空。
- 不调用钱包、套餐额度、旧 `token_usage_logs`、Outbox 或结算 Worker。
- 不实现预算硬限制、并发/RPM/TPM、内容审核、多模态和自动 fallback。

## 2. 代码结构

| 文件 | 职责 |
|---|---|
| `server/internal/modules/token_gateway/service/request_orchestrator.go` | Prepare、Execute、Finalize、Reconcile 唯一编排与模型列表权限判断 |
| `server/internal/modules/token_gateway/repository/g2_repository.go` | Project、SK、权限和正式账本的 GORM 查询与事务 |
| `server/internal/modules/token_gateway/service/project_service.go` | Project CRUD、Project SK 创建/轮换/吊销、显式模型权限 |
| `server/internal/modules/token_gateway/handler/project_handler.go` | 登录态 Project 与 SK 管理接口 |
| `server/internal/modules/token_gateway/handler/chat_handler.go` | OpenAI 请求解析、编排调用和 JSON/SSE 输出适配 |
| `server/migrations/000061_add_ai_gateway_g2_projects_keys.*.sql` | API Key Project 归属、权限表、过期/轮换和复合外键 |
| `infra/scripts/verify-ai-gateway-migration-000061.sh` | 隔离 MySQL 8 首次 up、保留式 down、re-up 和租户约束验证 |

## 3. Project 与 Project SK

Project 只属于一个 `user_id`，没有组织成员和共享钱包。所有读取和写入都使用 `(id,user_id)` 条件；停用使用 `status=suspended`，归档使用 `status=archived`，不物理删除。

Project SK 规则：

1. 明文格式为 `sk-molin-*`，数据库只保存 HMAC-SHA256。
2. 明文只在创建或轮换成功响应中出现一次，响应设置 `Cache-Control: no-store`。
3. 新建 Key 默认 `scope_mode=allowlist`；空 allowlist 拒绝全部模型。
4. `scope_mode=all` 必须由调用方显式提交，且不得同时提交 `model_codes`。
5. 旧 Key 标记为 `legacy_all`，继续按旧 `model_scope` 语义工作，不静默改变权限。
6. 支持 `expires_at`、吊销和原子轮换。轮换在同一事务内创建新 Key、复制权限并吊销旧 Key。
7. Project 停用、用户停用、未实名、Key 吊销或过期都会在上游调用前拒绝。

数据库使用 `(api_key_id,project_id,user_id)` 和 `(project_id,user_id)` 复合外键，应用校验不是唯一防线。

## 4. RequestOrchestrator

公开接口只调用以下统一契约：

```go
type RequestOrchestrator interface {
    Prepare(ctx context.Context, cmd PrepareCommand) (*PreparedRequest, error)
    Execute(ctx context.Context, requestID string, sink StreamSink) error
    Finalize(ctx context.Context, requestID string, result ExecutionResult) error
    Reconcile(ctx context.Context, requestID string) error
}
```

### 4.1 Prepare

Handler 先拒绝空 `messages`、非文字内容和多值 `Idempotency-Key`。随后 Prepare 按顺序完成：Project SK 身份、用户状态和实名、Project 状态、Key 状态和过期时间、模型发布状态、文字模态、用户分组/角色可见性、显式模型权限、渠道状态、执行驱动配置、请求指纹和幂等查询。全部通过后才创建 `ai_requests`，此时仍未调用上游。

提示词和响应正文不落库。进程只临时持有本次请求正文；进程重启后遗留的 pending/running 请求只能进入 `unknown`，不能凭缺失正文重试上游。

### 4.2 Execute

先用事务把请求从 `pending` 推进到 `running` 并创建唯一 `attempt_no=1`，再调用已选定的 Native 或 Bifrost 驱动。一次请求只选择一个驱动，结果未知、超时或 SSE 不完整时不自动 fallback。

### 4.3 Finalize

一个事务内完成：

- 更新 `ai_execution_attempts` 终态。
- 按唯一键写 `ai_usage_items`。
- 更新 `ai_requests.execution_status`、错误、断连和完成时间。
- 强制保持 `billing_status=unquoted`。

重复 Finalize 检查已有 attempt 终态，返回幂等成功，不重复写 Usage。

### 4.4 Reconcile

对中断的 pending/running 请求进行安全收敛。启动后的周期扫描会处理超过安全窗口的遗留请求：pending 超过 1 分钟，running 超过“最长流式执行时间 + 1 分钟”。选出候选 ID 后，仓储必须在事务内锁定请求并重新校验当前状态与截止时间，再把运行中的 attempt 与请求改为 `unknown`，标记 `result_unknown=true`。刚进入 running 或刷新为终态的请求不会被旧扫描结果误收敛；全程不重试、不 fallback、不伪造 Usage。

## 5. JSON 与 SSE

非流式 JSON 会先读完经过驱动清洗的响应并完成 Finalize，随后才向客户端写成功响应。若写回时客户端断开，补记 `client_disconnected=true`，不撤销已经确定的上游执行事实。

SSE 会逐段输出公开数据，但暂存 `[DONE]`：只有 Usage、attempt 和请求终态持久化成功后才发送 `[DONE]`。客户端中途断开后，编排器在独立最长执行期限内继续读取上游尾部 Usage；缺少 `[DONE]` 或流读取失败时进入 `unknown`，不会伪造成功终止符。

## 6. 幂等规则

- `X-Request-ID` 仍按 G1 契约由墨灵生成，是公开响应、执行驱动和账本共用的全链路身份。
- 客户端重放使用 `Idempotency-Key`；唯一范围为 `(user_id,idempotency_key)`。
- 同用户、同 Key、同 Project、同请求指纹返回已有 request_id 和状态，不重复调用上游。
- 同 Idempotency-Key 不同指纹返回 `40901`。
- 同 request_id 换用户、Project 或 SK 统一拒绝，不泄露原请求状态。
- 请求指纹为规范化 JSON 的 SHA-256；账本不保存对话正文。

Project SK 创建、轮换和吊销使用现有审计服务记录脱敏摘要。审计失败不反转已经成功的密钥操作，但 ProjectService 会输出只含 action、Project ID、Key ID 和安全错误的告警，不记录明文 SK、HMAC Secret 或请求正文。

## 7. Usage 规则

上游返回完整 Usage 时写入：

- `input_tokens`
- `output_tokens`
- `total_tokens`
- 非零时的 `reasoning_tokens`
- 非零时的 `cached_tokens`

数量使用 `DECIMAL(30,10)`，来源为 `provider`。Usage 缺失时不写计量行，不按 `max_tokens` 估算，也不产生任何金额。

## 8. 接口

登录态 JWT 管理接口：

```text
POST   /api/token/projects
GET    /api/token/projects
GET    /api/token/projects/{id}
PATCH  /api/token/projects/{id}
POST   /api/token/projects/{id}/keys
GET    /api/token/projects/{id}/keys
POST   /api/token/projects/{id}/keys/{key_id}/rotate
DELETE /api/token/projects/{id}/keys/{key_id}
```

模型目录仍兼容 JWT 与 Project SK；Project SK 请求会按 allowlist 过滤。正式 Chat 仅允许 Project SK：

```text
GET  /api/token/models  （JWT 或 Project SK）
GET  /v1/models         （JWT 或 Project SK）
POST /api/token/chat/completions
POST /v1/chat/completions
```

模型列表在使用 Project SK 时会按 `scope_mode` 和 allowlist 过滤，JWT 用于控制台浏览用户可见模型。公开 Chat 接口在 G2 要求 Project SK；JWT 仍可管理 Project，但不能绕过 Project 归属直接进入正式文字链。

## 9. 验证

已执行：

- `go test ./...`
- Project 默认空 allowlist、显式 all、停用、模型合法性、过期、跨用户、轮换测试。
- 请求幂等、20 并发重复请求、不同指纹、不同用户/Project/SK 测试。
- JSON、SSE、断连、Finalize 重试、超时结果未知、不完整 SSE 和 Reconcile 测试。
- OpenAI Handler、模型列表权限和统一 Request ID 测试。
- 隔离 MySQL 8：首次 up、保留式 down、re-up、租户外键、allowlist 和 `unquoted` 约束。

Windows 本机因 `CGO_ENABLED=0` 且没有 `gcc` 无法执行 race。已把当前非忽略 `server` 源码与 G1 契约文件放入测试 Linux 临时目录，使用已缓存 `golang:1.25` 镜像执行 `go test -race -count=1 ./...`，退出码为 0；临时目录和本地传输归档随后均已清理。远程分支 CI 仍会重复执行相同门禁。

## 10. 非生产声明

G2 仅证明无收费文字链的代码、自动化测试和隔离 MySQL 约束。本阶段没有部署测试或生产环境，没有切换真实用户流量，没有执行真实收费模型调用，也没有进入 G3 价格、钱包预占、扣费、结算和释放。
