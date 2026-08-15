# G8 Drop 最小只读入口安装 020 工程授权清单

## 1. 当前状态

`PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`

020 使用新的独立 ChangeId `CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260815-020`。本轮只授权工程实现、Git 推送、PR、CI、独立评审、merge commit 与合并后原始 blob 复核；不授权 SSH、sudo、安装器、post-check 或任何测试服操作。020 尚未安装、尚未消费，`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。

019 永久墓碑，禁止恢复、再次授权、重试、重放或执行历史生成文件。020 只读取 014 已证明 `PRESENT / PASS / NONE` 的固定 011 暂存资产，不改变 011，也不继承 019 的一次性授权。

## 2. 020 要解决的问题

019 的唯一执行在恢复 `$ErrorActionPreference` 时因保存值为 `Null` 失败，窗口内固定标志不可恢复，导致 SSH 与远端阶段只能保守记录为 `UNKNOWN / 最多 1`。020 不猜测 019 是否到达服务器，而是同时收敛本地可诊断性和远端未知安装状态：

1. 进入脚本时把 `Null` 或非法的 ActionPreference 规范化为 `Continue`；回执创建失败也必须恢复调用方偏好，回执写入、普通刷盘、耐久刷盘与流清理异常都不得覆盖主要结果或泄露原始异常。
2. 正式生成命令固定使用 Windows API 取得的可信用户目录，并以 `CreateNew` 创建 `.g8-020-execution-receipt.txt`；这是固定可信用户目录耐久低敏回执。每个阶段写入后同时 `Flush()` 与 `Flush(true)`，窗口关闭后不依赖回滚查看控制台。
3. 回执只能包含固定状态：`RECEIPT=STARTED`、`PRE_SSH_GATE=PASS`、`SSH_ATTEMPTED=YES`、固定失败原因与最终 `HOST_RESULT`；禁止写入真实路径、指纹、密钥正文、原始异常、密码或其他凭据。
4. 回执已存在或无法创建时，必须在 SSH 前固定返回 `receipt_unavailable`，不覆盖既有文件、不回显路径、不关闭父 PowerShell。
5. `PRE_SSH_GATE` 之后立即强制刷盘；只有紧邻唯一 SSH 调用时才写入并刷盘 `SSH_ATTEMPTED=YES`。任一失败立即停止、零重试。

## 3. 远端三态与固定影响

020 在唯一 SSH 会话内、任何交互 sudo 前，把 live 状态严格分类为“精确已安装 / 完全未安装 / 部分或漂移”：

- `EXACT`：审计器、对账工具和 sudoers 的类型、owner、mode、SHA-256、对账工具大小、唯一 NOPASSWD 有效范围、pc 非 docker 组成员和审计器 `--self-test` 全部匹配。只输出固定 post-check 通过，不执行 `sudo -k -v`，也不重复安装。
- `ABSENT`：三个 live 目标与本 ChangeId 的 root-only 副本都不存在，才允许进入一次 `sudo -k -v`、一次固定安装器事务和固定 post-check。
- `DRIFT`：任一目标部分存在、类型/owner/mode/摘要/大小/有效 sudo 范围不匹配，或本 ChangeId root-only 副本已存在，均在交互 sudo 前失败关闭；禁止覆盖、修复或清理。

安装路径继续保留 019 已评审通过的 BatchMode、空口令私钥配对、固定且持锁 known_hosts、`LogLevel=QUIET`、单一 `ssh -tt`、`ConnectionAttempts=1`、no-clobber、sudo 最小权限、pc 非 docker 组成员、HUP/TERM/INT 临界区回滚和不可重入清理控制。完整远端脚本作为不含秘密的 Base64 参数随唯一 SSH 调用传入；stdin/TTY 只保留给将来精确授权范围内最多一次 sudo 密码提示。

## 4. TDD 与离线证据

- Windows PowerShell 5.1 动态覆盖：入口 ActionPreference 为 `Null`、回执预占、回执 `WriteLine()` / `Flush()` / `Flush(true)` / Dispose 故障、失效 writer 不再重写、假 `ssh.cmd` 成功与失败、固定 PRE_SSH/SSH_ATTEMPTED 顺序、父 PowerShell 保活、六类低敏异常、加密私钥无提示拒绝、伪造 TEMP、known_hosts 持锁与预占不删除、UNC/device/DOS 保留名/ADS 输出拒绝。
- Linux `--network none` 动态覆盖：安装器 no-clobber、HUP/TERM/INT 与重复信号回滚、sudo 精确范围、审计器入口失败回滚、CRLF manifest，以及 live `EXACT`、`ABSENT`、部分存在 `DRIFT` 和 sudoers 摘要漂移 `DRIFT` 分类。
- 两个平台都解析完整 PowerShell/Bash 生成物，验证单一 SSH 目标、唯一交互 sudo、冻结 011 来源、嵌入安装器字节、无 Secret 与未来消费墓碑顺序。
- 离线测试可创建本地假 `ssh.cmd` 或调用 `ssh-keygen` 生成临时夹具；不得建立网络连接。

## 5. 冻结工程候选

| 文件/生成物 | 大小 | SHA-256 | 换行/落盘 |
|---|---:|---|---|
| `infra/scripts/g8-test-readonly-access-install-020.sh` | 10977 | `f76c1bd10560fc4d5ea5de569065db65d4f0114510184e64311b52bc7d71a62f` | CRLF=0 |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-020-command.py` | 32417 | `e7fe5aa686457c2455426e29e84127550384da53fe59317d8ebea3fb134fd9cd` | CRLF=0 |
| `infra/scripts/test_g8_test_readonly_access_install_020.py` | 18254 | `008904184e4c625a5599f2b021b9995e491cd86be078c916f2941c186861e5f7` | CRLF=0 |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_020_command.py` | 29405 | `c95cc176b4c979bd2f24ff68a992034228c5a5f23bacd769898c6154172c1bc0` | CRLF=0 |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_020_security_controls.py` | 36650 | `caa1005a13f0a7b81c118b151dcd4c68e8437cad6ae681da6ca47b52799deb9a` | CRLF=0 |
| 纯内存冻结命令 | 40912 | `cf10ee50e45c615ccb31aa9ab8ae2a4e5884ddac9582d06ddfac87109ea20f25` | 不写盘 |

冻结命令使用运行时可信用户目录回执模式，不包含具体用户名或调用方路径；唯一 SSH 目标为 1，远端 `sudo -k -v` 最多 1，父 PowerShell `exit 2` 为 0。以上摘要只证明仓库候选可复核，不证明测试服已安装或运行态通过。

## 6. 工程门禁、停止与后续授权

工程候选必须依次通过：015 至 019 永久墓碑回归、020 安装器与两个生成器测试集、019/020 授权契约、CI workflow 契约、`py_compile`、`bash -n`、`git diff --check`、敏感信息扫描、Windows PowerShell 5.1 与已缓存镜像 `--network none` 回归。随后才可推送合规 `feature/backend-d-*` 分支、创建中文 PR，并完成代码安全、QA、产品/规格独立评审；精确 HEAD 的 P0/P1 必须为 0，全部适用 CI 必须 `completed/success`，再以 merge commit 合入 main、删除远端分支并从 main 原始 Git blob 复核摘要。

完成工程合并和合并后复核后必须停止。只有用户对精确 020 ChangeId、合并提交、冻结命令摘要和最大影响作出新的独立授权，才可生成或执行正式命令。该未来授权也不得隐含业务 HTTP、真实上游、钱包、费用、通知、客户流量、数据库、队列、migration、远端 Docker、服务启停、部署或生产动作；任何失败仍立即停止、零重试，并按永久消费规则归档 020。
