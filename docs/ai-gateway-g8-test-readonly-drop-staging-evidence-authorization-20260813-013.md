# G8 Drop 暂存只读取证 013 授权清单

## 1. 当前状态

`INVALIDATED_BY_DIAGNOSTIC_FIX / REMOTE_NOT_AUTHORIZED`

工程候选最终 HEAD `1b542dc656b09ace80bcdd370fac360ba19b4091` 经 CI run `31719189481` 12/12 SUCCESS 及独立代码安全、QA、产品/规格 P0/P1/P2=0 后，由 PR #374 按 merge commit `d0349353342bc37a912b1942d743e0c45c75ea80` 合入主干。工程门禁与合并不构成执行授权。2026-08-14 用户仅批准运行无 ChangeId 本地诊断，首次结果为 `FAILED / known_hosts_unavailable`；该步骤未联网且不消耗 013。随后定位到 Windows 最小环境遗漏 `PROGRAMDATA` 的工程缺陷，修复候选复测本地诊断为 PASS，但修复改变了冻结脚本摘要，因此本清单按第 3 节规则失效。当前未授权 013 正式入口、SSH 或任何测试服连接；不得在本清单上替换摘要或执行远端动作。

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
  - SHA-256：`aa1bb957e9b950eed263424a0b1e104695f68cad076e33b74fe5b70e54b320ed`
  - 大小：`14615`
- 一次性 013 包装器：`infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py`
  - SHA-256：`02c48a4aca2387e0baa0c179f9a1ea99c8a981adcdd22551b0010fd7b6fb1dfe`
  - 大小：`22272`

已从 merge commit `d0349353342bc37a912b1942d743e0c45c75ea80` 的原始 Git 对象复核：上述摘要和大小与最终工程 HEAD 一致。后续脚本任一漂移即使本清单失效并须重新完成工程门禁与用户授权。

## 4. 固定五文件

| 文件 | SHA-256 | 大小 | 权限 |
|---|---|---:|---:|
| `SHA256SUMS` | `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f` | 362 | 0600 |
| `ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | 13066129 | 0700 |
| `g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | 18377 | 0700 |
| `manifest.env` | `763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6` | 863 | 0600 |
| `molin-g8-test-readonly-audit.sudoers` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | 416 | 0600 |

`SHA256SUMS` 自身摘要是 011 Windows 候选回执；内容必须精确列出另外四文件。`manifest.env` 必须与设计文档冻结的完整键值集合完全一致。

## 5. 历史关闭结论

- 013 从未获得远端 SSH 授权，也未执行远端动作。
- 013 可执行入口已墓碑化，任何参数都在解析、材料读取和联网前固定返回 `change_id_consumed`。
- 本文件不再包含可复制执行命令；新候选使用 014 ChangeId 和独立授权清单。
- 本清单不代表生产部署，不授权真实付费调用、通知、客户灰度或商业观察。
