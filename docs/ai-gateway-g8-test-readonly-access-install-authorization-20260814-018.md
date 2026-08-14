# G8 Drop 最小只读入口安装 018 工程授权清单

## 1. 当前状态

`PENDING_ENGINEERING_REVIEW / REMOTE_NOT_AUTHORIZED`

018 是独立工程候选，ChangeId 为 `CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260814-018`。本清单只冻结代码、测试、命令摘要和允许影响；不授权 SSH、sudo、安装器、post-check、运行态审计或任何测试服操作。生成器固定声明 `CHANGE_ID_CONSUMED=False`、`REMOTE_EXECUTION_AUTHORIZED=False`。

017 已按 `CONSUMED_LOCAL_GATE_FAILED_SSH_REACHABILITY_UNKNOWN` 永久消费并墓碑化，禁止恢复、重试、重放或复用历史命令。018 不继承 017 的任何授权。

## 2. 固定输入与诊断边界

- 冻结来源 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- 只允许使用 014 已证明 `PRESENT / PASS / NONE` 的固定 011 五文件暂存资产。
- Drop 端点仍固定复用 011/017 已批准的唯一 ED25519 端点，连接重试为 0；工程测试不得建立网络连接。
- Windows 系统目录、公共数据目录和用户目录只从 Windows API 获取；禁止信任伪造环境路径。
- 本地失败只允许输出以下六类固定低敏原因：`trusted_windows_path_failed`、`material_evidence_failed`、`known_hosts_failed`、`identity_pair_failed`、`material_drift_failed`、`ssh_session_failed`。
- 禁止输出真实路径、指纹、密钥正文、原始异常或凭据。只有全部 SSH 前门禁通过后才输出 `G8_TEST_READONLY_ACCESS_018_PRE_SSH_GATE=PASS`；只有紧邻唯一真实 SSH 调用时才输出 `G8_TEST_READONLY_ACCESS_018_SSH_ATTEMPTED=YES`。
- 任一失败均立即停止，零重试。现有 BatchMode、空口令私钥配对、固定 known_hosts、`LogLevel=QUIET`、no-clobber、sudo 最小权限与 HUP/TERM/INT 信号回滚控制不得降低。

## 3. 冻结工程候选

| 文件/生成物 | 大小 | SHA-256 | Git blob / 换行 |
|---|---:|---|---|
| `infra/scripts/g8-test-readonly-access-install-018.sh` | 10977 | `3232f3265da00d0a8f531798c32917bc77efd4725c30b4c9a99022d91484de85` | `635002eac75a1300cb69df57cfa1006288092cae` / CRLF=0 |
| `infra/scripts/prepare-ai-gateway-g8-test-readonly-access-018-command.py` | 19006 | `dc5037b22555c500e152985edf231da4e44931ec4470bb645dd254a9d6e44db9` | `4530158756d75712f5e01f36d209660df853e622` / CRLF=0 |
| `infra/scripts/test_g8_test_readonly_access_install_018.py` | 18254 | `a7575710f402aa26b4f6a37fc9bc499c6dd0f982b34ef7dd42f3e4207d1f95d6` | `e6688b0e1fff300cd9b94ecd41f267b78ee9f237` / CRLF=0 |
| `infra/scripts/test_prepare_ai_gateway_g8_test_readonly_access_018_command.py` | 33386 | `2519059d48b7dd0a9273476cb033d23d3aaa546b8991dccccef90f7c6baa5fb0` | `fc202a28cce7484086a96966da6e4f00b15582c8` / CRLF=0 |
| 纯内存生成的冻结双段命令 | 26932 | `7cf503dd0a32a43fa716680b0287838a5d0b8d7a2bb31b15c39195698da09500` | 不写盘 |

这些值只对应当前未合并工程候选。本地 Windows PowerShell 5.1 门禁与已缓存 `python:3.13-bookworm --network none`、只读挂载的 Linux 回归已通过，下一步仅允许 Git 集成、CI 与独立评审。018 五个脚本/测试由 `.gitattributes` 强制 LF，防止 Windows checkout 改变冻结字节。冻结 known_hosts 在经 Windows API 验证的用户目录中以 `CreateNew` 建立，并持有只允许其他读取的文件句柄直到 SSH 返回；调用方伪造 TEMP/TMP、预占、写入或删除都不能改变已校验内容，只有成功取得创建所有权才允许清理该文件，任何创建或写入失败均归为 `known_hosts_failed` 且不会形成 SSH 尝试标志。生成器在任何 `exists/open` 前以纯字符串门禁拒绝 UNC、设备命名空间、DOS 保留设备名、尾随点/空格别名与 ADS，禁止输出文件参数触发 SMB 或设备访问。任何代码、测试或文档修订都必须重新计算并同步契约；工程合并后还必须从 main 原始 Git blob 与纯内存命令重新复核，复核前不得申请远端安装授权。

## 4. 将来独立授权最多允许的影响

只有工程 PR 以精确 HEAD 通过全部适用 CI、代码安全、QA、产品/规格评审并合入 main，且合并后摘要复核完成后，用户才可对 018 作出新的独立精确授权。届时最多允许 1 个固定 SSH 会话、1 次非特权只读预检、预检成功后的 1 次人工 `sudo -k -v` 提示，以及安装固定最小只读审计入口；连接和安装失败均零重试。

即使将来获得独立安装授权，也不允许 SFTP、SCP、覆盖既有 live 文件、服务启停、远端 Docker、数据库、队列、migration、业务 HTTP、真实上游、钱包、费用、通知、客户流量或生产动作。业务请求、上游请求和费用固定为 `0 / 0 / 0 CNY`。

## 5. 回滚与停止条件

- 所有 root/live 文件继续使用 no-clobber；已有目标、符号链接、owner/mode/摘要漂移、sudo 范围扩张或 Docker 组异常均立即停止。
- 安装事务失败时只撤销本次已创建项；HUP、TERM、INT 以及清理期间重复信号必须保持不可重入回滚。
- 011 暂存不得删除或修改；017 墓碑不得修改。
- 本轮在 018 工程合并及合并后摘要归档完成后停止，等待新的独立远端安装授权。不得把工程合并、CI 或离线测试表述为测试服已安装、运行态通过或 `G8_SOFTWARE_CLOSED_LOOP` 完成。
