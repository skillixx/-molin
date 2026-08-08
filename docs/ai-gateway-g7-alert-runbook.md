# AI 网关 G7 逐告警 Runbook

> 仅适用于测试环境。所有 SQL 均为 `SELECT`；先停止扩大测试流量并保存 ChangeId、告警时间窗和只读对账报告。禁止复制密钥原文、自动修账、批量改状态或重试结果未知请求。

公共诊断命令：`curl -fsS http://127.0.0.1:19090/api/v1/alerts`、`APP_ENV=test AI_GATEWAY_RECONCILE_READ_ONLY=YES ./ai-gateway-reconcile --format json`。数据库命令统一使用受限凭据文件：`mysql --defaults-extra-file=<受限文件> -Nse '<只读 SQL>'`。

<a id="molin-ai-gateway-availability-slo-breach"></a>
## MolinAIGatewayAvailabilitySLOBreach

- 含义与影响：五分钟执行失败率超过 1%，JSON/SSE 可用性失守，可能伴随账务在途扩大。
- 命令：查询 `molin_ai_gateway_requests_total`、API/ready、Bifrost、MySQL、Redis 和 RabbitMQ 健康。
- SQL：`SELECT execution_status,billing_status,COUNT(*) FROM ai_requests WHERE created_at>=NOW()-INTERVAL 10 MINUTE GROUP BY execution_status,billing_status;`
- 处置：暂停新测试流量，按 driver/outcome 分离上游、治理和数据库故障；结果未知请求不得自动重试。
- 回滚：回到最近健康 API/监控配置，不回滚或删除账本事实。
- 账务补偿：先按 request_id 只读对账；仅由已审计异常处理入口处理 exception。
- 恢复：健康检查通过、连续十分钟成功率不低于 99%、差额/异常/积压为 0 后关闭。

<a id="molin-ai-gateway-latency-p95-high"></a>
## MolinAIGatewayLatencyP95High

- 含义与影响：P95 超过 5 秒，用户多数请求明显变慢，超时可能制造结果未知。
- 命令：查询 P50/P95/P99、TTFT、Bifrost timeout、数据库慢查询与连接池。
- SQL：`SELECT execution_driver,COUNT(*),AVG(latency_ms),MAX(latency_ms) FROM ai_execution_attempts WHERE created_at>=NOW()-INTERVAL 10 MINUTE GROUP BY execution_driver;`
- 处置：停止压测，区分网关附加开销和真实上游耗时，禁止用扩大超时掩盖账务未收敛。
- 回滚：回退导致延迟回归的 API/路由配置；保留已形成请求。
- 账务补偿：仅检查超时 request_id 的 Usage、hold 和钱包流水，不自动扣补。
- 恢复：P95 连续十分钟低于 5 秒且对账为 PASS。

<a id="molin-ai-gateway-latency-p99-high"></a>
## MolinAIGatewayLatencyP99High

- 含义与影响：P99 超过 10 秒，尾延迟可能触发客户端重试和幂等竞争。
- 命令：查询 P99、并发租约、重试和超时分布，保存慢请求 request_id 列表。
- SQL：`SELECT request_id,latency_ms,status,result_unknown FROM ai_execution_attempts WHERE created_at>=NOW()-INTERVAL 10 MINUTE ORDER BY latency_ms DESC LIMIT 100;`
- 处置：限住测试并发，定位慢节点/慢事务；不得重放 result_unknown。
- 回滚：恢复最近健康驱动/路由/API 配置。
- 账务补偿：逐 request_id 核对，未知请求保持人工对账状态。
- 恢复：P99 连续十分钟低于 10 秒、无新增未知请求且对账通过。

<a id="molin-ai-gateway-ttft-p95-high"></a>
## MolinAIGatewayTTFTP95High

- 含义与影响：流式首 Token P95 超过 2.5 秒，SSE 体验退化并提高断连概率。
- 命令：查询 TTFT、节点 probe、stream interruption 和对应 driver。
- SQL：`SELECT execution_driver,COUNT(*),AVG(latency_ms) FROM ai_execution_attempts WHERE created_at>=NOW()-INTERVAL 10 MINUTE GROUP BY execution_driver;`
- 处置：暂停流式压测，核查 Bifrost 节点、上游排队和审核延迟。
- 回滚：回退导致 TTFT 回归的路由或 API 配置。
- 账务补偿：断连请求仍必须读完 Usage；缺失 Usage 保持 hold 并人工核定。
- 恢复：TTFT P95 连续十分钟达标且断连率、差额为 0。

<a id="molin-ai-gateway-upstream-failure-rate-high"></a>
## MolinAIGatewayUpstreamFailureRateHigh

- 含义与影响：某模型/驱动失败率超过 10%，继续流量会扩大失败或待核对请求。
- 命令：按 model/driver/outcome 查询指标并检查 Bifrost/LB 日志的脱敏摘要。
- SQL：`SELECT r.logical_model_code,a.execution_driver,a.status,a.error_class,COUNT(*) FROM ai_execution_attempts a JOIN ai_requests r ON r.request_id=a.request_id WHERE a.created_at>=NOW()-INTERVAL 10 MINUTE GROUP BY r.logical_model_code,a.execution_driver,a.status,a.error_class;`
- 处置：下调或停止对应测试路由；只对明确 request_not_sent 执行受控重试。
- 回滚：恢复上一版健康路由快照。
- 账务补偿：有可信 Usage 按既有结算；无可信 Usage 不得猜测扣费。
- 恢复：失败率连续十分钟低于阈值、差额和异常为 0。

<a id="molin-ai-gateway-bifrost-timeout-detected"></a>
## MolinAIGatewayBifrostTimeoutDetected

- 含义与影响：Bifrost 请求超时，是否送达未知，存在重复执行和重复扣费风险。
- 命令：检查两个节点、LB、超时指标和同 request_id 的 attempt；不得输出 Authorization。
- SQL：`SELECT request_id,attempt_no,status,result_unknown,error_class,prompt_tokens,completion_tokens FROM ai_execution_attempts WHERE (status='timeout' OR error_class='network_timeout') AND created_at>=NOW()-INTERVAL 10 MINUTE;`
- 处置：停止该路由；结果未知不自动 fallback、不自动重试。
- 回滚：恢复上一版超时/路由配置并保持账本事实。
- 账务补偿：通过供应商事实和 Usage 人工核定，任何补扣/退款需独立审批。
- 恢复：节点健康、无新增 timeout、历史 request_id 均收敛后关闭。

<a id="molin-ai-gateway-bifrost-nodes-insufficient"></a>
## MolinAIGatewayBifrostNodesInsufficient

- 含义与影响：两个节点中至少一个 Blackbox probe 失败，冗余和单节点演练能力丢失。
- 命令：查询 `probe_success{job="molin-bifrost-node"}`，只读执行 `docker inspect bifrost-1 bifrost-2 bifrost-lb`。
- SQL：`SELECT code,status,health_status,updated_at FROM token_channels WHERE status='active';`
- 处置：停止新增测试流量，确认 exporter 已接入 `bifrost-net`，再检查节点健康与 LB upstream。
- 回滚：恢复上一版容器/配置；不得切换到未经验收节点。
- 账务补偿：节点故障期间 request_id 逐笔核对；结果未知不自动重试。
- 恢复：两个 probe 连续五分钟为 1，统一入口健康且对账为 PASS。

<a id="molin-ai-gateway-usage-missing"></a>
## MolinAIGatewayUsageMissing

- 含义与影响：在线响应缺失 Usage，或只读对账发现原始/销售 Usage 的集合、来源、数量、单价、逐项金额或最低收费不一致，均无法可靠结算，按 P1 处置。
- 命令：运行 JSON 对账并从 `issues` 取 `issue_code=missing_usage` 的 request_id；再核对 attempt、冻结价格快照和 Provider 原始证据。不要只看在线 Counter，因为历史损坏由 `billing_anomalies{kind="missing_usage"}` 触发同一告警。
- SQL：辅查 `SELECT request_id,meter_type,source,sequence_no,quantity,unit_price,amount FROM ai_usage_items WHERE request_id=? ORDER BY sequence_no,source,meter_type;`。正式判定使用 CLI 固化 SQL：Provider 原始 `total=input+output`，销售 `input+cached=raw input`、`output+reasoning=raw output`，单价匹配快照，金额按 `ceil_8(quantity×unit_price/scale)` 与最低收费重算。
- 处置：保持 hold，停止对应模型测试流量，禁止用估算 Token 自动结算。
- 回滚：回退导致 Usage 丢失的驱动/API 版本。
- 账务补偿：仅通过已审计人工 Usage 核定入口；不直接更新钱包表。
- 恢复：在线缺失计数不再增长、对账 `missing_usage=0`、hold 收敛、三项金额差额为 0。

<a id="molin-ai-gateway-stream-interruption-rate-high"></a>
## MolinAIGatewayStreamInterruptionRateHigh

- 含义与影响：SSE 断连率超过 5%，用户响应不完整，但服务端仍负有 Usage 结算责任。
- 命令：查询断连指标、API 连接日志和 Bifrost stream 状态。
- SQL：`SELECT client_disconnected,billing_status,COUNT(*) FROM ai_requests WHERE is_stream=1 AND created_at>=NOW()-INTERVAL 10 MINUTE GROUP BY client_disconnected,billing_status;`
- 处置：暂停流式负载，验证尾部 Usage 仍被读完和持久化。
- 回滚：回退 SSE 写回/Flush 相关版本。
- 账务补偿：断连不等于免单；以可信 Usage 和交付策略按既有规则核定。
- 恢复：断连率低于 5%、所有断连 request_id 财务终态收敛。

<a id="molin-ai-gateway-moderation-unavailable"></a>
## MolinAIGatewayModerationUnavailable

- 含义与影响：分类器超时或审核依赖错误，系统已失败关闭，模型调用被拒绝。
- 命令：查询 classifier_timeout/fail_closed 指标和安全服务健康，禁止查看请求正文。
- SQL：`SELECT reason_code,COUNT(*) FROM ai_gateway_rejection_events WHERE created_at>=NOW()-INTERVAL 10 MINUTE GROUP BY reason_code;`；分类器超时和依赖失败关闭以低基数 Prometheus 拒绝指标为准。
- 处置：保持失败关闭，修复审核依赖；禁止临时绕过内容审核。
- 回滚：恢复上一版安全策略/审核服务配置。
- 账务补偿：上游未执行时释放预占；如执行边界不确定则进入人工核对。
- 恢复：审核健康、两类拒绝不再增长且安全回归通过。

<a id="molin-ai-gateway-billing-difference"></a>
## MolinAIGatewayBillingDifference

- 含义与影响：账本与 Usage、hold 或钱包流水金额不一致，属于 P0 财务门禁。
- 命令：立即运行只读 JSON 对账，保存 `issues` request_id 和三项差额。
- SQL：使用 CLI 固化 SQL；辅查 `SELECT request_id,billing_status,held_amount,settled_amount FROM ai_requests WHERE billing_status IN('settled','exception');`
- 处置：停止所有新增测试流量，禁止批量 SQL 修账。
- 回滚：回退代码不能抹除既有差额；仅阻止继续扩大。
- 账务补偿：逐 request_id 提交人工审批后走审计入口，不直接改余额/流水。
- 恢复：三项差额严格为 `0.00000000` 且 QA 复核通过。

<a id="molin-ai-gateway-billing-anomaly"></a>
## MolinAIGatewayBillingAnomaly

- 含义与影响：重复结算、无计费放行、缺失完整价格快照或钱包财务链异常至少一项非零，属于 P0；Usage 缺失、未收敛状态和账务 exception 分别由 P1 规则告警，不能被本规则错误升级。
- 命令：运行只读对账，按 `issue_code` 分类 request_id。
- SQL：`SELECT billing_status,execution_status,COUNT(*) FROM ai_requests GROUP BY billing_status,execution_status;`
- 处置：冻结对应验收流量，先证明事实再决定动作。
- 回滚：恢复上一版代码，不删除异常记录。
- 账务补偿：重复扣费与漏扣分开审批；禁止相互抵销掩盖差额。
- 恢复：全部 anomaly 为 0、逐笔证据闭环。

<a id="molin-ai-gateway-billing-exception-exists"></a>
## MolinAIGatewayBillingExceptionExists

- 含义与影响：存在需要人工核定的账务 exception，请求不可自动恢复，按 P1 处置。
- 命令：查询 exception 指标和只读对账 issues。
- SQL：`SELECT request_id,error_class,error_code,updated_at FROM ai_requests WHERE billing_status='exception' ORDER BY updated_at;`
- 处置：停止对应用户/模型测试，收集供应商和 Usage 证据。
- 回滚：回滚仅阻止新增，不能把 exception 改成成功。
- 账务补偿：由审核过的 exception resolver 单笔处理并记录管理员审计。
- 恢复：exception 清零且钱包、Usage、hold 差额为 0。

<a id="molin-ai-gateway-billing-state-stale"></a>
## MolinAIGatewayBillingStateStale

- 含义与影响：held/settlement_pending 超过五分钟，可能有执行或结算 Worker 中断。
- 命令：查询状态年龄、Worker/RabbitMQ/数据库健康。
- SQL：`SELECT request_id,execution_status,billing_status,updated_at FROM ai_requests WHERE billing_status IN('held','settlement_pending') AND updated_at<NOW()-INTERVAL 5 MINUTE;`
- 处置：停止扩大流量，按 request_id 判断未发送、结果未知或已获 Usage。
- 回滚：恢复健康 Worker/API，不批量改状态。
- 账务补偿：只通过幂等恢复/人工核定路径；结果未知保持 hold。
- 恢复：超龄请求为 0、积压为 0、对账通过。

<a id="molin-ai-gateway-unreleased-hold-amount-high"></a>
## MolinAIGatewayUnreleasedHoldAmountHigh

- 含义与影响：任一 AI 预占最老年龄超过 300 秒，或未释放总额超过 10 元，都会影响用户可用余额并按 P1 处置；小额 hold 不能因金额低而永久滞留。
- 命令：同时查询 `unreleased_holds`、`unreleased_holds_amount_cny` 和 `unreleased_holds_oldest_age_seconds`，再关联账务状态；孤立但幂等键后缀为 `:ai-hold` 的 AI hold 也计入年龄。
- SQL：`SELECT h.id,l.request_id,h.idempotency_key,h.hold_amount,h.status,h.updated_at FROM wallet_holds h LEFT JOIN ai_request_wallet_links l ON l.wallet_hold_id=h.id WHERE h.status='holding' AND (l.id IS NOT NULL OR h.idempotency_key LIKE '%:ai-hold') ORDER BY h.updated_at;`
- 处置：区分正常在途与异常 hold；异常逐 request_id 处理。
- 回滚：恢复导致 hold 泄漏的版本；不得直接清零 frozen_amount。
- 账务补偿：仅调用幂等释放/核定服务并核对钱包流水。
- 恢复：最老年龄不超过 300 秒、异常 hold 为 0、钱包冻结金额与 hold 汇总一致。

<a id="molin-ai-gateway-secret-leak-detected"></a>
## MolinAIGatewaySecretLeakDetected

- 含义与影响：用户账单申诉已通过请求归属校验，且提交值的 HMAC 精确匹配该请求所属有效平台 SK；服务已拒绝正文入库并按唯一 API Key 写入脱敏安全审计。
- 命令：隔离相关日志文件并记录哈希/权限；只查询告警计数，禁止把命中原文复制到工单。
- SQL：`SELECT id,action,target_type,target_id,created_at FROM audit_logs WHERE module='token_gateway' AND action='secret_leak_detected' AND target_type='api_key' AND created_at>=NOW()-INTERVAL 10 MINUTE;`
- 处置：停止相关服务出口、撤销并轮换受影响凭据，扩大只读扫描范围。
- 回滚：恢复安全版本和受限日志配置，不能恢复已泄漏旧凭据。
- 账务补偿：核对泄漏窗口内全部 request_id 和异常消费；任何退款/补扣独立审批。
- 恢复：旧凭据失效、新凭据验证通过、扫描与安全测试无新增发现；验证审计不可用时申诉路径仍失败关闭。

<a id="molin-ai-gateway-outbox-backlog-stale"></a>
## MolinAIGatewayOutboxBacklogStale

- 含义与影响：Outbox pending/publishing 超过五分钟，账务事件未及时下游收敛。
- 命令：检查 RabbitMQ、发布 Worker、积压数和最老年龄。
- SQL：`SELECT event_id,aggregate_id,status,retry_count,updated_at FROM ai_outbox_events WHERE status IN('pending','publishing') ORDER BY created_at LIMIT 200;`
- 处置：修复依赖后让既有幂等 Worker 继续，不手工标 published。
- 回滚：恢复健康 Worker/队列配置。
- 账务补偿：确认重复投递不会产生第二条钱包流水；异常进入人工核对。
- 恢复：活跃积压为 0、消费端幂等检查通过。

<a id="molin-ai-gateway-outbox-dead-exists"></a>
## MolinAIGatewayOutboxDeadExists

- 含义与影响：存在 dead Outbox，自动发布已放弃，下游事实可能缺失。
- 命令：查询 dead 事件、失败类别和 RabbitMQ 健康。
- SQL：`SELECT event_id,aggregate_id,event_type,retry_count,last_error_class FROM ai_outbox_events WHERE status='dead' ORDER BY updated_at;`
- 处置：按 event_id 评审后使用受控幂等重入流程，禁止复制 payload 到日志。
- 回滚：恢复发布器版本；dead 事实必须保留。
- 账务补偿：重入前核对下游是否已消费，防止重复扣费。
- 恢复：dead 为 0、下游事实齐全、对账通过。

<a id="molin-ai-gateway-compensation-backlog-stale"></a>
## MolinAIGatewayCompensationBacklogStale

- 含义与影响：补偿 pending/retry 超过五分钟，预算或资源状态未收敛。
- 命令：检查补偿 Worker、数据库和 Redis，查询最老任务。
- SQL：`SELECT id,aggregate_id,task_type,status,retry_count,updated_at FROM ai_compensation_tasks WHERE status IN('pending','retry') ORDER BY created_at LIMIT 200;`
- 处置：修复根因后依赖幂等 Worker 重试，不批量标 done。
- 回滚：恢复健康 Worker/API 配置。
- 账务补偿：任何涉及钱包的任务先做 request_id 对账，禁止盲重试。
- 恢复：pending/retry 为 0、预算与钱包不变量通过。

<a id="molin-ai-gateway-compensation-manual-action-required"></a>
## MolinAIGatewayCompensationManualActionRequired

- 含义与影响：补偿进入 dead/manual_review，需要人工决定，存在状态或金额风险。
- 命令：查询任务、request_id 对账报告和管理员审计。
- SQL：`SELECT id,aggregate_id,task_type,status,retry_count,last_error_class FROM ai_compensation_tasks WHERE status IN('dead','manual_review') ORDER BY updated_at;`
- 处置：逐任务形成审批记录，禁止直接修改任务或钱包状态。
- 回滚：回滚故障版本但保留人工队列。
- 账务补偿：仅使用审计服务入口单笔执行，完成后复核钱包流水唯一性。
- 恢复：人工队列为 0、差额/异常为 0、审批证据完整。

<a id="molin-ai-gateway-heartbeat-failure"></a>
## MolinAIGatewayHeartbeatFailure

- 含义与影响：并发租约续期失败，运行中请求可能成为幽灵租约或超卖资源。
- 命令：检查 Redis ping、时钟、连接错误和租约指标。
- SQL：`SELECT execution_status,COUNT(*) FROM ai_requests WHERE created_at>=NOW()-INTERVAL 10 MINUTE GROUP BY execution_status;`
- 处置：停止新测试请求，保持失败关闭，恢复 Redis 后运行资源集成测试。
- 回滚：恢复 Redis/API 配置；不手工删除不确定租约。
- 账务补偿：核对心跳失败窗口内运行中请求，按实际 Usage 处理。
- 恢复：心跳不再增长、租约 Gauge 正确回落、幽灵租约可自动清理。

<a id="molin-ai-gateway-ghost-lease-detected"></a>
## MolinAIGatewayGhostLeaseDetected

- 含义与影响：系统清理到过期租约，通常表示进程退出、断连或心跳失败。
- 命令：查询 ghost counter、各 scope 当前 Gauge、Redis 和进程重启记录。
- SQL：`SELECT request_id,execution_status,billing_status,updated_at FROM ai_requests WHERE execution_status IN('pending','running') AND updated_at<NOW()-INTERVAL 5 MINUTE;`
- 处置：确认 Gauge 已按 scope 回落，调查对应进程/请求，禁止全量删 Redis Key。
- 回滚：恢复稳定 API/Redis 配置。
- 账务补偿：对关联 request_id 执行只读核对；结果未知不自动扣费。
- 恢复：新租约可准入/释放、Gauge 归零且无超龄运行请求。
