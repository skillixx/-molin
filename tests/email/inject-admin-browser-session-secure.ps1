param(
    [switch]$SelfTest,
    [switch]$FakeChild,
    [ValidateSet('probe', 'inject')]
    [string]$FakeMode
)

$ErrorActionPreference = 'Stop'

$script:BrowseExe = 'C:\Users\skillixx\.codex\skills\gstack\browse\dist\browse.exe'
$script:BrowseServerScript = 'C:\Users\skillixx\.codex\skills\gstack\browse\dist\server-node.mjs'
$script:ExpectedOrigin = 'http://8.130.9.163:3001'
$script:JwtPattern = '^[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}$'
$script:UseFakeTransport = $false
$script:ChildWorkingDirectory = (Get-Location).Path
$script:SelfTestStage = 'not_started'

function Clear-CharArray {
    param([char[]]$Value)
    if ($null -eq $Value) {
        return
    }
    for ($index = 0; $index -lt $Value.Length; $index++) {
        $Value[$index] = [char]0
    }
}

function Write-FixedFailure {
    param(
        [string]$Category,
        [bool]$TokenExposed = $false
    )
    Write-Output 'injected=false'
    Write-Output ("token_exposed=" + $TokenExposed.ToString().ToLowerInvariant())
    Write-Output ("failure=" + $Category)
}

function Test-JwtShape {
    param([string]$Token)
    return -not [string]::IsNullOrWhiteSpace($Token) -and $Token -cmatch $script:JwtPattern
}

function Get-ProcessSpec {
    param([ValidateSet('probe', 'inject')][string]$Mode)

    if (-not $script:UseFakeTransport) {
        return [pscustomobject]@{
            FileName = $script:BrowseExe
            Arguments = 'chain'
        }
    }

    $powershellExe = Join-Path $PSHOME 'powershell.exe'
    $escapedScript = $PSCommandPath.Replace('"', '""')
    return [pscustomobject]@{
        FileName = $powershellExe
        Arguments = '-NoProfile -ExecutionPolicy Bypass -File "' + $escapedScript + '" -FakeChild -FakeMode ' + $Mode
    }
}

function Invoke-ChainProcess {
    param(
        [ValidateSet('probe', 'inject')][string]$Mode,
        [string]$ChainJson,
        [string]$SensitiveValue
    )

    $spec = Get-ProcessSpec -Mode $Mode
    if (-not [string]::IsNullOrEmpty($SensitiveValue) -and
        ($spec.FileName.Contains($SensitiveValue) -or $spec.Arguments.Contains($SensitiveValue))) {
        throw 'TOKEN_IN_ARGV'
    }

    $startInfo = New-Object System.Diagnostics.ProcessStartInfo
    $startInfo.FileName = $spec.FileName
    $startInfo.Arguments = $spec.Arguments
    $startInfo.WorkingDirectory = $script:ChildWorkingDirectory
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    # 显式使用无 BOM UTF-8，避免 PowerShell 5.1 的默认编码破坏 stdin JSON。
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    $startInfo.StandardOutputEncoding = $utf8NoBom
    $startInfo.StandardErrorEncoding = $utf8NoBom
    # 固定服务脚本路径，拒绝继承调用方可能注入的其他路径。
    $startInfo.EnvironmentVariables['BROWSE_SERVER_SCRIPT'] = $script:BrowseServerScript

    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $startInfo
    $stdout = $null
    $stderr = $null
    $stdinBytes = $null
    try {
        if (-not $process.Start()) {
            throw 'PROCESS_START_FAILED'
        }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        # 机密只经标准输入传递，绝不进入 Arguments、环境变量或临时文件。
        $stdinBytes = $utf8NoBom.GetBytes($ChainJson)
        $process.StandardInput.BaseStream.Write($stdinBytes, 0, $stdinBytes.Length)
        $process.StandardInput.BaseStream.Flush()
        $process.StandardInput.BaseStream.Close()
        if (-not $process.WaitForExit(30000)) {
            try { $process.Kill() } catch { }
            try { $process.WaitForExit() } catch { }
            throw 'PROCESS_TIMEOUT'
        }
        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        if (-not [string]::IsNullOrEmpty($SensitiveValue) -and
            (($null -ne $stdout -and $stdout.Contains($SensitiveValue)) -or
             ($null -ne $stderr -and $stderr.Contains($SensitiveValue)))) {
            throw 'TOKEN_IN_OUTPUT'
        }
        return [pscustomobject]@{
            ExitCode = $process.ExitCode
            Stdout = $stdout
            Stderr = $stderr
            ArgumentsSafe = [string]::IsNullOrEmpty($SensitiveValue) -or -not $spec.Arguments.Contains($SensitiveValue)
        }
    } finally {
        $stdout = $null
        $stderr = $null
        if ($null -ne $stdinBytes) {
            [Array]::Clear($stdinBytes, 0, $stdinBytes.Length)
            $stdinBytes = $null
        }
        if ($null -ne $process) {
            $process.Dispose()
        }
    }
}

function Assert-ExpectedOrigin {
    param([string]$CapturedOutput)

    if ([string]::IsNullOrWhiteSpace($CapturedOutput)) {
        throw 'ORIGIN_MISSING'
    }
    $matches = [regex]::Matches($CapturedOutput, 'https?://[A-Za-z0-9.-]+(?::\d+)?(?:/[^\s"\\]*)?')
    $accepted = $false
    foreach ($match in $matches) {
        $uri = $null
        if ([Uri]::TryCreate($match.Value, [UriKind]::Absolute, [ref]$uri) -and
            $uri.GetLeftPart([UriPartial]::Authority) -ceq $script:ExpectedOrigin) {
            $accepted = $true
            break
        }
    }
    if (-not $accepted) {
        throw 'ORIGIN_REJECTED'
    }
}

function Invoke-OriginProbe {
    $probeJson = '[["url"]]'
    $result = Invoke-ChainProcess -Mode probe -ChainJson $probeJson -SensitiveValue $null
    if ($script:UseFakeTransport) { $script:SelfTestStage = 'origin_probe_process_done' }
    if ($result.ExitCode -ne 0) {
        if ($script:UseFakeTransport -and $result.ExitCode -in @(41, 42, 43, 44, 46)) {
            $script:SelfTestStage = 'origin_probe_fake_exit_' + $result.ExitCode
        }
        throw 'BROWSE_PROBE_FAILED'
    }
    if ($script:UseFakeTransport) { $script:SelfTestStage = 'origin_probe_origin_check' }
    Assert-ExpectedOrigin -CapturedOutput ($result.Stdout + "`n" + $result.Stderr)
}

function Invoke-TokenInjection {
    param([Security.SecureString]$SecureToken)

    $bstr = [IntPtr]::Zero
    $token = $null
    $tokenChars = $null
    $chainJson = $null
    try {
        $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($SecureToken)
        $token = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
        if (-not (Test-JwtShape -Token $token)) {
            throw 'JWT_INVALID'
        }
        $tokenChars = $token.ToCharArray()
        $chainJson = '[["storage","set","access_token","' + $token + '"],["reload"],["url"]]'
        $result = Invoke-ChainProcess -Mode inject -ChainJson $chainJson -SensitiveValue $token
        if ($result.ExitCode -ne 0 -or -not $result.ArgumentsSafe) {
            throw 'BROWSE_INJECT_FAILED'
        }
        Assert-ExpectedOrigin -CapturedOutput ($result.Stdout + "`n" + $result.Stderr)
    } finally {
        Clear-CharArray -Value $tokenChars
        $chainJson = $null
        $token = $null
        if ($bstr -ne [IntPtr]::Zero) {
            [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
        }
    }
}

function Invoke-FakeChild {
    param([ValidateSet('probe', 'inject')][string]$Mode)

    $stdinReader = New-Object System.IO.StreamReader([Console]::OpenStandardInput(), (New-Object System.Text.UTF8Encoding($false)), $true)
    try {
        $inputJson = $stdinReader.ReadToEnd().TrimStart([char]0xFEFF)
    } finally {
        $stdinReader.Dispose()
    }
    $commandLine = [Environment]::CommandLine
    if ($commandLine -match 'eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}') {
        exit 41
    }
    if ($env:BROWSE_SERVER_SCRIPT -cne $script:BrowseServerScript) {
        exit 42
    }
    if ([string]::IsNullOrEmpty($inputJson)) {
        exit 46
    }
    if ($Mode -eq 'probe') {
        if ($inputJson -cne '[["url"]]') {
            exit 44
        }
        [Console]::Out.WriteLine('{"ok":true,"url":"http://8.130.9.163:3001/admin"}')
        exit 0
    }
    $injectMatch = [regex]::Match($inputJson, '^\[\["storage","set","access_token","(?<token>[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)"\],\["reload"\],\["url"\]\]$')
    if (-not $injectMatch.Success -or -not (Test-JwtShape -Token $injectMatch.Groups['token'].Value)) {
        exit 45
    }
    [Console]::Out.WriteLine('{"ok":true}')
    [Console]::Out.WriteLine('{"ok":true,"url":"http://8.130.9.163:3001/admin"}')
    exit 0
}

function Invoke-SelfTest {
    $script:UseFakeTransport = $true
    $testRoot = Join-Path ([IO.Path]::GetTempPath()) ('molin-browser-session-selftest-' + [Guid]::NewGuid().ToString('N'))
    $secure = $null
    $token = $null
    $completed = $false
    try {
        $script:SelfTestStage = 'prepare'
        [IO.Directory]::CreateDirectory($testRoot) | Out-Null
        $script:ChildWorkingDirectory = $testRoot
        # 分段构造合成 JWT，避免测试源码本身包含可被误认为真实令牌的完整值。
        $token = ('eyJ' + ('a' * 20)) + '.' + ('b' * 24) + '.' + ('c' * 24)
        $secure = ConvertTo-SecureString -String $token -AsPlainText -Force
        $script:SelfTestStage = 'origin_probe'
        Invoke-OriginProbe
        $script:SelfTestStage = 'token_injection'
        Invoke-TokenInjection -SecureToken $secure
        $script:SelfTestStage = 'file_check'
        $files = @(Get-ChildItem -LiteralPath $testRoot -Force -File -Recurse)
        if ($files.Count -ne 0) {
            throw 'SELFTEST_FILE_EXPOSURE'
        }
        $completed = $true
        Write-Output 'selftest=true'
        Write-Output 'argv_exposed=false'
        Write-Output 'output_exposed=false'
        Write-Output 'file_exposed=false'
        Write-Output 'network=false'
    } finally {
        if ($completed) {
            $script:SelfTestStage = 'cleanup'
        }
        $token = $null
        if ($null -ne $secure) {
            $secure.Dispose()
        }
        if (Test-Path -LiteralPath $testRoot -PathType Container) {
            # 仅删除本轮创建且已确认为空的随机临时目录。
            if (@(Get-ChildItem -LiteralPath $testRoot -Force).Count -eq 0) {
                [IO.Directory]::Delete($testRoot, $false)
            }
        }
    }
}

if ($FakeChild) {
    Invoke-FakeChild -Mode $FakeMode
    exit $LASTEXITCODE
}

if ($SelfTest) {
    try {
        Invoke-SelfTest
        exit 0
    } catch {
        Write-Output 'selftest=false'
        Write-Output ("failure_stage=" + $script:SelfTestStage)
        Write-Output 'argv_exposed=unknown'
        Write-Output 'output_exposed=unknown'
        Write-Output 'file_exposed=unknown'
        Write-Output 'network=false'
        exit 1
    }
}

$secureToken = $null
try {
    if (-not (Test-Path -LiteralPath $script:BrowseExe -PathType Leaf) -or
        -not (Test-Path -LiteralPath $script:BrowseServerScript -PathType Leaf)) {
        throw 'BROWSE_UNAVAILABLE'
    }
    # 先无机密确认当前 tab 的 origin，错误域名时绝不读取或注入 Token。
    Invoke-OriginProbe
    $secureToken = Read-Host '请输入已完成管理员双 MFA 的短期 Access Token' -AsSecureString
    Invoke-TokenInjection -SecureToken $secureToken
    Write-Output 'injected=true'
    Write-Output 'token_exposed=false'
    exit 0
} catch {
    $category = switch ($_.Exception.Message) {
        'ORIGIN_MISSING' { 'origin_rejected' }
        'ORIGIN_REJECTED' { 'origin_rejected' }
        'JWT_INVALID' { 'jwt_invalid' }
        'BROWSE_UNAVAILABLE' { 'browse_unavailable' }
        'TOKEN_IN_ARGV' { 'token_exposed' }
        'TOKEN_IN_OUTPUT' { 'token_exposed' }
        default { 'browse_failed' }
    }
    Write-FixedFailure -Category $category -TokenExposed ($category -eq 'token_exposed')
    exit 1
} finally {
    if ($null -ne $secureToken) {
        $secureToken.Dispose()
    }
}
