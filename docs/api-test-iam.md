# 二、IAM 模块（管理员接口）手动测试文档

> ⚠️ **已弃用（2026-06-15）**：本文档为早期分模块版本，未同步 Round 7（D-83/D-89/D-90/D-93/D-94/D-95/D-96）变更，部分接口契约已过期（如分页结构、audit:read 权限）。
> **请改用整合后的最新手册：[`docs/api-test-guide-backend-a.md`](./api-test-guide-backend-a.md)**（覆盖 auth/iam/identity/audit 全部接口，已对齐现行代码）。

## 基本信息

| 项目 | 内容 |
|---|---|
| 模块 | IAM — 角色、权限、用户角色分配、权限覆盖 |
| 负责开发 | 后端工程师甲（后端 A） |
| 代码路径 | `server/internal/modules/iam/` |
| 测试环境 | `http://8.130.9.163:8080` |
| 测试工具 | Apipost |
| 测试日期 | 2026-06-05 |
| 测试结论 | 全部通过（含 BUG-01/02/TODO-01/TODO-02 修复验收） |

---

## 前置条件

所有 IAM 接口均需要 **管理员 Token**（具备 `role:manage` 权限）。

### 准备管理员账号

1. 在 Apipost 注册账号并获取用户 ID（调用 `GET /api/me`）
2. SSH 到测试服务器，手动赋予 admin 角色：

```bash
ssh -p 10003 pc@8.130.9.163
mysql -h 127.0.0.1 -P 13306 -u molin -pmolin_password molin -e "
INSERT IGNORE INTO user_roles (user_id, role_id)
SELECT {YOUR_USER_ID}, id FROM roles WHERE code='admin';
"
```

3. 重新登录获取新 `access_token`（旧 token 不含新角色）

---

## 全局配置（Apipost）

```
Base URL：http://8.130.9.163:8080
全局 Header：Content-Type: application/json
管理员 Header：Authorization: Bearer <admin_access_token>
```

---

## 接口列表

### 1. 查询权限列表

- **方法：** `GET`
- **URL：** `/api/admin/permissions`
- **是否需要 Token：** 是（admin）
- **查询参数：** `page`（默认 1）、`page_size`（默认 20）

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": {
    "list": [
      {
        "id": 1,
        "code": "role:manage",
        "name": "角色管理",
        "resource": "role",
        "action": "manage"
      },
      {
        "id": 2,
        "code": "identity:review",
        "name": "实名审核",
        "resource": "identity",
        "action": "review"
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 5,
      "total": 2
    }
  }
}
```

> 记下常用权限的 `id`，接口 8（设置权限覆盖）会用到。

---

### 2. 创建角色

- **方法：** `POST`
- **URL：** `/api/admin/roles`
- **是否需要 Token：** 是（admin）
- **请求 Body：**

```json
{
  "code": "test_role",
  "name": "测试角色",
  "description": "用于手动测试"
}
```

- **成功响应（201）：**

```json
{
  "code": 0,
  "data": {
    "id": 2,
    "code": "test_role",
    "name": "测试角色"
  }
}
```

> 记下返回的角色 `id`，后续接口（更新、分配、删除）会用到。

---

### 3. 查询角色列表

- **方法：** `GET`
- **URL：** `/api/admin/roles`
- **是否需要 Token：** 是（admin）
- **查询参数：** `page`（默认 1）、`page_size`（默认 20）

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": {
    "list": [
      {
        "id": 1,
        "code": "admin",
        "name": "超级管理员",
        "description": "系统内置管理员角色"
      },
      {
        "id": 2,
        "code": "test_role",
        "name": "测试角色"
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 2,
      "total": 3
    }
  }
}
```

---

### 4. 更新角色

- **方法：** `PUT`
- **URL：** `/api/admin/roles/{id}`
- **是否需要 Token：** 是（admin）
- **请求 Body：**

```json
{
  "name": "测试角色（已更新）",
  "description": "更新后的描述"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

> 注意：`code` 字段不支持修改，只能更新 `name` 和 `description`。

---

### 5. 分配角色给用户

- **方法：** `POST`
- **URL：** `/api/admin/users/{id}/roles`
- **是否需要 Token：** 是（admin）
- **请求 Body：**

```json
{
  "role_id": 2,
  "reason": "手动测试分配"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

- **失败场景：重复分配同一角色（BUG-02 修复验收）**

  对同一用户重复分配已拥有的角色，应返回 `409`：

```json
{
  "code": 40900,
  "message": "该用户已拥有此角色",
  "data": null
}
```

---

### 6. 查询用户角色列表

- **方法：** `GET`
- **URL：** `/api/admin/users/{id}/roles`
- **是否需要 Token：** 是（admin）
- **查询参数：** `page`（默认 1）、`page_size`（默认 20）

- **成功响应（200）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": 1,
        "code": "admin",
        "name": "超级管理员",
        "description": "系统内置管理员角色",
        "created_at": "2026-06-05T07:04:04+08:00"
      },
      {
        "id": 4,
        "code": "test_role_1780643206",
        "name": "测试角色（已更新）",
        "created_at": "2026-06-05T15:06:47+08:00"
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 2
    }
  }
}
```

> BUG-01 已修复：响应字段改为小写 snake_case，新增角色 `code`、`name`、`description` 字段，并带分页结构。

---

### 7. 撤销用户角色

- **方法：** `DELETE`
- **URL：** `/api/admin/users/{id}/roles/{role_id}`
- **是否需要 Token：** 是（admin）
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

> 撤销后调接口 6 验证，对应 role_id 条目应消失。

---

### 8. 设置用户权限覆盖

- **方法：** `POST`
- **URL：** `/api/admin/users/{id}/permission-overrides`
- **是否需要 Token：** 是（admin）
- **请求 Body：**

```json
{
  "permission_id": 1,
  "effect": "deny",
  "reason": "手动测试覆盖"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

- **安全场景（effect 大写被拦截）：**

```json
{
  "permission_id": 1,
  "effect": "DENY",
  "reason": "测试大写被拦截"
}
```

应返回 `400`：
```json
{
  "code": 40000,
  "message": "effect 只能为 allow 或 deny"
}
```

> `effect` 只接受小写 `allow` 或 `deny`，防止非标准值绕过 deny 覆盖逻辑。

---

### 9. 查询用户权限覆盖列表

- **方法：** `GET`
- **URL：** `/api/admin/users/{id}/permission-overrides`
- **是否需要 Token：** 是（admin）
- **查询参数：** `page`（默认 1）、`page_size`（默认 20）、`effect`（可选：`allow` / `deny`，不传则返回全部）、`permission_code`（可选：按权限 code 精确过滤）
- **无需 Body**

- **成功响应（200，有数据时）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": 1,
        "permission_code": "role:manage",
        "effect": "deny",
        "reason": "手动测试覆盖",
        "created_at": "2026-06-05T..."
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 1
    }
  }
}
```

- **成功响应（200，无数据时）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 0
    }
  }
}
```

- **分页参数验收（TODO-02 修复验收，2026-06-05）：**

| 用例 | 请求 | 期望 | 实际 | 结果 |
|---|---|---|---|---|
| 不带分页参数 | `GET .../permission-overrides` | code=0，page=1，page_size=20 | 符合 | 通过 |
| 带 page_size=2 | `GET .../permission-overrides?page=1&page_size=2` | code=0，page_size=2 | 符合 | 通过 |
| 超范围页码 page=999 | `GET .../permission-overrides?page=999&page_size=10` | code=0，list 为空 | 符合 | 通过 |
| 无 Token | 不带 Authorization Header | code=40001，未登录 | 符合 | 通过 |

> 记下覆盖记录的 `id`，接口 10 删除时会用到。

---

### 10. 删除用户权限覆盖

- **方法：** `DELETE`
- **URL：** `/api/admin/users/{id}/permission-overrides/{override_id}`
- **是否需要 Token：** 是（admin）
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

> 删除后调接口 9 验证，该覆盖记录应消失。

---

### 11. 删除角色

- **方法：** `DELETE`
- **URL：** `/api/admin/roles/{id}`
- **是否需要 Token：** 是（admin）
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

> 放在最后执行，避免中途删除后其他分配类接口无角色可用。

---

## 测试流程（推荐顺序）

```
1.  GET  /api/admin/permissions              → 记下 permission id
2.  POST /api/admin/roles                   → 创建 test_role，记下 role id
3.  GET  /api/admin/roles                   → 验证角色已创建，检查分页结构
4.  PUT  /api/admin/roles/{id}              → 更新 test_role 名称
5.  POST /api/admin/users/{id}/roles        → 分配 test_role 给用户
    POST /api/admin/users/{id}/roles        → 重复分配，验证返回 409（BUG-02 修复验收）
6.  GET  /api/admin/users/{id}/roles        → 验证分配成功，检查 code/name 字段（BUG-01 修复验收）
7.  DELETE /api/admin/users/{id}/roles/{role_id}  → 撤销一个角色
8.  POST /api/admin/users/{id}/permission-overrides  → 设置 deny 覆盖
    POST /api/admin/users/{id}/permission-overrides  → 测试 effect="DENY" 被拦截（期望 400）
9.  GET  /api/admin/users/{id}/permission-overrides  → 验证分页结构（TODO-02 修复验收），记下 override id
    GET  /api/admin/users/{id}/permission-overrides?page=1&page_size=2  → 验证 page_size 生效
    GET  /api/admin/users/{id}/permission-overrides?page=999&page_size=10  → 超范围返回空列表
10. DELETE /api/admin/users/{id}/permission-overrides/{override_id}  → 删除覆盖
11. DELETE /api/admin/roles/{id}            → 删除 test_role
```

---

## 安全场景覆盖

| 场景 | 期望结果 | 验证方式 |
|---|---|---|
| 无 Token 访问管理接口 | 401 | 不带 Header 请求任意管理接口 |
| effect 填 `"DENY"`（大写）| 400 | POST permission-overrides 时填大写 |
| effect 填 `"Allow"`（混合大小写）| 400 | POST permission-overrides 时填混合大小写 |
| 撤销角色后再查用户角色列表 | 对应记录消失 | DELETE 后 GET 验证 |
| 删除覆盖后再查覆盖列表 | 对应记录消失 | DELETE 后 GET 验证 |
| 重复分配同一角色 | 409，code=40900 | 对同一用户同一角色连续 POST 两次 |

---

## 已知问题（待优化）

| 编号 | 接口 | 问题描述 | 优先级 | 状态 |
|---|---|---|---|---|
| IAM-BUG-01 | `GET /api/admin/users/{id}/roles` | 响应字段为大写（`ID`、`UserID`、`RoleID`），缺少角色 `code`、`name`，不符合 API 响应规范 | P2 | 已修复 |
| IAM-BUG-02 | `POST /api/admin/users/{id}/roles` | 重复分配同一角色时触发 DB 唯一键冲突，应返回 `409` 但实际返回 `500` | P1 | 已修复 |
| IAM-TODO-01 | 所有列表接口 | 当前全量返回数据，无分页支持 | P2 | 已修复 |
| IAM-TODO-02 | `GET /api/admin/users/{id}/permission-overrides` | 全量返回权限覆盖列表，无分页结构，与其他列表接口规范不一致 | P2 | 已修复（2026-06-05） |

---

## 错误码说明

| 错误码 | 含义 |
|---|---|
| 40000 | 请求参数错误（含 effect 非法值） |
| 40001 | 未登录或 Token 无效 |
| 40003 | 无操作权限（缺少 role:manage） |
| 40900 | 该用户已拥有此角色（重复分配） |
| 50000 | 服务器内部错误 |
