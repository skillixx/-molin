# Provision 模块 — 后端 C 负责

## 职责边界

只负责：按 product_type 路由到对应业务处理器、调用 asset 模块创建资产。

不负责：下单（order 模块）、扣费（billing 模块）、具体业务逻辑（由各 Provisioner 实现）。

## 需要创建的文件

```text
interface.go              -- ProvisionHandler 接口定义

service/
  provision_service.go    -- 按 product_type 分发到对应 Provisioner
  app_provisioner.go      -- 应用类型开通处理器（第一阶段实现）

route.go                  -- 内部路由（不对外暴露）
```

## 核心接口（所有业务模块 Provisioner 必须实现）

```go
// interface.go
type ProvisionHandler interface {
    // 首次开通
    Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error)
    // 续费
    Renew(ctx context.Context, assetID uint64, orderID uint64) error
    // 暂停（欠费、管理员操作）
    Suspend(ctx context.Context, assetID uint64, reason string) error
    // 恢复
    Resume(ctx context.Context, assetID uint64) error
    // 取消（退款、到期）
    Cancel(ctx context.Context, assetID uint64) error
}

type ProvisionRequest struct {
    OrderID   uint64
    UserID    uint64
    ProductID uint64
    PlanID    uint64
    Product   product.Product
    Plan      product.ProductPlan
}

type ProvisionResult struct {
    AssetID            uint64
    BusinessInstanceID *uint64   // 业务实例 ID，例如 gpu_rentals.id
    Status             string    // active / pending
}
```

## 路由分发

```go
// service/provision_service.go
func (s *ProvisionService) Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
    handler, ok := s.handlers[req.Product.ProductType]
    if !ok {
        return nil, ErrUnsupportedProductType
    }
    result, err := handler.Provision(ctx, req)
    if err != nil { return nil, err }

    // 统一创建 user_assets（无论哪种业务类型）
    assetID, err := s.assetService.Create(ctx, asset.CreateAssetRequest{
        UserID:             req.UserID,
        AssetType:          toAssetType(req.Product.ProductType),
        ProductID:          req.ProductID,
        ProductPlanID:      req.PlanID,
        SourceOrderID:      req.OrderID,
        BusinessInstanceID: result.BusinessInstanceID,
        Status:             result.Status,
        ExpiresAt:          calcExpiresAt(req.Plan),
    })
    result.AssetID = assetID
    return result, err
}
```

## 注册 Provisioner

```go
// server/internal/bootstrap/app.go 启动时注册
provisionService.Register("app", appProvisioner)
// 第二阶段
provisionService.Register("gpu", gpuProvisioner)
// 第三阶段
provisionService.Register("agent", agentProvisioner)
provisionService.Register("skill", skillProvisioner)
provisionService.Register("token", tokenProvisioner)
```

## 应用 Provisioner（第一阶段实现）

```go
// service/app_provisioner.go
func (p *AppProvisioner) Provision(ctx context.Context, req ProvisionRequest) (*ProvisionResult, error) {
    // 应用类型只需要生成访问权限记录，不需要启动实例
    // 直接返回 active 状态，asset 由 provision_service 统一创建
    return &ProvisionResult{Status: "active"}, nil
}
```

## 依赖关系

- 依赖 `modules/asset/service` — 统一创建用户资产
- 依赖 `modules/membership/service` — 会员类型开通
- 被 `modules/product/handler` 调用 — 购买成功后触发开通
