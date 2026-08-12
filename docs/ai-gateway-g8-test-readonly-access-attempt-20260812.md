# AI 网关 G8 测试服只读入口安装预检记录（2026-08-12）

## 1. 变更身份与结论

| 项目 | 结果 |
|---|---|
| ChangeId | `CHG-G8-TEST-READONLY-ACCESS-20260812-001` |
| 目标 | `pc@8.130.9.163:10003` |
| 结论 | `STOPPED_BEFORE_WRITE` |
| 远端写入 | 0 |
| 上传 / 安装 / sudoers 修改 | 均未执行 |
| 业务请求 / 上游请求 / 费用 | `0 / 0 / 0 CNY` |

该 ChangeId 已执行一次批准的只读预检并触发停止条件，必须视为已消费，禁止重放。

## 2. 已执行证据

1. 本机现有 `known_hosts` 中 `[8.130.9.163]:10003` 只有一条 ED25519 记录；计算结果为 `SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`，与批准基线一致。未运行 `ssh-keyscan`，未接受新主机密钥。
2. 使用 `BatchMode=yes`、`NumberOfPasswordPrompts=0`、`StrictHostKeyChecking=yes`、固定 `UserKnownHostsFile`、`ConnectionAttempts=1` 和有限连接超时建立一次 SSH。
3. 首个远端命令为 `sudo -n -l`。命令返回“需要密码”并以退出码 1 结束。
4. 后续 `whoami`、hostname、machine-id 摘要和组成员核对由 `&&` 串联，因此未执行；更没有执行 SCP、候选包生成、目录创建、`install`、`visudo`、self-test、Docker、数据库、队列或服务命令。

## 3. 停止原因与后续门禁

当前 `pc` 会话不是获批描述中的“现有免密 sudo”管理员通道，无法按 Runbook 以非交互方式完成 root-owned 安装。不得在聊天、命令参数、脚本或日志中提供 sudo 密码，也不得放宽为 Docker 组、任意 `NOPASSWD: ALL`、Shell 或通配符 sudo。

如需继续，必须使用新的候选 ChangeId `CHG-G8-TEST-READONLY-ACCESS-20260812-002`，并由用户明确指定一个仓库外受控 root 管理通道，例如云控制台或由管理员人工执行固定命令。新授权仍须绑定相同目标、冻结摘要、会话上限、零业务请求、零费用、回滚和停止条件；本文件不构成该授权。

本次结果不关闭测试服 API 停止、运行态 P1=3，以及 schema、Bifrost、监控和账务 UNKNOWN；也不授权测试服部署、重启、Migration、凭据轮换、生产操作、付费上游、真实通知或客户灰度。
