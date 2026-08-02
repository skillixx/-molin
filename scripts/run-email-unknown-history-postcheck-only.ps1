[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm,

    [Parameter(Mandatory = $false)]
    [switch]$SelfTest,

    [Parameter(Mandatory = $false)]
    [switch]$LocalPreflightOnly
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RequiredPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_HISTORY_POSTCHECK_ONLY_ONCE'
$script:CleanupBinarySHA256 = 'd211268ec0b13e5c92ba1992d41b98f4a0c3415ae4fd348deb9ac843614854a4'
$script:CycleDumpSHA256 = @(
    'd6696c7e0b76952a04cedc6ee7212ceed098c4f9ef6ab276b082560f74fb479e',
    '9e1242742fe1fbbc44e8abe4ab9b0ac8f2d2be1071a6e2f8c843ff1d1a2a6dbc'
)
$script:MetadataPattern = '^status=pass recovery_filename=(?<recovery>molin-email-unknown-[a-f0-9]{32}\.sql) recovery_sha256=(?<recovery_sha>[a-f0-9]{64}) cycle_sha256_one=(?<cycle_one>[a-f0-9]{64}) cycle_sha256_two=(?<cycle_two>[a-f0-9]{64})\r?\n?\z'
$script:PostcheckSuccessPattern = '^status=pass api_health=true api_ready=true schema=57 dirty=false fixture_logs_absent=2 scope_rows=0 allowlist_absent=1 template_absent=1 redis_ping=true redis_key_absent=true recovery_mode=600 recovery_sha256_valid=true cleanup_binary_sha256_valid=true cycle_evidence_count=2 cycle_schema_count=2 state_dependency=false writes=false restarts=false retries=0\r?\n?\z'
$script:PostcheckFailureClassifications = @(
    'confirmation_required', 'payload_missing', 'payload_path_invalid', 'payload_bom_or_nul', 'payload_first_line',
    'payload_encoding', 'payload_placeholder_invalid', 'recovery_filename_invalid', 'binary_sha_invalid',
    'recovery_sha_invalid', 'cycle_dump_sha_invalid', 'ssh_tool_missing', 'temp_path_invalid', 'temp_path_unsafe',
    'temp_file_invalid', 'temp_file_mismatch', 'temp_file_unsafe', 'process_timeout', 'local_gate_failed',
    'remote_stderr_nonempty', 'remote_gate_failed', 'remote_exit_nonzero', 'remote_output_invalid'
)
$script:PostcheckRemoteStages = @(
    'shell_options', 'api_identity', 'api_environment', 'health_transport', 'health_json', 'ready_transport',
    'ready_json', 'required_environment', 'container_identity', 'recovery_gate', 'recovery_identity', 'identity_json',
    'schema_query', 'schema_gate', 'fixture_query', 'fixture_absence', 'redis_ping', 'redis_exists', 'binary_gate',
    'cycle_metadata', 'cycle_schema', 'final_artifacts'
)

function ConvertTo-Utf8PayloadBytes {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 远端 stdin 必须是首行严格模式、LF 且无 BOM/NUL，避免隐藏字节改变 Bash 语义。
    $normalized = $Payload.Replace("`r`n", "`n").Replace("`r", "`n")
    if ($normalized.Length -eq 0 -or [int][char]$normalized[0] -in @(0xFEFF, 0xFFFE) -or $normalized.IndexOf([char]0) -ge 0) { throw 'payload_encoding_invalid' }
    if (-not $normalized.StartsWith("set -Eeuo pipefail`n", [StringComparison]::Ordinal)) { throw 'payload_encoding_invalid' }
    $bytes = (New-Object Text.UTF8Encoding($false, $true)).GetBytes($normalized)
    if ($bytes.Length -lt 4 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)) { throw 'payload_encoding_invalid' }
    return ,$bytes
}

function Read-VerifiedMetadataPayload {
    $expected = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'email-unknown-history-postcheck-metadata.payload.sh'))
    if (-not [IO.File]::Exists($expected)) { throw 'metadata_payload_missing' }
    $item = [IO.FileInfo]::new($expected)
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.FullName -cne $expected) { throw 'metadata_payload_path_invalid' }
    return [IO.File]::ReadAllText($expected, (New-Object Text.UTF8Encoding($false, $true)))
}

function New-RestrictedTempDirectory {
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $path = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-postcheck-only-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $path.StartsWith($root, [StringComparison]::OrdinalIgnoreCase) -or [IO.Directory]::Exists($path) -or [IO.File]::Exists($path)) { throw 'temp_path_invalid' }
    [void][IO.Directory]::CreateDirectory($path)
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $security = New-Object Security.AccessControl.DirectorySecurity
    $security.SetOwner($sid)
    $security.SetAccessRuleProtection($true, $false)
    [void]$security.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid, [Security.AccessControl.FileSystemRights]::FullControl, [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit', [Security.AccessControl.PropagationFlags]::None, [Security.AccessControl.AccessControlType]::Allow)))
    [IO.Directory]::SetAccessControl($path, $security)
    if (([IO.DirectoryInfo]::new($path).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'temp_path_invalid' }
    return $path
}

function Write-RestrictedBytes {
    param([Parameter(Mandatory = $true)][string]$Path, [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes)
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.File]::Exists($Path) -or [IO.Directory]::Exists($Path)) { throw 'temp_file_invalid' }
    [IO.File]::WriteAllBytes($Path, $Bytes)
    $actual = [IO.File]::ReadAllBytes($Path)
    if ($actual.Length -ne $Bytes.Length -or ([IO.FileInfo]::new($Path).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'temp_file_invalid' }
    for ($index = 0; $index -lt $Bytes.Length; $index++) { if ($actual[$index] -ne $Bytes[$index]) { throw 'temp_file_invalid' } }
}

function Remove-RestrictedTempDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.Path]::GetFileName($Path) -cnotmatch '^molin-email-postcheck-only-[a-f0-9]{32}$') { throw 'temp_cleanup_path_invalid' }
    if ([IO.Directory]::Exists($Path)) {
        foreach ($file in [IO.Directory]::GetFiles($Path)) {
            if ([IO.Path]::GetFileName($file) -cnotin @('metadata.stdin', 'metadata.stdout', 'metadata.stderr', 'postcheck.stdin', 'postcheck.stdout', 'postcheck.stderr')) { throw 'temp_cleanup_file_invalid' }
            [IO.File]::Delete($file)
        }
        if ([IO.Directory]::GetFileSystemEntries($Path).Length -ne 0) { throw 'temp_cleanup_not_empty' }
        [IO.Directory]::Delete($Path, $false)
    }
}

function Initialize-RunFiles {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][byte[]]$MetadataBytes
    )
    if ($MetadataBytes.Length -eq 0) { throw 'temp_file_invalid' }
    foreach ($stage in @('metadata', 'postcheck')) {
        if ($stage -ceq 'metadata') {
            Write-RestrictedBytes (Join-Path $Path 'metadata.stdin') $MetadataBytes
        }
        else {
            # Windows PowerShell 5.1 会把赋值表达式中的空数组折叠为 null，必须在调用点显式传入。
            Write-RestrictedBytes (Join-Path $Path 'postcheck.stdin') ([byte[]]@())
        }
        Write-RestrictedBytes (Join-Path $Path ($stage + '.stdout')) ([byte[]]@())
        Write-RestrictedBytes (Join-Path $Path ($stage + '.stderr')) ([byte[]]@())
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
    $process = Microsoft.PowerShell.Management\Start-Process -FilePath $FilePath -ArgumentList $ArgumentList -RedirectStandardInput $InputPath -RedirectStandardOutput $OutputPath -RedirectStandardError $ErrorPath -NoNewWindow -PassThru
    # Windows PowerShell 5.1 必须先固化句柄，随后才能可靠读取原生退出码。
    try {
        $handle = $process.Handle
        if ($handle -eq [IntPtr]::Zero) { throw 'process_handle_unavailable' }
    }
    catch {
        try { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() } } catch { }
        throw 'process_handle_unavailable'
    }
    if (-not $process.WaitForExit($TimeoutMilliseconds)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
    $process.Refresh()
    try { $exitCode = $process.ExitCode } catch { throw 'process_exit_code_unavailable' }
    if ($null -eq $exitCode) { throw 'process_exit_code_unavailable' }
    return [pscustomobject]@{ ExitCode = [int]$exitCode }
}

function Test-MetadataSummary {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )
    if ($ExitCode -ne 0) { throw 'metadata_exit_nonzero' }
    if ($Stderr.Length -ne 0) { throw 'metadata_stderr_nonempty' }
    $match = [regex]::Match($Stdout, $script:MetadataPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success) { throw 'metadata_summary_invalid' }
    $actual = @($match.Groups['cycle_one'].Value, $match.Groups['cycle_two'].Value) | Sort-Object
    $expected = @($script:CycleDumpSHA256) | Sort-Object
    if ($actual.Count -ne 2 -or $actual[0] -cne $expected[0] -or $actual[1] -cne $expected[1]) { throw 'metadata_cycle_set_invalid' }
    return [pscustomobject]@{
        RecoveryFileName = $match.Groups['recovery'].Value
        RecoverySHA256 = $match.Groups['recovery_sha'].Value
        CycleDumpSHA256 = @($actual)
    }
}

function Test-PostcheckChildSummary {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )
    if ($ExitCode -eq 0 -and $Stderr.Length -eq 0 -and [regex]::IsMatch($Stdout, $script:PostcheckSuccessPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) {
        return [pscustomobject]@{ Status = 'pass'; Classification = 'pass'; PostcheckStage = $null }
    }
    # 子 runner 失败只接受固定 JSON 形状；额外字段、乱序、未知分类或未知 stage 一律折叠。
    if ($ExitCode -ne 2 -or $Stderr.Length -ne 0) { return [pscustomobject]@{ Status = 'failed'; Classification = 'postcheck_failed'; PostcheckStage = $null } }
    $pattern = '^\{"status":"failed","classification":"(?<classification>[a-z_]+)","ssh_attempt_count":(?<attempt>[01]),"ssh_completed_count":(?<completed>[01]),"stdout_length":[0-9]+,"stderr_length":[0-9]+,"writes":false,"restart":false,"cleanup":false,"retries":0(?:,"stage":"(?<stage>[a-z_]+)")?\}\r?\n?\z'
    $match = [regex]::Match($Stdout, $pattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success) { return [pscustomobject]@{ Status = 'failed'; Classification = 'postcheck_failed'; PostcheckStage = $null } }
    $classification = $match.Groups['classification'].Value
    $postcheckStage = if ($match.Groups['stage'].Success) { $match.Groups['stage'].Value } else { $null }
    if ($classification -cnotin $script:PostcheckFailureClassifications -or [int]$match.Groups['completed'].Value -gt [int]$match.Groups['attempt'].Value -or
        (($classification -ceq 'remote_gate_failed') -ne (-not [string]::IsNullOrEmpty($postcheckStage))) -or
        (-not [string]::IsNullOrEmpty($postcheckStage) -and $postcheckStage -cnotin $script:PostcheckRemoteStages)) {
        return [pscustomobject]@{ Status = 'failed'; Classification = 'postcheck_failed'; PostcheckStage = $null }
    }
    return [pscustomobject]@{ Status = 'failed'; Classification = $classification; PostcheckStage = $postcheckStage }
}

function Invoke-PostcheckOnlyFlow {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$MetadataAction,
        [Parameter(Mandatory = $true)][scriptblock]$PostcheckAction
    )
    try { $metadata = & $MetadataAction }
    catch {
        $known = @('metadata_exit_nonzero', 'metadata_stderr_nonempty', 'metadata_summary_invalid', 'metadata_cycle_set_invalid', 'process_timeout', 'process_handle_unavailable', 'process_exit_code_unavailable')
        $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'metadata_failed' }
        return [pscustomobject]@{ Status = 'failed'; Stage = 'metadata'; Classification = $classification; MetadataAttempts = 1; PostcheckCalls = 0; PostcheckStage = $null }
    }
    try { $postcheck = & $PostcheckAction $metadata }
    catch { return [pscustomobject]@{ Status = 'failed'; Stage = 'postcheck'; Classification = 'postcheck_failed'; MetadataAttempts = 1; PostcheckCalls = 1; PostcheckStage = $null } }
    if ($postcheck.Status -cne 'pass') {
        return [pscustomobject]@{ Status = 'failed'; Stage = 'postcheck'; Classification = $postcheck.Classification; MetadataAttempts = 1; PostcheckCalls = 1; PostcheckStage = $postcheck.PostcheckStage }
    }
    return [pscustomobject]@{ Status = 'pass'; Stage = 'complete'; Classification = 'pass'; MetadataAttempts = 1; PostcheckCalls = 1; PostcheckStage = $null }
}

function Write-SafeResult {
    param([Parameter(Mandatory = $true)]$Result)
    if ($Result.Status -ceq 'pass') {
        Write-Output 'status=pass stage=complete metadata_ssh_attempts=1 postcheck_calls=1 retries=0'
        return
    }
    if ($Result.Stage -ceq 'postcheck' -and $Result.Classification -ceq 'remote_gate_failed' -and -not [string]::IsNullOrEmpty([string]$Result.PostcheckStage)) {
        Write-Output ("status=failed stage=postcheck classification=remote_gate_failed postcheck_stage={0} metadata_ssh_attempts=1 postcheck_calls=1 retries=0" -f $Result.PostcheckStage)
        return
    }
    Write-Output ("status=failed stage={0} classification={1} metadata_ssh_attempts={2} postcheck_calls={3} retries=0" -f $Result.Stage, $Result.Classification, $Result.MetadataAttempts, $Result.PostcheckCalls)
}

function Invoke-LocalPreflightCheck {
    $temp = $null
    try {
        $payloadPath = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'email-unknown-history-postcheck-metadata.payload.sh'))
        $postcheckRunner = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'run-email-unknown-history-postcheck.ps1'))
        foreach ($path in @($payloadPath, $postcheckRunner)) {
            $item = [IO.FileInfo]::new($path)
            if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot)) { throw 'runner_path_invalid' }
        }
        $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
        $powerShellExe = Join-Path $PSHOME 'powershell.exe'
        foreach ($tool in @($sshExe, $powerShellExe)) { if (-not [IO.File]::Exists($tool) -or ([IO.FileInfo]::new($tool).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'tool_missing' } }
        $payloadBytes = ConvertTo-Utf8PayloadBytes (Read-VerifiedMetadataPayload)
        $temp = New-RestrictedTempDirectory
        Initialize-RunFiles $temp $payloadBytes
        return [pscustomobject]@{ Status = 'pass'; Classification = 'pass'; FilesVerified = 3 }
    }
    catch {
        $known = @('metadata_payload_missing', 'metadata_payload_path_invalid', 'payload_encoding_invalid', 'runner_path_invalid', 'tool_missing', 'temp_path_invalid', 'temp_file_invalid')
        $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'local_preflight_failed' }
        return [pscustomobject]@{ Status = 'failed'; Classification = $classification; FilesVerified = 0 }
    }
    finally { if ($null -ne $temp) { Remove-RestrictedTempDirectory $temp } }
}

if ($SelfTest) {
    # 自检只读取本地文件并启动本机 PowerShell 子进程，不发现或启动 SSH。
    $cases = 0
    $payload = Read-VerifiedMetadataPayload
    $bytes = ConvertTo-Utf8PayloadBytes $payload
    if ($bytes[0] -ne 0x73) { throw 'selftest_payload_encoding' }
    $cases++
    # 该夹具必须与正式路径使用相同的阶段初始化形状，防止空字节数组只在正式入口折叠为 null。
    $runtimeInitTemp = $null
    try {
        $runtimeInitTemp = New-RestrictedTempDirectory
        Initialize-RunFiles $runtimeInitTemp $bytes
        $cases++
    }
    finally { if ($null -ne $runtimeInitTemp) { Remove-RestrictedTempDirectory $runtimeInitTemp } }
    foreach ($required in @('parent_dirs=(/home/pc /home/pc/molin "$rollback_dir")', '[[ -d "$parent_dir" && ! -L "$parent_dir" ]]', '[[ "$(/usr/bin/stat -c ''%u'' -- "$parent_dir")" == "$(/usr/bin/id -u)" ]]', '$(( 8#$parent_mode & 022 )) == 0', 'recovery_candidates', '(( ${#recovery_candidates[@]} == 1 ))', 'sort_and_compare_cycle_sha_sets', 'status=pass recovery_filename=%s', '/usr/bin/docker ps', 'writes=false')) {
        if ($required -ceq 'writes=false') {
            if ($payload.Contains($required)) { throw 'selftest_payload_output_claim' }
        }
        elseif (-not $payload.Contains($required)) { throw 'selftest_payload_contract' }
        $cases++
    }
    foreach ($forbidden in @('/usr/bin/mysql ', '/usr/local/bin/redis-cli', ' FLUSHDB', ' FLUSHALL', ' KEYS ', ' SCAN ', ' DELETE ', ' UPDATE ', ' INSERT ', ' REPLACE ', ' ALTER ', ' DROP ', ' TRUNCATE ', 'docker restart', 'docker stop', 'docker kill', 'docker rm')) {
        if ($payload.Contains($forbidden)) { throw 'selftest_payload_side_effect' }
    }
    $cases++
    $recovery = 'molin-email-unknown-' + ('1' * 32) + '.sql'
    $validMetadata = "status=pass recovery_filename=$recovery recovery_sha256=$('a' * 64) cycle_sha256_one=$($script:CycleDumpSHA256[1]) cycle_sha256_two=$($script:CycleDumpSHA256[0])`n"
    $metadata = Test-MetadataSummary $validMetadata '' 0
    if ($metadata.RecoveryFileName -cne $recovery -or $metadata.CycleDumpSHA256.Count -ne 2) { throw 'selftest_metadata_valid' }
    $cases++
    foreach ($fixture in @(
        @{ Out = $validMetadata; Err = 'warning'; Code = 0 },
        @{ Out = $validMetadata + "extra=true`n"; Err = ''; Code = 0 },
        @{ Out = $validMetadata; Err = ''; Code = 2 },
        @{ Out = $validMetadata.Replace($script:CycleDumpSHA256[0], ('b' * 64)); Err = ''; Code = 0 }
    )) {
        try { [void](Test-MetadataSummary $fixture.Out $fixture.Err $fixture.Code); throw 'selftest_metadata_attack_accepted' }
        catch { if ($_.Exception.Message -eq 'selftest_metadata_attack_accepted') { throw } }
        $cases++
    }
    $validPostcheck = "status=pass api_health=true api_ready=true schema=57 dirty=false fixture_logs_absent=2 scope_rows=0 allowlist_absent=1 template_absent=1 redis_ping=true redis_key_absent=true recovery_mode=600 recovery_sha256_valid=true cleanup_binary_sha256_valid=true cycle_evidence_count=2 cycle_schema_count=2 state_dependency=false writes=false restarts=false retries=0`n"
    if ((Test-PostcheckChildSummary $validPostcheck '' 0).Status -cne 'pass') { throw 'selftest_postcheck_valid' }
    $cases++
    $safeFailure = '{"status":"failed","classification":"remote_gate_failed","ssh_attempt_count":1,"ssh_completed_count":1,"stdout_length":16,"stderr_length":0,"writes":false,"restart":false,"cleanup":false,"retries":0,"stage":"fixture_absence"}' + "`n"
    $failure = Test-PostcheckChildSummary $safeFailure '' 2
    if ($failure.Classification -cne 'remote_gate_failed' -or $failure.PostcheckStage -cne 'fixture_absence') { throw 'selftest_postcheck_failure' }
    $cases++
    $attackIndex = 0
    foreach ($attack in @($safeFailure.Replace('fixture_absence', 'unsafe_stage'), ($safeFailure + "extra=true`n"), ($safeFailure.TrimEnd() + ',"path":"unsafe"}'))) {
        if ((Test-PostcheckChildSummary $attack '' 2).Classification -cne 'postcheck_failed') { throw ("selftest_postcheck_attack_accepted_{0}" -f $attackIndex) }
        $cases++
        $attackIndex++
    }
    $calls = New-Object Collections.Generic.List[string]
    $flow = Invoke-PostcheckOnlyFlow { $calls.Add('metadata') | Out-Null; $metadata } { param($frozen); $calls.Add('postcheck') | Out-Null; Test-PostcheckChildSummary $safeFailure '' 2 }
    $line = (Write-SafeResult $flow | Out-String).Trim()
    if (($calls -join ',') -cne 'metadata,postcheck' -or $line -cne 'status=failed stage=postcheck classification=remote_gate_failed postcheck_stage=fixture_absence metadata_ssh_attempts=1 postcheck_calls=1 retries=0') { throw 'selftest_flow_order' }
    $cases++
    $calls.Clear()
    $flow = Invoke-PostcheckOnlyFlow { $calls.Add('metadata') | Out-Null; throw 'metadata_summary_invalid' } { $calls.Add('postcheck') | Out-Null }
    if (($calls -join ',') -cne 'metadata' -or $flow.PostcheckCalls -ne 0) { throw 'selftest_metadata_stop' }
    $cases++
    $processTemp = $null
    try {
        $processTemp = New-RestrictedTempDirectory
        $input = Join-Path $processTemp 'metadata.stdin'; $output = Join-Path $processTemp 'metadata.stdout'; $errorPath = Join-Path $processTemp 'metadata.stderr'
        Write-RestrictedBytes $input ([byte[]]@()); Write-RestrictedBytes $output ([byte[]]@()); Write-RestrictedBytes $errorPath ([byte[]]@())
        $windowsPowerShellExe = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
        foreach ($expectedExitCode in @(0, 7)) {
            $result = Start-FixedRedirectedProcess $windowsPowerShellExe @('-NoProfile', '-NonInteractive', '-Command', ('exit ' + $expectedExitCode)) $input $output $errorPath 10000
            if ($result.ExitCode -ne $expectedExitCode) { throw 'selftest_process_exit_code' }
            $cases++
        }
        # 使用完整正式参数顺序启动子 runner 的本地 SelfTest，覆盖 powershell.exe -File 的真实 argv 绑定。
        $postcheckRunnerForBinding = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'run-email-unknown-history-postcheck.ps1'))
        $bindingArguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $postcheckRunnerForBinding, '-SelfTest', '-Confirm', 'LOCAL_BINDING_PROBE', '-RecoveryFileName', ('molin-email-unknown-' + ('1' * 32) + '.sql'), '-ExpectedCleanupBinarySHA256', ('c' * 64), '-ExpectedRecoverySHA256', ('d' * 64), '-ExpectedCycleDumpSHA256One', ('a' * 64), '-ExpectedCycleDumpSHA256Two', ('b' * 64))
        $bindingResult = Start-FixedRedirectedProcess $windowsPowerShellExe $bindingArguments $input $output $errorPath 30000
        $bindingStdout = [IO.File]::ReadAllText($output, (New-Object Text.UTF8Encoding($false, $true)))
        $bindingStderr = [IO.File]::ReadAllText($errorPath, (New-Object Text.UTF8Encoding($false, $true)))
        if ($bindingResult.ExitCode -ne 0 -or $bindingStderr.Length -ne 0 -or $bindingStdout -cnotmatch '\Astatus=pass mode=selftest cases=[1-9][0-9]* external_access=false ssh_attempt_count=0 strict_json=true state_dependency=false output_verified=true process_exit_codes=0,7\r?\n?\z') {
            throw 'selftest_postcheck_argv_binding'
        }
        $cases++
    }
    finally { if ($null -ne $processTemp) { Remove-RestrictedTempDirectory $processTemp } }
    $preflight = Invoke-LocalPreflightCheck
    if ($preflight.Status -cne 'pass' -or $preflight.FilesVerified -ne 3) { throw ('selftest_preflight_' + $preflight.Classification) }
    $cases++
    Write-Output "status=pass mode=selftest cases=$cases external_access=false ssh_attempt_count=0 postcheck_calls=0 retries=0"
    exit 0
}

if ($LocalPreflightOnly) {
    $preflight = Invoke-LocalPreflightCheck
    if ($preflight.Status -ceq 'pass') {
        Write-Output 'status=pass stage=local_preflight files_verified=3 ssh_started=false postcheck_started=false retries=0'
        exit 0
    }
    Write-Output ("status=failed stage=local_preflight classification={0} ssh_started=false postcheck_started=false retries=0" -f $preflight.Classification)
    exit 2
}

$runTemp = $null
$finalResult = $null
try {
    if ($Confirm -cne $script:RequiredPhrase) { throw 'confirmation_required' }
    $metadataPayloadBytes = ConvertTo-Utf8PayloadBytes (Read-VerifiedMetadataPayload)
    $postcheckRunner = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'run-email-unknown-history-postcheck.ps1'))
    if (-not [IO.File]::Exists($postcheckRunner) -or ([IO.FileInfo]::new($postcheckRunner).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        [IO.Path]::GetDirectoryName($postcheckRunner) -cne [IO.Path]::GetFullPath($PSScriptRoot)) { throw 'runner_path_invalid' }
    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    $powerShellExe = Join-Path $PSHOME 'powershell.exe'
    if (-not [IO.File]::Exists($sshExe) -or -not [IO.File]::Exists($powerShellExe)) { throw 'tool_missing' }
    $runTemp = New-RestrictedTempDirectory
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    Initialize-RunFiles $runTemp $metadataPayloadBytes
    $metadataAction = {
        $arguments = @('-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', 'pc@8.130.9.163', '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc', 'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '45s', '/bin/bash', '--noprofile', '--norc', '-s', '--')
        $process = Start-FixedRedirectedProcess $sshExe $arguments (Join-Path $runTemp 'metadata.stdin') (Join-Path $runTemp 'metadata.stdout') (Join-Path $runTemp 'metadata.stderr') 60000
        return Test-MetadataSummary ([IO.File]::ReadAllText((Join-Path $runTemp 'metadata.stdout'), $encoding)) ([IO.File]::ReadAllText((Join-Path $runTemp 'metadata.stderr'), $encoding)) $process.ExitCode
    }
    $postcheckAction = {
        param($metadata)
        # 两个 cycle 摘要各使用独立命名参数，避免 Windows PowerShell 5.1 把第二个值当成未命名位置参数。
        $arguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $postcheckRunner, '-Confirm', 'I_CONFIRM_EMAIL_UNKNOWN_HISTORY_POSTCHECK_ONCE', '-RecoveryFileName', $metadata.RecoveryFileName, '-ExpectedCleanupBinarySHA256', $script:CleanupBinarySHA256, '-ExpectedRecoverySHA256', $metadata.RecoverySHA256, '-ExpectedCycleDumpSHA256One', $metadata.CycleDumpSHA256[0], '-ExpectedCycleDumpSHA256Two', $metadata.CycleDumpSHA256[1])
        $process = Start-FixedRedirectedProcess $powerShellExe $arguments (Join-Path $runTemp 'postcheck.stdin') (Join-Path $runTemp 'postcheck.stdout') (Join-Path $runTemp 'postcheck.stderr') 210000
        return Test-PostcheckChildSummary ([IO.File]::ReadAllText((Join-Path $runTemp 'postcheck.stdout'), $encoding)) ([IO.File]::ReadAllText((Join-Path $runTemp 'postcheck.stderr'), $encoding)) $process.ExitCode
    }
    $finalResult = Invoke-PostcheckOnlyFlow $metadataAction $postcheckAction
}
catch {
    $known = @('confirmation_required', 'metadata_payload_missing', 'metadata_payload_path_invalid', 'payload_encoding_invalid', 'runner_path_invalid', 'tool_missing', 'temp_path_invalid', 'temp_file_invalid', 'process_timeout', 'process_handle_unavailable', 'process_exit_code_unavailable')
    $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'local_gate_failed' }
    $finalResult = [pscustomobject]@{ Status = 'failed'; Stage = 'local'; Classification = $classification; MetadataAttempts = 0; PostcheckCalls = 0; PostcheckStage = $null }
}
finally {
    if ($null -ne $runTemp) {
        try { Remove-RestrictedTempDirectory $runTemp }
        catch {
            # 临时目录清理失败时保留已经发生的调用计数，但绝不输出临时路径或原始异常。
            $metadataAttempts = if ($null -ne $finalResult) { $finalResult.MetadataAttempts } else { 0 }
            $postcheckCalls = if ($null -ne $finalResult) { $finalResult.PostcheckCalls } else { 0 }
            $finalResult = [pscustomobject]@{ Status = 'failed'; Stage = 'local'; Classification = 'temp_cleanup_failed'; MetadataAttempts = $metadataAttempts; PostcheckCalls = $postcheckCalls; PostcheckStage = $null }
        }
    }
}

Write-SafeResult $finalResult
if ($finalResult.Status -ceq 'pass') { exit 0 }
exit 2
