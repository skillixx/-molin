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
    deploy-test.yml               -- 手动触发（workflow_dispatch，Actions 页面 Run workflow）部署测试环境

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

## 测试环境部署（.github/workflows/deploy-test.yml）

- **触发方式：手动触发（workflow_dispatch）**。合并到 main **不会**自动部署测试环境。
- 操作路径：GitHub 仓库 → Actions → 选择「部署测试环境（前端）」工作流 → Run workflow（分支选 main）。
- **范围：仅前端**。在 runner 上构建 `molin-admin` / `molin-user` 镜像 → scp 到测试服务器 → `docker load` → 在固定 `molin-sms-proxy` 专网和固定 `.2/.3` 地址重建容器，经专网网关 `.1` 代理宿主机 `molin-api:8080`。部署和自动回滚必须复验容器、固定 IP、首页、`/api/health`，并在同一 API PID 前后两次确认 `SMS_ENABLED=false`；任一失败均不得报告部署或回滚成功。工作流不得 POST 发码入口，以免写入 Redis 限流桶；`503/50300` 由 CI 契约和独立验收窗口验证。
- **需配置 Secrets**：`TEST_SERVER_HOST` / `TEST_SERVER_USER` / `TEST_SERVER_PASSWORD`（密码认证，端口 10003）。
- **后端 API 不在本工作流内**：测试服 `molin-api` 是宿主机二进制（非容器、非 systemd），按上文「测试服务器」一节单独编译 scp + nohup 重启。早期工作流里的 `/opt/molin` + `git pull` + compose 构建 api 容器与现实不符，已废弃。

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

## 阿里云短信部署契约

### 配置边界与权威来源

首期只支持阿里云中国大陆验证码短信。运维负责安全注入运行配置和控制发布开关，不负责维护模板业务数据或实现发送逻辑。

| 配置 | 作用 | 生产要求 |
|---|---|---|
| `SMS_ENABLED` | 短信总开关 | 默认 `false`；只有完成真实小流量验证和验收后才能开启 |
| `SMS_PROVIDER` | 短信供应商 | 首期固定为 `aliyun`，其他值必须拒绝启动或拒绝短信提交 |
| `SMS_ALIYUN_ACCESS_KEY_ID` | 最小权限 RAM 凭证标识 | 通过服务器环境变量或 Secret Manager 注入，不入库 |
| `SMS_ALIYUN_ACCESS_KEY_SECRET` | 最小权限 RAM 凭证密钥 | 只通过安全渠道注入，不得输出到日志或接口 |
| `SMS_ALIYUN_SIGN_NAME` | 首期唯一已审核签名 | 运行时固定值；数据库绑定中的签名只能作为提交快照且必须与其一致 |
| `SMS_ALIYUN_ENDPOINT` | 阿里云短信 API 域名 | 默认 `dysmsapi.aliyuncs.com`，不得配置为任意 URL 或内网地址 |
| `SMS_PHONE_HMAC_SECRET` | 完整手机号的不可逆 HMAC 密钥 | 与 AccessKey、JWT、身份证 HMAC 密钥完全独立，至少 32 字节 |
| `SMS_TEST_MODE` | 真实短信白名单限制开关 | `true` 仍调用阿里云；不得实现或开启模拟发送回退 |
| `SMS_TEST_PHONE_WHITELIST` | 测试发送和灰度手机号白名单 | 真实号码只存在于不入库环境文件或密钥系统；空白名单全拒 |
| `SMS_TEST_SCENE_ALLOWLIST` | 测试模式短信场景白名单 | `SMS_ENABLED=true` 且 `SMS_TEST_MODE=true` 时不能为空；长期测试登录仅配置 `login` |

五个业务场景 `register`、`login`、`reset_password`、`bind_phone`、`admin_verify` 的模板编码不得继续使用 `SMS_TEMPLATE_CODE_*` 环境变量。模板编码和审核状态来自阿里云同步快照，场景绑定和本地启停来自数据库；这是唯一权威来源，避免环境变量与数据库长期形成两套配置。

首期固定签名以 `SMS_ALIYUN_SIGN_NAME` 为运行权威。场景绑定和发送日志可以保存签名快照，但后台提交的签名必须与环境变量固定值一致，不允许借管理接口切换到未审核签名。

### 生产 fail-closed 规则

- `SMS_ENABLED=false` 时，所有手机短信提交均应明确返回服务未启用，不生成可校验的手机验证码；邮箱验证码链路不受此开关影响。
- `SMS_ENABLED=true` 时，供应商、AccessKey ID、AccessKey Secret、固定签名、端点、手机号 HMAC 密钥或当前场景有效绑定任一缺失，必须启动失败或拒绝短信提交。
- 阿里云超时、限流、签名错误、模板错误、账户异常和网络错误均不得回退到模拟供应商、固定验证码或响应明文验证码。
- `SMS_TEST_MODE=true` 且白名单为空时必须全拒；手机号不在白名单时必须拒绝，且日志只能记录脱敏手机号和 HMAC。
- `SMS_TEST_MODE=true` 时场景白名单同样必须非空；未列入 `SMS_TEST_SCENE_ALLOWLIST` 的场景必须在 OTP 创建、发送日志和供应商调用前拒绝。
- 生产环境不得把阿里云 `Code=OK` 表述为用户已收到，只能记录“供应商已受理”。

### 密钥注入与轮换

1. 本地开发使用不入库的 `infra/.env.local`，测试环境使用不入库的 `infra/.env.test`，生产使用服务器受控环境变量、`.env.prod` 或 Secret Manager。
2. 阿里云账号使用最小权限 RAM 用户或 RAM 角色，禁止使用主账号长期 AccessKey。
3. 轮换 AccessKey 时先注入新凭证，在 `SMS_TEST_MODE=true` 下完成白名单提交验证，再撤销旧凭证；整个过程不得把密钥打印到终端记录、CI 日志或工单正文。
4. `SMS_PHONE_HMAC_SECRET` 轮换会改变手机号 HMAC。轮换前必须确认历史限流和排障查询的兼容方案；不得直接覆盖后宣称历史索引仍连续可查。
5. CI/CD 只传递 Secret 引用，不得把真实值写入 workflow、Docker build argument、镜像层或制品。

### 网络放行

- API 运行环境必须能解析 `SMS_ALIYUN_ENDPOINT`，并按域名放行出站 TCP 443。
- 阿里云域名解析 IP 可能动态变化，防火墙或云安全策略不得固定单个解析 IP；应使用支持 FQDN 的出站策略或受控代理。
- 禁止为了短信接入放开任意公网出站；只放行批准的阿里云短信 API 域名和必要的 DNS、HTTPS 链路。
- 端点变更必须经过安全评审，禁止通过环境变量指向环回、内网或非阿里云域名。

### 灰度发布与回滚

上线顺序：

```text
注入新配置但保持 SMS_ENABLED=false
  → 执行 migration 和权限 seed
  → 后台同步阿里云已审核模板
  → 配置并核对五个数据库场景绑定
  → 设置 SMS_TEST_MODE=true 和受控白名单
  → 开启 SMS_ENABLED，执行小流量真实阿里云提交与手机收件验证
  → 测试工程师和产品经理确认
  → SMS_TEST_MODE=false，逐步放量
```

出现提交失败率异常、签名或模板错误、费用异常、验证码安全问题时，立即将 `SMS_ENABLED=false` 并重启或重新加载服务。回滚只关闭新短信提交，不删除模板快照、场景绑定、发送日志和审计记录，不启用模拟回退。恢复前必须重新完成白名单真实验证。

### 短信上线检查清单

```text
□ SMS_ENABLED 初始为 false
□ SMS_PROVIDER 固定为 aliyun
□ RAM 最小权限凭证已通过安全渠道注入，仓库和镜像中无真实密钥
□ 固定签名已审核通过，SMS_ALIYUN_SIGN_NAME 与首期批准签名一致
□ SMS_PHONE_HMAC_SECRET 为独立强随机密钥
□ SMS_ALIYUN_ENDPOINT 域名解析与 TCP 443 出站正常，未固定动态 IP
□ 环境变量中不存在 SMS_TEMPLATE_CODE_*，五个模板均通过数据库绑定
□ SMS_TEST_MODE=true 时白名单非空，非白名单号码实测被拒
□ 阿里云异常时手机验证码 fail-closed，无模拟回退和明文验证码响应
□ 已区分“阿里云受理”和“真实手机收到”，两项证据分别记录
□ 回滚开关、负责人和密钥撤销方式已确认
```
