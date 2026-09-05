param([switch]$LinuxRace, [ValidateSet('all','binding','recovery')][string]$Focus='all')
$ErrorActionPreference='Stop'
if ($env:VIDEO_GATEWAY_G7_REDIS_ISOLATED_APPROVED -ne 'YES') { Write-Output 'VIDEO_G7_REDIS=APPROVAL_REQUIRED'; exit 3 }
$repoRoot=(Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$redisImage='redis@sha256:e9b2e45ecd47fbb69b877cf8d045d5cccaaaed52524b6e098b4abe8212994f73'
$goImage='golang@sha256:908f8ff2ec296df2f349563072c7925775cd28b50361a52ed834a8a37399b9bf'
$suffix=[Guid]::NewGuid().ToString('N').Substring(0,12)
$redisName="molin-vidg7-capacity-redis-$suffix"
$builderName="molin-vidg7-capacity-go-$suffix"
$networkName="molin-vidg7-capacity-net-$suffix"
$redisID=''; $builderID=''; $networkID=''; $resultCode=2
$password=[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$variables=@('MOLIN_VIDEO_G7_REDIS_ISOLATED','MOLIN_VIDEO_G7_REDIS_ADDR','MOLIN_VIDEO_G7_REDIS_PASSWORD','MOLIN_VIDEO_G7_REDIS_RUN_ID')
$previous=@{}
foreach ($name in $variables) { $previous[$name]=[Environment]::GetEnvironmentVariable($name,'Process') }
function Get-RedisTestSourceHash {
    $paths=@(git -C $repoRoot ls-files --cached --others --exclude-standard -- server | Sort-Object -Unique)
    if ($LASTEXITCODE -ne 0 -or $paths.Count -eq 0) {throw 'source_list_failed'}
    $records=@($paths | ForEach-Object {$_+'|'+(Get-FileHash -LiteralPath (Join-Path $repoRoot $_) -Algorithm SHA256).Hash.ToLowerInvariant()})
    [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes(($records -join "`n")+"`n"))).ToLowerInvariant()
}
try {
    $sourceHash=Get-RedisTestSourceHash
    Push-Location (Join-Path $repoRoot 'server')
    try {$listed=@(go test ./internal/modules/token_gateway/service -list '^TestVideoG7RedisCapacity' 2>&1);$listedExit=$LASTEXITCODE} finally {Pop-Location}
    $required=@($listed | ForEach-Object {$_.ToString()} | Where-Object {$_ -match '^TestVideoG7RedisCapacity'})
    if ($Focus -eq 'binding') {$required=@($required | Where-Object {$_ -eq 'TestVideoG7RedisCapacityRequestBinding'})}
    if ($Focus -eq 'recovery') {$required=@($required | Where-Object {$_ -like 'TestVideoG7RedisCapacityRecovery*'})}
    if ($listedExit -ne 0 -or $required.Count -eq 0) {throw 'test_discovery_failed'}
    $pattern='^('+($required -join '|')+')$'
    docker image inspect $redisImage --format '{{.Id}}' | Out-Null
    if ($LASTEXITCODE -ne 0) {throw 'locked_image_missing'}
    if ($LinuxRace) {docker image inspect $goImage --format '{{.Id}}' | Out-Null;if ($LASTEXITCODE -ne 0) {throw 'locked_image_missing'}}
    $networkID=docker network create --label molin.goal=VID-G7 $networkName
    if ($LASTEXITCODE -ne 0 -or $networkID -notmatch '^[0-9a-f]{64}$') {$networkID='';throw 'network_create_failed'}
    # 不使用既有容器、磁盘卷或凭据；只发布loopback，Redis数据位于本轮tmpfs。
    $redisID=docker create --pull=never --name $redisName --label molin.goal=VID-G7 --network $networkName --read-only --tmpfs '/data:rw,nosuid,size=64m' -p '127.0.0.1::6379' $redisImage redis-server --protected-mode yes --requirepass $password --appendonly no
    if ($LASTEXITCODE -ne 0 -or $redisID -notmatch '^[0-9a-f]{64}$') {$redisID='';throw 'redis_create_failed'}
    docker start $redisID | Out-Null
    if ($LASTEXITCODE -ne 0) {throw 'redis_start_failed'}
    $runID=''
    for ($attempt=0;$attempt -lt 30;$attempt++) {
        $info=@(docker exec -e "REDISCLI_AUTH=$password" $redisID redis-cli --no-auth-warning INFO server 2>&1)
        if ($LASTEXITCODE -eq 0) {
            $line=@($info | ForEach-Object {$_.ToString().Trim()} | Where-Object {$_ -match '^run_id:[0-9a-f]{40}$'})
            if ($line.Count -eq 1) {$runID=$line[0].Substring(7);break}
        }
        Start-Sleep -Milliseconds 200
    }
    if ($runID -notmatch '^[0-9a-f]{40}$') {throw 'redis_not_ready'}
    $port=(docker port $redisID 6379/tcp | Select-Object -First 1)
    if ($LASTEXITCODE -ne 0 -or $port -notmatch '^127\.0\.0\.1:\d+$') {throw 'loopback_missing'}
    $env:MOLIN_VIDEO_G7_REDIS_ISOLATED='YES'
    $env:MOLIN_VIDEO_G7_REDIS_ADDR=$port
    $env:MOLIN_VIDEO_G7_REDIS_PASSWORD=$password
    $env:MOLIN_VIDEO_G7_REDIS_RUN_ID=$runID
    Write-Output ('VIDEO_G7_REDIS=TESTING required='+$required.Count+' linux_race='+[bool]$LinuxRace)
    Push-Location (Join-Path $repoRoot 'server')
    try {
        if ($LinuxRace) {
            $moduleCache=(go env GOMODCACHE).Trim()
            $builderID=docker create --pull=never --name $builderName --label molin.goal=VID-G7 --network $networkName --read-only --mount "type=bind,src=$repoRoot,dst=/workspace,readonly" --mount "type=bind,src=$moduleCache,dst=/go/pkg/mod,readonly" --tmpfs '/tmp:rw,exec,nosuid,size=2g' -w /workspace/server -e GOCACHE=/tmp/go-build -e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local -e CGO_ENABLED=1 -e MOLIN_VIDEO_G7_REDIS_ISOLATED=YES -e "MOLIN_VIDEO_G7_REDIS_ADDR=${redisName}:6379" -e "MOLIN_VIDEO_G7_REDIS_PASSWORD=$password" -e "MOLIN_VIDEO_G7_REDIS_RUN_ID=$runID" $goImage go test -race -json -count=1 -timeout=300s ./internal/modules/token_gateway/service -run $pattern
            if ($LASTEXITCODE -ne 0 -or $builderID -notmatch '^[0-9a-f]{64}$') {$builderID='';throw 'builder_create_failed'}
            $output=@(docker start -a $builderID 2>&1)
        } else {$output=@(go test -json -count=1 -timeout=300s ./internal/modules/token_gateway/service -run $pattern 2>&1)}
        $testExit=$LASTEXITCODE
    } finally {Pop-Location}
    if ((Get-RedisTestSourceHash) -ne $sourceHash) {throw 'source_changed'}
    Write-Output ('VIDEO_G7_REDIS_SOURCE=UNCHANGED sha256='+$sourceHash)
    $events=@($output | ForEach-Object {try {$_.ToString() | ConvertFrom-Json -ErrorAction Stop} catch {}})
    $runs=@($events | Where-Object {$_.Action -eq 'run' -and $_.Test -in $required})
    $passes=@($events | Where-Object {$_.Action -eq 'pass' -and $_.Test -in $required})
    $bad=@($events | Where-Object {$_.Action -in @('fail','skip')})
    $complete=$true
    foreach($test in $required) {if (@($runs | Where-Object {$_.Test -eq $test}).Count -ne 1 -or @($passes | Where-Object {$_.Test -eq $test}).Count -ne 1) {$complete=$false}}
    if ($testExit -ne 0 -or -not $complete -or $bad.Count -ne 0) {
        $output | Select-Object -Last 25 | ForEach-Object {Write-Output ($_.ToString().Replace($password,'[REDACTED]'))}
        throw 'tests_failed_or_skipped'
    }
    $passes | ForEach-Object {Write-Output ('VIDEO_G7_REDIS_TEST=PASS test='+$_.Test+' elapsed_seconds='+$_.Elapsed)}
    Write-Output ('VIDEO_G7_REDIS_COUNTS=PASS required='+$required.Count+' run='+$runs.Count+' pass='+$passes.Count+' skip=0')
    $resultCode=0
} catch {
    $safe=@('source_list_failed','test_discovery_failed','locked_image_missing','network_create_failed','redis_create_failed','redis_start_failed','redis_not_ready','loopback_missing','builder_create_failed','source_changed','tests_failed_or_skipped')
    $reason='local_runner_error'
    if ($_.Exception.Message -in $safe) {$reason=$_.Exception.Message}
    Write-Output ('VIDEO_G7_REDIS=FAIL reason='+$reason)
} finally {
    foreach($name in $variables) {[Environment]::SetEnvironmentVariable($name,$previous[$name],'Process')}
    # 只回收由本次create返回且标签相符的精确ID；任何清理失败都不得报告PASS。
    foreach($id in @($builderID,$redisID)) {
        if ($id) {
            $owner=docker inspect --format '{{index .Config.Labels "molin.goal"}}' $id
            if ($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7') {docker rm -f $id | Out-Null;if ($LASTEXITCODE -ne 0) {$resultCode=2}} else {$resultCode=2}
        }
    }
    if ($networkID) {
        $owner=docker network inspect --format '{{index .Labels "molin.goal"}}' $networkID
        if ($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7') {docker network rm $networkID | Out-Null;if ($LASTEXITCODE -ne 0) {$resultCode=2}} else {$resultCode=2}
    }
}
if ($resultCode -eq 0) {Write-Output 'VIDEO_G7_REDIS=PASS cleanup=PASS real_provider=0 real_wallet=0 test_server_writes=0'}
exit $resultCode
