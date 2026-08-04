# 短信阶段 5 测试服日志留存审计与变更手册

## 1. 当前证据

2026-08-04 只读检查确认测试服 `systemd-journald` 正常运行，`/var/log/journal` 持久目录存在，日志占用约 2.3G。合并后的 `journald.conf` 没有显式启用 `SystemMaxUse`、`SystemKeepFree`、`MaxRetentionSec` 和 `MaxFileSec`。

因此只能证明日志正在持久保存，不能证明容量上限、磁盘保留空间、最长留存时间或单文件轮转周期满足阶段 5 运维要求。未配置不等于默认值已获批准，当前固定结论为 `log_retention_policy_verified=false`。

## 2. 只读审计

执行入口：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-log-retention.ps1
```

脚本只读取 journald 服务状态、持久目录、磁盘用量可查询性以及 systemd 合并后的配置。输出只包含布尔摘要，不输出日志内容、环境变量、账号、手机号、验证码或配置值；业务配置修改数和真实短信发送数均为 0。SSH 登录可能增加服务端访问审计日志，因此固定披露 `access_audit_logs_may_increase=true`，不能把它描述为远端写入绝对为 0。

服务正常、持久目录存在、`Storage` 允许持久化、磁盘用量可查询，并且容量上限、磁盘保留空间、最长留存时间、单文件轮转周期四项均为可识别的非零显式配置时，只能输出 `log_retention_configuration_complete=true`。只读预检不能证明这些值已经获批，也不能证明运行中的 journald 已在变更后 reload/restart，因此固定保持 `log_retention_runtime_reload_verified=false`、`log_retention_policy_verified=false` 和 `log_retention_change_authorization_required=true`。

## 3. 策略决策门禁

日志配置变更必须单独授权。授权前由运维、产品和安全负责人确认：

- 测试服允许使用的最大日志容量；
- 必须为系统保留的最小磁盘空间；
- 阶段 5 观测证据的最短与最长留存时间；
- 单个 journal 文件的最大轮转周期；
- 变更窗口、执行人、复核人和回滚方式；
- 日志中出现手机号、验证码、Token 或密钥痕迹时的立即停止与处置流程。

不得由开发脚本猜测具体容量或天数，也不得把测试服策略直接复制到生产环境。

## 4. 关闭态变更与验证门禁

取得独立授权后，变更必须始终保持 `SMS_ENABLED=false`，并按以下顺序执行：

1. 备份当前合并配置摘要和 journald 健康状态，不备份业务日志正文到仓库。
2. 在受控 drop-in 中写入已批准的四项策略，不直接改供应商、短信模板或代理配置。
3. 离线校验配置语法，再执行 journald 受控 reload/restart；任何失败立即恢复原配置。
4. 复核 journald 健康、API health/ready、Prometheus 抓取和短信 Provider 调用累计值。
5. 重跑本手册的只读审计；四项显式策略全部出现在 systemd 合并配置后，只能把“配置完整性”记为通过。授权记录、获批值逐项比对和变更后的运行时重载证据仍须由独立验收材料证明，不能由本预检自动推定。

整个窗口预期远端短信业务写入为 0，真实短信发送数为 0。日志轮转、vacuum、删除历史日志以及生产环境变更不包含在本授权内，必须另行审批。
