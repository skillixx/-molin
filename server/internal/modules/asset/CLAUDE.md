# Asset 模块 — 后端 C 负责

## 职责边界

只负责：用户资产创建/查询/状态管理、权益额度管理（含并发消耗）、资产事件记录。

不负责：商品开通路由（provision 模块负责调用 asset）、会员权益（membership 模块）。

---

## Week 3 任务清单

```text
□ model/asset.go               — user_assets, user_entitlements, asset_events
□ repository/asset_repo.go     — 创建、按用户/商品查询、状态更新
□ repository/entitlement_repo.go — 权益额度 CRUD，含 SELECT FOR UPDATE
□ service/asset_service.go     — CreateAsset / ExpireAsset / FreezeAsset / ConsumeEntitlement
□ handler/asset_handler.go     — 用户查资产/权益，管理员查资产
□ dto/asset_dto.go
□ route.go

Migration：
□ server/migrations/000007_create_asset_tables.up.sql
```

---

## 资产状态机

```text
active  →  expired    （定时任务扫描到期时自动流转）
active  →  frozen     （管理员操作）
active  →  cancelled  （退款后）
frozen  →  active     （管理员解冻）
```

**每次状态变更必须写 asset_events 日志。**

## 权益原子消耗（并发安全）

```go
// service/asset_service.go
// ConsumeEntitlement 原子消耗权益额度，使用 SELECT FOR UPDATE 防止并发超用。
func (s *AssetService) ConsumeEntitlement(ctx context.Context, entitlementID uint64, amount decimal.Decimal) error {
    return s.db.Transaction(func(tx *gorm.DB) error {
        var ent model.UserEntitlement
        // 加行锁
        if err := tx.Set("gorm:query_option", "FOR UPDATE").
            Where("id = ? AND status = ?", entitlementID, "active").
            First(&ent).Error; err != nil {
            return err
        }
        // 校验额度是否充足
        if ent.QuotaTotal != nil {
            remaining := ent.QuotaTotal.Sub(ent.QuotaUsed)
            if remaining.LessThan(amount) {
                return ErrQuotaExceeded
            }
        }
        // 更新已用额度
        return tx.Model(&model.UserEntitlement{}).
            Where("id = ?", entitlementID).
            Update("quota_used", gorm.Expr("quota_used + ?", amount)).Error
    })
}
```

## 创建资产（由 provision 模块调用）

```go
// service/asset_service.go
func (s *AssetService) CreateAsset(ctx context.Context, req CreateAssetReq) (*model.UserAsset, error) {
    asset := &model.UserAsset{
        UserID:      req.UserID,
        AssetType:   req.AssetType,
        ProductID:   req.ProductID,
        PlanID:      req.PlanID,
        SourceOrderID: &req.OrderID,
        Status:      "active",
        StartedAt:   timePtr(time.Now()),
        ExpiresAt:   req.ExpiresAt, // 永久商品为 nil
    }
    if err := s.repo.Create(ctx, asset); err != nil {
        return nil, err
    }
    // 写资产创建事件
    s.writeEvent(ctx, asset.ID, req.UserID, "created", "", "active", nil, "")
    // 按套餐配置初始化权益额度
    if req.QuotaConfig != nil {
        s.createEntitlement(ctx, asset, req.QuotaConfig)
    }
    return asset, nil
}
```

---

## Migration

### server/migrations/000007_create_asset_tables.up.sql

```sql
CREATE TABLE IF NOT EXISTS user_assets (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  asset_type VARCHAR(64) NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  product_plan_id BIGINT UNSIGNED NULL,
  source_order_id BIGINT UNSIGNED NULL,
  business_instance_id VARCHAR(128) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  started_at DATETIME NULL,
  expires_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_user_assets_user_id (user_id),
  KEY idx_user_assets_status (status),
  KEY idx_user_assets_product_id (product_id),
  KEY idx_user_assets_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_entitlements (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  asset_id BIGINT UNSIGNED NOT NULL,
  entitlement_type VARCHAR(64) NOT NULL,
  product_id BIGINT UNSIGNED NOT NULL,
  quota_total DECIMAL(18,6) NULL,
  quota_used DECIMAL(18,6) NOT NULL DEFAULT 0,
  quota_unit VARCHAR(32) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  started_at DATETIME NULL,
  expires_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_entitlements_user_id (user_id),
  KEY idx_entitlements_asset_id (asset_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS asset_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  asset_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  event_type VARCHAR(64) NOT NULL,
  before_status VARCHAR(32) NULL,
  after_status VARCHAR(32) NULL,
  operator_id BIGINT UNSIGNED NULL,
  remark VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_asset_events_asset_id (asset_id),
  KEY idx_asset_events_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 接口清单

```text
GET  /api/my/assets              -- 用户查自己的资产列表
GET  /api/my/assets/:id          -- 用户查资产详情
GET  /api/my/entitlements        -- 用户查权益额度
GET  /api/admin/assets           -- 管理员查所有资产
GET  /api/admin/users/:id/assets -- 管理员查指定用户资产
PATCH /api/admin/assets/:id      -- 管理员冻结/解冻资产
```

## 到期处理（定时任务）

定时任务每小时运行一次，将 `status=active AND expires_at < NOW()` 的资产批量更新为 `expired`，并写 asset_events。定时任务代码放在 `server/internal/jobs/expire_assets.go`，由后端 C 负责。
