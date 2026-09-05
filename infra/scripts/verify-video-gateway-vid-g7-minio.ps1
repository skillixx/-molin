param([switch]$LinuxRace)

$ErrorActionPreference = 'Stop'
if ($env:VIDEO_GATEWAY_G7_MINIO_ISOLATED_APPROVED -ne 'YES') {
    Write-Output 'VIDEO_G7_MINIO=APPROVAL_REQUIRED target=temporary_minio test_server=false'
    exit 3
}

$minioImage = 'minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e'
$goImage = 'golang@sha256:908f8ff2ec296df2f349563072c7925775cd28b50361a52ed834a8a37399b9bf'
$suffix = [Guid]::NewGuid().ToString('N').Substring(0, 12)
$network = "molin-vidg7-minio-net-$suffix"
$container = "molin-vidg7-minio-$suffix"
$builder = "molin-vidg7-minio-go-$suffix"
$access = 'vidg7fakeaccess'
$secret = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$previous = @{}
foreach ($name in @('MOLIN_VIDEO_G7_MINIO_ISOLATED','MOLIN_VIDEO_G7_MINIO_ENDPOINT','MOLIN_VIDEO_G7_MINIO_ACCESS','MOLIN_VIDEO_G7_MINIO_SECRET')) {
    $previous[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
$createdNetwork = $false
$createdContainer = $false
$resultCode = 2

function Get-VideoG7MinIOSourceHash {
    # MinIO同时实现video物理对象与service上传封存；冻结整个server避免漏算跨包安全改动。
    $paths = @(git -C $repoRoot ls-files --cached --others --exclude-standard -- server infra/scripts/verify-video-gateway-vid-g7-minio.ps1 | Sort-Object -Unique)
    if ($LASTEXITCODE -ne 0 -or $paths.Count -eq 0) { throw 'source_list_failed' }
    $records = foreach ($path in $paths) { $path + '|' + (Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant() }
    return [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes(($records -join "`n") + "`n"))).ToLowerInvariant()
}

try {
    docker image inspect $minioImage --format '{{.Id}}' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'locked_minio_image_missing' }
    if ($LinuxRace) {
        docker image inspect $goImage --format '{{.Id}}' | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'locked_go_image_missing' }
    }
    $sourceHash = Get-VideoG7MinIOSourceHash
    # Windows Docker的internal网络不会发布宿主回环端口；使用本次专属网络并只绑定127.0.0.1。
    docker network create --label molin.goal=VID-G7 $network | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'isolated_network_failed' }
    $createdNetwork = $true
    docker run -d --pull=never --name $container --label molin.goal=VID-G7 --network $network --network-alias minio -p '127.0.0.1::9000' --tmpfs '/data:rw,noexec,nosuid,size=1g' -e "MINIO_ROOT_USER=$access" -e "MINIO_ROOT_PASSWORD=$secret" $minioImage server /data --console-address ':9001' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'isolated_minio_failed' }
    $createdContainer = $true
    $portOutput = @(docker port $container '9000/tcp' 2>&1)
    $portLine = if ($portOutput.Count -gt 0) { $portOutput[0].ToString().Trim() } else { '' }
    if ($LASTEXITCODE -ne 0 -or $portLine -notmatch '127\.0\.0\.1:(\d+)$') { throw 'isolated_port_failed' }
    $hostEndpoint = "127.0.0.1:$($Matches[1])"
    $ready = $false
    for ($attempt = 0; $attempt -lt 120; $attempt++) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri "http://$hostEndpoint/minio/health/ready" -TimeoutSec 2
            if ($response.StatusCode -eq 200) { $ready = $true; break }
        } catch {}
        Start-Sleep -Milliseconds 500
    }
    if (-not $ready) { throw 'isolated_minio_not_ready' }
    Write-Output 'VIDEO_G7_MINIO=READY scope=temporary_minio host_ports=loopback_only'

    $env:MOLIN_VIDEO_G7_MINIO_ISOLATED = 'YES'
    $env:MOLIN_VIDEO_G7_MINIO_ACCESS = $access
    $env:MOLIN_VIDEO_G7_MINIO_SECRET = $secret
    $requiredTests = @('TestVideoG7MinIOObjectStoreIntegration','TestVideoG7MinIOUploadSealIntegration')
    $testPattern = '^(' + ($requiredTests -join '|') + ')$'
    if ($LinuxRace) {
        $env:MOLIN_VIDEO_G7_MINIO_ENDPOINT = 'minio:9000'
        $goModCache = (go env GOMODCACHE).Trim()
        $arguments = @('run','--rm','--pull=never','--name',$builder,'--label','molin.goal=VID-G7','--network',$network,'--mount',"type=bind,src=$repoRoot,dst=/src,readonly",'--mount',"type=bind,src=$goModCache,dst=/go/pkg/mod,readonly",'-w','/src/server','-e','CGO_ENABLED=1','-e','GOPROXY=off','-e','MOLIN_VIDEO_G7_MINIO_ISOLATED=YES','-e','MOLIN_VIDEO_G7_MINIO_ENDPOINT=minio:9000','-e',"MOLIN_VIDEO_G7_MINIO_ACCESS=$access",'-e',"MOLIN_VIDEO_G7_MINIO_SECRET=$secret",$goImage,'go','test','-race','-json','-count=1','./internal/modules/token_gateway/video','./internal/modules/token_gateway/service','-run',$testPattern)
        $events = @(& docker @arguments 2>&1)
    } else {
        $env:MOLIN_VIDEO_G7_MINIO_ENDPOINT = $hostEndpoint
        Push-Location (Join-Path $repoRoot 'server')
        try { $events = @(go test -json -count=1 ./internal/modules/token_gateway/video ./internal/modules/token_gateway/service -run $testPattern 2>&1) } finally { Pop-Location }
    }
    $testExit = $LASTEXITCODE
    $safeEvents = @($events | ForEach-Object { $_.ToString().Replace($secret, '[REDACTED]') })
    $runCount = @($safeEvents | Where-Object { $_ -match '"Action":"run"' -and $_ -match '"Test":"TestVideoG7MinIO(ObjectStoreIntegration|UploadSealIntegration)"' }).Count
    $passCount = @($safeEvents | Where-Object { $_ -match '"Action":"pass"' -and $_ -match '"Test":"TestVideoG7MinIO(ObjectStoreIntegration|UploadSealIntegration)"' }).Count
    $skipCount = @($safeEvents | Where-Object { $_ -match '"Action":"skip"' -and $_ -match '"Test":"TestVideoG7MinIO(ObjectStoreIntegration|UploadSealIntegration)"' }).Count
    if ($testExit -ne 0 -or $runCount -ne $requiredTests.Count -or $passCount -ne $requiredTests.Count -or $skipCount -ne 0) {
        $safeEvents | Select-Object -Last 80 | Write-Output
        throw "minio_test_failed_exit_${testExit}_run_${runCount}_pass_${passCount}_skip_${skipCount}"
    }
    $afterHash = Get-VideoG7MinIOSourceHash
    if ($afterHash -ne $sourceHash) { throw 'source_changed_during_test' }
    Write-Output "VIDEO_G7_MINIO_SOURCE=UNCHANGED sha256=$sourceHash"
    Write-Output 'VIDEO_G7_MINIO_TEST=PASS required=2 run=2 pass=2 skip=0 concurrent_winner=1 anonymous_access=denied archive_fence=pass upload_seal=pass late_overwrite=denied discard_tombstone=pass temp_cleanup=pass'
    $resultCode = 0
} catch {
    Write-Output ('VIDEO_G7_MINIO=FAIL reason=' + $_.Exception.Message)
    $resultCode = 2
} finally {
    foreach ($name in $previous.Keys) { [Environment]::SetEnvironmentVariable($name, $previous[$name], 'Process') }
    $cleanupFailed = $false
    foreach ($name in @($container, $builder)) {
        docker container inspect $name *> $null
        if ($LASTEXITCODE -eq 0) {
            docker rm -f $name 2>$null | Out-Null
            if ($LASTEXITCODE -ne 0) { $cleanupFailed = $true }
        }
    }
    if ($createdNetwork) {
        docker network inspect $network *> $null
        if ($LASTEXITCODE -eq 0) {
            docker network rm $network 2>$null | Out-Null
            if ($LASTEXITCODE -ne 0) { $cleanupFailed = $true }
        }
    }
    $leftovers = @(docker ps -a --filter 'label=molin.goal=VID-G7' --format '{{.Names}}' | Where-Object { $_ -in @($container, $builder) })
    docker network inspect $network *> $null
    $networkLeft = $LASTEXITCODE -eq 0
    if ($cleanupFailed -or $leftovers.Count -ne 0 -or $networkLeft) {
        Write-Output 'VIDEO_G7_MINIO=CLEANUP_FAILED'
        $resultCode = 2
    }
}

if ($resultCode -eq 0) {
    Write-Output 'VIDEO_G7_MINIO=PASS cleanup=PASS real_provider=0 real_wallet=0 test_server_writes=0'
}
exit $resultCode
