# G8 021 固定启动器无安装只读运行态审计执行记录

## 1. 固定结果

`CONSUMED_LOCAL_RECEIPT_UNAVAILABLE_SSH_NOT_STARTED`

- ChangeId：`CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-021`。
- 用户授权：绑定工程 merge `8bc05cbf3bc71a8954087dc7f26732f836e5212e`、冻结命令大小 32009 与 SHA-256 `8407837bc7e9af65dc7d2fe8ad1f8a9728186745ad25d20e802c8793a9740dcd`；允许最多 1 个非交互 SSH，由 `pc` 使用既有 Docker 权限执行固定只读运行态审计；任何失败立即停止、零重试。
- 执行基线：工程 PR #398 的 CI run `31867790659` 成功后以 merge commit `8bc05cbf3bc71a8954087dc7f26732f836e5212e` 合入 main；合并后归档 PR #399 以 merge commit `2779608bbd33dec778363ec59df4b4497e5080c5` 合入 main。
- 本地不可变门禁：启动器、生成器和固定审计源的普通文件与工程 merge 原始对象逐字节一致；三者大小、SHA-256 与 CRLF=0 均匹配冻结清单。
- 唯一正式调用：固定启动器输出 `LOCAL_GATE_PASS` 和 `POWERSHELL_ATTEMPTED=YES`，随后 Windows PowerShell 5.1 固定返回 `HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2`，父启动器固定返回 `FAILED reason=powershell_session_failed`；进程非零结束。
- 耐久回执：不可用；未形成 `STARTED`，也未出现 `PRE_SSH_GATE=PASS` 或 `SSH_ATTEMPTED=YES`。失败发生在允许进入 SSH 调用的固定门禁之前。
- SSH 与远端命令：`0 / 0`；测试服 Docker、HTTP、数据库查询、sudo、安装、Docker 变更、宿主写入、migration、业务请求、真实上游、费用全部为 `0`。
- 重试：`0`；未为诊断回执失败而再次调用启动器、PowerShell、SSH 或任何服务器命令。
- 结论：021 已按失败关闭规则消费，禁止再次授权、重试或重放；该结果没有形成新的测试服运行态证据。

墓碑化后四个普通文件的本地冻结摘要如下，CRLF 计数均为 0：

| 文件 | 大小 | SHA-256 | Git blob |
|---|---:|---|---|
| `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py` | 433 | `db897b1849edd3e5b9af05794fa8520c2efeb03f3a8462240cdb57a66495ea7d` | `e02e97703c2b74e29c38e0150a5833734393c974` |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-021-command.py` | 422 | `b5f43b69906b3808f0531e8b796841f53ebcc5df00d8c9a5ba95a1442ab90ca2` | `e6632016528e2457b4b957507ca01c68e1c63eec` |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py` | 2222 | `d846651fedf420526a332fc6b736f32c241b1956d18178e61d2663cc7a5d6b16` | `d3f0f41f1e8e5d0293a5c38b8dca680791e954a9` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py` | 1878 | `91317a191e79872f30a2e69b0c9bd864a7c134b89ec6cd494988a314e8fb5e10` | `cdbff3ac27174a30885745112da9811c9dac2258` |

## 2. 证据边界

固定 PowerShell 脚本只有在耐久写入 `PRE_SSH_GATE=PASS` 后才允许写入 `SSH_ATTEMPTED=YES` 并调用唯一 SSH。本次在首次耐久回执阶段即固定失败，标准输出中不存在这两个标志，因此控制流未到达 SSH 调用；该时序证据直接证明 SSH 和远端能力均为 0，不依赖进程观察反推。

本轮授权要求任何失败立即停止、零重试，且不授权其他服务器命令。因此不得为判断回执路径状态、确认连接或补采运行态而建立第二个 SSH，也不得直接执行 Docker、HTTP、数据库、sudo、安装、服务或业务命令。

## 3. 失败关闭与后续门禁

021 固定启动器和生成器已替换为在参数解析、材料读取、子进程启动和联网前固定返回 `change_id_consumed` 的无 import 墓碑入口。021 不得再次授权、重试或重放；历史工程 merge、冻结命令和本次授权都不构成后续授权。

若继续，只能使用新的独立 ChangeId，并重新完成工程设计、双平台离线门禁、CI、独立评审、main 合并、原始 blob 复核和新的用户精确授权。新候选必须在不连接测试服的本地测试中覆盖耐久回执已存在、不可创建和不可刷盘等失败，并继续保持单 SSH、零重试、无安装、无 sudo 与固定只读 Docker 能力边界。

`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。
