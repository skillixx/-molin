# IMG-G7：图片基础设施、关闭态装配与可恢复性

> 当前阶段：`IMG-G7`
>
> 当前状态：`AUTO_PASS`
>
> 基线：`4e272776ecbbfa40445267badbedae8ad237f481`
>
> 分支：`codex/openrouter-image-poc-config`
>
> 本阶段只完成本地代码、配置候选和无外网隔离Docker验收；未读取真实Key、未调用真实Provider、未写入测试服务器。

## 1. 功能说明

IMG-G7把IMG-G6关闭态HTTP合同接入可安装、可观测、可回滚的基础设施候选：OpenRouter协议Adapter、MinIO私有对象存储、RabbitMQ任务/DLQ、异步执行、资产清理、补偿、指标、仪表盘、告警和bootstrap装配。

使用角色：后端、运维、测试、安全和财务审查人员。本阶段没有新增用户或管理员页面。

## 2. 默认关闭合同

默认值：

```text
IMAGE_GATEWAY_ENABLED=false
IMAGE_GATEWAY_TRAFFIC_ENABLED=false
IMAGE_GATEWAY_LOCAL_FAKE_TEST=false
IMAGE_GATEWAY_PROVIDER=fake
IMAGE_GATEWAY_OPENROUTER_ENABLED=false
IMAGE_GATEWAY_OPENROUTER_KEY_FILE=
```

规则：

- 模块关闭时流量或OpenRouter开关为true会拒绝启动。
- G7流量仅允许 `APP_ENV=test + LOCAL_FAKE_TEST=true + provider=fake`。
- Provider为OpenRouter但没有独立启用开关和受限Key文件时拒绝启动；真实启用属于IMG-G9。
- 图片路由装配后仍由独立流量门禁返回50330；关闭态不启动任务生成消费者。
- Chat总闸、路由和Worker不因图片模块关闭而变化。

## 3. OpenRouterImageAdapter

- 正式构造只允许 `https://openrouter.ai/api/v1/images`。
- 请求固定 `stream=false`、单Provider tag、`allow_fallbacks=false`。
- 每次调用严格一次，Adapter没有重试、fallback或透明换模型逻辑。
- 禁止重定向；超时、取消、传输不确定和畸形200分别归一为稳定故障。
- 响应总大小限制32 MiB；Base64继续交由IMG-G4有界完整解码。
- 不记录或持久化Authorization、Prompt、Base64、Provider正文和临时URL。
- 测试只使用httptest和Fake Key；无外部网络请求。

## 4. 受限凭据文件

Secret读取器要求：

- 绝对路径。
- 普通文件且不是符号链接。
- 文件不超过8 KiB。
- Linux权限不得宽于0600。
- 内容不得包含空白或控制字符，只允许行尾换行。
- Quote HMAC、Prompt HMAC、MinIO Access Key和Secret Key使用不同文件；Quote与Prompt内容也不得相同。
- bootstrap仅在 `IMAGE_GATEWAY_ENABLED=true` 后读取图片Secret；默认关闭不会读取OpenRouter Key文件。

真实凭据创建、复制和轮换仍需独立授权。

## 5. MinIO ObjectStore

实现使用 `github.com/minio/minio-go/v7 v7.0.99`；依赖通过Go checksum校验并纳入全量构建。

私有bucket：

```text
ai-upload-temp
ai-result
ai-quarantine
```

- 部署准备阶段由受控管理凭据幂等创建bucket并清除匿名policy；运行时最小权限账号只读确认bucket存在，不拥有策略管理权限。
- ObjectStore只允许配置白名单bucket和安全对象key。
- Put先有界读取并计算SHA-256；同键同内容幂等，不同内容冲突。
- Get有64 MiB硬上限；Head必须读取服务端SHA-256 metadata。
- `IMAGE_GATEWAY_MINIO_ENDPOINT` 只用于API容器访问MinIO；`IMAGE_GATEWAY_MINIO_PUBLIC_DOWNLOAD_ENDPOINT` 专门用于浏览器短效签名，二者不得用容器内单标签主机名冒充公开入口。
- 公开签名入口必须使用HTTPS和可公开解析的主机；仅本地隔离验收允许 `127.0.0.1`、`::1` 或 `localhost` 的HTTP回环地址。
- 下载URL最长15分钟，签名前先验证对象存在。
- 隔离测试验证匿名GET被拒绝、公开签名GET返回正确MIME和PNG签名、浏览器实际加载成功、删除后不可读。
- 最小权限候选见 `infra/image-gateway/minio-service-account-policy.json`。

## 6. RabbitMQ任务与DLQ

拓扑：

```text
molin.image.tasks (direct)
  → image.generate
  → molin.image.tasks.generate (durable, prefetch=1)
  → 失败不重回主队列
  → molin.image.tasks.dead / image.generate.dead
```

- 消息正文严格只有 `{"request_id":"..."}`，不含Prompt、Base64、URL、对象地址或凭据。
- 发布使用persistent、mandatory和publisher confirm。
- 消费成功Ack；格式或业务失败Nack且不requeue，进入DLQ。
- 拓扑候选见 `infra/image-gateway/rabbitmq-definitions.json`。

## 7. 异步Prompt与结果未知

- Prompt只保存在单实例有界内存，TTL 5分钟，最多1000项；RabbitMQ只保存request_id。
- 新任务原子Quote/Hold后才进入内存和队列。
- 分发失败时立即取消未执行任务并释放hold。
- 消费时先一次性取走Prompt，再调用ImageBillingService；Worker层不重试Provider。
- 进程重启或消息被其他实例消费导致Prompt缺失时，只取消仍未调用Provider的任务并释放hold。
- 独立到期Worker必须主动扫描内存任务；即使没有后续请求或消费者，达到300秒也会取消未执行任务、释放Redis租约和钱包Hold，Provider调用保持0。
- Worker还扫描MySQL中超过300秒的 `reserved + execution pending + billing held`；即使进程在Hold提交后、Rabbit发布前崩溃且内存/队列均为空，也只做取消和释放，不重建Prompt或调用Provider。
- 对超过300秒的 `processing/storing/moderating + execution running + billing held` 假活跃请求，Worker只原子收敛为 `result_unknown + settlement_pending`，同事务创建补偿和Outbox并保留Hold；这是Provider后本地双重落库失败的恢复，不会重调Provider。
- 重复或错实例Rabbit消息必须先读取权威三态：活跃 `running/processing` 直接Ack且不得释放其Redis租约或写取消；未执行reserved才取消；终态和settlement_pending幂等Ack，不污染DLQ。
- 该方案适用于当前单实例测试环境候选；多实例可靠Prompt Vault属于生产准备阶段，不能把G7表述为生产就绪。

## 8. 清理与补偿Worker

- 失败或取消请求的temporary超过24小时、quarantined过期、delete_failed超过10分钟进入候选；已结算的非计费派生临时资产也可按24小时清理。
- `held`、`settlement_pending`、`exception` 或存在活动补偿任务的对象全部跳过，避免删除仍可能补偿交付的主图。
- `available` 的30天生产留存策略在IMG-G0已冻结，但其自动删除属于IMG-G10；G7 Worker不把可交付成功资产列入候选。
- legal hold或开放争议永远不进入清理候选。
- 使用资产version CAS取得删除权；并发状态变化只跳过，不强制覆盖。
- `deleting`超过10分钟可由新租约CAS恢复；对象已删除/不存在时收敛到deleted，删除仍失败时收敛到delete_failed，数据库终态首败不会永久卡住。
- 对象删除失败进入delete_failed；成功后进入deleted。
- 临时对象直接删除失败时，必须把受控bucket/key转换为不泄露路径的描述符，并以 `image_object_cleanup` 幂等写入 `ai_compensation_tasks`；删除与补偿记录同时失败则任务进入 `asset_cleanup_unrecorded`，禁止假报已清理。
- 结果或隔离对象已经写入、但资产元数据事务失败时，也必须逐对象登记同类清理补偿，避免产生数据库不可追踪的孤儿对象。
- Put返回错误按“对象可能晚到”处理：四种目标ref无论即时Delete结果都写永久tombstone，至少延迟1分钟并保持5分钟静默复查；引用/Head瞬时未知不消耗8次失败额度。
- 对象清理Worker只可重建 `ai-upload-temp`、`ai-result`、`ai-quarantine` 的固定命名路径；删除前查询任何资产引用，存在引用即保护，查询未知失败关闭；静默窗结束后仍不存在才幂等完成，其余错误最多重试8次后进入dead。
- 补偿Worker继续只读取持久化任务/资产事实，不重调Provider，第8次失败进入dead。
- bootstrap在模块启用时启动清理和补偿；生成消费者只有本地Fake流量开启时启动。

## 8.1 Redis 图片执行门禁

- 同步接口和异步Dispatcher共用独立图片四维租约，不复用文字网关的宽松并发默认值。
- 并发硬上限固定为用户1、Project 2、API Key 1、模型4；数据库资源策略只能收紧，不能放宽这些上限。
- 图片租约有意复用同一用户、Project、API Key和模型的AI治理Redis实体键；同主体文字请求会占用图片容量，只会保守收紧，不会放宽图片上限。
- 租约覆盖排队、Provider调用和本地终态收口，并持续心跳；成功、失败、取消、Prompt丢失和进程恢复路径都必须幂等释放。
- 并发超限或本地1000任务绝对内存上限满载返回429；该1000是多租户、多模型安全兜底，不代表单模型可绕过4并发租约排队。Redis不可确认或RabbitMQ发布失败返回503，并在Provider调用前释放钱包Hold。

## 9. 指标、仪表盘和告警

新增：

- `molin_ai_gateway_requests_total{request_type="image"}`。
- `molin_ai_gateway_upstream_requests_total{driver="fake|openrouter-images"}`。
- `molin_ai_gateway_image_tasks_backlog{status}`。
- `molin_ai_gateway_image_tasks_oldest_age_seconds{status}`。
- `molin_ai_gateway_image_assets{lifecycle_state}`。
- `molin_ai_gateway_image_reconciliation_difference`。
- `molin_ai_gateway_image_queue_depth{queue="main|dead"}`。

图片对账指标按图片 `sale_line/usage_fact/available主图/cost_line` 计算；既有Chat快照完整性检查只处理chat，防止把V2图片快照误报为Chat异常。

资源：

- `infra/grafana/dashboards/image-gateway-g7.json`。
- `infra/prometheus/image-gateway-alerts.yml`。

告警覆盖pending_reconcile、非零对账、dead补偿和临时资产积压，不使用request_id、用户、Project、SK或错误原文标签。

## 10. 对账入口

<a id="image-settlement-pending"></a>

### 10.1 pending_reconcile处置

保持图片流量关闭，查询任务、补偿和Outbox状态；只允许根据持久化结果补偿，不重调Provider。无持久化结果时进入人工检查，不释放结果未知hold。

<a id="image-reconciliation-difference"></a>

### 10.2 非零对账差异

禁止签发下载URL和任何人工“改成成功”操作。按request_id核对请求、sale/cost/usage、钱包流水、主图和Outbox，差异归零后再关闭告警。

<a id="image-dlq-backlog"></a>

### 10.3 DLQ积压

检查消息是否只有request_id、原任务是否仍reserved/pending以及Prompt是否已丢失；Prompt缺失只执行安全取消释放，不把DLQ消息直接重放到Provider。

<a id="image-temporary-assets"></a>

### 10.4 临时资产积压

检查清理Worker、MinIO删除权限、资产version、legal hold和争议状态。不得为降低积压而删除保全资产。

- 既有只读 `cmd/ai-gateway-reconcile` 保留Chat口径。
- IMG-G6管理接口 `/api/admin/token/image-reconciliation/summary` 和request级reconcile继续作为图片处置入口。
- Prometheus图片聚合差异必须为0；非零时保持流量关闭。
- 所有写处置继续要求管理员双重认证、细粒度权限、reason和前置审计。

## 11. 部署候选

部署前必须另获测试服务器授权并记录：目标主机、当前commit/二进制来源、配置摘要、备份目录、维护窗口和回滚负责人。

候选顺序：

1. 保持图片三开关均为false，备份现有二进制、环境文件、MySQL和Rabbit definitions。
2. 以独立受限文件注入四个图片Secret；OpenRouter Key文件保持空。
3. 准备三个私有bucket和Rabbit主队列/DLQ。
4. 安装Prometheus规则和Grafana只读dashboard。
5. 部署候选二进制但保持图片模块/流量关闭，复验Chat健康和既有G8基线。
6. 如需验证基础设施，再单独授权开启 `IMAGE_GATEWAY_ENABLED=true`；流量仍为false。
7. 真实Provider和费用验证只能进入IMG-G9。

## 12. 备份与回滚

备份：

- API二进制及来源commit标记。
- 不含Secret正文的环境键名/布尔开关摘要。
- MySQL全库一致性备份。
- RabbitMQ definitions和队列深度摘要。
- MinIO bucket版本/生命周期/对象数量与校验抽样，不导出签名URL。
- Prometheus规则、Grafana dashboard和Alertmanager配置。

回滚：

1. 首先把 `IMAGE_GATEWAY_TRAFFIC_ENABLED=false`，确认任务消费者停止。
2. 再把 `IMAGE_GATEWAY_ENABLED=false`，保留任务、资产、钱包、Usage、Outbox和补偿事实。
3. 恢复上一二进制与监控配置；G7没有migration，不执行数据库down。
4. 不删除MinIO对象或Rabbit消息，待对账和保全结论后处理。
5. 复验Chat `/v1/chat/completions`注册、健康、G8测试和对账基线。

## 13. 凭据轮换手册

1. 从可信环境创建新的最小权限MinIO服务账号或OpenRouter Key。
2. 写入新的绝对Secret文件，权限0600，不覆盖旧文件。
3. 保持图片流量关闭，验证文件身份、权限和非符号链接；不得输出内容或摘要。
4. 在授权窗口切换文件路径并重启候选服务。
5. 只执行鉴权/基础设施健康验证；真实图片调用另需IMG-G9费用授权。
6. 验证后吊销旧凭据并记录低敏时间/操作人/凭据类别，不记录Key尾号或hash。

## 14. 本地隔离验证

- 无外网Docker internal网络。
- MySQL 8.0.46完整000001→000071。
- RabbitMQ临时Fake账号、持久主队列、confirm、DLQ。
- MinIO临时Fake凭据、三个私有bucket、匿名拒绝和短效签名。
- Fake Provider异步任务严格一次；Prompt丢失取消释放且Provider调用不增加。
- 清理Worker删除过期临时对象并保留legal hold。
- Linux race、图片指标、0差异、Prometheus规则通过。
- 真实Provider调用0、真实凭据0、宿主端口0、测试服务器操作0。

## 15. 证据边界和门禁

本地工程和部署候选已完成后，G7仍不能仅凭本地Docker声称“测试环境集成通过”。测试服务器写入、部署、配置、重启和回滚演练需要独立授权。

### 15.1 完成审计表

| IMG-G7明确要求 | 当前证据 | 审计结论 |
|---|---|---|
| OpenRouterImageAdapter | 固定端点、模型映射、零重试/回退、httptest成功/unknown/畸形响应 | 本地完成；真实调用0 |
| 默认关闭 | Config校验、env默认false、关闭态路由503、消费者不启动 | 本地完成 |
| 受限凭据注入 | 绝对普通文件、非symlink、0600、大小/空白和复用拒绝 | 本地完成；真实凭据未处理 |
| MinIO bucket和策略 | 三私有bucket、最小权限策略候选、匿名拒绝、签名GET、冲突/删除测试 | 隔离Docker完成 |
| 临时/失败资产清理 | 过期规则、version CAS、delete_failed、legal hold/争议保护 | 隔离Docker完成 |
| RabbitMQ队列和死信 | durable exchange/queue、confirm、mandatory、prefetch1、Nack→DLQ | 隔离Docker完成 |
| 并发租约和补偿 | Redis图片四维硬上限、队列prefetch、任务/资产CAS、对象清理补偿、G5补偿8次dead | 本地/隔离完成 |
| Prometheus指标 | 图片请求/Provider/任务/资产/队列/对账指标 | 本地/隔离完成 |
| Grafana仪表盘 | `image-gateway-g7.json` JSON与静态合同通过 | 本地完成 |
| 告警规则 | 四条告警、runbook锚点、promtool PASS | 本地完成 |
| 对账入口 | 管理summary/request reconcile、图片0差异指标、G5 request级对账 | 本地完成 |
| 部署/备份/回滚/轮换 | 第11～13节中文手册和默认关闭顺序 | 候选完成，未实际执行 |
| 无真实Key时Fake路径 | 无外网MySQL/MinIO/RabbitMQ，Fake异步生成/下载/清理/对账PASS | 隔离Docker完成 |
| 回滚后Chat基线 | 图片默认关闭、独立路由/Worker、全仓Go与Chat测试PASS | 本地完成；未执行测试服务器回滚 |
| 本地基础设施强制人工审查 | 默认关闭、Adapter、Secret、MinIO、RabbitMQ、Prompt、清理/补偿和监控合同 | **项目负责人已明确批准** |
| 测试环境安装/恢复 | 已授权并完成关闭态安装、候选验收和实际回滚 | **已证明** |

### 15.2 最小人工问题包

本地合同和测试服务器关闭态安装/回滚均已取得明确授权并完成；真实Key和真实Provider始终排除。

### 15.3 本地人工审查结论

2026-08-25，项目负责人明确批准：

```text
批准 IMG-G7 的默认关闭和仅本地Fake流量合同；批准固定OpenRouter Images端点、显式模型映射、零重试/零fallback且bootstrap禁止真实OpenRouter；批准受限Secret文件、MinIO私有bucket与短效签名、RabbitMQ持久队列/DLQ且消息仅含request_id；批准单实例有界内存Prompt、Prompt丢失时取消未执行任务并释放hold；批准清理/补偿worker、version CAS、legal hold保护、图片指标/告警、对账及部署回滚候选合同。该批准不授权真实Key、真实Provider、测试服务器写入或部署、服务重启、生产操作、客户流量或远程Git操作。
```

该结论关闭G7本地工程人审项，不构成测试服务器、真实Provider、生产或远程Git授权。

### 15.4 测试服务器关闭态验收结论

2026-08-25，项目负责人授权一次测试服务器关闭态安装和实际回滚，执行结果：

- 已核对登记测试主机；具体hostname保存在受限运维记录中。原API SHA-256为`1f27cf69...9a84fc`，health/ready均200。
- 数据库gzip、Rabbit definitions、原环境和原监控备份均验证有效；具体备份目录保存在受限运维记录中，不写入公开仓库。
- 候选二进制SHA-256为`8e0333a9...e1aa97`；来源边界为base `4e272776...`的未提交工作树快照，未推送。
- migration 000068～000071成功；图片表3、调账列4，图片请求/任务/资产/hold事实均为0。
- 候选窗口创建三个私有空bucket、独立MinIO最小权限账号和RabbitMQ主队列/DLQ；队列深度均为0。
- 候选API进程health/ready均为200；具体PID保存在受限运维记录中。图片生成和Quote均返回50330 `image_gateway_traffic_closed`。
- 候选进程provider=fake、traffic=false、OpenRouter=false，OpenRouter Key环境不存在；真实Provider调用0、真实钱包写入0。
- MinIO匿名访问403；图片指标HTTP 200，对账差异`0.00000000`；Prometheus实际加载4条图片告警，Grafana dashboard存在。
- 实际回滚恢复原二进制SHA和原环境，回滚后API进程health/ready均为200；具体PID保存在受限运维记录中。Chat三条基线路由均恢复401鉴权语义。
- 回滚后图片路由404、候选进程0、环境/进程图片键0、Rabbit图片对象0、候选MinIO账号/策略不存在、图片监控配置撤销。
- 按事实保留合同保留Schema和三个空私有bucket；图片及钱包事实仍为0。
- 候选证据SHA-256为`754db136...c4137a`；回滚证据SHA-256为`7dcb7606...7adc79`。

该证据只证明本次测试服务器关闭态安装和回滚，不证明真实Provider、生产或客户流量。

2026-08-26推送前加固新增了浏览器公开签名端点、Redis图片硬租约、300秒主动过期、Put结果未知回收、对象清理tombstone/引用保护和陈旧删除恢复。这些增量已由本地及隔离Docker重新验证，但没有再次写入测试服务器；上面的关闭态安装/回滚是历史候选和流程证据，不得表述为当前最终PR提交已在测试服务器运行。

```text
GATE=IMG-G7
DECISION=AUTO_PASS
CODE_STATE=codex/openrouter-image-poc-config，BASE_COMMIT=4e272776；阶段提交和远端CI状态以当前Git/PR为准
SCOPE_COMPLETED=OpenRouter Adapter候选、受限Secret、MinIO内部/公开端点、RabbitMQ/DLQ、Redis图片四维硬门禁、异步执行、资产与孤儿对象清理/补偿、关闭态bootstrap、指标/仪表盘/告警、对账和部署回滚文档
TEST_EVIDENCE=隔离MySQL/Redis/MinIO/RabbitMQ Linux race PASS；用户/Project/API Key/模型并发1/2/1/4、陈旧reserved释放、双重落库失败4分钟不动/6分钟unknown+pending且Provider仍1、重复活跃消息不释放租约、取消/终态Ack且DLQ=0、公开签名PNG GET、Put晚到tombstone、24h资金保护、stale deleting恢复、对象清理第8次dead、legal hold、图片指标和0差异PASS
P0=0
P1=0
EXTERNAL_ACTION_AUTHORIZED=YES（仅本次测试服务器关闭态安装与实际回滚）
NEXT_GOAL_ALLOWED=YES
EVIDENCE_BOUNDARY=最终PR增量仅本地/隔离Docker验证，未重新安装测试服务器；未验证真实Provider/Key/生产/客户流量；测试服务器已恢复原API且图片路由关闭
HUMAN_QUESTIONS=NONE
```
