[CmdletBinding()]
param(
    [Parameter(Mandatory = $false)]
    [string]$Confirm,

    [Parameter(Mandatory = $false)]
    [switch]$SelfTest,

    [Parameter(Mandatory = $false)]
    [switch]$LocalPreflightOnly,

    [Parameter(Mandatory = $false)]
    [switch]$MetadataDiagnosticOnly
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$script:RequiredPhrase = 'I_CONFIRM_AUTHORIZED_EMAIL_UNKNOWN_HISTORY_FLOW_ONCE'
$script:MetadataDiagnosticPhrase = 'I_CONFIRM_EMAIL_UNKNOWN_METADATA_READONLY_DIAGNOSTIC_ONCE'
$script:CleanupBinarySHA256 = 'd211268ec0b13e5c92ba1992d41b98f4a0c3415ae4fd348deb9ac843614854a4'
$script:CycleDumpSHA256 = @(
    'd6696c7e0b76952a04cedc6ee7212ceed098c4f9ef6ab276b082560f74fb479e',
    '9e1242742fe1fbbc44e8abe4ab9b0ac8f2d2be1071a6e2f8c843ff1d1a2a6dbc'
)
$script:MetadataPattern = '^status=pass recovery_filename=(?<recovery>molin-email-unknown-[a-f0-9]{32}\.sql) recovery_sha256=(?<recovery_sha>[a-f0-9]{64}) cycle_sha256_one=(?<cycle_one>[a-f0-9]{64}) cycle_sha256_two=(?<cycle_two>[a-f0-9]{64})\r?\n?\z'
$script:CleanupSuccessPattern = '^status=pass preflight_schema=57 preflight_dirty=false state_phase=phase1_created fixture_logs=2 fixture_allowlist=1 fixture_template=1 redis_key_preexisting=false cleanup_binary_launches=1 cleanup_db_logs=2 cleanup_allowlist=1 cleanup_template=1 redis_key_untouched=true state_removed=true backup_retained=true cycle_assets_retained=2 retries=0\r?\n?\z'
$script:PostcheckSuccessPattern = '^status=pass api_health=true api_ready=true schema=57 dirty=false fixture_logs_absent=2 scope_rows=0 allowlist_absent=1 template_absent=1 redis_ping=true redis_key_absent=true recovery_mode=600 recovery_sha256_valid=true cleanup_binary_sha256_valid=true cycle_evidence_count=2 cycle_schema_count=2 state_dependency=false writes=false restarts=false retries=0\r?\n?\z'
$script:PostcheckFailureClassifications = @(
    'confirmation_required', 'payload_missing', 'payload_path_invalid', 'payload_bom_or_nul', 'payload_first_line',
    'payload_encoding', 'payload_placeholder_invalid', 'recovery_filename_invalid', 'binary_sha_invalid',
    'recovery_sha_invalid', 'cycle_dump_sha_invalid', 'ssh_tool_missing', 'temp_path_invalid', 'temp_path_unsafe',
    'temp_file_invalid', 'temp_file_mismatch', 'temp_file_unsafe', 'process_timeout', 'local_gate_failed',
    'remote_stderr_nonempty', 'remote_gate_failed', 'remote_exit_nonzero', 'remote_output_invalid'
)
$script:PostcheckRemoteStages = @(
    'shell_options', 'api_identity', 'api_environment', 'health_transport', 'health_json', 'ready_transport',
    'ready_json', 'required_environment', 'container_identity', 'recovery_gate', 'recovery_identity', 'identity_json',
    'schema_query', 'schema_gate', 'fixture_query', 'fixture_absence', 'redis_ping', 'redis_exists', 'binary_gate',
    'cycle_metadata', 'cycle_schema', 'final_artifacts'
)

function ConvertTo-Utf8PayloadBytes {
    param([Parameter(Mandatory = $true)][string]$Payload)

    # 元数据 payload 与正式 runner 一样使用 LF、无 BOM UTF-8，避免首行严格模式被隐藏字节破坏。
    $normalized = $Payload.Replace("`r`n", "`n").Replace("`r", "`n")
    if ($normalized.Length -eq 0 -or [int][char]$normalized[0] -in @(0xFEFF, 0xFFFE) -or
        $normalized.IndexOf([char]0) -ge 0 -or
        -not $normalized.StartsWith("set -Eeuo pipefail`n", [StringComparison]::Ordinal)) {
        throw 'payload_encoding_invalid'
    }
    $bytes = (New-Object Text.UTF8Encoding($false, $true)).GetBytes($normalized)
    if ($bytes.Length -lt 4 -or ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)) {
        throw 'payload_encoding_invalid'
    }
    return ,$bytes
}

function New-RestrictedTempDirectory {
    # 临时目录关闭 ACL 继承并只授权当前 Windows 身份，保护尚未打印的恢复点文件名和摘要。
    $root = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd([IO.Path]::DirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
    $path = [IO.Path]::GetFullPath((Join-Path $root ('molin-email-authorized-flow-' + [Guid]::NewGuid().ToString('N'))))
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
        $verified = [IO.Directory]::GetAccessControl($path)
        $rules = @($verified.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]))
        $item = [IO.DirectoryInfo]::new($path)
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.FullName -cne $path -or
            -not $verified.AreAccessRulesProtected -or
            $verified.GetOwner([Security.Principal.SecurityIdentifier]).Value -cne $sid.Value -or
            $rules.Count -ne 1 -or $rules[0].IdentityReference.Value -cne $sid.Value -or
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
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.File]::Exists($Path) -or [IO.Directory]::Exists($Path)) { throw 'temp_file_invalid' }
    [IO.File]::WriteAllBytes($Path, $Bytes)
    $item = [IO.FileInfo]::new($Path)
    $actual = [IO.File]::ReadAllBytes($Path)
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $actual.Length -ne $Bytes.Length) { throw 'temp_file_invalid' }
    for ($index = 0; $index -lt $Bytes.Length; $index++) {
        if ($actual[$index] -ne $Bytes[$index]) { throw 'temp_file_invalid' }
    }
}

function Remove-RestrictedTempDirectory {
    param([Parameter(Mandatory = $true)][string]$Path)

    # 只删除本轮创建的随机叶目录及九个固定文件，不递归删除，也不接受额外文件。
    if (-not [IO.Path]::IsPathRooted($Path) -or [IO.Path]::GetFileName($Path) -cnotmatch '^molin-email-authorized-flow-[a-f0-9]{32}$') {
        throw 'temp_cleanup_path_invalid'
    }
    foreach ($name in @(
        'metadata.stdin', 'metadata.stdout', 'metadata.stderr',
        'cleanup.stdin', 'cleanup.stdout', 'cleanup.stderr',
        'postcheck.stdin', 'postcheck.stdout', 'postcheck.stderr',
        'cycle-sort-0.sh', 'cycle-sort-1.sh', 'cycle-sort-wrong.sh'
    )) {
        $file = Join-Path $Path $name
        if ([IO.File]::Exists($file)) {
            $item = [IO.FileInfo]::new($file)
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $item.DirectoryName -cne $Path) { throw 'temp_cleanup_file_invalid' }
            [IO.File]::Delete($file)
        }
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

function Get-MetadataPayload {
    # 远端脚本只做 find/stat/sha256sum 与 docker 只读查询；冻结结果只写入受限 stdout 临时文件。
    return @'
set -Eeuo pipefail
expected_binary_sha=d211268ec0b13e5c92ba1992d41b98f4a0c3415ae4fd348deb9ac843614854a4
expected_cycle_shas=(9e1242742fe1fbbc44e8abe4ab9b0ac8f2d2be1071a6e2f8c843ff1d1a2a6dbc d6696c7e0b76952a04cedc6ee7212ceed098c4f9ef6ab276b082560f74fb479e)
# SELFTEST_CYCLE_SHA_SET_BEGIN
sort_and_compare_cycle_sha_sets() {
  mapfile -t expected_sorted < <(/usr/bin/printf '%s\n' "${expected_cycle_shas[@]}" | /usr/bin/sort)
  mapfile -t actual_sorted < <(/usr/bin/printf '%s\n' "${cycle_shas[@]}" | /usr/bin/sort)
  [[ "${actual_sorted[0]}" == "${expected_sorted[0]}" && "${actual_sorted[1]}" == "${expected_sorted[1]}" ]]
}

# SELFTEST_CYCLE_SHA_SET_END
mapfile -t state_candidates < <(/usr/bin/find /home/pc -mindepth 1 -maxdepth 1 -name 'molin-email-unknown-*.state' -print)
(( ${#state_candidates[@]} == 1 ))
state_file=${state_candidates[0]}
[[ "$state_file" =~ ^/home/pc/molin-email-unknown-([a-f0-9]{32})\.state$ ]]
operation_nonce=${BASH_REMATCH[1]}
[[ -f "$state_file" && ! -L "$state_file" ]]
state_identity=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$state_file")
[[ "$state_identity" =~ ^$(/usr/bin/id -u):600:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
recovery_file="/home/pc/molin/rollback/molin-email-unknown-${operation_nonce}.sql"
recovery_name="molin-email-unknown-${operation_nonce}.sql"
[[ -f "$recovery_file" && ! -L "$recovery_file" ]]
recovery_identity=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$recovery_file")
[[ "$recovery_identity" =~ ^$(/usr/bin/id -u):600:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
recovery_sha=$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')
[[ "$recovery_sha" =~ ^[a-f0-9]{64}$ && "$recovery_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
cleanup_binary=/home/pc/molin/rollback/email-unknown-restart-cleanup.test
[[ -f "$cleanup_binary" && ! -L "$cleanup_binary" ]]
binary_identity=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cleanup_binary")
[[ "$binary_identity" =~ ^$(/usr/bin/id -u):500:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
[[ "$(/usr/bin/sha256sum -- "$cleanup_binary" | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]]
mapfile -t container_lines < <(/usr/bin/docker ps --format '{{.ID}}|{{.Image}}|{{.Names}}')
mysql_ids=()
for container_line in "${container_lines[@]}"; do
  container_id=${container_line%%|*}
  container_identity=${container_line#*|}
  container_identity=${container_identity,,}
  # 每个运行中容器都必须返回规范 ID；MySQL 只按冻结的镜像与容器名身份归类。
  [[ "$container_id" =~ ^[a-f0-9]{12,64}$ ]]
  case "$container_identity" in
    *mysql*) mysql_ids+=("$container_id") ;;
  esac
done
(( ${#mysql_ids[@]} == 1 ))
mysql_id=${mysql_ids[0]}
mapfile -t cycle_markers < <(/usr/bin/docker exec "$mysql_id" /usr/bin/find /root -mindepth 3 -maxdepth 3 -type f -path '/root/molin-000057-schema57-cycle-run-*/evidence/cycle_completed' -print)
(( ${#cycle_markers[@]} == 2 ))
cycle_dumps=()
cycle_identities=()
cycle_shas=()
for marker in "${cycle_markers[@]}"; do
  [[ "$marker" =~ ^(/root/molin-000057-schema57-cycle-run-[a-f0-9]{32})/evidence/cycle_completed$ ]]
  cycle_dump="${BASH_REMATCH[1]}/evidence/molin_source_schema57.sql"
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type f -print)" == "$cycle_dump" ]]
  cycle_identity=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cycle_dump")
  [[ "$cycle_identity" =~ ^0:600:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
  cycle_sha=$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "$cycle_dump" | /usr/bin/awk '{print $1}')
  [[ "$cycle_sha" =~ ^[a-f0-9]{64}$ ]]
  cycle_dumps+=("$cycle_dump")
  cycle_identities+=("$cycle_identity")
  cycle_shas+=("$cycle_sha")
done
sort_and_compare_cycle_sha_sets
# 输出前再次核对所有身份和摘要，避免只读冻结窗口内发生替换。
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$state_file")" == "$state_identity" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$recovery_file")" == "$recovery_identity" ]]
[[ "$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')" == "$recovery_sha" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cleanup_binary")" == "$binary_identity" ]]
[[ "$(/usr/bin/sha256sum -- "$cleanup_binary" | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]]
for index in 0 1; do
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "${cycle_dumps[$index]}")" == "${cycle_identities[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "${cycle_dumps[$index]}" | /usr/bin/awk '{print $1}')" == "${cycle_shas[$index]}" ]]
done
/usr/bin/printf 'status=pass recovery_filename=%s recovery_sha256=%s cycle_sha256_one=%s cycle_sha256_two=%s\n' "$recovery_name" "$recovery_sha" "${actual_sorted[0]}" "${actual_sorted[1]}"
'@
}

function Get-MetadataDiagnosticPayload {
    # 该诊断只比较 metadata 独有资产与已通过 preflight 的容器发现差异，不读取数据库或 Redis。
    return @'
set -Eeuo pipefail
exec 2>/dev/null
expected_binary_sha=d211268ec0b13e5c92ba1992d41b98f4a0c3415ae4fd348deb9ac843614854a4
expected_cycle_shas=(9e1242742fe1fbbc44e8abe4ab9b0ac8f2d2be1071a6e2f8c843ff1d1a2a6dbc d6696c7e0b76952a04cedc6ee7212ceed098c4f9ef6ab276b082560f74fb479e)
stage=state_count
fail() {
  local failed_stage=$stage
  trap - ERR
  /usr/bin/printf 'status=failed stage=%s state_count=%s mysql_identity_count=%s mysql_compose_label_count=%s cycle_marker_count=%s cycle_dump_valid_count=%s writes=false cleanup=false restarts=false retries=0\n' \
    "$failed_stage" "${state_count:-0}" "${mysql_identity_count:-0}" "${mysql_compose_label_count:-0}" "${cycle_marker_count:-0}" "${cycle_dump_valid_count:-0}"
  exit 2
}
trap fail ERR
mapfile -t state_candidates < <(/usr/bin/find /home/pc -mindepth 1 -maxdepth 1 -name 'molin-email-unknown-*.state' -print)
state_count=${#state_candidates[@]}
(( state_count == 1 ))
state_file=${state_candidates[0]}
stage=state_safe
[[ "$state_file" =~ ^/home/pc/molin-email-unknown-([a-f0-9]{32})\.state$ ]]
operation_nonce=${BASH_REMATCH[1]}
[[ -f "$state_file" && ! -L "$state_file" && "$(/usr/bin/stat -c '%u:%a' -- "$state_file")" == "$(/usr/bin/id -u):600" ]]
recovery_file="/home/pc/molin/rollback/molin-email-unknown-${operation_nonce}.sql"
stage=recovery_safe
[[ -f "$recovery_file" && ! -L "$recovery_file" ]]
recovery_identity=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$recovery_file")
[[ "$recovery_identity" =~ ^$(/usr/bin/id -u):600:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
recovery_sha=$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')
[[ "$recovery_sha" =~ ^[a-f0-9]{64}$ && "$recovery_sha" != 0000000000000000000000000000000000000000000000000000000000000000 ]]
cleanup_binary=/home/pc/molin/rollback/email-unknown-restart-cleanup.test
stage=binary_safe
[[ -f "$cleanup_binary" && ! -L "$cleanup_binary" ]]
binary_identity=$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cleanup_binary")
[[ "$binary_identity" =~ ^$(/usr/bin/id -u):500:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
stage=binary_sha
[[ "$(/usr/bin/sha256sum -- "$cleanup_binary" | /usr/bin/awk '{print $1}')" == "$expected_binary_sha" ]]
stage=mysql_identity_count
mapfile -t container_lines < <(/usr/bin/docker ps --format '{{.ID}}|{{.Image}}|{{.Names}}')
mysql_ids=()
for container_line in "${container_lines[@]}"; do
  container_id=${container_line%%|*}; container_identity=${container_line#*|}; container_identity=${container_identity,,}
  [[ "$container_id" =~ ^[a-f0-9]{12,64}$ ]]
  case "$container_identity" in *mysql*) mysql_ids+=("$container_id") ;; esac
done
mysql_identity_count=${#mysql_ids[@]}
(( mysql_identity_count == 1 ))
mysql_id=${mysql_ids[0]}
mapfile -t compose_mysql_ids < <(/usr/bin/docker ps --filter label=com.docker.compose.project=molin --filter label=com.docker.compose.service=mysql --format '{{.ID}}')
mysql_compose_label_count=${#compose_mysql_ids[@]}
stage=cycle_marker_count
mapfile -t cycle_markers < <(/usr/bin/docker exec "$mysql_id" /usr/bin/find /root -mindepth 3 -maxdepth 3 -type f -path '/root/molin-000057-schema57-cycle-run-*/evidence/cycle_completed' -print)
cycle_marker_count=${#cycle_markers[@]}
(( cycle_marker_count == 2 ))
cycle_dump_valid_count=0
cycle_shas=()
cycle_dumps=()
cycle_identities=()
stage=cycle_dump_valid
for marker in "${cycle_markers[@]}"; do
  [[ "$marker" =~ ^(/root/molin-000057-schema57-cycle-run-[a-f0-9]{32})/evidence/cycle_completed$ ]]
  cycle_dump="${BASH_REMATCH[1]}/evidence/molin_source_schema57.sql"
  [[ -z "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type l -print)" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/find "$cycle_dump" -mindepth 0 -maxdepth 0 -type f -print)" == "$cycle_dump" ]]
  cycle_identity=$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cycle_dump")
  [[ "$cycle_identity" =~ ^0:600:[0-9]+:[0-9]+:[1-9][0-9]*$ ]]
  cycle_sha=$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "$cycle_dump" | /usr/bin/awk '{print $1}')
  [[ "$cycle_sha" =~ ^[a-f0-9]{64}$ ]]
  cycle_dumps+=("$cycle_dump"); cycle_identities+=("$cycle_identity"); cycle_shas+=("$cycle_sha")
  cycle_dump_valid_count=$(( cycle_dump_valid_count + 1 ))
done
stage=cycle_sha_set
mapfile -t expected_sorted < <(/usr/bin/printf '%s\n' "${expected_cycle_shas[@]}" | /usr/bin/sort)
mapfile -t actual_sorted < <(/usr/bin/printf '%s\n' "${cycle_shas[@]}" | /usr/bin/sort)
[[ "${actual_sorted[0]}" == "${expected_sorted[0]}" && "${actual_sorted[1]}" == "${expected_sorted[1]}" ]]
stage=snapshot_stable
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$recovery_file")" == "$recovery_identity" ]]
[[ "$(/usr/bin/sha256sum -- "$recovery_file" | /usr/bin/awk '{print $1}')" == "$recovery_sha" ]]
[[ "$(/usr/bin/stat -c '%u:%a:%d:%i:%s' -- "$cleanup_binary")" == "$binary_identity" ]]
for index in 0 1; do
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/stat -c '%u:%a:%d:%i:%s' -- "${cycle_dumps[$index]}")" == "${cycle_identities[$index]}" ]]
  [[ "$(/usr/bin/docker exec "$mysql_id" /usr/bin/sha256sum -- "${cycle_dumps[$index]}" | /usr/bin/awk '{print $1}')" == "${cycle_shas[$index]}" ]]
done
trap - ERR
if (( mysql_compose_label_count == 1 )); then label_contract_match=true; else label_contract_match=false; fi
/usr/bin/printf 'status=pass stage=diagnostic_complete state_count=1 state_safe=true recovery_safe=true binary_safe=true binary_sha_match=true mysql_identity_count=1 mysql_compose_label_count=%s label_contract_match=%s cycle_marker_count=2 cycle_dump_valid_count=2 cycle_sha_set_match=true snapshot_stable=true writes=false cleanup=false restarts=false retries=0\n' "$mysql_compose_label_count" "$label_contract_match"
'@
}

function Test-MetadataDiagnosticSummary {
    param([string]$Stdout, [string]$Stderr, [int]$ExitCode)
    $pass = '^status=pass stage=diagnostic_complete state_count=1 state_safe=true recovery_safe=true binary_safe=true binary_sha_match=true mysql_identity_count=1 mysql_compose_label_count=(?<labels>[0-9]+) label_contract_match=(?<label_match>true|false) cycle_marker_count=2 cycle_dump_valid_count=2 cycle_sha_set_match=true snapshot_stable=true writes=false cleanup=false restarts=false retries=0\r?\n?\z'
    $fail = '^status=failed stage=(?<stage>state_count|state_safe|recovery_safe|binary_safe|binary_sha|mysql_identity_count|cycle_marker_count|cycle_dump_valid|cycle_sha_set|snapshot_stable) state_count=[0-9]+ mysql_identity_count=[0-9]+ mysql_compose_label_count=[0-9]+ cycle_marker_count=[0-9]+ cycle_dump_valid_count=[0-9]+ writes=false cleanup=false restarts=false retries=0\r?\n?\z'
    if ($Stderr.Length -ne 0) { throw 'metadata_diagnostic_stderr_nonempty' }
    if ($ExitCode -eq 0 -and [regex]::IsMatch($Stdout, $pass, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) { return $Stdout.TrimEnd([char[]]@("`r", "`n")) }
    if ($ExitCode -eq 2 -and [regex]::IsMatch($Stdout, $fail, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) { return $Stdout.TrimEnd([char[]]@("`r", "`n")) }
    throw 'metadata_diagnostic_summary_invalid'
}
function Get-SafeMetadataDiagnostics {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout)

    # 必须先不可逆替换所有摘要和 operation nonce，后续只在脱敏文本上识别字段结构，不保留或返回原值。
    $sanitized = [regex]::Replace($Stdout, '(?i)(?<![0-9a-f])[0-9a-f]{64}(?![0-9a-f])', '<sha256>', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $sanitized = [regex]::Replace($sanitized, '(?i)(molin-email-unknown-)[0-9a-f]{32}(\.sql)', '$1<nonce>$2', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $separatorCount = [regex]::Matches($sanitized, '\r\n|\r|\n', [Text.RegularExpressions.RegexOptions]::CultureInvariant).Count
    $lineCount = if ($sanitized.Length -eq 0) { 0 } else { $separatorCount + 1 }
    $lines = [regex]::Split($sanitized, '\r\n|\r|\n', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $statusLines = @($lines | Where-Object { $_ -match '(?:^|\s)status=' })
    $fieldLine = if ($statusLines.Count -gt 0) { $statusLines[0] } elseif ($lines.Count -gt 0) { $lines[0] } else { '' }
    $fields = @($fieldLine.Trim() -split '\s+' | Where-Object { $_.Length -gt 0 })
    $fieldNames = @($fields | ForEach-Object { if ($_.Contains('=')) { $_.Substring(0, $_.IndexOf('=')) } else { '<invalid>' } })
    $expectedNames = @('status', 'recovery_filename', 'recovery_sha256', 'cycle_sha256_one', 'cycle_sha256_two')
    $fieldOrderCorrect = $fieldNames.Count -eq $expectedNames.Count
    if ($fieldOrderCorrect) {
        for ($index = 0; $index -lt $expectedNames.Count; $index++) {
            if ($fieldNames[$index] -cne $expectedNames[$index]) { $fieldOrderCorrect = $false; break }
        }
    }
    $knownFieldsOnly = $fieldNames.Count -eq $expectedNames.Count -and @($fieldNames | Where-Object { $_ -cnotin $expectedNames }).Count -eq 0
    $prefixCorrect = $sanitized.StartsWith('status=pass ', [StringComparison]::Ordinal)
    $statusCorrect = @($fields | Where-Object { $_ -ceq 'status=pass' }).Count -eq 1
    $hasCR = $sanitized.Contains("`r")
    $hasLF = $sanitized.Contains("`n")
    $hasNUL = $sanitized.IndexOf([char]0) -ge 0
    $hasNonASCII = [regex]::IsMatch($sanitized, '[^\x00-\x7f]', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $withoutOneTrailingNewline = [regex]::Replace($sanitized, '\r?\n\z', '', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $hasInteriorLineBreak = [regex]::IsMatch($withoutOneTrailingNewline, '\r|\n', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $hasExtraText = $hasNUL -or $hasNonASCII -or $hasInteriorLineBreak -or -not $prefixCorrect -or -not $knownFieldsOnly -or $statusLines.Count -ne 1
    $classification = if ($hasNUL -or $hasNonASCII) { 'control_or_nonascii' } elseif ($hasExtraText) { 'extra_text' } elseif (-not $fieldOrderCorrect) { 'field_order' } else { 'dynamic_format' }
    return [pscustomobject]@{
        StdoutLength = $Stdout.Length; LineCount = $lineCount; FieldCount = $fields.Count
        PrefixCorrect = $prefixCorrect; StatusCorrect = $statusCorrect
        HasCR = $hasCR; HasLF = $hasLF; HasNUL = $hasNUL; HasNonASCII = $hasNonASCII
        HasExtraText = $hasExtraText; FieldOrderCorrect = $fieldOrderCorrect; Shape = $classification
    }
}

function Test-MetadataSummary {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )
    if ($ExitCode -ne 0) { throw 'metadata_exit_nonzero' }
    if ($Stderr.Length -ne 0) { throw 'metadata_stderr_nonempty' }
    $match = [regex]::Match($Stdout, $script:MetadataPattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success -or $match.Groups['recovery_sha'].Value -ceq ('0' * 64)) {
        $diagnostics = Get-SafeMetadataDiagnostics $Stdout
        $exception = New-Object InvalidOperationException('metadata_summary_invalid')
        foreach ($property in $diagnostics.PSObject.Properties) { $exception.Data[$property.Name] = $property.Value }
        throw $exception
    }
    $actualCycle = @($match.Groups['cycle_one'].Value, $match.Groups['cycle_two'].Value) | Sort-Object
    $expectedCycle = @($script:CycleDumpSHA256) | Sort-Object
    if ($actualCycle.Count -ne 2 -or $actualCycle[0] -cne $expectedCycle[0] -or $actualCycle[1] -cne $expectedCycle[1]) { throw 'metadata_cycle_set_invalid' }
    return [pscustomobject]@{
        RecoveryFileName = $match.Groups['recovery'].Value
        RecoverySHA256 = $match.Groups['recovery_sha'].Value
        CycleDumpSHA256 = @($actualCycle)
    }
}

function Get-SafePostcheckChildFailure {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )

    # 只接受 postcheck runner 自己生成的固定 JSON 形状；任何额外字段、乱序或非白名单值都继续折叠为通用失败。
    if ($ExitCode -ne 2 -or $Stderr.Length -ne 0) { return $null }
    $pattern = '^\{"status":"failed","classification":"(?<classification>[a-z_]+)","ssh_attempt_count":(?<attempt>[01]),"ssh_completed_count":(?<completed>[01]),"stdout_length":[0-9]+,"stderr_length":[0-9]+,"writes":false,"restart":false,"cleanup":false,"retries":0(?:,"stage":"(?<stage>[a-z_]+)")?\}\r?\n?\z'
    $match = [regex]::Match($Stdout, $pattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $match.Success) { return $null }
    $classification = $match.Groups['classification'].Value
    $postcheckStage = if ($match.Groups['stage'].Success) { $match.Groups['stage'].Value } else { $null }
    if ($classification -cnotin $script:PostcheckFailureClassifications -or
        [int]$match.Groups['completed'].Value -gt [int]$match.Groups['attempt'].Value) { return $null }
    if (($classification -ceq 'remote_gate_failed') -ne (-not [string]::IsNullOrEmpty($postcheckStage)) -or
        (-not [string]::IsNullOrEmpty($postcheckStage) -and $postcheckStage -cnotin $script:PostcheckRemoteStages)) { return $null }
    return [pscustomobject]@{ Classification = $classification; PostcheckStage = $postcheckStage }
}

function Assert-ChildSuccess {
    param(
        [Parameter(Mandatory = $true)][ValidateSet('cleanup', 'postcheck')][string]$Stage,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stdout,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Stderr,
        [Parameter(Mandatory = $true)][int]$ExitCode
    )
    $pattern = if ($Stage -ceq 'cleanup') { $script:CleanupSuccessPattern } else { $script:PostcheckSuccessPattern }
    if ($ExitCode -eq 0 -and $Stderr.Length -eq 0 -and
        [regex]::IsMatch($Stdout, $pattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)) { return }
    if ($Stage -ceq 'postcheck') {
        $safeFailure = Get-SafePostcheckChildFailure -Stdout $Stdout -Stderr $Stderr -ExitCode $ExitCode
        if ($null -ne $safeFailure) {
            $exception = New-Object InvalidOperationException($safeFailure.Classification)
            if (-not [string]::IsNullOrEmpty($safeFailure.PostcheckStage)) { $exception.Data['PostcheckStage'] = $safeFailure.PostcheckStage }
            throw $exception
        }
    }
    throw ($Stage + '_failed')
}

function Invoke-OrderedFlow {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$MetadataAction,
        [Parameter(Mandatory = $true)][scriptblock]$CleanupAction,
        [Parameter(Mandatory = $true)][scriptblock]$PostcheckAction
    )
    $cleanupStarted = $false
    $postcheckStarted = $false
    try { $metadata = & $MetadataAction }
    catch {
        $known = @('metadata_exit_nonzero', 'metadata_stderr_nonempty', 'metadata_summary_invalid', 'metadata_cycle_set_invalid', 'process_timeout')
        $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'metadata_failed' }
        $failure = [ordered]@{ Status = 'failed'; Stage = 'metadata'; Classification = $classification; CleanupStarted = $false; PostcheckStarted = $false }
        if ($classification -ceq 'metadata_summary_invalid') {
            foreach ($name in @('StdoutLength', 'LineCount', 'FieldCount', 'PrefixCorrect', 'StatusCorrect', 'HasCR', 'HasLF', 'HasNUL', 'HasNonASCII', 'HasExtraText', 'FieldOrderCorrect', 'Shape')) {
                $failure[$name] = $_.Exception.Data[$name]
            }
        }
        return [pscustomobject]$failure
    }
    try {
        $cleanupStarted = $true
        & $CleanupAction $metadata
    }
    catch { return [pscustomobject]@{ Status = 'failed'; Stage = 'cleanup'; Classification = 'cleanup_failed'; CleanupStarted = $cleanupStarted; PostcheckStarted = $false } }
    try {
        $postcheckStarted = $true
        & $PostcheckAction $metadata
    }
    catch {
        # 子 runner 的分类只有通过严格 JSON 形状和固定白名单后才穿透；未知异常仍保持通用失败。
        $classification = if ($_.Exception.Message -cin $script:PostcheckFailureClassifications) { $_.Exception.Message } else { 'postcheck_failed' }
        $failure = [ordered]@{ Status = 'failed'; Stage = 'postcheck'; Classification = $classification; CleanupStarted = $cleanupStarted; PostcheckStarted = $postcheckStarted }
        if ($classification -ceq 'remote_gate_failed' -and $_.Exception.Data.Contains('PostcheckStage') -and
            [string]$_.Exception.Data['PostcheckStage'] -cin $script:PostcheckRemoteStages) {
            $failure['PostcheckStage'] = [string]$_.Exception.Data['PostcheckStage']
        }
        return [pscustomobject]$failure
    }
    return [pscustomobject]@{ Status = 'pass'; Stage = 'complete'; Classification = 'pass'; CleanupStarted = $cleanupStarted; PostcheckStarted = $postcheckStarted }
}

function Write-SafeResult {
    param([Parameter(Mandatory = $true)]$Result)
    if ($Result.Status -ceq 'pass') {
        Write-Output 'status=pass stage=complete cleanup_started=true postcheck_started=true metadata_ssh_attempts=1 cleanup_calls=1 postcheck_calls=1 retries=0'
        return
    }
    if ($Result.Stage -ceq 'metadata' -and $Result.Classification -ceq 'metadata_summary_invalid') {
        Write-Output ("status=failed stage=metadata classification=metadata_summary_invalid shape={0} stdout_length={1} line_count={2} field_count={3} prefix_correct={4} status_correct={5} has_cr={6} has_lf={7} has_nul={8} has_nonascii={9} extra_text={10} field_order_correct={11} cleanup_started=false postcheck_started=false" -f
            $Result.Shape, $Result.StdoutLength, $Result.LineCount, $Result.FieldCount,
            $Result.PrefixCorrect.ToString().ToLowerInvariant(), $Result.StatusCorrect.ToString().ToLowerInvariant(),
            $Result.HasCR.ToString().ToLowerInvariant(), $Result.HasLF.ToString().ToLowerInvariant(),
            $Result.HasNUL.ToString().ToLowerInvariant(), $Result.HasNonASCII.ToString().ToLowerInvariant(),
            $Result.HasExtraText.ToString().ToLowerInvariant(), $Result.FieldOrderCorrect.ToString().ToLowerInvariant())
        return
    }
    if ($Result.Stage -ceq 'postcheck' -and $Result.Classification -ceq 'remote_gate_failed' -and
        -not [string]::IsNullOrEmpty([string]$Result.PostcheckStage)) {
        Write-Output ("status=failed stage=postcheck classification=remote_gate_failed postcheck_stage={0} cleanup_started={1} postcheck_started={2}" -f
            $Result.PostcheckStage, $Result.CleanupStarted.ToString().ToLowerInvariant(), $Result.PostcheckStarted.ToString().ToLowerInvariant())
        return
    }
    Write-Output ("status=failed stage={0} classification={1} cleanup_started={2} postcheck_started={3}" -f
        $Result.Stage, $Result.Classification, $Result.CleanupStarted.ToString().ToLowerInvariant(), $Result.PostcheckStarted.ToString().ToLowerInvariant())
}

function Invoke-LocalPreflightCheck {
    # 本地预检复用正式路径与文件函数，但只解析工具路径，不创建任何进程。
    $temp = $null
    $result = [pscustomobject]@{ Status = 'failed'; Classification = 'local_preflight_failed'; FilesVerified = 0; CleanupVerified = $false }
    try {
        $cleanupRunner = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'run-email-unknown-history-cleanup.ps1'))
        $postcheckRunner = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'run-email-unknown-history-postcheck.ps1'))
        foreach ($runner in @($cleanupRunner, $postcheckRunner)) {
            $item = [IO.FileInfo]::new($runner)
            if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
                $item.DirectoryName -cne [IO.Path]::GetFullPath($PSScriptRoot)) { throw 'runner_path_invalid' }
        }
        $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
        $powerShellExe = Join-Path $PSHOME 'powershell.exe'
        foreach ($tool in @($sshExe, $powerShellExe)) {
            $item = [IO.FileInfo]::new($tool)
            if (-not $item.Exists -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'tool_missing' }
        }
        $metadataBytes = ConvertTo-Utf8PayloadBytes (Get-MetadataPayload)
        $temp = New-RestrictedTempDirectory
        foreach ($stage in @('metadata', 'cleanup', 'postcheck')) {
            if ($stage -ceq 'metadata') {
                Write-RestrictedBytes -Path (Join-Path $temp 'metadata.stdin') -Bytes $metadataBytes
            }
            else {
                Write-RestrictedBytes -Path (Join-Path $temp ($stage + '.stdin')) -Bytes ([byte[]]@())
            }
            Write-RestrictedBytes -Path (Join-Path $temp ($stage + '.stdout')) -Bytes ([byte[]]@())
            Write-RestrictedBytes -Path (Join-Path $temp ($stage + '.stderr')) -Bytes ([byte[]]@())
        }
        $files = @([IO.Directory]::GetFiles($temp))
        if ($files.Count -ne 9 -or [IO.File]::ReadAllBytes((Join-Path $temp 'metadata.stdin')).Length -ne $metadataBytes.Length) {
            throw 'temp_file_invalid'
        }
        foreach ($name in @('metadata.stdout', 'metadata.stderr', 'cleanup.stdin', 'cleanup.stdout', 'cleanup.stderr', 'postcheck.stdin', 'postcheck.stdout', 'postcheck.stderr')) {
            if ([IO.File]::ReadAllBytes((Join-Path $temp $name)).Length -ne 0) { throw 'temp_file_invalid' }
        }
        $result = [pscustomobject]@{ Status = 'pass'; Classification = 'pass'; FilesVerified = 9; CleanupVerified = $false }
    }
    catch {
        $known = @('runner_path_invalid', 'tool_missing', 'payload_encoding_invalid', 'temp_path_invalid', 'temp_acl_invalid', 'temp_file_invalid')
        $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'local_preflight_failed' }
        $result = [pscustomobject]@{ Status = 'failed'; Classification = $classification; FilesVerified = 0; CleanupVerified = $false }
    }
    finally {
        if ($null -ne $temp) {
            try {
                Remove-RestrictedTempDirectory $temp
                $result.CleanupVerified = $true
            }
            catch {
                $result.Status = 'failed'
                $result.Classification = 'temp_cleanup_failed'
                $result.CleanupVerified = $false
            }
        }
    }
    return $result
}

if ($MetadataDiagnosticOnly) {
    $diagnosticTemp = $null
    try {
        if ($Confirm -cne $script:MetadataDiagnosticPhrase) { throw 'confirmation_required' }
        $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
        if (-not [IO.File]::Exists($sshExe)) { throw 'tool_missing' }
        $diagnosticTemp = New-RestrictedTempDirectory
        $inputPath = Join-Path $diagnosticTemp 'metadata.stdin'
        $outputPath = Join-Path $diagnosticTemp 'metadata.stdout'
        $errorPath = Join-Path $diagnosticTemp 'metadata.stderr'
        Write-RestrictedBytes $inputPath (ConvertTo-Utf8PayloadBytes (Get-MetadataDiagnosticPayload))
        Write-RestrictedBytes $outputPath ([byte[]]@())
        Write-RestrictedBytes $errorPath ([byte[]]@())
        $arguments = @('-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', 'pc@8.130.9.163', '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc', 'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '45s', '/bin/bash', '--noprofile', '--norc', '-s', '--')
        $process = Start-FixedRedirectedProcess $sshExe $arguments $inputPath $outputPath $errorPath 60000
        $encoding = New-Object Text.UTF8Encoding($false, $true)
        $safeLine = Test-MetadataDiagnosticSummary ([IO.File]::ReadAllText($outputPath, $encoding)) ([IO.File]::ReadAllText($errorPath, $encoding)) $process.ExitCode
        Write-Output $safeLine
        if ($process.ExitCode -ne 0) { exit 2 }
        exit 0
    }
    catch {
        $known = @('confirmation_required', 'tool_missing', 'payload_encoding_invalid', 'temp_path_invalid', 'temp_acl_invalid', 'temp_file_invalid', 'process_timeout', 'process_handle_unavailable', 'process_exit_code_unavailable', 'metadata_diagnostic_stderr_nonempty', 'metadata_diagnostic_summary_invalid')
        $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'metadata_diagnostic_failed' }
        Write-Output ("status=failed stage=local classification={0} cleanup_started=false postcheck_started=false" -f $classification)
        exit 2
    }
    finally {
        if ($null -ne $diagnosticTemp) { Remove-RestrictedTempDirectory $diagnosticTemp }
    }
}

if ($LocalPreflightOnly) {
    $preflight = Invoke-LocalPreflightCheck
    if ($preflight.Status -ceq 'pass' -and $preflight.CleanupVerified) {
        Write-Output 'status=pass stage=local_preflight files_verified=9 cleanup_verified=true ssh_started=false cleanup_started=false postcheck_started=false'
        exit 0
    }
    Write-Output ("status=failed stage=local_preflight classification={0} cleanup_started=false postcheck_started=false" -f $preflight.Classification)
    exit 2
}

if ($SelfTest) {
    # SelfTest 只调用内存桩，不解析 ssh.exe，也不启动 cleanup/postcheck runner。
    $cases = 0
    $payload = Get-MetadataPayload
    $diagnosticPayload = Get-MetadataDiagnosticPayload
    $bytes = ConvertTo-Utf8PayloadBytes $payload
    if ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) { throw 'selftest_bom_present' }
    $cases++
    # 正式 metadata 必须与 recovery/cleanup/postcheck 使用同一唯一容器身份契约，禁止再次依赖环境易漂移的 Compose 标签。
    foreach ($required in @("docker ps --format '{{.ID}}|{{.Image}}|{{.Names}}'", 'container_identity=${container_identity,,}', '[[ "$container_id" =~ ^[a-f0-9]{12,64}$ ]]', '(( ${#mysql_ids[@]} == 1 ))')) {
        if (-not $payload.Contains($required)) { throw 'selftest_metadata_mysql_identity_contract' }
    }
    foreach ($forbidden in @('label=com.docker.compose.project=molin', 'label=com.docker.compose.service=mysql')) {
        if ($payload.Contains($forbidden)) { throw 'selftest_metadata_compose_label_dependency' }
    }
    $cases++
    $diagnosticBytes = ConvertTo-Utf8PayloadBytes $diagnosticPayload
    foreach ($required in @('stage=mysql_identity_count', 'label=com.docker.compose.project=molin', 'mysql_compose_label_count=%s', 'label_contract_match=%s', 'writes=false cleanup=false restarts=false retries=0')) {
        if (-not $diagnosticPayload.Contains($required)) { throw 'selftest_metadata_diagnostic_contract' }
    }
    foreach ($forbidden in @('redis-cli', 'MYSQL_PWD', 'DELETE ', 'UPDATE ', 'INSERT ', 'REPLACE ', 'ALTER ', 'DROP ', 'TRUNCATE ', 'docker restart', 'docker stop', 'docker kill', 'docker rm')) {
        if ($diagnosticPayload.Contains($forbidden)) { throw 'selftest_metadata_diagnostic_side_effect' }
    }
    if ($diagnosticBytes[0] -ne 0x73 -or $diagnosticPayload.Contains('cleanup_started=true') -or $diagnosticPayload.Contains('postcheck_started=true')) { throw 'selftest_metadata_diagnostic_encoding' }
    $diagnosticPass = 'status=pass stage=diagnostic_complete state_count=1 state_safe=true recovery_safe=true binary_safe=true binary_sha_match=true mysql_identity_count=1 mysql_compose_label_count=0 label_contract_match=false cycle_marker_count=2 cycle_dump_valid_count=2 cycle_sha_set_match=true snapshot_stable=true writes=false cleanup=false restarts=false retries=0' + "`n"
    if ((Test-MetadataDiagnosticSummary $diagnosticPass '' 0) -cne $diagnosticPass.Trim()) { throw 'selftest_metadata_diagnostic_pass' }
    $diagnosticFail = 'status=failed stage=mysql_identity_count state_count=1 mysql_identity_count=0 mysql_compose_label_count=0 cycle_marker_count=0 cycle_dump_valid_count=0 writes=false cleanup=false restarts=false retries=0' + "`n"
    if ((Test-MetadataDiagnosticSummary $diagnosticFail '' 2) -cne $diagnosticFail.Trim()) { throw 'selftest_metadata_diagnostic_fail' }
    foreach ($attack in @($diagnosticPass + "extra=true`n", $diagnosticPass.Replace(' writes=false', ''), $diagnosticPass)) {
        $attackStderr = if ($attack -ceq $diagnosticPass) { 'warning' } else { '' }
        try { [void](Test-MetadataDiagnosticSummary $attack $attackStderr 0); throw 'selftest_metadata_diagnostic_attack_accepted' }
        catch { if ($_.Exception.Message -eq 'selftest_metadata_diagnostic_attack_accepted') { throw } }
    }
    $cases += 6
    # 从真实 payload 提取远端集合函数，并在本机 Bash 中按两种 find 顺序执行，防止 PowerShell 层排序掩盖远端错误。
    $expectedMatch = [regex]::Match($payload, '(?m)^expected_cycle_shas=\((?<values>[a-f0-9]{64} [a-f0-9]{64})\)$', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    $contractMatch = [regex]::Match($payload, '(?ms)^# SELFTEST_CYCLE_SHA_SET_BEGIN\r?\n(?<contract>.*?)^# SELFTEST_CYCLE_SHA_SET_END$', [Text.RegularExpressions.RegexOptions]::CultureInvariant)
    if (-not $expectedMatch.Success -or -not $contractMatch.Success) { throw 'selftest_cycle_sort_source_missing' }
    $cycleValues = $expectedMatch.Groups['values'].Value.Split(' ')
    $bash = Join-Path $env:ProgramFiles 'Git\bin\bash.exe'
    if (-not [IO.File]::Exists($bash) -or ([IO.FileInfo]::new($bash).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw 'selftest_bash_tool_missing'
    }
    $sortTemp = $null
    try {
        $sortTemp = New-RestrictedTempDirectory
        $orders = @(
            [pscustomobject]@{ One = $cycleValues[0]; Two = $cycleValues[1] },
            [pscustomobject]@{ One = $cycleValues[1]; Two = $cycleValues[0] }
        )
        for ($orderIndex = 0; $orderIndex -lt $orders.Count; $orderIndex++) {
            $order = $orders[$orderIndex]
            $probe = "set -Eeuo pipefail`n$($expectedMatch.Value)`n$($contractMatch.Groups['contract'].Value)cycle_shas=($($order.One) $($order.Two))`nsort_and_compare_cycle_sha_sets`nprintf 'status=pass'`n"
            $probePath = Join-Path $sortTemp ("cycle-sort-$orderIndex.sh")
            Write-RestrictedBytes -Path $probePath -Bytes ((New-Object Text.UTF8Encoding($false, $true)).GetBytes($probe))
            $probeOutput = & $bash --noprofile --norc $probePath 2>$null
            if ($LASTEXITCODE -ne 0 -or ($probeOutput | Out-String).Trim() -cne 'status=pass') { throw 'selftest_cycle_sort_order' }
            $cases++
        }
        $wrongProbe = "set -Eeuo pipefail`n$($expectedMatch.Value)`n$($contractMatch.Groups['contract'].Value)cycle_shas=($($cycleValues[0]) $('b' * 64))`nsort_and_compare_cycle_sha_sets`n"
        $wrongPath = Join-Path $sortTemp 'cycle-sort-wrong.sh'
        Write-RestrictedBytes -Path $wrongPath -Bytes ((New-Object Text.UTF8Encoding($false, $true)).GetBytes($wrongProbe))
        [void](& $bash --noprofile --norc $wrongPath 2>$null)
        if ($LASTEXITCODE -eq 0) { throw 'selftest_cycle_sort_wrong_set' }
        $cases++
    }
    finally { if ($null -ne $sortTemp) { Remove-RestrictedTempDirectory $sortTemp } }
    # 使用真实 Windows PowerShell 5.1 子进程验证 0 与非零退出码都能原样穿透，防止 null 再次被转换成 0。
    $processExitTemp = $null
    try {
        $processExitTemp = New-RestrictedTempDirectory
        $processExitInput = Join-Path $processExitTemp 'metadata.stdin'
        $processExitOutput = Join-Path $processExitTemp 'metadata.stdout'
        $processExitError = Join-Path $processExitTemp 'metadata.stderr'
        $windowsPowerShellExe = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
        Write-RestrictedBytes -Path $processExitInput -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path $processExitOutput -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path $processExitError -Bytes ([byte[]]@())
        foreach ($expectedExitCode in @(0, 7)) {
            $processResult = Start-FixedRedirectedProcess -FilePath $windowsPowerShellExe -ArgumentList @('-NoProfile', '-NonInteractive', '-Command', ('exit ' + $expectedExitCode)) -InputPath $processExitInput -OutputPath $processExitOutput -ErrorPath $processExitError -TimeoutMilliseconds 10000
            if ($processResult.ExitCode -ne $expectedExitCode) { throw 'selftest_process_exit_code_mismatch' }
            if ($expectedExitCode -eq 7) {
                try { [void](Test-MetadataSummary '' '' $processResult.ExitCode); throw 'selftest_nonzero_exit_accepted' }
                catch { if ($_.Exception.Message -eq 'selftest_nonzero_exit_accepted' -or $_.Exception.Message -cne 'metadata_exit_nonzero') { throw } }
            }
            $cases++
        }
        # 使用完整正式参数顺序启动子 runner 的本地 SelfTest，覆盖 powershell.exe -File 的真实 argv 绑定。
        $postcheckRunnerForBinding = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'run-email-unknown-history-postcheck.ps1'))
        $bindingArguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $postcheckRunnerForBinding, '-SelfTest', '-Confirm', 'LOCAL_BINDING_PROBE', '-RecoveryFileName', ('molin-email-unknown-' + ('1' * 32) + '.sql'), '-ExpectedCleanupBinarySHA256', ('c' * 64), '-ExpectedRecoverySHA256', ('d' * 64), '-ExpectedCycleDumpSHA256One', ('a' * 64), '-ExpectedCycleDumpSHA256Two', ('b' * 64))
        $bindingResult = Start-FixedRedirectedProcess -FilePath $windowsPowerShellExe -ArgumentList $bindingArguments -InputPath $processExitInput -OutputPath $processExitOutput -ErrorPath $processExitError -TimeoutMilliseconds 30000
        $bindingStdout = [IO.File]::ReadAllText($processExitOutput, (New-Object Text.UTF8Encoding($false, $true)))
        $bindingStderr = [IO.File]::ReadAllText($processExitError, (New-Object Text.UTF8Encoding($false, $true)))
        if ($bindingResult.ExitCode -ne 0 -or $bindingStderr.Length -ne 0 -or $bindingStdout -cnotmatch '\Astatus=pass mode=selftest cases=[1-9][0-9]* external_access=false ssh_attempt_count=0 strict_json=true state_dependency=false output_verified=true process_exit_codes=0,7\r?\n?\z') {
            throw 'selftest_postcheck_argv_binding'
        }
        $cases++
    }
    finally { if ($null -ne $processExitTemp) { Remove-RestrictedTempDirectory $processExitTemp } }
    try { [void](ConvertTo-Utf8PayloadBytes ([char]0xFEFF + $payload)); throw 'selftest_bom_accepted' } catch { if ($_.Exception.Message -eq 'selftest_bom_accepted') { throw } }
    $cases++
    $recovery = 'molin-email-unknown-' + ('1' * 32) + '.sql'
    $recoverySHA = 'a' * 64
    $validMetadata = "status=pass recovery_filename=$recovery recovery_sha256=$recoverySHA cycle_sha256_one=$($script:CycleDumpSHA256[1]) cycle_sha256_two=$($script:CycleDumpSHA256[0])`n"
    $parsed = Test-MetadataSummary $validMetadata '' 0
    if ($parsed.RecoveryFileName -cne $recovery -or $parsed.CycleDumpSHA256.Count -ne 2) { throw 'selftest_metadata_parse' }
    $cases++
    $expectedPrintf = "/usr/bin/printf 'status=pass recovery_filename=%s recovery_sha256=%s cycle_sha256_one=%s cycle_sha256_two=%s\n'"
    if (-not $payload.Contains($expectedPrintf)) { throw 'selftest_metadata_static_order' }
    $cases++
    $crlfMetadata = $validMetadata.Replace("`n", "`r`n")
    [void](Test-MetadataSummary $crlfMetadata '' 0)
    $cases++
    $reorderedMetadata = "status=pass cycle_sha256_one=$($script:CycleDumpSHA256[1]) recovery_filename=$recovery recovery_sha256=$recoverySHA cycle_sha256_two=$($script:CycleDumpSHA256[0])`n"
    $diagnosticFixtures = @(
        [pscustomobject]@{ Text = "remote-banner`n$validMetadata"; Shape = 'extra_text'; Prefix = $false; Extra = $true; NUL = $false; Order = $true },
        [pscustomobject]@{ Text = $validMetadata + "`n"; Shape = 'extra_text'; Prefix = $true; Extra = $true; NUL = $false; Order = $true },
        [pscustomobject]@{ Text = $validMetadata.ToUpperInvariant().Replace('STATUS=PASS', 'status=pass').Replace('RECOVERY_FILENAME=', 'recovery_filename=').Replace('RECOVERY_SHA256=', 'recovery_sha256=').Replace('CYCLE_SHA256_ONE=', 'cycle_sha256_one=').Replace('CYCLE_SHA256_TWO=', 'cycle_sha256_two=').Replace('MOLIN-EMAIL-UNKNOWN-', 'molin-email-unknown-').Replace('.SQL', '.sql'); Shape = 'dynamic_format'; Prefix = $true; Extra = $false; NUL = $false; Order = $true },
        [pscustomobject]@{ Text = $reorderedMetadata; Shape = 'field_order'; Prefix = $true; Extra = $false; NUL = $false; Order = $false },
        [pscustomobject]@{ Text = $validMetadata.TrimEnd("`n") + [char]0 + "`n"; Shape = 'control_or_nonascii'; Prefix = $true; Extra = $true; NUL = $true; Order = $true }
    )
    foreach ($fixture in $diagnosticFixtures) {
        $diagnostic = Get-SafeMetadataDiagnostics $fixture.Text
        if ($diagnostic.Shape -cne $fixture.Shape -or $diagnostic.PrefixCorrect -ne $fixture.Prefix -or
            $diagnostic.HasExtraText -ne $fixture.Extra -or $diagnostic.HasNUL -ne $fixture.NUL -or
            $diagnostic.FieldOrderCorrect -ne $fixture.Order) { throw 'selftest_metadata_diagnostic_shape' }
        try { [void](Test-MetadataSummary $fixture.Text '' 0); throw 'selftest_metadata_diagnostic_accepted' }
        catch {
            if ($_.Exception.Message -eq 'selftest_metadata_diagnostic_accepted' -or $_.Exception.Message -cne 'metadata_summary_invalid' -or
                $_.Exception.Data['Shape'] -cne $fixture.Shape) { throw }
        }
        $cases++
    }
    $safeFailure = Invoke-OrderedFlow { Test-MetadataSummary $diagnosticFixtures[0].Text '' 0 } { throw 'cleanup_must_not_start' } { throw 'postcheck_must_not_start' }
    $safeFailureLine = (Write-SafeResult $safeFailure | Out-String).Trim()
    if ($safeFailureLine -cnotmatch '\Astatus=failed stage=metadata classification=metadata_summary_invalid shape=extra_text stdout_length=[0-9]+ line_count=[0-9]+ field_count=[0-9]+ prefix_correct=false status_correct=true has_cr=false has_lf=true has_nul=false has_nonascii=false extra_text=true field_order_correct=true cleanup_started=false postcheck_started=false\z' -or
        $safeFailureLine.Contains($recovery) -or $safeFailureLine.Contains($recoverySHA) -or $safeFailureLine.Contains('remote-banner')) {
        throw 'selftest_metadata_safe_failure_output'
    }
    $cases++
    foreach ($fixture in @(
        @{ Out = $validMetadata; Err = 'warning'; Code = 0 },
        @{ Out = $validMetadata + "injected=true`n"; Err = ''; Code = 0 },
        @{ Out = $validMetadata.Replace($script:CycleDumpSHA256[0], ('b' * 64)); Err = ''; Code = 0 },
        @{ Out = $validMetadata; Err = ''; Code = 1 }
    )) {
        try { [void](Test-MetadataSummary $fixture.Out $fixture.Err $fixture.Code); throw 'selftest_metadata_attack_accepted' }
        catch { if ($_.Exception.Message -eq 'selftest_metadata_attack_accepted') { throw } }
        $cases++
    }
    $cleanupSummary = 'status=pass preflight_schema=57 preflight_dirty=false state_phase=phase1_created fixture_logs=2 fixture_allowlist=1 fixture_template=1 redis_key_preexisting=false cleanup_binary_launches=1 cleanup_db_logs=2 cleanup_allowlist=1 cleanup_template=1 redis_key_untouched=true state_removed=true backup_retained=true cycle_assets_retained=2 retries=0' + "`n"
    $postcheckSummary = 'status=pass api_health=true api_ready=true schema=57 dirty=false fixture_logs_absent=2 scope_rows=0 allowlist_absent=1 template_absent=1 redis_ping=true redis_key_absent=true recovery_mode=600 recovery_sha256_valid=true cleanup_binary_sha256_valid=true cycle_evidence_count=2 cycle_schema_count=2 state_dependency=false writes=false restarts=false retries=0' + "`n"
    Assert-ChildSuccess cleanup $cleanupSummary '' 0
    Assert-ChildSuccess postcheck $postcheckSummary '' 0
    $cases += 2
    foreach ($fixture in @($cleanupSummary + "extra=true`n", $postcheckSummary.Replace(' writes=false', ''), $postcheckSummary)) {
        $stage = if ($fixture -ceq $postcheckSummary) { 'postcheck' } elseif ($fixture.Contains('fixture_logs_absent')) { 'postcheck' } else { 'cleanup' }
        $stderr = if ($fixture -ceq $postcheckSummary) { 'warning' } else { '' }
        try { Assert-ChildSuccess $stage $fixture $stderr 0; throw 'selftest_child_attack_accepted' }
        catch { if ($_.Exception.Message -eq 'selftest_child_attack_accepted') { throw } }
        $cases++
    }
    $calls = New-Object Collections.Generic.List[string]
    $failed = Invoke-OrderedFlow { $calls.Add('metadata') | Out-Null; $parsed } { param($metadata); $calls.Add('cleanup') | Out-Null; throw 'fixture_cleanup_failed' } { param($metadata); $calls.Add('postcheck') | Out-Null }
    if ($failed.Stage -cne 'cleanup' -or $failed.PostcheckStarted -or ($calls -join ',') -cne 'metadata,cleanup') { throw 'selftest_cleanup_stop' }
    $cases++
    $calls.Clear()
    $safePostcheckFailure = '{"status":"failed","classification":"remote_gate_failed","ssh_attempt_count":1,"ssh_completed_count":1,"stdout_length":14,"stderr_length":0,"writes":false,"restart":false,"cleanup":false,"retries":0,"stage":"fixture_absence"}' + "`n"
    $failed = Invoke-OrderedFlow { $calls.Add('metadata') | Out-Null; $parsed } { param($metadata); $calls.Add('cleanup') | Out-Null } { param($metadata); $calls.Add('postcheck') | Out-Null; Assert-ChildSuccess postcheck $safePostcheckFailure '' 2 }
    $safePostcheckLine = (Write-SafeResult $failed | Out-String).Trim()
    if ($failed.Stage -cne 'postcheck' -or $failed.Classification -cne 'remote_gate_failed' -or $failed.PostcheckStage -cne 'fixture_absence' -or
        ($calls -join ',') -cne 'metadata,cleanup,postcheck' -or
        $safePostcheckLine -cne 'status=failed stage=postcheck classification=remote_gate_failed postcheck_stage=fixture_absence cleanup_started=true postcheck_started=true') {
        throw 'selftest_postcheck_classification_swallowed'
    }
    $cases++
    if ($null -ne (Get-SafePostcheckChildFailure ($safePostcheckFailure.Replace('fixture_absence', 'unsafe_stage')) '' 2) -or
        $null -ne (Get-SafePostcheckChildFailure ($safePostcheckFailure.TrimEnd() + ',"path":"unsafe"}') '' 2)) {
        throw 'selftest_postcheck_failure_attack_accepted'
    }
    $cases += 2
    $calls.Clear()
    $passed = Invoke-OrderedFlow { $calls.Add('metadata') | Out-Null; $parsed } { param($metadata); $calls.Add('cleanup') | Out-Null } { param($metadata); $calls.Add('postcheck') | Out-Null }
    if ($passed.Status -cne 'pass' -or ($calls -join ',') -cne 'metadata,cleanup,postcheck') { throw 'selftest_order' }
    $cases++
    $preflight = Invoke-LocalPreflightCheck
    if ($preflight.Status -cne 'pass' -or $preflight.FilesVerified -ne 9 -or -not $preflight.CleanupVerified) { throw ('selftest_local_preflight_' + $preflight.Classification) }
    $cases++
    Write-Output "status=pass mode=selftest cases=$cases external_access=false ssh_attempt_count=0 cleanup_calls=0 postcheck_calls=0 strict_output=true order_verified=true process_exit_codes=0,7"
    exit 0
}

$runTemp = $null
try {
    if ($Confirm -cne $script:RequiredPhrase) { throw 'confirmation_required' }
    $cleanupRunner = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'run-email-unknown-history-cleanup.ps1'))
    $postcheckRunner = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'run-email-unknown-history-postcheck.ps1'))
    if (-not [IO.File]::Exists($cleanupRunner) -or -not [IO.File]::Exists($postcheckRunner) -or
        ([IO.FileInfo]::new($cleanupRunner).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        ([IO.FileInfo]::new($postcheckRunner).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'runner_path_invalid' }
    $sshExe = Join-Path $env:WINDIR 'System32\OpenSSH\ssh.exe'
    $powerShellExe = Join-Path $PSHOME 'powershell.exe'
    if (-not [IO.File]::Exists($sshExe) -or -not [IO.File]::Exists($powerShellExe)) { throw 'tool_missing' }
    $runTemp = New-RestrictedTempDirectory
    $encoding = New-Object Text.UTF8Encoding($false, $true)
    $metadataPayloadBytes = ConvertTo-Utf8PayloadBytes (Get-MetadataPayload)
    foreach ($stage in @('metadata', 'cleanup', 'postcheck')) {
        if ($stage -ceq 'metadata') {
            Write-RestrictedBytes -Path (Join-Path $runTemp 'metadata.stdin') -Bytes $metadataPayloadBytes
        }
        else {
            # PowerShell 5.1 会把赋值表达式中的零长度数组折叠为 null，因此必须在调用点显式传参。
            Write-RestrictedBytes -Path (Join-Path $runTemp ($stage + '.stdin')) -Bytes ([byte[]]@())
        }
        Write-RestrictedBytes -Path (Join-Path $runTemp ($stage + '.stdout')) -Bytes ([byte[]]@())
        Write-RestrictedBytes -Path (Join-Path $runTemp ($stage + '.stderr')) -Bytes ([byte[]]@())
    }
    # 元数据 action 是全流程唯一 SSH；后续两个 action 各自只调用一次既有 runner。
    $metadataAction = {
        $arguments = @('-T', '-p', '10003', '-o', 'BatchMode=yes', '-o', 'NumberOfPasswordPrompts=0', '-o', 'StrictHostKeyChecking=yes', '-o', 'ConnectTimeout=10', 'pc@8.130.9.163', '/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin', 'HOME=/home/pc', 'USER=pc', 'LOGNAME=pc', 'LANG=C.UTF-8', '/usr/bin/timeout', '--signal=TERM', '--kill-after=5s', '45s', '/bin/bash', '--noprofile', '--norc', '-s', '--')
        $process = Start-FixedRedirectedProcess $sshExe $arguments (Join-Path $runTemp 'metadata.stdin') (Join-Path $runTemp 'metadata.stdout') (Join-Path $runTemp 'metadata.stderr') 60000
        $stdout = [IO.File]::ReadAllText((Join-Path $runTemp 'metadata.stdout'), $encoding)
        $stderr = [IO.File]::ReadAllText((Join-Path $runTemp 'metadata.stderr'), $encoding)
        return Test-MetadataSummary $stdout $stderr $process.ExitCode
    }
    $cleanupAction = {
        param($metadata)
        $arguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $cleanupRunner, '-Confirm', 'I_CONFIRM_EXACT_EMAIL_UNKNOWN_HISTORY_CLEANUP_ONCE', '-ExpectedCleanupBinarySHA256', $script:CleanupBinarySHA256)
        $process = Start-FixedRedirectedProcess $powerShellExe $arguments (Join-Path $runTemp 'cleanup.stdin') (Join-Path $runTemp 'cleanup.stdout') (Join-Path $runTemp 'cleanup.stderr') 180000
        Assert-ChildSuccess cleanup ([IO.File]::ReadAllText((Join-Path $runTemp 'cleanup.stdout'), $encoding)) ([IO.File]::ReadAllText((Join-Path $runTemp 'cleanup.stderr'), $encoding)) $process.ExitCode
    }
    $postcheckAction = {
        param($metadata)
        # 两个 cycle 摘要各使用独立命名参数，避免 Windows PowerShell 5.1 把第二个值当成未命名位置参数。
        $arguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $postcheckRunner, '-Confirm', 'I_CONFIRM_EMAIL_UNKNOWN_HISTORY_POSTCHECK_ONCE', '-RecoveryFileName', $metadata.RecoveryFileName, '-ExpectedCleanupBinarySHA256', $script:CleanupBinarySHA256, '-ExpectedRecoverySHA256', $metadata.RecoverySHA256, '-ExpectedCycleDumpSHA256One', $metadata.CycleDumpSHA256[0], '-ExpectedCycleDumpSHA256Two', $metadata.CycleDumpSHA256[1])
        $process = Start-FixedRedirectedProcess $powerShellExe $arguments (Join-Path $runTemp 'postcheck.stdin') (Join-Path $runTemp 'postcheck.stdout') (Join-Path $runTemp 'postcheck.stderr') 210000
        Assert-ChildSuccess postcheck ([IO.File]::ReadAllText((Join-Path $runTemp 'postcheck.stdout'), $encoding)) ([IO.File]::ReadAllText((Join-Path $runTemp 'postcheck.stderr'), $encoding)) $process.ExitCode
    }
    $result = Invoke-OrderedFlow $metadataAction $cleanupAction $postcheckAction
    Write-SafeResult $result
    if ($result.Status -cne 'pass') { exit 2 }
    exit 0
}
catch {
    # 只暴露固定分类，既能定位本地门禁，也不会泄露路径、随机标识或摘要。
    $known = @('confirmation_required', 'runner_path_invalid', 'tool_missing', 'payload_encoding_invalid', 'temp_path_invalid', 'temp_acl_invalid', 'temp_file_invalid', 'temp_cleanup_path_invalid', 'temp_cleanup_file_invalid', 'temp_cleanup_not_empty')
    $classification = if ($_.Exception.Message -cin $known) { $_.Exception.Message } else { 'local_gate_failed' }
    Write-Output ("status=failed stage=local classification={0} cleanup_started=false postcheck_started=false" -f $classification)
    exit 2
}
finally {
    if ($null -ne $runTemp) { Remove-RestrictedTempDirectory $runTemp }
}
