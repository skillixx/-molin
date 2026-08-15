# G8 Drop 最小只读入口安装 019 执行记录

## 1. 固定结果

`CONSUMED_POWERSHELL_PREFERENCE_RESTORE_FAILED_EXECUTION_REACHABILITY_UNKNOWN`

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-019`。
- 用户授权：允许生成并执行冻结命令；最多 1 个 SSH 会话、1 次 `sudo -k -v`、固定安装器与 post-check；任一失败立即停止、零重试。
- 执行基线：PR #391 最终 HEAD `2b0c6379d1950817e1cad9404a8f3d7e3396ca3a` 经 CI run `31831396476` 成功后，以 merge commit `752ca9d7705e9f6ba6d0652d6c0f34f580ce66ce` 合入 main；执行前 `origin/main` 精确匹配该提交。
- 本地工程门禁：PASS；冻结命令大小 33675 字节，SHA-256 为 `b731b656e79e506b470bd3e1074bc965983b789a2a4f547e3df3c86505622087`。
- 唯一执行：启动 1 个可见 PowerShell 5.1 窗口；用户最终看到冻结命令在 `finally` 恢复 `$ErrorActionPreference` 时因保存值为 `Null` 失败。
- 固定低敏标志：窗口缓冲区无法向上翻阅，未能恢复 `PRE_SSH_GATE`、`SSH_ATTEMPTED`、远端预检、安装或 post-check 标志。
- SSH 启动与连接：`UNKNOWN / 最多 1`；执行结束后的本地只读观察为 0 个活动 `ssh.exe`，但不能反证此前未启动或连接。
- 远端预检、sudo、安装器与 post-check：`UNKNOWN / 最多 1 / 最多 1 / 最多 1`；不得把缺失的终端标志表述为零触达或安装成功。
- 业务请求、上游请求、费用：`0 / 0 / 0 CNY`；冻结命令不包含这些能力。
- 重试：`0`；019 按失败关闭规则消费并禁止重放。

执行结束后，由本流程创建的本地 `.ps1` 冻结命令已在摘要复核后改名为不可直接执行的 `.consumed-do-not-run.txt`，内容未修改。该文件不得执行、改回 `.ps1` 或用于任何形式的重放。

墓碑化后四个普通文件的冻结摘要如下，CRLF 计数均为 0：

| 文件 | 大小 | SHA-256 | Git blob |
|---|---:|---|---|
| `infra/scripts/g8-test-readonly-access-install-019.sh` | 182 | `368091106a2b09bcb6353e9030309820ce2a19776d2444016bb3df066a158f78` | `db6b3babf107fff414ba513cb15a4cedb6c51b88` |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-019-command.py` | 425 | `2fbd91d95b585b694cdebb9013925b38627a03feb00ca455ab61a1894694eaf9` | `148d98a91cfc8f2328fba50aeb72fb0281b36903` |
| `infra/scripts/test_g8_test_readonly_access_install_019.py` | 1195 | `7a6704b66fe6105b751da66bb2f4ca27e5890bab88c8a7aec5c9d6b0f67563d8` | `50162acb6b028a69a6acb2dd4a669d6d49838979` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_019_command.py` | 1463 | `fe121f8925352cb7a75f43722a1ec0d480ec09a3dbcf3ad4a7aae8cd8e227594` | `e048406b9a07c1e35896fc44c76690d93c1e088b` |

## 2. 证据边界

已确认的唯一终端事实是 PowerShell 在外层 `finally` 恢复状态时抛出枚举转换错误。该位置在完整 `try/catch` 之后，因此既可能发生在本地门禁失败之后，也可能发生在 SSH 返回之后；缺少固定标志时，不能从代码位置反推实际到达阶段。执行结束后没有活动 `ssh.exe` 只说明观察时会话已经不存在，不能证明历史调用次数为 0。

本轮授权不包含额外服务器诊断或安装后核验，且明确要求任何失败零重试。因此不得为确认远端状态而新建第二个 SSH 会话，也不得执行 sudo、安装器、post-check、服务、Docker、数据库、队列或 HTTP 命令。测试服最小只读入口保持“安装状态未知”，不能报告已安装、未安装或运行态通过。

## 3. 失败关闭与后续门禁

019 的生成器和安装器已替换为参数解析、材料读取和联网前固定返回 `change_id_consumed` 的墓碑入口。019 不得再次授权、重试或重放；历史冻结命令、已改名的本地生成文件以及工程 merge commit 均不构成后续授权。

若继续，只能使用新的独立 ChangeId。后续候选至少必须：

1. 在设置严格错误策略前把原值规范化为有效 `ActionPreference`，恢复时对空值或异常值安全回退，且不得让状态恢复错误覆盖主结果。
2. 对恢复逻辑补 Windows PowerShell 5.1 的 `Null`、无效枚举和异常注入回归。
3. 让最终低敏结果在窗口无法滚动时仍可由用户安全取得，但不得记录密码、私钥、指纹、真实路径或原始异常。
4. 继续保持唯一 SSH、零重试、固定 known_hosts、BatchMode、空口令私钥校验、最小 sudo、no-clobber 和信号回滚边界。
5. 重新完成本地门禁、CI、独立评审、main 合并、原始 Git blob 复核和新的用户精确授权。

`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。
