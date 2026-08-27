# IMG-G9：真实OpenRouter图片与人民币测试计费闭环

> 当前状态：`AUTO_PASS`；Seedream/seed真实图片、人民币测试计费、私有资产交付、零差异对账、Key撤销和实际回滚均已完成
>
> 切换基线：`5f5bc0f14044756a3a2119420c3b0c6c2e90cf75`
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
model=bytedance-seed/seedream-5-0-lite
provider_tag=seed
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

ProviderTag来自OpenRouter官方图片端点目录；该端点当前按可交付图片计费，2K单张目录价为0.035美元。运行时只允许这一模型和端点，禁止透明换模、路由到第二Provider或提高费用上限。

## 3. 代码结构

- `server/internal/config/config.go`：IMG-G9测试环境、模型、ProviderTag、Key路径和费用上限失败关闭。
- `server/internal/bootstrap/app.go`：关闭态禁用Adapter与真实OpenRouter Adapter装配。
- `server/internal/modules/token_gateway/image/openrouter_adapter.go`：固定Images端点、单Provider、零重试，并提取HTTP状态和封闭字符集错误码；禁止保存上游错误消息和原始响应。
- `server/internal/modules/token_gateway/image/gateway.go`：把Provider尝试标记、请求标识、HTTP状态、错误码、产物数量和美元费用回执传入深模块结果。
- `server/internal/modules/token_gateway/service/image_provider_attempt.go`：在任何Provider调用前独立提交`provider_code + attempt_count=1`；已有尝试或记录失败时禁止进入上游。
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
IMAGE_GATEWAY_OPENROUTER_MODEL=bytedance-seed/seedream-5-0-lite
IMAGE_GATEWAY_OPENROUTER_PROVIDER_TAG=seed
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

最终调用门禁要求Key限额、调用前Usage基线、撤销方式、钱包基线、图片事实、队列深度和对账差异全部通过。验收只计算本轮Usage增量，不能把此前已确认的诊断费用当成本轮费用。唯一真实调用后立即关闭traffic和OpenRouter，恢复原二进制与环境，删除临时Project SK和Key文件，并确认用户已在OpenRouter控制台撤销Key。

回滚保留请求、Quote、Usage、成本、钱包、资产、Outbox、补偿和审计事实；禁止migration down、force、Provider重试、生产部署和客户流量。

## 8. 首次真实调用复盘与复验门禁

本节至第10节记录Gemini/Vertex历史候选和诊断事实，不再代表当前Seedream/seed冻结合同；保留原文用于追踪为何更换供应商。

首次真实调用只执行了一次，网关返回502，OpenRouter Key费用增量为0，可交付图片为0，钱包0.50元Hold已完整释放且对账差异为0。OpenRouter官方合同说明，图片生成失败可以返回502且不计费，因此控制台`Usage=0`或`Last Used=Never`不能单独证明请求没有到达OpenRouter。

首次候选把所有上游非2xx都压缩成`provider_failed`，任务仍显示`attempt_count=0`和空`provider_code`，无法区分Workspace Guardrail拒绝、模型或Provider限制、账户费用不足、参数拒绝和上游生成失败。修复后必须在不保存Prompt、Key、错误消息或原始响应的前提下持久化：

- `provider_attempted`。
- `provider_code`。
- `provider_http_status`。
- 仅允许字母、数字、点、下划线、冒号和短横线的`provider_error_code`。
- 成功时经过字符集校验的`provider_request_id`和`provider_cost_usd`。

尝试事实必须在调用上游之前提交。若进程在提交后、网络调用前后或Provider返回后退出，陈旧恢复只能进入结果未知并保留Hold；后续执行看到`attempt_count=1`必须失败关闭，不能再次调用Provider。成功、明确失败和结果未知的任务摘要都必须保留已确认的美元费用，不能因本地解码、审核、存储或结算失败而覆盖丢失。

第二次真实调用不是原一次请求Goal的自动重试。只有完成以下检查并取得新的单次书面授权后才允许执行：

1. OpenRouter账户可用余额足以覆盖本轮上限，而不只是Key本身设置了0.25美元限额。
2. Workspace Guardrail的有效模型和Provider集合包含`google/gemini-3-pro-image`与本轮唯一`provider_tag`，且Prompt安全规则不会误拦截测试输入。
3. 新候选的全量Go测试、`go vet`、关闭态安装、备份、回滚和日志脱敏门禁通过。
4. 新授权明确追加一次请求、累计Provider费用仍不高于0.25美元、零重试、零fallback，并声明是否继续固定Google Vertex。

## 9. 第二次真实调用结果

第二次真实调用使用`fe6297cfa2ff373b56d6a1860e0c0045d2eab531`候选，在全部关闭态、备份、Key、Quote、钱包、MinIO、RabbitMQ、监控和安全门禁通过后执行。结果为OpenRouter HTTP 403，低敏错误码403；任务已在调用前持久化`provider_code=openrouter-images`和`attempt_count=1`，证明请求进入真实Adapter。Provider费用和Key Usage均为0，重试和fallback均为0。

失败终态为：

```text
Quote 0.50
→ Hold 0.50
→ OpenRouter HTTP 403
→ sale/cost/usage 0
→ Release 0.50
→ 资产 0
→ Outbox held/released published
→ 对账差异 0
```

本轮没有可交付图片，因此IMG-G9不得通过。OpenRouter原始错误消息按安全合同未落库、未写日志，当前证据只能证明控制面HTTP/code均为403；Workspace Guardrail的模型、Provider、ZDR、内容过滤或预算规则仍需由控制台确认。用户要求图片闭环成功后再撤销Key，因此服务器副本已删除，本机Key尚未撤销。

本轮同时发现并修复正式Project SK签发缺陷：旧实现使用`ActiveChatModelsExist`，会把图片模型统一判为不可授权。修复后只允许已激活且已发布的`chat/image`模型进入显式allowlist；图片调用仍要求`api_key_model_scopes`存在精确模型记录，`all/legacy_all`不会自动获得图片能力。

测试夹具时间窗不得直接假设MySQL `NOW()`与应用`loc=Local`一致。优先通过应用服务创建价格；必须使用SQL夹具时，`effective_at/expires_at/cost_expires_at`需要覆盖应用本地时区比较窗口，并在调用前实际执行Quote验证，不能只用数据库会话时间证明未过期。

## 10. 本机直连诊断与403安全分类

在不经过Molin接口、测试服务器、钱包和对象存储的条件下，使用同一短效测试Key固定调用`google/gemini-3-pro-image + google-vertex/global + 2K + 1:1 + n=1`。本机直连返回HTTP 200和一张`image/png`，图片为4,559,278字节，Provider回执费用为0.134436美元，重试和fallback均为0。图片只在进程内完成Base64解码和签名校验，未落盘，Prompt、Key和原始响应未进入证据正文。

该证据证明OpenRouter账户、Key、模型、Provider tag和固定图片参数本身可用，但不证明测试服务器出口IP、原测试Prompt或Molin运行时请求链路可用。测试服务器后续只读检查确认NTP、DNS、TLS、公开模型目录、Images POST路径和代理环境均无一般性异常，因此旧403继续按“测试服务器路径特有”处理，不能把本机直连成功冒充IMG-G9网关闭环通过。

为避免下一次真实请求仍只留下裸`403`，`OpenRouterImageAdapter`固定发送`X-OpenRouter-Experimental-Metadata: enabled`，但只在内存中读取错误码、错误消息、元数据键、路由阶段名称/摘要和Provider响应状态。HTTP 403最终只能落入以下白名单分类：

| `provider_error_code` | 含义 |
|---|---|
| `403:credit_limit` | Key或账户费用额度不足 |
| `403:workspace_budget` | Workspace预算门禁 |
| `403:model_policy` | 模型访问或allowlist策略 |
| `403:provider_policy` | Provider访问或allowlist策略 |
| `403:data_policy` | ZDR或数据策略 |
| `403:content_guardrail` | Prompt注入、敏感信息、内容过滤或其他Guardrail |
| `403:key_permission` | OpenRouter API Key权限不足 |
| `403:upstream_permission` | 已确认的上游Provider HTTP 403 |
| `403:unknown` | 无法安全归类，失败关闭 |

分类器不得保存或输出完整错误消息、Prompt、Key、Base64、原始响应、Pipeline原文或Provider响应原文。现有`provider_http_status`与`provider_error_code`字段足以承载证据，本次不新增表、migration、重试、fallback或交付路径。

## 11. 第三次测试服务器结果与供应商切换

第三次测试服务器真实调用使用`5f5bc0f14044756a3a2119420c3b0c6c2e90cf75`候选。调用前完成安全预检、数据库/RabbitMQ/MinIO/二进制/环境备份、关闭态安装、专用Project SK、0.50元Quote、钱包和队列门禁。首次网关HTTP因JWT Quote与Project SK归属不一致返回`40420 quote_not_found`，该请求在Provider之前结束，未创建请求、任务或Hold，Provider尝试和费用均为0；随后改用同一Project SK创建正确Quote，才消费唯一Provider授权。

唯一Provider调用仍由OpenRouter返回HTTP 403，低敏分类为`403:unknown`。任务持久化`attempt_count=1`、`provider_code=openrouter-images`和`provider_http_status=403`；Provider费用增量为0，重试和fallback均为0。钱包0.50元Hold完整释放，sale/cost/usage均为0，资产0，Outbox held/released均published，补偿0，对账差异`0.00000000`。验收后恢复原二进制和原环境，图片RabbitMQ队列/exchange、临时MinIO服务账号和服务器Secret目录均已清理，Chat、Bifrost、用户端和管理端基线通过。

同一Key、Gemini模型、Vertex Provider和固定规格在可信本机直连返回HTTP 200，而测试服务器目录GET和Key认证GET正常、真实Images POST返回403，说明Gemini/Vertex失败集中在测试服务器出口或上游控制路径，不能再用付费重试猜测。IMG-G9因此继续不通过。

供应商切换合同如下：

- 当前模型固定`bytedance-seed/seedream-5-0-lite`。
- 当前ProviderTag固定`seed`。
- 规格继续固定`n=1 / 2K / 1:1 / standard / url`。
- OpenRouter官方目录当前显示2K、1:1和n=1均受支持，按张费用0.035美元。
- 同一测试服务器已有Seedream 5.0 Lite真实POC成功证据，但该POC不等于Molin钱包、资产和对账闭环。
- 单次Provider费用上限仍为0.25美元，Adapter和Worker继续零重试、零fallback。
- 本地切换提交不授权真实Provider、测试服务器、部署或远程Git；再次测试必须使用新的SOURCE_COMMIT和单次书面授权。

## 12. Seedream真实闭环验收结果

2026-08-27以`d97baf1400840d5e707b5ed2c9bfc4237885353c`为唯一SOURCE_COMMIT，在已登记测试服务器完成一次Seedream真实图片网关闭环验收。调用固定`bytedance-seed/seedream-5-0-lite + seed + n=1 + 2K + 1:1`，Provider请求精确1次，重试和fallback均为0。

Provider返回HTTP 200和一张图片，Molin完成完整解码、格式/签名/尺寸校验、统一PNG重编码、显式与隐式标识、审核和MinIO私有存储。主图为2048×2048 PNG、195,648字节，SHA-256为`07b41590154a2e5097f18e4524fae31af1bdc8b6720470172331ea21de2608b2`；同时生成512×512缩略图。资产所有者的短效签名下载返回HTTP 200，移除签名参数后的匿名访问返回HTTP 403。

费用和财务事实：

- OpenRouter Key Usage从0.134436增加到0.169436美元，本轮增量精确为0.035美元。
- Quote、Hold和结算金额均为0.50000000元，未超过0.60元钱包上限。
- 钱包流水依次形成freeze、unfreeze和consume，最终余额9.50000000元、冻结0。
- `usage_fact`、`sale_line`和`cost_line`各1条；销售行0.50元，非商业测试成本行0.30元。
- 主图与缩略图共2个available资产；主图可交付数量为1。
- Outbox的held、settled和delivery_available三条事件均published且retry=0；补偿任务0。
- 请求、Quote、钱包、Usage、资产和Outbox对账差异为`0.00000000`。

安全和回滚事实：

- Prompt、OpenRouter Key、Project SK和Base64在普通日志、MySQL任务摘要与RabbitMQ消息中的匹配均为0。
- 调用结束后立即恢复traffic=false、OpenRouter=false，再恢复原API二进制SHA-256 `1f27cf69f0b1f76648e55453060b87b794a168dc6a94af06e5fd1fbed09a84fc`和原环境。
- 测试Project已归档、Project SK已吊销、测试模型/价格已退役、测试用户已禁用；请求、钱包、Usage、成本、资产、Outbox和审计事实完整保留。
- RabbitMQ图片队列/exchange、临时MinIO服务账号和服务器Secret目录均已清理；两个交付对象继续保存在私有`ai-result` bucket中。
- 回滚后health/ready为200，Chat为401鉴权语义，图片路由恢复404，Bifrost与用户端/管理端均为200。
- OpenRouter账号侧`gatwayimage` Key已撤销，旧Key认证GET返回HTTP 401；服务器Key副本不存在。

受限证据目录为`/home/pc/molin/backups/img-g9-d97baf1-20260827T010540Z`，最终清单SHA-256为`10f9b93f8fb7d4069eef3fd58fa31cf1bf0ad968e66cb93343cc26bf44bc4be1`。

该结论只证明测试环境真实Provider和非商业人民币测试计费闭环，不代表正式价格、生产开放、客户流量或商业验收；完成后立即停止，不自动进入IMG-G10。
