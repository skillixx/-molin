# VID-G7 共享 Outbox 视频领取合同

## 功能范围

本切片面向视频后台运行时，提供显式的视频 Outbox 领取入口。仍读取原 `ai_outbox_events`，不新增表、消息账本、Task、Quote、钱包或计费策略。没有新增用户页面和 HTTP 路由。

当前已实现领取、租约、失败重试、受控重排、原事件到四字段消息的投影，并新增共享OutboxWorker到真实RabbitMQ的发布桥接。持久化任务消费者及运行时装配尚未完成，不得把本切片当作异步执行闭环。

## 开发接口

核心文件为 `server/internal/modules/token_gateway/repository/g3_outbox_repository.go`。

- `NewG3OutboxRepository`：保持旧 Chat/Image 领取规则，排除视频聚合以及字面 `video_` 前缀。
- `NewVideoOutboxRepository`：显式视频入口，必须同时逐字节匹配 `aggregate_type=video_request` 和 `event_type` 的 `video_` 前缀。构造不领取、不发布、不启动 Worker；后续只能由启用的视频模块装配。
- `ClaimBatch`：在真实事务中通过 `FOR UPDATE SKIP LOCKED` 领取到期 pending 或过期 publishing。未发布的前序事件阻断同聚合后序；包括 dead 前序，不能跳过。
- `MarkPublished/MarkRetry`：使用事件 ID、publishing 状态和当前租约做 CAS。视频入口额外限制聚合和事件前缀，不能回写 Chat/Image。
- `RequeueDead`：只重排原 dead 事件，不复制 event_id。旧管理员入口仍保留原权限、MFA、原因和审计要求，本切片不提供新的无鉴权运维入口。

视频批量默认 50，最大 1000；无存储、零时钟、过期阈值不早于当前秒值或超过最大批量均拒绝。未知视频事件前缀并不代表业务许可；后续发布器还必须严格验证事件类型、载荷及关联事实。

## 租约防重用

共享列 `locked_at` 为秒精度 DATETIME。仅使用当前秒值，会在同一秒内“领取 → 失败 → 重排 → 重领”时重用旧令牌，旧 Worker 因而能误确认新租约。真实 MySQL 反例已复现这一问题。

视频路径采用以下规则：

1. pending/dead 保留上一次 `locked_at` 作为最后令牌，不表示仍有活动 Worker。
2. 每次认领在同一行锁事务内生成 `max(now, previous_locked_at + 1秒)`；同批各行独立保存及返回令牌。
3. `MarkRetry` 与 `RequeueDead` 无论来自视频入口还是既有管理入口，均只对精确视频事实保留该值。Chat/Image 仍清空它。
4. 只有 `status=publishing` 的 `locked_at` 参与过期接管；pending 仍按 `next_retry_at` 判断。
5. published 不可再次重排，因此确认发布后可以清空令牌。

快速连续人工重排或时钟回退可能使令牌略晚于墙钟，接管窗口随之保守后移；不能以清空令牌缩短等待。正常共享 Worker 失败退避至少两秒，通常不会累积这种偏移。此令牌只保护 Outbox 状态，不替代 Task CAS、Redis 执行租约或 Provider 幂等。

## 验证边界

`server/internal/modules/token_gateway/service/video_outbox_repository_mysql_test.go` 使用原 G5 真实 Quote/Hold/Task 事务创建夹具，不把 Repository 或财务逻辑 Mock 为恒成功。合成钱包只存在于一次性测试库。

必测项：

- 旧入口仍可领取图片且不领取视频，视频入口反向隔离。
- 视频入口不得通过其他聚合的真实 ID/租约执行发布、重试或重排。
- 100 并发只有一个领取者；过期接管复用原事件，旧令牌和重复确认拒绝。
- 同秒 dead/旧管理重排不能复用旧令牌。
- 真实取消链的 held → released → delivery_rejected 按原事实顺序发布，dead 前序阻断后序。
- 普通重试及旧入口回写连续六轮同秒认领；旧 MarkRetry/MarkPublished 均拒绝；未来高水位两分钟边界不提前接管。
- 两个聚合一批领取，各自不同历史令牌与数据库一致。

运行入口：`infra/scripts/verify-video-gateway-vid-g7-outbox.ps1`，要求显式本地隔离批准；`-LinuxRace` 使用锁定 Go 镜像。当前应用全部121个up migration，按Focus或Finance组合检查每个必选顶层测试恰好RUN/PASS一次、无SKIP、进程退出成功、执行前后server源码哈希一致及精确资源清理。改变不可回退容量门闩的测试必须使用独立数据库，不能污染普通财务回归。

运行器使用独立临时网络和 tmpfs MySQL，仅映射随机 loopback 端口；这是本机资源隔离，不是无出口网络证明。复用 G5 夹具的专用库名和环境变量仅限本轮临时容器，不能解释为访问 G5 既有数据库。Linux 源码和依赖缓存只读，编译缓存临时回收。

## 回滚与待办

本切片无 migration。完整接线前可撤回视频构造入口，旧领取规则不变。曾经运行过视频发布后，不能回滚到会清除视频高水位的旧 Worker/管理重排实现；必须先停止新视频领取并排空或保留兼容发布器，避免迟到发布结果覆盖接管者。

不删除Outbox、任务、资产、输入、钱包或审计事实。本地已覆盖数据库与RabbitMQ联合故障、消息到业务状态恢复、全量Finance兼容及Chat/Image全库回归；测试服关闭态安装和实际回滚仍未授权。最终测试结果以同源证据回执为准。

## 原事件到任务引用的投影

`service/video_outbox_projection.go`的`VideoOutboxProjector.Project`只接收内部已领取Outbox，不提供HTTP入口。五秒上下文内先读取原事件，核对ID、事件身份、原载荷字节、publishing状态及当前两分钟租约；随后锁定原Task/Request、读取原Quote/Hold/Input，最后再读Outbox，发现等待期间已接管或已确认则返回零消息及固定低敏错误。它不持有Provider或Secret，不创建任务、不更新财务或Outbox状态。

Task细粒度状态不等于Request粗粒度状态：原G5预占为reserved/pending；其他已进入迁移的任务复用Repository原映射（例如submitted→running、pending_reconcile→unknown）。未知Task状态拒绝，不能简单删除跨表一致性核验。

| 原事件 | 必须存在的原事实 |
|---|---|
| H：video_billing_held | 原Quote/Hold/Link以及同钱包、用户、金额的冻结流水 |
| P/C：待结算与要求补偿 | 原video_reconcile补偿、精确task_key/aggregate_id及版本；P还要求原补偿确需pending事实 |
| R/J：释放与拒绝交付 | 请求/预占已released、零实结、原全额解冻流水、无消费关联及拒绝交付状态 |
| S/A：结算与交付 | 请求/预占已settled、同一正实结金额、原解冻及消费流水顺序；A另要求已形成available或其后expired |
| adjustment | 原序号Usage及独立资金动作，由原G5调整校验器复核 |

所有事件仍核原确定性event_id、六/七字段白名单、CNY、operation、版本、原冻结金额或原实结金额。格式和摘要ID正确但不存在原资金/补偿依据的事件不能投影。该依据核验是只读追溯，不替代原完整17项对账或可交付媒体检查。

T2V必须零TaskInput；I2V恰好一个reference_image/ordinal0，原用户、Project、规范化hash及输入版本必须完整。输入公开ID通过原Asset读取；上传来源核对completed会话、final_input_asset_id及原Key，图片来源核对原Image Asset→Task→Request与Key链。这里读取历史归属，不要求原媒体当前可用，也不授予使用资格，避免迟到财务事件因已清理媒体永久堵塞。真正执行前仍必须由原任务链检查当前权限、快照、租约、隔离和删除状态。

消息只输出task_id、request_id、I2V的input_asset_id与attempt=0。Outbox的retry_count属于发布尝试，不能作为业务处理attempt传入。投影成功不等于消息已经发布，更不等于获得Provider提交权；发布确认、消息重投递与Task CAS由后续接线独立验证。

## 共享Worker到真实Broker的发布桥接

`service/video_outbox_publisher.go`提供`NewVideoOutboxPublisher(db, taskPublisher)`，实现原`OutboxPublisher`接口。依赖缺失即失败，构造不启动后台任务。发布顺序为原Projector核验→既有生产TaskPublisher确认发布→共享OutboxWorker标记原事件。没有新的发布重试层、事件表、财务表或Provider调用。

所有事件进入同一TaskSubmit调度入口，T2V/I2V不分基础设施。这里的submit消息表示“重新检查原Task是否有工作”，不是“立即调用Provider”。以后接线的消费者必须读取原Task状态与资格，处理迟到财务事件和重复引用；这一要求目前尚未实现为业务消费者。

失败恢复规则：

- 不可路由或未得到明确发布确认时，不标记published，由原Worker记录原事件待重试。
- Broker已接受而确认丢失时，原引用可能已经在队列中；恢复重投允许至少一次重复消息，不创建新的Task/Quote/Hold。
- Broker明确成功但MySQL确认写入失败时，原Outbox保持publishing；租约接管后重投同一引用，事件ID不变。
- 七张财务表（排除预期变化的Outbox运输字段）在失败/重试前后逐行不变，任务和事件数量保持唯一。
- 真正业务结果的持久化与消费者ACK仍由后续Task处理器验证；本切片只证明发布侧恢复。

联合专项`TestVideoG7OutboxRelayMySQLRabbit`覆盖T2V、I2V、真实QueueUnbind不可路由、网络边界丢弃真实basic.ack、Broker成功后GORM确认写入故障、100并发认领和坏财务事实前置拒绝。独立观察通道实际读取持久消息并ACK，不是业务消费者；不能据此宣称Provider至多一次已经验收。

`db_ack_failed`是GORM更新前的合成持久化故障，不是实际数据库断连；接管使用受控时钟前移，不冒充实际等待三分钟。真实DB连接丢失、进程kill、业务消费与多实例恢复仍为G7待办。

运行器增加`-Broker`与`-Focus relay`；真实联合运行须同时设置MYSQL/RABBIT两个本机隔离批准变量。Broker使用独立随机凭据、固定digest、随机loopback端口和同一临时网络，凭据与MySQL用途分离。成功create返回的Broker精确ID用于启动和清理，无创建证明不自动删除同名资源。默认关闭、单门禁或relay缺Broker时均在Docker之前退出3。是否通过以当前源码绑定的实际回执为准。
