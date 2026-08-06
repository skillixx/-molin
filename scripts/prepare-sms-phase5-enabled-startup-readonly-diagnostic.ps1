param(
    [string]$ChangeId = "",
    [string]$OutputDirectory = "",
    [switch]$ExportCandidate,
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
$FixedServerHost = "8.130.9.163"
$FixedSSHPort = 10003
$FixedSSHUser = "pc"

function Assert-LocalAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Description
    )

    # 候选只能写入本机绝对路径，避免生成阶段因 UNC 或 Provider 路径意外联网。
    if ([string]::IsNullOrWhiteSpace($Path) -or $Path -match '^(?:\\\\|//)' -or $Path.Contains("::")) {
        throw "${Description}必须是本地文件系统绝对路径"
    }
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
        if ($Path -cnotmatch '^[A-Za-z]:[\\/]') { throw "Windows ${Description}必须使用本地盘符绝对路径" }
        $drive = Get-PSDrive -Name $Path.Substring(0, 1) -PSProvider FileSystem -ErrorAction Stop
        if (([string]$drive.Root).StartsWith("\\") -or ([string]$drive.DisplayRoot).StartsWith("\\")) {
            throw "${Description}不得使用网络映射盘"
        }
    }
    elseif (-not [IO.Path]::IsPathRooted($Path)) {
        throw "${Description}必须使用本地绝对路径"
    }
}

if (-not $ExportCandidate -and -not $SelfTest) {
    Write-Output "enabled_startup_readonly_candidate_authorized=false"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExportCandidate -and $SelfTest) { throw "ExportCandidate 与 SelfTest 必须互斥" }

if ($SelfTest) {
    if ($FixedServerHost -cne "8.130.9.163" -or $FixedSSHPort -ne 10003 -or $FixedSSHUser -cne "pc") {
        throw "固定测试服 SSH 目标发生漂移"
    }
    Write-Output "enabled_startup_readonly_candidate_self_test=passed"
    Write-Output "fixed_ssh_target_frozen=true"
    Write-Output "boolean_only_result_contract=true"
    Write-Output "candidate_files_written=0"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ChangeId -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') { throw "ChangeId 必须使用 UTC 基本格式" }
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) { throw "导出候选必须提供全新的 OutputDirectory" }
Assert-LocalAbsolutePath -Path $OutputDirectory -Description "候选输出目录"
$outputPath = [IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputPath
if ([string]::IsNullOrWhiteSpace($outputParent) -or -not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "候选输出目录的父目录必须已存在"
}
if (Test-Path -LiteralPath $outputPath) { throw "候选输出目录已存在，禁止覆盖" }

# 远端负载只读取当前关闭态 API 的进程环境、环境文件元数据和本机 ready 端点。
# 所有短信配置值仅在远端 Python 进程内存中参与判断，stdout 仅允许预定义布尔字段。
$remotePayload = @'
set -euo pipefail
python3 - <<'PY'
import os
import pathlib
import pwd
import re
import stat
import sys
import urllib.request

API_PATH = "/home/pc/molin/molin-api"
ENV_PATH = "/home/pc/molin/infra/.env.test"
SMS_KEYS = (
    "SMS_ENABLED",
    "SMS_TEST_MODE",
    "SMS_PROVIDER",
    "SMS_ALIYUN_ACCESS_KEY_ID",
    "SMS_ALIYUN_ACCESS_KEY_SECRET",
    "SMS_ALIYUN_SIGN_NAME",
    "SMS_ALIYUN_ENDPOINT",
    "SMS_PHONE_HMAC_SECRET",
    "SMS_TEST_PHONE_WHITELIST",
)
LEGACY_KEYS = ("SMS_ACCESS_KEY", "SMS_ACCESS_SECRET", "SMS_SIGN_NAME")

def emit(name, value):
    print(f"{name}={'true' if value else 'false'}")

def find_api_pids():
    matches = []
    for entry in pathlib.Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            if (entry / "cmdline").read_bytes() == API_PATH.encode() + b"\0":
                matches.append(int(entry.name))
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            continue
    return matches

def read_process_environment(pid):
    raw = pathlib.Path(f"/proc/{pid}/environ").read_bytes().split(b"\0")
    result = {}
    counts = {}
    for item in raw:
        if not item:
            continue
        key, separator, value = item.partition(b"=")
        if not separator:
            continue
        name = key.decode("utf-8", "strict")
        text = value.decode("utf-8", "strict")
        counts[name] = counts.get(name, 0) + 1
        result[name] = text
    return result, counts

def read_file_environment(path):
    raw = pathlib.Path(path).read_bytes()
    if raw.startswith(b"\xef\xbb\xbf") or b"\r" in raw:
        raise ValueError("环境文件编码不符合启动器契约")
    result = {}
    counts = {}
    for raw_line in raw.decode("utf-8", "strict").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        match = re.fullmatch(r"(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
        if match is None:
            raise ValueError("环境文件格式不符合启动器契约")
        key, value = match.groups()
        value = value.strip()
        if len(value) > 1 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        counts[key] = counts.get(key, 0) + 1
        result[key] = value
    return result, counts

def validate_sms(environment, counts):
    unique = all(counts.get(key, 0) == 1 for key in SMS_KEYS)
    enabled_false = environment.get("SMS_ENABLED", "").strip().lower() == "false"
    test_mode_true = environment.get("SMS_TEST_MODE", "").strip().lower() == "true"
    provider_aliyun = environment.get("SMS_PROVIDER", "").strip().lower() == "aliyun"
    legacy_absent = all(key not in environment for key in LEGACY_KEYS)
    raw_endpoint = environment.get("SMS_ALIYUN_ENDPOINT", "")
    endpoint = "dysmsapi.aliyuncs.com" if raw_endpoint == "" else raw_endpoint.strip()
    hmac_secret = environment.get("SMS_PHONE_HMAC_SECRET", "")
    required_present = all(
        value.strip() != ""
        for value in (
            environment.get("SMS_ALIYUN_ACCESS_KEY_ID", ""),
            environment.get("SMS_ALIYUN_ACCESS_KEY_SECRET", ""),
            environment.get("SMS_ALIYUN_SIGN_NAME", ""),
            endpoint,
            hmac_secret,
        )
    )
    endpoint_valid = endpoint != "" and "://" not in endpoint and "/" not in endpoint and " " not in endpoint
    hmac_valid = len(hmac_secret.encode("utf-8")) >= 32
    whitelist_nonempty = any(item.strip() for item in environment.get("SMS_TEST_PHONE_WHITELIST", "").split(","))
    return {
        "sms_environment_keys_unique": unique,
        "sms_enabled_false": enabled_false,
        "sms_test_mode_true": test_mode_true,
        "sms_provider_aliyun": provider_aliyun,
        "legacy_sms_keys_absent": legacy_absent,
        "required_sms_values_present": required_present,
        "sms_endpoint_shape_valid": endpoint_valid,
        "sms_hmac_length_valid": hmac_valid,
        "sms_whitelist_nonempty": whitelist_nonempty,
    }

pids = find_api_pids()
single_process = len(pids) == 1
emit("api_process_single", single_process)
if not single_process:
    print("enabled_startup_readonly_diagnostic=blocked")
    raise SystemExit(2)

pid = pids[0]
try:
    binary_identity = os.path.realpath(f"/proc/{pid}/exe") == API_PATH
    env_stat = os.lstat(ENV_PATH)
    env_identity = stat.S_ISREG(env_stat.st_mode) and not stat.S_ISLNK(env_stat.st_mode)
    env_permissions = pwd.getpwuid(env_stat.st_uid).pw_name == "pc" and stat.S_IMODE(env_stat.st_mode) == 0o600
    process_environment, process_counts = read_process_environment(pid)
    file_environment, file_counts = read_file_environment(ENV_PATH)
    process_state = validate_sms(process_environment, process_counts)
    file_state = validate_sms(file_environment, file_counts)
    parity = all(process_environment.get(key) == file_environment.get(key) for key in SMS_KEYS + LEGACY_KEYS)
    try:
        with urllib.request.urlopen("http://127.0.0.1:8080/api/ready", timeout=3) as response:
            closed_ready = response.status == 200
    except Exception:
        closed_ready = False
except Exception:
    binary_identity = False
    env_identity = False
    env_permissions = False
    process_state = {key: False for key in (
        "sms_environment_keys_unique", "sms_enabled_false", "sms_test_mode_true", "sms_provider_aliyun",
        "legacy_sms_keys_absent", "required_sms_values_present", "sms_endpoint_shape_valid",
        "sms_hmac_length_valid", "sms_whitelist_nonempty",
    )}
    file_state = dict(process_state)
    parity = False
    closed_ready = False

emit("api_binary_identity_verified", binary_identity)
emit("environment_file_identity_verified", env_identity)
emit("environment_file_permissions_verified", env_permissions)
for key in (
    "sms_environment_keys_unique",
    "sms_enabled_false",
    "sms_test_mode_true",
    "sms_provider_aliyun",
    "legacy_sms_keys_absent",
    "required_sms_values_present",
    "sms_endpoint_shape_valid",
    "sms_hmac_length_valid",
    "sms_whitelist_nonempty",
):
    emit(key, process_state[key])
emit("environment_file_sms_config_valid", all(file_state.values()))
emit("file_process_sms_config_parity", parity)
emit("current_closed_api_ready", closed_ready)
ready = all((binary_identity, env_identity, env_permissions, parity, closed_ready, *process_state.values(), all(file_state.values())))
emit("enabled_startup_config_ready", ready)
print(f"enabled_startup_readonly_diagnostic={'passed' if ready else 'blocked'}")
print("configuration_mutations=0")
print("service_signals=0")
print("service_restarts=0")
print("business_posts=0")
print("emails_sent=0")
print("sms_submission_requests=0")
print("real_sms_sent=0")
raise SystemExit(0 if ready else 3)
PY
'@
$payloadBase64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($remotePayload))
$sshHelperPath = Join-Path $PSScriptRoot "sms-phase5-test-server-ssh.ps1"
$sshHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
$repoScripts = [IO.Path]::GetFullPath($PSScriptRoot)

$runnerTemplate = @'
param([switch]$ExecuteReadOnly, [switch]$SelfTest, [string]$ExpectedRunnerSHA256 = "")
$ErrorActionPreference = "Stop"
$ChangeId = "__CHANGE_ID__"
$RemotePayloadBase64 = "__PAYLOAD_BASE64__"
$ExpectedSSHHelperSHA256 = "__SSH_HELPER_SHA256__"
$ResultPath = Join-Path (Split-Path -Parent $PSCommandPath) "result-$ChangeId.txt"

if (-not $ExecuteReadOnly -and -not $SelfTest) {
    Write-Output "enabled_startup_readonly_execution_authorized=false"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}
if ($ExecuteReadOnly -and $SelfTest) { throw "ExecuteReadOnly 与 SelfTest 必须互斥" }

if ($SelfTest) {
    $payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
    foreach ($marker in @("SMS_PHONE_HMAC_SECRET", "file_process_sms_config_parity", "business_posts=0", "real_sms_sent=0")) {
        if (-not $payload.Contains($marker)) { throw "只读诊断负载缺少安全标记：$marker" }
    }
    Write-Output "enabled_startup_readonly_runner_self_test=passed"
    Write-Output "boolean_only_result_contract=true"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ($ExpectedRunnerSHA256 -cnotmatch '^[0-9a-f]{64}$') { throw "只读执行必须提供获批的完整 runner SHA-256" }
$actualRunnerSHA256 = (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualRunnerSHA256 -cne $ExpectedRunnerSHA256) { throw "runner SHA-256 与批准值不匹配" }
if (Test-Path -LiteralPath $ResultPath) { throw "低敏结果文件已存在，禁止重复执行" }

$sshHelperPath = Join-Path "__REPO_SCRIPTS__" "sms-phase5-test-server-ssh.ps1"
$actualSSHHelperSHA256 = (Get-FileHash -LiteralPath $sshHelperPath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSSHHelperSHA256 -cne $ExpectedSSHHelperSHA256) { throw "固定 SSH 身份辅助脚本摘要不匹配" }
. $sshHelperPath
$knownHosts = Assert-SmsPhase5FixedTestServerIdentity -ServerHost '8.130.9.163' -SSHPort 10003 -SSHUser 'pc'

$utf8 = New-Object Text.UTF8Encoding($false)
$payload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($RemotePayloadBase64))
$inputBytes = $utf8.GetBytes($payload.Replace("`r`n", "`n").Replace("`r", "`n"))
$startInfo = New-Object Diagnostics.ProcessStartInfo
$startInfo.FileName = "ssh.exe"
$startInfo.UseShellExecute = $false
$startInfo.RedirectStandardInput = $true
$startInfo.RedirectStandardOutput = $true
$startInfo.RedirectStandardError = $true
$startInfo.CreateNoWindow = $true
$startInfo.StandardOutputEncoding = $utf8
$startInfo.StandardErrorEncoding = $utf8
$startInfo.Arguments = "-p 10003 -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 -o `"UserKnownHostsFile=$knownHosts`" -- pc@8.130.9.163 bash -s"
$process = New-Object Diagnostics.Process
$process.StartInfo = $startInfo
try {
    if (-not $process.Start()) { throw "无法启动固定 SSH 只读诊断进程" }
    $stdoutTask = $process.StandardOutput.ReadToEndAsync()
    $stderrTask = $process.StandardError.ReadToEndAsync()
    try {
        $process.StandardInput.BaseStream.Write($inputBytes, 0, $inputBytes.Length)
        $process.StandardInput.BaseStream.Flush()
    }
    finally {
        [Array]::Clear($inputBytes, 0, $inputBytes.Length)
        $process.StandardInput.Close()
    }
    $process.WaitForExit()
    $stdout = $stdoutTask.Result
    $stderr = $stderrTask.Result
    $remoteExitCode = $process.ExitCode
}
finally {
    $process.Dispose()
    $payload = $null
    $inputBytes = $null
}

$safeKeys = @(
    "api_process_single", "api_binary_identity_verified", "environment_file_identity_verified",
    "environment_file_permissions_verified", "sms_environment_keys_unique", "sms_enabled_false",
    "sms_test_mode_true", "sms_provider_aliyun", "legacy_sms_keys_absent", "required_sms_values_present",
    "sms_endpoint_shape_valid", "sms_hmac_length_valid", "sms_whitelist_nonempty",
    "environment_file_sms_config_valid", "file_process_sms_config_parity", "current_closed_api_ready",
    "enabled_startup_config_ready", "enabled_startup_readonly_diagnostic", "configuration_mutations",
    "service_signals", "service_restarts", "business_posts", "emails_sent", "sms_submission_requests", "real_sms_sent"
)
$safeLines = @()
foreach ($line in @($stdout -split "`r?`n" | Where-Object { $_ -ne "" })) {
    if ($line -cnotmatch '^(?<key>[a-z][a-z0-9_]*)=(?:true|false|passed|blocked|0)$' -or
        $safeKeys -cnotcontains $Matches['key']) {
        throw "远端输出不符合低敏字段白名单"
    }
    $safeLines += $line
}
if ($safeLines -cnotcontains "configuration_mutations=0" -or
    $safeLines -cnotcontains "service_signals=0" -or
    $safeLines -cnotcontains "service_restarts=0" -or
    $safeLines -cnotcontains "business_posts=0" -or
    $safeLines -cnotcontains "emails_sent=0" -or
    $safeLines -cnotcontains "sms_submission_requests=0" -or
    $safeLines -cnotcontains "real_sms_sent=0") {
    throw "远端只读零副作用证据不完整"
}
$safeLines += "network_connections=1"
$safeLines += "remote_stderr_present=$(((-not [string]::IsNullOrWhiteSpace($stderr))).ToString().ToLowerInvariant())"
$safeLines += "readonly_exit_code=$remoteExitCode"
$content = ($safeLines -join "`r`n") + "`r`n"
$bytes = [Text.Encoding]::UTF8.GetBytes($content)
$stream = [IO.File]::Open($ResultPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
try { $stream.Write($bytes, 0, $bytes.Length) }
finally { $stream.Dispose(); [Array]::Clear($bytes, 0, $bytes.Length) }
$safeLines | Write-Output
Write-Output "network_connections=1"
Write-Output "remote_stderr_present=$(((-not [string]::IsNullOrWhiteSpace($stderr))).ToString().ToLowerInvariant())"
Write-Output "readonly_exit_code=$remoteExitCode"
Write-Output "low_sensitivity_result_persisted=true"
Write-Output "result_sha256=$((Get-FileHash -LiteralPath $ResultPath -Algorithm SHA256).Hash.ToLowerInvariant())"
if ($remoteExitCode -ne 0) { throw "固定测试服启用态启动只读诊断未通过，退出码：$remoteExitCode" }
'@

$runnerText = $runnerTemplate.Replace("__CHANGE_ID__", $ChangeId).
    Replace("__PAYLOAD_BASE64__", $payloadBase64).
    Replace("__SSH_HELPER_SHA256__", $sshHelperSHA256).
    Replace("__REPO_SCRIPTS__", $repoScripts)
$runnerPath = Join-Path $outputPath "run-sms-phase5-enabled-startup-readonly-diagnostic-$ChangeId.ps1"
$directoryCreated = $false
$fileCreated = $false
try {
    $null = New-Item -ItemType Directory -Path $outputPath -ErrorAction Stop
    $directoryCreated = $true
    [IO.File]::WriteAllText($runnerPath, $runnerText, (New-Object Text.UTF8Encoding($true)))
    $fileCreated = $true

    # 生成阶段只做语法、默认关闭、自测和负载静态检查，绝不进入 ExecuteReadOnly 分支。
    $tokens = $null
    $parseErrors = $null
    $null = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    if (@($parseErrors).Count -ne 0) { throw "runner PowerShell 语法校验失败" }
    $decodedPayload = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($payloadBase64))
    foreach ($forbidden in @("kill ", "systemctl", "docker restart", "curl -X POST", "INSERT ", "UPDATE ", "DELETE ", "scp ", "SMS_ENABLED=true")) {
        if ($decodedPayload.Contains($forbidden)) { throw "远端负载包含禁止动作：$forbidden" }
    }
    $closedOutput = @(& $runnerPath)
    $selfTestOutput = @(& $runnerPath -SelfTest)
    if ($closedOutput -cnotcontains "enabled_startup_readonly_execution_authorized=false" -or
        $selfTestOutput -cnotcontains "enabled_startup_readonly_runner_self_test=passed") {
        throw "runner 默认关闭或离线自测失败"
    }
    $runnerSHA256 = (Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash.ToLowerInvariant()
    Write-Output "enabled_startup_readonly_candidate=passed"
    Write-Output "change_id=$ChangeId"
    Write-Output "runner_sha256=$runnerSHA256"
    Write-Output "runner_path=$runnerPath"
    Write-Output "candidate_files_written=1"
    Write-Output "network_connections=0"
    Write-Output "configuration_mutations=0"
    Write-Output "service_signals=0"
    Write-Output "service_restarts=0"
    Write-Output "business_posts=0"
    Write-Output "emails_sent=0"
    Write-Output "real_sms_sent=0"
}
catch {
    if ($fileCreated -and (Test-Path -LiteralPath $runnerPath -PathType Leaf)) {
        Remove-Item -LiteralPath $runnerPath -Force
    }
    if ($directoryCreated -and (Test-Path -LiteralPath $outputPath -PathType Container) -and
        @(Get-ChildItem -LiteralPath $outputPath -Force).Count -eq 0) {
        Remove-Item -LiteralPath $outputPath -Force
    }
    throw
}
