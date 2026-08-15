#!/usr/bin/env python3
"""生成 020 单次交互安装会话的冻结命令；本工具本身不连接测试服。"""

import sys

# 在加载脚本目录可影响的模块前强制隔离解释器，避免本地模块劫持。
if not sys.flags.isolated:
    print("G8_TEST_READONLY_ACCESS_020_COMMAND=FAILED reason=isolated_python_required")
    raise SystemExit(2)

import argparse
import base64
import hashlib
import stat
from pathlib import Path, PureWindowsPath


CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260815-020"
SOURCE_CHANGE_ID = "CHG-G8-TEST-READONLY-ACCESS-DROP-20260813-011"
CHANGE_ID_CONSUMED = False
# 该常量只记录冻结清单状态；生成低敏命令不等于获得远端执行授权。
REMOTE_EXECUTION_AUTHORIZED = False
ROOT_COPY = "/root/molin-g8-install-CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260815-020"
INSTALLER_NAME = "g8-test-readonly-access-install-020.sh"
EXPECTED_INSTALLER_SHA256 = "f76c1bd10560fc4d5ea5de569065db65d4f0114510184e64311b52bc7d71a62f"
EXPECTED_INSTALLER_SIZE = 10977
TRUSTED_PROFILE_RECEIPT = "__G8_TRUSTED_PROFILE_RECEIPT__"


class SafeArgumentParser(argparse.ArgumentParser):
    """把所有参数错误收敛为固定低敏结果，禁止回显调用方输入。"""

    def error(self, message: str) -> None:
        raise ValueError("invalid_request")


def sha256_bytes(content: bytes) -> str:
    """只对仓库冻结字节计算 SHA-256。"""
    return hashlib.sha256(content).hexdigest()


def is_safe_local_output_path(value: str) -> bool:
    """只做纯字符串与路径语义检查，在任何文件探测前拒绝 UNC 和设备路径。"""
    if not value or value.startswith(("\\\\", "//")):
        return False
    try:
        if not Path(value).is_absolute():
            return False
        windows_path = PureWindowsPath(value)
        if windows_path.drive:
            reserved = {"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$"}
            reserved.update(f"{prefix}{suffix}" for prefix in ("COM", "LPT") for suffix in "123456789¹²³")
            for component in windows_path.parts[1:]:
                # Windows 会折叠尾随点/空格，并把冒号解释为 ADS；两者都可能绕过全新普通文件语义。
                if ":" in component or component != component.rstrip(" ."):
                    return False
                if component.split(".", 1)[0].upper() in reserved:
                    return False
        return True
    except (OSError, ValueError):
        return False


def read_frozen_installer() -> bytes:
    """读取一次稳定的普通安装器文件，并严格核对冻结大小与摘要。"""
    installer_path = Path(__file__).with_name(INSTALLER_NAME)
    before = installer_path.lstat()
    if not stat.S_ISREG(before.st_mode) or installer_path.is_symlink():
        raise RuntimeError("invalid_installer")
    installer = installer_path.read_bytes()
    after = installer_path.stat()
    stable_identity = (
        before.st_dev,
        before.st_ino,
        before.st_size,
        before.st_mtime_ns,
        before.st_ctime_ns,
    ) == (
        after.st_dev,
        after.st_ino,
        after.st_size,
        after.st_mtime_ns,
        after.st_ctime_ns,
    )
    if (
        not stable_identity
        or len(installer) != EXPECTED_INSTALLER_SIZE
        or sha256_bytes(installer) != EXPECTED_INSTALLER_SHA256
        or not installer.startswith(b"#!/bin/bash\n")
    ):
        raise RuntimeError("invalid_installer")
    return installer


def build_known_hosts_guard() -> str:
    """使用固定 ssh-keygen 枚举明文与哈希端点，只接受唯一批准的 ED25519 key。"""
    return r'''$foundHostMatches = @(& $sshKeygen -F '[8.130.9.163]:10003' -f $knownHosts 2>$null | Where-Object {
  $_ -and -not $_.StartsWith('#')
})
if ($LASTEXITCODE -ne 0) { throw 'known_hosts_mismatch' }
$foundHostEntries = @($foundHostMatches | Where-Object { ($_ -split '\s+')[1] -eq 'ssh-ed25519' })
if ($foundHostEntries.Count -ne 1) { throw 'known_hosts_mismatch' }
$targetParts = ($foundHostEntries[0] -split '\s+')
if ($targetParts.Count -ne 3) { throw 'known_hosts_mismatch' }
$sha = [Security.Cryptography.SHA256]::Create()
try {
  $targetFingerprint = 'SHA256:' + [Convert]::ToBase64String($sha.ComputeHash([Convert]::FromBase64String($targetParts[2]))).TrimEnd('=')
} finally { $sha.Dispose() }
if ($targetFingerprint -ne 'SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I') {
  throw 'known_hosts_mismatch'
}'''


def build_command(
    installer: bytes,
    *,
    receipt_path: str | None = None,
    test_scenario: str | None = None,
    test_ssh_path: str | None = None,
) -> str:
    """生成不包含密码或凭据正文的 PowerShell 连接段和远端固定安装段。"""
    if test_scenario not in (None, "fail_before_ssh", "fake_ssh"):
        raise ValueError("invalid_test_scenario")
    if test_scenario is not None and receipt_path is None:
        raise ValueError("receipt_required")
    if test_scenario == "fake_ssh":
        if test_ssh_path is None:
            raise ValueError("test_ssh_required")
        # 此分支只由 Python 测试直接调用，正式 CLI 不暴露测试入口，也不会连接网络。
        receipt_literal = receipt_path.replace("'", "''")
        ssh_literal = test_ssh_path.replace("'", "''")
        return f"""$g8PreviousErrorActionPreferenceName = [string]$ErrorActionPreference
if (@('SilentlyContinue','Stop','Continue','Inquire','Ignore','Suspend') -cnotcontains $g8PreviousErrorActionPreferenceName) {{
  $g8PreviousErrorActionPreferenceName = 'Continue'
}}
$ErrorActionPreference = 'Stop'
$g8ReceiptStream = [IO.File]::Open('{receipt_literal}', [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
$g8ReceiptWriter = New-Object IO.StreamWriter($g8ReceiptStream, (New-Object Text.UTF8Encoding($false)))
$g8ReceiptWritable = $true
function Write-G8Receipt([string]$line) {{
  try {{
    $g8ReceiptWriter.WriteLine($line)
    $g8ReceiptWriter.Flush()
    $g8ReceiptStream.Flush($true)
    return $true
  }} catch {{ return $false }}
}}
$g8LocalGatePassed = $false
$g8FailureReason = 'ssh_session_failed'
if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_020_RECEIPT=STARTED')) {{
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2')
  $global:LASTEXITCODE = 2
  try {{ $g8ReceiptWriter.Dispose() }} catch {{}}
  try {{ $g8ReceiptStream.Dispose() }} catch {{}}
  $ErrorActionPreference = $g8PreviousErrorActionPreferenceName
  return
}}
try {{
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_PRE_SSH_GATE=PASS')
  if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_020_PRE_SSH_GATE=PASS')) {{ $g8ReceiptWritable = $false; $g8FailureReason = 'receipt_unavailable'; throw 'receipt_unavailable' }}
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES')
  if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES')) {{ $g8ReceiptWritable = $false; $g8FailureReason = 'receipt_unavailable'; throw 'receipt_unavailable' }}
  & '{ssh_literal}'
  if ($LASTEXITCODE -ne 0) {{ throw 'ssh_session_failed' }}
  $g8LocalGatePassed = $true
}} catch {{
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_LOCAL_GATE=FAILED reason=' + $g8FailureReason)
  if ($g8ReceiptWritable -and -not (Write-G8Receipt ('G8_TEST_READONLY_ACCESS_020_LOCAL_GATE=FAILED reason=' + $g8FailureReason))) {{ $g8ReceiptWritable = $false }}
}} finally {{
  $ErrorActionPreference = $g8PreviousErrorActionPreferenceName
}}
if ($g8LocalGatePassed) {{
  $g8HostExitCode = 0
  $g8HostResult = 'G8_TEST_READONLY_ACCESS_020_HOST_RESULT=PASS exit_code=0'
}} else {{
  $g8HostExitCode = 2
  $g8HostResult = 'G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=' + $g8FailureReason + ' exit_code=2'
}}
[Console]::Out.WriteLine($g8HostResult)
if ($g8ReceiptWritable -and -not (Write-G8Receipt $g8HostResult)) {{ $g8ReceiptWritable = $false }}
try {{ $g8ReceiptWriter.Dispose() }} catch {{}}
try {{ $g8ReceiptStream.Dispose() }} catch {{}}
$global:LASTEXITCODE = $g8HostExitCode
"""
    encoded = base64.b64encode(installer).decode("ascii")
    digest = sha256_bytes(installer)
    size = len(installer)
    known_hosts_guard = build_known_hosts_guard()
    receipt_literal = "" if receipt_path is None else receipt_path.replace("'", "''")
    receipt_setup = ""
    receipt_started = ""
    receipt_failure = ""
    receipt_result = ""
    receipt_close = ""
    receipt_pre_ssh = ""
    receipt_attempted = ""
    if receipt_path is not None:
        if receipt_path == TRUSTED_PROFILE_RECEIPT:
            receipt_path_setup = """  $g8ReceiptProfileRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
  if ([string]::IsNullOrWhiteSpace($g8ReceiptProfileRoot) -or $g8ReceiptProfileRoot -notmatch '^[A-Za-z]:\\' -or $g8ReceiptProfileRoot.StartsWith('\\')) {
    throw 'receipt_unavailable'
  }
  if ([IO.Path]::GetFullPath($g8ReceiptProfileRoot).TrimEnd('\\') -cne $g8ReceiptProfileRoot.TrimEnd('\\')) { throw 'receipt_unavailable' }
  $g8ReceiptProfileItem = Get-Item -LiteralPath $g8ReceiptProfileRoot -Force -ErrorAction Stop
  if (-not $g8ReceiptProfileItem.PSIsContainer -or ($g8ReceiptProfileItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'receipt_unavailable' }
  $g8ReceiptPath = Join-Path $g8ReceiptProfileRoot '.g8-020-execution-receipt.txt'
"""
        else:
            receipt_path_setup = f"  $g8ReceiptPath = '{receipt_literal}'\n"
        receipt_setup = f"""$g8ReceiptPath = $null
$g8ReceiptStream = $null
$g8ReceiptWriter = $null
$g8ReceiptWritable = $true
try {{
{receipt_path_setup}
  $g8ReceiptStream = [IO.File]::Open($g8ReceiptPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::Read)
  $g8ReceiptWriter = New-Object IO.StreamWriter($g8ReceiptStream, (New-Object Text.UTF8Encoding($false)))
}} catch {{
  if ($null -ne $g8ReceiptWriter) {{ try {{ $g8ReceiptWriter.Dispose() }} catch {{}} }}
  if ($null -ne $g8ReceiptStream) {{ try {{ $g8ReceiptStream.Dispose() }} catch {{}} }}
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2')
  $global:LASTEXITCODE = 2
  $ErrorActionPreference = $g8PreviousErrorActionPreferenceName
  return
}}
function Write-G8Receipt([string]$line) {{
  try {{
    $g8ReceiptWriter.WriteLine($line)
    $g8ReceiptWriter.Flush()
    $g8ReceiptStream.Flush($true)
    return $true
  }} catch {{ return $false }}
}}
"""
        receipt_started = """if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_020_RECEIPT=STARTED')) {
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=receipt_unavailable exit_code=2')
  $global:LASTEXITCODE = 2
  try { $g8ReceiptWriter.Dispose() } catch {}
  try { $g8ReceiptStream.Dispose() } catch {}
  $ErrorActionPreference = $g8PreviousErrorActionPreferenceName
  return
}
"""
        receipt_failure = "  if ($g8ReceiptWritable -and -not (Write-G8Receipt ('G8_TEST_READONLY_ACCESS_020_LOCAL_GATE=FAILED reason=' + $g8FailureReason))) { $g8ReceiptWritable = $false }\n"
        receipt_result = "if ($g8ReceiptWritable -and -not (Write-G8Receipt $g8HostResult)) { $g8ReceiptWritable = $false }\n"
        receipt_close = (
            "$global:LASTEXITCODE = $g8HostExitCode\n"
            "try { $g8ReceiptWriter.Dispose() } catch {}\n"
            "try { $g8ReceiptStream.Dispose() } catch {}\n"
        )
        receipt_pre_ssh = "    if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_020_PRE_SSH_GATE=PASS')) { $g8ReceiptWritable = $false; $g8FailureReason = 'receipt_unavailable'; throw 'receipt_unavailable' }\n"
        receipt_attempted = "    if (-not (Write-G8Receipt 'G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES')) { $g8ReceiptWritable = $false; $g8FailureReason = 'receipt_unavailable'; throw 'receipt_unavailable' }\n"
    injected_failure = "  $g8FailureReason = 'test_failure'\n  throw 'test_failure'\n" if test_scenario else ""
    local_command = f"""# 020 尚未获得远端执行授权。本文件只可在清单转为 REMOTE_AUTHORIZED 后执行。
# 第一步：在 PowerShell 中从 Windows API 获取可信系统目录，校验固定身份材料并建立唯一 SSH 会话。
$g8PreviousErrorActionPreferenceName = [string]$ErrorActionPreference
if (@('SilentlyContinue','Stop','Continue','Inquire','Ignore','Suspend') -cnotcontains $g8PreviousErrorActionPreferenceName) {{
  $g8PreviousErrorActionPreferenceName = 'Continue'
}}
$ErrorActionPreference = 'Stop'
$g8LocalGatePassed = $false
$g8FailureReason = 'trusted_windows_path_failed'
{receipt_setup}{receipt_started}try {{
{injected_failure}
$windowsRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::Windows)
$programData = [Environment]::GetFolderPath([Environment+SpecialFolder]::CommonApplicationData)
$profileRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
foreach ($systemPath in @($windowsRoot, $programData, $profileRoot)) {{
  if ([string]::IsNullOrWhiteSpace($systemPath) -or $systemPath -notmatch '^[A-Za-z]:\\\\' -or $systemPath.StartsWith('\\')) {{
    throw 'trusted_windows_path_unavailable'
  }}
  if ([IO.Path]::GetFullPath($systemPath).TrimEnd('\\') -cne $systemPath.TrimEnd('\\')) {{ throw 'trusted_windows_path_unavailable' }}
  $systemItem = Get-Item -LiteralPath $systemPath -Force -ErrorAction Stop
  if (-not $systemItem.PSIsContainer -or ($systemItem.Attributes -band [IO.FileAttributes]::ReparsePoint)) {{
    throw 'trusted_windows_path_unavailable'
  }}
}}
$ssh = Join-Path $windowsRoot 'System32\\OpenSSH\\ssh.exe'
$sshKeygen = Join-Path $windowsRoot 'System32\\OpenSSH\\ssh-keygen.exe'
$knownHosts = Join-Path $profileRoot '.ssh\\known_hosts'
$identity = Join-Path $profileRoot '.ssh\\id_ed25519'
$identityPublic = Join-Path $profileRoot '.ssh\\id_ed25519.pub'
function Get-FrozenMaterialEvidence([string]$path) {{
  $item = Get-Item -LiteralPath $path -Force -ErrorAction Stop
  if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {{ throw 'identity_material_invalid' }}
  # 使用 .NET 流式计算摘要，避免交互宿主的模块自动加载或发布者信任状态改变失败关闭语义。
  $stream = [IO.File]::Open($path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
  try {{
    $sha = [Security.Cryptography.SHA256]::Create()
    try {{
      $digest = ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '')
    }} finally {{
      $sha.Dispose()
    }}
  }} finally {{
    $stream.Dispose()
  }}
  return "$($item.Length):$($item.CreationTimeUtc.Ticks):$($item.LastWriteTimeUtc.Ticks):$digest"
}}
$previousSystemRoot = $env:SystemRoot
$previousProgramData = $env:ProgramData
try {{
  $env:SystemRoot = $windowsRoot
  $env:ProgramData = $programData
  $g8FailureReason = 'material_evidence_failed'
  $materialEvidence = @{{}}
  foreach ($path in @($ssh, $sshKeygen, $knownHosts, $identity, $identityPublic)) {{
    $materialEvidence[$path] = Get-FrozenMaterialEvidence $path
  }}
  $g8FailureReason = 'known_hosts_failed'
  {known_hosts_guard}
  $g8FailureReason = 'identity_pair_failed'
  # 显式传入空口令并抑制标准错误；加密私钥必须快速失败，禁止在 sudo 前出现额外凭据提示。
  $derivedPublic = (& $sshKeygen -y -P '' -f $identity 2>$null)
  if ($LASTEXITCODE -ne 0) {{ throw 'identity_pair_mismatch' }}
  $declaredParts = ((Get-Content -LiteralPath $identityPublic -Raw -ErrorAction Stop).Trim() -split '\\s+')
  $derivedParts = (($derivedPublic.Trim()) -split '\\s+')
  if ($declaredParts.Count -lt 2 -or $derivedParts.Count -lt 2 -or $declaredParts[0] -ne 'ssh-ed25519' -or $declaredParts[0] -ne $derivedParts[0] -or $declaredParts[1] -ne $derivedParts[1]) {{
    throw 'identity_pair_mismatch'
  }}
  $sha = [Security.Cryptography.SHA256]::Create()
  try {{
    $clientFingerprint = 'SHA256:' + [Convert]::ToBase64String($sha.ComputeHash([Convert]::FromBase64String($declaredParts[1]))).TrimEnd('=')
  }} finally {{ $sha.Dispose() }}
  if ($clientFingerprint -ne 'SHA256:oQNs45Icrw5B6RCqPHOFnsub4jfRzk3evFy+wmhF8K0') {{ throw 'identity_pair_mismatch' }}
  $g8FailureReason = 'material_drift_failed'
  foreach ($path in $materialEvidence.Keys) {{
    if ((Get-FrozenMaterialEvidence $path) -ne $materialEvidence[$path]) {{ throw 'identity_material_drift' }}
  }}
  $g8FailureReason = 'known_hosts_failed'
  $frozenKnownHosts = Join-Path $profileRoot ('.g8-known-hosts-' + [Guid]::NewGuid().ToString('N'))
  $knownHostsStream = $null
  $createdFrozenKnownHosts = $false
  try {{
    # 在可信用户目录以 CreateNew 独占创建，并持有只读共享句柄直到 SSH 返回，阻止替换或删除竞态。
    $knownHostsStream = [IO.File]::Open($frozenKnownHosts, [IO.FileMode]::CreateNew, [IO.FileAccess]::ReadWrite, [IO.FileShare]::Read)
    $createdFrozenKnownHosts = $true
    $knownHostsBytes = [Text.Encoding]::ASCII.GetBytes("[8.130.9.163]:10003 ssh-ed25519 $($targetParts[2])`n")
    $knownHostsStream.Write($knownHostsBytes, 0, $knownHostsBytes.Length)
    $knownHostsStream.Flush($true)
    $g8FailureReason = 'ssh_session_failed'
    [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_PRE_SSH_GATE=PASS')
{receipt_pre_ssh}    [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES')
{receipt_attempted}    & $ssh `
      -F none -tt -p 10003 `
      -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectionAttempts=1 -o ConnectTimeout=15 `
      -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=0 `
      -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 `
      -o ForwardAgent=no -o ClearAllForwardings=yes -o RequestTTY=force `
      -o PermitLocalCommand=no -o LogLevel=QUIET `
      -o UserKnownHostsFile="$frozenKnownHosts" `
      -i "$identity" `
      pc@8.130.9.163
    if ($LASTEXITCODE -ne 0) {{ throw 'ssh_session_failed' }}
  }} finally {{
    if ($createdFrozenKnownHosts) {{
      try {{ $knownHostsStream.Dispose() }} finally {{ Remove-Item -LiteralPath $frozenKnownHosts -Force -ErrorAction SilentlyContinue }}
    }}
  }}
  $g8LocalGatePassed = $true
}} finally {{
  if ($null -eq $previousSystemRoot) {{ Remove-Item Env:SystemRoot -ErrorAction SilentlyContinue }} else {{ $env:SystemRoot = $previousSystemRoot }}
  if ($null -eq $previousProgramData) {{ Remove-Item Env:ProgramData -ErrorAction SilentlyContinue }} else {{ $env:ProgramData = $previousProgramData }}
}}
}} catch {{
  # 只输出当前固定阶段，不回显路径、指纹、密钥正文、调用位置或原始异常。
  [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_LOCAL_GATE=FAILED reason=' + $g8FailureReason)
{receipt_failure}}} finally {{
  # 清理只能恢复有效枚举值；即使进入脚本时宿主值为 Null，也不得覆盖主要执行结果。
  $ErrorActionPreference = $g8PreviousErrorActionPreferenceName
}}
if ($g8LocalGatePassed) {{
  $g8HostExitCode = 0
  $g8HostResult = 'G8_TEST_READONLY_ACCESS_020_HOST_RESULT=PASS exit_code=0'
}} else {{
  $g8HostExitCode = 2
  $g8HostResult = 'G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=' + $g8FailureReason + ' exit_code=2'
}}
[Console]::Out.WriteLine($g8HostResult)
{receipt_result}{receipt_close}# 只更新调用方可读取的退出码，不终止父 PowerShell，也不关闭用户当前窗口。
$global:LASTEXITCODE = $g8HostExitCode
"""
    remote = f"""set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
unset BASH_ENV ENV CDPATH PYTHONPATH PYTHONHOME
unalias sudo 2>/dev/null || :
unset -f sudo 2>/dev/null || :
auth_change_id={CHANGE_ID}
source_change_id={SOURCE_CHANGE_ID}
staging=/home/pc/molin/.g8-staging-{SOURCE_CHANGE_ID}
root_copy={ROOT_COPY}
auditor_target=/usr/local/libexec/molin/g8-test-readonly-audit
reconcile_target=/usr/local/libexec/molin/ai-gateway-reconcile
sudoers_target=/etc/sudoers.d/molin-g8-test-readonly-audit
ah=308908d2a2b9fa8679fd21d77fde68b5ce5d521ed37dac6b7726e6c323452256
rh=37f6ee369f1ce489a3966123dfea3bd172d5386045495e069433c7f3d993f2c1
sh=1ec266c71f00d99da18b9e8cf59af91d6126811384adef62ce48750b97a0986f
rs=13066129
classify_live_state() {{
  # 新 ChangeId 必须先收敛 019 的未知状态：全缺失、精确已安装、部分或漂移三者互斥。
  present=0
  for target in "$auditor_target" "$reconcile_target" "$sudoers_target"; do
    if /usr/bin/test -e "$target" || /usr/bin/test -L "$target"; then present=$((present + 1)); fi
  done
  if [ "$present" -eq 0 ]; then /usr/bin/printf '%s\n' absent; return 0; fi
  if [ "$present" -ne 3 ]; then /usr/bin/printf '%s\n' drift; return 0; fi
  for target in "$auditor_target" "$reconcile_target" "$sudoers_target"; do
    /usr/bin/test -f "$target" && /usr/bin/test ! -L "$target" || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  done
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G:%a' -- "$auditor_target")" = root:root:755 || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G:%a' -- "$reconcile_target")" = root:root:755 || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G:%a' -- "$sudoers_target")" = root:root:440 || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/test "$(/usr/bin/sha256sum "$auditor_target" | /usr/bin/cut -d' ' -f1)" = "$ah" || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/test "$(/usr/bin/sha256sum "$reconcile_target" | /usr/bin/cut -d' ' -f1)" = "$rh" || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/test "$(/usr/bin/sha256sum "$sudoers_target" | /usr/bin/cut -d' ' -f1)" = "$sh" || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/test "$(/usr/bin/stat -c '%s' -- "$reconcile_target")" = "$rs" || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  scope=$(LC_ALL=C /usr/bin/sudo -n -l 2>/dev/null) || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/printf '%s\n' "$scope" | /usr/bin/grep -q SETENV && {{ /usr/bin/printf '%s\n' drift; return 0; }}
  nopasswd=$(/usr/bin/printf '%s\n' "$scope" | /usr/bin/grep 'NOPASSWD:' || :)
  /usr/bin/test "$(/usr/bin/printf '%s\n' "$nopasswd" | /usr/bin/grep -c .)" -eq 1 || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/printf '%s\n' "$nopasswd" | /usr/bin/grep -Eq '^[[:space:]]*\\(root\\) NOPASSWD: /usr/local/libexec/molin/g8-test-readonly-audit[[:space:]]*$' || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/id -nG pc | /usr/bin/grep -Eq '(^|[[:space:]])docker([[:space:]]|$)' && {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/sudo -n "$auditor_target" --self-test >/dev/null 2>&1 || {{ /usr/bin/printf '%s\n' drift; return 0; }}
  /usr/bin/printf '%s\n' exact
}}
/usr/bin/test "$(/usr/bin/id -un)" = pc
/usr/bin/test "$auth_change_id" = 'CHG-G8-TEST-READONLY-ACCESS-INSTALL-DROP-20260815-020'
/usr/bin/test "$(/usr/bin/realpath -e /home/pc/molin)" = /home/pc/molin
/usr/bin/test "$(/usr/bin/realpath -e "$staging")" = "$staging"
/usr/bin/test "$(/usr/bin/stat -c '%U:%G:%a' -- "$staging")" = pc:pc:700
actual=$(/usr/bin/find "$staging" -mindepth 1 -maxdepth 1 -printf '%f\\n' | /usr/bin/sort | /usr/bin/tr '\\n' ' ')
/usr/bin/test "$actual" = 'SHA256SUMS ai-gateway-reconcile g8-test-readonly-audit manifest.env molin-g8-test-readonly-audit.sudoers '
for entry in SHA256SUMS:600 ai-gateway-reconcile:700 g8-test-readonly-audit:700 manifest.env:600 molin-g8-test-readonly-audit.sudoers:600; do
  name=${{entry%%:*}}; mode=${{entry##*:}}; path="$staging/$name"
  /usr/bin/test -f "$path"; /usr/bin/test ! -L "$path"
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G:%a' -- "$path")" = "pc:pc:$mode"
done
/usr/bin/test "$(/usr/bin/sha256sum "$staging/SHA256SUMS" | /usr/bin/cut -d' ' -f1)" = 15617634b0d291f12cc5776eb80ec29e26369af1959ab4a596fcd5c836c3361f
(cd "$staging" && /usr/bin/sha256sum -c SHA256SUMS >/dev/null 2>&1)
/usr/bin/test "$(/usr/bin/sha256sum "$staging/g8-test-readonly-audit" | /usr/bin/cut -d' ' -f1)" = "$ah"
/usr/bin/test "$(/usr/bin/sha256sum "$staging/molin-g8-test-readonly-audit.sudoers" | /usr/bin/cut -d' ' -f1)" = "$sh"
/usr/bin/test "$(/usr/bin/sha256sum "$staging/ai-gateway-reconcile" | /usr/bin/cut -d' ' -f1)" = "$rh"
/usr/bin/test "$(/usr/bin/stat -c '%s' -- "$staging/ai-gateway-reconcile")" = "$rs"
/usr/bin/grep -Fqx "CHANGE_ID=$source_change_id"$'\\r' "$staging/manifest.env"
/usr/bin/grep -Fqx 'TARGET_TRANSPORT=DROP_SSH_INTERACTIVE_SUDO'$'\\r' "$staging/manifest.env"
/usr/bin/grep -Fqx 'PHYSICAL_HOST_IDENTITY=NOT_APPLICABLE'$'\\r' "$staging/manifest.env"
for parent in /home /home/pc /home/pc/molin; do
  /usr/bin/test -d "$parent"; /usr/bin/test ! -L "$parent"
  mode=$((8#$(/usr/bin/stat -c '%a' -- "$parent"))); /usr/bin/test $((mode & 0022)) -eq 0
done
/usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- /home)" = root:root
/usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- /home/pc)" = pc:pc
/usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- /home/pc/molin)" = pc:pc
for parent in /usr /usr/local /usr/local/libexec /etc /etc/sudoers.d; do
  /usr/bin/test -d "$parent"; /usr/bin/test ! -L "$parent"
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- "$parent")" = root:root
  mode=$((8#$(/usr/bin/stat -c '%a' -- "$parent"))); /usr/bin/test $((mode & 0022)) -eq 0
done
if /usr/bin/test -e /usr/local/libexec/molin || /usr/bin/test -L /usr/local/libexec/molin; then
  /usr/bin/test -d /usr/local/libexec/molin; /usr/bin/test ! -L /usr/local/libexec/molin
  /usr/bin/test "$(/usr/bin/stat -c '%U:%G' -- /usr/local/libexec/molin)" = root:root
  mode=$((8#$(/usr/bin/stat -c '%a' -- /usr/local/libexec/molin))); /usr/bin/test $((mode & 0022)) -eq 0
fi
if /usr/bin/test -e "$root_copy" || /usr/bin/test -L "$root_copy"; then
  /usr/bin/printf '%s\n' 'G8_TEST_READONLY_ACCESS_LIVE_STATE_020=DRIFT'
  exit 2
fi
live_state=$(classify_live_state)
if [ "$live_state" = exact ]; then
  /usr/bin/printf '%s\n' 'G8_TEST_READONLY_ACCESS_LIVE_STATE_020=EXACT'
  /usr/bin/printf '%s\n' 'G8_TEST_READONLY_ACCESS_POSTCHECK_020=PASS'
  exit 0
fi
if [ "$live_state" = drift ]; then
  /usr/bin/printf '%s\n' 'G8_TEST_READONLY_ACCESS_LIVE_STATE_020=DRIFT'
  exit 2
fi
/usr/bin/printf '%s\n' 'G8_TEST_READONLY_ACCESS_LIVE_STATE_020=ABSENT'
if /usr/bin/sudo -n -l >/dev/null 2>&1; then exit 1; fi
/usr/bin/printf '%s\\n' 'G8_TEST_READONLY_ACCESS_PREFLIGHT_020=PASS'
/usr/bin/sudo -k -v
/usr/bin/sudo -n /bin/bash -ceu 'PATH=/usr/sbin:/usr/bin:/sbin:/bin; export PATH; unset BASH_ENV ENV CDPATH PYTHONPATH PYTHONHOME; umask 077; root={ROOT_COPY}; target=$root/{INSTALLER_NAME}; /usr/bin/mkdir -m 0700 -- $root; /usr/bin/chown root:root $root; [ ! -L $root ]; [ "$(/usr/bin/stat -c %U:%G:%a -- $root)" = root:root:700 ]; set -o noclobber; exec 3> $target; /usr/bin/base64 -d >&3; exec 3>&-; /usr/bin/chown root:root $target; /usr/bin/chmod 0700 $target; [ "$(/usr/bin/stat -c %s -- $target)" = {size} ]; [ "$(/usr/bin/sha256sum $target | /usr/bin/cut -d" " -f1)" = {digest} ]; exec $target' <<'G8_020_INSTALL_B64'
{encoded}
G8_020_INSTALL_B64
/usr/bin/printf '%s\\n' 'G8_TEST_READONLY_ACCESS_POSTCHECK_020=PASS'
exit"""
    # 020 将完整远端脚本作为无秘密 Base64 参数随唯一 SSH 调用传入，stdin 继续保留给 TTY 上的 sudo 提示。
    remote_payload = base64.b64encode(remote.encode("utf-8")).decode("ascii")
    command = local_command
    interactive_ssh = f"""    [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES')
{receipt_attempted}    & $ssh `
      -F none -tt -p 10003 `
      -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectionAttempts=1 -o ConnectTimeout=15 `
      -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=0 `
      -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 `
      -o ForwardAgent=no -o ClearAllForwardings=yes -o RequestTTY=force `
      -o PermitLocalCommand=no -o LogLevel=QUIET `
      -o UserKnownHostsFile="$frozenKnownHosts" `
      -i "$identity" `
      pc@8.130.9.163"""
    single_session_ssh = f"""    $remotePayload = '{remote_payload}'
    $remoteCommand = "/usr/bin/printf '%s' '$remotePayload' | /usr/bin/base64 -d | /bin/bash"
    [Console]::Out.WriteLine('G8_TEST_READONLY_ACCESS_020_SSH_ATTEMPTED=YES')
{receipt_attempted}    & $ssh `
      -F none `
      -tt `
      -p 10003 `
      -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectionAttempts=1 -o ConnectTimeout=15 `
      -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=0 `
      -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 `
      -o ForwardAgent=no -o ClearAllForwardings=yes -o RequestTTY=force `
      -o PermitLocalCommand=no -o LogLevel=QUIET `
      -o UserKnownHostsFile="$frozenKnownHosts" `
      -i "$identity" `
      pc@8.130.9.163 `
      $remoteCommand"""
    if command.count(interactive_ssh) != 1:
        raise RuntimeError("invalid_command")
    return command.replace(interactive_ssh, single_session_ssh)


def self_test(installer: bytes) -> None:
    """离线验证生成物的关键授权边界，不读取身份材料也不启动子进程。"""
    command = build_command(installer, receipt_path=TRUSTED_PROFILE_RECEIPT)
    payload = command.split("$remotePayload = '", 1)[1].split("'\n", 1)[0]
    remote = base64.b64decode(payload, validate=True).decode("utf-8")
    local_required = (
        "BatchMode=yes",
        "ConnectionAttempts=1",
        "NumberOfPasswordPrompts=0",
        "  -tt `",
        "RequestTTY=force",
        "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=PASS exit_code=0",
        "G8_TEST_READONLY_ACCESS_020_HOST_RESULT=FAILED reason=",
        "/usr/bin/base64 -d | /bin/bash",
    )
    remote_required = (
        CHANGE_ID,
        SOURCE_CHANGE_ID,
        EXPECTED_INSTALLER_SHA256,
        "G8_TEST_READONLY_ACCESS_PREFLIGHT_020=PASS",
        "G8_TEST_READONLY_ACCESS_POSTCHECK_020=PASS",
        "/usr/bin/sudo -k -v",
        "/usr/bin/sudo -n /bin/bash -ceu",
    )
    if any(value not in command for value in local_required) or any(value not in remote for value in remote_required):
        raise RuntimeError("invalid_command")
    if b"G8_TEST_READONLY_ACCESS_INSTALL_020=PASS" not in installer \
            or b"validate_auditor_entry" not in installer \
            or b'/usr/bin/sudo -u pc -- /usr/bin/sudo -n "$AUDITOR_TARGET" --self-test' not in installer:
        raise RuntimeError("invalid_installer")
    for forbidden in ("sudo -S", "SUDO_ASKPASS", "SSH_ASKPASS", "PASSWORD=", "TOKEN=", "PRIVATE KEY"):
        if forbidden in command or forbidden in remote:
            raise RuntimeError("invalid_command")


def main() -> int:
    # 消费态未来必须在参数解析和安装器读取前拒绝；当前 020 仅处于工程候选态。
    if CHANGE_ID_CONSUMED:
        print("G8_TEST_READONLY_ACCESS_020_COMMAND=FAILED reason=change_id_consumed")
        return 2
    parser = SafeArgumentParser(add_help=False)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--change-id")
    parser.add_argument("--output-file")
    try:
        arguments = parser.parse_args()
        installer = read_frozen_installer()
        if arguments.self_test:
            if arguments.change_id is not None or arguments.output_file is not None:
                raise ValueError("invalid_request")
            self_test(installer)
            print("G8_TEST_READONLY_ACCESS_020_COMMAND_SELF_TEST=PASS")
            return 0
        output_value = arguments.output_file or ""
        if arguments.change_id != CHANGE_ID:
            raise ValueError("invalid_request")
        if not is_safe_local_output_path(output_value):
            raise ValueError("invalid_request")
        output = Path(output_value)
        if output.exists():
            raise ValueError("invalid_request")
        command = build_command(installer, receipt_path=TRUSTED_PROFILE_RECEIPT)
        with output.open("x", encoding="utf-8", newline="\n") as handle:
            handle.write(command)
    except Exception:
        print("G8_TEST_READONLY_ACCESS_020_COMMAND=FAILED reason=invalid_request")
        return 2
    print("G8_TEST_READONLY_ACCESS_020_COMMAND=PASS")
    print(f"root_installer_sha256={sha256_bytes(installer)}")
    print(f"root_installer_size={len(installer)}")
    print(f"command_sha256={sha256_bytes(command.encode('utf-8'))}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
