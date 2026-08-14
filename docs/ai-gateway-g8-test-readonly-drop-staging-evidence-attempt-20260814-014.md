# G8 Drop 暂存只读取证 014 执行记录

> 状态：`CONSUMED_PRESENT_PASS`。本记录只描述 014 唯一获批执行的低敏结果，不构成再次执行、清理、安装、部署、运行态审计、生产或商业授权。

## 1. 冻结基线

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260814-014`
- 目标 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- 执行基线：主干 merge commit `d05012f5c2b1983bd7e60562bbfd816e5f14f550`
- 执行时诊断器 SHA-256：`3382b66c289c08b54ad36abc78969983ce89a89b7216e84c23b31aec6e34cadf`
- 执行时诊断器大小：`15833` bytes
- 执行时包装器 SHA-256：`a2b20b22fe97769d49a88e80338380c3392411466ec94ebdfea63e51567809d8`
- 执行时包装器大小：`22846` bytes
- 固定端点：`pc@8.130.9.163:10003`
- 固定暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- 上限：本地诊断 1、只读 SSH 1、重试 0、业务请求 0、上游请求 0、费用 0 CNY

执行前已确认 PR `#378` 按 merge commit `d05012f5c2b1983bd7e60562bbfd816e5f14f550` 合入 main，远端 closeout 分支已删除；从 `origin/main` 原始 Git blob 复核诊断器、013 和 014 的大小、SHA-256 与 LF 均精确匹配。执行工作树随后以 detached HEAD 精确指向该 merge commit。

## 2. 唯一执行结果

执行日期：`2026-08-14`（Asia/Shanghai）。

| 项目 | 实际结果 |
|---|---|
| 本地诊断门禁 | `1/1`，精确输出 `G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=PASS` |
| 只读 SSH | `1/1` |
| 包装器返回码 | `0` |
| `staging_state` | `PRESENT` |
| `staging_integrity` | `PASS` |
| `staging_mismatch_reason` | `NONE` |
| 重试 | `0` |
| SFTP/SCP/上传/下载 | `0` |
| 清理/安装/sudo/Docker/数据库/队列/服务控制 | `0` |
| 业务请求/上游请求/费用 | `0 / 0 / 0 CNY` |

固定低敏输出为：

```text
G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=PASS
change_id=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260814-014
target_change_id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011
staging_state=PRESENT
staging_integrity=PASS
staging_mismatch_reason=NONE
```

本次结果证明固定 011 暂存目录存在，且冻结五文件的集合、元数据、内容、manifest 与回执完整性全部通过。它不证明只读入口已经安装，不证明 API、数据库、Bifrost、监控、备份或账务运行态可用，也不授权把暂存内容安装到 live 目标。

## 3. 消费与防重放

014 ChangeId、授权和历史正式命令均已消费。包装器已收敛为墓碑入口，任何参数都必须在参数解析、身份材料读取和联网前固定返回：

```text
G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=FAILED reason=change_id_consumed
```

墓碑入口退出码为 `2`，stderr 为空；当前大小为 `414` bytes，SHA-256 为 `378abc9b143113750d5cf5bcd70e00fc7dcfe83ac0f0347f9f006ef0b7742e1b`。不得通过恢复历史包装器、改变参数或复制历史命令重放 014。

## 4. 下一阶段边界

011 暂存从 `UNKNOWN` 收敛为 `PRESENT / PASS / NONE`，因此后续无需再次诊断其存在性或完整性。下一步若要安装最小只读入口，必须使用新的 ChangeId，明确冻结 root/交互 sudo 边界、安装目标、回滚步骤与安装后只读 self-test，并重新完成工程门禁、独立代码安全/QA/产品复评、精确 HEAD CI、主线合并和用户独立授权。

清理、安装、部署测试候选、运行态审计、业务旅程、生产、真实付费上游、客户流量、真实通知和四周商业观察均不属于 014 授权。

## 5. 当前验收结论

- 014 本次只读取证形成预期三态且未发生重试或越界动作；工程收口缺陷等级以本分支后续独立复评为准。
- 011 暂存存在性与完整性差额已关闭。
- 测试服最小只读入口仍未安装；既有 API 停止以及 schema、数据库、Bifrost、监控、备份和账务等运行态差额仍未关闭。
- `G8_SOFTWARE_CLOSED_LOOP` 尚未完成；`G8_COMMERCIAL_ACCEPTED` 未开始。

## 6. 工程收口待办

本执行记录和 014 墓碑入口仍需形成精确 HEAD 提交，通过 CI 与独立代码安全、QA、产品/规格复评后合入主干。完成前只能报告当前工作树范围的执行事实，不得把本地记录视为主线证据。
