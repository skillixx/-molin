# G8 Drop 最小只读入口安装 018 执行记录

## 1. 固定结果

`CONSUMED_HOST_WINDOW_CLOSED_NO_OUTPUT_SSH_REACHABILITY_UNKNOWN`

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-018`。
- 用户授权：最多 1 个 SSH 会话、1 次只读预检，预检通过后最多 1 次 `sudo -k -v`、固定安装器与 post-check；失败立即停止、零重试。
- 唯一人工本地段：PowerShell 窗口直接关闭且没有可见输出。
- SSH 启动与连接：`UNKNOWN / 最多 1`；没有足够证据把窗口关闭绑定到 SSH 前或 SSH 返回后。
- 远端固定段、sudo、安装器与 post-check：`0 / 0 / 0 / 0`；没有出现任何对应 PASS 标志或 sudo 提示。
- 业务请求、上游请求、费用：`0 / 0 / 0 CNY`。
- 重试：`0`；018 按失败关闭规则消费并禁止重放。

墓碑化后四个普通文件的冻结摘要如下，CRLF 计数均为 0：

| 文件 | 大小 | SHA-256 | Git blob |
|---|---:|---|---|
| `infra/scripts/g8-test-readonly-access-install-018.sh` | 182 | `dd0e3f1a563772be2c6961d15fb8c1622a9c2f09e0a392e59e8ee1cf31038dd5` | `7261b9a6e6fbbaed12bec9fe9eeb43dd338a95f7` |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-018-command.py` | 416 | `6a3b304057b0e569d4dc07ad95607c5042bc6df00a55448eb42b77fe31c34fe5` | `945cc9614024dd8a138a59c57e5ca1998827abe6` |
| `infra/scripts/test_g8_test_readonly_access_install_018.py` | 1195 | `4ff08707407db6a11dd8844d5ba2207f46983d26e0bee0b4a5253ed170d14221` | `67b8c8cc94e273a14dded15eb4c7325077a0e276` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_018_command.py` | 1409 | `296066323cc9f5355fcfd71cd8b0da3e0ff9df6044a8ec92093f1b9cc1416cfc` | `2c27001ae9424744b16eaa4a773ec0ecc05ae016` |

## 2. 证据边界

用户只报告第一步执行后窗口直接关闭、没有输出。未观察到 `PRE_SSH_GATE`、`SSH_ATTEMPTED`、远端预检、安装或 post-check 标志，也未出现 sudo 密码提示。该缺失不能反证 `ssh.exe` 没有被启动，因此 SSH 到达边界保持 `UNKNOWN / 最多 1`；同时没有证据证明远端固定段实际开始，确定的远端安装影响保持 0。

018 的父 PowerShell 使用进程级退出码结束失败路径，窗口宿主可能随之关闭；远端脚本仍依赖人工第二次粘贴，无法在窗口关闭后继续。为避免循环重试，018 立即消费，不允许重新打开窗口、重复粘贴、执行未跟踪的历史生成文件或以任何方式重放。

## 3. 后续门禁

继续工程只能使用新的独立 ChangeId。019 必须把完整远端固定段作为无秘密 Base64 参数随唯一 `ssh -tt` 调用传入，保留终端输入给最多一次 sudo 密码提示；父 PowerShell 只输出固定低敏结果并设置可读取的退出码，不得主动关闭窗口。019 仍须先完成离线测试、CI、独立评审、main 合并和合并后冻结复核，再取得新的独立精确远端授权。

本记录不授权 019 的 SSH、sudo、安装、post-check、运行态审计、部署、服务、数据库、队列、业务请求、上游请求、费用、通知、客户流量或生产动作。测试服最小只读入口仍未确认安装，`G8_SOFTWARE_CLOSED_LOOP` 尚未完成。
