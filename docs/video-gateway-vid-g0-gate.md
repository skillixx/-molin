# VID-G0：视频网关决策冻结、Bifrost合同预探针与最小人工问题包

> 当前阶段：`VID-G0`
>
> 当前结论：`HUMAN_REQUIRED`
>
> 记录日期：2026-08-27
>
> 代码基线：`a44c9bc2c0b25b2e106a5d65f7276d73fa932f75`
>
> 当前分支：`codex/video-gateway-goal-doc`
>
> 证据边界：本记录已形成G0-A关闭态工程合同并执行G0-B本地零费用Bifrost+Fake预探针；Runware、底层Runway Gen-4.5、`runway:1@2`、5秒规格、候选商业阈值和关闭态法律策略已经冻结，但尚未执行真实Provider调用、DPA签署、测试服务器部署、真实人民币结算、生产开放或商业验收。

## 1. 目标问题结论

文生视频和图生视频已经收敛为同一个异步视频深模块，并冻结了关闭态工程所需的协议、Fake规格、计费状态机、安全、输入资产、并发、存储、轮询、取消、对账和回滚合同。

当前用户已明确要求执行VID-G0至VID-G8，并要求普通代码、文档、Fake配置和可逆工程问题由Codex自动处理。因此本轮形成以下治理决定：

```text
DECISION_ID=VID-DEC-ENGINEERING-20260827-001
VIDEO_ENGINEERING_EXCEPTION_APPROVED=YES
VID_G0_G8_NON_COMMERCIAL_TEST_FIXTURE_ALLOWED=YES
REAL_PROVIDER_REQUESTS_AUTHORIZED=NO
TEST_SERVER_WRITE_AUTHORIZED=NO
GIT_COMMIT_PUSH_PR_AUTHORIZED=YES
GIT_MERGE_AUTHORIZED=NO
PRODUCTION_OPEN_AUTHORIZED=NO
COMMERCIAL_ACCEPTED=NO
```

但VID-G0仍不能`AUTO_PASS`，原因不是普通工程问题：当前授权只覆盖commit、push和创建PR，不包含产品经理合并。DPA、真实Provider调用、正式法律适用结论、生产和商业验收属于G9/G10后续边界，不在本轮执行。

## 2. G0-B锁定镜像与实际合同结论

### 2.1 锁定版本

2026-08-27只读核对发现，Bifrost最新稳定HTTP Transport已经从仓库旧文档的`v1.6.6`升级为带破坏性变更的`v2.0.0`。视频G0不得把旧版本证据复用为当前结论。

```text
BIFROST_IMAGE_TAG=maximhq/bifrost:v2.0.0
BIFROST_IMAGE_DIGEST=maximhq/bifrost@sha256:cf71be9fad4e0749b6e26cbb774c687413dad9a0970b83f4e1dadb6f503ea208
BIFROST_RELEASE=v2.0.0
BIFROST_RELEASE_KIND=STABLE_WITH_BREAKING_CHANGES
```

官方资料：

- [Bifrost v2.0.0 Release](https://github.com/maximhq/bifrost/releases/tag/transports/v2.0.0)
- [Bifrost Generate Video](https://docs.getbifrost.ai/api-reference/videos/generate-a-video)
- [Bifrost Runway视频能力](https://docs.getbifrost.ai/providers/supported-providers/runway)
- [Bifrost Retry与Fallback](https://docs.getbifrost.ai/features/retries-and-fallbacks)

### 2.2 探针范围

探针使用：

- 锁定摘要的Bifrost v2.0.0。
- 本机回环Fake OpenAI视频上游。
- 固定测试模型`openai/sora-2`，只作为Bifrost合同夹具，不是墨灵正式Provider选择。
- 文生与图生create。
- 复合ID retrieve、content、delete。
- list诊断。
- 上游500、响应超时、上游已接收请求但ACK丢失。
- Fake提交次数、图生参考图转发和上游鉴权存在性。

探针不读取真实Key，不访问真实Provider，不创建真实钱包事实，不连接测试服务器。

### 2.3 实际结果

```text
UNIT_TESTS=23/23 PASS
PREFLIGHT=PASS
BIFROST_HEALTH=200
TEXT_TO_VIDEO_CREATE=200
IMAGE_TO_VIDEO_CREATE=200
IMAGE_REFERENCE_FORWARDED=YES
IMAGE_REFERENCE_SIZE_SHA256_MATCH=YES
RETRIEVE=2/2 PASS
CONTENT=2/2 PASS
DELETE=2/2 PASS
LIST=PASS
UPSTREAM_500_SUBMIT_COUNT=1
UPSTREAM_TIMEOUT_SUBMIT_COUNT=1
ACK_DROP_SUBMIT_COUNT=4
REAL_PROVIDER_REQUESTS=0
PROVIDER_COST=CNY 0
CONTRACT_ASSERTIONS=42/43
CONTRACT_RESULT=FAIL_ACK_DROP_HIDDEN_RETRY
```

低敏回执：[`evidence/video-gateway-vid-g0-bifrost-contract.json`](./evidence/video-gateway-vid-g0-bifrost-contract.json)

测试源码快照不包含上述回执和源码清单两个生成型证据文件，避免输出文件把自己的hash递归写入自身。源码、配置、测试和合同文档全部参与`TESTED_SOURCE_STATE`；最终独立复核另绑定包含回执在内的完整`REVIEWED_SOURCE_STATE`。规范化快照字段见[`evidence/video-gateway-vid-g0-source-state.json`](./evidence/video-gateway-vid-g0-source-state.json)。

复现命令：

```powershell
python -I -W error::ResourceWarning infra/scripts/test_probe_bifrost_video_contract.py
python -I -W error::ResourceWarning infra/scripts/probe-bifrost-video-contract.py
python -I -W error::ResourceWarning infra/scripts/probe-bifrost-video-contract.py --execute --source-state <SOURCE_STATE_ID> --receipt docs/evidence/video-gateway-vid-g0-bifrost-contract.json
```

### 2.4 根因与工程裁决

`network_config.max_retries=0`能够阻止Bifrost核心层对普通500和超时进行再次提交，但不能阻止HTTP传输层对“服务端已经收到POST、随后连接在首个响应字节前断开”的请求重发。Fake上游实际收到4次相同高成本create。

Bifrost源码公开说明中的`StaleConnectionRetryIfErr`认为此类错误发生在服务端处理前，因此允许重发；G0故障注入证明“服务端已创建任务、ACK丢失”同样可能呈现该错误。对高成本异步视频create而言，这个假设不足以证明安全。

因此冻结：

```text
VIDEO_EXECUTION_DRIVER=native_async
BIFROST_VIDEO_CREATE_ENABLED=NO
BIFROST_VIDEO_STATUS_ENABLED=NO
BIFROST_VIDEO_CONTENT_ENABLED=NO
BIFROST_VIDEO_DELETE_ENABLED=NO
BIFROST_VIDEO_RETRY_WAIVER_ALLOWED=NO
```

在满足以下任一条件前，不允许把视频执行切回Bifrost：

1. 锁定的新Bifrost版本提供可验证的非幂等POST传输重试禁用开关，故障注入下Fake任务数始终为1。
2. 锁定Provider正式支持客户端幂等键，并证明4次HTTP重发只形成1个Provider任务、1笔Provider成本。
3. Provider支持按Molin request_id查询已创建任务，且Adapter在未知结果时只查询恢复、不重新create。

这项裁决只决定执行驱动，不代表外部Provider已经选定。

## 3. G0-A产品、协议与模型合同

### 3.1 统一领域边界

```text
CAPABILITY=video.generate
OPERATIONS=text_to_video,image_to_video
PROJECT_SK_SCOPE=video:generate
PUBLIC_MODEL_CODE=molin/video-standard
N=1
DRIVER=native_async
FALLBACKS=NONE
PROVIDER_RETRY=0
```

- 先完成text_to_video Tracer，再在同一VideoGateway上增加image_to_video。
- 两种operation共用请求、Quote、Hold、Task、Usage、Provider成本、资产、Outbox、回调和对账事实。
- 图生视频不是第二套网关、第二套钱包或第二套任务表。
- `molin/video-standard`是关闭态工程公开代码；没有外部Provider决定和正式价格时不得发布为可调用模型。

### 3.2 Provider与模型冻结

| 项目 | 当前结论 | 证据边界 |
|---|---|---|
| 外部Provider | Runware | 项目负责人于2026-08-27明确批准；不授权真实调用 |
| 底层模型 | Runway Gen-4.5 | Runware官方模型页明确支持T2V和I2V |
| provider_model_id | `runway:1@2` | 固定AIR版本；变化触发合同重验 |
| Provider标签 | `runware` | 只用于内部路由，不进入外部Video Job |
| 区域 | `global/public-api` | Runware公共API；生产网络落点仍需G9留痕 |
| API端点 | `POST https://api.runware.ai/v1` | 请求为task数组，固定`taskType=videoInference` |
| 客户端任务ID | `taskUUID=Molin预生成UUIDv4` | 创建前持久化，可用于ACK丢失恢复和审计 |
| 关闭态规格 | 5秒、`1280x720`、无音频 | `runway:1@2`只允许5、8、10秒；首发取最小值 |
| 目录成本信号 | 约0.60 USD/5秒720p | 不是实际成本、人民币成本或正式销售价 |
| 主路由 | `native_async` | Bifrost ACK丢失隐藏重试不满足高成本提交要求 |
| 运行时fallback | 关闭 | 不允许跨Provider自动切换 |
| Bifrost | 保留为以后重新评估的可插拔驱动 | 当前不得进入视频数据面 |
| MVP Provider准入 | `PASS_TASKUUID_RECOVERY` | Molin预生成taskUUID；`getTaskDetails/getResponse`按同一UUID恢复 |

VID-G1至VID-G8继续只使用`fake/video-standard`和非商业测试夹具。Runware选择仅冻结后续Adapter合同，不授权创建账号、读取Key、上传用户数据或产生费用。Provider训练、保留和删除条款采用失败关闭值：未完成DPA与法务复核前`EXTERNAL_DATA_TRANSFER=NO`，该门禁在VID-G9真实调用授权前必须重新验证。

官方依据：

- [Runware统一任务合同](https://runware.ai/docs/platform/introduction)
- [Runware按taskUUID恢复任务详情](https://runware.ai/docs/platform/task-details)
- [Runware异步轮询](https://runware.ai/docs/platform/task-polling)
- [Runware Runway Gen-4.5 `runway:1@2`](https://runware.ai/docs/models/runway-gen-4-5)
- [Runware错误合同](https://runware.ai/docs/platform/errors)

### 3.2.1 Runware原生任务与ACK恢复合同

| 能力 | 冻结合同 | 当前证据 |
|---|---|---|
| 创建 | `POST /v1`，发送单元素数组；taskType=`videoInference`、taskUUID、model、positivePrompt、width、height、duration | Runware统一任务合同 |
| T2V/I2V | T2V不携带`inputs.frameImages`；I2V携带一张`frame=first`的规范化图片 | `runway:1@2`模型合同 |
| ACK恢复 | create前持久化taskUUID；网络未知时只调用`getTaskDetails`/`getResponse`查询同一UUID，不生成新UUID | Runware任务详情与轮询合同 |
| 查询 | `getResponse`返回`processing/success/error`和progress；`getTaskDetails`恢复原请求与原响应 | Runware官方文档 |
| 取消 | SDK取消只停止本地等待，Provider任务继续执行并可能计费；Molin记录`cancel_requested`并继续查询/对账 | Runware Python SDK合同 |
| 回调 | 首版不依赖Provider回调，只使用持久化轮询；以后启用Webhook时按taskUUID幂等 | 当前关闭态策略 |
| 结果URL | 默认保留7天；只由Asset Fetch Worker读取，完成归档后不向客户端暴露Provider URL | Runware统一任务合同 |
| Usage/成本 | 请求固定`includeCost=true`以收集Provider成本候选；墨灵销售额仍只读取CNY价格快照 | Runware统一响应合同 |
| 错误 | HTTP 400/401/402/403/404/429/500/503与错误code归一化；429/5xx只查询同taskUUID，不重新create | Runware错误合同 |
| 超时 | create超时不等于任务失败；同taskUUID进入`pending_reconcile`并查询恢复 | 平台状态合同 |
| List | 不作为任务事实源；外部列表始终读取Molin账本 | 平台兼容差异 |

恢复规则：Molin在本地事务中生成一次UUIDv4 taskUUID并与request_id、provider_route_version一起持久化；第一次create和所有恢复查询使用同一值。`taskNotFound`不能立即触发第二次create，必须完成至少三次带退避查询并核对本地submit时间窗；仍无法确认时保持`pending_reconcile`，不产生第二个Provider任务。Runway direct的旧决定保留为已否决历史证据，不再进入MVP数据面。

### 3.3 关闭态Fake规格

以下值只用于VID-G0至VID-G8本地/隔离工程，不是外部Provider正式合同：

| 参数 | 冻结值 |
|---|---|
| 文生/图生 | 均支持 |
| 时长 | 5秒 |
| 分辨率 | 1280×720 |
| 容器 | MP4 |
| Codec | H.264 |
| 帧率 | 24fps |
| 音轨 | 无 |
| 张数/视频数 | n=1 |
| Prompt | 1～1000个Unicode字符，UTF-8不超过4KiB；采用Runware `runway:1@2`的更严格交集 |
| 输出最大正文 | 256MiB |
| 结果URL | 只返回墨灵短效签名URL，TTL 15分钟 |

外部Provider最终规格必须取“墨灵工程上限”和“锁定Provider已验证上限”的更严格交集；不得用Fake规格声称真实Provider可用。

## 4. 图生视频输入资产合同

### 4.1 格式和资源上限

| 项目 | 工程冻结值 | 失败行为 |
|---|---:|---|
| 参考图数量 | 1 | 多图返回422 |
| 格式 | 静态JPEG、PNG | 其他格式返回422 |
| 文件大小 | 10MiB | 流式上传立即中止，不创建Quote/Hold |
| 最短边 | 640px | 返回422 |
| 最长边 | 4096px | 返回422 |
| 总像素 | 16,777,216 | 解码前后双重校验 |
| 宽高比 | 0.5～2 | 采用Runway Gen-4.5官方输入上限的更严格交集，超限返回422 |
| 解码内存预算 | 128MiB | 超限失败关闭 |
| 上传超时 | 30秒 | 清理临时对象 |
| 单用户同时上传 | 2 | 返回429 |
| 单Project同时上传 | 4 | 返回429 |

默认拒绝SVG、GIF、APNG、动画WebP、HEIC、TIFF和PSD。客户端不能提供任意外部URL、bucket、object_key或Provider临时URL。

### 4.2 规范化顺序

```text
归属和状态
  → Content-Length与流式字节上限
  → 魔数、MIME和完整解码
  → 动画帧、宽高、像素、比例和解码预算
  → EXIF方向归一化
  → 删除GPS和非必要EXIF
  → 转换sRGB
  → 透明PNG合成到中性背景
  → OCR/视觉安全审核
  → 生成不可变normalized_sha256和version
  → Quote/Hold
```

- 禁止静默拉伸。
- 需要裁剪时必须由用户预览确认；确认后生成新input asset version并使旧Quote失效。
- 原图和规范化副本分别保存hash、状态和生命周期。
- 只有同一用户、同一Project、审核通过、可交付、未过期的图片资产可绑定图生任务。

## 5. OpenAI兼容快照v1

版本化OpenAPI：[`video-gateway-openapi-snapshot-v1.yaml`](./video-gateway-openapi-snapshot-v1.yaml)

### 5.1 路径

| 路径 | 语义 | 数据源 |
|---|---|---|
| `POST /v1/videos` | multipart创建文生/图生任务 | VideoGateway |
| `GET /v1/videos` | 当前主体可见任务列表 | Molin任务账本 |
| `GET /v1/videos/{video_id}` | 查询公开任务状态 | Molin任务账本 |
| `GET /v1/videos/{video_id}/content` | 流式/Range下载MP4 | Molin对象存储 |
| `DELETE /v1/videos/{video_id}` | 删除终态媒体正文 | Molin资产生命周期 |
| `/api/token/videos/*` | Quote、输入资产、事件、账单、取消和管理增强 | Molin平台协议 |

### 5.2 有意兼容差异

| 项目 | OpenAI可观察合同 | Molin快照v1 |
|---|---|---|
| 生命周期 | queued/in_progress/completed/failed | 外部保持四态；内部另有cancel_requested和pending_reconcile |
| 幂等 | 未作为创建必填字段 | 强制`Idempotency-Key` |
| 报价 | 客户端直接创建 | 服务端先Quote/Hold，失败前不产生费用 |
| 模型 | OpenAI模型枚举 | 墨灵公开逻辑模型，不暴露Provider模型 |
| 参考图 | file_id或image_url | 只接受受控上传或同Project已有资产，不抓取任意URL |
| 删除 | 删除视频和资产 | 删除媒体正文与兼容可见性，保留财务和审计事实 |
| 列表 | Provider/平台列表 | 只读取Molin任务账本，不依赖Bifrost list |
| 下载 | 视频或派生内容 | MVP只交付MP4，支持有界Range |
| Provider字段 | 可能可见 | 对外隐藏Provider、成本、上游ID和临时URL |
| API存续 | 官方API计划关闭 | Molin快照由自身维护，独立弃用 |

### 5.3 SDK夹具

截至本轮官方GitHub Release页面可直接验证的固定夹具：

```text
PYTHON_SDK_FIXTURE=openai==2.45.0
TYPESCRIPT_SDK_FIXTURE=openai@6.39.0
RAW_HTTP_FIXTURE=REQUIRED
```

这些版本只用于兼容回归，不表示SDK在OpenAI删除Videos资源后仍受支持。每次升级必须先运行快照差异测试；SDK资源删除后以原始HTTP示例为长期SSOT。

### 5.4 原始HTTP示例

文生视频：

```bash
curl -X POST "${MOLIN_BASE_URL}/v1/videos" \
  -H "Authorization: Bearer ${MOLIN_PROJECT_SK}" \
  -H "Idempotency-Key: vid-t2v-example-0001" \
  -F "model=molin/video-standard" \
  -F "prompt=一只纸飞机穿过安静的图书馆" \
  -F "seconds=5" \
  -F "size=1280x720"
```

图生视频：

```bash
curl -X POST "${MOLIN_BASE_URL}/v1/videos" \
  -H "Authorization: Bearer ${MOLIN_PROJECT_SK}" \
  -H "Idempotency-Key: vid-i2v-example-0001" \
  -F "model=molin/video-standard" \
  -F "prompt=镜头缓慢靠近，主体轻微转身" \
  -F "input_reference=@reference.png;type=image/png" \
  -F "seconds=5" \
  -F "size=1280x720"
```

上述示例是文档结构，不得在仓库或聊天中替换为真实SK。

## 6. 异步状态、轮询、租约和取消

### 6.1 三轴状态

```text
执行轴：queued → submitting → submitted → processing → succeeded|failed|unknown
取消轴：none → cancel_requested → cancel_accepted|cancel_rejected|cancel_unknown
计费轴：quoted → held → pending_reconcile → settled|released|adjusted
交付轴：none → fetching → moderating → available|rejected|media_deleted
```

外部OpenAI四态映射：

- queued、submitting、submitted → queued。
- processing、cancel_requested且Provider仍在执行 → in_progress。
- succeeded且资产已审核、结算和可交付 → completed。
- 明确失败且账本已释放 → failed。
- unknown、cancel_unknown、pending_reconcile保持最后安全外部状态，并通过平台增强接口显示内部解释；不得猜测完成或免费。

### 6.2 轮询和租约

| 项目 | 工程冻结值 |
|---|---:|
| 初始轮询间隔 | 2秒 |
| 退避阶梯 | 2、5、10、15秒 |
| 抖动 | ±20% |
| 最大轮询间隔 | 15秒 |
| Worker heartbeat | 10秒 |
| 执行租约 | 30秒 |
| 租约续期 | heartbeat成功时CAS续期 |
| fencing | 单调递增lease_version |
| 最大执行观察窗 | 20分钟 |
| 超窗行为 | 进入unknown/pending_reconcile，不重新create |

`next_poll_at`、`lease_owner`、`lease_version`、`heartbeat_at`和`attempt_count`必须持久化。仅持有当前fencing token的Worker可以提交Provider、更新终态或结算。

### 6.3 取消与计费矩阵

| 场景 | Provider动作 | 用户销售额 | Hold | Provider成本 | 最终处理 |
|---|---|---:|---|---|---|
| queued且未提交 | 不调用Provider | 0 | 全额释放 | 0 | cancelled |
| 提交中但未获ACK | 只按request_id查询恢复 | 暂不结算 | 保持 | 待核对 | unknown/pending_reconcile |
| Provider确认取消且无产物 | 记录取消事实 | 0 | 全额释放 | 按账单事实 | cancelled |
| Provider拒绝取消 | 继续跟踪 | 暂不结算 | 保持 | 按事实 | cancel_rejected |
| 取消后迟到成功 | 抓取、审核、对账 | 按冻结政策人工/自动裁决 | 保持至对账 | 按事实 | pending_reconcile |
| 明确失败且无产物 | 不重试create | 0 | 全额释放 | 按账单事实 | failed |
| 结果成功但输出审核拒绝 | 不交付 | 0 | 全额释放 | 保留成本事实 | rejected |
| 归档失败 | 不重新生成 | 暂不结算 | 保持 | 保留成本事实 | compensation |

## 7. 价格、Quote和成本熔断

### 7.1 非商业测试价格夹具

VID-G0至VID-G8只允许以下显式测试夹具：

```json
{
  "currency": "CNY",
  "source": "test_fixture",
  "meter_type": "video_seconds",
  "scale": "5",
  "unit_price": "1.00000000",
  "variants": [
    {"operation": "text_to_video", "seconds": "5", "size": "1280x720"},
    {"operation": "image_to_video", "seconds": "5", "size": "1280x720"}
  ]
}
```

- 两种operation价格显式存在，即使测试值相同也不得省略operation。
- 当前不启用`video_megapixel_seconds`。
- Quote有效期5分钟，只能消费一次。
- 正式销售价、Provider美元成本、毛利、税费和退款政策保持未决定。

### 7.2 工程成本熔断

| 维度 | VID-G0～G8关闭态值 | 生产值 |
|---|---:|---|
| 全局真实Provider请求 | 0 | HUMAN_REQUIRED |
| 单Provider真实Provider请求 | 0 | HUMAN_REQUIRED |
| 单Project真实Provider请求 | 0 | HUMAN_REQUIRED |
| 单日Provider成本 | CNY 0 | HUMAN_REQUIRED |
| Fake异常提交次数 | 单请求1次 | 必须保持 |

任一真实Provider请求或非零Provider成本都触发失败关闭；不得用Bifrost Usage或动态Provider价格直接扣墨灵钱包。

## 8. 容量、上传下载和SLO

| 项目 | 工程冻结值 | 超限行为 |
|---|---:|---|
| 单用户queued | 2 | 429，无Hold、无MQ |
| 单Project queued | 10 | 429，无Hold、无MQ |
| 单用户running | 1 | 429或保持排队 |
| 单Project running | 2 | 保持排队 |
| 单模型running | 2 | 保持排队 |
| 单Provider hard cap | 2 | 保持排队 |
| 平台队列上限 | 100 | 429，无Hold、无MQ |
| 队列年龄告警/严重 | 30秒/120秒 | 告警并停止接收新任务 |
| Create HTTP P95/P99 | 500ms/1s | 只衡量入队，不包含生成时长 |
| Fake完成P95/P99 | 5秒/10秒 | 关闭态工程目标 |
| 视频结果抓取超时 | 60秒 | 补偿，不重新生成 |
| Range并发：用户/Project | 2/4 | 429 |
| 单连接下载带宽 | 20MiB/s | 限速 |
| 慢客户端写超时 | 30秒 | 中止连接，不改变资产事实 |

容量值是可收紧的关闭态工程默认值，不代表测试服务器或生产已经具备对应资源。

## 9. 安全、标识和留存

### 9.1 失败关闭规则

- Prompt、参考图正文、视频正文、Base64、签名URL、Provider原始响应和Key不得进入普通日志、MySQL或RabbitMQ。
- 真人、未成年人、换脸、身份冒用、裸露、暴恐和违法素材默认拒绝；审核不可用或不确定时失败关闭。
- 用户必须确认参考图使用权、肖像授权和必要版权；声明版本、时间、user_id和project_id进入审计。
- 商标、角色和版权高风险请求进入拒绝或人工复核，不因用户声明绕过平台审核。
- 显式“AI生成”水印和隐式生成属性任一写入或复检失败时，不得available、结算或签发下载URL。

### 9.2 工程留存

| 对象 | 工程默认留存 |
|---|---:|
| 未完成上传会话 | 24小时 |
| 已完成但未绑定任务的输入资产 | 7天 |
| 已绑定规范化输入 | 任务终态后7天 |
| 审核拒绝输入 | 24小时 |
| quarantined输入 | 30天 |
| 成功视频媒体正文 | 30天候选，生产需重新批准 |
| 失败/取消临时视频 | 24小时 |
| 短效签名URL | 15分钟 |
| 标识和交付审计 | 至少6个月候选，法务确认后生效 |

legal hold、争议和申诉优先阻止删除。Provider侧训练、保留和删除能力尚未取得正式合同，外部Provider未锁定前不得上传用户数据。

## 10. 权限与maker/checker

| 权限 | 作用 | maker/checker |
|---|---|---|
| `video:view` | 查询任务和公开资产 | 普通授权 |
| `video:model` | 编辑模型草稿 | 发布需产品复核 |
| `video:price` | 编辑价格草稿 | 发布需财务复核 |
| `video:task` | 运维任务、重放安全补偿 | 高风险动作双人复核 |
| `video:safety` | 审核和策略配置 | 解除隔离需安全复核 |
| `video:reconcile` | 对账和调账 | 财务maker/checker |
| `video:resource` | 容量、并发和队列 | 运维复核 |
| `video:retention` | 留存和清理策略 | 法务/安全复核 |
| `video:secret` | Secret引用和轮换 | 运维maker/checker，不显示明文 |
| `video:release` | 开关和发布 | 产品+运维checker |

权限码如需进入正式角色，必须由后续seed migration创建；不得只在代码中写字符串。

## 11. VID-G10商业候选阈值

项目负责人已于2026-08-27批准以下VID-G10候选阈值，并明确兼任本阶段财务批准人。该决定只冻结未来验收阈值，不授权生产、客户流量、正式价格或真实费用：

```text
COMMERCIAL_THRESHOLD_DECISION_ID=VID-DEC-COMMERCIAL-20260827-003
COMMERCIAL_THRESHOLD_DECISION_STATUS=VALID
DESIGN_CUSTOMERS=3
REAL_API_INTEGRATIONS=2
PAYING_CUSTOMERS=1
OBSERVATION_DAYS=14
SUCCESS_RATE_MIN=95%
P95_GENERATION_MAX=15m
P99_GENERATION_MAX=20m
GROSS_MARGIN_MIN=30%
RECONCILIATION_DIFFERENCES=0
P0=0
P1=0
SIGNED_BY=产品,财务,安全,法务,运维,项目负责人
```

阈值在价格、成本、客户范围或观察周期发生变化时自动失效；VID-G10仍须实际观察证据和所有签署角色通过。

## 12. 初始阻塞审计

| BLOCKER_ID | 类别 | 当前状态 | 结论 |
|---|---|---|---|
| VID-BLK-001 | P1/执行安全 | CODEX_AUTO_RESOLVED | Bifrost ACK丢失提交4次；MVP切换`native_async`并关闭Bifrost视频数据面 |
| VID-BLK-002 | P1/状态机 | CODEX_AUTO_RESOLVED | 已冻结执行、取消、计费、交付四轴和迟到成功矩阵 |
| VID-BLK-003 | P1/Schema | CODEX_AUTO_RESOLVED | G1固定Expand-only，down不删除视频、财务、资产或审计事实 |
| VID-BLK-004 | P1/并发 | CODEX_AUTO_RESOLVED | 已冻结next_poll_at、退避、heartbeat、租约和fencing |
| VID-BLK-005 | P1/容量 | CODEX_AUTO_RESOLVED | 已冻结关闭态容量、队列、上传下载和Fake SLO；生产值另行授权 |
| VID-BLK-006 | P1/资源保护 | CODEX_AUTO_RESOLVED | 已冻结multipart和Range的大小、超时、并发、带宽和慢客户端保护 |
| VID-BLK-007 | P1/成本 | CODEX_AUTO_RESOLVED | G0～G8全局/Provider/Project真实请求和成本均为0 |
| VID-BLK-008 | P1/Provider准入 | CODEX_AUTO_RESOLVED | 已批准Runware `runway:1@2`，Molin预生成taskUUID并按同一UUID恢复ACK丢失任务 |
| VID-BLK-009 | P1/商业签署 | CODEX_AUTO_RESOLVED | 项目负责人明确兼任本阶段财务批准人并确认第11节阈值 |
| VID-BLK-010 | 人工授权 | CODEX_AUTO_RESOLVED | 已批准第9节关闭态法律/数据失败关闭策略；DPA与生产法律结论留到G9/G10 |
| VID-BLK-011 | 人工授权 | CODEX_AUTO_RESOLVED | 已授权当前VID-G0范围的commit、push和创建PR |
| VID-BLK-012 | 人工授权 | CODEX_ESCALATED_HUMAN | merge仍未授权，且必须由产品经理按仓库流程执行 |

## 13. 缺陷台账

| DEFECT_ID | SEVERITY | DEFECT_STATUS | RESOLUTION | SUMMARY | ROOT_CAUSE | EVIDENCE | TESTED_SOURCE_STATE | REVIEWED_SOURCE_STATE | CLOSED_AT |
|---|---|---|---|---|---|---|---|---|---|
| DEF-VID-G0-001 | P1 | CLOSED_VERIFIED | FIXED：关闭Bifrost视频数据面并冻结`native_async` | ACK丢失导致高成本create重复提交 | Bifrost传输层`StaleConnectionRetryIfErr`不受Provider `max_retries=0`完全约束 | G0-B回执、ACK丢失4次提交 | `41626fca8f0306ddb8c2dcedc54b0779f555c0bc3120a5ed4b44b6d77109fef6` | `7d23099b4a2761d5bd81531abdf42bebeacb1a269b91591d0f9945584db56902` | 2026-08-27T10:42:07Z |
| DEF-VID-G0-002 | P1 | CLOSED_VERIFIED | FIXED：故障HTTP状态、逐场景鉴权和动态断言计数进入测试门禁 | 初版验证器不能证明全部故障与鉴权合同 | 初版只检查Fake次数和全局鉴权OR值，且断言总数硬编码 | Python单测12/12与动态合同断言40/41 | `41626fca8f0306ddb8c2dcedc54b0779f555c0bc3120a5ed4b44b6d77109fef6` | `7d23099b4a2761d5bd81531abdf42bebeacb1a269b91591d0f9945584db56902` | 2026-08-27T10:42:07Z |
| DEF-VID-G0-003 | P1 | CLOSED_VERIFIED | FIXED：OpenAPI与G6的Project SK、model可省略、null字段、Range和Request-ID合同对齐 | G0 OpenAPI与G6冻结合同漂移 | 初版OpenAPI在G6合同对账前独立形成 | Redocly校验与逐字段合同对账 | `41626fca8f0306ddb8c2dcedc54b0779f555c0bc3120a5ed4b44b6d77109fef6` | `7d23099b4a2761d5bd81531abdf42bebeacb1a269b91591d0f9945584db56902` | 2026-08-27T10:42:07Z |
| DEF-VID-G0-004 | P1 | CLOSED_VERIFIED | FIXED：按固定低风险PNG的长度和SHA-256验证端到端转发 | 初版只证明input_reference字段或PNG魔数存在 | 为降低证据敏感度过度压缩为布尔值，未证明正文完整 | Python单测、当前G0-B回执中的size/hash/match字段 | 当前回执的`tested_source_state` | 本轮最终门禁输出的`REVIEWED_SOURCE_STATE` | 本轮最终复核时间 |
| DEF-VID-G0-005 | P2 | CLOSED_VERIFIED | FIXED：补齐统一门禁报告全部字段 | 初版门禁只输出核心字段 | G0文档摘要误替代统一门禁模板 | 第16节统一门禁报告 | 当前源码清单的`source_state_id` | 本轮最终门禁输出的`REVIEWED_SOURCE_STATE` | 本轮最终复核时间 |
| DEF-VID-G0-006 | P1 | CLOSED_VERIFIED | FIXED：文件内保留历史关闭快照，最终输出绑定当前完整复核快照 | 生成证据的自引用hash与源码复核状态混用 | 测试源、生成证据和最终复核没有明确分层 | source-state.json、G0-B回执、最终三类Agent复核 | 当前回执的`tested_source_state` | 本轮最终门禁输出的`REVIEWED_SOURCE_STATE` | 本轮最终复核时间 |
| DEF-VID-G0-007 | P1 | CLOSED_VERIFIED | FIXED：切换Runware并使用Molin预生成taskUUID恢复 | Runway direct无法恢复ACK丢失任务 | Provider仅在成功响应后返回task ID | Runware taskUUID/getTaskDetails/getResponse官方合同与最终复核 | 当前源码快照 | 本轮最终门禁输出 | 2026-08-27T15:35:38Z |
| DEF-VID-G0-008 | P1 | CLOSED_VERIFIED | FIXED：项目负责人明确兼任本阶段财务批准人 | VID-G10候选阈值缺少财务签署身份 | 初次批准只记录项目负责人身份 | 用户明确`FINANCE_APPROVER=PROJECT_OWNER_ACTING_AS_FINANCE` | 当前源码快照 | 本轮最终门禁输出 | 2026-08-27T15:35:38Z |

只有`CLOSED_VERIFIED`不计入开放P0/P1；源码快照变化后上述状态自动退回`FIXED_PENDING_VERIFY`。人工授权阻塞不是实现缺陷，不计入P0/P1，但会使阶段保持`HUMAN_REQUIRED`。

## 14. 决策账本

| DECISION_ID | OWNER | APPROVED_BY | APPROVED_AT | SOURCE | APPLIES_TO | STATUS | REVALIDATE_ON |
|---|---|---|---|---|---|---|---|
| VID-DEC-ENGINEERING-20260827-001 | 项目负责人 | 当前用户 | 2026-08-27 | Goal执行指令 | G0～G8关闭态、Fake、零费用工程 | VALID | 用户撤销、范围扩大、真实费用或生产动作 |
| VID-DEC-DRIVER-20260827-001 | Codex工程裁决 | 独立工程/QA/产品复核 | 2026-08-27T10:42:07Z | G0-B锁定镜像故障注入 | Bifrost v2.0.0视频执行驱动 | VALID | Bifrost版本、传输重试能力或Provider幂等合同变化 |
| VID-DEC-PROVIDER-20260827-002 | 项目负责人 | 当前用户 | 2026-08-27T13:35:52Z | `RUNWAY_ROUTE=SINGLE_GEN4_5` | Runway/gen4.5/global/public-api/API 2024-11-06/native_async；真实请求0 | STALE | Provider能力已确认，但ACK恢复不满足MVP准入；需新Provider决定 |
| VID-DEC-COMMERCIAL-20260827-003 | 项目负责人+财务 | 当前用户（项目负责人兼任财务批准人） | 2026-08-27T15:35:38Z | `COMMERCIAL=APPROVE_CANDIDATE_THRESHOLDS` + `FINANCE_APPROVER=PROJECT_OWNER_ACTING_AS_FINANCE` | 第11节VID-G10商业阈值 | VALID | 价格、成本、客户范围或验收周期变化 |
| VID-DEC-LEGAL-20260827-004 | 项目负责人（关闭态） | 当前用户 | 2026-08-27T13:35:52Z | `LEGAL=APPROVE_CLOSED_ENGINEERING_POLICY` | 第9节关闭态真人/未成年人/版权/留存/标识与`EXTERNAL_DATA_TRANSFER=NO` | VALID | 法规、Provider条款、DPA或产品范围变化 |
| VID-DEC-GIT-20260827-005 | 项目负责人 | 当前用户 | 2026-08-27T13:35:52Z | `GIT=AUTHORIZE_COMMIT_PUSH_PR` | 当前VID-G0文件的commit、push、PR；不含merge | VALID | 授权动作、分支或提交范围变化 |
| VID-DEC-MERGE-20260827-006 | 项目负责人+产品经理 | NONE | NONE | 尚未授权 | 当前VID-G0 PR merge | PENDING | PR提交、CI或评审结论变化 |
| VID-DEC-PROVIDER-20260827-007 | 项目负责人 | 当前用户 | 2026-08-27T15:35:38Z | `VIDEO_PROVIDER=RUNWARE_RUNWAY_GEN4_5_TASKUUID_5S` | Runware/runway:1@2/5秒/1280x720/native_async；真实请求0 | VALID | Provider、模型、taskUUID恢复、价格或数据合同变化 |

只读`git fetch origin main`已经自动执行并确认`origin/main=a44c9bc2...`，无需人工授权；该决定不授权本地commit、push、PR或merge。

## 15. 人工决定记录与剩余问题包

### 15.1 已批准Provider替换

```text
DECISION_ID=VID-DEC-PROVIDER-20260827-007
BLOCKER_ID=VID-BLK-008
当前证据=Runway direct的gen4.5支持T2V/I2V，但没有公开客户端幂等或request_id恢复；Runware API允许Molin预生成taskUUID，并通过getTaskDetails/getResponse按同一UUID恢复；Runware提供底层Runway Gen-4.5模型`runway:1@2`
批准值=外部Provider为Runware，底层模型runway:1@2，native_async，无fallback，5秒1280x720；taskUUID=Molin预生成request-scoped UUID
成本影响=Runware目录候选约0.60 USD/5秒；与Runway direct 5秒目录成本同量级，但实际成本仍需G9核定
备选项=继续Runway direct但保持MVP Provider未通过；或提供Runway官方幂等/request_id恢复合同
影响范围=Provider Adapter字段、关闭态Fake合同、首发时长由4秒调整为5秒、G9真实验收
精确授权动作=只冻结Provider/模型/5秒规格和恢复合同，不授权账号、Key、数据传输或真实调用
费用上限=CNY 0
有效期=Runware taskUUID/getTaskDetails合同或runway:1@2版本变化前
负责人=项目负责人
```

候选依据：

- [Runware统一任务合同](https://runware.ai/docs/platform/introduction)
- [Runware按taskUUID恢复原请求与响应](https://runware.ai/docs/platform/task-details)
- [Runware Runway Gen-4.5模型`runway:1@2`](https://runware.ai/docs/models/runway-gen-4-5)

### 15.2 已批准财务签署

```text
DECISION_ID=VID-DEC-COMMERCIAL-20260827-003
BLOCKER_ID=VID-BLK-009
当前批准=项目负责人已批准第11节全部候选值
财务批准人=PROJECT_OWNER_ACTING_AS_FINANCE
影响范围=只影响VID-G10商业验收，不授权生产或客户流量
费用上限=CNY 0
负责人=项目负责人+财务
```

### 15.3 已批准关闭态法律与数据策略

```text
DECISION_ID=VID-DEC-LEGAL-20260827-004
BLOCKER_ID=VID-BLK-010
批准值=关闭态工程采用第9节最严格失败关闭值；DPA和正式法律结论完成前不向外部Provider上传用户数据
影响范围=图生视频G0门禁、G9真实Provider和G10生产
费用上限=CNY 0
负责人=产品+安全+法务
```

### 15.4 已批准Git动作与待批准merge

```text
DECISION_ID=VID-DEC-GIT-20260827-005
BLOCKER_ID=VID-BLK-011
已批准=本地commit,push,创建PR,等待CI
仍缺失=产品经理执行merge的明确授权
当前范围=仅VID-G0文档、OpenAPI、Fake探针、测试和低敏回执
Codex推荐项=完成commit/push/PR和CI后，由产品经理提交merge授权
影响范围=最终PR、CI、产品经理合并和MAIN_CONTAINS_ACCEPTED_COMMIT
费用上限=CNY 0
负责人=项目负责人
```

## 16. 当前门禁报告

```text
GATE=VID-G0
SOURCE_COMMIT=WORKTREE
BASE_COMMIT=a44c9bc2c0b25b2e106a5d65f7276d73fa932f75
HEAD_COMMIT=a44c9bc2c0b25b2e106a5d65f7276d73fa932f75
ORIGIN_MAIN_COMMIT=a44c9bc2c0b25b2e106a5d65f7276d73fa932f75
ORIGIN_MAIN_REMOTE_URL=github.com/<owner>/-molin.git
ORIGIN_MAIN_PROVENANCE=FRESH_FETCH
ORIGIN_MAIN_OBSERVED_AT=见docs/evidence/video-gateway-vid-g0-source-state.json
TRACKED_PATCH_SHA256=见docs/evidence/video-gateway-vid-g0-source-state.json
UNTRACKED_MANIFEST_SHA256=见docs/evidence/video-gateway-vid-g0-source-state.json
SOURCE_STATE_ID=见docs/evidence/video-gateway-vid-g0-source-state.json
EVIDENCE_CAPTURED_AT=见docs/evidence/video-gateway-vid-g0-source-state.json
DECISION=HUMAN_REQUIRED
CODE_STATE=branch codex/video-gateway-goal-doc；WORKTREE；PUSHED=NO
SCOPE_COMPLETED=G0-A关闭态工程合同已形成；Runware/runway:1@2/taskUUID恢复与5秒规格已冻结；G0-B锁定Bifrost v2.0.0+Fake实际预探针；Bifrost隐藏重试根因和native_async裁决；OpenAPI快照；状态/计费/安全/容量/留存/权限矩阵
OPERATION_RESULTS=text_to_video合同通过；image_to_video合同和input_reference通过；ACK丢失出现4次上游提交，Bifrost视频数据面关闭
TEST_EVIDENCE=Python单测23/23；Runware T2V/I2V/taskUUID恢复和Prompt 1000/1001边界合同通过；Bifrost动态合同断言42/43，唯一失败为ACK丢失重复提交墓碑；图生参考图长度和SHA-256一致；Bifrost+Fake create/retrieve/content/delete/list实际探针；Redocly和Go全量测试通过
TEST_MATRIX=VID-G0-PY-001..023；VID-G0-RUNWARE-T2V；VID-G0-RUNWARE-I2V；VID-G0-RUNWARE-ACK-RECOVERY；VID-G0-RUNWARE-PROMPT-LIMIT；VID-G0-B-T2V；VID-G0-B-I2V；VID-G0-B-LIST；VID-G0-B-500；VID-G0-B-TIMEOUT；VID-G0-B-ACK-DROP；VID-G0-OAS；VID-G0-GO-REGRESSION
CODEX_BLOCKER_AUDIT=HUMAN_REQUIRED
BLOCKER_SUMMARY=VID-BLK-001..011已解决；VID-BLK-012 merge授权仍需人工
AUTO_RESOLVED_BLOCKERS=VID-BLK-001..011
AUTO_CONFIRMED_OPEN_BLOCKERS=NONE
HUMAN_REQUIRED_BLOCKERS=VID-BLK-012
BLOCKER_VERIFY_EVIDENCE=第2节G0-B回执、第12节阻塞表、第13节缺陷台账、Python/Redocly/Go命令输出
INDEPENDENT_AGENT_REVIEWS=工程PASS(P0=0/P1=0/P2=0)；QA PASS(覆盖率估计86%，P0=0/P1=0/P2=1非阻断测试债)；产品PASS(P0=0/P1=0/P2=0)；最终复核绑定当前工作树精确REVIEWED_SOURCE_STATE
DECISION_LEDGER=第14节VID-DEC-ENGINEERING/DRIVER/PROVIDER/COMMERCIAL/LEGAL/GIT/MERGE
DEFECT_LEDGER=第13节DEF-VID-G0-001..008
SOURCE_LEVEL=L1
PROVIDER_CONTRACT_LEVEL=L2
SCHEMA_LEVEL=NOT_IN_SCOPE
BILLING_LEVEL=L0
SECURITY_LEVEL=L0
FRONTEND_LEVEL=NOT_IN_SCOPE
RUNTIME_LEVEL=L2
TEST_ENV_RUNTIME=NO
DEPLOY_SOURCE_COMMIT=NOT_IN_SCOPE
BINARY_SHA256=NOT_IN_SCOPE
CONFIG_SHA256=NOT_IN_SCOPE
MIGRATION_SET=NOT_IN_SCOPE
IMAGE_DIGEST=maximhq/bifrost@sha256:cf71be9fad4e0749b6e26cbb774c687413dad9a0970b83f4e1dadb6f503ea208
REAL_PROVIDER_REQUESTS=0
PROVIDER_COST=CNY 0
CNY_TEST_SETTLEMENT=NOT_IN_SCOPE
RECONCILIATION_DIFFERENCES=NOT_IN_SCOPE
ROLLBACK=PASS
P0=0
P1=0
P2=1（非阻断测试债：服务启动/健康失败、少数HTTP/Handler异常分支及Runware真实或沙箱HTTP E2E留待后续授权阶段）
QA_ACCEPTANCE=PASS
PM_CONFIRMATION=PASS
DEV_CODE_REVIEW=PASS
CI_STATUS=PENDING
REVIEW_THREADS_RESOLVED=YES
BRANCH_POLICY=PASS
PR_STATE=NOT_CREATED
PR_NUMBER=NOT_APPLICABLE
MERGE_COMMIT=NOT_APPLICABLE
PR_MERGED_BY=NOT_APPLICABLE
PR_METADATA_EVIDENCE=NOT_APPLICABLE
PM_MERGE_POLICY=PENDING
MAIN_CONTAINS_ACCEPTED_COMMIT=NO
EXTERNAL_ACTION_AUTHORIZED=YES(commit,push,PR only)
NEXT_GOAL_ALLOWED=NO
EVIDENCE_BOUNDARY=Runware/runway:1@2/taskUUID恢复已冻结但未证明真实Provider、DPA、实际成本、测试服务器、真实人民币结算、生产或商业验收
HUMAN_QUESTIONS=VID-DEC-MERGE-20260827-006；仅在PR/CI完成后询问
```
