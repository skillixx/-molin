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
| 全量 Go/前端/Promtool/敏感扫描 | 开发自测 PASS | Go 全量测试、vet、mod verify、Linux race、双端契约/type-check/lint/production build、22 条 Promtool 规则与阈值、生产抓取配置、Actionlint、脚本语法、diff check 和差异敏感扫描均通过；PR CI 仍待执行 |
| G7 可靠性回归 | PASS | 隔离 MySQL/Redis/Fake 上游：1000@100、幂等 100、断连、Fake 上游与 Redis 故障恢复通过；三项差额、七类异常、hold、Outbox、补偿均为 0 |
| 生产等价隔离部署/回滚 | 开发自测 PASS | 精确提交 `c62fe964bdb2bcb6ce3682f846db32ffba10a82b` 完成基线→候选→基线回滚→候选恢复；双 Fake Bifrost 节点和 LB 鉴权、内部鉴权头清除、三层网络与 Secret 白名单、TLS、SSE 禁缓冲、20m 请求体限制、日志轮转、指标 404、候选应用总闸、四类入口边缘 kill switch、旧版回滚四入口 50330、98 表结构备份恢复、MySQL 8 门禁 SQL 和数据库保留通过；候选镜像 SHA-256 `eb1c52a228e0d6096670e2dd6b32b4bd0616cdb200dbd84753082e7830ef6ee9`，候选二进制 SHA-256 `a2cecdc6b8074c3a384c57fcb6beb266dd8822e1f5a0257b972793af81933b5c`，仅为本地临时制品，不是发布制品 |
| 真实后端浏览器 E2E | 开发自测 PASS | 无 API Mock；管理员发布、用户模型发现、Project/SK、一次 Fake 文字调用、Usage、账单、申诉及 1440/768/375 视口通过；三项差额、异常、hold、Outbox、补偿均为 0 |
| 小额账单八位精度 | 开发自测 PASS | 真实 MySQL 发现并修复 DECIMAL 除法把 `0.00001400` 截断为四位的对账误报；改用显式八位定点乘法，Go 回归及真实后端只读对账通过 |
| 首轮独立代码/产品评审 | 已修复，待复评 | 精确 HEAD `16abed3` 发现的生产门禁与运行规则、共享转发总闸、Bifrost 重试/编排和预演认证等 P1 已在 `444c6a1` 修复并完成开发自测；不得以首轮 QA PASS 覆盖，最终 HEAD 必须重新独立评审 |
| 第二轮独立复评 | 已修复，待第三轮复评 | 精确 HEAD `7ef8f93` 的产品/规格复评 P0/P1=0；代码安全与 QA 发现的 Bifrost 完整环境文件过度授权、旧版回滚缺少独立边缘总闸两项 P1 已在 `3a7fa37` 修复并完成隔离预演，必须对最终 HEAD 再复评 |
| 第三轮独立复评 | 已修复，待第四轮复评 | 精确 HEAD `270a1c6` 的产品/规格复评 P0/P1=0；代码安全与 QA 发现节点可被 API/公开前端绕过 LB 直连的 P1，当前已拆分 API↔LB、LB↔节点、节点出站三网并补精确拓扑断言，必须对新 HEAD 再复评 |
| 独立规格/代码安全评审 | 待执行 | P0/P1 必须为 0 |
| QA / 产品 | 待执行 | 必须同一精确 PR HEAD |
| PR / CI / merge commit | 待执行 | CI 全绿后转 Ready；禁止 squash |

## 3. 商业验收

`G8_COMMERCIAL_ACCEPTED` 尚不具备：生产目标、真实上游费用、真实客户、真实资金、价格/财务批准和真实告警联系人均未获本轮独立授权；设计客户数量、真实集成、真实付费、四周成功率和毛利不得填写虚构值。

## 4. 完成判定

- 工程门禁全部通过并合并后，只能报告“G8 工程就绪，商业观察未完成”。
- 只有另获逐项生产授权并满足四周商业指标，才可报告 `G8_COMMERCIAL_ACCEPTED`。
