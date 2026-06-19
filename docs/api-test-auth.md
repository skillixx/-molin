# 一、认证模块（Auth）接口手动测试文档

> ⚠️ **已弃用（2026-06-15）**：本文档为早期分模块版本，未同步 Round 7（D-83/D-89/D-90/D-93/D-94/D-95/D-96）变更，部分接口契约已过期。
> **请改用整合后的最新手册：[`docs/api-test-guide-backend-a.md`](./api-test-guide-backend-a.md)**（覆盖 auth/iam/identity/audit 全部接口，已对齐现行代码）。

## 基本信息

| 项目 | 内容 |
|---|---|
| 模块 | Auth — 注册、登录、会话、验证码、JWT |
| 负责开发 | 后端工程师甲（后端 A） |
| 代码路径 | `server/internal/modules/auth/` |
| 测试环境 | `http://8.130.9.163:8080` |
| 测试工具 | Apipost |
| 测试日期 | 2026-06-05（初版）；2026-06-10（补丁更新） |
| 测试结论 | 全部通过 |

---

## 全局配置（Apipost）

```
Base URL：http://8.130.9.163:8080
全局 Header：Content-Type: application/json
```

需要登录的接口统一在 Header 中携带：
```
Authorization: Bearer <access_token>
```

`access_token` 从登录接口响应的 `data.access_token` 字段获取。

---

## 接口列表

### 1. 发送邮箱验证码

- **方法：** `POST`
- **URL：** `/api/auth/verification-codes/email`
- **是否需要 Token：** 否
- **请求 Body：**

```json
{
  "target": "test001@example.com",
  "scene": "register"
}
```

> `scene` 可选值：`register`（注册）、`login`（登录）、`reset_password`（重置密码）

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": { "code": "097441" }
}
```

> 测试环境（非 production）直接在响应里返回明文验证码，无需查数据库。

---

### 2. 发送手机验证码

- **方法：** `POST`
- **URL：** `/api/auth/verification-codes/phone`
- **是否需要 Token：** 否
- **请求 Body：**

```json
{
  "target": "13800138000",
  "scene": "register"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": { "code": "xxxxxx" }
}
```

> 测试环境不发真实短信，手机号可填任意 11 位数字格式。

---

### 3. 统一注册（唯一注册入口）

> 说明：旧的 `POST /api/auth/register/email`、`POST /api/auth/register/phone`
> 两个单独注册接口已下线（产品确认前端用户控制台尚未对接，不存在兼容负担）。
> 现在 **`POST /api/auth/register` 是系统唯一的注册入口**：
> 必须同时提交手机号和邮箱，并通过双重 OTP 验证码（`phone_code` + `email_code`）校验。

- **方法：** `POST`
- **URL：** `/api/auth/register`
- **是否需要 Token：** 否
- **前置条件：** 先调用接口 1 获取邮箱验证码（`scene: register`），再调用接口 2 获取手机验证码（`scene: register`）
- **请求 Body：**

```json
{
  "username": "tester001",
  "phone": "13800138000",
  "email": "test001@example.com",
  "password": "Test@123456",
  "phone_code": "xxxxxx",
  "email_code": "097441",
  "invite_code": "ABC12345"
}
```

> `invite_code` 为**可选**字段：传有效组邀请码 → 落入对应分组并赋邀请码配置的组内角色；为空/无效/过期/已满 → 降级落入默认组（`is_default=true`）；未配置默认组则不落组。落组为 best-effort，失败不影响注册结果。

- **成功响应（201）：**

```json
{
  "code": 0,
  "data": {
    "access_token": "eyJ...",
    "refresh_token": "xxxx",
    "expires_in": 7200
  }
}
```

- **失败场景：**
  - 手机或邮箱验证码错误/已过期 → `400`（错误码 `40000`）
  - 手机号已注册 → `409`（错误码 `40900`）
  - 邮箱已注册 → `409`（错误码 `40900`）
  - 用户名已存在或格式不合法 → `409` / `400`
  - 注意：`invite_code` 无效**不报错**（方案 A 静默降级落默认组）

- **验证旧接口已下线：**
  - `POST /api/auth/register/email` → 应返回 `404`
  - `POST /api/auth/register/phone` → 应返回 `404`

---

### 4. 邮箱登录

- **方法：** `POST`
- **URL：** `/api/auth/login/email`
- **是否需要 Token：** 否
- **请求 Body：**

```json
{
  "email": "test001@example.com",
  "password": "Test@123456"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": {
    "access_token": "eyJ...",
    "refresh_token": "xxxx",
    "expires_in": 7200
  }
}
```

- **失败场景：**
  - 密码错误 → `401`

---

### 5. 手机号登录

- **方法：** `POST`
- **URL：** `/api/auth/login/phone`
- **是否需要 Token：** 否
- **前置条件：** 先调用接口 2 获取验证码（`scene: login`）
- **请求 Body：**

```json
{
  "phone": "13800138000",
  "code": "xxxxxx"
}
```

- **成功响应（200）：** 同邮箱登录

---

### 6. 刷新 Token

- **方法：** `POST`
- **URL：** `/api/auth/refresh`
- **是否需要 Token：** 否
- **请求 Body：**

```json
{
  "refresh_token": "登录时拿到的refresh_token"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": {
    "access_token": "eyJ...新的...",
    "refresh_token": "新的refresh_token",
    "expires_in": 7200
  }
}
```

> 刷新后旧的 `refresh_token` 立即作废（Token 轮换机制），下次使用新返回的。

- **失败场景：**
  - 已退出或已过期的 refresh_token → `401`

---

### 7. 退出登录

- **方法：** `POST`
- **URL：** `/api/auth/logout`
- **是否需要 Token：** 是
- **请求 Body：**

```json
{
  "refresh_token": "登录时拿到的refresh_token"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

- **验证退出是否生效：** 退出后再调用接口 6（刷新 Token），应返回 `401`，说明 `user_sessions` 黑名单已生效。

---

### 8. 获取当前用户信息

- **方法：** `GET`
- **URL：** `/api/me`
- **是否需要 Token：** 是
- **无需 Body**

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": {
    "id": 1,
    "email": "test001@example.com",
    "phone": null,
    "real_name_status": "unverified",
    "status": "active",
    "created_at": "2026-06-05T14:42:53Z"
  }
}
```

- **安全验证：** 不带 Token 直接请求，应返回 `401`。

---

### 9. 修改密码

- **方法：** `PATCH`
- **URL：** `/api/me/password`
- **是否需要 Token：** 是
- **请求 Body：**

```json
{
  "old_password": "Test@123456",
  "new_password": "NewPass@789"
}
```

- **成功响应（200）：**

```json
{
  "code": 0,
  "data": null
}
```

- **验证修改是否生效：** 修改后用旧密码调用接口 4（邮箱登录），应返回 `401`，说明旧密码已失效。

---

## 测试流程（推荐顺序）

```
1. 发送邮箱验证码（scene: register）
2. 发送手机验证码（scene: register）
3. 统一注册（POST /api/auth/register，需同时提交 phone+email+phone_code+email_code）
   → 保存 access_token / refresh_token
   → 同时验证旧路径 /api/auth/register/email、/api/auth/register/phone 均返回 404
4. 邮箱登录             → 保存新的 access_token / refresh_token
5. 发送手机验证码（scene: login）
6. 手机号登录
7. 获取当前用户信息（GET /api/me）
8. 刷新 Token
9. 修改密码            → 用旧密码登录验证 401
10. 退出登录           → 用旧 refresh_token 刷新验证 401
```

---

## 安全场景覆盖

| 场景 | 期望结果 | 验证方式 |
|---|---|---|
| 无 Token 访问 `/api/me` | 401 | 不带 Header 直接请求 |
| 错误密码登录 | 401 | 密码填错再登录 |
| 错误验证码注册 | 400 | code 填 `000000` |
| 已退出 refresh_token 刷新 | 401 | 退出后再刷新 |
| 修改密码后旧密码登录 | 401 | 修改后用旧密码登录 |

---

---

## 补丁测试用例（2026-06-10）

> 以下用例验证本次 BUG-03 ~ BUG-05 修复后的正确行为。

### 发码拦截测试

| 用例 | 请求 | 期望 HTTP | 期望 code | 说明 |
|---|---|---|---|---|
| 已注册邮箱发注册码 | `POST /verification-codes/email` target=已注册邮箱 scene=register | 409 | 40900 | 应拒绝，不投递 OTP |
| 已注册手机号发注册码 | `POST /verification-codes/phone` target=已注册手机 scene=register | 409 | 40900 | 应拒绝，不投递 OTP |
| 未注册手机号发登录码 | `POST /verification-codes/phone` target=未注册手机 scene=login | 404 | 40404 | 提示"手机号未注册，请先注册" |
| 未注册邮箱发登录码 | `POST /verification-codes/email` target=未注册邮箱 scene=login | 404 | 40404 | 提示"邮箱未注册，请先注册" |
| 已注册手机号发登录码 | `POST /verification-codes/phone` target=已注册手机 scene=login | 200 | 0 | 正常发码 |

### 管理员双重认证测试

> 前置条件：使用一个 `admin_phone_verified_at` 和 `admin_email_verified_at` 均为 NULL 的管理员账号（如 `test_a01_1781082294@example.com`）

| 步骤 | 请求 | 期望 | 说明 |
|---|---|---|---|
| 1. 未认证直接访问 IAM | `GET /api/admin/roles` + Bearer Token | 403 / 40031 | 验证拦截生效 |
| 2. 发手机 admin_verify 码 | `POST /verification-codes/phone` scene=admin_verify | 200 | 需携带 Bearer Token |
| 3. 完成手机认证 | `POST /api/admin/auth/verify-phone` + code | 200 | 写入 admin_phone_verified_at |
| 4. 仅手机已认证访问 IAM | `GET /api/admin/roles` | 403 / 40031 | 必须手机+邮箱双认证才放行 |
| 5. 发邮箱 admin_verify 码 | `POST /verification-codes/email` scene=admin_verify | 200 | 需手机先认证才能发邮箱码 |
| 6. 完成邮箱认证 | `POST /api/admin/auth/verify-email` + code | 200 | 写入 admin_email_verified_at |
| 7. 双认证完成后访问 IAM | `GET /api/admin/roles` | 200 | 正常返回角色列表 |

---

## 错误码说明

| 错误码 | 含义 |
|---|---|
| 40000 | 请求参数错误 / 验证码错误或已过期 |
| 40001 | 未登录或 Token 无效 |
| 40003 | 无权限 / 账号已被封禁 |
| 40031 | 管理员未完成双重认证（手机+邮箱均需在有效期内） |
| 40404 | 账号未注册，请先注册（登录 scene 发码时触发） |
| 40900 | 邮箱或手机号已被注册（注册 scene 发码时触发） |
| 50000 | 服务器内部错误 |
