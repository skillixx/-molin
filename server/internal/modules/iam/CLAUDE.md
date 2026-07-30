# IAM 模块 — 后端 A 负责

## 职责边界

只负责：角色 CRUD、权限 CRUD、用户角色分配、用户权限覆盖、权限计算、权限 Redis 缓存；
**用户分组管理**（分组 CRUD、组成员、组权限、组邀请码、数据范围 scope）、**注册落组**。

不负责：登录鉴权（auth 模块）、商品访问规则（product 模块）。

---

## 用户分组与注册落组

分组相关代码：`model/group.go`、`repository/group_repo.go`、`service/group_service.go`、`service/scope_service.go`、`handler/group_handler.go`。

**注册落组**（供 auth 模块在用户注册成功后调用，跨模块通过 auth 侧 `GroupJoiner` 接口解耦）：

- `GroupService.AssignOnRegister(ctx, userID, inviteCode)` — 落组策略总入口：
  - 有效 `inviteCode` → 复用 `JoinByInviteCode`（落对应组、赋邀请码 `default_group_role`、原子递增 `used_count`）；
  - 无效/过期/已满 `inviteCode`（`ErrInviteCodeNotFound`/`ErrInviteCodeFull`）→ **降级落默认组（方案 A）**；
  - 无 `inviteCode` → 落默认组；
  - 未配置默认组（`FindDefaultGroup` 返回 `ErrGroupNotFound`）→ 跳过，返回 nil。
- `GroupRepository.FindDefaultGroup(ctx)` — 查 `is_default=true` 的默认组（全局最多一个）。
- 方案 A 适用边界：邀请码当前仅承载「分组归属 + 组内角色」，不承载准入门槛语义；升级为强准入门槛时需重评降级策略。

**权限计算含组权限**：`getAllUserPermCodes` 合并「角色权限 ∪ 用户所在组的权限码」，去重后用于 4 步优先级判定。

---

## Week 1 任务清单

```text
□ model/role.go             — roles, permissions, user_roles, role_permissions, user_permission_overrides, role_change_logs
□ repository/role_repo.go   — 角色 CRUD
□ repository/permission_repo.go — 权限 CRUD
□ repository/user_role_repo.go  — 用户角色关联
□ repository/override_repo.go   — 用户权限覆盖
□ service/iam_service.go    — 权限计算（4 步优先级）
□ service/cache_service.go  — Redis 缓存写入/失效
□ handler/iam_handler.go
□ dto/iam_dto.go
□ route.go
□ server/internal/middleware/permission.go — 权限校验中间件

Migration：
□ server/migrations/000002_create_iam_tables.up.sql
```

---

## 权限计算逻辑（核心，严格实现）

```go
// service/iam_service.go
// CheckPermission 按 4 步优先级计算用户是否拥有 permCode 权限：
// 1. 用户显式 deny → 禁止（最高优先级）
// 2. 用户显式 allow → 允许
// 3. 角色权限中包含 → 允许
// 4. 默认 → 禁止
func (s *IAMService) CheckPermission(ctx context.Context, userID uint64, permCode string) bool {
    // 先查缓存
    if cached, ok := s.cache.GetUserPerms(ctx, userID); ok {
        return evalPerms(cached, permCode)
    }
    overrides, _ := s.overrideRepo.FindByUser(ctx, userID)
    for _, o := range overrides {
        if o.PermissionCode == permCode {
            if o.Effect == "deny" {
                return false
            }
            if o.Effect == "allow" {
                return true
            }
        }
    }
    rolePerms, _ := s.GetUserRolePermissions(ctx, userID)
    for _, p := range rolePerms {
        if p.Code == permCode {
            return true
        }
    }
    return false
}
```

## 权限缓存

```go
// Redis key 格式：perm:user:{userID}
// TTL：5 分钟
// 触发失效：修改用户角色、修改角色权限、修改用户权限覆盖

const permCacheKeyFmt = "perm:user:%d"
const permCacheTTL = 5 * time.Minute

func (s *CacheService) InvalidateUserPerms(ctx context.Context, userID uint64) {
    key := fmt.Sprintf(permCacheKeyFmt, userID)
    s.redis.Del(ctx, key)
}
```

## 权限校验中间件

```go
// server/internal/middleware/permission.go
// RequirePerm 在 RequireAuth 之后使用，校验当前用户是否有 permCode 权限。
func RequirePerm(iamSvc IAMChecker, permCode string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        userID := UserIDFromContext(r.Context())
        if !iamSvc.CheckPermission(r.Context(), userID, permCode) {
            response.Error(w, http.StatusForbidden, 40003, "无操作权限")
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

---

## Migration

### server/migrations/000002_create_iam_tables.up.sql

```sql
CREATE TABLE IF NOT EXISTS roles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(128) NOT NULL,
  name VARCHAR(128) NOT NULL,
  description VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_roles_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS permissions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code VARCHAR(191) NOT NULL,
  name VARCHAR(128) NOT NULL,
  resource VARCHAR(128) NOT NULL,
  action VARCHAR(64) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_permissions_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_roles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_roles (user_id, role_id),
  KEY idx_user_roles_role_id (role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS role_permissions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  role_id BIGINT UNSIGNED NOT NULL,
  permission_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_role_permissions (role_id, permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_permission_overrides (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  permission_id BIGINT UNSIGNED NOT NULL,
  effect VARCHAR(16) NOT NULL,
  reason VARCHAR(512) NULL,
  expires_at DATETIME NULL,
  created_by BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_permission_overrides (user_id, permission_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS role_change_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  role_id BIGINT UNSIGNED NOT NULL,
  action VARCHAR(32) NOT NULL,
  operator_id BIGINT UNSIGNED NULL,
  reason VARCHAR(512) NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_role_change_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  operator_id BIGINT UNSIGNED NULL,
  module VARCHAR(64) NOT NULL,
  action VARCHAR(64) NOT NULL,
  target_type VARCHAR(64) NULL,
  target_id VARCHAR(128) NULL,
  ip VARCHAR(64) NULL,
  request_summary JSON NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_audit_operator_id (operator_id),
  KEY idx_audit_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 接口清单

```text
GET    /api/admin/roles
POST   /api/admin/roles
PUT    /api/admin/roles/:id
DELETE /api/admin/roles/:id
GET    /api/admin/permissions
GET    /api/admin/users/:id/roles
POST   /api/admin/users/:id/roles
DELETE /api/admin/users/:id/roles/:role_id
GET    /api/admin/users/:id/permission-overrides
POST   /api/admin/users/:id/permission-overrides
DELETE /api/admin/users/:id/permission-overrides/:override_id
GET    /api/admin/audit-logs
```

## 权限码规范

格式：`resource:action`

| 权限码 | 说明 |
|---|---|
| user:list | 查用户列表 |
| user:edit | 编辑用户 |
| user:disable | 封禁用户 |
| role:manage | 管理角色 |
| product:create | 创建商品 |
| product:edit | 编辑商品 |
| order:list | 查订单 |
| wallet:view | 查钱包 |
| identity:review | 审核实名 |
| content:manage | 管理公告/帮助文档 |

## DirectMail bootstrap 000056 IAM 专项边界

- 000056 仅 seed `email:template:bootstrap`，精确元数据为名称“首次配置管理员邮箱认证模板”、resource=`email_template`、action=`bootstrap`。
- 该权限只授权一次性内部入口，不隐含 `email:template:view/manage/sync/test`，普通四权限也不隐含 bootstrap。
- 身份门禁必须确认用户通过 `user_roles` 直接关联唯一 code=`admin` 角色；分组角色或动态权限 allow 不能替代直接管理员身份。
- 专项 ownership 必须区分权限和 admin 绑定的预存/新增状态；精确 down 不得删除预存项，存在未知引用或成功 receipt 时失败关闭。
