# AI 网关 G8 测试服只读入口安装授权清单（009）

> 当前状态：`PENDING_USER_APPROVAL`。PR #358 精确 HEAD `2efb809ba090c9af780d8c6be2f75ee707b92d6b` 已通过 CI run `31665135810` 12/12 和独立代码安全、QA、产品/规格 P0/P1/P2=0，并按 merge commit `1f0c2d11dc705be9496eb18c73688d21ee0e8ab5` 合入主干。仓库离线包装器 `--self-test` 只证明工程门禁；尚未授权执行 `--local-check`、SSH、SFTP、root 安装或安装后的远端 sudo auditor self-test，也未连接测试服务。

## 1. ChangeId 与目标

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009`。
- Drop SSH 端点：`pc@8.130.9.163:10003`。
- 传输类型：`DROP_SSH`；物理主机身份：`NOT_APPLICABLE`。不得读取或门禁 hostname、machine-id、实例元数据或 CMDB。
- SSH ED25519 指纹：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`。
- 部署根：`/home/pc/molin`，必须为真实目录、`pc:pc`、属主可完整访问且组/其他用户不可写。
- 暂存目录：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009`，必须在预检时不存在，并由唯一一次 SFTP 独占新建。
- 管理员通道：仅限后续用户另行指定并批准的受控 root 控制台；不得在聊天、参数、日志或文档中提供密码。

## 2. 冻结候选

| 文件 | 最终目标 | SHA-256 | 最终属主 / 权限 |
|---|---|---|---|
| `g8-test-readonly-audit` | `/usr/local/libexec/molin/g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | `root:root / 0755` |
| `ai-gateway-reconcile` | `/usr/local/libexec/molin/ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | `root:root / 0755` |
| `molin-g8-test-readonly-audit.sudoers` | `/etc/sudoers.d/molin-g8-test-readonly-audit` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | `root:root / 0440` |

- 来源提交：`7f3325e2d6801567fea34a2049a2f3ada114e348`；来源树：`4563feb59850dca87789adfb5eea820f78b1a209`。
- Go：`go1.26.5`，`linux/amd64`，`CGO_ENABLED=0`，连续双构建摘要一致；对账器大小 `13066129` 字节。
- 本地候选：`D:\molingproject\g8-artifacts\CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009`。
- 本地 `SHA256SUMS` 回执：`840bdbed48edab6d70d351fa232b7426903bf3f3098f682e2884f513b9cd0efd`。
- Drop stage 包装器 SHA-256：`3ad9cac165355ea1be150f141af6072d787fe9888733ec025cbf3466d6af5f04`；PR 合并后必须由合并提交中的同一 blob 重新计算并精确一致，任何漂移都须新 ChangeId。
- 候选恰含五个文件；manifest 必须声明 `TARGET_TRANSPORT=DROP_SSH`、`PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE`，且不得出现 `TARGET_HOSTNAME` 或 `TARGET_MACHINE_ID_SHA256`。

## 3. 待再次批准的精确执行顺序

以下步骤当前全部禁止执行。仓库工程门禁和 PR 合并已经完成，但不构成远端授权；只有用户再次明确批准 009 后，方可严格依次执行：

1. 本地以 `python -I infra/scripts/run-ai-gateway-g8-test-readonly-access-stage-drop.py --local-check` 绑定本 ChangeId、上述候选绝对路径、现有 `known_hosts` 及同目录 `id_ed25519`/`id_ed25519.pub`；只做五文件、回执、Drop manifest、known_hosts、客户端公钥指纹和密钥对核验，不联网。
2. 本地检查完整 PASS 后，去掉 `--local-check`，同一包装器只允许一次 SSH 预检。固定 OpenSSH、`ConnectionAttempts=1`、显式密钥、禁止密码/键盘交互/代理/X11/端口转发/本地命令；远端只允许读取登录用户、登录组、部署根真实路径与元数据，并检查本次暂存及三个 live 目标均不存在。任何非零、超时、stderr、输出超限、键集合或返回契约不符立即停止且零重试。
3. SSH 预检完整 PASS 后，包装器只允许一次 SFTP；第一条批处理命令必须独占 `mkdir` 上述暂存目录，随后设为 `0700` 并逐项上传五文件。目录已存在、任一上传失败、非零或 stderr 均停止；禁止合并、覆盖、下载或删除。
4. 后续 root 控制台必须先将五文件逐项复制到全新的 `root:root:0700` 临时目录 `/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009`，再从该 root-only 副本复核普通文件/非链接、白名单、回执、四项摘要、对账器大小，并精确执行 `visudo -cf /root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009/molin-g8-test-readonly-audit.sudoers`。禁止从 `pc` 可写暂存目录直接安装。
5. 安装前逐级核对 `/usr`、`/usr/local`、`/usr/local/libexec`、已有 `/usr/local/libexec/molin`、`/etc`、`/etc/sudoers.d` 均为非链接 root-owned 目录且组/其他不可写；仅 `/usr/local/libexec/molin` 缺失时允许以 `install -d -o root -g root -m 0755 /usr/local/libexec/molin` 新建、立即登记并复核。安装三个 live 文件前必须再次逐项执行 `lstat`/存在性门禁；每个目标只能先由 root shell 的 noclobber 独占创建空普通文件（已存在或链接即失败），立即登记为本次新建，再从 root-only 副本复制内容并设置精确 owner/mode。禁止直接使用会覆盖目标的普通 `install <source> <target>`。
6. 安装后重新核对三个 live 文件的普通文件/非链接、owner、mode、SHA-256及对账器大小，再执行 `visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit`、`sudo -n -l -U pc` 与 `id -nG pc`；sudo 必须只允许固定审计器，`pc` 不得属于 Docker 组。
7. 最后仅允许 `pc` 非特权会话执行一次 `sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test`；禁止直接执行、附加参数或真实运行态审计。

三个 live 目标的独占创建与写入必须在 root 控制台逐项使用以下固定模式；`/bin/bash -ceu` 启用立即退出，`noclobber` 负责独占创建，并始终通过同一已打开文件描述符写入，绝不在关闭门禁后按路径二次打开。每条命令以 `created` 标志和 `EXIT` trap 记录所有权：预存目标导致独占打开失败时绝不删除；独占创建后任一步失败则只删除本次刚创建的目标。命令成功后立即登记对应目标为本次新建，任一失败立刻停止并进入部分回滚：

```bash
/bin/bash -ceu 'target=/usr/local/libexec/molin/g8-test-readonly-audit; source=/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009/g8-test-readonly-audit; created=0; cleanup() { rc=$?; if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /usr/bin/rm -f -- "$target"; fi; exit "$rc"; }; trap cleanup EXIT; set -o noclobber; exec 3> "$target"; created=1; /usr/bin/cat "$source" >&3; exec 3>&-; /usr/bin/chown root:root "$target"; /usr/bin/chmod 0755 "$target"; trap - EXIT'

/bin/bash -ceu 'target=/usr/local/libexec/molin/ai-gateway-reconcile; source=/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009/ai-gateway-reconcile; created=0; cleanup() { rc=$?; if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /usr/bin/rm -f -- "$target"; fi; exit "$rc"; }; trap cleanup EXIT; set -o noclobber; exec 3> "$target"; created=1; /usr/bin/cat "$source" >&3; exec 3>&-; /usr/bin/chown root:root "$target"; /usr/bin/chmod 0755 "$target"; trap - EXIT'

/bin/bash -ceu 'target=/etc/sudoers.d/molin-g8-test-readonly-audit; source=/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009/molin-g8-test-readonly-audit.sudoers; created=0; cleanup() { rc=$?; if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /usr/bin/rm -f -- "$target"; fi; exit "$rc"; }; trap cleanup EXIT; set -o noclobber; exec 3> "$target"; created=1; /usr/bin/cat "$source" >&3; exec 3>&-; /usr/bin/chown root:root "$target"; /usr/bin/chmod 0440 "$target"; trap - EXIT'
```

## 4. 影响、回滚与停止条件

- 授权上限：本地检查 1 次、只读 SSH 1 次、SFTP 1 次、root 控制台安装 1 次、非特权 sudo self-test 1 次；全部零重试。业务请求 0、上游请求 0、费用上限 0 CNY。
- 计划创建面包括：一个 `pc:pc:0700` SFTP 暂存目录、一个 `root:root:0700` root-only 临时目录、可能新建的 `/usr/local/libexec/molin`，以及两个 root-owned 只读工具和一个单命令 sudoers 文件；每一项都必须在创建后立即登记“本次新建”。不得修改 API、容器、服务、环境文件、数据库、Redis、RabbitMQ、Bifrost、监控、日志、备份或流量。
- 任一步失败时，只逆序删除本次已明确记录为新建的 live 目标；sudoers 必须先删除并再次 `visudo -cf /etc/sudoers`。若 `/usr/local/libexec/molin` 确由本次新建，只有在两个工具均已回滚且目录再次证明为空时才允许精确 `rmdir`，禁止递归删除。任何预存目标不得覆盖或删除；不得删除账本、Usage、钱包、Outbox、日志、审计或备份。
- root-only 临时目录在成功安装、live 全量复核和非特权 sudo self-test 全部通过后，才允许精确清理；若中途失败则保留用于受控取证，清理必须使用新 ChangeId。SFTP 暂存无论成功或失败都保留用于独立取证，009 不授权删除；其后清理同样需要新 ChangeId 和独立授权。
- 身份、路径、父链、文件类型、摘要、大小、属主、权限、`visudo`、sudo 范围、Docker 组或 self-test 任一不符立即停止。检测到 Secret、额外命令需求或无法证明目标为本次新建时同样停止。

本清单不授权真实运行态审计、服务重启、Migration、配置或凭据修改、生产连接、付费上游、真实通知、客户灰度或四周商业观察。`G8_ENGINEERING_READY` 保持；`G8_COMMERCIAL_ACCEPTED` 继续未完成。
