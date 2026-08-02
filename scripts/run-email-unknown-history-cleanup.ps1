[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm,

    [Parameter(Mandatory = $false)]
    [string]$ExpectedCleanupBinarySHA256,

    [Parameter(Mandatory = $false)]
    [switch]$SelfTest
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RequiredPhrase = 'I_CONFIRM_EXACT_EMAIL_UNKNOWN_HISTORY_CLEANUP_ONCE'
$script:SuccessPattern = '^status=pass preflight_schema=57 preflight_dirty=false state_phase=phase1_created fixture_logs=2 fixture_allowlist=1 fixture_template=1 redis_key_preexisting=false cleanup_binary_launches=1 cleanup_db_logs=2 cleanup_allowlist=1 cleanup_template=1 redis_key_untouched=true state_removed=true backup_retained=true cycle_assets_retained=2 retries=0\r?\n?\z'
$script:FailurePattern = '^status=failed stage=(?<stage>shell_options|api_identity|api_environment|health_transport|health|ready_transport|ready|required_environment|container_identity|schema_query|schema_gate|state_gate|recovery_gate|cycle_metadata|state_parse|fixture_ownership|redis_exists|binary_gate|cleanup_execute|postflight)\r?\n?\z'
$script:BinarySHAPlaceholder = '__MOLIN_EXPECTED_CLEANUP_BINARY_SHA256__'

function ConvertTo-Utf8PayloadBytes {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 统一换行为 LF，并显式使用无 BOM UTF-8，避免远端首行严格模式被 BOM 破坏。
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
    # payload 必须是 scripts 目录中的固定普通文件，拒绝符号链接、联接和目录逃逸。
    $scriptsRoot = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $expected = [IO.Path]::GetFullPath((Join-Path $scriptsRoot 'email-unknown-history-cleanup.payload.sh'))
    $item = [IO.FileInfo]::new($expected)
    if (-not $item.Exists -or
        ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $item.DirectoryName -cne $scriptsRoot -or
        $item.FullName -cne $expected) {
        throw 'payload_path_invalid'
    }
    return [IO.File]::ReadAllText($expected, (New-Object Text.UTF8Encoding($false, $true)))
}

function Assert-FrozenBinarySHA {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Expected,
        [Parameter(Mandatory = $false)][AllowEmptyString()][string]$Actual
    )

    # 只接受小写 64 位十六进制且拒绝全零；参数无法携带 shell 元字符或空白。
    if ($Expected -cnotmatch '\A[a-f0-9]{64}\z' -or $Expected -cmatch '\A0{64}\z') {
        throw 'binary_sha_invalid'
    }
    if ($PSBoundParameters.ContainsKey('Actual')) {
        if ($Actual -cnotmatch '\A[a-f0-9]{64}\z' -or -not [string]::Equals($Expected, $Actual, [StringComparison]::Ordinal)) {
            throw 'binary_sha_mismatch'
        }
    }
}

function Set-FrozenBinarySHA {
    param(
        [Parameter(Mandatory = $true)][string]$Payload,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Expected
    )

    Assert-FrozenBinarySHA -Expected $Expected
    if ([regex]::Matches($Payload, [regex]::Escape($script:BinarySHAPlaceholder)).Count -ne 1) {
        throw 'payload_sha_placeholder_invalid'
    }
    $resolved = $Payload.Replace($script:BinarySHAPlaceholder, $Expected)
    if ($resolved.Contains($script:BinarySHAPlaceholder) -or
        [regex]::Matches($resolved, '(?m)^expected_binary_sha=[a-f0-9]{64}$').Count -ne 1) {
        throw 'payload_sha_placeholder_invalid'
    }
    return $resolved
}

function Assert-OperationArtifactNames {
    param(
        [Parameter(Mandatory = $true)][string]$StatePath,
        [Parameter(Mandatory = $true)][string]$RecoveryPath
    )

    # 离线攻击测试复刻远端文件名契约：两个精确绝对路径必须携带同一 operation nonce。
    $stateMatch = [regex]::Match($StatePath, '\A/home/pc/molin-email-unknown-(?<nonce>[a-f0-9]{32})\.state\z', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $recoveryMatch = [regex]::Match($RecoveryPath, '\A/home/pc/molin/rollback/molin-email-unknown-(?<nonce>[a-f0-9]{32})\.sql\z', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $stateMatch.Success -or -not $recoveryMatch.Success -or
        -not [string]::Equals($stateMatch.Groups['nonce'].Value, $recoveryMatch.Groups['nonce'].Value, [StringComparison]::Ordinal)) {
        throw 'operation_nonce_mismatch'
    }
}

function New-RestrictedTempDirectory {
    # 随机目录关闭 ACL 继承，只授权当前 Windows 身份，防止临时 stdin 被其他本机账号读取或替换。
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $path = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-cleanup-' + [Guid]::NewGuid().ToString('N'))))
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
        $item = [IO.DirectoryInfo]::new($path)
        $verifiedAcl = [IO.Directory]::GetAccessControl($path)
        $rules = @($verifiedAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
            $item.FullName -cne $path -or
            -not $verifiedAcl.AreAccessRulesProtected -or
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

    # 只删除本运行器创建的随机叶目录内四个固定文件，拒绝递归删除和任何额外目标。
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.Path]::GetFileName($Path) -notmatch '^molin-email-cleanup-[a-f0-9]{32}$') {
        throw 'temp_cleanup_path_invalid'
    }
    foreach ($name in @(
        'stdin.bin', 'stdout.txt', 'stderr.txt', 'probe.ps1', 'state-parser.py',
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
    if ($Stderr.Length -ne 0) { return [pscustomobject]@{ Classification = 'remote_stderr_nonempty'; Stage = $null } }
    if ($ExitCode -eq 0 -and [Text.RegularExpressions.Regex]::IsMatch($Stdout, $script:SuccessPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) {
        return [pscustomobject]@{ Classification = 'pass'; Stage = $null }
    }
    $failure = [Text.RegularExpressions.Regex]::Match($Stdout, $script:FailurePattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if ($failure.Success) { return [pscustomobject]@{ Classification = 'remote_gate_failed'; Stage = $failure.Groups['stage'].Value } }
    if ($ExitCode -ne 0) { return [pscustomobject]@{ Classification = 'remote_exit_nonzero'; Stage = $null } }
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
        retries = 0
        mutation_state = $(if ($AttemptCount -eq 0) { 'not_started' } else { 'unknown' })
    }
    if ($Classification -ceq 'remote_gate_failed' -and -not [string]::IsNullOrEmpty($Stage)) { $safe.stage = $Stage }
    Write-Output ($safe | ConvertTo-Json -Compress)
}

function Invoke-StateParserFixture {
    param(
        [Parameter(Mandatory = $true)][string]$Payload,
        [Parameter(Mandatory = $true)][int]$ExpectedFieldCount,
        [Parameter(Mandatory = $false)][string[]]$AdditionalArguments = @()
    )

    # 直接执行 payload 中的真实 Python 解析段，覆盖 artifact nonce 与 fixture nonce 独立的历史状态形态。
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
    # SelfTest 只启动本机固定探针，不解析确认词、不调用 ssh.exe，保证外部连接数为零。
    $cases = 0
    $payload = Read-VerifiedPayload
    if ([regex]::Matches($payload, [regex]::Escape($script:BinarySHAPlaceholder)).Count -ne 1 -or
        $payload.Contains('recovery_candidates=') -or
        $payload.Contains('recovery_files=') -or
        -not $payload.Contains('recovery_file="/home/pc/molin/rollback/molin-email-unknown-${operation_nonce}.sql"') -or
        -not $payload.Contains('[[ "$recovery_nonce" == "$operation_nonce" ]]') -or
        $payload.Contains('nonce != operation_nonce') -or
        -not $payload.Contains('if not isinstance(nonce, str) or not re.fullmatch(r"[a-f0-9]{32}", nonce):')) {
        throw 'selftest_static_binding_contract'
    }
    $cases++
    # 完成标记是存在性标记；本地契约夹具显式证明大小不参与判定，其余安全属性仍全部收紧。
    $markerContract = {
        param(
            [bool]$IsRegularFile,
            [bool]$IsSymbolicLink,
            [int]$OwnerId,
            [int]$Mode,
            [long]$Size
        )
        [void]$Size
        return $IsRegularFile -and -not $IsSymbolicLink -and $OwnerId -eq 0 -and $Mode -eq 600
    }
    $markerFixtures = @(
        @{ Name = 'zero_byte'; Regular = $true; Symlink = $false; Owner = 0; Mode = 600; Size = 0; Accepted = $true },
        @{ Name = 'wrong_type'; Regular = $false; Symlink = $false; Owner = 0; Mode = 600; Size = 0; Accepted = $false },
        @{ Name = 'symlink'; Regular = $true; Symlink = $true; Owner = 0; Mode = 600; Size = 0; Accepted = $false },
        @{ Name = 'wrong_owner'; Regular = $true; Symlink = $false; Owner = 1; Mode = 600; Size = 0; Accepted = $false },
        @{ Name = 'wrong_mode'; Regular = $true; Symlink = $false; Owner = 0; Mode = 644; Size = 0; Accepted = $false }
    )
    foreach ($fixture in $markerFixtures) {
        $accepted = & $markerContract $fixture.Regular $fixture.Symlink $fixture.Owner $fixture.Mode $fixture.Size
        if ($accepted -ne $fixture.Accepted) { throw ('selftest_cycle_marker_fixture_' + $fixture.Name) }
        $cases++
    }
    foreach ($requiredMarkerContract in @(
        '/usr/bin/find "$cycle_marker" -mindepth 0 -maxdepth 0 -type l -print',
        '/usr/bin/find "$cycle_marker" -mindepth 0 -maxdepth 0 -type f -print',
        "/usr/bin/stat -c '%u:%a' -- `"`$cycle_marker`"",
        '[[ "$current_cycle_marker_metadata" == 0:600 ]]'
    )) {
        if (-not $payload.Contains($requiredMarkerContract)) { throw 'selftest_cycle_marker_contract_missing' }
        $cases++
    }
    if ($payload.Contains("stat -c '%u:%a:%s' -- `"`$cycle_marker`"") -or
        $payload.Contains("stat -c '%u:%a:%s' -- `"`${cycle_markers[`$index]}`"") -or
        $payload.Contains('current_cycle_marker_metadata" =~ ^0:600:[1-9][0-9]*$')) {
        throw 'selftest_cycle_marker_size_reintroduced'
    }
    $cases++
    # 后置核验必须沿用相同存在性标记契约，不能在清理提交后重新引入 marker 大小比较。
    foreach ($requiredPostflightMarkerContract in @(
        '/usr/bin/find "${cycle_markers[$index]}" -mindepth 0 -maxdepth 0 -type l -print',
        '/usr/bin/find "${cycle_markers[$index]}" -mindepth 0 -maxdepth 0 -type f -print',
        "/usr/bin/stat -c '%u:%a' -- `"`${cycle_markers[`$index]}`"",
        '== "${cycle_marker_metadata[$index]}"'
    )) {
        if (-not $payload.Contains($requiredPostflightMarkerContract)) { throw 'selftest_postflight_cycle_marker_contract_missing' }
        $cases++
    }
    # dump 仍必须是 root:600 非空普通文件，并继续校验 SHA 与对应隔离 schema。
    foreach ($requiredCycleEvidence in @(
        '[[ "$current_cycle_dump_metadata" =~ ^0:600:[1-9][0-9]*$ ]]',
        '[[ "$cycle_dump_sha" =~ ^[a-f0-9]{64}$ ]]',
        '[[ "$(cycle_schema_exists "$cycle_target")" == 1 ]]'
    )) {
        if (-not $payload.Contains($requiredCycleEvidence)) { throw 'selftest_cycle_evidence_contract_missing' }
        $cases++
    }
    # 清理前所有权判定必须使用 JSON 语义集合，不能依赖 variables_json 的字面顺序。
    $semanticVariablesPredicate = "JSON_LENGTH(variables_json)=2 AND JSON_CONTAINS(variables_json, JSON_QUOTE('Code')) AND JSON_CONTAINS(variables_json, JSON_QUOTE('ExpireMinutes'))"
    if (-not $payload.Contains($semanticVariablesPredicate) -or
        $payload.Contains("variables_json='[\`"Code\`",\`"ExpireMinutes\`"]'")) {
        throw 'selftest_template_variables_semantic_contract'
    }
    $cases++
    $expectedSHA = 'b' * 64
    $resolvedPayload = Set-FrozenBinarySHA -Payload $payload -Expected $expectedSHA
    if ($resolvedPayload.Contains($script:BinarySHAPlaceholder) -or -not $resolvedPayload.Contains("expected_binary_sha=$expectedSHA")) {
        throw 'selftest_sha_injection'
    }
    [void](ConvertTo-Utf8PayloadBytes -Payload $resolvedPayload)
    Assert-FrozenBinarySHA -Expected $expectedSHA -Actual $expectedSHA
    $cases++
    $oldSHARejected = $false
    try { Assert-FrozenBinarySHA -Expected ('a' * 64) -Actual $expectedSHA } catch { $oldSHARejected = $_.Exception.Message -ceq 'binary_sha_mismatch' }
    if (-not $oldSHARejected) { throw 'selftest_old_sha_accepted' }
    $cases++
    foreach ($maliciousSHA in @('', ('0' * 64), ('A' * 64), (('c' * 63) + ';'), (('d' * 64) + ' touch'))) {
        $rejected = $false
        try { [void](Set-FrozenBinarySHA -Payload $payload -Expected $maliciousSHA) } catch { $rejected = $_.Exception.Message -ceq 'binary_sha_invalid' }
        if (-not $rejected) { throw 'selftest_malicious_sha_accepted' }
        $cases++
    }
    $nonce = '1' * 32
    Assert-OperationArtifactNames -StatePath "/home/pc/molin-email-unknown-$nonce.state" -RecoveryPath "/home/pc/molin/rollback/molin-email-unknown-$nonce.sql"
    $cases++
    foreach ($artifactAttack in @(
        @{ State = "/home/pc/molin-email-unknown-$nonce.state"; Recovery = "/home/pc/molin/rollback/molin-email-unknown-$('2' * 32).sql" },
        @{ State = '/home/pc/molin-email-unknown-../../unsafe.state'; Recovery = "/home/pc/molin/rollback/molin-email-unknown-$nonce.sql" },
        @{ State = "/home/pc/molin-email-unknown-$nonce.state"; Recovery = "/home/pc/molin/rollback/../molin-email-unknown-$nonce.sql" }
    )) {
        $rejected = $false
        try { Assert-OperationArtifactNames -StatePath $artifactAttack.State -RecoveryPath $artifactAttack.Recovery } catch { $rejected = $_.Exception.Message -ceq 'operation_nonce_mismatch' }
        if (-not $rejected) { throw 'selftest_artifact_attack_accepted' }
        $cases++
    }

    Invoke-StateParserFixture -Payload $payload -ExpectedFieldCount 17 -AdditionalArguments @(
        'molin_restore_57_reverify_' + ('4' * 32),
        'molin_restore_57_reverify_' + ('5' * 32)
    )
    $cases += 2
    $bytes = ConvertTo-Utf8PayloadBytes -Payload $payload
    if ($bytes[0] -ne 0x73 -or $bytes[1] -ne 0x65 -or $bytes[2] -ne 0x74) { throw 'selftest_ascii_prefix' }
    $cases++
    $crlfBytes = ConvertTo-Utf8PayloadBytes -Payload ($payload.Replace("`n", "`r`n"))
    if ($crlfBytes[0] -ne 0x73 -or $crlfBytes[1] -ne 0x65 -or $crlfBytes[2] -ne 0x74) { throw 'selftest_crlf_encoding' }
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
    if ([regex]::Matches($payload, [regex]::Escape('cleanup_output=$("$cleanup_binary" -test.run')).Count -ne 1 -or
        [regex]::Matches($payload, '^export RUN_EMAIL_UNKNOWN_RESTART_CLEANUP=1$', [Text.RegularExpressions.RegexOptions]::Multiline).Count -ne 1 -or
        [regex]::Matches($payload, '^export EMAIL_UNKNOWN_RESTART_CLEANUP_ACK=', [Text.RegularExpressions.RegexOptions]::Multiline).Count -ne 1) {
        throw 'selftest_unique_cleanup_launch'
    }
    $cases++
    foreach ($forbidden in @('KEYS ', 'SCAN ', 'FLUSHDB', 'FLUSHALL', 'redis-cli --raw DEL', 'rm -rf', 'find / -')) {
        if ($payload.Contains($forbidden)) { throw 'selftest_forbidden_command' }
    }
    if ([regex]::IsMatch($payload, '(?mi)redis-cli[^\r\n]*\sDEL(?:\s|$)')) { throw 'selftest_redis_del' }
    $cases++
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
    $validSummary = "status=pass preflight_schema=57 preflight_dirty=false state_phase=phase1_created fixture_logs=2 fixture_allowlist=1 fixture_template=1 redis_key_preexisting=false cleanup_binary_launches=1 cleanup_db_logs=2 cleanup_allowlist=1 cleanup_template=1 redis_key_untouched=true state_removed=true backup_retained=true cycle_assets_retained=2 retries=0`n"
    if ((Test-RemoteSummary -Stdout $validSummary -Stderr '' -ExitCode 0).Classification -cne 'pass') { throw 'selftest_valid_summary' }
    $cases++
    foreach ($invalid in @(
        @{ Out = $validSummary + "extra=true`n"; Err = ''; Code = 0 },
        @{ Out = ($validSummary -replace ' cycle_assets_retained=2', ''); Err = ''; Code = 0 },
        @{ Out = $validSummary; Err = 'raw'; Code = 0 },
        @{ Out = ''; Err = ''; Code = 255 }
    )) {
        if ((Test-RemoteSummary -Stdout $invalid.Out -Stderr $invalid.Err -ExitCode $invalid.Code).Classification -ceq 'pass') { throw 'selftest_invalid_summary' }
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
        $probeSource = '[Console]::Out.Write("status=failed stage=shell_options`n"); [Environment]::Exit(2)'
        Write-RestrictedBytes -Path $probePath -Bytes ((New-Object Text.UTF8Encoding($false, $true)).GetBytes($probeSource))
        $powershellExe = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
        $probe = Start-FixedRedirectedProcess -FilePath $powershellExe -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', ('"' + $probePath + '"')) -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 10000
        $probeOut = [IO.File]::ReadAllText($outputPath, (New-Object Text.UTF8Encoding($false, $true)))
        $probeErr = [IO.File]::ReadAllText($errorPath, (New-Object Text.UTF8Encoding($false, $true)))
        $probeClassification = (Test-RemoteSummary -Stdout $probeOut -Stderr $probeErr -ExitCode $probe.ExitCode).Classification
        if ($probeErr.Length -ne 0 -or $probeClassification -cne 'remote_gate_failed') {
            throw ("selftest_redirected_output exit={0} stdout_length={1} stderr_length={2} classification={3}" -f $probe.ExitCode, $probeOut.Length, $probeErr.Length, $probeClassification)
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

    $timeoutTemp = $null
    try {
        $timeoutTemp = New-RestrictedTempDirectory
        $timeoutInput = Join-Path $timeoutTemp 'stdin.bin'
        $timeoutOutput = Join-Path $timeoutTemp 'stdout.txt'
        $timeoutError = Join-Path $timeoutTemp 'stderr.txt'
        $timeoutProbe = Join-Path $timeoutTemp 'probe.ps1'
        Write-RestrictedBytes -Path $timeoutInput -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path $timeoutOutput -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path $timeoutError -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path $timeoutProbe -Bytes ((New-Object Text.UTF8Encoding($false, $true)).GetBytes('Start-Sleep -Seconds 5'))
        $timedOut = $false
        try {
            [void](Start-FixedRedirectedProcess -FilePath $powershellExe -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', ('"' + $timeoutProbe + '"')) -InputPath $timeoutInput -OutputPath $timeoutOutput -ErrorPath $timeoutError -TimeoutMilliseconds 100)
        }
        catch {
            if ($_.Exception.Message -ceq 'process_timeout') { $timedOut = $true } else { throw }
        }
        if (-not $timedOut) { throw 'selftest_timeout_missing' }
        $cases++
    }
    finally {
        if ($null -ne $timeoutTemp) { Remove-RestrictedTempDirectory -Path $timeoutTemp }
    }
    Write-Output "status=pass mode=selftest cases=$cases external_access=false ssh_attempt_count=0 payload_bom=false acl=restricted cleanup_binary_launches=0 timeout_verified=true state_parser_fixture=true artifact_fixture_nonce_independent=true python_normal=true python_opt=true output_verified=true process_exit_codes=0,7"
    exit 0
}

$attemptCount = 0
$completedCount = 0
$stdoutLength = 0
$stderrLength = 0
$runTemp = $null
try {
    # 确认词在任何 ssh.exe 路径解析或进程创建前核验；缺失时保证零连接、零写入。
    if ($Confirm -cne $script:RequiredPhrase) { throw 'confirmation_required' }
    $payloadTemplate = Read-VerifiedPayload
    $payload = Set-FrozenBinarySHA -Payload $payloadTemplate -Expected $ExpectedCleanupBinarySHA256
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
        # 远端 timeout 管住 bash 及其测试子进程，客户端 120 秒超时作为第二层运输兜底。
        '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '105s',
        '/bin/bash', '--noprofile', '--norc', '-s', '--'
    )
    $attemptCount = 1
    $process = Start-FixedRedirectedProcess -FilePath $sshExe -ArgumentList $sshArguments -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 120000
    $completedCount = 1
    $stdout = [IO.File]::ReadAllText($outputPath, (New-Object Text.UTF8Encoding($false, $true)))
    $stderr = [IO.File]::ReadAllText($errorPath, (New-Object Text.UTF8Encoding($false, $true)))
    $stdoutLength = $stdout.Length
    $stderrLength = $stderr.Length
    $result = Test-RemoteSummary -Stdout $stdout -Stderr $stderr -ExitCode $process.ExitCode
    if ($result.Classification -cne 'pass') {
        Write-SafeFailure -Classification $result.Classification -Stage $result.Stage -AttemptCount $attemptCount -CompletedCount $completedCount -StdoutLength $stdoutLength -StderrLength $stderrLength
        exit 2
    }
    Write-Output $stdout.TrimEnd([char[]]@("`r", "`n"))
    exit 0
}
catch {
    $classification = 'local_gate_failed'
    if ($_.Exception.Message -in @('confirmation_required', 'payload_path_invalid', 'payload_encoding_invalid', 'binary_sha_invalid', 'payload_sha_placeholder_invalid', 'ssh_tool_missing', 'temp_path_invalid', 'temp_acl_invalid', 'temp_file_invalid', 'process_timeout')) {
        $classification = $_.Exception.Message
    }
    Write-SafeFailure -Classification $classification -AttemptCount $attemptCount -CompletedCount $completedCount -StdoutLength $stdoutLength -StderrLength $stderrLength
    exit 2
}
finally {
    if ($null -ne $runTemp) { Remove-RestrictedTempDirectory -Path $runTemp }
}
