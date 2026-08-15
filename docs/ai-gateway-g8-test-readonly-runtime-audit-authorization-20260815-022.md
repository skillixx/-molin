# G8 022 耐久回执修复与无安装只读运行态审计工程清单

## 1. 当前状态

`PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`

022 使用独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022`。021 的唯一授权尝试在 Windows PowerShell 5.1 启动后、SSH 前固定返回 `receipt_unavailable`；021 已永久消费并墓碑化，不得再次授权、重试或重放。

本地离线复现确认：021 普通 Python 字符串把回执目录校验正则末尾的两个反斜杠折叠为一个，生成的 PowerShell 正则无效，异常被旧实现统一映射为 `receipt_unavailable`。022 删除该正则，改用 Windows SpecialFolder、盘符根、固定盘和逐级非 reparse 校验，并把目录、预占、写入与刷盘故障分别收敛为固定低敏原因。

022 尚未执行、尚未消费。工程实现、测试、CI、评审和合并均不授权 SSH、测试服 Docker、HTTP、数据库、sudo、安装、宿主写入、migration、业务请求、真实上游或费用动作；`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。

## 2. 固定启动入口

仓库内 `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py` 是唯一正式入口。它只接受精确 ChangeId、工程 merge、冻结命令大小与 SHA-256 以及显式 `--execute-authorized`；默认模式在读取工程材料和启动子进程前失败关闭。

工程合并和摘要复核完成后，正式形式只能由后续新的独立用户授权启用：

```text
python -I infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py --change-id=CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022 --engineering-merge=<40位工程merge> --expected-command-size=34027 --expected-command-sha256=d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9 --execute-authorized
```

当前禁止执行上述正式形式。启动器必须验证双父 merge，并逐字节核对 merge 与第二父中的启动器、生成器和审计源；任一材料或摘要漂移均在 PowerShell 前固定失败，零重试。

## 3. 耐久回执与能力边界

- 正式回执目录由 `Environment.SpecialFolder.LocalApplicationData` 获取，不信任调用方的 `LOCALAPPDATA`、相对路径、UNC 或设备路径；目录必须位于固定盘，且目录到根的每一级都不得是 reparse point。
- 回执名称固定前缀 `.g8-022-runtime-audit-` 加进程内新 GUID，使用 `FileMode.CreateNew`，不覆盖、不删除既有文件。预占失败固定为 `receipt_preoccupied`。
- 创建目录或权限失败固定为 `receipt_directory_unavailable`；WriteLine/Writer 失败固定为 `receipt_write_failed`；Flush/Flush(true) 失败固定为 `receipt_flush_failed`。不得输出真实路径、异常或凭据。
- `STARTED` 必须完成 WriteLine、Flush 与 Flush(true) 后才可继续本地材料门禁；`PRE_SSH_GATE=PASS` 必须先耐久落盘，随后才允许记录唯一 `SSH_ATTEMPTED=YES` 并调用一次 SSH。
- 最多一次非交互 SSH，由 `pc` 使用既有 Docker 权限执行固定无参数内存审计；不安装、不使用 sudo、不申请 TTY。
- 保留 `BatchMode=yes`、`ConnectionAttempts=1`、固定 known_hosts、固定 ED25519 身份、空口令密钥配对、禁止代理/转发/本地命令和 `LogLevel=QUIET`。
- Docker 权限接近宿主 root；冻结远端脚本反向禁止容器变更、镜像/网络/卷操作、宿主写入、migration、DDL/DML、队列消费、业务请求、真实上游和费用动作。
- `COLLECTION_PASS` 只表示低敏证据采集完整，不表示运行态验收通过或软件闭环完成。

## 4. TDD 与离线门禁

- Windows PowerShell 5.1 动态覆盖可信 LocalAppData、伪造环境变量、目录/权限失败、预占不覆盖、Writer/WriteLine、Flush、Flush(true)、Null ActionPreference、父窗口保活与固定低敏输出。
- 本地假 SSH 必须只调用一次，回执中 `SSH_ATTEMPTED` 恰好一次；失败后不得重试。
- Linux cached `python:3.13-bookworm` 以 `--pull=never --network none`、仓库只读挂载运行生成器、启动器、授权契约和 015 至 021 历史墓碑；不得读取真实 SSH 身份或建立网络连接。
- Windows 与 Linux 同时运行 py_compile、Bash 语法、差异格式、敏感信息和 CI workflow 契约门禁。

## 5. 冻结工程候选

以下摘要来自当前普通文件及纯内存生成物，换行均为 LF、CRLF=0：

| 文件/生成物 | 大小 | SHA-256 | 状态 |
|---|---:|---|---|
| `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py` | 13256 | `3f9d4dfbb283a4275556d6c3949bbfd790dd06eeaf2c9b88ece0e0db29e2f65f` | 当前候选 |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py` | 30098 | `4ecf224f848f6597c59db705f122c5e4ffe8593ac48395451f2e11e8973fba00` | 当前候选 |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py` | 6079 | `12b6855db5a186d821376d961ad0210c567e17a239221d6637c653a557c4f6d1` | 当前候选 |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py` | 33218 | `b043ec28f936e7cc700982291f481e8d529a6dc3cfae2d3998157279dd70ab12` | 当前候选 |
| 纯内存冻结命令 | 34027 | `d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9` | 不落盘 |

固定审计源仍为 `infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh`：18377 字节，SHA-256 `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256`，022 不修改该文件。

## 6. 合并与停止条件

022 必须完成 Windows/Linux 断网本地门禁、敏感信息扫描、精确 HEAD 的全部适用 CI，以及代码安全、QA、产品/规格独立评审；P0/P1 必须为 0。满足后以 merge commit 合入 main并删除远端功能分支，再从 main 原始 Git blob重新核对执行文件、测试、审计源、换行和纯内存命令摘要。

合并及摘要复核后必须停止并保持 `REMOTE_NOT_AUTHORIZED`。只有用户对精确 ChangeId、工程 merge、命令大小、命令 SHA-256、最多一次非交互 SSH 和零重试另行作出独立授权，才允许调用固定正式入口。
