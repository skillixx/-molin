# AI 网关 G8 验收记录

> 当前状态：`G8_ENGINEERING_READY` 已达成；生产授权、客户灰度和四周商业观察尚未开始，因此尚未达到 `G8_COMMERCIAL_ACCEPTED`。

## 1. 基线

| 项目 | 当前值 |
|---|---|
| 基线提交 | `6e1f67ad4c1a10bb1ad79b3aeac6b16211ccfac1` |
| 功能分支 | `feature/backend-d-ai-gateway-g8-commercial-gray` |
| 代码评审 HEAD | `2189deda7c290f75b1b1c8892fc963e7a9b0b6c4` |
| G8 PR 最终 HEAD | `f560345f893189e3d15feec299bbb4dafde87632` |
| G8 PR | #327，已按 merge commit 合并 |
| merge commit | `71fce50f8bdab5078865154bb715e598cec32e0c` |
| G7 PR | #326，merge commit `6e1f67ad`，CI 7/7 |
| G8 Migration | 当前无新增 |
| 生产/真实客户/真实费用 | 未执行 |

## 2. 工程门禁

| 门禁 | 状态 | 证据 |
|---|---|---|
| G7 旧状态同步 | PASS | README 与 G7 验收记录已同步 PR #326、CI、测试服、QA、产品和 merge commit |
| 渠道健康 SSRF | 开发自测 PASS | loopback/RFC1918/link-local/IPv6、本地 DNS 解析、重定向和精确内网白名单专项测试 |
| 生产配置与流量总闸 | 开发自测 PASS | 生产默认关闭；配置缺项和 DB 发布事实缺项失败关闭；关闭态返回 50330 |
| 全量 Go/前端/Promtool/敏感扫描 | PASS | Go 全量测试、vet、mod verify、Linux race、双端契约/type-check/lint/production build、22 条 Promtool 规则与阈值、生产抓取配置、Actionlint、脚本语法、diff check 和差异敏感扫描均通过；精确代码 HEAD `2189deda7c290f75b1b1c8892fc963e7a9b0b6c4` 的 CI run `31506128982` 为 9/9 PASS |
| G7 可靠性回归 | PASS | 隔离 MySQL/Redis/Fake 上游：1000@100、幂等 100、断连、Fake 上游与 Redis 故障恢复通过；三项差额、七类异常、hold、Outbox、补偿均为 0 |
| 生产等价隔离部署/回滚 | 开发自测 PASS | 精确提交 `c74c86614ce592dd8db168db6ef17bac078bac57` 完成基线→候选→基线回滚→候选恢复；隔离环境实际创建数据、API↔LB、LB↔节点、节点独占出站、模拟公网五个网络，并动态证明 API/模拟公网入口不能直连 Bifrost 节点、LB 可以访问节点；边缘保险丝保持关闭时开启应用总闸，真实 MySQL 8 有效发布事实可以启动，异常汇率、重复 meter、健康过期和 circuit_open 四类负例均拒绝启动；双 Fake Bifrost 节点和 LB 鉴权、内部鉴权头清除、Secret 白名单、TLS、SSE 禁缓冲、生产用户入口显式 1m 请求体限制、日志轮转、指标 404、候选应用总闸、四类入口边缘 kill switch、旧版回滚四入口 50330、98 表结构备份恢复和数据库保留通过；候选镜像 SHA-256 `3168678cd65f6d7140641dea1755397d754b6c3895d1b224e9ac19e70af41dc0`，基线镜像 SHA-256 `1c3d5ba44457bbf90b849c6c7a059281f7dbf67c40ab1d11785fc87fd6c47ea8`，候选二进制 SHA-256 `2023755e943591f0467b4f3a9b480da7148eea0920e5dfa8c675eac261d6b13a`，备份 SHA-256 `869c334eb39c988dab456288c7ee6e8d311926232dd90e4e68552bd20c4db3da`；上述均为本地临时制品和隔离数据，不是发布制品或生产备份 |
| 真实后端浏览器 E2E | 开发自测 PASS | 无 API Mock；管理员发布、用户模型发现、Project/SK、一次 Fake 文字调用、Usage、账单、申诉及 1440/768/375 视口通过；三项差额、异常、hold、Outbox、补偿均为 0 |
| 小额账单八位精度 | 开发自测 PASS | 真实 MySQL 发现并修复 DECIMAL 除法把 `0.00001400` 截断为四位的对账误报；改用显式八位定点乘法，Go 回归及真实后端只读对账通过 |
| 首轮独立代码/产品评审 | 已修复，待复评 | 精确 HEAD `16abed3` 发现的生产门禁与运行规则、共享转发总闸、Bifrost 重试/编排和预演认证等 P1 已在 `444c6a1` 修复并完成开发自测；不得以首轮 QA PASS 覆盖，最终 HEAD 必须重新独立评审 |
| 第二轮独立复评 | 已修复，待第三轮复评 | 精确 HEAD `7ef8f93` 的产品/规格复评 P0/P1=0；代码安全与 QA 发现的 Bifrost 完整环境文件过度授权、旧版回滚缺少独立边缘总闸两项 P1 已在 `3a7fa37` 修复并完成隔离预演，必须对最终 HEAD 再复评 |
| 第三轮独立复评 | 已修复，待第四轮复评 | 精确 HEAD `270a1c6` 的产品/规格复评 P0/P1=0；代码安全与 QA 发现节点可被 API/公开前端绕过 LB 直连的 P1，当前已拆分 API↔LB、LB↔节点、节点出站三网并补精确拓扑断言，必须对新 HEAD 再复评 |
| 第四轮独立复评 | P0/P1=0，P2 已修复待终审 | 精确 HEAD `450384b` 的产品、代码安全和 QA 均为 P0=0、P1=0；三方共同指出隔离预演仍使用单一临时网络、证据强于运行态验证的 P2，已在 `115828b` 改为五网络实际预演并补直连失败/成功动态断言，最终 HEAD 仍须聚焦复评 |
| 第五轮独立复评 | 已修复，待最终复评 | 精确 HEAD `609d5b5` 的 QA 为 P0/P1/P2=0；产品和代码安全发现应用总闸关闭导致真实 MySQL 启动门禁未执行的 P1，以及生产用户入口请求体限制、共享 ChatOnce 错误契约两项 P2，已在 `dd0dca0` 补齐真实正负门禁、20m 生产入口和工作台/会话 50330 映射并完成隔离演练 |
| 最终独立规格/代码安全评审 | PASS | 精确代码 HEAD `2189deda7c290f75b1b1c8892fc963e7a9b0b6c4`；代码安全与产品/规格均为 P0=0、P1=0、P2=0 |
| 最终 QA / 产品 | PASS | 同一精确代码 HEAD；隔离真实后端 admin/user E2E 均通过，三项账务差额和 Outbox 均为 0，1440/768/375 视口通过 |
| 代码 HEAD CI | PASS | 精确代码 HEAD `2189deda7c290f75b1b1c8892fc963e7a9b0b6c4` 的 CI run `31506128982` 为 `completed/success`，9/9 门禁通过 |
| PR 最终 HEAD CI / Ready / merge commit | PASS | 最终 HEAD `f560345f893189e3d15feec299bbb4dafde87632` 的 CI run `31507153082` 为 `completed/success`，9/9 门禁通过；PR 转 Ready 后已按 merge commit 合并，未使用 squash，远端功能分支已删除 |

### 2.1 工程就绪后的迁移准备

- 已新增不含 Secret 的分阶段迁移清单示例与离线校验器，将 `test_candidate`、`production_readonly`、`production_closed_deploy`、`production_gray` 四阶段分别失败关闭；测试候选不得声明生产授权或打开流量。
- 2026-08-12 仅对仓库声明的测试入口执行 3 个匿名 GET：用户端和管理端返回 HTTP 200，公网 API health 不可达。该结果不代表测试 API、数据库、Bifrost、监控或账务通过。
- 首次匿名入口核验时，执行环境尚未取得测试服务器 SSH 连接条件，因此该次未读取远端配置值、进程、schema、备份或监控，也未上传、重启或写入测试服务器；后续单次 SSH 只读基线见本节末尾及独立报告。
- 历史 README 中出现过测试凭据字面量，当前文件已移除，但 Git 历史仍可能保留旧值；相关测试凭据必须视为已暴露并完成轮换后，才能进入生产关闭态部署门禁。
- 迁移操作边界和待收集证据见 `docs/ai-gateway-g8-test-to-production-handoff.md`。本节不改变 `G8_ENGINEERING_READY`，也不构成任何生产授权。
- 2026-08-12 测试服单次只读基线确认：现有 API 进程/监听为 0，health/ready 不可达；API 二进制仍为 G7 摘要，Docker、schema、Bifrost、监控和账务因权限与工具缺失保持 UNKNOWN；历史 MySQL/RabbitMQ/MinIO 密码已不匹配，但 Redis 无密码、SSH 账号仍有密码。P0=0、P1=3，测试服 G8 验收未通过。完整证据见 `docs/ai-gateway-g8-test-server-readonly-audit-20260812.md`。
- 已完成最小只读运维入口的仓库候选设计：不授予 Docker 组权限，只允许 root-owned 固定审计器和对账器，并由单命令 sudoers 约束。该入口尚未上传或安装，不能据此关闭测试服 UNKNOWN；安装与后续只读核验分别需要独立 ChangeId。见 `docs/ai-gateway-g8-test-readonly-access-runbook.md`。

## 3. 商业验收

`G8_COMMERCIAL_ACCEPTED` 尚不具备：生产目标、真实上游费用、真实客户、真实资金、价格/财务批准和真实告警联系人均未获本轮独立授权；设计客户数量、真实集成、真实付费、四周成功率和毛利不得填写虚构值。

## 4. 完成判定

- 工程门禁已全部通过并完成合并：当前结论为“G8 工程就绪，商业观察未完成”。
- 只有另获逐项生产授权并满足四周商业指标，才可报告 `G8_COMMERCIAL_ACCEPTED`。
