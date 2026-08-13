# AI 网关 G8 测试服只读入口 010 执行记录

## 1. 授权与边界

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-010`。
- 目标：`pc@8.130.9.163:10003`，部署根 `/home/pc/molin`，传输类型 `DROP_SSH_DIRECT`。
- 授权上限：本地检查、只读 SSH、原子 SFTP、root 安装、由 `pc` 发起的固定 sudo self-test 各一次，全部零重试。
- 请求与费用：业务请求 `0`、上游请求 `0`、费用 `0 CNY`。
- 禁止业务 HTTP、数据库/队列读写、服务重启、生产连接、付费调用、通知和客户灰度。

## 2. 实际执行结果

| 阶段 | 次数 | 结果 | 低敏证据 |
|---|---:|---|---|
| 本地候选检查 | 1/1 | PASS | 固定输出 `G8_TEST_READONLY_ACCESS_STAGE_DROP_DIRECT_LOCAL_CHECK=PASS` |
| 只读 SSH 预检 | 1/1 | PASS | 端点、登录用户、部署根、暂存与三个 live 目标不存在门禁通过 |
| 原子 SFTP 暂存 | 1/1 | PASS | 五文件已上传至固定 010 暂存目录；包装器固定输出 `staging_uploaded=true` |
| root 安装编排 | 1/1 | STOPPED | 本地启动器在参数构造阶段失败，SSH 客户端未取得 root 目标参数；退出码 255，stderr 仅记录长度 498 字节，不保留正文 |
| root-only 副本与 live 安装 | 0/1 | NOT_RUN | 未建立 root 连接，未发送 root 安装脚本，未创建 root-only 目录或三个 live 目标 |
| `visudo`、sudo 范围、Docker 组 | 0/1 | NOT_RUN | root 安装未开始，按停止条件不得继续 |
| `pc` 固定 sudo self-test | 0/1 | NOT_RUN | 未安装 root-owned 审计器与 sudoers，按停止条件不得执行 |

执行时冻结事实：

- Windows 五文件回执：`3ff8cf3ad7237f866f83305d00ab73f766381b7f3247abee915efee629e41fb0`。
- Linux 临时复现回执：`b3fac1a1530124da9dc604c32d11bd665de3daa5d6799aebb33c38a3d2f174f4`。
- 执行时 010 包装器 SHA-256：`185c0ccda420d3bbe97e95c3218a03642372e05525d2663258287ebd981360b8`。
- 冻结 helper SHA-256：`4be88638f2a4a271ebbf23751bd3f7238ea5f78f1f18fcb6889c9e071b953f30`。
- 消费后包装器 SHA-256：`4fb920e32574c640685ddd9bed919485473dc54873d157a409c1adf987b3ab6a`。

## 3. 停止、回滚与消费

- root 通道未建立，因此没有本次新建的 root-only、live 或 sudoers 目标需要回滚。
- SFTP 暂存目录已创建并保留用于独立取证；按照冻结清单，删除暂存必须使用新 ChangeId 和独立授权，本次不删除。
- 用户提出“不能用 root 就用 pc 用户”。该替代没有执行：冻结安装要求 live 工具和 sudoers 为 `root:root`，并必须通过 `visudo`、精确 sudo 范围及 `pc` 非 Docker 组门禁；直接由 `pc` 运行暂存审计器不能满足该契约，也不能冒充安装或 self-test 成功。
- 010 已消费，普通、`--local-check` 和 `--self-test` 入口均在 helper、候选、身份材料或网络读取前固定返回 `change_id_consumed`；生成器不再保留活动候选，只允许在系统临时目录复现历史回执。
- 如需继续，必须使用新 ChangeId：要么提供可用 root 管理通道完成原 root-owned 安装，要么重新设计、评审并授权明确的 `pc` 非特权只读方案。不得重放 010。

## 4. 结论与残余风险

- 本次执行结果：`P0=0`、`P1=1`、`P2=0`。P1 为本地 root 启动器参数构造失败导致安装未开始；它不代表 root 认证失败，也不得通过重试或改用 `pc` 绕过。
- 测试服只读入口仍未安装，固定 sudo self-test 未执行。
- 仅确认 010 暂存上传成功；API 运行态、schema、Bifrost、监控、备份与账务状态未在本轮读取，既有 UNKNOWN 不因本次执行关闭。
- `G8_ENGINEERING_READY` 保持；生产部署、真实付费调用、真实通知、客户灰度和四周商业观察未执行，`G8_COMMERCIAL_ACCEPTED` 继续未完成。
