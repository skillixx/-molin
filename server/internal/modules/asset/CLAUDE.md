# Asset 模块 — 后端 C 负责

## 职责边界

只负责：用户资产创建/查询/状态管理、权益额度管理、资产事件记录。

不负责：订单（order 模块）、钱包扣费（billing 模块）、开通逻辑（provision 模块调用 asset）。

## 需要创建的文件

```text
model/
  asset.go              -- user_assets, user_entitlements, asset_events

repository/
  asset_repo.go         -- 资产 CRUD，含按用户/类型/状态查询
  entitlement_repo.go   -- 权益额度 CRUD，含原子消耗
  event_repo.go         -- 资产事件写入（只追加）

service/
  asset_service.go          -- Create / UpdateStatus / Expire / Query
  entitlement_service.go    -- Consume / Replenish / CheckSufficient

handler/
  asset_handler.go          -- 用户端 + 管理端

dto/
  asset_dto.go

route.go
```

## 关键类型

```go
type UserAsset struct {
    ID                 uint64
    UserID             uint64
    AssetType          string    // app_access / gpu_instance / agent_instance / skill_license / token_quota / membership
    ProductID          uint64
    ProductPlanID      uint64
    SourceOrderID      uint64
    BusinessInstanceID *uint64   // 指向业务表（gpu_rentals.id 等）
    Status             string    // active / expired / frozen / cancelled
    StartedAt          time.Time
    ExpiresAt          *time.Time
    CreatedAt          time.Time
    UpdatedAt          time.Time
}

type UserEntitlement struct {
    ID               uint64
    UserID           uint64
    AssetID          uint64
    EntitlementType  string
    ProductID        uint64
    QuotaTotal       decimal.Decimal
    QuotaUsed        decimal.Decimal
    QuotaUnit        string    // tokens / gb / requests
    Status           string
    StartedAt        time.Time
    ExpiresAt        *time.Time
    CreatedAt        time.Time
    UpdatedAt        time.Time
}
```

## 权益消耗（必须原子操作）

```go
func (s *EntitlementService) Consume(ctx context.Context, userID uint64, productID uint64, amount decimal.Decimal) error {
    return s.db.Transaction(func(tx *gorm.DB) error {
        // 查询有效权益，加行锁
        var ent UserEntitlement
        tx.Set("gorm:query_option", "FOR UPDATE").
            Where("user_id = ? AND product_id = ? AND status = 'active' AND expires_at > NOW()", userID, productID).
            First(&ent)

        available := ent.QuotaTotal.Sub(ent.QuotaUsed)
        if available.LessThan(amount) {
            return ErrInsufficientQuota
        }

        return tx.Model(&UserEntitlement{}).
            Where("id = ?", ent.ID).
            Update("quota_used", gorm.Expr("quota_used + ?", amount)).Error
    })
}
```

## 资产状态流转

```text
（创建后）-> active
active -> expired   （到期定时任务，每小时扫描）
active -> frozen    （管理员冻结 / 欠费）
active -> cancelled （退款 / 管理员取消）
frozen -> active    （解冻）
```

每次状态变化必须写入 `asset_events`，字段包含：`before_status`、`after_status`、`operator_id`（0 表示系统）、`remark`。

## 接口清单

```text
GET /api/my/assets
GET /api/my/assets/:id
GET /api/my/entitlements
GET /api/admin/user-assets
GET /api/admin/user-entitlements
GET /api/admin/asset-events
GET /api/admin/users/:id/assets
GET /api/admin/users/:id/entitlements
```

## 依赖关系

- 被 `modules/provision/service` 依赖 — 开通成功后调用 AssetService.Create
- 被 `modules/finance_consumer/service` 依赖 — 按量扣费时调用 EntitlementService.Consume
- 不依赖其他业务模块
