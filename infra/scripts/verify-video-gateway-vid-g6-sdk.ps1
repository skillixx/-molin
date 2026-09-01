param()

$ErrorActionPreference = 'Stop'
if ($env:VIDEO_GATEWAY_G6_SDK_APPROVED -ne 'YES') {
    Write-Output 'VIDEO_G6_SDK=APPROVAL_REQUIRED'
    exit 3
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$sdkDir = Join-Path $repoRoot 'tests\api\video-gateway-vid-g6-sdk'
$python = Join-Path $sdkDir '.venv\Scripts\python.exe'
$mysqlImage = 'sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b'
if (-not (Test-Path -LiteralPath $python) -or -not (Test-Path -LiteralPath (Join-Path $sdkDir 'node_modules\openai\package.json'))) {
    Write-Output 'VIDEO_G6_SDK=FAILED reason=locked_dependencies_missing'
    exit 2
}
docker image inspect $mysqlImage | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Output 'VIDEO_G6_SDK=FAILED reason=mysql_image_missing'
    exit 2
}

$containerName = 'molin-vid-g6-sdk-' + [Guid]::NewGuid().ToString('N').Substring(0, 12)
$passwordBytes = New-Object byte[] 24
[Security.Cryptography.RandomNumberGenerator]::Fill($passwordBytes)
$databasePassword = [Convert]::ToHexString($passwordBytes).ToLower()

try {
    $containerID = docker run -d --pull=never --name $containerName --label molin.goal=VID-G6 `
        --tmpfs /var/lib/mysql:rw,noexec,nosuid,size=1g -p 127.0.0.1::3306 `
        -e "MYSQL_ROOT_PASSWORD=$databasePassword" -e MYSQL_DATABASE=molin_video_g6_contract `
        $mysqlImage --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($containerID)) {
        throw 'temporary_mysql_start_failed'
    }

    $ready = 0
    for ($attempt = 0; $attempt -lt 90; $attempt++) {
        docker exec -e "MYSQL_PWD=$databasePassword" $containerName mysql --no-defaults --protocol=socket `
            -uroot --database=molin_video_g6_contract -Nse 'SELECT 1' 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) { $ready++ } else { $ready = 0 }
        if ($ready -ge 2) { break }
        Start-Sleep -Seconds 1
    }
    if ($ready -lt 2) { throw 'temporary_mysql_not_ready' }

    $portLine = docker port $containerName 3306/tcp
    if ($portLine -notmatch '127\.0\.0\.1:(\d+)$') { throw 'mysql_not_loopback' }
    $databasePort = $Matches[1]
    $migrations = Get-ChildItem -LiteralPath (Join-Path $repoRoot 'server\migrations') -Filter '*.up.sql' | Sort-Object Name
    foreach ($migration in $migrations) {
        Get-Content -LiteralPath $migration.FullName -Raw | docker exec -i -e "MYSQL_PWD=$databasePassword" `
            $containerName mysql --no-defaults --protocol=socket -uroot --database=molin_video_g6_contract `
            --default-character-set=utf8mb4 | Out-Null
        if ($LASTEXITCODE -ne 0) { throw ('migration_failed_' + $migration.Name) }
    }

    $env:MOLIN_VIDEO_G6_ISOLATED = 'YES'
    $env:MOLIN_VIDEO_G6_SDK_EXECUTE = 'YES'
    $env:MOLIN_VIDEO_G6_MYSQL_DSN = "root:$databasePassword@tcp(127.0.0.1:$databasePort)/molin_video_g6_contract?charset=utf8mb4&parseTime=true&loc=UTC"
    Push-Location (Join-Path $repoRoot 'server')
    try {
        go test ./internal/modules/token_gateway/service -run '^TestVideoG6LockedSDKHTTPMySQL$' -count=1 -v -timeout 10m
        if ($LASTEXITCODE -ne 0) { throw 'locked_sdk_http_failed' }
    } finally {
        Pop-Location
    }
    Write-Output 'VIDEO_G6_SDK=PASS python=openai==2.45.0 typescript=openai@6.39.0 real_provider_calls=0 real_wallet_writes=0 test_server_writes=0 production_operations=0'
} catch {
    Write-Output ('VIDEO_G6_SDK=FAILED reason=' + $_.Exception.Message)
    exit 2
} finally {
    Remove-Item Env:MOLIN_VIDEO_G6_MYSQL_DSN -ErrorAction SilentlyContinue
    Remove-Item Env:MOLIN_VIDEO_G6_ISOLATED -ErrorAction SilentlyContinue
    Remove-Item Env:MOLIN_VIDEO_G6_SDK_EXECUTE -ErrorAction SilentlyContinue
    if (docker ps -a --format '{{.Names}}' | Select-String -SimpleMatch $containerName -Quiet) {
        docker rm -f $containerName | Out-Null
    }
}
