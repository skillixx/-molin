# 二、IAM 模块（管理员接口）手动测试文档

## 基本信息

| 项目 | 内容 |
|---|---|
| 模块 | IAM — 角色、权限、用户角色分配、权限覆盖 |
| 负责开发 | 后端工程师甲（后端 A） |
| 代码路径 | `server/internal/modules/iam/` |
| 测试环境 | `http://8.130.9.163:8080` |
| 测试工具 | Apipost |
| 测试日期 | 2026-06-05 |
| 测试结论 | 功能通过，发现 2 处响应格式问题（见末尾） |

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
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": [
    {
      "id": 1,
      "code": "role:manage",
      "name": "角色管理",
      "resource": "role",
      "action": "manage"
    }
  ]
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
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": [
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
  ]
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

---

### 6. 查询用户角色列表

- **方法：** `GET`
- **URL：** `/api/admin/users/{id}/roles`
- **是否需要 Token：** 是（admin）
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": [
    {
      "ID": 12,
      "UserID": 13,
      "RoleID": 1,
      "CreatedAt": "2026-06-05T11:47:35+08:00"
    },
    {
      "ID": 16,
      "UserID": 13,
      "RoleID": 2,
      "CreatedAt": "2026-06-05T20:26:24+08:00"
    }
  ]
}
```

> ⚠️ 已知问题：响应字段为 Go 结构体大写字段名（`ID`、`UserID`、`RoleID`），缺少角色 `code`、`name` 字段，不符合 API 响应规范。待后端工程师甲优化 DTO 后修复。

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
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": [
    {
      "id": 1,
      "permission_id": 1,
      "effect": "deny",
      "reason": "手动测试覆盖",
      "created_at": "2026-06-05T..."
    }
  ]
}
```

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
3.  GET  /api/admin/roles                   → 验证角色已创建
4.  PUT  /api/admin/roles/{id}              → 更新 test_role 名称
5.  POST /api/admin/users/{id}/roles        → 分配 test_role 给用户
6.  GET  /api/admin/users/{id}/roles        → 验证分配成功
7.  DELETE /api/admin/users/{id}/roles/{role_id}  → 撤销一个角色
8.  POST /api/admin/users/{id}/permission-overrides  → 设置 deny 覆盖
    POST /api/admin/users/{id}/permission-overrides  → 测试 effect="DENY" 被拦截（期望 400）
9.  GET  /api/admin/users/{id}/permission-overrides  → 记下 override id
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

---

## 已知问题（待优化）

| 编号 | 接口 | 问题描述 | 优先级 |
|---|---|---|---|
| IAM-BUG-01 | `GET /api/admin/users/{id}/roles` | 响应字段为大写（`ID`、`UserID`、`RoleID`），缺少角色 `code`、`name`，不符合 API 响应规范 | P2 |
| IAM-BUG-02 | `POST /api/admin/users/{id}/roles` | 重复分配同一角色时触发 DB 唯一键冲突，应返回 `409` 但实际返回 `500` | P1 |

---

## 错误码说明

| 错误码 | 含义 |
|---|---|
| 40000 | 请求参数错误（含 effect 非法值） |
| 40001 | 未登录或 Token 无效 |
| 40003 | 无操作权限（缺少 role:manage） |
| 50000 | 服务器内部错误 |
