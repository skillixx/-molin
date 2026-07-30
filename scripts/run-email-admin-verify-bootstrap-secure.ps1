[CmdletBinding(DefaultParameterSetName = "Run")]
param(
    [Parameter(Mandatory = $true, ParameterSetName = "Run")]
    [string]$ApiBase,

    [Parameter(Mandatory = $true, ParameterSetName = "Run")]
    [string]$TemplateId,

    [Parameter(Mandatory = $true, ParameterSetName = "SelfTest")]
    [switch]$SelfTest
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# 固定确认短语明确约束本次操作最多发出一次请求。

$script:ConfirmationPhrase = "I_CONFIRM_SINGLE_EMAIL_ADMIN_VERIFY_BOOTSTRAP_CALL"
$script:BootstrapPath = "/api/internal/email/bootstrap/admin-verify"
$script:TimeoutSeconds = 15
$script:MaximumResponseBytes = 4096

Add-Type -AssemblyName System.Net.Http

function Test-TemplateId {
    param([AllowEmptyString()][string]$Value)

    if ($null -eq $Value -or $Value.Length -lt 1 -or $Value.Length -gt 64) { return $false }
    $containsNonZero = $false
    foreach ($character in $Value.ToCharArray()) {
        $characterCode = [int]$character
        if ($characterCode -lt 48 -or $characterCode -gt 57) { return $false }
        if ($characterCode -ne 48) { $containsNonZero = $true }
    }
    return $containsNonZero
}

function ConvertTo-ValidatedApiBase {
    param([AllowEmptyString()][string]$Value)

    $parsed = $null
    if ([string]::IsNullOrWhiteSpace($Value) -or
        -not [Uri]::TryCreate($Value, [UriKind]::Absolute, [ref]$parsed)) {
        return $null
    }

    # 基址禁止携带凭据、查询串、片段或业务路径，避免改变固定接口目标。

    if ($parsed.Scheme -cne 'http' -and $parsed.Scheme -cne 'https') { return $null }
    if ([string]::IsNullOrWhiteSpace($parsed.Host)) { return $null }
    if (-not [string]::IsNullOrEmpty($parsed.UserInfo)) { return $null }
    if (-not [string]::IsNullOrEmpty($parsed.Query)) { return $null }
    if (-not [string]::IsNullOrEmpty($parsed.Fragment)) { return $null }
    if ($parsed.AbsolutePath -ne '/') { return $null }

    # 真实远端只允许 HTTPS；HTTP 仅服务于本机回环或 SSH 本地端口转发。

    if ($parsed.Scheme -ceq 'http') {
        $hostValue = $parsed.DnsSafeHost
        if ($hostValue -ieq 'localhost') { return $parsed }
        if ($hostValue.Contains(':')) {
            $ipv6Address = $null
            if ([Net.IPAddress]::TryParse($hostValue, [ref]$ipv6Address) -and $ipv6Address.Equals([Net.IPAddress]::IPv6Loopback)) {
                return $parsed
            }
            return $null
        }
        if ($hostValue -notmatch '^\d{1,3}(?:\.\d{1,3}){3}$') { return $null }

        $loopbackAddress = $null
        if (-not [Net.IPAddress]::TryParse($hostValue, [ref]$loopbackAddress)) { return $null }
        if ($loopbackAddress.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) { return $null }
        if ($loopbackAddress.GetAddressBytes()[0] -ne 127) { return $null }
    }

    return $parsed
}

function Test-SingleLineSecret {
    param([AllowEmptyString()][string]$Value)

    if ([string]::IsNullOrEmpty($Value) -or $Value.Length -gt 8192 -or $Value -ne $Value.Trim()) {
        return $false
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            return $false
        }
    }
    return $true
}

function ConvertFrom-SecureInput {
    param([Security.SecureString]$Value)

    $pointer = [IntPtr]::Zero
    try {
        $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Value)
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    }
    finally {
        if ($pointer -ne [IntPtr]::Zero) {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
        }
        $pointer = [IntPtr]::Zero
    }
}

function New-IdempotencyKey {
    $randomBytes = New-Object byte[] 32
    $generator = $null
    try {
        $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
        $generator.GetBytes($randomBytes)
        return [Convert]::ToBase64String($randomBytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
    }
    finally {
        if ($null -ne $generator) {
            $generator.Dispose()
        }
        [Array]::Clear($randomBytes, 0, $randomBytes.Length)
        $randomBytes = $null
        $generator = $null
    }
}

function New-SecureHttpClientBundle {
    $handler = $null
    $client = $null
    try {
        $handler = [System.Net.Http.HttpClientHandler]::new()
        $handler.AllowAutoRedirect = $false
        $handler.UseCookies = $false
        $client = [System.Net.Http.HttpClient]::new($handler, $false)
        $client.Timeout = [TimeSpan]::FromSeconds($script:TimeoutSeconds)
        return [pscustomobject]@{ Handler = $handler; Client = $client }
    }
    catch {
        if ($null -ne $client) { $client.Dispose() }
        if ($null -ne $handler) { $handler.Dispose() }
        throw
    }
}

function New-BootstrapRequest {
    param(
        [Uri]$BaseUri,
        [string]$ProviderTemplateId,
        [string]$AdminJwt,
        [string]$BootstrapToken,
        [string]$IdempotencyKey
    )

    $request = $null
    $content = $null
    $body = $null
    $targetUri = $null
    try {
        $targetUri = [Uri]::new($BaseUri, $script:BootstrapPath)
        $request = [System.Net.Http.HttpRequestMessage]::new([System.Net.Http.HttpMethod]::Post, $targetUri)
        $request.Headers.Authorization = [System.Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $AdminJwt)
        if (-not $request.Headers.TryAddWithoutValidation('X-Email-Bootstrap-Token', $BootstrapToken)) {
            throw 'header_rejected'
        }
        if (-not $request.Headers.TryAddWithoutValidation('Idempotency-Key', $IdempotencyKey)) {
            throw 'header_rejected'
        }

        # 模板编号已经限定为 ASCII 数字，因此可安全构造仅含一个字段的固定 JSON。

        $body = ('{"provider_template_id":"' + $ProviderTemplateId + '"}')
        $content = [System.Net.Http.StringContent]::new($body, [Text.Encoding]::UTF8)
        [void]$content.Headers.Remove('Content-Type')
        if (-not $content.Headers.TryAddWithoutValidation('Content-Type', 'application/json;charset=utf-8')) {
            throw 'content_type_rejected'
        }
        $request.Content = $content
        return $request
    }
    catch {
        if ($null -ne $content) { $content.Dispose() }
        if ($null -ne $request) { $request.Dispose() }
        throw
    }
}

function Test-BootstrapSuccessResponse {
    param([AllowNull()][byte[]]$ContentBytes)

    if ($null -eq $ContentBytes -or $ContentBytes.Length -gt $script:MaximumResponseBytes) { return $false }
    $expectedBytes = [Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}}')
    $acceptedLength = $expectedBytes.Length
    if ($ContentBytes.Length -ne $acceptedLength -and $ContentBytes.Length -ne ($acceptedLength + 1)) { return $false }
    for ($index = 0; $index -lt $acceptedLength; $index++) {
        if ($ContentBytes[$index] -ne $expectedBytes[$index]) { return $false }
    }

    # Go 编码器可在唯一 JSON 后附加一个 LF；其他 BOM、CR、空白、重排或重复字段均失败关闭。

    return $ContentBytes.Length -eq $acceptedLength -or $ContentBytes[$acceptedLength] -eq 10
}

function Read-BoundedResponseBytes {
    param(
        [IO.Stream]$Stream,
        [Threading.CancellationToken]$CancellationToken
    )

    $buffer = New-Object byte[] 1024
    $output = [IO.MemoryStream]::new()
    try {
        while ($true) {
            # 最多再读取到第 4097 字节，以便在不缓冲完整响应的情况下确认超限。

            $remainingProbeBytes = ($script:MaximumResponseBytes + 1) - [int]$output.Length
            if ($remainingProbeBytes -le 0) {
                return [pscustomobject]@{ Exceeded = $true; Bytes = [byte[]]@() }
            }
            $readLength = [Math]::Min($buffer.Length, $remainingProbeBytes)
            $readCount = $Stream.ReadAsync($buffer, 0, $readLength, $CancellationToken).GetAwaiter().GetResult()
            if ($readCount -eq 0) { break }
            $output.Write($buffer, 0, $readCount)
            if ($output.Length -gt $script:MaximumResponseBytes) {
                return [pscustomobject]@{ Exceeded = $true; Bytes = [byte[]]@() }
            }
        }
        return [pscustomobject]@{ Exceeded = $false; Bytes = [byte[]]$output.ToArray() }
    }
    finally {
        [Array]::Clear($buffer, 0, $buffer.Length)
        $output.Dispose()
        $buffer = $null
        $output = $null
    }
}

function New-SafeResult {
    param([string]$Status, [int]$HttpStatus, [string]$Code)
    return [pscustomobject]@{ Status = $Status; HttpStatus = $HttpStatus; Code = $Code }
}

function Format-SafeResult {
    param($Result)

    $allowedStatus = @('SUCCESS', 'BLOCKED', 'PASS', 'FAIL')
    $allowedCode = @(
        'bootstrap_configured', 'invalid_template_id', 'invalid_api_base', 'confirmation_rejected',
        'secret_input_invalid', 'request_failed', 'response_http_rejected',
        'response_contract_rejected', 'self_test_passed', 'self_test_failed'
    )
    if ($allowedStatus -cnotcontains $Result.Status -or $allowedCode -cnotcontains $Result.Code -or
        $Result.HttpStatus -lt 0 -or $Result.HttpStatus -gt 599) {
        return '{"status":"FAIL","http_status":0,"code":"self_test_failed"}'
    }
    return '{"status":"' + $Result.Status + '","http_status":' + $Result.HttpStatus + ',"code":"' + $Result.Code + '"}'
}

function Invoke-BootstrapCore {
    param(
        [Uri]$BaseUri,
        [string]$ProviderTemplateId,
        [string]$AdminJwt,
        [string]$BootstrapToken,
        [string]$IdempotencyKey,
        [scriptblock]$Transport
    )

    $request = $null
    $transportResult = $null
    try {
        $request = New-BootstrapRequest -BaseUri $BaseUri -ProviderTemplateId $ProviderTemplateId -AdminJwt $AdminJwt -BootstrapToken $BootstrapToken -IdempotencyKey $IdempotencyKey
        if ($null -eq $Transport) { throw 'transport_missing' }
        # 传输函数在此处且仅在此处调用一次；失败不会进入重试分支。

        $transportResult = & $Transport -Request $request
        if ($null -eq $transportResult -or $transportResult -is [array] -or $transportResult.HttpStatus -isnot [int]) {
            return New-SafeResult 'BLOCKED' 0 'request_failed'
        }
        if ($transportResult.HttpStatus -ne 200) {
            return New-SafeResult 'BLOCKED' $transportResult.HttpStatus 'response_http_rejected'
        }
        if (-not (Test-BootstrapSuccessResponse -ContentBytes $transportResult.ContentBytes)) {
            return New-SafeResult 'BLOCKED' 200 'response_contract_rejected'
        }
        return New-SafeResult 'SUCCESS' 200 'bootstrap_configured'
    }
    finally {
        if ($null -ne $request) { $request.Dispose() }
        $request = $null
    }
}

function Invoke-RealTransport {
    param([System.Net.Http.HttpRequestMessage]$Request)

    $bundle = $null
    $response = $null
    $contentStream = $null
    $readResult = $null
    $deadlineSource = $null
    $streamTask = $null
    $deadlineTask = $null
    try {
        $bundle = New-SecureHttpClientBundle
        # 单一取消源从发送前开始计时，响应头、取得响应流和全部正文读取共享同一总期限。

        $deadlineSource = [Threading.CancellationTokenSource]::new([TimeSpan]::FromSeconds($script:TimeoutSeconds))
        $response = $bundle.Client.SendAsync($Request, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead, $deadlineSource.Token).GetAwaiter().GetResult()

        # 旧版框架的 ReadAsStreamAsync 没有 token 重载，因此与同一取消源的期限任务竞争。

        $streamTask = $response.Content.ReadAsStreamAsync()
        $deadlineTask = [Threading.Tasks.Task]::Delay([Threading.Timeout]::Infinite, $deadlineSource.Token)
        $completedTask = [Threading.Tasks.Task]::WhenAny([Threading.Tasks.Task[]]@($streamTask, $deadlineTask)).GetAwaiter().GetResult()
        if (-not [object]::ReferenceEquals($completedTask, $streamTask)) {
            $deadlineSource.Token.ThrowIfCancellationRequested()
            throw 'response_stream_deadline_exceeded'
        }
        $contentStream = $streamTask.GetAwaiter().GetResult()
        $readResult = Read-BoundedResponseBytes -Stream $contentStream -CancellationToken $deadlineSource.Token
        return [pscustomobject]@{ HttpStatus = [int]$response.StatusCode; ContentBytes = $readResult.Bytes }
    }
    finally {
        if ($null -ne $contentStream) { $contentStream.Dispose() }
        if ($null -ne $response) { $response.Dispose() }
        if ($null -ne $bundle) {
            $bundle.Client.Dispose()
            $bundle.Handler.Dispose()
        }
        if ($null -ne $deadlineSource) {
            try { $deadlineSource.Cancel() } catch [ObjectDisposedException] { }
            $deadlineSource.Dispose()
        }
        $completedTask = $null
        $deadlineTask = $null
        $streamTask = $null
        $deadlineSource = $null
        $readResult = $null
        $contentStream = $null
        $response = $null
        $bundle = $null
    }
}

function Assert-OfflineCondition {
    param([bool]$Condition)
    if (-not $Condition) { throw 'self_test_assertion_failed' }
}

function Invoke-OfflineSelfTest {
    $sentinelJwt = 'jwt_selftest_secret_7fba'
    $sentinelToken = 'bootstrap_selftest_secret_2cd1'
    $sentinelKey = 'idempotency_selftest_secret_91ab'
    $stateVariableName = 'MolinEmailBootstrapSelfTestState_4f9299dfec7c4f00a489542db2216266'
    $state = @{ RequestCount = 0; Method = ''; Path = ''; ContentMediaType = ''; ContentCharset = ''; Body = ''; Authorization = ''; Token = ''; Key = '' }
    # 假传输会跨越函数调用作用域；仅离线自测使用唯一全局引用共享同一个状态对象。

    New-Variable -Scope Global -Name $stateVariableName -Value $state -Option None -ErrorAction Stop
    try {
        if ($null -eq ('MolinEmailBootstrapBlockingStream' -as [type])) {
            Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Threading;
using System.Threading.Tasks;

public sealed class MolinEmailBootstrapBlockingStream : Stream
{
    public int AsyncReadCalls { get; private set; }
    public int SyncReadCalls { get; private set; }
    public override bool CanRead { get { return true; } }
    public override bool CanSeek { get { return false; } }
    public override bool CanWrite { get { return false; } }
    public override long Length { get { throw new NotSupportedException(); } }
    public override long Position { get { throw new NotSupportedException(); } set { throw new NotSupportedException(); } }
    public override void Flush() { }
    public override int Read(byte[] buffer, int offset, int count)
    {
        SyncReadCalls++;
        throw new InvalidOperationException("synchronous_read_forbidden");
    }
    public override async Task<int> ReadAsync(byte[] buffer, int offset, int count, CancellationToken cancellationToken)
    {
        AsyncReadCalls++;
        await Task.Delay(Timeout.Infinite, cancellationToken).ConfigureAwait(false);
        return 0;
    }
    public override long Seek(long offset, SeekOrigin origin) { throw new NotSupportedException(); }
    public override void SetLength(long value) { throw new NotSupportedException(); }
    public override void Write(byte[] buffer, int offset, int count) { throw new NotSupportedException(); }
}
'@
        }

        $baseUri = ConvertTo-ValidatedApiBase 'https://localhost:8443'
        Assert-OfflineCondition ($null -ne $baseUri)

        $generatedKey = New-IdempotencyKey
        Assert-OfflineCondition ($generatedKey -cmatch '^[A-Za-z0-9_-]{16,128}$')

        $fakeTransport = {
            param([System.Net.Http.HttpRequestMessage]$Request)
            $sharedState = (Get-Variable -Scope Global -Name 'MolinEmailBootstrapSelfTestState_4f9299dfec7c4f00a489542db2216266' -ErrorAction Stop).Value
            $sharedState['RequestCount'] = ([int]$sharedState['RequestCount'] + 1)
            if ($sharedState['RequestCount'] -gt 1) { throw 'second_request_detected' }
            $sharedState.Method = $Request.Method.Method
            $sharedState.Path = $Request.RequestUri.AbsolutePath
            $sharedState.ContentMediaType = $Request.Content.Headers.ContentType.MediaType
            $sharedState.ContentCharset = $Request.Content.Headers.ContentType.CharSet
            $sharedState.Body = $Request.Content.ReadAsStringAsync().GetAwaiter().GetResult()
            $sharedState.Authorization = $Request.Headers.Authorization.ToString()
            $sharedState.Token = $Request.Headers.GetValues('X-Email-Bootstrap-Token') | Select-Object -First 1
            $sharedState.Key = $Request.Headers.GetValues('Idempotency-Key') | Select-Object -First 1
            return [pscustomobject]@{
                HttpStatus = 200
                ContentBytes = [Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}}')
            }
        }

        Assert-OfflineCondition ($fakeTransport -is [scriptblock])
        # 使用具名参数传递脚本块，避免 Windows PowerShell 5.1 将末尾脚本块按调用语法重新解释。

        $result = Invoke-BootstrapCore -BaseUri $baseUri -ProviderTemplateId '123456' -AdminJwt $sentinelJwt -BootstrapToken $sentinelToken -IdempotencyKey $sentinelKey -Transport $fakeTransport
        Assert-OfflineCondition ($state['RequestCount'] -eq 1)
        Assert-OfflineCondition ($state.Method -ceq 'POST')
        Assert-OfflineCondition ($state.Path -ceq $script:BootstrapPath)
        Assert-OfflineCondition ($state.ContentMediaType -ceq 'application/json')
        Assert-OfflineCondition ($state.ContentCharset -ceq 'utf-8')
        Assert-OfflineCondition ($state.Body -ceq '{"provider_template_id":"123456"}')
        Assert-OfflineCondition ($state.Authorization -ceq ('Bearer ' + $sentinelJwt))
        Assert-OfflineCondition ($state.Token -ceq $sentinelToken)
        Assert-OfflineCondition ($state.Key -ceq $sentinelKey)
        Assert-OfflineCondition ($result.Code -ceq 'bootstrap_configured')

        $bundle = New-SecureHttpClientBundle
        try {
            Assert-OfflineCondition ($bundle.Handler.AllowAutoRedirect -eq $false)
            Assert-OfflineCondition ($bundle.Client.Timeout -eq [TimeSpan]::FromSeconds(15))
        }
        finally {
            $bundle.Client.Dispose()
            $bundle.Handler.Dispose()
            $bundle = $null
        }

        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'http://192.0.2.10:8080'))
        Assert-OfflineCondition ($null -ne (ConvertTo-ValidatedApiBase 'http://127.0.0.1:18080'))
        Assert-OfflineCondition ($null -ne (ConvertTo-ValidatedApiBase 'http://127.255.255.254:18080'))
        Assert-OfflineCondition ($null -ne (ConvertTo-ValidatedApiBase 'http://[::1]:18080'))
        Assert-OfflineCondition ($null -ne (ConvertTo-ValidatedApiBase 'http://localhost:18080'))
        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'http://localhost.example.invalid:18080'))
        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'http://user@localhost:18080'))
        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'http://localhost:18080/path'))
        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'http://localhost:18080?query=1'))
        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'http://localhost:18080#fragment'))
        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'http://[::ffff:127.0.0.1]:18080'))
        Assert-OfflineCondition ($null -ne (ConvertTo-ValidatedApiBase 'https://api.example.invalid'))
        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'https://user@api.example.invalid'))
        Assert-OfflineCondition ($null -eq (ConvertTo-ValidatedApiBase 'https://api.example.invalid/path'))

        $boundary4096 = New-Object byte[] $script:MaximumResponseBytes
        $boundaryStream = [IO.MemoryStream]::new($boundary4096, $false)
        try {
            $boundaryResult = Read-BoundedResponseBytes -Stream $boundaryStream -CancellationToken ([Threading.CancellationToken]::None)
            Assert-OfflineCondition (-not $boundaryResult.Exceeded)
            Assert-OfflineCondition ($boundaryResult.Bytes.Length -eq 4096)
        }
        finally {
            $boundaryStream.Dispose()
        }
        $boundary4097 = New-Object byte[] ($script:MaximumResponseBytes + 1)
        $boundaryStream = [IO.MemoryStream]::new($boundary4097, $false)
        try {
            $boundaryResult = Read-BoundedResponseBytes -Stream $boundaryStream -CancellationToken ([Threading.CancellationToken]::None)
            Assert-OfflineCondition $boundaryResult.Exceeded
            Assert-OfflineCondition ($boundaryStream.Position -eq 4097)
        }
        finally {
            $boundaryStream.Dispose()
        }

        $blockingStream = [MolinEmailBootstrapBlockingStream]::new()
        $testDeadlineSource = [Threading.CancellationTokenSource]::new([TimeSpan]::FromMilliseconds(100))
        $deadlineStopwatch = [Diagnostics.Stopwatch]::StartNew()
        $deadlineCancelled = $false
        try {
            [void](Read-BoundedResponseBytes -Stream $blockingStream -CancellationToken $testDeadlineSource.Token)
        }
        catch [OperationCanceledException] {
            $deadlineCancelled = $true
        }
        finally {
            $deadlineStopwatch.Stop()
            $testDeadlineSource.Dispose()
        }
        Assert-OfflineCondition $deadlineCancelled
        Assert-OfflineCondition ($deadlineStopwatch.ElapsedMilliseconds -lt 1000)
        Assert-OfflineCondition ($blockingStream.AsyncReadCalls -eq 1)
        Assert-OfflineCondition ($blockingStream.SyncReadCalls -eq 0)
        $blockingStream.Dispose()

        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}}'))
        Assert-OfflineCondition $responseAccepted
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes("{`"code`":0,`"message`":`"ok`",`"data`":{`"scene`":`"admin_verify`",`"configured`":true,`"idempotent`":false}}`n"))
        Assert-OfflineCondition $responseAccepted
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes("{`"code`":0,`"message`":`"ok`",`"data`":{`"scene`":`"admin_verify`",`"configured`":true,`"idempotent`":false}}`r`n"))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes(' {"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}}'))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}} '))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"code":0,"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}}'))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}}'))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false},"data":{}}'))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"message":"ok","code":0,"data":{"scene":"admin_verify","configured":true,"idempotent":false}}'))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([byte[]]@(239, 187, 191) + [Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false}}'))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":true}}'))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","data":{"scene":"admin_verify","configured":true,"idempotent":false,"email":"sensitive@example.invalid"}}'))
        Assert-OfflineCondition (-not $responseAccepted)
        $responseAccepted = Test-BootstrapSuccessResponse -ContentBytes ([Text.Encoding]::UTF8.GetBytes('{"code":0,"message":"ok","request_id":"raw","data":{"scene":"admin_verify","configured":true,"idempotent":false}}'))
        Assert-OfflineCondition (-not $responseAccepted)

        $safeOutput = Format-SafeResult $result
        foreach ($secret in @($sentinelJwt, $sentinelToken, $sentinelKey, 'sensitive@example.invalid')) {
            Assert-OfflineCondition (-not $safeOutput.Contains($secret))
        }

        return New-SafeResult 'PASS' 0 'self_test_passed'
    }
    finally {
        $sentinelJwt = $null
        $sentinelToken = $null
        $sentinelKey = $null
        $generatedKey = $null
        $safeOutput = $null
        $state.Clear()
        $state = $null
        # 精确清理本次离线自测创建的唯一全局变量，不影响真实运行路径或其他全局状态。

        Remove-Variable -Scope Global -Name $stateVariableName -ErrorAction SilentlyContinue
        $stateVariableName = $null
    }
}

if ($PSCmdlet.ParameterSetName -eq 'SelfTest') {
    try {
        [Console]::Out.WriteLine((Format-SafeResult (Invoke-OfflineSelfTest)))
        exit 0
    }
    catch {
        [Console]::Out.WriteLine((Format-SafeResult (New-SafeResult 'FAIL' 0 'self_test_failed')))
        exit 1
    }
}

$secureJwt = $null
$secureBootstrapToken = $null
$adminJwt = $null
$bootstrapToken = $null
$idempotencyKey = $null
$confirmation = $null
try {
    if (-not (Test-TemplateId $TemplateId)) {
        [Console]::Out.WriteLine((Format-SafeResult (New-SafeResult 'BLOCKED' 0 'invalid_template_id')))
        exit 2
    }
    $baseUri = ConvertTo-ValidatedApiBase $ApiBase
    if ($null -eq $baseUri) {
        [Console]::Out.WriteLine((Format-SafeResult (New-SafeResult 'BLOCKED' 0 'invalid_api_base')))
        exit 2
    }

    $confirmation = Read-Host "本次操作最多发出一次 HTTP 请求且不会自动重试。请输入固定确认短语 $script:ConfirmationPhrase"
    if ($confirmation -cne $script:ConfirmationPhrase) {
        [Console]::Out.WriteLine((Format-SafeResult (New-SafeResult 'BLOCKED' 0 'confirmation_rejected')))
        exit 2
    }

    $secureJwt = Read-Host '请输入临时管理员 JWT（安全输入）' -AsSecureString
    $secureBootstrapToken = Read-Host '请输入一次性邮件 bootstrap token（安全输入）' -AsSecureString
    $adminJwt = ConvertFrom-SecureInput $secureJwt
    $bootstrapToken = ConvertFrom-SecureInput $secureBootstrapToken
    if (-not (Test-SingleLineSecret $adminJwt) -or -not (Test-SingleLineSecret $bootstrapToken)) {
        [Console]::Out.WriteLine((Format-SafeResult (New-SafeResult 'BLOCKED' 0 'secret_input_invalid')))
        exit 2
    }

    $idempotencyKey = New-IdempotencyKey
    if ($idempotencyKey -cnotmatch '^[A-Za-z0-9_-]{16,128}$') {
        [Console]::Out.WriteLine((Format-SafeResult (New-SafeResult 'BLOCKED' 0 'request_failed')))
        exit 2
    }

    $transport = { param($Request) Invoke-RealTransport $Request }
    try {
        $finalResult = Invoke-BootstrapCore -BaseUri $baseUri -ProviderTemplateId $TemplateId -AdminJwt $adminJwt -BootstrapToken $bootstrapToken -IdempotencyKey $idempotencyKey -Transport $transport
    }
    catch {
        $finalResult = New-SafeResult 'BLOCKED' 0 'request_failed'
    }
    [Console]::Out.WriteLine((Format-SafeResult $finalResult))
    if ($finalResult.Code -ceq 'bootstrap_configured') { exit 0 }
    exit 2
}
catch {
    [Console]::Out.WriteLine((Format-SafeResult (New-SafeResult 'BLOCKED' 0 'request_failed')))
    exit 2
}
finally {
    # 释放安全字符串并清除所有受管引用，不修改进程或系统环境变量。

    if ($null -ne $secureJwt) { $secureJwt.Dispose() }
    if ($null -ne $secureBootstrapToken) { $secureBootstrapToken.Dispose() }
    $adminJwt = $null
    $bootstrapToken = $null
    $idempotencyKey = $null
    $confirmation = $null
    $secureJwt = $null
    $secureBootstrapToken = $null
    $transport = $null
    $finalResult = $null
    $baseUri = $null
}
