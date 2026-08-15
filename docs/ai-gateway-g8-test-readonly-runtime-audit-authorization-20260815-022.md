# G8 022 耐久回执修复与无安装只读运行态审计工程清单

## 1. 当前状态

`CONSUMED_LOCAL_IDENTITY_PAIR_FAILED_SSH_NOT_STARTED`

022 使用独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022`。021 已永久消费，不得再次授权、重试或重放；022 历史工程候选只复用其固定无安装、无 sudo、Docker 只读审计能力，并修复本地耐久回执缺口。

022 工程 HEAD `fc0344283813bd873aa70520e0b8fcd1da424500` 已通过 PR #401、CI run `31884793587 completed/success` 及代码安全、QA、产品/规格三项独立零缺陷评审，以 merge commit `84ae5b0ad87958ee63fbfa709c4f164baca39a1b` 合入 main；父提交顺序为 `dc035aec34903bbaf2a991cd64c6109db52fbdeb` 后 `fc0344283813bd873aa70520e0b8fcd1da424500`，远端工程分支已删除。

用户随后绑定上述工程 merge、34027 字节冻结命令及 SHA-256 `d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9`，授权唯一一次固定调用。该调用在 PowerShell 启动后固定返回 `identity_pair_failed`；耐久回执形成 `STARTED`，但未形成 `PRE_SSH_GATE` 或 `SSH_ATTEMPTED`，因此 SSH 与全部远端能力为 0。

消费归档的 TDD RED 编写过程中，历史完整参数在入口墓碑化前被错误用于公开 CLI 用例，导致一次不应发生的本地重放；它同样在 `identity_pair_failed` 停止，未触达 SSH。该流程偏差如实记录为本地正式入口与 PowerShell 总调用数 `2 / 2`、未授权本地重放 `1`、SSH/远端 `0 / 0`。发现后两个普通入口立即墓碑化，后续测试只验证固定消费状态。完整证据见 `docs/ai-gateway-g8-test-readonly-runtime-audit-attempt-20260815-022.md`。

022 已永久消费，不得再次授权、重试或重放；`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。

## 2. 历史固定启动入口（已失效）

历史 `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py` 曾把工程 merge 原始 blob 复核、冻结命令内存生成、大小和 SHA-256 校验、PowerShell 5.1 语法解析及启动收敛为一次固定调用。

下列形式仅保存执行前的冻结边界，当前两个普通入口均已墓碑化，禁止执行、恢复或据此重新授权：

```text
python -I infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py --change-id=CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022 --engineering-merge=84ae5b0ad87958ee63fbfa709c4f164baca39a1b --expected-command-size=34027 --expected-command-sha256=d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9 --execute-authorized
```

当前禁止执行上述历史形式。固定入口已在参数、材料和子进程之前返回 `change_id_consumed`；任何恢复都必须使用新的独立 ChangeId 重新完成工程流程。

## 3. 历史能力与回执边界

- 历史候选使用可信 `LocalApplicationData`、固定盘、逐级非 reparse 与唯一 GUID `CreateNew` 回执；不信任调用方路径，不覆盖或删除既有文件。
- `STARTED`、`PRE_SSH_GATE`、`SSH_ATTEMPTED` 都必须先完成 WriteLine、Flush 与 Flush(true) 耐久写入，再输出对应阶段并继续。
- 最多一次非交互 SSH，由 `pc` 使用既有 Docker 权限执行固定无参数内存审计；不安装、不使用 sudo、不申请 TTY。
- 保留 `BatchMode=yes`、`ConnectionAttempts=1`、固定 known_hosts、固定 ED25519 身份、空口令密钥配对、禁止代理/转发/本地命令和 `LogLevel=QUIET`。
- Docker 权限接近宿主 root；冻结脚本反向禁止容器变更、镜像/网络/卷操作、宿主写入、migration、DDL/DML、队列消费、业务请求、真实上游和费用动作。
- `COLLECTION_PASS` 只表示低敏证据采集完整，不表示运行态验收通过或软件闭环完成。

## 4. 当前墓碑与离线门禁

- 两个 022 普通入口均无 import，不解析参数、不读取 Git/身份材料、不启动 PowerShell/SSH/Docker，也不建立网络连接。
- 默认、自检与历史完整参数调用都固定输出 `reason=change_id_consumed` 并退出 2。
- Windows 与 Linux `--pull=never --network none`、仓库只读挂载必须同时运行 015 至 022 墓碑、授权契约、py_compile、差异格式和敏感信息门禁。
- 历史工程 merge 原始 blob 与冻结命令仍须可独立复算，但不得导入或恢复当前普通入口的执行能力。

## 5. 历史冻结工程候选

以下摘要来自工程 merge 原始 Git blob 和当时的纯内存生成物，换行均为 LF、CRLF=0；当前普通文件已按执行结果墓碑化，摘要见执行记录：

| 文件/生成物 | 大小 | SHA-256 | Git blob | 状态 |
|---|---:|---|---|---|
| `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-022.py` | 13256 | `3f9d4dfbb283a4275556d6c3949bbfd790dd06eeaf2c9b88ece0e0db29e2f65f` | `0a3e88fd1830cf2a1da328b9dc342d28bc125c67` | 工程 merge 原始 blob |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-022-command.py` | 30098 | `4ecf224f848f6597c59db705f122c5e4ffe8593ac48395451f2e11e8973fba00` | `931947ed15128004b80fd16cde04fb3d4e8921b4` | 工程 merge 原始 blob |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_022.py` | 6079 | `12b6855db5a186d821376d961ad0210c567e17a239221d6637c653a557c4f6d1` | `1025f1722d80f6d6dc0956e9da2b2e25f66625aa` | 工程 merge 原始 blob |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_022_command.py` | 33218 | `b043ec28f936e7cc700982291f481e8d529a6dc3cfae2d3998157279dd70ab12` | `feb27b095a6c9cf787bdf05e92fb37d2cb2a8a27` | 工程 merge 原始 blob |
| 纯内存冻结命令 | 34027 | `d649d2f896a224f3c1063b4bbb49953de1a7330d36b1db0cbaaf2bbfdea2e9e9` | 不落盘 | 工程冻结 |

固定审计源仍为 `infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh`：18377 字节，SHA-256 `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256`，Git blob `27450efc39af7e763ea8df0c59d584433d5e5edd`，022 不修改该文件。

## 6. 停止条件

022 的授权调用和一次不应发生的本地重放均已在 SSH 前失败关闭。022 不得再次授权、调用、重试或重放；历史授权、工程 merge 和冻结摘要都不构成后续执行授权。

若继续，只能使用新的独立 ChangeId，并先以完全离线夹具诊断和修复身份配对门禁，再重新完成双平台门禁、CI、独立评审、main 合并、原始 blob 复核与新的用户精确授权。
