[CmdletBinding(DefaultParameterSetName = 'SelfTest')]
param(
    [Parameter(Mandatory = $true, ParameterSetName = 'SelfTest')]
    [switch]$SelfTest,

    [Parameter(Mandatory = $true, ParameterSetName = 'Launch')]
    [switch]$Launch,

    [Parameter(Mandatory = $true, ParameterSetName = 'Poll')]
    [switch]$Poll,

    [Parameter(Mandatory = $true, ParameterSetName = 'Launch')]
    [Parameter(Mandatory = $true, ParameterSetName = 'Poll')]
    [string]$Nonce,

    [Parameter(Mandatory = $true, ParameterSetName = 'Launch')]
    [string]$SnapshotPath,

    [Parameter(Mandatory = $false, ParameterSetName = 'Poll')]
    [ValidateRange(60, 3600)]
    [int]$TimeoutSeconds = 900
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:PayloadPath = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'email_unknown_cleanup_isolated_build.payload.sh'))
$script:SshPath = 'C:\Windows\System32\OpenSSH\ssh.exe'
$script:ScpPath = 'C:\Windows\System32\OpenSSH\scp.exe'
$script:TarPath = 'C:\Windows\System32\tar.exe'
$script:HostName = 'pc@8.130.9.163'
$script:Port = '10003'
$script:LegacyScpProtocolFlag = '-O'
$script:ScpCommon = @($script:LegacyScpProtocolFlag, '-P', $script:Port, '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10')

function Assert-Nonce {
    param([Parameter(Mandatory = $true)][string]$Value)

    # nonce 只允许固定长度的小写十六进制，确保不能注入远端命令或逃逸目录。
    if ($Value -cnotmatch '\A[a-f0-9]{32}\z') { throw 'nonce_invalid' }
}

function Read-FixedFileSHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    # 上传资产必须是非重解析普通文件，并在上传前冻结 SHA256。
    $full = [IO.Path]::GetFullPath($Path)
    $item = [IO.FileInfo]::new($full)
    if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.Length -le 0) {
        throw 'upload_file_invalid'
    }
    return (Get-FileHash -LiteralPath $full -Algorithm SHA256).Hash.ToLowerInvariant()
}

function Assert-SafeServerSnapshot {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not [IO.File]::Exists($script:TarPath)) { throw 'tar_missing' }
    $members = @(& $script:TarPath -tf $Path)
    if ($LASTEXITCODE -ne 0 -or $members.Count -eq 0) { throw 'snapshot_manifest_invalid' }
    foreach ($member in $members) {
        if ($member -notmatch '\Aserver/' -or $member.StartsWith('/') -or ('/' + $member + '/') -match '/\.\.?/' -or
            $member -match '(?i)(^|/)(\.env($|\.)|[^/]*(credential|secret)[^/]*|[^/]*\.(pem|key)$)') {
            throw 'snapshot_member_invalid'
        }
    }
    $verbose = @(& $script:TarPath -tvf $Path)
    if ($LASTEXITCODE -ne 0 -or $verbose.Count -ne $members.Count) { throw 'snapshot_type_manifest_invalid' }
    foreach ($line in $verbose) {
        if ([string]::IsNullOrEmpty($line) -or ($line[0] -cne '-' -and $line[0] -cne 'd')) { throw 'snapshot_member_type_invalid' }
    }
}

function Invoke-FixedOpenSSH {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )

    # 每次调用不重试；任意 stderr 或非零退出都立即失败关闭。
    $lines = @(& $FilePath @Arguments 2>&1)
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) { throw "transport_failed:$exitCode" }
    return (($lines | ForEach-Object { $_.ToString() }) -join "`n").Trim()
}

function Assert-FixedScpArguments {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    # SCP 参数必须与固定清单逐项一致，禁止退回 SFTP、替换传输程序或加载其他配置。
    $expected = @($script:LegacyScpProtocolFlag, '-P', $script:Port, '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10')
    if (@($Arguments | Where-Object { $_ -ceq $script:LegacyScpProtocolFlag }).Count -ne 1) {
        throw 'legacy_scp_protocol_flag_invalid'
    }
    if (@($Arguments | Where-Object { $_ -ceq '-s' -or $_ -ceq '-S' -or $_ -ceq '-F' }).Count -ne 0) {
        throw 'scp_argument_override_forbidden'
    }
    if ($Arguments.Count -ne $expected.Count) { throw 'scp_argument_list_invalid' }
    for ($index = 0; $index -lt $expected.Count; $index++) {
        if ($Arguments[$index] -cne $expected[$index]) { throw 'scp_argument_list_invalid' }
    }
}

if ($SelfTest) {
    # SelfTest 仅调用本机 Git Bash 和固定 payload 的离线 fixture，不解析或连接 SSH。
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash)) { throw 'bash_missing' }
    $null = Read-FixedFileSHA256 -Path $script:PayloadPath
    Assert-Nonce -Value ('a' * 32)
    $invalidNonceRejected = $false
    try { Assert-Nonce -Value '../unsafe' } catch { $invalidNonceRejected = $_.Exception.Message -ceq 'nonce_invalid' }
    if (-not $invalidNonceRejected) { throw 'nonce_attack_accepted' }
    Assert-FixedScpArguments -Arguments $script:ScpCommon
    $missingLegacyRejected = $false
    try { Assert-FixedScpArguments -Arguments @($script:ScpCommon | Where-Object { $_ -cne $script:LegacyScpProtocolFlag }) } catch { $missingLegacyRejected = $_.Exception.Message -ceq 'legacy_scp_protocol_flag_invalid' }
    if (-not $missingLegacyRejected) { throw 'missing_legacy_scp_flag_accepted' }
    $sftpModeRejected = $false
    try { Assert-FixedScpArguments -Arguments @($script:ScpCommon + @('-s')) } catch { $sftpModeRejected = $_.Exception.Message -ceq 'scp_argument_override_forbidden' }
    if (-not $sftpModeRejected) { throw 'sftp_mode_accepted' }
    $configOverrideRejected = $false
    try { Assert-FixedScpArguments -Arguments @($script:ScpCommon + @('-F', 'unsafe-config')) } catch { $configOverrideRejected = $_.Exception.Message -ceq 'scp_argument_override_forbidden' }
    if (-not $configOverrideRejected) { throw 'scp_config_override_accepted' }
    $output = @(& $bash --noprofile --norc $script:PayloadPath --self-test 2>&1)
    if ($LASTEXITCODE -ne 0) { throw 'payload_selftest_failed' }
    $summary = (($output | ForEach-Object { $_.ToString() }) -join "`n").Trim()
    if ($summary -cnotmatch '\Astatus=pass mode=selftest cases=13 duplicate_launch_rejected=true pid_reuse_rejected=true running_observed=true partial_marker_rejected=true stderr_rejected=true timeout_unknown=true worker_missing_unknown=true pass_binary_verified=true integration_env_rejected=true fixture_worker_go_executed=false external_access=false\z') {
        throw 'payload_selftest_summary_invalid'
    }
    Write-Output 'status=pass mode=background_controller_selftest cases=17 shared_poll_implementation=true legacy_scp_protocol=true sftp_mode_rejected=true config_override_rejected=true external_access=false repeated_start=false cleanup_executed=false'
    exit 0
}

Assert-Nonce -Value $Nonce
$remoteRoot = "/home/pc/molin-qa-email-cleanup-build-$Nonce"
$remotePayload = "$remoteRoot/build.payload.sh"
$commonSsh = @('-T', '-p', $script:Port, '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10')

if ($Launch) {
    $snapshotFull = [IO.Path]::GetFullPath($SnapshotPath)
    $snapshotSHA = Read-FixedFileSHA256 -Path $snapshotFull
    Assert-SafeServerSnapshot -Path $snapshotFull
    $payloadSHA = Read-FixedFileSHA256 -Path $script:PayloadPath
    if (-not [IO.File]::Exists($script:SshPath) -or -not [IO.File]::Exists($script:ScpPath)) { throw 'openssh_missing' }
    Assert-FixedScpArguments -Arguments $script:ScpCommon

    # 创建命令要求目录此前不存在；同 nonce 的第二次启动会在这里失败，不覆盖任何证据。
    $createCommand = "/usr/bin/test ! -e $remoteRoot && /usr/bin/mkdir -m 700 -- $remoteRoot && /usr/bin/stat -c %U:%a -- $remoteRoot"
    $created = Invoke-FixedOpenSSH -FilePath $script:SshPath -Arguments ($commonSsh + @($script:HostName, $createCommand))
    if ($created -cne 'pc:700') { throw 'remote_directory_invalid' }

    $snapshotUpload = Invoke-FixedOpenSSH -FilePath $script:ScpPath -Arguments ($script:ScpCommon + @($snapshotFull, "${script:HostName}:$remoteRoot/server-snapshot.tar"))
    if ($snapshotUpload.Length -ne 0) { throw 'snapshot_upload_output_invalid' }
    $payloadUpload = Invoke-FixedOpenSSH -FilePath $script:ScpPath -Arguments ($script:ScpCommon + @($script:PayloadPath, "${script:HostName}:$remotePayload"))
    if ($payloadUpload.Length -ne 0) { throw 'payload_upload_output_invalid' }

    # 短 SSH 只启动一次 nohup worker；真正构建与 SSH 生命周期解耦。
    $launchCommand = "/usr/bin/chmod 500 -- $remotePayload && /bin/bash --noprofile --norc $remotePayload --launch $Nonce $payloadSHA $snapshotSHA"
    $started = Invoke-FixedOpenSSH -FilePath $script:SshPath -Arguments ($commonSsh + @($script:HostName, $launchCommand))
    if ($started -cnotmatch '\Astatus=started pid=[1-9][0-9]* starttime=[1-9][0-9]* retained=true repeated=false\z') { throw 'launch_summary_invalid' }
    Write-Output "$started snapshot_sha256=$snapshotSHA payload_sha256=$payloadSHA"
    exit 0
}

# Poll 模式只读固定证据文件；每次调用只建立一次短 SSH，不会触发 --launch 或 --worker。
$polled = Invoke-FixedOpenSSH -FilePath $script:SshPath -Arguments ($commonSsh + @($script:HostName, "/bin/bash --noprofile --norc $remotePayload --poll $Nonce $TimeoutSeconds"))
if ($polled -cnotmatch '\Astatus=(pending reason=running|unknown reason=(partial_evidence|unexpected_output|timeout|worker_missing|pid_reused)|failed stage=(preflight|environment_gate|extract|toolchain|gofmt|unit_test|vet|build|summary)|pass stage=complete binary_sha256=[a-f0-9]{64} binary_size=[1-9][0-9]*) retained=true(?: cleanup_executed=false)?\z') {
    throw 'poll_summary_invalid'
}
Write-Output $polled
