[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$ApiExecutable,

    [ValidateRange(5, 120)]
    [int]$StartupTimeoutSeconds = 30
)

$ErrorActionPreference = 'Stop'
$BootstrapPath = '/api/internal/email/bootstrap/admin-verify'
$BootstrapKeys = @(
    'EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED',
    'EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN',
    'EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS',
    'EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS'
)
$ProtectedPid = 120124

function Get-FreeLoopbackPort {
    # 由操作系统分配回环空闲端口，避免触碰当前运行中的 API。
    $Listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    try {
        $Listener.Start()
        return ([System.Net.IPEndPoint]$Listener.LocalEndpoint).Port
    }
    finally {
        $Listener.Stop()
    }
}

function Start-IsolatedApi {
    param(
        [Parameter(Mandatory = $true)]
        [hashtable]$BootstrapEnvironment,
        [Parameter(Mandatory = $true)]
        [int]$Port,
        [Parameter(Mandatory = $true)]
        [string]$PhaseName
    )

    # 使用 ProcessStartInfo 直接传递子进程环境，不经过 shell，也不把配置值拼入命令行。
    $StartInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $StartInfo.FileName = (Resolve-Path -LiteralPath $ApiExecutable).Path
    $StartInfo.UseShellExecute = $false
    $StartInfo.CreateNoWindow = $true
    $StartInfo.RedirectStandardOutput = $true
    $StartInfo.RedirectStandardError = $true
    foreach ($Key in $BootstrapKeys) {
        [void]$StartInfo.Environment.Remove($Key)
    }
    foreach ($Entry in $BootstrapEnvironment.GetEnumerator()) {
        $StartInfo.Environment[$Entry.Key] = [string]$Entry.Value
    }
    $StartInfo.Environment['API_HOST'] = '127.0.0.1'
    $StartInfo.Environment['API_PORT'] = [string]$Port

    $Process = [System.Diagnostics.Process]::new()
    $Process.StartInfo = $StartInfo
    if (-not $Process.Start()) {
        throw "$PhaseName：测试 API 子进程启动失败。"
    }
    if ($Process.Id -eq $ProtectedPid) {
        throw "$PhaseName：检测到受保护 PID，已拒绝继续操作。"
    }

    # 异步读取并丢弃输出，避免缓冲区阻塞；验证报告不会暴露日志中的敏感内容。
    $Process.BeginOutputReadLine()
    $Process.BeginErrorReadLine()
    return $Process
}

function Stop-IsolatedApi {
    param([System.Diagnostics.Process]$Process)

    if ($null -eq $Process) {
        return
    }
    if (-not $Process.HasExited) {
        if ($Process.Id -eq $ProtectedPid) {
            throw '拒绝终止受保护 PID 120124。'
        }
        $Process.Kill()
        $Process.WaitForExit(5000) | Out-Null
    }
    $Process.Dispose()
}

function Wait-ForHealth {
    param(
        [System.Diagnostics.Process]$Process,
        [int]$Port,
        [string]$PhaseName
    )

    $Deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $Client = [System.Net.Http.HttpClient]::new()
    try {
        while ([DateTime]::UtcNow -lt $Deadline) {
            if ($Process.HasExited) {
                throw "$PhaseName：测试 API 在健康检查前退出；为避免泄密，脚本不输出子进程日志。"
            }
            try {
                $Response = $Client.GetAsync("http://127.0.0.1:$Port/api/health").GetAwaiter().GetResult()
                if ([int]$Response.StatusCode -eq 200) {
                    $Response.Dispose()
                    return
                }
                $Response.Dispose()
            }
            catch [System.Net.Http.HttpRequestException] {
                # 服务仍在启动时连接失败属于预期，继续在时限内轮询。
            }
            Start-Sleep -Milliseconds 200
        }
        throw "$PhaseName：健康检查在 $StartupTimeoutSeconds 秒内未返回 200。"
    }
    finally {
        $Client.Dispose()
    }
}

function Assert-BootstrapNotFound {
    param(
        [int]$Port,
        [string]$PhaseName
    )

    $Client = [System.Net.Http.HttpClient]::new()
    try {
        foreach ($MethodName in @('GET', 'POST')) {
            $Response = $null
            $Request = [System.Net.Http.HttpRequestMessage]::new(
                [System.Net.Http.HttpMethod]::new($MethodName),
                "http://127.0.0.1:$Port$BootstrapPath"
            )
            if ($MethodName -eq 'POST') {
                $Request.Content = [System.Net.Http.StringContent]::new('{}', [Text.Encoding]::UTF8, 'application/json')
            }
            try {
                $Response = $Client.SendAsync($Request).GetAwaiter().GetResult()
                if ([int]$Response.StatusCode -ne 404) {
                    throw "$PhaseName：$MethodName 请求应返回 404，实际为 $([int]$Response.StatusCode)。"
                }
            }
            finally {
                if ($null -ne $Response) { $Response.Dispose() }
                $Request.Dispose()
            }
        }
    }
    finally {
        $Client.Dispose()
    }
}

function Assert-MissingConfigurationFailsStartup {
    $Port = Get-FreeLoopbackPort
    $Process = $null
    try {
        $Process = Start-IsolatedApi -BootstrapEnvironment @{
            EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED = 'true'
        } -Port $Port -PhaseName '启用但配置缺失'

        if (-not $Process.WaitForExit($StartupTimeoutSeconds * 1000)) {
            throw '启用但配置缺失：API 未按失败关闭规则退出。'
        }
        if ($Process.ExitCode -eq 0) {
            throw '启用但配置缺失：API 以成功状态退出，未满足启动失败契约。'
        }
    }
    finally {
        Stop-IsolatedApi -Process $Process
    }
}

function Assert-DisabledState {
    param(
        [hashtable]$BootstrapEnvironment,
        [string]$PhaseName
    )

    $Port = Get-FreeLoopbackPort
    $Process = $null
    try {
        $Process = Start-IsolatedApi -BootstrapEnvironment $BootstrapEnvironment -Port $Port -PhaseName $PhaseName
        Wait-ForHealth -Process $Process -Port $Port -PhaseName $PhaseName
        Assert-BootstrapNotFound -Port $Port -PhaseName $PhaseName
    }
    finally {
        Stop-IsolatedApi -Process $Process
    }
}

if (-not (Test-Path -LiteralPath $ApiExecutable -PathType Leaf)) {
    throw '测试 API 二进制不存在。'
}
Assert-DisabledState -BootstrapEnvironment @{} -PhaseName '四键缺失默认关闭'
Write-Host '通过：四键缺失时入口对 GET/POST 均返回 404。'

Assert-MissingConfigurationFailsStartup
Write-Host '通过：启用但 Token/CIDR 配置缺失时应用启动失败。'

Assert-DisabledState -BootstrapEnvironment @{
    EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED = 'false'
} -PhaseName '显式关闭'
Write-Host '通过：显式关闭后入口对 GET/POST 均返回 404。'

Write-Host 'admin_verify bootstrap 本地隔离验证通过；未执行 migration、外部发送或远程访问。'
