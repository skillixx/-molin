# AI 网关 G8 测试服最小只读运维入口 Runbook

## 1. 状态与范围

本文记录测试服务器 `pc@8.130.9.163:10003` 的候选安装、核验和撤销约束。001 因 sudo 权限不符停止，002 因远端预检命令解析错误停止，003 因远端阶段固定返回 `remote_stage_failed` 停止，三者均已消费。003 未进入 root 控制台、未安装 live 目标或修改 sudoers；其低敏结果当时不能区分 SSH 与 SFTP，后续 008 已把固定暂存目录状态收敛为 `ABSENT`。

该入口用于补齐 MySQL、Redis、RabbitMQ、Bifrost、Prometheus、Grafana、Alertmanager、备份和只读账务证据。它不授予 `pc` Docker 组成员资格，不允许任意 `docker`、Shell、服务控制、文件写入、DDL/DML、队列消费或业务请求。

## 2. 固定资产

| 资产 | 安装目标 | 所有权 / 权限 |
|---|---|---|
| `infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh` | `/usr/local/libexec/molin/g8-test-readonly-audit` | `root:root / 0755` |
| Linux `ai-gateway-reconcile` | `/usr/local/libexec/molin/ai-gateway-reconcile` | `root:root / 0755` |
| `infra/sudoers/molin-g8-test-readonly-audit` | `/etc/sudoers.d/molin-g8-test-readonly-audit` | `root:root / 0440` |

审计器在特权模式下会验证自身真实路径和 `root:root:755`，否则以退出码 42 失败关闭。对账器只有满足同样的 root 所有权和权限才会运行；它只从环境文件读取 `MYSQL_PASSWORD`，并要求 `MYSQL_USER/MYSQL_DATABASE` 精确为 `molin`，实际连接固定到 `127.0.0.1:13306/molin`，防止用户可修改环境文件诱导特权进程向外部地址发送凭据。子进程仅接收上述固定 MySQL 配置以及 `APP_ENV=test`、`AI_GATEWAY_RECONCILE_READ_ONLY=YES`。

### 2.1 历史本地候选包（001 已消费，禁止再次生成或使用）

`infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py` 可从冻结提交 `c50f092339fcad79ca1262925480219db1755318` 生成全新本地目录。生成器必须由 `python -I` 启动，并在导入可替换模块前拒绝非隔离解释器；同时锁定唯一 ChangeId、源码树、审计器、sudoers、对账器摘要和对账器大小，任一来源或制品漂移均失败关闭，不能使用同一审批替换资产。它通过 `git archive` 固定源码，使用 Go 1.26.5 以及 `GOENV=off`、`GOWORK=off`、`GOTOOLCHAIN=local`、`GOOS=linux`、`GOARCH=amd64`、`CGO_ENABLED=0` 和 `-trimpath -buildvcs=false` 连续构建两次；输出仅包含审计器、sudoers 候选、对账器、低敏清单和 `SHA256SUMS`。失败时只清理本次创建的全新输出目录。

以下命令仅保留为 001 的历史设计证据，**不得再次执行**：

```bash
# 历史命令，禁止执行；001 已消费。
python -I infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py \
  --change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-001 \
  --source-commit=c50f092339fcad79ca1262925480219db1755318 \
  --output-dir=/absolute/new/g8-test-readonly-access-bundle

(cd /absolute/new/g8-test-readonly-access-bundle && sha256sum -c SHA256SUMS)
```

生成器不连接测试服务器，也不包含 SSH、SCP、sudo、安装、Docker 或服务控制命令；本地 Go 构建仍可能按标准模块配置读取依赖缓存或下载缺失依赖。历史 PASS 只证明当时候选包与冻结来源及摘要一致，不代表已上传、已安装或测试服运行态通过。001 已消费，未来新 ChangeId 必须重新冻结生成器身份、制品与授权，不得复用本节命令。

PR `#333` 已按 merge commit `69439c4c9b14c67bf8a17dd8822d80ecdc784a27` 合并。精确功能 HEAD `c0479f607c9dbd5713c9fbbde7b3fb83ac2a3adc` 的 CI run `31566629193` 为 9/9 SUCCESS；其中候选包回执 SHA-256 为 `14b7d8cd832f0b719031fcc93adbbb2208afe76d34383e63d51c44b044772b5a`。该回执只绑定历史 CI 临时目录内的 `SHA256SUMS`，不是测试服安装回执；001 已消费，禁止据此重新生成、上传或再次申请执行。未来新 ChangeId 必须按第 3.5 节重新冻结。

### 2.2 历史本地候选包（002 已消费，禁止上传或安装）

用户曾授权准备并安装 `CHG-G8-TEST-READONLY-ACCESS-20260812-002`。候选、CI 和独立门禁通过后，实际执行只到唯一一次只读 SSH 预检；machine-id 摘要命令因跨 shell 引号解析错误非零退出，随即停止。没有 SCP、root 控制台、安装、sudoers 修改或 self-test。002 已消费，禁止重试、上传或安装。

当前候选冻结事实：

- 来源提交：`50b3e2f9d18b38e7d4a91ebeb4f03c413ef33c44`。
- 来源树：`73fb652a1f86db84991c8745f8c10e1d2a255f29`。
- 审计器 SHA-256：`308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256`。
- sudoers SHA-256：`1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f`。
- 对账器 SHA-256：`37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1`，大小 `13066129` 字节。
- 本地 Windows/amd64 构建候选 `SHA256SUMS` 回执：`d6d07f7b4959e48f5ffe0e92ee4116cef55fe56f5318df6ae3f0d9c5350ee567`。
- 候选只包含 `SHA256SUMS`、对账器、审计器、低敏 `manifest.env` 和 sudoers 候选五个文件。

本地回执绑定历史本地候选；CI 的 Linux 回执为 `7ae580cc06fb101fe44c9e3a4d7581116fd258ef1e2d09d99bba0bda50151a1f`。两者均只用于复现审计，不得安装。历史授权与停止证据见 `docs/ai-gateway-g8-test-readonly-access-install-authorization-20260812-002.md` 和 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812-002.md`。

首次本地构建的清单未包含部署根目录，已移动为明确的 `superseded-without-deployment-root` 取证目录；第二次构建把新字段错误地带入历史 001 复现路径，已移动为 `superseded-shared-manifest-field`。最终 002 候选将 `TARGET_DEPLOYMENT_ROOT=/home/pc/molin` 限定为 002 专属字段，并保持 001 历史清单语义不漂移。所有 002 回执以及旧回执 `704e3f99b31865ec9849a5ebc31dc572bd103d8e9a88ef812c198998114cf5c7`、`4826429551a15a7e78c2836c5e755150c68ea3e5fedc7ef87f2f6656bf622b32` 均不得用于安装。

### 2.3 已消费候选包（003，禁止重放）

003 重新冻结为：来源提交 `8ec878572f62ef2584c38aaadc1bca1cb802b13f`、来源树 `988bdcdc8017322264733ebe68876e4811b01412`、本地 `SHA256SUMS` 回执 `82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d`；三项制品摘要和对账器大小保持第 2.2 节批准值。候选仍只有五文件，部署根固定为 `/home/pc/molin`。

`infra/scripts/run-ai-gateway-g8-test-readonly-access-stage.py` 必须以 `python -I` 运行。它在联网前核对 003 候选、来源、回执、五文件、known_hosts 指纹和同目录显式 ED25519 密钥对，再通过固定系统 OpenSSH 发起恰好一次 SSH。子进程使用最小环境且不继承代理、AskPass 或调用方 PATH；SSH 禁止隐式密钥发现、密码、键盘交互、代理、X11、本地命令和端口转发。远端脚本固定 `/usr/bin` 绝对命令，只经 stdin 交给固定 `/bin/sh -s`，不作为 SSH 命令参数参与 Windows 引号重构；摘要提取使用 POSIX 参数展开，不使用 `cut`、`awk` 或嵌套引号。预检完整 PASS 后，包装器才以相同身份和最小环境调用固定 SFTP 一次；批处理首条为不带忽略前缀的 `mkdir`，目录已存在即失败，不合并或覆盖。任一步非零、超时、预检 stderr、额外 stdout、不安全部署根权限或身份漂移均固定低敏失败且绝不重试。

003 历史安装清单见 `docs/ai-gateway-g8-test-readonly-access-install-authorization-20260812-003.md`。该授权已经唯一一次正式包装器调用消费，固定结果为 `remote_stage_failed`；随后未进入 root 控制台、未安装 live 目标、未修改 sudoers 或执行 self-test。由于结果无法区分 SSH 与 SFTP，暂存目录及部分上传状态为 `UNKNOWN`。禁止重试、继续上传或按历史清单安装，完整记录见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812-003.md`。

### 2.4 已消费并停止的只读暂存取证（004）

`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004` 只用于关闭 003 暂存状态 `UNKNOWN`。`infra/scripts/run-ai-gateway-g8-test-staging-evidence.py` 先离线核对固定 known_hosts、显式 ED25519 密钥对和 OpenSSH ACL，再以最小环境、禁用密码/代理/转发/TTY 的固定 OpenSSH 发起至多一次 SSH。远端仅通过 stdin 执行 `/usr/bin/python3 -I -` 的固定只读程序，不接受远端路径参数。

远端程序先核对登录用户、hostname、machine-id 摘要和部署根真实路径/属主/权限。003 暂存路径不存在时只输出 `ABSENT`；存在时只读取固定五文件的白名单、普通文件/非链接、`pc:pc`、组和其他用户不可写、大小及 SHA-256。全部匹配输出 `PRESENT/PASS`；路径、文件集、文件元数据、内容或读取状态不符时输出固定 `PRESENT/MISMATCH` 类别并以退出码 3 阻断后续动作。输出不包含文件内容、动态路径、实际属主数值、权限值、stderr 或 Secret。

该候选不包含 SFTP、SCP、下载、删除、sudo、Docker、数据库、队列、服务或 HTTP 能力。用户批准后，本地检查 PASS，唯一正式调用返回 `remote_evidence_failed` 并按停止条件零重试结束；未形成远端状态证据，暂存仍为 `UNKNOWN`。004 已消费，禁止再次连接或重放。读取和 SSH 可能由操作系统产生 sshd/journald/audit 日志，并可能按文件系统策略更新 atime；不得表述为操作系统层绝对零写入。授权与执行记录见 `docs/ai-gateway-g8-test-readonly-staging-evidence-authorization-20260812-004.md`、`docs/ai-gateway-g8-test-readonly-staging-evidence-attempt-20260812-004.md`。

## 3. 017 已消费安装尝试与历史停止记录

015 已在唯一获批本地段中出现 PowerShell 正则错误并消费；下游影响保持 `UNKNOWN`。016 经 PR #381 合入 main 并完成合并后冻结摘要复核，用户随后独立批准唯一执行；人工第一段在交互 PowerShell 解析 `Get-FileHash` 时以终止错误停止，错误位于唯一 SSH 调用之前，SSH、sudo、安装和远端影响均为 0，016 已消费并墓碑化。017 使用新 ChangeId `CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-017`，仍只以 014 已证明 `PRESENT / PASS / NONE` 的 011 暂存为输入，并以纯 .NET 流式 SHA-256 消除模块自动加载依赖，同时关闭加密私钥提示、低敏输出和 HUP/TERM/INT 回滚边界。PR #384 最终 HEAD `ee947fd61919215500ef516488d56e01ad2ea72d` 通过 CI run `31791430839` 与三方零缺陷复评，按 merge commit `e2a7e4f89c4115b3e32dc27292b0bc11d7d09a57` 合入 main，合并后原始 Git blob 与冻结命令复核一致。用户随后独立批准唯一执行；人工本地段返回固定 `local_gate_failed` 并退出 2，事后 SSH 前同构门禁通过，但本地审计不能证明当时是否启动 `ssh.exe`。远端第二段未粘贴，sudo、安装器、post-check 和业务影响均为 0；017 以 `CONSUMED_LOCAL_GATE_FAILED_SSH_REACHABILITY_UNKNOWN` 消费并墓碑化。完整证据见 `docs/ai-gateway-g8-test-readonly-access-install-attempt-20260814-016.md`、`docs/ai-gateway-g8-test-readonly-access-install-attempt-20260814-017.md` 与 `docs/ai-gateway-g8-test-readonly-access-install-authorization-20260814-017.md`。

## 3.1 018 与 019 失败关闭记录

018 的唯一人工本地段窗口直接关闭且无可见输出；SSH 启动/连接保持 `UNKNOWN / 最多 1`，远端固定段、sudo、安装器和 post-check 均为 0。018 已失败关闭消费并墓碑化，禁止重试或重放。019 使用独立 ChangeId `CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-019`，以唯一 `ssh -tt` 自动携带无秘密 Base64 远端脚本；PR #390、CI run `31829691838`、独立评审、main merge commit `70485d893fd86db00be4dbb9e324f9d4322d55b0` 和合并后摘要复核均通过。用户精确授权后，本地门禁和冻结摘要通过，唯一可见 PowerShell 最终在恢复 `$ErrorActionPreference` 时因保存值为 `Null` 失败；窗口固定标志不可恢复，SSH、远端预检、sudo、安装器与 post-check 均保持 `UNKNOWN / 最多 1`。019 已失败关闭消费并墓碑化，禁止再次授权、重试或重放；见 `docs/ai-gateway-g8-test-readonly-access-install-attempt-20260815-018.md`、`docs/ai-gateway-g8-test-readonly-access-install-attempt-20260815-019.md` 与 `docs/ai-gateway-g8-test-readonly-access-install-authorization-20260815-019.md`。

### 3.1 历史已停止安装变更（禁止执行）

已消费 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-20260812-001`。本节保留原批准计划用于审计，所有命令均已作废并禁止执行。

当前已冻结的候选制品证据如下；管理员通道仍须由用户单独指定并确认：

- 源码合并提交：`c50f092339fcad79ca1262925480219db1755318`。
- 源码树：`2e9701c3f5d8ba12aebc9631b01696b189f1d313`，与功能提交树一致。
- 审计脚本 SHA-256：`308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256`。
- sudoers 文件 SHA-256：`1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f`。
- 对账器构建：`go1.26.5 windows/amd64` 交叉构建 Linux amd64，`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`，参数 `-trimpath -buildvcs=false`。
- 对账器 SHA-256：`37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1`，大小 `13066129` 字节；连续两次独立构建摘要一致。
- `<ADMIN_CHANNEL>`：可执行 root 命令的受控运维通道；不得在聊天或命令参数中传递 sudo 密码。

### 3.2 精确目标

- 主机：`pc@8.130.9.163:10003`
- hostname 基线：`pc-Z790-UD-AX`
- machine-id SHA-256 基线：`b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4`
- SSH ED25519 指纹基线：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`
- 部署目录：`/home/pc/molin`

任一身份不一致立即停止，禁止安装。

### 3.3 历史命令摘要（未执行，禁止重放）

1. 在本地从源码提交 `c50f092339fcad79ca1262925480219db1755318` 按上述参数重新构建 Linux amd64 只读对账器，或使用第 2.1 节生成器生成候选包；无论采用哪种方式，均要求三份资产 SHA-256 与冻结值精确一致。
2. 通过单次 SCP 将两个资产和 sudoers 文件上传到 `/home/pc/molin/.g8-staging/CHG-G8-TEST-READONLY-ACCESS-20260812-001/`；不覆盖运行文件。
3. 管理员通过 `<ADMIN_CHANNEL>` 逐项复核暂存文件 SHA-256、sudoers 内容和目标身份。
4. 使用 `install -d -o root -g root -m 0755 /usr/local/libexec/molin` 创建固定目录。
5. 使用 `install -o root -g root -m 0755` 安装审计器和对账器；使用 `install -o root -g root -m 0440` 安装 sudoers 文件。
6. 执行 `visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit`；再执行 `sudo -n -l -U pc`，确认只新增固定审计器命令，没有 `SETENV`、通配符、Shell 或 Docker 命令；执行 `id -nG pc` 确认不存在 `docker` 组。
7. 只执行审计器 `--self-test`；本 ChangeId 不执行真实审计，不读取业务数据。

### 3.4 影响、上限与回滚

- 影响范围：测试服务器新增两个 root-owned 可执行文件和一个 sudoers 文件；不改变现有容器、服务、数据库、队列、环境文件或流量。
- 最大 SSH/SCP 会话：上传 1 次、管理员安装会话 1 次、非特权核验会话 1 次。
- 最大业务请求：0；最大上游请求：0；最大费用：0 CNY。
- 回滚：管理员精确删除上述三个安装目标，并执行 `visudo -c` 与 `sudo -n -l -U pc` 确认规则消失；不得删除任何账本、Usage、钱包、Outbox、审计或备份事实。
- 停止条件：目标身份不一致、任一 SHA 不一致、暂存目录或父目录可疑、安装目标不是 root 所有、`visudo` 失败、规则出现额外命令/参数能力、self-test 失败，或发现真实密钥输出。

上述上传与安装命令均未执行，且不得使用 001 重放。

### 3.5 首次授权执行记录

`CHG-G8-TEST-READONLY-ACCESS-20260812-001` 已于 2026-08-12 执行一次只读前置检查：本机固定 ED25519 指纹匹配，但首个远端命令 `sudo -n -l` 返回“需要密码”。该结果触发停止条件，未上传、安装或修改任何测试服资产，ChangeId 已消费且禁止重放。若继续必须使用新的 ChangeId 和经用户明确指定的受控 root 管理通道，见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812.md`。

### 3.6 当前重新申请顺序

001 至 019 均已消费。014 在修复 Windows 可信系统目录缺陷并完成工程门禁后，获得独立用户授权；唯一一次本地诊断 PASS，唯一一次只读 SSH 返回 `PRESENT / PASS / NONE`，把 011 暂存从 `UNKNOWN` 收敛为存在且五文件、manifest 与回执完整。014 结果与墓碑已由 PR #379 按 merge commit `97ee6037cafa90577be619fc67e78866c4d75efe` 合入 main；015 的本地正则错误与下游 `UNKNOWN`、016 的本地模块错误与远端零触达、017 的统一低敏本地失败、018 的无输出窗口关闭，以及 019 的 PowerShell 状态恢复失败与执行到达边界 `UNKNOWN` 均已归档。当前顺序为：

1. 011、012、013、014 的包装器、命令生成器、历史命令和授权均保持消费态，禁止重放。
2. 011 暂存存在性和完整性已关闭，无需再次诊断；不得把该结论外推为 live 入口或运行态可用。
3. 015 已在独立用户授权后的唯一人工本地段中出现 PowerShell 路径正则错误；由于该错误默认非终止，身份材料读取、SSH、sudo、root-only 副本和 live 安装均保持 `UNKNOWN`，重试 0，015 已消费并墓碑化。
4. 016 在独立用户授权后的唯一人工第一段因 `Get-FileHash` 模块解析错误终止；控制流没有到达 SSH，远端影响为 0。016 已消费并墓碑化。
5. 017 在独立用户授权后的唯一人工本地段返回统一低敏 `local_gate_failed` 并退出 2；现有证据不能区分 SSH 前瞬时失败与 SSH 非零返回，因此 SSH 启动/连接保持 `UNKNOWN / 最多 1`。
6. 017 的远端第二段未粘贴，sudo、安装器、post-check、业务请求、上游请求和费用均为 0；安装未确认，017 已失败关闭消费并墓碑化。
7. 当前只可为新的 ChangeId 完成工程候选；必须先让低敏结果可区分 SSH 前门禁和 SSH 调用失败，017 的历史批准、工程合并或生成命令均不构成新授权。
8. 018 已完成工程合并；独立授权后的唯一人工段关闭父窗口且无可见输出，SSH 到达保持未知，所有远端安装影响为 0，018 已消费并墓碑化。
9. 019 改为单次 `ssh -tt` 自动携带远端固定脚本；独立授权后的唯一执行在 PowerShell 状态恢复阶段失败，固定标志不可恢复，SSH 与安装链路到达保持 UNKNOWN，019 已消费并墓碑化。
10. 020 使用独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-020`，不安装受控入口或 sudoers，不使用 sudo，候选经 PR #394、CI run `31861762018`、merge `3c63539279a34ae2365fc9d7e26e207dd728c4ba` 和合并后摘要复核通过。用户独立授权后，本地生成与冻结摘要 PASS，但外层 PowerShell 包装在整段解析时因缺少右括号失败，未调用 Windows PowerShell 5.1、冻结命令或 SSH。SSH、Docker/HTTP/数据库查询、sudo、安装、宿主写入、业务/上游/费用均为 0，重试 0；020 已消费并墓碑化，禁止重试或重放。`G8_SOFTWARE_CLOSED_LOOP` 仍未完成。
11. 020 工程合并和本次失败尝试均不证明运行态通过；020 已永久消费，不得再次授权、重试或重放。若继续，只能为新的独立 ChangeId 重新完成工程、摘要与用户精确授权。测试候选部署、Fake 旅程、零差额对账和实际回滚继续分开授权。
12. API、数据库、Bifrost、监控、备份和账务 UNKNOWN/P1 不因本地测试、安装候选或暂存三态自动关闭。
13. 021 使用新的独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-021` 修复本地启动链路：固定 Python 入口从工程 merge 原始 blob 复核生成器、审计源和自身，在内存中生成并核对命令后只启动一次可信 Windows PowerShell 5.1；工程阶段只运行假 SSH 与断网测试。PR #398 已合并为 `8bc05cbf3bc71a8954087dc7f26732f836e5212e`，合并后摘要复核无漂移。唯一授权尝试固定结果为 `CONSUMED_LOCAL_RECEIPT_UNAVAILABLE_SSH_NOT_STARTED`：PowerShell 启动一次后因本地耐久回执不可用在 SSH 前失败关闭，`PRE_SSH_GATE`、`SSH_ATTEMPTED`、SSH 与远端命令均为 0，重试为 0。021 已永久消费并墓碑化，不得再次授权、重试或重放；若继续必须使用新的独立 ChangeId，`G8_SOFTWARE_CLOSED_LOOP` 仍未完成。
14. 022 使用新的独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-022` 修复 021 的耐久回执缺口。PR #401 已以 merge commit `84ae5b0ad87958ee63fbfa709c4f164baca39a1b` 合入 main，合并后摘要复核一致。唯一授权调用在 PowerShell 启动后因 `identity_pair_failed` 于 SSH 前失败关闭；消费归档 TDD 错误触发一次未授权本地重放，亦在相同门禁停止。固定状态为 `CONSUMED_LOCAL_IDENTITY_PAIR_FAILED_SSH_NOT_STARTED`：本地正式入口/PowerShell 总调用 `2 / 2`、未授权本地重放 `1`，两份非空耐久回执都没有 `PRE_SSH_GATE`/`SSH_ATTEMPTED`，SSH 与全部远端能力为 0。发现偏差后两个入口立即永久墓碑化；022 已永久消费，不得再次授权、重试或重放，若继续只能使用新的独立 ChangeId，`G8_SOFTWARE_CLOSED_LOOP` 仍未完成。
15. 023 使用新的独立 ChangeId `CHG-G8-TEST-READONLY-RUNTIME-AUDIT-DROP-20260815-023`，移除固定客户端私钥、公钥和指纹配对门禁，使用开发机现有 OpenSSH 免交互认证链。唯一授权调用形成 `PRE_SSH_GATE=PASS` 与 `SSH_ATTEMPTED=YES`，随后以 `ssh_session_failed` 非零停止且零重试。固定状态为 `CONSUMED_SSH_SESSION_FAILED_REMOTE_AUDIT_NOT_PROVEN`：SSH 调用 `1`、会话成功 `0`，远端固定脚本与 Docker 只读查询为 `UNKNOWN / 最多启动 1 次`，没有 `COLLECTION_PASS`；其余未授权能力均为 0。023 已永久消费并墓碑化，不得再次授权、重试或重放。
16. 024 使用新的独立 ChangeId `CHG-G8-TEST-READONLY-SSH-DIAGNOSTIC-20260816-024`，只诊断固定 SSH 连接与固定 `printf` 回执。它允许当前用户 OpenSSH 认证配置生效，但用命令行重新固定目标、端口、host key、BatchMode、禁密码/代理/转发/TTY、单会话与零重试；原始 stderr 只在本机内存有界映射为固定低敏原因。当前为 `PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`；本轮禁止执行 024、SSH 或任何测试服操作。

`CHG-G8-TEST-READONLY-TRANSPORT-DIAG-20260812-005` 已完成唯一一次本地检查和正式只读 SSH，结果为 `ZERO / EXACT / stderr EMPTY / diagnostic PASS`；005 已消费并禁止重放。该历史结果当时只证明传输链路可用，暂存 UNKNOWN 后续已由 014 收敛为 `PRESENT / PASS / NONE`。授权与执行记录见 `docs/ai-gateway-g8-test-readonly-transport-diagnostic-authorization-20260812-005.md`、`docs/ai-gateway-g8-test-readonly-transport-diagnostic-attempt-20260812-005.md`。

`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006` 通过 PR #345、CI 12/12 和三方独立门禁后合入主干。用户批准后，本地检查 PASS；唯一正式只读 SSH 返回 `BLOCKED / MACHINE_ID` 并零重试停止。暂存查找未执行，003 暂存继续为 `UNKNOWN`。006 已消费，禁止重放；授权与执行记录见 `docs/ai-gateway-g8-test-readonly-staging-evidence-authorization-20260812-006.md`、`docs/ai-gateway-g8-test-readonly-staging-evidence-attempt-20260812-006.md`。

`CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007` 完成工程门禁后，用户批准并执行唯一一次本地检查和正式只读 SSH：本地检查 PASS，正式结果为 `BLOCKED / READABLE_MISMATCH`，随后零重试停止；不输出当前 machine-id 原文或摘要，也未读取 003 暂存目录。007 已消费；按 007 执行时的停止条件，后续原本要求使用新 ChangeId 完成独立受控来源核验。此要求属于 Drop 映射确认前的历史规则，不再是 008 的前置门禁；现行顺序以本节清单和下段为准。历史授权与执行记录见 `docs/ai-gateway-g8-test-readonly-host-identity-diagnostic-authorization-20260812-007.md`、`docs/ai-gateway-g8-test-readonly-host-identity-diagnostic-attempt-20260813-007.md`。

后续确认该地址由 Drop 服务映射，底层物理主机身份不属于固定 SSH 入口契约。007 的 `READABLE_MISMATCH` 只作为历史事实保留，不再登记为当前测试服运行态 P1，也不得据此自动更新任一摘要。008 使用 ChangeId `CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008`，只验证固定 known_hosts/客户端密钥、登录用户 `pc`、部署根和 003 五文件；禁止读取 hostname、machine-id、实例元数据或 CMDB。008 已完成唯一一次本地检查和唯一一次只读 SSH，结果为 `ABSENT / NOT_APPLICABLE / NONE`、stderr 为空、零重试、业务请求/上游请求/费用为 `0 / 0 / 0 CNY`；003 暂存 `UNKNOWN` 已关闭为 `ABSENT`，008 已消费。授权清单与执行记录见 `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-008.md`、`docs/ai-gateway-g8-test-readonly-drop-staging-evidence-attempt-20260813-008.md`。

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009` 已消费。用户批准后唯一一次本地检查 PASS；唯一正式调用在创建 Windows 冻结私钥副本时以 `invalid_request`、退出码 2 停止。离线最小复现确认临时副本仅经 `chmod 0600`，没有收紧 NTFS ACL，固定 `ssh-keygen -y` 因权限诊断退出 255；代码顺序证明该失败发生在 SSH/SFTP 调用之前。没有连接测试服务、创建远端暂存、上传文件、进入 root 管理通道、安装 live 目标或执行 sudo self-test，业务请求/上游请求/费用为 `0 / 0 / 0 CNY`。009 禁止重放。消费证据 HEAD `bab8f89a317f9bcb7ca1fd1f534f3fa6a9545f49` 经 CI run `31667550392` 12/12 SUCCESS 及独立三方零缺陷后，由 PR #360 按 merge commit `c9402d94129da4042e3fb1bb978d63018af4a439` 合入主干；远端功能分支已删除。009 当时的私钥冻结修复要求已由后续用户批准的 010 直连方案替代。证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260813-009.md`。

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010` 已消费。用户授权后一次本地检查、一次只读 SSH 与一次原子 SFTP 均 PASS，五文件暂存成功；唯一 root 安装编排在本地参数构造阶段停止，未建立 root 连接、未发送安装脚本、未创建 root-only/live/sudoers 目标，也未执行 visudo、sudo 范围、Docker 组或固定 self-test。零重试，业务/上游/费用为 `0 / 0 / 0 CNY`。状态为 `CONSUMED_STAGED_ROOT_NOT_RUN`，禁止重放；直接改用 `pc` 不满足 root-owned 和 sudoers 契约，未执行。暂存清理、root 安装或新的 `pc` 非特权方案均须新 ChangeId、重新工程门禁和独立用户授权。证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260813-010.md`。

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011` 已消费。用户批准后唯一一次 local-check 为 PASS；唯一正式暂存包装器调用以 `invalid_request`、退出码 2、stderr 为空停止并零重试。当时低敏失败无法区分 SFTP 是否启动、远端独占建目录是否成功或五文件是否部分上传；后续 014 已通过独立只读取证把该暂存收敛为 `PRESENT / PASS / NONE`。011 的交互 SSH、sudo 认证、root 安装、`visudo`、sudo 范围、Docker 组和 self-test 从未执行；没有 live 目标需要回滚。011 禁止重放；015 与 016 都只把已证明完整的 011 暂存作为冻结输入且现已消费，017 继续使用同一只读来源但采用新的独立 ChangeId。历史证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260813-011.md`，当前暂存证据见 `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-attempt-20260814-014.md`。

`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260814-014` 已完成唯一一次本地诊断和唯一一次只读 SSH，结果为 `PRESENT / PASS / NONE`、退出码 0、重试 0；014 已消费并墓碑化。该结果只关闭 011 暂存存在性与完整性，不授权清理、安装或运行态审计。结果归档见 `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-attempt-20260814-014.md`。

`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-015` 已消费。其工程候选由 PR #380 合入 main 后获得独立用户授权；唯一人工本地段在可信 Windows 路径正则处报告末尾反斜杠非法，但该错误默认非终止，身份材料读取、SSH、sudo、安装和 root-only/live/sudoers 状态均不能由错误顺序证明，保持 `UNKNOWN`；四项成功标志均未形成。事后单独打印的 `$LASTEXITCODE=0` 可能来自此前任一原生程序，不能证明成功或 SSH 为 0。015 禁止重放，修复版必须使用 016 新 ChangeId。证据见 `docs/ai-gateway-g8-test-readonly-access-install-attempt-20260814-015.md`。

`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016` 已消费。用户独立批准后，唯一人工第一段在交互 PowerShell 的 `Get-FileHash` 处返回 `CommandNotFoundException`；`$ErrorActionPreference='Stop'` 使控制流在唯一 SSH 调用之前终止，SSH、sudo、安装、业务请求、上游请求和费用均为 0。016 禁止重放；017 以纯 .NET 流式 SHA-256 替代模块 cmdlet，并须重新完成工程门禁和独立授权。证据见 `docs/ai-gateway-g8-test-readonly-access-install-attempt-20260814-016.md`。

## 4. 020 无安装独立只读核验

001 至 019 的 root-owned 审计器、sudoers 与 `sudo -n /usr/local/libexec/...` 路径均为已停止的历史方案，禁止用于 020。020 当时只允许在工程合并、原始 blob/命令摘要复核和用户独立授权后，由 `pc` 通过一次 `ssh -T` 执行冻结内存脚本；该唯一尝试现已在 SSH 前失败关闭消费，020 不得再次授权、重试或重放。

远端脚本只允许固定 Docker/宿主/本机 HTTP/数据库只读查询。hostname、machine-id、密码状态与 Docker 组枚举不属于 Drop 入口运行态验收范围，020 不读取也不输出。全部低敏结果先保存在会话内存；脚本非零、必需探针出现 `UNAVAILABLE/MISSING/INVALID/000`、空值、缺少固定必需键或缺少 `AUDIT_COMPLETE=true` 时，固定返回 `audit_evidence_failed` 并结束唯一会话，零重试。`COLLECTION_PASS` 仅表示证据采集完整，不表示证据值已满足 G8 验收。

## 5. 020 运行态验收标准

- `pc` 直接使用既有 Docker 权限；Docker 权限接近宿主 root，因此实际命令必须保持冻结白名单，任何容器创建、启停、删除、复制、compose、网络/卷变更、宿主 bind mount 或任意参数入口均失败关闭。
- 不存在 020 安装目标或 sudoers 验收项；固定 011 暂存对账器只按批准路径、`pc:pc:700`、大小与 SHA-256 执行只读模式。
- schema 精确为批准版本且 `dirty=0`；MySQL/Redis/RabbitMQ/Bifrost/监控事实不再是 UNKNOWN。Bifrost 两节点与 LB 必须输出镜像摘要、健康和仅变量名的运行时注入集合；审计器在内存中按完整 `KEY=value` 从容器环境扣除相同镜像的基础环境，再只输出差异键名并与专用变量白名单精确比较。同名变量被容器覆盖也属于运行时注入；任何额外业务 Secret 或缺失必需变量均失败关闭。
- 22 条告警、16 个 Grafana 面板和 Alertmanager discard/受控路由均可核对，不发送真实通知。
- 三项正常账务差额、七类异常、未释放 hold、Outbox 和补偿积压全部为 0；任一非零均失败关闭。
- 备份仅验证可读性和摘要；共享数据库恢复仍禁止。

达到以上标准只表示测试服只读证据补齐，不代表测试服关闭态部署完成，更不代表生产上线或 `G8_COMMERCIAL_ACCEPTED`。
