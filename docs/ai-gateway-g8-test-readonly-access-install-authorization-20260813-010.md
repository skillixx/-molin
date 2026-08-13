# AI 网关 G8 测试服只读入口安装授权清单（010）

> 当前状态：`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。本清单只冻结未来可能执行的测试服最小只读入口安装步骤；当前仓库授权不允许运行 `--local-check`、SSH、SFTP、root 安装或远端 sudo self-test。只有精确 HEAD 的测试、CI、独立安全评审、QA、产品/规格验收和 merge commit 全部完成后，状态才可收敛为 `PENDING_USER_APPROVAL`，并仍须用户再次明确批准。

## 1. ChangeId 与目标

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010`。
- Drop SSH 端点：`pc@8.130.9.163:10003`。
- 传输类型：`DROP_SSH_DIRECT`；物理主机身份：`NOT_APPLICABLE`。
- SSH ED25519 指纹：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`。
- 部署根：`/home/pc/molin`。
- 暂存目录：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010`。
- root-only 临时目录：`/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010`。
- 管理员通道：仅限未来另行批准的现有 root 通道，不得在参数、日志、文档或聊天中提供密码。

010 使用现有 ED25519 密钥免密码认证（即免密钥口令交互，但仍显式绑定密钥文件）。包装器必须显式使用原始 `C:\Users\skillixx\.ssh\id_ed25519`、`id_ed25519.pub` 和 `known_hosts`，不复制、不 chmod、不修改私钥或其 NTFS ACL；禁用 Agent、AskPass、密码和键盘交互。

## 2. 冻结候选与证据

| 文件 | 最终目标 | SHA-256 | 最终属主 / 权限 |
|---|---|---|---|
| `g8-test-readonly-audit` | `/usr/local/libexec/molin/g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | `root:root / 0755` |
| `ai-gateway-reconcile` | `/usr/local/libexec/molin/ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | `root:root / 0755` |
| `molin-g8-test-readonly-audit.sudoers` | `/etc/sudoers.d/molin-g8-test-readonly-audit` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | `root:root / 0440` |

- 来源提交：`75b1fc4ddb7138495547cec03fa948648de337d7`。
- 来源树：`53ba990318bc1a036b442d88ff8133d776a453dc`。
- Go：`go1.26.5`，目标 `linux/amd64`，`CGO_ENABLED=0`，连续双构建一致。
- 对账器大小：`13066129` 字节。
- Windows 本地候选：`D:\molingproject\g8-artifacts\CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010`。
- Windows `SHA256SUMS` 回执：`3ff8cf3ad7237f866f83305d00ab73f766381b7f3247abee915efee629e41fb0`。
- Linux CI 临时复现回执：`b3fac1a1530124da9dc604c32d11bd665de3daa5d6799aebb33c38a3d2f174f4`；仅用于跨平台复现，不替代未来实际执行所绑定的 Windows 候选回执。
- 010 直连包装器 SHA-256：`185c0ccda420d3bbe97e95c3218a03642372e05525d2663258287ebd981360b8`。
- 冻结 009 helper SHA-256：`4be88638f2a4a271ebbf23751bd3f7238ea5f78f1f18fcb6889c9e071b953f30`。
- 候选必须恰含五个文件；manifest 必须声明 `TARGET_TRANSPORT=DROP_SSH_DIRECT`、`PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE`，不得出现 hostname 或 machine-id 字段。

合并后必须从 merge commit 重新计算包装器与 helper 摘要并精确一致；任一漂移须新 ChangeId。

## 3. 待用户再次批准的精确顺序

以下命令当前全部禁止执行，只作为独立安装授权的冻结摘要。

### 3.1 一次本地检查

```powershell
python -I infra\scripts\run-ai-gateway-g8-test-readonly-access-stage-drop-direct.py --local-check --change-id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010 --candidate-dir=D:\molingproject\g8-artifacts\CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010 --known-hosts=C:\Users\skillixx\.ssh\known_hosts --identity-file=C:\Users\skillixx\.ssh\id_ed25519 --identity-public-file=C:\Users\skillixx\.ssh\id_ed25519.pub
```

只核验五文件、manifest、回执、known_hosts、固定公钥指纹、密钥对和本地文件稳定性，不联网。固定输出必须只有 `G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT_LOCAL_CHECK=PASS`。

### 3.2 一次只读 SSH 与一次原子 SFTP

本地检查完整通过后，未来授权只能将同一命令移除 `--local-check` 并执行一次。包装器内部固定：

- 一次 SSH 预检，`ConnectionAttempts=1`，零重试；
- 明确 `IdentityFile`、`UserKnownHostsFile`、`IdentitiesOnly=yes`、`-F none`；
- 禁密码、键盘交互、Agent、X11、转发、本地命令和 TTY；
- 远端只读登录用户、组、部署根真实路径与低敏元数据，并检查暂存与三个 live 目标不存在；
- 完整通过后才执行一次 SFTP；首条命令独占创建暂存目录，随后设为 `0700` 并逐项上传五文件；
- SFTP 只读取随机临时目录中的五文件候选快照，SSH/SFTP 直接引用原始身份路径；
- 任意非零、stderr、超时、输出超限、字段或返回契约漂移立即停止。

### 3.3 一次 root 安装

只有前两步完整通过后，root 控制台才可：

1. 原子新建 `/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010`，要求 `root:root:0700`、普通目录、非链接；
2. 从 `pc` 暂存逐项复制五文件到 root-only 副本；
3. 在 root-only 副本重新核验五文件白名单、普通文件/非链接、回执、四项摘要、对账器大小和 `visudo -cf`；
4. 逐级核对 `/usr`、`/usr/local`、`/usr/local/libexec`、已有 `/usr/local/libexec/molin`、`/etc`、`/etc/sudoers.d` 均为非链接 root-owned 目录且组/其他不可写；
5. 仅当 `/usr/local/libexec/molin` 缺失时允许以 `root:root:0755` 新建并立即登记；
6. 安装前再次逐项确认三个 live 目标不存在且非链接；
7. 只从 root-only 副本以 no-clobber 同一文件描述符独占创建三个 live 目标；
8. 安装后复核普通文件/非链接、owner、mode、三个 SHA-256 和对账器大小；
9. 精确执行 `visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit`、`sudo -n -l -U pc` 和 `id -nG pc`。

三个 live 目标必须逐项使用以下 no-clobber 模式；成功后立即登记为本次新建。预存目标导致独占打开失败时不得删除或覆盖；独占创建后的任何失败只删除本次刚创建的目标：

```bash
/bin/bash -ceu 'target=/usr/local/libexec/molin/g8-test-readonly-audit; source=/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010/g8-test-readonly-audit; created=0; cleanup() { rc=$?; if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /usr/bin/rm -f -- "$target"; fi; exit "$rc"; }; trap cleanup EXIT; set -o noclobber; exec 3> "$target"; created=1; /usr/bin/cat "$source" >&3; exec 3>&-; /usr/bin/chown root:root "$target"; /usr/bin/chmod 0755 "$target"; trap - EXIT'

/bin/bash -ceu 'target=/usr/local/libexec/molin/ai-gateway-reconcile; source=/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010/ai-gateway-reconcile; created=0; cleanup() { rc=$?; if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /usr/bin/rm -f -- "$target"; fi; exit "$rc"; }; trap cleanup EXIT; set -o noclobber; exec 3> "$target"; created=1; /usr/bin/cat "$source" >&3; exec 3>&-; /usr/bin/chown root:root "$target"; /usr/bin/chmod 0755 "$target"; trap - EXIT'

/bin/bash -ceu 'target=/etc/sudoers.d/molin-g8-test-readonly-audit; source=/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010/molin-g8-test-readonly-audit.sudoers; created=0; cleanup() { rc=$?; if [ "$rc" -ne 0 ] && [ "$created" -eq 1 ]; then /usr/bin/rm -f -- "$target"; fi; exit "$rc"; }; trap cleanup EXIT; set -o noclobber; exec 3> "$target"; created=1; /usr/bin/cat "$source" >&3; exec 3>&-; /usr/bin/chown root:root "$target"; /usr/bin/chmod 0440 "$target"; trap - EXIT'
```

### 3.4 一次非特权 sudo self-test

只有全部安装和权限门禁通过后，允许由 `pc` 非特权会话执行一次：

```bash
sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test
```

禁止直接执行、附加参数或运行真实审计。sudo 必须只允许该固定命令，`pc` 不得属于 Docker 组。

## 4. 影响、回滚和停止条件

- 授权上限：本地检查 1 次、SSH 1 次、SFTP 1 次、root 安装 1 次、sudo self-test 1 次；全部零重试。
- 请求与费用：业务请求 0、上游请求 0、费用上限 0 CNY。
- 计划创建面：一个 `pc:pc:0700` 暂存目录、一个 `root:root:0700` root-only 临时目录、可能新建的 `/usr/local/libexec/molin`、两个只读工具和一个单命令 sudoers 文件。
- 每项创建后立即登记。失败时只逆序删除本次已明确登记的新建 live 目标；sudoers 先删除并重新执行 `visudo -cf /etc/sudoers`。
- 若 `/usr/local/libexec/molin` 由本次新建，仅当两个工具均已回滚且目录再次证明为空时允许精确 `rmdir`；禁止递归删除。
- 预存目标绝不覆盖、删除或修改。
- root-only 临时目录只有在安装、live 复核和 sudo self-test 全部通过后才可精确清理；中途失败则保留取证，后续清理使用新 ChangeId。
- SFTP 暂存无论成功或失败都保留用于独立取证；删除必须使用新 ChangeId 和独立授权。

任一端点、登录用户、路径、父链、文件类型、摘要、回执、密钥对、known_hosts、属主、权限、stderr、返回码、输出契约、`visudo`、sudo 范围、Docker 组或 self-test 不符立即停止。检测到 Secret、额外命令需求或无法证明目标为本次新建时同样停止。

本清单不授权真实运行态审计、服务重启、Migration、配置或凭据修改、数据库/Redis/RabbitMQ/队列读取、生产连接、付费上游、真实通知、客户灰度或商业观察。`G8_ENGINEERING_READY` 保持；`G8_COMMERCIAL_ACCEPTED` 继续未完成。
