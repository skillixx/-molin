# AI 网关 G8 验收记录

> 当前状态：开发中，尚未达到 `G8_ENGINEERING_READY`；生产授权和四周商业观察尚未开始。

## 1. 基线

| 项目 | 当前值 |
|---|---|
| 基线提交 | `6e1f67ad4c1a10bb1ad79b3aeac6b16211ccfac1` |
| 功能分支 | `feature/backend-d-ai-gateway-g8-commercial-gray` |
| G7 PR | #326，merge commit `6e1f67ad`，CI 7/7 |
| G8 Migration | 当前无新增 |
| 生产/真实客户/真实费用 | 未执行 |

## 2. 工程门禁

| 门禁 | 状态 | 证据 |
|---|---|---|
| G7 旧状态同步 | PASS | README 与 G7 验收记录已同步 PR #326、CI、测试服、QA、产品和 merge commit |
| 渠道健康 SSRF | 开发自测 PASS | loopback/RFC1918/link-local/IPv6、本地 DNS 解析、重定向和精确内网白名单专项测试 |
| 生产配置与流量总闸 | 开发自测 PASS | 生产默认关闭；配置缺项和 DB 发布事实缺项失败关闭；关闭态返回 50330 |
| 全量 Go/前端/Promtool/敏感扫描 | 进行中 | 本地 Go、vet、mod verify、Linux race、双端 typecheck/lint/build/契约与既有 Playwright 已通过；最终 HEAD 仍需复跑敏感扫描与全部静态门禁 |
| G7 可靠性回归 | PASS | 隔离 MySQL/Redis/Fake 上游：1000@100、幂等 100、断连、Fake 上游与 Redis 故障恢复通过；三项差额、七类异常、hold、Outbox、补偿均为 0 |
| 生产等价隔离部署/回滚 | 开发自测 PASS | 临时生产形态栈完成基线→候选→基线回滚→候选恢复；TLS、SSE 禁缓冲、20m 请求体限制、日志轮转、指标 404、流量关闭、98 表结构备份恢复、MySQL 8 门禁 SQL 和数据库保留通过；候选二进制 SHA-256 `48f92ff6...17d1`，仅为本地临时制品，不是发布制品 |
| 真实后端浏览器 E2E | 开发自测 PASS | 无 API Mock；管理员发布、用户模型发现、Project/SK、一次 Fake 文字调用、Usage、账单、申诉及 1440/768/375 视口通过；三项差额、异常、hold、Outbox、补偿均为 0 |
| 小额账单八位精度 | 开发自测 PASS | 真实 MySQL 发现并修复 DECIMAL 除法把 `0.00001400` 截断为四位的对账误报；改用显式八位定点乘法，Go 回归及真实后端只读对账通过 |
| 独立规格/代码安全评审 | 待执行 | P0/P1 必须为 0 |
| QA / 产品 | 待执行 | 必须同一精确 PR HEAD |
| PR / CI / merge commit | 待执行 | CI 全绿后转 Ready；禁止 squash |

## 3. 商业验收

`G8_COMMERCIAL_ACCEPTED` 尚不具备：生产目标、真实上游费用、真实客户、真实资金、价格/财务批准和真实告警联系人均未获本轮独立授权；设计客户数量、真实集成、真实付费、四周成功率和毛利不得填写虚构值。

## 4. 完成判定

- 工程门禁全部通过并合并后，只能报告“G8 工程就绪，商业观察未完成”。
- 只有另获逐项生产授权并满足四周商业指标，才可报告 `G8_COMMERCIAL_ACCEPTED`。
