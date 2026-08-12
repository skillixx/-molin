# G8 测试服主机身份低敏诊断 007 授权清单

> 当前状态：`CONSUMED_BLOCKED_READABLE_MISMATCH`。用户已批准并执行唯一一次本地检查和只读 SSH；正式结果为 `READABLE_MISMATCH`，已按停止条件零重试结束。007 及本文件中的全部执行命令均已消费并作废，禁止重放。

## 0. 工程门禁证据

- 功能 HEAD：`21cc6698df1aa0b48de66213c97a4a31d074fcb5`。
- PR：[#349](https://github.com/skillixx/-molin/pull/349)，已从 Draft 转 Ready 后按 merge commit 合并，未使用 squash。
- CI：run `31618217697`，精确绑定功能 HEAD，12/12 SUCCESS，包含 G8 生产就绪、Linux `--network none` 007 测试和必选汇总。
- 独立门禁：代码安全、QA、产品/规格均为 P0=0、P1=0、P2=0。
- merge commit：`9f5c028232e03bd1d8ad99fb99211680af790625`，双父为原主干 `2cb93163b0f4b49d7313866e3b5c4b7d0a9d718b` 与功能 HEAD。
- 远端功能分支已删除。

上述工程事实与本次执行事实必须分开理解；执行结果见 `docs/ai-gateway-g8-test-readonly-host-identity-diagnostic-attempt-20260813-007.md`，不能反向证明测试服其他运行态门禁通过。

## 1. ChangeId 与精确目标

| 项目 | 冻结值 |
|---|---|
| ChangeId | `CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007` |
| 目标 | `pc@8.130.9.163:10003` |
| 诊断目标 | 仅判断固定 `/etc/machine-id` 为 `READABLE_MATCH`、`READABLE_MISMATCH` 或 `UNREADABLE` |
| 部署根 | `/home/pc/molin`，007 不读取该目录及其内容 |
| 诊断脚本 SHA-256 | `5858ab020ae5f1491af51582bd4079c5ff84b9da251a92d85265887c511c2e50` |
| 冻结 004 helper SHA-256 | `599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89` |
| SSH 主机 ED25519 指纹 | `SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I` |
| 本地 ED25519 公钥指纹 | `SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0` |

仓库中既有批准 machine-id 摘要只在远端进程内存中用于比较。本清单、脚本输出、SSH stdout/stderr、PR、CI 和验收文档均不得输出当前 machine-id 原文或摘要，也不得用本次结果自动更新批准基线。

## 2. 已消费的历史动作与命令摘要

以下顺序已经执行完毕，仅供历史审计。所有命令均已作废，不得再次运行：

1. 在本机以隔离 Python 执行一次 `--local-check`，核对脚本/helper 摘要、known_hosts 唯一 ED25519 记录、私钥 ACL、公私钥指纹和密钥对一致性；本步骤不联网。
2. 只有本地检查完整 PASS，才允许以同一脚本执行一次正式调用。
3. 正式调用只能启动一个固定系统 OpenSSH 进程：`-F none`、`ConnectionAttempts=1`、严格 known_hosts、固定 IdentityFile、仅公钥认证，禁用密码、键盘交互、Agent、X11、TTY、本地命令及全部转发。
4. 远端仅通过 stdin 执行 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -`；程序最多读取固定 `/etc/machine-id` 4097 字节，在内存比较后只返回精确三键和三态之一。

执行时必须使用以下本地资产路径：

```powershell
python -I infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py `
  --local-check `
  --change-id CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007 `
  --known-hosts C:\Users\skillixx\.ssh\known_hosts `
  --identity-file C:\Users\skillixx\.ssh\id_ed25519 `
  --identity-public-file C:\Users\skillixx\.ssh\id_ed25519.pub

python -I infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py `
  --change-id CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007 `
  --known-hosts C:\Users\skillixx\.ssh\known_hosts `
  --identity-file C:\Users\skillixx\.ssh\id_ed25519 `
  --identity-public-file C:\Users\skillixx\.ssh\id_ed25519.pub
```

## 3. 数量、费用与影响上限

- 本地检查：最多 1 次。
- SSH：最多 1 次，零重试。
- 业务请求：0。
- 上游请求：0。
- 费用上限：0 CNY。
- 允许读取：仅 `/etc/machine-id`，最多 4097 字节。
- 禁止读取：003 暂存目录、部署目录内容、日志、数据库、Redis、RabbitMQ、Bifrost、监控、备份和业务数据。
- 禁止动作：SFTP/SCP、上传、下载、创建、修改、移动、删除、sudo、root 控制台、Docker、HTTP、服务控制、生产连接、真实通知、付费调用和客户灰度。

SSH 与只读文件访问可能由操作系统自动产生 sshd、journald、audit 访问日志或 atime；不得表述为系统层绝对零写入，也不得删除这些事实。

实际执行次数为本地检查 `1`、SSH `1`、重试 `0`；业务请求、上游请求和费用为 `0 / 0 / 0 CNY`。正式结果为 `READABLE_MISMATCH`，没有继续读取暂存或执行任何禁止动作。

## 4. 停止条件

任一条件出现立即停止，禁止重试：

- 脚本/helper 摘要、目标、主机指纹、本地公钥指纹、known_hosts、私钥 ACL 或密钥对不匹配。
- 本地检查非零、stderr 非空或输出不是精确 PASS。
- OpenSSH 非零、stderr 非空、输出超过 64 KiB、输出不是精确三键、ChangeId/目标 ChangeId 错误、重复键、额外键、非 ASCII 或未知状态。
- 返回 `READABLE_MISMATCH`：不得更新受信基线，不得继续暂存取证；必须使用阿里云 root/CMDB 等独立受控来源核验并另行批准。
- 返回 `UNREADABLE`：不得推断为摘要漂移；权限、文件状态或读取异常诊断必须使用新的 ChangeId 和独立授权。

## 5. 回滚与后续边界

007 不创建或修改候选资产、配置、服务、数据库、队列或业务数据，因此没有应用层回滚动作。若本次访问自动形成系统审计日志，应原样保留。

007 已消费并关闭普通入口。后续只能使用新的 ChangeId 通过阿里云 root/CMDB 等独立受控来源核验主机身份和既有批准基线；不得更新受信摘要，不得重放 006 或 007，也不得直接读取、清理或安装 003 暂存资产。

本清单不授权生产部署、真实付费上游、真实通知、客户灰度或商业观察，不改变 `G8_ENGINEERING_READY`，也不代表 `G8_COMMERCIAL_ACCEPTED`。
