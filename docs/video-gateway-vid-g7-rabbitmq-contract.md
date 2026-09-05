# VID-G7 RabbitMQ消息与拓扑合同

## 功能与当前边界

面向后端运行时与运维，提供视频低敏任务消息编解码和持久RabbitMQ拓扑。T2V/I2V共享基础设施，仅按提交、轮询、媒体抓取阶段分流，不增加视频或Outbox平行账本。

当前发布器、消费传输、G5 Outbox、视频运行时、持久化处理器和Redis准入已经装配；阶段整体仍受测试服关闭态安装和实际回滚授权门禁约束。

## 代码与消息

- `server/internal/modules/token_gateway/video/task_message.go`：`TaskMessage`、`EncodeTaskMessage`、`DecodeTaskMessage`。
- `server/internal/modules/token_gateway/video/rabbitmq_topology.go`：`TaskTopology`及服务端阶段路由。
- `server/internal/modules/token_gateway/video/rabbitmq_publisher.go`：`TaskPublisher`普通、延迟及死信发布。
- `server/internal/modules/token_gateway/video/rabbitmq_consumer.go`：`TaskConsumer`、显式处理结果、prefetch和本地Worker调度。
- `rabbitmq_topology_integration_test.go`：真实隔离Broker验证。
- `infra/scripts/verify-video-gateway-vid-g7-rabbitmq.ps1`：一次性隔离资源与必需用例检查。

消息只允许 `task_id`、`request_id`、可选 `input_asset_id`、`attempt`。必需字段必须存在，标识1—128个有限ASCII字符，消息正文不超过1024字节，attempt为uint32。重复字段、大小写别名、未知字段、null、错误类型、溢出及尾随JSON均失败关闭。错误不回显原始正文，失败不返回部分解析结果。

编解码器不判断业务归属，也不将语法有效当成授权。后续消费者必须从G3/G5原账本核对任务、请求、唯一输入和当前状态，I2V缺输入或跨任务标识不得执行；最大重试次数由消费策略限制，不能凭消息attempt自行获得新Submit许可。

## 拓扑

命名空间只允许 `molin.video` 或其合法子域；生产与隔离验收使用不同作用域，实际权限必须限制到对应vhost。以下以命名空间 `N` 表示：

| 对象 | 名称 | 规则 |
|---|---|---|
| 工作交换机 | N.work | 持久direct |
| 延迟交换机 | N.delay | 持久direct |
| 死信交换机 | N.dead | 持久direct |
| 阶段主队列 | N.submit / N.poll / N.fetch | 持久quorum，死信按阶段进入N.dead |
| 阶段死信队列 | N.dead.submit / N.dead.poll / N.dead.fetch | 持久quorum，不自动回流Submit，不设置消息过期 |
| 阶段延迟队列 | N.delay.STAGE.2s / 5s / 10s / 15s | 每阶段4条持久quorum，TTL后回同阶段工作路由 |

总计3个交换机、18条队列。T2V/I2V不会产生额外队列。声明幂等；类型或配置冲突失败关闭，不自动删除或重建已有队列。

主队列和延迟队列使用 `x-dead-letter-strategy=at-least-once` 与 `x-overflow=reject-publish`，避免目标暂时不可路由时由默认至多一次死信机制丢失任务。该组合及等价x参数来自[官方至少一次死信说明](https://www.rabbitmq.com/blog/2022/03/29/at-least-once-dead-lettering)。单节点隔离测试不证明多节点Broker高可用。

`Declare`使用调用方拥有的通道，不关闭共享连接；其本身不提供网络超时保证。后续运行时必须用有界连接和明确资源所有权封装声明/发布/消费，不能把取消context当作底层IO一定会中断。

## 发布与恢复必须保持的合同

发布器使用persistent、mandatory及publisher confirm。不可路由mandatory消息可能同时收到return和ack，不能仅按ack判定路由成功；官方保证return先于对应确认发送，见[确认合同](https://www.rabbitmq.com/docs/next/confirms)。实现即使先选中ack也再次检查return缓冲，任何退回均不返回成功。

`TaskConnectionOpener`由可信运行时注入，每次必须返回独占且遵守context的连接，禁止复用共享消费连接。发布器超时必须大于0且不超过30秒，校验失败不打开连接。开始发送前失败返回低敏连接错误或取消错误；发送后连接/确认关闭、确认丢失、等待超时统一返回`ErrTaskPublishUnknown`。明确return为`ErrTaskUnroutable`，明确nack为`ErrTaskPublishRejected`。只有正确确认序号的ack且没有return才成功，不回显底层错误或正文。

取消/超时通过`CloseDeadline`关闭独占连接，不能仅依赖`PublishWithContext`中断进行中的IO。退出前等待关闭回调结束，连接回收有界。发布本身不重建拓扑、不循环重发；调用方只能在G5 Outbox持久化状态协调下显式重试同一消息，后续消费者仍必须幂等和fencing，不能把AMQP重投递等同于再次Provider Submit。

真实Broker专项验证三阶段普通/死信、2秒延迟、20次不可路由、网络层丢弃真实basic.ack后的结果未知且队列仅保留原消息、确认一直不可见时的超时关闭，以及真实quorum NACK。quorum的reject-publish长度上限允许少量在途超额，测试在有界发布次数内必须实际观察NACK并逐条核对所有已确认消息，不假设第二条一定被拒绝；业务hard cap仍须Redis/MySQL原子门禁，见[官方长度限制](https://www.rabbitmq.com/docs/3.13/quorum-queues)。

死信目标故障的内部重试与正常轮询退避不同：RabbitMQ默认内部重试间隔为3分钟。因此故障恢复用例等待210秒，正常2秒延迟断言不变；这不调整G0轮询间隔、队列年龄告警或业务SLO。必须通过恢复后原消息出现证明不丢失，而非只看声明参数。

## 本地隔离验证

执行前显式设置 `VIDEO_GATEWAY_G7_RABBIT_ISOLATED_APPROVED=YES`，再执行上述PowerShell脚本。默认`-Focus all`执行拓扑、发布器和消费者三项必需集成；`topology`/`publisher`/`consumer`只作定位，不替代组合回归。

脚本仅使用缓存镜像 `rabbitmq@sha256:606d8c0d6b3c18d1da9afc53bc7cdb2a8d5486df91b5a9830e9e07626c9ae281`、随机独立bridge网络、本轮临时容器/tmpfs与专用vid_g7 vhost。宿主机仅发布127.0.0.1随机AMQP端口；这是资源隔离，不冒称bridge已封禁所有出站网络。测试拒绝非loopback或非专用vhost连接。

以rabbitmq服务用户运行check_running验证应用就绪，避免root诊断抢先创建错误属主cookie，也不把Docker代理已接受TCP连接误当Broker就绪。临时密码在内存生成，不写入仓库；诊断输出先遮蔽该值。结束时仅按精确名称和VID-G7标签回收本轮资源，清理失败不能报告最终PASS。

真实Broker用例检查重复声明、四级延迟队列存在、三阶段的T2V/I2V同队列投递、Nack进入相应死信、2秒TTL回流，以及目标解绑/重新绑定后原消息最终恢复。必需用例必须实际RUN/PASS，SKIP、零匹配及失败都拒绝。

当前测试不包含真实Provider、钱包、测试服或生产，不证明完整G7集成或实际部署回滚。

## 消费协调与ACK边界

`ConsumeOne`每次使用独占连接和manual ACK，`prefetch=1`，只处理一条后返回。`RunWorkers`允许同一进程1—8个并行Worker；每个Worker仍只预取一条。本地Worker数不能替代2/4/8个Go实例下的Redis多轴容量及Provider hard cap验证。

连接/订阅设置有5秒上限，处理器时限显式配置且不超过60秒。等待消息支持调用方取消；处理或订阅超时关闭独占连接，未ACK原消息由Broker重投递。处理器必须合作响应context并使用持久化fencing，不通过脱离后台协程宣称任意Go代码可被强制终止。

`TaskMessageHandler`必须复验原任务/请求/输入归属、当前租约与幂等，再提交本阶段结果及可靠后续工作。仅显式`TaskHandled`才表示可ACK；零值、处理错误、panic或期限失效均返回低敏不确定错误，不ACK。该接口目前尚未接到G3/G5真实持久化实现，传输测试中的阻塞/故障处理器只检验ACK顺序，不能冒充业务数据库验收。

`TaskRetry`在次数内保留原身份并递增attempt，以2/5/10/15秒阶梯发布；第4级之后不再提高延迟。配置最大重试1—16次，到限转死信。`TaskReject`只表示将合法消息转DLQ，不表示视频任务失败、不释放Hold、不改财务终态。超过最大attempt的消息不再调用处理器，也不能把attempt重置为0。

重试或死信必须先收到目标队列publisher confirm，才ACK原消息。目标不可路由或发布结果未知时，保留未ACK原消息；目标已收但确认丢失可能同时保留原消息与一个重试副本，这是至少一次传输，后续真实处理器必须用原账本唯一键/CAS/fencing去重，不能据消息重复再次Submit或结算。

错误Content-Type、非持久消息或不合规正文在业务处理前失败关闭，不复制原始错误正文到DLQ，也不打印正文；原消息保留，`RunWorkers`遇此错误停止当前组。运行时先写系统审计，再以122号状态表version CAS形成blocked；同进程和新进程都不会每2秒盲目重启该组。普通审计裸插不能解封，只有绑定原blocked审计、stage、正文SHA-256、管理员前后审计和恢复审计的数据库Trigger迁移才能回ready。管理员使用`POST /api/admin/token/video-rabbit/{stage}/poison/discard`，携带JWT、双MFA、`ai_gateway:reconcile_manage`、幂等头、原因和作为`version_no`的精确熔断审计ID。同一毒消息可依次使最多8个进程停止，每个进程另一个Worker最多回队1条合法消息；处置连接以prefetch=9暂存最多8条合法重排消息，保证还能读取其后的同摘要非法消息。完成前后审计后才ACK毒消息，再把合法消息逐条requeue；9条均为合法时全部保留并失败关闭。合法消息绝不允许由该入口丢弃，ACK未知时相同恢复事实只补ACK。

DLQ不被普通Worker自动消费。`POST /api/admin/token/video-tasks/{task_id}/dlq/{stage}/recover`要求同一管理权限/MFA、原Task版本、原因和幂等头。恢复器从DLQ头读取低敏四字段，重新核对原Task、Request、operation、InputAsset、当前状态、Provider绑定和attempt；提交阶段只允许尚未形成Provider尝试的created/reserved/queued任务，poll/fetch要求相应在途状态和既有Provider任务。同一管理员+Idempotency-Key由users行锁串行并冻结kind、Task、Request、stage、attempt、version或fuse摘要，异意图重放409。恢复请求与发布结果分别写入追加式TaskEvent和前后审计，工作队列publisher confirm及完成事实都成功后才ACK原DLQ。发布完成只绑定先前冻结的请求事件；即使工作消费者已推进Task版本，仍能完成审计并ACK。attempt原值保持，已发布恢复重放只ACK，不再次发布；发送或持久化不确定时保留DLQ并返回人工核对，禁止重置attempt或盲目Submit。

实际Broker消费专项验证：未释放处理屏障前只取一条、处理完成后ACK、错误/panic/超时原消息重投递、0→1有界延迟重试后转DLQ、重试不可路由及确认丢失保留原消息、超过上限不进入处理器、格式错误正文不复制到DLQ、死信受控恢复/重复ACK/无许可保留、毒消息受控丢弃及合法消息保护，以及两个本地Worker严格并行上限。MySQL专项另验证管理员权限、MFA、原Task/Request/version、TaskEvent和前后审计。
