# G8 023 系统免交互 SSH 认证只读运行态审计工程清单

## 1. 当前状态

`CONSUMED_SSH_SESSION_FAILED_REMOTE_AUDIT_NOT_PROVEN`

023 使用独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-023`。022 因固定客户端私钥、公钥和指纹配对门禁返回 `identity_pair_failed`，且已永久消费并墓碑化，不得再次授权、重试或重放。

023 取消固定客户端私钥路径、对应公钥文件、客户端指纹和 `IdentitiesOnly` 门禁，改用开发机现有的免交互 SSH 认证链。工程 HEAD `9a969d4dd2881e659c50ab694a4d35b57adba803` 经 PR #404、CI run `31892659673 completed/success` 和代码安全、QA、产品/规格三项独立零缺陷评审后，以 merge commit `1eb23c8b87720cceea64dcfc349b0a9b9c04de4b` 合入 main；父提交顺序为 `0db6d060f4b3763c39f13a030fb7bec2485b546b` 后 `9a969d4dd2881e659c50ab694a4d35b57adba803`，远端工程分支已删除。

用户精确授权后的唯一正式调用形成 `PRE_SSH_GATE=PASS` 与 `SSH_ATTEMPTED=YES`，随后 SSH 会话非零并固定返回 `ssh_session_failed`，零重试停止。SSH 调用 `1`、会话成功 `0`，没有固定远端审计结果或 `COLLECTION_PASS`；远端固定脚本与 Docker 只读查询为 `UNKNOWN / 最多启动 1 次`。sudo、安装、Docker 变更、宿主写入、业务 HTTP、数据库写入、migration、真实上游和费用动作均为 0。023 已永久消费并墓碑化，不得再次授权、重试或重放；`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。执行证据见 `docs/ai-gateway-g8-test-readonly-runtime-audit-attempt-20260816-023.md`。

## 2. 固定启动入口

历史工程中的 `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-023.py` 曾是唯一正式入口。023 现已消费，当前启动器与生成器均在参数解析、材料读取、子进程启动和联网前固定返回 `change_id_consumed`。

以下形式仅用于记录已经失效的历史授权对象，不得再次执行：

```text
python -I infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-023.py --change-id=CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-023 --engineering-merge=<40位工程merge> --expected-command-size=32954 --expected-command-sha256=bb48f5b4baf69eb6f563f021f676b97880e9570eaf5327daaffe69aaa32d6fe6 --execute-authorized
```

当前禁止执行上述正式形式。历史启动器曾验证双父 merge，并逐字节核对 merge 与第二父中的启动器、生成器和审计源；当前无 import 墓碑在参数解析和 PowerShell 前固定拒绝，零重试。

## 3. 认证与能力边界

- 只允许固定目标 `pc@8.130.9.163:10003`，禁止调用方覆盖用户、主机或端口。
- 使用开发机现有 OpenSSH 默认免交互认证链；不指定 `-i`，不启用 `IdentitiesOnly=yes`，不读取或校验固定客户端私钥、公钥和客户端指纹。
- 保留 `-F none`、`BatchMode=yes`、`PasswordAuthentication=no`、`KbdInteractiveAuthentication=no`、`NumberOfPasswordPrompts=0` 和 `ConnectionAttempts=1`，禁止代理转发、端口转发、本地命令和 TTY；任何认证失败立即停止且零重试。
- 继续使用冻结 `known_hosts` 与固定 ED25519 服务器 host key 校验；不得降低 `StrictHostKeyChecking=yes` 和 `LogLevel=QUIET`。
- `STARTED`、`PRE_SSH_GATE=PASS` 与 `SSH_ATTEMPTED=YES` 均须先完成低敏耐久回执的 WriteLine、Flush 与 Flush(true)，再输出阶段标志或调用唯一 SSH。
- 最多一次非交互 SSH，由 `pc` 使用既有 Docker 权限执行固定无参数内存审计；不安装、不使用 sudo、不申请 TTY。
- Docker 权限接近宿主 root；冻结远端脚本反向禁止容器变更、镜像/网络/卷操作、宿主写入、migration、DDL/DML、队列消费、业务请求、真实上游和费用动作。
- `COLLECTION_PASS` 只表示低敏证据采集完整，不表示运行态验收通过或软件闭环完成。

## 4. TDD 与离线门禁

- Windows PowerShell 5.1 使用假 SSH 动态证明：系统认证链参数不含 `-i`/`IdentitiesOnly`，固定目标、host key、BatchMode、单会话和零重试保持不变。
- 测试必须反向拒绝固定客户端身份路径、`.pub` 配对、客户端指纹和私钥导出命令；不得读取真实 SSH 身份材料。
- 回执目录、权限、预占、Writer/WriteLine、Flush、Flush(true)、Null ActionPreference、父窗口保活与固定低敏输出回归继续通过。
- Linux CI 先获取固定 digest `python@sha256:62eafe52c91cad83c2c74e630bfde917da8c253673e695665d454def84fc9a13`，随后所有测试容器均以 `--pull=never --network none`、仓库只读挂载运行 023 生成器、启动器、授权契约及 015 至 022 历史墓碑。
- Windows 与 Linux 同时运行 py_compile、Bash 语法、差异格式、敏感信息和 CI workflow 契约门禁。

## 5. 当前墓碑与历史冻结候选

当前四个普通入口与测试均为墓碑，换行均为 LF、CRLF=0：

| 文件 | 大小 | SHA-256 | Git blob | 状态 |
|---|---:|---|---|---|
| `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-023.py` | 401 | `ce1cc20d4950ad99ef9d5ab73c74649f0f46a522ceb1de149ef1ad9a4fb5fe32` | `89a7cc213b27c2b8797d8eb6b910d0692760aed4` | 当前消费墓碑 |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-023-command.py` | 399 | `bcd90bf71d9e4711f1d92708c5e78ad4c4f9c4defc3add91ef092da8100970bc` | `3e0950e36eccccd343dd200688f17aca4b9157d7` | 当前消费墓碑 |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_023.py` | 2222 | `ae221ac32077d0e8c197259aa90c651b4b3989f60ecd96c7a7463e4c0e5f8c80` | `d126dee1c9d856d7e1e6a21a2b0761366718ecdf` | 当前墓碑测试 |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_023_command.py` | 1831 | `3d69da0ec7bd4fd7135a1f2648c29193d9285e2f889a10aa93f31a84ab918e2e` | `c1ee5271581bbe5c130635b4a4643a8e5a68e30b` | 当前墓碑测试 |

以下摘要来自 merge commit `1eb23c8b87720cceea64dcfc349b0a9b9c04de4b` 的原始 Git blob 及纯内存重建，换行均为 LF、CRLF=0：

| 文件/生成物 | 大小 | SHA-256 | Git blob | 状态 |
|---|---:|---|---|---|
| `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-023.py` | 13228 | `4e0b03a0579573b0496ff2a5233cef804457b60ea5173d888ada77738270b473` | `5ce32975cd5188a70a5f4f9f81c4a6b3e5db40fc` | 合并后 main 原始 blob |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-023-command.py` | 29581 | `c8235e147c0757b2bea7e0807efa5e6fa2c5ccb08b23b63230329acfa038871e` | `a2621cedec31a0f5b6a5077a91ee00c5e340bbff` | 合并后 main 原始 blob |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_023.py` | 6079 | `c143ffe0308029228f57064b6f91046c53b3676d768d2019a37965598299f207` | `4d541cd40a238c05e8d6243a63fbcb30ef42be53` | 合并后 main 原始 blob |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_023_command.py` | 34397 | `f188b0edbcc73b2cf8a2c6be1ee39241eb5a5d26a2201dc853b2ff3da0d7ab18` | `1f559e2d3bcfaa310801d9b7db630f070d505239` | 合并后 main 原始 blob |
| `infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh` | 18377 | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | `27450efc39af7e763ea8df0c59d584433d5e5edd` | 合并后 main 原始 blob |
| 纯内存冻结命令 | 32954 | `bb48f5b4baf69eb6f563f021f676b97880e9570eaf5327daaffe69aaa32d6fe6` | 不适用 | 由原始 blob 重建且不落盘 |

## 6. 合并与停止条件

023 已完成 Windows/Linux 断网本地门禁、敏感信息扫描、精确 HEAD 的全部适用 CI，以及代码安全、QA、产品/规格独立评审；P0/P1 均为 0。工程候选已以 merge commit 合入 main并删除远端功能分支，执行文件、测试、审计源、换行和纯内存命令摘要已从 main 原始 Git blob 重新核对一致。

023 已失败关闭消费；历史工程 merge、冻结命令和本次授权均不构成后续授权。继续只能使用新的独立 ChangeId，重新完成工程、离线门禁、PR、CI、独立评审、main 合并、冻结摘要复核与新的用户精确授权。
