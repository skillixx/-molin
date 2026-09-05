# VID-G7 Outbox运输状态与财务事实兼容

## 功能与范围

使用者为视频发布器、结算/释放服务、恢复Worker及对账服务。本增量解决G5关闭态期间形成的限制：原校验要求Outbox始终pending且locked_at为空，G7正常领取后会误判原财务事实损坏。

不新增API、账本、表或migration，不改变价格、钱包、退款、调账及输入租约政策。运输状态与业务事实分开核验；消息发布并不证明生成成功，也不构成结算、交付或释放依据。

## 实现合同

`server/internal/modules/token_gateway/service/video_outbox_state.go`统一验证有限四态的结构，四个调用点仍完整核对原事实：

- `video_billing_settle.go`：结算重放及交付前置财务校验。
- `video_billing_cancel.go`：取消/释放原事实校验。
- `video_adjustment_reconciliation.go`：独立资金动作、序号与调账事件对应。
- `video_execution_reconcile.go`：沿原P/C事件安排恢复，不重建已存在事件。

| 运输状态 | 结构约束 |
|---|---|
| pending | processed_at为空；locked_at允许保留非零历史高水位 |
| publishing | processed_at为空，locked_at非空且非零；允许保留上次失败分类 |
| published | processed_at非空且非零，locked_at及last_error_class为空 |
| dead | processed_at为空，retry_count大于0，last_error_class非空，locked_at必须保留非零历史高水位 |

所有状态都要求next_retry_at非零；存在的时间值不能是Go零值，存在的错误分类不能全空白。未知状态及大小写变体拒绝。人工重排允许retry_count归零并保留locked_at及manual_requeue，不能把它当作从未领取，也不能以高水位晚于当前墙钟判错。

此检查不代替：事件ID、聚合类型/ID、事件类型、六/七字段白名单、金额、币种、operation、sequence_no、Quote、Task、钱包归属、资金流水、唯一性及事实全集校验。不能预先过滤坏聚合或事件而把其隐藏为不存在。大小写不敏感SQL查询只负责找到候选，关键身份仍须读取后逐字节核对。

## 验证要求

1. 原G5真实Quote/Hold/Task与Fake执行形成T2V/I2V的settled、released及独立adjustment；每条H/S/R/A/J/adjustment事件依次经过领取、普通重试、dead、重排和发布，再调用真实业务重放及17项对账。每次验证前后八张财务表完整行快照一致。
2. 原held事件已领取时，首次结算、交付和首次未提交取消仍能正确完成。
3. 结果未知的H/P/C已领取或发布后，恢复沿同一事实且不释放I2V输入，Fake Submit仍只有一次。
4. 发布无完成时间、发布仍持锁、领取无锁、非发布状态带完成时间、dead无重试或缺历史租约、大小写状态，以及合法published/dead中的错误金额/币种均失败关闭。单字段反例的其他字段须合法，防止被另一项约束掩盖。
5. 聚合ID、调整事件类型、恢复事件ID的大小写漂移不得通过；SQL不能用不敏感排序规则替代身份校验。
6. 所有影响G5四条财务校验链的修改必须复验原G5默认范围，包括原金额、冲突、缺事实、坏聚合及钱包安全反例。局部绿灯不等于完整财务兼容通过。

测试位于`video_outbox_financial_mysql_test.go`与`video_outbox_integrity_mysql_test.go`。本地隔离运行器为`infra/scripts/verify-video-gateway-vid-g7-outbox.ps1`；实际RUN/PASS、源码状态、失败反例与回归结果另存证据，不在本合同预填通过。

## 回滚边界

曾发生视频领取或发布后，不能直接回退到仍要求全部pending的旧财务校验器。应先关视频新流量，保留理解四态和租约高水位的兼容Worker、财务恢复与管理重排路径。原Task、Quote、Hold、Usage、Outbox及审计事实全部保留，禁止为迎合旧代码把published/dead改回pending或清空历史。

消息投影、持久化任务处理器、真实隔离RabbitMQ联合事务窗口、Redis和MinIO本地装配已由后续同源门禁覆盖；测试服装配与实际回滚仍未授权，本地证据不能替代远端验收。
