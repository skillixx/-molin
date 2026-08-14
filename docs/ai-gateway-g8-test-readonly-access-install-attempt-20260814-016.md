# G8 Drop 最小只读入口安装 016 执行记录

## 1. 固定结果

`CONSUMED_LOCAL_MODULE_ERROR_REMOTE_NOT_REACHED`

- ChangeId：`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016`。
- 用户授权：已明确批准固定 016 清单的一次执行。
- 冻结双段命令：大小 `22967`，SHA-256 `0173d043baa4d60a96659a77a8387f8d1de1a8fc9b77928f0abdf9d2793008fb`；生成后再次核对一致。
- 本地门禁：授权契约 4/4、生成器 13/13、安装器 Windows 适用项 3/3、生成器自检全部通过。
- SSH 会话：`0/1`。
- sudo 与安装：`0`。
- 业务请求、上游请求、费用：`0 / 0 / 0 CNY`。
- 重试：`0`；016 已消费并禁止重放。

## 2. 唯一人工第一段

可见系统 PowerShell 由冻结命令第一段启动。交互宿主先尝试加载 Codex runtime 中的 PSReadLine，并显示 Microsoft 发布者信任选择；用户选择“始终运行”后，PSReadLine 仍加载失败。随后冻结函数 `Get-FrozenMaterialEvidence` 第一次执行 `Get-FileHash` 时返回：

`POWERSHELL_GET_FILE_HASH_UNAVAILABLE`

PowerShell 原始错误类别为 `CommandNotFoundException`。错误发生在命令第一段第 25 行，而唯一 `ssh.exe` 调用位于第 71 行；第一段开头已把 `$ErrorActionPreference` 设为 `Stop`，因此该错误是终止错误，后续身份材料循环、临时 known_hosts、SSH、远端 here-doc、sudo 和安装器均不可到达。

执行后本机只读进程检查没有发现 `ssh.exe` 或 `ssh-keygen.exe`。随后仅运行不含 SSH 的本地阶段诊断，Windows API 路径、固定 known_hosts、公私钥配对和指纹校验均通过；通过 `Start-Process` 创建的新系统 PowerShell 可稳定复现 `Get-FileHash` 不可用，而直接非交互子进程可解析该 cmdlet，根因收敛为交互/新进程模块解析依赖，不是测试服或身份材料错误。

用户选择“始终运行”可能改变本机发布者信任状态；本任务没有独立核验该系统信任存储的最终变化，也未自动回滚，状态保持 `LOCAL_PUBLISHER_TRUST_EFFECT=UNKNOWN`。

## 3. 结论与后续

016 没有连接测试服，也没有产生任何远端文件、sudoers、服务、容器、数据库或业务影响。为消除审计歧义，016 生成器和安装器必须固定返回 `change_id_consumed`，不得以调整 `PSModulePath`、更换启动参数或重新打开 PowerShell 的方式重放。

017 仅为新的工程候选，不继承 016 执行授权。017 将冻结材料摘要改为 Windows PowerShell 5.1 可用的纯 .NET 流式 SHA-256，消除 `Get-FileHash` 和模块自动加载依赖；完成全部工程门禁、主线合并及合并后摘要复核前，不得生成或执行新的 SSH、sudo 或安装段。
