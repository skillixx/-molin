# 三、Identity 模块（实名认证）手动测试文档

> ⚠️ **已弃用（2026-06-15）**：本文档为早期分模块版本，未同步 Round 7 变更（如审核请求体 D-89 `{action,reject_reason}`、查询路由 D-90 `/latest`、分页扁平化 D-95）。
> **请改用整合后的最新手册：[`docs/api-test-guide-backend-a.md`](./api-test-guide-backend-a.md)**（覆盖 auth/iam/identity/audit 全部接口，已对齐现行代码）。

## 基本信息

| 项目 | 内容 |
|---|---|
| 模块 | Identity — 实名认证提交、状态查询、管理员审核 |
| 负责开发 | 后端工程师甲（后端 A） |
| 代码路径 | `server/internal/modules/identity/` |
| 测试环境 | `http://8.130.9.163:8080` |
| 测试工具 | Apipost |
| 测试日期 | 2026-06-05 |
| 测试结论 | 全部通过 |

---

## 前置条件

| 接口 | 所需权限 |
|---|---|
| POST /api/identity/verifications | 普通用户 token |
| GET /api/identity/verifications/me | 普通用户 token |
| GET /api/admin/identity-verifications | 管理员 token（`identity:review` 权限） |
| GET /api/admin/identity-verifications/{id} | 管理员 token（`identity:review` 权限） |
| PATCH /api/admin/identity-verifications/{id}/review | 管理员 token（`identity:review` 权限） |

### 管理员账号缺少 identity:review 权限时

SSH 到测试服务器执行：

```bash
mysql -h 127.0.0.1 -P 13306 -u molin -pmolin_password molin -e "
INSERT IGNORE INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p
WHERE r.code='admin' AND p.code='identity:review';
"
```

执行后重新登录获取新 token。

---

## 全局配置（Apipost）

```
Base URL：http://8.130.9.163:8080
全局 Header：Content-Type: application/json
```

---

## 接口列表

### 1. 提交实名认证

- **方法：** `POST`
- **URL：** `/api/identity/verifications`
- **是否需要 Token：** 是（普通用户）
- **请求 Body：**

```json
{
  "real_name": "张三",
  "id_card_no": "330102199001011234"
}
```

- **成功响应（201）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 1,
    "status": "pending"
  }
}
```

- **失败场景：**
  - 重复提交（pending/verified 状态）→ `409`，code `40901`
  - 身份证号已被其他用户绑定 → `409`，code `40902`

> 身份证号严禁明文存储，后端只保存 HMAC-SHA256 哈希值和脱敏值（`330102********1234`），响应和数据库均不会出现明文。

---

### 2. 查询我的实名认证状态

- **方法：** `GET`
- **URL：** `/api/identity/verifications/me`
- **是否需要 Token：** 是（普通用户）
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": {
    "id": 1,
    "real_name": "张三",
    "id_card_no_masked": "330102********1234",
    "status": "pending",
    "submitted_at": "2026-06-05T..."
  }
}
```

- **失败场景：** 未提交过 → `404`

> 记下认证记录的 `id`，接口 4（查详情）和接口 5（审核）会用到。

---

### 3. 管理员查待审列表

- **方法：** `GET`
- **URL：** `/api/admin/identity-verifications`
- **是否需要 Token：** 是（admin，需 `identity:review` 权限）
- **查询参数：** `page`（默认 1）、`page_size`（默认 20）、`status`（可选：`pending` / `verified` / `rejected`，不传则返回全部）

- **成功响应（200）：**

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": 3,
        "real_name": "张三",
        "id_card_no_masked": "110101********1234",
        "status": "pending"
      },
      {
        "id": 8,
        "real_name": "张勃勃",
        "id_card_no_masked": "610502********4434",
        "status": "pending"
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 10,
      "total": 6
    }
  }
}
```

> TODO-01 已修复：接口新增分页支持，通过 `page` 和 `page_size` 参数控制返回条数。

---

### 4. 管理员查认证详情

- **方法：** `GET`
- **URL：** `/api/admin/identity-verifications/{id}`
- **是否需要 Token：** 是（admin，需 `identity:review` 权限）
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": {
    "id": 1,
    "user_id": 13,
    "real_name": "张三",
    "id_card_no_masked": "330102********1234",
    "status": "pending",
    "submitted_at": "2026-06-05T...",
    "reviewed_at": null,
    "reject_reason": null
  }
}
```

> `reviewed_at`：审核操作时间（ISO 8601），待审状态为 `null`；`user_id`：提交认证的用户 ID，可用于跳转用户详情页。

- **失败场景：** ID 不存在 → `404`

---

### 5. 管理员审核

- **方法：** `PATCH`
- **URL：** `/api/admin/identity-verifications/{id}/review`
- **是否需要 Token：** 是（admin，需 `identity:review` 权限）

**审核通过：**

```json
{
  "approve": true,
  "reason": "信息核实无误"
}
```

**审核拒绝：**

```json
{
  "approve": false,
  "reason": "证件照片模糊，请重新上传"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

- **验证审核通过是否生效：**
  - 调 `GET /api/identity/verifications/me` → `status` 变为 `verified`
  - 调 `GET /api/me` → `real_name_status` 变为 `verified`

- **验证审核拒绝是否生效：**
  - 调 `GET /api/identity/verifications/me` → `status` 变为 `rejected`，`reject_reason` 有内容

---

## 测试流程（推荐顺序）

```
1. POST /api/identity/verifications          → 提交实名（记下认证 id）
2. POST /api/identity/verifications          → 重复提交，验证 409 拦截
3. GET  /api/identity/verifications/me       → 查询状态为 pending
4. GET  /api/admin/identity-verifications    → 管理员查待审列表，验证分页结构
5. GET  /api/admin/identity-verifications/1  → 管理员查详情
6. PATCH /api/admin/identity-verifications/1/review  → 审核通过（approve: true）
7. GET  /api/identity/verifications/me       → 验证 status 变为 verified
8. GET  /api/me                              → 验证 real_name_status 变为 verified
```

---

## 安全场景覆盖

| 场景 | 期望结果 | 验证方式 |
|---|---|---|
| 无 Token 访问提交接口 | 401 | 不带 Header 请求 |
| 重复提交实名认证 | 409（code 40901）| 同账号连续提交两次 |
| 身份证号已被其他用户绑定 | 409（code 40902）| 两个账号用同一身份证号提交 |
| 无 Token 访问管理员审核接口 | 401 | 不带 Header 请求 |
| 普通用户 token 访问管理员接口 | 403 | 用普通 token 请求 /api/admin/identity-verifications |
| 审核通过后用户实名状态同步 | real_name_status 变为 verified | 审核后调 GET /api/me 验证 |

---

## 错误码说明

| 错误码 | 含义 |
|---|---|
| 40001 | 未登录或 Token 无效 |
| 40003 | 无操作权限（缺少 identity:review）|
| 40400 | 认证记录不存在 |
| 40901 | 已提交过实名认证，请勿重复提交 |
| 40902 | 身份证号已被其他账号绑定 |
| 50000 | 服务器内部错误 |
