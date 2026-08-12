# AI 网关 G8 测试服暂存只读取证授权清单（006）

> 当前状态：`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。本文件只是仓库候选，不构成连接测试服的授权。

## 1. ChangeId 与目标

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006`。
- 目标：`pc@8.130.9.163:10003`。
- 目标暂存：已消费 003 的固定路径 `/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003`。
- 部署根：`/home/pc/molin`。
- 006 脚本 SHA-256：`4a4c47525cd4e2d1bd20a2fa87f959fa94a741a8ea468240cc77200bb0205cb3`。
- 冻结 004 helper SHA-256：`599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89`。

## 2. 精确命令摘要

1. 使用 `python -I` 执行唯一一次 `--local-check`，只读核对固定 known_hosts、ED25519 密钥 ACL、指纹和密钥对一致性，不联网。
2. 本地检查完整 PASS 后，使用相同参数执行唯一一次正式只读 SSH；固定 `ConnectionAttempts=1`，零重试。
3. 远端固定 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -`，stdin 仅传递由冻结 004 程序派生的 006 取证程序，不接受路径或命令参数。
4. 取证程序固定部署根和暂存目录 inode；逐文件使用 `O_NOFOLLOW`、fd 前后元数据、最终目录项、文件集、目录 mtime/ctime 及父路径 inode 复核，避免同名替换竞态。
5. 早期门禁失败只返回固定枚举：`IDENTITY`、`MACHINE_ID`、`DEPLOYMENT_ROOT_PATH`、`DEPLOYMENT_ROOT_METADATA`、`STAGING_LOOKUP`、`DEPLOYMENT_ROOT_DRIFT`。
6. 成功证据只允许 `ABSENT`、`PRESENT/PASS` 或 `PRESENT/MISMATCH`；本地有界排空输出，只保留 64 KiB 加 1 字节，不输出错误正文。

## 3. 上限与禁止项

- 最大本地检查：1 次；最大 SSH：1 次；重试：0。
- 最大业务请求：0；最大上游请求：0；最大费用：0 CNY。
- 只允许读取目标账户/组、主机名、`/etc/machine-id`、部署根元数据，以及固定暂存路径的文件名、元数据和摘要。
- 禁止 SFTP/SCP、上传、下载、创建、修改、移动、删除、sudo、root 控制台、Docker、数据库、Redis、RabbitMQ、Bifrost、监控、备份、日志、HTTP、生产、真实通知、付费上游和客户灰度。

## 4. 停止条件与后续

- 任一本地门禁失败、SSH 非零、stderr 非空、输出超限、未知字段、未知枚举或解析异常立即停止，不重试。
- `BLOCKED`：只记录固定 `gate_reason`，不得推定暂存存在或不存在。
- `ABSENT`：关闭 003 暂存 UNKNOWN；无需远端清理。
- `PRESENT/PASS`：证明五文件与冻结候选一致；清理必须另建 ChangeId 并独立授权。
- `PRESENT/MISMATCH`：只记录 `PATH`、`FILE_SET`、`FILE_METADATA`、`FILE_CONTENT` 或 `READ_ERROR`；诊断或清理均须另建 ChangeId。

本轮不创建应用资产，没有应用回滚动作。SSH 与文件读取可能产生系统审计日志或 atime；不得删除日志。任何结果都不授权安装只读入口、运行态审计、生产部署或商业灰度。
