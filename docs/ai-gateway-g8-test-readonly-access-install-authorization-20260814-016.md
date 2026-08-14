# G8 Drop 最小只读入口安装 016 候选清单

## 1. 当前状态

`PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`

本清单仅冻结 016 工程候选，不是测试服执行授权。当前禁止运行生成命令中的 SSH、交互 sudo 或安装段；只有精确 HEAD CI、独立代码安全/QA/产品复评、PR 合并、合并后 Git blob 摘要复核全部通过，并由用户再次明确批准本 ChangeId 后，才能执行一次。

前置证据：014 已在 main merge commit `97ee6037cafa90577be619fc67e78866c4d75efe` 中完成结果归档。其唯一只读 SSH 证明固定 011 暂存为 `PRESENT / PASS / NONE`，五文件、manifest 与回执完整；014 已消费并墓碑化。015 后续获得独立授权，但唯一人工本地段出现 PowerShell 正则末尾转义错误；该错误默认非终止，身份材料读取、SSH、sudo、安装和远端影响均保持 `UNKNOWN`，015 已消费并墓碑化。两项历史证据都不授权 016，也不证明 live 入口或任何运行态可用。

## 2. 固定身份与输入

- 安装 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016`。
- 冻结来源 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- Drop 端点：`pc@8.130.9.163:10003`。
- 部署根：`/home/pc/molin`。
- 只读来源：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- root-only 副本：`/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-016`。
- 物理主机身份：`NOT_APPLICABLE`；只冻结 Drop ED25519 主机指纹与本地 ED25519 客户端公钥指纹，执行输出禁止回显指纹、路径证据或密钥正文。
- Windows 的 `SystemRoot`、公共数据目录和用户目录只能来自 Windows API；调用方伪造的 `SystemRoot`、`PROGRAMDATA`、`USERPROFILE`、PATH 或代理变量不得改变固定 OpenSSH 与身份文件选择。

固定 011 五文件：

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
| `infra/scripts/g8-test-readonly-access-install-016.sh` | 9465 | `dee24046f11de7ba12994b3c93a68c28b5505f73b9dc6085a025f4ea790be85c` |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-016-command.py` | 15805 | `a1d96f8cc3d7abc1fa2ea04ab198133e2f60281d4664af83c93e378ac80dedbd` |
| `infra/scripts/test_g8_test_readonly_access_install_016.py` | 14481 | `9427886d06e8adc4577e838839f1d1890d29880b6ab1d829aadcea9fb6d213cf` |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_016_command.py` | 13920 | `bb81a134882c0c6bad2b2531137877b05d41e33cc3cf0402cc2759867be6d226` |
| 生成器输出的冻结双段命令 | 22967 | `0173d043baa4d60a96659a77a8387f8d1de1a8fc9b77928f0abdf9d2793008fb` |

上述大小与摘要必须在最终提交后重新计算并更新，随后由 CI、独立复评和合并后原始 Git blob 再次核对。任一漂移使本候选失效，不得执行旧生成物。

生成器只在本地写入调用方指定的全新绝对路径，不读取 SSH 身份材料、不联网、不调用子进程。`--self-test` 只读取冻结安装器并在内存构造命令，不创建输出文件。生成命令文件不等于获得远端授权。

016 把盘符根路径正则冻结为生成文本中的两个反斜杠，并在真实第一段开头保存 `$ErrorActionPreference`、设为 `Stop`，最终恢复原值。原生 Windows 回归除 ScriptBlock 语法解析和命令参数执行外，还从标准输入运行真实路径前缀并要求 stderr 严格为空；故障注入非法正则后，身份/SSH 阶段哨兵不可到达，从而覆盖人工粘贴的失败关闭语义。

本地工程验证另以工作区外只读挂载的冻结 011 五文件，在 `--network none` 一次性 Linux 容器中完整执行安装器主流程：成功路径的三项 live owner/mode/摘要与审计器 self-test 均通过；注入额外 `/bin/bash` NOPASSWD 后，失败路径先撤销 sudoers，再撤销两个工具和本次空父目录，同时保留 011 暂存与 root-only 016 副本。该容器证据不连接测试服，也不替代精确 HEAD CI 或远端授权。

## 4. 唯一允许的远端影响

获得独立批准后，016 最多允许：

1. 建立 1 个固定 SSH 交互会话，连接重试 0；SSH 密码认证、键盘交互认证、代理、端口转发和本地命令均禁用。
2. 完成一次非特权只读预检；只有固定输出 `G8_TEST_READONLY_ACCESS_PREFLIGHT_016=PASS` 后，才允许人工响应一次 `sudo -k -v` 的终端密码提示。密码不得出现在聊天、命令、环境变量、文件、日志或生成物中。
3. 在 root 下创建唯一 root-only 副本目录及安装器，复制已冻结的五文件并再次校验。
4. 仅在目标均不存在时创建：
   - `/usr/local/libexec/molin/g8-test-readonly-audit`，`root:root:0755`；
   - `/usr/local/libexec/molin/ai-gateway-reconcile`，`root:root:0755`；
   - `/etc/sudoers.d/molin-g8-test-readonly-audit`，`root:root:0440`；
   - 若 `/usr/local/libexec/molin` 不存在，可创建 `root:root:0755` 目录；若已存在，必须是非符号链接、root 所有且组/其他不可写。
5. 执行 `visudo -cf`、`sudo -n -l -U pc` 范围校验、`pc` 非 Docker 组校验，以及一次固定审计器 `--self-test`。

sudoers 只能新增一条精确的 root `NOPASSWD` 命令：`/usr/local/libexec/molin/g8-test-readonly-audit`。禁止 `SETENV`、通配符、Shell、Docker 或任意参数扩权。

不允许 SFTP、SCP、下载、覆盖既有 live 文件、修改用户组、启动/停止/重启服务、Docker 操作、数据库/队列操作、migration、业务 HTTP、上游请求、钱包或费用动作。业务请求、上游请求和费用固定为 `0 / 0 / 0 CNY`。

## 5. 事务、回滚与停止条件

- 所有 root/live 文件使用 no-clobber 创建；既有目标、符号链接、owner/mode/摘要漂移、父目录可写、`visudo` 失败、sudo 范围超出或 Docker 组成员关系异常均立即失败。
- 安装未完成时，只撤销本次已创建的 sudoers、对账器、审计器和可选空父目录；必须先移除 sudoers 并重新校验全局 sudoers 语法。
- 011 暂存不删除、不修改。root-only 016 副本作为低敏执行证据保留；无论成功或失败，其后续清理均须新 ChangeId 和独立授权。
- 首次 SSH、sudo、安装器或 post-check 任一步完成或失败后，016 都立即消费，重试为 0；不得在同一 ChangeId 下重新连接或修复。
- 成功必须依次可见：`PREFLIGHT_016=PASS`、审计器 `G8_TEST_READONLY_AUDIT_SELF_TEST=PASS`、`INSTALL_016=PASS`、`POSTCHECK_016=PASS`。审计器以 `pc` 身份实际命中唯一 NOPASSWD 规则，且该检查位于安装器回滚事务内；缺失、额外敏感输出或非零退出均会先回滚本次 live 文件再停止。

## 6. 后续边界

016 成功只证明最小只读入口安装及 self-test 通过，不证明 API、schema、MySQL、Redis、RabbitMQ、Bifrost、Prometheus、Grafana、Alertmanager、备份或账务运行态通过。

安装结果必须先归档、墓碑化 016 并完成独立工程门禁。随后运行态审计必须使用新的 ChangeId 和独立用户授权；测试候选部署、临时 Fake 业务旅程、对账和回滚也分别使用新的 ChangeId。生产、真实付费上游、真实客户流量、真实通知及商业观察不属于 016。
