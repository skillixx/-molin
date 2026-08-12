# AI 网关 G8 测试服只读入口安装预检记录（2026-08-12）

## 1. 变更身份与结论

| 项目 | 结果 |
|---|---|
| ChangeId | `CHG-G8-TEST-READONLY-ACCESS-20260812-001` |
| 目标 | `pc@8.130.9.163:10003` |
| 结论 | `STOPPED_BEFORE_ASSET_WRITE` |
| 资产、配置、服务与业务数据写入 | 0 |
| 上传 / 安装 / sudoers 修改 | 均未执行 |
| 业务请求 / 上游请求 / 费用 | `0 / 0 / 0 CNY` |

该 ChangeId 已执行一次批准的只读预检并触发停止条件，必须视为已消费，禁止重放。

## 2. 已执行证据

1. 本机现有 `known_hosts` 中 `[8.130.9.163]:10003` 只有一条 ED25519 记录；计算结果为 `SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`，与批准基线一致。未运行 `ssh-keyscan`，未接受新主机密钥。
2. 使用 `BatchMode=yes`、`NumberOfPasswordPrompts=0`、`StrictHostKeyChecking=yes`、固定 `UserKnownHostsFile`、`ConnectionAttempts=1` 和有限连接超时建立一次 SSH。
3. 首个远端命令为 `sudo -n -l`。命令返回“需要密码”并以退出码 1 结束。
4. 后续 `whoami`、hostname、machine-id 摘要和组成员核对由 `&&` 串联，因此未执行；更没有执行 SCP、候选包生成、目录创建、`install`、`visudo`、self-test、Docker、数据库、队列或服务命令。
5. 未执行或观察到候选资产、配置、服务、数据库、队列或业务数据写入。SSH 登录和 `sudo -n -l` 可能由系统自动写入 sshd、sudo、journald 或 audit 访问审计日志，本次未获授权读取这些日志，因此不得表述为操作系统层绝对零写入。

## 3. 停止原因与后续门禁

当前 `pc` 会话不是获批描述中的“现有免密 sudo”管理员通道，无法按 Runbook 以非交互方式完成 root-owned 安装。不得在聊天、命令参数、脚本或日志中提供 sudo 密码，也不得放宽为 Docker 组、任意 `NOPASSWD: ALL`、Shell 或通配符 sudo。

用户随后仅批准为新 ChangeId `CHG-G8-TEST-READONLY-ACCESS-20260812-002` 准备仓库和本地候选，并指定阿里云控制台 root 通道。该批准明确禁止连接测试服务器、上传、安装或修改 sudoers；候选完成后仍须提交独立安装授权清单并再次等待确认。因此本文件和候选准备均不构成远端安装授权。

本次结果不关闭测试服 API 停止、运行态 P1=3，以及 schema、Bifrost、监控和账务 UNKNOWN；也不授权测试服部署、重启、Migration、凭据轮换、生产操作、付费上游、真实通知或客户灰度。
