# App 模块 — 后端 C 负责

## 职责边界

只负责：应用业务详情 CRUD（图标/描述/回调地址/适配器配置）、应用适配器注册管理。

不负责：商品交易（套餐/价格/角色权限走 `product_plans`/`product_prices`/`product_role_access`，
通过 `products.business_ref_id = applications.id` 关联）、应用开通逻辑（`provision` 模块的
`AppProvisioner` 已实现，无需修改）。

> **重要边界**：`applications` 表只存业务详情字段，禁止在本模块中重复实现套餐/价格/权限相关逻辑。

---

## Week 4 任务清单

```text
□ model/app.go                  — Application, ApplicationAdapter
□ repository/app_repo.go        — 应用 CRUD、按 product_id 反查
□ repository/adapter_repo.go    — 适配器 CRUD
□ service/app_service.go        — 应用业务逻辑（创建/更新/上下架）
□ service/adapter_service.go    — 适配器注册/启停
□ handler/app_handler.go        — 用户端查询 + 管理端 CRUD
□ dto/app_dto.go
□ route.go

Migration：
□ server/migrations/000010_create_app_tables.up.sql / .down.sql
```

---

## 数据模型

### applications（应用业务详情，仅非交易字段）

```go
type Application struct {
    ID                uint64
    Code              string  // 唯一标识，如 "netdisk-basic"
    Name              string
    Type              string  // 应用类型，如 netdisk / ai-tool
    Description       string
    IconURL           string
    AccessURL         string  // 用户访问入口地址（用户端「进入应用」跳转目标，面向用户、进白名单；写入须 https）
    CallbackURL       string
    AdapterConfigJSON string  // JSON：应用特有配置（非交易字段）
    Status            string  // draft / active / inactive / archived
    CreatedAt, UpdatedAt time.Time
}
```

### application_adapters（适配器注册信息）

```go
type ApplicationAdapter struct {
    ID                  uint64
    AppCode             string
    AppName             string
    AppType             string
    AdapterType         string // internal / external
    ServiceName         string
    CallbackURL         string
    SupportedActionsJSON string // JSON 数组：["provision","renew","suspend","resume","cancel"]
    UsageEventTypesJSON  string // JSON 数组
    Status              string  // active / inactive
    CreatedAt, UpdatedAt time.Time
}
```

---

## 应用与商品的关系（务必遵守）

```text
applications（业务详情表）
  只保存：icon、description、callback_url、adapter_config 等非交易字段

products（统一商品表，后端 B 负责）
  保存：product_type = "application" 时，business_ref_id 指向 applications.id
  套餐/价格/角色权限统一走 product_plans / product_prices / product_role_access
```

- 创建应用本身（`POST /api/admin/apps`）与"上架为可购买商品"是两个独立动作：
  应用创建后，需要由管理员在商品管理里新建 `product_type = "application"` 且
  `business_ref_id` 指向该应用 ID 的商品记录，才能进入应用市场购买流程。
- 本模块**不需要**也**不应该**创建/修改 `products`/`product_plans` 记录。
- `provision` 模块的 `AppProvisioner`（`server/internal/modules/provision/handler/app_provisioner.go`）
  已实现并通过 Week 3 验收测试（按 `product.Status` 校验后直接返回成功），**无需修改**，
  本模块只负责应用元数据管理。

---

## 接口清单

```text
GET   /api/marketplace/apps/:id        -- 用户查应用业务详情（icon/description等，登录用户可访问）
GET   /api/admin/apps                  -- 管理员查应用列表（支持分页、按 status/type 筛选）
GET   /api/admin/apps/:id              -- 管理员查应用详情
POST  /api/admin/apps                  -- 管理员创建应用
PATCH /api/admin/apps/:id              -- 管理员更新应用 / 上下架（status: draft/active/inactive/archived）
GET   /api/admin/app-adapters          -- 管理员查适配器列表
POST  /api/admin/app-adapters          -- 管理员注册适配器
PATCH /api/admin/app-adapters/:id      -- 管理员更新/启停适配器
```

权限：管理端接口要求 `RequirePerm("app:manage")`（参考 `iam/CLAUDE.md` 权限码命名规范，
若该权限码不存在需在 `permissions` 表 seed 中补充并告知后端 A）。

---

## Migration

### server/migrations/000010_create_app_tables.up.sql

```sql
CREATE TABLE IF NOT EXISTS applications (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(64) NOT NULL,
  name VARCHAR(128) NOT NULL,
  type VARCHAR(64) NOT NULL,
  description VARCHAR(1024) NULL,
  icon_url VARCHAR(512) NULL,
  callback_url VARCHAR(512) NULL,
  adapter_config_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_applications_code (code),
  KEY idx_applications_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS application_adapters (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  app_code VARCHAR(64) NOT NULL,
  app_name VARCHAR(128) NOT NULL,
  app_type VARCHAR(64) NOT NULL,
  adapter_type VARCHAR(32) NOT NULL DEFAULT 'internal',
  service_name VARCHAR(128) NULL,
  callback_url VARCHAR(512) NULL,
  supported_actions_json JSON NULL,
  usage_event_types_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_app_adapters_app_code (app_code),
  KEY idx_app_adapters_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### server/migrations/000010_create_app_tables.down.sql

```sql
DROP TABLE IF EXISTS application_adapters;
DROP TABLE IF EXISTS applications;
```

---

## bootstrap 接入

在 `server/internal/bootstrap/app.go` 中注册 `app` 模块路由（参考 `content`/`membership`
模块的接入方式，无需新增 adapter，因为本模块不被其他模块依赖）。

---

## 自测要点

- 创建应用 → 管理员在商品模块新建 `product_type=application` 且 `business_ref_id` 指向该应用的商品 →
  完整购买链路验证应用详情可正确展示（与 Week 3 已验证的开通链路衔接）
- 应用 `status` 流转：`draft → active → inactive/archived`，下架后用户端不可见/不可购买
  （不可购买的校验已由 `product_role_access`/`products.status` 控制，本模块只需保证
  `applications.status != active` 时业务详情接口返回恰当的状态提示）
- 适配器 `app_code` 唯一性校验
