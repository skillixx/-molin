# G8 Drop 最小只读入口安装 017 候选清单

## 1. 当前状态

`PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`

本清单仅冻结 017 工程候选，不是测试服执行授权。工程候选正在进行 PR、精确 HEAD CI 与独立复评；在主线合并和合并后原始 Git blob 摘要复核全部通过前，当前仍禁止运行生成命令中的 SSH、交互 sudo 或安装段。只有全部工程门禁关闭，并再次取得用户对 017 的独立精确授权后，才能执行一次。

016 已在用户批准后的唯一人工第一段中失败关闭：交互 PowerShell 无法解析 `Get-FileHash`，终止错误发生在唯一 SSH 调用之前，SSH、sudo、安装、业务请求、上游请求和费用均为 `0 / 0 / 0 / 0 / 0 / 0 CNY`。016 已消费并禁止重放；017 仅为新的工程候选，不继承 016 执行授权。

## 2. 固定身份与输入

- 安装 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-017`。
- 冻结来源 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- Drop 端点：`pc@8.130.9.163:10003`。
- 部署根：`/home/pc/molin`。
- 只读来源：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- root-only 副本：`/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-017`。
- 物理主机身份：`NOT_APPLICABLE`；只冻结 Drop ED25519 主机指纹与本地 ED25519 客户端公钥指纹，执行输出禁止回显指纹、路径证据或密钥正文。
- Windows 的系统目录、公共数据目录和用户目录只能来自 Windows API；调用方伪造的环境路径、PATH 或代理变量不得改变固定 OpenSSH 与身份文件选择。

固定 011 五文件保持不变：

| 文件 | SHA-256 | 大小 | 暂存权限 |
|---|---|---:|---:|
| `SHA256SUMS` | `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f` | 362 | 0600 |
| `ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | 13066129 | 0700 |
| `g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | 18377 | 0700 |
| `manifest.env` | `763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6` | 863 | 0600 |
| `molin-g8-test-readonly-audit.sudoers` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | 416 | 0600 |

在密码提示前必须重新核对目录、文件集合、类型、owner/mode、五项摘要、对账器大小、`SHA256SUMS -c`、manifest 的 CRLF 冻结字节格式、来源 ChangeId、Drop 传输和 `PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE`。任一差异立即停止且不得提示 sudo 密码。

## 3. 冻结工程候选

| 文件/生成物 | 大小 | SHA-256 |
|---|---:|---|
| `infra/scripts/g8-test-readonly-access-install-017.sh` | 10977 | `4deb5a26c27e83a2afe766dd815e4b611b5bc0c3c19eed9afb1bfe0e1d0b1188` |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-017-command.py` | 16696 | `b9b552a71118560e5a2d18789ac9a1bc3c312fd80666b50d318cc08994fac669` |
| `infra/scripts/test_g8_test_readonly_access_install_017.py` | 18234 | `24b12102942ffb6128b44360b375ac827dda8a41ba003fab61fd619025c2e00c` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_017_command.py` | 21493 | `7b8cd85bdb5917dea6fdb0b86e0f055bb98946e09028ddd87c0bd456c906eb7d` |
| 生成器输出的冻结双段命令 | 25862 | `6acc63972cb779eea18df49dcaec271c7d50223000d96f2a1c1d57364d4cc98e` |

017 相对 016 的基础修复是把冻结材料摘要从模块自动加载的 `Get-FileHash` 改为 Windows PowerShell 5.1 可用的纯 .NET 流式 SHA-256；嵌套 `try/finally` 保证哈希对象创建失败或释放失败时，文件流仍由外层 `finally` 关闭。生成命令在禁用模块自动加载并卸载 `Microsoft.PowerShell.Utility` 后仍能对固定字节得到标准摘要，并以故障注入覆盖哈希对象创建和释放失败。

工程复评进一步收紧失败关闭边界：客户端公钥派生显式传入空口令，使加密私钥在 sudo 前快速无提示拒绝；本地材料异常统一输出固定低敏原因，OpenSSH 客户端采用 `LogLevel=QUIET`，不回显身份绝对路径、临时 known_hosts 路径、对端指纹或原始异常；live 目标独占创建期间先暂缓 HUP、TERM 与 INT 终止，在所有权标记稳定后再触发 EXIT trap，覆盖断连和 Ctrl-C 落在创建与登记之间的最窄信号窗口；回滚入口立即移除 EXIT trap 并忽略后续重复信号，防止清理被二次中断。SSH 数量、sudo 唯一人工提示、no-clobber、远端输出和允许影响范围保持不变。

生成器只在本地写入调用方指定的全新绝对路径，不读取 SSH 身份材料、不联网、不调用子进程。`--self-test` 只读取冻结安装器并在内存构造命令，不创建输出文件。017 仍未消费；生成命令文件不等于获得远端授权。

## 4. 唯一允许的远端影响

获得独立批准后，017 最多允许：

1. 建立 1 个固定 SSH 交互会话，连接重试 0；SSH 密码认证、键盘交互认证、代理、端口转发和本地命令均禁用。
2. 完成一次非特权只读预检；只有固定输出 `G8_TEST_READONLY_ACCESS_PREFLIGHT_017=PASS` 后，才允许人工响应一次 `sudo -k -v` 的终端密码提示。密码不得出现在聊天、命令、环境变量、文件、日志或生成物中。
3. 在 root 下创建唯一 root-only 副本目录及安装器，复制已冻结的五文件并再次校验。
4. 仅在目标均不存在时创建两个 root-owned 工具和 `/etc/sudoers.d/molin-g8-test-readonly-audit`；若父目录不存在，只允许创建固定 root-owned 目录。
5. 执行 `visudo -cf`、`sudo -n -l -U pc` 范围校验、`pc` 非 Docker 组校验，以及一次固定审计器 `--self-test`。

sudoers 只能新增一条精确的 root `NOPASSWD` 命令：`/usr/local/libexec/molin/g8-test-readonly-audit`。禁止 `SETENV`、通配符、Shell、Docker 或任意参数扩权。

不允许 SFTP、SCP、下载、覆盖既有 live 文件、修改用户组、启动/停止/重启服务、Docker 操作、数据库/队列操作、migration、业务 HTTP、上游请求、钱包或费用动作。业务请求、上游请求和费用固定为 `0 / 0 / 0 CNY`。

## 5. 事务、回滚与停止条件

- 所有 root/live 文件使用 no-clobber 创建；既有目标、符号链接、owner/mode/摘要漂移、父目录可写、`visudo` 失败、sudo 范围超出或 Docker 组成员关系异常均立即失败。
- 安装未完成时，只撤销本次已创建的 sudoers、对账器、审计器和可选空父目录；live 目标独占创建与所有权登记构成暂缓终止的临界区，异步 HUP、TERM 或 INT 会在标记稳定后触发不可重入的 EXIT 回滚，清理期间重复信号不得中断后续撤销；必须先移除 sudoers 并重新校验全局 sudoers 语法。
- 011 暂存不删除、不修改。root-only 017 副本作为低敏执行证据保留；其后续清理均须新 ChangeId 和独立授权。
- 首次 SSH、sudo、安装器或 post-check 任一步完成或失败后，017 都立即消费，重试为 0；不得在同一 ChangeId 下重新连接或修复。
- 成功必须依次可见：`PREFLIGHT_017=PASS`、审计器 `G8_TEST_READONLY_AUDIT_SELF_TEST=PASS`、`INSTALL_017=PASS`、`POSTCHECK_017=PASS`。缺失、额外敏感输出或非零退出均会按冻结事务停止。

## 6. 后续边界

017 成功只证明最小只读入口安装及 self-test 通过，不证明 API、schema、MySQL、Redis、RabbitMQ、Bifrost、Prometheus、Grafana、Alertmanager、备份或账务运行态通过。

安装结果必须先归档、墓碑化 017 并完成独立工程门禁。随后运行态审计、测试候选部署、临时 Fake 业务旅程、对账和回滚必须分别使用新的 ChangeId 和独立用户授权。生产、真实付费上游、真实客户流量、真实通知及商业观察不属于 017。
