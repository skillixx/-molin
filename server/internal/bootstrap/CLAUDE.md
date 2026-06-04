# Bootstrap — 应用初始化与模块接入

## 职责

`bootstrap/app.go` 负责：初始化所有基础设施（DB、Redis）、依次实例化各模块的 repository / service / handler，并将路由注册到 mux。

**这是整个后端的依赖注入中心，由后端 A 负责搭建框架，其他开发者按照约定格式在自己的 route.go 中暴露注册函数。**

---

## 模块接入约定

每个模块暴露一个 `RegisterRoutes` 函数，签名统一为：

```go
// modules/{module_name}/route.go
func RegisterRoutes(mux *http.ServeMux, deps *Deps) {
    // 在此注册该模块所有路由
}

type Deps struct {
    DB        *gorm.DB
    Redis     *redis.Client
    Config    config.Config
    // 被依赖的其他模块 service（只注入 interface，不注入具体类型）
    IAMSvc    IAMChecker
    BillingSvc BillingDeductor
    // ...
}
```

---

## bootstrap/app.go 完整模板

```go
package bootstrap

import (
    "fmt"
    "net/http"

    "molin/server/internal/config"
    "molin/server/internal/httpserver"
    "molin/server/internal/middleware"
    "molin/server/pkg/db"
    "molin/server/pkg/cache"
    "molin/server/pkg/response"

    // 各模块
    authmod    "molin/server/internal/modules/auth"
    iammod     "molin/server/internal/modules/iam"
    identitymod "molin/server/internal/modules/identity"
    productmod "molin/server/internal/modules/product"
    ordermod   "molin/server/internal/modules/order"
    billingmod "molin/server/internal/modules/billing"
    assetmod   "molin/server/internal/modules/asset"
    provisionmod "molin/server/internal/modules/provision"
    membershipmod "molin/server/internal/modules/membership"
    appmod     "molin/server/internal/modules/app"
    contentmod "molin/server/internal/modules/content"
)

type App struct {
    Config config.Config
    Server *http.Server
}

func NewApp() (*App, error) {
    cfg := config.Load()

    // 初始化数据库
    gormDB, err := db.New(cfg.MySQLHost, cfg.MySQLPort, cfg.MySQLUser, cfg.MySQLPassword, cfg.MySQLDatabase)
    if err != nil {
        return nil, fmt.Errorf("数据库初始化失败: %w", err)
    }

    // 初始化 Redis
    redisClient, err := cache.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
    if err != nil {
        return nil, fmt.Errorf("Redis 初始化失败: %w", err)
    }

    // 初始化各模块（按依赖顺序）
    iamSvc    := iammod.NewService(gormDB, redisClient)
    authSvc   := authmod.NewService(gormDB, redisClient, cfg, iamSvc)
    identitySvc := identitymod.NewService(gormDB, cfg)
    orderSvc  := ordermod.NewService(gormDB)
    billingSvc := billingmod.NewService(gormDB, orderSvc)
    membershipSvc := membershipmod.NewService(gormDB)
    assetSvc  := assetmod.NewService(gormDB)

    // 初始化 provision，注册各商品类型处理器
    provisionSvc := provisionmod.NewService(gormDB, assetSvc)
    appSvc    := appmod.NewService(gormDB)
    provisionSvc.RegisterHandler("application", appmod.NewProvisioner(appSvc))

    productSvc := productmod.NewService(gormDB, billingSvc, orderSvc, provisionSvc, iamSvc, membershipSvc)
    contentSvc := contentmod.NewService(gormDB)

    // 构建路由
    mux := http.NewServeMux()

    // 健康检查（无需鉴权）
    mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
        response.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
    })

    // 注册各模块路由
    authmod.RegisterRoutes(mux, authSvc, cfg)
    iammod.RegisterRoutes(mux, iamSvc, authSvc)
    identitymod.RegisterRoutes(mux, identitySvc, authSvc, iamSvc)
    productmod.RegisterRoutes(mux, productSvc, authSvc, iamSvc)
    ordermod.RegisterRoutes(mux, orderSvc, authSvc)
    billingmod.RegisterRoutes(mux, billingSvc, authSvc, iamSvc, cfg)
    assetmod.RegisterRoutes(mux, assetSvc, authSvc)
    membershipmod.RegisterRoutes(mux, membershipSvc, authSvc)
    appmod.RegisterRoutes(mux, appSvc, authSvc)
    contentmod.RegisterRoutes(mux, contentSvc, authSvc)

    // 全局中间件（最外层）
    handler := middleware.RequestID(middleware.Recovery(middleware.Logger(mux)))

    srv := httpserver.New(cfg, handler)
    return &App{Config: cfg, Server: srv}, nil
}

func (a *App) Run() error {
    fmt.Printf("API server 启动，监听 %s\n", a.Server.Addr)
    return a.Server.ListenAndServe()
}
```

---

## 模块接入顺序（Week 对应关系）

| Week | 需要接入的模块 | 负责人 |
|---|---|---|
| Week 1 | auth, iam, identity | 后端 A |
| Week 2 | product, order, billing（基础） | 后端 B |
| Week 3 | billing（完整扣费+回调）, provision, asset, finance_consumer | 后端 B + 后端 C |
| Week 4 | membership, app, content | 后端 C |

---

## 启动新模块的标准步骤

1. 在模块目录创建 `model/`、`repository/`、`service/`、`handler/`、`dto/`、`route.go`
2. 在 `route.go` 暴露 `RegisterRoutes` 函数
3. 在 `bootstrap/app.go` 中 import 并调用 `RegisterRoutes`
4. 编写对应 migration 文件
5. 运行 `go build ./...` 确认编译通过
