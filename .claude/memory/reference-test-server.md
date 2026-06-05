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

## 当前测试状态（2026-06-05）

- 后端 A（auth / iam / identity）：33/33 测试用例全部通过
- 测试脚本位置：`tests/test_backend_a.py`
- API 二进制：`~/molin/molin-api`（部署在测试服务器）

**Why:** 测试环境运行在公网测试服务器，本地 Python 沙箱无法访问 localhost:8080，需要在测试服务器上执行测试脚本。
**How to apply:** 每次更新后端代码后，重新编译并上传二进制，再执行测试脚本。
