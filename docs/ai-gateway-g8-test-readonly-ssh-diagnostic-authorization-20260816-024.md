# G8 024 最小 SSH 连接诊断工程授权清单

## 1. 当前状态

- ChangeId：`CHG-G8-TEST-READONLY-SSH-DIAGNOSTIC-20260816-024`。
- 当前状态：`PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`。
- 工程 HEAD `97876c03baeed226362aaa304fb1a30e959ac42a` 已经 PR #407、CI run `31897233312 completed/success` 和代码安全/QA/产品规格三项独立零缺陷评审，以 merge commit `ffca18aace03fd9185280fb7a2b2807d337a590d` 合入 main。
- merge 父提交依次为旧 main `337560b819c0105bde9d6991bf65e2f8c5f8fc3a` 与工程 HEAD；远端工程分支已删除，合并后原始 Git blob、大小、SHA-256 与 CRLF=0 已重新核对一致。
- 023 已固定为 `CONSUMED_SSH_SESSION_FAILED_REMOTE_AUDIT_NOT_PROVEN` 并永久墓碑化；024 不恢复、重试或重放 023。
- 本轮工程授权已经消费完毕；工程合并和摘要复核仍**不授权执行 024**，执行须用户针对上述精确 merge 与冻结 runner 作出新的独立授权。
- `G8_SOFTWARE_CLOSED_LOOP` 尚未完成。

## 2. 诊断目标与唯一远端能力

023 把连接、host key、认证和远端审计脚本的所有非零结果统一收敛成 `ssh_session_failed`，同时丢弃原始 stderr，因此现有记录无法确定失败发生在哪一层。024 只验证固定 SSH 会话能否建立并执行固定 `printf` 回执，不运行 Docker 审计。

- 固定目标：`pc@8.130.9.163:10003`。
- 最多 1 个非交互 SSH 会话；`ConnectionAttempts=1`；零重试。
- 唯一远端命令是固定 `printf` 回执 `G8_TEST_READONLY_SSH_DIAGNOSTIC_024_REMOTE=PASS`。
- 不包含 Docker、sudo、安装、宿主写入、HTTP、数据库、migration、业务请求、真实上游或费用能力。

## 3. 认证与主机身份边界

- 使用开发机现有 OpenSSH 免交互认证链，不固定客户端私钥、公钥或客户端指纹。
- 024 不固定 `-i` 或 `IdentitiesOnly`，继续允许 OpenSSH 使用默认身份文件和当前 `SSH_AUTH_SOCK`；同时以 `-F none` 隔离可执行任意本地命令或扩张信任面的用户 `ssh_config`。
- 固定关闭全局 known_hosts、KnownHostsCommand、DNS host key 验证、连接复用和持久连接；临时单条 known_hosts 是唯一主机密钥信任源。
- 命令行重新固定目标、端口和所有安全边界；代理跳转、代理命令、本地命令、端口转发、X11、TTY 与密码交互全部显式关闭。
- 保留 `BatchMode=yes`、`ConnectionAttempts=1`、`StrictHostKeyChecking=yes`、固定 ED25519 host key 与单条临时 `known_hosts`。
- 原始 stderr 只在本机内存中有界采集并映射，不输出真实路径、指纹、密钥正文、配置正文、原始异常或凭据。

## 4. 固定低敏分类

024 只允许输出以下结果之一：

- `pass`：SSH 返回 0、stdout 精确等于固定远端回执且 stderr 为空；
- `authentication_failed`：免交互认证被拒绝；
- `host_key_failed`：固定服务器 host key 校验失败；
- `connect_timeout`：连接超时；
- `connect_refused`：目标端口拒绝连接；
- `network_unreachable`：本地到目标的网络不可达；
- `transport_failed`：其他 SSH 传输层失败；
- `remote_probe_failed`：SSH 已返回远端命令的非 255 非零状态；
- `remote_marker_failed`：返回 0 但固定回执不完整；
- `output_limit_exceeded`、`ssh_client_unavailable`、`ssh_capture_unavailable`：本地失败关闭分类。

任何失败都立即停止，零重试；失败后的 ChangeId 是否消费只能依据未来独立执行授权和实际记录确定。

## 5. 本地与 CI 门禁

- Windows 使用 Python 隔离模式与假 SSH 动态验证固定参数、一次调用、分类、超时和低敏输出；假 SSH 不建立网络连接。
- Linux 使用固定缓存镜像、`--pull=never --network none`、仓库只读挂载运行相同纯离线测试。
- 普通入口和不完整授权在 Git、材料、SSH 子进程前返回 `remote_not_authorized`。
- 正式入口未来必须绑定双父工程 merge、第二父与 merge 中的 runner 原始 blob、runner 大小和 SHA-256；普通 feature HEAD 不得执行。
- 015 至 023 历史墓碑必须继续通过，023 两个入口保持无 import 固定 `change_id_consumed`。

## 6. 冻结文件

以下值由当前工程候选原始字节计算，CRLF 计数均为 0：

| 文件 | 大小 | SHA-256 | Git blob | CRLF |
|---|---:|---|---|---:|
| `infra/scripts/run-ai-gateway-g8-test-readonly-ssh-diagnostic-024.py` | 16980 | `9e350c349245187bea3c5325cc041b11504ca13d4f8ccfa5bb78bbea29ccca73` | `06f2182f9415a44c54b61bd5be62a2778773e17b` | 0 |
| `infra/scripts/test_run_ai_gateway_g8_test_readonly_ssh_diagnostic_024.py` | 10230 | `98dfea8847b10d47a94d284e4082978b20e44c213a265e3fe252f46ed638fe5d` | `538db27efb1314cced24ff39abe608544748bcf6` | 0 |

工程合并、CI 成功和摘要复核不代表 024 已执行，也不代表测试服 Docker 审计、运行态验收或 `G8_SOFTWARE_CLOSED_LOOP` 已完成。合并后必须停止，等待用户针对精确工程 merge 与 runner 摘要作出新的独立授权。
