# 基础架构环境与代码目录说明

## 1. 基础架构目标

这个基础架构用于给开发团队统一开发入口，先跑通基础 API、前端骨架和本地基础服务。

基础环境包括：

- Go API 服务。
- Vue3 管理后台。
- Vue3 用户控制台。
- MySQL。
- Redis。
- RabbitMQ。
- MinIO。
- 项目专用 Codex skills。

## 2. 基础架构点

```text
server/cmd/api
  后端 API 入口。

server/internal/bootstrap
  应用启动装配。

server/internal/config
  环境变量和配置加载。

server/internal/httpserver
  HTTP server 创建和超时配置。

server/internal/router
  API 路由网关。

server/internal/middleware
  请求 ID、日志、异常恢复、认证、权限等中间件。

server/internal/modules
  按业务模块拆分代码。

server/pkg
  可复用公共 Go 工具。

web/admin-console
  管理后台。

web/user-console
  用户控制台。

web/shared
  前端共享代码。

infra
  本地基础设施。
```

## 3. 后端文件说明

```text
server/go.mod
```

定义 Go module。

```text
server/cmd/api/main.go
```

API 服务入口。负责创建 App 并启动 HTTP 服务。

```text
server/internal/bootstrap/app.go
```

负责装配配置、路由和 HTTP server。后续数据库、Redis、RabbitMQ、MinIO 初始化也放在这里接入。

```text
server/internal/config/config.go
```

读取环境变量，生成应用配置。后续扩展 MySQL、Redis、RabbitMQ、MinIO、JWT 等配置。

```text
server/internal/httpserver/server.go
```

创建 `http.Server`，统一设置监听地址、读写超时和空闲超时。

```text
server/internal/router/router.go
```

当前基础路由网关。已包含：

- `GET /api/health`
- `GET /api/ready`
- `GET /api/version`

后续模块路由统一在这里挂载。

```text
server/internal/middleware/request_id.go
```

为每个请求生成或透传 `X-Request-ID`。

```text
server/internal/middleware/logger.go
```

记录请求方法、路径和耗时。

```text
server/internal/middleware/recovery.go
```

捕获 panic，返回统一错误响应。

```text
server/pkg/response/response.go
```

统一 JSON 响应结构。

```text
server/pkg/idgen/idgen.go
```

生成请求 ID。后续可以扩展订单号、流水号、资产编号。

## 4. 后端模块目录说明

```text
server/internal/modules/auth
```

邮箱注册、手机号注册、邮箱登录、手机号登录、验证码、JWT、Refresh Token。

```text
server/internal/modules/identity
```

实名制认证提交、审核、实名状态、实名审核日志。

```text
server/internal/modules/iam
```

用户、角色、权限、动态授权、权限缓存失效。

```text
server/internal/modules/product
```

统一商品、套餐、价格、角色可见、会员规则、商品购买入口。

```text
server/internal/modules/order
```

订单创建、支付、取消、状态流转、幂等。

```text
server/internal/modules/billing
```

钱包、充值、消费、流水、冻结、退款、余额一致性。

```text
server/internal/modules/asset
```

用户资产、权益额度、资产状态、资产事件。

```text
server/internal/modules/membership
```

会员等级、会员权益、用户会员、会员价、会员内含规则。

```text
server/internal/modules/content
```

系统公告、帮助分类、帮助文档、发布和可见范围。

```text
server/internal/modules/audit
```

审计日志。所有关键写操作都应该写审计。

## 5. 前端文件说明

```text
web/admin-console/package.json
```

管理后台依赖和脚本。

```text
web/admin-console/src/main.ts
```

管理后台入口，挂载 Vue、Pinia、Vue Router、Element Plus。

```text
web/admin-console/src/router/index.ts
```

管理后台路由。后续加用户、角色、商品、订单、资产、实名审核页面。

```text
web/admin-console/src/views/LoginView.vue
```

管理后台登录页占位。

```text
web/admin-console/src/views/DashboardView.vue
```

管理后台首页占位。

```text
web/user-console/package.json
```

用户控制台依赖和脚本。

```text
web/user-console/src/main.ts
```

用户控制台入口。

```text
web/user-console/src/router/index.ts
```

用户控制台路由。后续加注册、登录、实名、商品市场、购买、资产、钱包、会员页面。

```text
web/user-console/src/views/LoginView.vue
```

用户登录页占位。

```text
web/user-console/src/views/MarketplaceView.vue
```

商品市场页占位。

```text
web/shared
```

前端公共代码目录。后续拆分：

- `api-client`
- `components`
- `constants`
- `types`
- `utils`

## 6. 基础环境文件说明

```text
.env.example
```

后端、本地服务和密钥的环境变量模板。

```text
infra/docker-compose.yml
```

本地启动 MySQL、Redis、RabbitMQ、MinIO。

```text
infra/README.md
```

基础环境启动说明。

## 7. 开发人员下一步

后端 1：

- 完成 `auth` 表结构。
- 完成邮箱和手机号注册登录接口。
- 完成 `identity` 实名认证接口。
- 完成 `iam` 角色权限接口。

后端 2：

- 完成商品、订单、钱包表结构。
- 完成商品 CRUD。
- 完成订单和钱包扣费。

后端 3：

- 完成用户资产、会员、公告、帮助文档表结构。
- 完成购买成功后的资产生成。

前端 1：

- 完善管理后台登录、用户、角色、商品、订单、资产、实名审核页面。

前端 2：

- 完善用户注册、登录、实名、商品市场、购买、资产、钱包页面。

运维：

- 验证 Docker Compose。
- 准备测试环境。
- 增加 CI。
