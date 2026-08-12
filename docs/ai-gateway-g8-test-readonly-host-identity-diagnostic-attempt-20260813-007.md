# AI 网关 G8 测试服主机身份低敏诊断执行记录（007）

> 结果：`BLOCKED / READABLE_MISMATCH`。本记录只证明唯一一次正式只读 SSH 读取到的主机身份与仓库内既有批准基线不一致；不得据此输出、推导或更新当前 `machine-id` 原文或摘要。

## 1. 执行绑定

- ChangeId：`CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007`。
- 目标：`pc@8.130.9.163:10003`。
- 执行日期：2026-08-13（Asia/Shanghai）。
- 执行基线：`45c14645d93ad781c964d9a57f8efd43f0f1c494`。
- 执行分支：`feature/backend-d-ai-gateway-g8-host-identity-diagnostic-007-attempt`。
- 执行时诊断脚本 SHA-256：`5858ab020ae5f1491af51582bd4079c5ff84b9da251a92d85265887c511c2e50`。
- 冻结 004 helper SHA-256：`599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89`。

## 2. 精确结果

1. 执行前确认当前 HEAD 与 `origin/main` 均为 `45c14645d93ad781c964d9a57f8efd43f0f1c494`，工作树干净，分支、授权状态和两份摘要精确匹配。
2. 唯一一次本地检查返回 `G8_TEST_READONLY_HOST_IDENTITY_DIAG_LOCAL_CHECK=PASS`。
3. 本地检查通过后执行唯一一次正式只读 SSH，返回固定低敏结果：

```text
G8_TEST_READONLY_HOST_IDENTITY_DIAG=BLOCKED
change_id=CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007
target_change_id=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006
machine_id_state=READABLE_MISMATCH
```

4. 收到阻断结果后立即停止，重试次数为 0。
5. 业务请求、上游请求和费用分别为 `0 / 0 / 0 CNY`。

## 3. 证据边界

- `READABLE_MISMATCH` 只表示当前只读值与既有批准摘要不一致，不证明哪一方是正确事实，也不授权更新仓库基线。
- 007 没有读取 003 暂存目录、部署目录内容、日志、数据库、Redis、RabbitMQ、Bifrost、监控、备份或业务数据；003 暂存状态继续为 `UNKNOWN`。
- 未执行 SFTP/SCP、上传、下载、创建、修改、移动、删除、sudo、root 控制台、Docker、HTTP、服务控制、生产连接、真实通知、付费调用或客户灰度。
- SSH 与只读文件访问可能由操作系统自动产生 sshd、journald、audit 访问日志或 atime；本轮未获授权读取或删除这些事实。
- 本记录不包含当前 `machine-id` 原文或摘要，也不得据此反向推导该值。

## 4. 消费与后续门禁

- 测试服运行态问题分级为 `P0=0 / P1=1 / P2=0`：唯一 P1 是主机身份与批准基线不一致，在独立受控来源核验前禁止继续测试服迁移准备。该分级不代表 G8 仓库代码新增缺陷。
- 007 已消费；普通执行入口必须在加载 helper、读取本地身份材料和联网前固定返回 `change_id_consumed`，禁止重放本次本地检查或 SSH。
- 必须使用新的 ChangeId，通过阿里云 root 控制台、CMDB 或等价独立受控来源核验主机身份和既有批准基线；该诊断须重新完成范围、命令、次数、影响、回滚、停止条件、工程门禁和用户独立授权。
- 在独立核验完成前，不得更新批准摘要，不得继续暂存取证、清理、安装或运行态审计。
- 本次结果不改变 `G8_ENGINEERING_READY`，也不构成生产部署、真实付费上游、真实通知、客户灰度、商业观察或 `G8_COMMERCIAL_ACCEPTED`。

## 5. 仓库证据收口

- 执行证据 HEAD：`6edbd89c3c6c1c8392262a775b2ac087caee3df7`。
- CI：run `31650387182`，精确绑定执行证据 HEAD，12/12 SUCCESS，包含后端、双前端、G7、G8 生产就绪、无 Mock 真实后端浏览器验收和必选汇总。
- 独立评审：代码安全、QA、产品/规格对仓库变更均为 `P0=0 / P1=0 / P2=0`；测试服运行态另保留 `P1=1`，即 `READABLE_MISMATCH`。
- PR：[#351](https://github.com/skillixx/-molin/pull/351)，从 Draft 转 Ready 后按 merge commit 合并，未使用 squash。
- merge commit：`492b56b9345592f1b5580e6de9fb1a1dfc540b93`，双父为原主干 `45c14645d93ad781c964d9a57f8efd43f0f1c494` 与执行证据 HEAD。
- 远端执行证据分支已删除。

上述收口只证明 007 执行事实、消费门禁和仓库证据已合并，不关闭主机身份 P1、003 暂存 `UNKNOWN` 或测试服其他运行态门禁。
