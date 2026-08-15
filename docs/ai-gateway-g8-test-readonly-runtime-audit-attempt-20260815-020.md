# G8 020 无安装 Docker 只读运行态审计执行记录

## 1. 固定结果

`CONSUMED_LOCAL_WRAPPER_PARSE_FAILED_SSH_NOT_STARTED`

- ChangeId：`CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-020`。
- 用户授权：绑定工程 merge `3c63539279a34ae2365fc9d7e26e207dd728c4ba` 和冻结命令 SHA-256 `31c1eaaf6f3916dbabb51447a63d263ac4f73509bb8e535451df28db4e024a3d`；允许最多 1 个非交互 SSH，由 `pc` 使用既有 Docker 权限执行固定只读容器与 G8 运行态审计；任何失败立即停止、零重试。
- 执行基线：工程 PR #394 的 CI run `31861762018` 成功后以 merge commit `3c63539279a34ae2365fc9d7e26e207dd728c4ba` 合入 main；合并后归档 PR #396 以 merge commit `8405f8e709854f30031949f96c459d62a534ac7a` 合入 main。
- 本地冻结门禁：审计源 blob `27450efc39af7e763ea8df0c59d584433d5e5edd`、生成器 blob `212124e085c2f34adf11eae62b0e0119c5d8f44e` 与工程 merge 精确一致；生成命令为 32009 字节，SHA-256 精确匹配授权值，SSH 目标 1，sudo、`docker run`、`Get-FileHash` 与父 PowerShell `exit 2` 均为 0。
- 唯一执行尝试：本地外层 PowerShell 包装在解析 `$powershell=Join-Path ...` 时因缺少右括号返回 `ParserError / Unexpected token`；整个包装脚本在语法分析阶段停止，未调用 Windows PowerShell 5.1，更未调用冻结命令或 `ssh.exe`。
- 耐久回执：不存在；固定 `STARTED`/`PRE_SSH_GATE`/`SSH_ATTEMPTED`/`HOST_RESULT` 标志均为 0，与正式脚本未启动一致。
- SSH 与远端命令：`0 / 0`；失败后本地只读观察为 0 个活动 `ssh` 进程。此结论由外层解析失败时序直接证明，不依赖进程观察反推。
- Docker/HTTP/数据库查询、sudo、安装、Docker 变更、宿主写入、migration、业务请求、真实上游、费用：全部为 `0`。
- 重试：`0`；020 按失败关闭规则消费，禁止修正外层包装后重试或重放。

本流程生成的本地 `g8-020-authorized-command.ps1` 已在大小和 SHA-256 复核后原样改名为 `g8-020-authorized-command.consumed-do-not-run.txt`；改名前后均为 32009 字节、SHA-256 `31c1eaaf6f3916dbabb51447a63d263ac4f73509bb8e535451df28db4e024a3d`。该文件不得执行、改回 `.ps1` 或用于任何形式的重放。

墓碑化后两个普通文件的本地冻结摘要如下，CRLF 计数均为 0：

| 文件 | 大小 | SHA-256 | Git blob |
|---|---:|---|---|
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py` | 425 | `57acdab38d9eb9fe9adaa34541c8024bd6b70fc2e36f4214a79eeb50b59e405f` | `a020e485a8f272848ce612aaafdeea6431d27c54` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py` | 1878 | `9ca44161de7a6b013ddbb374bb1ca074fb86db9b163cf789fcae67a53dfbf5ca` | `08f4929ee5aff691c5b184d0b6d7a87197f1e62a` |

## 2. 证据边界

外层 PowerShell 命令在执行前必须完成整段语法解析。本次 `ParserError` 位于赋值并调用 Windows PowerShell 的表达式中，所以没有子 PowerShell、冻结命令、SSH 或远端能力到达的控制流路径。回执不存在与固定标志为 0 是补强证据，不是对远端状态的推测。

本轮授权要求任何失败立即停止、零重试，且不授权其他服务器命令。因此不得为确认运行态而建立第二个 SSH，也不得直接执行 Docker、HTTP、数据库、sudo、安装、服务或业务命令。测试服 G8 运行态仍未取得新证据。

## 3. 失败关闭与后续门禁

020 生成器已替换为在参数解析、材料读取和联网前固定返回 `change_id_consumed` 的无 import 墓碑入口。020 不得再次授权、重试或重放；历史冻结命令、改名后的本地证据文件和工程 merge 都不构成后续授权。

若继续，只能使用新的独立 ChangeId。新候选必须把“复核、生成、启动”收敛为一个经 Windows PowerShell 5.1 完整动态测试的固定本地入口，禁止人工或临时外层包装；并重新完成工程门禁、CI、独立评审、main 合并、原始 blob 复核和新的用户精确授权。

`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。
