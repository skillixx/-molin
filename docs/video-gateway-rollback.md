# 视频网关VID-G7回滚手册

## 三种动作不能混同

### 流量关闸

设置`VIDEO_GATEWAY_TRAFFIC_ENABLED=false`并保持`VIDEO_GATEWAY_ENABLED=true`。新视频请求返回稳定503，不创建Quote、Hold、Task或MQ；既有任务仍允许回调、轮询、抓取、审核、标识、结算和补偿。

### 应用回滚

停止接收新消息后取消runtime上下文，等待消费者连接关闭和未ACK消息回到RabbitMQ。必须保留最后一个兼容Worker与回调接收器，直到`submitted`、`processing`和`pending_reconcile`收口或形成可恢复证据。不得因回滚重新生成Provider发送许可。

### Schema兼容撤回

VID-G7 migration 110–122均为Expand-only。down只撤回应用装配意图，不删除列、触发器、恢复epoch、发送许可摘要、扫描游标、对象观察、Rabbit毒消息熔断、会话/输入/输出retention事实或补偿任务。禁止用DROP、TRUNCATE、重置epoch、清空Redis/MQ或删除Bucket冒充回滚。

## 本地演练结果

隔离Linux环境完成：

- 流量关闭返回503，同时三个队列阶段各保留2个Worker。
- runtime先停止领取并等待9类后台组件与在途Handler退出，再关闭Redis/RabbitMQ/MinIO；同一持久事实可重新装配。
- 关闸后已入RabbitMQ的submit/poll/fetch消息由最后一个兼容Worker继续收口；Fetch成功必须在同一执行租约内完成结算、交付与容量释放，任一步失败不ACK。迟到poll/fetch及结算重放不得增加Provider任务、Usage或钱包流水。
- 保留真实`pending_reconcile`与因Provider hard cap保持`queued`的任务、两个holding Hold及提交计划/发送事件。
- 逆序执行122至110共13个down，前后14字段事实快照一致。
- 独立事实快照运行器以4个G6真实服务种子覆盖13类非空事实和T2V/I2V，在单一Repeatable Read事务中只输出表级行数与聚合摘要；逻辑备份必须用`--hex-blob`保持AES-GCM密文和nonce逐字节一致，并以受控钱包篡改证明摘要能够发现事实变化。
- down后再次启动关闭态runtime通过。
- 真实Provider、真实钱包、测试服和生产写入均为0。

证据见`docs/evidence/video-gateway-vid-g7-rollback-verification.json`。

## 测试服停止边界

当前`TEST_SERVER_AUTHORIZATION=NOT_GRANTED`。未获得精确主机、commit、镜像digest、migration、备份路径、在途任务处理和回滚命令授权前，不得安装、迁移、重启或执行共享环境回滚。测试服未执行的实际备份恢复不能由本地演练替代。
