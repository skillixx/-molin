# G8 021 固定启动器无安装只读运行态审计工程清单

## 1. 当前状态

`PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`

021 使用独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-021`。020 已永久消费，不得再次授权、重试或重放；021 只复用 020 已审计的固定无安装、无 sudo、Docker 只读审计能力，并修复仓库外临时 PowerShell 包装导致的启动链路缺口。

021 尚未执行、尚未消费。工程实现、离线测试、CI、评审或合并均不授权 SSH、测试服 Docker、HTTP、数据库、sudo、安装、migration、业务请求、真实上游或费用动作；`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。

## 2. 固定启动入口

仓库内 `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py` 是唯一正式入口。它把工程 merge 原始 blob 复核、冻结命令内存生成、大小和 SHA-256 校验、PowerShell 5.1 语法解析及启动收敛为一次固定调用，不创建可重放的 `.ps1` 文件，也不接受脚本路径、SSH 路径、回执路径或远端参数覆盖。

未来若获得新的独立精确执行授权，只允许把已归档的工程 merge、命令大小和命令摘要代入以下单次入口；`--execute-authorized` 只表示操作者正在使用另行取得的授权，不能由工程合并或 CI 自动满足：

```text
python -I infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py --change-id=CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-021 --engineering-merge=8bc05cbf3bc71a8954087dc7f26732f836e5212e --expected-command-size=32009 --expected-command-sha256=8407837bc7e9af65dc7d2fe8ad1f8a9728186745ad25d20e802c8793a9740dcd --execute-authorized
```

当前禁止执行上述正式形式。固定入口缺少任一参数、工程 blob 漂移、命令摘要漂移、PowerShell 启动失败或子脚本失败时必须输出固定低敏原因并立即停止，零重试。

## 3. 能力和输出边界

- 最多一次非交互 SSH，由 `pc` 使用既有 Docker 权限执行固定无参数内存审计；不安装、不使用 sudo、不申请 TTY。
- 继续保留 `BatchMode=yes`、`ConnectionAttempts=1`、固定 known_hosts、固定 ED25519 身份、空口令密钥配对、禁止代理/转发/本地命令和 `LogLevel=QUIET`。
- Docker 权限接近宿主 root；冻结远端脚本反向禁止容器变更、镜像/网络/卷操作、宿主写入、migration、DDL/DML、队列消费、业务请求、真实上游和费用动作。
- 固定启动器只转发冻结必需键、`AUDIT_COMPLETE=true` 与 021 固定阶段标志；stderr、原始异常、真实材料路径、指纹和凭据不得输出。
- `COLLECTION_PASS` 只证明证据采集完整，不证明运行态符合验收，更不证明软件闭环完成。

## 4. TDD 与离线门禁

- Windows PowerShell 5.1 动态验证固定外层表达式：无效脚本解析失败时假 SSH 为 0；成功测试脚本只调用一次假 SSH并留下唯一 `SSH_ATTEMPTED` 回执。
- 生成器继续覆盖 Null/非法 ActionPreference、回执写入和耐久刷盘故障、父窗口保活、known_hosts、密钥配对、单 SSH、必需键完整性及低敏失败关闭。
- Linux `--network none`、仓库只读挂载运行生成器、固定启动器和授权契约；不得读取真实 SSH 身份或建立网络连接。
- 015 至 020 历史墓碑继续回归，021 完成唯一授权尝试后也必须按结果永久墓碑化。

## 5. 冻结工程候选

以下摘要来自当前普通文件和纯内存生成物，换行均为 LF、CRLF=0：

| 文件/生成物 | 大小 | SHA-256 | 状态 |
|---|---:|---|---|
| `infra/scripts/run-ai-gateway-g8-test-readonly-runtime-audit-021.py` | 13157 | `092ebfa2453552a46eda55e91c3db2777e28bb87dcfc191156f7690e472d348f` | main 原始 blob |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-021-command.py` | 27486 | `d1d413c3e82ff97de221c611c35c507daeb928e6cba674a4b0843c603724036f` | main 原始 blob |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_runtime_audit_021.py` | 5404 | `47cff939adade4b695ca62869b04e63a4ce0806d9e41071dbfebb3a9008cfc8b` | main 原始 blob |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_021_command.py` | 14896 | `bf338ef520cd2000991455c1dec8405b4dd2195dfd8f363f3f31f0599d1318ee` | main 原始 blob |
| 纯内存冻结命令 | 32009 | `8407837bc7e9af65dc7d2fe8ad1f8a9728186745ad25d20e802c8793a9740dcd` | 不落盘 |

固定审计源仍为 `infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh`：18377 字节，SHA-256 `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256`，不因 021 修改。

合并后 Git blob 已从 merge commit 原始对象逐项复核：启动器 `8662e3e6558453799245d084e32b8826ec84e969`、生成器 `087683242cae3b3a1696e8815a9102f6650f002b`、启动器测试 `78b68b48cf18892393f6e71abb89ac2e96c59d6e`、生成器测试 `ec8a2e184ea7e1abd5aa1dfe8d3db4d4eee69adc`、固定审计源 `27450efc39af7e763ea8df0c59d584433d5e5edd`；五个文件均为 LF、CRLF=0。

## 6. 合并与停止条件

021 工程 HEAD `c73ef139721bcfc693ffb31caa6fe803be526286` 已通过 PR #398、CI run `31867790659 completed/success` 及代码安全、QA、产品/规格独立评审，并以 merge commit `8bc05cbf3bc71a8954087dc7f26732f836e5212e` 合入 main。父提交顺序为 `358edfd8e8d5d3293944314d79d503245049649a` 后 `c73ef139721bcfc693ffb31caa6fe803be526286`，远端工程分支已删除；main 原始 Git blob、LF 和纯内存命令摘要已重新核对且无漂移。

合并后必须停止并保持 `REMOTE_NOT_AUTHORIZED`。只有用户对精确 ChangeId、工程 merge、命令大小、命令 SHA-256、最多一次非交互 SSH 和零重试重新作出独立授权，才允许调用固定正式入口。
