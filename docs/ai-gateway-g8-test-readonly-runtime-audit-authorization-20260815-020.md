# G8 020 无安装 Docker 只读运行态审计工程清单

## 1. 当前状态

`PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`

020 使用独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-020`。根据用户确认，本候选不安装受控只读审计入口，不创建 `/usr/local/libexec` 文件、不写 sudoers、不执行 sudo；由 `pc` 直接使用既有 Docker 权限，在单次非交互 SSH 会话中以内存脚本完成固定只读运行态核验。

本轮只授权本地工程实现、Git 推送、PR、CI、独立评审、merge commit 与合并后原始 blob 复核；不授权 SSH、Docker 命令、HTTP、数据库查询或测试服操作。020 尚未执行、尚未消费，测试服运行态仍未形成新证据，`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。

## 2. 权限与能力边界

Docker 控制权限本质上接近宿主 root 能力，因此“不使用 sudo”不等于低权限。020 必须把实际命令收窄为冻结白名单，并在工程门禁中反向禁止任何变更能力：

- 允许 `docker info/version/ps/inspect/image inspect`，以及对固定 `molin-mysql`、`molin-redis`、`molin-rabbitmq` 容器执行只读版本、健康、队列聚合和 SQL `SELECT`。
- 允许读取宿主 API 进程、监听、health/ready、固定环境变量名称、Prometheus/Grafana/Alertmanager 低敏状态、备份摘要，以及执行 014 已证明完整的固定 011 `ai-gateway-reconcile` 只读二进制；Drop 物理 hostname、machine-id、密码状态与 Docker 组枚举不属于本候选范围，禁止读取或输出。
- 禁止 `docker run/create/start/stop/restart/rm/cp/compose`、容器或镜像构建、网络/卷变更、调用方提供的任意 shell 或参数、宿主 bind mount、`--privileged`、文件创建/覆盖/删除、服务控制、migration、DDL/DML、队列消费、业务请求、真实上游、钱包或费用动作；冻结审计器内部只允许为读取固定容器环境执行预定命令。
- 审计源中的 MySQL/Redis 凭据只在固定容器或只读对账子进程内部使用；输出只包含聚合结果、版本、状态、摘要和变量名，不得回显 Secret。
- 远端脚本通过唯一 `ssh -T` 的 Base64 参数在内存中执行，不上传、不落盘、不申请 TTY。全部低敏结果先在会话内存聚合；脚本非零、任一必需探针出现 `UNAVAILABLE/MISSING/INVALID/000`、空值、缺少固定必需键或缺少完成标志时，固定返回 `audit_evidence_failed` 并结束唯一会话，零重试。`COLLECTION_PASS` 只证明采集完整，业务状态偏差仍须按验收标准判定，不能据此宣称运行态或软件闭环通过。

## 3. 本地耐久证据

正式命令继续使用 Windows API 获取可信系统目录、固定 OpenSSH、唯一 known_hosts 条目和固定 ED25519 客户端公钥；拒绝密码、键盘交互、代理、转发和本地命令。固定可信用户目录以 `CreateNew` 创建 `.g8-020-runtime-audit-receipt.txt`，同步记录 `STARTED`、`PRE_SSH_GATE`、`SSH_ATTEMPTED` 与最终 `HOST_RESULT`。

入口 ActionPreference 为 `Null` 或非法值时规范化为 `Continue`。回执创建、`WriteLine()`、`Flush()`、`Flush(true)` 或 Dispose 失败不得泄露原始异常、真实路径或凭据；SSH 前回执失败固定返回 `receipt_unavailable`，恢复父 PowerShell 状态并设置 `$LASTEXITCODE=2`。

## 4. TDD 与离线门禁

- Windows PowerShell 5.1：完整语法、Null preference、回执写入/刷盘故障、父窗口保活、假 `ssh.cmd` 成功/失败、PRE/ATTEMPTED 顺序、可信路径、known_hosts、空口令密钥配对和单一 `ssh -T`。
- Linux `--network none`：生成器导入、自检、远端 Bash 解析、冻结审计源转换、禁止 sudo/安装/Docker 变更/写 SQL，以及 015 至 019 永久墓碑回归。
- CI 同时在原生 Windows 与 Linux 只读断网容器运行 020 生成器和授权契约；不生成或执行正式远端命令。

## 5. 冻结工程候选

| 文件/生成物 | 大小 | SHA-256 | 换行/落盘 |
|---|---:|---|---|
| `infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh` | 18377 | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | CRLF=0 |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-runtime-audit-020-command.py` | 27486 | `3a286187602277c2255e978712e37cff7d6edf46d292a185e665aaa70654bbae` | CRLF=0 |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_runtime_audit_020_command.py` | 14896 | `a156e62417826ce5a8f6347d46edca384f6abfaa5e819aa300dc0dc55b3d5b8b` | CRLF=0 |
| 纯内存冻结命令 | 32009 | `31c1eaaf6f3916dbabb51447a63d263ac4f73509bb8e535451df28db4e024a3d` | 不写盘 |

冻结命令只有一个 SSH 目标、`ConnectionAttempts=1`、`RequestTTY=no`；远端 `sudo`、安装器、host 写入与 Docker 变更命令均为 0。摘要只证明工程候选可复核，不证明测试服运行态通过。

## 6. 合并、停止与未来授权

精确 HEAD 必须通过本地 Windows/Linux 断网门禁、敏感信息扫描、适用 CI 和代码安全、QA、产品/规格独立评审，P0/P1 为 0 后才可 merge commit 合入 main；合并后从 main 原始 Git blob 重算大小、SHA-256、Git blob、CRLF 与冻结命令摘要。

合并和摘要复核完成后必须停止。只有用户对精确 020 ChangeId、合并提交、命令摘要以及“单次 SSH + 固定只读 Docker/宿主/HTTP/数据库查询”的最大影响作出新的独立授权，才可生成或执行正式命令。任何失败立即停止、零重试；执行完成或失败后 020 都必须永久消费并墓碑化。
