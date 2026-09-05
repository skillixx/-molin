param()

$ErrorActionPreference = 'Stop'
foreach ($approval in @('VIDEO_GATEWAY_G7_MYSQL_ISOLATED_APPROVED','VIDEO_GATEWAY_G7_REDIS_ISOLATED_APPROVED','VIDEO_GATEWAY_G7_RABBIT_ISOLATED_APPROVED','VIDEO_GATEWAY_G7_MINIO_ISOLATED_APPROVED')) {
    if ([Environment]::GetEnvironmentVariable($approval, 'Process') -ne 'YES') {
        Write-Output "VIDEO_G7_RUNTIME=APPROVAL_REQUIRED missing=$approval"
        exit 3
    }
}

$images = @{
    mysql='mysql@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b'
    redis='redis@sha256:e9b2e45ecd47fbb69b877cf8d045d5cccaaaed52524b6e098b4abe8212994f73'
    rabbit='rabbitmq@sha256:606d8c0d6b3c18d1da9afc53bc7cdb2a8d5486df91b5a9830e9e07626c9ae281'
    minio='minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e'
    go='golang@sha256:908f8ff2ec296df2f349563072c7925775cd28b50361a52ed834a8a37399b9bf'
    prometheus='prom/prometheus@sha256:69f5241418838263316593f7274a304b095c40bcf22e57272865da91bd60a8ac'
}
$suffix=[Guid]::NewGuid().ToString('N').Substring(0,12)
$network="molin-vidg7-runtime-net-$suffix"
$mysql="molin-vidg7-runtime-mysql-$suffix"
$redis="molin-vidg7-runtime-redis-$suffix"
$rabbit="molin-vidg7-runtime-rabbit-$suffix"
$minio="molin-vidg7-runtime-minio-$suffix"
$builder="molin-vidg7-runtime-go-$suffix"
$rollbackBuilder="molin-vidg7-runtime-go-rollback-$suffix"
$buildCache="molin-vidg7-runtime-cache-$suffix"
$mysqlPassword=[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$redisPassword=[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$rabbitPassword=[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$minioAccess='vidg7fakeaccess'
$minioSecret=[Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(24)).ToLowerInvariant()
$repoRoot=(Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$created=@()
$networkCreated=$false
$cacheCreated=$false
$resultCode=2

function Get-VideoRuntimeSourceHash {
    $paths=@(git -C $repoRoot ls-files --cached --others --exclude-standard -- server infra/scripts/verify-video-gateway-vid-g7-runtime.ps1 infra/prometheus/video-gateway-alerts.yml infra/grafana/dashboards/video-gateway-g7.json | Sort-Object -Unique)
    if($LASTEXITCODE -ne 0 -or $paths.Count -eq 0){throw 'source_list_failed'}
    $records=foreach($path in $paths){$path+'|'+(Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant()}
    return [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes(($records -join "`n")+"`n"))).ToLowerInvariant()
}

try {
    foreach($image in $images.Values){docker image inspect $image --format '{{.Id}}' | Out-Null;if($LASTEXITCODE -ne 0){throw 'locked_image_missing'}}
    $sourceHash=Get-VideoRuntimeSourceHash
    docker network create --internal --label molin.goal=VID-G7 $network | Out-Null
    if($LASTEXITCODE -ne 0){throw 'network_create_failed'}
    $networkCreated=$true
    docker volume create --label molin.goal=VID-G7 $buildCache | Out-Null
    if($LASTEXITCODE -ne 0){throw 'build_cache_create_failed'}
    $cacheCreated=$true
    docker run -d --pull=never --name $mysql --label molin.goal=VID-G7 --network $network --network-alias mysql --tmpfs '/var/lib/mysql:rw,noexec,nosuid,size=1g' -e "MYSQL_ROOT_PASSWORD=$mysqlPassword" -e MYSQL_ROOT_HOST=% -e MYSQL_DATABASE=molin_video_g6_contract $images.mysql --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci | Out-Null
    if($LASTEXITCODE -ne 0){throw 'mysql_create_failed'};$created+=$mysql
    docker run -d --pull=never --name $redis --label molin.goal=VID-G7 --network $network --network-alias redis --tmpfs '/data:rw,noexec,nosuid,size=256m' $images.redis redis-server --save '' --appendonly no --requirepass $redisPassword | Out-Null
    if($LASTEXITCODE -ne 0){throw 'redis_create_failed'};$created+=$redis
    docker run -d --pull=never --name $rabbit --label molin.goal=VID-G7 --network $network --network-alias rabbit --tmpfs '/var/lib/rabbitmq:rw,noexec,nosuid,size=512m' -e RABBITMQ_DEFAULT_USER=vidg7fake -e "RABBITMQ_DEFAULT_PASS=$rabbitPassword" $images.rabbit | Out-Null
    if($LASTEXITCODE -ne 0){throw 'rabbit_create_failed'};$created+=$rabbit
    docker run -d --pull=never --name $minio --label molin.goal=VID-G7 --network $network --network-alias minio --tmpfs '/data:rw,noexec,nosuid,size=1g' -e "MINIO_ROOT_USER=$minioAccess" -e "MINIO_ROOT_PASSWORD=$minioSecret" $images.minio server /data --console-address ':9001' | Out-Null
    if($LASTEXITCODE -ne 0){throw 'minio_create_failed'};$created+=$minio

    $ready=$false
    for($attempt=0;$attempt -lt 120;$attempt++){
        # 就绪轮询只读取有限日志，避免某个容器内CLI在启动期把单轮阻塞数十秒。
        $mysqlReady=@(docker logs --tail 80 $mysql 2>&1 | Where-Object {$_.ToString() -match 'ready for connections'}).Count -gt 0
        $redisReady=@(docker logs --tail 40 $redis 2>&1 | Where-Object {$_.ToString() -match 'Ready to accept connections'}).Count -gt 0
        $rabbitReady=@(docker logs --tail 120 $rabbit 2>&1 | Where-Object {$_.ToString() -match 'Ready to start client connection listeners|Server startup complete'}).Count -gt 0
        $minioReady=@(docker logs $minio 2>&1 | Where-Object {$_.ToString() -match 'API:'}).Count -gt 0
        if($mysqlReady -and $redisReady -and $rabbitReady -and $minioReady){$ready=$true;break}
        Start-Sleep -Milliseconds 500
    }
    if(-not $ready){throw 'dependencies_not_ready'}
    Write-Output 'VIDEO_G7_RUNTIME=DEPENDENCIES_READY network=internal host_ports=0'

    $migrations=@(Get-ChildItem -LiteralPath (Join-Path $repoRoot 'server/migrations') -Filter '*.up.sql' | Sort-Object Name)
    foreach($migration in $migrations){
        $output=@(Get-Content -Raw -Encoding utf8 -LiteralPath $migration.FullName | docker exec -i -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g6_contract --batch 2>&1)
        if($LASTEXITCODE -ne 0){$output|Select-Object -Last 5;throw 'migration_failed'}
    }
    Write-Output ('VIDEO_G7_RUNTIME=SCHEMA_PASS count='+$migrations.Count+' last='+$migrations[-1].Name)
    docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot -e 'CREATE DATABASE molin_image_g7_contract CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci' | Out-Null
    if($LASTEXITCODE -ne 0){throw 'image_schema_create_failed'}
    foreach($migration in $migrations){
        $imageOutput=@(Get-Content -Raw -Encoding utf8 -LiteralPath $migration.FullName | docker exec -i -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_image_g7_contract --batch 2>&1)
        if($LASTEXITCODE -ne 0){$imageOutput|Select-Object -Last 5;throw 'image_migration_failed'}
    }
    Write-Output ('VIDEO_G7_RUNTIME=IMAGE_SCHEMA_PASS count='+$migrations.Count)

    # 模拟119—121在部分Trigger创建后中断；原up必须能在两套schema重放并恢复完整约束。
    $partialDDL='DROP TRIGGER IF EXISTS trg_video_object_scan_cursor_insert; DROP TRIGGER IF EXISTS trg_video_upload_retention_fact_update; DROP TRIGGER IF EXISTS trg_video_output_retention_fact_delete;'
    $replayMigrations=@($migrations|Where-Object{[int]$_.Name.Substring(0,6) -ge 119 -and [int]$_.Name.Substring(0,6) -le 121})
    if($replayMigrations.Count -ne 3){throw 'retention_migration_set_failed'}
    foreach($database in @('molin_video_g6_contract','molin_image_g7_contract')){
        docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=$database --batch -e $partialDDL | Out-Null
        if($LASTEXITCODE -ne 0){throw 'retention_partial_ddl_failed'}
        foreach($migration in $replayMigrations){
            $replayOutput=@(Get-Content -Raw -Encoding utf8 -LiteralPath $migration.FullName | docker exec -i -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=$database --batch 2>&1)
            if($LASTEXITCODE -ne 0){$replayOutput|Select-Object -Last 5;throw 'retention_migration_replay_failed'}
        }
        $triggerShape=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=$database --batch --skip-column-names -e "SELECT SUM(trigger_name LIKE 'trg_video_object_scan_cursor_%'),SUM(trigger_name LIKE 'trg_video_upload_retention_fact_%'),SUM(trigger_name LIKE 'trg_video_output_retention_fact_%') FROM information_schema.triggers WHERE trigger_schema=DATABASE();" 2>&1)
        if($LASTEXITCODE -ne 0 -or $triggerShape.Count -ne 1 -or $triggerShape[0].ToString().Trim() -notmatch '^3\s+3\s+3$'){throw 'retention_trigger_recovery_failed'}
    }
    $invalidInitial=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g6_contract --batch -e "INSERT INTO ai_video_object_scan_cursors(scope_key,direction,last_numeric_id) VALUES('runner-invalid-initial','retention',9);" 2>&1)
    if($LASTEXITCODE -eq 0){throw 'cursor_invalid_initial_accepted'}
    docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g6_contract --batch -e "INSERT INTO ai_video_object_scan_cursors(scope_key,direction) VALUES('runner-cursor-guard','retention');" | Out-Null
    if($LASTEXITCODE -ne 0){throw 'cursor_guard_fixture_failed'}
    $invalidAdvance=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g6_contract --batch -e "UPDATE ai_video_object_scan_cursors SET version_no=version_no+1,last_numeric_id=0 WHERE scope_key='runner-cursor-guard';" 2>&1)
    if($LASTEXITCODE -eq 0){throw 'cursor_non_monotonic_update_accepted'}
    Write-Output 'VIDEO_G7_RUNTIME=RETENTION_MIGRATION_REPLAY_PASS migrations=119-121 schemas=2 triggers=3x3 invalid_initial=denied non_monotonic=denied'

    $fuseMigration=Join-Path $repoRoot 'server/migrations/000122_video_rabbit_poison_fuses.up.sql'
    foreach($database in @('molin_video_g6_contract','molin_image_g7_contract')){
        docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=$database --batch -e 'DROP TRIGGER trg_video_rabbit_fuse_update;' | Out-Null
        if($LASTEXITCODE -ne 0){throw 'fuse_trigger_drop_failed'}
        Get-Content -Raw -Encoding utf8 $fuseMigration | docker exec -i -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=$database --batch | Out-Null
        if($LASTEXITCODE -ne 0){throw 'fuse_migration_replay_failed'}
        $fuseShape=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=$database --batch --skip-column-names -e "SELECT COUNT(*),(SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name LIKE 'trg_video_rabbit_fuse_%') FROM ai_video_rabbit_poison_fuses;" 2>&1)
        if($LASTEXITCODE -ne 0 -or $fuseShape.Count -ne 1 -or $fuseShape[0].ToString().Trim() -notmatch '^3\s+3$'){throw 'fuse_migration_replay_shape_failed'}
    }
    $invalidFuseInsert=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g6_contract --batch -e "INSERT INTO ai_video_rabbit_poison_fuses(stage,status,version_no,updated_at) VALUES('other','ready',1,UTC_TIMESTAMP(6));" 2>&1)
    if($LASTEXITCODE -eq 0){throw 'fuse_invalid_insert_accepted'}
    $invalidFuseUpdate=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g6_contract --batch -e "UPDATE ai_video_rabbit_poison_fuses SET status='blocked',body_sha256=REPEAT('a',64),version_no=version_no+1,updated_at=UTC_TIMESTAMP(6) WHERE stage='submit';" 2>&1)
    if($LASTEXITCODE -eq 0){throw 'fuse_unbound_update_accepted'}
    $invalidFuseDelete=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g6_contract --batch -e "DELETE FROM ai_video_rabbit_poison_fuses WHERE stage='submit';" 2>&1)
    if($LASTEXITCODE -eq 0){throw 'fuse_delete_accepted'}
    Write-Output 'VIDEO_G7_RUNTIME=FUSE_MIGRATION_REPLAY_PASS migration=122 schemas=2 rows=3 triggers=3 invalid_insert=denied unbound_update=denied delete=denied'

    $modCache=(go env GOMODCACHE).Trim()
    if($LASTEXITCODE -ne 0 -or -not(Test-Path -LiteralPath $modCache)){throw 'module_cache_missing'}
    $dsn="root:${mysqlPassword}@tcp(mysql:3306)/molin_video_g6_contract?charset=utf8mb4&parseTime=true&loc=UTC&timeout=5s&readTimeout=30s&writeTimeout=30s"
    $imageDSN="root:${mysqlPassword}@tcp(mysql:3306)/molin_image_g7_contract?charset=utf8mb4&parseTime=true&loc=UTC&timeout=5s&readTimeout=30s&writeTimeout=30s"
    $testPattern='^(TestVideoG7(BootstrapClosedRuntimeMySQLRedisRabbitMinIO|ObjectScannerMySQLMinIO|ImportRetentionCursorSkipsProtectedPrefixMySQL|UploadSessionRetentionMySQL|OutputRetentionWorkerMySQL|AdminDLQRecoveryPermissionAuditMySQL|AdminDLQRecoveryMySQLRabbitUnknownWindows|RuntimePoisonStopsWithoutHotRestart|RuntimePersistentPoisonFenceBlocksStartup|RuntimeTransientFailureRetriesAndRecordsHealth|RuntimeShutdownTimeoutPreservesLifecycle|RuntimePoisonFenceMySQL)|TestAppRunContextClosesRuntimeWhenListenFails|TestNativeAsyncHTTPVideoAdapter.*|TestVideoG6ImportReadyRetentionMySQL|TestVideoMinIOImportStoreSeparatesSourceAndNormalizedTarget|TestImageG7(MinIOIntegration|RabbitMQTopologyAndDLQ|InfrastructureClosedLoop))$'
    Push-Location (Join-Path $repoRoot 'server')
    try{$listedTests=@(go test ./internal/bootstrap ./internal/modules/token_gateway ./internal/modules/token_gateway/service ./internal/modules/token_gateway/video ./internal/modules/token_gateway/image -list $testPattern 2>&1);$listExit=$LASTEXITCODE}finally{Pop-Location}
    $requiredTests=@($listedTests|ForEach-Object{$_.ToString().Trim()}|Where-Object{$_ -match '^Test' -and $_ -match $testPattern}|Sort-Object -Unique)
    if($listExit -ne 0 -or $requiredTests.Count -eq 0){throw 'runtime_test_discovery_failed'}
    Write-Output ('VIDEO_G7_RUNTIME=DISCOVERED required='+$requiredTests.Count)
    $arguments=@('run','--rm','--pull=never','--name',$builder,'--label','molin.goal=VID-G7','--network',$network,'--read-only','--mount',"type=bind,src=$repoRoot,dst=/src,readonly",'--mount',"type=bind,src=$modCache,dst=/go/pkg/mod,readonly",'--mount',"type=volume,src=$buildCache,dst=/go-cache",'--tmpfs','/tmp:rw,exec,nosuid,size=2g','-w','/src/server','-e','GOCACHE=/go-cache','-e','GOPROXY=off','-e','GOSUMDB=off','-e','GOTOOLCHAIN=local','-e','CGO_ENABLED=1','-e','MOLIN_VIDEO_G7_RUNTIME_ISOLATED=YES','-e',"MOLIN_VIDEO_G7_RUNTIME_MYSQL_DSN=$dsn",'-e','MOLIN_VIDEO_G6_ISOLATED=YES','-e',"MOLIN_VIDEO_G6_MYSQL_DSN=$dsn",'-e','MOLIN_IMAGE_G7_ISOLATED=YES','-e',"MOLIN_IMAGE_G7_MYSQL_DSN=$imageDSN",'-e',"MOLIN_IMAGE_G7_RABBIT_URL=amqp://vidg7fake:${rabbitPassword}@rabbit:5672/",'-e','MOLIN_IMAGE_G7_MINIO_ENDPOINT=minio:9000','-e',"MOLIN_IMAGE_G7_MINIO_ACCESS=$minioAccess",'-e',"MOLIN_IMAGE_G7_MINIO_SECRET=$minioSecret",'-e',"MOLIN_VIDEO_G7_RUNTIME_REDIS_PASSWORD=$redisPassword",'-e',"MOLIN_VIDEO_G7_RUNTIME_RABBIT_PASSWORD=$rabbitPassword",'-e','MOLIN_VIDEO_G7_RUNTIME_MINIO_ENDPOINT=minio:9000','-e',"MOLIN_VIDEO_G7_RUNTIME_MINIO_ACCESS=$minioAccess",'-e',"MOLIN_VIDEO_G7_RUNTIME_MINIO_SECRET=$minioSecret",$images.go,'go','test','-race','-json','-count=1','-p=1','-timeout=180s','./internal/bootstrap','./internal/modules/token_gateway','./internal/modules/token_gateway/service','./internal/modules/token_gateway/video','./internal/modules/token_gateway/image','-run',$testPattern)
    $events=@(& docker @arguments 2>&1)
    $testExit=$LASTEXITCODE
    $safe=@($events|ForEach-Object{$_.ToString().Replace($mysqlPassword,'[REDACTED]').Replace($redisPassword,'[REDACTED]').Replace($rabbitPassword,'[REDACTED]').Replace($minioSecret,'[REDACTED]')})
    $parsedEvents=@($safe|ForEach-Object{try{$_|ConvertFrom-Json -ErrorAction Stop}catch{}})
    $run=@($parsedEvents|Where-Object{$_.Action -eq 'run' -and $_.Test -in $requiredTests}).Count
    $pass=@($parsedEvents|Where-Object{$_.Action -eq 'pass' -and $_.Test -in $requiredTests}).Count
    $skip=@($parsedEvents|Where-Object{$_.Action -eq 'skip'}).Count
    $fail=@($parsedEvents|Where-Object{$_.Action -eq 'fail'}).Count
    Write-Output ('VIDEO_G7_RUNTIME=OBSERVED required='+$requiredTests.Count+' run='+$run+' pass='+$pass+' skip='+$skip+' exit='+$testExit)
    if($testExit -ne 0 -or $run -ne $requiredTests.Count -or $pass -ne $requiredTests.Count -or $skip -ne 0 -or $fail -ne 0){$safe|Select-Object -Last 260|Write-Output;throw 'runtime_test_failed'}
    $rulePath=(Resolve-Path (Join-Path $repoRoot 'infra/prometheus/video-gateway-alerts.yml')).Path
    docker run --rm --pull=never --network none --mount "type=bind,src=$rulePath,dst=/rules/video.yml,readonly" --entrypoint /bin/promtool $images.prometheus check rules /rules/video.yml | Out-Null
    if($LASTEXITCODE -ne 0){throw 'prometheus_rule_failed'}
    $dashboard=Get-Content -Raw -Encoding utf8 -LiteralPath (Join-Path $repoRoot 'infra/grafana/dashboards/video-gateway-g7.json') | ConvertFrom-Json
    if($dashboard.uid -ne 'molin-video-gateway-g7' -or $dashboard.panels.Count -ne 8){throw 'grafana_dashboard_failed'}
    $factSQL="SELECT (SELECT COUNT(*) FROM ai_video_object_reconciliation_observations),(SELECT COUNT(*) FROM ai_compensation_tasks WHERE task_type IN ('video_object_missing_reconcile','video_orphan_cleanup')),(SELECT capacity_epoch FROM ai_video_queue_admission_guard WHERE id=1),(SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_gateway_tasks' AND column_name IN ('worker_lease_owner','submission_capacity_epoch','submission_send_token_hash')),(SELECT COUNT(*) FROM ai_video_input_deletion_requests WHERE request_kind='retention'),(SELECT COUNT(*) FROM ai_video_input_cleanup_facts),(SELECT COUNT(*) FROM ai_video_object_scan_cursors),(SELECT COUNT(*) FROM ai_video_upload_session_retention_facts),(SELECT COUNT(*) FROM ai_video_output_retention_facts),(SELECT COUNT(*) FROM ai_gateway_tasks WHERE status='queued'),(SELECT COUNT(*) FROM ai_gateway_tasks WHERE status='pending_reconcile'),(SELECT COUNT(*) FROM wallet_holds h JOIN ai_request_wallet_links l ON l.wallet_hold_id=h.id JOIN ai_requests r ON r.request_id=l.request_id WHERE h.status='holding' AND r.capability='video.generate'),(SELECT COUNT(*) FROM ai_gateway_task_events WHERE event_type IN ('video_submission_planned','video_submission_send_claimed')),(SELECT COUNT(*) FROM ai_video_rabbit_poison_fuses WHERE status='ready');"
    $beforeRollback=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g6_contract --batch --skip-column-names -e $factSQL 2>&1)
    if($LASTEXITCODE -ne 0 -or $beforeRollback.Count -ne 1){$beforeRollback|Select-Object -Last 5|Write-Output;throw 'rollback_snapshot_failed'}
    $inflightFields=@($beforeRollback[0].ToString().Trim() -split "`t")
    if($inflightFields.Count -ne 14 -or [int64]$inflightFields[9] -lt 1 -or [int64]$inflightFields[10] -lt 1 -or [int64]$inflightFields[11] -lt 2 -or [int64]$inflightFields[12] -lt 2 -or [int64]$inflightFields[13] -ne 3){throw 'rollback_inflight_fixture_missing'}
$downMigrations=@(Get-ChildItem -LiteralPath (Join-Path $repoRoot 'server/migrations') -Filter '*.down.sql' | Where-Object {[int]$_.Name.Substring(0,6) -ge 110 -and [int]$_.Name.Substring(0,6) -le 122} | Sort-Object Name -Descending)
if($downMigrations.Count -ne 13){throw 'rollback_migration_set_failed'}
    foreach($migration in $downMigrations){
        $downOutput=@(Get-Content -Raw -Encoding utf8 -LiteralPath $migration.FullName | docker exec -i -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --default-character-set=utf8mb4 --database=molin_video_g6_contract --batch 2>&1)
        if($LASTEXITCODE -ne 0){$downOutput|Select-Object -Last 5;throw 'rollback_migration_failed'}
    }
    $afterRollback=@(docker exec -e "MYSQL_PWD=$mysqlPassword" $mysql mysql --no-defaults --protocol=tcp '--host=127.0.0.1' -uroot --database=molin_video_g6_contract --batch --skip-column-names -e $factSQL 2>&1)
    if($LASTEXITCODE -ne 0 -or $afterRollback.Count -ne 1 -or $afterRollback[0].ToString().Trim() -ne $beforeRollback[0].ToString().Trim()){throw 'rollback_facts_changed'}
    $rollbackArguments=@('run','--rm','--pull=never','--name',$rollbackBuilder,'--label','molin.goal=VID-G7','--network',$network,'--read-only','--mount',"type=bind,src=$repoRoot,dst=/src,readonly",'--mount',"type=bind,src=$modCache,dst=/go/pkg/mod,readonly",'--mount',"type=volume,src=$buildCache,dst=/go-cache",'--tmpfs','/tmp:rw,exec,nosuid,size=2g','-w','/src/server','-e','GOCACHE=/go-cache','-e','GOPROXY=off','-e','GOSUMDB=off','-e','GOTOOLCHAIN=local','-e','CGO_ENABLED=1','-e','MOLIN_VIDEO_G7_RUNTIME_ISOLATED=YES','-e',"MOLIN_VIDEO_G7_RUNTIME_MYSQL_DSN=$dsn",'-e',"MOLIN_VIDEO_G7_RUNTIME_REDIS_PASSWORD=$redisPassword",'-e',"MOLIN_VIDEO_G7_RUNTIME_RABBIT_PASSWORD=$rabbitPassword",'-e','MOLIN_VIDEO_G7_RUNTIME_MINIO_ENDPOINT=minio:9000','-e',"MOLIN_VIDEO_G7_RUNTIME_MINIO_ACCESS=$minioAccess",'-e',"MOLIN_VIDEO_G7_RUNTIME_MINIO_SECRET=$minioSecret",$images.go,'go','test','-race','-json','-count=1','-p=1','-timeout=180s','./internal/bootstrap','-run','^TestVideoG7BootstrapClosedRuntimeMySQLRedisRabbitMinIO$')
    $rollbackEvents=@(& docker @rollbackArguments 2>&1)
    $rollbackExit=$LASTEXITCODE
    $rollbackSafe=@($rollbackEvents|ForEach-Object{$_.ToString().Replace($mysqlPassword,'[REDACTED]').Replace($redisPassword,'[REDACTED]').Replace($rabbitPassword,'[REDACTED]').Replace($minioSecret,'[REDACTED]')})
    $rollbackPass=@($rollbackSafe|Where-Object{$_ -match '"Action":"pass"' -and $_ -match '"Test":"TestVideoG7BootstrapClosedRuntimeMySQLRedisRabbitMinIO"'}).Count
    if($rollbackExit -ne 0 -or $rollbackPass -ne 1){$rollbackSafe|Select-Object -Last 100|Write-Output;throw 'rollback_runtime_failed'}
    if((Get-VideoRuntimeSourceHash) -ne $sourceHash){throw 'source_changed_during_test'}
    Write-Output "VIDEO_G7_RUNTIME_SOURCE=UNCHANGED sha256=$sourceHash"
    Write-Output ('VIDEO_G7_RUNTIME_TEST=PASS required='+$requiredTests.Count+' run='+$run+' pass='+$pass+' skip=0 routes_disabled=404 traffic_closed=503 workers=2x3 capacity_recovery=ready object_scan=bidirectional_paginated_restart_safe upload_session_retention=24h_tombstone_fact upload_and_import_retention=fair_restart_safe output_retention=parent_child_fair_restart_safe image_gateway_regression=3 native_http=runware_fake_ack_recovery_cross_process_refresh prometheus_rules=10 grafana_panels=8 provider=fake')
Write-Output 'VIDEO_G7_ROLLBACK=PASS down_migrations=13 facts_preserved=true closed_runtime_restart=pass'
    $resultCode=0
} catch {
    $reason=$_.Exception.Message
    Write-Output "VIDEO_G7_RUNTIME=FAIL reason=$reason"
} finally {
    # 任一目标不存在不能阻断其余清理；存在目标的删除退出码和最终三类资源零遗留都必须验证。
    $cleanupFailed=$false
    $cleanupTargets=@($builder,$rollbackBuilder,$mysql,$redis,$rabbit,$minio)+@($created)
    foreach($name in $cleanupTargets){
        docker container inspect $name *> $null
        if($LASTEXITCODE -eq 0){docker rm -f $name 2>$null | Out-Null;if($LASTEXITCODE -ne 0){$cleanupFailed=$true}}
    }
    if($networkCreated){docker network inspect $network *> $null;if($LASTEXITCODE -eq 0){docker network rm $network 2>$null | Out-Null;if($LASTEXITCODE -ne 0){$cleanupFailed=$true}}}
    if($cacheCreated){docker volume inspect $buildCache *> $null;if($LASTEXITCODE -eq 0){docker volume rm $buildCache 2>$null | Out-Null;if($LASTEXITCODE -ne 0){$cleanupFailed=$true}}}
    $leftovers=@(docker ps -a --filter "name=molin-vidg7-runtime-" --format '{{.Names}}' | Where-Object {$_ -in $cleanupTargets})
    docker network inspect $network *> $null
    $networkLeft=$LASTEXITCODE -eq 0
    docker volume inspect $buildCache *> $null
    $volumeLeft=$LASTEXITCODE -eq 0
    if($cleanupFailed -or $leftovers.Count -ne 0 -or $networkLeft -or $volumeLeft){Write-Output 'VIDEO_G7_RUNTIME=CLEANUP_FAILED';$resultCode=2}
}
if($resultCode -eq 0){Write-Output 'VIDEO_G7_RUNTIME=PASS cleanup=PASS real_provider=0 real_wallet=0 test_server_writes=0'}
exit $resultCode
