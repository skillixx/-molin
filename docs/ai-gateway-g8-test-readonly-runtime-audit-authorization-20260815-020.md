# G8 020 无安装 Docker 只读运行态审计工程清单

## 1. 当前状态

`CONSUMED_LOCAL_WRAPPER_PARSE_FAILED_SSH_NOT_STARTED / REMOTE_NOT_AUTHORIZED`

020 当时使用独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-020`。根据用户确认，历史候选不安装受控只读审计入口，不创建 `/usr/local/libexec` 文件、不写 sudoers、不执行 sudo；由 `pc` 直接使用既有 Docker 权限，在单次非交互 SSH 会话中以内存脚本完成固定只读运行态核验。

用户后续对精确 ChangeId、工程 merge、冻结命令摘要和唯一非交互 SSH 上限作出了独立授权。本地冻结门禁与命令生成 PASS，但外层 PowerShell 包装在语法解析阶段因缺少右括号失败，没有调用 Windows PowerShell 5.1、冻结命令或 SSH。SSH、Docker/HTTP/数据库查询、sudo、安装、宿主写入、业务/上游/费用均为 0，重试为 0；020 已失败关闭消费，测试服运行态仍未形成新证据，`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。详见 `docs/ai-gateway-g8-test-readonly-runtime-audit-attempt-20260815-020.md`。

## 2. 执行前历史权限与能力边界

Docker 控制权限本质上接近宿主 root 能力，因此“不使用 sudo”不等于低权限。020 执行前的历史批准边界曾把实际命令收窄为冻结白名单，并在工程门禁中反向禁止任何变更能力；下列权限随唯一尝试失败消费，现均已失效：

- 当时只允许 `docker info/version/ps/inspect/image inspect`，以及对固定 `molin-mysql`、`molin-redis`、`molin-rabbitmq` 容器执行只读版本、健康、队列聚合和 SQL `SELECT`。
- 当时只允许读取宿主 API 进程、监听、health/ready、固定环境变量名称、Prometheus/Grafana/Alertmanager 低敏状态、备份摘要，以及执行 014 已证明完整的固定 011 `ai-gateway-reconcile` 只读二进制；Drop 物理 hostname、machine-id、密码状态与 Docker 组枚举不属于历史候选范围，禁止读取或输出。
- 历史边界禁止 `docker run/create/start/stop/restart/rm/cp/compose`、容器或镜像构建、网络/卷变更、调用方提供的任意 shell 或参数、宿主 bind mount、`--privileged`、文件创建/覆盖/删除、服务控制、migration、DDL/DML、队列消费、业务请求、真实上游、钱包或费用动作；冻结审计器内部当时只允许为读取固定容器环境执行预定命令。
- 审计源中的 MySQL/Redis 凭据当时只允许在固定容器或只读对账子进程内部使用；输出只包含聚合结果、版本、状态、摘要和变量名，不得回显 Secret。
- 远端脚本原计划通过唯一 `ssh -T` 的 Base64 参数在内存中执行，不上传、不落盘、不申请 TTY。全部低敏结果先在会话内存聚合；脚本非零、任一必需探针出现 `UNAVAILABLE/MISSING/INVALID/000`、空值、缺少固定必需键或缺少完成标志时，固定返回 `audit_evidence_failed` 并结束唯一会话，零重试。`COLLECTION_PASS` 当时也只证明采集完整，不能据此宣称运行态或软件闭环通过。

## 3. 执行前历史本地耐久证据

历史正式命令当时使用 Windows API 获取可信系统目录、固定 OpenSSH、唯一 known_hosts 条目和固定 ED25519 客户端公钥；拒绝密码、键盘交互、代理、转发和本地命令。历史设计在固定可信用户目录以 `CreateNew` 创建 `.g8-020-runtime-audit-receipt.txt`，同步记录 `STARTED`、`PRE_SSH_GATE`、`SSH_ATTEMPTED` 与最终 `HOST_RESULT`。

历史入口把 ActionPreference 为 `Null` 或非法值时规范化为 `Continue`。回执创建、`WriteLine()`、`Flush()`、`Flush(true)` 或 Dispose 失败不得泄露原始异常、真实路径或凭据；SSH 前回执失败固定返回 `receipt_unavailable`，恢复父 PowerShell 状态并设置 `$LASTEXITCODE=2`。

## 4. 执行前历史 TDD 与当前墓碑门禁

- 执行前 Windows PowerShell 5.1 历史门禁覆盖完整语法、Null preference、回执写入/刷盘故障、父窗口保活、假 `ssh.cmd` 成功/失败、PRE/ATTEMPTED 顺序、可信路径、known_hosts、空口令密钥配对和单一 `ssh -T`。
- 执行前 Linux `--network none` 历史门禁覆盖生成器导入、自检、远端 Bash 解析、冻结审计源转换、禁止 sudo/安装/Docker 变更/写 SQL，以及 015 至 019 永久墓碑回归。
- 消费归档后的当前 CI 仅在原生 Windows 与 Linux 只读断网容器验证 015 至 020 全部墓碑、消费授权契约及历史工程 merge 原始 Git blob；不再生成或执行 020 正式远端命令。

## 5. 冻结工程候选

| 文件/生成物 | 大小 | SHA-256 | 换行/落盘 |
|---|---:|---|---|
| `infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh` | 18377 | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | CRLF=0 |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py` | 27486 | `3a286187602277c2255e978712e37cff7d6edf46d292a185e665aaa70654bbae` | CRLF=0 |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py` | 14896 | `a156e62417826ce5a8f6347d46edca384f6abfaa5e819aa300dc0dc55b3d5b8b` | CRLF=0 |
| 纯内存冻结命令 | 32009 | `31c1eaaf6f3916dbabb51447a63d263ac4f73509bb8e535451df28db4e024a3d` | 不写盘 |

上表是执行前冻结的历史工程候选。冻结命令只有一个 SSH 目标、`ConnectionAttempts=1`、`RequestTTY=no`；远端 `sudo`、安装器、host 写入与 Docker 变更命令均为 0。该命令现已消费，禁止执行或重放；摘要不证明测试服运行态通过。

工程 PR #394 精确 HEAD 为 `dcb594d33e79bfbb059293e4734e49e62409d51a`，CI run `31861762018` 为 `completed/success`，代码安全、QA、产品/规格独立复评均为 P0/P1/P2/P3=`0/0/0/0`。PR 以 merge commit `3c63539279a34ae2365fc9d7e26e207dd728c4ba` 合入 main，父提交顺序为 `b9211b8a90610aa2e45873fa9de54575bce58fb5` 后 `dcb594d33e79bfbb059293e4734e49e62409d51a`，远端工程分支已删除。

从合并后 main 原始 Git blob 独立复核：审计源 blob `27450efc39af7e763ea8df0c59d584433d5e5edd`，生成器 blob `212124e085c2f34adf11eae62b0e0119c5d8f44e`，专项测试 blob `c3930bc478b2b05d33822db2996618949384f9f3`；三者大小、SHA-256 与 CRLF=0 均与上表一致。从合并 blob 纯内存重建的命令仍为 32009 字节、SHA-256 `31c1eaaf6f3916dbabb51447a63d263ac4f73509bb8e535451df28db4e024a3d`，`Get-FileHash`/sudo/`docker run`/父 PowerShell `exit 2` 均为 0，SSH 目标为 1。

## 6. 合并、停止与未来授权

精确 HEAD 已通过本地 Windows/Linux 断网门禁、敏感信息扫描、适用 CI 和代码安全、QA、产品/规格独立评审，并按 merge commit 合入 main；合并后 main 原始 Git blob 大小、SHA-256、Git blob、CRLF 与冻结命令摘要已重算且一致。

020 的唯一授权尝试已失败并按零重试规则消费。现有冻结命令、工程 merge、归档证据与改名后的本地文件均不构成再次授权；020 不得再次授权、重试或重放。继续运行态审计只能使用新的独立 ChangeId，重新完成工程门禁和用户精确授权。
