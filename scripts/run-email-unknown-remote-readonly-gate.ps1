[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm,

    [Parameter(Mandatory = $false)]
    [switch]$SelfTest
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:SuccessPattern = '^status=pass api_count=1 health=true ready=true live_adapter_mock=false mysql_count=1 redis_count=1 schema=57 dirty=false clock_drift_ok=true state_safe=true state_phase=(phase1_created|phase2_verified) primary_owned=1 unexpected_owned=1 scope_rows=2 template_owned=1 allowlist_owned=1 redis_ping=true run_id_changed=(true|false) lock_exists=(0|1) orphan_count=([0-9]+) orphan_safe_count=([0-9]+) cycle_evidence_count=2 cycle_valid_count=2 cycle_schema_count=2 cycle_excluded_count=2 writes=false restart=false cleanup=false\r?\n?\z'
$script:FailurePattern = '^status=failed stage=(?<stage>shell_options|api_identity|api_environment|health_transport|health|ready_transport|ready|required_environment|test_environment|mysql_identity|redis_identity|schema_query|schema_gate|clock_query|clock_format|clock_drift|state_type|state_owner|state_mode|state_parse|state_phase|state_values|identity_derive|primary_query|unexpected_query|scope_query|template_query|allowlist_query|fixture_ownership|redis_ping_transport|redis_ping|redis_info|redis_run_id|redis_exists|redis_exists_value|runtime_parent|orphan_metadata|cycle_metadata|cycle_count|cycle_name|cycle_target_source|cycle_dir_metadata|cycle_marker_metadata|cycle_dump_symlink|cycle_dump_metadata|cycle_targets_duplicate|cycle_schema_query|cycle_schema_missing|cycle_exclusion)\r?\n?\z'

function ConvertTo-Utf8PayloadBytes {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 统一为 LF 后直接使用无 BOM UTF-8 编码；拒绝 BOM 字符、NUL 和空载荷。
    $normalized = $Payload.Replace("`r`n", "`n").Replace("`r", "`n")
    if ($normalized.Length -eq 0 -or
        [int][char]$normalized[0] -eq 0xFEFF -or
        [int][char]$normalized[0] -eq 0xFFFE -or
        $normalized.IndexOf([char]0) -ge 0) {
        throw 'payload_bom_or_nul'
    }
    if (-not $normalized.StartsWith("set -Eeuo pipefail`n", [StringComparison]::Ordinal)) {
        throw 'payload_first_line'
    }

    $encoding = New-Object System.Text.UTF8Encoding($false, $true)
    $bytes = $encoding.GetBytes($normalized)
    if ($bytes.Length -lt 4 -or
        ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)) {
        throw 'payload_encoding'
    }
    return ,$bytes
}

function Test-RemoteSummary {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )

    if ($Stderr.Length -ne 0) {
        return [pscustomobject]@{ Classification = 'remote_stderr_nonempty'; Stage = $null }
    }
    if ($ExitCode -eq 0 -and [Text.RegularExpressions.Regex]::IsMatch($Stdout, $script:SuccessPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) {
        return [pscustomobject]@{ Classification = 'pass'; Stage = $null }
    }
    $failureMatch = [Text.RegularExpressions.Regex]::Match($Stdout, $script:FailurePattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if ($failureMatch.Success) {
        return [pscustomobject]@{ Classification = 'remote_gate_failed'; Stage = $failureMatch.Groups['stage'].Value }
    }
    if ($ExitCode -ne 0) {
        return [pscustomobject]@{ Classification = 'remote_exit_nonzero'; Stage = $null }
    }
    return [pscustomobject]@{ Classification = 'remote_output_invalid'; Stage = $null }
}

function Write-SafeFailure {
    param(
        [Parameter(Mandatory = $true)][string]$Classification,
        [Parameter(Mandatory = $true)][int]$AttemptCount,
        [Parameter(Mandatory = $true)][int]$CompletedCount,
        [Parameter(Mandatory = $true)][int]$StdoutLength,
        [Parameter(Mandatory = $true)][int]$StderrLength,
        [Parameter(Mandatory = $false)][AllowNull()][string]$Stage
    )

    $safe = [ordered]@{
        status = 'failed'
        classification = $Classification
        ssh_attempt_count = $AttemptCount
        ssh_completed_count = $CompletedCount
        stdout_length = $StdoutLength
        stderr_length = $StderrLength
        writes = $false
        restart = $false
        cleanup = $false
    }
    if ($Classification -ceq 'remote_gate_failed' -and -not [string]::IsNullOrEmpty($Stage)) {
        $safe.stage = $Stage
    }
    Write-Output ($safe | ConvertTo-Json -Compress)
}

function New-RestrictedTempDirectory {
    # 临时目录使用随机叶名称并关闭继承，只允许当前 Windows 身份完全控制。
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $leaf = 'molin-email-readonly-' + [Guid]::NewGuid().ToString('N')
    $path = [IO.Path]::GetFullPath((Join-Path $tempRoot $leaf))
    if (-not $path.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or [IO.Directory]::Exists($path) -or [IO.File]::Exists($path)) {
        throw 'temp_path_invalid'
    }
    [void][IO.Directory]::CreateDirectory($path)
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $security = New-Object Security.AccessControl.DirectorySecurity
    $security.SetOwner($currentSid)
    $security.SetAccessRuleProtection($true, $false)
    $rule = New-Object Security.AccessControl.FileSystemAccessRule(
        $currentSid,
        [Security.AccessControl.FileSystemRights]::FullControl,
        [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit',
        [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Allow
    )
    [void]$security.AddAccessRule($rule)
    [IO.Directory]::SetAccessControl($path, $security)
    $item = [IO.DirectoryInfo]::new($path)
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.FullName -cne $path) {
        throw 'temp_path_unsafe'
    }
    return $path
}

function Write-RestrictedBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes
    )
    if ([IO.File]::Exists($Path) -or [IO.Directory]::Exists($Path) -or -not [IO.Path]::IsPathRooted($Path)) {
        throw 'temp_file_invalid'
    }
    [IO.File]::WriteAllBytes($Path, $Bytes)
    $readBack = [IO.File]::ReadAllBytes($Path)
    if ($readBack.Length -ne $Bytes.Length) { throw 'temp_file_mismatch' }
    for ($index = 0; $index -lt $Bytes.Length; $index++) {
        if ($readBack[$index] -ne $Bytes[$index]) { throw 'temp_file_mismatch' }
    }
    $item = [IO.FileInfo]::new($Path)
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'temp_file_unsafe' }
}

function Remove-RestrictedTempDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not [IO.Path]::IsPathRooted($Path) -or -not ([IO.Path]::GetFileName($Path) -match '^molin-email-readonly-[a-f0-9]{32}$')) {
        throw 'temp_cleanup_path_invalid'
    }
    if ([IO.Directory]::Exists($Path)) {
        foreach ($name in @('stdin.bin', 'stdout.txt', 'stderr.txt', 'probe.ps1')) {
            $target = Join-Path $Path $name
            if ([IO.File]::Exists($target)) { [IO.File]::Delete($target) }
        }
        if ([IO.Directory]::GetFileSystemEntries($Path).Length -ne 0) { throw 'temp_cleanup_not_empty' }
        [IO.Directory]::Delete($Path, $false)
    }
}

function Start-FixedRedirectedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$ArgumentList,
        [Parameter(Mandatory = $true)][string]$InputPath,
        [Parameter(Mandatory = $true)][string]$OutputPath,
        [Parameter(Mandatory = $true)][string]$ErrorPath,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    $process = Microsoft.PowerShell.Management\Start-Process -FilePath $FilePath -ArgumentList $ArgumentList `
        -RedirectStandardInput $InputPath -RedirectStandardOutput $OutputPath -RedirectStandardError $ErrorPath `
        -NoNewWindow -PassThru
    # Windows PowerShell 5.1 必须在等待前立即取得原生句柄，否则带超时的 WaitForExit 可能无法固化真实退出码。
    try {
        $processHandle = $process.Handle
        if ($processHandle -eq [IntPtr]::Zero) { throw 'process_handle_unavailable' }
    }
    catch {
        try { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() } } catch { }
        throw 'process_handle_unavailable'
    }
    if (-not $process.WaitForExit($TimeoutMilliseconds)) {
        $process.Kill()
        $process.WaitForExit()
        throw 'process_timeout'
    }
    $process.Refresh()
    try { $exitCode = $process.ExitCode } catch { throw 'process_exit_code_unavailable' }
    # 禁止把 null 强制转换为 0；无法取得退出码时必须失败关闭，不能继续解析远端摘要。
    if ($null -eq $exitCode) { throw 'process_exit_code_unavailable' }
    return [pscustomobject]@{ ExitCode = [int]$exitCode }
}

$payloadPath = Join-Path $PSScriptRoot 'email-unknown-remote-readonly.payload.sh'

if ($SelfTest) {
    # SelfTest 只验证编码、严格摘要和失败分类，不启动 SSH 或访问任何外部资源。
    $cases = 0
    $payload = [IO.File]::ReadAllText($payloadPath, (New-Object Text.UTF8Encoding($false, $true)))
    $bytes = ConvertTo-Utf8PayloadBytes -Payload $payload
    if ($bytes[0] -ne 0x73 -or $bytes[1] -ne 0x65 -or $bytes[2] -ne 0x74) { throw 'selftest_ascii_prefix' }
    $cases++
    $crlfBytes = ConvertTo-Utf8PayloadBytes -Payload ($payload.Replace("`n", "`r`n"))
    if ($crlfBytes[0] -ne 0x73) { throw 'selftest_crlf' }
    $cases++
    $invalidPayloads = @()
    $invalidPayloads += ([char]0xFEFF) + $payload
    $invalidPayloads += ([char]0xFFFE) + $payload
    $invalidPayloads += "set -Eeuo pipefail`n`0"
    foreach ($invalidPayload in $invalidPayloads) {
        $rejected = $false
        try { [void](ConvertTo-Utf8PayloadBytes -Payload $invalidPayload) } catch { $rejected = $true }
        if (-not $rejected) { throw 'selftest_invalid_encoding' }
        $cases++
    }
    $validSummary = 'status=pass api_count=1 health=true ready=true live_adapter_mock=false mysql_count=1 redis_count=1 schema=57 dirty=false clock_drift_ok=true state_safe=true state_phase=phase1_created primary_owned=1 unexpected_owned=1 scope_rows=2 template_owned=1 allowlist_owned=1 redis_ping=true run_id_changed=true lock_exists=1 orphan_count=2 orphan_safe_count=2 cycle_evidence_count=2 cycle_valid_count=2 cycle_schema_count=2 cycle_excluded_count=2 writes=false restart=false cleanup=false' + "`n"
    if ((Test-RemoteSummary -Stdout $validSummary -Stderr '' -ExitCode 0).Classification -cne 'pass') { throw 'selftest_valid_summary' }
    $cases++
    foreach ($invalidResult in @(
        @{ Out = ($validSummary -replace ' cycle_excluded_count=2', ''); Err = ''; Code = 0 },
        @{ Out = $validSummary + "extra=true`n"; Err = ''; Code = 0 },
        @{ Out = ($validSummary -replace 'api_count=1 health=true', 'health=true api_count=1'); Err = ''; Code = 0 },
        @{ Out = $validSummary; Err = 'raw'; Code = 0 },
        @{ Out = ''; Err = ''; Code = 255 }
    )) {
        if ((Test-RemoteSummary -Stdout $invalidResult.Out -Stderr $invalidResult.Err -ExitCode $invalidResult.Code).Classification -ceq 'pass') {
            throw 'selftest_invalid_result'
        }
        $cases++
    }
    foreach ($stageCase in @(
        @{ Out = "status=failed stage=primary_query`n"; Err = ''; Code = 2; Class = 'remote_gate_failed'; Stage = 'primary_query'; Expose = $true },
        @{ Out = "status=failed stage=unknown_stage`n"; Err = ''; Code = 2; Class = 'remote_exit_nonzero'; Stage = $null; Expose = $false },
        @{ Out = "status=failed stage=primary_query`nextra=true`n"; Err = ''; Code = 2; Class = 'remote_exit_nonzero'; Stage = $null; Expose = $false },
        @{ Out = "status=failed stage=primary_query`n"; Err = 'transport'; Code = 2; Class = 'remote_stderr_nonempty'; Stage = $null; Expose = $false },
        @{ Out = "status=failed stage=primary_query`n`n"; Err = ''; Code = 2; Class = 'remote_exit_nonzero'; Stage = $null; Expose = $false },
        @{ Out = "status=failed stage=primary_query`r`n`r`n"; Err = ''; Code = 2; Class = 'remote_exit_nonzero'; Stage = $null; Expose = $false }
    )) {
        $stageResult = Test-RemoteSummary -Stdout $stageCase.Out -Stderr $stageCase.Err -ExitCode $stageCase.Code
        if ($stageResult.Classification -cne $stageCase.Class -or $stageResult.Stage -cne $stageCase.Stage) { throw 'selftest_stage_classification' }
        $safeJson = Write-SafeFailure -Classification $stageResult.Classification -Stage $stageResult.Stage `
            -AttemptCount 1 -CompletedCount 1 -StdoutLength $stageCase.Out.Length -StderrLength $stageCase.Err.Length
        $safeObject = $safeJson | ConvertFrom-Json
        $hasStage = $safeObject.PSObject.Properties.Name -ccontains 'stage'
        if ($hasStage -ne $stageCase.Expose) { throw 'selftest_stage_exposure' }
        if ($hasStage -and $safeObject.stage -cne $stageCase.Stage) { throw 'selftest_stage_value' }
        $cases++
    }
    $selfTestTemp = $null
    try {
        $selfTestTemp = New-RestrictedTempDirectory
        $inputPath = Join-Path $selfTestTemp 'stdin.bin'
        $outputPath = Join-Path $selfTestTemp 'stdout.txt'
        $errorPath = Join-Path $selfTestTemp 'stderr.txt'
        $probePath = Join-Path $selfTestTemp 'probe.ps1'
        Write-RestrictedBytes -Path $inputPath -Bytes $bytes
        Write-RestrictedBytes -Path $outputPath -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path $errorPath -Bytes ([byte[]]@())
        $probeSource = @'
$stream = [Console]::OpenStandardInput()
$first = $stream.ReadByte()
$second = $stream.ReadByte()
$third = $stream.ReadByte()
$buffer = New-Object byte[] 4096
while ($stream.Read($buffer, 0, $buffer.Length) -gt 0) { }
if ($first -ne 115 -or ($first -eq 239 -and $second -eq 187 -and $third -eq 191)) {
    [Console]::Error.Write('input_encoding_invalid')
    exit 3
}
[Console]::Out.Write("status=failed stage=shell_options`n")
exit 2
'@
        $probeBytes = (New-Object Text.UTF8Encoding($false, $true)).GetBytes($probeSource)
        Write-RestrictedBytes -Path $probePath -Bytes $probeBytes
        $powershellExe = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
        if (-not [IO.File]::Exists($powershellExe)) { throw 'selftest_powershell_missing' }
        $probe = Start-FixedRedirectedProcess -FilePath $powershellExe `
            -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', ('"' + $probePath + '"')) `
            -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 10000
        $probeOut = [IO.File]::ReadAllText($outputPath, (New-Object Text.UTF8Encoding($false, $true)))
        $probeErr = [IO.File]::ReadAllText($errorPath, (New-Object Text.UTF8Encoding($false, $true)))
        if ($probe.ExitCode -ne 2 -or $probeOut -cne "status=failed stage=shell_options`r`n" -and $probeOut -cne "status=failed stage=shell_options`n" -or $probeErr -cne '') {
            throw 'selftest_redirected_stdin'
        }
        $cases++
        # 使用真实 Windows PowerShell 5.1 子进程验证 0 与非零退出码都能原样穿透，防止 null 再次被转换成 0。
        foreach ($expectedExitCode in @(0, 7)) {
            $exitProbe = Start-FixedRedirectedProcess -FilePath $powershellExe -ArgumentList @('-NoProfile', '-NonInteractive', '-Command', ('exit ' + $expectedExitCode)) -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 10000
            if ($exitProbe.ExitCode -ne $expectedExitCode) { throw 'selftest_process_exit_code_mismatch' }
            $cases++
        }
    }
    finally {
        if ($null -ne $selfTestTemp) { Remove-RestrictedTempDirectory -Path $selfTestTemp }
    }
    Write-Output "status=pass mode=selftest cases=$cases external_access=false process_exit_codes=0,7"
    exit 0
}

$attemptCount = 0
$completedCount = 0
$stdoutLength = 0
$stderrLength = 0
$runTemp = $null
try {
    if ($Confirm -cne 'I_CONFIRM_EMAIL_UNKNOWN_REMOTE_READONLY_GATE_ONCE') {
        throw 'confirmation_required'
    }
    if (-not [IO.File]::Exists($payloadPath)) {
        throw 'payload_missing'
    }
    $payload = [IO.File]::ReadAllText($payloadPath, (New-Object Text.UTF8Encoding($false, $true)))
    $payloadBytes = ConvertTo-Utf8PayloadBytes -Payload $payload

    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($sshExe)) {
        throw 'ssh_tool_missing'
    }

    # 参数全部冻结且无外部输入；stdin 使用文件句柄重定向，避免 .NET 默认 StreamWriter 注入 UTF-8 BOM。
    $runTemp = New-RestrictedTempDirectory
    $inputPath = Join-Path $runTemp 'stdin.bin'
    $outputPath = Join-Path $runTemp 'stdout.txt'
    $errorPath = Join-Path $runTemp 'stderr.txt'
    Write-RestrictedBytes -Path $inputPath -Bytes $payloadBytes
    Write-RestrictedBytes -Path $outputPath -Bytes ([byte[]]@())
    Write-RestrictedBytes -Path $errorPath -Bytes ([byte[]]@())
    $sshArguments = @(
        '-T', '-p', '10003',
        '-o', 'BatchMode=yes',
        '-o', 'NumberOfPasswordPrompts=0',
        '-o', 'StrictHostKeyChecking=yes',
        '-o', 'ConnectTimeout=10',
        'pc@8.130.9.163',
        '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc', 'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8',
        '/bin/bash', '--noprofile', '--norc', '-s', '--'
    )
    $attemptCount = 1
    $process = Start-FixedRedirectedProcess -FilePath $sshExe -ArgumentList $sshArguments `
        -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 120000
    $completedCount = 1
    $stdout = [IO.File]::ReadAllText($outputPath, (New-Object Text.UTF8Encoding($false, $true)))
    $stderr = [IO.File]::ReadAllText($errorPath, (New-Object Text.UTF8Encoding($false, $true)))
    $stdoutLength = $stdout.Length
    $stderrLength = $stderr.Length
    $remoteResult = Test-RemoteSummary -Stdout $stdout -Stderr $stderr -ExitCode $process.ExitCode
    if ($remoteResult.Classification -cne 'pass') {
        Write-SafeFailure -Classification $remoteResult.Classification -Stage $remoteResult.Stage -AttemptCount $attemptCount -CompletedCount $completedCount -StdoutLength $stdoutLength -StderrLength $stderrLength
        exit 2
    }

    # stdout 已通过唯一整行正则，只包含固定布尔、计数和安全状态，可原样作为最终摘要输出。
    Write-Output $stdout.TrimEnd([char[]]@("`r", "`n"))
    exit 0
}
catch {
    $classification = 'local_gate_failed'
    if ($_.Exception.Message -in @('confirmation_required', 'payload_missing', 'payload_bom_or_nul', 'payload_first_line', 'payload_encoding', 'ssh_tool_missing', 'ssh_start_failed', 'process_timeout', 'temp_path_invalid', 'temp_path_unsafe', 'temp_file_invalid', 'temp_file_mismatch', 'temp_file_unsafe')) {
        $classification = $_.Exception.Message
    }
    Write-SafeFailure -Classification $classification -AttemptCount $attemptCount -CompletedCount $completedCount -StdoutLength $stdoutLength -StderrLength $stderrLength
    exit 2
}
finally {
    if ($null -ne $runTemp) { Remove-RestrictedTempDirectory -Path $runTemp }
}
