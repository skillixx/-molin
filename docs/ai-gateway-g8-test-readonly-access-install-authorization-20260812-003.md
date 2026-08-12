# AI 网关 G8 测试服只读入口安装授权清单（003）

> 当前状态：`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。本文只是新的安装申请草案；在精确 PR HEAD 的 CI、独立代码安全、QA 和产品验收全部通过且用户再次明确批准前，禁止连接、上传或安装。

## 1. ChangeId 与精确目标

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-20260812-003`。
- 测试服务器：`pc@8.130.9.163:10003`。
- hostname：`pc-Z790-UD-AX`。
- machine-id SHA-256：`b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4`。
- SSH ED25519 指纹：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`。
- 部署根目录：`/home/pc/molin`，必须为 `pc:pc`，属主权限完整且组/其他用户不可写。
- 管理员通道：阿里云控制台 root 通道；不得在聊天、命令参数或日志中提供密码。

## 2. 冻结候选

| 文件 | 安装目标 | SHA-256 | 所有权 / 权限 |
|---|---|---|---|
| `g8-test-readonly-audit` | `/usr/local/libexec/molin/g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | `root:root / 0755` |
| `ai-gateway-reconcile` | `/usr/local/libexec/molin/ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | `root:root / 0755` |
| `molin-g8-test-readonly-audit.sudoers` | `/etc/sudoers.d/molin-g8-test-readonly-audit` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | `root:root / 0440` |

- 来源提交：`8ec878572f62ef2584c38aaadc1bca1cb802b13f`。
- 来源树：`988bdcdc8017322264733ebe68876e4811b01412`。
- Go：`go1.26.5`，Linux amd64，`CGO_ENABLED=0`，连续双构建摘要一致。
- 对账器大小：`13066129` 字节。
- 本地五文件候选目录：`D:\molingproject\g8-artifacts\CHG-G8-TEST-READONLY-ACCESS-20260812-003`。
- 本地 `SHA256SUMS` 回执：`82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d`。

001、002 及其全部回执均已消费，不得上传。实际执行只能使用上述 003 本地候选及完整五文件白名单，不得改用 CI 临时目录或其他构建清单。

## 3. 待批准命令摘要

1. 本地以 `python -I` 执行 `infra/scripts/run-ai-gateway-g8-test-readonly-access-stage.py`，绑定 003、上述候选绝对目录、现有 `known_hosts`、同目录 `id_ed25519` 与 `id_ed25519.pub`。包装器必须先离线核对五文件、回执、来源、目标、主机指纹和冻结本地公钥指纹，并由固定 `ssh-keygen -y` 验证私钥可读取、ACL 被 OpenSSH 接受且密钥对一致；再以固定 OpenSSH 路径、清空代理/AskPass/调用方 PATH 的最小环境发起唯一一次 SSH。禁止隐式密钥发现、密码、键盘交互、代理、X11、本地命令和端口转发。失败、超时、任何 stderr 或额外 stdout 均停止且不重试。
2. 远端只读脚本只通过该 SSH 会话的 stdin 交给固定 `/bin/sh -s`，不作为 SSH 命令参数参与 Windows 引号重构；脚本只执行 `id -un`、hostname、`sha256sum /etc/machine-id`、`realpath`、`stat` 和三个目标/暂存路径的存在性测试。摘要提取使用 POSIX 参数展开，不执行 `cut`、`awk`、sudo、Docker、数据库、队列或服务命令。
3. 只有只读预检完整 PASS，包装器才以相同显式身份、最小环境和禁止转发参数调用固定 SFTP 一次。SFTP 批处理第一条必须是不可忽略失败的原子 `mkdir /home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003`，随后设为 `0700` 并逐项上传五文件；目录已存在时立即失败，禁止合并或覆盖。
4. 阿里云控制台 root 会话再次只读核对 `id`、hostname、machine-id 摘要、`id -nG pc`、暂存真实路径/所有权/权限、五文件白名单与 `SHA256SUMS`。任一不符停止；三个安装目标必须全部不存在。
5. root 必须以原子 `mkdir` 创建全新、非链接、`root:root:0700` 的 `/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-20260812-003`，逐项用 `install -o root -g root -m 0600` 把五个暂存文件复制到该目录。复制完成后只对 root-owned 副本重新核对普通文件/非链接、五文件白名单、`SHA256SUMS` 自身回执精确为 `82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d`、四项清单摘要、三个冻结摘要和对账器大小，并精确执行 `visudo -cf /root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-20260812-003/molin-g8-test-readonly-audit.sudoers`；不得从 `pc` 可写暂存目录直接安装任何 live 目标。
6. 安装前分别逐级核对工具父链 `/usr`、`/usr/local`、`/usr/local/libexec`、已有 `/usr/local/libexec/molin`，以及 sudoers 父链 `/etc`、`/etc/sudoers.d`；每一级都必须存在、为目录且非链接、`root:root`、组/其他用户不可写。只有 `/usr/local/libexec/molin` 不存在时，才允许用 `install -d -o root -g root -m 0755` 创建并立即复核；`/etc` 或 `/etc/sudoers.d` 不存在时不得创建，必须停止。随后仅从 root-only 副本逐项安装两个 `0755` 工具和一个 `0440` sudoers 文件；每创建一项目标立即记录，供部分失败回滚。
7. 安装后先重新核对三个 live 目标均为普通文件且非链接：审计器必须为 `root:root:0755` 且 SHA-256 精确为 `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256`；对账器必须为 `root:root:0755`、大小精确为 `13066129` 字节且 SHA-256 精确为 `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1`；sudoers 文件必须为 `root:root:0440` 且 SHA-256 精确为 `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f`。全部一致后再精确执行 `visudo -cf /etc/sudoers.d/molin-g8-test-readonly-audit`、`sudo -n -l -U pc` 和 `id -nG pc`；必须仅允许固定审计器，且 `pc` 不属于 Docker 组。全部通过后可精确删除本次 root-only 临时目录；不得清理 `pc` 暂存目录，保留用于独立取证。
8. 通过一次 `pc` 非特权会话精确执行 `sudo -n /usr/local/libexec/molin/g8-test-readonly-audit --self-test`，禁止直接执行工具绕过 sudo 规则验收，也禁止添加任何其他参数。本 ChangeId 禁止真实运行态审计。

## 4. 上限与影响

- 最大会话：只读 SSH 预检 1 次、SFTP 暂存上传 1 次、管理员控制台 1 次、非特权 self-test 1 次；全部零重试。
- 最大业务请求：0；最大上游请求：0；最大费用：0 CNY。
- 影响范围：只新增两个 root-owned 只读工具和一个单命令 sudoers 文件；不修改 API、容器、服务、环境文件、数据库、Redis、RabbitMQ、Bifrost、监控或流量。

## 5. 回滚

安装任一步失败，管理员只逆序删除本次日志已确认新建的 live 目标。若 sudoers 已创建，先精确删除该文件并执行 `visudo -c`；再删除本次新建的对账器和审计器。随后以 `sudo -n -l -U pc` 确认规则消失。root-only 临时目录只在其真实路径、root 所有权、0700 权限和本次 ChangeId 全部匹配时精确删除；任何预存目标不得覆盖或删除。`pc` 暂存目录保留取证，未经新的删除授权不得清理。禁止递归删除 `/usr/local/libexec/molin`、部署目录、账本、Usage、钱包、Outbox、审计、日志或备份。

## 6. 停止条件

出现任一情况立即停止：本地候选、主机指纹、本地密钥指纹/密钥对/ACL 不匹配；SSH/SFTP 非零或超时；SSH 产生 stderr、远端键集合不完整或额外输出；身份不一致；部署根不是安全的 `pc:pc` 目录；暂存、root-only 临时目录或安装目标已存在；工具或 sudoers 任一父链缺失（仅允许按本清单新建 `/usr/local/libexec/molin`）、为链接、非 root 所有或可被组/其他用户写入；root-only 副本摘要/白名单/权限不符；安装后任一 live 文件不是普通非链接文件，或其摘要、大小、属主、属组、权限不符；`visudo` 失败；sudo 规则含额外能力；`pc` 属于 Docker 组；self-test 失败；输出真实 Secret，或需要清单外命令。

本清单不授权真实运行态审计、服务重启、Migration、配置修改、凭据轮换、生产连接、付费上游、真实通知或客户灰度。
