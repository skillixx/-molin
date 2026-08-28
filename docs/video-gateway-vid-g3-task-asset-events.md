# VID-G3：视频任务、输入租约、事件、回调与媒体资产

> 阶段：`VID-G3`
>
> 基线：`origin/main@036427603ed5580caf031ca0a9becdd7e8ac83f3`
>
> 分支：`feature/video-gateway-vid-g3-task-asset-events`
>
> Git：`LOCAL_ONLY`
>
> 外部副作用：真实Provider请求0、真实Provider Key 0、真实钱包写入0、费用CNY 0、测试服写入0、生产操作0

VID-G3让每个文生视频和图生视频任务都能从公开任务ID追溯到用户、Project、API Key、Quote、`request_id`、输入快照、状态事件、Provider回调摘要和媒体产物。本阶段只实现共享Repository、状态与安全持久化边界，不实现VID-G4 Provider Adapter、Worker、轮询、媒体抓取、审核或标识闭环。

## 1. 功能说明

使用角色：

- 后端开发：后续阶段只能通过本阶段Repository推进任务、读取输入、记录回调和创建资产。
- 测试工程师：验证CAS、状态矩阵、横向隔离、回调重放、租约和清理竞争。
- 安全与资产审查人员：验证Prompt密文、追加事实、争议、legal hold和删除保留边界。
- 用户、管理员和前端：本阶段没有新页面，也没有已注册HTTP入口。

页面入口：无。

公开HTTP接口：无。`/v1/videos`与`/api/token/videos/*`仍只是未来合同，VID-G3不能据此声称可调用。

## 2. 共享事实链

```text
ai_requests ── ai_gateway_quotes ── ai_gateway_tasks
     │                  │                    │
     │                  │                    ├── ai_gateway_task_inputs ── ai_gateway_input_assets
     │                  │                    ├── ai_gateway_task_events
     │                  │                    ├── ai_gateway_provider_callback_events
     │                  │                    ├── ai_gateway_task_payloads
     │                  │                    └── ai_gateway_assets（content及派生资产）
     │                  └── 不可变价格快照
     └── 计费轴、交付轴、用户/Project/API Key归属
```

`text_to_video`与`image_to_video`复用同一套表和Repository。没有`video_tasks`、`video_assets`或另一套视频财务账本。

## 3. 核心文件

| 文件 | 作用 |
|---|---|
| `server/migrations/000075_enforce_video_task_asset_events.up.sql` | 三轴计费值、TaskInput冻结、输入/资产/回调/密文不可变触发器及视频派生媒体约束 |
| `server/internal/modules/token_gateway/repository/video_task_repository.go` | 任务归属查询、执行/计费/交付三轴CAS与同事务事件 |
| `server/internal/modules/token_gateway/repository/video_input_repository.go` | UploadSession、InputAsset、TaskInput、Provider前复核、删除竞争与租约释放 |
| `server/internal/modules/token_gateway/repository/video_callback_repository.go` | 回调三元幂等、body摘要、验签结论、乱序与迟到终态 |
| `server/internal/modules/token_gateway/repository/video_asset_event_payload_repository.go` | 输出资产、父子关系、TaskEvent和TaskPayload Repository |
| `server/internal/modules/token_gateway/service/video_task_payload_cipher.go` | AES-GCM密文信封、nonce、AAD及完整性摘要 |
| `server/internal/modules/token_gateway/service/video_reservation_service.go` | 原子预占显式经过`unquoted→quoted→held`并追加状态事件 |
| `server/internal/modules/token_gateway/repository/video_repository_mysql_test.go` | 隔离MySQL、100并发、回调、输入、密文和资产验收 |
| `infra/scripts/verify-video-gateway-migration-000075.sh` | 无出口、无宿主端口、tmpfs的完整Migration与race门禁 |

## 4. Repository Reference

### 4.1 VideoTask Repository

- `FindForOwner`：强制`public_id + user_id + project_id + api_key_id + video.generate + operation`。
- `TransitionExecution`：使用Task `version_no`和原状态双CAS，在同一事务更新执行态并追加TaskEvent。
- `TransitionBilling`：使用Request `version_no` CAS，只推进计费轴。
- `TransitionDelivery`：使用Request `version_no` CAS，只推进交付轴；`available`还要求任务`succeeded`且计费`settled/adjusted`。

任务细粒度执行状态以`ai_gateway_tasks.status`为权威。`ai_requests.execution_status`只同步兼容的粗粒度状态，不替代Task状态机。

### 4.2 UploadSession、InputAsset与TaskInput Repository

- `VideoUploadSessionRepository.FindForOwner`：User、Project或API Key不匹配统一不存在。
- `VideoInputAssetRepository.FindReadyForBinding`：只返回`ready + passed + 未过期 + 未删除`输入。
- `VideoTaskInputRepository.BindReadyInput`：锁定I2V任务和输入后冻结快照并建立租约。
- `ValidateTaskInputForProvider`：Provider提交前重新读取TaskInput与InputAsset，复核数量、owner、hash、version、审核、过期和删除状态。
- `RequestDelete`：锁定输入并统计活动租约，只有零租约才能进入`pending_delete`。
- `ReleaseTaskLeases`：执行安全终态且计费`settled/released/adjusted`后一次性释放。

正常生成仍由VID-G2原子事务一次写入Request、Task和I2V TaskInput；`BindReadyInput`只供受控补偿和并发合同验证。

### 4.3 TaskEvent、Callback与TaskPayload Repository

- TaskEvent只提供`Append`和归属读取。数据库触发器拒绝UPDATE和DELETE。
- TaskEvent详情只允许`reason/attempt/status/result`四个结构化键及冻结枚举；`message/data`、未知键、嵌套对象和自由文本在Go与MySQL两层拒绝。
- Callback唯一键为`provider_code + provider_task_id + external_event_id`。
- Callback只保存body SHA-256、验签状态、处理状态和低敏结果，不存在原始正文列。
- TaskPayload Repository只保存密文信封；AES-GCM加解密由Service专用保护器完成。
- TaskPayload触发器拒绝UPDATE和DELETE，历史密文不能被轮换操作覆盖。
- Repository创建和读取都会重算AAD/密文SHA-256、校验kind、key version、12字节nonce与GCM最小长度，并强制调用Protector实际认证解密；未注入验证器或任意字节不能落库。

### 4.4 OutputAsset Repository

输出命令故意没有`bucket`、`object_key`、URL或签名字段。位置只能由`VideoObjectLocationFactory`生成。VID-G3只使用Fake实现做合同验证，不访问MinIO或外网。

角色：

| 角色 | 根/派生 | 媒体 |
|---|---|---|
| `content` | 根资产、唯一可计费视频输出 | MP4 |
| `cover` | 必须关联content | PNG/JPEG/WebP |
| `preview` | 必须关联content | MP4 |
| `thumbnail` | 必须关联content | PNG/JPEG/WebP |
| `moderation_copy` | 必须关联content | 图片或MP4 |
| `derived` | 必须关联content | 图片或MP4 |

## 5. 三轴状态机

### 5.1 执行轴

```text
created → reserved → queued → submitting → submitted → processing
→ fetching → storing → moderating → labeling → succeeded

执行中状态 → failed | cancelled | expired
submitting及以后 → pending_reconcile
pending_reconcile → succeeded | failed | cancelled | expired
```

设计决定：`pending_reconcile`不返回任一执行中阶段。系统没有可信字段证明回调应恢复到哪个中间位置，允许恢复会把乱序回调变成状态回退。后续只能由新事件把它收敛到安全终态。

### 5.2 计费轴

```text
unquoted → quoted → held → settlement_pending → settled | released
settled | released → adjusted
```

`settled`与`released`是相反终态，不能互相覆盖；两者只能形成新的`adjusted`事实。历史`exception`只为旧Chat/Image兼容保留，不进入视频正常迁移矩阵。

### 5.3 交付轴

```text
pending → available | rejected | expired
```

三个终态互斥且不可覆盖。`pending_reconcile`不能进入任何交付终态。

## 6. 输入资产与执行租约

- T2V必须零TaskInput。
- I2V必须且只能有一个`role=reference_image, ordinal=0`的TaskInput。
- TaskInput冻结`input_asset_id/normalized_sha256/input_version/user_id/project_id/role/ordinal`。
- 上传输入和GeneratedImageAsset都必须先形成独立规范化InputAsset快照。
- GeneratedImageAsset来源还必须为`modality=image + capability=image.generate + operation IS NULL`，并沿来源Task核对API Key。
- Quote、Reserve、TaskInput绑定和Provider前复核都会重新检查GeneratedImageAsset仍为`available + passed + 双标识 + 未过期 + 未删除 + 非争议`；InputAsset为ready不能替代源图当前可用性。
- `pending_delete`、过期、隔离、审核拒绝、hash/version漂移或已释放租约都在Provider提交前失败关闭。
- 绑定与删除都先锁同一InputAsset；100并发竞争最终只能是“一个绑定且输入ready”或“零绑定且输入pending_delete”。

## 7. 回调重放与乱序

处理顺序：

1. 在内存计算原始正文SHA-256。
2. 按回调三元键加锁查重。
3. 同键同hash返回原ACK；同键不同hash返回冲突。
4. 通过Provider code/task id查找视频Task；未知或错绑只记录未关联低敏事实。
5. 验签无效记录`failed`，不推进状态。
6. 合法但乱序、回退或相反终态记录`ignored`。
7. 合法迁移使用Task `version_no` CAS并追加TaskEvent。

原始正文不会进入MySQL、普通JSON响应、RabbitMQ、Outbox或日志。

视频Task的普通`input_json`也使用数据库白名单，只能包含`operation/resolution/duration_seconds/aspect_ratio/frame_rate/audio`六个规范化规格键。VID-G3要求`result_json/error_message_safe`为空；Prompt、Provider正文和自由文本只能进入通过认证的AES-GCM信封。

## 8. AES-GCM敏感载荷

Prompt、Provider请求和受保护结果使用`AIGatewayTaskPayload`密文信封：

```text
AAD = task_id + user_id + project_id + payload_kind + schema version
持久化 = ciphertext + 12字节nonce + key_version + aad_sha256 + ciphertext_sha256
```

规则：

- AES密钥仅接受16、24或32字节，由受限环境注入。
- 每次Seal使用新的随机nonce；相同明文形成不同密文。
- 解密前先验证key version、nonce长度、AAD SHA-256和ciphertext SHA-256。
- 归属、kind、nonce、AAD、密文或key version任一漂移都失败关闭。
- 明文、密钥和长期签名URL不进入模型JSON、普通字段或证据文件。

## 9. 输出资产、争议与删除

- `version_no` CAS保护`available/quarantined/expiring/deleting/deleted/delete_failed`。
- `available`只验证既有`passed + applied + applied`事实，VID-G3不会写入或伪造审核与标识结果。
- `dispute_status=open`原子设置`legal_hold=true`，立即阻断交付和破坏性清理。
- 争议关闭后仍保留legal hold，必须由后续独立审计动作解除。
- 视频资产的owner、request、task、父子关系、对象位置、来源和已形成hash由触发器冻结。
- `deleted`写入`deleted_at/media_deleted_at`，但继续保留request、Quote关联、hash、尺寸、时长、Codec、生命周期和审计事实。

## 10. 404与横向隔离

以下维度任一不匹配都不能返回“禁止访问”或资源详情，而是统一映射为404式不存在：

- User
- Project
- API Key
- UploadSession
- InputAsset
- GeneratedImageAsset来源
- TaskInput
- Task
- 删除态、隔离态和争议中不可用资产

Repository错误为中文内部错误；未来VID-G6 Handler负责映射稳定公开错误码，当前没有HTTP响应实现。

## 11. How to：在本地验证VID-G3

前置条件：Windows本地安装Go、Docker Desktop、Git for Windows；本地已有`mysql:8.0`和`golang:1.25-bookworm`镜像。不要配置Provider Key。

1. 运行定向单元测试：

   ```powershell
   Set-Location D:\molingproject\molin-gateway-worktree\server
   go test ./internal/modules/token_gateway/model ./internal/modules/token_gateway/repository ./internal/modules/token_gateway/service ./migrations -run TestVideo -count=1
   ```

2. 运行隔离MySQL与100并发验收：

   ```powershell
   Set-Location D:\molingproject\molin-gateway-worktree
   $env:VIDEO_GATEWAY_G3_ISOLATED_MYSQL_APPROVED='YES'
   & 'C:\Program Files\Git\bin\bash.exe' ./infra/scripts/verify-video-gateway-migration-000075.sh
   ```

   预期末行包含`VIDEO_G3_MYSQL=PASS`、`provider_calls=0`、`provider_keys=0`、`real_wallet_writes=0`和`cost_cny=0`。

3. 运行全量兼容回归：

   ```powershell
   Set-Location D:\molingproject\molin-gateway-worktree\server
   go test ./... -count=1
   go vet ./...
   ```

### Troubleshooting

- 默认`bash.exe`报WSL缺少`/bin/bash`：显式使用`C:\Program Files\Git\bin\bash.exe`。
- 脚本返回`APPROVAL_REQUIRED`：只为一次性内部容器设置上述阶段变量；不要把它用于项目数据库。
- `TaskInput事实禁止删除`：这是预期保护，不要通过清理脚本删除历史绑定；使用新的隔离数据库运行测试。
- `同一Provider事件正文不一致`：停止处理并核对签名/Provider事件源，不要覆盖原事件。

## 12. 测试证据

- MySQL 8完整`000001→000075`、重复up、保留式down/re-up通过。
- 任务状态CAS 100并发：1胜者、99冲突，只追加1个状态事件。
- 绑定/删除100并发：无悬空TaskInput。
- T2V零输入、I2V唯一输入、Provider前快照复核通过。
- 上传来源与GeneratedImageAsset来源的User/Project/API Key隔离通过。
- 回调重放、同事件异body、乱序、未知任务、错绑和pending_reconcile迟到成功通过。
- TaskEvent和TaskPayload UPDATE/DELETE触发器通过。
- AES-GCM round-trip、nonce唯一、AAD/nonce/密文/key version篡改失败关闭通过。
- content、cover、preview、thumbnail、moderation_copy、derived父子关系通过。
- available、quarantined、expiring、deleting、delete_failed、deleted及争议访问阻断通过。
- VID-G2当前HEAD兼容门禁通过，Provider、真实钱包、测试服、生产和费用均为0。

## 13. 回滚与边界

- `000075` down为事实保留式no-op，不删除触发器、约束、表、列或数据。
- 应用回退时保持视频HTTP关闭；新旧二进制都不能覆盖追加事实。
- 无法避免Prompt明文持久化时必须停止，不能通过日志脱敏或文档声明替代密文边界。
- 本阶段不证明真实Provider、Bifrost、RabbitMQ、MinIO、钱包结算、测试环境、生产或商业可用。

## 14. 缺陷台账

| ID | 级别 | 状态 | 问题 | 修复 |
|---|---|---|---|---|
| DEF-VID-G3-001 | P1 | CLOSED_VERIFIED | VID-G1视频派生图片被available约束误要求MP4，且缺少cover角色 | 000075增加cover并按视频/图片派生媒体分别校验 |
| DEF-VID-G3-002 | P1 | CLOSED_VERIFIED | VID-G2预占直接`unquoted→held`，不符合三轴完整链 | 同事务显式写`quoted`、`held`及两个计费事件 |
| DEF-VID-G3-003 | P1 | CLOSED_VERIFIED | 旧VID-G2脚本只装000074却运行当前HEAD | 阶段断言后补装000075兼容层 |
| DEF-VID-G3-004 | P2 | CLOSED_VERIFIED | 默认Windows bash指向不可用WSL | 文档和执行固定使用Git for Windows Bash |
| DEF-VID-G3-005 | P1 | CLOSED_VERIFIED | Quote/Reserve可信输入未贯穿API Key归属 | Resolver接口增加APIKeyID，四层复核并补双Key/JWT主链负例 |
| DEF-VID-G3-006 | P1 | CLOSED_VERIFIED | GeneratedImageAsset失效后ready快照仍可使用 | 每次复核源图当前状态，覆盖隔离、过期、删除、争议与未双标识 |
| DEF-VID-G3-007 | P1 | CLOSED_VERIFIED | TaskPayload Repository可绕过Protector保存任意字节 | 强制信封结构、摘要与认证解密验证，只允许Seal产物 |
| DEF-VID-G3-008 | P1 | CLOSED_VERIFIED | available迁移自动写审核/标识，越界进入VID-G4 | 改为只验证既有结果，测试显式播种低敏阶段夹具 |
| DEF-VID-G3-009 | P1 | CLOSED_VERIFIED | 多MySQL测试连接池未关闭导致100并发门禁Error 1040 | 每个测试清理后关闭连接池，包级`-p=1`且保留100 goroutine |
| DEF-VID-G3-010 | P1 | CLOSED_VERIFIED | Task/Event普通JSON可用换名字段绕过敏感正文禁令 | Go与MySQL改为结构白名单，Task结果/错误正文保持空并补直接SQL负例 |
| DEF-VID-G3-011 | P1 | CLOSED_VERIFIED | Callback终态应用结果可二次覆盖且事实可DELETE | received只允许一次进入终态，终态UPDATE和DELETE触发器拒绝 |
| DEF-VID-G3-012 | P2 | CLOSED_VERIFIED | Callback任意INSERT错误被误报为重放冲突 | 只把MySQL 1062分类为唯一键竞争，其他错误原样返回 |
| DEF-VID-G3-013 | P2 | CLOSED_VERIFIED | 回调、验签和Payload kind散落裸字符串 | 集中为`model`领域常量并统一引用 |

最终独立QA、产品和工程审查完成前，新增缺陷继续记录在本表；P0/P1/P2必须全部为0才能进入Git授权门。

## 15. 当前阶段门禁

```text
TARGET_GOAL=VID-G3
DECISION=LOCAL_READY_FOR_GIT_AUTH
BASE_COMMIT=036427603ed5580caf031ca0a9becdd7e8ac83f3
SOURCE_COMMIT=见docs/evidence/video-gateway-vid-g3-source-state.json
QA_ACCEPTANCE=PASS
PM_CONFIRMATION=PASS
DEV_CODE_REVIEW=PASS
P0=0
P1=0
P2=0
REAL_PROVIDER_REQUESTS=0
REAL_PROVIDER_KEYS=0
REAL_WALLET_WRITES=0
PROVIDER_COST_CNY=0
TEST_SERVER_WRITES=0
PRODUCTION_OPERATIONS=0
PR_STATE=NOT_CREATED
NEXT_GOAL_ALLOWED=NO
VID_G4_STARTED=NO
```

## 16. 相关文档

- [数据库设计](./database-schema-design.md)
- [完整API设计](./full-api-design.md)
- [测试计划](./test-plan.md)
- [VID-G2价格与Quote](./video-gateway-vid-g2-pricing-quote.md)
- [VID-G3 MySQL证据](./evidence/video-gateway-vid-g3-mysql-contract.json)
- [VID-G3源码状态](./evidence/video-gateway-vid-g3-source-state.json)
- [VID-G3独立验收回执](./evidence/video-gateway-vid-g3-independent-reviews.md)
- [VID-G3验收摘要](./evidence/video-gateway-vid-g3-acceptance.json)
