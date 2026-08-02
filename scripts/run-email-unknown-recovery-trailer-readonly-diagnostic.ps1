[CmdletBinding()]
param(
    [string]$Confirm,
    [switch]$SelfTest
)

$ErrorActionPreference = 'Stop'
$script:RequiredPhrase = 'I_CONFIRM_RECOVERY_TRAILER_READONLY_DIAGNOSTIC_ONCE'
$script:SSHArgumentPrefix = '-n -T -p 10003 -o BatchMode=yes -o NumberOfPasswordPrompts=0 -o StrictHostKeyChecking=yes -o ConnectTimeout=10 pc@8.130.9.163 /usr/bin/printf %s '
$script:SSHArgumentSuffix = ' | /usr/bin/base64 -d | /usr/bin/gzip -d | /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/home/pc USER=pc LOGNAME=pc LANG=C LC_ALL=C /usr/bin/timeout --signal=TERM --kill-after=5s 45s /bin/bash --noprofile --norc -s --'
$script:MaxSSHArgumentLength = 30000
$script:CaptureLimit = 1024
$structure = 'completion_prefix_count=(0|1|2\+) last_nonempty_is_completion_prefix=(true|false) eof_type=(no_newline|LF|CRLF|other) trailing_blank_lines=(0|1|2\+) (?:suffix=other_ascii other_ascii_shape=(trailing_space|double_space|tab|compact_offset_colon|spaced_offset_nocolon|compact_offset_nocolon|attached_z|t_separator|date_only|timezone_parenthesized|other) lead_token=(on|at|colon|paren|space|other) alpha_runs=(0|1|2|3\+) digit_runs=(0|1|2|3|4|5|6|7\+) space_runs=(0|1|2|3|4\+) punctuation_mask=(date_time|date_time_dot|date_time_offset|letters_digits|mixed) separator_profile=(hyphen_colon|slash_colon|dot_colon|hyphen_dot|slash_dot|dot_dot|contains_semicolon|contains_comma|contains_paren|contains_other) field_width_profile=(expected|has_short|has_long|mixed|other) space_width_profile=(all_single|after_prefix_multi|after_on_multi|between_multi|multiple_multi|other)|suffix=(undated|dated_seconds|dated_fractional|dated_utc|dated_timezone|nonascii) other_ascii_shape=not_applicable lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable) last_line_length=(0|1-64|65-128|129-256|257\+)'
$errorClassification = '(recovery_find|recovery_uid|recovery_stat|recovery_read|recovery_size|recovery_utf8|unclassified)'
$script:SuccessPattern = '\A(?:status=pass classification=pass candidate_unique=true file_identity=true ' + $structure + ' writes=false database=false redis=false retries=0|status=pass classification=' + $errorClassification + ' candidate_unique=(true|false) file_identity=(true|false) writes=false database=false redis=false retries=0)\r?\n?\z'

if ($null -eq ('MolinOps.TrailerDiagnosticCappedReader' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Threading.Tasks;

namespace MolinOps {
    public sealed class TrailerDiagnosticCappedReadResult {
        public byte[] Data { get; set; }
        public bool Exceeded { get; set; }
    }

    public static class TrailerDiagnosticCappedReader {
        // 超限后继续读取到 EOF，但不保留额外内容，避免双管道互相阻塞.
        public static async Task<TrailerDiagnosticCappedReadResult> ReadAsync(Stream source, int limit) {
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
                return new TrailerDiagnosticCappedReadResult { Data = retained.ToArray(), Exceeded = total > limit };
            }
        }
    }
}
'@
}

function Read-StrictLocalFile {
    param([Parameter(Mandatory = $true)][string]$Name)
    $path = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot $Name))
    $item = [IO.FileInfo]::new($path)
    if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot)) { throw 'local_file_invalid' }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes.Length -eq 0 -or ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or $bytes -contains 0) { throw 'local_file_encoding' }
    return (New-Object Text.UTF8Encoding($false, $true)).GetString($bytes)
}

function Test-StrictBase64 {
    param([AllowEmptyString()][Parameter(Mandatory = $true)][string]$Value)
    return $Value.Length -gt 0 -and $Value.Length % 4 -eq 0 -and $Value -cmatch '\A[A-Za-z0-9+/]+={0,2}\z'
}

function New-PayloadTransport {
    param([Parameter(Mandatory = $true)][string]$Payload)
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    $payloadBytes = $encoding.GetBytes($Payload)
    $memory = New-Object IO.MemoryStream
    try {
        $gzip = New-Object IO.Compression.GZipStream($memory, [IO.Compression.CompressionMode]::Compress, $true)
        try { $gzip.Write($payloadBytes, 0, $payloadBytes.Length) } finally { $gzip.Dispose() }
        $base64 = [Convert]::ToBase64String($memory.ToArray())
    }
    finally { $memory.Dispose() }
    if (-not (Test-StrictBase64 $base64)) { throw 'transport_base64_invalid' }
    $arguments = $script:SSHArgumentPrefix + "'" + $base64 + "'" + $script:SSHArgumentSuffix
    if ($arguments.Length -ge $script:MaxSSHArgumentLength -or $arguments -cnotmatch '\A[ -~]+\z') { throw 'transport_argv_invalid' }
    return [pscustomobject]@{ Arguments = $arguments; Base64 = $base64; PayloadBytes = $payloadBytes }
}

function ConvertFrom-TransportBase64 {
    param([Parameter(Mandatory = $true)][string]$Base64)
    if (-not (Test-StrictBase64 $Base64)) { throw 'transport_base64_invalid' }
    $source = New-Object IO.MemoryStream(,[Convert]::FromBase64String($Base64))
    $target = New-Object IO.MemoryStream
    try {
        $gzip = New-Object IO.Compression.GZipStream($source, [IO.Compression.CompressionMode]::Decompress)
        try { $gzip.CopyTo($target) } finally { $gzip.Dispose() }
        return $target.ToArray()
    }
    finally { $target.Dispose(); $source.Dispose() }
}

function New-SSHStartInfo {
    param([Parameter(Mandatory = $true)][string]$Executable, [Parameter(Mandatory = $true)][string]$Arguments)
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $Executable
    $start.Arguments = $Arguments
    $start.UseShellExecute = $false
    $start.RedirectStandardInput = $false
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    return $start
}

function Stop-ChildSafely {
    param([Parameter(Mandatory = $true)][Diagnostics.Process]$Process)
    try { if (-not $Process.HasExited) { $Process.Kill(); [void]$Process.WaitForExit(5000) } } catch {}
}

function Wait-ConcurrentCapture {
    param([Parameter(Mandatory = $true)][Diagnostics.Process]$Process, [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds)
    $clock = [Diagnostics.Stopwatch]::StartNew()
    $stdoutTask = [MolinOps.TrailerDiagnosticCappedReader]::ReadAsync($Process.StandardOutput.BaseStream, $script:CaptureLimit)
    $stderrTask = [MolinOps.TrailerDiagnosticCappedReader]::ReadAsync($Process.StandardError.BaseStream, $script:CaptureLimit)
    if (-not $Process.WaitForExit($TimeoutMilliseconds)) {
        Stop-ChildSafely $Process
        [void][Threading.Tasks.Task]::WaitAll([Threading.Tasks.Task[]]@($stdoutTask, $stderrTask), 5000)
        throw 'process_timeout'
    }
    $remaining = [Math]::Max(1, $TimeoutMilliseconds - [int]$clock.ElapsedMilliseconds)
    if (-not [Threading.Tasks.Task]::WaitAll([Threading.Tasks.Task[]]@($stdoutTask, $stderrTask), $remaining)) { Stop-ChildSafely $Process; throw 'process_timeout' }
    return [pscustomobject]@{ Stdout = $stdoutTask.Result; Stderr = $stderrTask.Result }
}

function Get-ProtocolResult {
    param([Parameter(Mandatory = $true)][int]$ExitCode, [Parameter(Mandatory = $true)]$Capture)
    $stdout = $null
    if (-not $Capture.Stdout.Exceeded) {
        try { $stdout = (New-Object Text.UTF8Encoding($false, $true)).GetString($Capture.Stdout.Data) } catch [Text.DecoderFallbackException] {}
    }
    $stderrEmpty = -not $Capture.Stderr.Exceeded -and $Capture.Stderr.Data.Length -eq 0
    $valid = $ExitCode -eq 0 -and $stderrEmpty -and $null -ne $stdout -and $stdout -cmatch $script:SuccessPattern
    return [pscustomobject]@{ Valid = $valid; Stdout = $stdout; ExitZero = $ExitCode -eq 0; StderrEmpty = $stderrEmpty; StdoutSafe = $null -ne $stdout -and $stdout -cmatch '\Astatus=(pass|failed) [ -~]*\r?\n?\z' }
}

function Invoke-BashSyntaxCheck {
    param([Parameter(Mandatory = $true)][string]$Payload)
    $bash = @('C:\Program Files\Git\bin\bash.exe', 'C:\Program Files\Git\usr\bin\bash.exe') | Where-Object { [IO.File]::Exists($_) } | Select-Object -First 1
    if ([string]::IsNullOrEmpty($bash)) { throw 'bash_tool_missing' }
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $bash; $start.Arguments = '-n -s'; $start.UseShellExecute = $false
    $start.RedirectStandardInput = $true; $start.RedirectStandardOutput = $true; $start.RedirectStandardError = $true
    $process = New-Object Diagnostics.Process; $process.StartInfo = $start; [void]$process.Start()
    $process.StandardInput.Write($Payload); $process.StandardInput.Close()
    $stdout = $process.StandardOutput.ReadToEnd(); $stderr = $process.StandardError.ReadToEnd(); $process.WaitForExit()
    if ($process.ExitCode -ne 0 -or $stdout.Length -ne 0 -or $stderr.Length -ne 0) { throw 'bash_syntax_failed' }
}

function Invoke-PayloadProtocolAssociationSelfTest {
    param([Parameter(Mandatory = $true)][string]$Payload)
    $match = [regex]::Match($Payload, '(?m)^if \[\[ \$parser_exit -eq 0 && "\$parser_output" =~ (?<pattern>\^SAFE_TRAILER_RESULT=.*\$) \]\]; then$', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success -or $match.Index -ne $Payload.LastIndexOf($match.Value, [StringComparison]::Ordinal)) { throw 'selftest_payload_protocol_source' }
    $pattern = $match.Groups['pattern'].Value
    $validOther = 'SAFE_TRAILER_RESULT=completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other lead_token=colon alpha_runs=0 digit_runs=2 space_runs=0 punctuation_mask=mixed separator_profile=hyphen_colon field_width_profile=other space_width_profile=other last_line_length=1-64'
    $validDated = 'SAFE_TRAILER_RESULT=completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=dated_seconds other_ascii_shape=not_applicable lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable last_line_length=1-64'
    $invalidDated = 'SAFE_TRAILER_RESULT=completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=dated_seconds other_ascii_shape=trailing_space lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable last_line_length=1-64'
    $invalidOther = 'SAFE_TRAILER_RESULT=completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=not_applicable lead_token=colon alpha_runs=0 digit_runs=2 space_runs=0 punctuation_mask=mixed separator_profile=hyphen_colon field_width_profile=other space_width_profile=other last_line_length=1-64'
    $invalidOtherLexical = 'SAFE_TRAILER_RESULT=completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable last_line_length=1-64'
    $invalidDatedLexical = 'SAFE_TRAILER_RESULT=completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=dated_seconds other_ascii_shape=not_applicable lead_token=colon alpha_runs=0 digit_runs=2 space_runs=0 punctuation_mask=mixed separator_profile=hyphen_colon field_width_profile=other space_width_profile=other last_line_length=1-64'
    $scriptText = @"
#!/bin/bash
set -uo pipefail
accept() { local parser_output=`$1; [[ "`$parser_output" =~ $pattern ]]; }
if ! accept '$validOther'; then exit 11; fi
if ! accept '$validDated'; then exit 12; fi
if accept '$invalidDated'; then exit 13; fi
if accept '$invalidOther'; then exit 14; fi
if accept '$invalidOtherLexical'; then exit 15; fi
if accept '$invalidDatedLexical'; then exit 16; fi
"@
    $bash = @('C:\Program Files\Git\bin\bash.exe', 'C:\Program Files\Git\usr\bin\bash.exe') | Where-Object { [IO.File]::Exists($_) } | Select-Object -First 1
    if ([string]::IsNullOrEmpty($bash)) { throw 'bash_tool_missing' }
    $protocolPath = [IO.Path]::GetTempFileName()
    try {
        [IO.File]::WriteAllText($protocolPath, $scriptText, (New-Object Text.UTF8Encoding($false)))
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = $bash; $start.Arguments = '"' + $protocolPath + '"'; $start.UseShellExecute = $false
        $start.RedirectStandardInput = $false; $start.RedirectStandardOutput = $true; $start.RedirectStandardError = $true
        $process = New-Object Diagnostics.Process; $process.StartInfo = $start; [void]$process.Start()
        $stdout = $process.StandardOutput.ReadToEnd(); $stderr = $process.StandardError.ReadToEnd(); $process.WaitForExit()
        if ($process.ExitCode -ne 0 -or $stdout.Length -ne 0 -or $stderr.Length -ne 0) { throw ('selftest_payload_protocol_association_' + $process.ExitCode) }
    }
    finally { if ([IO.File]::Exists($protocolPath)) { [IO.File]::Delete($protocolPath) } }
}

function Write-GeneratedSizeFixture {
    param(
        [Parameter(Mandatory = $true)][string]$FixturePath,
        [Parameter(Mandatory = $true)][int64]$TotalBytes,
        [byte[]]$Suffix = [byte[]]@()
    )
    if ($TotalBytes.GetType() -ne [int64] -or $TotalBytes -lt [int64]$Suffix.Length) { throw 'selftest_generated_size_argument' }
    $buffer = New-Object byte[] 65536
    for ($index = 0; $index -lt $buffer.Length; $index++) { $buffer[$index] = 0x78 }
    $stream = [IO.File]::Open($FixturePath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        [int64]$bytesToWrite = $TotalBytes - [int64]$Suffix.Length
        while ($bytesToWrite -gt 0) {
            [int]$writeCount = [int][Math]::Min([int64]$buffer.Length, $bytesToWrite)
            $stream.Write($buffer, 0, $writeCount)
            $bytesToWrite -= [int64]$writeCount
        }
        if ($Suffix.Length -gt 0) { $stream.Write($Suffix, 0, $Suffix.Length) }
    }
    finally { $stream.Dispose() }
    if ([IO.FileInfo]::new($FixturePath).Length -ne $TotalBytes) { throw 'selftest_generated_size_write' }
}

function Invoke-AnalyzerSelfTest {
    param([Parameter(Mandatory = $true)][string]$Payload)
    $match = [regex]::Match($Payload, "(?ms)<<'RECOVERY_TRAILER_DIAGNOSTIC'\r?\n(?<source>.*?)^RECOVERY_TRAILER_DIAGNOSTIC\r?\n", [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success) { throw 'selftest_parser_source_missing' }
    $source = $match.Groups['source'].Value
    $oldCall = 'print("SAFE_TRAILER_RESULT=" + discover_and_analyze())'
    $newCall = 'analysis = analyze_stream(open(__import__("sys").argv[1], "rb", buffering=0))' + "`n" + '    print("SAFE_TRAILER_RESULT=" + analysis[0] + " SAFE_STATE=" + str(analysis[1]))'
    if ($source.IndexOf($oldCall, [StringComparison]::Ordinal) -lt 0 -or $source.IndexOf($oldCall, [StringComparison]::Ordinal) -ne $source.LastIndexOf($oldCall, [StringComparison]::Ordinal)) { throw 'selftest_parser_patch_invalid' }
    $source = $source.Replace($oldCall, $newCall)
    $temp = Join-Path ([IO.Path]::GetTempPath()) ('molin-trailer-diagnostic-' + [Guid]::NewGuid().ToString('N'))
    [IO.Directory]::CreateDirectory($temp) | Out-Null
    try {
        $encoding = New-Object Text.UTF8Encoding($false, $true)
        $parserPath = Join-Path $temp 'parser.py'
        [IO.File]::WriteAllText($parserPath, $source, $encoding)
        $python = [string]@(Get-Command python.exe -CommandType Application -ErrorAction Stop)[0].Source
        $dated = '-- Dump completed on 2026-01-01 02:03:04'
        $notApplicableLexical = 'lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable'
        $fixtures = @(
            @{ Data = '-- Dump completed'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=undated other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = "$dated`n"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=dated_seconds other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = "$dated.123`r`n"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=CRLF trailing_blank_lines=0 suffix=dated_fractional other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = "$dated UTC`n"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=dated_utc other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = "$dated +08:00`n"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=dated_timezone other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = ($dated + ' '); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=trailing_space last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=4+ punctuation_mask=date_time'; Separator = 'hyphen_colon' },
            @{ Data = '-- Dump completed on 2026-01-01  02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=double_space last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=date_time'; Separator = 'hyphen_colon'; Widths = 'field_width_profile=expected space_width_profile=between_multi' },
            @{ Data = "-- Dump completed on 2026-01-01`t02:03:04"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=tab last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=date_time'; Separator = 'hyphen_colon' },
            @{ Data = ($dated + '+08:00'); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=compact_offset_colon last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=7+ space_runs=3 punctuation_mask=date_time_offset'; Separator = 'hyphen_colon' },
            @{ Data = ($dated + ' +0800'); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=spaced_offset_nocolon last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=7+ space_runs=4+ punctuation_mask=date_time_offset'; Separator = 'hyphen_colon' },
            @{ Data = ($dated + '+0800'); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=compact_offset_nocolon last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=7+ space_runs=3 punctuation_mask=date_time_offset'; Separator = 'hyphen_colon' },
            @{ Data = ($dated + 'Z'); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=attached_z last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=2 digit_runs=6 space_runs=3 punctuation_mask=date_time_offset'; Separator = 'hyphen_colon' },
            @{ Data = '-- Dump completed on 2026-01-01T02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=t_separator last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=2 digit_runs=6 space_runs=2 punctuation_mask=date_time'; Separator = 'hyphen_colon' },
            @{ Data = '-- Dump completed on 2026-01-01'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=date_only last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=3 space_runs=2 punctuation_mask=mixed' },
            @{ Data = ($dated + ' (UTC)'); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=timezone_parenthesized last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=2 digit_runs=6 space_runs=4+ punctuation_mask=date_time'; Separator = 'contains_paren' },
            @{ Data = '-- Dump completed strangely'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=space alpha_runs=1 digit_runs=0 space_runs=1 punctuation_mask=letters_digits' },
            @{ Data = ($dated + '+08:00;secret_marker'); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=3+ digit_runs=7+ space_runs=3 punctuation_mask=date_time_offset'; Separator = 'contains_semicolon' },
            @{ Data = ($dated + '.123 extra!'); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=2 digit_runs=7+ space_runs=4+ punctuation_mask=date_time_dot'; Separator = 'hyphen_colon' },
            @{ Data = '-- Dump completed on 2026/01/01 02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=mixed'; Separator = 'slash_colon' },
            @{ Data = '-- Dump completed on 2026.01.01 02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=mixed'; Separator = 'dot_colon' },
            @{ Data = '-- Dump completed on 2026-01-01 02.03.04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=mixed'; Separator = 'hyphen_dot' },
            @{ Data = '-- Dump completed on 2026/01/01 02.03.04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=mixed'; Separator = 'slash_dot' },
            @{ Data = '-- Dump completed on 2026.01.01 02.03.04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=mixed'; Separator = 'dot_dot' },
            @{ Data = ($dated + ',extra'); Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=2 digit_runs=6 space_runs=3 punctuation_mask=date_time'; Separator = 'contains_comma' },
            @{ Data = '-- Dump completed on 26-01-01 02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=mixed'; Separator = 'hyphen_colon'; Widths = 'field_width_profile=has_short space_width_profile=all_single' },
            @{ Data = '-- Dump completed on 20260-01-01 02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=date_time'; Separator = 'hyphen_colon'; Widths = 'field_width_profile=has_long space_width_profile=all_single' },
            @{ Data = '-- Dump completed on 26-001-01 02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=mixed'; Separator = 'hyphen_colon'; Widths = 'field_width_profile=mixed space_width_profile=all_single' },
            @{ Data = '-- Dump completed  on 2026-01-01 02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=space alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=date_time'; Separator = 'hyphen_colon'; Widths = 'field_width_profile=expected space_width_profile=after_prefix_multi' },
            @{ Data = '-- Dump completed on  2026-01-01 02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=on alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=date_time'; Separator = 'hyphen_colon'; Widths = 'field_width_profile=expected space_width_profile=after_on_multi' },
            @{ Data = '-- Dump completed  on  2026-01-01 02:03:04'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=space alpha_runs=1 digit_runs=6 space_runs=3 punctuation_mask=date_time'; Separator = 'hyphen_colon'; Widths = 'field_width_profile=expected space_width_profile=multiple_multi' },
            @{ Data = '-- Dump completed at 1'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=at alpha_runs=1 digit_runs=1 space_runs=2 punctuation_mask=letters_digits' },
            @{ Data = '-- Dump completed:1'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=colon alpha_runs=0 digit_runs=1 space_runs=0 punctuation_mask=mixed' },
            @{ Data = '-- Dump completed(1)'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=paren alpha_runs=0 digit_runs=1 space_runs=0 punctuation_mask=mixed'; Separator = 'contains_paren' },
            @{ Data = '-- Dump completed:1-2'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=colon alpha_runs=0 digit_runs=2 space_runs=0 punctuation_mask=mixed'; Separator = 'hyphen_colon' },
            @{ Data = '-- Dump completed:1-2-3-4'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=colon alpha_runs=0 digit_runs=4 space_runs=0 punctuation_mask=mixed'; Separator = 'hyphen_colon' },
            @{ Data = '-- Dump completed:1-2-3-4-5'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=colon alpha_runs=0 digit_runs=5 space_runs=0 punctuation_mask=mixed'; Separator = 'hyphen_colon' },
            @{ Data = '-- Dump completed 完成'; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=nonascii other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = 'SELECT 1;'; Expected = 'completion_prefix_count=0 last_nonempty_is_completion_prefix=false eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=other alpha_runs=1 digit_runs=1 space_runs=1 punctuation_mask=mixed'; Separator = 'contains_semicolon' },
            @{ Data = "-- Dump completed`n$dated"; Expected = 'completion_prefix_count=2+ last_nonempty_is_completion_prefix=true eof_type=no_newline trailing_blank_lines=0 suffix=dated_seconds other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = "-- Dump completed`nSELECT secret_marker`n"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=false eof_type=LF trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=1-64'; Lexical = 'lead_token=other alpha_runs=3+ digit_runs=0 space_runs=1 punctuation_mask=mixed' },
            @{ Data = "-- Dump completed`n`n"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=1 suffix=undated other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = "-- Dump completed`n`n`n"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=2+ suffix=undated other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = "-- Dump completed`r"; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=other trailing_blank_lines=0 suffix=undated other_ascii_shape=not_applicable last_line_length=1-64' },
            @{ Data = ('x' * 65); Expected = 'completion_prefix_count=0 last_nonempty_is_completion_prefix=false eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=65-128'; Lexical = 'lead_token=other alpha_runs=1 digit_runs=0 space_runs=0 punctuation_mask=letters_digits' },
            @{ Data = ('x' * 129); Expected = 'completion_prefix_count=0 last_nonempty_is_completion_prefix=false eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=129-256'; Lexical = 'lead_token=other alpha_runs=1 digit_runs=0 space_runs=0 punctuation_mask=letters_digits' },
            @{ Data = ('x' * 257); Expected = 'completion_prefix_count=0 last_nonempty_is_completion_prefix=false eof_type=no_newline trailing_blank_lines=0 suffix=other_ascii other_ascii_shape=other last_line_length=257+'; Lexical = 'lead_token=other alpha_runs=1 digit_runs=0 space_runs=0 punctuation_mask=letters_digits' }
        )
        for ($fixtureIndex = 0; $fixtureIndex -lt $fixtures.Count; $fixtureIndex++) {
            $path = Join-Path $temp ("fixture-$fixtureIndex.sql")
            [IO.File]::WriteAllText($path, $fixtures[$fixtureIndex].Data, $encoding)
            foreach ($mode in @('normal', 'optimized')) {
                $arguments = @('-B'); if ($mode -ceq 'optimized') { $arguments += '-O' }
                $saved = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
                $output = & $python @arguments $parserPath $path 2>$null
                $exitCode = $LASTEXITCODE; $ErrorActionPreference = $saved
                $text = ($output | Out-String).Trim()
                $fixtureExpected = [string]$fixtures[$fixtureIndex].Expected
                if ($fixtures[$fixtureIndex].ContainsKey('Lexical')) {
                    $separatorExpected = if ($fixtures[$fixtureIndex].ContainsKey('Separator')) { [string]$fixtures[$fixtureIndex].Separator } else { 'contains_other' }
                    $baseLexicalExpected = [string]$fixtures[$fixtureIndex].Lexical
                    if ($fixtures[$fixtureIndex].ContainsKey('Widths')) {
                        $widthExpected = [string]$fixtures[$fixtureIndex].Widths
                    }
                    else {
                        $fieldWidthExpected = if ($baseLexicalExpected.Contains(' digit_runs=6 ')) { 'expected' } else { 'other' }
                        $spaceWidthExpected = if ($baseLexicalExpected.Contains(' space_runs=3 ')) { 'all_single' } else { 'other' }
                        $widthExpected = 'field_width_profile=' + $fieldWidthExpected + ' space_width_profile=' + $spaceWidthExpected
                    }
                    $lexicalExpected = $baseLexicalExpected + ' separator_profile=' + $separatorExpected + ' ' + $widthExpected
                }
                else {
                    $lexicalExpected = $notApplicableLexical
                }
                if ([string]::IsNullOrWhiteSpace($lexicalExpected)) { throw ('selftest_fixture_lexical_' + $fixtureIndex) }
                $fixtureExpected = $fixtureExpected.Replace(' last_line_length=', ' ' + $lexicalExpected + ' last_line_length=')
                $expectedPattern = '\ASAFE_TRAILER_RESULT=' + [regex]::Escape($fixtureExpected) + ' SAFE_STATE=(?<state>[0-9]{1,4})\z'
                $outputMatch = [regex]::Match($text, $expectedPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
                if ($exitCode -ne 0 -or -not $outputMatch.Success -or [int]$outputMatch.Groups['state'].Value -gt 1024 -or $text.Contains('secret_marker') -or $text.Contains('2026-01-01')) { throw ('selftest_fixture_' + $fixtureIndex) }
            }
        }

        # 生成式夹具不在 PowerShell 内存中构造大字符串：覆盖 64 MiB 边界、超限一字节、跨块 UTF-8 与 CRLF。
        $generatedRoot = [IO.Path]::GetFullPath([string]$temp).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
        $boundaryPath = [IO.Path]::Combine([string]$temp, 'boundary-64m.sql')
        if ([string]::IsNullOrWhiteSpace($boundaryPath) -or -not [IO.Path]::GetFullPath($boundaryPath).StartsWith($generatedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'selftest_boundary_path' }
        $boundaryTrailer = [Text.Encoding]::ASCII.GetBytes("`n-- Dump completed`n")
        $oversizePath = [IO.Path]::Combine([string]$temp, 'oversize-64m-plus-one.sql')
        if ([string]::IsNullOrWhiteSpace($oversizePath) -or -not [IO.Path]::GetFullPath($oversizePath).StartsWith($generatedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'selftest_oversize_path' }
        [int64]$boundaryExpectedBytes = 67108864
        [int64]$oversizeExpectedBytes = 67108865
        if ($boundaryExpectedBytes.GetType() -ne [int64] -or $oversizeExpectedBytes.GetType() -ne [int64] -or $boundaryExpectedBytes -ne [int64]67108864 -or $oversizeExpectedBytes -ne [int64]67108865) { throw 'selftest_generated_size_constant' }
        Write-GeneratedSizeFixture -FixturePath $boundaryPath -TotalBytes ([int64]67108864) -Suffix $boundaryTrailer
        Write-GeneratedSizeFixture -FixturePath $oversizePath -TotalBytes ([int64]67108865)
        if ([IO.FileInfo]::new($boundaryPath).Length -ne [int64]67108864 -or [IO.FileInfo]::new($oversizePath).Length -ne [int64]67108865) { throw 'selftest_generated_size_fixture' }

        $crossUtf8Path = [IO.Path]::Combine([string]$temp, 'cross-chunk-utf8.sql')
        if ([string]::IsNullOrWhiteSpace($crossUtf8Path) -or -not [IO.Path]::GetFullPath($crossUtf8Path).StartsWith($generatedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'selftest_cross_utf8_path' }
        $fillBuffer = New-Object byte[] 65536
        for ($fillIndex = 0; $fillIndex -lt $fillBuffer.Length; $fillIndex++) { $fillBuffer[$fillIndex] = 0x78 }
        $crossUtf8Stream = [IO.File]::Open($crossUtf8Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try {
            $crossUtf8Stream.Write($fillBuffer, 0, 65535)
            $crossUtf8Bytes = [byte[]]@(0xE5, 0xAE, 0x8C)
            $crossUtf8Stream.Write($crossUtf8Bytes, 0, $crossUtf8Bytes.Length)
            $crossUtf8Tail = [Text.Encoding]::ASCII.GetBytes("`n-- Dump completed`n")
            $crossUtf8Stream.Write($crossUtf8Tail, 0, $crossUtf8Tail.Length)
        }
        finally { $crossUtf8Stream.Dispose() }

        $crossNewlinePath = [IO.Path]::Combine([string]$temp, 'cross-chunk-crlf.sql')
        if ([string]::IsNullOrWhiteSpace($crossNewlinePath) -or -not [IO.Path]::GetFullPath($crossNewlinePath).StartsWith($generatedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'selftest_cross_newline_path' }
        $crossNewlineStream = [IO.File]::Open($crossNewlinePath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try {
            $crossNewlineStream.Write($fillBuffer, 0, 65535)
            $crossNewlineTail = [Text.Encoding]::ASCII.GetBytes("`r`n-- Dump completed`r`n")
            $crossNewlineStream.Write($crossNewlineTail, 0, $crossNewlineTail.Length)
        }
        finally { $crossNewlineStream.Dispose() }

        $crossInvalidPath = [IO.Path]::Combine([string]$temp, 'cross-chunk-invalid-utf8.sql')
        if ([string]::IsNullOrWhiteSpace($crossInvalidPath) -or -not [IO.Path]::GetFullPath($crossInvalidPath).StartsWith($generatedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'selftest_cross_invalid_path' }
        $crossInvalidStream = [IO.File]::Open($crossInvalidPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
        try {
            $crossInvalidStream.Write($fillBuffer, 0, 65535)
            $crossInvalidStream.WriteByte(0xE5)
            $crossInvalidTail = [Text.Encoding]::ASCII.GetBytes("(`n-- Dump completed`n")
            $crossInvalidStream.Write($crossInvalidTail, 0, $crossInvalidTail.Length)
        }
        finally { $crossInvalidStream.Dispose() }

        $generatedCases = @(
            @{ Path = $boundaryPath; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=undated other_ascii_shape=not_applicable last_line_length=1-64'; Error = $null },
            @{ Path = $oversizePath; Expected = $null; Error = 'recovery_size' },
            @{ Path = $crossUtf8Path; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=undated other_ascii_shape=not_applicable last_line_length=1-64'; Error = $null },
            @{ Path = $crossNewlinePath; Expected = 'completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=CRLF trailing_blank_lines=0 suffix=undated other_ascii_shape=not_applicable last_line_length=1-64'; Error = $null },
            @{ Path = $crossInvalidPath; Expected = $null; Error = 'recovery_utf8' }
        )
        foreach ($generatedCase in $generatedCases) {
            $generatedPath = [string]$generatedCase['Path']
            $generatedExpected = $generatedCase['Expected']
            $generatedError = $generatedCase['Error']
            foreach ($mode in @('normal', 'optimized')) {
                $arguments = @('-B'); if ($mode -ceq 'optimized') { $arguments += '-O' }
                $saved = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
                $output = & $python @arguments $parserPath $generatedPath 2>$null
                $exitCode = $LASTEXITCODE; $ErrorActionPreference = $saved
                $text = ($output | Out-String).Trim()
                if ($null -ne $generatedError) {
                    $errorMatch = [regex]::Match($text, '\ASAFE_TRAILER_ERROR=(?<classification>recovery_size|recovery_utf8)\z', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
                    if ($exitCode -ne 0) { throw ('selftest_generated_exit_' + $generatedError) }
                    if (-not $errorMatch.Success) { throw ('selftest_generated_shape_' + $generatedError) }
                    if ($errorMatch.Groups['classification'].Value -cne [string]$generatedError) { throw ('selftest_generated_classification_' + $generatedError) }
                }
                else {
                    $generatedExpectedWithLexical = ([string]$generatedExpected).Replace(' last_line_length=', ' ' + $notApplicableLexical + ' last_line_length=')
                    $expectedPattern = '\ASAFE_TRAILER_RESULT=' + [regex]::Escape($generatedExpectedWithLexical) + ' SAFE_STATE=(?<state>[0-9]{1,4})\z'
                    $outputMatch = [regex]::Match($text, $expectedPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
                    if ($exitCode -ne 0 -or -not $outputMatch.Success -or [int]$outputMatch.Groups['state'].Value -gt 1024 -or $text.Contains('2026-01-01')) { throw 'selftest_generated_result' }
                }
            }
        }
        $invalidPath = Join-Path $temp 'invalid.sql'
        [IO.File]::WriteAllBytes($invalidPath, [byte[]]@(0xC3, 0x28))
        foreach ($mode in @('normal', 'optimized')) {
            $arguments = @('-B'); if ($mode -ceq 'optimized') { $arguments += '-O' }
            $saved = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
            $output = & $python @arguments $parserPath $invalidPath 2>$null
            $exitCode = $LASTEXITCODE; $ErrorActionPreference = $saved
            if ($exitCode -ne 0 -or ($output | Out-String).Trim() -cne 'SAFE_TRAILER_ERROR=recovery_utf8') { throw 'selftest_invalid_utf8' }
        }
    }
    finally { if ([IO.Directory]::Exists($temp)) { [IO.Directory]::Delete($temp, $true) } }
}

if ($SelfTest) {
    $payload = Read-StrictLocalFile 'email-unknown-recovery-trailer-readonly-diagnostic.payload.sh'
    Invoke-BashSyntaxCheck $payload
    Invoke-PayloadProtocolAssociationSelfTest $payload
    Invoke-AnalyzerSelfTest $payload
    $transport = New-PayloadTransport $payload
    $roundTrip = @(ConvertFrom-TransportBase64 $transport.Base64)
    if ($roundTrip.Count -ne $transport.PayloadBytes.Count) { throw 'selftest_transport_length' }
    for ($index = 0; $index -lt $roundTrip.Count; $index++) { if ([byte]$roundTrip[$index] -ne [byte]$transport.PayloadBytes[$index]) { throw 'selftest_transport_bytes' } }
    if ($script:SSHArgumentPrefix -cne '-n -T -p 10003 -o BatchMode=yes -o NumberOfPasswordPrompts=0 -o StrictHostKeyChecking=yes -o ConnectTimeout=10 pc@8.130.9.163 /usr/bin/printf %s ' -or
        $script:SSHArgumentSuffix -cne ' | /usr/bin/base64 -d | /usr/bin/gzip -d | /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/home/pc USER=pc LOGNAME=pc LANG=C LC_ALL=C /usr/bin/timeout --signal=TERM --kill-after=5s 45s /bin/bash --noprofile --norc -s --' -or
        $transport.Arguments.Length -ge $script:MaxSSHArgumentLength) { throw 'selftest_transport_contract' }
    foreach ($attack in @("abc'def=", 'abcd|ef=', 'abcd;ef=', 'abcd$(id)=', "abcd`nef=")) { if (Test-StrictBase64 $attack) { throw 'selftest_transport_injection' } }
    if ($payload -cmatch '(?m)^\s*(?:rm|mv|cp|touch|mkdir|tee|dd|mysql|redis-cli|docker)\b' -or $payload -match '/api/|sha256|hashlib|O_WRONLY|O_CREAT|os\.unlink|os\.remove') { throw 'selftest_readonly_or_leak_contract' }
    $successSample = 'status=pass classification=pass candidate_unique=true file_identity=true completion_prefix_count=1 last_nonempty_is_completion_prefix=true eof_type=LF trailing_blank_lines=0 suffix=dated_seconds other_ascii_shape=not_applicable lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable last_line_length=1-64 writes=false database=false redis=false retries=0' + "`n"
    if ($successSample -cnotmatch $script:SuccessPattern) { throw 'selftest_success_protocol' }
    $otherSuccessSample = $successSample.Replace('suffix=dated_seconds other_ascii_shape=not_applicable lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable', 'suffix=other_ascii other_ascii_shape=other lead_token=colon alpha_runs=0 digit_runs=2 space_runs=0 punctuation_mask=mixed separator_profile=hyphen_colon field_width_profile=other space_width_profile=other')
    if ($otherSuccessSample -cnotmatch $script:SuccessPattern) { throw 'selftest_other_success_protocol' }
    foreach ($impossible in @(
        $successSample.Replace('other_ascii_shape=not_applicable', 'other_ascii_shape=trailing_space'),
        $otherSuccessSample.Replace('other_ascii_shape=other', 'other_ascii_shape=not_applicable'),
        $successSample.Replace('lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable', 'lead_token=colon alpha_runs=0 digit_runs=2 space_runs=0 punctuation_mask=mixed separator_profile=hyphen_colon field_width_profile=other space_width_profile=other'),
        $otherSuccessSample.Replace('lead_token=colon alpha_runs=0 digit_runs=2 space_runs=0 punctuation_mask=mixed separator_profile=hyphen_colon field_width_profile=other space_width_profile=other', 'lead_token=not_applicable alpha_runs=not_applicable digit_runs=not_applicable space_runs=not_applicable punctuation_mask=not_applicable separator_profile=not_applicable field_width_profile=not_applicable space_width_profile=not_applicable'),
        $successSample.Replace('separator_profile=not_applicable', 'separator_profile=hyphen_colon'),
        $otherSuccessSample.Replace('separator_profile=hyphen_colon', 'separator_profile=not_applicable'),
        $successSample.Replace('field_width_profile=not_applicable space_width_profile=not_applicable', 'field_width_profile=expected space_width_profile=all_single'),
        $otherSuccessSample.Replace('field_width_profile=other space_width_profile=other', 'field_width_profile=not_applicable space_width_profile=not_applicable')
    )) {
        if ($impossible -cmatch $script:SuccessPattern) { throw 'selftest_impossible_protocol_accepted' }
    }
    foreach ($classification in @('recovery_find', 'recovery_uid', 'recovery_stat', 'recovery_read', 'recovery_size', 'recovery_utf8', 'unclassified')) {
        $unique = if ($classification -ceq 'recovery_find') { 'false' } else { 'true' }
        $identity = if ($classification -cin @('recovery_find', 'recovery_uid', 'recovery_stat', 'recovery_read')) { 'false' } else { 'true' }
        $errorSample = "status=pass classification=$classification candidate_unique=$unique file_identity=$identity writes=false database=false redis=false retries=0`n"
        if ($errorSample -cnotmatch $script:SuccessPattern) { throw ('selftest_error_protocol_' + $classification) }
    }

    $testStart = New-SSHStartInfo 'ssh.exe' $transport.Arguments
    if ($null -eq ('MolinOps.TrailerDiagnosticCappedReader' -as [type]) -or $testStart.RedirectStandardInput -or -not $testStart.RedirectStandardOutput -or -not $testStart.RedirectStandardError) { throw 'selftest_stream_contract' }
    $stdoutMemory = New-Object IO.MemoryStream(,[byte[]](New-Object byte[] 65536))
    $stderrMemory = New-Object IO.MemoryStream(,[byte[]](New-Object byte[] 65536))
    try {
        $stdoutTask = [MolinOps.TrailerDiagnosticCappedReader]::ReadAsync($stdoutMemory, $script:CaptureLimit)
        $stderrTask = [MolinOps.TrailerDiagnosticCappedReader]::ReadAsync($stderrMemory, $script:CaptureLimit)
        if (-not [Threading.Tasks.Task]::WaitAll([Threading.Tasks.Task[]]@($stdoutTask, $stderrTask), 5000) -or
            -not $stdoutTask.Result.Exceeded -or -not $stderrTask.Result.Exceeded -or
            $stdoutTask.Result.Data.Length -ne $script:CaptureLimit -or $stderrTask.Result.Data.Length -ne $script:CaptureLimit -or
            $stdoutMemory.Position -ne $stdoutMemory.Length -or $stderrMemory.Position -ne $stderrMemory.Length) { throw 'selftest_capped_concurrent_drain' }
    }
    finally { $stdoutMemory.Dispose(); $stderrMemory.Dispose() }
    Write-Output ("status=pass mode=selftest cases=100 argv_length={0} categories=all other_ascii_shapes=all lexical_buckets=all separator_profiles=all width_profiles=all suffix_shape_association=true injection=true size_limit_mib=64 size_boundary=true cross_chunk=true bounded_state_bytes=1024 no_leak=true identity_errors=fixed capped_concurrent_drain=true bash_n=true external_access=false ssh_attempt_count=0 writes=false database=false redis=false" -f $transport.Arguments.Length)
    exit 0
}

if ($Confirm -cne $script:RequiredPhrase) {
    Write-Output 'status=failed stage=local classification=confirmation_required ssh_attempt_count=0 writes=false database=false redis=false'
    exit 2
}

$sshStarted = $false
try {
    $payload = Read-StrictLocalFile 'email-unknown-recovery-trailer-readonly-diagnostic.payload.sh'
    $transport = New-PayloadTransport $payload
    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    if (-not [IO.File]::Exists($sshExe) -or ([IO.FileInfo]::new($sshExe).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'ssh_tool_missing' }
    $start = New-SSHStartInfo $sshExe $transport.Arguments
    $process = New-Object Diagnostics.Process; $process.StartInfo = $start; [void]$process.Start(); $sshStarted = $true
    $capture = Wait-ConcurrentCapture $process 60000
    $protocol = Get-ProtocolResult $process.ExitCode $capture
    if (-not $protocol.Valid) {
        Write-Output ("status=failed stage=remote classification=diagnostic_protocol ssh_attempt_count=1 process_exit_zero={0} stderr_empty={1} stdout_single_ascii_line={2} writes=false database=false redis=false" -f $protocol.ExitZero.ToString().ToLowerInvariant(), $protocol.StderrEmpty.ToString().ToLowerInvariant(), $protocol.StdoutSafe.ToString().ToLowerInvariant())
        exit 2
    }
    Write-Output $protocol.Stdout.Trim()
    exit 0
}
catch {
    $known = @('local_file_invalid', 'local_file_encoding', 'transport_base64_invalid', 'transport_argv_invalid', 'ssh_tool_missing', 'process_timeout')
    $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'local_gate_failed' }
    $attempts = if ($sshStarted) { 1 } else { 0 }
    Write-Output ("status=failed stage=local classification={0} ssh_attempt_count={1} writes=false database=false redis=false" -f $classification, $attempts)
    exit 2
}
