# 阿里云短信验证码阶段 5 回滚手册

## 1. 第一动作

任何短信故障先将 `SMS_ENABLED=false`，通过受控配置发布重启或滚动替换 API。不得删除发送日志、验证码记录、
审计日志或模板绑定，不得回退到 Mock、固定验证码或接口明文验证码。

## 2. 标准顺序

1. 记录当前版本、health/ready、指标时间窗、五场景聚合和脱敏日志计数。
2. 关闭 `SMS_ENABLED`，保留 `SMS_TEST_MODE` 和白名单现状用于证据对账。
3. 验证所有手机发码入口返回 `503/50300`，供应商调用计数停止增长。
4. 验证邮箱验证码、管理员登录、两套控制台和九个短信管理只读接口未被误伤。
5. 必要时回滚到已验证应用二进制/镜像；恢复前一份不入库配置时不得打印差异值。
6. 默认保留 `000058/000059`。只有确认无引用、已备份且获数据库专项授权时才讨论 down。

## 3. 反向代理回滚

可信代理配置异常时，先关闭短信，再恢复上一份已验证配置。不得用清空安全校验代码、信任 XFF 或加入全网 CIDR
作为恢复手段。代理仍须覆盖 X-Real-IP 并删除 XFF/Forwarded。

固定代理网络变更必须保存原容器 inspect、原网络列表、原 API 环境文件和原二进制。回滚顺序为：关闭短信，
恢复原环境文件并重启旧二进制，按原 inspect 参数恢复两个前端容器，验证 health/ready 与关闭态后再删除本轮专用网络。
删除网络只能在确认没有容器连接后执行；不得用通配名删除 Docker 网络。正式命令中的备份路径、时间戳、容器 ID、
镜像摘要和旧配置哈希必须由部署窗口现场快照填充，计划文档不伪造未知值。

测试服前端手动部署工作流会把部署前正在运行的镜像按时间戳打成 `molin-admin:rollback-*` 和
`molin-user:rollback-*`，新容器启动、固定 IP 校验或健康检查任一失败时自动恢复原镜像。自动恢复保留固定代理专网，
因为 API 的可信代理配置依赖该网段；只有整个阶段 5 代理方案被撤销且容器已迁出时，才允许单独删除网络。

## 4. Dry Run 证据

本地已执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-rollback-dry-run.ps1 -Environment test -CurrentSmsEnabled false
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-rollback-dry-run.ps1 -Environment production -CurrentSmsEnabled true
```

两次均输出 `rollback_dry_run=passed`，远端连接、配置写入、重启、migration 和真实短信均为 0。

## 5. 测试服实备份点

2026-08-04 关闭态部署前已建立 `/home/pc/molin/backups/sms-phase5-20260804T120056Z`。目录权限 700、文件权限
600，`SHA256SUMS` 全量校验通过；旧 API SHA-256 为
`c18aa8d0efe51e2b9cccf924b275983741dcd5194fa3bb25e1d292888b926cc9`。本轮后端与 Prometheus 部署脚本均带
自动恢复路径；一次监控验证条件写错时已实际触发规则文件回滚并确认 Prometheus 恢复就绪，修正验证条件后再部署成功。

## 6. 测试服恢复材料只读预检

可重复执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-recovery-readiness.ps1
```

该入口只读取固定备份、当前 API、Prometheus 和 Alertmanager 运行态，不复制文件、不改权限、不重启、不 reload、
不触发告警、不连接短信供应商；SSH 与 HTTP GET 仍可能增加系统访问和审计日志。2026-08-04 正式结果为：11 个
固定文件、外部固定清单摘要、权限、无符号链接、新旧 API 及运行 PID/监听、x86-64 架构和当前 health/ready
全部通过；三份容器快照结构有效，其精确镜像 ID 在测试服仍可读取。旧环境语法、基础键和短信关闭态也通过，但
缺少当前固定代理信任，且含 `SMS_TEMPLATE_CODE_LOGIN`、`SMS_TEMPLATE_CODE_REGISTER` 两项已废弃键。固定输出为：

```text
rollback_materials_verified=true
rollback_environment_wholesale_restore_allowed=false
rollback_environment_restore_strategy=current_env_preserve_proxy_no_legacy_template_keys
rollback_static_prerequisites_verified=false
rollback_static_blocker=backup_env_missing_fixed_proxy_trust
rollback_restore_runtime_verified=false
```

因此 `env.test` 备份只能用于对账，禁止整份覆盖当前配置。实际回滚候选必须基于当前受控环境文件生成，保留
`TRUSTED_PROXY_IPS` 和其他当前安全边界，保持 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`，并确保不存在任何
`SMS_TEMPLATE_CODE_*`。候选文件不得输出值，必须另存到 600 权限的受控临时路径，经人工逐键确认后才能在已授权
窗口替换旧二进制并重启。该流程尚未执行；真正替换二进制、恢复配置或重启 API 会改变测试服服务状态，必须取得独立授权。

候选生成器已固化为 `scripts/prepare-sms-phase5-test-server-rollback-candidate.ps1` 与同名 Bash payload。默认不执行任何
动作，离线检查命令为：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prepare-sms-phase5-test-server-rollback-candidate.ps1 -SelfTest
```

真实模式必须同时提供 `-Execute`、精确批准口令和 UTC `ChangeId`，并通过固定 SSH 主机、账号、端口及 ED25519 公钥指纹校验；远端使用排他创建，候选
已存在时拒绝覆盖，目标目录/文件权限固定为 700/600。生成器只创建候选，不替换当前环境、不重启服务。

项目负责人已提供精确批准口令，2026-08-05 在测试服生成：

```text
候选文件：/home/pc/molin/rollback/sms-phase5/candidate-20260805T015043Z.env
SHA-256：8435f846ff2e5815bec889ac4e4c32d432acb06bb05c0e1e9c3bd6b02bb65494
目录/文件：pc:700 / pc:600
```

独立只读核验确认 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`、固定代理精确为 `172.20.250.0/28`，废弃模板键和重复键均为 0；
核验只输出布尔摘要与文件哈希，不输出环境值。生成与核验期间当前环境未替换、服务重启 0、短信发送 0。该候选尚未用于替换当前环境或启动旧二进制，
因此不能记为实际回滚或恢复运行时验证通过。

候选生成后的可重复只读验证入口为：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-rollback-candidate.ps1 `
  -ChangeId 20260805T015043Z
```

验证器固定测试服 SSH 身份，只读取候选文件的类型、权限和环境安全断言，输出 SHA-256 与布尔摘要；不输出环境值，
不替换当前环境、不重启服务、不调用短信接口。SSH 访问审计日志可能增加。

同一预检现确认 Prometheus Alertmanager 引用、Alertmanager 容器和进程均为 1，管理端只绑定回环地址，状态为
`transport_present_receiver_unverified`。关闭态部署已通过，但实际邮件投递和值班人确认仍须另行批准单次合成演练；
禁止用制造真实短信失败的方式验证通知。

## 7. 测试服旧二进制运行与当前版本恢复候选

实际运行时演练已固化为：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass `
  -File scripts/prepare-sms-phase5-test-server-rollback-drill.ps1 `
  -ChangeId <UTC_CHANGE_ID> `
  -SelfTest
```

生成器只在本地冻结 `scripts/run-sms-phase5-test-server-rollback-drill.sh`，默认不连接测试服；通过
`-ExportOperatorPayload` 导出时使用排他创建并输出 SHA-256，禁止覆盖旧候选。冻结内容包括固定测试服 machine-id、
候选 `20260805T015043Z` 及其摘要、新旧二进制摘要、Alertmanager 关闭态配置摘要、10 秒旧版本稳定窗口和 10 秒恢复后
稳定窗口。执行脚本支持 `--self-test`、`--preflight` 和 `--execute` 三种互斥意图；无参数运行失败关闭。

`--preflight` 只读核对：唯一当前 API PID、磁盘与运行二进制摘要、备份旧二进制、候选配置、`SMS_ENABLED=false`、
`SMS_TEST_MODE=true`、双前端代理 health、Alertmanager `discard`、活动告警 0、通知/请求/失败四计数、数据库发送摘要和
磁盘余量。它不创建远端文件、不发信号、不重启、不 POST、不发送邮件或短信。

`--execute` 必须在取得新的独立授权后，由运维输入
`APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_<UTC_CHANGE_ID>`。执行器先保存当前二进制和原进程 NUL 环境快照，再武装 EXIT
自动恢复；旧二进制只使用已验证的关闭态候选启动。旧版本 health/ready、版本接口、双代理和进程摘要连续稳定后，执行器
在同一窗口恢复当前二进制及原进程环境，再次验证相同健康条件、`discard`、零活动告警、通知零增量和
`sms_send_logs` 零增量。当前 `infra/.env.test` 始终不被替换；任何失败或信号都必须先恢复当前二进制。仅在自动恢复本身
失败时保留含 Secret 的 0600 原进程环境快照供人工恢复，禁止下载或输出其内容。

2026-08-05 首个本地冻结候选 `20260805T112823Z` 因把前端端口错误冻结为 `13001/13000`，在只读预检阶段失败，已在
候选目录写入 `INVALIDATED.txt`，禁止上传或执行；该失败发生在服务重启、配置替换和任何外发之前。修正版 ChangeId
`20260805T113149Z` 虽通过只读预检，但随后被“写入前预检、候选根身份和运行时候选快照”加固版取代，仓库外已写
`SUPERSEDED.txt`，同样禁止上传或执行。加固候选 `20260805T113924Z` 又被增加敏感快照全退出路径清理的版本取代；
`20260805T114239Z` 则被执行前环境一致性门禁版本取代。最终修正版 ChangeId `20260805T115540Z` 的 runner SHA-256 为
`2724b89ea0096b15e5c443a2f5dfdd7e80f93c971ff2fb22a3585a5a1ad2bb46`。本地包装器 SelfTest、远端流式 `bash -n`、
runner SelfTest 和关闭态只读预检均通过；预检确认通知基线 `3:0:3:0`、活动告警 `0:0`、短信关闭/测试模式开启，
在暂存授权前的流式预检中，远端文件写入、服务重启、通知 POST、业务 POST 和真实短信均为 0。随后取得独立暂存授权，
最终候选已上传至固定精确目录；远端摘要、`pc:600`、`bash -n`、runner SelfTest 和关闭态 `--preflight` 均通过。
暂存窗口只新增 1 个 runner 文件，服务重启、通知 POST、业务 POST 和真实短信仍为 0；尚未执行实际服务切换，不能记为回滚通过。

候选上传与远端只读预检必须使用 `scripts/stage-sms-phase5-test-server-rollback-drill.ps1` 与
`scripts/stage-sms-phase5-test-server-rollback-drill.sh`。默认模式和
`-SelfTest` 均不连接测试服；真实暂存必须同时提供 `-StageAndPreflight` 和精确批准短语
`APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_STAGE_20260805T115540Z`。包装器先复算本地候选摘要，再以固定 ED25519 身份在
`/home/pc/molin/rollback/sms-phase5/runtime-drill-staging/20260805T115540Z` 排他创建 700 目录，上传 runner 后固定为
600，并只执行摘要、`bash -n`、runner SelfTest 和 `--preflight`。任一失败只用精确文件删除和空目录 `rmdir` 清理本次
暂存，不使用递归删除；不进入实际服务切换模式，不发信号、不重启、不 POST、不发送邮件或短信。暂存与预检通过仍须另行
批准实际执行。

实际执行统一使用 `scripts/execute-sms-phase5-test-server-rollback-drill.ps1`。默认模式和 `-SelfTest` 均零连接；只有同时提供
`-Execute` 与 `APPROVE_SMS_PHASE5_TEST_ROLLBACK_DRILL_20260805T115540Z` 才会连接固定测试服。包装器在远端再次核对
精确 runner 路径、普通文件、`pc:600`、单硬链接、SHA-256 及证据目录不存在，然后只调用一次 `--execute`，禁止自动重试。
runner 成功后包装器立即调用既有独立只读验收器；只有运行和独立验收同时通过，才输出组合通过结论。

实际窗口结束后必须由 `scripts/verify-sms-phase5-test-server-rollback-drill.ps1` 与
`scripts/verify-sms-phase5-test-server-rollback-drill.sh` 独立只读验收。该验证器不信任单一成功标记，而是同时核对：

- 暂存 runner 摘要、证据目录固定文件集合、700/600 权限、属主、硬链接和符号链接；
- `drill-result.txt`、`old-runtime.txt`、`exit-evidence.txt` 和执行前预检的精确字段集合；
- 原进程环境与运行时候选快照均已清理，临时二进制不存在；
- 当前磁盘、运行进程及 `infra/.env.test` 均恢复当前版本/候选固定摘要，短信仍为关闭和测试模式；
- API health/ready/version、双前端代理、`sms_send_logs=13:13:0`、Provider 0；
- Alertmanager 配置摘要与通知计数仍为 `3:0:3:0`、活动告警 0、Prometheus 活跃 Alertmanager 1；
- API 日志不含当前运行进程中的 Secret、完整手机号、Bearer 或验证码形态，且没有遗留执行进程。

全部通过才输出 `rollback_restore_runtime_verified=true`。该只读验收可能增加 SSH/HTTP/数据库只读审计记录，但远端文件
写入、服务重启、通知 POST、业务 POST 和真实短信均为 0。
