[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://8.130.9.163:3000',
    [string]$Model = 'molin/qwen-turbo',
    [string]$Prompt = '仅回复 OK',
    [ValidateRange(1, 128)][int]$MaxTokens = 16,
    [ValidateRange(1, 60)][int]$PollCount = 10,
    [ValidateRange(0, 30000)][int]$PollIntervalMilliseconds = 1000,
    [switch]$ConfirmSend
)

$ErrorActionPreference = 'Stop'

# Windows PowerShell 5.1 不会自动加载 HttpClient 所在程序集，显式加载以保持 5.1/7 双版本兼容。
Add-Type -AssemblyName System.Net.Http

# 即使当前进程残留平台 SK，也必须再次显式确认，避免误运行脚本产生真实请求和费用。
if (-not $ConfirmSend) {
    [Console]::Error.WriteLine('未确认发送。仅在明确授权一次真实请求后增加 -ConfirmSend。')
    exit 2
}

function ConvertFrom-SafeJson {
    param([string]$Content)

    if ([string]::IsNullOrWhiteSpace($Content)) {
        return $null
    }
    try {
        return $Content | ConvertFrom-Json
    } catch {
        return $null
    }
}

function Get-PublicErrorSummary {
    param(
        $Body,
        [int]$StatusCode
    )

    $parts = @("HTTP $StatusCode")
    if ($null -ne $Body) {
        if ($null -ne $Body.code) { $parts += "code=$($Body.code)" }
        if (-not [string]::IsNullOrWhiteSpace([string]$Body.error)) { $parts += "error=$($Body.error)" }
        if (-not [string]::IsNullOrWhiteSpace([string]$Body.message)) { $parts += "message=$($Body.message)" }
        if (-not [string]::IsNullOrWhiteSpace([string]$Body.request_id)) { $parts += "request_id=$($Body.request_id)" }
    }
    return ($parts -join '；')
}

function Invoke-MolinRequest {
    param(
        [System.Net.Http.HttpClient]$Client,
        [System.Net.Http.HttpRequestMessage]$Request
    )

    $response = $Client.SendAsync($Request).GetAwaiter().GetResult()
    $content = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
    return [pscustomobject]@{
        Response = $response
        Content = $content
        Body = ConvertFrom-SafeJson -Content $content
    }
}

function Get-RequestId {
    param(
        [System.Net.Http.HttpResponseMessage]$Response,
        $Body
    )

    try {
        $headerValue = $Response.Headers.GetValues('X-Request-ID') | Select-Object -First 1
        if (-not [string]::IsNullOrWhiteSpace([string]$headerValue)) {
            return [string]$headerValue
        }
    } catch {
        # 响应头不存在时继续检查公开 JSON 字段，不输出内部异常。
    }

    if ($null -ne $Body) {
        if (-not [string]::IsNullOrWhiteSpace([string]$Body.request_id)) {
            return [string]$Body.request_id
        }
        if ($null -ne $Body.data -and -not [string]::IsNullOrWhiteSpace([string]$Body.data.request_id)) {
            return [string]$Body.data.request_id
        }
    }
    return $null
}

function Test-TerminalLedger {
    param($Ledger)

    if ($null -eq $Ledger) {
        return 'pending'
    }
    if ($Ledger.execution_status -eq 'succeeded' -and $Ledger.billing_status -eq 'settled') {
        return 'success'
    }
    if ($Ledger.billing_status -in @('released', 'exception')) {
        return 'failure'
    }
    if ($Ledger.execution_status -in @('failed', 'timeout') -and $Ledger.billing_status -ne 'settlement_pending') {
        return 'failure'
    }
    return 'pending'
}

$apiKey = [Environment]::GetEnvironmentVariable('MOLIN_API_KEY', 'Process')
if ([string]::IsNullOrWhiteSpace($apiKey)) {
    [Console]::Error.WriteLine('请先设置 MOLIN_API_KEY 环境变量，平台 SK 只能保存在当前服务端进程中。')
    exit 2
}
$apiKey = $apiKey.Trim()
$normalizedBaseUrl = $BaseUrl.TrimEnd('/')

$client = $null
$chatRequest = $null
$chatResult = $null
try {
    $handler = [System.Net.Http.HttpClientHandler]::new()
    $client = [System.Net.Http.HttpClient]::new($handler, $true)
    $client.Timeout = [TimeSpan]::FromSeconds(30)
    $client.DefaultRequestHeaders.Authorization = [System.Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $apiKey)

    $chatBody = [ordered]@{
        model = $Model
        messages = @(
            [ordered]@{ role = 'user'; content = $Prompt }
        )
        max_tokens = $MaxTokens
        stream = $false
    }
    $chatJson = $chatBody | ConvertTo-Json -Depth 8 -Compress
    $chatRequest = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Post, "$normalizedBaseUrl/v1/chat/completions")
    $chatRequest.Headers.Add('Idempotency-Key', "molin-smoke-$([guid]::NewGuid().ToString('N'))")
    $chatRequest.Content = [System.Net.Http.StringContent]::new($chatJson, [System.Text.Encoding]::UTF8, 'application/json')

    try {
        $chatResult = Invoke-MolinRequest -Client $client -Request $chatRequest
    } catch {
        [Console]::Error.WriteLine('Chat 请求失败：无法连接墨灵测试服务或请求超时。')
        exit 3
    }

    $chatStatusCode = [int]$chatResult.Response.StatusCode
    if ($chatStatusCode -notin @(200, 202)) {
        [Console]::Error.WriteLine("Chat 请求被拒绝：$(Get-PublicErrorSummary -Body $chatResult.Body -StatusCode $chatStatusCode)")
        exit 3
    }

    $requestId = Get-RequestId -Response $chatResult.Response -Body $chatResult.Body
    if ([string]::IsNullOrWhiteSpace($requestId)) {
        [Console]::Error.WriteLine('Chat 响应缺少 request_id，无法核对结算账本。')
        exit 4
    }

    Write-Output "Chat 请求已受理：request_id=$requestId"
    $encodedRequestId = [Uri]::EscapeDataString($requestId)
    for ($attempt = 1; $attempt -le $PollCount; $attempt++) {
        $statusRequest = $null
        $statusResult = $null
        try {
            $statusRequest = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Get, "$normalizedBaseUrl/v1/requests/$encodedRequestId")
            try {
                $statusResult = Invoke-MolinRequest -Client $client -Request $statusRequest
            } catch {
                [Console]::Error.WriteLine('账本查询失败：无法连接墨灵测试服务或请求超时。')
                exit 3
            }

            $statusCode = [int]$statusResult.Response.StatusCode
            if ($statusCode -ne 200) {
                [Console]::Error.WriteLine("账本查询被拒绝：$(Get-PublicErrorSummary -Body $statusResult.Body -StatusCode $statusCode)")
                exit 3
            }

            $ledger = $statusResult.Body
            if ($null -ne $ledger.data) {
                $ledger = $ledger.data
            }
            $terminalState = Test-TerminalLedger -Ledger $ledger
            if ($terminalState -eq 'success') {
                Write-Output "执行状态：$($ledger.execution_status)"
                Write-Output "计费状态：$($ledger.billing_status)"
                Write-Output "输入 Token：$($ledger.input_tokens)"
                Write-Output "输出 Token：$($ledger.output_tokens)"
                Write-Output "结算金额：$($ledger.settled_amount) CNY"
                exit 0
            }
            if ($terminalState -eq 'failure') {
                [Console]::Error.WriteLine("请求未成功结算：request_id=$requestId；execution_status=$($ledger.execution_status)；billing_status=$($ledger.billing_status)")
                exit 3
            }
        } finally {
            if ($null -ne $statusResult -and $null -ne $statusResult.Response) {
                $statusResult.Response.Dispose()
            }
            if ($null -ne $statusRequest) {
                $statusRequest.Dispose()
            }
        }

        if ($attempt -lt $PollCount -and $PollIntervalMilliseconds -gt 0) {
            Start-Sleep -Milliseconds $PollIntervalMilliseconds
        }
    }

    [Console]::Error.WriteLine("账本在轮询期限内尚未完成结算：request_id=$requestId")
    exit 5
} finally {
    if ($null -ne $chatResult -and $null -ne $chatResult.Response) {
        $chatResult.Response.Dispose()
    }
    if ($null -ne $chatRequest) {
        $chatRequest.Dispose()
    }
    if ($null -ne $client) {
        $client.Dispose()
    }
}
