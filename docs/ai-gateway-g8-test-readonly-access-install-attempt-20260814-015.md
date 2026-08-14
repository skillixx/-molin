# G8 Drop 最小只读入口安装 015 执行记录

> 结果：`CONSUMED_LOCAL_PATH_ERROR_DOWNSTREAM_UNKNOWN`。本记录只归档低敏本地错误事实；身份材料读取、SSH 与远端影响因证据不足保持 `UNKNOWN`，不包含私钥、公钥、密码、主机记录正文或远端输出。

## 1. 授权与冻结基线

用户明确批准 `CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-015` 最多执行 1 次 SSH、1 次人工 sudo 认证、重试 0，并要求取得安装与 self-test 结果后立即停止。执行使用已合入 main 的冻结 015 双段命令；该授权不扩展到清理、部署、运行态审计、生产、付费上游、客户流量或真实通知。

## 2. 观察到的本地结果

人工粘贴第一段后，PowerShell 在可信 Windows 路径循环的 `-notmatch` 表达式处报告正则末尾反斜杠非法。冻结生成器把预期匹配盘符根路径的正则输出为单个结尾反斜杠；PowerShell 将其作为不完整转义处理。

该表达式在源码顺序上位于身份材料读取与 SSH 之前，但 Windows PowerShell 5.1 的只读最小复现证明此类错误在默认策略下是非终止错误：错误写入 stderr 后仍会继续执行后续语句，宿主也可能保持退出码 0。因此现有记录不能据源码顺序证明以下动作未发生：

1. 固定身份文件和 known_hosts 的读取与摘要复核；
2. `ssh.exe` 启动；
3. 远端 here-document、预检和密码提示；
4. root-only 副本、live 文件、sudoers 与 self-test。

终端记录没有出现四项冻结成功标志，也没有形成可核验的远端段或 sudo 结果。随后单独打印的 `G8_015_LOCAL_EXIT=0` 可能保留此前任一原生程序退出码；它不是绑定冻结步骤边界的回执，不能证明成功，也不能证明 SSH 为 0。

## 3. 次数与影响

| 项目 | 结果 |
|---|---|
| 本地路径表达式 | `ERROR_OBSERVED / POWERSHELL_REGEX_TRAILING_ESCAPE` |
| 身份材料读取 | `UNKNOWN` |
| SSH | `UNKNOWN / APPROVED_MAX_1` |
| 远端安装段/人工 sudo | `NOT_EVIDENCED / UNKNOWN` |
| 重试 | `0` |
| SFTP/SCP/上传/下载 | 未获授权；现有记录不证明发生 |
| root-only/live/sudoers 创建 | `UNKNOWN` |
| 服务、Docker、数据库、队列、业务 HTTP | 未获授权且无发生证据 |
| 业务请求/上游请求/费用 | 未获授权且无发生证据 |

## 4. 消费与修复

按冻结清单“任一步完成或失败后立即消费、重试 0”，015 已消费并禁止重放。当前仓库把原生成器和安装器入口收敛为固定 `change_id_consumed` 墓碑。

016 把 `$ErrorActionPreference = 'Stop'` 及其 `finally` 恢复写入真实第一段，再通过标准输入执行生成命令的真实路径前缀并要求 stderr 严格为空。故障注入把正则恢复为非法单反斜杠后，身份/SSH 阶段哨兵不可到达。修复版迁入新的 016 ChangeId，仍须完整工程门禁、主线合并和用户再次明确授权后才能连接测试服。
