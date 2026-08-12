# AI 网关 G8 测试服暂存状态只读取证 004 执行记录

## 1. 结论

`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004` 已消费并停止。

- 本地身份检查：`PASS`。
- 正式包装器调用：1 次。
- 正式结果：`G8_TEST_READONLY_STAGING_EVIDENCE=FAILED reason=remote_evidence_failed`。
- 正式退出码：`2`。
- 重试：0。
- 业务请求：0；上游请求：0；费用：0 CNY。

本次没有形成符合契约的远端状态证据，不能判定暂存目录为 `ABSENT`、`PRESENT/PASS` 或 `PRESENT/MISMATCH`。003 暂存状态继续保持 `UNKNOWN`。

## 2. 执行边界

- 执行前 HEAD 与 `origin/main` 均为 `ab66446a692888aa7935728a71f26daf34bcdf6d`，工作树干净。
- 执行前脚本 SHA-256 为 `4b90221e8af3b6e2c882cac7bd97b2cee947451270eb4b36bbccfe8b336556e0`，与已合并授权清单一致。
- 本地门禁只读取固定 `known_hosts`、显式 ED25519 密钥对和 ACL；没有联网。
- 正式入口只发起一次固定 SSH；没有 SFTP/SCP、上传、下载、删除、sudo、Docker、数据库、队列、服务控制、业务 HTTP 或上游请求。
- 读取和 SSH 可能由操作系统产生 sshd/journald/audit 访问日志，并可能按文件系统策略更新 atime；本记录不宣称操作系统层绝对零写入。

## 3. 失败语义

`remote_evidence_failed` 是固定低敏汇总，表示 SSH 返回码、stderr 或远端 stdout 契约至少一项不满足；为避免泄露主机错误正文，本次没有保存或输出 stderr 内容，也没有在同一授权下追加诊断。

禁止根据该汇总推断：

- SSH 握手一定失败或一定成功；
- 远端 Python 一定启动或一定未启动；
- 暂存目录存在或不存在；
- 任意暂存文件已完整上传、部分上传或未上传。

## 4. 后续门禁

004 及其授权、回执均已消费，禁止重试或重放。仓库入口必须在读取本地身份文件和联网前返回 `change_id_consumed`。

继续只能使用新的 ChangeId，先准备单次、零重试、只输出固定分类与计数/摘要的低敏传输诊断候选。该候选不得读取暂存文件、业务数据、数据库、队列或日志，不得执行 sudo、上传、删除或服务控制；完成代码、测试、独立评审、QA、产品、CI 与 merge commit 后，仍须用户另行批准。

本次结果不关闭测试服 API 停止、运行态 P1=3、schema/Bifrost/监控/账务 UNKNOWN，也不授权生产、付费上游、真实通知、客户灰度或商业观察。
