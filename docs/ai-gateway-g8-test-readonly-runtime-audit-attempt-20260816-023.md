# G8 023 系统免交互 SSH 只读运行态审计执行记录

## 1. 固定结果

`CONSUMED_SSH_SESSION_FAILED_REMOTE_AUDIT_NOT_PROVEN`

- ChangeId：`CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-023`。
- 授权绑定：工程 merge `1eb23c8b87720cceea64dcfc349b0a9b9c04de4b`，冻结命令大小 32954，SHA-256 `bb48f5b4baf69eb6f563f021f676b97880e9570eaf5327daaffe69aaa32d6fe6`。
- 授权上限：最多 1 个非交互 SSH 会话，由 `pc` 使用既有 Docker 权限执行固定只读审计；任何失败立即停止、零重试。
- 唯一正式入口调用：`1`；PowerShell 启动：`1`；`PRE_SSH_GATE=PASS`：`1`；`SSH_ATTEMPTED=YES`：`1`；固定 SSH 调用：`1`。
- 固定结果：SSH 调用返回非零，低敏结果为 `ssh_session_failed`；父启动器返回 `powershell_session_failed`，进程退出码为 `1`。
- SSH 会话成功：`0`；没有收到固定远端审计结果或 `COLLECTION_PASS`。
- 远端固定脚本与 Docker 只读查询：`UNKNOWN / 最多启动 1 次`。现有证据只证明唯一 SSH 调用已开始且未成功返回，不能反推远端脚本或其中某个查询一定未启动。
- sudo、安装、Docker 变更、宿主写入、业务 HTTP、数据库写入、migration、真实上游和费用动作：`0`；远端重试：`0`。
- 结论：023 已失败关闭并永久消费，禁止再次授权、重试或重放；没有形成可用于关闭测试服运行态验收的证据。

墓碑化后四个普通文件的本地冻结摘要如下，CRLF 计数均为 0：

| 文件 | 大小 | SHA-256 | Git blob |
|---|---:|---|---|
| `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-023.py` | 401 | `ce1cc20d4950ad99ef9d5ab73c74649f0f46a522ceb1de149ef1ad9a4fb5fe32` | `89a7cc213b27c2b8797d8eb6b910d0692760aed4` |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-023-command.py` | 399 | `bcd90bf71d9e4711f1d92708c5e78ad4c4f9c4defc3add91ef092da8100970bc` | `3e0950e36eccccd343dd200688f17aca4b9157d7` |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_023.py` | 2222 | `ae221ac32077d0e8c197259aa90c651b4b3989f60ecd96c7a7463e4c0e5f8c80` | `d126dee1c9d856d7e1e6a21a2b0761366718ecdf` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_023_command.py` | 1831 | `3d69da0ec7bd4fd7135a1f2648c29193d9285e2f889a10aa93f31a84ab918e2e` | `c1ee5271581bbe5c130635b4a4643a8e5a68e30b` |

唯一正式调用的固定低敏输出为：

```text
G8_TEST_READONLY_RUNTIME_AUDIT_023_RUNNER=LOCAL_GATE_PASS
G8_TEST_READONLY_RUNTIME_AUDIT_023_POWERSHELL_ATTEMPTED=YES
G8_TEST_READONLY_ACCESS_023_PRE_SSH_GATE=PASS
G8_TEST_READONLY_ACCESS_023_SSH_ATTEMPTED=YES
G8_TEST_READONLY_ACCESS_023_LOCAL_GATE=FAILED reason=ssh_session_failed
G8_TEST_READONLY_ACCESS_023_HOST_RESULT=FAILED reason=ssh_session_failed exit_code=2
G8_TEST_READONLY_RUNTIME_AUDIT_023_RUNNER=FAILED reason=powershell_session_failed
```

## 2. 证据边界

`SSH_ATTEMPTED=YES` 在唯一 SSH 调用之前形成，因此能够证明控制流到达 SSH；非零会话结果能够证明它没有成功完成。它不能证明远端固定脚本完全没有开始，也不能证明任何容器或 G8 运行态事实。由于没有 `COLLECTION_PASS` 或固定远端结果，测试服运行态保持未知。

本次冻结能力不包含 sudo、安装、Docker 变更、宿主写入、业务 HTTP、数据库写入、migration、真实上游或费用动作；执行在 SSH 会话失败后立即停止，没有重试。

## 3. 永久墓碑

023 固定启动器和生成器已替换为在参数解析、材料读取、子进程启动和联网前固定返回 `change_id_consumed` 的无 import 墓碑。工程 merge、冻结命令及本次授权都不构成后续授权。

若继续诊断 SSH 会话失败或运行态，只能使用新的独立 ChangeId，重新完成离线工程、CI、独立评审、main 合并、冻结摘要复核和用户精确授权。

`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。
