# G8 Drop 暂存只读取证 012 设计

## 1. 背景与决策

`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011` 已完成唯一一次本地检查和唯一一次正式暂存包装器调用。正式调用返回固定低敏 `invalid_request`、退出码 2、stderr 为空，并按停止条件零重试。该结果不能区分 SFTP 是否启动、远端独占目录是否创建或五文件是否部分上传，因此 011 暂存状态必须继续登记为 `UNKNOWN`。

001 至 011 均已消费。不得重放 011、修复旧包装器后再次调用，或把本地候选目录当作远端状态证据。用户已批准方案 A 的仓库设计、实现、测试、评审、CI 和 PR，但没有授权连接测试服。

本设计新增独立 ChangeId：

`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012`

012 仅用于对固定 011 暂存路径形成存在性和完整性三态证据。它不清理暂存、不安装入口，也不恢复 011 的任何能力。

## 2. 方案比较

### 2.1 采用：精确五文件只读取证

012 使用一个新的、独立冻结的只读包装器。在未来获得独立执行授权后，包装器最多发起一次固定 Drop SSH，在远端只读核验固定部署根、固定 011 暂存目录以及五文件白名单、类型、属主、权限、大小、摘要、manifest 和候选回执，输出 `ABSENT`、`PRESENT/PASS` 或 `PRESENT/MISMATCH`。

该方案能为后续“无需清理”“需要独立清理”或“可据完整暂存另行准备安装候选”提供足够证据，同时不产生远端写能力。

### 2.2 不采用：仅检查目录是否存在

目录存在不代表五文件完整、可信或可安全清理。该方案无法区分部分上传、额外文件、链接替换、权限漂移或内容漂移，不能安全指导后续动作。

### 2.3 不采用：修改或调用已消费的 008/011 入口

008 和 011 的 ChangeId、目标、授权与执行次数均已消费。修改历史入口会破坏执行证据的可追溯性，也可能恢复旧授权能力。012 可以参考已审计算法，但必须使用独立文件、独立常量、独立测试和独立消费门禁，不得调用历史 `main`。

## 3. 范围

### 3.1 包含

- 新增 012 独立只读包装器及自动化测试。
- 一次离线 `--local-check`，只校验包装器契约、固定 OpenSSH、known_hosts、ED25519 客户端密钥对、指纹和本地材料稳定性，不联网。
- 在未来另行获得用户明确授权后，最多执行一次固定只读 SSH，`ConnectionAttempts=1`，零重试。
- 远端只读核验登录用户、部署根、固定 011 暂存路径和固定五文件。
- 输出 `ABSENT`、`PRESENT/PASS` 或 `PRESENT/MISMATCH` 三态。
- 分级 CI、独立代码安全评审、QA、产品/规格验收、PR merge commit 和中文证据文档。
- 执行后将 012 转为消费态，所有入口在本地材料或网络读取前固定拒绝重放。

### 3.2 不包含

- 当前设计、实现、测试、评审、CI 和合并均不授权运行 `--local-check` 或连接测试服。
- 不执行 SFTP、SCP、上传、下载、创建、修改、移动、删除、sudo、root、Docker、服务控制或业务 HTTP。
- 不读取 hostname、`/etc/machine-id`、云实例元数据、CMDB、环境变量、日志、监控、备份、数据库、Redis、RabbitMQ、Bifrost、业务队列或业务数据。
- 不清理 011 暂存，不安装只读入口，不执行特权审计器或对账器。
- 不连接生产服务器，不调用真实付费上游，不发送真实通知，不开放客户灰度。
- 不包含图片、音频、视频、多模态异步任务、对象存储生命周期、GPU、Agent、Skills 或公开自助支付。

## 4. 冻结目标与证据基线

### 4.1 固定目标

- Drop SSH：`pc@8.130.9.163:10003`。
- 部署根：`/home/pc/molin`。
- 目标 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- 目标暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- 物理主机身份：`NOT_APPLICABLE`。不读取或推断底层物理主机身份。
- Drop ED25519 指纹：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`。
- 客户端 ED25519 指纹：`SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0`。

### 4.2 固定五文件

目标目录必须恰好包含以下五个普通非链接文件，不允许额外目录项：

| 文件 | SHA-256 | 大小 | 期望权限 |
|---|---|---:|---:|
| `SHA256SUMS` | `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f` | `362` | `0600` |
| `ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | `13066129` | `0700` |
| `g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | `18377` | `0700` |
| `manifest.env` | `763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6` | `863` | `0600` |
| `molin-g8-test-readonly-audit.sudoers` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | `416` | `0600` |

`SHA256SUMS` 文件自身的 SHA-256 即 011 Windows 候选回执。其内容必须恰好列出另外四个文件及上表摘要。`manifest.env` 必须按精确键集合和值核验，至少包括 011 ChangeId、source commit `099c38ed62ccd62c3c5a3b6811f1369d7f0d3084`、source tree `c2d1252a05d031d842549345128fa7a1ffe53dc8`、`DROP_SSH_INTERACTIVE_SUDO`、`NOT_APPLICABLE`、固定端点、部署根、三项制品摘要、对账器大小和双构建次数。不得把 Linux 临时复现回执替代 Windows 实际候选回执。

目标暂存目录必须是 `pc:pc:0700`；五文件必须是 `pc:pc`。部署根必须是固定真实路径、普通非链接目录、`pc:pc`，且组和其他用户不可写。

## 5. 组件设计

### 5.1 012 独立包装器

新增 `infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py`：

- 仅在隔离 Python 中运行；普通 Python 固定失败。
- 仅接受 `--self-test`、`--local-check`、精确 012 ChangeId、known_hosts、私钥和公钥绝对路径。
- 不导入或调用 008、011 的已消费入口。允许复用经测试锁定的只读算法思想，但生产代码必须是 012 自身的冻结实现。
- 本地先冻结并复核系统 OpenSSH、`ssh-keygen`、known_hosts、私钥、公钥的普通文件类型、非 reparse/非链接、路径、大小、时间元数据和 SHA-256，再做语义校验，SSH 前再次复核。
- known_hosts 必须通过固定 `ssh-keygen -F '[8.130.9.163]:10003' -f` 枚举明文和哈希命中；去除注释后，全部命中必须恰好只有一条，算法必须为 ED25519，指纹必须为批准值。任何额外 ED25519、RSA、ECDSA 或其他算法条目均失败关闭。SSH 只消费由该批准条目派生的临时单行 known_hosts。
- 公私钥必须匹配，客户端公钥指纹必须精确等于冻结值；不得复制、修改或放宽原私钥 ACL。
- 正式模式只启动一个固定系统 OpenSSH 进程，不包含重试循环。
- stdout 和 stderr 使用双线程有界排空；每流最多保留 `64 KiB + 1`，同时累计完整字节数、逻辑行数和 SHA-256。任何读取异常只收敛为固定低敏错误。
- 任何异常均不得回显本地路径、调用参数、远端 stderr、实际摘要、uid/gid/mode 或凭据。
- 后续执行证据提交把 `CHANGE_ID_CONSUMED` 固定为 `True`；包括 `--self-test`、`--local-check`、帮助、未知参数和正式入口在内的所有入口必须在 argparse、材料和网络读取前固定返回 `change_id_consumed`。

### 5.2 远端只读程序

远端程序仅通过 stdin 交给 `/usr/bin/python3 -I -`，不接受路径或命令参数。程序只允许：

1. 使用 NSS 确认当前 uid 对应固定登录用户 `pc`。
2. 以 `lstat`、`realpath`、`O_DIRECTORY | O_NOFOLLOW` 和目录描述符固定部署根，并核验完整安全元数据。
3. 以固定 basename 和部署根描述符查询 011 暂存；不存在时输出 `ABSENT`。
4. 暂存存在时固定其目录描述符，核验目录为 `pc:pc:0700`，并检查完整五文件集合。
5. 对每个文件执行目录项 `lstat`、`O_RDONLY | O_NOFOLLOW` 打开、`fstat`、有界分块 SHA-256、解析固定 `SHA256SUMS` 与 `manifest.env`。
6. 哈希前后复核文件描述符元数据，重新列出文件集合，并复核当前目录项与原文件身份一致。
7. 输出前再次复核暂存目录描述符、暂存目录项以及部署根描述符和绝对路径目录项的完整安全元数据。

生产远端程序不得导入或调用 socket、subprocess、写文件、创建目录、chmod、chown、rename、unlink、sudo 或 shell 能力。对只读打开可能触发的 sshd/journald/audit 访问日志或文件系统 atime，文档必须诚实说明，不得宣称操作系统层绝对零写入。

## 6. 输出契约

远端成功形成证据时，stdout 必须只包含精确九键：

```text
EVIDENCE_CHANGE_ID=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012
TARGET_CHANGE_ID=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011
LOGIN_USER=pc
DEPLOYMENT_ROOT_REALPATH=/home/pc/molin
DEPLOYMENT_ROOT_CHECK=PASS
STAGING_STATE=ABSENT|PRESENT
STAGING_INTEGRITY=NOT_APPLICABLE|PASS|MISMATCH
STAGING_MISMATCH_REASON=NONE|PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|MANIFEST|RECEIPT|READ_ERROR
EVIDENCE_RESULT=PASS
```

解析器要求 ASCII、无重复键、无额外键、精确 ChangeId、精确路径，并只接受：

- `ABSENT / NOT_APPLICABLE / NONE`
- `PRESENT / PASS / NONE`
- `PRESENT / MISMATCH / PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|MANIFEST|RECEIPT|READ_ERROR`

本地稳定输出：

```text
G8_TEST_READONLY_DROP_STAGING_EVIDENCE_012=PASS|MISMATCH|FAILED
change_id=CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012
target_change_id=CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011
staging_state=ABSENT|PRESENT
staging_integrity=NOT_APPLICABLE|PASS|MISMATCH
staging_mismatch_reason=NONE|PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|MANIFEST|RECEIPT|READ_ERROR
```

- `ABSENT` 和 `PRESENT/PASS` 退出 0，但只证明固定暂存状态，不授权清理或安装。
- `PRESENT/MISMATCH` 退出 3，立即停止；后续诊断或清理必须使用新 ChangeId。
- SSH 非零、stderr 非空、输出超限、键集合或枚举异常、ChangeId 漂移、读取管道异常、本地材料漂移或远端程序异常均退出 2，只输出固定 `reason=evidence_unavailable`。

## 7. 数据流与状态机

```text
冻结 012 代码、Drop 端点和本地 SSH 身份材料
  -> 离线 --local-check PASS（不联网）
  -> 工程门禁、merge commit、用户另行执行授权
  -> 唯一一次固定只读 SSH、零重试
  -> 固定部署根和 011 暂存只读取证
  -> 严格九键解析
  -> ABSENT / PRESENT-PASS / PRESENT-MISMATCH
  -> 消费 012，所有入口防重放
```

状态分支：

- `ABSENT`：关闭 011 暂存 `UNKNOWN` 为 `ABSENT`；若继续安装，必须创建新的安装 ChangeId 和候选，不得复用 011。
- `PRESENT/PASS`：关闭 011 暂存 `UNKNOWN` 为“完整存在”；清理或安装仍各自需要新的 ChangeId、工程门禁和独立授权，不得直接执行 011 历史命令。
- `PRESENT/MISMATCH`：关闭为“存在但不可信”；立即停止。诊断和清理分别使用新的 ChangeId，不自动删除任何远端内容。
- `evidence_unavailable`：没有形成远端三态证据，011 暂存继续为 `UNKNOWN`；012 仍按唯一正式调用消费，禁止重试。

无论哪一分支，都不关闭 API、schema、数据库、Bifrost、监控、备份、账务或运行态审计门禁。

## 8. 安全与停止条件

- 最大本地检查 1 次、SSH 1 次、重试 0、业务请求 0、上游请求 0、费用上限 0 CNY。
- OpenSSH 固定 `-F none`、`BatchMode=yes`、`IdentitiesOnly=yes`、`ConnectionAttempts=1`、`StrictHostKeyChecking=yes`、`HostKeyAlgorithms=ssh-ed25519`，显式 known_hosts 和 IdentityFile；禁用密码、键盘交互、Agent、X11、TTY、本地命令和全部转发。
- 子进程使用最小环境，不继承调用方 PATH、`SSH_AUTH_SOCK`、AskPass、代理或 Python 注入变量。
- 任一代码摘要、文件类型、reparse/链接、known_hosts 唯一性、服务端指纹、客户端指纹、密钥对、路径、元数据、五文件、manifest、回执、SSH 参数、stderr、返回码或输出契约不符，立即停止且不重试。
- 不输出密码、私钥、Token、环境变量值、当前文件摘要或远端错误正文。

## 9. 测试设计

采用 TDD，至少覆盖：

1. 普通 Python 拒绝，隔离 Python self-test 通过。
2. 012 初始未消费；001 至 011 继续保持消费态且没有任何旧入口被恢复。
3. `--local-check` 不启动 SSH；未获授权的工程测试只能 mock 远端进程或在无网络容器内运行远端程序。
4. OpenSSH、`ssh-keygen`、known_hosts、私钥、公钥的类型、reparse/链接、路径、元数据、摘要、指纹、密钥对和语义漂移均失败关闭。
5. known_hosts 的明文/哈希重复、其他 ED25519、RSA/ECDSA 混入以及错误固定端点均拒绝；实际 SSH 只消费临时单行批准 ED25519。
6. 临时目录动态形成 `ABSENT`、`PRESENT/PASS` 和八类 `PRESENT/MISMATCH`。
7. `SHA256SUMS` 四项内容、文件自身回执、manifest 精确键值、五文件大小和权限分别有正负例。
8. 部署根、暂存目录、文件目录项、文件描述符、文件内容、权限和文件集合在核验期间发生替换或漂移时失败关闭。
9. 解析器拒绝缺键、额外键、重复键、非 ASCII、错误 ChangeId、错误路径、未知枚举和非法状态组合。
10. 固定 OpenSSH 参数，正式路径只有一个子进程调用，`ConnectionAttempts=1` 且没有重试循环。
11. stdout/stderr 并发排空、`64 KiB` 精确边界、超限、管道读取异常、SSH 非零和任意 stderr 均低敏失败关闭。
12. 正式结果的本地首行与退出码精确对应：PASS=0、MISMATCH=3、FAILED=2。
13. 消费后空参数、帮助、未知参数、畸形参数、self-test、local-check 和正式入口均在 argparse、材料或网络读取前固定拒绝，不回显调用方输入。
14. Windows 本地测试、Linux `python:3.13-alpine --network none` 动态测试、`py_compile`、self-test、Actionlint、敏感信息扫描和 `git diff --check` 全部通过。

测试不得连接测试服、不得读取真实私钥内容到输出、不得调用真实 sudo，也不得借 CI 访问生产或业务网络。

## 10. 文档、工程门禁与授权边界

实施阶段同步更新：

- `.github/workflows/ci.yml` 的 G8 适用门禁与分级分类器测试。
- `README.md`、`docs/ai-gateway-g8-acceptance.md`、`docs/test-plan.md`、`docs/tools.md`。
- `docs/ai-gateway-g8-test-readonly-access-runbook.md`。
- 新的 012 执行授权清单。

012 授权清单必须冻结 ChangeId、精确 PR HEAD、脚本摘要、Drop 端点、known_hosts 与客户端指纹、目标暂存、五文件摘要/大小/权限、manifest、回执、精确命令、最大次数、`0 / 0 / 0 CNY`、影响、回滚和停止条件。初始状态只能是 `PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`。

只有精确 PR HEAD 的适用 CI、独立代码安全评审、QA、产品/规格验收均达到 P0/P1=0，并以 merge commit 合并后，文档状态才可收敛为 `PENDING_USER_APPROVAL`。用户再次明确批准前，不得运行 012 `--local-check` 或连接测试服。

012 的任何结果均不授权清理、安装、特权审计、生产部署、真实付费调用、通知、客户灰度或商业观察。`G8_ENGINEERING_READY` 保持；`G8_COMMERCIAL_ACCEPTED` 继续未完成。
