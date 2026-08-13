# G8 Drop 暂存只读取证 013 设计

## 1. 背景与决策

011 的唯一正式暂存调用只返回了低敏 `invalid_request`，无法判断 SFTP 是否启动、远端独占目录是否创建或五文件是否部分上传。012 随后的唯一 `--local-check` 又返回 `evidence_unavailable`，没有启动 SSH，因此固定 011 暂存仍为 `UNKNOWN`。001 至 012 均已消费，禁止重放。

此前流程把可重复的本地身份材料诊断和一次性远端 ChangeId 绑定在一起。本地材料问题会在联网前消耗整次远端授权，导致“修复本地问题—新建 ChangeId—重新工程门禁”的循环。013 采用已经批准的方案 A，将两类能力彻底拆分：

1. 新增完全独立、无 ChangeId、可重复执行的本地身份材料诊断器；它不包含 SSH、SFTP 或任何远端访问能力。
2. 新增一次性远端只读取证器 013；它不提供 `--local-check`，只在未来另行取得用户执行授权后最多发起一次固定只读 SSH。
3. 后续清理、审计入口安装、测试候选部署和运行态审计仅形成总方案、状态机和验收矩阵，本 PR 不生成这些阶段的远端可执行资产。

013 的 ChangeId 固定为：

`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-013`

目标历史暂存固定为：

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`

## 2. 范围

### 2.1 本 PR 包含

- 一份无 ChangeId、可重复执行的本地身份材料诊断器及自动化测试。
- 一份独立的一次性 013 只读取证器及自动化测试。
- 013 远端只读程序、严格九键解析、固定六行低敏结果和消费门禁。
- Windows 本地测试与 Linux `--network none` 动态测试。
- Draft/Ready 分级 CI 接入、中文工具文档、Runbook、验收矩阵和 013 独立授权清单。
- 清理、审计入口安装、测试候选部署、运行态审计的总状态机和验收矩阵。
- 独立代码安全、QA、产品/规格评审和 Draft PR。

### 2.2 本 PR 不包含

- 不运行本地诊断器的真实身份材料检查；自动化测试只使用临时伪造材料。
- 不运行 013 正式入口，不执行 SSH、SFTP、SCP、上传、下载、删除或远端修改。
- 不执行 sudo、root、Docker、服务控制、数据库、Redis、RabbitMQ、业务队列、日志、监控或备份读取。
- 不执行业务 HTTP、真实付费上游、真实通知、客户灰度或生产连接。
- 不清理 011 暂存，不安装只读审计入口，不部署或启动 G8 测试候选，不执行运行态审计。
- 不把 `G8_ENGINEERING_READY` 解释为 `G8_COMMERCIAL_ACCEPTED`。

## 3. 固定目标与证据基线

### 3.1 Drop 端点

- SSH 端点：`pc@8.130.9.163:10003`
- 部署根：`/home/pc/molin`
- 目标暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- 物理主机身份：`NOT_APPLICABLE`
- 服务端 ED25519 指纹：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`
- 客户端 ED25519 公钥指纹：`SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0`

不得读取或推断 hostname、`/etc/machine-id`、云实例元数据或 CMDB。

### 3.2 固定五文件

目标目录必须恰好包含以下五个普通非链接文件：

| 文件 | SHA-256 | 大小 | 权限 |
|---|---|---:|---:|
| `SHA256SUMS` | `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f` | 362 | 0600 |
| `ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | 13066129 | 0700 |
| `g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | 18377 | 0700 |
| `manifest.env` | `763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6` | 863 | 0600 |
| `molin-g8-test-readonly-audit.sudoers` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | 416 | 0600 |

`SHA256SUMS` 的自身摘要也是 011 Windows 候选回执；其内容必须精确列出另外四个文件。`manifest.env` 必须精确绑定 011 ChangeId、source commit `099c38ed62ccd62c3c5a3b6811f1369d7f0d3084`、source tree `c2d1252a05d031d842549345128fa7a1ffe53dc8`、`DROP_SSH_INTERACTIVE_SUDO`、`NOT_APPLICABLE`、固定端点、部署根、制品摘要、对账器大小和双构建次数。

## 4. 无 ChangeId 本地诊断器

### 4.1 文件与能力边界

新增 `infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py`。

诊断器只接受三个绝对路径参数：known_hosts、ED25519 私钥和对应公钥。目标端点、服务端指纹和客户端指纹全部内置冻结。它只允许：

- 读取并冻结本地系统 `ssh-keygen`、known_hosts、私钥和公钥。
- 使用本地 `ssh-keygen -F`、`ssh-keygen -lf` 和 `ssh-keygen -y` 校验主机条目、批准指纹及公私钥配对。
- 创建进程内临时数据；不得复制、chmod、修改 ACL 或写回任何身份材料。

源码中不得包含 SSH/SFTP/SCP 客户端调用、socket、远端命令、远端 Python、目标用户登录或上传下载能力。CI 通过源码契约测试锁定这一边界。

### 4.2 本地材料冻结

所有路径必须为绝对路径。每个文件先 `lstat`，再以 `O_RDONLY | O_NOFOLLOW | O_CLOEXEC` 打开同一文件描述符，使用 `fstat` 对齐 dev/inode/type/size/mtime/ctime，在同一 fd 上读取并计算 SHA-256，读取后再次 `fstat`，最后再次 `lstat` 当前目录项。任一类型、链接/reparse、inode、大小或时间元数据漂移立即失败关闭。

Windows 还必须拒绝 reparse point；不得放宽或重写 NTFS ACL。诊断输出不得包含路径、指纹、摘要、密钥正文、环境变量值或子进程 stderr。

### 4.3 known_hosts 与密钥语义

使用固定系统 `ssh-keygen -F '[8.130.9.163]:10003' -f <known_hosts>` 同时枚举明文和哈希目标条目：

- ED25519 命中必须恰好一条，且指纹精确等于批准值。
- 同端点的额外 ED25519 命中必须拒绝。
- 同端点的 RSA/ECDSA 条目允许存在，因为未来 013 SSH 固定 `HostKeyAlgorithms=ssh-ed25519`，且只消费从批准 ED25519 行派生的临时单行 known_hosts；其他算法不会进入协商。
- 错误端点、未知格式或无法安全解析时失败关闭。

私钥通过 `ssh-keygen -y` 派生公钥，与显式公钥的算法和 key blob 精确一致；公钥指纹必须等于批准客户端指纹。任何非零、stderr、超限或输出异常均低敏失败。

### 4.4 固定输出与退出码

成功只输出：

```text
G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=PASS
```

失败只输出以下固定单行之一并退出 2：

```text
G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=invalid_request
G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=tool_unavailable
G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=known_hosts_unavailable
G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=identity_unavailable
G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=FAILED reason=materials_drift
```

stderr 必须为空。分类只表达本地低敏阶段，不输出实际值。诊断器可重复运行，不改变任何远端 ChangeId 生命周期。

## 5. 一次性 013 只读取证器

### 5.1 文件与依赖冻结

新增 `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py`。

013 不导入或调用已消费的 012。它可以按固定摘要加载第 4 节本地诊断模块的已验证字节，但必须在执行前后核验模块为普通非链接文件、fd 与目录项 inode 一致、摘要一致，并断言固定函数/常量契约。授权清单同时冻结 013 包装器和本地诊断器摘要。

013 初始为 `CHANGE_ID_CONSUMED = False`。执行证据提交后必须翻转为 `True`；消费态所有入口必须在 argparse、helper、身份材料和网络读取前固定返回 `change_id_consumed`。

013 只提供：

- `--self-test`：离线编译并核验冻结契约，不读取真实身份材料、不联网。
- 正式入口：精确 013 ChangeId、known_hosts、私钥和公钥的绝对路径。

013 不提供 `--local-check`。本地诊断 PASS 是未来申请 013 执行授权的外部门禁，但不消耗 013。

### 5.2 单次 SSH

正式入口先调用冻结的本地诊断能力并形成材料证据，再派生仅含批准 ED25519 条目的临时单行 known_hosts。它只启动一次固定系统 OpenSSH：

```text
-F none
-p 10003
BatchMode=yes
IdentitiesOnly=yes
ConnectionAttempts=1
StrictHostKeyChecking=yes
HostKeyAlgorithms=ssh-ed25519
PasswordAuthentication=no
KbdInteractiveAuthentication=no
ForwardAgent=no
ClearAllForwardings=yes
RequestTTY=no
```

显式使用批准私钥和临时单行 known_hosts；禁止 Agent、密码、键盘交互、TTY、转发和本地命令。远端命令固定为 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -`。无重试循环，正式路径最多一个 `Popen`。

stdout/stderr 用双线程有界排空，每流最多保留 `64 KiB + 1`，同时累计完整字节数、逻辑行数和 SHA-256。超限、读取异常、超时、非零返回码、任意 stderr、材料漂移或输出契约异常均统一失败关闭，不回显正文。

### 5.3 远端只读程序

远端程序仅可使用 NSS、`lstat`、`realpath`、`open`、`fstat`、`listdir`、只读分块读取、SHA-256 和 stdout：

1. 确认当前 uid 对应 `pc`。
2. 以部署根真实路径和目录 fd 锚定 `/home/pc/molin`，核验普通非链接目录、`pc:pc`、组和其他用户不可写及完整元数据稳定性。
3. 只用部署根 fd 和固定 basename 查询 011 暂存；不存在时形成 `ABSENT`。
4. 存在时固定 stage fd，核验 `pc:pc:0700` 和完整五文件集合。
5. 每个文件先检查目录项类型，再用 `O_RDONLY | O_NOFOLLOW` 打开；元数据合规后才读取和哈希。
6. 精确解析 `SHA256SUMS` 与 `manifest.env`。
7. 哈希后复核文件 fd、当前目录项、文件集合、stage fd/目录项、部署根 fd/目录项的完整元数据。

远端程序不得导入或调用 socket、subprocess、shell、写文件、创建目录、chmod、chown、rename、unlink、sudo 或 Docker。sshd/journald/audit 日志和文件系统 atime 可能由系统产生，文档不得宣称操作系统层绝对零写入。

## 6. 013 输出契约

远端成功证据必须恰好包含以下九键、ASCII、无重复或额外键：

```text
EVIDENCE_CHANGE_ID=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-013
TARGET_CHANGE_ID=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011
LOGIN_USER=pc
DEPLOYMENT_ROOT_REALPATH=/home/pc/molin
DEPLOYMENT_ROOT_CHECK=PASS
STAGING_STATE=ABSENT|PRESENT
STAGING_INTEGRITY=NOT_APPLICABLE|PASS|MISMATCH
STAGING_MISMATCH_REASON=NONE|PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|MANIFEST|RECEIPT|READ_ERROR
EVIDENCE_RESULT=PASS
```

本地稳定输出恰好六行：

```text
G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013=PASS|MISMATCH|FAILED
change_id=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-013
target_change_id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011
staging_state=ABSENT|PRESENT
staging_integrity=NOT_APPLICABLE|PASS|MISMATCH
staging_mismatch_reason=NONE|PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|MANIFEST|RECEIPT|READ_ERROR
```

合法三态：

- `ABSENT / NOT_APPLICABLE / NONE`：退出 0。
- `PRESENT / PASS / NONE`：退出 0。
- `PRESENT / MISMATCH / 固定原因`：退出 3。

任何材料、SSH、stderr、超限、超时、返回码、解析或内部异常只输出：

```text
G8_TEST_READONLY_DROP_STAGING_EVIDENCE_013=FAILED reason=evidence_unavailable
```

并退出 2。正式调用无论形成三态还是 `evidence_unavailable`，都在随后证据提交中消费 013，禁止重试。

## 7. 后续总状态机

本 PR 只实现 `EVIDENCE_013_READY`，不实现后续动作：

```text
STAGING_UNKNOWN
  -> LOCAL_MATERIALS_PASS（可重复、无 ChangeId、无联网）
  -> EVIDENCE_013_AUTHORIZED（未来独立授权）
  -> ABSENT | PRESENT_PASS | PRESENT_MISMATCH | EVIDENCE_UNAVAILABLE

ABSENT
  -> CLEANUP_NOT_REQUIRED
  -> 新 ChangeId 准备全新候选和安装

PRESENT_PASS | PRESENT_MISMATCH
  -> 新 ChangeId 只清理固定 011 暂存
  -> 清理后另一个新 ChangeId 复核 ABSENT
  -> 再以新 ChangeId 准备全新候选和安装

EVIDENCE_UNAVAILABLE
  -> 保持 STAGING_UNKNOWN
  -> 新 ChangeId 诊断，不得重试 013

审计入口安装完成
  -> 新 ChangeId 部署/启动测试候选
  -> 新 ChangeId 运行态只读审计
  -> TEST_RUNTIME_ACCEPTED 或继续保持 P1/UNKNOWN
```

即使 011 暂存 `PRESENT/PASS`，也不得直接安装历史候选；必须先独立清理，再生成新的安装候选。每个箭头均需独立设计、工程门禁和用户授权。

## 8. 后续验收矩阵

| 阶段 | 必须证据 | 通过条件 | 不自动授权 |
|---|---|---|---|
| 013 暂存取证 | 固定路径和五文件三态 | 低敏契约、零重试、P0/P1=0 | 清理、安装 |
| 暂存清理 | 清理前证据、精确目标、只删本次授权目标、清理后 ABSENT | 无预存目标误删、回滚/停止条件成立 | 安装 |
| 审计入口安装 | root-only 副本、no-clobber、父链、摘要、visudo、精确 sudo 范围、pc 非 Docker 组、self-test | 三 live 目标 root:root、0755/0440、只允许固定审计器 | G8 服务部署 |
| 测试候选部署 | 精确制品、配置、migration、进程和回滚 | API/worker 启动且不连接生产/付费上游 | 商业灰度 |
| 运行态审计 | 固定 sudo 审计器输出 | schema 66 且 dirty=0；MySQL/Redis/RabbitMQ/Bifrost/监控/备份/账务无 UNKNOWN；固定健康端点通过 | 生产部署 |
| 商业验收 | 生产授权、客户灰度和连续观察 | 满足 G8 商业指标及四周观察 | 无，满足后才能完成 Goal |

运行态审计还必须覆盖：Bifrost 双节点和负载均衡摘要/健康、运行变量键白名单、22 条告警、16 个 Grafana 面板、Alertmanager 丢弃或受控路由、正常差异与七类异常、holds/outbox/补偿为零、备份可读性与摘要。不得在共享数据库执行恢复演练。

## 9. TDD 与安全测试边界

### 9.1 本地诊断器

- 普通 Python 拒绝、隔离 Python 自检通过。
- 无 ChangeId、重复运行结果稳定。
- 源码契约拒绝 SSH/SFTP/SCP/socket/远端命令能力。
- 相对路径、链接/reparse、非普通文件、fd/目录项替换、读取中漂移均失败关闭。
- known_hosts 的正确 ED25519、重复 ED25519、哈希重复、错误指纹、允许的 RSA/ECDSA 共存分别有正负例。
- 公私钥匹配、错误配对、错误批准指纹、ssh-keygen 非零/stderr/超限分别失败关闭。
- 固定低敏输出、stderr 空、敏感哨兵不回显。

### 9.2 013 包装器与远端程序

- 初始未消费；消费转换测试保证所有入口在 argparse/helper/材料/网络前拒绝。
- 不存在 `--local-check`，正式模式只允许精确 013 ChangeId。
- helper 类型/inode/摘要/契约漂移失败关闭。
- 单 OpenSSH、`ConnectionAttempts=1`、零重试、最小环境和固定参数。
- 双流并发排空、64 KiB 精确边界、超限、超时、读取异常、非零、任意 stderr 失败关闭。
- 九键精确解析、六行低敏输出、三态与退出码精确对应。
- ABSENT、PRESENT/PASS、八类 MISMATCH 动态覆盖。
- 部署根、stage、文件 fd/目录项/集合/权限/内容在检查中的替换与漂移失败关闭。
- 文件 symlink 归 `FILE_METADATA`，stage symlink/路径竞态归 `PATH`，真实系统读取异常归 `READ_ERROR`。
- Windows 测试和 Linux `python:3.13-alpine --network none` 测试均执行；CI 不传真实身份材料，不建立网络。

## 10. CI、评审与授权生命周期

Draft PR 仅运行分级轻量和定向门禁；本地必须额外完成完整 Windows 与 Linux 断网测试。只有未来获得转 Ready 授权后，同一精确 HEAD 才运行全部适用重型门禁。不得删除或弱化现有测试。

013 授权生命周期：

```text
DESIGN_APPROVED
  -> IMPLEMENTED_AND_TESTED
  -> DRAFT_REVIEWED
  -> READY_FULL_CI（需另行授权）
  -> MERGED_ENGINEERING_EVIDENCE（需另行授权）
  -> PENDING_USER_APPROVAL
  -> 单次正式 SSH（需另行明确授权）
  -> CONSUMED
```

本轮在 Draft PR 和独立评审证据处停止。不得转 Ready、合并、连接测试服或执行 013。授权清单必须冻结 ChangeId、目标、脚本和本地诊断器 SHA/大小、精确命令、次数、输出、影响、回滚和停止条件；PR 精确 HEAD 通过正文或固定评论绑定，HEAD 漂移即失效。

## 11. 完成定义

- 设计、实施计划、代码、测试、CI 和中文文档一致，不存在两套真相。
- 本地诊断器无 ChangeId、可重复且没有远端访问能力。
- 013 没有 `--local-check`，只有未来授权后的一次 SSH 能力。
- Windows、Linux 断网、py_compile、自检、敏感扫描、`git diff --check` 全部通过。
- 独立 Standards/安全、QA、产品/规格评审均为 P0=0、P1=0；P2 必须显式处置。
- 创建 Draft PR 并取得精确 HEAD 的 Draft 分级 CI 证据。
- 未连接测试服、未执行远端操作，011 暂存继续为 `UNKNOWN`。
- `G8_ENGINEERING_READY` 保持；`G8_COMMERCIAL_ACCEPTED` 未完成，Goal 不得标记完成。
