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
- 已完成最小只读运维入口的仓库候选设计：不授予 Docker 组权限，只允许 root-owned 固定审计器和对账器，并由单命令 sudoers 约束。该入口尚未安装到 live 目标；003 暂存上传状态后续已由 008 收敛为 `ABSENT`，但不能据此关闭其他测试服 UNKNOWN。后续安装与运行态核验仍需要相互独立的 ChangeId。见 `docs/ai-gateway-g8-test-readonly-access-runbook.md`。
- 最小只读入口 PR `#331` 已按 merge commit 合并为 `c50f092339fcad79ca1262925480219db1755318`，精确功能 HEAD `f45be6f99c5d363caf166dba1ad2d172ad4646a8` 的 CI run `31563417231` 为 9/9 SUCCESS；独立 Standards、Spec 与 QA 均为 P0/P1/P2=0。上述事实只证明仓库资产和 CI，通过并不代表测试服已安装入口或补齐运行态证据。
- 本地候选包生成器及 CI 门禁已通过 PR `#333` 合并：精确 HEAD `c0479f607c9dbd5713c9fbbde7b3fb83ac2a3adc` 锁定唯一 ChangeId、冻结提交、源码树、Go 1.26.5、三项制品摘要和对账器大小，并在隔离环境连续构建两次；CI run `31566629193` 为 9/9 SUCCESS，独立代码安全、QA 和产品/规格签署均为 P0/P1/P2=0，merge commit 为 `69439c4c9b14c67bf8a17dd8822d80ecdc784a27`，远端功能分支已删除。CI 生成的低敏 `SHA256SUMS` 回执为 `14b7d8cd832f0b719031fcc93adbbb2208afe76d34383e63d51c44b044772b5a`。上述证据不代表已经上传、安装或重新核验测试服，运行态 P1=3 和 UNKNOWN 仍未关闭。
- 用户批准 `CHG-G8-TEST-READONLY-ACCESS-20260812-001` 后，2026-08-12 只执行了一次固定 known_hosts 与 `sudo -n -l` 前置检查。主机 ED25519 指纹匹配，但 sudo 明确要求密码，触发“权限不符合立即停止”；未执行后续身份读取、候选包生成、上传、安装或 sudoers 修改，候选资产、配置、服务、数据库、队列和业务数据写入以及业务请求、上游请求和费用均为 0。SSH/sudo 可能产生系统访问审计日志，本次未读取。该 ChangeId 已消费，继续安装须使用新的 ChangeId 和独立受控 root 管理通道。证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812.md`。
- `CHG-G8-TEST-READONLY-ACCESS-20260812-002` 候选曾通过 PR #336 精确 HEAD `91c2bd9e70774319f67436c8b545bc57181f5aa8`、CI run `31573880151` 9/9 和独立三方验收。用户批准安装后，本地五文件/回执及 known_hosts 指纹通过，但唯一一次只读 SSH 在 machine-id 摘要命令处因跨 shell 引号解析错误非零退出；随即停止，未执行 SCP、root 控制台、安装、sudoers 修改或 self-test。002 已消费且禁止重试，证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812-002.md`。
- `CHG-G8-TEST-READONLY-ACCESS-20260812-003` 的仓库工程门禁曾通过，用户随后批准执行一次只读 SSH、一次原子 SFTP、一次 root 安装和一次 sudo self-test。正式调用前本地 `--self-test`、`--local-check` 与五文件回执均通过；包装器仅正式调用一次并返回固定低敏结果 `G8_TEST_READONLY_ACCESS_STAGE=FAILED reason=remote_stage_failed`，随即按停止条件零重试结束。未进入 root 控制台，未创建 live 安装目标，未修改 sudoers，也未执行 self-test；业务请求、上游请求和费用为 `0 / 0 / 0 CNY`。由于该结果同时覆盖 SSH 与 SFTP 失败，是否创建远端暂存目录或上传部分文件为 `UNKNOWN`，不得推定为不存在。003 已消费，普通候选生成和 stage 调用现均失败关闭；继续须使用新 ChangeId 取得只读暂存取证授权。证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812-003.md`。测试服 P1 与 UNKNOWN 均未关闭。
- `CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004` 的工程门禁已合并，用户随后批准唯一一次本地检查和正式 SSH。本地检查 PASS；正式调用返回 `G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=remote_evidence_failed`、退出码 2 后零重试停止，业务请求、上游请求和费用为 `0 / 0 / 0 CNY`。该低敏汇总不能区分 SSH 返回码、stderr 或 stdout 契约失败，也不能证明暂存存在或不存在；003 暂存状态继续为 `UNKNOWN`。004 已消费，继续诊断必须使用新 ChangeId、重新完成工程门禁并取得独立用户授权。证据见 `docs/ai-gateway-g8-test-readonly-staging-evidence-attempt-20260812-004.md`。
- `CHG-G8-TEST-READONLY-TRANSPORT-DIAG-20260812-005` 的工程门禁已通过并合入主干，用户随后独立批准唯一一次本地检查和正式只读 SSH。本地检查 PASS；正式结果为 `ssh_exit_class=ZERO`、`stdout_contract=EXACT`、`stdout_bytes=39`、`stderr_state=EMPTY`、`diagnostic=PASS`，零重试且业务请求、上游请求、费用为 `0 / 0 / 0 CNY`。该结果只证明固定 SSH 与远端隔离 Python 标记可用，未读取暂存目录，不能证明 003 暂存存在或不存在；暂存状态继续为 `UNKNOWN`。005 已消费，证据见 `docs/ai-gateway-g8-test-readonly-transport-diagnostic-attempt-20260812-005.md`。
- `CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006` 用户批准后完成唯一一次本地检查和正式只读 SSH：本地检查 PASS；正式调用返回 `BLOCKED`、`gate_reason=MACHINE_ID` 后零重试停止，业务请求、上游请求和费用为 `0 / 0 / 0 CNY`。该门禁发生在暂存查找之前，不能证明 003 暂存存在或不存在，暂存状态继续为 `UNKNOWN`。006 已消费并在身份读取和联网前失败关闭；执行证据精确 HEAD `7157ee0f4a92b73a06855b0a8f35f12f07575ce4` 通过独立代码安全、QA、产品/规格 P0/P1/P2=0 与 CI run `31613370496` 12/12 SUCCESS，由 PR #347 按 merge commit `2399f58143b683b95fdf3011be8a535bfedef222` 合入主干。继续诊断必须使用新 ChangeId 并重新完成工程与用户授权门禁。证据见 `docs/ai-gateway-g8-test-readonly-staging-evidence-attempt-20260812-006.md`。
- `CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007` 完成工程门禁后，用户批准并执行唯一一次本地检查和正式只读 SSH：本地检查 PASS；正式结果为 `BLOCKED / READABLE_MISMATCH`，随后零重试停止，业务请求、上游请求和费用为 `0 / 0 / 0 CNY`。该结果只证明当前只读值与既有批准摘要不一致，不输出当前原文或摘要，也不能判断哪一方正确；未读取 003 暂存目录，暂存状态继续为 `UNKNOWN`。007 已消费；按执行当时的停止条件，原本要求后续通过阿里云 root/CMDB 等独立来源核验且不得自动更新批准基线。该物理主机身份要求已被下一条 Drop 场景确认替代，不再是 008 的前置门禁。执行证据 HEAD `6edbd89c3c6c1c8392262a775b2ac087caee3df7` 通过 CI run `31650387182` 12/12、独立代码安全、QA、产品/规格 P0/P1/P2=0，并由 PR #351 按 merge commit `492b56b9345592f1b5580e6de9fb1a1dfc540b93` 合入主干；远端分支已删除。证据见 `docs/ai-gateway-g8-test-readonly-host-identity-diagnostic-attempt-20260813-007.md`。
- 后续确认测试入口由 Drop 服务映射，因此物理 hostname/machine-id 并非适用的目标身份门禁；007 的真实历史结果继续保留，但不再作为当前运行态 P1。008 在独立用户授权后完成唯一一次本地检查和唯一一次只读 SSH，固定结果为 `ABSENT / NOT_APPLICABLE / NONE`、stderr 为空、零重试，业务请求、上游请求和费用为 `0 / 0 / 0 CNY`。固定 003 暂存状态已从 `UNKNOWN` 关闭为 `ABSENT`，未执行也无需执行清理；008 已消费并禁止重放。执行证据 HEAD `f0d726ad5d347dd1f35f2ebec2e118f0093e958f` 经 CI run `31661245959` 12/12 与独立代码安全、QA、产品/规格 P0/P1/P2=0 后，由 PR #356 按 merge commit `6b2a1fa438dbad2e7d0a15b33d4c8c0d8ff8b7be` 合入主干。该证据不证明 API、数据库、Bifrost、监控、备份、账务或只读审计入口可用，继续安装准备必须使用新 ChangeId 并重新完成工程与用户授权门禁；见 `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-attempt-20260813-008.md`。
- 009 已按 Drop 契约重新冻结最小只读入口候选：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009` 只绑定 `pc@8.130.9.163:10003`、登录用户、部署根和制品，不读取或门禁物理 hostname/machine-id；本地五文件回执为 `840bdbed48edab6d70d351fa232b7426903bf3f3098f682e2884f513b9cd0efd`。PR #358 精确 HEAD `2efb809ba090c9af780d8c6be2f75ee707b92d6b` 的 CI run `31665135810` 为 12/12 SUCCESS，独立代码安全、QA、产品/规格均为 P0/P1/P2=0，并按 merge commit `1f0c2d11dc705be9496eb18c73688d21ee0e8ab5` 合入主干。用户批准后唯一一次本地检查 PASS；唯一正式调用在 Windows 冻结私钥副本的 NTFS ACL 门禁处以 `invalid_request`、退出码 2 停止，离线复现确认固定 `ssh-keygen -y` 因副本权限退出 255，且失败位于 SSH/SFTP 调用之前。未连接测试服务、未上传、未进入 root 通道、未创建 live 目标或执行 sudo self-test；009 已消费。继续须使用新 ChangeId 完成 Windows 私钥冻结修复、工程门禁和独立用户授权；见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260813-009.md`。

## 3. 商业验收

`G8_COMMERCIAL_ACCEPTED` 尚不具备：生产目标、真实上游费用、真实客户、真实资金、价格/财务批准和真实告警联系人均未获本轮独立授权；设计客户数量、真实集成、真实付费、四周成功率和毛利不得填写虚构值。

## 4. 完成判定

- 工程门禁已全部通过并完成合并：当前结论为“G8 工程就绪，商业观察未完成”。
- 只有另获逐项生产授权并满足四周商业指标，才可报告 `G8_COMMERCIAL_ACCEPTED`。
