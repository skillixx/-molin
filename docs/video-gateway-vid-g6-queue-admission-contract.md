# VID-G6 视频生成排队容量合同（开发中）

## 范围

G6在Redis、RabbitMQ和后台Worker关闭时实现两层MySQL本地准入：创建阶段同一用户最多2个、同一Project最多10个、全局最多100个`created/reserved/queued`视频Task；Fake执行从queued取得submitting权时，同一用户最多1个、同一Project最多2个、同一逻辑模型最多2个活动任务。活动状态为submitting、submitted、processing、fetching、storing、moderating和labeling；容量满保持queued，不调用Provider、不释放Hold。真实Provider hard cap2、分布式TTL与幽灵租约仍属于G7，本阶段不得冒称实现。

## 一致性

000109新增单行`ai_video_queue_admission_guard`，只用于序列化queued和本地running裁决；实际数量始终从原`ai_gateway_tasks`读取，不保存第二套深度、租约或任务账本。G6 HTTP创建的Repository Ledger启用运行裁决，旧G4/G5直接构造器不自动改变合同。

生成事务先验证Quote、输入和权利，再按原G5顺序形成仅在事务内可见的Hold、Link、Task、Event与Outbox暂态事实，事务末尾锁门闩读取当前Task计数。容量拒绝返回429并整笔回滚Request、Quote消费、权利声明、Hold、Link、Task、Event与Outbox；未知/过期/跨Key Quote继续优先返回原404/409，不被队列错误覆盖。门闩位于提交前而不是Hold前，不能为改写文档而改变已验证锁序。

平台错误为HTTP429/code42922/type`concurrency_limit_exceeded`，data只公开`limit_scope=user|project|global`，`Retry-After: 1`；OpenAI兼容门面使用同一HTTP/type但不泄露当前深度。

## 当前证据与边界

schema109临时MySQL运行`queue-admission`专项：用户第1/2个成功、第3个失败且Request/Task/Hold只增加2；100个不同意图同时起跑时精确2个成功、98个user范围拒绝；原HTTP Quote错误、T2V和I2V回归通过。运行副本SHA-256为`cd3ae7cae50866bf166539b80852988bdee1504d008dd662452767cb01d41b7d`。

`running-admission`专项在真实临时MySQL/Linux race验证用户1、Project2、逻辑模型2；容量输家保持queued且Provider调用0，两个Worker同起跑只有一个用户赢家，并回归取消/提交互斥与Fake完整执行。运行副本SHA-256为`c947c4bce1c673d455a91f21a4afef1550b4f7e7da8a651367651802194a2727`。

Project10与全局100已纳入后续统一all。终审整改新增用户容量拒绝、guard读取故障和queue_admission末尾故障三类完整快照，Request、Quote、Task、TaskInput、Hold、Budget、Outbox、权利声明和钱包均零变化；最新副本SHA-256为`5fa8f26c901065bd3ff11fcf7023079b0bc87635ad3b3fc71c22c5d996b6d3a5`。生成COMMIT未知由同一Reserve/Queue/Hold事务的inline与预算专项验证恢复。最终全阶段审查仍未完成；真实Provider、Redis、RabbitMQ、测试服与生产均未操作。
