param([switch]$LinuxRace, [switch]$FinanceRegression, [switch]$Broker, [switch]$Redis, [ValidateSet('all','integrity','projection','relay','rabbit_business','native_http','process_kill','object_observation','lease','heartbeat','receipt','financial_fence','cancel_fence','outer_cancel','capacity_epoch','capacity_epoch_version','capacity_audit_types','capacity_boundary','capacity_cutoff','capacity_snapshot','capacity_ready','capacity_coordinator','capacity_runtime','capacity_execution','capacity_execution_history','capacity_execution_recovery','capacity_send_crash','capacity_terminal_release','capacity_reservation','capacity_process_2','capacity_process_4','capacity_process_8','submission_plan')][string]$Focus='all')
$ErrorActionPreference = 'Stop'
if ($env:VIDEO_GATEWAY_G7_MYSQL_ISOLATED_APPROVED -ne 'YES') {
    Write-Output 'VIDEO_G7_OUTBOX=APPROVAL_REQUIRED'
    exit 3
}
if (($Focus -in @('relay','rabbit_business') -and -not $Broker) -or ($Broker -and $env:VIDEO_GATEWAY_G7_RABBIT_ISOLATED_APPROVED -ne 'YES')) {
    Write-Output 'VIDEO_G7_OUTBOX=BROKER_APPROVAL_REQUIRED'
    exit 3
}
if (($Focus -in @('rabbit_business','native_http','process_kill','capacity_coordinator','capacity_runtime','capacity_execution','capacity_execution_history','capacity_execution_recovery','capacity_send_crash','capacity_terminal_release','capacity_reservation','capacity_process_2','capacity_process_4','capacity_process_8') -and -not $Redis) -or ($Redis -and $env:VIDEO_GATEWAY_G7_REDIS_ISOLATED_APPROVED -ne 'YES')) { Write-Output 'VIDEO_G7_OUTBOX=REDIS_APPROVAL_REQUIRED'; exit 3 }

# 复用G5真实预占夹具和全部migration，不读取应用配置或连接共享数据库。
$image = 'mysql@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b'
$goImage = 'golang@sha256:908f8ff2ec296df2f349563072c7925775cd28b50361a52ed834a8a37399b9bf'
$brokerImage = 'rabbitmq@sha256:606d8c0d6b3c18d1da9afc53bc7cdb2a8d5486df91b5a9830e9e07626c9ae281'
$redisImage = 'redis@sha256:e9b2e45ecd47fbb69b877cf8d045d5cccaaaed52524b6e098b4abe8212994f73'
$suffix = [Guid]::NewGuid().ToString('N').Substring(0, 12)
$container = "molin-vidg7-outbox-mysql-$suffix"
$network = "molin-vidg7-outbox-net-$suffix"
$builder = "molin-vidg7-outbox-go-$suffix"
$brokerContainer = "molin-vidg7-outbox-rabbit-$suffix"
$redisContainer = "molin-vidg7-capacity-redis-$suffix"
$brokerID = ''
$redisID = ''
$password = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$brokerPassword = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$redisPassword = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$previousDSN = $env:MOLIN_VIDEO_G5_MYSQL_DSN
$previousApproval = $env:MOLIN_VIDEO_G5_ISOLATED
$previousLeaseUUID = $env:MOLIN_VIDEO_G7_LEASE_MYSQL_SERVER_UUID
$previousRelayURL = $env:MOLIN_VIDEO_G7_RELAY_AMQP_URL
$previousRelayApproval = $env:MOLIN_VIDEO_G7_RELAY_ISOLATED
$redisVars = @('MOLIN_VIDEO_G7_REDIS_ISOLATED','MOLIN_VIDEO_G7_REDIS_ADDR','MOLIN_VIDEO_G7_REDIS_PASSWORD','MOLIN_VIDEO_G7_REDIS_RUN_ID')
$previousRedis = @{}; foreach($name in $redisVars){$previousRedis[$name]=[Environment]::GetEnvironmentVariable($name,'Process')}
$previousEncoding = $OutputEncoding
$OutputEncoding = [Text.UTF8Encoding]::new($false)
$createdNetwork = $false
$createdContainer = $false
$createdBuilder = $false
$resultCode = 2
$separatePassed = 0
$separateGroups = 0
$completeRequired = 0
$required = @('TestVideoG7OutboxScopeMySQL', 'TestVideoG7OutboxConcurrentClaimMySQL', 'TestVideoG7OutboxSameSecondRequeueMySQL', 'TestVideoG7OutboxAggregateOrderMySQL', 'TestVideoG7OutboxRetryHighWaterMySQL', 'TestVideoG7OutboxBatchTokensMySQL', 'TestVideoG5ReserveMySQLOutboxDispatcherOff')
$required += @('TestVideoG7OutboxFinancialReplayMySQL', 'TestVideoG7OutboxBeforeFinancialTerminalMySQL')
$required += @('TestVideoG7OutboxIdentityMySQL', 'TestVideoG7OutboxRecoveryTransportMySQL', 'TestVideoG7OutboxMalformedTransportMySQL')
$required += @('TestVideoG7OutboxProjectionMySQL', 'TestVideoG7OutboxProjectionLeaseMySQL')
$required += @('TestVideoG7OutboxProjectionFabricatedFactsMySQL', 'TestVideoG7OutboxProjectionRecoveryAndAdjustmentMySQL', 'TestVideoG7OutboxProjectionRunningMySQL')
$required += 'TestVideoG7OutboxPublisherRequiresDependencies'
$required += @('TestVideoG7ObjectObservationMySQL','TestVideoG7ObjectCleanupRetryBoundedMySQL','TestVideoG7ObjectMissingWorkerRecoveryMySQL')
$required += @('TestVideoG7WorkerLeaseMySQL','TestVideoG7WorkerLeaseExpiryMySQL','TestVideoG7WorkerLeaseFencesTaskMySQL')
$required += 'TestVideoG7WorkerLeaseSQLMySQL'
$required += @('TestVideoG7WorkerLeaseRollbackMySQL','TestVideoG7WorkerLeaseIsolationMySQL','TestVideoG7WorkerLeasePendingInputMySQL')
$required += 'TestVideoG7WorkerHeartbeatMySQL'
$required += @('TestVideoG7WorkerHeartbeatFailureMySQL','TestVideoG7WorkerHeartbeatExitMySQL')
$required += 'TestVideoG7SubmissionWorkerFenceMySQL'
$required += 'TestVideoG7SubmissionWorkerTailExpiryMySQL'
$required += 'TestVideoG7WorkerFinancialSettleMySQL'
$required += 'TestVideoG7WorkerFinancialReleaseMySQL'
$required += 'TestVideoG7WorkerFinancialCompensationMySQL'
$required += 'TestVideoG7WorkerFinancialTailMySQL'
$required += 'TestVideoG7WorkerCancelFenceMySQL'
$required += 'TestVideoG7WorkerCancelOuterTransactionMySQL'
$required += 'TestVideoG7CapacityRecoveryEpochMySQL'
$required += 'TestVideoG7CapacityRecoveryVersionMySQL'
$required += 'TestVideoG7CapacityRecoveryAuditTypesMySQL'
$required += 'TestVideoG7CapacityRecoveryBoundaryMySQL'
$required += 'TestVideoG7SubmissionPlanMySQL'
$required += @('TestVideoG7SubmissionPlanConcurrentMySQL','TestVideoG7SubmissionPlanSQLMySQL','TestVideoG7SubmissionPlanIdentityMySQL')
$required += @('TestVideoG7SubmissionPlanRootAndOwnerMySQL','TestVideoG7SubmissionPlanCommitUnknownMySQL','TestVideoG7SubmissionPlanTailExpiryMySQL')
$required += 'TestVideoG7CapacityCutoffMySQL'
$required += 'TestVideoG7CapacitySnapshotMySQL'
$required += 'TestVideoG7CapacityReadyMySQL'
if ($Redis) { $required += @('TestVideoG7NativeHTTPSubmissionMySQLRedis','TestVideoG7WorkerProcessKillNoResubmitMySQLRedis','TestVideoG7CapacityRecoveryCoordinatorMySQLRedis','TestVideoG7CapacityRuntimeBootstrapMySQLRedis','TestVideoG7CapacityExecutionCoordinatorMySQLRedis','TestVideoG7CapacityExecutionHistoricalPlanMySQLRedis','TestVideoG7CapacityExecutionNextEpochRecoveryMySQLRedis','TestVideoG7CapacitySendPermitCrashWindowMySQLRedis','TestVideoG7CapacityTerminalReleaseMySQLRedis','TestVideoG7CapacityReservationCoordinatorMySQLRedis','TestVideoG7CapacityMultiProcess2MySQLRedis','TestVideoG7CapacityMultiProcess4MySQLRedis','TestVideoG7CapacityMultiProcess8MySQLRedis') }
if ($Broker) { $required += 'TestVideoG7OutboxRelayMySQLRabbit' }
if ($Broker -and $Redis) { $required += 'TestVideoG7RabbitBusinessHandlerMySQLRedis' }
if ($Focus -eq 'integrity') { $required = @('TestVideoG7OutboxIdentityMySQL', 'TestVideoG7OutboxRecoveryTransportMySQL', 'TestVideoG7OutboxMalformedTransportMySQL') }
if ($Focus -eq 'projection') { $required = @($required | Where-Object { $_ -like 'TestVideoG7OutboxProjection*' }) }
if ($Focus -eq 'relay') { $required = @('TestVideoG7OutboxPublisherRequiresDependencies', 'TestVideoG7OutboxRelayMySQLRabbit') }
if ($Focus -eq 'rabbit_business') { $required = @('TestVideoG7RabbitBusinessHandlerMySQLRedis') }
if ($Focus -eq 'native_http') { $required = @('TestVideoG7NativeHTTPSubmissionMySQLRedis') }
if ($Focus -eq 'process_kill') { $required = @('TestVideoG7WorkerProcessKillNoResubmitMySQLRedis') }
if ($Focus -eq 'object_observation') { $required = @('TestVideoG7ObjectObservationMySQL','TestVideoG7ObjectCleanupRetryBoundedMySQL','TestVideoG7ObjectMissingWorkerRecoveryMySQL') }
if ($Focus -eq 'lease') { $required = @($required | Where-Object { $_ -like 'TestVideoG7WorkerLease*' }) }
if ($Focus -eq 'heartbeat') { $required = @($required | Where-Object { $_ -like 'TestVideoG7WorkerHeartbeat*' }) }
if ($Focus -eq 'receipt') { $required = @($required | Where-Object { $_ -like 'TestVideoG7Submission*' }) }
if ($Focus -eq 'financial_fence') { $required = @($required | Where-Object { $_ -like 'TestVideoG7WorkerFinancial*' }) }
if ($Focus -eq 'cancel_fence') { $required = @($required | Where-Object { $_ -like 'TestVideoG7WorkerCancel*' }) }
if ($Focus -eq 'outer_cancel') { $required = @('TestVideoG7WorkerCancelOuterTransactionMySQL') }
if ($Focus -eq 'capacity_epoch') { $required = @('TestVideoG7CapacityRecoveryEpochMySQL','TestVideoG7CapacityRecoveryVersionMySQL') }
if ($Focus -eq 'capacity_epoch_version') { $required = @('TestVideoG7CapacityRecoveryVersionMySQL') }
if ($Focus -eq 'capacity_audit_types') { $required = @('TestVideoG7CapacityRecoveryAuditTypesMySQL') }
if ($Focus -eq 'capacity_boundary') { $required = @('TestVideoG7CapacityRecoveryBoundaryMySQL') }
if ($Focus -eq 'capacity_cutoff') { $required = @('TestVideoG7CapacityCutoffMySQL') }
if ($Focus -eq 'capacity_snapshot') { $required = @('TestVideoG7CapacitySnapshotMySQL') }
if ($Focus -eq 'capacity_ready') { $required = @('TestVideoG7CapacityReadyMySQL') }
if ($Focus -eq 'capacity_coordinator') { $required = @('TestVideoG7CapacityRecoveryCoordinatorMySQLRedis') }
if ($Focus -eq 'capacity_runtime') { $required = @('TestVideoG7CapacityRuntimeBootstrapMySQLRedis') }
if ($Focus -eq 'capacity_execution') { $required = @('TestVideoG7CapacityExecutionCoordinatorMySQLRedis') }
if ($Focus -eq 'capacity_execution_history') { $required = @('TestVideoG7CapacityExecutionHistoricalPlanMySQLRedis') }
if ($Focus -eq 'capacity_execution_recovery') { $required = @('TestVideoG7CapacityExecutionNextEpochRecoveryMySQLRedis') }
if ($Focus -eq 'capacity_send_crash') { $required = @('TestVideoG7CapacitySendPermitCrashWindowMySQLRedis') }
if ($Focus -eq 'capacity_terminal_release') { $required = @('TestVideoG7CapacityTerminalReleaseMySQLRedis') }
if ($Focus -eq 'capacity_reservation') { $required = @('TestVideoG7CapacityReservationCoordinatorMySQLRedis') }
if ($Focus -eq 'capacity_process_2') { $required = @('TestVideoG7CapacityMultiProcess2MySQLRedis') }
if ($Focus -eq 'capacity_process_4') { $required = @('TestVideoG7CapacityMultiProcess4MySQLRedis') }
if ($Focus -eq 'capacity_process_8') { $required = @('TestVideoG7CapacityMultiProcess8MySQLRedis') }
if ($Focus -eq 'submission_plan') { $required = @($required | Where-Object { $_ -like 'TestVideoG7SubmissionPlan*' }) }
$pattern = '^(' + ($required -join '|') + ')$'

# 源码与migration执行前后必须相同，测试过程中不得编辑；证据文件不参与此哈希。
function Get-TestSourceHash {
    $paths = @(git -C $repoRoot ls-files --cached --others --exclude-standard -- server | Sort-Object -Unique)
    if ($LASTEXITCODE -ne 0 -or $paths.Count -eq 0) { throw 'source_list_failed' }
    $records = foreach ($path in $paths) { $path + '|' + (Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant() }
    return [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes(($records -join "`n") + "`n"))).ToLowerInvariant()
}

function Protect-RelayOutput([string]$Value) { return $Value.Replace($password, '[REDACTED]').Replace($brokerPassword, '[REDACTED]').Replace($redisPassword, '[REDACTED]') }
try {
    $sourceHash = Get-TestSourceHash
    $testTimeout = '300s'
    if ($Focus -eq 'receipt') {
        # 写入围栏必须连同G5原提交/迟到/审计/重放合同验证，不能只跑新拒绝路径。
        $submissionFilter='^TestVideoG5Submission'
        Push-Location (Join-Path $repoRoot 'server')
        try { $submissionList=@(go test ./internal/modules/token_gateway/service -list $submissionFilter 2>&1); $submissionExit=$LASTEXITCODE } finally { Pop-Location }
        $submissionRequired=@($submissionList | ForEach-Object { $_.ToString() } | Where-Object { $_ -match $submissionFilter })
        if ($submissionExit -ne 0 -or $submissionRequired.Count -eq 0) { throw 'submission_test_discovery_failed' }
        $required=@($required+$submissionRequired | Sort-Object -Unique)
        $pattern='^('+($required -join '|')+')$'
        Write-Output ('VIDEO_G7_SUBMISSION=DISCOVERED required='+$submissionRequired.Count)
    }
    if ($FinanceRegression) {
        # 沿用G5默认all的完整筛选，并把每个发现的顶层测试加入必需RUN/PASS，禁止零匹配冒充回归。
        $financeFilter = '^TestVideoG5(Reserve|Usage|Cancel|Media|Settle|Release|Compensation|Delivery|Reconciliation|Unknown|Submission|Adjustment|Golden|Compatibility)'
        Push-Location (Join-Path $repoRoot 'server')
        try { $listed = @(go test ./internal/modules/token_gateway/service -list $financeFilter 2>&1); $listExit = $LASTEXITCODE } finally { Pop-Location }
        $financeRequired = @($listed | ForEach-Object { $_.ToString() } | Where-Object { $_ -match $financeFilter })
        if ($listExit -ne 0 -or $financeRequired.Count -eq 0) { throw 'finance_test_discovery_failed' }
        $required = @($required + $financeRequired | Sort-Object -Unique)
        $pattern = '^(' + ($required -join '|') + ')$'
        $testTimeout = '1200s'
        Write-Output ('VIDEO_G7_FINANCE=DISCOVERED required=' + $financeRequired.Count)
    }
    $completeRequired = $required.Count
    # G6真实创建遵守全局queued=100；旧Outbox并发测试保留超过100个Task，必须按数据库边界隔离，不能提高限额或删除事实。
    # 上界测试会合法耗尽原单行门闩，须先分到新库；不能让它进入普通恢复组后再清零。
    foreach ($group in @(@{Focus='outer_cancel';Tests=@('TestVideoG7WorkerCancelOuterTransactionMySQL')},@{Focus='capacity_boundary';Tests=@('TestVideoG7CapacityRecoveryBoundaryMySQL')},@{Focus='capacity_epoch';Tests=@('TestVideoG7CapacityRecoveryEpochMySQL','TestVideoG7CapacityRecoveryVersionMySQL')},@{Focus='capacity_audit_types';Tests=@('TestVideoG7CapacityRecoveryAuditTypesMySQL')},@{Focus='capacity_cutoff';Tests=@('TestVideoG7CapacityCutoffMySQL')},@{Focus='capacity_snapshot';Tests=@('TestVideoG7CapacitySnapshotMySQL')},@{Focus='capacity_ready';Tests=@('TestVideoG7CapacityReadyMySQL')},@{Focus='capacity_coordinator';Tests=@('TestVideoG7CapacityRecoveryCoordinatorMySQLRedis')},@{Focus='capacity_runtime';Tests=@('TestVideoG7CapacityRuntimeBootstrapMySQLRedis')},@{Focus='capacity_execution';Tests=@('TestVideoG7CapacityExecutionCoordinatorMySQLRedis')},@{Focus='capacity_execution_history';Tests=@('TestVideoG7CapacityExecutionHistoricalPlanMySQLRedis')},@{Focus='capacity_execution_recovery';Tests=@('TestVideoG7CapacityExecutionNextEpochRecoveryMySQLRedis')},@{Focus='capacity_send_crash';Tests=@('TestVideoG7CapacitySendPermitCrashWindowMySQLRedis')},@{Focus='capacity_terminal_release';Tests=@('TestVideoG7CapacityTerminalReleaseMySQLRedis')},@{Focus='capacity_reservation';Tests=@('TestVideoG7CapacityReservationCoordinatorMySQLRedis')},@{Focus='capacity_process_2';Tests=@('TestVideoG7CapacityMultiProcess2MySQLRedis')},@{Focus='capacity_process_4';Tests=@('TestVideoG7CapacityMultiProcess4MySQLRedis')},@{Focus='capacity_process_8';Tests=@('TestVideoG7CapacityMultiProcess8MySQLRedis')},@{Focus='native_http';Tests=@('TestVideoG7NativeHTTPSubmissionMySQLRedis')},@{Focus='process_kill';Tests=@('TestVideoG7WorkerProcessKillNoResubmitMySQLRedis')},@{Focus='rabbit_business';Tests=@('TestVideoG7RabbitBusinessHandlerMySQLRedis')})) {
        # 只计算子Focus实际运行的精确名称，不能将独立AuditTypes等通配匹配项假计为PASS。
        $groupTests=@($required | Where-Object {$_ -in $group.Tests})
        if ($groupTests.Count -gt 0 -and ($Focus -eq 'all' -or $FinanceRegression)) {
            $childFocus=$group.Focus
            if ($groupTests.Count -eq 1 -and $groupTests[0] -eq 'TestVideoG7CapacityRecoveryVersionMySQL') { $childFocus='capacity_epoch_version' }
            Write-Output ('VIDEO_G7_GROUP=START name='+$childFocus+' database=fresh_isolated')
            $childRedis = $childFocus -in @('native_http','process_kill','capacity_coordinator','capacity_runtime','capacity_execution','capacity_execution_history','capacity_execution_recovery','capacity_send_crash','capacity_terminal_release','capacity_reservation','capacity_process_2','capacity_process_4','capacity_process_8')
            $childBroker = $childFocus -eq 'rabbit_business'
            if ($childBroker) { $childRedis = $true }
            & $PSCommandPath -LinuxRace:$LinuxRace -Broker:$childBroker -Redis:$childRedis -Focus $childFocus
            if ($LASTEXITCODE -ne 0) { throw 'isolated_group_failed' }
            $separatePassed += $groupTests.Count
            $separateGroups++
            $required = @($required | Where-Object { $_ -notin $groupTests })
            $pattern = '^(' + ($required -join '|') + ')$'
            Write-Output ('VIDEO_G7_GROUP=PASS name='+$childFocus+' required='+$groupTests.Count+' run='+$groupTests.Count+' pass='+$groupTests.Count+' skip=0 cleanup=PASS')
        }
    }
    docker image inspect $image --format '{{.Id}}' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'locked_image_missing' }
    if ($LinuxRace) {
        docker image inspect $goImage --format '{{.Id}}' | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'locked_image_missing' }
    }
    if ($Broker) {
        docker image inspect $brokerImage --format '{{.Id}}' | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'locked_image_missing' }
    }
    if ($Redis) { docker image inspect $redisImage --format '{{.Id}}' | Out-Null; if($LASTEXITCODE -ne 0){throw 'locked_image_missing'} }
    docker network create --label molin.goal=VID-G7 $network | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'isolated_network_failed' }
    $createdNetwork = $true
    $createdContainer = $true
    docker run -d --pull=never --name $container --label molin.goal=VID-G7 --network $network -p '127.0.0.1::3306' --tmpfs '/var/lib/mysql:rw,noexec,nosuid,size=1g' -e "MYSQL_ROOT_PASSWORD=$password" -e MYSQL_ROOT_HOST=% -e MYSQL_DATABASE=molin_video_g5_contract $image --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'isolated_container_failed' }
    Write-Output 'VIDEO_G7_OUTBOX=STARTED target=temporary_mysql'
    if ($Broker) {
        # 先保存成功create返回的精确ID，再启动；没有创建证明不能自动删除同名资源。
        $brokerID = docker create --pull=never --name $brokerContainer --label molin.goal=VID-G7 --network $network -p '127.0.0.1::5672' --tmpfs '/var/lib/rabbitmq:rw,nosuid,size=512m' -e RABBITMQ_DEFAULT_USER=vid_g7 -e "RABBITMQ_DEFAULT_PASS=$brokerPassword" -e RABBITMQ_DEFAULT_VHOST=vid_g7 $brokerImage
        if ($LASTEXITCODE -ne 0 -or $brokerID -notmatch '^[0-9a-f]{64}$') { $brokerID=''; throw 'isolated_broker_create_failed' }
        docker start $brokerID | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'isolated_broker_start_failed' }
    }
    if($Redis){$redisID=docker create --pull=never --name $redisContainer --label molin.goal=VID-G7 --network $network -p '127.0.0.1::6379' --read-only --tmpfs '/data:rw,nosuid,size=64m' $redisImage redis-server --protected-mode yes --requirepass $redisPassword --appendonly no;if($LASTEXITCODE -ne 0 -or $redisID -notmatch '^[0-9a-f]{64}$'){$redisID='';throw 'isolated_redis_create_failed'};docker start $redisID|Out-Null;if($LASTEXITCODE -ne 0){throw 'isolated_redis_start_failed'}}
    $portLine = (docker port $container 3306/tcp | Select-Object -First 1)
    if ($LASTEXITCODE -ne 0 -or $portLine -notmatch '^127\.0\.0\.1:(\d+)$') { throw 'loopback_port_missing' }
    $port = $Matches[1]
    $ready = $false
    for ($attempt = 0; $attempt -lt 90; $attempt++) {
        $state = docker inspect --format '{{.State.Status}}' $container
        if ($LASTEXITCODE -ne 0 -or $state -in @('exited','dead')) { throw 'mysql_exited_before_ready' }
        # TCP查询不会误把初始化时仅监听socket的临时MySQL当作最终服务。
        $probeOutput = @(docker exec -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g5_contract -e 'SELECT 1' 2>&1)
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        if ($attempt -eq 20) {
            $probeOutput | Select-Object -Last 2 | ForEach-Object { Write-Output ($_.ToString().Replace($password, '[REDACTED]')) }
        }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) { throw 'mysql_not_ready' }
    # 全局DDL极限夹具只允许本轮新建实例，不能仅凭旧G5库名/环境标记误连接共享数据库。
    $leaseServerUUID = (docker exec -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g5_contract --batch --skip-column-names -e 'SELECT @@server_uuid').Trim()
    if ($LASTEXITCODE -ne 0 -or $leaseServerUUID -notmatch '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') { throw 'lease_target_identity_missing' }
    $env:MOLIN_VIDEO_G7_LEASE_MYSQL_SERVER_UUID=$leaseServerUUID
    $migrations = @(Get-ChildItem -LiteralPath (Join-Path $repoRoot 'server/migrations') -Filter '*.up.sql' | Sort-Object Name)
    if ($migrations.Count -eq 0) { throw 'migrations_missing' }
    foreach ($migration in $migrations) {
        $sqlOutput = @(Get-Content -LiteralPath $migration.FullName -Raw -Encoding utf8 | docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch 2>&1)
        if ($LASTEXITCODE -ne 0) {
            Write-Output ('VIDEO_G7_SCHEMA=FAIL migration=' + $migration.Name)
            $sqlOutput | Select-Object -Last 5 | ForEach-Object { Write-Output ($_.ToString().Replace($password, '[REDACTED]')) }
            throw 'migration_failed'
        }
        if ([int]$migration.Name.Substring(0, 6) % 20 -eq 0) { Write-Output ('VIDEO_G7_SCHEMA=APPLYING through=' + $migration.Name.Substring(0, 6)) }
    }
    Write-Output ('VIDEO_G7_SCHEMA=PASS count=' + $migrations.Count + ' last=' + $migrations[-1].Name)
    if ($Focus -eq 'capacity_ready') {
        # 模拟DDL在生成列成功、索引或触发器尚未完成时中断，再执行原migration验证可恢复重入。
        $partialSQL = 'ALTER TABLE ai_video_queue_admission_guard DROP CHECK chk_video_capacity_recovery; ALTER TABLE audit_logs DROP INDEX uk_video_capacity_ready_audit_event; DROP TRIGGER IF EXISTS trg_video_capacity_ready_audit_insert; DROP TRIGGER IF EXISTS trg_video_capacity_ready_audit_update; DROP TRIGGER IF EXISTS trg_video_capacity_ready_audit_delete;'
        $partialOutput = @(docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch -e $partialSQL 2>&1)
        if ($LASTEXITCODE -ne 0) { $partialOutput | Select-Object -Last 5; throw 'migration_failed' }
        $readyMigration = Join-Path $repoRoot 'server/migrations/000113_video_capacity_ready.up.sql'
        $retryOutput = @(Get-Content -LiteralPath $readyMigration -Raw -Encoding utf8 | docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch 2>&1)
        if ($LASTEXITCODE -ne 0) { $retryOutput | Select-Object -Last 5; throw 'migration_failed' }
        $retryShape = @(docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch --skip-column-names -e "SELECT (SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_video_queue_admission_guard' AND constraint_name='chk_video_capacity_recovery' AND constraint_type='CHECK'),(SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='audit_logs' AND index_name='uk_video_capacity_ready_audit_event'),(SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name IN ('trg_video_capacity_ready_audit_insert','trg_video_capacity_ready_audit_update','trg_video_capacity_ready_audit_delete'));" 2>&1)
        if ($LASTEXITCODE -ne 0 -or $retryShape.Count -ne 1 -or $retryShape[0].ToString().Trim() -notmatch '^1\s+1\s+3$') { throw 'migration_failed' }
        Write-Output 'VIDEO_G7_READY_MIGRATION_RETRY=PASS partial=columns_present_check_index_and_triggers_missing check=1 index=1 triggers=3'
    }
    if ($Focus -eq 'capacity_execution') {
        # 114替换旧计划CHECK和Trigger；模拟DROP已提交后的中断，重跑必须恢复完整约束。
        $partialSQL = 'ALTER TABLE ai_gateway_tasks DROP CHECK chk_video_submission_plan; DROP TRIGGER IF EXISTS trg_video_submission_plan_update; DROP TRIGGER IF EXISTS trg_video_submission_capacity_event_insert;'
        $partialOutput = @(docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch -e $partialSQL 2>&1)
        if ($LASTEXITCODE -ne 0) { $partialOutput | Select-Object -Last 5; throw 'migration_failed' }
        $capacityMigration = Join-Path $repoRoot 'server/migrations/000114_video_submission_capacity_epoch.up.sql'
        $retryOutput = @(Get-Content -LiteralPath $capacityMigration -Raw -Encoding utf8 | docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch 2>&1)
        if ($LASTEXITCODE -ne 0) { $retryOutput | Select-Object -Last 5; throw 'migration_failed' }
        $retryShape = @(docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch --skip-column-names -e "SELECT (SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_video_submission_plan' AND constraint_type='CHECK'),(SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name IN ('trg_video_submission_plan_update','trg_video_submission_capacity_event_insert'));" 2>&1)
        if ($LASTEXITCODE -ne 0 -or $retryShape.Count -ne 1 -or $retryShape[0].ToString().Trim() -notmatch '^1\s+2$') { throw 'migration_failed' }
        Write-Output 'VIDEO_G7_CAPACITY_EXECUTION_MIGRATION_RETRY=PASS partial=check_and_triggers_missing check=1 triggers=2'
        $sendPartialSQL = 'ALTER TABLE ai_gateway_tasks DROP CHECK chk_video_submission_send; DROP TRIGGER IF EXISTS trg_video_submission_send_insert; DROP TRIGGER IF EXISTS trg_video_submission_send_update; DROP TRIGGER IF EXISTS trg_video_submission_send_event_insert;'
        $sendPartialOutput = @(docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch -e $sendPartialSQL 2>&1)
        if ($LASTEXITCODE -ne 0) { $sendPartialOutput | Select-Object -Last 5; throw 'migration_failed' }
        $sendMigration = Join-Path $repoRoot 'server/migrations/000115_video_submission_send_permit.up.sql'
        $sendRetryOutput = @(Get-Content -LiteralPath $sendMigration -Raw -Encoding utf8 | docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch 2>&1)
        if ($LASTEXITCODE -ne 0) { $sendRetryOutput | Select-Object -Last 5; throw 'migration_failed' }
        $sendRetryShape = @(docker exec -i -e "MYSQL_PWD=$password" $container mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g5_contract --batch --skip-column-names -e "SELECT (SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema=DATABASE() AND table_name='ai_gateway_tasks' AND constraint_name='chk_video_submission_send' AND constraint_type='CHECK'),(SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name IN ('trg_video_submission_send_insert','trg_video_submission_send_update','trg_video_submission_send_event_insert'));" 2>&1)
        if ($LASTEXITCODE -ne 0 -or $sendRetryShape.Count -ne 1 -or $sendRetryShape[0].ToString().Trim() -notmatch '^1\s+3$') { throw 'migration_failed' }
        Write-Output 'VIDEO_G7_SEND_PERMIT_MIGRATION_RETRY=PASS partial=check_and_triggers_missing check=1 triggers=3'
    }
    $relayTestEnv = @()
    if ($Broker) {
        $brokerReady = $false
        for ($probe = 0; $probe -lt 45; $probe++) {
            $brokerState = docker inspect --format '{{.State.Status}}' $brokerID
            if ($LASTEXITCODE -ne 0 -or $brokerState -in @('exited','dead')) { throw 'isolated_broker_not_ready' }
            docker exec --user rabbitmq $brokerID rabbitmq-diagnostics -q check_running 2>$null | Out-Null
            if ($LASTEXITCODE -eq 0) { $brokerReady=$true; break }
            Start-Sleep -Seconds 1
        }
        if (-not $brokerReady) { throw 'isolated_broker_not_ready' }
        $brokerPortLine = docker port $brokerID 5672/tcp | Select-Object -First 1
        if ($LASTEXITCODE -ne 0 -or $brokerPortLine -notmatch '^127\.0\.0\.1:(\d+)$') { throw 'loopback_port_missing' }
        $brokerPort=$Matches[1]
        $env:MOLIN_VIDEO_G7_RELAY_AMQP_URL="amqp://vid_g7:${brokerPassword}@127.0.0.1:${brokerPort}/vid_g7"
        $env:MOLIN_VIDEO_G7_RELAY_ISOLATED='YES'
        $relayTestEnv=@('-e','MOLIN_VIDEO_G7_RELAY_ISOLATED=YES','-e',"MOLIN_VIDEO_G7_RELAY_AMQP_URL=amqp://vid_g7:${brokerPassword}@${brokerContainer}:5672/vid_g7")
        Write-Output 'VIDEO_G7_RELAY=BROKER_READY scope=temporary_mysql_and_rabbit'
    }
    if($Redis){$redisRunID='';for($probe=0;$probe -lt 30;$probe++){$info=@(docker exec -e "REDISCLI_AUTH=$redisPassword" $redisID redis-cli --no-auth-warning INFO server 2>&1);if($LASTEXITCODE -eq 0){$line=@($info|ForEach-Object{$_.ToString().Trim()}|Where-Object{$_ -match '^run_id:[0-9a-f]{40}$'});if($line.Count -eq 1){$redisRunID=$line[0].Substring(7);break}};Start-Sleep -Milliseconds 200};$redisPortLine=docker port $redisID 6379/tcp|Select-Object -First 1;if($redisRunID -notmatch '^[0-9a-f]{40}$' -or $redisPortLine -notmatch '^127\.0\.0\.1:(\d+)$'){throw 'isolated_redis_not_ready'};$redisPort=$Matches[1];$env:MOLIN_VIDEO_G7_REDIS_ISOLATED='YES';$env:MOLIN_VIDEO_G7_REDIS_ADDR="127.0.0.1:$redisPort";$env:MOLIN_VIDEO_G7_REDIS_PASSWORD=$redisPassword;$env:MOLIN_VIDEO_G7_REDIS_RUN_ID=$redisRunID;$relayTestEnv+=@('-e','MOLIN_VIDEO_G7_REDIS_ISOLATED=YES','-e',"MOLIN_VIDEO_G7_REDIS_ADDR=${redisContainer}:6379",'-e',"MOLIN_VIDEO_G7_REDIS_PASSWORD=$redisPassword",'-e',"MOLIN_VIDEO_G7_REDIS_RUN_ID=$redisRunID");Write-Output 'VIDEO_G7_REDIS=READY scope=temporary_mysql_and_redis'}
    $env:MOLIN_VIDEO_G5_MYSQL_DSN = "root:${password}@tcp(127.0.0.1:${port})/molin_video_g5_contract?charset=utf8mb4&parseTime=true&loc=UTC&timeout=5s&readTimeout=30s&writeTimeout=30s"
    $env:MOLIN_VIDEO_G5_ISOLATED = 'YES'
    Push-Location (Join-Path $repoRoot 'server')
    try {
        if ($LinuxRace) {
            $modCache = go env GOMODCACHE
            if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $modCache)) { throw 'module_cache_missing' }
            $createdBuilder = $true
            $testDSN = "root:${password}@tcp(${container}:3306)/molin_video_g5_contract?charset=utf8mb4&parseTime=true&loc=UTC&timeout=5s&readTimeout=30s&writeTimeout=30s"
            Write-Output 'VIDEO_G7_OUTBOX=TESTING mode=linux_race'
$output = @(docker run --pull=never --name $builder --label molin.goal=VID-G7 --network $network --read-only --mount "type=bind,src=$repoRoot,dst=/workspace,readonly" --mount "type=bind,src=$modCache,dst=/go/pkg/mod,readonly" --tmpfs '/tmp:rw,exec,nosuid,size=2g' -w /workspace/server -e GOCACHE=/tmp/go-build -e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local -e CGO_ENABLED=1 -e MOLIN_VIDEO_G5_ISOLATED=YES -e "MOLIN_VIDEO_G5_MYSQL_DSN=$testDSN" -e "MOLIN_VIDEO_G7_LEASE_MYSQL_SERVER_UUID=$leaseServerUUID" @relayTestEnv $goImage go test -race -json -count=1 "-timeout=$testTimeout" ./internal/modules/token_gateway/service -run $pattern 2>&1)
        } else {
            $output = @(go test -json -count=1 "-timeout=$testTimeout" ./internal/modules/token_gateway/service -run $pattern 2>&1)
        }
        $testExit = $LASTEXITCODE
    } finally { Pop-Location }
    if ((Get-TestSourceHash) -ne $sourceHash) { throw 'source_changed_during_test' }
    Write-Output ('VIDEO_G7_SOURCE=UNCHANGED sha256=' + $sourceHash)
    $events = @($output | ForEach-Object { try { $_.ToString() | ConvertFrom-Json -ErrorAction Stop } catch { } })
    $runs = @($events | Where-Object { $_.Action -eq 'run' -and $_.Test -in $required })
    $passes = @($events | Where-Object { $_.Action -eq 'pass' -and $_.Test -in $required })
    $bad = @($events | Where-Object { $_.Action -in @('fail','skip') })
    $complete = $true
    foreach ($test in $required) {
        if (@($runs | Where-Object { $_.Test -eq $test }).Count -ne 1 -or @($passes | Where-Object { $_.Test -eq $test }).Count -ne 1) { $complete = $false }
    }
    # 即使一项失败也保留精确顶层计数；部分通过不代表整个组合通过。
    Write-Output ('VIDEO_G7_OUTBOX_OBSERVED required=' + $required.Count + ' run=' + $runs.Count + ' pass=' + $passes.Count + ' bad_events=' + $bad.Count)
    if ($testExit -ne 0 -or -not $complete -or $bad.Count -ne 0) {
        # 仅打印失败/跳过测试的诊断和顶层结果，避免大量已通过JSON淹没真正反例。
        $badNames = @($bad | Where-Object { $_.Test } | ForEach-Object { $_.Test })
        $events | Where-Object { $_.Action -in @('fail','skip') -or ($_.Action -eq 'output' -and $_.Test -in $badNames) } | ForEach-Object { Write-Output (Protect-RelayOutput ($_ | ConvertTo-Json -Compress)) }
        if ($badNames.Count -eq 0) { $output | Select-Object -Last 10 | ForEach-Object { Write-Output (Protect-RelayOutput $_.ToString()) } }
        throw 'required_test_failed_or_skipped'
    }
    $passes | Where-Object { $_.Test -like 'TestVideoG7*' -or -not $FinanceRegression } | ForEach-Object { Write-Output ('VIDEO_G7_OUTBOX_TEST=PASS test=' + $_.Test + ' elapsed_seconds=' + $_.Elapsed) }
    # Go顶层并行测试耗时不包含等待子例的时间，单列四个真实租约到期子例，避免把0.01秒误当全部执行时间。
    $events | Where-Object { $_.Action -eq 'pass' -and $_.Test -like 'TestVideoG7WorkerFinancialTailMySQL/*' } | ForEach-Object { Write-Output ('VIDEO_G7_FINANCIAL_TAIL_CASE=PASS test=' + $_.Test + ' elapsed_seconds=' + $_.Elapsed) }
    $events | Where-Object { $_.Action -eq 'pass' -and $_.Test -like 'TestVideoG7WorkerCancel*MySQL/*' } | ForEach-Object { Write-Output ('VIDEO_G7_CANCEL_CASE=PASS test=' + $_.Test + ' elapsed_seconds=' + $_.Elapsed) }
    Write-Output ('VIDEO_G7_OUTBOX_COUNTS=PASS required=' + $required.Count + ' run=' + $runs.Count + ' pass=' + $passes.Count + ' skip=0')
    if ($FinanceRegression) { Write-Output ('VIDEO_G7_FINANCE=PASS required=' + $financeRequired.Count) }
    $resultCode = 0
} catch {
    $safeReasons = @('locked_image_missing','isolated_network_failed','isolated_container_failed','mysql_not_ready','mysql_exited_before_ready','loopback_port_missing','migrations_missing','migration_failed','required_test_failed_or_skipped','source_list_failed','source_changed_during_test','module_cache_missing','finance_test_discovery_failed','isolated_broker_create_failed','isolated_broker_start_failed','isolated_broker_not_ready','isolated_redis_create_failed','isolated_redis_start_failed','isolated_redis_not_ready','lease_target_identity_missing','submission_test_discovery_failed','isolated_group_failed')
    $reason = 'unexpected_local_runner_error'
    if ($_.Exception.Message -in $safeReasons) { $reason = $_.Exception.Message }
    Write-Output "VIDEO_G7_OUTBOX=FAIL reason=$reason"
} finally {
    $env:MOLIN_VIDEO_G5_MYSQL_DSN = $previousDSN
    $env:MOLIN_VIDEO_G5_ISOLATED = $previousApproval
    $env:MOLIN_VIDEO_G7_LEASE_MYSQL_SERVER_UUID = $previousLeaseUUID
    $env:MOLIN_VIDEO_G7_RELAY_AMQP_URL = $previousRelayURL
    $env:MOLIN_VIDEO_G7_RELAY_ISOLATED = $previousRelayApproval
    foreach($name in $redisVars){[Environment]::SetEnvironmentVariable($name,$previousRedis[$name],'Process')}
    $OutputEncoding = $previousEncoding
    # 仅清理本轮精确名称且标签匹配的资源；清理失败不得报告整体通过。
    if ($createdBuilder) {
        $owner = docker inspect --format '{{index .Config.Labels "molin.goal"}}' $builder
        if ($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7') {
            docker rm -f $builder | Out-Null
            if ($LASTEXITCODE -ne 0) { $resultCode = 2 }
        } elseif ($resultCode -eq 0) { $resultCode = 2 }
    }
    if ($createdContainer) {
        $owner = docker inspect --format '{{index .Config.Labels "molin.goal"}}' $container
        if ($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7') {
            docker rm -f $container | Out-Null
            if ($LASTEXITCODE -ne 0) { $resultCode = 2 }
        } elseif ($resultCode -eq 0) { $resultCode = 2 }
    }
    if ($brokerID) {
        $owner = docker inspect --format '{{index .Config.Labels "molin.goal"}}' $brokerID
        if ($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7') {
            docker rm -f $brokerID | Out-Null
            if ($LASTEXITCODE -ne 0) { $resultCode=2 }
        } elseif ($resultCode -eq 0) { $resultCode=2 }
    }
    if($redisID){$owner=docker inspect --format '{{index .Config.Labels "molin.goal"}}' $redisID;if($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7'){docker rm -f $redisID|Out-Null;if($LASTEXITCODE -ne 0){$resultCode=2}}elseif($resultCode -eq 0){$resultCode=2}}
    if ($createdNetwork) {
        $owner = docker network inspect --format '{{index .Labels "molin.goal"}}' $network
        if ($LASTEXITCODE -eq 0 -and $owner -eq 'VID-G7') {
            docker network rm $network | Out-Null
            if ($LASTEXITCODE -ne 0) { $resultCode = 2 }
        } elseif ($resultCode -eq 0) { $resultCode = 2 }
    }
}
if ($resultCode -eq 0) {
    Write-Output ('VIDEO_G7_SUITE=PASS groups=' + (1+$separateGroups) + ' required=' + $completeRequired + ' run=' + ($runs.Count+$separatePassed) + ' pass=' + ($passes.Count+$separatePassed) + ' skip=0 cleanup=PASS')
    Write-Output 'VIDEO_G7_OUTBOX=PASS scope=repository cleanup=PASS real_provider=0 real_wallet=0 test_server_writes=0'
}
exit $resultCode
