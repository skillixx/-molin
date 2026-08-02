[CmdletBinding()]
param(
    [string]$Confirm,
    [switch]$SelfTest
)

$ErrorActionPreference = 'Stop'
$script:RequiredPhrase = 'I_CONFIRM_RECOVERY_IDENTITY_READONLY_DIAGNOSTIC_ONCE'
$script:SSHArgumentPrefix = '-n -T -p 10003 -o BatchMode=yes -o NumberOfPasswordPrompts=0 -o StrictHostKeyChecking=yes -o ConnectTimeout=10 pc@8.130.9.163 /usr/bin/printf %s '
$script:SSHArgumentSuffix = ' | /usr/bin/base64 -d | /usr/bin/gzip -d | /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/home/pc USER=pc LOGNAME=pc LANG=C LC_ALL=C /usr/bin/timeout --signal=TERM --kill-after=5s 45s /bin/bash --noprofile --norc -s --'
$script:MaxSSHArgumentLength = 30000
$script:StdoutCaptureLimit = 1024
$script:StderrCaptureLimit = 1024
$script:AllowedClassifications = @(
    'pass', 'recovery_find', 'recovery_stat', 'recovery_uid', 'artifact_name',
    'dump_header', 'dump_trailer', 'sql_lexer', 'ddl_statement_count', 'ddl_shape',
    'table_options', 'column_signature', 'primary_key', 'insert_statement_count',
    'insert_shape', 'row_parse', 'schema_ddl', 'schema_migrations', 'fixture_nonce',
    'fixture_rows', 'fixture_hmac', 'fixture_idempotency_hash', 'fixture_scope',
    'fixture_contract', 'fixture_related', 'fixture_fingerprint', 'fixture_ownership',
    'unclassified'
)
$classificationPattern = ($script:AllowedClassifications | ForEach-Object { [regex]::Escape($_) }) -join '|'
$script:SuccessPattern = '\Astatus=pass parser_pass=(true|false) classification=(' + $classificationPattern + ') candidate_unique=(true|false) file_identity=(true|false) writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0\r?\n?\z'

if ($null -eq ('MolinOps.IdentityDiagnosticCappedReader' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Threading.Tasks;

namespace MolinOps {
    public sealed class IdentityDiagnosticCappedReadResult {
        public byte[] Data { get; set; }
        public bool Exceeded { get; set; }
    }

    public static class IdentityDiagnosticCappedReader {
        // 超过保留上限后继续读取到流结束，避免子进程因管道写满而阻塞.
        public static async Task<IdentityDiagnosticCappedReadResult> ReadAsync(Stream source, int limit) {
            if (source == null) throw new ArgumentNullException("source");
            if (limit < 1) throw new ArgumentOutOfRangeException("limit");
            using (var retained = new MemoryStream()) {
                var buffer = new byte[4096];
                long total = 0;
                int read;
                while ((read = await source.ReadAsync(buffer, 0, buffer.Length).ConfigureAwait(false)) != 0) {
                    var remaining = limit - (int)retained.Length;
                    if (remaining > 0) retained.Write(buffer, 0, Math.Min(remaining, read));
                    total += read;
                }
                return new IdentityDiagnosticCappedReadResult {
                    Data = retained.ToArray(),
                    Exceeded = total > limit
                };
            }
        }
    }
}
'@
}

function Stop-DiagnosticProcessSafely {
    param([Parameter(Mandatory = $true)][Diagnostics.Process]$Process)
    try {
        if (-not $Process.HasExited) {
            $Process.Kill()
            [void]$Process.WaitForExit(5000)
        }
    }
    catch {}
}

function Wait-ConcurrentProcessCapture {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds,
        [Parameter(Mandatory = $true)][int]$StdoutLimit,
        [Parameter(Mandatory = $true)][int]$StderrLimit
    )
    if ($TimeoutMilliseconds -lt 1 -or $StdoutLimit -lt 1 -or $StderrLimit -lt 1) { throw 'capture_argument_invalid' }
    $clock = [Diagnostics.Stopwatch]::StartNew()
    $stdoutTask = [MolinOps.IdentityDiagnosticCappedReader]::ReadAsync($Process.StandardOutput.BaseStream, $StdoutLimit)
    $stderrTask = [MolinOps.IdentityDiagnosticCappedReader]::ReadAsync($Process.StandardError.BaseStream, $StderrLimit)
    if (-not $Process.WaitForExit($TimeoutMilliseconds)) {
        Stop-DiagnosticProcessSafely $Process
        [void][Threading.Tasks.Task]::WaitAll([Threading.Tasks.Task[]]@($stdoutTask, $stderrTask), 5000)
        throw 'process_timeout'
    }
    $remaining = [Math]::Max(1, $TimeoutMilliseconds - [int]$clock.ElapsedMilliseconds)
    if (-not [Threading.Tasks.Task]::WaitAll([Threading.Tasks.Task[]]@($stdoutTask, $stderrTask), $remaining)) {
        Stop-DiagnosticProcessSafely $Process
        throw 'process_timeout'
    }
    return [pscustomobject]@{ Stdout = $stdoutTask.Result; Stderr = $stderrTask.Result }
}

function Get-DiagnosticProtocolResult {
    param(
        [Parameter(Mandatory = $true)][int]$ExitCode,
        [Parameter(Mandatory = $true)]$Capture
    )
    $stdout = $null
    if (-not $Capture.Stdout.Exceeded) {
        try { $stdout = (New-Object Text.UTF8Encoding($false, $true)).GetString($Capture.Stdout.Data) }
        catch [Text.DecoderFallbackException] {}
    }
    $stderrEmpty = -not $Capture.Stderr.Exceeded -and $Capture.Stderr.Data.Length -eq 0
    $stdoutProtocol = $null -ne $stdout -and $stdout -cmatch $script:SuccessPattern
    $stdoutSafeLine = $null -ne $stdout -and $stdout -cmatch '\Astatus=(pass|failed) [ -~]*\r?\n?\z'
    return [pscustomobject]@{
        Pass = $ExitCode -eq 0 -and $stderrEmpty -and $stdoutProtocol
        ExitZero = $ExitCode -eq 0
        StderrEmpty = $stderrEmpty
        StdoutSafeLine = $stdoutSafeLine
        Stdout = $stdout
    }
}

function Read-StrictUtf8File {
    param([Parameter(Mandatory = $true)][string]$Path)
    $item = [IO.FileInfo]::new([IO.Path]::GetFullPath($Path))
    if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot)) {
        throw 'local_file_invalid'
    }
    $bytes = [IO.File]::ReadAllBytes($item.FullName)
    if ($bytes.Length -eq 0 -or ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or $bytes -contains 0) {
        throw 'local_file_encoding'
    }
    return (New-Object Text.UTF8Encoding($false, $true)).GetString($bytes)
}

function Test-StrictBase64 {
    param([AllowEmptyString()][Parameter(Mandatory = $true)][string]$Value)
    return $Value.Length -gt 0 -and $Value.Length % 4 -eq 0 -and $Value -cmatch '\A[A-Za-z0-9+/]+={0,2}\z'
}

function ConvertTo-GzipBase64 {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $compressed = New-Object IO.MemoryStream
    try {
        $gzip = New-Object IO.Compression.GZipStream($compressed, [IO.Compression.CompressionMode]::Compress, $true)
        try { $gzip.Write($Bytes, 0, $Bytes.Length) }
        finally { $gzip.Dispose() }
        $value = [Convert]::ToBase64String($compressed.ToArray())
    }
    finally { $compressed.Dispose() }
    if (-not (Test-StrictBase64 $value)) { throw 'transport_base64_invalid' }
    return $value
}

function ConvertFrom-GzipBase64 {
    param([Parameter(Mandatory = $true)][string]$Value)
    if (-not (Test-StrictBase64 $Value)) { throw 'transport_base64_invalid' }
    $source = New-Object IO.MemoryStream(,[Convert]::FromBase64String($Value))
    $decoded = New-Object IO.MemoryStream
    try {
        $gzip = New-Object IO.Compression.GZipStream($source, [IO.Compression.CompressionMode]::Decompress)
        try { $gzip.CopyTo($decoded) }
        finally { $gzip.Dispose() }
        return $decoded.ToArray()
    }
    finally { $decoded.Dispose(); $source.Dispose() }
}

function New-PayloadTransport {
    param([Parameter(Mandatory = $true)][string]$Payload)
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    $payloadBytes = $encoding.GetBytes($Payload)
    $base64 = ConvertTo-GzipBase64 $payloadBytes
    # Base64 动态段使用单引号包裹且自身不允许引号或 shell 元字符.
    $arguments = $script:SSHArgumentPrefix + "'" + $base64 + "'" + $script:SSHArgumentSuffix
    if ($arguments.Length -ge $script:MaxSSHArgumentLength) { throw 'transport_argv_too_long' }
    return [pscustomobject]@{ Arguments = $arguments; Base64 = $base64; PayloadBytes = $payloadBytes }
}

function New-SSHProcessStartInfo {
    param(
        [Parameter(Mandatory = $true)][string]$SSHExecutable,
        [Parameter(Mandatory = $true)][string]$Arguments
    )
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $SSHExecutable
    $start.Arguments = $Arguments
    $start.UseShellExecute = $false
    # SSH 必须彻底断开本地标准输入，远端脚本只从受约束的压缩参数恢复.
    $start.RedirectStandardInput = $false
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    return $start
}

function Get-InstrumentedParser {
    $postcheck = Read-StrictUtf8File (Join-Path $PSScriptRoot 'email-unknown-history-postcheck.payload.sh')
    $match = [regex]::Match($postcheck, '(?ms)^identity_json=\$\(/usr/bin/python3 - "\$recovery_file" <<''RECOVERY_IDENTITY''\r?\n(?<source>.*?)^RECOVERY_IDENTITY\r?\n\)', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success) { throw 'parser_source_missing' }
    $source = $match.Groups['source'].Value

    $labels = [regex]::Matches($source, 'raise ValueError\("(?<label>[a-z_]+)"\)') | ForEach-Object { $_.Groups['label'].Value } | Sort-Object -Unique
    if ($labels.Count -lt 30) { throw 'parser_label_set_invalid' }
    $mapping = @{
        name = 'artifact_name'; header = 'dump_header'; header_database = 'dump_header'; completion = 'dump_trailer'; completion_count = 'dump_trailer'
        quoted_text = 'sql_lexer'; block_comment = 'sql_lexer'; executable_comment_nested = 'sql_lexer'; executable_comment_version = 'sql_lexer'; executable_comment_empty = 'sql_lexer'; string = 'sql_lexer'; identifier = 'sql_lexer'; unterminated_statement = 'sql_lexer'
        create_tokens = 'ddl_shape'; create_shape = 'ddl_shape'; create_open = 'ddl_shape'; create_depth = 'ddl_shape'; create_segment = 'ddl_shape'; create_close = 'ddl_shape'; create_empty = 'ddl_shape'; table_options = 'table_options'; business_columns = 'column_signature'; business_primary_key = 'primary_key'
        insert_tokens = 'insert_shape'; insert_shape = 'insert_shape'; insert_body = 'insert_shape'; depth = 'row_parse'; syntax = 'row_parse'; tuple = 'row_parse'; escape = 'row_parse'; scalar = 'row_parse'
        schema_ddl_count = 'schema_ddl'; schema_version_column = 'schema_ddl'; schema_dirty_column = 'schema_ddl'; schema_primary_key = 'schema_ddl'; schema = 'schema_migrations'
        fixture_nonce = 'fixture_nonce'; logs = 'fixture_rows'; scope_rows = 'fixture_rows'; log_identity = 'fixture_idempotency_hash'; scope = 'fixture_scope'; log_contract = 'fixture_contract'; related = 'fixture_related'; allowlist = 'fixture_ownership'; binding = 'fixture_ownership'
        insert_statement_count = 'insert_statement_count'; ddl_statement_count = 'ddl_statement_count'; fixture_hmac = 'fixture_hmac'; fixture_fingerprint = 'fixture_fingerprint'
        recovery_find = 'recovery_find'; recovery_stat = 'recovery_stat'; recovery_uid = 'recovery_uid'
    }
    foreach ($label in $labels) { if (-not $mapping.ContainsKey($label) -and $label -ne 'table_statement_count') { throw ('parser_label_unmapped_' + $label) } }

    $pairs = $mapping.GetEnumerator() | Sort-Object Name | ForEach-Object { '    "' + $_.Name + '": "' + $_.Value + '",' }
    $prefix = "import os`nimport re`nimport stat`nimport subprocess`nimport sys`n_SAFE_CLASSIFICATION = {`n" + ($pairs -join "`n") + "`n}`n" + @'
def _safe_exception_hook(exc_type, exc_value, traceback):
    classification = _SAFE_CLASSIFICATION.get(str(exc_value), "unclassified") if exc_type is ValueError else "unclassified"
    print("SAFE_RECOVERY_IDENTITY=" + classification)
sys.excepthook = _safe_exception_hook

def _run_readonly_command(arguments, classification):
    try:
        completed = subprocess.run(arguments, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5, check=False)
    except (OSError, subprocess.SubprocessError):
        raise ValueError(classification)
    if completed.returncode != 0 or completed.stderr != b"":
        raise ValueError(classification)
    try:
        output = completed.stdout.decode("ascii", errors="strict")
    except UnicodeDecodeError:
        raise ValueError(classification)
    if "\x00" in output or "\r" in output:
        raise ValueError(classification)
    return output

def _discover_recovery_file():
    find_output = _run_readonly_command([
        "/usr/bin/find", "/home/pc/molin/rollback", "-mindepth", "1", "-maxdepth", "1", "-type", "f",
        "-name", "molin-email-unknown-*.sql", "-print",
    ], "recovery_find")
    if not re.fullmatch(r"/home/pc/molin/rollback/molin-email-unknown-[a-f0-9]{32}\.sql\n", find_output):
        raise ValueError("recovery_find")
    recovery_file = find_output[:-1]

    uid_output = _run_readonly_command(["/usr/bin/id", "-u"], "recovery_uid")
    if not re.fullmatch(r"[0-9]+\n", uid_output):
        raise ValueError("recovery_uid")
    current_uid = int(uid_output[:-1])

    stat_output = _run_readonly_command(["/usr/bin/stat", "-c", "%f:%u:%a:%s", "--", recovery_file], "recovery_stat")
    stat_match = re.fullmatch(r"([0-9a-f]+):([0-9]+):([0-7]{3,4}):([1-9][0-9]*)\n", stat_output)
    if not stat_match:
        raise ValueError("recovery_stat")
    mode_bits, owner_uid, mode, _ = stat_match.groups()
    if not stat.S_ISREG(int(mode_bits, 16)) or int(owner_uid) != current_uid or mode != "600":
        raise ValueError("recovery_stat")
    return recovery_file
'@
    $prefix += "`n"

    $oldPath = 'path = sys.argv[1]'
    if ($source.IndexOf($oldPath, [StringComparison]::Ordinal) -lt 0 -or $source.IndexOf($oldPath, [StringComparison]::Ordinal) -ne $source.LastIndexOf($oldPath, [StringComparison]::Ordinal)) { throw 'parser_path_patch_invalid' }
    $source = $source.Replace($oldPath, 'path = _discover_recovery_file()')

    $oldCount = 'if any(len(values) != 1 for values in bodies.values()) or any(len(values) != 1 for values in creates.values()):' + "`n" + '    raise ValueError("table_statement_count")'
    $newCount = 'if any(len(values) != 1 for values in bodies.values()):' + "`n" + '    raise ValueError("insert_statement_count")' + "`n" + 'if any(len(values) != 1 for values in creates.values()):' + "`n" + '    raise ValueError("ddl_statement_count")'
    if ($source.IndexOf($oldCount, [StringComparison]::Ordinal) -lt 0 -or $source.IndexOf($oldCount, [StringComparison]::Ordinal) -ne $source.LastIndexOf($oldCount, [StringComparison]::Ordinal)) { throw 'parser_count_patch_invalid' }
    $source = $source.Replace($oldCount, $newCount)

    $oldLogs = 'log_candidates = [row for row in parsed["logs"] if len(row) == 19 and row[7] == recipient_hmac]' + "`n" + 'if len(log_candidates) != 2:' + "`n" + '    raise ValueError("logs")'
    $newLogs = 'fixture_log_candidates = [row for row in parsed["logs"] if len(row) == 19 and row[4] == provider_template]' + "`n" + 'if len(fixture_log_candidates) != 2:' + "`n" + '    raise ValueError("logs")' + "`n" + 'if any(row[7] != recipient_hmac for row in fixture_log_candidates):' + "`n" + '    raise ValueError("fixture_hmac")' + "`n" + 'log_candidates = fixture_log_candidates'
    if ($source.IndexOf($oldLogs, [StringComparison]::Ordinal) -lt 0 -or $source.IndexOf($oldLogs, [StringComparison]::Ordinal) -ne $source.LastIndexOf($oldLogs, [StringComparison]::Ordinal)) { throw 'parser_hmac_patch_invalid' }
    $source = $source.Replace($oldLogs, $newLogs)

    $resultMarker = 'result = {'
    $fingerprintGate = 'expected_fingerprint = hashlib.sha256(f"POST\n/api/admin/email/templates/{template_id}/test-send\nregister\n{recipient_hmac}".encode()).hexdigest()' + "`n" + 'if any(row[11] != expected_fingerprint for row in log_candidates):' + "`n" + '    raise ValueError("fixture_fingerprint")' + "`n"
    if ($source.IndexOf($resultMarker, [StringComparison]::Ordinal) -lt 0 -or $source.IndexOf($resultMarker, [StringComparison]::Ordinal) -ne $source.LastIndexOf($resultMarker, [StringComparison]::Ordinal)) { throw 'parser_fingerprint_patch_invalid' }
    $source = $source.Replace($resultMarker, $fingerprintGate + $resultMarker)

    $oldPrint = 'print(json.dumps(result, separators=(",", ":"), sort_keys=True))'
    if ($source.IndexOf($oldPrint, [StringComparison]::Ordinal) -lt 0 -or $source.IndexOf($oldPrint, [StringComparison]::Ordinal) -ne $source.LastIndexOf($oldPrint, [StringComparison]::Ordinal)) { throw 'parser_output_patch_invalid' }
    $source = $source.Replace($oldPrint, 'print("SAFE_RECOVERY_IDENTITY=pass")')

    return $prefix + $source
}

function Get-FinalPayload {
    $template = Read-StrictUtf8File (Join-Path $PSScriptRoot 'email-unknown-recovery-identity-readonly-diagnostic.payload.sh')
    $placeholder = '__MOLIN_RECOVERY_IDENTITY_DIAGNOSTIC_PARSER__'
    if ($template.IndexOf($placeholder, [StringComparison]::Ordinal) -lt 0 -or $template.IndexOf($placeholder, [StringComparison]::Ordinal) -ne $template.LastIndexOf($placeholder, [StringComparison]::Ordinal)) { throw 'diagnostic_placeholder_invalid' }
    $payload = $template.Replace($placeholder, (Get-InstrumentedParser))
    if ($payload.Contains($placeholder)) { throw 'diagnostic_placeholder_invalid' }
    return $payload
}

function Invoke-ParserSmokeTest {
    param([Parameter(Mandatory = $true)][string]$Parser)
    $tempRoot = Join-Path ([IO.Path]::GetTempPath()) ('molin-recovery-identity-' + [Guid]::NewGuid().ToString('N'))
    [IO.Directory]::CreateDirectory($tempRoot) | Out-Null
    try {
        $encoding = New-Object Text.UTF8Encoding($false, $true)
        $parserPath = Join-Path $tempRoot 'parser.py'
        $fixturePath = Join-Path $tempRoot ('molin-email-unknown-' + ('1' * 32) + '.sql')
        [IO.File]::WriteAllText($fixturePath, "invalid`n", $encoding)
        $python = [string]@(Get-Command python.exe -CommandType Application -ErrorAction Stop)[0].Source
        $pathCall = 'path = _discover_recovery_file()'
        if ($Parser.IndexOf($pathCall, [StringComparison]::Ordinal) -lt 0 -or $Parser.IndexOf($pathCall, [StringComparison]::Ordinal) -ne $Parser.LastIndexOf($pathCall, [StringComparison]::Ordinal)) { throw 'parser_smoke_path_invalid' }
        $testCases = [ordered]@{
            dump_header = $Parser.Replace($pathCall, 'path = sys.argv[1]')
            recovery_find = $Parser.Replace($pathCall, 'raise ValueError("recovery_find")')
            recovery_stat = $Parser.Replace($pathCall, 'raise ValueError("recovery_stat")')
            recovery_uid = $Parser.Replace($pathCall, 'raise ValueError("recovery_uid")')
            unclassified = $Parser.Replace($pathCall, 'raise ValueError("unknown_remote_error")')
        }
        foreach ($classification in $testCases.Keys) {
            [IO.File]::WriteAllText($parserPath, $testCases[$classification], $encoding)
            foreach ($mode in @('normal', 'optimized')) {
                $arguments = @('-B')
                if ($mode -ceq 'optimized') { $arguments += '-O' }
                $saved = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
                $output = & $python @arguments $parserPath $fixturePath 2>&1
                $exitCode = $LASTEXITCODE; $ErrorActionPreference = $saved
                if ($exitCode -eq 0 -or ($output | Out-String).Trim() -cne ('SAFE_RECOVERY_IDENTITY=' + $classification)) { throw ('parser_smoke_test_failed_' + $classification) }
            }
        }
    }
    finally {
        if ([IO.Directory]::Exists($tempRoot)) { [IO.Directory]::Delete($tempRoot, $true) }
    }
}

function Invoke-BashSyntaxCheck {
    param([Parameter(Mandatory = $true)][string]$Payload)
    $bashCandidates = @('C:\Program Files\Git\bin\bash.exe', 'C:\Program Files\Git\usr\bin\bash.exe')
    $bash = $bashCandidates | Where-Object { [IO.File]::Exists($_) } | Select-Object -First 1
    if ([string]::IsNullOrEmpty($bash)) { throw 'bash_tool_missing' }
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $bash
    $start.Arguments = '-n -s'
    $start.UseShellExecute = $false
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    [void]$process.Start()
    $process.StandardInput.Write($Payload)
    $process.StandardInput.Close()
    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    if ($process.ExitCode -ne 0 -or $stdout.Length -ne 0 -or $stderr.Length -ne 0) { throw 'bash_syntax_failed' }
}

if ($SelfTest) {
    $parser = Get-InstrumentedParser
    Invoke-ParserSmokeTest $parser
    $payload = Get-FinalPayload
    if ($payload -cnotmatch 'writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0' -or $payload -match '__MOLIN_') { throw 'selftest_payload_contract' }
    $transport = New-PayloadTransport $payload
    if ($transport.Arguments.Length -ge $script:MaxSSHArgumentLength) { throw 'selftest_transport_argv_length' }
    if (-not (Test-StrictBase64 $transport.Base64)) { throw 'selftest_transport_base64_shape' }
    $roundTrip = @(ConvertFrom-GzipBase64 $transport.Base64)
    if ($roundTrip.Count -ne $transport.PayloadBytes.Count) { throw 'selftest_transport_roundtrip_length' }
    for ($byteIndex = 0; $byteIndex -lt $roundTrip.Count; $byteIndex++) {
        if ([byte]$roundTrip[$byteIndex] -ne [byte]$transport.PayloadBytes[$byteIndex]) { throw 'selftest_transport_roundtrip_bytes' }
    }
    $expectedArguments = $script:SSHArgumentPrefix + "'" + $transport.Base64 + "'" + $script:SSHArgumentSuffix
    if ($transport.Arguments -cne $expectedArguments) { throw 'selftest_transport_argument_shape' }
    if ($script:SSHArgumentPrefix -cne '-n -T -p 10003 -o BatchMode=yes -o NumberOfPasswordPrompts=0 -o StrictHostKeyChecking=yes -o ConnectTimeout=10 pc@8.130.9.163 /usr/bin/printf %s ') { throw 'selftest_transport_ssh_stdin_contract' }
    if ($script:SSHArgumentSuffix -cne ' | /usr/bin/base64 -d | /usr/bin/gzip -d | /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/home/pc USER=pc LOGNAME=pc LANG=C LC_ALL=C /usr/bin/timeout --signal=TERM --kill-after=5s 45s /bin/bash --noprofile --norc -s --') { throw 'selftest_transport_pipeline_contract' }
    if ($transport.Arguments -cnotmatch '\A[ -~]+\z') { throw 'selftest_transport_argument_characters' }
    if ($transport.Arguments -cnotmatch '(?:^| )LANG=C(?: |$)' -or $transport.Arguments -cnotmatch '(?:^| )LC_ALL=C(?: |$)' -or $transport.Arguments -cmatch 'C\.UTF-8') {
        throw 'selftest_ssh_locale_contract'
    }
    foreach ($injectionSample in @("abc'def=", 'abcd|ef=', 'abcd;ef=', 'abcd ef=', "abcd`nef=", 'abcd$(id)=', 'abcd`whoami=', 'abcd>ef=', 'abcd<ef=')) {
        if (Test-StrictBase64 $injectionSample) { throw 'selftest_transport_injection_accepted' }
    }
    if ($script:SSHArgumentSuffix -cmatch '[<>]' -or $script:SSHArgumentSuffix -cmatch '(?:^|[ /])(tee|dd|touch|mkdir|rm)(?: |$)' -or $script:SSHArgumentSuffix -cmatch '(?:sh|bash) -c') { throw 'selftest_transport_remote_write_surface' }
    $testStart = New-SSHProcessStartInfo 'ssh.exe' $transport.Arguments
    if ($testStart.RedirectStandardInput -or -not $testStart.RedirectStandardOutput -or -not $testStart.RedirectStandardError) { throw 'selftest_transport_process_stdio' }
    $template = Read-StrictUtf8File (Join-Path $PSScriptRoot 'email-unknown-recovery-identity-readonly-diagnostic.payload.sh')
    $caseMatch = [regex]::Match($template, '(?m)^    (?<values>artifact_name\|[a-z_|]+\|unclassified)\)$', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $caseMatch.Success) { throw 'selftest_payload_classification_source' }
    $payloadClassifications = $caseMatch.Groups['values'].Value.Split('|') | Sort-Object
    $runnerCaseClassifications = $script:AllowedClassifications | Where-Object { $_ -notin @('pass', 'recovery_find', 'recovery_stat', 'recovery_uid') } | Sort-Object
    if (($payloadClassifications -join ',') -cne ($runnerCaseClassifications -join ',')) { throw 'selftest_payload_classification_drift' }
    if ($template -cnotmatch '(?m)^    recovery_find\)$' -or $template -cnotmatch '(?m)^    recovery_stat\|recovery_uid\)$') { throw 'selftest_payload_recovery_classification_drift' }
    foreach ($classification in $script:AllowedClassifications) {
        $safeSample = "status=pass parser_pass=false classification=$classification candidate_unique=true file_identity=true writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0`n"
        if ($safeSample -cnotmatch $script:SuccessPattern) { throw ('selftest_allowed_classification_' + $classification) }
    }
    $baseSample = 'status=pass parser_pass=false classification=table_options candidate_unique=true file_identity=true writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0' + "`n"
    $rejectedIndex = 0
    foreach ($rejected in @(
        $baseSample.Replace('classification=table_options', 'classification=unexpected_remote_word'),
        $baseSample.Replace('classification=table_options', 'classification=TABLE_OPTIONS'),
        ($baseSample.TrimEnd("`n") + ' extra=true' + "`n")
    )) {
        if ($rejected -cmatch $script:SuccessPattern) { throw ("selftest_rejected_output_pattern_$rejectedIndex") }
        $rejectedIndex++
    }

    # 仅启动本地 PowerShell 子进程，覆盖大流、持续双流和统一超时，不接触网络.
    $localPowerShell = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if (-not [IO.File]::Exists($localPowerShell)) { throw 'selftest_local_process_tool' }
    $startLocalCaptureProcess = {
        param([Parameter(Mandatory = $true)][string]$Command)
        $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($Command))
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = $localPowerShell
        $start.Arguments = '-NoProfile -NonInteractive -EncodedCommand ' + $encoded
        $start.UseShellExecute = $false
        $start.RedirectStandardInput = $false
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $child = New-Object Diagnostics.Process
        $child.StartInfo = $start
        [void]$child.Start()
        return $child
    }

    $largeStdoutProcess = & $startLocalCaptureProcess '[Console]::Out.Write((''x'' * 65536))'
    $largeStdoutCapture = Wait-ConcurrentProcessCapture $largeStdoutProcess 10000 $script:StdoutCaptureLimit $script:StderrCaptureLimit
    $largeStdoutResult = Get-DiagnosticProtocolResult $largeStdoutProcess.ExitCode $largeStdoutCapture
    if (-not $largeStdoutCapture.Stdout.Exceeded -or $largeStdoutCapture.Stdout.Data.Length -ne $script:StdoutCaptureLimit -or $largeStdoutResult.Pass) { throw 'selftest_large_stdout' }

    $largeStderrProcess = & $startLocalCaptureProcess '[Console]::Out.Write("status=pass parser_pass=true classification=pass candidate_unique=true file_identity=true writes=false database=false redis=false postcheck=false cleanup=false restarts=false retries=0`n"); [Console]::Error.Write((''x'' * 65536))'
    $largeStderrCapture = Wait-ConcurrentProcessCapture $largeStderrProcess 10000 $script:StdoutCaptureLimit $script:StderrCaptureLimit
    $largeStderrResult = Get-DiagnosticProtocolResult $largeStderrProcess.ExitCode $largeStderrCapture
    if (-not $largeStderrCapture.Stderr.Exceeded -or $largeStderrCapture.Stderr.Data.Length -ne $script:StderrCaptureLimit -or $largeStderrResult.Pass -or $largeStderrResult.StderrEmpty) { throw 'selftest_large_stderr' }

    $sustainedProcess = & $startLocalCaptureProcess 'for ($i = 0; $i -lt 128; $i++) { [Console]::Out.Write((''o'' * 64)); [Console]::Out.Flush(); [Console]::Error.Write((''e'' * 64)); [Console]::Error.Flush(); Start-Sleep -Milliseconds 2 }'
    $sustainedCapture = Wait-ConcurrentProcessCapture $sustainedProcess 10000 $script:StdoutCaptureLimit $script:StderrCaptureLimit
    if (-not $sustainedCapture.Stdout.Exceeded -or -not $sustainedCapture.Stderr.Exceeded -or $sustainedCapture.Stdout.Data.Length -ne $script:StdoutCaptureLimit -or $sustainedCapture.Stderr.Data.Length -ne $script:StderrCaptureLimit) { throw 'selftest_sustained_output' }

    $timeoutProcess = & $startLocalCaptureProcess 'while ($true) { [Console]::Out.Write((''o'' * 64)); [Console]::Error.Write((''e'' * 64)); Start-Sleep -Milliseconds 5 }'
    $timedOut = $false
    try { [void](Wait-ConcurrentProcessCapture $timeoutProcess 150 $script:StdoutCaptureLimit $script:StderrCaptureLimit) }
    catch { if ($_.Exception.Message -ceq 'process_timeout') { $timedOut = $true } else { throw } }
    if (-not $timedOut -or -not $timeoutProcess.HasExited) { throw 'selftest_timeout' }

    Invoke-BashSyntaxCheck $payload
    $cases = 39 + $script:AllowedClassifications.Count + 3
    Write-Output ("status=pass mode=selftest cases={0} transport_argv_length={1} transport_roundtrip=true transport_base64_strict=true ssh_stdin=false concurrent_drain=true capped_streams=true timeout_covered=true remote_files=false external_access=false ssh_attempt_count=0 writes=false database=false redis=false bash_n=true" -f $cases, $transport.Arguments.Length)
    exit 0
}

if ($Confirm -cne $script:RequiredPhrase) {
    Write-Output 'status=failed stage=local classification=confirmation_required ssh_attempt_count=0 writes=false database=false redis=false'
    exit 2
}

$sshStarted = $false
try {
    $payload = Get-FinalPayload
    $transport = New-PayloadTransport $payload
    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($sshExe)) { throw 'ssh_tool_missing' }
    $start = New-SSHProcessStartInfo $sshExe $transport.Arguments
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $start
    [void]$process.Start()
    $sshStarted = $true
    $capture = Wait-ConcurrentProcessCapture $process 60000 $script:StdoutCaptureLimit $script:StderrCaptureLimit
    $protocol = Get-DiagnosticProtocolResult $process.ExitCode $capture
    if (-not $protocol.Pass) {
        Write-Output ("status=failed stage=remote classification=diagnostic_protocol ssh_attempt_count=1 process_exit_zero={0} stderr_empty={1} stdout_single_ascii_line={2} writes=false database=false redis=false" -f $protocol.ExitZero.ToString().ToLowerInvariant(), $protocol.StderrEmpty.ToString().ToLowerInvariant(), $protocol.StdoutSafeLine.ToString().ToLowerInvariant())
        exit 2
    }
    Write-Output $protocol.Stdout.Trim()
    exit 0
}
catch {
    $known = @('local_file_invalid', 'local_file_encoding', 'transport_base64_invalid', 'transport_argv_too_long', 'parser_source_missing', 'parser_label_set_invalid', 'parser_path_patch_invalid', 'parser_count_patch_invalid', 'parser_hmac_patch_invalid', 'parser_fingerprint_patch_invalid', 'parser_output_patch_invalid', 'diagnostic_placeholder_invalid', 'ssh_tool_missing', 'process_timeout', 'diagnostic_protocol')
    $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'local_gate_failed' }
    $sshAttempts = if ($sshStarted) { 1 } else { 0 }
    Write-Output ("status=failed stage=local classification={0} ssh_attempt_count={1} writes=false database=false redis=false" -f $classification, $sshAttempts)
    exit 2
}
