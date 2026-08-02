[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm,

    [Parameter(Mandatory = $false)]
    [string]$RecoveryFileName,

    [Parameter(Mandatory = $false)]
    [string]$ExpectedCleanupBinarySHA256,

    [Parameter(Mandatory = $false)]
    [string]$ExpectedRecoverySHA256,

    [Parameter(Mandatory = $false)]
    [string]$ExpectedCycleDumpSHA256One,

    [Parameter(Mandatory = $false)]
    [string]$ExpectedCycleDumpSHA256Two,

    [Parameter(Mandatory = $false)]
    [switch]$SelfTest
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RecoveryPlaceholder = '__MOLIN_RECOVERY_FILENAME__'
$script:BinarySHAPlaceholder = '__MOLIN_EXPECTED_CLEANUP_BINARY_SHA256__'
$script:RecoverySHAPlaceholder = '__MOLIN_EXPECTED_RECOVERY_SHA256__'
$script:CycleDumpSHAOnePlaceholder = '__MOLIN_EXPECTED_CYCLE_DUMP_SHA256_ONE__'
$script:CycleDumpSHATwoPlaceholder = '__MOLIN_EXPECTED_CYCLE_DUMP_SHA256_TWO__'
$script:SuccessPattern = '^status=pass api_health=true api_ready=true schema=57 dirty=false fixture_logs_absent=2 scope_rows=0 allowlist_absent=1 template_absent=1 redis_ping=true redis_key_absent=true recovery_mode=600 recovery_sha256_valid=true cleanup_binary_sha256_valid=true cycle_evidence_count=2 cycle_schema_count=2 state_dependency=false writes=false restarts=false retries=0\r?\n?\z'
$script:FailurePattern = '^status=failed stage=(?<stage>shell_options|api_identity|api_environment|health_transport|health_json|ready_transport|ready_json|required_environment|container_identity|recovery_gate|recovery_identity|identity_json|schema_query|schema_gate|fixture_query|fixture_absence|redis_ping|redis_exists|binary_gate|cycle_metadata|cycle_schema|final_artifacts)\r?\n?\z'

function ConvertTo-Utf8PayloadBytes {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # stdin 必须是无 BOM UTF-8 且首行固定，避免远端 Bash 收到隐藏前缀或 NUL。
    $normalizedPayload = $Payload.Replace("`r`n", "`n").Replace("`r", "`n")
    if ($normalizedPayload.Length -eq 0 -or
        [int][char]$normalizedPayload[0] -eq 0xFEFF -or
        [int][char]$normalizedPayload[0] -eq 0xFFFE -or
        $normalizedPayload.IndexOf([char]0) -ge 0) {
        throw 'payload_bom_or_nul'
    }
    if (-not $normalizedPayload.StartsWith("set -Eeuo pipefail`n", [StringComparison]::Ordinal)) { throw 'payload_first_line' }
    $bytes = (New-Object Text.UTF8Encoding($false, $true)).GetBytes($normalizedPayload)
    if ($bytes.Length -lt 4 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)) { throw 'payload_encoding' }
    return ,$bytes
}

function Read-VerifiedPayload {
    $expected = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'email-unknown-history-postcheck.payload.sh'))
    if (-not [IO.File]::Exists($expected)) { throw 'payload_missing' }
    $item = [IO.FileInfo]::new($expected)
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.FullName -cne $expected) { throw 'payload_path_invalid' }
    return [IO.File]::ReadAllText($expected, (New-Object Text.UTF8Encoding($false, $true)))
}

function Set-FrozenPostcheckInputs {
    param(
        [Parameter(Mandatory = $true)][string]$Payload,
        [Parameter(Mandatory = $true)][string]$RecoveryName,
        [Parameter(Mandatory = $true)][string]$BinarySHA,
        [Parameter(Mandatory = $true)][string]$RecoverySHA,
        [Parameter(Mandatory = $true)][string[]]$CycleDumpSHA
    )

    if ($RecoveryName -cnotmatch '\Amolin-email-unknown-[a-f0-9]{32}\.sql\z') { throw 'recovery_filename_invalid' }
    if ($BinarySHA -cnotmatch '\A[a-f0-9]{64}\z' -or $BinarySHA -ceq ('0' * 64)) { throw 'binary_sha_invalid' }
    if ($RecoverySHA -cnotmatch '\A[a-f0-9]{64}\z' -or $RecoverySHA -ceq ('0' * 64)) { throw 'recovery_sha_invalid' }
    if ($CycleDumpSHA.Count -ne 2 -or $CycleDumpSHA[0] -cnotmatch '\A[a-f0-9]{64}\z' -or $CycleDumpSHA[1] -cnotmatch '\A[a-f0-9]{64}\z' -or
        $CycleDumpSHA[0] -ceq ('0' * 64) -or $CycleDumpSHA[1] -ceq ('0' * 64) -or $CycleDumpSHA[0] -ceq $CycleDumpSHA[1]) {
        throw 'cycle_dump_sha_invalid'
    }
    if ([regex]::Matches($Payload, [regex]::Escape($script:RecoveryPlaceholder)).Count -ne 1 -or
        [regex]::Matches($Payload, [regex]::Escape($script:BinarySHAPlaceholder)).Count -ne 1 -or
        [regex]::Matches($Payload, [regex]::Escape($script:RecoverySHAPlaceholder)).Count -ne 1 -or
        [regex]::Matches($Payload, [regex]::Escape($script:CycleDumpSHAOnePlaceholder)).Count -ne 1 -or
        [regex]::Matches($Payload, [regex]::Escape($script:CycleDumpSHATwoPlaceholder)).Count -ne 1) {
        throw 'payload_placeholder_invalid'
    }
    $resolved = $Payload.Replace($script:RecoveryPlaceholder, $RecoveryName).Replace($script:BinarySHAPlaceholder, $BinarySHA).Replace($script:RecoverySHAPlaceholder, $RecoverySHA).Replace($script:CycleDumpSHAOnePlaceholder, $CycleDumpSHA[0]).Replace($script:CycleDumpSHATwoPlaceholder, $CycleDumpSHA[1])
    if ($resolved.Contains('__MOLIN_')) { throw 'payload_placeholder_invalid' }
    return $resolved
}

function Test-RemoteSummary {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )
    if ($Stderr.Length -ne 0) { return [pscustomobject]@{ Classification = 'remote_stderr_nonempty'; Stage = $null } }
    if ($ExitCode -eq 0 -and [regex]::IsMatch($Stdout, $script:SuccessPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) {
        return [pscustomobject]@{ Classification = 'pass'; Stage = $null }
    }
    $failure = [regex]::Match($Stdout, $script:FailurePattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
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
        status = 'failed'; classification = $Classification; ssh_attempt_count = $AttemptCount
        ssh_completed_count = $CompletedCount; stdout_length = $StdoutLength; stderr_length = $StderrLength
        writes = $false; restart = $false; cleanup = $false; retries = 0
    }
    if ($Classification -ceq 'remote_gate_failed' -and -not [string]::IsNullOrEmpty($Stage)) { $safe.stage = $Stage }
    Write-Output ($safe | ConvertTo-Json -Compress)
}

function New-RestrictedTempDirectory {
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $path = [IO.Path]::GetFullPath((Join-Path $tempRoot ('molin-email-postcheck-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $path.StartsWith($tempRoot, [StringComparison]::OrdinalIgnoreCase) -or [IO.Directory]::Exists($path) -or [IO.File]::Exists($path)) { throw 'temp_path_invalid' }
    [void][IO.Directory]::CreateDirectory($path)
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
    $security = New-Object Security.AccessControl.DirectorySecurity
    $security.SetOwner($sid)
    $security.SetAccessRuleProtection($true, $false)
    [void]$security.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid, [Security.AccessControl.FileSystemRights]::FullControl, [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit', [Security.AccessControl.PropagationFlags]::None, [Security.AccessControl.AccessControlType]::Allow)))
    [IO.Directory]::SetAccessControl($path, $security)
    if (([IO.DirectoryInfo]::new($path).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'temp_path_unsafe' }
    return $path
}

function Write-RestrictedBytes {
    param([Parameter(Mandatory = $true)][string]$Path, [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes)
    if ([IO.File]::Exists($Path) -or [IO.Directory]::Exists($Path) -or -not [IO.Path]::IsPathRooted($Path)) { throw 'temp_file_invalid' }
    [IO.File]::WriteAllBytes($Path, $Bytes)
    $readBack = [IO.File]::ReadAllBytes($Path)
    if ($readBack.Length -ne $Bytes.Length) { throw 'temp_file_mismatch' }
    for ($index = 0; $index -lt $Bytes.Length; $index++) { if ($readBack[$index] -ne $Bytes[$index]) { throw 'temp_file_mismatch' } }
    if (([IO.FileInfo]::new($Path).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'temp_file_unsafe' }
}

function Remove-RestrictedTempDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.Path]::GetFileName($Path) -notmatch '^molin-email-postcheck-[a-f0-9]{32}$') { throw 'temp_cleanup_path_invalid' }
    if ([IO.Directory]::Exists($Path)) {
        foreach ($file in [IO.Directory]::GetFiles($Path)) {
            $name = [IO.Path]::GetFileName($file)
            if ($name -notin @('stdin.bin', 'stdout.txt', 'stderr.txt', 'api-json.py', 'identity-json.py', 'recovery-identity.py') -and
                $name -notmatch '^molin-email-unknown-[a-f0-9]{32}\.sql$') {
                throw 'temp_cleanup_file_invalid'
            }
            [IO.File]::Delete($file)
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
    $process = Microsoft.PowerShell.Management\Start-Process -FilePath $FilePath -ArgumentList $ArgumentList -RedirectStandardInput $InputPath -RedirectStandardOutput $OutputPath -RedirectStandardError $ErrorPath -NoNewWindow -PassThru
    # Windows PowerShell 5.1 必须在等待前立即取得原生句柄，否则带超时的 WaitForExit 可能无法固化真实退出码。
    try {
        $processHandle = $process.Handle
        if ($processHandle -eq [IntPtr]::Zero) { throw 'process_handle_unavailable' }
    }
    catch {
        try { if (-not $process.HasExited) { $process.Kill(); $process.WaitForExit() } } catch { }
        throw 'process_handle_unavailable'
    }
    if (-not $process.WaitForExit($TimeoutMilliseconds)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
    $process.Refresh()
    try { $exitCode = $process.ExitCode } catch { throw 'process_exit_code_unavailable' }
    # 禁止把 null 强制转换为 0；无法取得退出码时必须失败关闭，不能继续解析远端摘要。
    if ($null -eq $exitCode) { throw 'process_exit_code_unavailable' }
    return [pscustomobject]@{ ExitCode = [int]$exitCode }
}

function Invoke-StrictApiJsonSelfTest {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 直接运行 payload 内的真实解析器，防止 SelfTest 与远端契约产生两份实现。
    $match = [regex]::Match($Payload, '(?ms)^  /usr/bin/python3 - "\$expected" "\$body" <<''STRICT_API_JSON''\r?\n(?<source>.*?)^STRICT_API_JSON$', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success) { throw 'selftest_api_json_source_missing' }
    $temp = $null
    try {
        $temp = New-RestrictedTempDirectory
        $parserPath = Join-Path $temp 'api-json.py'
        $parserSource = "import base64, sys`nsys.argv[2] = base64.b64decode(sys.argv[2]).decode('utf-8')`n" + $match.Groups['source'].Value
        Write-RestrictedBytes -Path $parserPath -Bytes ((New-Object Text.UTF8Encoding($false, $true)).GetBytes($parserSource))
        $python = [string]@(Get-Command python.exe -CommandType Application -ErrorAction Stop)[0].Source
        $valid = '{"code":0,"message":"ok","data":{"status":"ok"}}'
        $validEncoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($valid))
        $savedPreference = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        $validOutput = & $python -B $parserPath ok $validEncoded 2>$null
        $ErrorActionPreference = $savedPreference
        if ($LASTEXITCODE -ne 0 -or ($validOutput | Out-String).Trim() -cne 'true') { throw 'selftest_api_json_valid_rejected' }
        foreach ($invalid in @(
            '{"code":0,"code":0,"message":"ok","data":{"status":"ok"}}',
            '{"code":0,"message":"ok","data":{"status":"ok"},"extra":true}',
            '{"code":0,"message":"ok","data":{"status":"ok"}}{"tail":true}',
            '{"code":0,"message":"ok","data":{"status":"ok","extra":true}}'
        )) {
            $invalidEncoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($invalid))
            $ErrorActionPreference = 'Continue'
            [void](& $python -B $parserPath ok $invalidEncoded 2>$null)
            $ErrorActionPreference = $savedPreference
            if ($LASTEXITCODE -eq 0) { throw 'selftest_api_json_attack_accepted' }
        }
    }
    finally { if ($null -ne $temp) { Remove-RestrictedTempDirectory -Path $temp } }
}

function Invoke-StrictIdentityJsonSelfTest {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 身份 JSON 使用 payload 内同一解析器，覆盖重复键、未知键和尾随 JSON 三类攻击。
    $match = [regex]::Match($Payload, '(?ms)^identity_values=\$\(/usr/bin/python3 - "\$identity_json" <<''STRICT_IDENTITY_JSON''\r?\n(?<source>.*?)^STRICT_IDENTITY_JSON\r?\n\)', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success) { throw 'selftest_identity_json_source_missing' }
    $temp = $null
    try {
        $temp = New-RestrictedTempDirectory
        $parserPath = Join-Path $temp 'identity-json.py'
        $parserSource = "import base64, sys`nsys.argv[1] = base64.b64decode(sys.argv[1]).decode('utf-8')`n" + $match.Groups['source'].Value
        Write-RestrictedBytes -Path $parserPath -Bytes ((New-Object Text.UTF8Encoding($false, $true)).GetBytes($parserSource))
        $python = [string]@(Get-Command python.exe -CommandType Application -ErrorAction Stop)[0].Source
        $valid = '{"operator_id":7,"template_id":11,"allowlist_id":13,"primary_id":17,"unexpected_id":19,"recipient_hmac":"' + ('a' * 64) + '","scope_hex":"6162","provider_template_hex":"6364","lock_key":"lock:email:dispatch:' + ('b' * 64) + '"}'
        $savedPreference = $ErrorActionPreference
        foreach ($fixture in @(
            @{ Json = $valid; Accepted = $true },
            @{ Json = $valid.Replace('"operator_id":7', '"operator_id":7,"operator_id":7'); Accepted = $false },
            @{ Json = $valid.TrimEnd('}') + ',"extra":true}'; Accepted = $false },
            @{ Json = $valid + '{"tail":true}'; Accepted = $false },
            @{ Json = $valid.Replace('"primary_id":17', '"primary_id":19'); Accepted = $false }
        )) {
            $encoded = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($fixture.Json))
            $ErrorActionPreference = 'Continue'
            [void](& $python -B $parserPath $encoded 2>$null)
            $exitCode = $LASTEXITCODE
            $ErrorActionPreference = $savedPreference
            if (($exitCode -eq 0) -ne $fixture.Accepted) { throw 'selftest_identity_json_fixture_failed' }
        }
    }
    finally { if ($null -ne $temp) { Remove-RestrictedTempDirectory -Path $temp } }
}

function Invoke-RecoveryIdentitySelfTest {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 接近真实结构的完整 dump 夹具直接运行远端解析器，并让 artifact nonce 与 fixture nonce 保持独立。
    $match = [regex]::Match($Payload, '(?ms)^identity_json=\$\(/usr/bin/python3 - "\$recovery_file" <<''RECOVERY_IDENTITY''\r?\n(?<source>.*?)^RECOVERY_IDENTITY\r?\n\)', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success) { throw 'selftest_recovery_parser_source_missing' }
    $artifactNonce = '1' * 32
    $fixtureNonce = '2' * 32
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    $addressHmac = New-Object Security.Cryptography.HMACSHA256 -ArgumentList (,$encoding.GetBytes('qa-phase4-address-secret-32-bytes-only'))
    try {
        $email = "phase4-$fixtureNonce@example.invalid"
        $recipientHmac = ([BitConverter]::ToString($addressHmac.ComputeHash($encoding.GetBytes($email)))).Replace('-', '').ToLowerInvariant()
        $scope = "admin-email-template-test:admin:7:template:11:scene:register:recipient:$recipientHmac"
        $oldHash = ([BitConverter]::ToString($sha256.ComputeHash($encoding.GetBytes("phase4-old-$fixtureNonce")))).Replace('-', '').ToLowerInvariant()
        $newHash = ([BitConverter]::ToString($sha256.ComputeHash($encoding.GetBytes("phase4-new-$fixtureNonce")))).Replace('-', '').ToLowerInvariant()
        $providerTemplate = "qa-phase4-$fixtureNonce"
        $fingerprint = 'c' * 64
        $logOne = "(17,'request-one',NULL,11,'$providerTemplate','register','test','$recipientHmac','ph***@example.invalid','$scope','$oldHash','$fingerprint','aliyun_directmail',NULL,'failed','provider_outcome_unknown',NULL,'2026-01-01 00:00:00','2026-01-01 00:00:00')"
        $logTwo = "(19,'request-two',NULL,11,'$providerTemplate','register','test','$recipientHmac','ph***@example.invalid','$scope','$newHash','$fingerprint','aliyun_directmail',NULL,'failed','provider_outcome_unknown',NULL,'2026-01-01 00:00:00','2026-01-01 00:00:00')"
        $allowlist = "(13,'$recipientHmac','ph***@example.invalid','active',1,7,7,'2026-01-01 00:00:00','2026-01-01 00:00:00',NULL)"
        $template = "(11,'aliyun_directmail','$providerTemplate','name','subject',NULL,'text','vars','d','approved',NULL,1,1,0,NULL,NULL,'2026-01-01 00:00:00',1,'2026-01-01 00:00:00','2026-01-01 00:00:00')"
        $cards = @'
-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)
--
-- Host: 127.0.0.1    Database: molin
-- Table structure for table `email_send_logs`
--
CREATE TABLE `email_send_logs` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `business_request_no` varchar(64) NOT NULL,
  `verification_code_id` bigint unsigned DEFAULT NULL,
  `template_id` bigint unsigned NOT NULL,
  `provider_template_id` varchar(64) NOT NULL,
  `scene` varchar(32) NOT NULL,
  `purpose` varchar(16) NOT NULL,
  `recipient_hmac` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  `recipient_masked` varchar(191) NOT NULL,
  `idempotency_scope` varchar(191) NOT NULL,
  `idempotency_key_hash` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  `request_fingerprint` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  `provider` varchar(32) NOT NULL,
  `provider_request_id` varchar(128) DEFAULT NULL,
  `status` varchar(16) NOT NULL,
  `failure_reason` varchar(64) DEFAULT NULL,
  `expires_at` datetime DEFAULT NULL,
  `submitted_at` datetime NOT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=101 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
-- Table structure for table `email_test_recipient_allowlist`
CREATE TABLE `email_test_recipient_allowlist` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `email_hmac` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  `email_masked` varchar(191) NOT NULL,
  `status` varchar(16) NOT NULL,
  `version` bigint unsigned NOT NULL DEFAULT 1,
  `created_by` bigint unsigned NOT NULL,
  `updated_by` bigint unsigned NOT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `revoked_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=202 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
-- Table structure for table `email_provider_templates`
CREATE TABLE `email_provider_templates` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `provider` varchar(32) NOT NULL,
  `provider_template_id` varchar(64) NOT NULL,
  `name` varchar(64) NOT NULL,
  `subject` varchar(256) NOT NULL,
  `sender_nickname` varchar(64) DEFAULT NULL,
  `template_text` mediumtext NOT NULL,
  `variables_json` json NOT NULL,
  `content_sha256` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  `provider_status` varchar(16) NOT NULL,
  `review_comment` varchar(512) DEFAULT NULL,
  `variables_complete` tinyint(1) NOT NULL DEFAULT 0,
  `local_enabled` tinyint(1) NOT NULL DEFAULT 0,
  `missing` tinyint(1) NOT NULL DEFAULT 0,
  `missing_since` datetime DEFAULT NULL,
  `provider_created_at` datetime DEFAULT NULL,
  `last_synced_at` datetime NOT NULL,
  `version` bigint unsigned NOT NULL DEFAULT 1,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB AUTO_INCREMENT=303 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
-- Table structure for table `schema_migrations`
CREATE TABLE `schema_migrations` (
  `version` bigint NOT NULL,
  `dirty` tinyint(1) NOT NULL,
  PRIMARY KEY (`version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
'@
        $cards = $cards.Replace("`r`n", "`n").Replace("`r", "`n")
        # 普通注释、优化器提示、字符串和反引号内容中的伪 INSERT 必须跳过，版本 SET 则按可执行 SQL 解析并允许。
        $datedTrailer = "-- Dump completed on 2026-01-01 00:00:00`n"
        $undatedTrailer = "-- Dump completed`n"
        $variableMinTrailer = "-- Dump completed on 2026-1-1  0:0:0`n"
        $variableMaxTrailer = "-- Dump completed on 2026-12-31        23:59:59`n"
        $dump = "$cards-- INSERT/**/INTO ``email_send_logs`` VALUES (1);`n/* INSERT INTO ``email_send_logs`` VALUES (2); */;`n/*+ INSERT INTO ``email_send_logs`` VALUES (4) */;`nSELECT 'INSERT INTO ``email_send_logs`` VALUES (3)';`nSELECT ``INSERT INTO email_send_logs VALUES``;`n/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;`n/*!501000 SET NAMES utf8mb4 */;`nINSERT INTO ``email_send_logs`` VALUES $logOne,$logTwo;`nINSERT INTO ``email_test_recipient_allowlist`` VALUES $allowlist;`nINSERT INTO ``email_provider_templates`` VALUES $template;`nINSERT INTO ``schema_migrations`` VALUES (57,0);`n$datedTrailer"
        $minimalDump = "INSERT INTO ``email_send_logs`` VALUES $logOne,$logTwo;`nINSERT INTO ``email_test_recipient_allowlist`` VALUES $allowlist;`nINSERT INTO ``email_provider_templates`` VALUES $template;`n"
        $temp = $null
        try {
            $temp = New-RestrictedTempDirectory
            $parserPath = Join-Path $temp 'recovery-identity.py'
            $parserSource = $match.Groups['source'].Value
            Write-RestrictedBytes $parserPath $encoding.GetBytes($parserSource)
            $python = [string]@(Get-Command python.exe -CommandType Application -ErrorAction Stop)[0].Source
            $outputs = @()
            $savedPreference = $ErrorActionPreference
            # 默认带日期与 --skip-dump-date 无日期两种结束行都属于 mysqldump 契约，最终换行可存在或省略。
            $acceptedDumps = @(
                $dump,
                $dump.TrimEnd("`n"),
                $dump.Replace($datedTrailer, $undatedTrailer),
                $dump.Replace($datedTrailer, $undatedTrailer).TrimEnd("`n"),
                $dump.Replace($datedTrailer, $variableMinTrailer),
                $dump.Replace($datedTrailer, $variableMinTrailer).TrimEnd("`n"),
                $dump.Replace($datedTrailer, $variableMaxTrailer),
                $dump.Replace($datedTrailer, $variableMaxTrailer).TrimEnd("`n")
            )
            for ($acceptedIndex = 0; $acceptedIndex -lt $acceptedDumps.Count; $acceptedIndex++) {
                $acceptedPath = Join-Path $temp ("molin-email-unknown-" + ('{0:x32}' -f ($acceptedIndex + 1)) + '.sql')
                Write-RestrictedBytes $acceptedPath $encoding.GetBytes($acceptedDumps[$acceptedIndex])
                foreach ($mode in @('normal', 'optimized')) {
                    $arguments = @('-B')
                    if ($mode -ceq 'optimized') { $arguments += '-O' }
                    $ErrorActionPreference = 'Continue'
                    $output = & $python @arguments $parserPath $acceptedPath 2>$null
                    $exitCode = $LASTEXITCODE
                    $ErrorActionPreference = $savedPreference
                    if ($exitCode -ne 0 -or [string]::IsNullOrWhiteSpace(($output | Out-String))) { throw 'selftest_recovery_parser_fixture_failed' }
                    $outputs += ($output | Out-String).Trim()
                }
            }
            if (@($outputs | Sort-Object -Unique).Count -ne 1) { throw 'selftest_recovery_parser_opt_diff' }
            $identity = $outputs[0] | ConvertFrom-Json
            $expectedProviderHex = ([BitConverter]::ToString($encoding.GetBytes($providerTemplate))).Replace('-', '').ToLowerInvariant()
            if ($identity.provider_template_hex -cne $expectedProviderHex -or $outputs[0].Contains($artifactNonce)) { throw 'selftest_independent_nonce_failed' }
            $attackDumps = @(
                $minimalDump,
                $dump.Replace($datedTrailer, ''),
                ($dump + "SELECT 1;`n"),
                ($dump + "-- trailing comment`n"),
                ($dump + "`n"),
                $dump.Replace($datedTrailer, "-- Dump completed`n$datedTrailer"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-01-01 00:00:00 UTC`n"),
                $dump.Replace($datedTrailer, "-- Dump completed by attacker`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-1 0:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-1         0:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-1`t0:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-1  0:0:0 UTC`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-0-1  0:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-0  0:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-13-1  0:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-32  0:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-1  24:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-1  0:60:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-1  0:0:60`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 026-1-1  0:0:0`n"),
                $dump.Replace($datedTrailer, "-- Dump completed on 2026-001-1  0:0:0`n"),
                $dump.Replace($datedTrailer, "$variableMinTrailer$variableMaxTrailer"),
                $dump.Replace("INSERT INTO ``schema_migrations``", "insert into ``schema_migrations``"),
                $dump.Replace("INSERT INTO ``email_send_logs``", "INSERT/**/INTO ``email_send_logs``"),
                $dump.Replace("INSERT INTO ``email_send_logs``", "INSERT`nINTO ``email_send_logs``"),
                $dump.Replace("INSERT INTO ``email_send_logs`` VALUES", "INSERT INTO ``email_send_logs`` (``id``) VALUES"),
                $dump.Replace("INSERT INTO ``email_send_logs`` VALUES $logOne,$logTwo;", "INSERT INTO ``email_send_logs`` VALUES $logOne,$logTwo;`nINSERT INTO ``email_send_logs`` VALUES $logOne;"),
                $dump.Replace("CREATE TABLE ``schema_migrations`` (`n", ''),
                $dump.Replace("  ``dirty`` tinyint(1) NOT NULL,", "  ``dirty_flag`` tinyint(1) NOT NULL,"),
                $dump.Replace("CREATE TABLE ``schema_migrations`` (`n", "CREATE TABLE ``schema_migrations`` (``version`` bigint NOT NULL,``dirty`` tinyint(1) NOT NULL,PRIMARY KEY (``version``)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`nCREATE TABLE ``schema_migrations`` (`n"),
                $dump.Replace("INSERT INTO ``schema_migrations`` VALUES (57,0);", "INSERT INTO ``schema_migrations`` VALUES (57,0);`n/*!40101 INSERT INTO ``email_send_logs`` VALUES $logOne */;"),
                $dump.Replace("INSERT INTO ``schema_migrations`` VALUES (57,0);", "INSERT INTO ``schema_migrations`` VALUES (57,0);`n/*!40101 INSERT/**/INTO ``email_send_logs`` VALUES $logOne */;"),
                $dump.Replace("INSERT INTO ``schema_migrations`` VALUES (57,0);", "INSERT INTO ``schema_migrations`` VALUES (57,0);`n/*!40101 INSERT`nINTO ``email_send_logs`` VALUES $logOne */;"),
                $dump.Replace("INSERT INTO ``schema_migrations`` VALUES (57,0);", "INSERT INTO ``schema_migrations`` VALUES (57,0);`n/*!40101 CREATE TABLE ``email_send_logs`` (``id`` bigint) ENGINE=InnoDB */;"),
                $dump.Replace("INSERT INTO ``schema_migrations`` VALUES (57,0);", "INSERT INTO ``schema_migrations`` VALUES (57,0);`n/*!4010 SET NAMES utf8mb4 */;"),
                $dump.Replace("INSERT INTO ``schema_migrations`` VALUES (57,0);", "INSERT INTO ``schema_migrations`` VALUES (57,0);`n/*!40101 SET /* nested */ NAMES utf8mb4 */;"),
                $dump.Replace("INSERT INTO ``schema_migrations`` VALUES (57,0);", "INSERT INTO ``schema_migrations`` VALUES (57,0);`n/*!40101 SET NAMES utf8mb4 ;")
            )
            # 三张业务表分别覆盖列名漂移、列顺序漂移、主键缺失与表引擎漂移。
            $ddlAttacks = @(
                $dump.Replace('  `business_request_no` varchar(64) NOT NULL,', '  `business_request_bad` varchar(64) NOT NULL,'),
                $dump.Replace("  ``id`` bigint unsigned NOT NULL AUTO_INCREMENT,`n  ``business_request_no`` varchar(64) NOT NULL,", "  ``business_request_no`` varchar(64) NOT NULL,`n  ``id`` bigint unsigned NOT NULL AUTO_INCREMENT,"),
                $dump.Replace("  PRIMARY KEY (``id``)`n) ENGINE=InnoDB AUTO_INCREMENT=101 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``email_test_recipient_allowlist``", ") ENGINE=InnoDB AUTO_INCREMENT=101 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``email_test_recipient_allowlist``"),
                $dump.Replace(") ENGINE=InnoDB AUTO_INCREMENT=101 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``email_test_recipient_allowlist``", ") ENGINE=MyISAM AUTO_INCREMENT=101 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``email_test_recipient_allowlist``"),
                $dump.Replace('  `email_hmac` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,', '  `email_hmac_bad` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,'),
                $dump.Replace("  ``email_hmac`` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,`n  ``email_masked`` varchar(191) NOT NULL,", "  ``email_masked`` varchar(191) NOT NULL,`n  ``email_hmac`` char(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,"),
                $dump.Replace("  PRIMARY KEY (``id``)`n) ENGINE=InnoDB AUTO_INCREMENT=202 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``email_provider_templates``", ") ENGINE=InnoDB AUTO_INCREMENT=202 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``email_provider_templates``"),
                $dump.Replace(") ENGINE=InnoDB AUTO_INCREMENT=202 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``email_provider_templates``", ") ENGINE=MyISAM AUTO_INCREMENT=202 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``email_provider_templates``"),
                $dump.Replace('  `provider_template_id` varchar(64) NOT NULL,', '  `provider_template_bad` varchar(64) NOT NULL,'),
                $dump.Replace("  ``provider`` varchar(32) NOT NULL,`n  ``provider_template_id`` varchar(64) NOT NULL,", "  ``provider_template_id`` varchar(64) NOT NULL,`n  ``provider`` varchar(32) NOT NULL,"),
                $dump.Replace("  PRIMARY KEY (``id``)`n) ENGINE=InnoDB AUTO_INCREMENT=303 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``schema_migrations``", ") ENGINE=InnoDB AUTO_INCREMENT=303 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``schema_migrations``"),
                $dump.Replace(") ENGINE=InnoDB AUTO_INCREMENT=303 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``schema_migrations``", ") ENGINE=MyISAM AUTO_INCREMENT=303 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;`n-- Table structure for table ``schema_migrations``"),
                # 动态自增值只接受真实 mysqldump 位置的一次正整数；其他形态和额外表选项必须拒绝。
                $dump.Replace('ENGINE=InnoDB AUTO_INCREMENT=101', 'ENGINE=InnoDB AUTO_INCREMENT=101 AUTO_INCREMENT=102'),
                $dump.Replace('ENGINE=InnoDB AUTO_INCREMENT=101', 'ENGINE=InnoDB AUTO_INCREMENT=0'),
                $dump.Replace('ENGINE=InnoDB AUTO_INCREMENT=101', 'ENGINE=InnoDB AUTO_INCREMENT=-1'),
                $dump.Replace('ENGINE=InnoDB AUTO_INCREMENT=101', 'ENGINE=InnoDB AUTO_INCREMENT=invalid'),
                $dump.Replace('ENGINE=InnoDB AUTO_INCREMENT=101 DEFAULT CHARSET=utf8mb4', 'ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 AUTO_INCREMENT=101'),
                $dump.Replace('ENGINE=InnoDB AUTO_INCREMENT=101 DEFAULT CHARSET=utf8mb4', 'ENGINE=InnoDB AUTO_INCREMENT=101 ROW_FORMAT=DYNAMIC DEFAULT CHARSET=utf8mb4'),
                $dump.Replace(') ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;', ') ENGINE=InnoDB AUTO_INCREMENT=2 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;')
            )
            $attackDumps += $ddlAttacks
            for ($attackIndex = 0; $attackIndex -lt $attackDumps.Count; $attackIndex++) {
                if ($attackDumps[$attackIndex] -ceq $dump) { throw ("selftest_recovery_parser_attack_fixture_noop_$attackIndex") }
                $attackPath = Join-Path $temp ("molin-email-unknown-" + ('{0:x32}' -f ($attackIndex + 20)) + '.sql')
                Write-RestrictedBytes $attackPath $encoding.GetBytes($attackDumps[$attackIndex])
                foreach ($mode in @('normal', 'optimized')) {
                    $arguments = @('-B')
                    if ($mode -ceq 'optimized') { $arguments += '-O' }
                    $ErrorActionPreference = 'Continue'
                    [void](& $python @arguments $parserPath $attackPath 2>$null)
                    $attackExit = $LASTEXITCODE
                    $ErrorActionPreference = $savedPreference
                    if ($attackExit -eq 0) { throw 'selftest_recovery_parser_attack_accepted' }
                }
            }

            # 故障注入分别削弱 EOF 锚点与唯一计数，反例必须能击穿突变体，证明测试确实覆盖两道门禁。
            $eofGate = 'if not completion_at_eof or not is_completion_line(completion_at_eof.group(1)):'
            $weakEofGate = 'if not any(is_completion_line(line) for line in raw_dump.splitlines()):'
            $countGate = 'if sum(1 for line in raw_dump.splitlines() if is_completion_line(line)) != 1:'
            $weakCountGate = 'if sum(1 for line in raw_dump.splitlines() if is_completion_line(line)) < 1:'
            $rangeGate = 'return 1 <= month <= 12 and 1 <= day <= 31 and 0 <= hour <= 23 and 0 <= minute <= 59 and 0 <= second <= 59'
            $weakRangeGate = 'return True'
            $spaceGate = '([0-9]{1,2}) {2,8}([0-9]{1,2})'
            $weakSpaceGate = '([0-9]{1,2}) {1,9}([0-9]{1,2})'
            $faultCases = @(
                @{ Source = $parserSource.Replace($eofGate, $weakEofGate); Dump = $dump + "-- trailing comment`n" },
                @{ Source = $parserSource.Replace($countGate, $weakCountGate); Dump = $dump.Replace($datedTrailer, "-- Dump completed`n$datedTrailer") },
                @{ Source = $parserSource.Replace($rangeGate, $weakRangeGate); Dump = $dump.Replace($datedTrailer, "-- Dump completed on 2026-13-1  0:0:0`n") },
                @{ Source = $parserSource.Replace($spaceGate, $weakSpaceGate); Dump = $dump.Replace($datedTrailer, "-- Dump completed on 2026-1-1 0:0:0`n") }
            )
            for ($faultIndex = 0; $faultIndex -lt $faultCases.Count; $faultIndex++) {
                if ($faultCases[$faultIndex].Source -ceq $parserSource) { throw ("selftest_recovery_parser_fault_noop_$faultIndex") }
                $faultPath = Join-Path $temp ("molin-email-unknown-" + ('{0:x32}' -f ($faultIndex + 100)) + '.sql')
                [IO.File]::Delete($parserPath)
                Write-RestrictedBytes $parserPath $encoding.GetBytes($faultCases[$faultIndex].Source)
                Write-RestrictedBytes $faultPath $encoding.GetBytes($faultCases[$faultIndex].Dump)
                foreach ($mode in @('normal', 'optimized')) {
                    $arguments = @('-B')
                    if ($mode -ceq 'optimized') { $arguments += '-O' }
                    $ErrorActionPreference = 'Continue'
                    [void](& $python @arguments $parserPath $faultPath 2>$null)
                    $faultExit = $LASTEXITCODE
                    $ErrorActionPreference = $savedPreference
                    if ($faultExit -ne 0) { throw 'selftest_recovery_parser_fault_not_exposed' }
                }
            }
        }
        finally { if ($null -ne $temp) { Remove-RestrictedTempDirectory -Path $temp } }
    }
    finally { $addressHmac.Dispose(); $sha256.Dispose() }
}

if ($SelfTest) {
    # SelfTest 只读取本地文件并启动本机 Python/PowerShell，不解析确认词、不发现 ssh.exe。
    $cases = 0
    $payload = Read-VerifiedPayload
    $resolved = Set-FrozenPostcheckInputs -Payload $payload -RecoveryName ('molin-email-unknown-' + ('1' * 32) + '.sql') -BinarySHA ('a' * 64) -RecoverySHA ('b' * 64) -CycleDumpSHA @(('c' * 64), ('d' * 64))
    [void](ConvertTo-Utf8PayloadBytes -Payload $resolved)
    $cases++
    foreach ($attack in @(
        @{ Recovery = '../unsafe.sql'; Binary = ('a' * 64); RecoverySHA = ('b' * 64); Cycle = @(('c' * 64), ('d' * 64)); Error = 'recovery_filename_invalid' },
        @{ Recovery = 'molin-email-unknown-' + ('1' * 31) + '.sql'; Binary = ('a' * 64); RecoverySHA = ('b' * 64); Cycle = @(('c' * 64), ('d' * 64)); Error = 'recovery_filename_invalid' },
        @{ Recovery = 'molin-email-unknown-' + ('1' * 32) + '.sql'; Binary = ('0' * 64); RecoverySHA = ('b' * 64); Cycle = @(('c' * 64), ('d' * 64)); Error = 'binary_sha_invalid' },
        @{ Recovery = 'molin-email-unknown-' + ('1' * 32) + '.sql'; Binary = ('a' * 64); RecoverySHA = ('0' * 64); Cycle = @(('c' * 64), ('d' * 64)); Error = 'recovery_sha_invalid' },
        @{ Recovery = 'molin-email-unknown-' + ('1' * 32) + '.sql'; Binary = ('a' * 64); RecoverySHA = ('b' * 64); Cycle = @(('c' * 64), ('c' * 64)); Error = 'cycle_dump_sha_invalid' }
    )) {
        $rejected = $false
        try { [void](Set-FrozenPostcheckInputs -Payload $payload -RecoveryName $attack.Recovery -BinarySHA $attack.Binary -RecoverySHA $attack.RecoverySHA -CycleDumpSHA $attack.Cycle) } catch { $rejected = $_.Exception.Message -ceq $attack.Error }
        if (-not $rejected) { throw 'selftest_input_attack_accepted' }
        $cases++
    }
    foreach ($required in @(
        'identity_json=$(/usr/bin/python3 - "$recovery_file"',
        'object_pairs_hook=no_duplicates',
        'set(value) != {"code", "message", "data"}',
        'set(value) != required',
        'artifact_match = re.fullmatch',
        'template_nonce_candidates',
        'if parsed["schema"] != [[57, 0]]:',
        'raise ValueError("insert_shape")',
        'raise ValueError("completion")',
        'dated_variable_width_spaced_pattern',
        '1 <= month <= 12 and 1 <= day <= 31 and 0 <= hour <= 23 and 0 <= minute <= 59 and 0 <= second <= 59',
        'fixture_logs_absent=2 scope_rows=0 allowlist_absent=1 template_absent=1',
        'PING)', 'EXISTS "$lock_key")',
        'state_dependency=false writes=false restarts=false retries=0',
        "expected_binary_sha=$('a' * 64)",
        "expected_recovery_sha=$('b' * 64)",
        '[[ "$recovery_sha" == "$expected_recovery_sha" ]]',
        "expected_cycle_dump_shas=($('c' * 64) $('d' * 64))",
        'cycle_evidence_dir="${cycle_dir}/evidence"',
        "`$'57\t0\t69\t1\t1'",
        'cycle_evidence_identities',
        'cycle_dump_identities',
        'stage=final_artifacts'
    )) {
        if (-not $resolved.Contains($required)) { throw 'selftest_required_contract_missing' }
        $cases++
    }
    if ($resolved -match '(?mi)\b(?:KEYS|SCAN|FLUSHDB|FLUSHALL)\b' -or $resolved.Contains('molin-email-unknown-*.state')) { throw 'selftest_forbidden_contract' }
    $cases++
    Invoke-StrictApiJsonSelfTest -Payload $payload
    $cases += 5
    Invoke-StrictIdentityJsonSelfTest -Payload $payload
    $cases += 5
    Invoke-RecoveryIdentitySelfTest -Payload $payload
    $cases += 6
    $recoverySHAContract = { param([string]$Expected, [string]$Actual) return $Expected -ceq $Actual }
    if (-not (& $recoverySHAContract ('b' * 64) ('b' * 64)) -or (& $recoverySHAContract ('b' * 64) ('e' * 64))) {
        throw 'selftest_recovery_sha_fixture_failed'
    }
    $cases += 2
    # 离线 cycle 契约夹具覆盖 dump 替换、空壳 schema、中间目录 symlink 与 TOCTOU 身份漂移。
    $cycleContract = {
        param([string]$ExpectedSHA, [string]$ActualSHA, [string]$SchemaModel, [bool]$EvidenceSymlink, [string]$BeforeIdentity, [string]$AfterIdentity)
        return $ExpectedSHA -ceq $ActualSHA -and $SchemaModel -ceq "57`t0`t69`t1`t1" -and -not $EvidenceSymlink -and $BeforeIdentity -ceq $AfterIdentity
    }
    foreach ($fixture in @(
        @{ Expected = ('c' * 64); Actual = ('c' * 64); Schema = "57`t0`t69`t1`t1"; Symlink = $false; Before = '0:700:1:2:3'; After = '0:700:1:2:3'; Accepted = $true },
        @{ Expected = ('c' * 64); Actual = ('e' * 64); Schema = "57`t0`t69`t1`t1"; Symlink = $false; Before = '0:700:1:2:3'; After = '0:700:1:2:3'; Accepted = $false },
        @{ Expected = ('c' * 64); Actual = ('c' * 64); Schema = "57`t0`t0`t0`t0"; Symlink = $false; Before = '0:700:1:2:3'; After = '0:700:1:2:3'; Accepted = $false },
        @{ Expected = ('c' * 64); Actual = ('c' * 64); Schema = "57`t0`t69`t1`t1"; Symlink = $true; Before = '0:700:1:2:3'; After = '0:700:1:2:3'; Accepted = $false },
        @{ Expected = ('c' * 64); Actual = ('c' * 64); Schema = "57`t0`t69`t1`t1"; Symlink = $false; Before = '0:700:1:2:3'; After = '0:700:1:9:3'; Accepted = $false }
    )) {
        $accepted = & $cycleContract $fixture.Expected $fixture.Actual $fixture.Schema $fixture.Symlink $fixture.Before $fixture.After
        if ($accepted -ne $fixture.Accepted) { throw 'selftest_cycle_contract_fixture_failed' }
        $cases++
    }
    $validSummary = "status=pass api_health=true api_ready=true schema=57 dirty=false fixture_logs_absent=2 scope_rows=0 allowlist_absent=1 template_absent=1 redis_ping=true redis_key_absent=true recovery_mode=600 recovery_sha256_valid=true cleanup_binary_sha256_valid=true cycle_evidence_count=2 cycle_schema_count=2 state_dependency=false writes=false restarts=false retries=0`n"
    if ((Test-RemoteSummary -Stdout $validSummary -Stderr '' -ExitCode 0).Classification -cne 'pass') { throw 'selftest_valid_summary' }
    $cases++
    foreach ($invalid in @($validSummary + "extra=true`n", ($validSummary -replace ' state_dependency=false', ''), $validSummary.Replace('scope_rows=0', 'scope_rows=1'))) {
        if ((Test-RemoteSummary -Stdout $invalid -Stderr '' -ExitCode 0).Classification -ceq 'pass') { throw 'selftest_invalid_summary' }
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
    Write-Output "status=pass mode=selftest cases=$cases external_access=false ssh_attempt_count=0 strict_json=true state_dependency=false output_verified=true process_exit_codes=0,7"
    exit 0
}

$attemptCount = 0
$completedCount = 0
$stdoutLength = 0
$stderrLength = 0
$runTemp = $null
try {
    if ($Confirm -cne 'I_CONFIRM_EMAIL_UNKNOWN_HISTORY_POSTCHECK_ONCE') { throw 'confirmation_required' }
    # powershell.exe -File 无法可靠地把相邻两个 argv 绑定为 string[]；两个摘要必须使用独立命名参数。
    $cycleDumpSHA = @($ExpectedCycleDumpSHA256One, $ExpectedCycleDumpSHA256Two)
    $payload = Set-FrozenPostcheckInputs -Payload (Read-VerifiedPayload) -RecoveryName $RecoveryFileName -BinarySHA $ExpectedCleanupBinarySHA256 -RecoverySHA $ExpectedRecoverySHA256 -CycleDumpSHA $cycleDumpSHA
    $payloadBytes = ConvertTo-Utf8PayloadBytes -Payload $payload
    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($sshExe)) { throw 'ssh_tool_missing' }
    $runTemp = New-RestrictedTempDirectory
    $inputPath = Join-Path $runTemp 'stdin.bin'; $outputPath = Join-Path $runTemp 'stdout.txt'; $errorPath = Join-Path $runTemp 'stderr.txt'
    Write-RestrictedBytes $inputPath $payloadBytes
    Write-RestrictedBytes $outputPath ([byte[]]@())
    Write-RestrictedBytes $errorPath ([byte[]]@())
    $arguments = @('-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', 'pc@8.130.9.163', '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc', 'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '120s', '/bin/bash', '--noprofile', '--norc', '-s', '--')
    $attemptCount = 1
    $process = Start-FixedRedirectedProcess -FilePath $sshExe -ArgumentList $arguments -InputPath $inputPath -OutputPath $outputPath -ErrorPath $errorPath -TimeoutMilliseconds 150000
    $completedCount = 1
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    $stdout = [IO.File]::ReadAllText($outputPath, $encoding); $stderr = [IO.File]::ReadAllText($errorPath, $encoding)
    $stdoutLength = $stdout.Length; $stderrLength = $stderr.Length
    $result = Test-RemoteSummary -Stdout $stdout -Stderr $stderr -ExitCode $process.ExitCode
    if ($result.Classification -cne 'pass') { Write-SafeFailure $result.Classification $attemptCount $completedCount $stdoutLength $stderrLength $result.Stage; exit 2 }
    Write-Output $stdout.TrimEnd([char[]]@("`r", "`n"))
    exit 0
}
catch {
    $classification = 'local_gate_failed'
    if ($_.Exception.Message -in @('confirmation_required', 'payload_missing', 'payload_path_invalid', 'payload_bom_or_nul', 'payload_first_line', 'payload_encoding', 'payload_placeholder_invalid', 'recovery_filename_invalid', 'binary_sha_invalid', 'recovery_sha_invalid', 'cycle_dump_sha_invalid', 'ssh_tool_missing', 'temp_path_invalid', 'temp_path_unsafe', 'temp_file_invalid', 'temp_file_mismatch', 'temp_file_unsafe', 'process_timeout')) { $classification = $_.Exception.Message }
    Write-SafeFailure $classification $attemptCount $completedCount $stdoutLength $stderrLength $null
    exit 2
}
finally { if ($null -ne $runTemp) { Remove-RestrictedTempDirectory -Path $runTemp } }
