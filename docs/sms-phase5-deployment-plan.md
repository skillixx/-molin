# 阿里云短信验证码阶段 5 部署计划

## 1. 授权边界

测试服固定代理、监控规则、后端关闭态部署与本地提交/主线同步已于 2026-08-04 获批并执行。真实短信、生产部署、
生产开关、Git 推送/PR/合并仍须分别取得项目负责人明确授权。默认状态始终为 `SMS_ENABLED=false`。

## 2. 发布对象

- 后端：阶段 5 指标导出改动及阶段 4 已合并业务代码。
- 管理后台/用户控制台：阶段 4 主线构建，不新增 API 契约。
- 配置：测试与生产独立的短信凭据、HMAC、白名单、可信代理和内部指标 Token。
- 数据库：只读核验 schema version 和 `000058/000059` 结构；默认不执行 migration。
- 监控：统一内部 metrics 端点新增短信固定低基数序列和四条告警。

当前测试服只具备 Prometheus 规则计算，没有 Alertmanager 配置引用或运行实例。通知接收渠道、值班人、Secret 注入、
Alertmanager 关闭态部署和后续触发演练必须作为独立变更审批，不能把 4 条规则已加载写成通知链已完成。
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
当前环境未替换、服务未重启、短信未发送。候选生成已经完成，但旧二进制运行时仍需在独立授权窗口验证，不能据此记为实际回滚通过。
通知链为 `receiver_configuration_required`。该入口不会执行回滚或触发告警，但 SSH 与 HTTP GET 可能增加系统访问和审计日志。

手动前端部署工作流已同步使用 `molin-sms-proxy`、`172.20.250.0/28`、管理端 `.2`、用户端 `.3` 和宿主网关
`.1`。工作流会在删除旧容器前保存真实运行镜像，检查宿主路由和全部 Docker 网络重叠，并在部署或健康检查失败时
自动以原镜像恢复两套容器。不得再使用默认 `bridge` 或漂移容器 IP 作为短信来源 IP 信任边界。

## 4. 生产顺序

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
