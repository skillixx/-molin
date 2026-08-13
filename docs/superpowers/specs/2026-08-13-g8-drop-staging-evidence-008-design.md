# G8 Drop 映射测试服暂存只读取证（008）设计

## 1. 背景与决策

测试入口 `pc@8.130.9.163:10003` 由 Drop 服务映射到测试服务器，并不是可通过云厂商实例身份或稳定物理主机身份识别的直连入口。此前 006、007 把 hostname 和 `/etc/machine-id` 摘要作为暂存取证的前置门禁；007 得到 `READABLE_MISMATCH` 后按当时规则停止。用户现已确认该测试环境无需验证底层服务器身份，因此上述门禁前提不适用于此 Drop 映射场景。

本设计采用已批准的方案 A：新增 Drop 场景专用 ChangeId `CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008`，以固定 SSH 入口和加密身份材料验证传输边界，以登录用户和部署根验证操作范围，但不读取或校验 hostname、`/etc/machine-id`、云实例元数据或 CMDB。

007 的执行事实和 `READABLE_MISMATCH` 记录必须保留，不得改写为 MATCH；其测试服 P1 分类改为“门禁前提不适用，不再阻断 Drop 场景”，但 003 暂存状态仍为 `UNKNOWN`。只有后续 008 获得独立执行授权并形成有效三态证据，才能关闭该 UNKNOWN。

## 2. 方案比较

### 2.1 采用：新建 Drop 专用包装器

新增独立包装器和测试，冻结并复用已合并 004 helper 的本地 SSH、known_hosts、客户端密钥与受控子进程校验能力；008 自己构造不含主机身份门禁的远端暂存取证程序和输出解析器。

优点：历史脚本与证据不漂移；Drop 信任边界清晰；可以单独消费和防重放。缺点：暂存文件取证算法会与 004 helper 保持一份受控副本，需要用行为测试锁定二者的文件白名单、大小和摘要一致性。

### 2.2 不采用：修改历史 004/006/007 脚本

这些脚本已经执行并消费，修改会破坏历史摘要、CI 与执行证据的可追溯性，也可能意外恢复旧 ChangeId 的执行能力。

### 2.3 不采用：把 machine-id 降级为信息字段

即使不阻断，读取或输出该字段仍会制造“它代表 Drop 后端稳定身份”的错误暗示，并扩大远端读取面，没有必要。

## 3. 范围

### 3.1 包含

- 新增 008 本地包装器及自动化测试。
- 一次离线 `--local-check`：核对冻结 helper、严格 known_hosts、ED25519 客户端密钥、ACL、指纹和密钥对一致性，不联网。
- 在另行获得精确用户授权后，至多执行一次固定只读 SSH，`ConnectionAttempts=1`，零重试。
- 远端只读取登录用户、固定部署根元数据及固定 003 暂存目录的文件名、元数据和摘要。
- 输出 `ABSENT`、`PRESENT/PASS`、`PRESENT/MISMATCH` 三态，或固定低敏失败结果。
- ChangeId 消费门禁、分级 CI、独立代码安全评审、QA、产品/规格验收、PR merge commit 和中文证据文档。

### 3.2 不包含

- 本次设计、实现、测试、评审、CI 和合并均不授权连接测试服务。
- 不读取或验证 hostname、`/etc/machine-id`、云实例 ID、云实例元数据、CMDB、日志、数据库、Redis、RabbitMQ、Bifrost、监控、备份或业务数据。
- 不执行 SFTP/SCP、上传、下载、创建、修改、移动、删除、sudo、root 控制台、Docker、HTTP 或服务控制。
- 不清理 003 暂存，不安装只读入口，不运行特权审计器或对账器。
- 不连接生产服务器，不调用真实付费上游，不发送真实通知，不开放客户灰度。

## 4. 信任边界

### 4.1 Drop 映射入口

`8.130.9.163:10003` 只被视为已批准的 Drop SSH 传输入口，不被解释为底层物理服务器或云实例身份。严格 known_hosts 中唯一的 ED25519 条目用于阻止入口被错误主机或中间人替换；这是 SSH 加密端点校验，不是底层服务器资产核验。

本地身份材料必须同时满足：

- known_hosts、私钥和公钥均为调用方显式传入的绝对普通文件且非链接；
- 三者位于同一受控目录；
- known_hosts 中精确存在唯一 `[8.130.9.163]:10003 ssh-ed25519` 条目，指纹等于仓库冻结值；
- 公钥指纹等于仓库冻结值，私钥 ACL 不允许其他账户读取或修改，公私钥匹配；
- helper 是普通文件、非链接，打开前后 inode 和摘要均等于 008 授权清单冻结值；
- helper 的目标、端口、主机公钥指纹、本地公钥指纹、固定 OpenSSH 路径和最小环境契约没有漂移。

### 4.2 远端操作范围

远端固定程序只接受 stdin，不接收调用方路径或命令参数。程序只允许：

1. 通过 `pwd.getpwnam('pc')` 和当前 uid 确认登录用户为 `pc`；不读取 hostname。
2. 以 `lstat`、`realpath`、`O_DIRECTORY | O_NOFOLLOW` 和目录描述符钉住 `/home/pc/molin`，验证它是 `pc:pc`、用户可读写执行、组和其他用户不可写的规范目录。
3. 仅以固定 basename、相对部署根目录描述符检查 `.g8-staging-CHG-G8-TEST-READONLY-ACCESS-20260812-003`。
4. 暂存存在时，仅核对固定五文件白名单、普通文件/非链接、`pc:pc`、组和其他用户不可写、冻结大小及 SHA-256。
5. 对部署根、暂存目录、每个文件描述符和最终目录项执行前后 inode、元数据和文件集稳定性复核，任何竞态均失败关闭。

远端程序不得导入或调用网络、子进程、写文件、删除、重命名、创建目录、sudo 或 shell 能力。

## 5. 组件设计

### 5.1 008 包装器

新增 `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence.py`：

- 在任何可替换模块导入前要求 `sys.flags.isolated`，普通 Python 固定退出 2。
- 仅接受 `--self-test`、`--local-check`、精确 ChangeId、known_hosts、私钥和公钥绝对路径。
- 冻结校验 `infra/scripts/run-ai-gateway-g8-test-staging-evidence.py`，但只复用其本地身份材料、固定 OpenSSH 和最小环境函数；不得调用其已消费 main 或旧远端程序。
- 构造一个新的 Drop 专用远端只读程序和独立解析器，输出键集合不包含 `HOSTNAME`、`MACHINE_ID_SHA256` 或任何实际 uid/gid/mode/摘要。
- 正式模式只启动一个固定系统 OpenSSH 进程；stdout/stderr 使用双线程有界排空，每流最多保留 64 KiB 加 1 字节，同时累计完整字节数、逻辑行数和 SHA-256。
- 任何异常只输出固定低敏错误，不回显本地路径、调用参数、远端 stderr、文件名之外的动态值或 Secret。
- 后续执行证据提交把 `CHANGE_ID_CONSUMED` 固定为 `True`；普通入口必须在 helper、身份材料和网络访问之前返回 `change_id_consumed`。

### 5.2 远端输出契约

成功形成证据时，stdout 只能包含精确九键：

```text
EVIDENCE_CHANGE_ID=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008
TARGET_CHANGE_ID=CHG-G8-TEST-READONLY-ACCESS-20260812-003
LOGIN_USER=pc
DEPLOYMENT_ROOT_REALPATH=/home/pc/molin
DEPLOYMENT_ROOT_CHECK=PASS
STAGING_STATE=ABSENT|PRESENT
STAGING_INTEGRITY=NOT_APPLICABLE|PASS|MISMATCH
STAGING_MISMATCH_REASON=NONE|PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|READ_ERROR
EVIDENCE_RESULT=PASS
```

解析器要求 ASCII、无重复键、无额外键、值与固定 ChangeId 和路径完全一致，并只接受以下组合：

- `ABSENT / NOT_APPLICABLE / NONE`
- `PRESENT / PASS / NONE`
- `PRESENT / MISMATCH / PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|READ_ERROR`

本地稳定输出：

```text
G8_TEST_READONLY_DROP_STAGING_EVIDENCE=PASS|MISMATCH|FAILED
change_id=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-008
target_change_id=CHG-G8-TEST-READONLY-ACCESS-20260812-003
staging_state=ABSENT|PRESENT
staging_integrity=NOT_APPLICABLE|PASS|MISMATCH
staging_mismatch_reason=NONE|PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|READ_ERROR
```

- `ABSENT` 与 `PRESENT/PASS` 退出 0，但只证明暂存三态，不授权清理或安装。
- `PRESENT/MISMATCH` 退出 3，立即停止；后续诊断或清理使用新 ChangeId。
- SSH 非零、stderr 非空、输出超限、键集/枚举/ChangeId 异常、读管道异常或本地校验异常退出 2，只输出固定 `reason=evidence_unavailable`。

## 6. 数据流

```text
冻结 helper 与本地 SSH 身份材料
  -> --local-check PASS（不联网）
  -> 后续独立授权的一次固定 Drop SSH
  -> 登录用户与部署根范围校验
  -> 固定 003 暂存目录只读取证
  -> 严格九键解析
  -> ABSENT / PRESENT-PASS / PRESENT-MISMATCH
  -> 消费 008，禁止重放
```

## 7. 安全和停止条件

- 最大本地检查 1 次、SSH 1 次、重试 0、业务请求 0、上游请求 0、费用上限 0 CNY。
- OpenSSH 固定 `-F none`、`BatchMode=yes`、`StrictHostKeyChecking=yes`、显式 known_hosts 与 IdentityFile，并禁用密码、键盘交互、Agent、X11、本地命令、TTY 和全部端口转发。
- 子进程使用最小环境，不继承调用方 PATH、`SSH_AUTH_SOCK`、AskPass、Python 注入变量或代理配置。
- 任一 helper、known_hosts、密钥、ACL、指纹、密钥对、目标、端口、登录用户、部署根、输出契约或暂存证据不符合预期，立即停止且不重试。
- SSH 和只读文件访问可能产生 sshd、journald、audit 访问日志或 atime；不得宣称系统层绝对零写入，也不得清理这些事实。
- 007 的历史事实保留；008 不更新 machine-id 基线，也不把 Drop 映射入口解释为物理主机身份。

## 8. 测试设计

采用测试先行，至少覆盖：

1. 普通 Python 拒绝，隔离 Python self-test 通过。
2. helper 类型、链接、inode、摘要、目标/端口/指纹和函数契约漂移全部失败关闭。
3. `--local-check` 只执行本地校验，不启动 SSH。
4. 远端程序源码和动态测试证明不读取 hostname、`/etc/machine-id`、实例元数据或其他路径。
5. 临时目录动态形成 `ABSENT`、`PRESENT/PASS` 和五类 `PRESENT/MISMATCH`。
6. 部署根、暂存目录、文件目录项和文件内容替换竞态均失败关闭。
7. 解析器拒绝缺键、额外键、重复键、非 ASCII、错误 ChangeId、错误路径、未知枚举和非法状态组合。
8. OpenSSH 参数固定，正式路径只有一次子进程调用，`ConnectionAttempts=1` 且无重试循环。
9. stdout/stderr 并发排空、64 KiB 精确边界、超限、管道读取异常和 SSH 非零均低敏失败关闭。
10. 消费后在 helper、身份材料和网络访问前拒绝重放。
11. Windows 本地测试、Linux `python:3.13-alpine --network none` 测试、`py_compile`、self-test、`git diff --check` 和敏感信息扫描通过。

## 9. 文档与工程门禁

实施阶段同步更新：

- `.github/workflows/ci.yml` 的 G8 生产就绪门禁；
- `README.md`、`docs/ai-gateway-g8-acceptance.md`、`docs/test-plan.md`、`docs/tools.md`；
- `docs/ai-gateway-g8-test-readonly-access-runbook.md`；
- 新的 008 授权清单。

授权清单必须精确绑定 ChangeId、合并后源码提交、脚本/helper SHA-256、目标、命令摘要、最大次数、0/0/0 CNY、影响、回滚和停止条件，初始状态只能是 `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。只有精确 PR HEAD 完成适用 CI、独立代码安全评审、QA、产品/规格验收且 P0/P1=0，并以 merge commit 合并后，才能收敛为 `PENDING_USER_APPROVAL`。

用户再次明确批准 008 执行前，不得运行本地检查或连接测试服务。008 的任何结果都不授权清理暂存、安装入口、运行特权审计、生产部署、真实付费调用、告警通知、客户灰度或商业观察。`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 继续未完成。
