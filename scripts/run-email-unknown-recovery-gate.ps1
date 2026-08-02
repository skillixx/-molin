[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm,

    [Parameter(Mandatory = $false)]
    [switch]$SelfTest
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RequiredPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_RECOVERY_GATE_ONCE'
$script:SuccessPattern = '^status=pass schema=57 dirty=false fixture_logs=2 fixture_allowlist=1 fixture_template=1 snapshot_stable=true backup_published=true backup_mode=600 backup_sha256_valid=true cycle_evidence_retained=2 cleanup=false database_writes=false restarts=false retries=0\r?\n?\z'
$script:FailurePattern = '^status=failed stage=(?<stage>shell_options|state_gate|target_gate|container_gate|state_parse|preflight_snapshot|temp_create|dump|temp_verify|postdump_snapshot|atomic_publish|published_verify) backup_published=(?<published>true|false)\r?\n?\z'

function ConvertTo-Utf8PayloadBytes {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 统一为无 BOM 的 UTF-8 和 LF，避免远端严格模式因编码或换行差异失效。
    $normalized = $Payload.Replace("`r`n", "`n").Replace("`r", "`n")
    if ($normalized.Length -eq 0 -or
        [int][char]$normalized[0] -eq 0xFEFF -or
        [int][char]$normalized[0] -eq 0xFFFE -or
        $normalized.IndexOf([char]0) -ge 0 -or
        -not $normalized.StartsWith("set -Eeuo pipefail`n", [StringComparison]::Ordinal)) {
        throw 'payload_encoding_invalid'
    }
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    $bytes = $encoding.GetBytes($normalized)
    if ($bytes.Length -lt 4 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)) {
        throw 'payload_encoding_invalid'
    }
    return ,$bytes
}

function Read-VerifiedPayload {
    # payload 必须是 scripts 目录直属的普通文件，拒绝符号链接、联接和目录逃逸。
    $scriptsRoot = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $expected = [IO.Path]::GetFullPath((Join-Path $scriptsRoot 'email-unknown-recovery-gate.payload.sh'))
    $item = [IO.FileInfo]::new($expected)
    if (-not $item.Exists -or
        ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $item.DirectoryName -cne $scriptsRoot -or
        $item.FullName -cne $expected) {
        throw 'payload_path_invalid'
    }
    return [IO.File]::ReadAllText($expected, (New-Object Text.UTF8Encoding($false, $true)))
}

function Assert-OperationArtifactNames {
    param(
        [Parameter(Mandatory = $true)][string]$StatePath,
        [Parameter(Mandatory = $true)][string]$RecoveryPath
    )

    # 离线攻击测试复刻远端绑定规则：恢复点必须携带状态文件中的同一个 operation nonce。
    $stateMatch = [regex]::Match($StatePath, '\A/home/pc/molin-email-unknown-(?<nonce>[a-f0-9]{32})\.state\z', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $recoveryMatch = [regex]::Match($RecoveryPath, '\A/home/pc/molin/rollback/molin-email-unknown-(?<nonce>[a-f0-9]{32})\.sql\z', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $stateMatch.Success -or -not $recoveryMatch.Success -or
        -not [string]::Equals($stateMatch.Groups['nonce'].Value, $recoveryMatch.Groups['nonce'].Value, [StringComparison]::Ordinal)) {
        throw 'operation_nonce_mismatch'
    }
}

function New-RestrictedTempDirectory {
    # 随机目录关闭 ACL 继承并仅授权当前 Windows 身份，防止远端 stdin 或输出被同机账号读取。
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $path = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-recovery-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $path.StartsWith($root, [StringComparison]::OrdinalIgnoreCase) -or [IO.Directory]::Exists($path) -or [IO.File]::Exists($path)) {
        throw 'temp_path_invalid'
    }
    $created = $false
    try {
        [void][IO.Directory]::CreateDirectory($path)
        $created = $true
        $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
        $acl = New-Object Security.AccessControl.DirectorySecurity
        $acl.SetOwner($sid)
        $acl.SetAccessRuleProtection($true, $false)
        $rule = New-Object Security.AccessControl.FileSystemAccessRule(
            $sid,
            [Security.AccessControl.FileSystemRights]::FullControl,
            [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit',
            [Security.AccessControl.PropagationFlags]::None,
            [Security.AccessControl.AccessControlType]::Allow
        )
        [void]$acl.AddAccessRule($rule)
        [IO.Directory]::SetAccessControl($path, $acl)
        $verifiedAcl = [IO.Directory]::GetAccessControl($path)
        $rules = @($verifiedAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
        if (-not $verifiedAcl.AreAccessRulesProtected -or
            $verifiedAcl.GetOwner([Security.Principal.SecurityIdentifier]).Value -cne $sid.Value -or
            $rules.Count -ne 1 -or
            $rules[0].IdentityReference.Value -cne $sid.Value -or
            $rules[0].AccessControlType -ne [Security.AccessControl.AccessControlType]::Allow) {
            throw 'temp_acl_invalid'
        }
        return $path
    }
    catch {
        if ($created -and [IO.Directory]::Exists($path) -and [IO.Directory]::GetFileSystemEntries($path).Length -eq 0) {
            [IO.Directory]::Delete($path, $false)
        }
        throw
    }
}

function Write-RestrictedBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes
    )

    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.File]::Exists($Path) -or [IO.Directory]::Exists($Path)) {
        throw 'temp_file_invalid'
    }
    [IO.File]::WriteAllBytes($Path, $Bytes)
    $item = [IO.FileInfo]::new($Path)
    $readBack = [IO.File]::ReadAllBytes($Path)
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $readBack.Length -ne $Bytes.Length) {
        throw 'temp_file_invalid'
    }
    for ($index = 0; $index -lt $Bytes.Length; $index++) {
        if ($readBack[$index] -ne $Bytes[$index]) { throw 'temp_file_invalid' }
    }
}

function Remove-RestrictedTempDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)

    # 只删除本次运行创建的随机叶目录中的三个固定文件，拒绝递归删除和额外目标。
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.Path]::GetFileName($Path) -notmatch '^molin-email-recovery-[a-f0-9]{32}$') {
        throw 'temp_cleanup_path_invalid'
    }
    foreach ($name in @(
        'stdin.bin', 'stdout.txt', 'stderr.txt', 'state-parser.py',
        ('molin-email-unknown-' + ('1' * 32) + '.state'), 'parser.stdin',
        'parser-normal.stdout', 'parser-normal.stderr', 'parser-opt.stdout', 'parser-opt.stderr'
    )) {
        $file = Join-Path $Path $name
        if ([IO.File]::Exists($file)) { [IO.File]::Delete($file) }
    }
    if ([IO.Directory]::Exists($Path)) {
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

function Test-RemoteSummary {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )

    if ($Stderr.Length -ne 0) { return [pscustomobject]@{ Classification = 'remote_stderr_nonempty'; Stage = $null; Published = $null } }
    if ($ExitCode -eq 0 -and [regex]::IsMatch($Stdout, $script:SuccessPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) {
        return [pscustomobject]@{ Classification = 'pass'; Stage = $null; Published = 'true' }
    }
    $failure = [regex]::Match($Stdout, $script:FailurePattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if ($failure.Success) {
        return [pscustomobject]@{ Classification = 'remote_gate_failed'; Stage = $failure.Groups['stage'].Value; Published = $failure.Groups['published'].Value }
    }
    if ($ExitCode -ne 0) { return [pscustomobject]@{ Classification = 'remote_exit_nonzero'; Stage = $null; Published = $null } }
    return [pscustomobject]@{ Classification = 'remote_output_invalid'; Stage = $null; Published = $null }
}

function Write-SafeFailure {
    param(
        [Parameter(Mandatory = $true)][string]$Classification,
        [Parameter(Mandatory = $true)][int]$AttemptCount,
        [Parameter(Mandatory = $true)][int]$CompletedCount,
        [Parameter(Mandatory = $true)][int]$StdoutLength,
        [Parameter(Mandatory = $true)][int]$StderrLength,
        [Parameter(Mandatory = $false)][AllowNull()][string]$Stage,
        [Parameter(Mandatory = $false)][AllowNull()][string]$Published
    )

    $safe = [ordered]@{
        status = 'failed'
        classification = $Classification
        ssh_attempt_count = $AttemptCount
        ssh_completed_count = $CompletedCount
        stdout_length = $StdoutLength
        stderr_length = $StderrLength
        retries = 0
    }
    if (-not [string]::IsNullOrEmpty($Stage)) { $safe.stage = $Stage }
    if ($Published -in @('true', 'false')) { $safe.backup_published = $Published }
    Write-Output ($safe | ConvertTo-Json -Compress)
}

function Invoke-StateParserFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Payload,
        [Parameter(Mandatory = $true)][int]$ExpectedFieldCount,
        [Parameter(Mandatory = $false)][string[]]$AdditionalArguments = @()
    )

    # 直接提取远端实际 Python 段运行，避免用另一份模拟解析器制造假阳性。
    $match = [regex]::Match(
        $Payload,
        '(?ms)^state_values=\$\(/usr/bin/python3 - [^\r\n]+ <<''PY''\r?\n(?<source>.*?)^PY\r?\n\)',
        [Text.RegularExpressions.RegexOptions]::CultureInvariant
    )
    if (-not $match.Success) { throw 'selftest_state_parser_source_missing' }
    if ($match.Groups['source'].Value.Contains('operation_nonce')) { throw 'selftest_artifact_fixture_nonce_coupled' }

    $artifactNonce = '1' * 32
    $fixtureNonce = '2' * 32
    $temp = $null
    try {
        $temp = New-RestrictedTempDirectory
        $parserPath = Join-Path $temp 'state-parser.py'
        $statePath = Join-Path $temp ("molin-email-unknown-$artifactNonce.state")
        $stdinPath = Join-Path $temp 'parser.stdin'
        $encoding = New-Object Text.UTF8Encoding($false, $true)
        $fixture = [ordered]@{
            version = 1
            phase = 'phase1_created'
            nonce = $fixtureNonce
            redis_run_id = ('3' * 40)
            operator_id = 7
            template_id = 11
            allowlist_id = 13
            send_log_id = 17
            unexpected_send_log_id = 19
        } | ConvertTo-Json -Compress
        Write-RestrictedBytes -Path $parserPath -Bytes $encoding.GetBytes($match.Groups['source'].Value)
        Write-RestrictedBytes -Path $statePath -Bytes $encoding.GetBytes($fixture)
        Write-RestrictedBytes -Path $stdinPath -Bytes ([byte[]]@())

        $expectedNameHex = ([BitConverter]::ToString($encoding.GetBytes('Phase4 Redis 重启隔离模板'))).Replace('-', '').ToLowerInvariant()
        $expectedSubjectHex = ([BitConverter]::ToString($encoding.GetBytes('Phase4 隔离验证'))).Replace('-', '').ToLowerInvariant()
        $expectedTextBytes = $encoding.GetBytes('<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>')
        $expectedTextHex = ([BitConverter]::ToString($expectedTextBytes)).Replace('-', '').ToLowerInvariant()
        $sha256 = [Security.Cryptography.SHA256]::Create()
        try { $expectedTextSHA = ([BitConverter]::ToString($sha256.ComputeHash($expectedTextBytes))).Replace('-', '').ToLowerInvariant() }
        finally { $sha256.Dispose() }

        $pythonCommand = @(Get-Command python.exe -CommandType Application -ErrorAction Stop)[0]
        $python = [string]$pythonCommand.Source
        $outputs = @()
        foreach ($mode in @('normal', 'opt')) {
            $stdoutPath = Join-Path $temp ("parser-$mode.stdout")
            $stderrPath = Join-Path $temp ("parser-$mode.stderr")
            Write-RestrictedBytes -Path $stdoutPath -Bytes ([byte[]]@())
            Write-RestrictedBytes -Path $stderrPath -Bytes ([byte[]]@())
            $arguments = @('-B')
            if ($mode -ceq 'opt') { $arguments += '-O' }
            $arguments += @(('"' + $parserPath + '"'), ('"' + $statePath + '"'))
            foreach ($argument in $AdditionalArguments) { $arguments += $argument }
            $process = Start-FixedRedirectedProcess -FilePath $python -ArgumentList $arguments -InputPath $stdinPath -OutputPath $stdoutPath -ErrorPath $stderrPath -TimeoutMilliseconds 10000
            $stdout = [IO.File]::ReadAllText($stdoutPath, $encoding)
            $stderr = [IO.File]::ReadAllText($stderrPath, $encoding)
            $fields = @($stdout.TrimEnd([char[]]@("`r", "`n")) -split "`t")
            if ($process.ExitCode -ne 0 -or $stderr.Length -ne 0 -or $fields.Count -ne $ExpectedFieldCount -or
                $stdout.Contains($artifactNonce) -or $stdout.Contains($fixtureNonce) -or
                $fields[11] -cne $expectedNameHex -or $fields[12] -cne $expectedSubjectHex -or
                $fields[14] -cne $expectedTextHex -or $fields[15] -cne $expectedTextSHA) {
                throw 'selftest_state_parser_fixture_failed'
            }
            $outputs += $stdout
        }
        if ($outputs[0] -cne $outputs[1]) { throw 'selftest_state_parser_opt_diff' }
    }
    finally {
        if ($null -ne $temp) { Remove-RestrictedTempDirectory -Path $temp }
    }
}

if ($SelfTest) {
    # SelfTest 不解析确认词、不发现 ssh.exe、不创建外部连接，只验证本地静态安全契约和输出解析。
    $cases = 0
    $payload = Read-VerifiedPayload
    $bytes = ConvertTo-Utf8PayloadBytes -Payload $payload
    if ($bytes[0] -ne 0x73 -or $bytes[1] -ne 0x65 -or $bytes[2] -ne 0x74) { throw 'selftest_ascii_prefix' }
    $cases++

    $requiredDump = '/usr/bin/mysqldump --no-defaults --single-transaction --quick --skip-lock-tables --routines --triggers --events --hex-blob --set-gtid-purged=OFF --no-tablespaces molin'
    if ([regex]::Matches($payload, [regex]::Escape($requiredDump)).Count -ne 1) { throw 'selftest_dump_contract' }
    $cases++
    foreach ($required in @(
        'mapfile -t state_candidates',
        '[[ "${BASH_REMATCH[1]}" == "$operation_nonce" ]]',
        'renameat2(-100, os.fsencode(source), -100, os.fsencode(target), 1)',
        'os.fsync(fd)',
        'database_after=$(database_snapshot)',
        'cycle_after=$(cycle_snapshot)',
        "JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes'))",
        'cleanup=false database_writes=false restarts=false retries=0'
    )) {
        if (-not $payload.Contains($required)) { throw 'selftest_required_contract' }
        $cases++
    }
    # 模板变量按 JSON 数组语义校验，避免相同集合因序列化顺序不同被误判；稳定摘要仍保留原始 JSON 文本。
    if ($payload.Contains("variables_json='[\`"Code\`",\`"ExpireMinutes\`"]'") -or
        -not $payload.Contains('HEX(template_text),variables_json,content_sha256')) {
        throw 'selftest_template_variables_semantic_contract'
    }
    $cases++
    foreach ($forbiddenPattern in @(
        '(?mi)\brm\s+-[^\r\n]*r',
        '(?mi)\bdocker\s+(?:compose\s+)?(?:restart|stop|kill|rm)\b',
        '(?mi)\bredis-cli\b',
        '(?mi)\b(?:DELETE|UPDATE|INSERT|REPLACE|ALTER|DROP|TRUNCATE|CREATE|RENAME|GRANT|REVOKE)\s+(?:FROM|INTO|TABLE|DATABASE|USER)\b',
        '(?mi)printf[^\r\n]*(?:operation_nonce|recovery_file|state_file|temp_file|MYSQL_ROOT_PASSWORD)'
    )) {
        if ([regex]::IsMatch($payload, $forbiddenPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) {
            throw 'selftest_forbidden_command'
        }
        $cases++
    }
    foreach ($utf8Literal in @(
        'Phase4 Redis 重启隔离模板',
        'Phase4 隔离验证',
        '<p>验证码：${Code}，有效期 ${ExpireMinutes} 分钟。</p>'
    )) {
        # 三个业务常量只能出现在 Python UTF-8 段一次，SQL 必须只使用 hex 参数比较。
        if ([regex]::Matches($payload, [regex]::Escape($utf8Literal)).Count -ne 1) { throw 'selftest_utf8_literal_contract' }
        $cases++
    }
    foreach ($mojibakeMarker in @([string][char]0xFFFD, 'ï¿½', 'éªŒ', 'é‡å¯', 'éš”ç¦»')) {
        if ($payload.Contains($mojibakeMarker)) { throw 'selftest_mojibake_detected' }
    }
    $cases++

    $nonce = '1' * 32
    Assert-OperationArtifactNames -StatePath "/home/pc/molin-email-unknown-$nonce.state" -RecoveryPath "/home/pc/molin/rollback/molin-email-unknown-$nonce.sql"
    $cases++
    foreach ($attack in @(
        @{ State = "/home/pc/molin-email-unknown-$nonce.state"; Recovery = "/home/pc/molin/rollback/molin-email-unknown-$('2' * 32).sql" },
        @{ State = '/home/pc/molin-email-unknown-../../bad.state'; Recovery = "/home/pc/molin/rollback/molin-email-unknown-$nonce.sql" },
        @{ State = "/home/pc/molin-email-unknown-$nonce.state"; Recovery = "/home/pc/molin/rollback/../molin-email-unknown-$nonce.sql" }
    )) {
        $rejected = $false
        try { Assert-OperationArtifactNames -StatePath $attack.State -RecoveryPath $attack.Recovery } catch { $rejected = $_.Exception.Message -ceq 'operation_nonce_mismatch' }
        if (-not $rejected) { throw 'selftest_artifact_attack_accepted' }
        $cases++
    }

    Invoke-StateParserFixture -Payload $payload -ExpectedFieldCount 16
    $cases += 2

    $validSummary = "status=pass schema=57 dirty=false fixture_logs=2 fixture_allowlist=1 fixture_template=1 snapshot_stable=true backup_published=true backup_mode=600 backup_sha256_valid=true cycle_evidence_retained=2 cleanup=false database_writes=false restarts=false retries=0`n"
    if ((Test-RemoteSummary -Stdout $validSummary -Stderr '' -ExitCode 0).Classification -cne 'pass') { throw 'selftest_valid_summary' }
    $cases++
    $failureSummary = "status=failed stage=temp_verify backup_published=false`n"
    $failureResult = Test-RemoteSummary -Stdout $failureSummary -Stderr '' -ExitCode 2
    if ($failureResult.Classification -cne 'remote_gate_failed' -or $failureResult.Stage -cne 'temp_verify' -or $failureResult.Published -cne 'false') {
        throw 'selftest_failure_summary'
    }
    $cases++
    foreach ($invalid in @(
        @{ Out = $validSummary + "extra=true`n"; Err = ''; Code = 0 },
        @{ Out = ($validSummary -replace ' cycle_evidence_retained=2', ''); Err = ''; Code = 0 },
        @{ Out = $validSummary; Err = 'raw'; Code = 0 },
        @{ Out = ''; Err = ''; Code = 255 }
    )) {
        if ((Test-RemoteSummary -Stdout $invalid.Out -Stderr $invalid.Err -ExitCode $invalid.Code).Classification -ceq 'pass') {
            throw 'selftest_invalid_summary'
        }
        $cases++
    }

    # 使用真实 Windows PowerShell 5.1 子进程验证 0 与非零退出码都能原样穿透，防止 null 再次被转换成 0。
    $processExitTemp = $null
    try {
        $processExitTemp = New-RestrictedTempDirectory
        $processExitInput = Join-Path $processExitTemp 'stdin.bin'
        $processExitOutput = Join-Path $processExitTemp 'stdout.txt'
        $processExitError = Join-Path $processExitTemp 'stderr.txt'
        $windowsPowerShellExe = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
        Write-RestrictedBytes -Path $processExitInput -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path $processExitOutput -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path $processExitError -Bytes ([byte[]]@())
        foreach ($expectedExitCode in @(0, 7)) {
            $processResult = Start-FixedRedirectedProcess -FilePath $windowsPowerShellExe -ArgumentList @('-NoProfile', '-NonInteractive', '-Command', ('exit ' + $expectedExitCode)) -InputPath $processExitInput -OutputPath $processExitOutput -ErrorPath $processExitError -TimeoutMilliseconds 10000
            if ($processResult.ExitCode -ne $expectedExitCode) { throw 'selftest_process_exit_code_mismatch' }
            $cases++
        }
    }
    finally { if ($null -ne $processExitTemp) { Remove-RestrictedTempDirectory -Path $processExitTemp } }

    Write-Output "status=pass mode=selftest cases=$cases external_access=false ssh_attempt_count=0 payload_bom=false dump_contract=true atomic_noreplace=true dangerous_scan=true state_parser_fixture=true artifact_fixture_nonce_independent=true python_normal=true python_opt=true output_verified=true process_exit_codes=0,7"
    exit 0
}

$attemptCount = 0
$completedCount = 0
$stdoutLength = 0
$stderrLength = 0
$runTemp = $null
try {
    # 精确确认词在查找 ssh.exe 和创建进程前核验；缺失时保证零连接、零远端写入。
    if ($Confirm -cne $script:RequiredPhrase) { throw 'confirmation_required' }
    $payload = Read-VerifiedPayload
    $payloadBytes = ConvertTo-Utf8PayloadBytes -Payload $payload
    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($sshExe)) { throw 'ssh_tool_missing' }

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
        # 远端十分钟上限覆盖逻辑备份；本地再加三十秒运输余量，且全程不自动重试。
        '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '600s',
        '/bin/bash', '--noprofile', '--norc', '-s', '--'
    )
    $attemptCount = 1
    $process = Start-FixedRedirectedProcess -FilePath $sshExe -ArgumentList $sshArguments -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 630000
    $completedCount = 1
    $stdout = [IO.File]::ReadAllText($outputPath, (New-Object Text.UTF8Encoding($false, $true)))
    $stderr = [IO.File]::ReadAllText($errorPath, (New-Object Text.UTF8Encoding($false, $true)))
    $stdoutLength = $stdout.Length
    $stderrLength = $stderr.Length
    $result = Test-RemoteSummary -Stdout $stdout -Stderr $stderr -ExitCode $process.ExitCode
    if ($result.Classification -cne 'pass') {
        Write-SafeFailure -Classification $result.Classification -Stage $result.Stage -Published $result.Published -AttemptCount $attemptCount -CompletedCount $completedCount -StdoutLength $stdoutLength -StderrLength $stderrLength
        exit 2
    }
    Write-Output $stdout.TrimEnd([char[]]@("`r", "`n"))
    exit 0
}
catch {
    $classification = 'local_gate_failed'
    if ($_.Exception.Message -in @('confirmation_required', 'payload_path_invalid', 'payload_encoding_invalid', 'ssh_tool_missing', 'temp_path_invalid', 'temp_acl_invalid', 'temp_file_invalid', 'process_timeout')) {
        $classification = $_.Exception.Message
    }
    Write-SafeFailure -Classification $classification -AttemptCount $attemptCount -CompletedCount $completedCount -StdoutLength $stdoutLength -StderrLength $stderrLength
    exit 2
}
finally {
    if ($null -ne $runTemp) { Remove-RestrictedTempDirectory -Path $runTemp }
}
