# AI 网关 G8 测试服只读入口安装授权清单（002）

> 当前状态：`CONSUMED_STOPPED_DURING_READONLY_PREFLIGHT`。用户曾批准本清单，但唯一 SSH 预检因远端命令解析错误非零退出；002 已消费，以下内容仅保留为历史审计证据，禁止重试、上传或安装。

## 1. ChangeId 与精确目标

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-20260812-002`。
- 测试服务器：`pc@8.130.9.163:10003`。
- hostname 基线：`pc-Z790-UD-AX`。
- machine-id SHA-256 基线：`b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4`。
- SSH ED25519 指纹：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`。
- 部署根目录：`/home/pc/molin`。
- 管理员通道：阿里云控制台 root 通道；不得在聊天或命令参数中提供密码。

## 2. 安装候选

| 文件 | 安装目标 | SHA-256 | 所有权 / 权限 |
|---|---|---|---|
| `g8-test-readonly-audit` | `/usr/local/libexec/molin/g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | `root:root / 0755` |
| `ai-gateway-reconcile` | `/usr/local/libexec/molin/ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | `root:root / 0755` |
| `molin-g8-test-readonly-audit.sudoers` | `/etc/sudoers.d/molin-g8-test-readonly-audit` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | `root:root / 0440` |

冻结来源提交为 `50b3e2f9d18b38e7d4a91ebeb4f03c413ef33c44`，来源树为 `73fb652a1f86db84991c8745f8c10e1d2a255f29`。本地候选 `SHA256SUMS` 回执为 `d6d07f7b4959e48f5ffe0e92ee4116cef55fe56f5318df6ae3f0d9c5350ee567`，清单同时绑定 `TARGET_DEPLOYMENT_ROOT=/home/pc/molin`；实际上传前必须再次核对所选候选目录的完整五文件白名单和 `SHA256SUMS`，禁止混用其他构建清单。旧回执 `704e3f99b31865ec9849a5ebc31dc572bd103d8e9a88ef812c198998114cf5c7` 与 `4826429551a15a7e78c2836c5e755150c68ea3e5fedc7ef87f2f6656bf622b32` 均属于废弃候选，禁止上传。

## 3. 历史批准命令摘要（已作废，禁止执行）

1. 使用既有固定 `known_hosts` 完成一次完全只读 SSH 预检；只核对登录用户、hostname、machine-id 摘要、`/home/pc/molin` 真实路径/所有权/权限、同名暂存目录不存在、三个安装目标均不存在。任一不符立即停止，禁止上传。
2. 只读预检通过后，使用单次递归 SCP 将五文件候选目录上传为全新暂存目录 `/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-002/`；其父目录 `/home/pc/molin` 已存在，不得预先创建、覆盖或合并同名暂存目录。
3. 通过阿里云控制台 root 通道再次只读核对 `id`、hostname、machine-id 摘要、`id -nG pc`、暂存目录真实路径/所有权/权限、五文件白名单和 `SHA256SUMS`。
4. 若三个安装目标任一已存在，立即停止，不覆盖；先对暂存 sudoers 执行 `visudo -cf`。
5. 使用 `install -d -o root -g root -m 0755 /usr/local/libexec/molin`，再按第 2 节目标、所有权和权限安装三个文件；管理员必须逐项记录本次实际新建的目标，供失败回滚使用。
6. 执行 `visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit`、`sudo -n -l -U pc` 和 `id -nG pc`；必须只出现固定审计器，且 `pc` 不属于 Docker 组。
7. 通过一次非特权核验会话只执行固定审计器 `--self-test`；不得执行真实运行态审计。

## 4. 上限与影响

- 最大会话：只读 SSH 预检 1 次、上传 1 次、管理员控制台 1 次、非特权核验 1 次。
- 最大业务请求：0。
- 最大上游请求：0。
- 最大费用：0 CNY。
- 影响范围：新增两个 root-owned 只读工具和一个单命令 sudoers 文件；不改变 API、容器、服务、环境文件、数据库、Redis、RabbitMQ、Bifrost、监控或流量。

## 5. 回滚

安装开始后任一步失败，管理员必须仅对本次操作日志中已确认新建的目标逐项逆序回滚；即使只创建一至两个目标也必须回滚已创建项。若 sudoers 已创建，先精确删除该文件并执行 `visudo -c`；再分别删除本次新建的对账器和审计器。随后执行 `sudo -n -l -U pc` 确认规则消失。暂存目录保留用于失败取证，未经新的删除授权不得清理。不得递归删除 `/usr/local/libexec/molin`、部署目录、账本、Usage、钱包、Outbox、审计、日志或备份。

## 6. 停止条件

出现以下任一情况立即停止：主机身份或指纹不一致、暂存路径已存在或可疑、文件白名单/摘要不一致、安装目标已存在、目标不是 root 所有、权限不符、暂存或安装后 `visudo` 失败、sudo 规则含额外命令/参数能力、`pc` 属于 Docker 组、self-test 失败、输出真实 Secret，或需要执行清单外命令。

实际执行只到第 1 步：本地候选和 known_hosts 门禁通过，但唯一 SSH 内的 machine-id 摘要命令因跨 shell 引号解析错误非零退出。第 2 至第 7 步均未执行，证据见 `docs/ai-gateway-g8-test-readonly-access-attempt-20260812-002.md`。本清单不得用于任何后续连接、上传或安装，也不授权真实运行态审计、服务重启、Migration、配置修改、凭据轮换、生产连接、付费上游、真实通知或客户灰度。
