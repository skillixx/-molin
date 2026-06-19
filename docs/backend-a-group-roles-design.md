# 后端甲设计方案：组绑定角色（group_roles）

> 负责模块：iam（后端甲）｜关联：auth 注册落组（已上线）、product 商品访问（后端乙，零代码改动）
> 状态：待评审 → 实现
> 目标读者：后端甲（实现）、后端乙（商品访问配置对接）、产品经理（验收）

## 1. 背景与目标

### 问题
商品访问/定价按**全局角色**控制（`product_role_access.role_id`、`product_prices.role_id`），但：
- 新注册用户默认**没有任何全局角色**（`Register` 不分配角色，系统仅有 `admin` 一个基础角色）；
- 给用户配角色只能管理员**逐个手动**操作（`POST /api/admin/users/{id}/roles`），无法对「所有新注册用户」自动生效。

结果：「角色驱动的商品访问」在自助注册场景下跑不通。

### 目标
把「组」和「全局角色」打通，使用户**自动继承所在组绑定的角色**，从而：

```
注册 → 自动落组（已实现）→ 组绑定角色（本方案）→ 用户自动拥有角色 → 商品按角色开放 → 用户自动可见可购
```

配合默认组（每个新用户自动落入），实现：**管理员配置一次「默认组→基础角色」，所有新用户自动获得商品访问，零逐人操作。**

### 非目标（本期不做）
- 不让组角色携带的**权限码**对组员生效（即只做「A 版：只影响商品」，详见 §3）；
- 不改 product 模块代码；
- 不做管理后台前端页面（前端甲后续）。

## 2. 方案选型（已确认）

| 决策 | 选定 | 理由 |
|---|---|---|
| 绑定粒度 | **多对多** `group_roles` 表 | 与 `group_permissions` 同构，一个组可绑多个角色 |
| 生效范围 | **A 版：只影响商品** | 只改 `GetUserRoleIDs`，不碰权限码判定。无提权面、无权限缓存失效扇出 |
| 解析方式 | **动态联表** | `GetUserRoleIDs` 实时合并，加组/退组/绑定变更即时生效，单一数据源无漂移 |

> A 版安全性质：组角色仅用于商品访问/定价，**不进入 `CheckPermission`**。即使误绑 `admin` 角色，组员也拿不到任何管理权限码。

## 3. 总体设计

### 核心改动只有一处
全系统取用户角色的唯一入口是 `IAMService.GetUserRoleIDs()`（product 的可见/可购/可用/定价四处全走它）。本方案把它从「用户自身角色」改为「**用户自身角色 ∪ 所在组绑定的角色**」，product 模块因此零改动。

### 数据流
```
GetUserRoleIDs(userID)
  = user_roles（用户自身角色）
  ∪ group_roles（用户所在所有组绑定的角色）   ← 新增
  → 去重返回
```

## 4. 数据模型

### 4.1 新增表 group_roles
```sql
-- migrations/000028_create_group_roles.up.sql
CREATE TABLE IF NOT EXISTS group_roles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  group_id BIGINT UNSIGNED NOT NULL,
  role_id  BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_group_roles (group_id, role_id),
  KEY idx_group_roles_role_id (role_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```
`down.sql`：`DROP TABLE IF EXISTS group_roles;`

### 4.2 基础角色 seed
```sql
-- migrations/000029_seed_registered_user_role.up.sql
-- 普通注册用户基础角色：仅作为商品访问/定价的角色键，不绑任何权限码（A 版无需权限）
INSERT IGNORE INTO roles (code, name, description)
VALUES ('registered_user', '普通注册用户', '自助注册用户的基础角色，用于商品访问授权');
```
`down.sql`：删除该角色（注意先确认无 role_permissions/user_roles/group_roles 引用，或仅 `DELETE FROM roles WHERE code='registered_user'`）。

> **基础角色刻意不绑任何权限码**——它只是 `product_role_access` / `product_prices` 里的一个角色键。这保证它绝不会带来任何管理权限，安全。

### 4.3 「默认组 → 基础角色」绑定
**不在 migration 内做**（默认组由管理员运行时创建，其 ID 未知）。改为**上线后一次性管理操作**：调用本方案新增的绑定接口，把当前默认组绑到 `registered_user`。详见 §10 上线步骤。

## 5. 代码改动清单（全部在 iam 模块）

| 层 | 文件 | 改动 |
|---|---|---|
| model | `model/group.go` | 新增 `GroupRole` 结构体（对应 group_roles 表） |
| repository | `repository/group_repo.go` | 新增 `AddRole / RemoveRole / ListRoles / GetRoleIDsByGroups / ExistsByRoleID` |
| service | `service/group_service.go` | 新增 `AddGroupRole / RemoveGroupRole / ListGroupRoles`；构造函数注入 `roleRepo`（校验角色存在 + 禁绑系统角色） |
| service | `service/iam_service.go` | **改 `GetUserRoleIDs`：合并组角色**；`DeleteRole` 增加「角色被组占用」校验 |
| handler | `handler/group_handler.go` | 新增 `ListGroupRoles / AddGroupRole / RemoveGroupRole` |
| route | `route.go` | 注册 3 个新路由（`group:manage` + 双重认证） |
| bootstrap | `bootstrap/app.go` | `NewGroupService` 传入 `roleRepo` |

> 无新增权限码（复用 `group:manage`）→ **不涉及权限码 seed**，避开历史 P1。

## 6. 核心改动详解

### 6.1 GetUserRoleIDs（唯一核心逻辑）
```go
// GetUserRoleIDs 返回用户的全部生效角色 ID：用户自身角色 ∪ 所在组绑定的角色，去重。
// 供 product 等模块判定商品访问与定价。
func (s *IAMService) GetUserRoleIDs(ctx context.Context, userID uint64) ([]uint64, error) {
    seen := make(map[uint64]struct{})
    ids := make([]uint64, 0)

    // 1. 用户自身角色（原逻辑）
    own, err := s.userRoleRepo.GetRoleIDs(ctx, userID)
    if err != nil {
        return nil, err
    }
    for _, id := range own {
        if _, ok := seen[id]; !ok { seen[id] = struct{}{}; ids = append(ids, id) }
    }

    // 2. 组绑定角色（新增）：用户所在组 → group_roles
    members, _ := s.groupRepo.GetUserGroups(ctx, userID)
    if len(members) > 0 {
        groupIDs := make([]uint64, len(members))
        for i, m := range members { groupIDs[i] = m.GroupID }
        groupRoleIDs, _ := s.groupRepo.GetRoleIDsByGroups(ctx, groupIDs)
        for _, id := range groupRoleIDs {
            if _, ok := seen[id]; !ok { seen[id] = struct{}{}; ids = append(ids, id) }
        }
    }
    return ids, nil
}
```
- 复用既有 `groupRepo.GetUserGroups`（落组已在用）；
- `GetRoleIDsByGroups`：`SELECT DISTINCT role_id FROM group_roles WHERE group_id IN (?)`；
- **无缓存失效顾虑**：该方法是无缓存直查，绑定/解绑即时生效。

### 6.2 绑定服务（仿 AddGroupPermission）
```go
func (s *GroupService) AddGroupRole(ctx context.Context, groupID, roleID uint64) error {
    // 校验角色存在
    if _, err := s.roleRepo.FindByID(ctx, roleID); err != nil {
        return repository.ErrRoleNotFound
    }
    // 安全护栏：禁止把系统/特权角色绑到组（先以 admin 为黑名单）
    if isSystemRole(roleID) { return ErrCannotBindSystemRole }
    return s.repo.AddRole(ctx, &model.GroupRole{GroupID: groupID, RoleID: roleID})
}
```
> A 版下组角色不进权限码缓存，故 `AddGroupRole/RemoveGroupRole` **无需**做组员缓存失效。

### 6.3 角色删除占用校验（DeleteRole）
```go
// DeleteRole 前置校验：角色被组绑定时禁止删除（仿删组的占用校验）
if used, _ := s.groupRepo.ExistsByRoleID(ctx, id); used {
    return ErrRoleInUseByGroup // "角色已绑定到分组，请先解绑后再删除"
}
```

## 7. 接口契约（管理端，需 `group:manage` + 管理员双重认证）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/user-groups/{id}/roles` | 列出组绑定的角色 |
| POST | `/api/admin/user-groups/{id}/roles` | 给组绑定角色，body `{ "role_id": 5 }` |
| DELETE | `/api/admin/user-groups/{id}/roles/{role_id}` | 解绑 |

错误：`404 40400` 组/角色不存在；`400 40000` 绑定系统角色被拒；`409 40900` 角色已绑定（重复）。

返回结构遵循扁平规范（列表 `{items,...}`）。契约同步到 `docs/full-api-design.md` 与 `docs/frontend-admin-backend-a-integration.md`。

## 8. 安全约束

1. **禁止绑系统角色**：`AddGroupRole` 黑名单拦截 `admin`（及未来标记为 system 的角色）。
2. **默认组高敏**：默认组绑定的角色 = 所有新用户的角色，配置需谨慎；建议给默认组只绑「无权限码的基础角色」。A 版下即便误配也不会造成权限提升（组角色不进权限判定）。
3. A 版边界：组角色**不授予管理权限码**。若将来需要「组角色连带权限码生效」（B 版），须另立方案并补：权限码解析合并组角色 + 角色权限变更时失效组员缓存 + 加强系统角色管控。

### 8.1 运营纪律（上线检查单，PM Review #186 补充）
1. **默认组只绑无权限码的基础角色**：黑名单仅强制拦 `admin`，其它「带权限码的业务角色」系统不拦。A 版下绑到默认组也不会提权（组角色不进权限码判定），但属运营纪律红线，配置时人工把关。
2. **B 版升级前必须审计 `group_roles`**：A 版承诺「组角色 = 纯商品键，绝不连带任何权限码」。若升级到 B 版（组角色连带权限码），历史误绑会一次性集体提权，升级前须全量审计现有绑定。
3. **后端乙配 `product_role_access` 时勿误配**：把 `registered_user` 配到**会员专属/受限商品**的 `can_view/can_buy` 会使其对全体注册用户开放。上线验收须专门回归「会员专属商品非会员不可购买」。
4. **定价优先级已确认安全**：`GetPrice` 会员价优先于角色价，`registered_user` 角色价**盖不过**已配置的会员价（既有逻辑，本功能未改动）。Week 4「会员用户按会员价扣费」回归点。

> backlog（非阻塞，PM P4）：删被组绑定角色的 409 错误信息未带「被哪些组绑定」，规模化后排查成本上升，可后续提供「角色→绑定它的组」反查能力。

## 9. 测试计划（回归脚本 + 端到端）

| 用例 | 预期 |
|---|---|
| 组绑角色 X → 组员 `GetUserRoleIDs` 含 X | 通过 |
| 组员对「角色 X 开了 can_buy」的商品 | 可见、可购 |
| 解绑后 | 立即不可见（无需等缓存） |
| 多角色命中定价 | 取最低角色价（现有 `GetPrice` 行为） |
| **端到端**：默认组绑基础角色 → 新注册用户 | 自动可见配给该角色的商品（联动落组） |
| 绑 `admin` 角色 | 被拒（系统角色护栏） |
| 删除被组绑定的角色 | 被拒（占用校验） |
| 组角色**不**赋予管理权限码 | `CheckPermission` 仍为 false（A 版边界确认） |

新增 Python 回归脚本：`tests/test_group_roles.py`（前置数据自建自清）。

## 10. 上线步骤与回滚

**上线**
1. 执行 migrate（000028 建表、000029 基础角色 seed）→ 到 000029；
2. 部署后端 API（仅二进制，无破坏性 DB 操作）；
3. 一次性管理操作：把**默认组**绑定到 `registered_user`（`POST /api/admin/user-groups/{默认组id}/roles`）；
4. 后端乙在目标商品上为 `registered_user` 角色配置 `product_role_access`（can_view/can_buy）与价格；
5. 验收：新注册一个用户 → 应能看到/购买这些商品。

**回滚**：解绑默认组角色（即时止血）→ 必要时回滚二进制 → 末选 migrate down（drop 表 / 删基础角色）。纯增量，回滚低风险。

## 11. 工作量与分工

| 项 | 归属 | 估时 |
|---|---|---|
| group_roles 表/仓库/服务/接口 + GetUserRoleIDs 合并 + 删角色护栏 | 后端甲 | ~1~1.5 人日（含测试） |
| 目标商品的 `product_role_access` / 价格配置 | 后端乙 | 配置，按商品数量 |
| 管理后台「组配角色」页面 | 前端甲 | 后续小活 |

## 12. 待确认决策

1. 基础角色命名：`registered_user`（普通注册用户）是否 OK？
2. 系统角色护栏黑名单：先只拦 `admin` 是否够？是否给 roles 表加 `is_system` 标记以长期治理（本期可不做，黑名单先行）。
3. 默认组绑定基础角色这一步，由谁执行（建议产品经理/管理员在后台操作，上线步骤 §10.3）。
