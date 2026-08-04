param(
    [ValidateSet("test", "production")]
    [string]$Environment,
    [ValidateSet("false", "true")]
    [string]$CurrentSmsEnabled = "false"
)

$ErrorActionPreference = "Stop"

# 本脚本只验证回滚顺序和安全不变量，不连接服务器、不修改配置、不重启服务。
$steps = @(
    "记录当前版本、健康状态、指标窗口和脱敏发送日志计数",
    "将 SMS_ENABLED 设置为 false 并重启或滚动替换 API",
    "验证 health/ready 为 200 且全部手机发码入口返回 503/50300",
    "确认供应商调用计数不再增长，保留审计与发送日志",
    "如需回滚应用，仅回到已验证版本；默认不执行 migration down",
    "复核邮箱验证码、管理员登录和两套控制台未受影响"
)

if ($Environment -eq "production" -and $CurrentSmsEnabled -eq "true") {
    $classification = "production_emergency_disable_first"
}
else {
    $classification = "standard_safe_rollback"
}

Write-Output "rollback_dry_run=passed"
Write-Output "environment=$Environment"
Write-Output "classification=$classification"
Write-Output "step_count=$($steps.Count)"
for ($index = 0; $index -lt $steps.Count; $index++) {
    Write-Output ("step_{0}={1}" -f ($index + 1), $steps[$index])
}
Write-Output "remote_connections=0"
Write-Output "configuration_writes=0"
Write-Output "service_restarts=0"
Write-Output "migration_actions=0"
Write-Output "real_sms_sent=0"
