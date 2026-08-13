# AI 网关 G8 测试服只读入口安装尝试记录（009）

## 1. 变更身份与结论

| 项目 | 结果 |
|---|---|
| ChangeId | `CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009` |
| Drop SSH 目标 | `pc@8.130.9.163:10003` |
| 执行日期 | 2026-08-13 |
| 结论 | `STOPPED_BEFORE_SSH_LOCAL_SNAPSHOT_FAILED` |
| 本地 `--local-check` | `PASS`，执行 `1 / 1` |
| 正式包装器调用 | `1 / 1`，已消费，禁止重试 |
| 固定结果 | `G8_TEST_READONLY_ACCESS_STAGE_DROP=FAILED reason=invalid_request`，退出码 `2` |
| SSH / SFTP | 均未启动 |
| root 安装 / live 目标 / sudoers / visudo / self-test | 均未执行 |
| 业务请求 / 上游请求 / 费用 | `0 / 0 / 0 CNY` |

009 在唯一一次正式包装器调用中触发停止条件后立即结束，没有重试、手工补发 SSH/SFTP、进入 root 管理通道或执行任何安装命令。该 ChangeId 与全部历史命令现已消费，禁止通过修复本地环境后重放。

## 2. 已确认的本地事实

1. 执行前包装器 SHA-256 为 `3ad9cac165355ea1be150f141af6072d787fe9888733ec025cbf3466d6af5f04`，与合并后授权清单一致。
2. 候选目录恰好包含五个冻结文件；`SHA256SUMS` 回执为 `840bdbed48edab6d70d351fa232b7426903bf3f3098f682e2884f513b9cd0efd`，审计器、sudoers、对账器摘要和对账器大小均通过本地门禁。
3. 固定 `known_hosts`、同目录 `id_ed25519` / `id_ed25519.pub`、目标公钥指纹和原始密钥对一致性在唯一一次 `--local-check` 中通过。
4. 唯一正式调用在创建冻结本地快照时失败。停止远端流程后，以不调用 SSH/SFTP 的离线最小复现确认：Windows 临时目录中的私钥副本仅经 POSIX 风格 `chmod 0600`，NTFS ACL 没有收紧；固定 `ssh-keygen -y` 因私钥权限诊断退出 `255`，包装器由此抛出 `identity_pair_mismatch`。
5. 生产代码顺序是先完成 `create_frozen_local_snapshot`，成功后才调用 `run_remote_preflight`。本次失败发生在前者内部，因此没有建立 SSH，也没有启动 SFTP；远端暂存目录和 live 目标均未由本次操作创建。
6. 防重放变更后的 stage 包装器 SHA-256 为 `4be88638f2a4a271ebbf23751bd3f7238ea5f78f1f18fcb6889c9e071b953f30`；所有入口均在读取 helper、候选、身份材料或网络前固定返回 `change_id_consumed`。生成器同时移除活动候选，009 只允许在系统临时目录按 Windows/Linux 历史回执复现并自动销毁。

离线诊断只在系统临时目录复制并校验本地材料，临时目录由上下文自动清理；没有输出私钥、公钥正文、known_hosts 正文、密码、Token 或其他 Secret。

## 3. 停止、回滚与残余状态

- 没有进入 root 管理通道，没有创建 root-only 临时目录、`/usr/local/libexec/molin`、两个工具或 sudoers live 文件，因此没有 live 目标需要回滚。
- 没有启动 SFTP，因此没有创建 `/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-009`，也没有部分上传。
- 没有执行 `visudo`、`sudo -n -l -U pc`、Docker 组核验或非特权 sudo self-test。
- 测试服 API 停止、现有运行态 P1=3，以及数据库、Bifrost、监控、备份和账务 UNKNOWN 均未关闭。

本次仓库变更只负责把 009 入口改为消费态并保存低敏证据，不授权再次连接测试服务。

## 4. 后续门禁

009 执行结束时曾要求新 ChangeId 设计严格 Windows 私钥冻结；后续低敏 SSH 诊断已证明现有原始密钥路径可直接免密码认证，用户随后批准 010 改用直连方案。以下第 1、2 项保留为历史判断，不再是 010 的当前实现要求：

1. 历史要求曾拟设计严格 Windows 私钥冻结方式；010 已由用户改为显式原始路径，不复制或修改 ACL，仍禁止 SSH Agent、AskPass 或隐式密钥发现。
2. 010 改为验证原始密钥对、known_hosts、文件元数据和摘要，并在远端调用边界重复复核；不再运行私钥副本 ACL 测试。
3. 新包装器仍须保持一次本地检查、一次 SSH、一次 SFTP、零重试和有界低敏输出，并完成独立代码安全、QA、产品/规格、精确 HEAD CI 与 merge commit。
4. 工程门禁全部通过后，仍需用户对 010 重新独立授权；不得复用 009 候选、回执或执行授权。

本次结果不授权真实运行态审计、服务重启、Migration、配置或凭据修改、生产连接、付费上游、真实通知、客户灰度或商业观察。`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 继续未完成。

## 5. 仓库合并证据

- 防重放与执行证据提交为 `bab8f89a317f9bcb7ca1fd1f534f3fa6a9545f49`，固定基线为 `c9247d3d9b36b0d189ad79b061261d6a316c80b6`。
- CI run `31667550392` 精确绑定该提交，状态为 `completed / success`，12/12 作业全部 SUCCESS，包含 G8 生产就绪、G7 零差额、真实后端浏览器和必选门禁汇总。
- 独立代码安全、QA、产品/规格均在该精确提交上给出 `P0=0 / P1=0 / P2=0`；这些结论只覆盖仓库变更，不关闭测试服既有运行态问题。
- PR #360 已按 merge commit `c9402d94129da4042e3fb1bb978d63018af4a439` 合入主干；该提交的两个父提交依次为基线和精确功能提交，未使用 squash，远端功能分支已删除。
