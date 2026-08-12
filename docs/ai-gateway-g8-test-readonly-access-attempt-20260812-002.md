# AI 网关 G8 测试服只读入口安装预检记录（002）

## 1. 变更身份与结论

| 项目 | 结果 |
|---|---|
| ChangeId | `CHG-G8-TEST-READONLY-ACCESS-20260812-002` |
| 目标 | `pc@8.130.9.163:10003` |
| 结论 | `STOPPED_DURING_READONLY_PREFLIGHT` |
| SSH 预检会话 | `1 / 1`，已消费，禁止重试 |
| SCP / root 控制台 / self-test | 均未执行 |
| 候选资产、配置、服务与业务数据写入 | 未执行或观察到 |
| 业务请求 / 上游请求 / 费用 | `0 / 0 / 0 CNY` |

该 ChangeId 已触发停止条件并消费完唯一 SSH 预检会话，禁止重放、上传或安装。SSH 登录可能由系统自动写入 sshd、journald 或 audit 访问审计日志，本次未获授权读取这些日志，因此不得表述为操作系统层绝对零写入。

## 2. 本地门禁证据

1. 最终 002 候选目录在联网前通过五文件白名单；`SHA256SUMS` 回执为 `d6d07f7b4959e48f5ffe0e92ee4116cef55fe56f5318df6ae3f0d9c5350ee567`，四项文件摘要、来源提交 `50b3e2f9d18b38e7d4a91ebeb4f03c413ef33c44`、来源树 `73fb652a1f86db84991c8745f8c10e1d2a255f29` 和部署根目录均匹配。
2. 本机固定 `known_hosts` 中目标唯一 ED25519 指纹为 `SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`，与批准值一致；未执行 `ssh-keyscan`，未接受新主机密钥。
3. 使用 `BatchMode=yes`、`NumberOfPasswordPrompts=0`、`StrictHostKeyChecking=yes`、固定 `UserKnownHostsFile` 和有限连接超时发起唯一一次 SSH。

## 3. 停止原因

远端只读命令在计算 machine-id 摘要时执行了 `cut -d " " -f1`。该参数经过 PowerShell、Windows OpenSSH 和远端 POSIX shell 的多层解析后不再是单字符分隔符，远端返回 `cut: 分隔符必须是单个字符` 并非零退出。预检没有形成完整固定键集合，不能将此前可能产生的局部 stdout 当作主机身份或目录门禁证据。

本次错误属于预检编排缺陷，不是服务器状态不匹配。由于授权明确限定一次 SSH、零重试，收到非零结果后立即停止；没有执行 SCP、阿里云 root 控制台、目录创建、`install`、`visudo`、sudoers 修改、非特权 self-test、运行态审计、Docker、数据库、队列或服务命令。

## 4. 后续门禁

继续前必须使用新的 ChangeId，并先在仓库内完成以下闭环：

1. 以隔离 Python 包装器生成固定 SSH 参数，远端摘要提取改用 POSIX 参数展开，不再依赖 `cut`、`awk` 或嵌套引号。
2. 自动化证明远端失败只调用一次 SSH、不重试、不回显远端错误内容，且任何额外 stdout/stderr、目标漂移或不安全部署根权限均失败关闭。
3. 002 转为已消费历史候选，只允许在系统临时目录复现验证；新的持久候选必须绑定 003、最新冻结来源和新回执。
4. 新候选、独立评审、QA、产品和精确 PR HEAD CI 全部通过后，再提交独立 003 安装授权清单等待用户确认。

本次结果不关闭测试服 API 停止、运行态 P1=3，以及 schema、Bifrost、监控和账务 UNKNOWN；也不授权任何生产、付费上游、真实通知或客户灰度动作。
