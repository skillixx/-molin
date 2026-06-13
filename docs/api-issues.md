# 后端 A 接口已知问题 & 待办清单

**记录人：** 测试工程师 / 产品经理
**最后更新：** 2026-06-13
**负责模块：** auth / iam / identity（后端工程师甲）

---

## 问题列表

| 编号 | 模块 | 接口 | 问题描述 | 优先级 | 状态 |
|---|---|---|---|---|---|
| BUG-01 | IAM | `GET /api/admin/users/{id}/roles` | 响应字段为 Go 结构体大写（`ID`、`UserID`、`RoleID`），缺少角色 `code`、`name`，不符合 API 响应规范 | P2 | 已修复（2026-06-05） |
| BUG-02 | IAM | `POST /api/admin/users/{id}/roles` | 重复分配同一角色时触发 DB 唯一键冲突，应返回 `409` 但实际返回 `500` | P1 | 已修复（2026-06-05） |
| TODO-01 | IAM / Identity | 所有列表接口 | 当前全量返回数据，无分页支持，数据量大时存在性能风险 | P2 | 已修复（2026-06-05） |
| BUG-03 | Auth | `POST /api/auth/verification-codes/*` scene=register | 已注册账号仍可收到注册验证码，未做账号唯一性拦截 | P1 | 已修复（2026-06-10） |
| BUG-04 | Auth | `POST /api/auth/verification-codes/phone` scene=login | 未注册手机号也可收到登录验证码，应提示用户先注册 | P1 | 已修复（2026-06-10） |
| BUG-05 | Auth/IAM/Identity | 所有管理端接口 | 管理员双重认证（verify-phone/email）未被中间件强制校验，登录即可直接访问所有管理权限 | P1 | 已修复（2026-06-10） |
| BUG-06 | IAM | `POST /api/admin/users/{id}/permission-overrides` | 测试服务器 `user_permission_overrides` 表缺少 `permission_code` 字段，导致设置接口返回 500 | P1 | 已修复（2026-06-10，手动执行 ALTER TABLE）|
| TODO-02 | IAM | `GET /api/admin/users/{id}/permission-overrides` | 全量返回权限覆盖列表，无分页结构，与其他列表接口规范不一致 | P2 | 已修复（2026-06-05） |
| TODO-03 | IAM/Auth | `GET /api/me/permissions`（缺失） | 缺少返回当前登录用户有效权限码集合的只读接口，前端无法做按钮级权限控制，只能等接口 403 才知道无权限 | P2 | 待开发（2026-06-13） |
| TODO-04 | IAM | `GET /api/admin/roles/{id}/permissions`（缺失） | 缺少返回指定角色权限码列表的只读接口；`PATCH /api/admin/roles/{id}/permissions` 为全量替换写接口，管理后台无法预填充当前权限集合 | P2 | 待开发（2026-06-13） |
| TODO-05 | IAM | `GET /api/admin/users/{id}/effective-permissions`（缺失） | 缺少返回用户最终生效权限码（角色∪分组叠加 overrides）的只读接口，`getAllUserPermCodes` 仅为 service 内部私有方法，未对外导出，管理后台无法做用户权限排查/一览 | P3 | 待开发（2026-06-13） |

---

## 详细说明

### BUG-01 — 用户角色列表响应字段大写

**接口：** `GET /api/admin/users/{id}/roles`

**修复前响应：**
```json
{
  "data": [
    { "ID": 12, "UserID": 13, "RoleID": 1, "CreatedAt": "..." }
  ]
}
```

**修复后响应：**
```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      { "id": 1, "code": "admin", "name": "超级管理员", "description": "系统内置管理员角色", "created_at": "2026-06-05T07:04:04+08:00" }
    ],
    "pagination": { "page": 1, "page_size": 20, "total": 2 }
  }
}
```

**修复方式：** 在 `handler/iam_handler.go` 的 `GetUserRoles` 中将返回值映射为 DTO，返回角色详情而非 `user_roles` 表的 model 结构体，并补充分页结构。

**验收结论：** 通过（2026-06-05）

---

### BUG-02 — 重复分配角色返回 500

**接口：** `POST /api/admin/users/{id}/roles`

**复现步骤：** 对同一用户分配同一角色两次。

**修复前行为：** DB 唯一键（`uk_user_roles`）冲突，服务返回 `500`。

**修复后行为：** 返回 `409 Conflict`，业务码 `40900`，提示"该用户已拥有此角色"。

**修复方式：** 在 `service/iam_service.go` 的 `AssignRole` 中捕获唯一键冲突错误，转换为业务错误返回。

**验收结论：** 通过（2026-06-05）

---

### TODO-01 — 列表接口缺少分页

**受影响接口：**

| 接口 | 说明 |
|---|---|
| `GET /api/admin/roles` | 全量返回所有角色 |
| `GET /api/admin/permissions` | 全量返回所有权限 |
| `GET /api/admin/users/{id}/roles` | 全量返回用户所有角色 |
| `GET /api/admin/identity-verifications` | 全量返回所有待审记录 |

**实现方案：**

请求参数：
```
GET /api/admin/identity-verifications?page=1&page_size=20
```

统一响应结构：
```json
{
  "code": 0,
  "data": {
    "list": [...],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 100
    }
  }
}
```

**验收结论：** 通过（2026-06-05）

---

### TODO-02 — permission-overrides 列表缺少分页

**接口：** `GET /api/admin/users/{id}/permission-overrides`

**问题描述：** 原接口直接返回数组，无分页结构，与其他列表接口（roles、permissions 等）规范不一致。

**修复前响应：**
```json
{
  "code": 0,
  "data": [
    { "id": 1, "permission_id": 1, "effect": "deny", "reason": "...", "created_at": "..." }
  ]
}
```

**修复后响应：**
```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [...],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 0
    }
  }
}
```

**验收用例（2026-06-05）：**

| 用例 | 请求 | 期望 | 实际 | 结果 |
|---|---|---|---|---|
| 不带分页参数 | `GET /api/admin/users/13/permission-overrides` | code=0，page=1，page_size=20，list 为空 | 完全符合 | 通过 |
| 带 page_size=2 | `GET .../permission-overrides?page=1&page_size=2` | code=0，page_size=2，list 为空 | 完全符合 | 通过 |
| 超范围页码 page=999 | `GET .../permission-overrides?page=999&page_size=10` | code=0，page=999，list 为空 | 完全符合 | 通过 |
| 无 Token | 不带 Authorization Header | code=40001，"未登录" | 完全符合 | 通过 |

**验收结论：** 通过（2026-06-05）。4 条用例全部符合期望，分页结构与其他列表接口规范一致。

---

### TODO-03 — 缺少「当前用户权限码」查询接口

**涉及接口（缺失）：** `GET /api/me/permissions`

**现状：** `GET /api/me`（`server/internal/modules/auth/handler/auth_handler.go:139-148`，返回 `dto.UserInfo`）只返回 `email_verified`/`real_name_status`/`admin_phone_verified` 等字段，不包含角色/权限码列表。

**问题：** 前端无法做"按钮级权限控制"（根据权限决定菜单/按钮是否显示），只能等接口返回 403 后才知道无权限，体验差且容易出现"先展示后报错"的闪烁问题。

**建议方案：**
- 新增 `GET /api/me/permissions`（需 Bearer Token，无需额外权限码）
- 返回当前登录用户的有效权限码集合（角色权限 ∪ 分组权限，再叠加 `user_permission_overrides` 的 allow/deny 调整后的最终结果）
- 复用 `service/iam_service.go` 中 `getAllUserPermCodes`（第 188 行）的计算逻辑

**状态：** 待开发，已记录至 `docs/dev-tasks.md` A-10（2026-06-13）

---

### TODO-04 — 缺少「角色权限码」查询接口

**涉及接口（缺失）：** `GET /api/admin/roles/{id}/permissions`

**现状：** `GET /api/admin/roles/{id}`（`iam_handler.go:310-330`，`GetRole`）只返回 `id/code/name/description`；权限相关只有 `PATCH /api/admin/roles/{id}/permissions`（`SetRolePermissions`，全量替换写接口），没有对应的读接口。

**问题：** 管理后台无法展示"该角色当前有哪些权限"，编辑权限时前端也拿不到当前值做预填充（全量替换接口必须先知道当前集合才能正确增删）。

**建议方案：**
- 新增 `GET /api/admin/roles/{id}/permissions`（需 `role:manage` 权限码）
- 返回该角色的权限码列表（数组）
- 复用 `permissionRepo.FindByRoleIDs`

**状态：** 待开发，已记录至 `docs/dev-tasks.md` A-11（2026-06-13）

---

### TODO-05 — 缺少「用户最终生效权限码」查询接口

**涉及接口（缺失）：** `GET /api/admin/users/{id}/effective-permissions`

**现状：** 权限计算逻辑 `getAllUserPermCodes`（`server/internal/modules/iam/service/iam_service.go:188`）是 service 内部私有方法（角色权限 ∪ 分组权限，再叠加用户 `user_permission_overrides` 的 allow/deny），从未导出。完全没有对应接口。

**问题：** 管理后台无法做"用户权限排查/一览"功能，只能运维/开发直连数据库写 SQL 手动计算。

**建议方案：**
- 新增 `GET /api/admin/users/{id}/effective-permissions`（需 `role:manage` 权限码）
- 封装并导出 `getAllUserPermCodes` 计算逻辑（含 overrides 调整明细），返回该用户最终生效的权限码列表

**状态：** 待开发，已记录至 `docs/dev-tasks.md` A-12（2026-06-13）

---

## 处理原则

- **P1（BUG-02）**：影响生产稳定性，建议在下一个 PR 中修复后重新验收
- **P2（BUG-01、TODO-01、TODO-02、TODO-03、TODO-04）**：不影响核心功能，可排入下一迭代
- **P3（TODO-05）**：功能缺口，不紧急，可排入后续迭代
