param(
    [string]$ChangeId = "",
    [string]$PlanFile = "",
    [string]$ExpectedPlanSHA256 = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$FixedServerHost = "8.130.9.163"
$FixedSSHPort = 10003
$FixedSSHUser = "pc"
$FixedFingerprint = "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I"

function Assert-LocalFileSystemPathInput {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    # 在任何路径解析前拒绝 UNC、Provider 路径和网络映射盘，确保本地生成不会意外联网。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    $isWindows = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
    if ($isWindows) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') {
            throw "Windows ${Description}必须使用本地盘符绝对路径"
        }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "${Description}不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "${Description}必须使用本地绝对路径"
    }
}

function Test-SyntheticReadonlyState {
    param(
        [int]$NewUserCount,
        [int]$AdminUserCount,
        [int]$DirectAdminRoleCount,
        [int]$AdminPermissionCount
    )

    # 合成状态只覆盖判断规则，不读取数据库、白名单或任何真实号码。
    return ($NewUserCount -eq 0 -and
        $AdminUserCount -eq 1 -and
        $DirectAdminRoleCount -eq 1 -and
        $AdminPermissionCount -eq 1)
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "target_state_readonly_candidate_authorized=false"
    Write-Output "interactive_prompts=0"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "business_posts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) {
    throw "ExportCandidate 与 SelfTest 必须互斥"
}

if ($SelfTest) {
    if ($FixedServerHost -cne "8.130.9.163" -or $FixedSSHPort -ne 10003 -or
        $FixedSSHUser -cne "pc" -or
        $FixedFingerprint -cne "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I") {
        throw "固定 SSH 身份契约发生漂移"
    }
    if (-not (Test-SyntheticReadonlyState -NewUserCount 0 -AdminUserCount 1 -DirectAdminRoleCount 1 -AdminPermissionCount 1)) {
        throw "合成只读状态正例未通过"
    }
    if (Test-SyntheticReadonlyState -NewUserCount 1 -AdminUserCount 1 -DirectAdminRoleCount 1 -AdminPermissionCount 1) {
        throw "合成只读状态反例未被阻断"
    }
    Write-Output "target_state_readonly_candidate_self_test=passed"
    Write-Output "fixed_ssh_identity_contract_frozen=true"
    Write-Output "readonly_state_fixture_verified=true"
    Write-Output "interactive_prompts=0"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "business_posts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "ChangeId 必须使用 UTC 基本格式" }
if ($ExpectedPlanSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "必须提供小写 SHA-256 计划摘要" }
if ([string]::IsNullOrWhiteSpace($PlanFile) -or [string]::IsNullOrWhiteSpace($OutputDirectory)) {
    throw "导出候选必须提供 PlanFile 与全新的 OutputDirectory"
}

Assert-LocalFileSystemPathInput -Path $OutputDirectory -Description "候选输出目录"
Assert-LocalFileSystemPathInput -Path $PlanFile -Description "Canary 计划文件"
$resolvedPlan = (Resolve-Path -LiteralPath $PlanFile -ErrorAction Stop).Path
$actualPlanSHA256 = (Get-FileHash -LiteralPath $resolvedPlan -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualPlanSHA256 -cne $ExpectedPlanSHA256) { throw "Canary 计划摘要不匹配" }

# 复用已经验收的计划校验器，阻止候选脱离原 ChangeId 和双别名 receipt-only 计划。
$planOutput = @(& (Join-Path $PSScriptRoot "verify-sms-phase5-canary-execution-plan.ps1") -PlanFile $resolvedPlan)
if ($planOutput -cnotcontains "canary_execution_plan=passed" -or
    $planOutput -cnotcontains "change_id=$ChangeId" -or
    $planOutput -cnotcontains "acceptance_scope=receipt_only") {
    throw "Canary 计划未通过绑定校验"
}

$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

# 远端负载只读取当前 API 进程环境、用户/IAM 表、白名单和发送计数；号码仅以 stdin 进入内存。
$remotePayload = @'
set -euo pipefail
api_path='/home/pc/molin/molin-api'
mapfile -t api_pids < <(pgrep -f "^${api_path}$" 2>/dev/null || true)
if [ "${#api_pids[@]}" -ne 1 ]; then
  printf 'target_state_readonly_preflight=blocked\n'
  printf 'api_process_count=%s\n' "${#api_pids[@]}"
  exit 2
fi
pid="${api_pids[0]}"

read_env() {
  tr '\0' '\n' < "/proc/$pid/environ" | sed -n "s/^$1=//p" | tail -n 1
}

new_phone="$(printf '%s' "$new_b64" | base64 -d)"
admin_phone="$(printf '%s' "$admin_b64" | base64 -d)"
new_b64=''
admin_b64=''
if ! [[ "$new_phone" =~ ^1[3-9][0-9]{9}$ ]] || ! [[ "$admin_phone" =~ ^1[3-9][0-9]{9}$ ]] || [ "$new_phone" = "$admin_phone" ]; then
  printf 'target_state_readonly_preflight=blocked\n'
  printf 'target_format_and_distinct=false\n'
  exit 2
fi

sms_enabled="$(read_env SMS_ENABLED)"
sms_test_mode="$(read_env SMS_TEST_MODE)"
sms_whitelist="$(read_env SMS_TEST_PHONE_WHITELIST)"
whitelist_env_count="$(tr '\0' '\n' < "/proc/$pid/environ" | grep -c '^SMS_TEST_PHONE_WHITELIST=' || true)"
db_host="$(read_env MYSQL_HOST)"
db_port="$(read_env MYSQL_PORT)"
db_user="$(read_env MYSQL_USER)"
db_pass="$(read_env MYSQL_PASSWORD)"
db_name="$(read_env MYSQL_DATABASE)"

run_mysql_readonly() {
  local statement="$1"
  if command -v mysql >/dev/null 2>&1; then
    printf '%s\n' "$statement" | MYSQL_PWD="$db_pass" mysql --batch --skip-column-names -h "$db_host" -P "${db_port:-3306}" -u "$db_user" "$db_name" 2>/dev/null
    return
  fi
  if command -v docker >/dev/null 2>&1 && docker inspect molin-mysql >/dev/null 2>&1; then
    { printf '%s\n' "$db_pass"; printf '%s\n' "$statement"; } | docker exec -i molin-mysql sh -c '
      IFS= read -r MYSQL_PWD
      export MYSQL_PWD
      exec mysql --batch --skip-column-names -u "$1" "$2"
    ' sh "$db_user" "$db_name" 2>/dev/null
    return
  fi
  return 127
}

to_hex() {
  printf '%s' "$1" | od -An -tx1 | tr -d ' \n'
}
new_hex="$(to_hex "$new_phone")"
admin_hex="$(to_hex "$admin_phone")"
new_user_count="$(run_mysql_readonly "SELECT COUNT(*) FROM users WHERE phone=CONVERT(0x${new_hex} USING utf8mb4);")"
admin_user_count="$(run_mysql_readonly "SELECT COUNT(*) FROM users WHERE phone=CONVERT(0x${admin_hex} USING utf8mb4) AND status='active' AND phone_verified=1;")"
direct_admin_count="$(run_mysql_readonly "SELECT COUNT(DISTINCT u.id) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.phone=CONVERT(0x${admin_hex} USING utf8mb4) AND u.status='active' AND u.phone_verified=1 AND r.code='admin';")"
admin_permission_count="$(run_mysql_readonly "SELECT COUNT(DISTINCT u.id) FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id JOIN role_permissions rp ON rp.role_id=r.id JOIN permissions p ON p.id=rp.permission_id WHERE u.phone=CONVERT(0x${admin_hex} USING utf8mb4) AND r.code='admin' AND p.code='user:manage';")"
send_count_before="$(run_mysql_readonly 'SELECT COUNT(*) FROM sms_send_logs;')"

whitelist_has() {
  local expected="$1"
  local item
  IFS=',' read -r -a items <<<"$sms_whitelist"
  for item in "${items[@]}"; do
    item="${item#"${item%%[![:space:]]*}"}"
    item="${item%"${item##*[![:space:]]}"}"
    if [ "$item" = "$expected" ]; then return 0; fi
  done
  return 1
}

new_whitelisted=false
admin_whitelisted=false
if whitelist_has "$new_phone"; then new_whitelisted=true; fi
if whitelist_has "$admin_phone"; then admin_whitelisted=true; fi
whitelist_read_verified=false
whitelist_targets_ready=false
whitelist_verified=false
[ "$whitelist_env_count" = 1 ] && whitelist_read_verified=true
if [ "$new_whitelisted" = true ] && [ "$admin_whitelisted" = true ]; then whitelist_targets_ready=true; fi
if [ "$whitelist_read_verified" = true ] && [ "$whitelist_targets_ready" = true ]; then whitelist_verified=true; fi
send_count_after="$(run_mysql_readonly 'SELECT COUNT(*) FROM sms_send_logs;')"

new_unregistered=false
admin_registered_verified=false
direct_admin_role_verified=false
admin_permission_verified=false
send_log_delta_zero=false
[ "$new_user_count" = 0 ] && new_unregistered=true
[ "$admin_user_count" = 1 ] && admin_registered_verified=true
[ "$direct_admin_count" = 1 ] && direct_admin_role_verified=true
[ "$admin_permission_count" = 1 ] && admin_permission_verified=true
[ -n "$send_count_before" ] && [ "$send_count_before" = "$send_count_after" ] && send_log_delta_zero=true

state_ready=false
if [ "$sms_enabled" = false ] && [ "$sms_test_mode" = true ] && [ "$new_unregistered" = true ] && [ "$admin_registered_verified" = true ] && [ "$direct_admin_role_verified" = true ] && [ "$admin_permission_verified" = true ] && [ "$whitelist_verified" = true ] && [ "$send_log_delta_zero" = true ]; then
  state_ready=true
fi

printf 'target_state_readonly_preflight=%s\n' "$([ "$state_ready" = true ] && printf passed || printf blocked)"
printf 'target_aliases=target-new,target-admin\n'
printf 'sms_enabled=%s\n' "$sms_enabled"
printf 'sms_test_mode=%s\n' "$sms_test_mode"
printf 'target_new_unregistered=%s\n' "$new_unregistered"
printf 'target_admin_registered_verified=%s\n' "$admin_registered_verified"
printf 'direct_admin_role_verified=%s\n' "$direct_admin_role_verified"
printf 'admin_user_manage_permission_verified=%s\n' "$admin_permission_verified"
printf 'target_new_whitelisted=%s\n' "$new_whitelisted"
printf 'target_admin_whitelisted=%s\n' "$admin_whitelisted"
printf 'whitelist_read_verified=%s\n' "$whitelist_read_verified"
printf 'whitelist_targets_ready=%s\n' "$whitelist_targets_ready"
printf 'whitelist_verified=%s\n' "$whitelist_verified"
printf 'send_log_delta_zero=%s\n' "$send_log_delta_zero"
printf 'business_configuration_mutations=0\n'
printf 'business_posts=0\n'
printf 'uploads=0\n'
printf 'sms_submission_requests=0\n'
new_phone=''
admin_phone=''
new_hex=''
admin_hex=''
db_pass=''
[ "$state_ready" = true ] || exit 3
'@
$remotePayloadBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($remotePayload))

$runnerTemplate = @'
param(
    [switch]$ExecuteReadOnly,
    [string]$ApprovalToken = "",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"
$PlanSHA256 = "__PLAN_SHA256__"
$RemotePayloadBase64 = "__REMOTE_PAYLOAD_BASE64__"
$ExpectedApprovalToken = "APPROVE_SMS_PHASE5_TARGET_STATE_READONLY___CHANGE_ID__"
$ServerHost = "__SERVER_HOST__"
$SSHPort = __SSH_PORT__
$SSHUser = "__SSH_USER__"
$ExpectedFingerprint = "__SSH_FINGERPRINT__"

function Assert-TargetPair {
    param([string]$NewTarget, [string]$AdminTarget)
    if ($NewTarget -cnotmatch '^1[3-9]\d{9}$' -or $AdminTarget -cnotmatch '^1[3-9]\d{9}$') {
        throw "目标号码格式无效"
    }
    if ($NewTarget -ceq $AdminTarget) { throw "两个目标号码必须互异" }
}

function Read-HiddenTarget {
    param([Parameter(Mandatory = $true)][string]$Prompt)
    $secureValue = Read-Host -Prompt $Prompt -AsSecureString
    $pointer = [IntPtr]::Zero
    try {
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureValue)
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    }
    finally {
        if ($pointer -ne [IntPtr]::Zero) { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer) }
        if ($null -ne $secureValue) { $secureValue.Dispose() }
    }
}

function Assert-FixedTarget {
    if ($ServerHost -cne "8.130.9.163" -or $SSHPort -ne 10003 -or $SSHUser -cne "pc" -or
        $ExpectedFingerprint -cne "SHA256:q5xYBX+tB+VPPCSTYFN6GTIbdn4sPicQslLLbkxRG+I") {
        throw "SSH 身份契约不是固定阶段 5 测试服"
    }
}

function Get-VerifiedKnownHosts {
    Assert-FixedTarget
    $knownHosts = [IO.Path]::GetFullPath((Join-Path $env:USERPROFILE ".ssh\known_hosts"))
    if (-not (Test-Path -LiteralPath $knownHosts -PathType Leaf) -or ([IO.FileInfo]$knownHosts).Attributes.HasFlag([IO.FileAttributes]::ReparsePoint)) {
        throw "固定 known_hosts 不存在或属于重解析路径"
    }
    $lines = @(& ssh-keygen.exe -F "[8.130.9.163]:10003" -f $knownHosts)
    if ($LASTEXITCODE -ne 0) { throw "known_hosts 缺少固定测试服身份" }
    $keys = @()
    foreach ($line in $lines) {
        $parts = @(($line.Trim()) -split '\s+')
        if ($parts.Count -ge 3 -and $parts[1] -ceq "ssh-ed25519") { $keys += $parts[2] }
    }
    if ($keys.Count -ne 1) { throw "固定测试服 ED25519 公钥数量异常" }
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        $fingerprint = "SHA256:" + [Convert]::ToBase64String($sha256.ComputeHash([Convert]::FromBase64String($keys[0]))).TrimEnd('=')
    }
    finally { $sha256.Dispose() }
    if ($fingerprint -cne $ExpectedFingerprint) { throw "固定测试服 ED25519 指纹不匹配" }
    return $knownHosts
}

function Invoke-FixedSshReadonlyScript {
    param(
        [Parameter(Mandatory = $true)][string]$KnownHosts,
        [Parameter(Mandatory = $true)][string]$Script
    )

    # 使用进程标准输入直接传送 LF/无 BOM 脚本，避免 PowerShell 到 SSH 的参数重组吞掉 Bash 换行。
    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = "ssh.exe"
    $startInfo.Arguments = "-p $SSHPort -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o `"UserKnownHostsFile=$KnownHosts`" -- ${SSHUser}@${ServerHost} bash -s"
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    $startInfo.StandardOutputEncoding = $utf8NoBom
    $startInfo.StandardErrorEncoding = $utf8NoBom

    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $startInfo
    try {
        if (-not $process.Start()) { throw "无法启动固定 SSH 只读进程" }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        # 号码的 Base64 值只存在于该内存字符串与 SSH stdin，不写文件、不进入远端进程参数。
        $normalizedScript = $Script.Replace("`r`n", "`n").Replace("`r", "`n")
        $inputBytes = $utf8NoBom.GetBytes($normalizedScript)
        try {
            # Windows PowerShell 5 没有 StandardInputEncoding，直接写底层字节流才能冻结 LF/无 BOM 契约。
            $process.StandardInput.BaseStream.Write($inputBytes, 0, $inputBytes.Length)
            $process.StandardInput.BaseStream.Flush()
        }
        finally {
            if ($null -ne $inputBytes) { [Array]::Clear($inputBytes, 0, $inputBytes.Length) }
            $inputBytes = $null
            $normalizedScript = $null
            $process.StandardInput.Close()
        }
        $process.WaitForExit()
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        return [PSCustomObject]@{
            ExitCode = $process.ExitCode
            Output = @($stdout -split "`r?`n" | Where-Object { $_ -ne "" })
            StderrPresent = -not [string]::IsNullOrWhiteSpace($stderr)
        }
    }
    finally {
        $process.Dispose()
    }
}

if (-not $ExecuteReadOnly -and -not $SelfTest) {
    Write-Output "target_state_change_id=$ChangeId"
    Write-Output "plan_sha256=$PlanSHA256"
    Write-Output "readonly_execution_authorized=false"
    Write-Output "interactive_prompts=0"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "business_posts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExecuteReadOnly -and $SelfTest) { throw "ExecuteReadOnly 与 SelfTest 必须互斥" }

if ($SelfTest) {
    Assert-FixedTarget
    $syntheticNew = "1" + "38" + ("0" * 8)
    $syntheticAdmin = "1" + "39" + ("0" * 8)
    Assert-TargetPair -NewTarget $syntheticNew -AdminTarget $syntheticAdmin
    if ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64)) -cnotmatch 'SELECT COUNT') {
        throw "内嵌只读负载不完整"
    }
    $syntheticNew = $null
    $syntheticAdmin = $null
    Write-Output "target_state_readonly_runner_self_test=passed"
    Write-Output "fixed_ssh_identity_contract_frozen=true"
    Write-Output "readonly_payload_verified=true"
    Write-Output "interactive_prompts=0"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "business_posts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ApprovalToken -cne $ExpectedApprovalToken) { throw "只读执行授权口令不匹配" }
$knownHosts = Get-VerifiedKnownHosts
$newTarget = $null
$adminTarget = $null
try {
    $newTarget = Read-HiddenTarget -Prompt "请输入 target-new（隐藏输入）"
    $adminTarget = Read-HiddenTarget -Prompt "请输入 target-admin（隐藏输入）"
    Assert-TargetPair -NewTarget $newTarget -AdminTarget $adminTarget
    $newBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($newTarget))
    $adminBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($adminTarget))
    $remotePayload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    # Base64 字符集不含单引号，可安全放入只存在于内存和 stdin 的 Bash 变量赋值。
    $remoteExecutionScript = "new_b64='$newBase64'`nadmin_b64='$adminBase64'`n$remotePayload`n"
    $remoteResult = Invoke-FixedSshReadonlyScript -KnownHosts $knownHosts -Script $remoteExecutionScript
    $remoteOutput = @($remoteResult.Output)
    $readonlyExitCode = $remoteResult.ExitCode
    # 失败关闭前先输出远端已经返回的低敏布尔结果，避免丢失实际阻断原因。
    $remoteOutput | Write-Output
    Write-Output "interactive_prompts=2"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=1"
    Write-Output "uploads=0"
    Write-Output "business_posts=0"
    Write-Output "real_sms_sent=0"
    Write-Output "remote_stderr_present=$($remoteResult.StderrPresent.ToString().ToLowerInvariant())"
    Write-Output "readonly_exit_code=$readonlyExitCode"
    if ($readonlyExitCode -ne 0) { throw "固定测试服只读状态预检未通过，退出码：$readonlyExitCode" }
}
finally {
    # 托管字符串无法保证物理清零，因此只缩短引用生命周期，并且从不输出或写入号码。
    $newTarget = $null
    $adminTarget = $null
    $newBase64 = $null
    $adminBase64 = $null
    $remotePayload = $null
    $remoteExecutionScript = $null
}
'@

$runner = $runnerTemplate.Replace("__CHANGE_ID__", $ChangeId).
    Replace("__PLAN_SHA256__", $ExpectedPlanSHA256).
    Replace("__REMOTE_PAYLOAD_BASE64__", $remotePayloadBase64).
    Replace("__SERVER_HOST__", $FixedServerHost).
    Replace("__SSH_PORT__", [string]$FixedSSHPort).
    Replace("__SSH_USER__", $FixedSSHUser).
    Replace("__SSH_FINGERPRINT__", $FixedFingerprint)
$runnerPath = Join-Path $outputPath "run-sms-phase5-canary-target-state-readonly-$ChangeId.ps1"
$directoryCreated = $false
$fileCreated = $false
try {
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runner, (New-Object Text.UTF8Encoding($true)))
    $fileCreated = $true

    # 仅执行语法、默认关闭和合成数据自测；本次绝不进入 ExecuteReadOnly 分支。
    $tokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) {
        $parseSummary = (@($parseErrors) | ForEach-Object { "第 $($_.Extent.StartLineNumber) 行：$($_.Message)" }) -join "；"
        throw "runner PowerShell 语法校验失败：$parseSummary"
    }
    $decodedPayload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($remotePayloadBase64))
    if ($decodedPayload -match '(?im)^\s*(?:INSERT|UPDATE|DELETE|REPLACE|ALTER|DROP|TRUNCATE|CREATE)\b' -or
        $decodedPayload -notmatch 'SELECT COUNT' -or
        $decodedPayload -notmatch 'SMS_TEST_PHONE_WHITELIST') {
        throw "远端负载未通过只读 SQL 静态门禁"
    }
    $closedOutput = @(& $runnerPath)
    $selfTestOutput = @(& $runnerPath -SelfTest)
    if ($closedOutput -cnotcontains "readonly_execution_authorized=false" -or
        $selfTestOutput -cnotcontains "target_state_readonly_runner_self_test=passed") {
        throw "runner 默认关闭或离线自测失败"
    }

    $runnerSHA256 = (Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "target_state_readonly_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "plan_sha256=$ExpectedPlanSHA256"
    Write-Output "runner_sha256=$runnerSHA256"
    Write-Output "runner_path=$runnerPath"
    Write-Output "candidate_files_written=1"
    Write-Output "interactive_prompts=0"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "uploads=0"
    Write-Output "business_posts=0"
    Write-Output "real_sms_sent=0"
}
catch {
    # 失败时只删除本次精确文件及已确认空的目录，不做递归清理。
    if ($fileCreated -and (Test-Path -LiteralPath $runnerPath -PathType Leaf)) {
        Remove-Item -LiteralPath $runnerPath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
