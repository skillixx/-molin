# 短信阶段 5 Alertmanager 变更与演练手册

## 1. 当前结论

测试服 Prometheus 已加载 4 条短信告警规则。2026-08-05 经独立授权完成 Alertmanager 邮件候选关闭态部署：固定
`prom/alertmanager:v0.32.1` 的 Linux/amd64 镜像摘要，管理端仅绑定 `127.0.0.1:19093`，Prometheus 已发现 1 个
Alertmanager。根路由仍只指向 `discard`，子路由为 0；邮件 receiver 已加载但不可路由。部署与独立复核期间活动告警、
通知累计、邮件和短信发送均为 0，状态只能记为 `transport_present_receiver_unverified`，不能证明邮件实际投递或值班人确认。
真实接收地址、SMTP Secret 和候选配置继续保存在仓库外受控目录，本手册不记录这些敏感值，也不授权告警触发。

整个流程必须保持 `SMS_ENABLED=false`。真实短信发送数必须为 0，合成告警不得通过制造真实短信失败产生。

## 2. 接收渠道决策门禁

项目负责人必须先在受控变更单中确认以下信息，缺一项不得生成 Alertmanager 配置：

- 测试环境接收渠道类型，例如组织内 Webhook、邮件或已批准的值班平台。
- 渠道所有者、当班确认人、确认时限和失联升级人。
- Secret 的受控来源、注入方式、轮换人和撤销方法；值不得出现在仓库、终端记录、PR 或本文档。
- 允许发送的告警标签与注解字段，必须排除手机号、验证码、Token、AccessKey、请求正文和供应商原始响应。
- 关闭态部署窗口、回滚操作者和回滚确认人。
- 单次合成演练的接收目标、开始时间、结束时间和最大通知数。

渠道决策只批准“制作候选配置”时，不自动批准部署；批准关闭态部署时，也不自动批准合成告警演练。

## 3. 离线配置校验门禁

候选配置只能在受控临时目录生成，文件权限必须为 600，配置文件和模板文件均不得进入 Git。Secret 必须通过受控文件、
Secret 管理设施或渠道官方支持的安全引用方式提供，不得拼接进命令参数。候选配置至少满足：

- 路由只接收 `environment="test"` 且告警名以 `MolinSMS` 开头的告警。
- `group_by` 只使用固定低基数标签，不使用手机号、用户 ID、Request ID 或供应商请求 ID。
- 明确配置重复通知间隔、已恢复通知策略和单次通知数量上限。
- 模板只展示告警名、场景、结果类型、环境、摘要和处置建议。
- 未知告警不得静默转发到短信渠道；本项目的告警通知链本身不得依赖阿里云短信。

使用与计划部署版本完全一致的 `amtool` 执行两次离线检查：

```text
amtool check-config <受控候选配置>
amtool check-config <受控候选配置> --enable-feature="utf8-strict-mode"
```

两次均须成功且没有兼容性警告。Alertmanager 官方文档说明该检查不需要运行中的 Alertmanager，并建议新安装兼容
UTF-8 strict mode。参考：
[Alertmanager 配置与校验](https://prometheus.io/docs/alerting/latest/configuration/)。

## 4. 关闭态部署门禁

只有取得独立部署授权后，才能执行以下变更：

1. 备份并固定当前 Prometheus 配置摘要、运行参数和 health/ready。
2. 使用固定版本或镜像摘要部署 Alertmanager，管理端口只绑定回环或监控专网，不直接暴露公网。
3. 挂载只读候选配置和受控 Secret，启用 `no-new-privileges`，不得把 Secret 放入容器环境输出。
4. 先验证 Alertmanager 自身 health/ready，再向 Prometheus 增加精确 Alertmanager 目标。
5. 验证 Prometheus 配置、目标发现和通知队列状态；不得调用告警提交 API，不得 reload 未经校验的配置。
6. 运行阶段 5 只读预检，期望状态只能推进到 `transport_present_receiver_unverified`。

关闭态部署通过不等于通知链通过。本窗口预期外部通知数为 0、真实短信数为 0。

2026-08-05 关闭态实服证据：Alertmanager 与 Prometheus health/ready 均为 `200/200`，运行镜像摘要为
`sha256:82c38dcc97cd0fbf5d5e31ddfb304dbb3a6e411194477de5de82ec71b328bb40`；容器使用只读根文件系统、移除全部
Capability、启用 `no-new-privileges`，SMTP Secret 仅以 `0400` 文件挂载。Prometheus 变更前配置已以 `0600` 备份，
成功路径未触发自动回滚。该证据不包含邮箱地址或 Secret。

关闭态部署后、申请真实通知演练授权前，必须运行专用只读预检：

```powershell
# 本地契约自测，不连接测试服
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-alertmanager-drill-readiness.ps1 -SelfTest

# 固定测试服只读检查，不提交告警、不重载服务
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-alertmanager-drill-readiness.ps1
```

只有输出 `notification_drill_preflight=passed`、`closed_route_discard_only=true`、
`notification_baseline_total=0` 且 `notification_drill_execution_authorization_required=true` 时，才允许提交独立演练审批。
该结果只证明可以申请演练，不构成演练授权或接收端投递证明。

## 5. 合成告警演练门禁

合成告警演练必须再次取得独立授权，并与关闭态部署分开记录。演练要求：

1. 使用专用 `MolinSMSDrill` 合成告警，不改业务数据库、不调用任何短信发码接口。
2. 只允许一次 firing 和对应的一次 resolved；禁止并发、循环或压力触发。
3. 告警标签固定为测试环境和非业务测试场景，不包含任何真实用户标识。
4. 同时记录 Alertmanager 接收、路由、通知尝试、接收渠道到达和值班人确认五层证据。
5. 演练结束确认合成告警已 resolved、通知队列清空、业务短信 Provider 指标增量为 0。

载荷生成后、任何配置重载或告警提交之前，必须离线验证 firing/resolved 状态转换：

```powershell
# 契约自测不会读取测试服、不会提交告警
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-alertmanager-drill-payload.ps1 -SelfTest

# 校验本次两个候选载荷；命令只读文件，不连接 Alertmanager
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-alertmanager-drill-payload.ps1 `
  -FiringPayloadPath C:\受控候选目录\firing-alert.json `
  -ResolvedPayloadPath C:\受控候选目录\resolved-alert.json `
  -ChangeId <UTC_CHANGE_ID>
```

`resolved.startsAt` 必须与 firing 完全一致，`resolved.endsAt` 必须晚于 `startsAt`。Alertmanager 告警提交接口返回
HTTP 200 只证明请求被接收，不能证明时间窗口有效、resolved 通知已发送或收件人已收到。

若流程在 firing 实际发送后中断，必须先恢复 `discard`，保留原 ChangeId 和通知计数证据，并等待人工确认 firing
实际收件。恢复候选只能提交一次有效 resolved，必须静态证明 firing POST 数为 0；禁止重新执行原始全流程、禁止自动重试
firing，也禁止用“先提交一次新的 firing 但依赖 repeat interval 去重”的方式绕过单次演练门禁。若原告警已过期或精确指纹不再
存在，恢复候选必须失败关闭并重新申请处置授权。失败清理只能恢复配置，不得在退出陷阱中异步提交 resolved 后立即重载
`discard`，因为通知分组等待尚未完成时会造成无法验证的状态。

只有接收渠道真实到达且值班人确认后，才能把通知链记为通过。HTTP 200、Alertmanager 接收成功或通知尝试成功均不能
单独代表最终到达。Alertmanager 官方说明其职责是接收 Prometheus 告警并路由到具体 receiver；可用的 receiver 包括
邮件、值班平台和通用 Webhook。参考：
[通知模板说明](https://prometheus.io/docs/alerting/latest/notifications/)、
[通知集成清单](https://prometheus.io/docs/alerting/latest/integrations/)。

### 5.1 聚合预检证据契约

演练完成后，在仓库外受控本机路径生成不超过 64KB 的无 BOM UTF-8 JSON。文件必须使用 schema
`molin.sms.phase5.notification-drill.v1`，包含测试环境、UTC ChangeId、UTC 创建时间、通过结果、短信关闭、一次
firing/resolved、通知队列清空、Provider 增量 0、真实短信 0、敏感值不存在，以及 Alertmanager 接收、路由匹配、
通知尝试、接收渠道到达、值班人确认五层布尔结果。五层原始证据分别以五个非零且互不相同的 SHA-256 引用，原始
截图、日志或工单内容不得写入仓库。JSON 同时记录五份仓库外原始证据的本机绝对路径；验证器会逐份从同一文件句柄
读取并复算摘要，文件缺失、摘要不符、路径重复、网络/设备/重解析路径或位于 Git 工作区都会失败关闭。

证据须在创建后 24 小时内由负责人使用以下只读入口验证。路径不得位于 Git 工作区、网络驱动器或重解析路径；摘要由
执行人从受控证据文件计算，不能使用占位值。精确确认短语只确认告警演练证据，不批准短信发送、开关变更或 Canary：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-canary-preflight.ps1 `
  -ValidateNotificationEvidenceOnly `
  -NotificationDrillConfirmation 我已确认阶段5测试服告警通知演练成功 `
  -NotificationDrillChangeId <UTC_CHANGE_ID> `
  -NotificationDrillEvidencePath C:\受控证据目录\notification-drill.json `
  -NotificationDrillEvidenceSHA256 <证据文件64位SHA256>
```

只有该证据校验通过且实服聚合预检仍确认 Alertmanager 传输存在时，`notification_drill_ready` 才能为 true。

## 6. 回滚与证据

部署失败时先从 Prometheus 恢复原配置并确认规则计算仍正常，再停止新 Alertmanager；不得删除告警规则、Prometheus
历史数据或阶段 5 观察证据。候选配置和 Secret 的处置必须遵循获批留存策略，不得由通用脚本自动删除。

验收报告只能分别记录：离线配置通过、关闭态部署通过、合成告警被 Alertmanager 接收、通知渠道实际到达、值班人确认。
任一后续层缺失时，通知链仍为未完成。
