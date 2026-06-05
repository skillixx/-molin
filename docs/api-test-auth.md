# 一、认证模块（Auth）接口手动测试文档

## 基本信息

| 项目 | 内容 |
|---|---|
| 模块 | Auth — 注册、登录、会话、验证码、JWT |
| 负责开发 | 后端工程师甲（后端 A） |
| 代码路径 | `server/internal/modules/auth/` |
| 测试环境 | `http://8.130.9.163:8080` |
| 测试工具 | Apipost |
| 测试日期 | 2026-06-05 |
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

### 3. 邮箱注册

- **方法：** `POST`
- **URL：** `/api/auth/register/email`
- **是否需要 Token：** 否
- **前置条件：** 先调用接口 1 获取验证码
- **请求 Body：**

```json
{
  "email": "test001@example.com",
  "password": "Test@123456",
  "code": "097441"
}
```

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
  - 验证码错误 → `400`
  - 邮箱已注册 → `409`

---

### 4. 手机号注册

- **方法：** `POST`
- **URL：** `/api/auth/register/phone`
- **是否需要 Token：** 否
- **前置条件：** 先调用接口 2 获取验证码（`scene: register`）
- **请求 Body：**

```json
{
  "phone": "13800138000",
  "password": "Test@123456",
  "code": "xxxxxx"
}
```

- **成功响应（201）：** 同邮箱注册

---

### 5. 邮箱登录

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

### 6. 手机号登录

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

### 7. 刷新 Token

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

### 8. 退出登录

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

- **验证退出是否生效：** 退出后再调用接口 7（刷新 Token），应返回 `401`，说明 `user_sessions` 黑名单已生效。

---

### 9. 获取当前用户信息

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

### 10. 修改密码

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

- **验证修改是否生效：** 修改后用旧密码调用接口 5（邮箱登录），应返回 `401`，说明旧密码已失效。

---

## 测试流程（推荐顺序）

```
1. 发送邮箱验证码（scene: register）
2. 邮箱注册             → 保存 access_token / refresh_token
3. 发送手机验证码（scene: register）
4. 手机号注册
5. 邮箱登录             → 保存新的 access_token / refresh_token
6. 发送手机验证码（scene: login）
7. 手机号登录
8. 获取当前用户信息（GET /api/me）
9. 刷新 Token
10. 修改密码            → 用旧密码登录验证 401
11. 退出登录            → 用旧 refresh_token 刷新验证 401
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

## 错误码说明

| 错误码 | 含义 |
|---|---|
| 40000 | 请求参数错误 / 验证码错误或已过期 |
| 40001 | 未登录或 Token 无效 |
| 40003 | 账号已被封禁 |
| 40900 | 邮箱或手机号已被注册 |
| 50000 | 服务器内部错误 |
