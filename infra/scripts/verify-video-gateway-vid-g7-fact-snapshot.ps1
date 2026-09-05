param()

$ErrorActionPreference = 'Stop'
if ($env:VIDEO_GATEWAY_G7_MYSQL_ISOLATED_APPROVED -ne 'YES') {
    Write-Output 'VIDEO_G7_FACT_SNAPSHOT=APPROVAL_REQUIRED'
    exit 3
}
$image = 'mysql@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b'
$goImage = 'golang@sha256:908f8ff2ec296df2f349563072c7925775cd28b50361a52ed834a8a37399b9bf'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path

function Get-FactSourceHash {
    $paths = @(git -C $repoRoot ls-files --cached --others --exclude-standard -- infra/scripts/video-gateway-vid-g7-fact-snapshot.sh infra/scripts/verify-video-gateway-vid-g7-fact-snapshot.ps1 server | Sort-Object -Unique)
    $records = foreach ($path in $paths) { $path + '|' + (Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant() }
    return [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes(($records -join "`n") + "`n"))).ToLowerInvariant()
}

function Invoke-FactGroup([ValidateSet('restore','expand_down')][string]$Group) {
    $suffix = [Guid]::NewGuid().ToString('N').Substring(0,10)
    $network = "molin-vidg7-fact-$Group-net-$suffix"
    $mysql = "molin-vidg7-fact-$Group-mysql-$suffix"
    $volume = "molin-vidg7-fact-$Group-volume-$suffix"
    $password = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(20)).ToLowerInvariant()
    $database = 'molin_video_g6_contract'
    $passed = $false
    function Invoke-Snapshot([string]$Database, [string]$Output, [ValidateSet('base','expanded')][string]$Mode, [string]$Manifest = '') {
        $arguments = @('run','--rm','--pull=never','--network',$network,'--read-only','--mount',"type=bind,src=$repoRoot,dst=/src,readonly",'--mount',"type=volume,src=$volume,dst=/evidence",'-e','MYSQL_HOST=mysql','-e','MYSQL_PORT=3306','-e','MYSQL_USER=root','-e',"MYSQL_PASSWORD=$password",'-e',"MYSQL_PWD=$password",$image,'bash','/src/infra/scripts/video-gateway-vid-g7-fact-snapshot.sh',$Database,$Output,$Mode)
        if ($Manifest) { $arguments += $Manifest }
        & docker @arguments | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'snapshot_failed' }
    }
    function Get-VolumeHash([string]$Path) {
        $line = docker run --rm --pull=never --network none --mount "type=volume,src=$volume,dst=/evidence,readonly" $image sha256sum $Path
        if ($LASTEXITCODE -ne 0) { throw 'snapshot_hash_failed' }
        return ($line -split '\s+')[0]
    }
    function Add-RepresentativeFacts {
        $modCache = (go env GOMODCACHE).Trim()
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $modCache)) { throw 'module_cache_missing' }
        $dsn = "root:${password}@tcp(mysql:3306)/${database}?charset=utf8mb4&parseTime=true&loc=UTC&timeout=5s&readTimeout=30s&writeTimeout=30s"
        # 四个种子全部复用G6隔离合同：I2V保留Quote/Hold/TaskInput，T2V保留Usage/输出资产，
        # 另两项分别留下Provider回调与管理员审计，避免旧夹具迁移不可变对象位置。
        $pattern = '^(TestVideoG6I2VMySQLRightsQuoteHold|TestVideoG6AssetSaveHTTPMySQL|TestVideoG6CallbackHTTPMySQL|TestVideoG6AdminCancelAuditReadRetryMySQL)$'
        $events = @(docker run --rm --pull=never --network $network --read-only --mount "type=bind,src=$repoRoot,dst=/src,readonly" --mount "type=bind,src=$modCache,dst=/go/pkg/mod,readonly" --tmpfs '/tmp:rw,exec,nosuid,size=2g' -w /src/server -e GOCACHE=/tmp/go-build -e GOPROXY=off -e GOSUMDB=off -e GOTOOLCHAIN=local -e CGO_ENABLED=1 -e MOLIN_VIDEO_G6_ISOLATED=YES -e "MOLIN_VIDEO_G6_MYSQL_DSN=$dsn" $goImage go test -json -count=1 -timeout=180s ./internal/modules/token_gateway/service -run $pattern 2>&1)
        if ($LASTEXITCODE -ne 0) {
            $events | Select-Object -Last 120 | ForEach-Object { Write-Output ($_.ToString().Replace($password, '[REDACTED]')) }
            throw 'representative_seed_failed'
        }
        $parsed = @($events | ForEach-Object { try { $_.ToString() | ConvertFrom-Json -ErrorAction Stop } catch {} })
        $run = @($parsed | Where-Object { $_.Action -eq 'run' -and $_.Test -match $pattern }).Count
        $pass = @($parsed | Where-Object { $_.Action -eq 'pass' -and $_.Test -match $pattern }).Count
        $skip = @($parsed | Where-Object { $_.Action -eq 'skip' }).Count
        if ($run -ne 4 -or $pass -ne 4 -or $skip -ne 0) { throw 'representative_seed_count_failed' }
        $factSQL = "SELECT (SELECT COUNT(*) FROM ai_requests WHERE capability='video.generate'),(SELECT COUNT(*) FROM ai_gateway_quotes WHERE capability='video.generate'),(SELECT COUNT(*) FROM ai_gateway_tasks WHERE capability='video.generate'),(SELECT COUNT(*) FROM ai_gateway_task_inputs),(SELECT COUNT(*) FROM ai_gateway_task_payloads),(SELECT COUNT(*) FROM ai_gateway_provider_callback_events),(SELECT COUNT(*) FROM ai_usage_items u JOIN ai_requests r ON r.request_id=u.request_id WHERE r.capability='video.generate'),(SELECT COUNT(*) FROM ai_gateway_assets a JOIN ai_gateway_tasks t ON t.id=a.task_id WHERE t.capability='video.generate'),(SELECT COUNT(*) FROM ai_gateway_task_events e JOIN ai_gateway_tasks t ON t.id=e.task_id WHERE t.capability='video.generate'),(SELECT COUNT(*) FROM ai_outbox_events WHERE aggregate_type='video_request'),(SELECT COUNT(*) FROM wallet_holds h JOIN ai_request_wallet_links l ON l.wallet_hold_id=h.id JOIN ai_requests r ON r.request_id=l.request_id WHERE r.capability='video.generate'),(SELECT COUNT(*) FROM ai_request_wallet_links l JOIN ai_requests r ON r.request_id=l.request_id WHERE r.capability='video.generate'),(SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'video_%' OR target_type LIKE 'video_%'),(SELECT COUNT(DISTINCT operation) FROM ai_gateway_tasks WHERE capability='video.generate' AND operation IN ('text_to_video','image_to_video'));"
        $shape = @(docker exec -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot --database=$database --batch --skip-column-names -e $factSQL)
        if ($LASTEXITCODE -ne 0 -or $shape.Count -ne 1) { throw 'representative_fact_query_failed' }
        $counts = @($shape[0].ToString().Trim() -split "`t" | ForEach-Object { [int64]$_ })
        if ($counts.Count -ne 14 -or @($counts[0..12] | Where-Object { $_ -lt 1 }).Count -ne 0 -or $counts[13] -ne 2) { throw 'representative_facts_incomplete' }
    }
    try {
        docker network create --internal --label molin.goal=VID-G7 $network | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'network_create_failed' }
        docker volume create --label molin.goal=VID-G7 $volume | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'volume_create_failed' }
        docker run -d --pull=never --name $mysql --network $network --network-alias mysql --label molin.goal=VID-G7 --tmpfs '/var/lib/mysql:rw,noexec,nosuid,size=1g' -e "MYSQL_ROOT_PASSWORD=$password" -e MYSQL_ROOT_HOST=% -e "MYSQL_DATABASE=$database" $image --character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'mysql_create_failed' }
        $ready = $false
        for ($attempt = 0; $attempt -lt 90; $attempt++) {
            docker exec -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot -e 'SELECT 1' 2>$null | Out-Null
            if ($LASTEXITCODE -eq 0) { $ready = $true; break }
            Start-Sleep -Seconds 1
        }
        if (-not $ready) { throw 'mysql_not_ready' }
        $pre = @(Get-ChildItem (Join-Path $repoRoot 'server/migrations') -Filter '*.up.sql' | Where-Object { [int]$_.Name.Substring(0,6) -le 109 } | Sort-Object Name)
        foreach ($migration in $pre) {
            Get-Content -Raw -Encoding utf8 $migration.FullName | docker exec -i -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot --database=$database --batch | Out-Null
            if ($LASTEXITCODE -ne 0) { throw 'migration_failed' }
        }
        # 当前G7执行使用共享私有Bucket并读取对象缺失围栏；先安装116/117两个官方expand-only兼容围栏，
        # 再经真实G6服务生成事实，避免手写SQL伪造TaskInput或绕过生产校验。
        foreach ($runtimeFenceName in @('000116_video_shared_minio_buckets.up.sql', '000117_video_object_reconciliation_observations.up.sql')) {
            $runtimeFence = Join-Path $repoRoot "server/migrations/$runtimeFenceName"
            Get-Content -Raw -Encoding utf8 $runtimeFence | docker exec -i -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot --database=$database --batch | Out-Null
            if ($LASTEXITCODE -ne 0) { throw 'runtime_fence_migration_failed' }
        }
        Add-RepresentativeFacts
        Invoke-Snapshot $database /evidence/pre.txt base
        if ($Group -eq 'restore') {
            # 损坏manifest即使带COMMIT/DDL也必须在连接数据库前由精确白名单拒绝。
            docker run --rm --pull=never --network none --mount "type=volume,src=$volume,dst=/evidence" $image sh -c "sed '1s/|1=1|/|1=1; COMMIT; DROP TABLE wallets|/' /evidence/pre.txt.columns > /evidence/malicious.columns" | Out-Null
            if ($LASTEXITCODE -ne 0) { throw 'malicious_manifest_fixture_failed' }
            $manifestRejected = $false
            try {
                Invoke-Snapshot $database /evidence/malicious.txt base /evidence/malicious.columns
            } catch {
                if ($_.Exception.Message -ne 'snapshot_failed') { throw }
                $manifestRejected = $true
            }
            if (-not $manifestRejected) { throw 'malicious_manifest_accepted' }
            $walletTable = docker exec -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot --database=$database --batch --skip-column-names -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='wallets';"
            if ($LASTEXITCODE -ne 0 -or @($walletTable)[-1].ToString().Trim() -ne '1') { throw 'malicious_manifest_changed_schema' }
            docker exec -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot -e 'CREATE DATABASE restored CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci' | Out-Null
            if ($LASTEXITCODE -ne 0) { throw 'restore_database_failed' }
            # Prompt密文、nonce等二进制列必须使用十六进制导出，避免宿主文本管道改变字节并破坏完整性约束。
            $dump = @(docker exec -e "MYSQL_PWD=$password" $mysql mysqldump --protocol=tcp --host=127.0.0.1 -uroot --single-transaction --skip-lock-tables --hex-blob --routines --triggers --set-gtid-purged=OFF --no-tablespaces $database)
            if ($LASTEXITCODE -ne 0) { throw 'dump_failed' }
            $dump | docker exec -i -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot --database=restored --batch | Out-Null
            if ($LASTEXITCODE -ne 0) { throw 'restore_failed' }
            Invoke-Snapshot restored /evidence/restored.txt base /evidence/pre.txt.columns
            if ((Get-VolumeHash /evidence/pre.txt) -ne (Get-VolumeHash /evidence/restored.txt)) { throw 'restore_facts_changed' }
            $tamper = docker exec -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot --database=restored --batch --skip-column-names -e "UPDATE wallets SET balance_amount=balance_amount+0.01 WHERE id=(SELECT wallet_id FROM (SELECT h.wallet_id FROM wallet_holds h JOIN ai_request_wallet_links l ON l.wallet_hold_id=h.id JOIN ai_requests r ON r.request_id=l.request_id WHERE r.capability='video.generate' ORDER BY h.id LIMIT 1) selected_wallet); SELECT ROW_COUNT();"
            if ($LASTEXITCODE -ne 0 -or @($tamper)[-1].ToString().Trim() -ne '1') { throw 'sensitivity_tamper_failed' }
            Invoke-Snapshot restored /evidence/restored-tampered.txt base /evidence/pre.txt.columns
            if ((Get-VolumeHash /evidence/pre.txt) -eq (Get-VolumeHash /evidence/restored-tampered.txt)) { throw 'sensitivity_digest_unchanged' }
        } else {
            $later = @(Get-ChildItem (Join-Path $repoRoot 'server/migrations') -Filter '*.up.sql' | Where-Object { [int]$_.Name.Substring(0,6) -ge 110 -and [int]$_.Name.Substring(0,6) -le 122 } | Sort-Object Name)
            foreach ($migration in $later) {
                Get-Content -Raw -Encoding utf8 $migration.FullName | docker exec -i -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot --database=$database --batch | Out-Null
                if ($LASTEXITCODE -ne 0) { throw 'migration_failed' }
            }
            Invoke-Snapshot $database /evidence/post-base.txt base /evidence/pre.txt.columns
            if ((Get-VolumeHash /evidence/pre.txt) -ne (Get-VolumeHash /evidence/post-base.txt)) { throw 'base_facts_changed' }
            Invoke-Snapshot $database /evidence/expanded.txt expanded
            $downs = @(Get-ChildItem (Join-Path $repoRoot 'server/migrations') -Filter '*.down.sql' | Where-Object { [int]$_.Name.Substring(0,6) -ge 110 -and [int]$_.Name.Substring(0,6) -le 122 } | Sort-Object Name -Descending)
            foreach ($migration in $downs) {
                Get-Content -Raw -Encoding utf8 $migration.FullName | docker exec -i -e "MYSQL_PWD=$password" $mysql mysql --protocol=tcp --host=127.0.0.1 -uroot --database=$database --batch | Out-Null
                if ($LASTEXITCODE -ne 0) { throw 'rollback_failed' }
            }
            Invoke-Snapshot $database /evidence/post-rollback.txt expanded /evidence/expanded.txt.columns
            if ((Get-VolumeHash /evidence/expanded.txt) -ne (Get-VolumeHash /evidence/post-rollback.txt)) { throw 'rollback_facts_changed' }
        }
        $passed = $true
    } finally {
        foreach ($name in @($mysql)) { docker rm -f $name 2>$null | Out-Null }
        docker network rm $network 2>$null | Out-Null
        docker volume rm $volume 2>$null | Out-Null
        $left = 0
        docker container inspect $mysql *> $null; if ($LASTEXITCODE -eq 0) { $left++ }
        docker network inspect $network *> $null; if ($LASTEXITCODE -eq 0) { $left++ }
        docker volume inspect $volume *> $null; if ($LASTEXITCODE -eq 0) { $left++ }
        if ($left -ne 0) { throw 'cleanup_failed' }
    }
    if (-not $passed) { throw 'group_failed' }
    Write-Output "VIDEO_G7_FACT_GROUP=PASS name=$Group cleanup=PASS"
}

$resultCode = 2
try {
    docker image inspect $image --format '{{.Id}}' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'locked_image_missing' }
    docker image inspect $goImage --format '{{.Id}}' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'locked_go_image_missing' }
    $sourceHash = Get-FactSourceHash
    Invoke-FactGroup restore
    Invoke-FactGroup expand_down
    if ((Get-FactSourceHash) -ne $sourceHash) { throw 'source_changed_during_test' }
    Write-Output "VIDEO_G7_FACT_SOURCE=UNCHANGED sha256=$sourceHash"
    Write-Output 'VIDEO_G7_FACT_TEST=PASS required=2 run=2 pass=2 skip=0 seed_tests=4x2 representative_facts=13x_nonempty operations=2 malicious_manifest=rejected sensitivity_tamper=detected cleanup=PASS host_ports=0 single_rr=true low_sensitive_digest=true'
    $resultCode = 0
} catch {
    Write-Output ('VIDEO_G7_FACT_TEST=FAIL reason=' + $_.Exception.Message)
}
exit $resultCode
