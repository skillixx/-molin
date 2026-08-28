# VID-G2 视频价格variant、快照与Quote

> 阶段状态：AUTO_PASS（PR #418已合并）
>
> 基线：`origin/main@d57afd6ec30861ebaadd0faf7775e1ff27a5ecee`
>
> 分支：`feature/video-gateway-vid-g2-pricing-quote`
>
> 商业边界：`NON_COMMERCIAL_TEST_FIXTURE_ONLY`
>
> 外部副作用：真实Provider请求0、真实钱包写入0、费用CNY 0、测试服/生产写入0

## 1. 功能说明

VID-G2在VID-G1共享媒体Schema之上，为`text_to_video`和`image_to_video`建立同一套视频报价深模块：

- 以`video_seconds`作为第一版唯一计量，不与`video_megapixel_seconds`叠加。
- operation、分辨率、时长、比例、帧率、音频六个维度形成规范化variant。
- 冻结矩阵必须同时含文生和图生规格，每个规格恰好对应一条独立active SKU。
- Quote保存不可变CNY价格快照、HMAC请求指纹、5分钟有效期和单次消费事实。
- 图生视频绑定可信输入资产ID、规范化SHA-256和版本；消费时重新读取ready快照。
- `/api/token/videos/quotes`语义先显式报价，`/api/token/videos/generations`消费显式Quote。
- `/v1/videos`语义在服务端自动Quote，再在一个事务内完成hard月预算检查、钱包余额检查、Hold、Request与Task创建。
- 两个门面使用同一报价器、快照、舍入和预占事务；本阶段不注册正式HTTP路由，路由交付属于VID-G6。

## 2. 使用角色与入口

| 角色 | 入口语义 | 本阶段状态 |
|---|---|---|
| OpenAI-compatible Project SK调用方 | `POST /v1/videos`自动Quote与预占 | Service合同与事务实现完成；HTTP路由未开放 |
| Molin用户控制台 | `POST /api/token/videos/quotes`预览价格，再调用generations | Service合同与事务实现完成；页面属于VID-G8 |
| 财务/产品负责人 | 审核非商业夹具、Decimal金样与价格解释 | 独立验收中 |
| 管理员 | 后续维护视频价格版本 | 管理API不在VID-G2范围 |

## 3. 核心业务规则

### 3.1 价格与variant

规范化variant固定包含：

```json
{
  "operation": "text_to_video|image_to_video",
  "resolution": "1280x720",
  "duration_seconds": 5,
  "aspect_ratio": "16:9",
  "frame_rate": 24,
  "audio": false
}
```

- 六个键必须全部显式存在；缺字段、未知字段、尾随JSON或非法值失败关闭。
- 文生与图生operation参与variant SHA-256，即使金额相同也不能共用价格行。
- 当前冻结夹具只开放`1280x720 / 5秒 / 16:9 / 24fps / 无音频`，且两个operation分别配置。
- active视频版本必须标记`price_purpose=non_commercial_test_fixture`且`cost_source`同值。
- 缺价、重复价、零价、币种不为CNY、汇率不为1、成本过期、毛利不足和未知计量均失败关闭。

### 3.2 金额与最坏规格

- 单价、用量和金额全部使用`shopspring/decimal`与MySQL Decimal，禁止`float64`。
- 逐行金额固定`ceil_8`；正用量低于最低收费时应用冻结的minimum charge。
- “允许最坏规格”指本次已选择variant允许执行的最大秒数。本阶段5秒请求按5秒Quote/Hold；实际3.25秒只结算3.25秒并释放差额。
- 实际秒数大于Quote秒数失败关闭，不允许静默追加扣费。
- 失败或零可交付秒数销售额为0并释放全部Hold。

### 3.3 HMAC、输入与幂等

- HMAC绑定capability、operation、用户、Project、Project SK、公开模型、Prompt HMAC、variant hash。
- 图生视频额外绑定`input_asset_id + normalized_sha256 + version`。
- 可信输入解析器只读取同owner、`ready + passed + 未过期`的数据库快照，并返回内部ID用于同事务写TaskInput。
- Quote幂等作用域为`user_id + project_id + command_kind + idempotency_key`。
- 同键同指纹在调价、停价或成本过期后仍先返回原Quote；同键异指纹稳定冲突。
- Quote消费采用行锁与CAS；相同request_id重放返回原事实，其他request_id只能有一个赢家。
- Project SK不进入幂等索引，但进入HMAC和归属查询；另一SK不能消费原Quote。

### 3.4 自动与显式门面

```text
/api/token显式路径
  CreateTokenQuote
  → 展示CNY价格
  → GenerateWithTokenQuote
  → 可信输入复核
  → hard月预算检查
  → Quote消费 + 钱包余额检查/Hold + Request + Task (+ I2V TaskInput)

/v1/videos自动路径
  CreateOpenAIVideo
  → 自动Quote
  → 同一可信输入与原子预占事务
```

两个门面的价格版本、快照、Hold、Task与后续结算输入完全相同。事务提交前不写MQ、不调用Provider。

## 4. 开发结构

| 文件 | 作用 |
|---|---|
| `service/video_pricing_service.go` | 严格variant、价格矩阵、不可变快照、Decimal报价与结算/释放 |
| `service/video_quote_service.go` | HMAC、可信输入复核、幂等Quote创建与单次消费 |
| `service/video_quote_facade.go` | `/v1`自动Quote与`/api/token`显式Quote的共享Service合同 |
| `service/video_reservation_service.go` | hard预算、钱包Hold、Request、Task与I2V TaskInput原子事务 |
| `service/video_input_snapshot_resolver.go` | ready输入资产的owner/hash/version可信读取 |
| `repository/video_quote_repository.go` | MySQL唯一键竞争、死锁重试、行锁消费与CAS |
| `model/ai_image.go` | 共享Quote增加command_kind与idempotency_key |
| `migration 000074` | 非商业视频价格门禁、Quote幂等列/索引/CHECK |

## 5. 数据库与状态

### 5.1 Migration

- `000074_expand_video_pricing_quotes.up.sql`
  - `ai_price_versions.price_purpose`扩为32字符。
  - active视频价格只允许`non_commercial_test_fixture`。
  - `ai_gateway_quotes`增加可空`command_kind`与`idempotency_key`。
  - 唯一键：`(user_id,project_id,command_kind,idempotency_key)`。
  - 旧Image Quote两列继续为空；VID-G1时期可能存在的旧Video Quote也允许两列同时为空，只读保留。
- `000074_expand_video_pricing_quotes.down.sql`
  - 采用事实保留式回滚，不删除价格、Quote、幂等、消费或金额事实。

### 5.2 状态流转

```text
Quote: created → consumed（同request_id可重放）
Request billing: unquoted → held
Task: created → reserved
```

VID-G2到`reserved`立即停止；`queued/submitting/provider/terminal`属于VID-G4及以后。

## 6. 权限、配置与Secret

- 沿用VID-G1权限：`video:view`、`video:model`、`video:price`、`video:task`等；本阶段不新增权限码。
- Quote HMAC Secret至少32字节，只从受限环境注入，不写源码、文档、日志或响应。
- Prompt必须先使用独立Prompt HMAC Secret摘要；不得与Quote HMAC Secret复用。
- 当前不需要Runware Key、Bifrost视频路由、RabbitMQ或真实钱包环境变量。
- 测试价格只能存在于隔离MySQL夹具；不能通过正式发布入口作为商业价格发布。

## 7. 测试方式与矩阵

```powershell
Set-Location D:\molingproject\molin-gateway-worktree\server
go test ./internal/modules/token_gateway/service ./internal/modules/token_gateway/repository ./migrations -run TestVideo -count=1

Set-Location D:\molingproject\molin-gateway-worktree
$env:VIDEO_GATEWAY_G2_MYSQL_MIGRATION_APPROVED='YES'
& 'C:\Program Files\Git\bin\bash.exe' ./infra/scripts/verify-video-gateway-migration-000074.sh
```

隔离门禁使用Docker internal network、无宿主端口、tmpfs数据库，并只清理本轮精确资源。

测试矩阵：

- 两operation独立价格与完整冻结矩阵。
- 六维允许/禁止值、operation错配、缺字段与未知字段。
- 缺价、零价、重复价、币种、模板、成本过期和商业价格失败关闭。
- Decimal、`ceil_8`、minimum charge、3.25/5秒释放与全额释放金样。
- 快照variant、单价、quoted amount和held amount篡改失败。
- Quote调价后幂等重放、异指纹冲突、过期和图片替换。
- HMAC Project SK隔离与I2V可信输入复核。
- 100并发Quote创建一条、100并发Quote消费一个赢家。
- 100并发Generation形成一个Request、Quote消费、Hold和Task。
- T2V与I2V真实临时MySQL Quote→Hold→Task；I2V额外形成唯一TaskInput。
- Chat/Image全量回归与旧价格/Quote兼容。

## 8. 部署、费用与回滚边界

- 本阶段不部署测试服务器、不执行项目数据库migration、不注册HTTP路由。
- 本阶段不调用Bifrost、Runware或任何真实Provider。
- “fixture_wallet_writes”只发生在无出口临时MySQL，用于验证事务；真实钱包写入为0。
- 应用回退时关闭视频入口；000074 down保留所有历史财务事实。
- 正式售价、税费、退款和毛利政策未批准，不能把夹具金额解释为销售价格。

## 9. 当前门禁

```text
GATE=VID-G2
DECISION=AUTO_PASS
BASE_COMMIT=d57afd6ec30861ebaadd0faf7775e1ff27a5ecee
SOURCE_COMMIT=THIS_COMMIT
IMPLEMENTATION_STATE=0a9db2f6924a86c1bff60df54e4626eec4de2b67bc7cd5790b004f2300a636e1
REVIEW_SOURCE_STATE=0cf0ab6f32a9d79b7663eb9750a091e47cddbbfba711f96e8daf1beb588c351f
GIT_MODE=COMMIT_PUSH_PR_AUTHORIZED_MERGE_SEPARATELY_GATED
COMMERCIAL=NON_COMMERCIAL_TEST_FIXTURE_ONLY
PROVIDER=FAKE_MOCK_ONLY
QA_ACCEPTANCE=PASS
PM_CONFIRMATION=PASS
DEV_CODE_REVIEW=PASS
STANDARDS_REVIEW=PASS
CI_STATUS=PASS
CI_RUN_IMPLEMENTATION=33148554156
CI_EVIDENCE_HEAD=17d9f6145a31e92b22964e5be9d5bc7e19134db6
FINAL_PR_HEAD_CI_SOURCE=GitHub PR #418 statusCheckRollup（外部权威状态，避免提交内自引用）
P0=0
P1=0
P2=0
REAL_PROVIDER_REQUESTS=0
REAL_WALLET_WRITES=0
MAX_COST_CNY=0
TEST_SERVER_WRITES=0
PRODUCTION_OPERATIONS=0
PR_STATE=MERGED
PR_NUMBER=418
PR_URL=https://github.com/skillixx/-molin/pull/418
NEXT_GOAL_ALLOWED=NO
MERGE_COMMIT=036427603ed5580caf031ca0a9becdd7e8ac83f3
MERGED_AT=2026-08-28T06:46:55Z
DEPLOYMENT=NOT_EXECUTED
```

QA、产品、独立工程审查、全量Go回归、vet、敏感扫描、隔离MySQL race和PR必选CI均已通过。PR #418 已按独立授权合并为`036427603ed5580caf031ca0a9becdd7e8ac83f3`，fresh fetch确认`origin/main`包含且当前正指向该提交；没有执行部署。VID-G2已关闭，允许从该基线开始VID-G3。

证据文件：

- [`evidence/video-gateway-vid-g2-source-state.json`](./evidence/video-gateway-vid-g2-source-state.json)
- [`evidence/video-gateway-vid-g2-mysql-contract.json`](./evidence/video-gateway-vid-g2-mysql-contract.json)
- [`evidence/video-gateway-vid-g2-final-merge.json`](./evidence/video-gateway-vid-g2-final-merge.json)
