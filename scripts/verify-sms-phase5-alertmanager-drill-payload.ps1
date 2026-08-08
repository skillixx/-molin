[CmdletBinding(DefaultParameterSetName = "Validate")]
param(
    [Parameter(Mandatory = $true, ParameterSetName = "Validate")]
    [ValidateNotNullOrEmpty()]
    [string]$FiringPayloadPath,

    [Parameter(Mandatory = $true, ParameterSetName = "Validate")]
    [ValidateNotNullOrEmpty()]
    [string]$ResolvedPayloadPath,

    [Parameter(Mandatory = $true, ParameterSetName = "Validate")]
    [ValidatePattern('^[0-9]{8}T[0-9]{6}Z$')]
    [string]$ChangeId,

    [Parameter(Mandatory = $true, ParameterSetName = "SelfTest")]
    [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

function ConvertTo-AlertArray {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Json,

        [Parameter(Mandatory = $true)]
        [string]$SourceName
    )

    $parsed = $Json | ConvertFrom-Json
    $items = @($parsed)
    if ($items.Count -ne 1) {
        throw "$SourceName 必须且只能包含 1 条告警"
    }
    return $items
}

function Assert-ExactLabels {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Labels,

        [Parameter(Mandatory = $true)]
        [string]$ExpectedChangeId,

        [Parameter(Mandatory = $true)]
        [string]$SourceName
    )

    $expected = [ordered]@{
        alertname       = "MolinSMSDrill"
        drill_change_id = $ExpectedChangeId
        environment     = "test"
        result_type     = "synthetic"
        scene           = "notification_drill"
        severity        = "info"
    }
    $actualNames = @($Labels.PSObject.Properties.Name | Sort-Object)
    $expectedNames = @($expected.Keys | Sort-Object)
    if (($actualNames -join "`n") -cne ($expectedNames -join "`n")) {
        throw "$SourceName 的标签集合不符合精确演练路由契约"
    }
    foreach ($name in $expected.Keys) {
        if ([string]$Labels.$name -cne [string]$expected[$name]) {
            throw "$SourceName 的标签 $name 不符合精确演练路由契约"
        }
    }
}

function Assert-DrillTransition {
    param(
        [Parameter(Mandatory = $true)]
        [object]$Firing,

        [Parameter(Mandatory = $true)]
        [object]$Resolved,

        [Parameter(Mandatory = $true)]
        [string]$ExpectedChangeId
    )

    Assert-ExactLabels -Labels $Firing.labels -ExpectedChangeId $ExpectedChangeId -SourceName "firing 载荷"
    Assert-ExactLabels -Labels $Resolved.labels -ExpectedChangeId $ExpectedChangeId -SourceName "resolved 载荷"

    $firingStart = [DateTimeOffset]::Parse([string]$Firing.startsAt)
    $firingEnd = [DateTimeOffset]::Parse([string]$Firing.endsAt)
    $resolvedStart = [DateTimeOffset]::Parse([string]$Resolved.startsAt)
    $resolvedEnd = [DateTimeOffset]::Parse([string]$Resolved.endsAt)

    if ($firingEnd -le $firingStart) {
        throw "firing endsAt 必须晚于 startsAt"
    }
    if ($resolvedStart -ne $firingStart) {
        throw "resolved startsAt 必须与 firing startsAt 完全一致，以保持同一告警指纹"
    }
    if ($resolvedEnd -le $resolvedStart) {
        throw "resolved endsAt 必须晚于 startsAt；HTTP 200 不能替代有效状态转换"
    }
}

function New-TestAlert {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ExpectedChangeId,

        [Parameter(Mandatory = $true)]
        [string]$StartsAt,

        [Parameter(Mandatory = $true)]
        [string]$EndsAt
    )

    return [pscustomobject]@{
        labels = [pscustomobject][ordered]@{
            alertname       = "MolinSMSDrill"
            environment     = "test"
            scene           = "notification_drill"
            result_type     = "synthetic"
            severity        = "info"
            drill_change_id = $ExpectedChangeId
        }
        startsAt = $StartsAt
        endsAt   = $EndsAt
    }
}

if ($SelfTest) {
    $testChangeId = "20990101T000000Z"
    $firing = New-TestAlert -ExpectedChangeId $testChangeId `
        -StartsAt "2099-01-01T00:00:00Z" -EndsAt "2099-01-01T00:10:00Z"
    $validResolved = New-TestAlert -ExpectedChangeId $testChangeId `
        -StartsAt "2099-01-01T00:00:00Z" -EndsAt "2099-01-01T00:01:00Z"
    Assert-DrillTransition -Firing $firing -Resolved $validResolved -ExpectedChangeId $testChangeId

    $invalidResolved = New-TestAlert -ExpectedChangeId $testChangeId `
        -StartsAt "2099-01-01T00:00:00Z" -EndsAt "2098-12-31T23:59:59Z"
    $invalidRejected = $false
    try {
        Assert-DrillTransition -Firing $firing -Resolved $invalidResolved -ExpectedChangeId $testChangeId
    }
    catch {
        $invalidRejected = $true
    }
    if (-not $invalidRejected) {
        throw "自测失败：无效 resolved 时间戳未被拒绝"
    }

    Write-Output "payload_transition_self_test=passed"
    Write-Output "invalid_resolved_timestamp_rejected=true"
    Write-Output "external_actions=0"
    Write-Output "notification_posts=0"
    Write-Output "real_sms_sent=0"
    exit 0
}

$firingResolvedPath = (Resolve-Path -LiteralPath $FiringPayloadPath).Path
$resolvedResolvedPath = (Resolve-Path -LiteralPath $ResolvedPayloadPath).Path
if ($firingResolvedPath -ceq $resolvedResolvedPath) {
    throw "firing 与 resolved 必须使用两个独立文件"
}

$firingJson = Get-Content -LiteralPath $firingResolvedPath -Raw -Encoding UTF8
$resolvedJson = Get-Content -LiteralPath $resolvedResolvedPath -Raw -Encoding UTF8
$firingItems = ConvertTo-AlertArray -Json $firingJson -SourceName "firing 载荷"
$resolvedItems = ConvertTo-AlertArray -Json $resolvedJson -SourceName "resolved 载荷"
Assert-DrillTransition -Firing $firingItems[0] -Resolved $resolvedItems[0] -ExpectedChangeId $ChangeId

Write-Output "payload_transition=passed"
Write-Output "change_id=$ChangeId"
Write-Output "synthetic_alert_firing_count=1"
Write-Output "synthetic_alert_resolved_count=1"
Write-Output "notification_posts=0"
Write-Output "real_sms_sent=0"
