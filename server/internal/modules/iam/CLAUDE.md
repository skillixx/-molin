# IAM 模块 — 后端 A 负责

## 职责边界

只负责：角色、权限、用户角色分配、用户权限覆盖、权限缓存、角色变更日志。

不负责：JWT 生成/校验（auth 模块）、具体业务权限点定义（由各业务模块定义 permission code）。

## 需要创建的文件

```text
model/
  role.go           -- roles, permissions, user_roles, role_permissions, user_permission_overrides
  change_log.go     -- role_change_logs

repository/
  role_repo.go          -- 角色 CRUD
  permission_repo.go    -- 权限 CRUD
  user_role_repo.go     -- 用户角色 CRUD，含批量查询
  override_repo.go      -- 用户权限覆盖 CRUD，含过期校验

service/
  iam_service.go        -- 权限计算、角色分配、变更日志写入
  cache_service.go      -- Redis 权限缓存读写失效

handler/
  iam_handler.go        -- Admin Handler

dto/
  iam_dto.go

route.go
```

## 关键类型

```go
type Role struct {
    ID          uint64
    Code        string   // 例如 platform_admin / normal_user
    Name        string
    Description string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type Permission struct {
    ID       uint64
    Code     string   // 格式：resource:action，例如 product:create
    Name     string
    Resource string
    Action   string
    CreatedAt time.Time
    UpdatedAt time.Time
}

type UserPermissionOverride struct {
    ID           uint64
    UserID       uint64
    PermissionID uint64
    Effect       string    // allow / deny
    Reason       string
    ExpiresAt    *time.Time
    CreatedBy    uint64
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

## 权限计算逻辑（必须按此优先级）

```go
func (s *IAMService) HasPermission(ctx context.Context, userID uint64, permCode string) (bool, error) {
    // 1. 查用户权限覆盖（过滤掉已过期的记录）
    overrides := s.getActiveOverrides(userID, permCode)
    for _, o := range overrides {
        if o.Effect == "deny"  { return false, nil }
        if o.Effect == "allow" { return true, nil  }
    }
    // 2. 查角色权限
    roles := s.getUserRoles(userID)
    for _, roleID := range roles {
        if s.roleHasPermission(roleID, permCode) { return true, nil }
    }
    return false, nil
}
```

## Redis 缓存约定

```text
Key:    perm:user:{userID}
Value:  []string  -- 该用户拥有的 permission code 列表（含 override 计算结果）
TTL:    5 分钟
```

缓存失效时机：
- 修改用户角色
- 修改角色权限
- 修改用户权限覆盖

失效方式：`DEL perm:user:{userID}`，下次请求重新计算并写入缓存。

## 接口清单

```text
GET    /api/admin/roles
POST   /api/admin/roles
GET    /api/admin/roles/:id
PATCH  /api/admin/roles/:id
DELETE /api/admin/roles/:id
PATCH  /api/admin/roles/:id/permissions
GET    /api/admin/permissions
POST   /api/admin/permissions
GET    /api/admin/users/:id/roles
PATCH  /api/admin/users/:id/roles
GET    /api/admin/users/:id/permission-overrides
PATCH  /api/admin/users/:id/permission-overrides
```

## 权限中间件（server/internal/middleware/permission.go）

```go
// 从 JWT Claims 取 userID
// 调用 iam_service.HasPermission(ctx, userID, requiredPermCode)
// 无权限返回 403，code = 40003
```

## 依赖关系

- 依赖 `server/pkg/redis` — 权限缓存
- 依赖 `modules/audit/service` — 角色变更时写审计日志
- 不依赖其他业务模块
