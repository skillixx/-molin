[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_EMPTY_STAGE_CLEANUP_ONCE'
$script:PayloadPath = Join-Path $PSScriptRoot 'email-unknown-empty-stage-cleanup.payload.sh'
$script:PayloadSHA = '6b628ff7cdd7da3318413affc4a1f823a2cd29a86bca2ef23a2c1d6cd6c70dc9'
$script:Remote = 'pc@8.130.9.163'
$script:SuccessPattern = '^status=pass classification=empty_stage_removed stage_count=1 stage_identity=true entry_count=0 stage_empty=true stage_removed=true writes=true database_access=false redis_access=false restart=false scp=false retries=0\r?\n?$'
$script:FailurePattern = '^status=failed classification=(?<classification>unexpected|parent_identity|stage_count|stage_identity|stage_not_empty|stage_changed|removal_not_verified) stage_count=(?<stage_count>[0-9]+) stage_identity=(?<stage_identity>true|false) entry_count=(?<entry_count>[0-9]+) stage_empty=(?<stage_empty>true|false) stage_removed=(?<stage_removed>true|false) writes=(?<writes>true|false) database_access=false redis_access=false restart=false scp=false retries=0\r?\n?$'

function Read-VerifiedPayload {
    $path = [IO.Path]::GetFullPath($script:PayloadPath)
    $root = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $item = [IO.FileInfo]::new($path)
    if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $item.DirectoryName -cne $root -or $item.FullName -cne $path) { throw 'payload_identity_invalid' }
    $bytes = [IO.File]::ReadAllBytes($path)
    if ($bytes.Length -lt 512 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) -or $bytes -contains 0) {
        throw 'payload_encoding_invalid'
    }
    [void][Text.UTF8Encoding]::new($false, $true).GetString($bytes)
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $script:PayloadSHA) {
        throw 'payload_hash_invalid'
    }
    return ,$bytes
}

function Assert-CleanupContract {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $text = [Text.UTF8Encoding]::new($false, $true).GetString($Bytes)
    foreach ($required in @(
        'stage_count -eq 1', 'pc:700', 'stage_file_id', 'entry_count -eq 0', '${#final_entries[@]} -eq 0',
        '/usr/bin/rmdir -- "$stage"', 'stage_removed=true',
        'database_access=false redis_access=false restart=false scp=false retries=0'
    )) {
        if (-not $text.Contains($required)) { throw 'payload_contract_missing' }
    }
    if ([regex]::Matches($text, '(?m)^/usr/bin/rmdir -- "\$stage"$').Count -ne 1) { throw 'rmdir_count_invalid' }
    foreach ($forbidden in @(
        'rm -', 'unlink ', 'chmod ', 'chown ', 'touch ', 'mkdir ', 'docker ', 'mysql ', 'redis-cli ',
        'curl ', 'wget ', 'scp ', 'DELETE ', 'UPDATE ', 'INSERT ', 'REPLACE ', 'ALTER ', 'DROP ',
        'TRUNCATE ', 'FLUSHDB', 'FLUSHALL', 'KEYS ', 'SCAN ', 'SingleSendMail'
    )) {
        if ($text.Contains($forbidden)) { throw 'payload_contract_forbidden' }
    }
}

function New-CaptureResult {
    param(
        [Parameter(Mandatory = $true)][int]$ExitCode,
        [Parameter(Mandatory = $true)][int]$StdoutBytes,
        [Parameter(Mandatory = $true)][int]$StderrBytes,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout
    )
    return [pscustomobject]([ordered]@{ExitCode=$ExitCode;StdoutBytes=$StdoutBytes;StderrBytes=$StderrBytes;Stdout=$Stdout})
}

function Invoke-CapturedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][byte[]]$Payload,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    if (-not [IO.File]::Exists($FilePath) -or $Arguments.Count -lt 2 -or $Payload.Length -lt 1) { throw 'process_arguments_invalid' }
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $temporary = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-empty-cleanup-' + [Guid]::NewGuid().ToString('N'))))
    if (-not $temporary.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
        [IO.Directory]::Exists($temporary) -or [IO.File]::Exists($temporary)) { throw 'temp_identity_invalid' }
    [void][IO.Directory]::CreateDirectory($temporary)
    $stdin = Join-Path $temporary 'stdin.bin'
    $stdout = Join-Path $temporary 'stdout.bin'
    $stderr = Join-Path $temporary 'stderr.bin'
    $process = $null
    try {
        [IO.File]::WriteAllBytes($stdin, $Payload)
        $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -RedirectStandardInput $stdin `
            -RedirectStandardOutput $stdout -RedirectStandardError $stderr -NoNewWindow -PassThru
        if (-not $process.WaitForExit($TimeoutMilliseconds)) { $process.Kill(); $process.WaitForExit(); throw 'process_timeout' }
        # 再等待一次，确保 PowerShell 5.1 已刷新重定向文件和退出码。
        $process.WaitForExit()
        $process.Refresh()
        $stdoutBytes = [IO.File]::ReadAllBytes($stdout)
        $stderrBytes = [IO.File]::ReadAllBytes($stderr)
        $stdoutText = [Text.UTF8Encoding]::new($false, $true).GetString($stdoutBytes)
        [void][Text.UTF8Encoding]::new($false, $true).GetString($stderrBytes)
        return New-CaptureResult -ExitCode ([int]$process.ExitCode) -StdoutBytes $stdoutBytes.Length `
            -StderrBytes $stderrBytes.Length -Stdout $stdoutText
    }
    finally {
        if ($null -ne $process) { $process.Dispose() }
        foreach ($path in @($stdin, $stdout, $stderr)) { if ([IO.File]::Exists($path)) { [IO.File]::Delete($path) } }
        if ([IO.Directory]::Exists($temporary)) {
            if ([IO.Directory]::GetFileSystemEntries($temporary).Length -ne 0) { throw 'temp_cleanup_not_empty' }
            [IO.Directory]::Delete($temporary, $false)
        }
    }
}

function Invoke-OneSSH {
    param([Parameter(Mandatory = $true)][byte[]]$Payload)
    $ssh = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    $arguments = @(
        '-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0',
        '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', $script:Remote,
        '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc',
        'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM',
        '--kill-after=5s', '60s', '/bin/bash', '--noprofile', '--norc', '-s', '--'
    )
    return Invoke-CapturedProcess -FilePath $ssh -Arguments $arguments -Payload $Payload -TimeoutMilliseconds 90000
}

function ConvertTo-SafeSummary {
    param([Parameter(Mandatory = $true)]$Result)
    $classification = 'transport_or_output_failure'
    if ([regex]::IsMatch($Result.Stdout, $script:SuccessPattern) -and $Result.ExitCode -eq 0 -and $Result.StderrBytes -eq 0) {
        $classification = 'empty_stage_removed'
    }
    else {
        $failure = [regex]::Match($Result.Stdout, $script:FailurePattern)
        if ($failure.Success -and $Result.ExitCode -eq 2) { $classification = $failure.Groups['classification'].Value }
    }
    return ([ordered]@{
        status=if($classification -ceq 'empty_stage_removed'){'pass'}else{'failed'}
        mode='email_unknown_empty_stage_cleanup';classification=$classification;exit_code=$Result.ExitCode
        stdout_length=$Result.StdoutBytes;stderr_length=$Result.StderrBytes;ssh_attempts=1;scp_attempts=0
        stage_removed=($classification -ceq 'empty_stage_removed');database_access=$false;redis_access=$false
        restart=$false;retries=0
    } | ConvertTo-Json -Compress)
}

if ($SelfTest) {
    if ($Execute -or $Confirm) { throw 'selftest_arguments_invalid' }
    $payload = Read-VerifiedPayload
    Assert-CleanupContract -Bytes $payload
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    & $bash -n $script:PayloadPath
    if ($LASTEXITCODE -ne 0) { throw 'payload_syntax_invalid' }
    $fixture = "printf '%s\n' 'status=pass classification=empty_stage_removed stage_count=1 stage_identity=true entry_count=0 stage_empty=true stage_removed=true writes=true database_access=false redis_access=false restart=false scp=false retries=0'`n"
    $result = Invoke-CapturedProcess -FilePath $bash -Arguments @('-s', '--') `
        -Payload ([Text.UTF8Encoding]::new($false).GetBytes($fixture)) -TimeoutMilliseconds 10000
    $summary = ConvertTo-SafeSummary -Result $result | ConvertFrom-Json
    if ($summary.status -cne 'pass' -or $summary.stage_removed -ne $true -or $summary.ssh_attempts -ne 1) { throw 'summary_regression' }
    # 使用 PowerShell 5.1 自身确认正式分支恰好包含一次 SSH 赋值语句。
    $tokens=$null; $parseErrors=$null
    $ast=[System.Management.Automation.Language.Parser]::ParseFile($MyInvocation.MyCommand.Path,[ref]$tokens,[ref]$parseErrors)
    $formalInvocations=@($ast.EndBlock.Statements | Where-Object {$_.Extent.Text -ceq '$result = Invoke-OneSSH -Payload $payload'})
    if ($parseErrors.Count -ne 0 -or $formalInvocations.Count -ne 1) { throw 'formal_branch_ast_regression' }
    Write-Output 'status=pass mode=email_unknown_empty_stage_cleanup_selftest external_access=false writes=false database_access=false redis_access=false restart=false scp=false retries=0'
    exit 0
}

if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase) { throw 'confirmation_required' }
$payload = Read-VerifiedPayload
Assert-CleanupContract -Bytes $payload
# 正式路径只允许一次 SSH，不包含 SCP、重试或第二阶段分支。
$result = Invoke-OneSSH -Payload $payload
Write-Output (ConvertTo-SafeSummary -Result $result)
if ($result.ExitCode -ne 0) { exit 2 }
