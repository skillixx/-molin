# Provision 模块 — 后端 C 负责

## 职责边界

只负责：按 product_type 路由到对应业务处理器、调用 asset 模块创建资产。

不负责：扣费（billing）、订单状态（order）、具体业务（app/gpu/agent 各自实现 ProvisionHandler 接口）。

---

## Week 3 任务清单

```text
□ service/provision_service.go    — 注册 handler、路由 Provision 调用
□ handler/app_provisioner.go      — 应用商品开通处理器（第一阶段实现）
□ server/internal/bootstrap/app.go — 注册各 handler 到 ProvisionService
```

---

## ProvisionHandler 接口（所有商品类型必须实现）

```go
// service/provision_service.go

// ProvisionHandler 是所有商品类型的开通接口。
// 新商品类型接入时只需实现此接口，并在 bootstrap/app.go 中注册。
type ProvisionHandler interface {
    Provision(ctx context.Context, req ProvisionReq) (*ProvisionResult, error)
    Renew(ctx context.Context, assetID uint64, planID uint64) error
    Suspend(ctx context.Context, assetID uint64) error
    Resume(ctx context.Context, assetID uint64) error
    Cancel(ctx context.Context, assetID uint64) error
}

type ProvisionReq struct {
    OrderID   uint64
    ProductID uint64
    PlanID    uint64
    UserID    uint64
}

type ProvisionResult struct {
    AssetID            uint64
    BusinessInstanceID string  // 可选，有实例概念的商品填入
    ExpiresAt          *time.Time
}
```

## ProvisionService 路由

```go
// service/provision_service.go

type ProvisionService struct {
    handlers map[string]ProvisionHandler  // product_type → handler
    assetSvc AssetService
}

// RegisterHandler 在 bootstrap 阶段注册各商品类型的处理器。
func (s *ProvisionService) RegisterHandler(productType string, h ProvisionHandler) {
    s.handlers[productType] = h
}

// Provision 按 product_type 路由到对应处理器，开通成功后创建资产。
func (s *ProvisionService) Provision(ctx context.Context, orderID, productID, planID, userID uint64) error {
    product, _ := s.productRepo.FindByID(ctx, productID)
    handler, ok := s.handlers[product.ProductType]
    if !ok {
        return fmt.Errorf("未找到商品类型 %s 的开通处理器", product.ProductType)
    }
    result, err := handler.Provision(ctx, ProvisionReq{
        OrderID: orderID, ProductID: productID, PlanID: planID, UserID: userID,
    })
    if err != nil {
        return err
    }
    // 创建用户资产（所有商品类型统一从这里创建）
    plan, _ := s.planRepo.FindByID(ctx, planID)
    var expiresAt *time.Time
    if plan.DurationDays != nil {
        t := time.Now().AddDate(0, 0, *plan.DurationDays)
        expiresAt = &t
    }
    _, err = s.assetSvc.CreateAsset(ctx, asset.CreateAssetReq{
        UserID:             userID,
        AssetType:          product.ProductType,
        ProductID:          productID,
        PlanID:             planID,
        OrderID:            orderID,
        BusinessInstanceID: result.BusinessInstanceID,
        ExpiresAt:          expiresAt,
        QuotaConfig:        plan.QuotaJSON,
    })
    return err
}
```

## AppProvisioner（第一阶段实现）

```go
// handler/app_provisioner.go

// AppProvisioner 处理应用类商品的开通。
// 应用类商品不需要启动实例，开通即激活，直接返回成功。
type AppProvisioner struct {
    appRepo AppRepository
}

func (p *AppProvisioner) Provision(ctx context.Context, req ProvisionReq) (*ProvisionResult, error) {
    // 应用类商品：确认应用状态正常即可，无需实际启动
    app, err := p.appRepo.FindByProductID(ctx, req.ProductID)
    if err != nil {
        return nil, err
    }
    if app.Status != "active" {
        return nil, errors.New("应用当前不可用")
    }
    return &ProvisionResult{}, nil
}

func (p *AppProvisioner) Renew(_ context.Context, _ uint64, _ uint64) error  { return nil }
func (p *AppProvisioner) Suspend(_ context.Context, _ uint64) error           { return nil }
func (p *AppProvisioner) Resume(_ context.Context, _ uint64) error            { return nil }
func (p *AppProvisioner) Cancel(_ context.Context, _ uint64) error            { return nil }
```

## 在 bootstrap/app.go 中注册

```go
// server/internal/bootstrap/app.go

// 在 NewApp() 中初始化各模块后注册：
provisionSvc := provision.NewProvisionService(productRepo, planRepo, assetSvc)
provisionSvc.RegisterHandler("application", app.NewAppProvisioner(appRepo))
// 后续注册：
// provisionSvc.RegisterHandler("gpu", gpu.NewGPUProvisioner(...))
// provisionSvc.RegisterHandler("agent", agent.NewAgentProvisioner(...))
```
