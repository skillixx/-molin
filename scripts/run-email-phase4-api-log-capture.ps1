[CmdletBinding()]
param(
    [ValidateSet('Capture', 'Restore')][string]$Action = 'Capture',
    [string]$CaptureId,
    [string]$Confirm,
    [switch]$Execute,
    [switch]$SelfTest
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$payloadPath = Join-Path $PSScriptRoot 'email-phase4-api-log-capture.payload.py'
$successPattern = '\Astatus=pass mode=(capture capture_id=[a-f0-9]{32} api_count=1 log_mode=600 state_mode=600 service_running=true|restore capture_id=[a-f0-9]{32} api_count=1 log_mode=400 state_mode=600 service_running=true)\n\z'
$failurePattern = '\Astatus=failed mode=phase4_api_log classification=closed service_state=(running|running_or_unknown|stopped_or_unknown|unchanged_or_unknown) evidence_retained=true\n\z'

function Get-VerifiedPayloadBytes {
    # payload 必须是 scripts 直属普通文件，拒绝目录逃逸和 Windows 重解析点。

    $root = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $path = [IO.Path]::GetFullPath($payloadPath)
    $item = [IO.FileInfo]::new($path)
    if (-not $item.Exists -or $item.DirectoryName -cne $root -or
        ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'payload_path_invalid' }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes.Length -lt 32 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or
        [Array]::IndexOf($bytes, [byte]0) -ge 0) { throw 'payload_encoding_invalid' }
    $utf8 = New-Object Text.UTF8Encoding($false, $true)
    $text = $utf8.GetString($bytes)
    if (-not $text.StartsWith("#!/usr/bin/env python3`n", [StringComparison]::Ordinal) -or $text.Contains("`r")) {
        throw 'payload_encoding_invalid'
    }
    return $bytes
}

function Test-SafeSummary {
    param([string]$Stdout, [string]$Stderr, [int]$ExitCode)
    if ($null -eq $Stdout -or $null -eq $Stderr -or $Stdout.Length -gt 512 -or $Stderr.Length -ne 0) { return $false }
    if ($ExitCode -eq 0) { return [regex]::IsMatch($Stdout, $successPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant) }
    if ($ExitCode -eq 2) { return [regex]::IsMatch($Stdout, $failurePattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant) }
    return $false
}

function Invoke-ExactRedirectedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string]$Arguments,
        [Parameter(Mandatory = $true)][byte[]]$InputBytes,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    # 通过文件句柄直接重定向 stdin，完全绕过可能写入编码前导字节的 StreamWriter。

    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $tempPath = [IO.Path]::GetFullPath((Join-Path $tempRoot ('molin-phase4-stdin-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $tempPath.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or [IO.Directory]::Exists($tempPath)) {
        throw 'temp_path_invalid'
    }
    [void][IO.Directory]::CreateDirectory($tempPath)
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetOwner($sid)
    $acl.SetAccessRuleProtection($true, $false)
    $rule = New-Object Security.AccessControl.FileSystemAccessRule(
        $sid, [Security.AccessControl.FileSystemRights]::FullControl,
        [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit',
        [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Allow
    )
    [void]$acl.AddAccessRule($rule)
    [IO.Directory]::SetAccessControl($tempPath, $acl)
    $stdinPath = Join-Path $tempPath 'stdin.bin'
    $stdoutPath = Join-Path $tempPath 'stdout.bin'
    $stderrPath = Join-Path $tempPath 'stderr.bin'
    try {
        [IO.File]::WriteAllBytes($stdinPath, $InputBytes)
        $readBack = [IO.File]::ReadAllBytes($stdinPath)
        if ($readBack.Length -ne $InputBytes.Length) { throw 'stdin_file_mismatch' }
        for ($index = 0; $index -lt $InputBytes.Length; $index++) {
            if ($readBack[$index] -ne $InputBytes[$index]) { throw 'stdin_file_mismatch' }
        }
        [IO.File]::WriteAllBytes($stdoutPath, [byte[]]@())
        [IO.File]::WriteAllBytes($stderrPath, [byte[]]@())
        $process = Microsoft.PowerShell.Management\Start-Process -FilePath $FilePath -ArgumentList $Arguments `
            -RedirectStandardInput $stdinPath -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath `
            -NoNewWindow -PassThru
        $handle = $process.Handle
        if ($handle -eq [IntPtr]::Zero) { throw 'process_handle_unavailable' }
        $deadline = [DateTime]::UtcNow.AddMilliseconds($TimeoutMilliseconds)
        while (-not $process.HasExited -and [DateTime]::UtcNow -lt $deadline) {
            if ((Get-Item -LiteralPath $stdoutPath).Length -gt 4096 -or (Get-Item -LiteralPath $stderrPath).Length -gt 4096) {
                try { $process.Kill(); $process.WaitForExit() } catch { }
                throw 'process_output_limit'
            }
            Start-Sleep -Milliseconds 50
            $process.Refresh()
        }
        if (-not $process.HasExited) {
            try { $process.Kill(); $process.WaitForExit() } catch { }
            throw 'process_timeout'
        }
        $process.WaitForExit()
        $utf8 = New-Object Text.UTF8Encoding($false, $true)
        return [pscustomobject]@{
            ExitCode = [int]$process.ExitCode
            Stdout = $utf8.GetString([IO.File]::ReadAllBytes($stdoutPath))
            Stderr = $utf8.GetString([IO.File]::ReadAllBytes($stderrPath))
        }
    }
    finally {
        foreach ($target in @($stdinPath, $stdoutPath, $stderrPath)) {
            if ([IO.File]::Exists($target)) { [IO.File]::Delete($target) }
        }
        if ([IO.Directory]::Exists($tempPath) -and [IO.Directory]::GetFileSystemEntries($tempPath).Length -eq 0) {
            [IO.Directory]::Delete($tempPath, $false)
        }
    }
}

function Test-ExactStdinTransport {
    param([byte[]]$ExpectedBytes)
    $arguments = '-B -c "import hashlib,sys;d=sys.stdin.buffer.read();print(hashlib.sha256(d).hexdigest()+''|''+str(len(d))+''|''+sys.argv[1])" "argv probe"'
    $result = Invoke-ExactRedirectedProcess -FilePath 'python' -Arguments $arguments -InputBytes $ExpectedBytes -TimeoutMilliseconds 10000
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $expectedSha = -join ($sha.ComputeHash($ExpectedBytes) | ForEach-Object { $_.ToString('x2') }) }
    finally { $sha.Dispose() }
    if ($result.ExitCode -ne 0 -or $result.Stderr.Length -ne 0 -or
        $result.Stdout.TrimEnd([char[]]"`r`n") -cne ($expectedSha + '|' + $ExpectedBytes.Length + '|argv probe')) {
        throw 'stdin_probe_mismatch'
    }
}

$payload = Get-VerifiedPayloadBytes
if ($SelfTest) {
    if ($payload.Length -lt 1024) { throw 'payload_too_small' }
    $valid = "status=pass mode=restore capture_id=0123456789abcdef0123456789abcdef api_count=1 log_mode=400 state_mode=600 service_running=true`n"
    $failure = "status=failed mode=phase4_api_log classification=closed service_state=running evidence_retained=true`n"
    if (-not (Test-SafeSummary $valid '' 0) -or -not (Test-SafeSummary $failure '' 2) -or
        (Test-SafeSummary ($valid + "extra`n") '' 0) -or (Test-SafeSummary ($valid -replace 'api_count=1 ', '') '' 0) -or
        (Test-SafeSummary $valid 'raw' 0) -or (Test-SafeSummary ('x' * 513) '' 0)) { throw 'summary_contract' }
    Test-ExactStdinTransport -ExpectedBytes $payload
    Write-Output 'status=pass mode=phase4_api_log_launcher_selftest cases=8 external_access=false process_changes=false'
    exit 0
}
if (-not $Execute) {
    Write-Output 'status=disabled mode=phase4_api_log_launcher external_access=false process_changes=false'
    exit 0
}
if ($CaptureId -notmatch '\A[a-f0-9]{32}\z') { throw 'capture_id_invalid' }
$required = if ($Action -ceq 'Capture') { 'I_CONFIRM_PHASE4_API_LOG_CAPTURE' } else { 'I_CONFIRM_PHASE4_API_LOG_RESTORE' }
if ($Confirm -cne $required) { throw 'confirmation_mismatch' }

# 只允许项目已登记的测试机；认证完全交给现有 SSH 配置，不读取密码或私钥内容。

$remoteAction = $Action.ToLowerInvariant()
$sshArguments = "-T -p 10003 -o BatchMode=yes -o ConnectTimeout=10 pc@8.130.9.163 python3 -B - $remoteAction $required $CaptureId"
$result = Invoke-ExactRedirectedProcess -FilePath 'ssh' -Arguments $sshArguments -InputBytes $payload -TimeoutMilliseconds 120000
$stdout = $result.Stdout
$stderr = $result.Stderr
if (-not (Test-SafeSummary $stdout $stderr $result.ExitCode)) {
    throw 'remote_gate_failed_verify_service_readonly'
}
if ($result.ExitCode -eq 0 -and $stdout -notmatch (" capture_id=" + [regex]::Escape($CaptureId) + " ")) {
    throw 'remote_capture_id_mismatch'
}
$safeSummary = $stdout.TrimEnd([char[]]"`r`n")
Write-Output $safeSummary
if ($result.ExitCode -eq 2) { exit 2 }
