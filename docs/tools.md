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

### 短信阶段 5 关闭态只读审计

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 / 产品经理 |
| **涉及模块** | 短信、认证、Nginx、Docker 网络、Prometheus |
| **涉及功能** | 测试服关闭态发布复核、零发送观察、部署漂移检测 |
| **代码位置** | `scripts/verify-sms-phase5-test-server-readonly.ps1`、配套 `.sh` |

**作用：** 通过 SSH 执行只读聚合检查，精确核对阶段 5 二进制、固定代理网络与 IP、模板/绑定、短信指标、
Prometheus 告警与抓取状态，并比较观察窗口前后的发送日志和 Provider 调用总数。脚本不输出 Token、数据库密码、
手机号或验证码；内部 Token 和数据库密码只通过 stdin 传递，不进入宿主机命令参数。窗口结束会再次确认 API PID、
短信开关、代理健康和 Prometheus 目标，任一安全条件不符合时返回失败。

```powershell
# 即时关闭态核验；建议锁定部署清单记录的二进制哈希
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-readonly.ps1 `
  -ExpectedBinarySHA256 <已核验的64位SHA256>

# 5 分钟只读观察；不会修改配置或发送短信
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-readonly.ps1 `
  -ExpectedBinarySHA256 <已核验的64位SHA256> -ObservationSeconds 300
```

真实短信、开启 `SMS_ENABLED`、生产目标连接以及 Git 推送不属于该工具权限范围，仍需独立人工授权。

---

### 短信阶段 5 敏感信息与关闭态门禁

| 项目 | 说明 |
|---|---|
| **使用者** | 开发 / 测试 / 产品经理 |
| **涉及模块** | 阶段分支、前端构建产物、CI、短信发布准备度 |
| **涉及功能** | 敏感值离线扫描、受保护环境文件检查、短信关闭态静态检查 |
| **代码位置** | `scripts/verify-sms-phase5-sensitive-data.py`、`tests/sms/phase5_sensitive_data_gate_contract.py` |

**作用：** 只读比较阶段分支与指定基线，按每个提交相对第一父提交的实际路径扫描新 blob（包括合并冲突解决）和提交说明，并分别扫描 Git index、未暂存/未忽略
工作树文件及 admin/user `dist` 产物；因此“先提交秘密再删除”“复用基线 blob”“暂存秘密后覆盖工作树”均不能绕过门禁。
同时检查全部 Git 跟踪及历史中是否存在受保护环境文件。发现真实凭据、JWT、裸阿里云 AccessKey ID、不透明 Bearer Token、
完整手机号、验证码、供应商原始正文、文本文件 NUL、危险 `SMS_ENABLED` 或关闭 `SMS_TEST_MODE` 时返回失败；布尔值判断与 Go
运行时 `1/t/true/y/yes/on` 真值表一致，赋值语法覆盖 env、JSON、flow YAML、PowerShell 和命令前缀。裸凭据形态命中即拒绝，
不再按 `test/example` 等子串放行。输出只包含规则分类、行号、Git 对象摘要和路径 SHA-256；路径安全时附相对路径，手机号、OTP、
凭据形态及 Unicode 控制/行/段分隔符路径仅显示脱敏标记，不打印命中正文。只读包装器的禁止模式字面量会经过上下文识别，
不会被误报为真实开启短信。

```powershell
# 独立扫描阶段分支；不连接服务器、不读取被 Git 忽略的真实环境文件
python scripts/verify-sms-phase5-sensitive-data.py --repo-root . --base-ref origin/main --require-dist

# 与阶段 5 准备度一起运行；CI 使用完整 Git 历史并先重新构建两套 dist
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-readiness.ps1 `
  -SelfTest -RunSensitiveScan -SensitiveScanBaseRef origin/main
```

该门禁通过只证明指定 Git 差异和本地构建产物未命中规则，不等同于测试服/生产日志、密钥系统或短信供应商侧完成审计。

---

### 短信阶段 5 回滚与日志留存门禁

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 / 产品经理 |
| **涉及模块** | 测试服回滚、固定代理、journald、告警通知链 |
| **涉及功能** | 回滚材料只读预检、安全回滚候选、旧二进制运行/当前版本恢复演练候选、日志留存策略只读审计与获批后受控变更 |
| **代码位置** | `scripts/sms-phase5-test-server-ssh.ps1`、`scripts/verify-sms-phase5-test-server-recovery-readiness.ps1`、`scripts/prepare-sms-phase5-test-server-rollback-candidate.ps1`、`scripts/verify-sms-phase5-test-server-rollback-candidate.ps1`、`scripts/prepare-sms-phase5-test-server-rollback-drill.ps1`、`scripts/run-sms-phase5-test-server-rollback-drill.sh`、`scripts/stage-sms-phase5-test-server-rollback-drill.ps1`、`scripts/stage-sms-phase5-test-server-rollback-drill.sh`、`scripts/execute-sms-phase5-test-server-rollback-drill.ps1`、`scripts/verify-sms-phase5-test-server-rollback-drill.ps1`、`scripts/verify-sms-phase5-test-server-rollback-drill.sh`、`scripts/verify-sms-phase5-test-server-log-retention.ps1`、`scripts/apply-sms-phase5-test-server-log-retention.ps1` 及相关 Bash payload |

**作用：** 将“材料存在”“候选可生成”“配置完整”“运行时已验证”拆成独立证据，禁止用旧环境整份覆盖当前固定代理配置，
也禁止把 journald 配置存在误报为策略已批准。六个阶段 5 远端包装器统一调用共享 SSH 目标与 ED25519 指纹校验，避免固定身份规则分叉；各入口都支持或配有离线安全契约，真实候选生成会写远端文件，必须单独授权。回滚候选验证器按 UTC ChangeId 只读核对文件类型、700/600 权限、SHA-256、短信关闭态、固定代理、必要发布键、废弃键和重复键，不输出任何环境值。日志留存变更入口还支持
`-ExportOperatorPayload`：在固定授权短语通过后，把四项批准值和测试服 machine-id 摘要冻结为本地运维脚本；该模式与
`-Apply` 和 `-SelfTest` 互斥，不读取 `known_hosts`、不连接远端，只接受完全限定的本机绝对路径，并拒绝 UNC、设备、映射网络驱动器和含重解析祖先的路径，
也拒绝覆盖已有文件，并输出 SHA-256 供安全传输后复核。导出成功不代表部署完成。

回滚恢复演练生成器同样只在本地冻结交接脚本：ChangeId、旧/新二进制摘要、现有关闭态候选、Alertmanager `discard`
配置摘要、10 秒旧版本稳定窗口和 10 秒恢复后稳定窗口全部写入同一候选。候选的 `--preflight` 只读检查固定测试服、
关闭态、活动告警、通知计数、数据库发送摘要和磁盘余量；`--execute` 还要求输入与 ChangeId 绑定的精确批准短语。
执行器只向精确 API PID 发送 TERM，超时后才对再次核验的同一 PID 使用 KILL；任何失败都优先恢复当前二进制和原进程环境。
它不替换 `infra/.env.test`，不 POST 告警或业务接口，不发送邮件或短信。交接候选通过只读预检仍不构成服务重启授权。
暂存包装器只在独立批准后排他创建远端暂存目录、上传固定摘要 runner，并运行语法、自测和关闭态只读预检；失败清理不用
递归删除。执行后独立验证器只读复核证据文件集合、当前二进制/环境摘要、双代理、数据库、Provider、Alertmanager、
Prometheus 和日志敏感值，不以 runner 自报成功替代运行态证据。
实际执行包装器默认关闭，固定 ChangeId、摘要、主机身份和唯一执行次数；成功后强制进入独立证据验收，失败后仅执行一次
关闭态恢复只读审计并以失败结束，任何结果都禁止自动重试。

```powershell
# 本地离线检查，不连接测试服
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-recovery-readiness.ps1 -SelfTest
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prepare-sms-phase5-test-server-rollback-candidate.ps1 -SelfTest
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-rollback-candidate.ps1 -SelfTest
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prepare-sms-phase5-test-server-rollback-drill.ps1 `
  -ChangeId 20990101T000000Z -SelfTest
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/stage-sms-phase5-test-server-rollback-drill.ps1 -SelfTest
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/execute-sms-phase5-test-server-rollback-drill.ps1 -SelfTest
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-rollback-drill.ps1 -SelfTest
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-log-retention.ps1 -SelfTest

# 已获只读测试服审计授权时使用；可能增加 SSH 访问审计日志，但不修改业务配置或发送短信
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-recovery-readiness.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-rollback-candidate.ps1 -ChangeId 20260805T015043Z
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-log-retention.ps1

# 默认只展示候选值，不连接测试服；真实变更还需 -Apply 和固定授权短语
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/apply-sms-phase5-test-server-log-retention.ps1

# sudo 自动化入口不可用时，向受控绝对路径离线导出；占位路径须由运维替换，导出不会连接测试服
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/apply-sms-phase5-test-server-log-retention.ps1 `
  -ExportOperatorPayload C:\受控目录\apply-journald-retention.sh `
  -Authorization APPROVE_TEST_JOURNALD_RETENTION
```

交接包必须通过批准的安全渠道传输，并由有权限的运维在同一获批测试服本地终端核对 SHA-256、完成组织规定的 sudo 身份验证后执行；
不得在命令、聊天、文件或仓库中传递 sudo 密码。实际回滚、候选生成、journald 配置变更或重载、Alertmanager 部署/演练、
短信开关及真实发送均不属于只读工具权限。

---

### 短信阶段 5 Alertmanager 通知演练只读预检

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 / 产品经理 |
| **涉及模块** | Alertmanager、Prometheus、短信关闭态 |
| **涉及功能** | 在申请一次 firing/resolved 邮件演练授权前验证关闭态和零通知基线 |
| **代码位置** | `scripts/verify-sms-phase5-alertmanager-drill-readiness.ps1`、`.sh` |

**作用：** 固定测试服、Alertmanager 部署 ChangeId、容器、端口和镜像摘要，只读核对根路由为 `discard`、邮件 receiver
已加载、配置不含明文 Secret、Secret 文件为 `pc:pc:400`、容器最小权限、Prometheus 活跃目标为 1、活动告警为 0、
通知累计为 0，以及 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`。脚本没有告警 POST、配置 reload、容器变更或通知发送能力；
通过时仍固定输出 `notification_drill_execution_authorization_required=true` 和 `receiver_delivery_unverified=true`。

```powershell
# 本地冻结资产检查，不连接测试服
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-alertmanager-drill-readiness.ps1 -SelfTest

# 固定测试服只读预检
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-alertmanager-drill-readiness.ps1
```

---

### 短信阶段 5 Alertmanager 演练载荷转换校验

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 |
| **涉及模块** | Alertmanager 合成告警演练 |
| **涉及功能** | 在提交告警前离线验证唯一 firing/resolved、精确标签和有效时间顺序 |
| **代码位置** | `scripts/verify-sms-phase5-alertmanager-drill-payload.ps1` |

**作用：** 只读解析本地候选 JSON，要求 firing 与 resolved 各恰好一条、标签严格匹配测试环境和 ChangeId、两者
`startsAt` 一致，并拒绝 `resolved.endsAt <= resolved.startsAt`。该检查专门防止把 Alertmanager 的 HTTP 200
误判为有效 resolved 状态；不连接测试服，不包含告警 POST、配置重载、邮件或短信发送能力。

```powershell
# 内置正反例自测
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-alertmanager-drill-payload.ps1 -SelfTest

# 校验本次受控候选载荷
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-alertmanager-drill-payload.ps1 `
  -FiringPayloadPath C:\受控候选目录\firing-alert.json `
  -ResolvedPayloadPath C:\受控候选目录\resolved-alert.json `
  -ChangeId <UTC_CHANGE_ID>
```

---

### 短信阶段 5 Canary 只读聚合预检

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 / 产品经理 |
| **涉及模块** | 短信关闭态、固定代理、Prometheus、Alertmanager、journald、回滚材料 |
| **涉及功能** | 在申请真实 Canary 授权前聚合判断全部技术前置条件 |
| **代码位置** | `scripts/verify-sms-phase5-test-server-canary-preflight.ps1` |

**作用：** 复用四个既有只读验证器，以失败关闭方式聚合关闭态、零发送增量、回滚候选、回滚材料、监控、通知演练
和日志留存状态。依赖输出必须符合严格键值协议，异常文本、重复键或缺键都会阻断。该工具不包含 HTTP 发送、短信开关
变更、配置写入或服务重启能力；`canary_preflight_ready=true` 也不等于获准真实发送。Alertmanager 运行态只能证明
传输存在，真实演练完成后还必须提供精确人工确认短语、UTC ChangeId、仓库外证据 JSON 路径和独立证据 SHA-256。
工具会校验清单摘要、24 小时有效期，逐份读取五层仓库外原始证据并复算独立摘要，再验证关闭态约束；不能用配置
存在、任意占位摘要或不存在的证据文件冒充通知可达。

```powershell
# 本地行为自测，不连接测试服
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-canary-preflight.ps1 -SelfTest

# 固定测试服只读聚合；当前任一门禁缺失时以退出码 2 失败关闭
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-test-server-canary-preflight.ps1
```

---

### 短信阶段 5 Canary 执行计划门禁

| 项目 | 说明 |
|---|---|
| **使用者** | 产品经理 / 运维 / 测试 |
| **涉及模块** | 五个短信业务场景、白名单目标角色、发送预算和业务状态变更边界 |
| **涉及功能** | 在生成真实发送执行器前阻断单号码状态冲突、超预算、重试和敏感值持久化 |
| **代码位置** | `scripts/verify-sms-phase5-canary-execution-plan.ps1` |

**作用：** 仅离线验证脱敏计划文件。`register` 的未注册目标不能与 `login/reset_password/admin_verify` 的已注册目标
复用同一号码别名；若选择完整 OTP 消费，还必须显式批准一次性账号、业务状态变更和恢复。本工具不连接测试服、不切换
短信开关、不调用 HTTP 或供应商。详细边界见 `docs/sms-phase5-canary-execution-design.md`。

```powershell
# 本地正反例自测；不会创建计划、连接网络或发送短信
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-canary-execution-plan.ps1 -SelfTest

# 对仓库外的脱敏 JSON 计划执行静态验证
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-sms-phase5-canary-execution-plan.ps1 `
  -PlanFile C:\受控目录\canary-plan.json
```

---

### 短信阶段 5 五档观察证据校验

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 / 产品经理 |
| **涉及模块** | 短信累计指标、发送日志、Alertmanager、health/ready、阶段 5 观察报告 |
| **涉及功能** | 校验 5 分钟、15 分钟、30 分钟、2 小时和 24 小时低敏证据 |
| **代码位置** | `scripts/verify-sms-phase5-observation-evidence.py` |

**作用：** 对仓库外 JSON 做纯本地契约检查，验证时间覆盖、发送日志守恒、计数单调、关闭态零新增、Provider 失败率和
平均耗时停止线、活动告警、通知失败及最终开关状态。测试服关闭态恢复会重启 API，因此持久发送日志必须精确为基线加 5，
当前进程 Provider 计数允许从 0 开始但五窗口不得增长；生产启用态仍要求 Provider 增量与发送日志一致。它不读取线上指标、不连接供应商，也不能把人工填写的 JSON 变成
真实运行证据；必须与原始只读快照、运行状态和收件确认共同复核。

```powershell
python scripts/verify-sms-phase5-observation-evidence.py --self-test
python scripts/verify-sms-phase5-observation-evidence.py --evidence C:\受控目录\phase5-observation.json
```

---

### 短信阶段 5 五档观察快照与证据组装工具链

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 / 产品经理 |
| **涉及模块** | 测试服关闭态、发送日志、Provider 指标、Prometheus、Alertmanager、人工收件证据 |
| **涉及功能** | 生成冻结的五窗口只读 runner，并在 24h 后离线生成最终状态与完整观察证据 |
| **代码位置** | `scripts/prepare-sms-phase5-observation-snapshot-readonly.ps1`、`scripts/verify-sms-phase5-observation-progress.py`、`scripts/assemble-sms-phase5-final-state.py`、`scripts/assemble-sms-phase5-observation-evidence.py` |

**作用：** 快照生成器只在源 Canary 成功结果摘要匹配时生成工作区外 runner。runner 固定
`5m/15m/30m/2h/24h` 五个最小经过时间窗口，每个窗口最多执行一次、只允许固定 SSH stdin 单连接和只读查询，
不内置等待或自动重试。实际连接必须逐窗口取得包含 ChangeId 和完整 runner SHA-256 的人工授权。

24h 快照完成后，最终状态组装器以源 Canary、修正版事后核验和 24h 快照三项完整摘要为输入，拒绝关闭态新增发送、
计数回退、OTP 消费、告警或通知失败；它只离线写出七个低敏最终状态字段。完整观察组装器随后要求五个快照、人工逐场景
收件确认、最终状态及全部摘要，并复用权威验证器生成最终 JSON。两个组装器均不联网、不发送短信，也不能替代原始证据。

观察尚未满五档时，可使用进度验证器核验从 5m 开始的连续前缀。它只读取工作区外源结果和快照，不创建补齐文件、
不伪造未来窗口；每个已有窗口仍必须提供完整 SHA-256。

```powershell
# 三个入口的离线自测；不会连接测试服或创建真实证据
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-observation-snapshot-readonly.ps1 -SelfTest
python scripts/assemble-sms-phase5-final-state.py --self-test
python scripts/assemble-sms-phase5-observation-evidence.py --self-test
python scripts/verify-sms-phase5-observation-progress.py --self-test
```

真实组装必须使用工作区外的新输出路径和全部输入文件的完整 SHA-256；命令参数不得包含手机号、Token、OTP 或供应商原始响应。

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

### 阶段 5 生产目标元数据候选生成器

| 项目 | 说明 |
|---|---|
| **用途** | 在任何生产连接前冻结目标身份、路径、服务形态以及回滚/观察操作者低敏别名 |
| **使用者** | 运维工程师、测试工程师、产品经理 |
| **涉及功能** | 生产只读基线、关闭态部署、白名单 Canary 和生产开启的前置身份边界 |
| **代码位置** | `scripts/prepare-sms-phase5-production-target-intake.ps1` |

默认入口和 `-SelfTest` 均不联网、不提示输入、不创建候选。实际导出只接受非密钥元数据，并把生产 SSH 地址/端口/用户、
唯一 ED25519 指纹、项目目录、项目内 `.env.prod` 路径、服务形态、API 服务唯一标识、API/Prometheus/Alertmanager 本机端口和操作者别名写入全新的工作区外 JSON。候选固定
`SMS_ENABLED=false`、`SMS_TEST_MODE=true`、零重试和零发送，并明确生产只读、部署、Canary、正式开启均未获授权。
密码、私钥、Token、手机号和环境值不得作为参数或输出；生成候选也不会验证生产真实状态。

```powershell
# 仅运行离线正反例，不生成候选或连接生产
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-production-target-intake.ps1 -SelfTest
```

### 阶段 5 生产关闭态只读基线候选生成器

| 项目 | 说明 |
|---|---|
| **用途** | 从摘要冻结的生产目标元数据生成单次 SSH、低敏输出的关闭态只读基线 runner |
| **使用者** | 运维工程师、测试工程师、产品经理 |
| **涉及功能** | 生产环境/进程一致性、health/ready、schema、模板绑定、发送聚合、指标、Prometheus 与 Alertmanager |
| **代码位置** | `scripts/prepare-sms-phase5-production-readonly-baseline.ps1` |

默认入口与 `-SelfTest` 只验证本地契约，不读取 `known_hosts`、不连接生产、不写候选。实际导出必须绑定生产目标候选完整
SHA-256，并只在全新的工作区外目录生成一个 runner。runner 默认关闭；后续单独批准 `-ExecuteReadOnly` 后，才重新核验
唯一 ED25519 指纹并固定 SSH stdin 连接一次。远端负载只读取 `.env.prod` 与进程一致性、服务状态、本机健康接口、数据库
schema/模板/绑定/发送聚合、内部指标和监控状态，输出固定布尔与聚合计数；不上传、不修改配置、不执行服务操作或业务 POST，
不发送邮件或短信。执行时必须向全新本地绝对路径以 `CreateNew` 排他保存字段白名单 JSON 和 SHA-256，禁止覆盖；备份可恢复性仍需人工证据，不能由只读 runner 自动推定。

```powershell
# 仅运行离线正反例，不生成或执行生产 runner
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-production-readonly-baseline.ps1 -SelfTest
```

### 阶段 5 生产关闭态部署计划生成器

| 项目 | 说明 |
|---|---|
| **用途** | 绑定生产目标、关闭态只读结果和全部发布制品摘要，生成默认未授权的部署计划 |
| **使用者** | 运维工程师、测试工程师、产品经理 |
| **涉及功能** | 生产关闭态部署、migration 决策、回滚与后续 Canary 人工门禁 |
| **代码位置** | `scripts/prepare-sms-phase5-production-closed-deployment-plan.ps1` |

默认入口和 `-SelfTest` 均不读取生产证据、不联网、不创建计划。实际导出必须同时绑定生产目标候选、实际只读 runner 与
结果的完整 SHA-256，并由同一权威只读基线生成器按目标候选与 ChangeId 重新生成 runner；完整文件摘要精确一致后，计划还会冻结生成器自身摘要，避免仅靠注释或死代码伪造结构标记。随后固定发布提交、API 制品、两套前端镜像、备份证据及回滚证据摘要。schema 已达到 59 时只能
`verify-only`；仅当前 schema 精确为 58 时允许规划 `apply-up-to-59`，并只允许 `schema_ready=false` 单项阻断。低于 58
必须先走独立 migration 方案，其他阻断结果也不能生成部署计划。

输出计划保持 `SMS_ENABLED=false`、`SMS_TEST_MODE=true`、数据库模板源、五模板/五绑定、四条告警、零自动重试和失败自动
回滚。计划生成不等于部署、migration、Canary 或正式开启授权，备份证据摘要也不等于恢复能力已经验证。

```powershell
# 仅运行离线正反例，不读取生产证据或生成真实计划
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-production-closed-deployment-plan.ps1 -SelfTest
```

### 阶段 5 Canary 双号码本地预检候选生成器

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 / 产品负责人 |
| **涉及功能** | 为真实收件 Canary 生成默认关闭的双号码隐藏输入 runner |
| **代码位置** | `scripts/prepare-sms-phase5-canary-target-preflight.ps1` |

生成器绑定 ChangeId、脱敏计划文件和计划 SHA-256，只允许写入全新的本地目录。生成时执行语法、默认关闭和合成值自测；不采集真实手机号、不连接测试服、不修改白名单、不上传、不打开短信开关、不发送短信。生成的 runner 仅在后续独立批准 `-Interactive` 后，才通过隐藏输入在内存中校验两个号码的格式与互异性。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-canary-target-preflight.ps1 -SelfTest
```

### 阶段 5 Canary 固定测试服双号码状态只读候选生成器

| 项目 | 说明 |
|---|---|
| **使用者** | 运维 / 测试 / 产品负责人 |
| **涉及功能** | 生成固定 SSH 身份、默认关闭的双号码注册/IAM/白名单只读预检 runner |
| **代码位置** | `scripts/prepare-sms-phase5-canary-target-state-readonly.ps1` |

生成器只在全新的本地目录写入候选，并执行 PowerShell 语法、只读 SQL、默认关闭和合成状态自测。runner 只允许单次 SSH stdin 内存传值，远端仅执行 `SELECT`，不会上传文件、修改白名单、调用业务 POST、改变短信开关或发送短信。`-ExecuteReadOnly` 属于后续独立人工门禁，本地生成授权不能直接用于执行。

实际只读执行必须先按 `docs/sms-phase5-canary-execution-design.md` 核对完整 runner SHA-256 并取得独立批准。ChangeId `20260805T132831Z` / runner `4fc5c444...d8e9c` 与 ChangeId `20260805T164138Z` / runner `d00ff59a...7f34` 的一次性执行授权均已消费且返回退出码 2，禁止重试。后者确认是 SSH 参数链丢失 Bash 换行，未进入状态查询。生成器现使用 LF/无 BOM 标准输入把完整脚本交给远端 `bash -s`，并保留失败关闭前输出低敏结果和精确退出码的约束。ChangeId `20260805T170528Z` runner `884ec7f6...c8c3` 随后执行一次并返回退出码 3；唯一失败项为 target-admin 未在白名单，账号/权限、target-new 白名单和零发送增量均通过。该候选已消费并隔离，禁止重试；白名单变更必须使用新的候选与独立授权。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-canary-target-state-readonly.ps1 -SelfTest
```

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

### 阶段 5 Canary target-admin 精确白名单变更候选生成器

| 项目 | 说明 |
|---|---|
| **使用者** | 运维工程师、测试工程师、产品经理 |
| **涉及功能** | 在关闭态下保留 target-new、只新增 target-admin，并冻结失败自动回滚契约 |
| **代码位置** | `scripts/prepare-sms-phase5-canary-whitelist-change.ps1` |

生成器默认关闭，只在全新的本地目录导出一个绑定 ChangeId 的 runner；生成和 `-SelfTest` 均不提示手机号、不读取 SSH 身份、不联网。候选通过隐藏输入和内存传递两个自有号码，未来执行前会重新核验 target-new 单项白名单、target-admin 注册/IAM 状态、关闭态、测试模式、Alertmanager `discard` 和零发送基线。它只允许精确新增 target-admin，并冻结环境备份、原进程环境快照、排他锁、一次停止/启动、10 秒稳定观察和失败自动回滚。

本地候选生成授权不继承为测试服执行授权。实际执行必须另行批准完整 ChangeId、runner SHA-256、配置影响、服务信号次数和回滚范围；不得上传 runner，不得发送邮件或短信，除非后续批准明确改变这些边界。

```powershell
# 仅离线自测，不写候选、不联网
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-canary-whitelist-change.ps1 -SelfTest
```

### 阶段 5 五场景真实收件默认关闭候选生成器

| 项目 | 说明 |
|---|---|
| **使用者** | 运维工程师、测试工程师、产品经理 |
| **涉及功能** | 生成绑定新 ChangeId 的五场景真实短信 runner，固定每场景一次、总量 5、零重试和关闭态自动恢复 |
| **代码位置** | `scripts/prepare-sms-phase5-canary-send-candidate.ps1` |

生成器和 runner 均默认关闭。生成与自测完全离线，不输入手机号或 Token；未来交互执行必须另行批准完整摘要，隐藏输入两个自有手机号和管理员 Bearer Token，并在任一结果后恢复 `SMS_ENABLED=false`。HTTP 成功仅表示业务入口受理本次提交，最终收件必须由号码持有人逐场景人工确认。

```powershell
# 仅离线自测，不写候选、不联网
powershell -NoProfile -ExecutionPolicy Bypass -File `
  scripts/prepare-sms-phase5-canary-send-candidate.ps1 -SelfTest
```
## AI 网关 G8 生产门禁工具

| 项目 | 说明 |
|---|---|
| **使用者** | 后端、运维、测试、产品、财务复核人 |
| **涉及模块** | `token_gateway`、MySQL、Bifrost、Prometheus、Grafana、Nginx |
| **涉及功能** | 生产流量总闸、只读发布事实门禁、SSRF 安全健康探测、隔离部署与回滚 |
| **代码位置** | `server/internal/modules/token_gateway/service/production_readiness.go`、`channel_service.go`、`docs/ai-gateway-g8-*-runbook.md` |

生产默认设置 `AI_GATEWAY_TRAFFIC_ENABLED=false`。只有受控发布明确开启时，API 才会在注册客户流量前只读核对模型、渠道、价格、路由、安全策略、毛利和重试配置；失败不会自动修复或写库。渠道健康探测的测试内网白名单使用 `AI_GATEWAY_HEALTH_INTERNAL_ALLOWLIST`，禁止配置全网段或通配符。

G7 的 `infra/scripts/verify-ai-gateway-g7-reliability.sh` 与 `server/cmd/ai-gateway-reconcile` 继续作为 G8 可靠性和零差额回归工具。通过只代表指定隔离/测试目标，不代表生产或真实客户验收。

`infra/scripts/verify-ai-gateway-g8-real-backend-e2e.sh` 在运行时随机创建临时 MySQL、Redis、RabbitMQ、API 和 Fake 文字上游，执行无 API Mock 的管理端与用户端浏览器旅程，并以只读对账收口。`infra/scripts/verify-ai-gateway-g8-production-rehearsal.sh` 构建 `origin/main` 基线与当前候选制品，验证 TLS 关闭态部署、结构备份恢复、旧版回滚、候选恢复、日志轮转和数据保留。两者都要求 `AI_GATEWAY_G8_ISOLATED_APPROVED=YES`，仅允许隔离环境，不连接生产、不调用付费上游。

### G8 测试到生产迁移清单校验器

`infra/scripts/verify-ai-gateway-g8-migration-manifest.py` 只读取不含 Secret 的 JSON 清单，不连网、不读环境文件、不输出主机/域名/路径/制品值。它以精确字段白名单拒绝密码或 Token 类额外字段，并要求从 `test_candidate` 开始按顺序传入不可变清单链；每个生产阶段绑定前序清单、审批回执和所需的测试凭据轮换回执 SHA-256，同时校验总闸、授权、请求/费用上限、模型/上游/价格批准、备份/回滚及发布目标防漂移。

```powershell
# 离线单元测试
python -m unittest infra/scripts/test_verify_ai_gateway_g8_migration_manifest.py

# 实际清单必须位于已忽略目录，示例文件含 PENDING 时应失败关闭
python infra/scripts/verify-ai-gateway-g8-migration-manifest.py `
  --manifest infra/.g8-private/01-test-candidate.json `
  --manifest infra/.g8-private/02-production-readonly.json
```

成功输出包含当前清单低敏 `receipt_sha256`，供下一阶段绑定；失败仅输出固定原因枚举，不回显调用方字段名或值。完整迁移规则见 `docs/ai-gateway-g8-test-to-production-handoff.md`。

### G8 测试服务器只读基线脚本

`infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh` 仅输出主机、制品、文件权限、环境键名、容器健康、表规模、监控、备份和凭据轮换分类。脚本不含目标凭据，不执行服务信号、DDL/DML、队列消费或业务请求；Docker 只读访问不可用时对应结果保持 `UNAVAILABLE`。

```powershell
# 连接前先执行本地语法、自检和只读静态门禁。
& 'C:\Program Files\Git\bin\bash.exe' -n infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh
& 'C:\Program Files\Git\bin\bash.exe' infra/scripts/audit-ai-gateway-g8-test-server-readonly.sh --self-test
```

实际 SSH 必须绑定唯一 ChangeId、固定 known_hosts、`BatchMode=yes`、精确目标和零重试。历史凭据比较值只以内存中的 SHA-256 注入，不写入脚本、命令输出或 Git。每个 ChangeId 只允许一次连接；失败后必须生成新候选，不能重放。

测试账号缺少 Docker 读取权限时，不得将账号直接加入 Docker 组。候选最小权限方案使用 root-owned 固定审计器、root-owned 对账二进制和单命令 sudoers；审计器拒绝非规范 ChangeId、非固定安装路径及错误所有权。安装、sudoers 修改和再次远端核验必须分别取得独立授权，详见 `docs/ai-gateway-g8-test-readonly-access-runbook.md`。

### G8 测试服务器只读入口候选生成器

`infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py` 不连接服务器。普通生成入口当前只接受已批准准备的 `002`、冻结来源提交和全新绝对输出目录；001 普通生成继续失败关闭。`--verify-consumed-candidate` 仅用于在系统临时目录复现历史 001，完成后自动销毁整个临时目录，不产生可安装输出。

```powershell
python -I infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py --self-test
python -I infra/scripts/prepare-ai-gateway-g8-test-readonly-access-bundle.py `
  --change-id=CHG-G8-TEST-READONLY-ACCESS-20260812-002 `
  --source-commit=50b3e2f9d18b38e7d4a91ebeb4f03c413ef33c44 `
  --output-dir=D:\absolute\new\g8-test-readonly-access-bundle-002
```

生成 PASS 只证明本地候选与冻结来源及摘要一致，不授权上传、安装、修改 sudoers 或执行远端审计。安装授权清单见 `docs/ai-gateway-g8-test-readonly-access-install-authorization-20260812-002.md`，必须在精确 PR HEAD CI 和独立验收通过后由用户另行批准。

## CI 变更范围分类器

| 项目 | 说明 |
|---|---|
| **使用者** | 全体开发、测试、产品与运维人员 |
| **涉及功能** | 根据 PR 精确 base/head 的变更路径开启适用 CI；纯文档只跑轻量门禁，高风险或未知路径完整回归 |
| **代码位置** | `infra/scripts/classify-ci-change-scope.py`、`infra/scripts/test_classify_ci_change_scope.py`、`.github/workflows/ci.yml` |

分类器默认失败关闭：`.github/`、`infra/`、账务交易、Migration、全局安全配置、根级未知文件和无法识别的路径会开启完整 CI；路径为空、绝对路径、反斜杠或路径穿越会直接失败。删除路径以及重命名/复制的源、目标路径会同时分类，禁止通过移动文件降级。每个 Job checkout 精确 PR head SHA；`skipped` 只表示该门禁经精确路径分类确认不适用，不能用于绕过已开启门禁。工作流末尾的 `CI 必选门禁汇总` 会复核完整 CI 标志、分类结果和所有适用 Job，必须作为分支保护 required check。

```powershell
# 本地验证分类规则和语法
python -m unittest infra/scripts/test_classify_ci_change_scope.py
python -m py_compile infra/scripts/classify-ci-change-scope.py infra/scripts/test_classify_ci_change_scope.py
```
