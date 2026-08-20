$ErrorActionPreference = 'Stop'

$scriptPath = Join-Path $PSScriptRoot '..\test_molin_chat.ps1'
$fakeSecret = 'sk-molin-fake-secret-never-print'

function Assert-True {
    param(
        [bool]$Condition,
        [string]$Message
    )

    if (-not $Condition) {
        throw $Message
    }
}

function Invoke-ClientScript {
    param([string[]]$Arguments)

    $stdoutFile = Join-Path ([System.IO.Path]::GetTempPath()) "molin-chat-stdout-$([guid]::NewGuid().ToString('N')).log"
    $stderrFile = Join-Path ([System.IO.Path]::GetTempPath()) "molin-chat-stderr-$([guid]::NewGuid().ToString('N')).log"
    $previousPreference = $ErrorActionPreference
    try {
        # 分离 native stdout/stderr，避免 Windows PowerShell 把预期错误输出提升为测试框架异常。
        $ErrorActionPreference = 'Continue'
        & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $scriptPath @Arguments 1> $stdoutFile 2> $stderrFile
        $exitCode = $LASTEXITCODE
        $stdout = Get-Content -LiteralPath $stdoutFile -Raw -ErrorAction SilentlyContinue
        $stderr = Get-Content -LiteralPath $stderrFile -Raw -ErrorAction SilentlyContinue
        return [pscustomobject]@{
            ExitCode = $exitCode
            Output = "$stdout$stderr"
        }
    } finally {
        $ErrorActionPreference = $previousPreference
        Remove-Item -LiteralPath $stdoutFile, $stderrFile -Force -ErrorAction SilentlyContinue
    }
}

function Get-FreeTcpPort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    try {
        $listener.Start()
        return ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
    } finally {
        $listener.Stop()
    }
}

function Start-FakeMolinServer {
    param(
        [int]$Port,
        [string]$Secret,
        [string]$ReadyFile,
        [string]$TraceFile,
        [ValidateSet('settled', 'pending', 'forbidden')][string]$Scenario
    )

    return Start-Job -ArgumentList $Port, $Secret, $ReadyFile, $TraceFile, $Scenario -ScriptBlock {
        param($Port, $Secret, $ReadyFile, $TraceFile, $Scenario)

        $listener = [System.Net.HttpListener]::new()
        $listener.Prefixes.Add("http://127.0.0.1:$Port/")
        $chatCount = 0
        $statusCount = 0
        $expectedRequestCount = switch ($Scenario) {
            'settled' { 2 }
            'pending' { 3 }
            'forbidden' { 1 }
        }
        try {
            $listener.Start()
            Set-Content -LiteralPath $ReadyFile -Value 'ready' -Encoding ASCII

            while (($chatCount + $statusCount) -lt $expectedRequestCount) {
                $contextTask = $listener.GetContextAsync()
                if (-not $contextTask.Wait(10000)) {
                    throw 'Fake 服务等待客户端请求超时'
                }
                $context = $contextTask.Result
                $request = $context.Request
                $response = $context.Response
                $response.ContentType = 'application/json; charset=utf-8'
                Add-Content -LiteralPath $TraceFile -Value "$($request.HttpMethod) $($request.Url.AbsolutePath)" -Encoding ASCII

                if ($request.HttpMethod -eq 'POST' -and $request.Url.AbsolutePath -eq '/v1/chat/completions') {
                    $chatCount++
                    $reader = [System.IO.StreamReader]::new($request.InputStream, $request.ContentEncoding)
                    try {
                        $body = $reader.ReadToEnd() | ConvertFrom-Json
                    } finally {
                        $reader.Dispose()
                    }

                    if ($request.Headers['Authorization'] -ne "Bearer $Secret") {
                        throw 'Fake 服务收到的 Authorization 不正确'
                    }
                    if ([string]::IsNullOrWhiteSpace($request.Headers['Idempotency-Key'])) {
                        throw 'Fake 服务没有收到 Idempotency-Key'
                    }
                    if ($body.model -ne 'molin/qwen-turbo' -or [int]$body.max_tokens -ne 16 -or $body.stream -ne $false) {
                        throw 'Fake 服务收到的 Chat 请求体不符合契约'
                    }

                    if ($Scenario -eq 'settled') {
                        $response.StatusCode = 200
                        $response.Headers.Add('X-Request-ID', 'req_fake_settled')
                        $payload = '{"id":"chatcmpl_fake","choices":[{"index":0,"message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}}'
                    } elseif ($Scenario -eq 'pending') {
                        $response.StatusCode = 202
                        $payload = '{"code":20201,"error":"settlement_pending","message":"请求结果正在结算","request_id":"req_fake_pending"}'
                    } else {
                        $response.StatusCode = 403
                        $payload = '{"code":40003,"error":"model_not_allowed","message":"该 Project SK 未授权调用此模型","request_id":"req_fake_forbidden"}'
                    }
                } elseif ($request.HttpMethod -eq 'GET' -and $request.Url.AbsolutePath -in @('/v1/requests/req_fake_settled', '/v1/requests/req_fake_pending')) {
                    $statusCount++
                    if ($request.Headers['Authorization'] -ne "Bearer $Secret") {
                        throw 'Fake 状态查询没有复用同一个平台 SK'
                    }
                    $response.StatusCode = 200
                    if ($Scenario -eq 'pending' -and $statusCount -eq 1) {
                        $payload = '{"code":0,"message":"ok","data":{"request_id":"req_fake_pending","execution_status":"succeeded","billing_status":"settlement_pending","input_tokens":"4","output_tokens":"1","settled_amount":null}}'
                    } else {
                        $requestID = if ($Scenario -eq 'pending') { 'req_fake_pending' } else { 'req_fake_settled' }
                        $payload = "{`"code`":0,`"message`":`"ok`",`"data`":{`"request_id`":`"$requestID`",`"execution_status`":`"succeeded`",`"billing_status`":`"settled`",`"input_tokens`":`"4`",`"output_tokens`":`"1`",`"settled_amount`":`"0.00000100`"}}"
                    }
                } else {
                    $response.StatusCode = 404
                    $payload = '{"code":40004,"message":"not found"}'
                }

                $bytes = [System.Text.Encoding]::UTF8.GetBytes($payload)
                $response.ContentLength64 = $bytes.Length
                $response.OutputStream.Write($bytes, 0, $bytes.Length)
                $response.Close()
            }

            [pscustomobject]@{
                ChatCount = $chatCount
                StatusCount = $statusCount
            }
        } finally {
            if ($listener.IsListening) {
                $listener.Stop()
            }
            $listener.Close()
        }
    }
}

function Wait-FakeServerReady {
    param(
        [string]$ReadyFile,
        [System.Management.Automation.Job]$Job
    )

    for ($attempt = 0; $attempt -lt 50; $attempt++) {
        if (Test-Path -LiteralPath $ReadyFile) {
            return
        }
        if ($Job.State -in @('Failed', 'Stopped', 'Completed')) {
            throw "Fake 服务启动失败，状态：$($Job.State)"
        }
        Start-Sleep -Milliseconds 100
    }
    throw '等待 Fake 服务启动超时'
}

$oldKey = $env:MOLIN_API_KEY
try {
    $env:MOLIN_API_KEY = $fakeSecret
    $confirmationResult = Invoke-ClientScript -Arguments @()
    Assert-True ($confirmationResult.ExitCode -ne 0) '缺少显式确认时必须失败'
    Assert-True ($confirmationResult.Output -match 'ConfirmSend') '缺少显式确认时必须提示确认参数'
    Assert-True ($confirmationResult.Output -notmatch [regex]::Escape($fakeSecret)) '确认门禁输出不得包含完整平台 SK'

    Remove-Item Env:MOLIN_API_KEY -ErrorAction SilentlyContinue
    $missingResult = Invoke-ClientScript -Arguments @('-ConfirmSend')
    Assert-True ($missingResult.ExitCode -ne 0) '缺少密钥时必须失败'
    Assert-True ($missingResult.Output -match 'MOLIN_API_KEY') '缺少密钥时必须给出安全提示'

    $port = Get-FreeTcpPort
    $readyFile = Join-Path ([System.IO.Path]::GetTempPath()) "molin-chat-fake-$([guid]::NewGuid().ToString('N')).ready"
    $traceFile = Join-Path ([System.IO.Path]::GetTempPath()) "molin-chat-fake-$([guid]::NewGuid().ToString('N')).trace"
    $job = Start-FakeMolinServer -Port $port -Secret $fakeSecret -ReadyFile $readyFile -TraceFile $traceFile -Scenario 'settled'
    try {
        Wait-FakeServerReady -ReadyFile $readyFile -Job $job
        $env:MOLIN_API_KEY = $fakeSecret
        $clientResult = Invoke-ClientScript -Arguments @('-ConfirmSend', '-BaseUrl', "http://127.0.0.1:$port", '-PollCount', '2', '-PollIntervalMilliseconds', '0')
        $output = $clientResult.Output
        $clientExitCode = $clientResult.ExitCode
        Wait-Job -Job $job -Timeout 10 | Out-Null
        $serverResult = Receive-Job -Job $job -ErrorAction Stop

        Assert-True ($clientExitCode -eq 0) "成功结算场景必须退出 0，实际输出：$output"
        Assert-True ($output -match 'req_fake_settled') '输出必须包含 request_id'
        Assert-True ($output -match 'succeeded') '输出必须包含执行成功状态'
        Assert-True ($output -match 'settled') '输出必须包含已结算状态'
        Assert-True ($output -notmatch [regex]::Escape($fakeSecret)) '输出不得包含完整平台 SK'
        Assert-True ($serverResult.ChatCount -eq 1) 'Fake 服务必须只收到一次 Chat 请求'
        Assert-True ($serverResult.StatusCount -eq 1) 'Fake 服务必须收到一次账本查询'
    } finally {
        if ($job) {
            Stop-Job -Job $job -ErrorAction SilentlyContinue
            Remove-Job -Job $job -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $readyFile -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $traceFile -Force -ErrorAction SilentlyContinue
    }

    $pendingPort = Get-FreeTcpPort
    $pendingReadyFile = Join-Path ([System.IO.Path]::GetTempPath()) "molin-chat-fake-$([guid]::NewGuid().ToString('N')).ready"
    $pendingTraceFile = Join-Path ([System.IO.Path]::GetTempPath()) "molin-chat-fake-$([guid]::NewGuid().ToString('N')).trace"
    $pendingJob = Start-FakeMolinServer -Port $pendingPort -Secret $fakeSecret -ReadyFile $pendingReadyFile -TraceFile $pendingTraceFile -Scenario 'pending'
    try {
        Wait-FakeServerReady -ReadyFile $pendingReadyFile -Job $pendingJob
        $pendingResult = Invoke-ClientScript -Arguments @('-ConfirmSend', '-BaseUrl', "http://127.0.0.1:$pendingPort", '-PollCount', '3', '-PollIntervalMilliseconds', '0')
        Wait-Job -Job $pendingJob -Timeout 10 | Out-Null
        $pendingServerResult = Receive-Job -Job $pendingJob -ErrorAction Stop

        Assert-True ($pendingResult.ExitCode -eq 0) "待结算轮询场景必须退出 0，实际输出：$($pendingResult.Output)"
        Assert-True ($pendingResult.Output -match 'req_fake_pending') '202 响应必须保留 request_id'
        Assert-True ($pendingResult.Output -match 'settled') '轮询后必须输出已结算状态'
        Assert-True ($pendingResult.Output -notmatch [regex]::Escape($fakeSecret)) '待结算输出不得包含完整平台 SK'
        Assert-True ($pendingServerResult.ChatCount -eq 1) '待结算轮询不得重复发送 Chat 请求'
        Assert-True ($pendingServerResult.StatusCount -eq 2) '待结算场景必须查询两次账本'
    } finally {
        if ($pendingJob) {
            Stop-Job -Job $pendingJob -ErrorAction SilentlyContinue
            Remove-Job -Job $pendingJob -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $pendingReadyFile, $pendingTraceFile -Force -ErrorAction SilentlyContinue
    }

    $forbiddenPort = Get-FreeTcpPort
    $forbiddenReadyFile = Join-Path ([System.IO.Path]::GetTempPath()) "molin-chat-fake-$([guid]::NewGuid().ToString('N')).ready"
    $forbiddenTraceFile = Join-Path ([System.IO.Path]::GetTempPath()) "molin-chat-fake-$([guid]::NewGuid().ToString('N')).trace"
    $forbiddenJob = Start-FakeMolinServer -Port $forbiddenPort -Secret $fakeSecret -ReadyFile $forbiddenReadyFile -TraceFile $forbiddenTraceFile -Scenario 'forbidden'
    try {
        Wait-FakeServerReady -ReadyFile $forbiddenReadyFile -Job $forbiddenJob
        $forbiddenResult = Invoke-ClientScript -Arguments @('-ConfirmSend', '-BaseUrl', "http://127.0.0.1:$forbiddenPort", '-PollCount', '1', '-PollIntervalMilliseconds', '0')
        Wait-Job -Job $forbiddenJob -Timeout 10 | Out-Null
        $forbiddenServerResult = Receive-Job -Job $forbiddenJob -ErrorAction Stop

        Assert-True ($forbiddenResult.ExitCode -ne 0) '未授权模型场景必须失败'
        Assert-True ($forbiddenResult.Output -match '40003') '错误输出必须包含平台稳定错误码'
        Assert-True ($forbiddenResult.Output -match '未授权调用此模型') '错误输出必须包含公开中文消息'
        Assert-True ($forbiddenResult.Output -notmatch [regex]::Escape($fakeSecret)) '错误输出不得包含完整平台 SK'
        Assert-True ($forbiddenServerResult.ChatCount -eq 1) '未授权模型场景只能发送一次 Chat 请求'
        Assert-True ($forbiddenServerResult.StatusCount -eq 0) 'Chat 被拒绝后不得查询账本'
    } finally {
        if ($forbiddenJob) {
            Stop-Job -Job $forbiddenJob -ErrorAction SilentlyContinue
            Remove-Job -Job $forbiddenJob -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $forbiddenReadyFile, $forbiddenTraceFile -Force -ErrorAction SilentlyContinue
    }

    Write-Output 'PASS: 墨灵 Chat 冒烟脚本 Fake 测试通过'
} finally {
    if ($null -eq $oldKey) {
        Remove-Item Env:MOLIN_API_KEY -ErrorAction SilentlyContinue
    } else {
        $env:MOLIN_API_KEY = $oldKey
    }
}
