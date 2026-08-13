# G8 Drop 暂存只读取证 012 执行记录

> 状态：`CONSUMED_LOCAL_CHECK_EVIDENCE_UNAVAILABLE`。本记录只描述 012 唯一获批执行的实际结果，不构成再次执行、测试服诊断、清理、安装、运行态审计、生产或商业授权。

## 1. 冻结基线

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012`
- 目标 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- 执行基线：主干 merge commit `cdf8d1b5896758e07fe27ffe424c12bc5e358336`
- 执行时包装器 SHA-256：`e417089d107f9fb92c4e7236b7b0c9bec63df66438b820812624b83b68563a9f`
- 执行时包装器大小：`34630` bytes
- 固定端点：`pc@8.130.9.163:10003`
- 固定暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- 上限：本地检查 1、只读 SSH 1、重试 0、业务请求 0、上游请求 0、费用 0 CNY

执行前只读门禁确认主干、包装器摘要与大小一致，包装器内容与主干一致，工作树洁净。本次不以 hostname、machine-id、云实例身份或 CMDB 作为门禁。

## 2. 唯一执行结果

| 项目 | 实际结果 |
|---|---|
| `--local-check` | `1/1`，固定低敏结果 `G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=FAILED reason=evidence_unavailable` |
| local-check 返回码 | `2` |
| local-check stderr | 空，`0` bytes |
| 只读 SSH | `0/1`，未启动 |
| 重试 | `0` |
| SFTP/SCP/上传/下载 | `0` |
| sudo/Docker/远端修改 | `0` |
| 业务请求/上游请求/费用 | `0 / 0 / 0 CNY` |
| 回滚 | 未连接测试服且未新建远端目标，无目标需要回滚 |

`evidence_unavailable` 是有意固定的低敏失败面，只能证明本地检查没有形成 PASS；它不公开也不能证明具体子原因。不得据此猜测 known_hosts、密钥对、OpenSSH 路径、权限或其他身份材料中的哪一项失败。失败发生后已按授权停止，未执行获批额度中的唯一 SSH。

## 3. 暂存状态与影响

本次没有形成 `ABSENT / NOT_APPLICABLE / NONE`、`PRESENT / PASS / NONE` 或 `PRESENT / MISMATCH / ...` 任一远端三态证据。因此固定 011 暂存的存在性、五文件、manifest 和候选回执完整性继续为 `UNKNOWN`。

没有连接测试服，没有创建、修改或删除远端目标，也没有产生应用层回滚目标。禁止为了确认原因继续读取身份材料、重试 local-check、启动 SSH、确认或清理暂存。

## 4. 消费与防重放

012 ChangeId、授权和全部历史命令均已消费。消费后包装器 SHA-256 为 `962789e0dd5041edb020eb0311bad55608d6ee6be97c324fbfb55aca9982f2ca`，大小为 `34629` bytes；默认所有 CLI 入口均在参数解析、身份材料读取和联网之前固定返回 `change_id_consumed`，退出码 2、stderr 为空。

如需继续，必须使用新的 ChangeId，先完成仓库设计、TDD、独立评审、精确 HEAD CI 和合并，再由用户独立授权。只读取证、清理、安装和安装后运行态审计仍是彼此独立的授权范围。

## 5. 阶段边界与缺陷分级

- 本次执行记录：P0=0、P1=1、P2=0；P1 为 012 本地门禁未通过且 011 暂存继续 `UNKNOWN`，不是生产事故，也不代表远端异常。
- 既有 API、schema、数据库、Bifrost、监控、备份和账务门禁不因本次本地失败而关闭。
- `G8_ENGINEERING_READY` 保持；生产部署、真实付费调用、通知、客户灰度和四周商业观察均未执行，`G8_COMMERCIAL_ACCEPTED` 未完成。
