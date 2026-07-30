[CmdletBinding()]
param(
    # 该参数仅接受本地 runner 路径，不允许承载 JWT、token 或幂等键。

    [string]$RunnerPath
)

# 本测试只解析脚本并运行其离线参数集，不向任何地址发起网络请求。

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($RunnerPath)) {
    # PSScriptRoot 在 Windows PowerShell 5.1 的 -File 模式下稳定指向当前测试脚本目录。

    if ([string]::IsNullOrWhiteSpace($PSScriptRoot)) { throw 'offline_self_test_path_missing' }
    $repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
    $RunnerPath = Join-Path $repositoryRoot 'scripts\run-email-admin-verify-bootstrap-secure.ps1'
}
$runnerPath = [IO.Path]::GetFullPath($RunnerPath)
$sentinels = @('jwt_selftest_secret_7fba', 'bootstrap_selftest_secret_2cd1', 'idempotency_selftest_secret_91ab', 'sensitive@example.invalid')

function Assert-Condition {
    param([bool]$Condition)
    if (-not $Condition) { throw 'offline_self_test_failed' }
}

try {
    Assert-Condition (Test-Path -LiteralPath $runnerPath -PathType Leaf)
    $tokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($runnerPath, [ref]$tokens, [ref]$parseErrors)
    Assert-Condition ($parseErrors.Count -eq 0)

    # AST 限定公开参数，确保 JWT、bootstrap token 与幂等键不能通过命令行传入。

    $parameterNames = @($ast.ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath } | Sort-Object)
    Assert-Condition (($parameterNames -join ',') -ceq 'ApiBase,SelfTest,TemplateId')
    $source = [IO.File]::ReadAllText($runnerPath)
    Assert-Condition (([regex]::Matches($source, '\.SendAsync\s*\(')).Count -eq 1)
    Assert-Condition (-not $source.Contains(('$' + 'env:')))
    Assert-Condition ($source -notmatch '(?i)Invoke-WebRequest|Invoke-RestMethod')
    Assert-Condition ($source -match '\.AllowAutoRedirect\s*=\s*\$false')
    Assert-Condition ($source -match 'TimeoutSeconds\s*=\s*15')
    Assert-Condition ($source -match 'ResponseHeadersRead')
    Assert-Condition ($source -notmatch 'ResponseContentRead|LoadIntoBufferAsync|\$response\.Content\.ReadAsStringAsync|ConvertFrom-Json')
    Assert-Condition ($source -match 'MaximumResponseBytes\s*\+\s*1')
    Assert-Condition ($source -notmatch '\$Stream\.Read\s*\(')

    $realTransportFunctions = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
            $node.Name -ceq 'Invoke-RealTransport'
    }, $true))
    Assert-Condition ($realTransportFunctions.Count -eq 1)
    $realTransportSource = $realTransportFunctions[0].Extent.Text
    Assert-Condition (([regex]::Matches($realTransportSource, 'CancellationTokenSource\]::new\s*\(')).Count -eq 1)
    Assert-Condition ($realTransportSource -match 'SendAsync\([^\r\n]+ResponseHeadersRead[^\r\n]+\$deadlineSource\.Token')
    Assert-Condition ($realTransportSource -match 'Task\]::WhenAny')
    Assert-Condition ($realTransportSource -match 'Read-BoundedResponseBytes[^\r\n]+-CancellationToken\s+\$deadlineSource\.Token')

    $securePrompts = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.CommandAst] -and
            $node.GetCommandName() -ceq 'Read-Host' -and
            ($node.CommandElements.Extent.Text -contains '-AsSecureString')
    }, $true))
    Assert-Condition ($securePrompts.Count -eq 2)

    $shellPath = Join-Path $PSHOME 'powershell.exe'
    $overlongTemplateId = '11111111111111111111111111111111111111111111111111111111111111111'
    $invalidTemplateOutput = @(& $shellPath -NoProfile -ExecutionPolicy Bypass -File $runnerPath -ApiBase 'https://localhost:8443' -TemplateId $overlongTemplateId 2>&1)
    Assert-Condition ($LASTEXITCODE -eq 2)
    Assert-Condition ($invalidTemplateOutput.Count -eq 1)
    Assert-Condition ($invalidTemplateOutput[0].ToString() -ceq '{"status":"BLOCKED","http_status":0,"code":"invalid_template_id"}')

    $invalidBaseOutput = @(& $shellPath -NoProfile -ExecutionPolicy Bypass -File $runnerPath -ApiBase 'ftp://localhost' -TemplateId '1' 2>&1)
    Assert-Condition ($LASTEXITCODE -eq 2)
    Assert-Condition ($invalidBaseOutput.Count -eq 1)
    Assert-Condition ($invalidBaseOutput[0].ToString() -ceq '{"status":"BLOCKED","http_status":0,"code":"invalid_api_base"}')

    $remoteHttpOutput = @(& $shellPath -NoProfile -ExecutionPolicy Bypass -File $runnerPath -ApiBase 'http://192.0.2.10:8080' -TemplateId '1' 2>&1)
    Assert-Condition ($LASTEXITCODE -eq 2)
    Assert-Condition ($remoteHttpOutput.Count -eq 1)
    Assert-Condition ($remoteHttpOutput[0].ToString() -ceq '{"status":"BLOCKED","http_status":0,"code":"invalid_api_base"}')

    $output = @(& $shellPath -NoProfile -ExecutionPolicy Bypass -File $runnerPath -SelfTest 2>&1)
    $exitCode = $LASTEXITCODE
    Assert-Condition ($exitCode -eq 0)
    Assert-Condition ($output.Count -eq 1)
    Assert-Condition ($output[0].ToString() -ceq '{"status":"PASS","http_status":0,"code":"self_test_passed"}')
    foreach ($sentinel in $sentinels) {
        Assert-Condition (-not $output[0].ToString().Contains($sentinel))
    }

    [Console]::Out.WriteLine('{"status":"PASS","http_status":0,"code":"self_test_passed"}')
    exit 0
}
catch {
    [Console]::Out.WriteLine('{"status":"FAIL","http_status":0,"code":"self_test_failed"}')
    exit 1
}
finally {
    $output = $null
    $source = $null
    $tokens = $null
    $parseErrors = $null
    $ast = $null
    $sentinels = $null
    $remoteHttpOutput = $null
    $realTransportFunctions = $null
    $realTransportSource = $null
}
