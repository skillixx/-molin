# 阿里云短信验证码阶段 5 部署计划

## 1. 授权边界

测试服固定代理、监控规则、后端关闭态部署已获批并执行；测试服五场景真实收件 Canary 也已在后续独立一次性授权下完成并恢复关闭态。
当前修正版代码、测试和证据文档已同步到阶段 5 分支及 PR #323，正式 CI 全部通过。生产部署、生产真实短信、生产开关、
PR 合并仍须分别取得项目负责人明确授权。默认状态始终为 `SMS_ENABLED=false`。

## 2. 发布对象

- 后端：阶段 5 指标导出改动及阶段 4 已合并业务代码。
- 管理后台/用户控制台：阶段 4 主线构建，不新增 API 契约。
- 配置：测试与生产独立的短信凭据、HMAC、白名单、可信代理和内部指标 Token。
- 数据库：只读核验 schema version 和 `000058/000059` 结构；默认不执行 migration。
- 监控：统一内部 metrics 端点新增短信固定低基数序列和四条告警。

当前测试服已完成 Alertmanager 邮件候选关闭态部署及随后独立授权的修正版通知演练：Prometheus 活跃 Alertmanager 为 1；
演练仅 1 次 firing 和 1 次 resolved，负责人均确认 QQ 收件，结束后根路由恢复 `discard`，活动告警和通知失败 0、短信增量 0。
部署健康与实际投递仍作为两层证据保存，不能只凭 4 条规则已加载或 Alertmanager 健康推定通知可达。
渠道中立的审批字段、离线 `amtool` 校验、关闭态部署和单次合成演练顺序见
`docs/sms-phase5-alertmanager-change-runbook.md`；该手册不包含任何接收渠道值或 Secret。

## 3. 测试服顺序

1. 记录 API 二进制哈希、health/ready、容器与监听端口、schema version、五模板/五绑定摘要和发送日志计数。
2. 备份当前 API 二进制及不入库环境文件；备份内容按密钥策略保管，不打印值。
3. 固定前端代理专用子网并配置 `TRUSTED_PROXY_IPS`；保持 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`。
4. 部署后端，验证 health/ready、关闭态 503/50300、邮箱链路和九个管理 API 只读能力。
5. 部署两套前端，验证 1440/1024/768/390、权限/MFA、模板与日志页面五态。
6. 验证 metrics 双闸、短信 40 条调用序列和 10 条耗时序列，加载告警规则。
7. 在新的独立授权窗口临时开启 `SMS_ENABLED=true`，白名单发送总上限 10 条，完成五场景入口；结束立即恢复 false。
8. 对账受理数、失败数、真实收件、发送日志和审计；不得把 accepted 写成送达。

上述第 7–8 步已使用最终 ChangeId `20260806T053735Z` 完成：五场景各提交一次、零重试，数据库确认五条供应商受理，
项目负责人逐场景确认两个自有号码均收件，5 条 OTP 均未消费，结束恢复 `SMS_ENABLED=false`。该结果不继承为生产授权。

代理网络已按计划落地为 `172.20.250.0/28`，管理端和用户端固定为 `.2`、`.3`。部署窗口已重新对照
`ip -4 route show` 和全部 `docker network inspect`，确认无重叠后才创建精确 `/28` 网络。复验规划可执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-proxy-network-plan.ps1 -SelfTest
```

该命令只验证规划，不执行远端操作。网络创建、容器重建、环境文件修改和 API 重启仍需分别列入获批窗口。

回滚材料和通知链可用以下只读入口复核：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-recovery-readiness.ps1
```

当前回滚材料完整性预检通过，三份容器快照引用的旧镜像仍存在；但旧环境缺少固定代理信任且包含废弃的
`SMS_TEMPLATE_CODE_*` 键，禁止整份恢复。实际回滚候选必须从当前环境生成，保留固定代理信任、保持
`SMS_ENABLED=false`/`SMS_TEST_MODE=true` 并排除废弃模板键。项目负责人已单独批准真实生成，测试服候选
`candidate-20260805T015043Z.env` 已排他创建并通过 700/600 权限、SHA-256、固定代理、短信关闭、废弃键和重复键只读核验；
当前环境未替换、服务未重启、短信未发送。候选生成当时只证明配置准备完成，不能单独记为实际回滚通过。
最终运行时 runner `20260805T115540Z` 后续已取得独立暂存授权并上传固定测试服精确目录；远端摘要、`pc:600`、
`bash -n`、SelfTest 和关闭态只读预检通过。暂存不包含 `--execute`，没有替换二进制或环境，也没有发送信号、重启服务、
触发告警、发送邮件或短信。后续独立执行授权已取得并消费：冻结 runner 仅执行一次、无重试，旧二进制关闭态稳定 10 秒后
自动恢复当前二进制及原进程环境并稳定 10 秒；独立只读验收通过，环境文件未替换，通知/业务 POST、邮件和短信均为 0。
通知演练证据已通过独立契约并进入 Canary 聚合预检。回滚只读入口不会执行回滚或触发告警，但 SSH 与 HTTP GET 可能增加系统访问和审计日志。

手动前端部署工作流已同步使用 `molin-sms-proxy`、`172.20.250.0/28`、管理端 `.2`、用户端 `.3` 和宿主网关
`.1`。工作流会在删除旧容器前保存真实运行镜像，检查宿主路由和全部 Docker 网络重叠，并在部署或健康检查失败时
自动以原镜像恢复两套容器。不得再使用默认 `bridge` 或漂移容器 IP 作为短信来源 IP 信任边界。

## 4. 生产顺序

生产目标元数据必须先通过本地离线候选冻结，至少包含目标别名、SSH 地址/端口/用户、唯一 ED25519 指纹、项目目录、
`.env.prod` 路径、服务形态、API 服务唯一标识、API/Prometheus/Alertmanager 本机端口以及回滚/观察操作者低敏别名。生成器不会连接生产、读取环境文件或取得任何后续授权：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prepare-sms-phase5-production-target-intake.ps1 -SelfTest
```

实际导出候选必须使用全新的 ChangeId 和工作区外目录，并由项目负责人提供上述非密钥元数据。候选 SHA-256 形成后，生产只读基线、
关闭态部署、白名单 Canary 和正式开启仍是四个互不继承的人工门禁；密码、私钥、Token、手机号和环境值不得作为生成参数或输出。

冻结生产目标后，可在本地生成摘要绑定的关闭态只读基线 runner。生成与 SelfTest 不读取 `known_hosts`、不连接生产；实际
`-ExecuteReadOnly` 才会重新核对唯一 ED25519 指纹并通过一次 SSH stdin 只读环境文件/进程一致性、health/ready、schema、
五模板/绑定、发送聚合、内部指标、Prometheus、Alertmanager、活动短信告警和通知失败。runner 只输出布尔与聚合计数，
并要求以 `CreateNew` 排他保存同一字段白名单的本地低敏 JSON 及 SHA-256；成功和阻断结果均可审计且禁止覆盖。它不会验证备份真实可恢复性；备份能力和回滚人仍须人工证明。生成授权不继承为只读执行授权：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prepare-sms-phase5-production-readonly-baseline.ps1 -SelfTest
```

生产只读结果形成 SHA-256 后，关闭态部署计划生成器会同时绑定目标候选、实际只读 runner 及结果、完整发布提交、API 制品、两套前端镜像、
migration 决策、备份证据摘要和回滚证据摘要。它还会调用同一权威只读基线生成器，按目标候选与 ChangeId 重新生成 runner；只有完整文件摘要精确一致才接受，并把生成器摘要写入计划，不能用注释、死代码或额外网络调用伪造结构标记。schema 已达到 59 时只能 `verify-only`；仅当前 schema 精确为 58 时允许
规划 `apply-up-to-59`，并只允许只读结果因 `schema_ready=false` 单项阻断，其他关闭态、配置、模板、指标和监控门禁必须全部通过。schema 低于 58 必须先走独立 migration 方案，不能由本计划跨版本补齐。计划仍固定短信关闭、测试模式开启、数据库模板源、五模板/绑定、四条告警、自动回滚和零重试；
备份摘要不等于恢复验证，且生成计划不会授予 migration、部署、Canary 或正式开启权限：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prepare-sms-phase5-production-closed-deployment-plan.ps1 -SelfTest
```

1. 只读确认生产目标、当前版本、拓扑、schema、备份能力、监控和回滚操作者。
2. 部署应用和前端，但保持 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`，白名单为空或仅包含批准号码。
3. 验证关闭态、反代来源头、metrics、九 API 只读与页面，不发送短信。
4. 产品经理批准后进入白名单 Canary：临时维护批准号码，总量和时窗单独授权。
5. Canary 通过且 QA/产品签字后，将 `SMS_TEST_MODE=false` 作为独立生产变更；`SMS_ENABLED` 的开启必须与其发布批次、回滚操作者和观察窗口绑定。
6. 按 5 分钟、15 分钟、30 分钟、2 小时、24 小时记录指标。任一停止条件触发时先关闭 `SMS_ENABLED`。

## 5. 禁止事项

- 不在命令参数、终端输出、日志、PR 或文档中写入凭据、Token、验证码或完整手机号。
- 不以手工 SQL 绑定场景，不恢复 `SMS_TEMPLATE_CODE_*` 环境变量。
- 不在无备份、无观察人、无回滚人的情况下开启生产短信。
- 不用 Mock、固定验证码或前端明文验证码代替生产故障降级。
- 不因应用回滚默认执行 `000059 down`；已有短信事实时保留表和日志。
