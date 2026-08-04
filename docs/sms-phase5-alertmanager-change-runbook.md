# 短信阶段 5 Alertmanager 变更与演练手册

## 1. 当前结论

测试服 Prometheus 已加载 4 条短信告警规则，但实际运行配置中的 Alertmanager 引用、容器、进程和 9093 监听均为 0。
因此当前只能证明规则能够计算，不能证明告警能够路由、通知或被值班人确认。本手册只固化后续实施顺序，不选择接收
渠道、不生成配置、不写入凭据，也不授权远端部署或告警触发。

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

## 5. 合成告警演练门禁

合成告警演练必须再次取得独立授权，并与关闭态部署分开记录。演练要求：

1. 使用专用 `MolinSMSDrill` 合成告警，不改业务数据库、不调用任何短信发码接口。
2. 只允许一次 firing 和对应的一次 resolved；禁止并发、循环或压力触发。
3. 告警标签固定为测试环境和非业务测试场景，不包含任何真实用户标识。
4. 同时记录 Alertmanager 接收、路由、通知尝试、接收渠道到达和值班人确认五层证据。
5. 演练结束确认合成告警已 resolved、通知队列清空、业务短信 Provider 指标增量为 0。

只有接收渠道真实到达且值班人确认后，才能把通知链记为通过。HTTP 200、Alertmanager 接收成功或通知尝试成功均不能
单独代表最终到达。Alertmanager 官方说明其职责是接收 Prometheus 告警并路由到具体 receiver；可用的 receiver 包括
邮件、值班平台和通用 Webhook。参考：
[通知模板说明](https://prometheus.io/docs/alerting/latest/notifications/)、
[通知集成清单](https://prometheus.io/docs/alerting/latest/integrations/)。

## 6. 回滚与证据

部署失败时先从 Prometheus 恢复原配置并确认规则计算仍正常，再停止新 Alertmanager；不得删除告警规则、Prometheus
历史数据或阶段 5 观察证据。候选配置和 Secret 的处置必须遵循获批留存策略，不得由通用脚本自动删除。

验收报告只能分别记录：离线配置通过、关闭态部署通过、合成告警被 Alertmanager 接收、通知渠道实际到达、值班人确认。
任一后续层缺失时，通知链仍为未完成。
