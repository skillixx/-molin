# 短信阶段 5 测试服日志留存审计与变更手册

## 1. 当前证据

2026-08-05 最新只读检查确认测试服 `systemd-journald` 正常运行，`/var/log/journal` 持久目录存在。journal 所在文件系统总量为 `982240026624` 字节、可用 `354741161984` 字节，journal 目录聚合占用 `2969567232` 字节，约占文件系统总量 `0.30%`。合并后的 `journald.conf` 没有显式启用 `SystemMaxUse`、`SystemKeepFree`、`MaxRetentionSec` 和 `MaxFileSec`。

因此只能证明日志正在持久保存，不能证明容量上限、磁盘保留空间、最长留存时间或单文件轮转周期满足阶段 5 运维要求。未配置不等于默认值已获批准，当前固定结论为 `log_retention_policy_verified=false`。

## 2. 只读审计

执行入口：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-log-retention.ps1
```

脚本只读取 journald 服务状态、持久目录、磁盘用量可查询性以及 systemd 合并后的配置。输出只包含布尔摘要，不输出日志内容、环境变量、账号、手机号、验证码或配置值；业务配置修改数为 0。SSH 登录可能增加服务端访问审计日志，因此固定披露 `access_audit_logs_may_increase=true`，不能把它描述为远端写入绝对为 0。脚本不调用短信接口，但也不读取 Provider 或手机收件证据，固定披露 `real_sms_delivery_not_verified=true`。

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

基于当前容量和阶段 5 至少 24 小时观察需求，以下数值已于 2026-08-05 获项目负责人明确批准用于**测试服短信关闭态**；
该授权不适用于生产环境，也没有增长率证据支撑生产使用：

| 配置 | 候选值 | 依据与限制 |
|---|---:|---|
| `SystemMaxUse` | `8G` | 高于当前约 2.97GB 占用并限制 journal 总量；实际可保留天数仍取决于后续增长率 |
| `SystemKeepFree` | `50G` | 当前可用约 354.74GB，预留固定磁盘安全边界；部署前必须重新读取可用空间 |
| `MaxRetentionSec` | `14day` | 覆盖 24 小时观察和后续复盘窗口；容量上限可能更早淘汰旧日志 |
| `MaxFileSec` | `1day` | 形成每日轮转上限，便于阶段证据管理；不等同于日志保留 1 天 |

若部署前磁盘可用空间显著下降，或 24 小时增长率
显示 `8G` 无法覆盖获批留存期，应停止变更并重新评估。

变更入口已经固化为 `scripts/apply-sms-phase5-test-server-log-retention.ps1`。默认模式不连接测试服，只输出四项非敏感
计划值；`-SelfTest` 同样保持远端连接、配置写入和服务重启均为 0。只有参数值获批后，才允许在独立窗口同时提供
`-Apply` 和固定授权短语 `APPROVE_TEST_JOURNALD_RETENTION`。2026-08-05 已按批准值执行该入口：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/apply-sms-phase5-test-server-log-retention.ps1 `
  -Apply -Authorization APPROVE_TEST_JOURNALD_RETENTION
```

真实模式固定主机、账号、端口和 ED25519 指纹；先确认同一 API PID 的 `SMS_ENABLED=false`、health/ready 和磁盘容量，
再备份已有 drop-in、严格校验候选值并原子安装。重启 journald 后再次核对合并配置、服务状态和同一 API PID 关闭态。
任一安装、重启或复验失败都会恢复原 drop-in 并再次重启 journald；回滚也失败时固定以退出码 90 暴露高优先级故障。
脚本不执行 vacuum/rotate/flush，不读取日志正文，不调用短信接口，也不删除备份。

### 3.1 2026-08-05 首次获批执行结果

首次执行在任何配置写入或服务重启之前失败，固定输出 `log_retention_change_applied=false` 与
`failure_stage=sudo_unavailable`。随后通过固定 SSH 身份执行 `sudo -n -l`，系统明确返回“需要密码”，证明当前 `pc`
账号没有可用于自动化窗口的非交互 sudo 权限。立即复跑只读审计后，四项显式策略仍全部缺失，journald 保持 active；
关闭态核验继续为 `SMS_ENABLED=false`、health/ready `200/200`、发送摘要 `13:13:0`、Provider 指标 0。

不得把批准口令等同于系统提权能力，也不得索取、输出或通过命令参数传递 sudo 密码。下一次执行必须由运维提供受控的
非交互提权入口，或由具备权限的运维人员在测试服本地执行同一冻结资产；执行后仍须完整通过本手册第 4 节复验。

### 3.2 离线运维交接包

当前自动化账号没有非交互 sudo 时，可由已获本次变更授权的执行人，在受控本地目录导出冻结后的运维脚本。导出路径必须是
执行人选择的完全限定本机绝对 `.sh` 路径；Windows 不接受驱动器相对或当前驱动器根相对写法。父目录必须已经存在，完整祖先链不能包含重解析点，也不能使用 UNC、设备或映射网络驱动器；目标文件已存在时脚本会拒绝覆盖。示例中的路径仅为
占位，执行前必须换成实际受控目录：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/apply-sms-phase5-test-server-log-retention.ps1 `
  -ExportOperatorPayload C:\受控目录\apply-journald-retention.sh `
  -Authorization APPROVE_TEST_JOURNALD_RETENTION
```

`-SelfTest`、`-ExportOperatorPayload` 与 `-Apply` 三种模式互斥。导出模式不读取 `known_hosts`、不建立 SSH 连接、不写测试服配置、不重启服务，
只在指定本地路径创建一个 UTF-8、LF、无 BOM 的新文件，并输出 `operator_payload_sha256`。因此“导出成功”只表示交接资产
已经生成，不能记为测试服已部署。脚本冻结 `8G/50G/14day/1day`，并以测试服 `/etc/machine-id` 的 SHA-256 摘要绑定
目标主机；不记录或输出原始 machine-id。目标摘要不匹配时会在 sudo 与任何配置写入之前失败。

执行人须通过已批准的安全传输渠道交接文件，并在测试服核对 SHA-256 与导出输出完全一致。具备权限的运维人员应在同一个
已授权的测试服本地终端中先完成其组织规定的 sudo 身份验证，再执行脚本；不得把 sudo 密码写入命令、聊天、文件或仓库。
脚本仍执行同一套短信关闭态、API、Prometheus、磁盘容量、原子安装、受控重启和失败自动回滚门禁。执行结束后，交接文件的
保留或清理由运维按证据留存制度人工处理，本流程不自动删除该文件。

Linux/CI 行为自测会在随机临时目录覆盖已有配置恢复、原配置不存在、普通复验失败、`HUP/INT/TERM` 中断、安装失败、
journald 重启失败和回滚失败退出码 90；自测通过时固定输出 `system_paths_written=0`、`service_restarts=0`。该自测不替代
获批测试服变更窗口，只证明回滚状态机和信号处理可重复执行。

## 4. 关闭态变更与验证门禁

取得独立授权后，变更必须始终保持 `SMS_ENABLED=false`，并按以下顺序执行：

1. 备份当前合并配置摘要和 journald 健康状态，不备份业务日志正文到仓库。
2. 在受控 drop-in 中写入已批准的四项策略，不直接改供应商、短信模板或代理配置。
3. 离线校验配置语法，再执行 journald 受控 reload/restart；任何失败立即恢复原配置。
4. 复核 journald 健康、API health/ready、Prometheus 抓取和短信 Provider 调用累计值。
5. 重跑本手册的只读审计；四项显式策略全部出现在 systemd 合并配置后，只能把“配置完整性”记为通过。授权记录、获批值逐项比对和变更后的运行时重载证据仍须由独立验收材料证明，不能由本预检自动推定。

整个窗口预期远端短信业务写入为 0，且不得执行任何短信发送操作；手机侧收件事实不在本脚本验证范围。日志轮转、vacuum、删除历史日志以及生产环境变更不包含在本授权内，必须另行审批。
