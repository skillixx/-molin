# AI 网关 G8 测试服暂存只读取证执行记录（006）

> 结果：`BLOCKED`。本记录只证明唯一一次正式调用在 `MACHINE_ID` 门禁停止；没有形成暂存三态证据，不能证明 003 暂存目录存在或不存在。

## 1. 执行绑定

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006`。
- 目标：`pc@8.130.9.163:10003`。
- 目标暂存：`CHG-G8-TEST-READONLY-ACCESS-20260812-003` 的固定暂存路径。
- 执行日期：2026-08-12（Asia/Shanghai）。
- 006 脚本 SHA-256：`4a4c47525cd4e2d1bd20a2fa87f959fa94a741a8ea468240cc77200bb0205cb3`。
- 冻结 004 helper SHA-256：`599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89`。

## 2. 精确结果

1. 唯一一次本地检查返回 `G8_TEST_READONLY_STAGING_EVIDENCE_V2_LOCAL_CHECK=PASS`。
2. 本地检查通过后执行唯一一次正式只读 SSH，返回：

```text
G8_TEST_READONLY_STAGING_EVIDENCE_V2=BLOCKED
change_id=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006
target_change_id=CHG-G8-TEST-READONLY-ACCESS-20260812-003
gate_reason=MACHINE_ID
```

3. 收到阻断结果后立即停止，重试次数为 0。
4. 业务请求、上游请求和费用分别为 `0 / 0 / 0 CNY`。

## 3. 证据边界

- `IDENTITY` 前置门禁已经通过，但 `MACHINE_ID` 门禁失败；该固定枚举不能区分摘要不匹配和读取异常，也不授权自行更新受信主机身份。
- 暂存目录查找和五文件摘要取证均未执行，因此 003 暂存状态继续为 `UNKNOWN`。
- 未执行 SFTP/SCP、上传、下载、创建、修改、移动、删除、sudo、root 控制台、Docker、数据库、队列、HTTP、业务请求、上游请求或真实通知。
- SSH 和只读文件访问可能由系统写入 sshd、journald 或 audit 访问日志；本轮未获授权读取或删除这些日志。
- 006 已消费，普通执行入口必须在读取身份文件和联网前以 `change_id_consumed` 失败关闭，禁止重放。

## 4. 后续门禁

继续前必须使用新的 ChangeId，在仓库内准备只读主机身份诊断候选，区分 machine-id 的“可读但漂移”和“不可读”状态；不得输出 machine-id 原文。候选仍须完成分级测试、独立代码安全评审、QA、产品验收、精确 PR HEAD 门禁和 merge commit。只有用户再次独立批准后，才允许新的单次只读连接。

任何后续结果都不授权清理暂存、安装只读入口、运行态审计、生产部署、真实付费调用、通知或客户灰度。

## 5. 仓库证据收口

- 执行证据精确 HEAD：`7157ee0f4a92b73a06855b0a8f35f12f07575ce4`。
- CI run：`31613370496`，12/12 SUCCESS，包含 G8 生产就绪、真实后端浏览器、G7、后端与必选汇总。
- 独立代码安全、QA、产品/规格：P0/P1/P2=0。
- PR：#347，使用 merge commit 合并；merge commit 为 `2399f58143b683b95fdf3011be8a535bfedef222`，远端功能分支已删除。

上述证据只收口仓库内消费门禁和执行事实，不证明测试服暂存状态、只读入口安装、运行态审计、生产部署或商业验收完成。
