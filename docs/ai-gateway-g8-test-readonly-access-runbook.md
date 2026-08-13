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

## 3. 历史已停止安装变更（禁止执行）

已消费 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-20260812-001`。本节保留原批准计划用于审计，所有命令均已作废并禁止执行。

当前已冻结的候选制品证据如下；管理员通道仍须由用户单独指定并确认：

- 源码合并提交：`c50f092339fcad79ca1262925480219db1755318`。
- 源码树：`2e9701c3f5d8ba12aebc9631b01696b189f1d313`，与功能提交树一致。
- 审计脚本 SHA-256：`308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256`。
- sudoers 文件 SHA-256：`1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f`。
- 对账器构建：`go1.26.5 windows/amd64` 交叉构建 Linux amd64，`GOOS=linux GOARCH=amd64 CGO_ENABLED=0`，参数 `-trimpath -buildvcs=false`。
- 对账器 SHA-256：`37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1`，大小 `13066129` 字节；连续两次独立构建摘要一致。
- `<ADMIN_CHANNEL>`：可执行 root 命令的受控运维通道；不得在聊天或命令参数中传递 sudo 密码。

### 3.1 精确目标

- 主机：`pc@8.130.9.163:10003`
- hostname 基线：`pc-Z790-UD-AX`
- machine-id SHA-256 基线：`b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4`
- SSH ED25519 指纹基线：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`
- 部署目录：`/home/pc/molin`

任一身份不一致立即停止，禁止安装。

### 3.2 历史命令摘要（未执行，禁止重放）

1. 在本地从源码提交 `c50f092339fcad79ca1262925480219db1755318` 按上述参数重新构建 Linux amd64 只读对账器，或使用第 2.1 节生成器生成候选包；无论采用哪种方式，均要求三份资产 SHA-256 与冻结值精确一致。
2. 通过单次 SCP 将两个资产和 sudoers 文件上传到 `/home/pc/molin/.g8-staging/CHG-G8-TEST-READONLY-ACCESS-20260812-001/`；不覆盖运行文件。
3. 管理员通过 `<ADMIN_CHANNEL>` 逐项复核暂存文件 SHA-256、sudoers 内容和目标身份。
4. 使用 `install -d -o root -g root -m 0755 /usr/local/libexec/molin` 创建固定目录。
5. 使用 `install -o root -g root -m 0755` 安装审计器和对账器；使用 `install -o root -g root -m 0440` 安装 sudoers 文件。
6. 执行 `visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit`；再执行 `sudo -n -l -U pc`，确认只新增固定审计器命令，没有 `SETENV`、通配符、Shell 或 Docker 命令；执行 `id -nG pc` 确认不存在 `docker` 组。
7. 只执行审计器 `--self-test`；本 ChangeId 不执行真实审计，不读取业务数据。

### 3.3 影响、上限与回滚

- 影响范围：测试服务器新增两个 root-owned 可执行文件和一个 sudoers 文件；不改变现有容器、服务、数据库、队列、环境文件或流量。
- 最大 SSH/SCP 会话：上传 1 次、管理员安装会话 1 次、非特权核验会话 1 次。
- 最大业务请求：0；最大上游请求：0；最大费用：0 CNY。
- 回滚：管理员精确删除上述三个安装目标，并执行 `visudo -c` 与 `sudo -n -l -U pc` 确认规则消失；不得删除任何账本、Usage、钱包、Outbox、审计或备份事实。
- 停止条件：目标身份不一致、任一 SHA 不一致、暂存目录或父目录可疑、安装目标不是 root 所有、`visudo` 失败、规则出现额外命令/参数能力、self-test 失败，或发现真实密钥输出。

上述上传与安装命令均未执行，且不得使用 001 重放。

### 3.4 首次授权执行记录

`CHG-G8-TEST-READONLY-ACCESS-20260812-001` 已于 2026-08-12 执行一次只读前置检查：本机固定 ED25519 指纹匹配，但首个远端命令 `sudo -n -l` 返回“需要密码”。该结果触发停止条件，未上传、安装或修改任何测试服资产，ChangeId 已消费且禁止重放。若继续必须使用新的 ChangeId 和经用户明确指定的受控 root 管理通道，见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812.md`。

### 3.5 当前重新申请顺序

001、002、003、004、005、006、007、008 均已消费。008 的唯一正式只读 SSH 已返回 `ABSENT / NOT_APPLICABLE / NONE`，把固定 003 暂存状态从 `UNKNOWN` 收敛为 `ABSENT`。Drop 映射下旧的物理主机身份核验顺序不再适用；当前必须依次完成：

1. 008 已按独立用户授权完成一次本地检查和一次只读 SSH，零重试；结果为 `ABSENT/NOT_APPLICABLE/NONE`，全部历史命令作废并禁止重放。
2. 固定 003 暂存目录不存在，因此未执行也无需执行清理；不得为“确认结果”再次连接或扩大读取范围。
3. 若继续准备安装，必须使用新 ChangeId 重新冻结安装候选、制品回执和授权，不得复用 001、002、003 或 008 的候选、回执和授权。
4. 安装后的真实运行态审计仍使用另一个独立 ChangeId，后续安装授权不得顺带执行；API、数据库、Bifrost、监控、备份和账务 UNKNOWN/P1 不因暂存目录不存在而自动关闭。

`CHG-G8-TEST-READONLY-TRANSPORT-DIAG-20260812-005` 已完成唯一一次本地检查和正式只读 SSH，结果为 `ZERO / EXACT / stderr EMPTY / diagnostic PASS`，证明传输链路可用但未关闭暂存 UNKNOWN；005 已消费并禁止重放。授权与执行记录见 `docs/ai-gateway-g8-test-readonly-transport-diagnostic-authorization-20260812-005.md`、`docs/ai-gateway-g8-test-readonly-transport-diagnostic-attempt-20260812-005.md`。下一次暂存只读取证必须使用新的 ChangeId，重新完成代码安全、QA、产品、精确 HEAD CI、merge commit 与用户独立授权。

`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006` 通过 PR #345、CI 12/12 和三方独立门禁后合入主干。用户批准后，本地检查 PASS；唯一正式只读 SSH 返回 `BLOCKED / MACHINE_ID` 并零重试停止。暂存查找未执行，003 暂存继续为 `UNKNOWN`。006 已消费，禁止重放；授权与执行记录见 `docs/ai-gateway-g8-test-readonly-staging-evidence-authorization-20260812-006.md`、`docs/ai-gateway-g8-test-readonly-staging-evidence-attempt-20260812-006.md`。

`CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007` 完成工程门禁后，用户批准并执行唯一一次本地检查和正式只读 SSH：本地检查 PASS，正式结果为 `BLOCKED / READABLE_MISMATCH`，随后零重试停止；不输出当前 machine-id 原文或摘要，也未读取 003 暂存目录。007 已消费；按 007 执行时的停止条件，后续原本要求使用新 ChangeId 完成独立受控来源核验。此要求属于 Drop 映射确认前的历史规则，不再是 008 的前置门禁；现行顺序以本节清单和下段为准。历史授权与执行记录见 `docs/ai-gateway-g8-test-readonly-host-identity-diagnostic-authorization-20260812-007.md`、`docs/ai-gateway-g8-test-readonly-host-identity-diagnostic-attempt-20260813-007.md`。

后续确认该地址由 Drop 服务映射，底层物理主机身份不属于固定 SSH 入口契约。007 的 `READABLE_MISMATCH` 只作为历史事实保留，不再登记为当前测试服运行态 P1，也不得据此自动更新任一摘要。008 使用 ChangeId `CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008`，只验证固定 known_hosts/客户端密钥、登录用户 `pc`、部署根和 003 五文件；禁止读取 hostname、machine-id、实例元数据或 CMDB。008 已完成唯一一次本地检查和唯一一次只读 SSH，结果为 `ABSENT / NOT_APPLICABLE / NONE`、stderr 为空、零重试、业务请求/上游请求/费用为 `0 / 0 / 0 CNY`；003 暂存 `UNKNOWN` 已关闭为 `ABSENT`，008 已消费。授权清单与执行记录见 `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-authorization-20260813-008.md`、`docs/ai-gateway-g8-test-readonly-drop-staging-evidence-attempt-20260813-008.md`。

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009` 已消费。用户批准后唯一一次本地检查 PASS；唯一正式调用在创建 Windows 冻结私钥副本时以 `invalid_request`、退出码 2 停止。离线最小复现确认临时副本仅经 `chmod 0600`，没有收紧 NTFS ACL，固定 `ssh-keygen -y` 因权限诊断退出 255；代码顺序证明该失败发生在 SSH/SFTP 调用之前。没有连接测试服务、创建远端暂存、上传文件、进入 root 管理通道、安装 live 目标或执行 sudo self-test，业务请求/上游请求/费用为 `0 / 0 / 0 CNY`。009 禁止重放。消费证据 HEAD `bab8f89a317f9bcb7ca1fd1f534f3fa6a9545f49` 经 CI run `31667550392` 12/12 SUCCESS 及独立三方零缺陷后，由 PR #360 按 merge commit `c9402d94129da4042e3fb1bb978d63018af4a439` 合入主干；远端功能分支已删除。009 当时的私钥冻结修复要求已由后续用户批准的 010 直连方案替代。证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260813-009.md`。

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010` 已消费。用户授权后一次本地检查、一次只读 SSH 与一次原子 SFTP 均 PASS，五文件暂存成功；唯一 root 安装编排在本地参数构造阶段停止，未建立 root 连接、未发送安装脚本、未创建 root-only/live/sudoers 目标，也未执行 visudo、sudo 范围、Docker 组或固定 self-test。零重试，业务/上游/费用为 `0 / 0 / 0 CNY`。状态为 `CONSUMED_STAGED_ROOT_NOT_RUN`，禁止重放；直接改用 `pc` 不满足 root-owned 和 sudoers 契约，未执行。暂存清理、root 安装或新的 `pc` 非特权方案均须新 ChangeId、重新工程门禁和独立用户授权。证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260813-010.md`。

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011` 采用已批准的方案 A：独立包装器未来只执行一次 SFTP；随后在唯一交互 SSH 会话内先完成非特权预检，再由操作者仅通过 TTY 执行一次 `sudo -k -v`，认证后只运行冻结的 `sudo -n` root 安装器和固定 self-test。密码不得进入参数、stdin、脚本、环境变量、日志或文档。PR #365 最终 HEAD `30cf58083088628c0ad8ac321cca3078f39b5341` 已通过 CI run `31685942115` 12/12 和独立三方 P0/P1/P2=0，并按 merge commit `018f7344a5a52ccc6c23b478555a7ddc02f5ba63` 合入主干；合并后的包装器、命令生成器与安装器摘要均未漂移。当前未连接测试服，状态为 `PENDING_USER_APPROVAL`；工程合并不授权执行，权威清单见 `docs/ai-gateway-g8-test-readonly-access-install-authorization-20260813-011.md`。

## 4. 安装后的独立只读核验

安装成功也不自动授权读取运行态。必须再申请新的 `CHG-G8-TEST-READONLY-YYYYMMDD-NNN`，固定为一次 SSH、零重试，并只执行：

```bash
sudo -n /usr/local/libexec/molin/g8-test-readonly-audit \
  --change-id=CHG-G8-TEST-READONLY-YYYYMMDD-NNN
```

输出必须只保存低敏聚合结果。出现目标身份不一致、`privileged_installation!=VERIFIED`、未知参数、真实 Secret、非只读 SQL、队列消费、服务信号或任何业务请求时立即停止。该核验仍不授权部署、重启、Migration、凭据轮换、付费上游、真实通知或客户灰度。

## 5. 验收标准

- `pc` 不属于 Docker 管理组，审计输出 `pc_docker_group_member=false`，且 `sudo -n -l -U pc` 仅出现固定审计命令。
- 审计器和对账器均为批准 SHA、`root:root:755`，sudoers 为 `root:root:440`。
- schema 精确为批准版本且 `dirty=0`；MySQL/Redis/RabbitMQ/Bifrost/监控事实不再是 UNKNOWN。Bifrost 两节点与 LB 必须输出镜像摘要、健康和仅变量名的运行时注入集合；审计器在内存中按完整 `KEY=value` 从容器环境扣除相同镜像的基础环境，再只输出差异键名并与专用变量白名单精确比较。同名变量被容器覆盖也属于运行时注入；任何额外业务 Secret 或缺失必需变量均失败关闭。
- 22 条告警、16 个 Grafana 面板和 Alertmanager discard/受控路由均可核对，不发送真实通知。
- 三项正常账务差额、七类异常、未释放 hold、Outbox 和补偿积压全部为 0；任一非零均失败关闭。
- 备份仅验证可读性和摘要；共享数据库恢复仍禁止。

达到以上标准只表示测试服只读证据补齐，不代表测试服关闭态部署完成，更不代表生产上线或 `G8_COMMERCIAL_ACCEPTED`。
