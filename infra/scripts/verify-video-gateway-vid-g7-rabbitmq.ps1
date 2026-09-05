param([ValidateSet('all','topology','publisher','consumer')][string]$Focus = 'all')
$ErrorActionPreference = 'Stop'
if ($env:VIDEO_GATEWAY_G7_RABBIT_ISOLATED_APPROVED -ne 'YES') {
    Write-Output 'VIDEO_G7_RABBIT=APPROVAL_REQUIRED'
    exit 3
}

# 镜像只使用已缓存的固定摘要；网络、容器和数据只属于本轮，不触碰共享服务。
$image = 'rabbitmq@sha256:606d8c0d6b3c18d1da9afc53bc7cdb2a8d5486df91b5a9830e9e07626c9ae281'
$suffix = [Guid]::NewGuid().ToString('N').Substring(0, 12)
$container = "molin-vidg7-rabbit-$suffix"
$network = "molin-vidg7-rabbit-net-$suffix"
$password = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$previousURL = $env:MOLIN_VIDEO_G7_AMQP_TEST_URL
$previousApproval = $env:MOLIN_VIDEO_G7_RABBIT_ISOLATED
$createdNetwork = $false
$createdContainer = $false
$resultCode = 2
$required = @('TestVideoG7RabbitTopologyIsolated', 'TestVideoG7RabbitPublisherIsolated', 'TestVideoG7RabbitConsumerIsolated')
if ($Focus -eq 'topology') { $required = @('TestVideoG7RabbitTopologyIsolated') }
if ($Focus -eq 'publisher') { $required = @('TestVideoG7RabbitPublisherIsolated') }
if ($Focus -eq 'consumer') { $required = @('TestVideoG7RabbitConsumerIsolated') }
$pattern = '^(' + ($required -join '|') + ')$'
try {
    docker image inspect $image --format '{{.Id}}' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'locked_image_missing' }
    # 本机Go进程通过loopback发布端口访问独立bridge；internal网络不提供此宿主机映射。
    # 这是资源隔离而非出站断网证明；测试代码只接受127.0.0.1与专用vhost。
    docker network create --label molin.goal=VID-G7 $network | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'isolated_network_failed' }
    $createdNetwork = $true
    $createdContainer = $true
    docker run -d --pull=never --name $container --label molin.goal=VID-G7 --network $network -p '127.0.0.1::5672' --tmpfs '/var/lib/rabbitmq:rw,nosuid,size=512m' -e RABBITMQ_DEFAULT_USER=vid_g7 -e "RABBITMQ_DEFAULT_PASS=$password" -e RABBITMQ_DEFAULT_VHOST=vid_g7 $image | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'isolated_container_failed' }
    $createdContainer = $true
    Write-Output 'VIDEO_G7_RABBIT=STARTED target=temporary_container'
    $portLine = (docker port $container 5672/tcp | Select-Object -First 1)
    if ($LASTEXITCODE -ne 0 -or $portLine -notmatch '^127\.0\.0\.1:(\d+)$') { throw 'loopback_port_missing' }
    $port = $Matches[1]
    $ready = $false
    for ($attempt = 0; $attempt -lt 45; $attempt++) {
        $state = docker inspect --format '{{.State.Status}}' $container
        if ($LASTEXITCODE -ne 0 -or $state -in @('exited','dead')) { throw 'rabbit_exited_before_ready' }
        # Docker代理可在Broker就绪前接受TCP；用服务身份验证Rabbit应用而非仅测试端口。
        # 不以root运行诊断，避免抢先创建root所有的服务cookie造成EACCES。
        docker exec --user rabbitmq $container rabbitmq-diagnostics -q check_running 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) { $ready = $true }
        if ($ready) { break }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) { throw 'rabbit_not_ready' }
    Write-Output 'VIDEO_G7_RABBIT=READY target=temporary_container'
    $env:MOLIN_VIDEO_G7_AMQP_TEST_URL = "amqp://vid_g7:${password}@127.0.0.1:${port}/vid_g7"
    $env:MOLIN_VIDEO_G7_RABBIT_ISOLATED = 'YES'
    Push-Location (Join-Path $repoRoot 'server')
    try {
        $output = @(go test -json -count=1 -timeout=360s ./internal/modules/token_gateway/video -run $pattern 2>&1)
        $testExit = $LASTEXITCODE
    } finally { Pop-Location }
    $events = @($output | ForEach-Object { try { $_.ToString() | ConvertFrom-Json -ErrorAction Stop } catch { } })
    $runs = @($events | Where-Object { $_.Action -eq 'run' -and $_.Test -in $required })
    $passes = @($events | Where-Object { $_.Action -eq 'pass' -and $_.Test -in $required })
    $bad = @($events | Where-Object { $_.Action -in @('fail','skip') })
    $complete = $true
    foreach ($test in $required) {
        if (@($runs | Where-Object { $_.Test -eq $test }).Count -ne 1 -or @($passes | Where-Object { $_.Test -eq $test }).Count -ne 1) { $complete = $false }
    }
    if ($testExit -ne 0 -or -not $complete -or $bad.Count -ne 0) {
        # 诊断先遮蔽本轮临时口令，不能直接打印连接URL或原始环境。
        $output | ForEach-Object { Write-Output ($_.ToString().Replace($password, '[REDACTED]')) }
        throw 'required_test_failed_or_skipped'
    }
    $passes | ForEach-Object { Write-Output ('VIDEO_G7_RABBIT_TEST=PASS test=' + $_.Test + ' elapsed_seconds=' + $_.Elapsed) }
    $resultCode = 0
} catch {
    $safeReasons = @('locked_image_missing','isolated_network_failed','isolated_container_failed','rabbit_not_ready','rabbit_exited_before_ready','loopback_port_missing','required_test_failed_or_skipped')
    $reason = 'unexpected_local_runner_error'
    if ($_.Exception.Message -in $safeReasons) { $reason = $_.Exception.Message }
    Write-Output "VIDEO_G7_RABBIT=FAIL scope=isolated_topology reason=$reason"
} finally {
    $env:MOLIN_VIDEO_G7_AMQP_TEST_URL = $previousURL
    $env:MOLIN_VIDEO_G7_RABBIT_ISOLATED = $previousApproval
    # 只回收本轮成功创建且带正确归属标签的精确名称；不枚举/删除任何共享卷。
    if ($createdContainer) {
        $owner = docker inspect --format '{{index .Config.Labels "molin.goal"}}' $container
        if ($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7') {
            docker rm -f $container | Out-Null
            if ($LASTEXITCODE -ne 0) { $resultCode = 2 }
        } elseif ($resultCode -eq 0) { $resultCode = 2 }
    }
    if ($createdNetwork) {
        $owner = docker network inspect --format '{{index .Labels "molin.goal"}}' $network
        if ($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7') {
            docker network rm $network | Out-Null
            if ($LASTEXITCODE -ne 0) { $resultCode = 2 }
        } elseif ($resultCode -eq 0) { $resultCode = 2 }
    }
}
if ($resultCode -eq 0) { Write-Output "VIDEO_G7_RABBIT=PASS scope=$Focus cleanup=PASS real_provider=0 real_wallet=0 test_server_writes=0" }
exit $resultCode
