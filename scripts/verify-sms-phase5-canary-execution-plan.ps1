param(
    [string]$PlanFile = "",
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function Assert-CanaryExecutionPlan {
    param([Parameter(Mandatory = $true)]$Plan)

    $allowedPlanFields = @(
        "change_id", "environment", "sms_test_mode", "restore_sms_enabled", "no_retries",
        "requested_sends", "max_sends", "acceptance_scope", "business_state_changes",
        "business_state_rollback_approved", "disposable_accounts", "scenes"
    )
    $actualPlanFields = @($Plan.PSObject.Properties.Name)
    if (@($actualPlanFields | Where-Object { $allowedPlanFields -cnotcontains $_ }).Count -ne 0) {
        throw "Canary 计划包含未定义字段，禁止借此持久化敏感值"
    }

    if ($Plan.change_id -cnotmatch '^[0-9]{8}T[0-9]{6}Z$') {
        throw "Canary ChangeId 必须使用 UTC 基本格式"
    }
    if ($Plan.environment -cne "test" -or $Plan.sms_test_mode -ne $true -or
        $Plan.restore_sms_enabled -cne "false" -or $Plan.no_retries -ne $true) {
        throw "Canary 必须固定为测试服、测试模式、零重试并恢复短信关闭态"
    }
    if ([int]$Plan.max_sends -lt 5 -or [int]$Plan.max_sends -gt 10 -or [int]$Plan.requested_sends -ne 5) {
        throw "Canary 必须恰好计划五次提交，窗口硬上限不得超过十次"
    }

    $requiredScenes = @("register", "login", "reset_password", "bind_phone", "admin_verify")
    $sceneNames = @($Plan.scenes | ForEach-Object { [string]$_.scene })
    if ($sceneNames.Count -ne 5 -or @($sceneNames | Sort-Object -Unique).Count -ne 5) {
        throw "Canary 场景必须恰好五项且不得重复"
    }
    foreach ($scene in $requiredScenes) {
        if ($sceneNames -cnotcontains $scene) {
            throw "Canary 缺少固定场景：$scene"
        }
    }

    # 计划文件只保存无业务含义的目标别名，不允许手机号、OTP、Token 或密钥进入磁盘。
    $raw = $Plan | ConvertTo-Json -Depth 8 -Compress
    foreach ($forbidden in @(
        '(?<!\d)1[3-9]\d{9}(?!\d)',
        '(?i)bearer\s+[a-z0-9._-]+',
        '(?i)"(?:access[_-]?key|secret|token|password|otp|verification[_-]?code)"\s*:'
    )) {
        if ($raw -match $forbidden) {
            throw "Canary 计划包含禁止持久化的敏感字段或值"
        }
    }

    $byScene = @{}
    foreach ($entry in $Plan.scenes) {
        $sceneFields = @($entry.PSObject.Properties.Name)
        if ($sceneFields.Count -ne 3 -or
            @($sceneFields | Where-Object { @("scene", "target_alias", "target_state") -cnotcontains $_ }).Count -ne 0) {
            throw "场景计划字段必须严格限定为 scene、target_alias、target_state"
        }
        $alias = [string]$entry.target_alias
        if ($alias -cnotmatch '^target-[a-z][a-z0-9-]{2,31}$') {
            throw "目标只能使用 target- 前缀的低敏别名"
        }
        $byScene[[string]$entry.scene] = $entry
    }

    if ($byScene.register.target_state -cne "unregistered" -or
        $byScene.login.target_state -cne "registered" -or
        $byScene.reset_password.target_state -cne "registered" -or
        $byScene.admin_verify.target_state -cne "registered_admin") {
        throw "场景目标账号状态与业务入口前置条件不一致"
    }
    if ($byScene.register.target_alias -ceq $byScene.login.target_alias -or
        $byScene.register.target_alias -ceq $byScene.reset_password.target_alias -or
        $byScene.register.target_alias -ceq $byScene.admin_verify.target_alias) {
        throw "未注册目标与已注册目标不能复用同一号码别名"
    }

    switch ([string]$Plan.acceptance_scope) {
        "receipt_only" {
            if ($Plan.business_state_changes -ne $false) {
                throw "仅收件 Canary 不得授权业务状态变更"
            }
        }
        "full_business_consume" {
            if ($Plan.business_state_changes -ne $true -or
                $Plan.business_state_rollback_approved -ne $true -or
                $Plan.disposable_accounts -ne $true) {
                throw "完整消费验收必须独立批准业务变更、回滚和一次性账号"
            }
            if ($byScene.bind_phone.target_state -cne "unregistered" -or
                $byScene.bind_phone.target_alias -ceq $byScene.register.target_alias) {
                throw "完整消费验收的注册号码与换绑新号码必须相互独立"
            }
        }
        default {
            throw "acceptance_scope 只能是 receipt_only 或 full_business_consume"
        }
    }

    Write-Output "canary_execution_plan=passed"
    Write-Output "change_id=$($Plan.change_id)"
    Write-Output "acceptance_scope=$($Plan.acceptance_scope)"
    Write-Output "requested_sends=5"
    Write-Output "max_sends=$($Plan.max_sends)"
    Write-Output "no_retries=true"
    Write-Output "sensitive_values_persisted=0"
    Write-Output "network_connections=0"
    Write-Output "real_sms_sent=0"
}

if ($SelfTest) {
    $valid = [pscustomobject]@{
        change_id = "20990101T000000Z"
        environment = "test"
        sms_test_mode = $true
        restore_sms_enabled = "false"
        no_retries = $true
        requested_sends = 5
        max_sends = 5
        acceptance_scope = "receipt_only"
        business_state_changes = $false
        scenes = @(
            [pscustomobject]@{ scene = "register"; target_alias = "target-new"; target_state = "unregistered" },
            [pscustomobject]@{ scene = "login"; target_alias = "target-admin"; target_state = "registered" },
            [pscustomobject]@{ scene = "reset_password"; target_alias = "target-admin"; target_state = "registered" },
            [pscustomobject]@{ scene = "bind_phone"; target_alias = "target-admin"; target_state = "registered" },
            [pscustomobject]@{ scene = "admin_verify"; target_alias = "target-admin"; target_state = "registered_admin" }
        )
    }
    $null = @(Assert-CanaryExecutionPlan -Plan $valid)

    $invalid = $valid | ConvertTo-Json -Depth 8 | ConvertFrom-Json
    foreach ($entry in $invalid.scenes) { $entry.target_alias = "target-one" }
    $rejected = $false
    try { $null = @(Assert-CanaryExecutionPlan -Plan $invalid) } catch { $rejected = $true }
    if (-not $rejected) {
        throw "单号码同时承担注册与已注册场景的反例未被阻断"
    }
    Write-Output "canary_execution_plan_self_test=passed"
    Write-Output "single_target_conflict_rejected=true"
    Write-Output "network_connections=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

if ([string]::IsNullOrWhiteSpace($PlanFile)) {
    throw "必须提供 PlanFile，或显式使用 SelfTest"
}
$resolved = (Resolve-Path -LiteralPath $PlanFile).Path
$plan = Get-Content -LiteralPath $resolved -Raw -Encoding UTF8 | ConvertFrom-Json
Assert-CanaryExecutionPlan -Plan $plan
