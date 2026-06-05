# 后端 A 接口已知问题 & 待办清单

**记录人：** 测试工程师 / 产品经理
**最后更新：** 2026-06-05
**负责模块：** auth / iam / identity（后端工程师甲）

---

## 问题列表

| 编号 | 模块 | 接口 | 问题描述 | 优先级 | 状态 |
|---|---|---|---|---|---|
| BUG-01 | IAM | `GET /api/admin/users/{id}/roles` | 响应字段为 Go 结构体大写（`ID`、`UserID`、`RoleID`），缺少角色 `code`、`name`，不符合 API 响应规范 | P2 | 已修复（2026-06-05） |
| BUG-02 | IAM | `POST /api/admin/users/{id}/roles` | 重复分配同一角色时触发 DB 唯一键冲突，应返回 `409` 但实际返回 `500` | P1 | 已修复（2026-06-05） |
| TODO-01 | IAM / Identity | 所有列表接口 | 当前全量返回数据，无分页支持，数据量大时存在性能风险 | P2 | 已修复（2026-06-05） |
| TODO-02 | IAM | `GET /api/admin/users/{id}/permission-overrides` | 全量返回权限覆盖列表，无分页结构，与其他列表接口规范不一致 | P2 | 已修复（2026-06-05） |

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

## 处理原则

- **P1（BUG-02）**：影响生产稳定性，建议在下一个 PR 中修复后重新验收
- **P2（BUG-01、TODO-01、TODO-02）**：不影响核心功能，可排入下一迭代
