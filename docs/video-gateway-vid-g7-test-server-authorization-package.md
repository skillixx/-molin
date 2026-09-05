# VID-G7 测试服关闭态安装与实际回滚最小授权包

## 当前决定

- `TARGET_GOAL=VID-G7`
- `TEST_SERVER_AUTHORIZATION=NOT_GRANTED`
- `TEST_SERVER_WRITES=0`
- `TEST_SERVER_MIGRATIONS=0`
- `TEST_SERVER_DEPLOYMENTS=0`
- `TEST_SERVER_RESTARTS=0`
- `REAL_PROVIDER_REQUESTS=0`
- `REAL_PROVIDER_KEYS=0`
- `REAL_WALLET_WRITES=0`
- `PRODUCTION_OPERATIONS=0`

本文件只定义待授权动作，不授权执行。主机身份、现有运行态和备份落点必须通过单独获批的只读预检取得；任何字段仍为`HUMAN_REQUIRED`时，不得安装、迁移、重启或回滚。

## 待锁定身份

| 项目 | 必须回填的精确值 | 当前状态 |
|---|---|---|
| 目标环境 | 明确的Molin共享测试环境名称 | `HUMAN_REQUIRED` |
| 主机身份 | 主机名、云实例ID、操作系统ID及SSH主机指纹 | `HUMAN_REQUIRED` |
| 当前API | 容器或systemd单元、PID、镜像ID、版本接口commit | `HUMAN_REQUIRED` |
| 当前数据库 | MySQL实例身份、库名、`@@server_uuid`及只读schema版本 | `HUMAN_REQUIRED` |
| 当前中间件 | Redis run_id、RabbitMQ节点/vhost、MinIO部署ID和Bucket策略摘要 | `HUMAN_REQUIRED` |
| 目标源码 | VID-G7最终提交和SOURCE_STATE_ID | `PENDING_SOURCE_FREEZE` |
| 目标镜像 | API镜像仓库、不可变digest及构建证明 | `PENDING_BUILD` |
| 备份落点 | 测试服外或独立受限卷中的精确路径、校验值和保留期 | `HUMAN_REQUIRED` |

## 固定依赖与部署影响面

本地验证使用以下不可变镜像摘要；测试服若复用其他版本，必须重新执行兼容门禁，不得把本地结果直接迁移：

- MySQL：`mysql@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b`
- Redis：`redis@sha256:e9b2e45ecd47fbb69b877cf8d045d5cccaaaed52524b6e098b4abe8212994f73`
- RabbitMQ：`rabbitmq@sha256:606d8c0d6b3c18d1da9afc53bc7cdb2a8d5486df91b5a9830e9e07626c9ae281`
- MinIO：`minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e`
- Go验证环境：`golang@sha256:908f8ff2ec296df2f349563072c7925775cd28b50361a52ed834a8a37399b9bf`
- Prometheus规则验证：`prom/prometheus@sha256:69f5241418838263316593f7274a304b095c40bcf22e57272865da91bd60a8ac`

预期影响范围：

- API：宿主机或容器内`8080`，安装期间必须保持视频三开关关闭。
- Redis：测试环境约定宿主机`16379`到容器`6379`；以只读预检实际值为准。
- RabbitMQ：测试环境约定宿主机`5673/15673`到容器`5672/15672`；以实际值为准。
- MinIO：测试环境约定宿主机`19000/19001`到容器`9000/9001`；以实际值为准。
- Prometheus：管理端只允许回环绑定；不得因为G7暴露公网管理端。
- 视频对象复用`ai-upload-temp`、`ai-result`、`ai-quarantine`及现有保存资产Bucket；不得创建平行公开Bucket。
- 不修改测试服前端`3000/3001`，不重建Bifrost视频数据面。

## 配置与Secret

关闭态安装必须保持：

```text
VIDEO_GATEWAY_ENABLED=false
VIDEO_GATEWAY_TRAFFIC_ENABLED=false
REAL_PROVIDER=false
VIDEO_GATEWAY_LOCAL_FAKE_TEST=false
BIFROST_VIDEO_DATA_PLANE=OFF
```

十类Secret只能使用仓库外绝对普通文件，Linux权限不宽于0600，禁止符号链接、硬链接、同用途复用和环境变量明文。关闭态不得读取这些文件；本次测试服安装禁止注入真实Provider Key。

## Migration与事实保留

目标新增范围为`000110`至`000122`。执行前必须：

1. 锁定`@@server_uuid`和当前schema版本。
2. 对受影响schema与业务表做一致性备份，并在独立位置计算SHA-256。
3. 记录在途视频Task、Quote、Hold、Usage、Outbox、Provider回调、资产、容量epoch和扫描游标计数。
4. 确认没有并行migration进程。

所有down均为Expand-only兼容撤回，只撤销应用装配意图，不DROP列、表、触发器或事实。禁止清空Redis、RabbitMQ、MinIO、钱包、任务、Outbox或审计记录来伪造回滚成功。

## 获批后的唯一执行序列

1. 只读预检并回填本文件全部`HUMAN_REQUIRED`字段。
2. 停止新视频流量；保持Chat/Image原基线可用。
3. 创建并验证数据库、配置和当前镜像恢复点。
4. 以三开关全false安装目标镜像；不启动视频路由和Worker。
5. 顺序应用110—122 migration，逐项核对schema和既有业务事实计数。
6. 验证`/api/health`、`/api/ready`、`/api/version`，视频路由为404，Chat/Image和钱包只读基线不变。
7. 不接收视频任务、不调用Provider、不创建真实Hold。
8. 执行应用回滚和13步Expand-only兼容撤回；复验14项事实、Chat/Image、钱包及关闭态重启。
9. 若任一步失败，停止后续动作，保留兼容Worker/回调接收器和全部事实，按已验证恢复点恢复应用。

## 授权精确文本

只有在主机身份、SOURCE_STATE_ID、目标提交、镜像digest和备份落点全部回填后，项目负责人才能发出：

```text
TEST_SERVER=AUTHORIZE_VID_G7_CLOSED_INSTALL_AND_ACTUAL_ROLLBACK
CHANGE_ID=<12至32位小写字母数字唯一值>
EXPECTED_HOST_FINGERPRINT=<精确值>
EXPECTED_MYSQL_SERVER_UUID=<精确值>
MYSQL_HOST=<宿主迁移连接地址>
MYSQL_PORT=<宿主迁移连接端口>
MYSQL_DATABASE=<精确库名>
MYSQL_USER=<精确受限用户>
MYSQL_PASSWORD_FILE=<仓库外绝对路径>
EXPECTED_MYSQL_PASSWORD_SHA256=<密码文件正文SHA-256>
API_MYSQL_HOST=<API容器内可达地址>
API_MYSQL_PORT=<API容器内可达端口>
MYSQL_CLIENT_IMAGE_DIGEST=mysql@sha256:<精确值>
ENV_FILE=<仓库外绝对路径>
EXPECTED_ENV_FILE_SHA256=<环境文件SHA-256>
SOURCE_COMMIT=<精确值>
SOURCE_STATE_ID=<精确值>
IMAGE_DIGEST=<精确值>
PREVIOUS_IMAGE_DIGEST=<精确值>
BACKUP_DIR=<仓库外精确备份目录>
REAL_PROVIDER_REQUESTS=0
REAL_PROVIDER_KEYS=0
REAL_WALLET_WRITES=0
PRODUCTION_OPERATIONS=0
```

任何泛化的“继续”“自动确认”或Git授权都不能替代上述精确授权。授权只覆盖一次关闭态安装、一次实际回滚及其必要验证；不覆盖真实Provider、真实钱包、生产或VID-G8。

## 参数化命令模板

以下命令只在精确授权后由测试服受限Shell执行。默认值故意失败关闭，不能直接复制后误写共享环境。

### 只读预检

```bash
set -euo pipefail
TARGET_ENV="${TARGET_ENV:-HUMAN_REQUIRED}"
EXPECTED_HOST_FINGERPRINT="${EXPECTED_HOST_FINGERPRINT:-HUMAN_REQUIRED}"
MYSQL_DATABASE="${MYSQL_DATABASE:-HUMAN_REQUIRED}"
for value in "$TARGET_ENV" "$EXPECTED_HOST_FINGERPRINT" "$MYSQL_DATABASE"; do
  test "$value" != HUMAN_REQUIRED
done
export MYSQL_DATABASE
test "$(ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub | awk '{print $2}')" = "$EXPECTED_HOST_FINGERPRINT"
hostnamectl --static
docker ps --no-trunc --format '{{.ID}} {{.Image}} {{.Names}} {{.Ports}}'
curl --fail --silent http://127.0.0.1:8080/api/health >/dev/null
curl --fail --silent http://127.0.0.1:8080/api/version
mysql --batch --skip-column-names "$MYSQL_DATABASE" -e 'SELECT @@server_uuid; SELECT MAX(version) FROM schema_migrations WHERE dirty=0;'
redis-cli -h 127.0.0.1 -p 16379 --no-auth-warning INFO server | sed -n 's/^run_id:/run_id:/p'
rabbitmq-diagnostics -q status
mc admin info local
mc anonymous get local/ai-upload-temp
mc anonymous get local/ai-result
mc anonymous get local/ai-quarantine
```

预检输出必须保存到仓库外受限证据目录并脱敏；不得打印密码、Token、对象键、用户标识或业务正文。

### 关闭态安装与验收

```bash
set -euo pipefail
SOURCE_COMMIT="${SOURCE_COMMIT:-HUMAN_REQUIRED}"
SOURCE_STATE_ID="${SOURCE_STATE_ID:-HUMAN_REQUIRED}"
EXPECTED_HOST_FINGERPRINT="${EXPECTED_HOST_FINGERPRINT:-HUMAN_REQUIRED}"
IMAGE_DIGEST="${IMAGE_DIGEST:-HUMAN_REQUIRED}"
BACKUP_DIR="${BACKUP_DIR:-HUMAN_REQUIRED}"
MYSQL_DATABASE="${MYSQL_DATABASE:-HUMAN_REQUIRED}"
MYSQL_HOST="${MYSQL_HOST:-HUMAN_REQUIRED}"
MYSQL_PORT="${MYSQL_PORT:-HUMAN_REQUIRED}"
MYSQL_USER="${MYSQL_USER:-HUMAN_REQUIRED}"
MYSQL_PASSWORD_FILE="${MYSQL_PASSWORD_FILE:-HUMAN_REQUIRED}"
EXPECTED_MYSQL_PASSWORD_SHA256="${EXPECTED_MYSQL_PASSWORD_SHA256:-HUMAN_REQUIRED}"
EXPECTED_MYSQL_SERVER_UUID="${EXPECTED_MYSQL_SERVER_UUID:-HUMAN_REQUIRED}"
API_MYSQL_HOST="${API_MYSQL_HOST:-HUMAN_REQUIRED}"
API_MYSQL_PORT="${API_MYSQL_PORT:-HUMAN_REQUIRED}"
MYSQL_CLIENT_IMAGE_DIGEST="${MYSQL_CLIENT_IMAGE_DIGEST:-HUMAN_REQUIRED}"
ENV_FILE="${ENV_FILE:-HUMAN_REQUIRED}"
EXPECTED_ENV_FILE_SHA256="${EXPECTED_ENV_FILE_SHA256:-HUMAN_REQUIRED}"
CHANGE_ID="${CHANGE_ID:-HUMAN_REQUIRED}"
for value in "$SOURCE_COMMIT" "$SOURCE_STATE_ID" "$EXPECTED_HOST_FINGERPRINT" "$IMAGE_DIGEST" "$BACKUP_DIR" "$MYSQL_DATABASE" "$MYSQL_HOST" "$MYSQL_PORT" "$MYSQL_USER" "$MYSQL_PASSWORD_FILE" "$EXPECTED_MYSQL_PASSWORD_SHA256" "$EXPECTED_MYSQL_SERVER_UUID" "$API_MYSQL_HOST" "$API_MYSQL_PORT" "$MYSQL_CLIENT_IMAGE_DIGEST" "$ENV_FILE" "$EXPECTED_ENV_FILE_SHA256" "$CHANGE_ID"; do
  test "$value" != HUMAN_REQUIRED
done
[[ "$CHANGE_ID" =~ ^[a-z0-9]{12,32}$ ]]
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
[[ "$SOURCE_STATE_ID" =~ ^[0-9a-f]{64}$ ]]
[[ "$IMAGE_DIGEST" =~ ^[A-Za-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]]
[[ "$MYSQL_PORT" =~ ^[0-9]{1,5}$ ]]
[[ "$API_MYSQL_PORT" =~ ^[0-9]{1,5}$ ]]
[[ "$MYSQL_USER" =~ ^[A-Za-z0-9_]{1,32}$ ]]
[[ "$EXPECTED_MYSQL_SERVER_UUID" =~ ^[0-9a-f-]{36}$ ]]
[[ "$MYSQL_CLIENT_IMAGE_DIGEST" =~ ^mysql@sha256:[0-9a-f]{64}$ ]]
[[ "$EXPECTED_MYSQL_PASSWORD_SHA256" =~ ^[0-9a-f]{64}$ ]]
[[ "$EXPECTED_ENV_FILE_SHA256" =~ ^[0-9a-f]{64}$ ]]
test "${MYSQL_PASSWORD_FILE#/}" != "$MYSQL_PASSWORD_FILE"
test -f "$MYSQL_PASSWORD_FILE"
test ! -L "$MYSQL_PASSWORD_FILE"
[[ "$(stat -c '%a' "$MYSQL_PASSWORD_FILE")" =~ ^(400|600)$ ]]
test "$(sha256sum "$MYSQL_PASSWORD_FILE" | cut -d' ' -f1)" = "$EXPECTED_MYSQL_PASSWORD_SHA256"
export MYSQL_HOST MYSQL_PORT MYSQL_USER MYSQL_DATABASE
export MYSQL_PASSWORD="$(<"$MYSQL_PASSWORD_FILE")"
export MYSQL_PWD="$MYSQL_PASSWORD"
MYSQL=(mysql --protocol=tcp --host="$MYSQL_HOST" --port="$MYSQL_PORT" --user="$MYSQL_USER")
MYSQLDUMP=(mysqldump --protocol=tcp --host="$MYSQL_HOST" --port="$MYSQL_PORT" --user="$MYSQL_USER")
test "$("${MYSQL[@]}" --batch --skip-column-names -e 'SELECT @@server_uuid')" = "$EXPECTED_MYSQL_SERVER_UUID"
test "${ENV_FILE#/}" != "$ENV_FILE"
test -f "$ENV_FILE"
test ! -L "$ENV_FILE"
[[ "$(stat -c '%a' "$ENV_FILE")" =~ ^(400|600)$ ]]
test "$(sha256sum "$ENV_FILE" | cut -d' ' -f1)" = "$EXPECTED_ENV_FILE_SHA256"
test "$(git rev-parse HEAD)" = "$SOURCE_COMMIT"
test "$(ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub | awk '{print $2}')" = "$EXPECTED_HOST_FINGERPRINT"
test "$(jq -r '.source_state_id' docs/evidence/video-gateway-vid-g7-source-state.json)" = "$SOURCE_STATE_ID"
jq -r '.manifest[] | "\(.sha256)  \(.path)"' docs/evidence/video-gateway-vid-g7-source-state.json | sha256sum --check --strict -
test "${VIDEO_GATEWAY_ENABLED:-false}" = false
test "${VIDEO_GATEWAY_TRAFFIC_ENABLED:-false}" = false
test "${REAL_PROVIDER:-false}" = false
test "${VIDEO_GATEWAY_LOCAL_FAKE_TEST:-false}" = false
test "${BIFROST_VIDEO_DATA_PLANE:-OFF}" = OFF
docker image inspect "$IMAGE_DIGEST" --format '{{index .RepoDigests 0}}' | grep -F "$IMAGE_DIGEST"
docker image inspect "$MYSQL_CLIENT_IMAGE_DIGEST" >/dev/null
test "$(docker image inspect "$IMAGE_DIGEST" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$SOURCE_COMMIT"
test "$(docker image inspect "$IMAGE_DIGEST" --format '{{index .Config.Labels "com.molin.source_state_id"}}')" = "$SOURCE_STATE_ID"
export VIDEO_G7_API_IMAGE_DIGEST="$IMAGE_DIGEST"
export VIDEO_G7_ENV_FILE="$ENV_FILE"
export VIDEO_G7_API_MYSQL_HOST="$API_MYSQL_HOST"
export VIDEO_G7_API_MYSQL_PORT="$API_MYSQL_PORT"
export VIDEO_G7_MYSQL_DATABASE="$MYSQL_DATABASE"
export VIDEO_G7_MYSQL_USER="$MYSQL_USER"
export VIDEO_G7_MYSQL_PASSWORD="$MYSQL_PASSWORD"
COMPOSE=(docker compose --env-file "$ENV_FILE" -p molin -f infra/docker-compose.prod.yml -f infra/docker-compose.video-gateway-g7-closed.yml)
test "$("${COMPOSE[@]}" config --images | grep -Fxc "$IMAGE_DIGEST")" = 1
test "$(./scripts/migrate.sh version 2>&1 | awk '/^[0-9]+$/ {print $1}')" = 109
test "${BACKUP_DIR#/}" != "$BACKUP_DIR"
test "$(basename "$BACKUP_DIR")" = "vid-g7-$CHANGE_ID"
backup_parent="$(dirname "$BACKUP_DIR")"
test -d "$backup_parent"
test ! -L "$backup_parent"
test ! -e "$BACKUP_DIR"
install -d -m 0700 "$BACKUP_DIR"
set -o noclobber
printf '%s:%s\n' "$CHANGE_ID" "$SOURCE_COMMIT" >"$BACKUP_DIR/.vid-g7-owner"
chmod 0600 "$BACKUP_DIR/.vid-g7-owner"
"${MYSQLDUMP[@]}" --single-transaction --routines --triggers "$MYSQL_DATABASE" | gzip -c >"$BACKUP_DIR/pre-vid-g7.sql.gz"
sha256sum "$BACKUP_DIR/pre-vid-g7.sql.gz" >"$BACKUP_DIR/pre-vid-g7.sql.gz.sha256"
./infra/scripts/video-gateway-vid-g7-fact-snapshot.sh "$MYSQL_DATABASE" "$BACKUP_DIR/pre-vid-g7-baseline.txt" base >/dev/null
sha256sum "$BACKUP_DIR/pre-vid-g7-baseline.txt" "$BACKUP_DIR/pre-vid-g7-baseline.txt.columns" >"$BACKUP_DIR/pre-vid-g7-baseline.sha256"
RESTORE_DB="molin_vid_g7_restore_${CHANGE_ID}"
[[ "$RESTORE_DB" =~ ^molin_vid_g7_restore_[a-z0-9]{12,32}$ ]]
test "$("${MYSQL[@]}" --batch --skip-column-names -e "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='$RESTORE_DB'")" = 0
"${MYSQL[@]}" -e "CREATE DATABASE \`$RESTORE_DB\` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"
cleanup_eligible=1
cleanup_restore() {
  test "$cleanup_eligible" = 1 || return 0
  marker="$("${MYSQL[@]}" --batch --skip-column-names "$RESTORE_DB" -e 'SELECT CONCAT(change_id,CHAR(58),source_commit) FROM _molin_vid_g7_restore_owner WHERE id=1')"
  test "$marker" = "$CHANGE_ID:$SOURCE_COMMIT"
  "${MYSQL[@]}" -e "DROP DATABASE \`$RESTORE_DB\`"
  cleanup_eligible=0
}
trap cleanup_restore EXIT
"${MYSQL[@]}" "$RESTORE_DB" -e "CREATE TABLE _molin_vid_g7_restore_owner(id TINYINT UNSIGNED PRIMARY KEY,change_id VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,source_commit CHAR(40) CHARACTER SET ascii COLLATE ascii_bin NOT NULL); INSERT INTO _molin_vid_g7_restore_owner VALUES(1,'$CHANGE_ID','$SOURCE_COMMIT');"
gzip -dc "$BACKUP_DIR/pre-vid-g7.sql.gz" | "${MYSQL[@]}" "$RESTORE_DB"
./infra/scripts/video-gateway-vid-g7-fact-snapshot.sh "$RESTORE_DB" "$BACKUP_DIR/restore-baseline.txt" base "$BACKUP_DIR/pre-vid-g7-baseline.txt.columns" >/dev/null
cmp "$BACKUP_DIR/pre-vid-g7-baseline.txt" "$BACKUP_DIR/restore-baseline.txt"
cleanup_restore
trap - EXIT
./scripts/migrate.sh up 12
test "$(./scripts/migrate.sh version 2>&1 | awk '/^[0-9]+$/ {print $1}')" = 122
"${COMPOSE[@]}" up -d --no-deps --no-build --force-recreate api
container_id="$("${COMPOSE[@]}" ps -q api)"
test -n "$container_id"
test "$(docker inspect "$container_id" --format '{{.Image}}')" = "$(docker image inspect "$IMAGE_DIGEST" --format '{{.Id}}')"
container_env="$(docker inspect "$container_id" --format '{{range .Config.Env}}{{println .}}{{end}}')"
grep -Fx "MYSQL_HOST=$API_MYSQL_HOST" <<<"$container_env" >/dev/null
grep -Fx "MYSQL_PORT=$API_MYSQL_PORT" <<<"$container_env" >/dev/null
grep -Fx "MYSQL_DATABASE=$MYSQL_DATABASE" <<<"$container_env" >/dev/null
grep -Fx "MYSQL_USER=$MYSQL_USER" <<<"$container_env" >/dev/null
container_password="$(grep '^MYSQL_PASSWORD=' <<<"$container_env" | cut -d= -f2-)"
test "$(printf '%s' "$container_password" | sha256sum | cut -d' ' -f1)" = "$(printf '%s' "$MYSQL_PASSWORD" | sha256sum | cut -d' ' -f1)"
unset container_env container_password
api_network="$(docker inspect "$container_id" --format '{{range $name,$settings := .NetworkSettings.Networks}}{{println $name}}{{end}}' | awk '/internal$/ {print; exit}')"
test -n "$api_network"
api_db_identity="$(docker run --rm --pull=never --network "$api_network" -e MYSQL_PWD "$MYSQL_CLIENT_IMAGE_DIGEST" mysql --protocol=tcp --host="$API_MYSQL_HOST" --port="$API_MYSQL_PORT" --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --batch --skip-column-names -e 'SELECT CONCAT(@@server_uuid,CHAR(58),DATABASE())')"
test "$api_db_identity" = "$EXPECTED_MYSQL_SERVER_UUID:$MYSQL_DATABASE"
curl --fail --silent http://127.0.0.1:8080/api/health >/dev/null
curl --fail --silent http://127.0.0.1:8080/api/ready >/dev/null
curl --fail --silent http://127.0.0.1:8080/api/version >/dev/null
test "$(curl --silent --output /dev/null --write-out '%{http_code}' -X POST http://127.0.0.1:8080/v1/videos)" = 404
test "$(curl --silent --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/v1/models)" = 401
./infra/scripts/video-gateway-vid-g7-fact-snapshot.sh "$MYSQL_DATABASE" "$BACKUP_DIR/post-install-base-baseline.txt" base "$BACKUP_DIR/pre-vid-g7-baseline.txt.columns" >/dev/null
cmp "$BACKUP_DIR/pre-vid-g7-baseline.txt" "$BACKUP_DIR/post-install-base-baseline.txt"
./infra/scripts/video-gateway-vid-g7-fact-snapshot.sh "$MYSQL_DATABASE" "$BACKUP_DIR/post-install-expanded-baseline.txt" expanded >/dev/null
sha256sum "$BACKUP_DIR/post-install-expanded-baseline.txt" "$BACKUP_DIR/post-install-expanded-baseline.txt.columns" >"$BACKUP_DIR/post-install-expanded-baseline.sha256"
```

安装后只允许Chat/Image和钱包只读基线；禁止POST视频、充值、扣费或真实Provider请求。

### 实际回滚

```bash
set -euo pipefail
PREVIOUS_IMAGE_DIGEST="${PREVIOUS_IMAGE_DIGEST:-HUMAN_REQUIRED}"
SOURCE_COMMIT="${SOURCE_COMMIT:-HUMAN_REQUIRED}"
SOURCE_STATE_ID="${SOURCE_STATE_ID:-HUMAN_REQUIRED}"
CHANGE_ID="${CHANGE_ID:-HUMAN_REQUIRED}"
BACKUP_DIR="${BACKUP_DIR:-HUMAN_REQUIRED}"
MYSQL_DATABASE="${MYSQL_DATABASE:-HUMAN_REQUIRED}"
MYSQL_HOST="${MYSQL_HOST:-HUMAN_REQUIRED}"
MYSQL_PORT="${MYSQL_PORT:-HUMAN_REQUIRED}"
MYSQL_USER="${MYSQL_USER:-HUMAN_REQUIRED}"
MYSQL_PASSWORD_FILE="${MYSQL_PASSWORD_FILE:-HUMAN_REQUIRED}"
EXPECTED_MYSQL_PASSWORD_SHA256="${EXPECTED_MYSQL_PASSWORD_SHA256:-HUMAN_REQUIRED}"
EXPECTED_MYSQL_SERVER_UUID="${EXPECTED_MYSQL_SERVER_UUID:-HUMAN_REQUIRED}"
API_MYSQL_HOST="${API_MYSQL_HOST:-HUMAN_REQUIRED}"
API_MYSQL_PORT="${API_MYSQL_PORT:-HUMAN_REQUIRED}"
MYSQL_CLIENT_IMAGE_DIGEST="${MYSQL_CLIENT_IMAGE_DIGEST:-HUMAN_REQUIRED}"
EXPECTED_HOST_FINGERPRINT="${EXPECTED_HOST_FINGERPRINT:-HUMAN_REQUIRED}"
ENV_FILE="${ENV_FILE:-HUMAN_REQUIRED}"
EXPECTED_ENV_FILE_SHA256="${EXPECTED_ENV_FILE_SHA256:-HUMAN_REQUIRED}"
for value in "$PREVIOUS_IMAGE_DIGEST" "$SOURCE_COMMIT" "$SOURCE_STATE_ID" "$CHANGE_ID" "$BACKUP_DIR" "$MYSQL_DATABASE" "$MYSQL_HOST" "$MYSQL_PORT" "$MYSQL_USER" "$MYSQL_PASSWORD_FILE" "$EXPECTED_MYSQL_PASSWORD_SHA256" "$EXPECTED_MYSQL_SERVER_UUID" "$API_MYSQL_HOST" "$API_MYSQL_PORT" "$MYSQL_CLIENT_IMAGE_DIGEST" "$EXPECTED_HOST_FINGERPRINT" "$ENV_FILE" "$EXPECTED_ENV_FILE_SHA256"; do
  test "$value" != HUMAN_REQUIRED
done
[[ "$MYSQL_PORT" =~ ^[0-9]{1,5}$ ]]
[[ "$API_MYSQL_PORT" =~ ^[0-9]{1,5}$ ]]
[[ "$MYSQL_USER" =~ ^[A-Za-z0-9_]{1,32}$ ]]
[[ "$EXPECTED_MYSQL_SERVER_UUID" =~ ^[0-9a-f-]{36}$ ]]
[[ "$PREVIOUS_IMAGE_DIGEST" =~ ^[A-Za-z0-9._/-]+@sha256:[0-9a-f]{64}$ ]]
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]]
[[ "$SOURCE_STATE_ID" =~ ^[0-9a-f]{64}$ ]]
[[ "$CHANGE_ID" =~ ^[a-z0-9]{12,32}$ ]]
[[ "$MYSQL_CLIENT_IMAGE_DIGEST" =~ ^mysql@sha256:[0-9a-f]{64}$ ]]
[[ "$EXPECTED_MYSQL_PASSWORD_SHA256" =~ ^[0-9a-f]{64}$ ]]
[[ "$EXPECTED_ENV_FILE_SHA256" =~ ^[0-9a-f]{64}$ ]]
test "${MYSQL_PASSWORD_FILE#/}" != "$MYSQL_PASSWORD_FILE"
test -f "$MYSQL_PASSWORD_FILE"
test ! -L "$MYSQL_PASSWORD_FILE"
[[ "$(stat -c '%a' "$MYSQL_PASSWORD_FILE")" =~ ^(400|600)$ ]]
test "$(sha256sum "$MYSQL_PASSWORD_FILE" | cut -d' ' -f1)" = "$EXPECTED_MYSQL_PASSWORD_SHA256"
export MYSQL_HOST MYSQL_PORT MYSQL_USER MYSQL_DATABASE
export MYSQL_PASSWORD="$(<"$MYSQL_PASSWORD_FILE")"
export MYSQL_PWD="$MYSQL_PASSWORD"
MYSQL=(mysql --protocol=tcp --host="$MYSQL_HOST" --port="$MYSQL_PORT" --user="$MYSQL_USER")
test "$("${MYSQL[@]}" --batch --skip-column-names -e 'SELECT @@server_uuid')" = "$EXPECTED_MYSQL_SERVER_UUID"
test "${ENV_FILE#/}" != "$ENV_FILE"
test -f "$ENV_FILE"
test ! -L "$ENV_FILE"
[[ "$(stat -c '%a' "$ENV_FILE")" =~ ^(400|600)$ ]]
test "$(sha256sum "$ENV_FILE" | cut -d' ' -f1)" = "$EXPECTED_ENV_FILE_SHA256"
test "$(ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub | awk '{print $2}')" = "$EXPECTED_HOST_FINGERPRINT"
test "$(git rev-parse HEAD)" = "$SOURCE_COMMIT"
test "$(jq -r '.source_state_id' docs/evidence/video-gateway-vid-g7-source-state.json)" = "$SOURCE_STATE_ID"
jq -r '.manifest[] | "\(.sha256)  \(.path)"' docs/evidence/video-gateway-vid-g7-source-state.json | sha256sum --check --strict -
test "$(basename "$BACKUP_DIR")" = "vid-g7-$CHANGE_ID"
test "${BACKUP_DIR#/}" != "$BACKUP_DIR"
test -d "$BACKUP_DIR"
test ! -L "$BACKUP_DIR"
test -f "$BACKUP_DIR/.vid-g7-owner"
test ! -L "$BACKUP_DIR/.vid-g7-owner"
test "$(<"$BACKUP_DIR/.vid-g7-owner")" = "$CHANGE_ID:$SOURCE_COMMIT"
test "${VIDEO_GATEWAY_ENABLED:-false}" = false
test "${VIDEO_GATEWAY_TRAFFIC_ENABLED:-false}" = false
test "${REAL_PROVIDER:-false}" = false
test "${VIDEO_GATEWAY_LOCAL_FAKE_TEST:-false}" = false
test "${BIFROST_VIDEO_DATA_PLANE:-OFF}" = OFF
test -r "$BACKUP_DIR/pre-vid-g7.sql.gz.sha256"
test -r "$BACKUP_DIR/pre-vid-g7.sql.gz"
sha256sum --check "$BACKUP_DIR/pre-vid-g7.sql.gz.sha256"
test -r "$BACKUP_DIR/pre-vid-g7-baseline.txt"
test -r "$BACKUP_DIR/pre-vid-g7-baseline.txt.columns"
test -r "$BACKUP_DIR/pre-vid-g7-baseline.sha256"
sha256sum --check "$BACKUP_DIR/pre-vid-g7-baseline.sha256"
test -r "$BACKUP_DIR/post-install-expanded-baseline.txt"
test -r "$BACKUP_DIR/post-install-expanded-baseline.txt.columns"
test -r "$BACKUP_DIR/post-install-expanded-baseline.sha256"
sha256sum --check "$BACKUP_DIR/post-install-expanded-baseline.sha256"
docker image inspect "$PREVIOUS_IMAGE_DIGEST" >/dev/null
docker image inspect "$MYSQL_CLIENT_IMAGE_DIGEST" >/dev/null
export VIDEO_G7_API_IMAGE_DIGEST="$PREVIOUS_IMAGE_DIGEST"
export VIDEO_G7_ENV_FILE="$ENV_FILE"
export VIDEO_G7_API_MYSQL_HOST="$API_MYSQL_HOST"
export VIDEO_G7_API_MYSQL_PORT="$API_MYSQL_PORT"
export VIDEO_G7_MYSQL_DATABASE="$MYSQL_DATABASE"
export VIDEO_G7_MYSQL_USER="$MYSQL_USER"
export VIDEO_G7_MYSQL_PASSWORD="$MYSQL_PASSWORD"
COMPOSE=(docker compose --env-file "$ENV_FILE" -p molin -f infra/docker-compose.prod.yml -f infra/docker-compose.video-gateway-g7-closed.yml)
test "$("${COMPOSE[@]}" config --images | grep -Fxc "$PREVIOUS_IMAGE_DIGEST")" = 1
test "$(./scripts/migrate.sh version 2>&1 | awk '/^[0-9]+$/ {print $1}')" = 122
export VIDEO_GATEWAY_ENABLED=false
export VIDEO_GATEWAY_TRAFFIC_ENABLED=false
export REAL_PROVIDER=false
export VIDEO_GATEWAY_LOCAL_FAKE_TEST=false
./scripts/migrate.sh down 13
test "$(./scripts/migrate.sh version 2>&1 | awk '/^[0-9]+$/ {print $1}')" = 109
"${COMPOSE[@]}" up -d --no-deps --no-build --force-recreate api
container_id="$("${COMPOSE[@]}" ps -q api)"
test -n "$container_id"
test "$(docker inspect "$container_id" --format '{{.Image}}')" = "$(docker image inspect "$PREVIOUS_IMAGE_DIGEST" --format '{{.Id}}')"
container_env="$(docker inspect "$container_id" --format '{{range .Config.Env}}{{println .}}{{end}}')"
grep -Fx "MYSQL_HOST=$API_MYSQL_HOST" <<<"$container_env" >/dev/null
grep -Fx "MYSQL_PORT=$API_MYSQL_PORT" <<<"$container_env" >/dev/null
grep -Fx "MYSQL_DATABASE=$MYSQL_DATABASE" <<<"$container_env" >/dev/null
grep -Fx "MYSQL_USER=$MYSQL_USER" <<<"$container_env" >/dev/null
container_password="$(grep '^MYSQL_PASSWORD=' <<<"$container_env" | cut -d= -f2-)"
test "$(printf '%s' "$container_password" | sha256sum | cut -d' ' -f1)" = "$(printf '%s' "$MYSQL_PASSWORD" | sha256sum | cut -d' ' -f1)"
unset container_env container_password
api_network="$(docker inspect "$container_id" --format '{{range $name,$settings := .NetworkSettings.Networks}}{{println $name}}{{end}}' | awk '/internal$/ {print; exit}')"
test -n "$api_network"
api_db_identity="$(docker run --rm --pull=never --network "$api_network" -e MYSQL_PWD "$MYSQL_CLIENT_IMAGE_DIGEST" mysql --protocol=tcp --host="$API_MYSQL_HOST" --port="$API_MYSQL_PORT" --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --batch --skip-column-names -e 'SELECT CONCAT(@@server_uuid,CHAR(58),DATABASE())')"
test "$api_db_identity" = "$EXPECTED_MYSQL_SERVER_UUID:$MYSQL_DATABASE"
curl --fail --silent http://127.0.0.1:8080/api/health >/dev/null
curl --fail --silent http://127.0.0.1:8080/api/ready >/dev/null
test "$(curl --silent --output /dev/null --write-out '%{http_code}' -X POST http://127.0.0.1:8080/v1/videos)" = 404
test "$(curl --silent --output /dev/null --write-out '%{http_code}' http://127.0.0.1:8080/v1/models)" = 401
./infra/scripts/video-gateway-vid-g7-fact-snapshot.sh "$MYSQL_DATABASE" "$BACKUP_DIR/post-rollback-expanded-baseline.txt" expanded "$BACKUP_DIR/post-install-expanded-baseline.txt.columns" >/dev/null
cmp "$BACKUP_DIR/post-install-expanded-baseline.txt" "$BACKUP_DIR/post-rollback-expanded-baseline.txt"
"${MYSQL[@]}" --batch --skip-column-names "$MYSQL_DATABASE" -e "SELECT COUNT(*) FROM ai_requests WHERE capability='video.generate'; SELECT COUNT(*) FROM ai_gateway_tasks WHERE capability='video.generate'; SELECT COUNT(*) FROM ai_outbox_events WHERE aggregate_type='video_request'; SELECT COUNT(*) FROM wallet_holds h JOIN ai_request_wallet_links l ON l.wallet_hold_id=h.id JOIN ai_requests r ON r.request_id=l.request_id WHERE r.capability='video.generate';"
```

若环境不是Docker Compose、端口与既有约定不同或migration由专用工具管理，必须在授权前把模板替换为该主机真实命令并重新审查；不得临场自由改写后继续。
