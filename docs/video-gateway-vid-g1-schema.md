# VID-G1：视频 Expand Schema、兼容性与验收合同

> 阶段：`VID-G1`
>
> 当前状态：`LOCAL_SCHEMA_GREEN / HUMAN_REQUIRED`
>
> 进入基线：`origin/main@f9aff4d2aace3d9bf862a88f0ed6304e2953dacc`
>
> 前置证据：VID-G0经PR #416完成squash merge并取得`AUTO_PASS`
>
> 证据边界：本阶段只建立Expand Schema、Go模型、事务不变量与本地隔离验证。它不注册视频HTTP路由，不实现页面、Provider Adapter、Worker、钱包运行逻辑、测试环境migration或生产流量。真实Provider请求固定为0，费用固定为CNY 0。

## 1. 功能说明

VID-G1把现有只面向Chat和图片的AI网关事实表扩展为可承载视频的共享媒体底座。文生视频`text_to_video`与图生视频`image_to_video`不创建两套任务或账本，而是复用同一组请求、报价、任务、输入、回调、Usage、资产和审计事实。

本阶段解决四件事：

1. 让既有Chat和图片数据、二进制及唯一约束保持兼容。
2. 为视频任务显式保存`operation`，禁止根据输入数量或JSON正文反推业务类型。
3. 把图生视频的来源对象规范化为独立、不可变的输入快照，并用执行租约防止任务运行期间被清理。
4. 为异步任务追加事件、Provider回调去重和加密敏感载荷建立持久化边界。

### 1.1 使用角色

| 角色 | 本阶段用途 | 可执行动作 |
|---|---|---|
| 后端开发者 | 编写migration、Go模型和事务不变量 | 仅本地代码与测试 |
| 测试工程师 | 验证首次up、重复up、down/re-up、旧数据兼容和约束 | 仅本地隔离MySQL/Fake |
| 产品经理 | 确认两种operation共用体系且范围没有提前进入VID-G2 | 文档与验收确认 |
| 管理员 | 后续按细粒度权限查看或管理视频事实 | VID-G1不提供HTTP或页面入口 |
| 用户/Project SK | 后续创建与查询视频任务 | VID-G1不可调用 |

### 1.2 本阶段没有页面或接口入口

VID-G1不新增可调用端点。`/v1/videos`、`/api/token/videos/*`和`/api/admin/token/video-*`仍只是VID-G0冻结的未来合同，不得因为表和Go模型存在就声称接口可用。用户控制台和管理后台同样没有VID-G1页面验收。

## 2. 设计解释

### 2.1 为什么采用Expand-only

Chat和图片已经在同一套请求、价格、钱包、Usage与资产表中形成历史事实。直接重命名字段、收紧旧默认值或在down中删除列，会让旧二进制无法写入，也可能删除财务和审计证据。因此VID-G1只增加可空列、扩大允许枚举、新增表和索引；旧Chat/Image行在不写新字段时仍保持原语义。

### 2.2 为什么共用任务体系

```text
Chat/Image既有事实
        │
        ├── ai_requests / ai_gateway_quotes / ai_gateway_tasks
        ├── ai_usage_items / 钱包关联 / Outbox
        └── ai_gateway_assets
                        ▲
                        │ Expand，不复制账本
                        │
text_to_video ──────────┼────────── image_to_video
  0个TaskInput          │             1个reference_image
                        │
        upload/source asset → normalized input snapshot
                        │
        task event / callback event / encrypted payload
```

共享体系让同一`request_id`可以追溯Quote、任务、Usage、回调和资产，避免平行视频账本产生两份余额、两套对账和相互矛盾的状态。

### 2.3 为什么必须保存operation

`operation`固定取`text_to_video`或`image_to_video`，写入请求、Quote、任务和Usage事实。输入JSON会演进，输入资产也可能在生命周期结束后删除；如果依赖输入数量反推operation，历史计费和审计会随清理结果漂移。

价格表在VID-G1只扩大`video_seconds/video_megapixel_seconds`模板、同名meter及variant JSON的表达能力，不增加价格表`operation`列。operation规范化、选价和Quote行为属于VID-G2，VID-G1不得提前实现或声称价格可用。

### 2.4 source到snapshot的血缘

上传对象或已有图片资产都只是来源。系统必须从受控ObjectStore读取来源，完成方向规范化、移除不必要EXIF/地理信息、格式校验与审核后，写入独立私有输入对象，并记录：

- 来源类型和来源事实：`upload_session_id`或`source_gateway_asset_id`二选一。
- `original_sha256`与`normalized_sha256`。
- MIME、大小、宽高、审核策略版本和`version_no`。
- 私有对象定位、生命周期、过期、legal hold和删除请求时间。

`ai_gateway_task_inputs`只绑定规范化快照，不保存上传临时URL、Provider URL、来源对象键或签名URL。来源资产后续变化不能改变已创建任务看到的字节内容。

### 2.5 执行租约

以下条件同时成立时，TaskInput构成活动租约：

1. `lease_released_at IS NULL`；
2. 任务处于任一非终态，或处于`pending_reconcile`；
3. 输入资产仍绑定该任务。

只有任务安全终结、计费对账完成并在同一受控流程记录`lease_released_at`后，租约才能释放一次。清理查询必须排除活动租约、legal hold和未到`pending_delete_at`的资产。数据库索引支撑查询；跨行状态判断和“一次释放”由Service事务强制，不能伪装成单表CHECK。

## 3. Migration参考

### 3.1 文件清单

| Migration | 作用 | down策略 |
|---|---|---|
| `000072_expand_video_gateway_schema` | 扩展既有共享表并建立上传、输入、事件、回调、密文载荷表 | 保留式no-op，不删除列、表、任务、资产、财务或审计事实 |
| `000073_seed_video_gateway_permissions` | 幂等补齐视频细粒度权限并仅映射`admin`角色 | 保留式no-op，不删除权限或历史授权事实 |

两个up必须可重复执行。完整链路从`000001`执行至`000073`；down/re-up验证的是脚本可安全重放，不表示生产允许降级或物理删除事实。

### 3.2 既有表扩展

| 表 | VID-G1扩展 | 兼容规则 |
|---|---|---|
| `ai_requests` | `operation`；`modality=video`；`capability=video.generate` | Chat保持`chat/chat.completions/operation=NULL`；Image保持`image/image.generate/operation=NULL` |
| `ai_price_versions` | 允许`video_seconds/video_megapixel_seconds`定价模板与视频limits表达 | 不增加operation列；VID-G2才冻结视频选价 |
| `ai_price_skus` | 允许`video_seconds`、`video_megapixel_seconds`及规范化variant | 既有Token/Image meter继续有效 |
| `ai_gateway_quotes` | 可空`operation`并允许`video.generate` | 既有图片Quote不回填operation，不改写金额 |
| `ai_gateway_tasks` | 可空`operation`、`bifrost_provider`、`bifrost_task_id`、`bifrost_compound_id`及轮询索引 | `public_id`继续作为Molin外部ID；内部/Provider/Bifrost ID不外露 |
| `ai_gateway_assets` | `modality`、DECIMAL(10,3)时长/帧率、容器、视频/音频Codec、`has_audio`、`media_deleted_at`；视频使用content/preview/thumbnail等角色 | 图片行保持`modality=image`、既有角色集合且视频列为空；删除媒体正文仍保留账单和审计元数据 |
| `ai_usage_items` | 可空`operation`；允许seconds与megapixel-seconds计量语义 | Chat/Image历史Usage不改写；财务Decimal精度和唯一约束不降低 |

### 3.3 新表

#### `ai_upload_sessions`

保存上传入口归属、用途和完成事实。核心字段为`public_id`、`user_id`、`project_id`、`api_key_id`、`purpose`、`source_type`、`status`、MIME、大小、私有对象定位、`source_etag`/`source_version_id`、`final_input_asset_id`、截止时间及完成/拒绝/取消/过期时间。

上传会话的`source_type`只支持：

- `platform_presigned`
- `openai_inline_multipart`

状态固定为`created/uploading/verifying/completed/rejected/cancelled/expired`。完成必须只绑定一个最终input asset，并至少保存`source_etag`或`source_version_id`之一；拒绝、过期或取消分别记录终止时间，重复完成由Service事务和数据库唯一关系共同拒绝。`purpose`当前固定为`video_reference_image`，输入MIME只允许PNG/JPEG。

#### `ai_gateway_input_assets`

保存独立规范化输入快照。核心字段为用户/Project归属、`source_type`、`upload_session_id`或`source_gateway_asset_id`、私有对象定位、原始/规范化SHA-256、媒体元数据、审核策略版本/状态、版本号、生命周期、过期、legal hold和删除时间。Project SK归属在上传会话完成前校验；input asset本身不重复保存`api_key_id`。

来源字段必须二选一：上传来源使用`platform_presigned/openai_inline_multipart + upload_session_id`，已有资产来源使用`gateway_asset_snapshot + source_gateway_asset_id`。无论哪种来源都生成独立私有快照；MySQL不保存图片正文或Base64。

`upload_session_id`是一对一完成关系；`source_gateway_asset_id`故意不唯一，同一已有图片可在不同时间或审核策略版本下形成多个独立快照，历史任务仍绑定各自的hash和`input_version`。

`normalizing/moderating`期间，规范化对象定位、规范化hash、MIME、大小、宽高和策略版本允许为空；一旦进入`ready`，这些字段必须全部有效、MIME必须为JPEG/PNG且`moderation_status=passed`。这样既允许分步处理，又不会把半成品误判为可执行输入。

#### `ai_gateway_task_inputs`

保存任务与输入快照的绑定。字段为`task_id`、`input_asset_id`、`user_id`、`project_id`、`role`、`ordinal`、`normalized_sha256`、`input_version`、`lease_released_at`。

`role`当前只允许`reference_image`，`ordinal=0`。唯一键`(task_id,role,ordinal)`阻止重复参考图；组合外键阻止跨用户、跨Project绑定。

#### `ai_gateway_task_events`

追加式记录任务事件，包含全局唯一`event_id`、自增序列、任务、用户、Project、来源、事件类型、前后状态、`safe_detail_json`和创建时间。来源只允许`api/worker/provider_callback/reconciler/system`。禁止UPDATE/DELETE式覆盖历史状态；低敏详情不得保存Prompt、媒体正文或秘密。

#### `ai_gateway_provider_callback_events`

保存Provider回调去重、验签和应用结果，包含可空本地任务归属、`provider_code`、`provider_task_id`、`external_event_id`、正文SHA-256、签名状态、低敏应用结果、处理状态和时间。重放唯一键固定为`(provider_code,provider_task_id,external_event_id)`。尚不能关联本地任务的合法回调允许任务/用户/Project三列全空；关联后必须三列全非空并通过组合外键。原始回调正文不落库。

#### `ai_gateway_task_payloads`

每个任务可以按`prompt/provider_request/provider_result`各保存一个密文信封，唯一键为`(task_id,payload_kind)`。每行包含用户/Project、`ciphertext`、12字节`nonce`、`key_version`、`aad_sha256`和`ciphertext_sha256`。表内不保存密钥或明文；JSON序列化不得暴露密文信封内部字段。

### 3.4 关键约束与索引

| 约束/索引 | 证明的规则 |
|---|---|
| `chk_ai_requests_capability_delivery` | Chat/Image保持旧组合；Video必须`video.generate + operation + 媒体交付状态` |
| 既有`chk_ai_requests_image_stream` | 非Chat请求包括Image/Video都必须`is_stream=0` |
| `chk_ai_gateway_quotes_capability` / `chk_ai_gateway_tasks_capability` | 图片operation为空；视频operation只能二选一 |
| `chk_ai_gateway_tasks_bifrost_ref` | Bifrost三字段全空，或provider非空且task/compound至少一项非空 |
| `uk_ai_gateway_tasks_bifrost_ref` / `uk_ai_gateway_tasks_bifrost_compound` | 同一Bifrost Provider任务或复合ID不能绑定多个Molin任务 |
| `idx_ai_gateway_tasks_bifrost_poll` | 按Bifrost引用、任务状态与`next_poll_at`进行受控轮询 |
| `chk_ai_usage_media_unit` | 单位只允许tokens/count/megapixels/seconds/megapixel_seconds；视频meter必须带operation，其他meter必须operation为空；币种仍为CNY或空 |
| `chk_ai_price_sku_media_variant` | 视频meter的variant JSON必须显式包含合法`$.operation`；缺失、未知operation或无hash均失败 |
| `chk_ai_gateway_assets_role` / billable / parent | Image只允许既有primary_output根角色；Video使用content根角色，preview/thumbnail等派生物必须关联父资产，不放宽旧图片约束 |
| `chk_ai_gateway_assets_available` | 图片保持原门禁；可用视频必须MP4、时长/帧率/Codec完整且音频字段自洽 |
| `chk_ai_gateway_assets_media_delete` | 只有生命周期已到deleted/delete_failed时才能记录`media_deleted_at`，不把删除请求冒充正文已删除 |
| `uk_ai_upload_sessions_final_asset` | 一个规范化input asset不能成为多个上传会话的完成结果 |
| `uk_ai_upload_sessions_object_owner` | 同用户、Project、bucket、object_key不能创建第二个上传会话或快照入口 |
| `uk_ai_gateway_input_assets_upload` | 一个上传会话最多形成一个input asset |
| `idx_ai_gateway_input_assets_cleanup` | 按生命周期、legal hold、待删除时间和过期时间找清理候选 |
| `uk_ai_gateway_task_inputs_task_role_ordinal` | 同一任务不能重复绑定`reference_image,0` |
| `uk_ai_gateway_task_inputs_task_asset` | 同一任务不能重复绑定同一快照 |
| `idx_ai_gateway_task_inputs_lease` | 按未释放租约、任务和输入资产筛选 |
| `uk_ai_gateway_task_events_event_id` | 事件重放不会生成第二条同ID事件 |
| `uk_ai_gateway_provider_callbacks_replay` | 同Provider任务的同外部事件只能应用一次 |
| `uk_ai_gateway_task_payloads_kind` | 同任务每种密文载荷最多一行 |
| `trg_ai_gateway_task_events_no_update/no_delete` | MySQL在写入层拒绝修改或删除既有TaskEvent，追加式不只依赖调用方约定 |

上述组合外键均使用`ON DELETE RESTRICT`。数据库不级联删除任务、输入、回调、资产、Usage或审计事实。

MySQL的CHECK使用三值逻辑：表达式结果为`UNKNOWN`时可能被当成未违反约束。VID-G1因此不能只写`value IN (...)`。视频Request/Quote/Task/Usage的operation分支必须显式`IS NOT NULL`；视频SKU还必须断言`JSON_EXTRACT(variant_json,'$.operation') IS NOT NULL`。输入资产进入`ready`时，bucket、object key、规范化hash、MIME、大小、宽高和审核策略版本也必须逐项`IS NOT NULL`，再校验格式和值域。动态矩阵必须分别拒绝SQL NULL、JSON缺字段、JSON null和错误operation。

## 4. 业务不变量

### 4.1 operation与输入数量

| operation | TaskInput数量 | 事务规则 |
|---|---:|---|
| `text_to_video` | 0 | 任一参考图都拒绝 |
| `image_to_video` | 恰好1 | 缺失或多于1张都拒绝 |

该计数必须在创建请求、任务和TaskInput的同一Service事务内完成。数据库唯一键只能阻止相同`role+ordinal`重复，不能统计跨行数量。

VID-G1为此只提供一个窄域事务入口`CreateVideoSchemaFacts`：

1. 在打开事务前完成全部纯校验，包括operation、输入数量、请求/任务能力、request ID、公开任务ID、Quote、逻辑模型、用户/Project归属，以及图生输入的角色、序号、规范化SHA-256和版本。
2. 校验全部通过后，只调用一次`VideoSchemaTransactor.WithinTransaction`。
3. 事务内按`InsertRequest → InsertTask`顺序写事实；仅`image_to_video`继续写唯一`InsertTaskInput`，并使用同事务内回填的任务ID。
4. 任一写入、事务回调或提交失败时，必须回滚全部暂存事实；非法命令必须在事务开始前失败，数据库写入增量为0。

该入口是VID-G1事务合同，不是Repository实现：它没有CAS、Quote计算、钱包、消息队列、Provider调用或状态编排能力，这些仍属于后续Goal。

### 4.2 租户归属

- 上传会话、输入资产、TaskInput和任务都包含`user_id + project_id`组合归属。
- 存在`api_key_id`时，必须通过`(api_key_id,project_id,user_id)`组合外键证明同一租户。
- `source_gateway_asset_id`只能引用同用户、同Project图片资产。
- 对跨租户查询或绑定统一返回不泄露存在性的失败，不能暴露内部自增ID。

### 4.3 ID边界

- `ai_gateway_tasks.public_id`是Molin兼容快照v1的`video_id`。
- Provider任务ID、Bifrost provider/task/compound ID和内部自增ID只供服务端对账。
- Bifrost复合ID不得拼入或替代外部`video_id`。
- 共享Quote/Task/Asset及六个视频新模型的内部自增`id`均使用`json:"-"`隐藏；旧图片`PublicID`序列化合同保持不变。
- 未来VID-G6只能由专用DTO把`Task.PublicID`映射为`video_id`，不得把Go模型或内部ID直接作为OpenAI兼容响应。

### 4.4 内容与秘密边界

- 上传或图片正文不进入MySQL；RabbitMQ后续只能传`input_asset_id`或`request_id`。
- Prompt和其他敏感任务载荷只进入受控内存或加密payload表。
- 禁止保存原始Provider回调/响应正文、长期签名URL、明文SK和密钥。
- 普通DTO及JSON序列化不得暴露对象键、内部hash、Provider标识或密文信封字段。

## 5. 状态与生命周期

### 5.1 上传会话

```text
created ──开始接收对象──> uploading ──完整性核验──> verifying ──生成snapshot──> completed
   │                          │                 ├──校验或审核拒绝────────────> rejected
   │                          └──用户取消──────> cancelled
   └────────────────────────────超过expires_at───────────────────────────> expired
```

`completed/rejected/cancelled/expired`均为终态。重复完成必须幂等返回同一`final_input_asset_id`或报告冲突，禁止形成第二个快照；每种非成功终态使用自己的时间字段，不能混用取消时间冒充拒绝或过期。

`CompleteVideoUploadSession`把上传完成冻结为唯一事务：加锁读取会话并校验同一用户/Project、`verifying`、当前时间未超过`expires_at`、ETag或VersionID存在且尚未绑定最终输入；随后插入同归属、同来源会话的规范化snapshot，使用数据库回填ID完成会话。任何状态漂移、过期、重复完成、跨归属、来源漂移或写入/提交失败均零部分提交。数据库同时要求`completed_at<=expires_at`，防止绕过Service直接把过期会话改成完成。

### 5.2 输入资产

```text
pending ──开始规范化──> normalizing ──进入审核──> moderating ──通过──> ready
                              │                     ├──策略拒绝──> rejected
                              │                     └──需隔离────> quarantined
                              └──处理失败且需隔离────────────────> quarantined

ready/rejected/quarantined ──删除请求且无活动租约/legal hold──> pending_delete
pending_delete ──到清理窗口──> expiring ──持锁删除──> deleting ──确认──> deleted
                                                        └──失败──> delete_failed
```

来源资产变化不修改快照。legal hold或活动租约存在时，不得进入实际删除。

`RequestVideoInputPendingDelete`冻结删除请求的唯一窄域事务守卫：

1. `InputAssetID`、期望`user_id/project_id`和请求时间先做纯校验。
2. 同一事务内按`InputAssetID+user_id+project_id`执行`LoadInputForUpdate`并再次核对返回归属；只允许`ready/rejected/quarantined`进入删除申请。
3. `legal_hold=true`立即失败。
4. `CountActiveLeasesForUpdate`加锁统计活动租约，只有计数为0才允许继续。
5. 同一事务把`delete_requested_at`和`pending_delete_at`写为本次时间；读取、租约统计、状态检查或写入任一步失败都必须零提交。

该守卫不实现Repository、ObjectStore删除或清理Worker。数据库CHECK只保护单行状态形状，活动租约的跨表竞态必须由该Service事务负责，不能用“存在租约索引”冒充删除安全。

### 5.3 任务与租约

任务继续复用并扩展共享状态：`created/reserved/queued/submitting/submitted/processing/fetching/storing/moderating/labeling/succeeded/failed/cancelled/expired/pending_reconcile`。`pending_reconcile`虽然不是执行中的Provider状态，仍需保留输入租约，直到费用和最终事实安全收口。

`ReleaseVideoInputLeases`在唯一事务中锁定Task、AIRequest和全部TaskInput。`succeeded`只在账单`settled`后释放；`failed/cancelled/expired`只在账单`released`或`settled`后释放；非终态、`pending_reconcile`、归属/request_id漂移或任一完成时间缺失均拒绝。只更新`lease_released_at IS NULL`的输入，已释放输入和文生视频零输入幂等成功，任一步读取、写入或提交失败均零部分提交。

## 6. 权限点

`000073`幂等创建以下权限，并只自动授予全局`admin`角色；普通用户、Project SK和其他角色不自动继承：

| 权限码 | 用途边界 |
|---|---|
| `video:view` | 查看低敏视频任务和资产元数据 |
| `video:model` | 管理视频模型配置 |
| `video:price` | 管理视频价格候选 |
| `video:task` | 处理受控任务操作 |
| `video:safety` | 安全审核与隔离 |
| `video:reconcile` | 对账与异常处理 |
| `video:resource` | 并发和资源策略 |
| `video:retention` | 留存与清理策略 |
| `video:secret` | Provider秘密配置治理 |
| `video:release` | 发布与开关审批 |

权限seed不等于HTTP路由已注册，也不授权测试环境、真实Provider或生产操作。

## 7. 代码结构

| 路径 | 职责 |
|---|---|
| `server/migrations/000072_expand_video_gateway_schema.*.sql` | Expand Schema和保留式down |
| `server/migrations/000073_seed_video_gateway_permissions.*.sql` | 权限seed和保留式down |
| `server/migrations/ai_gateway_video_g1_migration_test.go` | migration静态合同与隔离脚本安全合同 |
| `server/internal/modules/token_gateway/model/ai_ledger.go` | 请求与Usage的operation扩展 |
| `server/internal/modules/token_gateway/model/ai_image.go` | 共享Quote、任务和资产媒体扩展 |
| `server/internal/modules/token_gateway/model/ai_video.go` | 上传、输入、事件、回调和payload模型 |
| `server/internal/modules/token_gateway/service/video_schema_invariant.go` | 创建事实、上传完成、输入删除申请和租约释放四个窄域事务合同；不含Repository/CAS/ObjectStore/Worker |
| `server/internal/modules/token_gateway/repository/image_media_isolation_test.go` | 证明图片Repository不会读取或改写共享表中的视频任务/资产 |
| `server/internal/modules/token_gateway/service/image_media_isolation_test.go` | 证明图片结算、Provider领取、恢复和取消链不会命中视频事实 |
| `infra/scripts/verify-video-gateway-migration-000072.sh` | 本地无出口MySQL完整链路验证 |

VID-G1不应出现视频Handler、路由注册、Provider Adapter、Worker、钱包结算服务或Vue页面代码；出现这些内容视为提前进入后续Goal。

## 8. 如何执行本地验收

### 前置条件

- 当前分支必须是VID-G1语义分支，不得在`main`直接开发。
- 只使用本地Go、静态检查和本机已有MySQL镜像。
- 隔离MySQL使用内部Docker网络、无宿主端口、`--pull=never`和tmpfs。
- 禁止读取真实Provider Key、连接项目数据库或测试服务器。

### 验证顺序

1. 运行模型、migration与Service不变量定向测试。
2. 运行`go test ./...`、`go vet ./...`及目标包race测试。
3. 运行migration静态扫描，确认up幂等、down无破坏语句、无敏感字段。
4. 在隔离MySQL执行`000001→000073`首次up、重复up、`000073/000072` down和re-up。
5. 插入旧Chat、旧Image、text-to-video和image-to-video夹具，验证兼容、归属、唯一、回调重放、租约与清理索引。
6. 核对`provider_calls=0`、`wallet_writes=0`、无残留容器和网络。

### 当前本地证据

2026-08-28已使用隔离MySQL完成`000001→000073`首次up、重复up、保留式down/re-up。动态回执覆盖`preexisting_chat_image/upload_expiry/expired_complete_rejected/duplicate_complete/cross_owner_complete/source_snapshot/price_operation_variant/safe_lease_release/null_fail_closed/empty_string_fail_closed/pending_delete_guard/task_event_append_only/video_asset_null_fail_closed/payload_crypto/callback_state_shape/bifrost_uniqueness/permission_admin_only`，以及T2V/I2V、组合归属、唯一键、租约和回调重放；回执明确`provider_calls=0`、`wallet_writes=0`。Go模型、migration与Service定向测试覆盖纯校验、创建、上传完成、删除申请和租约释放事务的原子提交、任一步失败回滚、内部ID隐藏及图片/视频共享表隔离。测试执行时修复保持`FIXED_PENDING_VERIFY`；随后已在同一源码快照完成独立QA、产品、工程和规范复核，并在第11节按复核结论关闭。

## 9. 测试矩阵

| ID | 场景 | 必须证明 |
|---|---|---|
| VID-G1-MIG-001 | 完整首次up | `000001→000073`成功 |
| VID-G1-MIG-002 | 重复up | 000072/000073幂等 |
| VID-G1-MIG-003 | down/re-up | 不删除事实且可重放 |
| VID-G1-COMPAT-001 | `preexisting_chat_image` | 先写入完整旧Chat/Image事实再执行000072/000073，默认、金额、hash、状态和写入合同不变 |
| VID-G1-OP-001 | text-to-video | operation显式、TaskInput为0 |
| VID-G1-OP-002 | image-to-video | operation显式、恰好1个reference image |
| VID-G1-OWNER-001 | 组合归属 | 跨用户/Project/SK绑定失败 |
| VID-G1-UPLOAD-001 | `upload_expiry` | expired终态字段完整，不能伪装completed/cancelled |
| VID-G1-UPLOAD-001A | `expired_complete_rejected` | 过期verifying会话即使携带合法ETag和同归属snapshot也不能完成 |
| VID-G1-UPLOAD-002 | `duplicate_complete` | 同最终input asset或同对象重复完成不能形成第二份事实 |
| VID-G1-UPLOAD-003 | `cross_owner_complete` | 跨用户/Project完成上传被组合外键拒绝 |
| VID-G1-SNAPSHOT-001 | `source_snapshot` | 已有图片形成独立快照；源资产隔离状态变化不修改snapshot hash/version，源事实受FK保护 |
| VID-G1-PRICE-001 | `price_operation_variant` | 两种operation各有合法视频variant；视频meter缺失/非法operation失败 |
| VID-G1-NULL-001 | `null_fail_closed` | Request/Quote/Task/Usage operation和SKU JSON operation对SQL NULL、缺字段、JSON null、错误值全部失败；ready字段逐项非空 |
| VID-G1-NULL-002 | `video_asset_null_fail_closed` | 可用视频缺MIME、时长、帧率或音频标记任一字段均失败关闭 |
| VID-G1-EMPTY-001 | `empty_string_fail_closed` | 上传版本标识、上传对象、ready输入对象及可用视频对象/Codec的空白字符串全部失败关闭 |
| VID-G1-LEASE-001 | 活动任务 | 清理排除未释放租约 |
| VID-G1-LEASE-002 | `safe_lease_release` | created和pending_reconcile保持租约；仅task succeeded且request settled后释放，重复释放不改变 |
| VID-G1-DELETE-001 | `pending_delete_guard` | legal hold、活动租约和不允许生命周期不能进入pending_delete；Service事务任一步失败零提交 |
| VID-G1-CALLBACK-001 | 回调重放 | Provider外部事件重复写入失败/幂等 |
| VID-G1-EVENT-001 | `task_event_append_only` | UPDATE和DELETE均被触发器拒绝，原事件行保持不变 |
| VID-G1-PAYLOAD-001 | 密文信封 | 密钥/明文/敏感字段不暴露 |
| VID-G1-PAYLOAD-002 | `payload_crypto` | 有效AES-GCM信封成功；坏nonce、空密文、坏hash、重复kind和跨owner全部拒绝 |
| VID-G1-CALLBACK-002 | `callback_state_shape` | 未关联回调owner全空合法；部分空和处理状态/时间错配拒绝 |
| VID-G1-BIFROST-001 | `bifrost_uniqueness` | Provider任务及compound唯一键动态拒绝重复，轮询索引完整 |
| VID-G1-PERM-001 | 权限seed | 十类权限幂等且仅admin自动映射 |
| VID-G1-PERM-002 | `permission_admin_only` | 非admin角色的视频权限绑定数量为0 |
| VID-G1-TXN-001 | 文生原子创建 | 一次事务只提交Request→Task |
| VID-G1-TXN-002 | 图生原子创建 | 一次事务提交Request→Task→唯一TaskInput |
| VID-G1-TXN-003 | 纯校验失败 | 非法operation/owner/ID/hash/version在事务前失败，写入0 |
| VID-G1-TXN-004 | 任一步/提交失败 | Request、Task、Input任一步及commit失败都无部分提交 |
| VID-G1-TXN-005 | 输入删除申请事务 | 加锁读资产→校验生命周期/legal hold→加锁统计租约→写两个删除时间，任一步失败无部分提交 |
| VID-G1-TXN-006 | 上传完成事务 | 锁会话→校验归属/状态/过期/版本→插入snapshot→完成会话，任一步失败无部分提交 |
| VID-G1-TXN-007 | 租约释放事务 | 锁Task/Request/Input→校验终态和账单→只释放未释放输入，任一步失败无部分提交 |
| VID-G1-JSON-001 | 内部ID边界 | Quote/Task/Asset和六新模型内部id不进入JSON，PublicID兼容不变 |
| VID-G1-COMPAT-003 | 图片/视频共享表隔离 | 图片Repository、结算、领取、恢复、清理和观测只命中image.generate与modality=image |
| VID-G1-BOUNDARY-001 | 外部副作用 | Provider调用0、钱包写入0、远程写入0 |

## 10. 回滚

VID-G1使用事实保留式回滚：

- `000073.down.sql`不删除权限或角色授权。
- `000072.down.sql`不执行`DROP TABLE`、`DROP COLUMN`、`DELETE`或`TRUNCATE`。
- 应用回滚到旧二进制时，新列保持可空/兼容，旧Chat和图片路径继续工作。
- 物理移除视频列、表或权限属于未来Contract Migration，必须单独盘点事实、备份并授权。

## 11. 缺陷台账

| DEFECT_ID | 严重度 | 状态 | 问题 | 修复 | 待复验证据 |
|---|---|---|---|---|---|
| DEF-VID-G1-001 | P1 | CLOSED_VERIFIED | 上传/输入缺7态、11生命周期、JPEG/PNG边界，且Go模型与DDL漂移 | 冻结完整状态、终态时间、ready门禁并逐字段对齐六新模型 | 状态/MIME/model-schema合同及独立QA/工程复核 |
| DEF-VID-G1-002 | P1 | CLOSED_VERIFIED | 缺Request→Task→I2V Input唯一事务边界 | 新增`CreateVideoSchemaFacts`，事务前纯校验、同事务顺序写入、任一步失败零部分提交 | Service失败注入测试及独立QA/工程复核 |
| DEF-VID-G1-003 | P1 | CLOSED_VERIFIED | 隔离矩阵缺preexisting Chat/Image、上传终态/跨owner/重复完成和source snapshot | 补齐`preexisting_chat_image/upload_expiry/duplicate_complete/cross_owner_complete/source_snapshot`动态断言 | 最终SOURCE_STATE下隔离回执与独立QA复跑 |
| DEF-VID-G1-004 | P1 | CLOSED_VERIFIED | created任务可能错误释放输入租约 | created与pending_reconcile继续持租约，仅task succeeded且request settled后允许一次释放 | `safe_lease_release`动态断言及独立QA复跑 |
| DEF-VID-G1-005 | P1 | CLOSED_VERIFIED | 视频价格variant未显式绑定operation | 视频meter variant JSON强制合法operation并动态插入T2V/I2V两条SKU | `price_operation_variant`动态断言及独立工程/QA复核 |
| DEF-VID-G1-006 | P2 | CLOSED_VERIFIED | 共享媒体与六新模型内部自增ID可随JSON暴露 | 内部自增ID统一`json:"-"`，只保留兼容PublicID；未来由G6 DTO映射video_id | 模型序列化合同与独立工程复核 |
| DEF-VID-G1-007 | P2 | CLOSED_VERIFIED | G0归档和G1状态文档仍反映执行前状态 | 回填PR #416最终事实并同步Schema/API/前端/测试/索引文档 | 文档一致性、链接、敏感扫描与产品复核 |
| DEF-VID-G1-008 | P1 | CLOSED_VERIFIED | MySQL CHECK的NULL/UNKNOWN可绕过operation和ready完整性约束 | operation、SKU JSON operation和ready字段全部显式`IS NOT NULL`后再校验值域，并增加缺字段/JSON null/错误值动态拒绝 | `null_fail_closed`隔离回执及独立QA/工程复核 |
| DEF-VID-G1-009 | P2 | CLOSED_VERIFIED | TaskEvent追加式只靠约定，缺少数据库不可变证据 | 新增BEFORE UPDATE/DELETE触发器并动态验证两种修改失败且原行不变 | `task_event_append_only`隔离回执及独立QA复跑 |
| DEF-VID-G1-010 | P1 | CLOSED_VERIFIED | 可用视频资产的媒体必填字段可被SQL NULL/UNKNOWN绕过 | 视频MIME、大小、尺寸、hash、时长、帧率、容器、Codec和音频标记逐项显式非空，并增加四类NULL动态拒绝 | `video_asset_null_fail_closed`隔离回执及独立工程/QA复核 |
| DEF-VID-G1-011 | P1 | CLOSED_VERIFIED | 过期上传会话仍可能绕过状态形状直接完成 | 增加`completed_at<=expires_at`及`CompleteVideoUploadSession`加锁事务，过期和重复完成零部分提交 | `expired_complete_rejected`与上传事务失败注入测试 |
| DEF-VID-G1-012 | P1 | CLOSED_VERIFIED | 租约安全释放只有验收SQL，缺少Service事务合同和完整终态矩阵 | 新增`ReleaseVideoInputLeases`，锁定Task/Request/Input并按执行/账单终态一次释放 | 四终态、pending_reconcile、归属漂移和失败回滚测试 |
| DEF-VID-G1-013 | P1 | CLOSED_VERIFIED | 输入删除申请只按资产ID锁定，缺少调用人用户/Project作用域 | 删除命令、加锁读取、租约统计和写入全部携带user_id/project_id，并再次核对返回资产归属 | 跨用户/Project、错误loader归属和失败回滚测试 |
| DEF-VID-G1-014 | P1 | CLOSED_VERIFIED | 上传版本标识及对象定位可用空白字符串绕过非NULL门禁 | ETag/VersionID、上传/ready对象和视频对象/Codec统一trim后非空，Service同步拒绝空白版本 | `empty_string_fail_closed`隔离回执及上传事务测试 |
| DEF-VID-G1-015 | P2 | CLOSED_VERIFIED | 测试证据段仍写待复核，与同文档已完成三方复核和缺陷关闭状态矛盾 | 改为“测试时待复核、随后同快照完成复核并关闭”的历史证据口径 | 最终产品轻复核与文档一致性检查 |
| DEF-VID-G1-016 | P2 | CLOSED_VERIFIED | 统一门禁缺少源码哈希、复核证据、分层、部署/财务/PR占位和回滚字段 | 按阶段统一模板补齐全部字段，不适用项显式写明 | 最终产品轻复核与模板字段一致性检查 |
| DEF-VID-G1-017 | P1 | CLOSED_VERIFIED | 共享表扩展后图片Repository和Service缺少显式图片能力/模态过滤，可能读取或清理未来视频事实 | 图片Task全链限定`image.generate + operation IS NULL`，图片Asset全链限定`modality=image`，并新增同owner混合数据隔离测试 | 最终SOURCE_STATE下工程、QA、产品与预落地复核 |
| DEF-VID-G1-018 | P1 | CLOSED_VERIFIED | 图片对象删除前引用检查错误限定`modality=image`，可能误删仍被视频资产引用的同一对象 | 图片清理候选继续限定图片，最终`bucket + object_key`引用保护改为检查全部模态 | 跨模态同对象SQLMock负例、全量Go与独立预落地复核 |
| DEF-VID-G1-019 | P1 | CLOSED_VERIFIED | 图片Quote写入、消费与预占入口可能锁定或消费同owner视频Quote | Create/Consume/既有/非内联/内联入口统一强制`image.generate + operation IS NULL`；内联视频Quote在事务前失败关闭 | 同owner视频Quote消费负例、钱包/数据库写入0及独立预落地复核 |
| DEF-VID-G1-020 | P2 | CLOSED_VERIFIED | 图片资产白名单仍接受视频共享角色`content/preview`，仅依赖数据库CHECK兜底 | Repository与Service图片角色白名单移除`content/preview`并补真实枚举负例 | 角色负例、全量Go与独立预落地复核 |

DEF-VID-G1-001..020均已完成修复和本地动态验证；最终门禁以自排除源码快照及其独立轻签为准。

## 12. 当前统一门禁

以下是开发中的初始门禁，不是完成声明。每次源码、migration、测试或复核变化后，旧`SOURCE_STATE_ID`自动失效。

```text
GATE=VID-G1
SOURCE_COMMIT=见docs/evidence/video-gateway-vid-g1-source-state.json
BASE_COMMIT=f9aff4d2aace3d9bf862a88f0ed6304e2953dacc
HEAD_COMMIT=见docs/evidence/video-gateway-vid-g1-source-state.json
ORIGIN_MAIN_COMMIT=f9aff4d2aace3d9bf862a88f0ed6304e2953dacc
ORIGIN_MAIN_REMOTE_URL=github.com/<owner>/-molin.git
ORIGIN_MAIN_PROVENANCE=FRESH_FETCH
ORIGIN_MAIN_OBSERVED_AT=见docs/evidence/video-gateway-vid-g1-source-state.json
TRACKED_PATCH_SHA256=见docs/evidence/video-gateway-vid-g1-source-state.json
UNTRACKED_MANIFEST_SHA256=见docs/evidence/video-gateway-vid-g1-source-state.json
SOURCE_STATE_ID=见docs/evidence/video-gateway-vid-g1-source-state.json
EVIDENCE_CAPTURED_AT=见docs/evidence/video-gateway-vid-g1-source-state.json
DECISION=HUMAN_REQUIRED
CODE_STATE=feature/video-gateway-vid-g1-schema；LOCAL_COMMITTED；PUSH_PENDING
SCOPE_COMPLETED=G0归档已回填；000072/000073、Go模型、事务不变量、本地隔离动态验证和文档已形成
OPERATION_RESULTS=text_to_video=LOCAL_SCHEMA_PASS；image_to_video=LOCAL_SCHEMA_PASS
TEST_EVIDENCE=隔离MySQL首次up/重复up/down-reup PASS；preexisting_chat_image/upload_expiry/expired_complete_rejected/duplicate_complete/cross_owner_complete/source_snapshot/price_operation_variant/safe_lease_release/null_fail_closed/empty_string_fail_closed/pending_delete_guard/task_event_append_only/video_asset_null_fail_closed/payload_crypto/callback_state_shape/bifrost_uniqueness/permission_admin_only PASS；T2V/I2V/归属/唯一/回调重放PASS；四类Service事务原子/回滚测试PASS；图片Task/Asset/Quote共享表隔离、跨模态对象引用保护和角色负例PASS；内部ID隐藏PASS；Provider调用0；钱包写入0
TEST_MATRIX=VID-G1-MIG/COMPAT/OP/OWNER/UPLOAD/SNAPSHOT/PRICE/NULL/LEASE/DELETE/CALLBACK/EVENT/PAYLOAD/PERM/TXN/JSON/BOUNDARY/IMAGE-ISOLATION/QUOTE-ISOLATION/OBJECT-REFERENCE
DEFECT_LEDGER=DEF-VID-G1-001..020（全部CLOSED_VERIFIED）
CODEX_BLOCKER_AUDIT=HUMAN_REQUIRED
BLOCKER_SUMMARY=代码、测试、文档和本地证据门禁已关闭；commit/push/PR已授权，merge仍未授权
AUTO_RESOLVED_BLOCKERS=G0前置门禁,DEF-VID-G1-001..020
CODEX_AUDITING_BLOCKERS=NONE
AUTO_CONFIRMED_OPEN_BLOCKERS=NONE
HUMAN_REQUIRED_BLOCKERS=VID-G1-GIT-MERGE
BLOCKER_VERIFY_EVIDENCE=Go全量/vet/mod/Python/race/隔离MySQL/敏感扫描与docs/evidence低敏回执；独立QA、产品、工程和规范复核
INDEPENDENT_AGENT_REVIEWS=QA/测试工程师/READ_ONLY/IMPLEMENTATION_OWNER=NO/PASS；产品经理/READ_ONLY/IMPLEMENTATION_OWNER=NO/PASS；独立工程/READ_ONLY/IMPLEMENTATION_OWNER=NO/PASS；Standards/PASS；均绑定当前SOURCE_STATE
DECISION_LEDGER=VID-G0治理/Provider/法务/商业决定继续VALID；VID-G0 Git授权已消费；VID-G1 commit/push/PR已授权，merge未授权
SOURCE_LEVEL=L1
PROVIDER_CONTRACT_LEVEL=NOT_IN_SCOPE
SCHEMA_LEVEL=L2
BILLING_LEVEL=L1
SECURITY_LEVEL=L1
FRONTEND_LEVEL=NOT_IN_SCOPE
RUNTIME_LEVEL=L2
REAL_PROVIDER_REQUESTS=0
PROVIDER_COST=CNY 0
TEST_ENV_RUNTIME=NO
DEPLOY_SOURCE_COMMIT=NOT_IN_SCOPE
BINARY_SHA256=NOT_IN_SCOPE
CONFIG_SHA256=NOT_IN_SCOPE
MIGRATION_SET=000072,000073（仅本地隔离执行；测试服未执行）
IMAGE_DIGEST=NOT_IN_SCOPE
CNY_TEST_SETTLEMENT=NOT_IN_SCOPE
RECONCILIATION_DIFFERENCES=NOT_IN_SCOPE
ROLLBACK=PASS
P0=0
P1=0
P2=0
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
EXTERNAL_ACTION_AUTHORIZED=YES(commit,push,PR only；merge NO)
NEXT_GOAL_ALLOWED=NO
EVIDENCE_BOUNDARY=仅本地Expand Schema开发；不证明HTTP、页面、Provider、钱包运行、测试环境、生产或商业可用
HUMAN_QUESTIONS=VID-G1 merge授权只在PR创建、CI与仓库复核通过后提出；当前无需额外费用、凭据或环境授权
```

## 13. 相关文档

- [VID-G1源码快照](./evidence/video-gateway-vid-g1-source-state.json)
- [VID-G1隔离MySQL低敏回执](./evidence/video-gateway-vid-g1-mysql-contract.json)
- [视频阶段执行文档](./video-gateway-goal-stage-execution-prompt.md)
- [VID-G0最终门禁](./video-gateway-vid-g0-gate.md)
- [数据库设计](./database-schema-design.md)
- [完整API设计](./full-api-design.md)
- [前端接口参考](./frontend-api-reference.md)
- [测试计划](./test-plan.md)
