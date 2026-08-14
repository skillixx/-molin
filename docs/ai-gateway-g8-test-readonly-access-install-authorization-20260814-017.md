# G8 Drop 最小只读入口安装 017 已消费授权记录

## 1. 当前状态

`CONSUMED_LOCAL_GATE_FAILED_SSH_REACHABILITY_UNKNOWN / REMOTE_NOT_AUTHORIZED`

用户已对固定 017 清单作出一次独立精确授权。唯一人工本地段返回固定低敏 `local_gate_failed` 并以退出码 2 停止；该输出不能区分 SSH 前瞬时门禁异常与 `ssh.exe` 非零返回。用户没有粘贴远端第二段、没有响应 sudo 密码，四项成功标志均未形成。017 因无法反证 SSH 是否到达而按失败关闭规则消费，历史生成命令与授权现均作废，禁止重放。完整记录见 `docs/ai-gateway-g8-test-readonly-access-install-attempt-20260814-017.md`。

工程门禁与合并后归档回执：

- PR：`#384`，最终工程 HEAD：`ee947fd61919215500ef516488d56e01ad2ea72d`。
- CI：run `31791430839` completed/success；全部适用门禁成功，Draft 门禁按设计跳过。
- 独立代码安全、QA、产品/规格复评均绑定同一最终 HEAD，P0/P1/P2/P3=`0/0/0/0`，允许合并。
- 主线 merge commit：`e2a7e4f89c4115b3e32dc27292b0bc11d7d09a57`；父提交依次为旧 main `8cdffdfe2bf62a5ff8454e227d6724e893b1c0cb` 与工程 HEAD `ee947fd61919215500ef516488d56e01ad2ea72d`。远端功能分支已删除。
- 合并后从 `origin/main` 原始 Git blob 复核 4 个冻结文件：大小、SHA-256、Git blob 与第 3 节冻结值全部一致，CRLF 计数均为 0；纯内存重建双段命令仍为 `25862 / 6acc63972cb779eea18df49dcaec271c7d50223000d96f2a1c1d57364d4cc98e`，未写出或执行命令资产。
- 工程集成与合并后归档复核未连接测试服。其后唯一获批本地段的 SSH 启动/连接保持 `UNKNOWN / 最多 1`；远端第二段、sudo、安装、post-check、业务请求、上游请求和费用均为 `0 / 0 / 0 / 0 / 0 / 0 / 0 CNY`。017 未确认安装且已消费；本记录不构成新的远端执行授权。

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
| `infra/scripts/test_g8_test_readonly_access_install_017.py` | 18254 | `90ed63db0d0caacd38ecc3f292ea393aeae9575cc9aa8be586da5eaa722dbc34` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_017_command.py` | 21493 | `7b8cd85bdb5917dea6fdb0b86e0f055bb98946e09028ddd87c0bd456c906eb7d` |
| 生成器输出的冻结双段命令 | 25862 | `6acc63972cb779eea18df49dcaec271c7d50223000d96f2a1c1d57364d4cc98e` |

上述大小与摘要已经由最终 HEAD、CI、独立复评和合并后原始 Git blob 再次核对。合并后 Git blob 分别为安装器 `429b73bb7b5487d6539e1c604ef9410b34c3b0c1`、生成器 `74a5c63d18c001154a36c1c22003bab433855c36`、安装器测试 `4122ce7915acd71e917326f9237be16c8d07fd69`、生成器测试 `ed9711c9ac7cdf5d8bb3e87a8a428ce8fe31d14f`；任一后续漂移都会使本候选失效，不得执行旧生成物。

017 相对 016 的基础修复是把冻结材料摘要从模块自动加载的 `Get-FileHash` 改为 Windows PowerShell 5.1 可用的纯 .NET 流式 SHA-256；嵌套 `try/finally` 保证哈希对象创建失败或释放失败时，文件流仍由外层 `finally` 关闭。生成命令在禁用模块自动加载并卸载 `Microsoft.PowerShell.Utility` 后仍能对固定字节得到标准摘要，并以故障注入覆盖哈希对象创建和释放失败。

工程复评进一步收紧失败关闭边界：客户端公钥派生显式传入空口令，使加密私钥在 sudo 前快速无提示拒绝；本地材料异常统一输出固定低敏原因，OpenSSH 客户端采用 `LogLevel=QUIET`，不回显身份绝对路径、临时 known_hosts 路径、对端指纹或原始异常；live 目标独占创建期间先暂缓 HUP、TERM 与 INT 终止，在所有权标记稳定后再触发 EXIT trap，覆盖断连和 Ctrl-C 落在创建与登记之间的最窄信号窗口；回滚入口立即移除 EXIT trap 并忽略后续重复信号，防止清理被二次中断。SSH 数量、sudo 唯一人工提示、no-clobber、远端输出和允许影响范围保持不变。

以上生成器行为是已消费前的历史冻结事实。当前生成器与安装器均已墓碑化，只能固定返回 `change_id_consumed`；历史生成命令文件不再有效，也不构成新的远端授权。

## 4. 已消费授权曾允许的远端影响

017 的一次性独立批准当时最多允许：

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

## 6. 实际结果与后续边界

017 没有形成成功。唯一人工本地段返回 `local_gate_failed`、退出码 2；SSH 是否启动保持 `UNKNOWN / 最多 1`，远端第二段、sudo、安装器、post-check、业务请求、上游请求和费用均为 0。测试服最小只读入口继续保持“未确认安装”，API、schema、MySQL、Redis、RabbitMQ、Bifrost、Prometheus、Grafana、Alertmanager、备份和账务运行态均未因此关闭。

017 已消费并墓碑化。继续诊断或安装必须使用新的 ChangeId，先补齐可区分 SSH 前门禁与 SSH 调用失败的低敏阶段证据并重新完成工程门禁；运行态审计、测试候选部署、临时 Fake 业务旅程、对账和回滚仍必须分别使用新的 ChangeId 和独立用户授权。生产、真实付费上游、真实客户流量、真实通知及商业观察不属于 017。
