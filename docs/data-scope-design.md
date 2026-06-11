# 数据范围（Data Scope）设计文档

> 状态：**设计待评审**（2026-06-11 提出）
> 关联模块：iam / auth / billing / finance
> 背景：需要把特定批次用户分配给特定管理员管理（如「上海区域用户 → 上海管理员」「师范大学用户 → 师范大学管理员」），并限制这些管理员只能使用部分功能（如查看用户数量、使用费用）。

---

## 1. 问题定性：两种正交的权限

当前 IAM 模块实现的是 **功能权限（RBAC）**，回答的是「这个管理员**能不能**执行某个操作」。

本需求要的是 **数据权限 / 数据范围（Data Scope）**，回答的是「这个管理员能看到的数据**限定在哪个范围**」。

二者正交，最终生效条件是「功能权限 **AND** 数据范围」：

| 需求拆解 | 用哪套机制 | 现状 |
|---|---|---|
| 「只让他们查看用户数量、费用」 | **功能权限**：角色只配 `user:list` / `stats:view` / `billing:view`，不配 `user:edit` / `user:disable` | 现有 RBAC 已能做 |
| 「上海的用户给上海管理员」 | **数据权限**：限定可见 user_id 范围 | **需要新增，当前完全没有** |

> 结论：**不需要重写 IAM**，而是在它旁边新增一层「数据范围」能力，与 RBAC 叠加生效。

---

## 2. 评审已确认的设计选型

| 决策点 | 结论 |
|---|---|
| 分组建模 | **通用分组表 + `type` 字段**（region / org / custom），上海、师范大学都是其中一条记录，不为每个维度单独建表 |
| 用户归属 | **多对多**：一个用户可同时属于多个分组（既在「上海」又在「师范大学」），任一管辖该组的管理员都能看到他 |
| 层级 | 暂不启用层级，但表结构预留 `parent_id`，未来可支持「上海 > 浦东」 |

---

## 3. 数据模型

```sql
-- 3.1 用户分组（上海区域 / 师范大学 / 任意自定义组）
CREATE TABLE IF NOT EXISTS user_groups (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  code        VARCHAR(128) NOT NULL,            -- 唯一编码，如 region_shanghai
  name        VARCHAR(128) NOT NULL,            -- 显示名，如 上海区域
  type        VARCHAR(32)  NOT NULL DEFAULT 'custom', -- region / org / custom
  parent_id   BIGINT UNSIGNED NULL,             -- 预留层级，暂不使用
  description VARCHAR(512) NULL,
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_groups_code (code),
  KEY idx_user_groups_type (type),
  KEY idx_user_groups_parent (parent_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 3.2 用户 ↔ 分组（多对多）
CREATE TABLE IF NOT EXISTS user_group_members (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    BIGINT UNSIGNED NOT NULL,
  group_id   BIGINT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_group_members (user_id, group_id),
  KEY idx_ugm_group (group_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 3.3 管理员 ↔ 管辖分组（多对多）
CREATE TABLE IF NOT EXISTS admin_group_scopes (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  admin_user_id BIGINT UNSIGNED NOT NULL,        -- 管理员自己的 user.id
  group_id      BIGINT UNSIGNED NOT NULL,
  created_by    BIGINT UNSIGNED NULL,
  created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_admin_group_scopes (admin_user_id, group_id),
  KEY idx_ags_group (group_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

实体关系：

```
user_groups 1 ──< user_group_members >── N users        (谁属于这个组)
user_groups 1 ──< admin_group_scopes >── N admin_users   (谁能管这个组)

某管理员可见用户 = 该管理员管辖的所有组下的所有用户（去重）
```

---

## 4. 数据范围解析与注入（技术核心）

### 4.1 不能让每个 handler 自己过滤

数据范围必须做成**共享组件**，所有「列表 / 详情 / 统计」查询强制走它。任何一个接口漏了过滤，就是一次越权。

### 4.2 解析结果结构

```go
// server/internal/middleware/scope.go（建议位置）
type DataScope struct {
    All     bool      // true = 超级管理员，不受限
    UserIDs []uint64  // All=false 时，限定可见的 user_id 集合
}
```

判定规则：
1. 管理员拥有 `scope:all` 权限（或 admin 超管角色）→ `All = true`
2. 否则查 `admin_group_scopes` → 得到管辖的 group_id 集合 → JOIN `user_group_members` → 得到可见 `UserIDs`
3. 没有任何管辖组 → `UserIDs` 为空 → 查询结果为空（而非报错）

### 4.3 缓存

可见 user_id 集合可缓存进 Redis，复用现有 `perm:user:{id}` 的模式：

```
key:  scope:user:{adminID}
TTL:  5 分钟
失效: 修改 admin_group_scopes / user_group_members 时
```

### 4.4 在 repository 层统一套用

```go
func applyScope(db *gorm.DB, scope DataScope, idColumn string) *gorm.DB {
    if scope.All {
        return db
    }
    if len(scope.UserIDs) == 0 {
        return db.Where("1 = 0") // 无管辖范围，结果为空
    }
    return db.Where(idColumn+" IN ?", scope.UserIDs)
}
// 例：users 表用 applyScope(db, scope, "id")
//     billing 流水表用 applyScope(db, scope, "user_id")
```

> ⚠️ **跨模块一致性**：「使用费用」数据在 billing / finance 模块，不在 auth。数据范围必须是**跨模块共享能力**（放 `middleware` 或 `pkg`），auth 查用户、billing 查费用都用同一套解析结果。否则上海管理员能在费用接口里看到全国数据。

---

## 5. 与现有 RBAC 如何配合

「只让他们看数量和费用」由 RBAC 解决：新增角色，只绑只读权限码。

| 角色 | 绑定的功能权限 | 叠加的数据范围 |
|---|---|---|
| `admin`（超管） | 全部 + `scope:all` | 全部用户 |
| `region_admin`（区域管理员） | `user:list`、`stats:view`、`billing:view` | 仅管辖组下用户 |

请求链路：`RequireAuth → RequirePerm(功能权限) → 注入 DataScope → repository 套用范围`

---

## 6. 接口设计（新增）

### 6.1 分组管理（超管用）

```text
GET    /api/admin/user-groups                      查分组列表（?type= 过滤）
POST   /api/admin/user-groups                      建分组
PUT    /api/admin/user-groups/{id}                 改分组
DELETE /api/admin/user-groups/{id}                 删分组
POST   /api/admin/user-groups/{id}/members         给分组批量加用户
DELETE /api/admin/user-groups/{id}/members/{uid}   从分组移除用户
GET    /api/admin/users/{id}/groups                查某用户所属分组
```

### 6.2 管理员管辖范围（超管用）

```text
GET    /api/admin/admins/{id}/scopes               查某管理员管辖的分组
POST   /api/admin/admins/{id}/scopes               给管理员分配管辖分组
DELETE /api/admin/admins/{id}/scopes/{group_id}    取消管辖
```

### 6.3 受数据范围约束的查询（区域管理员用）

```text
GET    /api/admin/users                  现有接口，叠加 scope 过滤
GET    /api/admin/stats/users            新增：用户数量统计（按 scope）
GET    /api/admin/stats/billing          新增：费用汇总（按 scope）
```

新增权限码（需按规矩同步建 seed migration）：

| 权限码 | 说明 |
|---|---|
| `scope:all` | 数据范围不受限（超管标记） |
| `group:manage` | 管理分组与管辖关系 |
| `stats:view` | 查看统计数据 |
| `billing:view` | 查看费用（若已存在则复用） |

---

## 7. 分阶段落地计划

**阶段一 — 数据范围基础设施**
- migration：`user_groups` / `user_group_members` / `admin_group_scopes` 三张表
- `middleware/scope.go`：解析 DataScope + Redis 缓存 + 失效
- 分组管理、管辖关系管理接口（6.1 / 6.2）

**阶段二 — 接入查询接口**
- `GET /api/admin/users`、用户详情套用 scope
- 新增 `GET /api/admin/stats/users`、`GET /api/admin/stats/billing`（天生带 scope）
- billing / finance 查询接口套用 scope

**阶段三 — 角色与权限**
- 新增角色 `region_admin`，绑定只读权限码
- seed migration（声明权限码必须同步建 seed —— 项目反复出现的 P1 根因）
- 前端：分组管理页、管理员管辖配置页、区域管理员的统计视图

---

## 8. 安全与边界注意事项

- **越权防线**：任何返回用户数据的接口都必须套 scope，新增接口要纳入「数据范围 checklist」review。
- **统计接口同样受限**：用户数量、费用汇总也必须按 scope 聚合，不能返回全局数字。
- **空范围语义**：管理员无任何管辖组时返回空集，不报错、不降级为全量。
- **缓存失效**：调整用户分组或管理员管辖关系后，必须清 `scope:user:{adminID}` 缓存。
- **管理员也是 users 表记录**：`admin_group_scopes.admin_user_id` 指向 users.id，注意区分「作为管理员的账号」与「被管理的普通用户」。

---

## 9. 已确认决策（2026-06-11 评审通过）

1. **统计口径：实时聚合**。查询时实时 `COUNT` / `SUM` 并叠加 scope 过滤，不建预聚合表。数据准确、实现简单；现阶段用户量可承受，后续如有性能压力再引入缓存 / 预聚合。
2. **操作审计：要，复用 `audit_logs`**。分组、成员、管辖关系的增删改均写入现有 `audit_logs`（属高敏感权限类变更，需可追责）。记录 `module=scope`，`action` 如 `group.create` / `group.member.add` / `admin.scope.assign` 等，`target_type` / `target_id` 标明操作对象。
3. **删除分组：禁止删除非空组**。组内仍有用户成员或仍被管理员管辖时拒绝删除并返回提示；需先手动清空成员与管辖关系后才能删除。避免误删导致权限静默丢失。
