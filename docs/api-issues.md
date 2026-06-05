# 后端 A 接口已知问题 & 待办清单

**记录人：** 测试工程师 / 产品经理
**最后更新：** 2026-06-05
**负责模块：** auth / iam / identity（后端工程师甲）

---

## 问题列表

| 编号 | 模块 | 接口 | 问题描述 | 优先级 | 状态 |
|---|---|---|---|---|---|
| BUG-01 | IAM | `GET /api/admin/users/{id}/roles` | 响应字段为 Go 结构体大写（`ID`、`UserID`、`RoleID`），缺少角色 `code`、`name`，不符合 API 响应规范 | P2 | 待修复 |
| BUG-02 | IAM | `POST /api/admin/users/{id}/roles` | 重复分配同一角色时触发 DB 唯一键冲突，应返回 `409` 但实际返回 `500` | P1 | 待修复 |
| TODO-01 | IAM / Identity | 所有列表接口 | 当前全量返回数据，无分页支持，数据量大时存在性能风险 | P2 | 待排期 |

---

## 详细说明

### BUG-01 — 用户角色列表响应字段大写

**接口：** `GET /api/admin/users/{id}/roles`

**当前响应：**
```json
{
  "data": [
    { "ID": 12, "UserID": 13, "RoleID": 1, "CreatedAt": "..." }
  ]
}
```

**期望响应：**
```json
{
  "data": [
    { "id": 1, "code": "admin", "name": "超级管理员", "created_at": "..." }
  ]
}
```

**修复方向：** 在 `handler/iam_handler.go` 的 `GetUserRoles` 中将返回值映射为 DTO，而不是直接返回 `user_roles` 表的 model 结构体。

---

### BUG-02 — 重复分配角色返回 500

**接口：** `POST /api/admin/users/{id}/roles`

**复现步骤：** 对同一用户分配同一角色两次。

**当前行为：** DB 唯一键（`uk_user_roles`）冲突，服务返回 `500`。

**期望行为：** 返回 `409 Conflict`，业务码 `40900`，提示"该用户已拥有此角色"。

**修复方向：** 在 `service/iam_service.go` 的 `AssignRole` 中捕获唯一键冲突错误，转换为业务错误返回。

---

### TODO-01 — 列表接口缺少分页

**受影响接口：**

| 接口 | 说明 |
|---|---|
| `GET /api/admin/roles` | 全量返回所有角色 |
| `GET /api/admin/permissions` | 全量返回所有权限 |
| `GET /api/admin/users/{id}/roles` | 全量返回用户所有角色 |
| `GET /api/admin/identity-verifications` | 全量返回所有待审记录 |

**建议方案：**

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

**排期建议：** Week 2 开发用户管理、订单等大数据量模块时同步补充，或单独作为技术优化任务排期。当前 Week 1 数据量小，不影响功能验收。

---

## 处理原则

- **P1（BUG-02）**：影响生产稳定性，建议在下一个 PR 中修复后重新验收
- **P2（BUG-01、TODO-01）**：不影响核心功能，可排入下一迭代
