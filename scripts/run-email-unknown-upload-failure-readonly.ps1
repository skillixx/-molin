[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)][switch]$SelfTest,
    [Parameter(Mandatory = $false)][switch]$Execute,
    [Parameter(Mandatory = $false)][string]$Confirm
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:ConfirmPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_UPLOAD_FAILURE_WRITABILITY_READONLY_ONCE'
$script:PayloadPath = Join-Path $PSScriptRoot 'email-unknown-upload-failure-readonly.payload.sh'
$script:PayloadSHA = 'f7d5319e870243afb6066aa21c32761f65e104e5c17a0bbd33595652d0d4388f'
$script:Remote = 'pc@8.130.9.163'
$script:SuccessPattern = '^status=pass classification=(?<classification>upload_failure_stage_empty|upload_failure_stage_partial_binary|upload_failure_stage_complete_binary) stage_count=1 stage_identity=true entry_count=(?:0|1) stage_empty=(?:true|false) parent_writable=(?<parent_writable>true) stage_writable=(?<stage_writable>true) scp_tool=true free_space=(?:adequate|low) free_inodes=(?:adequate|low) binary_size_class=(?:absent|zero|partial|expected) binary_hash_match=(?:not_checked|true) writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0\r?\n?$'
$script:FailurePattern = '^status=failed classification=(?<classification>unexpected|parent_identity|parent_not_writable|stage_count|stage_identity|stage_not_writable|stage_contents_unexpected|binary_identity|stage_changed|scp_tool|space_metadata|inode_metadata) stage_count=(?<stage_count>[0-9]+) stage_identity=(?<stage_identity>true|false) entry_count=(?<entry_count>[0-9]+) stage_empty=(?<stage_empty>true|false) parent_writable=(?<parent_writable>true|false) stage_writable=(?<stage_writable>true|false) scp_tool=(?<scp_tool>true|false) free_space=(?<free_space>unknown|adequate|low) free_inodes=(?<free_inodes>unknown|adequate|low) binary_size_class=(?<binary_size_class>absent|zero|partial|expected|oversize) binary_hash_match=(?<binary_hash_match>not_checked|true|false) writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0\r?\n?$'

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

function Assert-ReadonlyContract {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $text = [Text.UTF8Encoding]::new($false, $true).GetString($Bytes)
    foreach ($required in @(
        'stage_count -eq 1', 'pc:700', 'entry_count -eq 0', '/usr/bin/df -Pk', '/usr/bin/df -Pi',
        'writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0'
    )) {
        if (-not $text.Contains($required)) { throw 'payload_contract_missing' }
    }
    foreach ($forbidden in @(
        'rm -', 'unlink ', 'chmod ', 'chown ', 'touch ', 'mkdir ', 'scp -t', 'docker ', 'mysql ', 'redis-cli ',
        'curl ', 'wget ', 'DELETE ', 'UPDATE ', 'INSERT ', 'REPLACE ', 'ALTER ', 'DROP ', 'TRUNCATE ',
        'FLUSHDB', 'FLUSHALL', 'KEYS ', 'SCAN ', 'SingleSendMail'
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
    return [pscustomobject]([ordered]@{
        ExitCode = $ExitCode
        StdoutBytes = $StdoutBytes
        StderrBytes = $StderrBytes
        Stdout = $Stdout
    })
}

function Invoke-CapturedProcess {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][byte[]]$Payload,
        [Parameter(Mandatory = $true)][int]$TimeoutMilliseconds
    )
    if (-not [IO.File]::Exists($FilePath) -or $Arguments.Count -lt 2 -or $Payload.Length -lt 1) {
        throw 'process_arguments_invalid'
    }
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar)
    $temporary = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-upload-readonly-' + [Guid]::NewGuid().ToString('N'))))
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
        # 再等待一次，确保 PowerShell 5.1 已刷新三个重定向文件和退出码。
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
        foreach ($path in @($stdin, $stdout, $stderr)) {
            if ([IO.File]::Exists($path)) { [IO.File]::Delete($path) }
        }
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
    $parentWritable = $false
    $stageWritable = $false
    $success = [regex]::Match($Result.Stdout, $script:SuccessPattern)
    $failure = [regex]::Match($Result.Stdout, $script:FailurePattern)
    if ($success.Success -and $Result.ExitCode -eq 0 -and $Result.StderrBytes -eq 0) {
        $classification = $success.Groups['classification'].Value
        $parentWritable = $success.Groups['parent_writable'].Value -ceq 'true'
        $stageWritable = $success.Groups['stage_writable'].Value -ceq 'true'
    }
    elseif ($failure.Success -and $Result.ExitCode -eq 2) {
        $classification = $failure.Groups['classification'].Value
        $parentWritable = $failure.Groups['parent_writable'].Value -ceq 'true'
        $stageWritable = $failure.Groups['stage_writable'].Value -ceq 'true'
    }
    $fields = [ordered]@{
        status = if ($classification -cmatch '^upload_failure_stage_(?:empty|partial_binary|complete_binary)$') { 'pass' } else { 'failed' }
        mode = 'email_unknown_upload_failure_readonly'
        classification = $classification
        exit_code = $Result.ExitCode
        stdout_length = $Result.StdoutBytes
        stderr_length = $Result.StderrBytes
        parent_writable = $parentWritable
        stage_writable = $stageWritable
        ssh_attempts = 1
        scp_attempts = 0
        writes = $false
        database_access = $false
        redis_access = $false
        cleanup = $false
        restart = $false
        retained = $true
        retries = 0
    }
    return ($fields | ConvertTo-Json -Compress)
}

if ($SelfTest) {
    if ($Execute -or $Confirm) { throw 'selftest_arguments_invalid' }
    $payload = Read-VerifiedPayload
    Assert-ReadonlyContract -Bytes $payload
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    & $bash -n $script:PayloadPath
    if ($LASTEXITCODE -ne 0) { throw 'payload_syntax_invalid' }
    $fixture = "printf '%s\n' 'status=pass classification=upload_failure_stage_empty stage_count=1 stage_identity=true entry_count=0 stage_empty=true parent_writable=true stage_writable=true scp_tool=true free_space=adequate free_inodes=adequate binary_size_class=absent binary_hash_match=not_checked writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0'`n"
    $result = Invoke-CapturedProcess -FilePath $bash -Arguments @('-s', '--') `
        -Payload ([Text.UTF8Encoding]::new($false).GetBytes($fixture)) -TimeoutMilliseconds 10000
    $summary = ConvertTo-SafeSummary -Result $result | ConvertFrom-Json
    if ($summary.status -cne 'pass' -or $summary.classification -cne 'upload_failure_stage_empty' -or
        $summary.parent_writable -ne $true -or $summary.stage_writable -ne $true -or
        $summary.ssh_attempts -ne 1 -or $summary.scp_attempts -ne 0) { throw 'summary_regression' }
    # 使用 PowerShell 5.1 自身解析正式分支，防止编码问题把唯一 SSH 赋值语句并入注释。
    $tokens = $null
    $parseErrors = $null
    $ast = [System.Management.Automation.Language.Parser]::ParseFile($MyInvocation.MyCommand.Path, [ref]$tokens, [ref]$parseErrors)
    $formalInvocations = @($ast.EndBlock.Statements | Where-Object {
        $_.Extent.Text -ceq '$result = Invoke-OneSSH -Payload $payload'
    })
    if ($parseErrors.Count -ne 0 -or $formalInvocations.Count -ne 1) { throw 'formal_branch_ast_regression' }
    Write-Output 'status=pass mode=email_unknown_upload_failure_readonly_selftest external_access=false writes=false database_access=false redis_access=false cleanup=false restart=false scp=false retries=0'
    exit 0
}

if (-not $Execute -or $Confirm -cne $script:ConfirmPhrase) { throw 'confirmation_required' }
$payload = Read-VerifiedPayload
Assert-ReadonlyContract -Bytes $payload
# 正式路径只有一次 SSH，不存在 SCP、循环、重试或第二阶段分支。
$result = Invoke-OneSSH -Payload $payload
Write-Output (ConvertTo-SafeSummary -Result $result)
if ($result.ExitCode -ne 0) { exit 2 }
