# G8 022 耐久回执无安装只读运行态审计执行记录

## 1. 固定结果

`CONSUMED_LOCAL_IDENTITY_PAIR_FAILED_SSH_NOT_STARTED`

- ChangeId：`CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022`。
- 用户授权：绑定工程 merge `84ae5b0ad87958ee63fbfa709c4f164baca39a1b`、冻结命令大小 34027 与 SHA-256 `d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9`；允许最多 1 个非交互 SSH，由 `pc` 使用既有 Docker 权限执行固定只读运行态审计；任何失败立即停止、零重试。
- 执行基线：工程 PR #401 的 CI run `31884793587 completed/success` 后以 merge commit `84ae5b0ad87958ee63fbfa709c4f164baca39a1b` 合入 main；合并后归档 PR #402 以 merge commit `79fddaa44fc728758b9897cd6e7d0a09a13f7d44` 合入 main。
- 首次授权调用：固定启动器输出 `LOCAL_GATE_PASS` 与 `POWERSHELL_ATTEMPTED=YES`；Windows PowerShell 5.1 随后固定输出 `HOST_RESULT=FAILED reason=identity_pair_failed exit_code=2`，父启动器返回 `FAILED reason=powershell_session_failed`，进程退出码为 2。
- 首次耐久回执：形成 `STARTED`、`LOCAL_GATE=FAILED reason=identity_pair_failed` 与失败 `HOST_RESULT`；没有 `PRE_SSH_GATE=PASS` 或 `SSH_ATTEMPTED=YES`。
- 流程偏差：消费归档的 TDD RED 用例在旧入口尚未墓碑化时传入历史完整授权参数，错误触发一次本地正式入口与 PowerShell 重放。该重放同样固定返回 `identity_pair_failed`、退出码 2，形成第二份同内容非空回执；未形成 `PRE_SSH_GATE` 或 `SSH_ATTEMPTED`。这次重放没有用户授权，违反零重试/零重放流程，不能计作合法验收或诊断证据。
- 本地正式入口与 PowerShell 总调用数：`2 / 2`；其中授权调用 `1`，未授权本地重放：`1`。
- 耐久证据：两份非空耐久回执均只记录 `STARTED`、`identity_pair_failed` 与失败 `HOST_RESULT`；两份均无 `PRE_SSH_GATE`、`SSH_ATTEMPTED`。
- SSH 与远端命令：`0 / 0`；测试服 Docker、HTTP、数据库查询/写入、sudo、安装、Docker 变更、宿主写入、migration、业务请求、真实上游、费用全部为 `0`。
- 远端重试：`0`；未建立 SSH，因此没有测试服命令重试。发现本地重放后立即墓碑化两个普通入口，未再调用历史执行能力。
- 结论：022 已按失败关闭规则永久消费，禁止再次授权、重试或重放；没有形成新的测试服运行态证据。

墓碑化后四个普通文件的本地冻结摘要如下，CRLF 计数均为 0：

| 文件 | 大小 | SHA-256 | Git blob |
|---|---:|---|---|
| `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py` | 421 | `75d57053fbf2c9cf60df0599fefe4750d5803dac442dee9df74f6cba9ceb659b` | `908969597ac07273d8ab312f717abd0a035fc19b` |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py` | 399 | `2d63b0e7a3898e144e70e2d4274c8bf612751526aeb12978dc3162d395f788bb` | `b8f2ae4e450727d455b4d90a85ab2a79ef76b8ba` |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py` | 2222 | `5d49bdcd6ee04c11e5347ca8357dddcf451f791e11d7b01848f0df2a96ca9be4` | `4da204189296744750fc5f83514855f9b1b03704` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py` | 1878 | `2b395e0e6a5c83acf3ab0f2c4e6dd06d77f4563c8b5af246cc2b179ab32da798` | `999515e9b05f234598469ef80bb9cf59f64311c1` |

## 2. 证据边界

历史 PowerShell 脚本只有在耐久写入 `PRE_SSH_GATE=PASS` 后才允许耐久记录 `SSH_ATTEMPTED=YES` 并调用唯一 SSH。两次本地调用均只有 `STARTED`，随后在身份配对门禁失败；标准输出和两份非空回执都不存在上述两个阶段标志。因此控制流未到达 SSH 调用，SSH 和远端能力均为 0，该结论不依赖进程观察反推。

本地重放属于流程违规，虽然没有越过本地门禁或触达远端，也必须与首次授权调用分开记录。它不能扩张授权次数、不能作为重试成功证据，也不能被删除或淡化。

## 3. 失败关闭与后续门禁

022 固定启动器和生成器已替换为在参数解析、材料读取、子进程启动和联网前固定返回 `change_id_consumed` 的无 import 墓碑入口。022 不得再次授权、重试或重放；历史工程 merge、冻结命令和本次授权都不构成后续授权。

若继续，只能使用新的独立 ChangeId，并重新完成工程设计、离线身份配对夹具、双平台门禁、CI、独立评审、main 合并、原始 blob 复核和新的用户精确授权。任何新候选仍须保持单 SSH、零重试、无安装、无 sudo 与固定只读 Docker 能力边界。

`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。
