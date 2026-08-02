[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm,

    [Parameter(Mandatory = $false)]
    [switch]$SelfTest
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RequiredPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_RECOVERY_PREFLIGHT_DIAGNOSTIC_ONCE'
$script:CountFields = @(
    'version', 'dirty', 'migration_rows',
    'primary_count', 'primary_id_match', 'primary_provider_match', 'primary_template_match', 'primary_target_match', 'primary_idempotency_match', 'primary_status_match',
    'unexpected_count', 'unexpected_id_match', 'unexpected_provider_match', 'unexpected_template_match', 'unexpected_target_match', 'unexpected_idempotency_match', 'unexpected_status_match',
    'scope_count', 'allowlist_count', 'allowlist_id_match', 'allowlist_recipient_match', 'allowlist_state_match', 'allowlist_ownership_match', 'allowlist_lifecycle_match',
    'template_count', 'template_id_match', 'template_provider_match', 'template_name_match', 'template_subject_match', 'template_text_match', 'semantic_variables_match', 'template_content_sha_match', 'template_status_match', 'template_flags_match', 'template_version_match'
)
$script:FixedFields = [ordered]@{
    digest_valid = 'true'
    writes = 'false'
    backup = 'false'
    cleanup = 'false'
    restarts = 'false'
    ssh_attempts = '1'
    retries = '0'
}
$script:FailurePattern = '^status=failed stage=(?<stage>shell_options|state_gate|container_gate|state_parse|database_snapshot|snapshot_parse) writes=false backup=false cleanup=false restarts=false retries=0\r?\n?\z'

function ConvertTo-Utf8PayloadBytes {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 远端 bash 固定接收无 BOM UTF-8 和 LF，拒绝空字节及异常文件头。
    $normalized = $Payload.Replace("`r`n", "`n").Replace("`r", "`n")
    if ($normalized.Length -eq 0 -or [int][char]$normalized[0] -in @(0xFEFF, 0xFFFE) -or
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
    # 只读取 scripts 目录直属的固定普通文件，拒绝重解析点和目录逃逸。
    $scriptsRoot = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $expected = [IO.Path]::GetFullPath((Join-Path $scriptsRoot 'email-unknown-recovery-preflight-diagnostic.payload.sh'))
    $item = [IO.FileInfo]::new($expected)
    if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $item.DirectoryName -cne $scriptsRoot -or $item.FullName -cne $expected) {
        throw 'payload_path_invalid'
    }
    return [IO.File]::ReadAllText($expected, (New-Object Text.UTF8Encoding($false, $true)))
}

function Get-SafeStderrCategory {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr)

    # 分类只检查锚定形态，不返回原文；超长、空字节、多余前后缀一律归为 unknown。
    if ($Stderr.Length -eq 0) { return $null }
    if ($Stderr.Length -gt 4096 -or $Stderr.IndexOf([char]0) -ge 0) { return 'unknown' }
    $normalized = $Stderr.Replace("`r`n", "`n").Replace("`r", "`n")
    $options = [Text.RegularExpressions.RegexOptions]::CultureInvariant
    $patterns = [ordered]@{
        transport_closed = '\A(?:Connection to [A-Za-z0-9.:-]+ closed(?: by remote host)?\.|client_loop: send disconnect: Broken pipe|kex_exchange_identification: Connection closed by remote host|ssh: connect to host [A-Za-z0-9.:-]+ port [0-9]+: (?:Connection timed out|Connection refused|No route to host))\n?\z'
        mysql_error = '\A(?:ERROR [0-9]{4} \([0-9A-Z]{5}\)(?: at line [0-9]+)?: [^\n]{1,1024}|mysql: \[(?:ERROR|Warning)\] [^\n]{1,1024})\n?\z'
        docker_error = '\A(?:Error response from daemon: [^\n]{1,1024}|docker: [^\n]{1,1024})\n?\z'
        bash_error = '\A(?:(?:/bin/)?bash): (?:line [0-9]+: )?[^\n]{1,1024}\n?\z'
        python_error = '\A(?:python3: [^\n]{1,1024}|Traceback \(most recent call last\):\n(?:[^\n]*\n){1,20}[A-Za-z_][A-Za-z0-9_.]*(?:: [^\n]{0,512})?)\n?\z'
    }
    foreach ($entry in $patterns.GetEnumerator()) {
        if ([regex]::IsMatch($normalized, $entry.Value, $options)) { return [string]$entry.Key }
    }
    return 'unknown'
}

function Test-RemoteSummary {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )

    $stderrCategory = Get-SafeStderrCategory -Stderr $Stderr
    $failure = [regex]::Match($Stdout, $script:FailurePattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    # 固定失败摘要优先于 stderr 分类，确保真实 stage 不会被运输层附加文本遮蔽。
    if ($failure.Success) { return [pscustomobject]@{ Classification = 'remote_gate_failed'; Stage = $failure.Groups['stage'].Value; StderrCategory = $stderrCategory } }
    if ($Stderr.Length -ne 0) { return [pscustomobject]@{ Classification = 'remote_stderr_nonempty'; Stage = $null; StderrCategory = $stderrCategory } }
    if ($ExitCode -ne 0) { return [pscustomobject]@{ Classification = 'remote_exit_nonzero'; Stage = $null; StderrCategory = $stderrCategory } }

    $trimmed = $Stdout.TrimEnd([char[]]@("`r", "`n"))
    if ($trimmed.Contains("`r") -or $trimmed.Contains("`n")) {
        return [pscustomobject]@{ Classification = 'remote_output_invalid'; Stage = $null; StderrCategory = $stderrCategory }
    }
    $tokens = @($trimmed -split ' ')
    $expectedCount = 1 + $script:CountFields.Count + $script:FixedFields.Count
    if ($tokens.Count -ne $expectedCount -or $tokens[0] -cne 'status=pass') {
        return [pscustomobject]@{ Classification = 'remote_output_invalid'; Stage = $null; StderrCategory = $stderrCategory }
    }
    $index = 1
    foreach ($field in $script:CountFields) {
        $match = [regex]::Match($tokens[$index], ('\A' + [regex]::Escape($field) + '=(?<value>[0-9]+)\z'), [Text.RegularExpressions.RegexOptions]::CultureInvariant)
        if (-not $match.Success) { return [pscustomobject]@{ Classification = 'remote_output_invalid'; Stage = $null; StderrCategory = $stderrCategory } }
        $index++
    }
    foreach ($entry in $script:FixedFields.GetEnumerator()) {
        if ($tokens[$index] -cne ($entry.Key + '=' + $entry.Value)) {
            return [pscustomobject]@{ Classification = 'remote_output_invalid'; Stage = $null; StderrCategory = $stderrCategory }
        }
        $index++
    }
    return [pscustomobject]@{ Classification = 'pass'; Stage = $null; StderrCategory = $stderrCategory }
}

function Write-SafeFailure {
    param(
        [Parameter(Mandatory = $true)][string]$Classification,
        [Parameter(Mandatory = $true)][int]$AttemptCount,
        [Parameter(Mandatory = $true)][int]$StdoutLength,
        [Parameter(Mandatory = $true)][int]$StderrLength,
        [Parameter(Mandatory = $false)][AllowNull()][string]$Stage,
        [Parameter(Mandatory = $false)][AllowNull()][string]$StderrCategory
    )

    $safe = [ordered]@{
        status = 'failed'
        classification = $Classification
        ssh_attempt_count = $AttemptCount
        stdout_length = $StdoutLength
        stderr_length = $StderrLength
        retries = 0
        writes = $false
        backup = $false
        cleanup = $false
        restarts = $false
    }
    if (-not [string]::IsNullOrEmpty($Stage)) { $safe.stage = $Stage }
    if (-not [string]::IsNullOrEmpty($StderrCategory)) { $safe.stderr_category = $StderrCategory }
    Write-Output ($safe | ConvertTo-Json -Compress)
}

function New-RestrictedTempDirectory {
    # 与正式只读门禁一致：随机叶目录关闭继承，只允许当前 Windows 身份完全控制。
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

if ($SelfTest) {
    # SelfTest 只检查本地 payload 和固定摘要解析，不发现或启动 ssh.exe。
    $cases = 0
    $payload = Read-VerifiedPayload
    $bytes = ConvertTo-Utf8PayloadBytes -Payload $payload
    if ($bytes[0] -ne 0x73 -or $bytes[1] -ne 0x65 -or $bytes[2] -ne 0x74) { throw 'selftest_ascii_prefix' }
    $cases++

    foreach ($required in @(
        'stage=database_snapshot',
        'sql="SELECT CONCAT_WS(CHAR(9),',
        'digest_valid=true',
        'writes=false backup=false cleanup=false restarts=false ssh_attempts=1 retries=0',
        'name=CONVERT(0x${template_name_hex} USING utf8mb4)',
        'subject=CONVERT(0x${template_subject_hex} USING utf8mb4)',
        'template_text=CONVERT(0x${template_text_hex} USING utf8mb4)',
        "JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes'))",
        'semantic_variables_match=%s'
    )) {
        if (-not $payload.Contains($required)) { throw 'selftest_required_contract' }
        $cases++
    }
    foreach ($forbidden in @(
        'mysqldump', 'redis-cli', 'curl ', 'rm -', 'unlink ',
        'DELETE ', 'UPDATE ', 'INSERT ', 'REPLACE ', 'ALTER ', 'DROP ', 'TRUNCATE ',
        'docker restart', 'docker stop', 'docker kill', 'docker rm'
    )) {
        if ($payload.Contains($forbidden)) { throw 'selftest_forbidden_command' }
    }
    if ([regex]::Matches($payload, '--execute="\$sql"').Count -ne 1 -or
        [regex]::Matches($payload, '(?m)^sql="SELECT ').Count -ne 1 -or
        [regex]::IsMatch($payload, '(?mi)printf[^\r\n]*(?:nonce|recipient_hmac|template_text_hash|state_file|MYSQL_ROOT_PASSWORD|digest)(?:=|%s)')) {
        throw 'selftest_readonly_or_output_contract'
    }
    $cases++
    # 诊断字段明确表达 JSON 语义匹配，并拒绝旧的字面字符串比较与旧字段名。
    if ($payload.Contains("variables_json='[\`"Code\`",\`"ExpireMinutes\`"]'") -or
        $payload.Contains('template_variables_match') -or
        -not $payload.Contains('HEX(template_text),variables_json,content_sha256')) {
        throw 'selftest_template_variables_semantic_contract'
    }
    $cases++

    $parts = New-Object Collections.Generic.List[string]
    $parts.Add('status=pass')
    foreach ($field in $script:CountFields) {
        $value = if ($field -ceq 'version') { '57' } elseif ($field -ceq 'dirty') { '0' } elseif ($field -ceq 'scope_count') { '2' } else { '1' }
        $parts.Add($field + '=' + $value)
    }
    foreach ($entry in $script:FixedFields.GetEnumerator()) { $parts.Add($entry.Key + '=' + $entry.Value) }
    $valid = ([string]::Join(' ', $parts)) + "`n"
    if ((Test-RemoteSummary -Stdout $valid -Stderr '' -ExitCode 0).Classification -cne 'pass') { throw 'selftest_valid_summary' }
    $cases++
    foreach ($invalid in @(
        @{ Out = $valid + "extra=true`n"; Err = ''; Code = 0 },
        @{ Out = ($valid -replace ' semantic_variables_match=1', ''); Err = ''; Code = 0 },
        @{ Out = ($valid -replace ' digest_valid=true', ' digest_valid=false'); Err = ''; Code = 0 },
        @{ Out = $valid; Err = 'raw'; Code = 0 },
        @{ Out = ''; Err = ''; Code = 255 }
    )) {
        if ((Test-RemoteSummary -Stdout $invalid.Out -Stderr $invalid.Err -ExitCode $invalid.Code).Classification -ceq 'pass') { throw 'selftest_invalid_summary' }
        $cases++
    }
    $failure = 'status=failed stage=database_snapshot writes=false backup=false cleanup=false restarts=false retries=0' + "`n"
    $failureResult = Test-RemoteSummary -Stdout $failure -Stderr '' -ExitCode 2
    if ($failureResult.Classification -cne 'remote_gate_failed' -or $failureResult.Stage -cne 'database_snapshot' -or $null -ne $failureResult.StderrCategory) { throw 'selftest_failure_summary' }
    $cases++

    $stderrCases = @(
        @{ Text = "Connection to 8.130.9.163 closed.`n"; Category = 'transport_closed' },
        @{ Text = "ERROR 1045 (28000): access denied`n"; Category = 'mysql_error' },
        @{ Text = "Error response from daemon: container unavailable`n"; Category = 'docker_error' },
        @{ Text = "/bin/bash: line 42: command failed`n"; Category = 'bash_error' },
        @{ Text = "python3: parser failed`n"; Category = 'python_error' }
    )
    foreach ($stderrCase in $stderrCases) {
        if ((Get-SafeStderrCategory -Stderr $stderrCase.Text) -cne $stderrCase.Category) { throw 'selftest_stderr_category' }
        $classifiedFailure = Test-RemoteSummary -Stdout $failure -Stderr $stderrCase.Text -ExitCode 2
        if ($classifiedFailure.Classification -cne 'remote_gate_failed' -or
            $classifiedFailure.Stage -cne 'database_snapshot' -or
            $classifiedFailure.StderrCategory -cne $stderrCase.Category) {
            throw 'selftest_failure_stage_with_stderr'
        }
        # 即使 stdout 是合法成功摘要，任何非空 stderr 也必须失败关闭。
        if ((Test-RemoteSummary -Stdout $valid -Stderr $stderrCase.Text -ExitCode 0).Classification -ceq 'pass') { throw 'selftest_stderr_accepted_as_pass' }
        $cases++
    }
    $knownLength = $stderrCases[0].Text.Length
    $sameLengthUnknown = 'X' * $knownLength
    if ($sameLengthUnknown.Length -ne $knownLength -or (Get-SafeStderrCategory -Stderr $sameLengthUnknown) -cne 'unknown') {
        throw 'selftest_same_length_unknown'
    }
    $cases++
    foreach ($malicious in @(
        'prefix Connection to 8.130.9.163 closed.',
        "Connection to 8.130.9.163 closed.`ntrailing",
        "token=fake Error response from daemon: hidden",
        (('A' * 4097)),
        ("python3: failed`0hidden")
    )) {
        if ((Get-SafeStderrCategory -Stderr $malicious) -cne 'unknown') { throw 'selftest_malicious_stderr_classified' }
        $unknownFailure = Test-RemoteSummary -Stdout $failure -Stderr $malicious -ExitCode 2
        if ($unknownFailure.Classification -cne 'remote_gate_failed' -or
            $unknownFailure.Stage -cne 'database_snapshot' -or
            $unknownFailure.StderrCategory -cne 'unknown') {
            throw 'selftest_unknown_stderr_not_closed'
        }
        $cases++
    }

    $selfSource = [IO.File]::ReadAllText($PSCommandPath, (New-Object Text.UTF8Encoding($true, $true)))
    foreach ($legacyTransport in @(
        ('.Standard' + 'Input'), ('.Base' + 'Stream'), ('RedirectStandardInput = ' + '$true'),
        ('RedirectStandardOutput = ' + '$true'), ('RedirectStandardError = ' + '$true'),
        ('New-Object Diagnostics.Process' + 'StartInfo'), ('.Sta' + 'rt()')
    )) {
        if ($selfSource.Contains($legacyTransport)) { throw 'selftest_legacy_transport_present' }
    }
    if ([regex]::Matches($selfSource, 'Microsoft\.PowerShell\.Management\\Start-Process').Count -ne 1 -or
        [regex]::Matches($selfSource, '-RedirectStandardInput \$InputPath').Count -ne 1 -or
        [regex]::Matches($selfSource, '-RedirectStandardOutput \$OutputPath').Count -ne 1 -or
        [regex]::Matches($selfSource, '-RedirectStandardError \$ErrorPath').Count -ne 1) {
        throw 'selftest_redirected_process_contract'
    }
    $cases++

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
[Console]::Out.Write('first_byte=115 bom=false')
exit 0
'@
        Write-RestrictedBytes -Path $probePath -Bytes ((New-Object Text.UTF8Encoding($false, $true)).GetBytes($probeSource))
        $powershellExe = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
        if (-not [IO.File]::Exists($powershellExe)) { throw 'selftest_powershell_missing' }
        $probe = Start-FixedRedirectedProcess -FilePath $powershellExe `
            -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', ('"' + $probePath + '"')) `
            -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 10000
        $probeOut = [IO.File]::ReadAllText($outputPath, (New-Object Text.UTF8Encoding($false, $true)))
        $probeErr = [IO.File]::ReadAllText($errorPath, (New-Object Text.UTF8Encoding($false, $true)))
        $inputBytes = [IO.File]::ReadAllBytes($inputPath)
        if ($probe.ExitCode -ne 0 -or $probeOut -cne 'first_byte=115 bom=false' -or $probeErr.Length -ne 0 -or
            $inputBytes.Length -lt 3 -or $inputBytes[0] -ne 115 -or
            ($inputBytes[0] -eq 239 -and $inputBytes[1] -eq 187 -and $inputBytes[2] -eq 191)) {
            throw ("selftest_redirected_stdin exit={0} stdout_length={1} stderr_length={2} first_byte={3}" -f $probe.ExitCode, $probeOut.Length, $probeErr.Length, $(if ($inputBytes.Length -gt 0) { $inputBytes[0] } else { -1 }))
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

    Write-Output "status=pass mode=selftest cases=$cases external_access=false ssh_attempt_count=0 payload_bom=false select_only=true safe_summary=true output_verified=true process_exit_codes=0,7"
    exit 0
}

$attemptCount = 0
$stdoutLength = 0
$stderrLength = 0
$runTemp = $null
try {
    # 确认词在解析 ssh.exe 前核验；正常模式固定一次连接且没有重试分支。
    if ($Confirm -cne $script:RequiredPhrase) { throw 'confirmation_required' }
    $payloadBytes = ConvertTo-Utf8PayloadBytes -Payload (Read-VerifiedPayload)
    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($sshExe)) { throw 'ssh_tool_missing' }

    # 与正式只读门禁一致：stdin 使用受限无 BOM 文件句柄重定向，避免 PS5.1 StreamWriter 改写首字节。
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
        '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '120s',
        '/bin/bash', '--noprofile', '--norc', '-s', '--'
    )
    $attemptCount = 1
    $process = Start-FixedRedirectedProcess -FilePath $sshExe -ArgumentList $sshArguments `
        -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 150000
    $stdout = [IO.File]::ReadAllText($outputPath, (New-Object Text.UTF8Encoding($false, $true)))
    $stderr = [IO.File]::ReadAllText($errorPath, (New-Object Text.UTF8Encoding($false, $true)))
    $stdoutLength = $stdout.Length
    $stderrLength = $stderr.Length
    $result = Test-RemoteSummary -Stdout $stdout -Stderr $stderr -ExitCode $process.ExitCode
    if ($result.Classification -cne 'pass') {
        Write-SafeFailure -Classification $result.Classification -Stage $result.Stage -StderrCategory $result.StderrCategory -AttemptCount $attemptCount -StdoutLength $stdoutLength -StderrLength $stderrLength
        exit 2
    }
    Write-Output $stdout.TrimEnd([char[]]@("`r", "`n"))
    exit 0
}
catch {
    $classification = 'local_gate_failed'
    if ($_.Exception.Message -in @('confirmation_required', 'payload_path_invalid', 'payload_encoding_invalid', 'ssh_tool_missing', 'process_timeout', 'temp_path_invalid', 'temp_path_unsafe', 'temp_file_invalid', 'temp_file_mismatch', 'temp_file_unsafe', 'temp_cleanup_path_invalid', 'temp_cleanup_not_empty')) {
        $classification = $_.Exception.Message
    }
    Write-SafeFailure -Classification $classification -AttemptCount $attemptCount -StdoutLength $stdoutLength -StderrLength $stderrLength
    exit 2
}
finally {
    if ($null -ne $runTemp) { Remove-RestrictedTempDirectory -Path $runTemp }
}
