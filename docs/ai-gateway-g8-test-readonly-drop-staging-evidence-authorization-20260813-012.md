# G8 Drop 暂存只读取证 012 授权清单

> 状态：`CONSUMED_LOCAL_CHECK_EVIDENCE_UNAVAILABLE`。用户批准后唯一一次 `--local-check` 返回固定低敏 `evidence_unavailable`、退出码 2、stderr 为空，随后按停止条件零重试结束；未启动 SSH，未连接测试服。012 ChangeId、授权和以下历史命令均已消费并作废，禁止重放。完整记录见 `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-attempt-20260813-012.md`。

## 1. ChangeId、目标与上限

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012`
- 目标 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- Drop SSH：`pc@8.130.9.163:10003`
- 部署根：`/home/pc/molin`
- 固定暂存：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- 最大本地检查：1；最大 SSH：1；重试：0
- 业务请求：0；上游请求：0；费用上限：0 CNY

Drop 地址只代表传输端点。012 不读取或验证 hostname、`/etc/machine-id`、实例元数据或 CMDB，物理主机身份固定为 `NOT_APPLICABLE`。

## 2. 冻结信任与程序

- 012 包装器：`infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py`
- 包装器 SHA-256：`e417089d107f9fb92c4e7236b7b0c9bec63df66438b820812624b83b68563a9f`
- 包装器大小：`34630` bytes
- 服务端 ED25519 指纹：`SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I`
- 客户端 ED25519 指纹：`SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0`
- Windows 工具：`C:\Windows\System32\OpenSSH\ssh.exe`、`C:\Windows\System32\OpenSSH\ssh-keygen.exe`
- 身份材料：`C:\Users\skillixx\.ssh\known_hosts`、`C:\Users\skillixx\.ssh\id_ed25519`、`C:\Users\skillixx\.ssh\id_ed25519.pub`

包装器在语义校验前冻结上述普通非链接文件的路径、dev/inode、mode、大小、mtime/ctime 和 SHA-256；固定端点的明文/哈希 known_hosts 命中合计必须恰好一条批准的 ED25519 密钥，客户端公钥指纹与公私钥对必须精确匹配。SSH 前后均复核全部材料；私钥不得复制或修改权限。

精确 PR HEAD 不写入自身提交内，避免形成无法满足的 Git 自引用。最终功能 HEAD 必须在 Draft PR #369 的正文或固定评论中记录，并由 CI、三方评审及合并授权共同精确绑定；若 HEAD 漂移，原记录和签署立即失效。当前分支固定基线为 `972a23572c9e09d9adc7038494ac1996b5cec33d`。

## 3. 冻结五文件、manifest 与回执

| 文件 | SHA-256 | 大小 | 权限 |
|---|---|---:|---:|
| `SHA256SUMS` | `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f` | 362 | `0600` |
| `ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | 13066129 | `0700` |
| `g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | 18377 | `0700` |
| `manifest.env` | `763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6` | 863 | `0600` |
| `molin-g8-test-readonly-audit.sudoers` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | 416 | `0600` |

`manifest.env` 必须恰含 20 键并绑定：格式 `1`、目标 ChangeId、来源提交 `099c38ed62ccd62c3c5a3b6811f1369d7f0d3084`、源码树 `c2d1252a05d031d842549345128fa7a1ffe53dc8`、Go `go1.26.5 windows/amd64 -> linux/amd64 CGO_ENABLED=0`、构建参数 `-trimpath,-buildvcs=false`、三制品摘要、对账器大小 `13066129`、双构建计数 `2`、固定 SSH/指纹、`DROP_SSH_INTERACTIVE_SUDO`、`PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE` 和部署根。`SHA256SUMS` 必须恰含除自身外四项映射；缺键、额外键或重复键均失败关闭。

## 4. 待后续独立批准的精确命令

以下命令是本次已消费授权的冻结历史记录，现已全部作废。不得再次执行，也不得用本地副本、临时目录或其他参数恢复 012 授权：

```powershell
python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py --local-check --known-hosts "C:\Users\skillixx\.ssh\known_hosts" --identity-file "C:\Users\skillixx\.ssh\id_ed25519" --identity-public-file "C:\Users\skillixx\.ssh\id_ed25519.pub"

python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-012.py --change-id CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-012 --known-hosts "C:\Users\skillixx\.ssh\known_hosts" --identity-file "C:\Users\skillixx\.ssh\id_ed25519" --identity-public-file "C:\Users\skillixx\.ssh\id_ed25519.pub"
```

正式路径只启动一个固定 OpenSSH 子进程，`ConnectionAttempts=1`，禁用密码、键盘交互、Agent、X11、TTY、转发和本地命令；远端固定执行 `/usr/bin/env -i PATH=/usr/bin:/bin /usr/bin/python3 -I -`，程序只经 stdin 传入。

## 5. 结果、停止条件与回滚

- `ABSENT / NOT_APPLICABLE / NONE`：只证明固定 011 暂存不存在；立即停止。
- `PRESENT / PASS / NONE`：只证明固定五文件、manifest 和回执完整；立即停止，清理或安装另立 ChangeId。
- `PRESENT / MISMATCH / PATH|FILE_SET|FILE_METADATA|FILE_CONTENT|MANIFEST|RECEIPT|READ_ERROR`：退出码 3，立即停止。
- 任一本地材料、端点、登录用户、路径、属主、权限、摘要、键集、stderr、输出上限、流读取、超时或返回码不符：退出码 2，立即停止且零重试。

012 不创建、修改或删除远端应用资产，因此没有应用层回滚。SSH 和只读文件访问可能产生 sshd/journald/audit 日志或 atime；不得清理这些系统证据。禁止 SFTP/SCP、上传、下载、sudo、Docker、数据库、Redis、RabbitMQ、Bifrost、业务队列、业务数据、日志/监控/备份读取、HTTP、服务重启、生产连接、真实付费、通知和客户灰度。

## 6. 阶段边界

001 至 012 均已消费。012 唯一 local-check 未通过且未启动 SSH，没有形成远端三态证据，011 暂存继续为 `UNKNOWN`。任何继续取证、清理、安装或运行态审计都必须使用新 ChangeId、重新完成工程门禁并取得独立用户授权；API、schema、数据库、Bifrost、监控、备份和账务门禁未关闭。`G8_ENGINEERING_READY` 保持；生产部署、真实付费调用、客户灰度和四周商业观察未执行，`G8_COMMERCIAL_ACCEPTED` 未完成。
