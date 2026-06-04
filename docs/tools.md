# 项目工具文档

> 记录项目中使用到的所有工具，包含作用说明、使用者、涉及功能模块和常用命令，方便团队成员快速上手。

---

## 目录

- [后端工具](#后端工具)
- [前端工具](#前端工具)
- [基础设施工具](#基础设施工具)
- [开发辅助工具](#开发辅助工具)

---

## 后端工具

### Go

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A / 后端 B / 后端 C |
| **涉及模块** | 全部后端模块 |
| **涉及功能** | 所有后端业务逻辑、API 服务启动、单元测试 |
| **代码位置** | `server/` 全部 `.go` 文件 |

**作用：** 后端服务主语言，编译型强类型语言，适合高并发 API 服务开发。

**常用命令：**
```bash
go run ./cmd/api              # 启动后端服务（开发模式）
go build -o bin/api ./cmd/api # 编译二进制文件
go test ./...                 # 运行所有单元测试
go test -race ./...           # 运行测试并检测数据竞争
go test -cover ./...          # 运行测试并输出覆盖率
go mod tidy                   # 整理依赖，移除未使用的包
go vet ./...                  # 静态分析，检查常见错误
```

---

### Gin

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A / 后端 B / 后端 C |
| **涉及模块** | 全部 HTTP Handler |
| **涉及功能** | 路由注册、请求参数绑定、中间件挂载、JSON 响应 |
| **代码位置** | `server/internal/modules/*/handler/*.go`、`server/internal/bootstrap/app.go` |

**作用：** Go HTTP Web 框架，提供路由、中间件、参数绑定、JSON 响应等能力。

**常用用法：**
```go
r := gin.New()                               // 创建路由（不带默认中间件）
r.Use(middleware.Logger())                   // 注册全局中间件
v1 := r.Group("/api/v1")                    // 路由分组
v1.POST("/auth/register", handler.Register) // 注册路由
r.Run(":8080")                              // 启动服务
```

**文档：** https://gin-gonic.com/zh-cn/docs/

---

### GORM

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A / 后端 B / 后端 C |
| **涉及模块** | 全部模块的 Repository 层 |
| **涉及功能** | 数据库 CRUD、事务、乐观锁、连接池管理 |
| **代码位置** | `server/internal/modules/*/repository/*.go`、`server/pkg/db/db.go` |

**作用：** Go ORM 框架，简化数据库操作，支持 MySQL，内置连接池管理。

**常用用法：**
```go
db.Create(&user)                                            // 插入记录
db.First(&user, id)                                         // 按主键查询
db.Where("email = ?", email).First(&user)                   // 条件查询
db.Model(&user).Updates(map)                                // 更新指定字段
db.Delete(&user, id)                                        // 软删除
db.Transaction(func(tx *gorm.DB) error { ... })             // 事务
db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&w)   // SELECT FOR UPDATE（钱包扣费用）
```

**文档：** https://gorm.io/zh_CN/docs/

---

### golang-migrate

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A / 后端 B / 后端 C / 运维 |
| **涉及模块** | 数据库 Migration |
| **涉及功能** | 数据库表结构版本管理，确保各环境表结构一致 |
| **代码位置** | `server/migrations/*.sql`、`scripts/migrate.sh` |

**作用：** 数据库 Migration 版本管理工具，支持 up/down 回滚。

**安装：**
```bash
go install -tags 'mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

**常用命令：**
```bash
# 执行所有未运行的 migration
migrate -path ./migrations -database "mysql://user:pass@tcp(localhost:3306)/molin" up

# 回滚最近一条 migration
migrate -path ./migrations -database "mysql://..." down 1

# 查看当前版本
migrate -path ./migrations -database "mysql://..." version

# 新建一对 up/down SQL 文件
migrate create -ext sql -dir ./migrations -seq create_users_table
```

---

### Viper

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A |
| **涉及模块** | `config` |
| **涉及功能** | 从 `.env` 文件和环境变量加载服务配置（数据库、Redis、JWT 密钥等） |
| **代码位置** | `server/internal/config/config.go` |

**作用：** Go 配置管理库，支持从 ENV 文件和环境变量读取配置。

**常用用法：**
```go
viper.SetConfigFile(".env")   // 指定配置文件
viper.AutomaticEnv()          // 自动读取同名环境变量
viper.ReadInConfig()          // 加载配置
viper.GetString("DB_HOST")   // 读取字符串配置
viper.Unmarshal(&cfg)         // 映射到结构体
```

---

### golang-jwt

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A |
| **涉及模块** | `auth`、`middleware` |
| **涉及功能** | 生成 Access Token（2小时有效期）、解析和校验 Token |
| **代码位置** | `server/pkg/jwt/jwt.go`、`server/internal/middleware/auth.go` |

**作用：** 生成和解析 JWT Token，用于用户身份认证。

**常用用法：**
```go
// 生成 Token
token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
signed, _ := token.SignedString([]byte(secret))

// 解析 Token
token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
    return []byte(secret), nil
})
```

---

### bcrypt

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A |
| **涉及模块** | `auth` |
| **涉及功能** | 用户注册时加密密码、登录时校验密码 |
| **代码位置** | `server/pkg/crypto/password.go` |

**作用：** 密码加密库，项目中 cost=12，防止彩虹表攻击。

**常用用法：**
```go
hash, _ := bcrypt.GenerateFromPassword([]byte(password), 12) // 加密（注册时）
err := bcrypt.CompareHashAndPassword(hash, []byte(password)) // 验证（登录时，nil 表示匹配）
```

---

### golangci-lint

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A / 后端 B / 后端 C / 运维（CI 中自动运行） |
| **涉及模块** | 全部后端代码 |
| **涉及功能** | PR 合并前代码质量检查，CI 自动触发 |
| **代码位置** | `.github/workflows/ci.yml` |

**作用：** Go 代码静态分析工具，集成多个 linter，在 CI 中用于代码质量检查。

**安装：**
```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

**常用命令：**
```bash
golangci-lint run ./...         # 检查所有代码
golangci-lint run --fix ./...   # 自动修复可修复的问题
```

---

## 前端工具

### Vite

| 项目 | 说明 |
|---|---|
| **使用者** | 前端 A / 前端 B |
| **涉及模块** | `web/admin-console`、`web/user-console` |
| **涉及功能** | 开发服务器、生产构建、TypeScript 编译 |
| **代码位置** | `web/admin-console/vite.config.ts`、`web/user-console/vite.config.ts` |

**作用：** 前端构建工具，开发时极速热更新（HMR），生产构建使用 Rollup。

**常用命令：**
```bash
npm run dev         # 启动开发服务器（默认 5173 端口）
npm run build       # 生产构建，输出到 dist/
npm run preview     # 本地预览生产构建结果
npm run type-check  # TypeScript 类型检查
npm run lint        # 代码规范检查
```

---

### Vue 3

| 项目 | 说明 |
|---|---|
| **使用者** | 前端 A / 前端 B |
| **涉及模块** | `web/admin-console`、`web/user-console` |
| **涉及功能** | 所有页面组件、UI 逻辑、响应式数据管理 |
| **代码位置** | `web/*/src/views/`、`web/*/src/components/` |

**作用：** 前端框架，使用 Composition API（`<script setup>`），项目中用于管理后台和用户控制台两个应用。

**常用用法：**
```vue
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
const count = ref(0)                           // 响应式数据
const double = computed(() => count.value * 2) // 计算属性
onMounted(() => { /* 页面加载后执行 */ })
</script>
```

**文档：** https://cn.vuejs.org/

---

### Pinia

| 项目 | 说明 |
|---|---|
| **使用者** | 前端 A / 前端 B |
| **涉及模块** | `web/admin-console`、`web/user-console` |
| **涉及功能** | 用户登录状态、Access Token 存储、实名认证状态、全局用户信息 |
| **代码位置** | `web/*/src/stores/auth.ts`、`web/*/src/stores/user.ts` |

**作用：** Vue 3 官方推荐状态管理库，管理用户登录态、Token、权限等全局状态。

**常用用法：**
```typescript
export const useAuthStore = defineStore('auth', () => {
  const token = ref('')
  const login = async (data) => { /* ... */ }
  return { token, login }
})
const auth = useAuthStore()
auth.login(formData)
```

---

### Vue Router

| 项目 | 说明 |
|---|---|
| **使用者** | 前端 A / 前端 B |
| **涉及模块** | `web/admin-console`、`web/user-console` |
| **涉及功能** | 页面路由、登录守卫（未登录跳 /login）、实名守卫（未实名跳 /identity） |
| **代码位置** | `web/*/src/router/index.ts` |

**作用：** Vue 3 官方路由库，项目中用于路由守卫保护需要登录或实名认证的页面。

**常用用法：**
```typescript
router.push('/login')               // 编程式跳转
router.replace('/dashboard')        // 替换当前历史记录（不留历史记录）
const route = useRoute()            // 获取当前路由信息
route.params.id / route.query.page  // 读取路由参数
```

---

### Element Plus

| 项目 | 说明 |
|---|---|
| **使用者** | 前端 A（为主） / 前端 B |
| **涉及模块** | `web/admin-console`（全量使用）、`web/user-console`（部分使用） |
| **涉及功能** | 数据表格、分页、表单校验、弹窗确认、消息提示 |
| **代码位置** | `web/*/src/views/` 全部页面组件 |

**作用：** Vue 3 UI 组件库，用于快速搭建管理后台界面。

**常用组件：**
```vue
<el-table :data="list">                <!-- 数据表格 -->
<el-pagination :total="total">         <!-- 分页 -->
<el-form :model="form" :rules="rules"> <!-- 带校验的表单 -->
<el-dialog v-model="visible">          <!-- 弹窗 -->
ElMessage.success('操作成功')           <!-- 提示消息 -->
ElMessageBox.confirm('确认删除？')      <!-- 确认框 -->
```

**文档：** https://element-plus.org/zh-CN/

---

### Axios

| 项目 | 说明 |
|---|---|
| **使用者** | 前端 A / 前端 B |
| **涉及模块** | `web/admin-console`、`web/user-console` |
| **涉及功能** | 所有 API 请求、Bearer Token 自动注入、401 自动刷新 Token（用户控制台）、统一错误提示 |
| **代码位置** | `web/*/src/api/http.ts` |

**作用：** HTTP 请求库，封装了统一拦截器（Token 注入、自动刷新、错误提示）。

**常用用法：**
```typescript
http.get('/api/v1/users', { params: { page: 1 } })    // GET 请求
http.post('/api/v1/auth/login', { email, password })   // POST 请求
http.put('/api/v1/users/1', data)                      // PUT 请求
http.delete('/api/v1/users/1')                         // DELETE 请求
```

---

### TypeScript

| 项目 | 说明 |
|---|---|
| **使用者** | 前端 A / 前端 B |
| **涉及模块** | `web/admin-console`、`web/user-console` |
| **涉及功能** | 接口类型定义、API 响应类型、组件 Props 类型校验 |
| **代码位置** | `web/*/src/types/`、所有 `.ts` / `.vue` 文件 |

**作用：** JavaScript 的类型超集，在编译阶段发现类型错误，提升代码可维护性。

**常用命令：**
```bash
npx tsc --noEmit   # 只做类型检查，不输出文件
npx tsc --watch    # 监听模式，实时检查类型
```

---

## 基础设施工具

### Docker

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 |
| **涉及模块** | `infra/` |
| **涉及功能** | 构建后端/前端镜像，生产环境容器化部署 |
| **代码位置** | `infra/Dockerfile.server`、`infra/Dockerfile.admin-console`、`infra/Dockerfile.user-console` |

**作用：** 容器化工具，将应用和依赖打包为镜像，保证开发/测试/生产环境一致。

**常用命令：**
```bash
docker build -t molin-server -f infra/Dockerfile.server .  # 构建后端镜像
docker images                          # 查看本地镜像
docker ps                              # 查看运行中的容器
docker logs -f <容器名>                 # 实时查看容器日志
docker exec -it <容器名> sh            # 进入容器终端
docker rm -f <容器名>                  # 强制删除容器
```

---

### Docker Compose

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 全体开发者（本地环境启动） |
| **涉及模块** | `infra/` |
| **涉及功能** | 本地开发环境一键启动（MySQL/Redis/RabbitMQ/MinIO）、生产环境编排 |
| **代码位置** | `infra/docker-compose.yml`（本地）、`infra/docker-compose.prod.yml`（生产） |

**作用：** 多容器编排工具，一键启动本地开发所需的全部依赖服务。

**常用命令：**
```bash
docker compose up -d                   # 后台启动所有服务
docker compose up -d mysql redis       # 只启动指定服务
docker compose down                    # 停止并删除容器
docker compose down -v                 # 停止并删除容器及数据卷（会清空数据，慎用）
docker compose logs -f api             # 实时查看 api 服务日志
docker compose ps                      # 查看所有服务状态
```

---

### Nginx

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 |
| **涉及模块** | `infra/nginx/` |
| **涉及功能** | 管理后台静态文件托管、用户控制台静态文件托管 + SSE 长连接代理、API 反向代理 |
| **代码位置** | `infra/nginx/admin.conf`、`infra/nginx/user.conf` |

**作用：** 反向代理服务器，托管前端静态文件，转发 API 请求，支持 SSE 长连接。

**常用命令：**
```bash
nginx -t                               # 检查配置文件语法
nginx -s reload                        # 热重载配置（不中断服务）
docker exec nginx nginx -s reload      # 在容器内重载配置
```

**关键配置（用户控制台 SSE 支持）：**
```nginx
proxy_buffering off;       # 关闭缓冲，SSE 实时推送必须开启
proxy_read_timeout 300s;   # 长连接超时时间
```

---

### MySQL

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A / 后端 B / 后端 C / 运维 |
| **涉及模块** | 全部后端模块 |
| **涉及功能** | 存储用户、订单、资产、权限等全部核心业务数据，共 35 张表 |
| **代码位置** | `server/migrations/*.sql`、`server/internal/modules/*/model/*.go` |

**作用：** 关系型数据库，存储所有核心业务数据。

**常用命令：**
```bash
mysql -h 127.0.0.1 -P 3306 -u root -p    # 连接数据库
show databases;                            # 查看所有数据库
use molin;                                 # 切换数据库
show tables;                               # 查看所有表
desc users;                                # 查看表结构
explain select * from orders where user_id=1; # 分析查询执行计划
```

---

### Redis

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A |
| **涉及模块** | `iam`、`auth` |
| **涉及功能** | 权限缓存（`perm:user:{userID}`，TTL 5分钟）、验证码存储（TTL 5分钟） |
| **代码位置** | `server/pkg/cache/redis.go`、`server/internal/modules/iam/service/iam_service.go` |

**作用：** 内存缓存数据库，用于权限缓存和验证码存储，减少数据库查询压力。

**常用命令：**
```bash
redis-cli -h 127.0.0.1 -p 6379        # 连接 Redis
keys perm:user:*                       # 查看所有权限缓存 key
get perm:user:123                      # 查看指定用户权限缓存
del perm:user:123                      # 手动清除权限缓存（调试用）
ttl perm:user:123                      # 查看缓存剩余时间（秒）
flushdb                                # 清空当前数据库（开发调试用，慎用）
```

---

### RabbitMQ

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 B / 后端 C |
| **涉及模块** | `billing`、`finance_consumer`、`provision` |
| **涉及功能** | 购买成功后异步触发资产开通（Provision），解耦购买链路和开通链路 |
| **代码位置** | `server/internal/modules/billing/`（发布）、`server/internal/modules/finance_consumer/`（消费） |

**作用：** 消息队列，用于异步处理购买后的资产开通事件，避免购买接口超时。

**管理界面：** http://localhost:15672（默认账号 guest/guest）

**常用命令：**
```bash
rabbitmqctl list_queues name messages consumers  # 查看队列积压情况
rabbitmqctl list_connections                     # 查看连接数
```

---

### MinIO

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 后端 A（实名认证材料上传） |
| **涉及模块** | `identity` |
| **涉及功能** | 存储实名认证上传的身份证照片等文件 |
| **代码位置** | `server/internal/modules/identity/` |

**作用：** 对象存储服务，兼容 AWS S3 API，用于存储用户上传的文件。

**管理界面：** http://localhost:9001

**常用命令：**
```bash
mc alias set local http://localhost:9000 minioadmin minioadmin  # 配置客户端
mc ls local/                             # 查看所有 bucket
mc cp ./file.jpg local/molin-uploads/    # 上传文件
```

---

## 开发辅助工具

### Git

| 项目 | 说明 |
|---|---|
| **使用者** | 全体开发者 |
| **涉及模块** | 全部 |
| **涉及功能** | 版本控制、分支管理、代码提交、PR 流程 |
| **代码位置** | 分支规范见 `docs/git-workflow.md` |

**作用：** 版本控制工具，项目采用 `feature/{开发者标识}-{模块}-{描述}` 分支规范。

**常用命令：**
```bash
git checkout -b feature/backend-a-auth-register  # 新建并切换到功能分支
git branch --show-current                        # 查看当前分支
git status                                       # 查看工作区状态
git add <文件>                                   # 暂存指定文件
git commit -m "新增：用户注册接口"               # 提交（必须使用中文）
git push -u origin feature/backend-a-auth-register # 首次推送并关联远程
git pull origin main                             # 拉取最新 main 代码
git log --oneline -10                            # 查看最近 10 条提交记录
```

---

### GitHub Actions

| 项目 | 说明 |
|---|---|
| **使用者** | 运维（配置）/ 全体开发者（自动触发） |
| **涉及模块** | 全部 |
| **涉及功能** | PR 合并前自动运行后端测试、前端构建、代码检查，防止破坏性代码合并 |
| **代码位置** | `.github/workflows/ci.yml` |

**作用：** CI/CD 自动化流水线，每次 PR 自动触发 3 个并行检查 Job。

**3 个并行 Job：**
- `backend-test`（后端 A/B/C）：go vet + go test -race + go build
- `frontend-admin-build`（前端 A）：type-check + lint + build
- `frontend-user-build`（前端 B）：type-check + lint + build

**查看运行结果：** GitHub 仓库 → Actions 标签页

---

### swag

| 项目 | 说明 |
|---|---|
| **使用者** | 后端 A / 后端 B / 后端 C |
| **涉及模块** | 全部后端 Handler |
| **涉及功能** | 从代码注释自动生成 Swagger 接口文档，前后端对接使用 |
| **代码位置** | `server/cmd/api/main.go`（入口注解）、`server/internal/modules/*/handler/*.go`（接口注解） |

**作用：** 从 Go 代码注释自动生成 Swagger/OpenAPI 接口文档。

**安装：**
```bash
go install github.com/swaggo/swag/cmd/swag@latest
```

**常用命令：**
```bash
swag init -g cmd/api/main.go -o docs/swagger  # 生成 swagger 文档
swag fmt                                       # 格式化 swag 注释
```

**访问地址（开发模式）：** http://localhost:8080/swagger/index.html
