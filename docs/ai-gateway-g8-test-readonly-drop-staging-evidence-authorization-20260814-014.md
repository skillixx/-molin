# G8 Drop 暂存只读取证 014 已消费授权记录

## 1. 当前状态

`CONSUMED_PRESENT_PASS / REMOTE_NOT_AUTHORIZED`

用户已对本清单作出独立执行批准。014 在主干 merge commit `d05012f5c2b1983bd7e60562bbfd816e5f14f550` 上完成唯一一次本地诊断和唯一一次只读 SSH，固定结果为 `PRESENT / PASS / NONE`、退出码 0、重试 0，随后立即停止。ChangeId 与历史命令现均已消费，禁止再次执行；本记录不授权清理、安装、部署或运行态审计。

工程门禁回执：

- PR：`#377`，最终工程 HEAD：`477a2d3da9b672c1fbc8b792de5eef7a4ed29af1`。
- CI：run `31762838448` completed/success；G8 主门禁、原生 Windows 门禁、真实后端 E2E 与 `CI 必选门禁汇总` 均为 SUCCESS。
- 独立代码安全、QA、产品/规格复评：同一最终 HEAD 均为 P0/P1/P2=0，并允许合并。
- 主线 merge commit：`3c9c5cf489b28e45b789c114243e45936a0d81d2`；父提交依次为旧 main `c56a0e8b0f357d4f27520b60333abd43abdd6f1d` 与工程 HEAD `477a2d3da9b672c1fbc8b792de5eef7a4ed29af1`。
- 远端功能分支已删除。合并后从 `origin/main` 原始 Git blob 复核：诊断器 `15833 / 3382b66c289c08b54ad36abc78969983ce89a89b7216e84c23b31aec6e34cadf`，014 包装器 `22846 / a2b20b22fe97769d49a88e80338380c3392411466ec94ebdfea63e51567809d8`，均为 LF 且与冻结值一致。
- closeout PR `#378` 最终 HEAD `ca513432bdd2f5106b9ed19c5872be3207058de8` 的 CI run `31763766230` completed/success，独立代码安全、QA、产品/规格复评均为 P0/P1/P2=0；已按 merge commit `d05012f5c2b1983bd7e60562bbfd816e5f14f550` 合入 main 并删除远端分支。
- 唯一执行低敏回执：`G8_TEST_READONLY_DROP_STAGING_EVIDENCE_014=PASS`、`staging_state=PRESENT`、`staging_integrity=PASS`、`staging_mismatch_reason=NONE`。完整记录见 `docs/ai-gateway-g8-test-readonly-drop-staging-evidence-attempt-20260814-014.md`。

## 2. 固定范围

- ChangeId：`CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260814-014`
- 目标历史 ChangeId：`CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`
- Drop 端点：`pc@8.130.9.163:10003`。
- 部署根：`/home/pc/molin`；暂存路径：`/home/pc/molin/.g8-staging-CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011`。
- 物理主机身份：`NOT_APPLICABLE`；ED25519 主机与本地身份指纹仅作为包装器批准常量冻结，执行输出不得回显。
- SSH 最多 1 次、重试 0；SFTP/SCP/上传/下载 0；业务请求、上游请求和费用为 `0 / 0 / 0 CNY`。
- 不允许清理、安装、sudo、Docker、数据库、队列、服务控制、业务 HTTP、生产或客户动作。

固定五文件必须同时存在且精确匹配：

| 文件 | SHA-256 | 大小 | 权限 |
|---|---|---:|---:|
| `SHA256SUMS` | `15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f` | 362 | 0600 |
| `ai-gateway-reconcile` | `37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1` | 13066129 | 0700 |
| `g8-test-readonly-audit` | `308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256` | 18377 | 0700 |
| `manifest.env` | `763c71547443a125b434071895b9a532fd966896e4ba9486b1c6b80f1541f3c6` | 863 | 0600 |
| `molin-g8-test-readonly-audit.sudoers` | `1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f` | 416 | 0600 |

`manifest.env` 必须与包装器冻结的完整键值集合完全一致，包含格式版本、目标 ChangeId、源码 commit/tree、Go 工具链与构建参数、四项制品摘要、固定传输端点、批准指纹、部署根、对账器大小和两次可复现构建计数；缺键、多键、重复键、值漂移或非 ASCII 均判为 `MANIFEST` 不匹配。

## 3. 工程候选

- 本地诊断器：`infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py`
  - 大小：`15833`
  - SHA-256：`3382b66c289c08b54ad36abc78969983ce89a89b7216e84c23b31aec6e34cadf`
- 014 包装器：`infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-014.py`
  - 工程候选大小：`22846`
  - 工程候选 SHA-256：`a2b20b22fe97769d49a88e80338380c3392411466ec94ebdfea63e51567809d8`

合并后必须从原始 Git 对象再次复核两项大小和摘要；任一漂移使候选失效。

## 4. 历史执行顺序（禁止重放）

以下命令已按批准唯一执行并消费，现仅用于保存历史顺序契约，任何情况下都禁止重放：

```powershell
$g8UserProfile = [Environment]::GetFolderPath('UserProfile')
$g8KnownHosts = [IO.Path]::GetFullPath((Join-Path $g8UserProfile '.ssh\known_hosts'))
$g8Identity = [IO.Path]::GetFullPath((Join-Path $g8UserProfile '.ssh\id_ed25519'))
$g8IdentityPublic = [IO.Path]::GetFullPath((Join-Path $g8UserProfile '.ssh\id_ed25519.pub'))
$g8DiagnosticOutput = @(& python -I infra/scripts/diagnose-ai-gateway-g8-local-ssh-materials.py --known-hosts $g8KnownHosts --identity-file $g8Identity --identity-public-key $g8IdentityPublic 2>&1)
$g8DiagnosticExit = $LASTEXITCODE
if ($g8DiagnosticExit -ne 0 -or $g8DiagnosticOutput.Count -ne 1 -or [string]$g8DiagnosticOutput[0] -cne 'G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC=PASS') {
  throw 'G8_LOCAL_SSH_MATERIALS_DIAGNOSTIC_GATE=FAILED'
}
python -I infra/scripts/run-ai-gateway-g8-test-drop-staging-evidence-014.py --change-id CHG-G8-TEST-READONLY-STAGING-EVIDENCE-DROP-20260814-014 --known-hosts $g8KnownHosts --identity-file $g8Identity --identity-public-key $g8IdentityPublic
```

执行时没有增加参数、改变身份文件、绕过本地 PASS、重试或把 014 权限扩展到后续动作。三态结果形成后已立即停止。

## 5. 固定三态与停止条件

| 远端状态 | 完整性 | 原因 | 本地退出码 |
|---|---|---|---:|
| `ABSENT` | `NOT_APPLICABLE` | `NONE` | 0 |
| `PRESENT` | `PASS` | `NONE` | 0 |
| `PRESENT` | `MISMATCH` | `PATH/FILE_SET/FILE_METADATA/FILE_CONTENT/MANIFEST/RECEIPT/READ_ERROR` 之一 | 3 |

实际结果为 `PRESENT / PASS / NONE`。三态结果形成后 ChangeId 已消费；包装器现于参数解析、材料读取和联网前固定返回 `change_id_consumed`。后续清理、安装、部署和运行态审计必须使用新的 ChangeId 与独立授权。
