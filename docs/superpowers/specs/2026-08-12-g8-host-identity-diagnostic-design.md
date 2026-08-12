# G8 测试服主机身份低敏诊断（007）设计

## 1. 背景与目标

`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006` 已完成唯一一次本地检查和正式只读 SSH，并在 `MACHINE_ID` 门禁返回 `BLOCKED`。该枚举不能区分 `/etc/machine-id` 可读但摘要与批准基线不一致，还是文件不可读或读取过程异常；暂存查找因此没有执行，003 暂存状态继续为 `UNKNOWN`。

本设计新增候选 ChangeId `CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007`，仅用于把 machine-id 门禁收敛为三个固定低敏状态：

- `READABLE_MATCH`：文件可读，内存摘要与仓库冻结的批准基线一致。
- `READABLE_MISMATCH`：文件可读，但内存摘要与批准基线不一致。
- `UNREADABLE`：文件不存在、不可读、为空、超过固定上限或读取/摘要过程异常。

用户已选择方案 A：任何结果都不得输出当前 machine-id 原文或摘要；诊断结果也不得自动修改、替换或批准新的信任基线。

## 2. 范围

### 2.1 包含

- 一个隔离 Python 本地包装器及其单元测试。
- 一次离线 `--local-check`，核对固定 known_hosts、ED25519 私钥 ACL、公私钥指纹和密钥对一致性。
- 在后续另获独立用户授权后，至多一次固定目标只读 SSH，`ConnectionAttempts=1`，零重试。
- 远端仅通过 stdin 执行 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -` 的固定程序。
- 固定分类、消费防重放、低敏错误收敛、分级 CI、独立评审、QA、产品验收和中文证据文档。

### 2.2 不包含

- 本设计和后续工程合并均不授权连接测试服；远端执行必须等待新的独立精确授权。
- 不读取暂存目录、部署目录内容、日志、数据库、Redis、RabbitMQ、Bifrost、监控、备份或业务数据。
- 不执行 SFTP/SCP、上传、下载、创建、修改、移动、删除、sudo、root 控制台、Docker、HTTP 或服务控制。
- 不连接生产服务器，不调用真实付费上游，不发送真实通知，不开放客户灰度。
- 不更新 machine-id 基线；若状态为 `READABLE_MISMATCH`，必须由阿里云 root 控制台、CMDB 或等价独立受控通道另行核验和批准。

## 3. 组件与信任边界

### 3.1 本地包装器

新增 `infra/scripts/run-ai-gateway-g8-test-host-identity-diagnostic.py`：

1. 在导入可替换模块前要求 `sys.flags.isolated`，普通 Python 固定退出 2。
2. 只接受 `--self-test`、`--local-check`、精确 ChangeId、known_hosts、私钥和公钥路径；参数错误不回显调用方内容。
3. 以普通文件、非链接、打开前后 inode 和 SHA-256 `599e6bbb800531d02b22cf9534636ebf8232002fafb8236d294f9d2dba2e3c89` 冻结 `infra/scripts/run-ai-gateway-g8-test-staging-evidence.py`；执行后断言其 004 ChangeId 已消费，并精确核对 `pc@8.130.9.163:10003`、SSH ED25519 主机指纹、本地 ED25519 公钥指纹和身份校验函数契约。
4. 本地身份门禁全部通过后，`--local-check` 直接 PASS，不创建网络连接。
5. 正式模式只构造一次固定 OpenSSH 调用，并使用双线程有界排空 stdout/stderr；每流最多保留 64 KiB 加 1 字节，但持续累计完整长度、行数和摘要，避免内存无界增长。
6. 正式执行完成后，后续证据提交把 `CHANGE_ID_CONSUMED` 固定为 `True`；普通入口必须在加载身份材料和联网前返回 `change_id_consumed`。

### 3.2 远端程序

远端程序只导入 Python 标准库 `sys` 和 `hashlib`，不接受调用方路径或命令参数。程序执行：

1. 验证隔离解释器；失败时非零退出，由本地统一收敛为 `diagnostic_unavailable`。
2. 以二进制只读方式打开固定 `/etc/machine-id`，最多读取 4097 字节。
3. 空内容、超过 4096 字节、打开/读取/关闭/摘要异常统一归为 `UNREADABLE`。
4. 正常读取时仅在内存计算 SHA-256，并与代码内冻结的既有批准摘要比较，得到 `READABLE_MATCH` 或 `READABLE_MISMATCH`。
5. stdout 只输出精确三键：`DIAGNOSTIC_CHANGE_ID`、`TARGET_CHANGE_ID`、`MACHINE_ID_STATE`；stderr 必须为空。

远端程序不得导入 `os`、`pwd`、`grp`、`subprocess`，不得枚举目录，不得打开其他路径，也不得把文件内容或摘要写入任何输出。

## 4. 数据流与结果语义

```text
本地固定身份门禁
  -> local-check PASS（不联网）
  -> 唯一一次固定 SSH
  -> 远端只读 /etc/machine-id
  -> 本地严格解析精确三键
  -> READABLE_MATCH / READABLE_MISMATCH / UNREADABLE
```

本地稳定输出为：

```text
G8_TEST_READONLY_HOST_IDENTITY_DIAG=PASS|BLOCKED
change_id=CHG-G8-TEST-READONLY-HOST-IDENTITY-DIAG-20260812-007
target_change_id=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-20260812-006
machine_id_state=READABLE_MATCH|READABLE_MISMATCH|UNREADABLE
```

- `READABLE_MATCH`：退出 0。只证明批准 machine-id 基线仍匹配；不证明暂存状态、运行态或账务状态。
- `READABLE_MISMATCH`：退出 3。立即停止，不得更新基线，不得继续暂存取证。
- `UNREADABLE`：退出 3。立即停止，后续权限或文件状态诊断需要新的 ChangeId。
- SSH 非零、stderr 非空、输出超限、键集合不精确、未知枚举、重复键或解析异常：退出 2，只输出 `G8_TEST_READONLY_HOST_IDENTITY_DIAG=FAILED reason=diagnostic_unavailable`，不得推定 machine-id 状态。

## 5. 安全约束

- 最大本地检查 1 次、最大 SSH 1 次、重试 0、业务请求 0、上游请求 0、费用上限 0 CNY。
- 固定 `-F none`、`BatchMode=yes`、`StrictHostKeyChecking=yes`、显式 known_hosts 和 IdentityFile，并禁用密码、键盘交互、代理、X11、本地命令、TTY 和全部端口转发。
- 子进程使用最小环境，不继承 `SSH_AUTH_SOCK`、AskPass、调用方 PATH、Python 注入变量或代理配置。
- stdout/stderr、异常、提交信息、PR、CI 和验收文档均不得出现当前 machine-id 原文或摘要。
- 仓库内既有批准摘要仅作为比较常量，不得因本次 SSH 返回值自动变化。
- SSH 和只读文件访问可能产生 sshd、journald、audit 访问日志或 atime；不得宣称操作系统层绝对零写入，也不得删除这些事实。

## 6. 测试设计

采用测试先行，至少覆盖：

1. 普通 Python 拒绝，隔离 Python self-test 通过。
2. 冻结 helper 的类型、inode、摘要和目标契约漂移均失败关闭。
3. `--local-check` 只运行本地身份门禁且不调用 SSH。
4. 真实远端程序在临时只读夹具上分别产生 `READABLE_MATCH`、`READABLE_MISMATCH`、`UNREADABLE`；输出中不含原文或任何 64 位摘要。
5. 文件缺失、权限/读取异常、空文件、4097 字节和摘要异常均为 `UNREADABLE`。
6. 解析器拒绝缺键、额外键、重复键、错误 ChangeId、错误目标 ChangeId、未知状态和非 ASCII 输出。
7. OpenSSH 参数固定，正式路径只有一次进程调用，`ConnectionAttempts=1` 且无重试循环。
8. stdout/stderr 并发排空、64 KiB 边界、超限和管道读取异常均低敏失败关闭。
9. 状态与退出码映射正确；任何传输/协议异常不得误报三态。
10. 消费后在 helper、身份文件和网络访问前拒绝重放。
11. Windows 本地测试、Linux `--network none` 测试、`py_compile`、self-test、`git diff --check` 和差异敏感扫描通过。

## 7. 工程交付与授权门禁

实施阶段至少更新：

- 新包装器及测试。
- `.github/workflows/ci.yml` 的 G8 生产就绪门禁。
- `README.md`、`docs/ai-gateway-g8-acceptance.md`、测试服只读入口 Runbook。
- `docs/ai-gateway-g8-test-readonly-host-identity-diagnostic-authorization-20260812-007.md`。

候选必须完成精确 HEAD 的独立代码安全评审、QA、产品/规格验收和适用 CI，P0/P1=0，并以 merge commit 合并。合并后的授权清单状态只能是 `PENDING_USER_APPROVAL`；用户再次精确批准前不得连接测试服。

即便后续 007 返回 `READABLE_MATCH`，也只允许另行准备新的暂存取证候选，不能直接重放 006。生产部署、真实上游、真实资金、告警联系人、客户灰度和四周商业观察仍需各自授权；`G8_COMMERCIAL_ACCEPTED` 保持未完成。
