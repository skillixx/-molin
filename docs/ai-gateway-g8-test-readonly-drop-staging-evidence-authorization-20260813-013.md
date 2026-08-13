# G8 Drop 暂存只读取证 013 授权清单

## 1. 当前状态

`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`

本清单仅冻结未来可能执行的候选。当前未授权运行真实本地材料诊断、013 正式入口、SSH 或任何测试服连接。Draft PR、CI、评审或合并均不自动构成执行授权。

## 2. 固定目标

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-013`
- 目标历史 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- Drop 端点：`pc@8.130.9.163:10003`
- 部署根：`/home/pc/molin`
- 暂存路径：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- 物理主机身份：`NOT_APPLICABLE`
- 服务端与客户端 ED25519 指纹只在批准常量中冻结，执行报告不得输出实际值。

## 3. 冻结脚本

- 无 ChangeId 本地诊断器：`infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py`
  - SHA-256：`06f0f883f7fe225e88691a64a8c407a77b6600d72bc18682b7ceea146978e997`
  - 大小：`13667`
- 一次性 013 包装器：`infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py`
  - SHA-256：`224ee5f022636a6052752e623b0c40ff48c5c0648c5d788714d222ea4badacca`
  - 大小：`20707`

最终合并后必须从 merge commit 原始 Git 对象重新计算并更新以上摘要和大小；HEAD 或脚本任一漂移即使本清单失效。

## 4. 固定五文件

| 文件 | SHA-256 | 大小 | 权限 |
|---|---|---:|---:|
| `SHA256SUMS` | `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f` | 362 | 0600 |
| `ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | 13066129 | 0700 |
| `g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | 18377 | 0700 |
| `manifest.env` | `763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6` | 863 | 0600 |
| `molin-g8-test-readonly-audit.sudoers` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | 416 | 0600 |

`SHA256SUMS` 自身摘要是 011 Windows 候选回执；内容必须精确列出另外四文件。`manifest.env` 必须与设计文档冻结的完整键值集合完全一致。

## 5. 未来执行顺序

1. 操作者先独立运行无 ChangeId 本地诊断器；失败可以在修复本地材料后重复运行，不消耗 013。
2. 只有本地诊断 PASS、合并后摘要复核、独立工程门禁和用户再次明确批准全部满足后，才可执行一次 013 正式入口。
3. 013 最多发起一次固定只读 SSH，`ConnectionAttempts=1`，重试为 0。
4. 无论结果为 `ABSENT`、`PRESENT/PASS`、`PRESENT/MISMATCH` 或 `evidence_unavailable`，记录低敏证据后立即停止并消费 013。

本清单不冻结或授权任何当前可执行命令。最终命令只允许在合并后证据收口 PR 中生成，并须显式引用系统 OpenSSH、原始 known_hosts/私钥/公钥绝对路径和精确 013 ChangeId。

## 6. 次数、费用与影响

- 本地诊断：可重复，不联网，不消耗 ChangeId。
- 013 正式 SSH：最多 1 次；SSH 重试：0。
- SFTP/SCP/上传/下载：0。
- 业务请求：0；上游请求：0；费用上限：0 CNY。
- 应用层远端写入：0；但 sshd/journald/audit 日志及文件系统 atime 可能由操作系统产生。

## 7. 停止条件

任一本地工具、身份材料、端点、登录用户、路径、父链、属主、权限、摘要、文件集合、manifest、回执、stderr、输出上限、超时、返回码、键集合或输出契约不符，立即停止且不重试。不得输出密码、私钥、Token、环境变量值、实际指纹、当前文件摘要、远端 stderr 或异常正文。

## 8. 回滚与后续边界

013 没有应用层远端写能力，因此没有应用层回滚目标。它不得自动清理、安装、部署或继续运行态审计。任何后续动作必须使用新的 ChangeId、独立设计、工程门禁和用户授权。

本清单不代表生产部署，不授权真实付费调用、通知、客户灰度或商业观察；`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 未完成。
