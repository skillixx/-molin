# G8 Drop 暂存只读取证 013 授权清单

## 1. 当前状态

`PENDING_ENGINEERING_GATES_AND_USER_APPROVAL`

本清单仅冻结未来可能执行的候选。当前未授权运行真实本地材料诊断、013 正式入口、SSH 或任何测试服连接。Draft PR、CI、评审或合并均不自动构成执行授权。

## 2. 固定目标

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-013`
- 目标历史 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- Drop 端点：`pc@8.130.9.163:10003`
- 部署根：`/home/pc/molin`
- 暂存路径：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- 物理主机身份：`NOT_APPLICABLE`
- 服务端与客户端 ED25519 指纹只在批准常量中冻结，执行报告不得输出实际值。

## 3. 冻结脚本

- 无 ChangeId 本地诊断器：`infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py`
  - SHA-256：`aa1bb957e9b950eed263424a0b1e104695f68cad076e33b74fe5b70e54b320ed`
  - 大小：`14615`
- 一次性 013 包装器：`infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py`
  - SHA-256：`02c48a4aca2387e0baa0c179f9a1ea99c8a981adcdd22551b0010fd7b6fb1dfe`
  - 大小：`22272`

最终合并后必须从 merge commit 原始 Git 对象重新计算并更新以上摘要和大小；HEAD 或脚本任一漂移即使本清单失效。

## 4. 固定五文件

| 文件 | SHA-256 | 大小 | 权限 |
|---|---|---:|---:|
| `SHA256SUMS` | `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f` | 362 | 0600 |
| `ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | 13066129 | 0700 |
| `g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | 18377 | 0700 |
| `manifest.env` | `763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6` | 863 | 0600 |
| `molin-g8-test-readonly-audit.sudoers` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | 416 | 0600 |

`SHA256SUMS` 自身摘要是 011 Windows 候选回执；内容必须精确列出另外四文件。`manifest.env` 必须与设计文档冻结的完整键值集合完全一致。

## 5. 未来执行顺序

1. 操作者先独立运行无 ChangeId 本地诊断器；失败可以在修复本地材料后重复运行，不消耗 013。
2. 只有本地诊断 PASS、合并后摘要复核、独立工程门禁和用户再次明确批准全部满足后，才可执行一次 013 正式入口。
3. 013 最多发起一次固定只读 SSH，`ConnectionAttempts=1`，重试为 0。
4. 无论结果为 `ABSENT`、`PRESENT/PASS`、`PRESENT/MISMATCH` 或 `evidence_unavailable`，记录低敏证据后立即停止并消费 013。

未来执行命令固定如下。以下命令当前全部禁止执行；只有合并后摘要复核、工程门禁和用户再次明确授权均满足时才可使用：

```powershell
$g8UserProfile = [Environment]::GetFolderPath('UserProfile')
$g8KnownHosts = [IO.Path]::GetFullPath((Join-Path $g8UserProfile '.ssh\known_hosts'))
$g8Identity = [IO.Path]::GetFullPath((Join-Path $g8UserProfile '.ssh\id_ed25519'))
$g8IdentityPublic = [IO.Path]::GetFullPath((Join-Path $g8UserProfile '.ssh\id_ed25519.pub'))

$g8DiagnosticOutput = @(& python -I infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py `
  --known-hosts $g8KnownHosts `
  --identity-file $g8Identity `
  --identity-public-key $g8IdentityPublic 2>&1)
$g8DiagnosticExit = $LASTEXITCODE
if ($g8DiagnosticExit -ne 0 -or $g8DiagnosticOutput.Count -ne 1 -or [string]$g8DiagnosticOutput[0] -cne 'G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=PASS') {
  throw 'G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC_GATE=FAILED'
}

python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-013.py `
  --change-id CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260813-013 `
  --known-hosts $g8KnownHosts `
  --identity-file $g8Identity `
  --identity-public-key $g8IdentityPublic
```

不得增加参数、改用其他身份文件、改变执行顺序、把两条命令放入重试循环，或在本地诊断非 PASS 时执行第二条命令。

## 6. 次数、费用与影响

- 本地诊断：可重复，不联网，不消耗 ChangeId。
- 013 正式 SSH：最多 1 次；SSH 重试：0。
- SFTP/SCP/上传/下载：0。
- 业务请求：0；上游请求：0；费用上限：0 CNY。
- 应用层远端写入：0；但 sshd/journald/audit 日志及文件系统 atime 可能由操作系统产生。

## 7. 停止条件

任一本地工具、身份材料、端点、登录用户、路径、父链、属主、权限、摘要、文件集合、manifest、回执、stderr、输出上限、超时、返回码、键集合或输出契约不符，立即停止且不重试。不得输出密码、私钥、Token、环境变量值、实际指纹、当前文件摘要、远端 stderr 或异常正文。

## 8. 回滚与后续边界

013 没有应用层远端写能力，因此没有应用层回滚目标。它不得自动清理、安装、部署或继续运行态审计。任何后续动作必须使用新的 ChangeId、独立设计、工程门禁和用户授权。

本清单不代表生产部署，不授权真实付费调用、通知、客户灰度或商业观察；`G8_ENGINEERING_READY` 保持，`G8_COMMERCIAL_ACCEPTED` 未完成。
