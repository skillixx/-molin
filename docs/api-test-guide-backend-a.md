# 后端甲（auth / iam / identity / audit）接口测试手册

> 面向人工测试（APIPost 等工具），覆盖后端工程师甲负责的全部接口。
> 基于代码当前状态整理（2026-06-12），main 分支 commit `88114e1`。
> 2026-06-13 更新：同步 PR#20（手机号登录改为验证码登录）、PR#22（退出登录吊销当前 Access Token）、PR#25（登录接口对未注册账号返回 404/40404）。
> 2026-06-13 更新：新增 PR#31 权限查询接口 —— 2.7 `GET /api/me/permissions`（A-10）、6.7 `GET /api/admin/roles/{id}/permissions`（A-11）、7.9 `GET /api/admin/users/{id}/effective-permissions`（A-12），原 6.7/6.8 顺延为 6.8/6.9。
> **2026-06-15 更新（Round 7 审计 D-83/D-89/D-90/D-93/D-94/D-95/D-96 全部闭环）**：
> - **D-95**：auth/iam/identity 模块所有分页响应改为**扁平结构**（`page`/`page_size`/`total` 直接位于 `data` 顶层，不再嵌套 `pagination` 子对象）。
> - **D-93**：登录/注册/刷新令牌（1.3~1.6）响应新增 `user` 对象。
> - **D-94**：密码长度统一约束为 **6-72 位**（注册/改密/重置/创建后台用户）。
> - **D-96**：`bind_phone`/`bind_email`/`admin_verify` 三个 scene 从公开发码端点移除，迁移到专属认证态端点（新增 2.8/2.9、3.3/3.4）。
> - **D-90**：查询我的实名记录路径由 `/api/identity/verifications/me` 改为 `/api/identity/verifications/latest`（5.2）。
> - **D-89**：实名审核请求体由 `{approve, reason}` 改为 `{action, reject_reason}`（5.5）。
> - **D-83**：审计日志接口（第八节）权限码由 `role:manage` 独立为 `audit:read`。
> - **新增接口补全**：2.10 `PATCH /api/me/profile`（A-27）、4.4 `POST /api/admin/users`（A-28）、4.5 `PATCH /api/admin/users/{id}`（A-29）、4.6 `GET /api/admin/users/{id}/login-logs`（A-30）、5.6 `GET /api/admin/users/{id}/identity`（A-31）。

## 0. 通用说明

### 0.1 Base URL

| 环境 | Base URL |
|---|---|
| 本地开发 | `http://localhost:8080` |
| 测试服务器 | `http://8.130.9.163:8080` |

下文路径均为相对路径，请自行拼接 Base URL。

### 0.2 统一响应格式

成功：
```json
{ "code": 0, "message": "ok", "data": { /* 具体数据，可能为 null */ } }
```

失败：
```json
{ "code": 40000, "message": "错误描述", "data": null }
```

**APIPost 断言建议**：先判断 HTTP 状态码，再判断 `code == 0`。

### 0.3 鉴权

需要登录的接口在 Header 中加：
```
Authorization: Bearer <access_token>
```

`access_token` 来自登录/注册/刷新接口返回的 `data.access_token`。

### 0.4 分页

所有分页接口统一支持 Query 参数：
- `page`：默认 1，最小 1
- `page_size`：默认 20，最大 100

分页响应统一结构（D-95，2026-06-15 起为**扁平结构**）：
```json
{
  "items": [ /* 列表数据 */ ],
  "page": 1,
  "page_size": 20,
  "total": 123
}
```
> ⚠️ `page`/`page_size`/`total` 已直接位于 `data` 顶层，**不再**嵌套在 `pagination` 子对象内。本手册第一至九节（auth/iam/identity 模块）的分页接口均按此结构返回。

### 0.5 验证码获取方式（重要）

非生产环境（`APP_ENV != production`，测试服务器 `APP_ENV=test`）下，发送验证码接口会在 `data.code` 字段直接返回明文 6 位验证码，**无需查数据库或对接短信/邮件服务**，可直接用于后续接口测试。

```json
// POST /api/auth/verification-codes/email 响应示例（测试环境）
{ "code": 0, "message": "ok", "data": { "code": "123456" } }
```

验证码 10 分钟有效，**校验一次后立即失效**（防重放），如需多次测试需重新获取。

### 0.6 通用错误码

| code | HTTP | 含义 |
|---|---|---|
| 40000 | 400 | 请求参数错误 / 验证码错误或已过期 / 格式不合法 |
| 40001 | 401 | 未登录 / token 无效或已过期 / 凭证无效 / 退出登录后该 Access Token 已被吊销（"token 已失效，请重新登录"，见 §2.1） |
| 40003 | 403 | 权限不足（包括无权限码、双重认证未完成、数据范围越权） |
| 40400 | 404 | 资源不存在 |
| 40404 | 404 | 手机号 / 邮箱未注册：发验证码接口（scene=login）及 `/api/auth/login/phone`、`/api/auth/login/email` 本身对未注册账号均返回此码（PR#25） |
| 40900 | 409 | 唯一性冲突（含义因接口而异，见各接口说明） |
| 40901 / 40902 | 409 | 业务专属冲突（identity 模块 / group 模块含义不同，见对应接口） |
| 50000 | 500 | 服务器内部错误 |

### 0.7 测试账号准备

**方式一：使用测试服务器已存在的管理员账号**（推荐，已绑定 admin 角色）

```
email: admintest_1781147075@example.com
password: Admin@Test123!
```

该账号在 `roles`/`user_roles` 中已绑定 `admin` 角色，admin 角色拥有：
`user:manage`、`user:list`、`role:manage`、`audit:read`、`identity:review`、`group:manage`、`scope:all`、`app:manage`、`product:view`、`order:list` 等权限码。

**方式二：自行注册新账号**（走第 1 节"无需鉴权"接口完成注册），但新账号默认无任何角色，访问 `/api/admin/*` 会返回 `403 40003`，需要先用方式一的管理员账号给该账号分配角色（见 7.3 `PATCH /api/admin/users/{id}/roles`）。

### 0.8 管理员双重认证（关键前置步骤）

以下接口除了需要对应权限码，**还需要管理员双重认证（手机+邮箱均在有效期内）**：
- `PATCH /api/admin/users/{id}/status`
- `GET /api/admin/users`、`GET /api/admin/users/{id}`
- `iam` 模块所有 `/api/admin/roles*`、`/api/admin/permissions*`、`/api/admin/users/{id}/roles*`、`/api/admin/users/{id}/permission-overrides*`（`role:manage`）
- `iam` 模块 `/api/admin/audit-logs`（**D-83 起改为 `audit:read` 权限码**，admin 角色已绑定）
- `identity` 模块所有 `/api/admin/identity-verifications*`、`/api/admin/users/{id}/identity`（`identity:review`）
- `iam` 模块所有 `/api/admin/user-groups*`、`/api/admin/users/{id}/groups`（`group:manage`）

**完成双重认证的步骤**（用管理员账号登录后的 token）：

> ⚠️ **D-96（2026-06-15）**：管理员双重认证发码已迁移到**专属认证态端点** `POST /api/admin/auth/verification-codes/{phone,email}`（需 Bearer + user:manage），**不再**走公开端点 `/api/auth/verification-codes/*`（公开端点传 `scene=admin_verify` 现返回 `400 40000`）。

1. 发管理员本人手机验证码（D-96，认证态，scene 由服务端固定 admin_verify）：
   ```
   POST /api/admin/auth/verification-codes/phone   (Bearer 管理员 token)
   { }    // 目标为当前管理员自己绑定的手机号，无需传参
   ```
   非生产环境会在 `data.code` 返回明文验证码。
2. 提交手机双重认证：
   ```
   POST /api/admin/auth/verify-phone   (Bearer 管理员 token)
   { "code": "<上一步拿到的验证码>" }
   ```
3. 发管理员本人邮箱验证码（D-96，认证态）：
   ```
   POST /api/admin/auth/verification-codes/email   (Bearer 管理员 token)
   { }    // 目标为当前管理员自己绑定的邮箱，无需传参
   ```
4. 提交邮箱双重认证：
   ```
   POST /api/admin/auth/verify-email   (Bearer 管理员 token)
   { "code": "<上一步拿到的验证码>" }
   ```

完成以上 4 步后，`GET /api/me` 中 `admin_phone_verified` 和 `admin_email_verified` 均应为 `true`。该状态有效期由环境变量 `ADMIN_VERIFY_EXPIRE_HOURS` 控制（默认值见部署配置，0 表示永不过期）。

---

## 一、Auth 模块 — 公开接口（无需鉴权）

### 1.1 POST /api/auth/verification-codes/email
发送邮箱验证码。

**请求体**
```json
{ "email": "user@example.com", "scene": "register" }
```
- `scene` 取值（**D-96 后公开端点仅接受这 3 个**）：`register` / `login` / `reset_password`
- 两字段均必填，缺失返回 `400 40000`
- ⚠️ **D-96**：`admin_verify` / `bind_email` 已从公开端点移除，传入返回 `400 40000`；换绑邮箱发码改用 2.9，管理员认证发码改用 3.4

**前置校验（按 scene）**
- `scene=register`：邮箱已注册 → `409 40900` "邮箱已被注册"
- `scene=login`：邮箱未注册 → `404 40404` "邮箱未注册，请先注册"
- `scene=reset_password`：不做存在性校验

**成功响应** `200`
```json
{ "code": 0, "message": "ok", "data": { "code": "123456" } }
```
（生产环境 `data` 为空对象 `{}`）

---

### 1.2 POST /api/auth/verification-codes/phone
发送手机验证码，逻辑与 1.1 完全对称。

**请求体**
```json
{ "phone": "13800001234", "scene": "register" }
```
- `scene` 取值（**D-96 后公开端点仅接受这 3 个**）：`register` / `login` / `reset_password`
- ⚠️ **D-96**：`admin_verify` / `bind_phone` 已从公开端点移除，传入返回 `400 40000`；换绑手机发码改用 2.8，管理员认证发码改用 3.3
- `scene=register` 手机已注册 → `409 40900` "手机号已被注册"
- `scene=login` 手机未注册 → `404 40404` "手机号未注册，请先注册"

**成功响应**：同 1.1

---

### 1.3 POST /api/auth/register
唯一注册入口：手机号 + 邮箱 + 用户名必须同时提交，需双重 OTP 校验。

**请求体**
```json
{
  "username": "alice01",
  "phone": "13800001234",
  "email": "alice@example.com",
  "password": "Alice@123",
  "phone_code": "123456",
  "email_code": "654321",
  "invite_code": "ABC12345"
}
```

| 字段 | 说明 |
|---|---|
| username | 必填，`^[a-zA-Z0-9_]{2,32}$`（字母/数字/下划线，2-32位） |
| password | 必填，长度 **6-72 位**（D-94），越界返回 `400 40000` |
| phone_code | scene=register 的手机验证码（先调 1.2 获取） |
| email_code | scene=register 的邮箱验证码（先调 1.1 获取） |
| invite_code | **可选**，组邀请码。传有效码落对应组，为空/无效落默认组（详见下方「注册落组」） |

**成功响应** `201`（D-93：新增 `user` 对象，email/phone 已脱敏）
```json
{
  "code": 0, "message": "ok",
  "data": {
    "access_token": "eyJ...",
    "refresh_token": "xxxx",
    "expires_in": 3600,
    "user": {
      "id": 1,
      "email": "al***@example.com",
      "phone": "138****1234",
      "real_name_status": "unverified",
      "status": "active"
    }
  }
}
```
注册成功后 `phone_verified`/`email_verified` 自动置为 `true`。
> 下文 1.4/1.5/1.6 的「TokenPair（同 1.3）」均指含此 `user` 对象的结构（D-93）。

**注册落组**：注册成功后系统按策略将新用户落入用户分组（`iam.GroupService.AssignOnRegister`，best-effort，失败不回滚注册仅记日志）。

| 场景 | 落组结果 | 验收断言（查 `user_group_members`） |
|---|---|---|
| 传有效 `invite_code` | 落邀请码对应组，角色 = 邀请码 `default_group_role`；邀请码 `used_count`+1 | group_id=邀请组、group_role 匹配、恰好 1 条 |
| 传无效/过期/已满 `invite_code` | 降级落默认组，注册仍成功（方案 A） | group_id=默认组、group_role=member |
| 不传 `invite_code` | 落默认组（`is_default=true`） | group_id=默认组、group_role=member |
| 未配置默认组 | 注册成功，不落任何组 | 该 user 无 member 记录 |

> 回归脚本：`tests/test_register_default_group.py`（覆盖上述 A/B/C/C2/D 共 6 项断言，前置数据自建自清）。

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | phone_code 或 email_code 错误/过期（统一提示"验证码错误或已过期"） |
| 40900 | 409 | 手机号已被注册 / 邮箱已被注册 / 用户名已被使用 |
| 40000 | 400 | 用户名格式不合法（"用户名只能包含字母、数字和下划线，长度2-32位"） |

> 注意：传入的 `invite_code` 无效**不会**返回错误码（方案 A 静默降级落默认组）。

---

### 1.4 POST /api/auth/login/email
**请求体**
```json
{ "email": "alice@example.com", "password": "Alice@123" }
```
**成功响应** `200`：TokenPair（同 1.3）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40404 | 404 | 邮箱未注册，请先注册 |
| 40003 | 403 | 账号已被禁用（status=disabled） |
| 40001 | 401 | 密码错误（消息："邮箱或密码错误"） |

---

### 1.5 POST /api/auth/login/phone
**手机号验证码登录**（PR#20，非密码登录）。登录前需先调用 `POST /api/auth/verification-codes/phone`（`scene=login`）获取验证码。

**前置步骤**
```json
// POST /api/auth/verification-codes/phone
{ "phone": "13800001234", "scene": "login" }
```
非生产环境响应 `data.code` 直接返回明文 6 位验证码（见 §0.5）。

**请求体**
```json
{ "phone": "13800001234", "code": "123456" }
```

**成功响应** `200`：TokenPair（同 1.3）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | 验证码错误或已过期 |
| 40404 | 404 | 手机号未注册，请先注册 |
| 40003 | 403 | 账号已被禁用（status=disabled） |

---

### 1.6 POST /api/auth/refresh
**请求体**
```json
{ "refresh_token": "xxxx" }
```
**成功响应** `200`：新的 TokenPair（旧 refresh_token 立即失效，Token 轮换）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40001 | 401 | refresh_token 不存在 / 已吊销 / 已过期 |

---

### 1.7 POST /api/auth/password/reset
OTP 验证后重置密码（无需旧密码），成功后吊销该用户全部会话。

**请求体**
```json
{
  "target": "alice@example.com",
  "target_type": "email",
  "code": "123456",
  "new_password": "NewPass@123"
}
```
- `target_type`：`phone` 或 `email`
- `code`：对应 `target` 在 `scene=reset_password` 下获取的验证码（先调 1.1/1.2）
- `new_password`：长度 **6-72 位**（D-94），越界返回 `400 40000`

**成功响应** `200`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | 验证码错误或已过期 / target_type 不合法 |
| 40001 | 401 | target 对应账号不存在 |

---

## 二、Auth 模块 — 登录用户接口（需 Bearer Token）

### 2.1 POST /api/auth/logout
**请求体**
```json
{ "refresh_token": "xxxx" }
```
**成功响应** `200`：`data: null`（即使 token 不存在也返回成功）

**Token 即时吊销（PR#22）**：退出成功后，当前请求 `Authorization` 头携带的 Access Token 会被立即加入 Redis 吊销黑名单（`revoked:token:<sha256>`，TTL=该 token 剩余有效期）。此后再用该 Token 请求任意需鉴权接口，均返回 `401 40001`「token 已失效，请重新登录」。该吊销仅针对本次退出所用的这一个 Access Token，不影响同账号其他设备/会话的 Token。

---

### 2.2 GET /api/me
当前登录用户信息。无请求体。

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": {
    "id": 1,
    "username": "alice01",
    "email": "al***@example.com",
    "email_verified": true,
    "phone": "138****1234",
    "phone_verified": true,
    "real_name_status": "unverified",
    "status": "active",
    "admin_phone_verified": false,
    "admin_email_verified": false,
    "created_at": "2026-06-01T10:00:00+08:00",
    "last_login_at": "2026-06-12T09:00:00+08:00"
  }
}
```
| 字段 | 说明 |
|---|---|
| email/phone | 已脱敏：邮箱 `@`前保留2位+`***`；手机号前3后4中间`****` |
| real_name_status | unverified / pending / verified / rejected |
| status | active / disabled |
| admin_phone_verified / admin_email_verified | 管理员双重认证是否在有效期内 |
| last_login_at | 可能为 `null`（无成功登录记录时） |

---

### 2.3 PATCH /api/me/password
**请求体**
```json
{ "old_password": "Alice@123", "new_password": "NewAlice@456" }
```
- `new_password`：长度 **6-72 位**（D-94），越界返回 `400 40000`

**成功响应** `200`：`data: null`，并吊销该用户所有 Refresh Token（需重新登录）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40001 | 401 | old_password 错误（消息固定为"邮箱或密码错误"，文案待优化但当前行为如此） |

---

### 2.4 PATCH /api/me/username
**请求体**
```json
{ "username": "alice_new" }
```
- 不能为空，需匹配 `^[a-zA-Z0-9_]{2,32}$`

**成功响应** `200`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | 格式不合法或为空 |
| 40900 | 409 | 用户名已被使用 |

---

### 2.5 PATCH /api/me/phone
修改手机号，需先用**新手机号**通过 **2.8**（D-96 认证态发码）获取验证码。

**请求体**
```json
{ "phone": "13900005678", "code": "123456" }
```
**成功响应** `200`：`data: null`，成功后 `phone_verified` 置为 `true`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | 验证码错误或已过期 |
| 40900 | 409 | 新手机号已被其他账号使用 |

---

### 2.6 PATCH /api/me/email
修改邮箱，需先用**新邮箱**通过 **2.9**（D-96 认证态发码）获取验证码。

**请求体**
```json
{ "email": "alice_new@example.com", "code": "123456" }
```
**成功响应** `200`：`data: null`，成功后 `email_verified` 置为 `true`

**错误**：同 2.5（40000 验证码错误 / 40900 邮箱已被使用）

---

### 2.8 POST /api/me/verification-codes/phone （D-96，需 Bearer）
向**新手机号**发送换绑验证码（scene 由服务端固定为 `bind_phone`），配合 2.5 完成换绑。按用户限流 5 次/分钟。

**请求体**
```json
{ "phone": "13900005678" }
```
**成功响应** `200`：`data: { "code": "123456" }`（测试环境返回明文；生产为空对象）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | phone 缺失 |
| 40001 | 401 | 未登录 |
| 42900 | 429 | 超过 5 次/分钟限流 |

---

### 2.9 POST /api/me/verification-codes/email （D-96，需 Bearer）
向**新邮箱**发送换绑验证码（scene 固定为 `bind_email`），配合 2.6 完成换绑。按用户限流 5 次/分钟。

**请求体**
```json
{ "email": "alice_new@example.com" }
```
**成功响应** `200`：`data: { "code": "123456" }`（测试环境）

**错误**：同 2.8（40000 email 缺失 / 40001 未登录 / 42900 限流）

---

### 2.10 PATCH /api/me/profile （A-27，需 Bearer）
修改个人资料（昵称 / 头像），PATCH 语义：字段为 `null`（不传）表示不更新，传 `""` 表示清空。

**请求体**
```json
{ "nickname": "新昵称", "avatar_url": "https://cdn.example.com/a.jpg" }
```
| 字段 | 说明 |
|---|---|
| nickname | 可选，最长 64 字符；传 `""` 清空 |
| avatar_url | 可选，须以 `https://` 开头，最长 512 字符；传 `""` 清空 |

**成功响应** `200`：`data: null`（更新后可调 2.2 `GET /api/me` 查看 `nickname`/`avatar_url`）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | nickname 超长 / avatar_url 格式非法或超长 |

---

### 2.7 GET /api/me/permissions （A-10）
返回当前登录用户最终生效的权限码集合。无请求体、无 Query 参数，**无需额外权限码**（仅需登录）。

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": { "permissions": ["user:list", "role:manage", "group:manage"] }
}
```

**计算逻辑**：角色权限 ∪ 用户所在分组的权限码，再叠加 `user_permission_overrides` 中
**未过期**的 allow/deny 调整（deny 从集合中移除对应权限码，allow 追加进集合）。

> 用途：前端可据此做按钮级权限控制（菜单/按钮显隐），避免只能依赖接口返回 403 才能感知无权限。

---

## 三、Auth 模块 — 管理员双重认证入口（需 Bearer + user:manage 权限）

### 3.1 POST /api/admin/auth/verify-phone
管理员对**自己账号**绑定的手机号完成认证（验证码 `scene=admin_verify`，见 0.8）。

**请求体**
```json
{ "code": "123456" }
```
**成功响应** `200`：`data: null`，写入 `admin_phone_verified_at = now()`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40001 | 401 | 当前账号未绑定手机号 |
| 40000 | 400 | 验证码错误或已过期 |

---

### 3.2 POST /api/admin/auth/verify-email
管理员邮箱认证，**必须先完成 3.1**（手机认证在有效期内）。

**请求体**
```json
{ "code": "123456" }
```
**成功响应** `200`：`data: null`，写入 `admin_email_verified_at = now()`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | 尚未完成手机号认证（"请先完成手机号认证"） |
| 40001 | 401 | 当前账号未绑定邮箱 |
| 40000 | 400 | 验证码错误或已过期 |

---

### 3.3 POST /api/admin/auth/verification-codes/phone （D-96，需 Bearer + user:manage）
向**当前管理员自己绑定的手机号**发送 `scene=admin_verify` 验证码，配合 3.1 完成手机双重认证。按用户限流 5 次/分钟。

**请求体**：`{}`（目标为管理员自己的手机号，无需传参）

**成功响应** `200`：`data: { "code": "123456" }`（测试环境返回明文）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40001 | 401 | 当前账号未绑定手机号 |
| 40003 | 403 | 无 user:manage 权限 |
| 42900 | 429 | 超过 5 次/分钟限流 |

---

### 3.4 POST /api/admin/auth/verification-codes/email （D-96，需 Bearer + user:manage）
向**当前管理员自己绑定的邮箱**发送 `scene=admin_verify` 验证码，配合 3.2 完成邮箱双重认证。

**请求体**：`{}`

**成功响应** `200`：`data: { "code": "123456" }`（测试环境）

**错误**：同 3.3（40001 未绑定邮箱 / 40003 无权限 / 42900 限流）

---

## 四、Auth 模块 — 管理员用户管理（需 Bearer + user:manage/user:list + 双重认证）

### 4.1 PATCH /api/admin/users/{id}/status
封禁 / 解封用户。需 `user:manage` 权限 + 双重认证。

**Path 参数**：`id` — 目标用户 ID

**请求体**
```json
{ "status": "disabled", "reason": "违规操作" }
```
- `status`：`active`（解封） 或 `disabled`（封禁）
- `reason`：可选，写入审计日志

**成功响应** `200`
```json
{ "code": 0, "message": "ok", "data": "updated" }
```

封禁效果：
- 用户 DB `status=disabled`
- 该用户 userID 写入 Redis 封禁黑名单（TTL=Access Token 有效期），存量 Access Token 立即失效
- 吊销该用户全部 Refresh Token

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | id 不合法 / status 不是 active 或 disabled |
| 50000 | 500 | 操作失败 |

---

### 4.2 GET /api/admin/users
管理员分页查询用户列表。需 `user:list` 权限 + 双重认证 + 数据范围注入（超管 `scope:all` 可看全部，组管理员只能看自己组内用户）。

**Query 参数**
| 参数 | 说明 |
|---|---|
| keyword | 可选，模糊搜索 |
| status | 可选，按 active/disabled 过滤 |
| page / page_size | 分页 |

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": {
    "items": [
      {
        "id": 1,
        "username": "alice01",
        "email": "al***@example.com",
        "email_verified": true,
        "phone": "138****1234",
        "phone_verified": true,
        "real_name_status": "unverified",
        "status": "active",
        "created_at": "2026-06-01T10:00:00+08:00",
        "last_login_at": "2026-06-12T09:00:00+08:00"
      }
    ],
    "page": 1,
    "page_size": 20,
    "total": 1
  }
}
```

---

### 4.3 GET /api/admin/users/{id}
管理员查看单个用户详情。同 4.2 权限要求，额外做数据范围校验（组管理员查询范围外用户返回 403）。

**成功响应** `200`：单个 `AdminUserResp`（结构同 4.2 中的 item）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | id 不合法 |
| 40003 | 403 | 目标用户超出当前管理员的数据范围 |
| 40400 | 404 | 用户不存在 |

---

### 4.4 POST /api/admin/users （A-28，需 user:manage + 双重认证）
管理员直接创建后台用户（**跳过 OTP**），可同时分配角色。

**请求体**
```json
{
  "email": "ops01@example.com",
  "phone": "13700001234",
  "password": "Ops@123456",
  "role_ids": [2, 3],
  "status": "active"
}
```
| 字段 | 说明 |
|---|---|
| email / phone | **至少传一个**（两者皆可选，但不能都为空） |
| password | 必填，长度 **6-72 位**（D-94） |
| role_ids | 可选，角色 ID 数组；省略则不分配角色 |
| status | 可选，`active`（默认） / `disabled` |

**成功响应** `201`
```json
{ "code": 0, "message": "ok", "data": { "user_id": 50 } }
```

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | email/phone 都为空 / password 越界 / status 非法 |
| 40900 | 409 | 邮箱或手机号已被注册 |

---

### 4.5 PATCH /api/admin/users/{id} （A-29，需 user:manage + 双重认证）
管理员修改用户的邮箱 / 手机号 / 状态，PATCH 语义：字段为 `null`（不传）表示不更新。

**Path 参数**：`id` — 目标用户 ID

**请求体**（按需传字段）
```json
{ "email": "new@example.com", "phone": "13900009999", "status": "disabled" }
```
| 字段 | 说明 |
|---|---|
| email | 可选，改后自动 `email_verified=true` |
| phone | 可选，改后自动 `phone_verified=true` |
| status | 可选，`active` / `disabled` |

**成功响应** `200`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | id 不合法 / status 非法 |
| 40900 | 409 | 新邮箱或手机号已被其他账号占用 |
| 40400 | 404 | 用户不存在 |

---

### 4.6 GET /api/admin/users/{id}/login-logs （A-30，需 user:list + 双重认证）
分页查询指定用户的登录日志。

**Path 参数**：`id` — 目标用户 ID
**Query 参数**：`page` / `page_size`

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": {
    "items": [
      {
        "id": 1001,
        "login_type": "email",
        "login_account": "al***@example.com",
        "ip": "127.0.0.1",
        "user_agent": "Mozilla/5.0 ...",
        "status": "success",
        "created_at": "2026-06-15T09:00:00+08:00"
      }
    ],
    "page": 1,
    "page_size": 20,
    "total": 1
  }
}
```
- `login_type`：`email` / `phone`；`login_account` 已脱敏；`status`：`success` / `failed`

---

## 五、Identity 模块 — 实名认证

### 5.1 POST /api/identity/verifications（需 Bearer）
用户提交实名认证。身份证号仅内存处理，DB 只存 HMAC hash + 脱敏值。

**请求体**
```json
{
  "real_name": "张三",
  "id_card_no": "330102199001011234",
  "verification_type": "id_card",
  "attachments": ["https://minio.example.com/idcard/front.jpg"]
}
```
- `id_card_no`：18 位身份证号，**不会被存储为明文**
- `verification_type`：可选，认证类型，当前仅支持 `id_card`
- `attachments`：可选，附件 URL 数组

**成功响应** `201`
```json
{ "code": 0, "message": "ok", "data": { "id": 10, "status": "pending" } }
```

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40901 | 409 | 已有 pending/verified 状态的认证记录，不允许重复提交 |
| 40902 | 409 | 该身份证号已绑定其他账号 |

---

### 5.2 GET /api/identity/verifications/latest（需 Bearer）
> ⚠️ **D-90（2026-06-15）**：路径由旧的 `/api/identity/verifications/me` 改为 `/api/identity/verifications/latest`，旧路径已下线。

查询当前用户最近一条认证记录。无请求体。

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": {
    "id": 10,
    "user_id": 1,
    "real_name": "张三",
    "id_card_no_masked": "330102********1234",
    "status": "pending",
    "reject_reason": null,
    "submitted_at": "2026-06-12T10:00:00+08:00",
    "reviewed_at": null
  }
}
```
- `status`：`pending` / `verified` / `rejected`
- `reviewed_at`：审核通过/拒绝后才非 null

**错误**：`404 40400` "暂无认证记录"（从未提交过）

---

### 5.3 GET /api/admin/identity-verifications（需 Bearer + identity:review + 双重认证）
管理员分页查询认证记录。

**Query 参数**
| 参数 | 说明 |
|---|---|
| status | 可选，`pending`/`verified`/`rejected`，空值返回全部状态 |
| page / page_size | 分页 |

**成功响应** `200`：扁平分页 `{ items: [VerificationResp...], page, page_size, total }`，item 结构同 5.2 的 `data`

---

### 5.4 GET /api/admin/identity-verifications/{id}（同 5.3 权限）
**Path 参数**：`id` — 认证记录 ID

**成功响应** `200`：单条 `VerificationResp`（结构同 5.2）

**错误**：`404 40400` "记录不存在"

---

### 5.5 PATCH /api/admin/identity-verifications/{id}/review（同 5.3 权限）
管理员审核。
> ⚠️ **D-89（2026-06-15）**：请求体字段由旧的 `{approve, reason}` 改为 `{action, reject_reason}`，旧字段不再生效。

**请求体**（通过）
```json
{ "action": "approve" }
```
或拒绝：
```json
{ "action": "reject", "reject_reason": "身份证照片不清晰" }
```
| 字段 | 说明 |
|---|---|
| action | 必填，`approve` 或 `reject` |
| reject_reason | `action=reject` 时必填；`approve` 时可省略 |

- `action=approve` → 记录状态 `verified`，同步写 `users.real_name_status=verified`，记录 `verified_at`
- `action=reject` → 记录状态 `rejected`，`reject_reason` 写入拒绝原因

**成功响应** `200`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | action 非法 / `action=reject` 但未填 reject_reason |
| 40400 | 404 | 记录不存在 |

写审核日志（`identity_verification_logs`）记录 operator_id。

---

### 5.6 GET /api/admin/users/{id}/identity （A-31，需 Bearer + identity:review + 双重认证）
管理员查看指定用户的实名信息卡片（返回该用户最近一条认证记录，结构同 5.2）。

**Path 参数**：`id` — 目标用户 ID

**成功响应** `200`：单条 `VerificationResp`（结构同 5.2 的 `data`，含 `id_card_no_masked` 脱敏证件号）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40400 | 404 | 该用户暂无实名认证记录 |

---

## 六、IAM 模块 — 角色与权限管理（需 Bearer + role:manage + 双重认证）

### 6.1 GET /api/admin/roles
**Query 参数**：`keyword`（可选，匹配 code 或 name）、`page`、`page_size`

**成功响应** `200`
```json
{
  "items": [
    { "id": 1, "code": "admin", "name": "超级管理员", "description": "系统管理员" }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```
`description` 为 `*string`，可能不出现（omitempty）

---

### 6.2 POST /api/admin/roles
**请求体**
```json
{ "code": "ops", "name": "运维角色", "description": "运维人员" }
```
**成功响应** `201`
```json
{ "code": 0, "message": "ok", "data": { "id": 5, "code": "ops", "name": "运维角色" } }
```
注意：创建响应**不返回** `description` 字段（即使请求中传了）。

---

### 6.3 GET /api/admin/roles/{id}
**成功响应** `200`：`RoleResp { id, code, name, description }`

**错误**：`404 40400` "角色不存在"

---

### 6.4 PUT /api/admin/roles/{id}
**请求体**
```json
{ "code": "ops", "name": "运维角色（更新）", "description": "更新后的描述" }
```
- 仅 `name`、`description` 会被更新，`code` 提交但**不生效**（实现上只 update name/description）

**成功响应** `200`：`data: null`

---

### 6.5 DELETE /api/admin/roles/{id}
**成功响应** `200`：`data: null`

---

### 6.6 PATCH /api/admin/roles/{id}/permissions （A-06）
全量替换角色的权限集合。

**请求体**
```json
{ "permission_ids": [1, 2, 3] }
```
- 空数组 `[]` 表示清空该角色所有权限
- 先删除该角色现有权限关联，再批量插入新关联（事务内完成）
- 会失效该角色下所有用户的权限缓存（Redis）
- 写审计日志 `iam / set_role_permissions`

**成功响应** `200`
```json
{ "code": 0, "message": "ok", "data": "updated" }
```

---

### 6.7 GET /api/admin/roles/{id}/permissions （A-11）
返回指定角色当前拥有的权限码列表（数组）。

**Path 参数**：`id` — 角色 ID

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": { "permissions": ["user:list", "role:manage", "group:manage"] }
}
```

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | id 不合法 |
| 40400 | 404 | 角色不存在 |

> 用途：编辑角色权限前先查当前已有权限集合，配合 6.6 `PATCH /api/admin/roles/{id}/permissions`（全量替换）做增删。

---

### 6.8 GET /api/admin/permissions
**Query 参数**：`keyword`（匹配 code 或 name）、`page`、`page_size`

**成功响应** `200`
```json
{
  "items": [
    { "id": 1, "code": "user:manage", "name": "用户管理", "resource": "user", "action": "manage" }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```

---

### 6.9 POST /api/admin/permissions （A-06，P3-1 已修复为 409）
创建权限码。

**请求体**
```json
{ "code": "report:export", "name": "导出报表", "resource": "report", "action": "export" }
```
- 4 个字段均必填，缺失返回 `400 40000`

**成功响应** `201`
```json
{
  "code": 0, "message": "ok",
  "data": { "id": 20, "code": "report:export", "name": "导出报表", "resource": "report", "action": "export" }
}
```

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | code/name/resource/action 任一为空 |
| 40900 | 409 | `code` 已存在（违反 `uk_permissions_code` 唯一约束，**P3-1 修复点**：此前返回 500） |

写审计日志 `iam / create_permission`。

---

## 七、IAM 模块 — 用户角色 / 权限覆盖（需 Bearer + role:manage + 双重认证）

### 7.1 GET /api/admin/users/{id}/roles
**Path 参数**：`id` — 用户 ID
**Query 参数**：`page`、`page_size`

**成功响应** `200`
```json
{
  "items": [
    { "id": 1, "code": "admin", "name": "超级管理员", "description": "系统管理员", "created_at": "2026-06-01T10:00:00+08:00" }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```

---

### 7.2 POST /api/admin/users/{id}/roles
为用户**追加**一个角色（非全量替换）。

**请求体**
```json
{ "role_id": 2, "reason": "晋升为运维" }
```
- `reason` 可选

**成功响应** `200`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40900 | 409 | 该用户已拥有此角色（`uk_user_roles` 唯一约束） |

---

### 7.3 PATCH /api/admin/users/{id}/roles （A-06）
全量替换用户的角色集合。

**请求体**
```json
{ "role_ids": [1, 2], "reason": "调岗调整" }
```
- 空数组 `[]` = 清空该用户所有角色
- 失效该用户权限缓存，写审计日志 `iam / replace_user_roles`

**成功响应** `200`
```json
{ "code": 0, "message": "ok", "data": "updated" }
```

---

### 7.4 DELETE /api/admin/users/{id}/roles/{role_id}
撤销用户的某个角色。

**Path 参数**：`id` — 用户 ID，`role_id` — 角色 ID

**成功响应** `200`：`data: null`

---

### 7.5 GET /api/admin/users/{id}/permission-overrides
**Query 参数**
| 参数 | 说明 |
|---|---|
| effect | 可选，`allow`/`deny`，非法值返回 `400 40000` |
| permission_code | 可选，精确匹配 |
| page / page_size | 分页 |

**成功响应** `200`
```json
{
  "items": [
    {
      "id": 1, "user_id": 5, "permission_id": 3, "permission_code": "user:list",
      "effect": "deny", "reason": "临时收回权限",
      "expires_at": "2026-12-31T23:59:59+08:00",
      "created_by": 1, "created_at": "2026-06-12T10:00:00+08:00"
    }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```
`expires_at` 为 `null` 时该字段不出现（omitempty）。

---

### 7.6 POST /api/admin/users/{id}/permission-overrides
新增单条权限覆盖（**追加**，非全量；与已存在的同 permission_id 记录冲突时行为见 repository，通常用 7.7 全量替换更安全）。

**请求体**
```json
{ "permission_id": 3, "effect": "deny", "reason": "临时收回权限" }
```
- `effect` 只能是 `allow` 或 `deny`，其他值返回 `400 40000`

**成功响应** `200`：`data: null`

---

### 7.7 PATCH /api/admin/users/{id}/permission-overrides （A-06）
全量替换用户的权限覆盖集合。

**请求体**
```json
{
  "items": [
    { "permission_id": 3, "effect": "deny", "reason": "临时收回", "expires_at": "2026-12-31T23:59:59Z" },
    { "permission_id": 7, "effect": "allow", "reason": "临时授予" }
  ]
}
```
- `items: []` = 清空该用户所有权限覆盖
- `effect` 必须是 `allow`/`deny`
- `permission_id` 必须存在于 `permissions` 表
- `expires_at` 可选，必须是 ISO 8601（如 `2026-12-31T23:59:59Z`），不传或传 `null`/`""` 表示永不过期

**成功响应** `200`
```json
{ "code": 0, "message": "ok", "data": "updated" }
```

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | effect 非法 / expires_at 格式错误 / permission_id 不存在（消息前缀"请求参数错误："） |

---

### 7.8 DELETE /api/admin/users/{id}/permission-overrides/{override_id}
**Path 参数**：`id` — 用户 ID，`override_id` — 覆盖记录 ID

**成功响应** `200`：`data: null`

---

### 7.9 GET /api/admin/users/{id}/effective-permissions （A-12）
返回指定用户最终生效的权限码列表（角色权限 ∪ 组权限，再叠加 `user_permission_overrides`
的 allow/deny 调整），并附带当前实际生效（未过期）的 overrides 调整明细。计算逻辑与 2.7
`GET /api/me/permissions` 一致，区别仅在于目标用户是路径参数 `:id` 指定的用户。

**Path 参数**：`id` — 用户 ID

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": {
    "permissions": ["user:list", "role:manage", "group:manage"],
    "overrides": [
      { "code": "user:list", "effect": "deny" },
      { "code": "report:export", "effect": "allow" }
    ]
  }
}
```
- `overrides` 为空数组 `[]` 表示该用户当前没有任何生效中的权限覆盖

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | id 不合法 |

> **注意**：本接口**不校验** `:id` 对应的用户是否存在（与 7.1/7.2 等同模块接口一致，
> IAM 模块不持有用户表）。若 `:id` 不存在，`permissions`/`overrides` 均返回空数组，
> HTTP 状态码仍为 `200`，**不会**返回 404。
>
> 用途：管理后台"用户权限排查/一览"功能，免去运维/开发直连数据库手动计算的步骤。

---

## 八、IAM 模块 — 审计日志（需 Bearer + audit:read + 双重认证）

> ⚠️ **D-83（2026-06-15）**：本接口权限码已从 `role:manage` 独立为 **`audit:read`**（最小权限原则）。0.7 中的 admin 测试账号已绑定 `audit:read`（migration 000021），可直接访问。

### 8.1 GET /api/admin/audit-logs
**Query 参数**
| 参数 | 说明 |
|---|---|
| module | 可选，按模块过滤（如 `iam`、`auth`） |
| action | 可选，按操作过滤（如 `ban_user`、`create_permission`） |
| page / page_size | 分页 |

**成功响应** `200`
```json
{
  "items": [
    {
      "id": 100,
      "operator_id": 1,
      "module": "iam",
      "action": "create_permission",
      "target_type": "permission",
      "target_id": "20",
      "ip": "127.0.0.1:54321",
      "created_at": "2026-06-12T10:00:00+08:00"
    }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```

---

## 九、IAM 模块 — 用户分组管理（需 Bearer + group:manage + 双重认证）

> 注意：本节所有接口的中间件链为 `RequireAuth → RequirePerm("group:manage") → RequireAdminVerified`，与第六/七节的 `role:manage` 是不同的权限码。0.7 中的管理员测试账号已绑定 `group:manage`。

### 9.1 GET /api/admin/user-groups
**Query 参数**：`type`（可选，`region`/`org`/`custom`）、`keyword`、`page`、`page_size`

**成功响应** `200`
```json
{
  "items": [
    {
      "id": 1, "code": "beijing", "name": "北京分组", "type": "region",
      "is_default": false, "description": "华北区域",
      "created_at": "2026-06-12T10:00:00+08:00"
    }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```

---

### 9.2 POST /api/admin/user-groups
**请求体**
```json
{ "code": "beijing", "name": "北京分组", "type": "region", "is_default": false, "description": "华北区域" }
```
- `code`、`name` 必填
- `type` 留空默认 `custom`
- `is_default=true` 时会在事务中先清除其他分组的默认标记（同一时刻只有一个默认组）

**成功响应** `201`：`GroupResp`（结构同 9.1 中的 item）

---

### 9.3 GET /api/admin/user-groups/{id}
**成功响应** `200`：`GroupResp`

**错误**：`404 40400` "分组不存在"

---

### 9.4 PUT /api/admin/user-groups/{id}
**请求体**
```json
{ "name": "北京分组（更新）", "type": "region", "is_default": true, "description": "更新描述" }
```
- `code` 不可改（请求体中即使传了也忽略）
- `is_default=true` 时同样会先清除旧的默认组

**成功响应** `200`：`data: null`

---

### 9.5 DELETE /api/admin/user-groups/{id}
**成功响应** `200`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40901 | 409 | 分组下还有成员，不能删除（ErrGroupNotEmpty） |
| 40902 | 409 | 分组下有未失效的邀请码，不能删除（ErrGroupHasActiveCodes） |

> 注意：这两个 code 与第五节 identity 模块的 40901/40902 **含义不同**，请按所在接口区分。

---

### 9.6 GET /api/admin/user-groups/{id}/members
**Query 参数**：`group_role`（可选，`admin`/`member`）、`page`、`page_size`

**成功响应** `200`
```json
{
  "items": [
    { "id": 1, "user_id": 5, "group_id": 1, "group_role": "member", "created_at": "2026-06-12T10:00:00+08:00" }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```

---

### 9.7 POST /api/admin/user-groups/{id}/members
**请求体**
```json
{ "user_id": 5, "group_role": "member" }
```
- `user_id` 必填且非 0
- `group_role` 留空默认 `member`，只能是 `admin`/`member`
- 加入成功后会清除该用户的权限缓存；若加入为 `admin`，还会清除该用户的数据范围缓存

**成功响应** `201`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | user_id 为空 / group_role 不合法 |
| 40900 | 409 | 该用户已是该分组成员（ErrMemberAlreadyExists） |

---

### 9.8 PATCH /api/admin/user-groups/{id}/members/{uid}
修改成员的组内角色。

**Path 参数**：`id` — 分组 ID，`uid` — 用户 ID

**请求体**
```json
{ "group_role": "admin" }
```

**成功响应** `200`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | group_role 不是 admin/member |
| 40400 | 404 | 该用户不是该分组成员（ErrMemberNotFound） |

---

### 9.9 DELETE /api/admin/user-groups/{id}/members/{uid}
移除分组成员。

**成功响应** `200`：`data: null`

**错误**：`404 40400` 该用户不是该分组成员

---

### 9.10 GET /api/admin/users/{id}/groups
查询指定用户所属的所有分组（含组内角色）。**不分页**，直接返回数组。

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": [
    { "group_id": 1, "group_role": "member", "joined_at": "2026-06-12T10:00:00+08:00" }
  ]
}
```

---

### 9.11 GET /api/admin/user-groups/{id}/permissions
查询分组的权限码列表。**不分页**，直接返回数组。

**成功响应** `200`
```json
{
  "code": 0, "message": "ok",
  "data": [
    { "id": 1, "group_id": 1, "permission_code": "user:list", "created_at": "2026-06-12T10:00:00+08:00" }
  ]
}
```

---

### 9.12 POST /api/admin/user-groups/{id}/permissions
给分组添加权限码（组内所有成员通过 `getAllUserPermCodes` 继承该权限）。

**请求体**
```json
{ "permission_code": "user:list" }
```
- 添加成功后会清除该分组**所有成员**的权限缓存

**成功响应** `201`：`data: null`

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | permission_code 为空 |
| 40900 | 409 | 该分组已拥有此权限码（ErrGroupPermissionExists） |

---

### 9.13 DELETE /api/admin/user-groups/{id}/permissions/{code}
移除分组的权限码。

**Path 参数**：`code` — 权限码（如 `user:list`，注意 URL 中冒号需保持原样或按 APIPost 自动编码）

**成功响应** `200`：`data: null`，同样会清除该分组所有成员的权限缓存

---

### 9.13a 组角色（绑定全局角色，组员继承用于商品访问/定价）

> 设计与边界见 `docs/backend-a-group-roles-design.md`。组员经 `GetUserRoleIDs` 继承所在组绑定的角色，用于 `product_role_access` / `product_prices` 的角色判定。
> **A 版边界**：组角色【只影响商品访问/定价】，不进入 `CheckPermission` 权限码判定（绑了角色 ≠ 获得该角色的管理权限码）。
> 权限：`group:manage` + 管理员双重认证。

**GET `/api/admin/user-groups/{id}/roles`** — 列出组绑定的角色
返回 `200`：`[{ "id":1, "group_id":3, "role_id":5, "created_at":"..." }]`

**POST `/api/admin/user-groups/{id}/roles`** — 给组绑定角色
请求体：`{ "role_id": 5 }`
- 成功 `201`：`data: null`；绑定即时生效（无缓存延迟），组员下次取角色即包含。
- `400 40000`：role_id 为空 / 角色不存在 / **绑定系统角色（如 admin）被拒**。
- `404 40400`：分组不存在。
- `409 40900`：该角色已绑定到此分组。

**DELETE `/api/admin/user-groups/{id}/roles/{role_id}`** — 解绑
- 成功 `200`：`data: null`，即时生效。
- `404 40400`：该角色未绑定到此分组。

> 关联约束：被任意分组绑定的角色**不可删除**（`DELETE /api/admin/roles/{id}` 返回错误「角色已绑定到分组，请先解绑」），需先解绑。

---

### 9.14 GET /api/admin/user-groups/{id}/invite-codes
**Query 参数**：`status`（可选）、`page`、`page_size`

**成功响应** `200`
```json
{
  "items": [
    {
      "id": 1, "code": "AB12CD34", "group_id": 1,
      "default_group_role": "member", "max_uses": 0, "used_count": 0,
      "expires_at": null, "status": "active",
      "created_by": 1, "created_at": "2026-06-12T10:00:00+08:00"
    }
  ],
  "page": 1,
  "page_size": 20,
  "total": 1
}
```

---

### 9.15 POST /api/admin/user-groups/{id}/invite-codes
生成邀请码。

**请求体**
```json
{ "code": "", "default_group_role": "member", "max_uses": 10, "expires_at": "2026-12-31T23:59:59Z" }
```
| 字段 | 说明 |
|---|---|
| code | 为空字符串时自动生成 8 位随机码 |
| default_group_role | 留空默认 `member` |
| max_uses | `0` 表示不限次数 |
| expires_at | ISO 8601，`null`/不传 = 永不过期 |

**成功响应** `201`：`InviteCodeResp`（结构同 9.14 中的 item）

**错误**
| code | HTTP | 场景 |
|---|---|---|
| 40000 | 400 | expires_at 格式错误（需 ISO 8601） |
| 40900 | 409 | `code` 已存在（自定义 code 重复） |

---

### 9.16 PATCH /api/admin/user-groups/{id}/invite-codes/{invite_id}/disable
禁用邀请码。

**Path 参数**：`id` — 分组 ID，`invite_id` — 邀请码 ID

**成功响应** `200`：`data: null`，邀请码 `status` 置为禁用状态

---

## 附：建议测试顺序

1. **0.7/0.8** 用管理员账号登录 + 完成双重认证，拿到可用于所有 `/api/admin/*` 的 token
2. **第一节** 注册一个新普通用户，跑通注册/登录/刷新/改资料/密码重置全流程
3. **第五节** 用普通用户提交实名认证，再用管理员账号审核
4. **第六、七节** 用管理员创建角色/权限码，分配给上一步注册的普通用户，验证权限生效（可结合 `CheckPermission` 间接通过普通用户访问需要该权限的接口来验证）；分配前后可用 6.7 查角色权限、7.9 查该用户最终生效权限，再用普通用户的 token 调 2.7 自查，三者结果应一致
4.1 **2.7 / 6.7 / 7.9（A-10/A-11/A-12）** 可结合 7.6/7.7 设置该用户的权限覆盖（allow/deny），验证 7.9 返回的 `overrides` 明细及 `permissions` 是否按预期增删，2.7 用该用户 token 自查应与 7.9 结果一致
5. **第四节** 用管理员封禁/解封刚注册的普通用户，验证封禁后该用户 token 立即失效（401）
6. **第九节** 创建分组、加入成员、配置组权限，验证组权限继承（普通用户加入有 `user:list` 权限的分组后，能访问 `GET /api/admin/users` —— 注意该接口还需双重认证，普通用户不具备，可改为验证 `CheckPermission` 内部逻辑或选用无需双重认证的权限码做验证）
7. **第八节** 最后查 `/api/admin/audit-logs`，核对前面操作（封禁/解封、创建权限码、设置角色权限等）是否都有审计记录
