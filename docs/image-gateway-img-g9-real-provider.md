# IMG-G9：真实OpenRouter图片与人民币测试计费闭环

> 当前状态：首次真实调用失败，已回滚；正在修复上游失败证据缺口，尚未授权第二次真实调用
>
> 基线：`3d094679bd5e74f620ee9fd025fdec85f0d5e338`
>
> 证据边界：只允许已登记测试服务器、一次真实请求、非商业价格夹具；不代表生产或商业验收。

## 1. 功能说明

IMG-G9验证墨灵图片网关从真实OpenRouter图片生成到人民币测试钱包结算、资产交付和零差异对账的完整闭环。使用角色包括后端、测试、运维、安全和财务审查人员；终端用户仅使用专用测试身份，不开放客户流量。

页面沿用IMG-G8用户图片工作台和管理端图片任务、资产、账单与对账入口，不新增独立UI体系。接口沿用：

- `POST /api/token/images/quotes`
- `POST /api/token/images/generations`
- `POST /v1/images/generations`
- `GET /api/token/image-tasks/{task_id}`
- `GET /api/token/image-assets/{asset_id}/download-url`
- `GET /api/admin/token/image-reconciliation/summary`

## 2. 冻结合同

```text
model=google/gemini-3-pro-image
provider_tag=google-vertex/global
n=1
resolution=2K
aspect_ratio=1:1
quality=standard
delivery=url
stream=false
allow_fallbacks=false
provider_retries=0
model_fallbacks=0
max_provider_cost_usd=0.25
test_sale_price_cny=0.50
max_wallet_charge_cny=0.60
```

ProviderTag来自OpenRouter官方图片端点目录。运行时只允许这一模型和端点，禁止透明换模、路由到第二Provider或提高费用上限。

## 3. 代码结构

- `server/internal/config/config.go`：IMG-G9测试环境、模型、ProviderTag、Key路径和费用上限失败关闭。
- `server/internal/bootstrap/app.go`：关闭态禁用Adapter与真实OpenRouter Adapter装配。
- `server/internal/modules/token_gateway/image/openrouter_adapter.go`：固定Images端点、单Provider、零重试，并提取HTTP状态和封闭字符集错误码；禁止保存上游错误消息和原始响应。
- `server/internal/modules/token_gateway/image/gateway.go`：把Provider尝试标记、请求标识、HTTP状态、错误码、产物数量和美元费用回执传入深模块结果。
- `server/internal/modules/token_gateway/service/image_billing_service.go`：将低敏Provider证据写入任务结果，并把实际尝试同步为`attempt_count=1`和`provider_code`；继续使用test_fixture完成CNY计费。

本阶段不新增migration。数据库继续使用 `ai_requests`、`ai_gateway_quotes`、`ai_gateway_tasks`、`ai_gateway_assets`、`ai_usage_items`、`ai_request_wallet_links`、`wallet_holds`、`wallet_transactions`、`ai_outbox_events` 和 `ai_compensation_tasks`。

## 4. 配置与状态流转

关闭态：

```text
IMAGE_GATEWAY_ENABLED=true
IMAGE_GATEWAY_TRAFFIC_ENABLED=false
IMAGE_GATEWAY_LOCAL_FAKE_TEST=false
IMAGE_GATEWAY_PROVIDER=openrouter
IMAGE_GATEWAY_OPENROUTER_ENABLED=false
IMAGE_GATEWAY_OPENROUTER_KEY_FILE=
```

关闭态只装配不可执行的OpenRouter禁用Adapter，不读取Key，所有生成入口返回50330。

真实调用窗口仅在最终门禁通过后临时开启：

```text
IMAGE_GATEWAY_TRAFFIC_ENABLED=true
IMAGE_GATEWAY_OPENROUTER_ENABLED=true
IMAGE_GATEWAY_OPENROUTER_KEY_FILE=/home/pc/molin/secrets/img-g9/openrouter-key
IMAGE_GATEWAY_OPENROUTER_MODEL=google/gemini-3-pro-image
IMAGE_GATEWAY_OPENROUTER_PROVIDER_TAG=google-vertex/global
IMAGE_GATEWAY_OPENROUTER_MAX_COST_USD=0.25
```

业务状态仍遵循：

```text
Quote → Hold → Generate → Decode → Moderate → Store → Settle → Available
```

超时、断连、费用回执缺失或越权进入结果未知，保留Hold且禁止自动重试。明确失败按现有合同释放Hold；结算和审核完成前不得签发下载URL。

## 5. 费用与对账

用户销售价继续使用非商业test_fixture：单张0.50元，钱包最大预占/结算0.60元。`usage.cost` 是OpenRouter返回的实际美元费用，只以规范Decimal字符串写入任务低敏结果摘要；它不替代人民币销售价格，也不被表述为正式CNY成本政策。

验收同时完成两组核对：

1. CNY测试账务：Quote、Hold、sale_line、cost_line、钱包流水、资产和Outbox差异为0。
2. Provider回执：任务中的 `provider_cost_usd` 与OpenRouter Key Usage增量一致且不超过0.25美元。

## 6. 权限与安全

- `/v1/images/generations`只接受具有显式图片模型scope的Project SK。
- 专用OpenRouter Key不得复用既有Bifrost或业务Key，只能通过0600普通文件注入。
- Prompt、Key、Provider原文和Base64不得进入日志、MySQL、RabbitMQ、文档或Git。
- 测试服务器继续标记为 `UNTRUSTED_TEST_ONLY`；发现挖矿、异常服务或矿池连接立即停止。
- 真实调用严格一次，失败、超时或结果未知都不得补发。

## 7. 测试、部署与回滚

本地必须通过配置矩阵、Adapter协议/费用回执、Gateway传播、全量Go测试和go vet。测试服务器先备份数据库、二进制、环境、RabbitMQ、MinIO和监控，再完成关闭态50330验收。

最终调用门禁要求Key限额、Usage=0、撤销方式、钱包基线、图片事实、队列深度和对账差异全部通过。唯一真实调用后立即关闭traffic和OpenRouter，恢复原二进制与环境，删除临时Project SK和Key文件，并确认用户已在OpenRouter控制台撤销Key。

回滚保留请求、Quote、Usage、成本、钱包、资产、Outbox、补偿和审计事实；禁止migration down、force、Provider重试、生产部署和客户流量。

## 8. 首次真实调用复盘与复验门禁

首次真实调用只执行了一次，网关返回502，OpenRouter Key费用增量为0，可交付图片为0，钱包0.50元Hold已完整释放且对账差异为0。OpenRouter官方合同说明，图片生成失败可以返回502且不计费，因此控制台`Usage=0`或`Last Used=Never`不能单独证明请求没有到达OpenRouter。

首次候选把所有上游非2xx都压缩成`provider_failed`，任务仍显示`attempt_count=0`和空`provider_code`，无法区分Workspace Guardrail拒绝、模型或Provider限制、账户费用不足、参数拒绝和上游生成失败。修复后必须在不保存Prompt、Key、错误消息或原始响应的前提下持久化：

- `provider_attempted`。
- `provider_code`。
- `provider_http_status`。
- 仅允许字母、数字、点、下划线、冒号和短横线的`provider_error_code`。
- 成功时经过字符集校验的`provider_request_id`和`provider_cost_usd`。

第二次真实调用不是原一次请求Goal的自动重试。只有完成以下检查并取得新的单次书面授权后才允许执行：

1. OpenRouter账户可用余额足以覆盖本轮上限，而不只是Key本身设置了0.25美元限额。
2. Workspace Guardrail的有效模型和Provider集合包含`google/gemini-3-pro-image`与本轮唯一`provider_tag`，且Prompt安全规则不会误拦截测试输入。
3. 新候选的全量Go测试、`go vet`、关闭态安装、备份、回滚和日志脱敏门禁通过。
4. 新授权明确追加一次请求、累计Provider费用仍不高于0.25美元、零重试、零fallback，并声明是否继续固定Google Vertex。
