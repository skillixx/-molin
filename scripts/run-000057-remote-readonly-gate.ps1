[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

# 最终结果只包含安全状态，不包含路径、密钥或远端原始输出。
$result = [ordered]@{
    status = 'not_started'
    ssh_attempt_count = 0
    ssh_completed_count = 0
    exit_code = $null
    stdout_exact = $false
    stderr_category = 'not_applicable'
    cleanup_ok = $true
    error_category = 'none'
}

$stdoutTemp = $null
$stderrTemp = $null
$phase = 'confirmation'

try {
    # 必须显式输入固定确认短语，确认失败时不会进入 SSH 调用。
    if ($Confirm -cne 'I_CONFIRM_000057_REMOTE_READONLY_GATE_ONCE') {
        $result.status = 'confirmation_required'
        throw 'CONFIRMATION_REQUIRED'
    }

    # 仅接受固定系统 SSH 文件，并拒绝目录或重解析点。
    $phase = 'local_tool'
    $sshExe = 'C:\Windows\System32\OpenSSH\ssh.exe'
    if (-not (Test-Path -LiteralPath $sshExe -PathType Leaf)) {
        throw 'LOCAL_TOOL_FAILED'
    }
    $sshItem = Get-Item -LiteralPath $sshExe -Force
    if (($sshItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw 'LOCAL_TOOL_FAILED'
    }

    # 固定 ASCII 远端脚本只检查进程、健康状态和文件元数据，不读取六个控制文件内容。
    $phase = 'payload'
    $payloadLines = @(
        'set -Eeuo pipefail',
        'export PATH=/usr/sbin:/usr/bin:/sbin:/bin',
        '[[ ${EUID} -eq "$(/usr/bin/id -u pc)" ]]',
        'for tool in /usr/bin/stat /usr/bin/realpath /usr/bin/pgrep /usr/bin/curl; do',
        '  [[ -x "$tool" ]]',
        'done',
        'mapfile -t api_pids < <(/usr/bin/pgrep -x molin-api)',
        '(( ${#api_pids[@]} == 1 ))',
        '/usr/bin/curl --fail --silent --show-error --max-time 5 --output /dev/null http://127.0.0.1:8080/api/health',
        'check_root_dir() {',
        '  local path="$1" mode',
        '  [[ -d "$path" && ! -L "$path" ]]',
        '  [[ "$(/usr/bin/realpath -e -- "$path")" == "$path" ]]',
        '  [[ "$(/usr/bin/stat -c %u -- "$path")" == 0 ]]',
        '  mode="$(/usr/bin/stat -c %a -- "$path")"',
        '  (( (8#$mode & 0022) == 0 ))',
        '}',
        'check_root_dir /opt',
        'check_root_dir /opt/molin-maintenance',
        'control_dir=/opt/molin-maintenance/000057_reverify_c7b7bf363dfbd214',
        'check_root_dir "$control_dir"',
        'for name in isolation_schema isolation_user isolation_password isolation_account_host restore_completed restore_aggregate.tsv; do',
        '  file="$control_dir/$name"',
        '  [[ -f "$file" && ! -L "$file" ]]',
        '  [[ "$(/usr/bin/realpath -e -- "$file")" == "$file" ]]',
        '  [[ "$(/usr/bin/stat -c %u -- "$file")" == 0 ]]',
        '  [[ "$(/usr/bin/stat -c %a -- "$file")" == 600 ]]',
        'done',
        'runtime_dir=/home/pc/molin-runtime/000057_reverify_c7b7bf363dfbd214',
        '[[ -d "$runtime_dir" && ! -L "$runtime_dir" ]]',
        '[[ "$(/usr/bin/realpath -e -- "$runtime_dir")" == "$runtime_dir" ]]',
        'pc_uid="$(/usr/bin/id -u pc)"',
        '[[ "$(/usr/bin/stat -c %u -- "$runtime_dir")" == "$pc_uid" ]]',
        '[[ "$(/usr/bin/stat -c %a -- "$runtime_dir")" == 700 ]]',
        '[[ ! -e "$runtime_dir/cycle_started" ]]',
        '[[ ! -e "$runtime_dir/cycle_completed" ]]',
        "printf 'molin_000057_remote_readonly_gate_v1=PASS\\n'"
    )
    $payload = [string]::Join("`n", $payloadLines)
    $ascii = [Text.Encoding]::ASCII
    if ($ascii.GetString($ascii.GetBytes($payload)) -cne $payload) {
        throw 'PAYLOAD_FAILED'
    }

    # 参数使用当前用户现有 SSH 配置和默认身份，只固定测试服务器连接与非交互门禁。
    $sshArgs = @(
        '-p', '10003',
        '-o', 'StrictHostKeyChecking=yes',
        '-o', 'BatchMode=yes',
        '-o', 'ConnectTimeout=10',
        'pc@8.130.9.163',
        '/usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/home/pc USER=pc LOGNAME=pc LANG=C.UTF-8 /bin/bash --noprofile --norc -s --'
    )

    # 两个本地临时文件只承接本轮标准流，最终按精确路径分别清理。
    $phase = 'temp_files'
    $stdoutTemp = [IO.Path]::GetTempFileName()
    $stderrTemp = [IO.Path]::GetTempFileName()

    # 这是脚本中唯一的 SSH 调用点，调用前记录尝试且绝不重试。
    $phase = 'ssh'
    $result.ssh_attempt_count = 1
    $previousErrorActionPreference = $ErrorActionPreference
    $nativeExitCode = $null
    $nativeReturned = $false
    try {
        # Windows PowerShell 5 可能把原生命令标准错误提升为错误记录，临时使用 Continue 以保留退出码和捕获文件。
        $ErrorActionPreference = 'Continue'
        $payload | & $sshExe @sshArgs 1> $stdoutTemp 2> $stderrTemp
        $nativeExitCode = $LASTEXITCODE
        $nativeReturned = $true
    }
    finally {
        # 无论原生命令是否抛出异常，都恢复调用前的全局错误策略。
        $ErrorActionPreference = $previousErrorActionPreference
    }
    if (-not $nativeReturned) {
        throw 'SSH_NATIVE_CALL_FAILED'
    }
    $result.exit_code = $nativeExitCode
    $result.ssh_completed_count = 1

    # 原始标准流仅在内存中比较和分类，绝不直接输出。
    $phase = 'capture'
    $stdoutText = [IO.File]::ReadAllText($stdoutTemp)
    $stderrText = [IO.File]::ReadAllText($stderrTemp)
    $expectedLf = "molin_000057_remote_readonly_gate_v1=PASS`n"
    $expectedCrLf = "molin_000057_remote_readonly_gate_v1=PASS`r`n"
    $result.stdout_exact = ($stdoutText -ceq $expectedLf -or $stdoutText -ceq $expectedCrLf)

    if ($stderrText.Length -eq 0) {
        $result.stderr_category = 'empty'
    }
    elseif ($stderrText -match '(?i)host key|remote host identification') {
        $result.stderr_category = 'host_key'
    }
    elseif ($stderrText -match '(?i)permission denied|authentication|no supported authentication') {
        $result.stderr_category = 'auth'
    }
    elseif ($stderrText -match '(?i)timed out|connection|resolve hostname|network|socket') {
        $result.stderr_category = 'network'
    }
    elseif ($stderrText -match '(?i)sudo:|password is required|not allowed to run') {
        $result.stderr_category = 'sudo'
    }
    else {
        $result.stderr_category = 'unknown'
    }

    if ($result.exit_code -eq 0 -and $result.stdout_exact -and
        $result.stderr_category -ceq 'empty' -and $result.cleanup_ok) {
        $result.status = 'pass'
    }
    else {
        $result.status = 'failed'
    }
}
catch {
    # 所有异常仅映射为固定安全类别，不读取或输出异常消息。
    if ($result.status -ceq 'confirmation_required') {
        $result.error_category = 'confirmation'
    }
    else {
        $result.status = 'failed'
        $result.error_category = $phase
    }
}
finally {
    # 每个临时文件只尝试一次精确删除；失败时不扩大删除范围。
    foreach ($tempPath in @($stdoutTemp, $stderrTemp)) {
        if ($null -ne $tempPath) {
            try {
                if (Test-Path -LiteralPath $tempPath) {
                    if (-not (Test-Path -LiteralPath $tempPath -PathType Leaf)) {
                        throw 'CLEANUP_TYPE_FAILED'
                    }
                    Remove-Item -LiteralPath $tempPath -Force -ErrorAction Stop
                    if (Test-Path -LiteralPath $tempPath) {
                        throw 'CLEANUP_VERIFY_FAILED'
                    }
                }
            }
            catch {
                $result.cleanup_ok = $false
                $result.status = 'failed'
                $result.error_category = 'cleanup'
            }
        }
    }
}

# 最终只输出一行紧凑 JSON。
$result | ConvertTo-Json -Compress
