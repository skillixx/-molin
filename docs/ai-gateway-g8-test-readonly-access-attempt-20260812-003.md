# AI 网关 G8 测试服只读入口安装尝试记录（003）

## 1. 变更身份与结论

| 项目 | 结果 |
|---|---|
| ChangeId | `CHG-G8-TEST-READONLY-ACCESS-20260812-003` |
| 目标 | `pc@8.130.9.163:10003` |
| 执行日期 | 2026-08-12（UTC） |
| 结论 | `STOPPED_DURING_REMOTE_STAGE` |
| 包装器正式调用 | `1 / 1`，已消费，禁止重试 |
| 本地离线门禁 | `--self-test` 与 `--local-check` 均 PASS |
| 固定结果 | `G8_TEST_READONLY_ACCESS_STAGE=FAILED reason=remote_stage_failed` |
| root 控制台 / live 安装 / sudoers / self-test | 均未执行 |
| 业务请求 / 上游请求 / 费用 | `0 / 0 / 0 CNY` |

003 的唯一正式调用触发停止条件后立即结束；没有手工补发 SSH 或 SFTP，没有进入阿里云 root 控制台，也没有执行安装、`visudo`、sudo 规则核验或非特权 self-test。SSH/SFTP 可能由系统自动写入访问审计日志，本次没有授权读取这些日志，不能表述为操作系统层绝对零写入。

## 2. 已确认的本地事实

1. 正式调用前，本地候选目录恰好包含五个冻结文件，候选回执为 `82b18d6040bcd6be72cf170fa066ecd7cf469a53f4901365f379bec5a89c496d`。
   历史 Linux CI 对同一来源树、三项制品摘要和对账器大小的临时复现回执为 `7f4633357bf6883d166b0ee7d9750d7e745cf0a15d23163a547d6519e217efc1`；它只用于跨平台复现门禁，不替代实际执行时绑定的 Windows 本地回执。
2. 固定 `known_hosts`、同目录 `id_ed25519` / `id_ed25519.pub`、密钥对一致性和 OpenSSH ACL 在 `--local-check` 中通过。
3. 包装器仅被正式调用一次；代码固定 `ConnectionAttempts=1`，不包含重试循环。
4. 包装器没有 sudo、Docker、数据库、队列、服务操作或业务 HTTP 请求能力。

## 3. 未知状态与停止原因

包装器将 SSH 预检失败、SSH 输出契约失败、SFTP 启动失败和 SFTP 非零结果统一收敛为低敏枚举 `remote_stage_failed`。该枚举不会回显远端 stderr 或路径内容，但也无法判断失败发生在 SSH 阶段还是 SFTP 阶段。因此当前只能得出：

- SSH/SFTP 远端阶段没有完整 PASS；
- 是否进入 SFTP、是否创建 `/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003`、是否上传部分文件均为 `UNKNOWN`；
- 三个 live 安装目标未由本次操作者创建，因为 root 控制台阶段没有开始；
- 不得用本地候选仍存在、包装器无成功输出或 root 未执行来推定远端暂存目录不存在。

授权要求任一远端失败立即停止且零重试，因此没有为了区分阶段而再次连接。003 已消费，禁止再次生成、上传、安装或调用 stage 包装器。

## 4. 后续门禁

继续前必须使用新的 ChangeId，并拆分为独立授权：

1. 先申请一次完全只读的暂存状态取证，只核对固定主机身份、003 暂存路径是否存在、文件名白名单、普通文件/非链接、属主、权限、大小和摘要；禁止下载内容、删除文件、执行 sudo、Docker、数据库、队列、服务或业务请求。
2. 如果暂存路径存在，另行申请精确清理授权；只能删除确认属于 003 且内容、真实路径、属主和权限全部匹配的暂存目录，不得与取证授权合并推定。
3. 只有 UNKNOWN 关闭后，才可另行准备新的安装候选与 ChangeId；不得复用 003 候选、回执或授权。
4. 新候选必须改进低敏阶段回执，在不泄漏 stderr 的前提下区分 `ssh_preflight_failed` 与 `sftp_upload_failed`，并继续保持单次、零重试和失败关闭。

本次结果不关闭测试服 API 停止、运行态 P1=3，以及 schema、Bifrost、监控和账务 UNKNOWN；也不授权生产、付费上游、真实通知或客户灰度。
