# 阿里云短信验证码阶段 5 观察报告

## 1. 当前状态

状态：**测试服真实短信 Canary 及五档关闭态观察已完成（5/5）。** 阶段 5A 已把固定低基数指标和 4 条短信告警部署至测试服；Prometheus 就绪、
抓取目标 up。Canary 五场景均获供应商受理并人工确认收件，OTP 未消费；恢复 API 后当前进程 Provider 计数为 0。生产 Prometheus 尚未审计。

2026-08-04 已使用可重复只读脚本执行 30 秒关闭态观察：窗口前后发送摘要均为 `13:13:0`
（总数:accepted:failed），Provider 调用总数均为 0，业务配置修改为 0；SSH 登录可能增加访问审计日志。该证据证明
平台发送日志和 Provider 调用零增量，不证明手机侧真实收件数；它只覆盖关闭态稳定性，
不能替代真实 Canary 后的 5 分钟至 24 小时观察。

2026-08-05 再次执行同一固定测试服 30 秒关闭态观察：API 进程保持稳定，`SMS_ENABLED=false`，health/ready、
管理代理、用户代理、Prometheus 就绪及抓取目标均正常；窗口前后发送摘要继续为 `13:13:0`，Provider 调用总数继续为 0。
数据库 schema 保持 `64:0`，模板与绑定仍为 `5:5:5`，固定代理网络和白名单数量契约均通过。此次刷新只产生可能的
SSH/API 访问审计日志，业务配置修改、服务重启和短信发送操作均为 0；结论仍不覆盖手机侧收件事实。

告警表达式另由 `infra/prometheus/email-alerts.test.yml` 做离线阈值测试：失败率严格大于 20% 且至少 10 次、
签名异常、网络异常持续时间、平均延迟严格大于 2 秒且至少 10 次均有正例；20% 边界和 9 次调用均有不触发反例。
离线测试不会写运行指标或制造供应商故障，不能替代测试服告警通知链路演练。

2026-08-05 经独立授权完成 Alertmanager 邮件候选关闭态部署。Prometheus 运行配置中 Alertmanager 引用为 1，容器和
进程各 1，管理端仅绑定 `127.0.0.1:19093`；Alertmanager/Prometheus health/ready 均为 `200/200`。根路由仍为
`discard`，子路由 0，活动告警、通知累计、邮件和短信发送均为 0，因此通知链只能记为
`transport_present_receiver_unverified`。下一步仍须在 `SMS_ENABLED=false` 下取得独立演练授权，且不得通过真实短信故障触发。

随后已在新的独立授权下完成修正版通知演练 `20260805T105517Z`：仅 1 次 firing 和 1 次 resolved，负责人均在 QQ
收件箱确认收到对应邮件；没有重试、其他告警或短信。演练结束后根路由恢复 `discard`，活动告警 0、通知失败 0、Provider
与短信增量 0，成功证据契约通过。因此当前通知链状态已经从上述部署时点的“仅传输存在”推进为“演练投递已确认”；
该历史段仍保留用于区分部署证据和后续投递证据。

2026-08-05 日志留存策略随后由有权限运维在固定测试服本地受控窗口完成部署。独立只读验证确认 journald 正常、持久目录存在，
`SystemMaxUse=8G`、`SystemKeepFree=50G`、`MaxRetentionSec=14day`、`MaxFileSec=1day` 已生效，
`log_retention_configuration_complete=true`、`log_retention_policy_verified=true`。执行保持短信关闭态，真实短信和 Provider
增量为 0；失败回滚资产继续保留。首次因 `sudo_unavailable` 的零写入失败记录仍作为审计历史保留，不再代表当前运行态。

在完成有权限运维部署之前，曾使用同一获批参数再次执行自动化入口。脚本先通过目标主机摘要核验，随后仍以
`failure_stage=sudo_unavailable` 在配置写入和服务重启之前失败。紧接着的只读复验确认 journald active、四项显式配置仍全部缺失、
API 单进程与 health/ready 正常、短信保持关闭、发送摘要仍为 `13:13:0`、Provider 指标仍为 0；本次没有部分部署或短信发送。

## 2. 指标口径

- `sms_provider_calls_total{scene,result}`：五固定场景、八固定结果；`accepted` 只表示供应商受理。
- `sms_provider_request_duration_seconds_sum/count{scene}`：按场景累计供应商调用耗时。
- 管理 API、发送日志与审计：补充模板、绑定、限流、权限和操作结果检查，不输出敏感值。
- 业务转化：注册、手机验证码登录和找回密码成功率由业务指标或脱敏聚合查询提供，不写手机号标签。

## 3. 观察窗口

五档观察完成后必须把低敏累计值写入仓库外 JSON，并使用以下离线入口验证计数守恒、时间覆盖、停止线、活动告警、
最终开关状态和零未授权业务变更。校验器不连接服务器或供应商，也不证明 JSON 中的数据真实；原始 Prometheus、数据库、
运行进程和人工收件证据仍须由独立操作者对照保存。

```powershell
python scripts/verify-sms-phase5-observation-evidence.py --self-test
python scripts/verify-sms-phase5-observation-evidence.py --evidence C:\受控目录\phase5-observation.json
```

测试服只允许 `closed_after_canary`，五个快照期间不得出现 Canary 之外的新发送增量。`production_enabled` 只允许生产环境，
且只有在生产开关已经取得独立批准后才能使用；计划文件中的模式字段本身不能证明批准存在。

| 窗口 | 必查内容 | 当前结果 |
|---|---|---|
| 开启前 | health/ready、开关、模板/绑定、告警、回滚人 | 关闭态应用、五模板/绑定、Alertmanager firing/resolved 收件、双目标/IAM/白名单和回滚均通过；真实发送按一次性授权完成 |
| 关闭态 30 秒 | 发送日志、Provider 调用、代理健康、Prometheus | 2026-08-04、2026-08-05 两次均通过，发送与 Provider 调用零增量 |
| 5 分钟 | 调用数、非受理数、配置类错误、平均延迟 | 通过；最小窗口实际于 1812 秒采集，累计 `21/20/1`，当前 Provider 非受理 0、活动告警/通知失败 0 |
| 15 分钟 | 五场景分布、429、认证失败、用户反馈 | 通过；最小窗口实际于 1821 秒采集，累计 `21/20/1`，无新增发送或告警 |
| 30 分钟 | 失败率、登录/注册转化差异、审计完整性 | 通过；最小窗口实际于 1830 秒采集，累计 `21/20/1`，无新增发送或告警 |
| 2 小时 | 趋势与供应商账户状态 | 通过；实际于 13965 秒采集，累计 `21/20/1`，health/ready 200，当前 Provider `0/0`、活动告警和通知失败均为 0；单次固定 SSH，只读且零副作用 |
| 24 小时 | 日累计、预算、转化、残余风险 | 通过：无 BOM 修正版实际经过 95952 秒，health/ready 200、累计发送 `21/20/1`、Provider `0/0`、零活动告警和零通知失败，stderr 为空 |

2026-08-07 的独立补充观察 ChangeId `20260806T131125Z` 在距 Canary 完成 75594 秒时再次取得 health/ready 200、累计发送 `21/20/1`、当前进程 Provider `0/0`、零活动告警和零通知失败增量，执行副作用为 0；结果 SHA-256 为 `f99749b50c0109cc5ef268921c6badd54392e846aab73ab30154520d346f88f6`。它不属于上表五档连续窗口，仅用于补充说明较晚时点未观察到异常，不能把 24 小时状态改为已执行。

原 24h runner 随后按一次性授权执行，固定 SSH 连接一次、远端退出码 0，但本地严格门禁检测到 stderr 非空并拒绝生成快照；原失败路径未保留 stderr 正文，也未输出足以归类的低敏字段，因此本轮只能记为失败关闭。修正版 ChangeId `20260807T061605Z`、runner SHA-256 `87b0a38f14c10161d7f58fa52a733f96537daeaca5fa2004276681ac2b80bd28` 已离线就绪：它不放宽 stderr 门禁，只补齐白名单 stdout、stderr 布尔值和退出码的失败证据，执行仍须独立批准。

修正版执行证明 24h 业务观察字段全部通过且零副作用，但仍因 SSH stderr 非空失败关闭。代码检查发现传输参数缺少 `-T`，与 OpenSSH 对非终端 stdin 的伪终端警告行为一致；第三版仅修正传输层，显式禁用伪终端并冻结零口令提示，不接受任何 stderr。新 ChangeId `20260807T062052Z`、runner SHA-256 `b4ca5febc35b6af93ebd03120148d57bce24e558141a8c0ea8d7bd7741df378e` 已离线验证，24h 状态仍未通过。

第三版在显式 `-T` 后仍因 stderr 非空失败，虽然实际经过 89098 秒时所有观察值再次通过且零副作用；因此伪终端告警推断未被证实。当前候选改用隔离 locale 和无启动文件 Bash，并只在失败时输出 stderr 分类、计数与不可逆摘要；任何 stderr 仍阻断快照。最终候选为 ChangeId `20260807T064628Z`、runner SHA-256 `8cb7068967f1993351b07b874d33a162f663f25fa3007c4db11a4682c46c4506`，尚未执行。

隔离环境版已单次执行：实际经过 89584 秒，关闭态、health/ready、累计发送 `21/20/1`、Provider `0/0`、Alertmanager discard、零活动告警、零通知失败及零副作用均通过；远端业务脚本退出码 0。唯一阻断项仍是 SSH stderr：1 行、57 字节、分类 `other`、SHA-256 `1be4aae086b93109187f5c53d6269e4a399e6cdb039e41389362ee63c6677076`。原始正文未输出，无法安全豁免。最终传输层最小诊断候选 ChangeId `20260807T071436Z` 已离线就绪，只允许远端 `/usr/bin/true`；执行前建立不可覆盖的一次性锁，仅保存脱敏单行、计数和摘要。runner SHA-256 `407b57e301c30e987abbba83fe8512b925e6dddd095a0f07ecc688fadd18123d`，执行仍待独立授权；此前三个诊断候选均已被替代。

基础传输诊断已按授权执行一次：远端退出码 0、stdout/stderr 均为空，且执行锁与低敏结果均已形成，结果 SHA-256 `9a17aec9c374b5ea334448f2bd062c5be25495aea52ba11fc97c54e2865c4b14`。这证明 57 字节 stderr 不来自固定 SSH 连接本身。后续诊断应保持同一 SSH 参数，仅把远端命令增加为观察 runner 使用的隔离 Bash 包装并执行 `/usr/bin/true`；该操作仍需要新 ChangeId 和独立授权。

上述隔离 Bash 差分候选已生成：ChangeId `20260807T072327Z`、runner SHA-256 `7fe114dd12236d47dc2a9111967f127ef8a37be9bd2b853a05997b7b4b56b4d7`。候选不含观察 payload、数据库、HTTP 或业务读取，只验证 `env -i + locale + bash --noprofile --norc` 包装是否产生同一 stderr；执行仍待独立授权。

隔离 Bash 差分诊断已按授权执行一次并返回空 stderr，结果 SHA-256 `cecb38090f9b03c26f1a852480426d14cca800b5a6aca68304acb741811e2ca2`。因此隔离环境包装本身不产生历史 57 字节输出；下一最小差异是观察 runner 使用 `bash -s --` 接收 stdin。后续候选只应输入一行 `true`，不包含观察 payload 或业务读取。

对应 stdin 模式候选已生成：ChangeId `20260807T073038Z`、runner SHA-256 `2c55627ead5ef550c1f5a8c9a5fe87398f4bb04ab9324d1b6a2f576d2dd34e9b`。它只把 `true\n` 写入隔离 Bash stdin；执行结果将区分 `-s`/stdin 传输与实际观察脚本内容，仍待独立授权。

stdin 模式差分诊断已按授权单次执行并命中根因：远端退出码 127，stderr 为 1 行 58 字节，安全脱敏内容表明 `true` 前被注入 UTF-8 BOM；结果 SHA-256 `adbbbc45a066e93d095e03e47222a529e2566211ae2213776df9c31ad8004069`，执行锁已保留，业务读取和所有副作用均为 0。离线复算进一步证明历史 57 字节 stderr 的 SHA-256 `1be4aae086b93109187f5c53d6269e4a399e6cdb039e41389362ee63c6677076` 精确对应首行 `set` 被同一 BOM 污染。根因位于 Windows PowerShell 5.1 的 stdin 流写入路径，而非 SSH、隔离 Bash 或业务观察脚本。

观察生成器现改为受限临时文件句柄配合 `Start-Process -RedirectStandardInput` 传输原始字节，并在执行前后逐字节复核和精确清理；任何 stderr 仍失败关闭。修正版 24h 候选已在本地离线生成：ChangeId `20260807T081228Z`、runner SHA-256 `cb7a5611c9382a026460a5e7a276f6732abb7318539012ecae919e5e6fbb7226`。默认关闭、SelfTest、PowerShell 解析、无 BOM 契约、167 项阶段 5 契约及敏感扫描均通过；尚未联网或执行，24h 状态仍为未通过。

上述无 BOM 修正版随后按摘要绑定授权仅执行一次且未重试。24h 快照在距 Canary 完成 95952 秒时取得：health/ready 200、累计发送 `21/20/1`、当前进程 Provider `0/0`、活动 SMS/Alertmanager 告警 0、通知失败增量 0；配置修改、服务信号/重启、业务 POST、邮件、短信提交和真实短信均为 0，远端 stderr 为空。快照 SHA-256 为 `138ea1907aef1b7ad29bdb1011c5e354dcb392a8d48b8b4de728399ec5eb9ba7`。

五份快照随后在仓库外按完整摘要合并并通过连续窗口验证。最终关闭态文件 SHA-256 为 `7c06c03a012d4e334c2c1e6e311f8d07c1e80889382dfbbfe47ba080ea780f07`；五窗口观察证据 SHA-256 为 `dbe5916a7fd8b63434d779178fd3b283d3fb55da38972a01073e0f1fed87f96f`。权威验证器确认环境为测试服、模式为 `closed_after_canary`、五次提交/受理/人工收件均为 5、敏感值持久化 0，离线验证未联网或发送短信。

## 4. 自动停止线

- 任一签名、模板或账户异常：立即停止灰度并关闭 `SMS_ENABLED`。
- 最近 5 分钟至少 10 次调用且非受理比例持续超过 20%。
- 出现 OTP、完整手机号、密钥或 Token 泄露迹象。
- 场景错配、重复消费、权限绕过或大面积认证失败。
- 无法确认告警、回滚操作者或监控数据真实性。
