# AI 网关 G8 测试服最小只读运维入口 Runbook

## 1. 状态与范围

本文只定义测试服务器 `pc@8.130.9.163:10003` 的候选安装、核验和撤销步骤。当前仅完成仓库内资产，**尚未上传、安装或修改 sudoers**，也未再次连接测试服务器。

该入口用于补齐 MySQL、Redis、RabbitMQ、Bifrost、Prometheus、Grafana、Alertmanager、备份和只读账务证据。它不授予 `pc` Docker 组成员资格，不允许任意 `docker`、Shell、服务控制、文件写入、DDL/DML、队列消费或业务请求。

## 2. 固定资产

| 资产 | 安装目标 | 所有权 / 权限 |
|---|---|---|
| `infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh` | `/usr/local/libexec/molin/g8-test-readonly-audit` | `root:root / 0755` |
| Linux `ai-gateway-reconcile` | `/usr/local/libexec/molin/ai-gateway-reconcile` | `root:root / 0755` |
| `infra/sudoers/molin-g8-test-readonly-audit` | `/etc/sudoers.d/molin-g8-test-readonly-audit` | `root:root / 0440` |

审计器在特权模式下会验证自身真实路径和 `root:root:755`，否则以退出码 42 失败关闭。对账器只有满足同样的 root 所有权和权限才会运行；它只从环境文件读取 `MYSQL_PASSWORD`，并要求 `MYSQL_USER/MYSQL_DATABASE` 精确为 `molin`，实际连接固定到 `127.0.0.1:13306/molin`，防止用户可修改环境文件诱导特权进程向外部地址发送凭据。子进程仅接收上述固定 MySQL 配置以及 `APP_ENV=test`、`AI_GATEWAY_RECONCILE_READ_ONLY=YES`。

### 2.1 本地候选包

`infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py` 可从冻结提交 `c50f092339fcad79ca1262925480219db1755318` 生成全新本地目录。生成器必须由 `python -I` 启动，并在导入可替换模块前拒绝非隔离解释器；同时锁定唯一 ChangeId、源码树、审计器、sudoers、对账器摘要和对账器大小，任一来源或制品漂移均失败关闭，不能使用同一审批替换资产。它通过 `git archive` 固定源码，使用 Go 1.26.5 以及 `GOENV=off`、`GOWORK=off`、`GOTOOLCHAIN=local`、`GOOS=linux`、`GOARCH=amd64`、`CGO_ENABLED=0` 和 `-trimpath -buildvcs=false` 连续构建两次；输出仅包含审计器、sudoers 候选、对账器、低敏清单和 `SHA256SUMS`。失败时只清理本次创建的全新输出目录。

```bash
# 输出目录必须是当前平台的绝对路径且不得已存在。
python -I infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py \
  --change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-001 \
  --source-commit=c50f092339fcad79ca1262925480219db1755318 \
  --output-dir=/absolute/new/g8-test-readonly-access-bundle

(cd /absolute/new/g8-test-readonly-access-bundle && sha256sum -c SHA256SUMS)
```

生成器不连接测试服务器，也不包含 SSH、SCP、sudo、安装、Docker 或服务控制命令；本地 Go 构建仍可能按标准模块配置读取依赖缓存或下载缺失依赖。生成 PASS 只证明本地候选包与冻结来源及摘要一致，不代表已上传、已安装或测试服运行态通过；上传与安装仍必须使用本 Runbook 第 3 节的独立授权。

PR `#333` 已按 merge commit `69439c4c9b14c67bf8a17dd8822d80ecdc784a27` 合并。精确功能 HEAD `c0479f607c9dbd5713c9fbbde7b3fb83ac2a3adc` 的 CI run `31566629193` 为 9/9 SUCCESS；其中候选包回执 SHA-256 为 `14b7d8cd832f0b719031fcc93adbbb2208afe76d34383e63d51c44b044772b5a`。该回执绑定 CI 临时目录内的 `SHA256SUMS`，不是测试服安装回执；实际上传前仍须重新生成包、逐项核对本节冻结摘要并取得第 3 节授权。

## 3. 待批准安装变更

候选 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-20260812-001`。

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

### 3.2 命令摘要

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

未经用户对补全后的 ChangeId 独立确认，不得执行上述任何上传或安装命令。

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
