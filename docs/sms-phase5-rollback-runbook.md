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

同一预检确认 Alertmanager 引用、容器、进程和 9093 监听均为 0，状态为
`receiver_configuration_required`。必须先明确接收渠道、值班人和 Secret 注入方式并取得部署授权，之后才能另行批准
关闭态通知演练；禁止用制造真实短信失败的方式验证通知。
