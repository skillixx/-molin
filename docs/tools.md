# 项目工具文档

> 记录项目中使用到的所有工具，包含作用说明和常用命令，方便团队成员快速上手。

---

## 目录

- [后端工具](#后端工具)
  - [Go](#go)
  - [Gin](#gin)
  - [GORM](#gorm)
  - [golang-migrate](#golang-migrate)
  - [Viper](#viper)
  - [golang-jwt](#golang-jwt)
  - [bcrypt](#bcrypt)
  - [golangci-lint](#golangci-lint)
- [前端工具](#前端工具)
  - [Vite](#vite)
  - [Vue 3](#vue-3)
  - [Pinia](#pinia)
  - [Vue Router](#vue-router)
  - [Element Plus](#element-plus)
  - [Axios](#axios)
  - [TypeScript](#typescript)
- [基础设施工具](#基础设施工具)
  - [Docker](#docker)
  - [Docker Compose](#docker-compose)
  - [Nginx](#nginx)
  - [MySQL](#mysql)
  - [Redis](#redis)
  - [RabbitMQ](#rabbitmq)
  - [MinIO](#minio)
- [开发辅助工具](#开发辅助工具)
  - [Git](#git)
  - [GitHub Actions](#github-actions)
  - [swag](#swag)

---

## 后端工具

### Go

**作用：** 后端服务主语言，编译型强类型语言，适合高并发 API 服务开发。

**常用命令：**
```bash
go run ./cmd/api            # 启动后端服务（开发模式）
go build -o bin/api ./cmd/api  # 编译二进制文件
go test ./...               # 运行所有单元测试
go test -race ./...         # 运行测试并检测数据竞争
go test -cover ./...        # 运行测试并输出覆盖率
go mod tidy                 # 整理依赖，移除未使用的包
go mod download             # 下载所有依赖到本地缓存
go vet ./...                # 静态分析，检查常见错误
```

---

### Gin

**作用：** Go HTTP Web 框架，提供路由、中间件、参数绑定、JSON 响应等能力。

**常用用法：**
```go
r := gin.New()                          // 创建路由（不带默认中间件）
r.Use(middleware.Logger())              // 注册全局中间件
v1 := r.Group("/api/v1")               // 路由分组
v1.POST("/auth/register", handler.Register) // 注册路由
r.Run(":8080")                          // 启动服务
```

**文档：** https://gin-gonic.com/zh-cn/docs/

---

### GORM

**作用：** Go ORM 框架，简化数据库 CRUD 操作，支持 MySQL/PostgreSQL/SQLite，内置连接池管理。

**常用用法：**
```go
db.Create(&user)                        // 插入记录
db.First(&user, id)                     // 按主键查询
db.Where("email = ?", email).First(&user) // 条件查询
db.Model(&user).Updates(map)            // 更新指定字段
db.Delete(&user, id)                    // 软删除
db.Begin() / tx.Commit() / tx.Rollback() // 事务
db.Clauses(clause.Locking{Strength: "UPDATE"}).First(&w) // SELECT FOR UPDATE
```

**文档：** https://gorm.io/zh_CN/docs/

---

### golang-migrate

**作用：** 数据库 Migration 版本管理工具，支持 up/down 回滚，确保各环境数据库结构一致。

**安装：**
```bash
go install -tags 'mysql' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

**常用命令：**
```bash
# 执行所有未运行的 migration
migrate -path ./migrations -database "mysql://user:pass@tcp(localhost:3306)/molin" up

# 只执行下一条 migration
migrate -path ./migrations -database "mysql://..." up 1

# 回滚最近一条 migration
migrate -path ./migrations -database "mysql://..." down 1

# 查看当前 migration 版本
migrate -path ./migrations -database "mysql://..." version

# 新建一对 up/down SQL 文件
migrate create -ext sql -dir ./migrations -seq create_users_table
```

---

### Viper

**作用：** Go 配置管理库，支持从 YAML/JSON/ENV 文件和环境变量读取配置，支持热重载。

**常用用法：**
```go
viper.SetConfigFile(".env")             // 指定配置文件
viper.AutomaticEnv()                    // 自动读取同名环境变量
viper.ReadInConfig()                    // 加载配置
viper.GetString("DB_HOST")             // 读取字符串配置
viper.Unmarshal(&cfg)                   // 映射到结构体
```

---

### golang-jwt

**作用：** 生成和解析 JWT Token，用于用户身份认证（Access Token 有效期 2 小时）。

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

**作用：** 密码加密库，使用自适应哈希算法，项目中 cost=12，防止彩虹表攻击。

**常用用法：**
```go
hash, err := bcrypt.GenerateFromPassword([]byte(password), 12) // 加密
err := bcrypt.CompareHashAndPassword(hash, []byte(password))   // 验证，nil 表示匹配
```

---

### golangci-lint

**作用：** Go 代码静态分析工具，集成多个 linter，CI 中用于代码质量检查。

**安装：**
```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

**常用命令：**
```bash
golangci-lint run ./...         # 检查所有代码
golangci-lint run --fix ./...   # 自动修复可修复的问题
golangci-lint run ./internal/... # 只检查指定目录
```

---

## 前端工具

### Vite

**作用：** 前端构建工具，开发时极速热更新（HMR），生产构建使用 Rollup，替代 Webpack。

**常用命令：**
```bash
npm run dev         # 启动开发服务器（默认 5173 端口）
npm run build       # 生产构建，输出到 dist/
npm run preview     # 本地预览生产构建结果
npm run type-check  # TypeScript 类型检查
```

---

### Vue 3

**作用：** 前端框架，使用 Composition API（`<script setup>`），项目中用于管理后台和用户控制台两个应用。

**常用用法：**
```vue
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
const count = ref(0)                    // 响应式数据
const double = computed(() => count.value * 2) // 计算属性
onMounted(() => { /* 页面加载后执行 */ })
</script>
```

**文档：** https://cn.vuejs.org/

---

### Pinia

**作用：** Vue 3 官方推荐状态管理库，替代 Vuex，用于管理用户登录状态、Token、权限等全局状态。

**常用用法：**
```typescript
// 定义 store
export const useAuthStore = defineStore('auth', () => {
  const token = ref('')
  const login = async (data) => { /* ... */ }
  return { token, login }
})

// 使用 store
const auth = useAuthStore()
auth.login(formData)
```

---

### Vue Router

**作用：** Vue 3 官方路由库，管理页面跳转，项目中用于路由守卫（未登录跳登录页、未实名跳认证页）。

**常用用法：**
```typescript
router.push('/login')               // 编程式跳转
router.replace('/dashboard')        // 替换当前历史记录
const route = useRoute()            // 获取当前路由信息
route.params.id / route.query.page  // 读取路由参数
```

---

### Element Plus

**作用：** Vue 3 UI 组件库，提供表格、表单、弹窗、分页等组件，用于快速搭建管理后台界面。

**常用组件：**
```vue
<el-table :data="list">             <!-- 数据表格 -->
<el-pagination :total="total">      <!-- 分页 -->
<el-form :model="form" :rules="rules"> <!-- 带校验的表单 -->
<el-dialog v-model="visible">       <!-- 弹窗 -->
<el-message-box>                    <!-- 确认框 -->
ElMessage.success('操作成功')        <!-- 提示消息 -->
```

**文档：** https://element-plus.org/zh-CN/

---

### Axios

**作用：** HTTP 请求库，项目中封装了统一拦截器（Bearer Token 注入、401 自动刷新、错误提示）。

**常用用法：**
```typescript
http.get('/api/v1/users', { params: { page: 1 } })   // GET 请求
http.post('/api/v1/auth/login', { email, password })  // POST 请求
http.put('/api/v1/users/1', data)                     // PUT 请求
http.delete('/api/v1/users/1')                        // DELETE 请求
```

---

### TypeScript

**作用：** JavaScript 的类型超集，在编译阶段发现类型错误，提升代码可维护性。

**常用命令：**
```bash
npx tsc --noEmit      # 只做类型检查，不输出文件
npx tsc --watch       # 监听模式，实时检查类型
```

---

## 基础设施工具

### Docker

**作用：** 容器化工具，将应用和依赖打包为镜像，保证开发/测试/生产环境一致。

**常用命令：**
```bash
docker build -t molin-server -f infra/Dockerfile.server . # 构建镜像
docker images                           # 查看本地镜像
docker ps                               # 查看运行中的容器
docker ps -a                            # 查看所有容器（含已停止）
docker logs -f <容器名>                  # 实时查看容器日志
docker exec -it <容器名> sh             # 进入容器终端
docker rm -f <容器名>                   # 强制删除容器
docker rmi <镜像名>                     # 删除镜像
```

---

### Docker Compose

**作用：** 多容器编排工具，用于一键启动本地开发所需的 MySQL/Redis/RabbitMQ/MinIO 等服务。

**常用命令：**
```bash
docker compose up -d                    # 后台启动所有服务
docker compose up -d mysql redis        # 只启动指定服务
docker compose down                     # 停止并删除容器
docker compose down -v                  # 停止并删除容器及数据卷（慎用，会清空数据）
docker compose logs -f api              # 实时查看 api 服务日志
docker compose restart api             # 重启指定服务
docker compose ps                       # 查看所有服务状态
```

---

### Nginx

**作用：** 反向代理服务器，项目中用于前端静态文件托管、API 请求转发、SSE 长连接代理。

**常用命令：**
```bash
nginx -t                                # 检查配置文件语法
nginx -s reload                         # 热重载配置（不中断服务）
nginx -s stop                           # 停止 Nginx
docker exec nginx nginx -s reload       # 在容器内重载配置
```

**关键配置（用户控制台 SSE 支持）：**
```nginx
proxy_buffering off;        # 关闭缓冲，SSE 实时推送必须
proxy_read_timeout 300s;    # 长连接超时时间
```

---

### MySQL

**作用：** 关系型数据库，存储用户、订单、资产、权限等核心业务数据，项目共 35 张表。

**常用命令：**
```bash
mysql -h 127.0.0.1 -P 3306 -u root -p  # 连接数据库
show databases;                          # 查看所有数据库
use molin;                               # 切换数据库
show tables;                             # 查看所有表
desc users;                              # 查看表结构
show create table users\G               # 查看建表语句
explain select * from orders where user_id=1; # 分析查询执行计划
```

---

### Redis

**作用：** 内存缓存数据库，项目中用于权限缓存（`perm:user:{userID}`，TTL 5分钟）和验证码存储。

**常用命令：**
```bash
redis-cli -h 127.0.0.1 -p 6379         # 连接 Redis
keys perm:user:*                        # 查看所有权限缓存 key
get perm:user:123                       # 查看指定用户权限缓存
del perm:user:123                       # 手动清除权限缓存
ttl perm:user:123                       # 查看缓存剩余时间（秒）
flushdb                                 # 清空当前数据库（开发调试用，慎用）
```

---

### RabbitMQ

**作用：** 消息队列，用于异步处理购买后的资产开通事件（Provision），解耦购买链路和开通链路。

**管理界面：** http://localhost:15672（默认账号 guest/guest）

**常用命令：**
```bash
# 查看队列状态
rabbitmqctl list_queues name messages consumers

# 查看连接
rabbitmqctl list_connections

# 重置（开发调试用）
rabbitmqctl reset
```

---

### MinIO

**作用：** 对象存储服务，兼容 AWS S3 API，用于存储用户上传的文件（实名认证材料、应用图标等）。

**管理界面：** http://localhost:9001

**常用命令：**
```bash
# 使用 mc 客户端
mc alias set local http://localhost:9000 minioadmin minioadmin
mc ls local/                            # 查看所有 bucket
mc cp ./file.jpg local/molin-uploads/   # 上传文件
mc rm local/molin-uploads/file.jpg      # 删除文件
```

---

## 开发辅助工具

### Git

**作用：** 版本控制工具，项目采用 `feature/{开发者标识}-{模块}-{描述}` 分支规范。

**常用命令：**
```bash
git checkout -b feature/backend-a-auth-register  # 新建并切换到功能分支
git branch --show-current                         # 查看当前分支
git status                                        # 查看工作区状态
git add <文件>                                    # 暂存指定文件
git commit -m "新增：用户注册接口"                # 提交（必须使用中文）
git push -u origin feature/backend-a-auth-register # 首次推送并关联远程
git pull origin main                              # 拉取最新 main 代码
git log --oneline -10                             # 查看最近 10 条提交记录
git diff                                          # 查看未暂存的改动
```

---

### GitHub Actions

**作用：** CI/CD 自动化流水线，每次 PR 自动触发后端测试、前端构建、代码检查，配置文件位于 `.github/workflows/ci.yml`。

**触发条件：** push 到任意分支 或 创建/更新 PR 时自动运行

**3 个并行 Job：**
- `backend-test`：go vet + go test -race + go build
- `frontend-admin-build`：type-check + lint + build
- `frontend-user-build`：type-check + lint + build

**查看运行结果：** GitHub 仓库 → Actions 标签页

---

### swag

**作用：** 从 Go 代码注释自动生成 Swagger/OpenAPI 接口文档，方便前后端对接。

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
