# AI 网关 G8 测试服低敏 SSH 传输诊断授权清单（005）

> 当前状态：`PENDING_USER_APPROVAL`。仓库工程门禁已通过，但本文件仍不构成连接测试服的授权；必须由用户再次独立明确批准后才可执行。

工程证据：PR #343 精确 HEAD `80d6114ad097ba4c7760b8f58221ac3d5ce0e5a8`，CI run `31603091477` 12/12 SUCCESS，独立代码安全、QA、产品/规格均为 P0/P1/P2=0，merge commit 为 `932265f3d770b5c30973fab611404d70f4273e34`。这些证据只证明仓库候选工程门禁，不证明远端诊断已经执行。

## 1. ChangeId 与目标

- ChangeId：`CHG-G8-TEST-READONLY-TRANSPORT-DIAG-20260812-005`。
- 目标：`pc@8.130.9.163:10003`。
- 关联已消费 ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-004`。
- 诊断目的：只区分 SSH 退出分类、远端固定 stdout 是否精确匹配、stderr 是否存在及其行数/字节数/SHA-256；不得读取或推断 stderr 正文。
- 候选脚本：`infra/scripts/run-ai-gateway-g8-test-readonly-transport-diagnostic.py`。
- 当前候选脚本 SHA-256：`2174ac5836f5bcd6de829fe6bf6e75c426769ec1ecc6529d7bbfd6ac72ac8aab`；最终授权前必须按合并后的精确文件重新计算，不一致即停止。
- 复用的 004 身份校验辅助脚本 SHA-256 固定为 `599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89`；加载前必须核对普通文件身份与摘要，加载后必须核对 ChangeId、已消费状态、目标、端口、主机公钥及本地身份公钥指纹，任一漂移立即停止。

## 2. 精确命令摘要

1. 以 `python -I` 执行一次 `--local-check`，仅核对固定 known_hosts、显式 ED25519 密钥对、ACL 和密钥对一致性，不联网。
2. 本地检查完整 PASS 后，以相同参数移除 `--local-check`，正式调用一次；只允许固定 OpenSSH、公钥认证、严格 known_hosts、`ConnectionAttempts=1`、零重试。
3. 远端命令固定为 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -`，stdin 只包含隔离解释器检查及固定 PASS 标记；不导入文件或身份数据库模块，不接受远端路径或命令参数。
4. 本地并发流式排空 stdout/stderr，分别只在内存保留最多 64 KiB 加 1 字节用于超限判断，同时累计完整字节数、stderr 行数与 SHA-256；只输出固定诊断字段，禁止输出正文。

## 3. 最大上限与禁止项

- 最大本地检查：1 次；最大 SSH：1 次；重试：0。
- 最大业务请求：0；最大上游请求：0；最大费用：0 CNY。
- 禁止读取暂存目录、部署文件、环境文件、日志、数据库、Redis、RabbitMQ、Bifrost、监控、备份或任何业务数据。
- 禁止 SFTP/SCP、上传、下载、创建、修改、移动、删除、sudo、root 控制台、Docker、服务控制、HTTP 请求、生产连接、真实通知、付费上游和客户灰度。

## 4. 结果与停止条件

- `PASS`：SSH 退出为 0、stdout 精确等于固定标记且 stderr 为空；只证明传输与固定远端 Python 标记程序可用，不证明 003 暂存状态。
- `EXIT_NONZERO`：区分 `TRANSPORT_255`、`REMOTE_NONZERO` 或其他非零分类并停止。
- `STDERR_PRESENT`：只保存存在性、行数、字节数和摘要并停止，不读取正文。
- `STDOUT_MISMATCH`：只保存字节数和摘要并停止，不读取正文。
- `OUTPUT_LIMIT_EXCEEDED`：任一输出超过 64 KiB 时阻断；采集器继续有界排空并计算完整计数与摘要，不把超限输出保存或解释为有效诊断。
- 本地校验失败、执行异常、超时、输出字段越界或任何非固定行为立即停止，不重试。

## 5. 回滚与后续

本 ChangeId 不创建仓库外业务资产，没有应用层回滚动作。SSH 可能产生系统访问审计日志；不得删除日志或以“回滚”为由进行任何远端清理。

任何结果都不授权重放 004、读取暂存状态、清理暂存、安装只读入口或执行运行态审计。只有诊断证据收口后，才可为下一步设计新的 ChangeId 和独立授权。
