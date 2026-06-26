---
name: reference-test-server
description: 测试服务器 SSH 连接、服务端口、API 进程启动和 Python 测试执行方式
metadata: 
  node_type: memory
  type: reference
  originSessionId: 25f47551-2bee-4efa-8a8b-d6653fbe1e67
---

## 测试服务器基本信息

| 项目 | 值 |
|---|---|
| 公网 IP | `8.130.9.163` |
| SSH 端口 | `10003` |
| SSH 用户 | `pc` |
| SSH 密码 | `Root123!` |
| 项目目录 | `~/molin/` |

SSH 连接命令：
```bash
sshpass -p 'Root123!' ssh -p 10003 pc@8.130.9.163
```

## 测试服务器上的服务端口（与本地开发完全一致）

| 服务 | 宿主机端口 | 说明 |
|---|---|---|
| Go API | 8080 | 后端 API |
| MySQL | 13306 | 偏移端口避免冲突 |
| Redis | 16379 | 偏移端口 |
| RabbitMQ | 5673 / 15673 | 偏移端口 |
| MinIO | 19000 / 19001 | 偏移端口 |

## 切换到测试环境

```bash
# 本地切换到测试环境变量
export $(grep -v '^#' infra/.env.test | xargs)
```

`infra/.env.test` 关键内容：
```
APP_ENV=test
MYSQL_HOST=8.130.9.163
REDIS_ADDR=8.130.9.163:16379
RABBITMQ_URL=amqp://molin:molin_password@8.130.9.163:5673/
MINIO_ENDPOINT=8.130.9.163:19000
```

**注意：** `infra/.env.test` 和 `infra/.env.local` 均须列入 `.gitignore`，禁止入库。

## API 进程管理（测试服务器上）

```bash
# 查看 API 进程
sshpass -p 'Root123!' ssh -p 10003 pc@8.130.9.163 'pgrep -a molin-api'

# 停止并重新部署
sshpass -p 'Root123!' ssh -p 10003 pc@8.130.9.163 'pkill molin-api; sleep 1'

# 本地编译 → 上传 → 启动
cd server
GOOS=linux GOARCH=amd64 go build -o ../molin-api ./cmd/api
sshpass -p 'Root123!' scp -P 10003 ../molin-api pc@8.130.9.163:~/molin/molin-api
sshpass -p 'Root123!' ssh -p 10003 pc@8.130.9.163 \
  "export \$(grep -v '^#' ~/molin/infra/.env.test | xargs) && \
   nohup ~/molin/molin-api > ~/molin/api.log 2>&1 &"
```

## 运行 Python 测试（测试服务器上远程执行）

```bash
# 上传测试脚本
sshpass -p 'Root123!' scp -P 10003 tests/test_backend_a.py pc@8.130.9.163:~/molin/test_backend_a.py

# 执行测试（全部 33 个用例）
sshpass -p 'Root123!' ssh -p 10003 pc@8.130.9.163 \
  "API_BASE=http://localhost:8080 \
   MYSQL_HOST=127.0.0.1 MYSQL_PORT=13306 \
   MYSQL_USER=molin MYSQL_PASSWORD=molin_password MYSQL_DATABASE=molin \
   python3 ~/molin/test_backend_a.py"
```

## MySQL 直连测试服务器（本地执行）

```bash
mysql -h 8.130.9.163 -P 13306 -u molin -pmolin_password molin
```

## 数据库迁移（测试服务器上执行，2026-06-21 修复后）

标准方式：在 `~/molin/` 下执行 `./migrate.sh <up|down|version|force>`。脚本（2026-06-21 已对齐仓库正确版）会自动 source `~/molin/infra/.env.test`、连 13306、**DB_URL 带 `charset=utf8mb4`**（中文 seed 不乱码）、迁移目录为同级 `~/molin/migrations/`。新增迁移 scp 到该目录后跑 `./migrate.sh up` 即可。

```bash
# 查看当前版本
sshpass -p 'Root123!' ssh -p 10003 pc@8.130.9.163 'cd ~/molin && ./migrate.sh version'
# 上传新迁移文件后执行
sshpass -p 'Root123!' scp -P 10003 server/migrations/0000NN_*.sql pc@8.130.9.163:~/molin/migrations/
sshpass -p 'Root123!' ssh -p 10003 pc@8.130.9.163 'cd ~/molin && ./migrate.sh up'
```

**踩过的坑（已修，勿重犯）：**
- `~/molin` **不是 git 仓库**，迁移文件靠 scp 手动同步，不能 `git pull`。
- 服务器旧 `migrate.sh` 曾 **缺 `charset=utf8mb4`**（latin1 连库→中文 seed 乱码，同仓库 000027 根因）且 `MIGRATIONS_DIR` 指向错误的 `../server/migrations`；2026-06-21 已修正指向 `~/molin/migrations` 并补 charset，备份为 `migrate.sh.bak.*`。
- 陈旧目录 `~/molin/server/migrations/`（只到 000029）已改名 `.deprecated.*` 弃用，**实际目录是 `~/molin/migrations/`**（000001–000035 连续）。
- 本机执行时 `.env.test` 的 `MYSQL_HOST=8.130.9.163`（公网）会绕公网回连；可 `MYSQL_HOST=127.0.0.1 ./migrate.sh ...` 临时覆盖（脚本支持环境变量优先）。

**迁移序号铁律**见 [[feedback-migration-sequential-numbering]]：golang-migrate 不支持 out-of-order，序号按合并顺序连续、不留空号。

## 已落库迁移进度

- 截至 2026-06-21：测试库 `schema_migrations` version=**35**（含 000034 api_keys、000035 wallet_holds，第二阶段 M1 S2-甲1/S2-乙0）。

## 当前测试状态（2026-06-05）

- 后端 A（auth / iam / identity）：33/33 测试用例全部通过
- 测试脚本位置：`tests/test_backend_a.py`
- API 二进制：`~/molin/molin-api`（部署在测试服务器）

**Why:** 测试环境运行在公网测试服务器，本地 Python 沙箱无法访问 localhost:8080，需要在测试服务器上执行测试脚本。
**How to apply:** 每次更新后端代码后，重新编译并上传二进制，再执行测试脚本。

## molin-api 部署/重启方式（非 systemd！2026-06-26 踩坑确认）

测试服 `~/molin/molin-api` **不是 systemd 服务**（`systemctl` 查无），而是 **nohup 普通进程**，靠 .env.test 注入环境变量启动。重启不能用 `systemctl restart`，直接 kill 不会自动拉起。标准重启（沿用部署脚本同款）：
```bash
# 本机交叉编译（go 在 ~/.local/go/bin；worktree 构建需加 -buildvcs=false）
cd server && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -o /tmp/molin-api.new ./cmd/api
# scp 上去（先备份旧的），再在测试服执行重启：
ssh ... 'cd /home/pc/molin && cp -f molin-api molin-api.bak.$(date +%Y%m%d-%H%M%S) && mv molin-api.new molin-api && chmod +x molin-api'
ssh ... "setsid bash -c 'cd /home/pc/molin && pkill -x molin-api; sleep 2; export \$(grep -v \"^#\" infra/.env.test | xargs); nohup ./molin-api > api.log 2>&1' < /dev/null &"
# 验证：pgrep -a molin-api；tail api.log；curl 127.0.0.1:8080/api/health 应 200
```
关键点：① 必须 `setsid`+`nohup` 脱离 SSH 会话，否则断开连接进程被 SIGHUP 杀掉；② 改了 .env.test 后**必须重启进程**才生效——运行中进程的 `/proc/<pid>/environ` 保留旧值（presenton 下线时即因此发现旧二进制仍带 PRESENTON_* 环境变量）；③ 二进制是本机构建后 scp 的，**回退后端代码后必须重新构建+部署二进制**，否则旧二进制仍编译着已删模块（presenton 回退时旧二进制 strings 仍含 96 处 modules/presenton）。
