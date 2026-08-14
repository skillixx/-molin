# G8 Drop 最小只读入口安装 019 工程授权清单

## 1. 当前状态

`PENDING_USER_APPROVAL / REMOTE_NOT_AUTHORIZED`

019 使用独立 ChangeId `CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-019`。018 已按 `CONSUMED_HOST_WINDOW_CLOSED_NO_OUTPUT_SSH_REACHABILITY_UNKNOWN` 失败关闭、消费并墓碑化，禁止恢复、重试、重放或执行历史生成文件。019 不继承 018 的任何远端授权；生成器固定声明 `CHANGE_ID_CONSUMED=False`、`REMOTE_EXECUTION_AUTHORIZED=False`。

## 2. 固定输入与单会话设计

- 唯一来源仍为 014 已证明 `PRESENT / PASS / NONE` 的固定 011 五文件暂存资产。
- 测试服务器只确认 SSH 公钥免认证；sudo 仍按交互密码处理，不保存、不读取、不输出密码。
- 完整远端脚本作为不含秘密的 Base64 命令参数随唯一 `ssh -tt` 调用传入，不再要求人工粘贴第二段；stdin/TTY 完整保留给最多一次 `sudo -k -v` 提示。
- 只有全部本地门禁通过后输出 `PRE_SSH_GATE=PASS`，只有紧邻唯一 SSH 调用时输出 `SSH_ATTEMPTED=YES`。
- 父 PowerShell 无论成功或失败都不得调用 `exit`；必须输出固定 `HOST_RESULT=PASS|FAILED exit_code=0|2`，并只设置 `$global:LASTEXITCODE`，确保用户窗口保持打开。
- 任一失败立即停止、零重试。继续保留 BatchMode、空口令私钥配对、固定并持锁 known_hosts、`LogLevel=QUIET`、no-clobber、sudo 最小权限以及 HUP/TERM/INT 不可重入回滚。
- 禁止输出真实路径、指纹、密钥正文、原始异常或凭据；禁止网络测试。离线夹具可调用 `ssh-keygen`，但不得建立 SSH 连接。

## 3. 合并后冻结工程候选

| 文件/生成物 | 大小 | SHA-256 | Git blob / 换行 |
|---|---:|---|---|
| `infra/scripts/g8-test-readonly-access-install-019.sh` | 10977 | `c1178bbc5b566357b5862484fab62dc9f267d8e341792eb8aa6871602e212935` | `dd550edf20aa913fa793754e6500604a95960f3a` / CRLF=0 |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-019-command.py` | 21450 | `7f994bd1be28e4b9d56a7aad600765325e1385c9bb2eaa6e26a08c72af626556` | `4605209a84d825301906f86bdce720d746c91cfd` / CRLF=0 |
| `infra/scripts/test_g8_test_readonly_access_install_019.py` | 18254 | `40b3997d0bcef8e122258a025485ee8bc2d751affb1f93dd049798712e1c3203` | `64e51a2c0407c22b4694f4d4b57ce364af1d08fa` / CRLF=0 |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_019_command.py` | 36093 | `255ab1f5be646d94dd88f5d2a2b531db132bf195f4fdbfc1d7c931381412698d` | `c2a4c2b6e3b4994dc6605f6042c1a586e44e6120` / CRLF=0 |
| 纯内存生成的冻结单会话命令 | 33675 | `b731b656e79e506b470bd3e1074bc965983b789a2a4f547e3df3c86505622087` | 不写盘 |

上述值已从合并后的 main 原始 Git blob 重新计算。任何代码、测试或文档修订都必须重新计算并同步这些契约。当前值只证明工程候选已冻结，不证明远端安装或运行态通过。

## 4. 工程集成与合并后复核

- 工程 HEAD：`a62a3a4d271055aa563b147319a0eceab30f4821`。
- PR：`#390`；适用 CI run：`31829691838`，状态为 `completed/success`。
- 独立评审：代码安全 P0/P1/P2 为 0，保留 1 个不阻断的 P3 可维护性气味；QA 与产品/规格 P0/P1/P2/P3 均为 0。
- merge commit：`70485d893fd86db00be4dbb9e324f9d4322d55b0`；父提交顺序为 `04ffc663f85c03efb995b35d06ac2b3a96b1e053`、`a62a3a4d271055aa563b147319a0eceab30f4821`。
- 远端工程分支 `feature/backend-d-ai-gateway-g8-install-019-single-session` 已删除。
- 合并后从 main 原始 Git blob 纯内存重建冻结命令，大小 33675 字节、SHA-256 为 `b731b656e79e506b470bd3e1074bc965983b789a2a4f547e3df3c86505622087`；唯一 SSH 目标、`-tt`、`SSH_ATTEMPTED` 与远端 `sudo -k -v` 均各 1 次，`Get-FileHash` 和父 PowerShell `exit 2` 均为 0 次。
- PR、CI、评审、合并与摘要复核均不构成 SSH、sudo、安装或测试服操作授权，也不证明测试服已安装或 `G8_SOFTWARE_CLOSED_LOOP` 已完成。

## 5. 允许影响与停止条件

当前工程集成门禁已完成，但仍不允许执行生成命令、SSH、sudo、安装器或 post-check。只有用户针对精确 019 ChangeId 作出新的独立远端授权后，才可按授权上限执行；没有该授权必须停止。

任何将来授权也不得隐含业务 HTTP、真实上游、钱包、费用、通知、客户流量、数据库、队列、migration、远端 Docker、服务启停、部署或生产动作。失败必须零重试；011 暂存和 015 至 018 墓碑不得修改。`G8_SOFTWARE_CLOSED_LOOP` 在测试服安装、只读入口验证及后续端到端闭环完成前始终保持未完成。
