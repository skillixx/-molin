# 运维 — 部署与环境负责人

## 职责边界

只负责：本地开发环境、CI/CD 流水线、测试环境、生产部署、环境变量管理、日志和监控。

不负责：业务代码实现、数据库表设计。

## 本地开发环境

### 一键启动

```bash
# 1. 启动基础服务
docker compose -f infra/docker-compose.yml up -d

# 2. 执行数据库建表
chmod +x scripts/create_mysql_tables.sh
./scripts/create_mysql_tables.sh

# 3. 启动后端 API
cd server && go run ./cmd/api

# 4. 启动管理后台
cd web/admin-console && npm install && npm run dev

# 5. 启动用户控制台
cd web/user-console && npm install && npm run dev
```

### 服务端口

| 服务 | 宿主机端口 | 容器端口 | 说明 |
|---|---|---|---|
| Go API | 8080 | 8080 | 后端 API（原生运行，无偏移） |
| Admin Console | 5173 | 5173 | 管理后台（Vite 默认端口） |
| User Console | 5174 | 5174 | 用户控制台 |
| MySQL | **13306** | 3306 | 避免与测试环境 3306 冲突 |
| Redis | **16379** | 6379 | 避免与测试环境 6379 冲突 |
| RabbitMQ | **5673 / 15673** | 5672 / 15672 | 避免与测试环境 5672/15672 冲突 |
| MinIO | **19000 / 19001** | 9000 / 9001 | 避免与测试环境 9000/9001 冲突 |

### 环境变量

本地开发使用 `infra/.env.local`（不入库），参考 `infra/.env.example`。

## 测试服务器

| 项目 | 值 |
|---|---|
| 公网 IP | `8.130.9.163` |
| SSH 端口 | `10003` |
| SSH 用户 | `pc` |
| 项目目录 | `~/molin/` |

```bash
# SSH 连接
sshpass -p '$TEST_SSH_PASS' ssh -p 10003 pc@8.130.9.163

# 编译 API 并上传到测试服务器
cd server
GOOS=linux GOARCH=amd64 go build -o ../molin-api ./cmd/api
sshpass -p '$TEST_SSH_PASS' scp -P 10003 ../molin-api pc@8.130.9.163:~/molin/molin-api

# 重启测试服务器 API
sshpass -p '$TEST_SSH_PASS' ssh -p 10003 pc@8.130.9.163 \
  "pkill molin-api 2>/dev/null; sleep 1; \
   export \$(grep -v '^#' ~/molin/infra/.env.test | xargs) && \
   nohup ~/molin/molin-api > ~/molin/api.log 2>&1 &"

# 查看 API 日志
sshpass -p '$TEST_SSH_PASS' ssh -p 10003 pc@8.130.9.163 'tail -20 ~/molin/api.log'

# 直连测试服务器 MySQL
mysql -h 8.130.9.163 -P 13306 -u molin -p$TEST_MYSQL_PASS molin
```

测试服务器的 Docker 基础服务端口与本地开发完全一致（偏移方案）：MySQL 13306、Redis 16379、RabbitMQ 5673/15673、MinIO 19000/19001。

环境变量文件 `infra/.env.test` 已配置指向 `8.130.9.163`，**必须加入 .gitignore**，禁止入库。

## 需要创建的文件

```text
infra/
  .env.example                    -- 环境变量模板（入库，不含真实密钥）
  .env.local                      -- 本地开发实际值（.gitignore 排除）
  Dockerfile.server               -- Go API 生产镜像
  Dockerfile.admin-console        -- 管理后台生产镜像
  Dockerfile.user-console         -- 用户控制台生产镜像
  docker-compose.yml              -- 本地开发基础服务（已存在）
  docker-compose.prod.yml         -- 生产环境全量编排
  nginx/
    admin.conf                    -- 管理后台 Nginx 配置
    user.conf                     -- 用户控制台 Nginx 配置
    api.conf                      -- API 反向代理配置

.github/
  workflows/
    ci.yml                        -- 代码提交 CI（构建 + 测试 + lint）
    deploy-test.yml               -- 推送 main 自动部署测试环境

scripts/
  create_mysql_tables.sh          -- 建表脚本（已存在）
  wait-for-it.sh                  -- 等待服务就绪
  migrate.sh                      -- 执行 migration
```

## Dockerfile.server 要点

```dockerfile
# 多阶段构建
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ .
RUN CGO_ENABLED=0 go build -o api ./cmd/api

FROM alpine:3.19
RUN apk add --no-cache tzdata ca-certificates
WORKDIR /app
COPY --from=builder /app/api .
EXPOSE 8080
CMD ["./api"]
```

## docker-compose.prod.yml 要点

```yaml
services:
  api:
    build:
      context: .
      dockerfile: infra/Dockerfile.server
    env_file: .env.prod
    ports: ["8080:8080"]
    depends_on: [mysql, redis, rabbitmq]
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/api/health"]
      interval: 30s
      timeout: 5s
      retries: 3

  admin-console:
    build:
      context: .
      dockerfile: infra/Dockerfile.admin-console
    ports: ["3001:80"]

  user-console:
    build:
      context: .
      dockerfile: infra/Dockerfile.user-console
    ports: ["3000:80"]
```

## CI 流水线（.github/workflows/ci.yml）

```yaml
# 触发：push 和 PR 到 main
# 步骤：
# 1. go vet + go test ./...（后端）
# 2. golangci-lint（后端代码风格）
# 3. npm run type-check（前端 TypeScript）
# 4. npm run lint（前端 ESLint）
# 5. npm run build（确认前端可以构建成功）
```

## 环境变量管理规则

- `infra/.env.example`：所有变量名 + 说明 + 示例值（不含真实密钥），**必须入库**。
- `infra/.env.local`：本地实际值，**必须加入 .gitignore**。
- `.env.prod`：生产值，**不入库**，通过服务器手动或 Secret Manager 管理。
- **绝对禁止**将 JWT_SECRET、REFRESH_TOKEN_SECRET、ID_CARD_HMAC_SECRET、TOKEN_PROVIDER_KEY、WECHAT_PAY_API_V3_KEY 等密钥提交到代码仓库。

## 部署 Checklist（每次上线前）

```text
□ migration 文件已执行（./scripts/migrate.sh）
□ .env.prod 已更新（新增的环境变量已补充）
□ docker-compose.prod.yml 已更新
□ 健康检查 /api/health 返回 200
□ 数据库备份已完成
□ Redis 缓存无异常
□ RabbitMQ 队列无积压
```

## 日志规范

- 后端日志输出到 stdout，由 Docker 收集。
- 生产环境必须配置日志持久化（建议挂载 volume 或接入日志服务）。
- 敏感字段（密码、Token、身份证号）不允许出现在日志中。
