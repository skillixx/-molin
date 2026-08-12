# AI 网关 G8 测试服 003 暂存状态只读取证授权清单（004）

> 当前状态：`PENDING_USER_APPROVAL`。仓库工程门禁已经完成，但本文件仍不构成连接测试服的授权；必须由用户再次明确批准后，才能执行一次只读 SSH。

## 0. 工程门禁证据

- PR：[#341](https://github.com/skillixx/-molin/pull/341)，最终 HEAD `1ec35a66e1833082da5b3b406610d8d6c6c46f67`。
- merge commit：`7e27e88b1c4b63630a0be346e41226c449d033e4`，使用 merge commit 合并，远端功能分支已删除。
- CI：run `31597619593`，`completed/success`，12/12 门禁成功，必选门禁汇总成功。
- 独立代码与安全评审：P0=0、P1=0、P2=0。
- 独立 QA：P0=0、P1=0、P2=0；Linux 004 测试 10/10，通过部署根替换、文件目录项替换和静态权限异常门禁。
- 独立产品/规格验收：P0=0、P1=0、P2=0。
- 合并后脚本 SHA-256：`4b90221e8af3b6e2c882cac7bd97b2cee947451270eb4b36bbccfe8b336556e0`。
- 上述证据只证明 004 取证入口工程就绪；尚未连接测试服，也不授权生产、真实上游、通知、客户灰度或商业观察。

## 1. ChangeId 与精确目标

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004`。
- 目标：`pc@8.130.9.163:10003`。
- hostname：`pc-Z790-UD-AX`。
- machine-id SHA-256：`b60555f0d8d48731b657d21b2e54559d263210688125ae56a4d662fc4d7278d4`。
- SSH ED25519 指纹：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`。
- 部署根：`/home/pc/molin`。
- 唯一取证目标：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003`。
- 基线提交：`6e1800de2a212f37d84b76107b8150ba025b82aa`。
- 当前候选脚本 SHA-256：`4b90221e8af3b6e2c882cac7bd97b2cee947451270eb4b36bbccfe8b336556e0`；最终授权前必须按合并后的精确文件重新计算，不一致即停止。

## 2. 允许的唯一操作

1. 本地以 `python -I` 对合并后的 `infra/scripts/run-ai-gateway-g8-test-staging-evidence.py` 执行一次 `--local-check`，绑定上述 ChangeId、现有绝对 `known_hosts`、同目录 `id_ed25519` 与 `id_ed25519.pub`。只核对固定主机指纹、公钥指纹、私钥可读性、OpenSSH ACL 和密钥对一致性。
2. 本地门禁完整 PASS 后，移除 `--local-check`，以完全相同的其余参数正式调用包装器一次。包装器只允许固定 OpenSSH、最小环境、`ConnectionAttempts=1`、公钥认证、严格 known_hosts，禁止密码、键盘交互、代理、X11、本地命令、端口转发和 TTY。
3. 远端只通过 stdin 执行固定 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -`，核对登录用户、hostname、machine-id 摘要、部署根及上述唯一暂存路径。禁止任何调用方提供远端路径或命令。
4. 暂存不存在时输出 `ABSENT`；存在且五文件白名单、普通文件/非链接、`pc:pc`、目录 `0700`、文件组/其他用户不可写、大小及冻结 SHA-256 全部匹配时输出 `PRESENT/PASS`；存在但不匹配时仅输出 `PATH`、`FILE_SET`、`FILE_METADATA`、`FILE_CONTENT` 或 `READ_ERROR` 固定类别，并以退出码 3 停止。

## 3. 明确禁止

- 禁止重放 001、002、003，禁止 SFTP、SCP、上传、下载或打印文件内容。
- 禁止创建、修改、移动或删除暂存目录及其文件。
- 禁止 sudo、root 控制台、安装、修改 sudoers、Docker、数据库、Redis、RabbitMQ、Bifrost、监控、备份、日志读取、服务控制或业务 HTTP 请求。
- 禁止生产连接、真实付费上游、真实通知和客户灰度。

## 4. 上限、影响和停止条件

- 最大本地检查：1 次；最大 SSH：1 次；重试：0。
- 最大业务请求：0；最大上游请求：0；最大费用：0 CNY。
- 计划影响仅为读取固定身份文件、部署根元数据和 003 暂存元数据/摘要。SSH 和读取可能产生 sshd/journald/audit 日志，并可能按文件系统策略更新 atime，不能表述为操作系统层绝对零写入。
- 任一脚本摘要、主机身份、known_hosts、公钥/密钥对/ACL、部署根真实路径/属主/权限或远端输出契约不符，立即停止且不重试。
- `PRESENT/MISMATCH` 是成功取得的阻断性证据，不得被解释为可清理或可安装；固定输出以外的任何 stdout、任意 stderr、远端非零或超时均视为取证失败。

## 5. 回滚与后续

本 ChangeId 不创建仓库外业务资产，没有应用层回滚动作。不得以“回滚”为由删除暂存目录或审计日志。

- `ABSENT`：关闭 003 暂存 UNKNOWN 后，才可另行准备新安装候选和新 ChangeId。
- `PRESENT/PASS`：必须另行提交精确清理授权；清理前再次核对真实路径、非链接、属主、权限、五文件白名单和摘要。
- `PRESENT/MISMATCH`：保持失败关闭，按固定类别另行设计只读诊断或清理方案，不得自动删除。
- 任何结果都不授权安装只读入口、执行运行态审计或推进生产/商业灰度。
