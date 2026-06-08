# 后端甲/乙/丙接口人工测试教程（ApiPost 手把手版）

> 用途：供人工使用 ApiPost（或 Postman/Apifox 等同类工具）按"操作流程"顺序逐一测试
> 后端甲/乙/丙三位工程师负责模块的全部已实现接口。每个接口都给出：
> 真实可用的完整 URL、Header、Body 示例（真实可信的示例值）、预期返回 JSON 结构，
> 并在已知有缺陷/未实现的接口下用"⚠️ 已知问题"标注现状。
>
> **字段名以代码（DTO struct 的 json tag）为准**——本文档已逐一核对
> `server/internal/modules/*/dto/*.go`，凡与 `docs/full-api-design.md` 不一致之处，
> 均以代码实现为准并在文中注明。

## 0. 准备工作

### 0.1 测试环境

- API 地址（base URL）：`http://8.130.9.163:8080`
- 本地开发环境（如自行启动后端）：`http://127.0.0.1:8080`
- 本文档下方所有请求 URL 均以 `http://8.130.9.163:8080` 为前缀书写，可直接复制到 ApiPost。

### 0.2 通用响应结构

所有接口统一返回如下信封结构（`code=0` 表示成功，非 0 为业务错误码）：

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "request_id": "req_xxx"
}
```

列表类接口的 `data` 固定使用分页结构：

```json
{
  "items": [],
  "page": 1,
  "page_size": 20,
  "total": 100
}
```

常见错误码：`40000` 参数错误、`40003` 无权限、`40100` 未登录/Token 失效、`40400` 资源不存在、
`40900` 资源冲突、`70001` 未实名认证、`60001` 余额不足。

### 0.3 ApiPost 使用技巧

- 在「环境变量」中新建变量 `base_url = http://8.130.9.163:8080`，请求 URL 写 `{{base_url}}/api/xxx`，
  方便切换测试环境。
- 再新建变量 `token`，登录成功后把 `data.access_token` 粘贴进去，后续请求 Header 统一写
  `Authorization: Bearer {{token}}`，无需每次手动替换。
- 同理可以建 `admin_token` 单独存放管理员账号的 token，方便在普通用户和管理员之间切换测试。

### 0.4 关于测试账号

**请勿使用任何已存在的管理员/测试账号登录**，避免互相干扰或污染共享数据。请按下面"第一步"
自行注册一个全新账号；如需要管理员权限做管理端接口测试，注册完成后把你的账号邮箱/用户 ID
发给测试或运维同学，**请他们在数据库里给你的账号绑定 `admin` 角色**（不要自己写库）。
绑定后重新登录一次（拿到包含新角色的新 token）即可。

---

## 第一步：注册账号 + 获取 Token

### 1.1 发送邮箱注册验证码（无需登录）

```
POST http://8.130.9.163:8080/api/auth/verification-codes/email
Header: Content-Type: application/json
Body (raw JSON):
{
  "target": "manualtest_zhang_1749000001@molin.io",
  "scene": "register"
}
```

说明：
- `target` 填邮箱地址，建议格式：`manualtest_<你的代号>_<13位时间戳毫秒/或随便几位数字>@molin.io`，
  保证全局唯一，不与他人冲突
- `scene` 取值：`register`（注册）/ `login`（登录）/ `bind_email`（绑定邮箱）/
  `bind_phone`（绑定手机号）/ `reset_password`（找回密码）/ `admin_verify`（管理员二次认证）

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "sent": true,
    "expires_in": 300
  }
}
```

> 说明：测试环境通常不会真实发送邮件，验证码会写入 Redis（key 形如 `verify_code:email:register:<邮箱>`），
> 请联系运维同学帮忙从 Redis 查询，或确认服务端日志/响应中是否在非生产环境下回显了验证码
> （生产环境严禁在响应中回显验证码，详见 `audit-week1.md` MEDIUM-04 修复记录）。

### 1.2 邮箱注册

```
POST http://8.130.9.163:8080/api/auth/register/email
Header: Content-Type: application/json
Body (raw JSON):
{
  "email": "manualtest_zhang_1749000001@molin.io",
  "password": "Test1234!",
  "code": "123456",
  "username": "manualtest_zhang"
}
```

字段说明：
- `email`、`password`、`code` 必填；`username` 选填（2-32 位字母/数字/下划线，全局唯一，
  留空则不设置用户名）
- `code` 填上一步收到的验证码
- 密码建议设置为含大小写字母+数字+符号的强密码，例如 `Test1234!`

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "user_id": 10086,
    "email": "manualtest_zhang_1749000001@molin.io",
    "real_name_status": "unverified",
    "status": "active"
  }
}
```

### 1.3 邮箱密码登录（获取 Token）

```
POST http://8.130.9.163:8080/api/auth/login/email
Header: Content-Type: application/json
Body (raw JSON):
{
  "email": "manualtest_zhang_1749000001@molin.io",
  "password": "Test1234!"
}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "refresh_token": "rt_8f3c2a1e9b7d4f5a6c8e0b1d2f3a4c5e6d7f8a9b",
    "expires_in": 7200,
    "user": {
      "id": 10086,
      "email": "ma***@molin.io",
      "phone": null,
      "real_name_status": "unverified",
      "status": "active"
    }
  }
}
```

▎ 拿到 `data.access_token` 后，把它粘贴到 ApiPost 环境变量 `token` 中。
后续所有"需要登录"的接口，请求 Header 统一加上：

```
Authorization: Bearer {{token}}
```

`refresh_token` 请妥善保存，第 1.6 节"刷新令牌"和第 1.5 节"退出登录"会用到。

### 1.4 手机号验证码登录（备选登录方式）

如果你走手机号注册线（`POST /api/auth/register/phone`，body 同邮箱注册改为 `phone`/`code`），
也可以用手机号验证码登录：

```
POST http://8.130.9.163:8080/api/auth/login/phone
Header: Content-Type: application/json
Body (raw JSON):
{
  "phone": "13800001234",
  "code": "123456"
}
```

预期返回结构与邮箱登录一致（`access_token` / `refresh_token` / `expires_in` / `user`）。

> 注意：发送短信验证码需先调用 `POST /api/auth/verification-codes/phone`，
> body 为 `{"phone": "13800001234", "scene": "register"}`（或 `login`）。

### 1.5 退出登录

```
POST http://8.130.9.163:8080/api/auth/logout
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
Body (raw JSON):
{
  "refresh_token": "rt_8f3c2a1e9b7d4f5a6c8e0b1d2f3a4c5e6d7f8a9b"
}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "logged_out": true
  }
}
```

**测试要点**：退出登录后，原 `refresh_token` 应立即失效（再次调用 `/api/auth/refresh` 应返回 401），
但已签发的 `access_token` 在过期前可能仍然有效（视具体实现是否将 access token 加入黑名单而定，
如果测试发现退出后旧 access_token 仍可访问 `/api/me`，属于正常的"短期有效窗口"，
除非清单另有标注才算缺陷）。

### 1.6 刷新令牌

```
POST http://8.130.9.163:8080/api/auth/refresh
Header: Content-Type: application/json
Body (raw JSON):
{
  "refresh_token": "rt_8f3c2a1e9b7d4f5a6c8e0b1d2f3a4c5e6d7f8a9b"
}
```

预期返回（与登录接口 `data` 结构一致，并会签发新的 `access_token`/`refresh_token`）：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "access_token": "eyJhbGciOiJIUzI1NiIs...",
    "refresh_token": "rt_新的刷新令牌字符串",
    "expires_in": 7200,
    "user": { "id": 10086, "email": "ma***@molin.io", "phone": null,
              "real_name_status": "unverified", "status": "active" }
  }
}
```

### 1.7 找回密码（OTP 重置）

无需登录，适用于忘记密码场景：

```
POST http://8.130.9.163:8080/api/auth/password/reset
Header: Content-Type: application/json
Body (raw JSON):
{
  "target": "manualtest_zhang_1749000001@molin.io",
  "target_type": "email",
  "code": "123456",
  "new_password": "NewTest5678!"
}
```

字段说明：
- `target_type` 取值仅 `"phone"` 或 `"email"`，需与 `target` 实际类型匹配
- `code` 是发送到 `target` 的验证码（先调用 `verification-codes/email` 或 `/phone`，
  `scene` 传 `reset_password`）

预期返回：`data` 为 `null`，HTTP 200 即表示成功。**重置成功后该用户所有 Refresh Token 会被自动吊销**，
之前所有终端的登录态都会失效，需要重新登录。

### 1.8 管理员 Token 怎么拿

涉及 `/api/admin/...` 路径的接口都需要管理员权限（具体到某个权限码，如 `user:manage`、
`product:view`、`order:list` 等，详见各接口说明）。获取方式：

1. 先按上面 1.1~1.3 步骤注册一个全新账号并登录拿到普通用户 token
2. 把你的注册邮箱或登录后 `GET /api/me` 返回的 `id`（用户 ID）发给测试/运维同学，
   **请他们帮你在数据库里把该账号绑定到已有的 `admin` 角色**（`INSERT INTO user_roles ...`），
   不要自己写库操作共享数据
3. 绑定完成后，**重新调用一次登录接口**（`POST /api/auth/login/email`），
   因为 JWT 中可能包含角色快照，旧 token 不会自动获得新权限
4. 把新登录拿到的 `access_token` 存入 ApiPost 的 `admin_token` 环境变量，
   后续管理端接口的 `Authorization` 头使用 `Bearer {{admin_token}}`

---

## 第二步：后端工程师甲负责的接口（auth 账号管理 → iam 权限 → identity 实名 → audit 审计）

### 2.1 查询当前登录用户信息

```
GET http://8.130.9.163:8080/api/me
Header: Authorization: Bearer {{token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 10086,
    "username": "manualtest_zhang",
    "email": "ma***@molin.io",
    "email_verified": true,
    "phone": null,
    "phone_verified": false,
    "real_name_status": "unverified",
    "status": "active",
    "admin_phone_verified": false,
    "admin_email_verified": false,
    "created_at": "2026-06-08T10:00:00Z",
    "last_login_at": "2026-06-08T10:05:00Z"
  }
}
```

说明：`email`/`phone` 已做脱敏处理（邮箱 @ 前保留 2 位 + `***`；手机号前 3 后 4 中间 `****`）；
`real_name_status` 取值：`unverified`/`pending`/`verified`/`rejected`。

### 2.2 修改个人资料（昵称/头像）

```
PATCH http://8.130.9.163:8080/api/me/profile
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
Body (raw JSON):
{
  "nickname": "测试昵称张三",
  "avatar_url": "https://cdn.molin.io/avatars/default_01.png"
}
```

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

### 2.3 修改密码（需旧密码）

```
PATCH http://8.130.9.163:8080/api/me/password
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
Body (raw JSON):
{
  "old_password": "Test1234!",
  "new_password": "NewTest5678!"
}
```

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

> 提示：修改成功后建议重新登录验证新密码是否生效；如继续往下测试，记得把 ApiPost 里
> 记录的密码同步更新，否则下次登录会失败。

### 2.4 修改用户名

```
PATCH http://8.130.9.163:8080/api/me/username
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
Body (raw JSON):
{
  "username": "zhang_manual_qa01"
}
```

字段要求：2-32 位字母/数字/下划线，且全局唯一（重复会返回 `409` 或 `400` 业务错误）。

预期返回：`data` 为 `null`，HTTP 200 即表示修改成功。

### 2.5 绑定/修改手机号（需先发验证码）

第一步：先发送验证码到新手机号，`scene` 必须传 `bind_phone`：

```
POST http://8.130.9.163:8080/api/auth/verification-codes/phone
Header: Content-Type: application/json
Body (raw JSON):
{
  "phone": "13900005678",
  "scene": "bind_phone"
}
```

第二步：携带收到的验证码提交绑定：

```
PATCH http://8.130.9.163:8080/api/me/phone
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
Body (raw JSON):
{
  "phone": "13900005678",
  "code": "123456"
}
```

预期返回：`data` 为 `null`，HTTP 200 表示成功，且该用户的 `phone_verified` 会自动置为 `true`。

### 2.6 绑定/修改邮箱（需先发验证码）

操作顺序与手机号一致，区别是 `scene` 传 `bind_email`：

```
POST http://8.130.9.163:8080/api/auth/verification-codes/email
Body: {"target": "manualtest_zhang_new@molin.io", "scene": "bind_email"}

PATCH http://8.130.9.163:8080/api/me/email
Header: Authorization: Bearer {{token}}
Body:
{
  "email": "manualtest_zhang_new@molin.io",
  "code": "123456"
}
```

预期返回：`data` 为 `null`，HTTP 200，且 `email_verified` 自动置为 `true`。

> **重点测试点**：验证码错误/过期/重复使用应被拦截（返回 `400`），不能绕过验证直接改绑。

### 2.7 管理员双重认证（手机号）

需要 `user:manage` 权限的管理员账号。这是管理员执行高敏感操作前的二次验证机制：

第一步：发验证码，`scene` 传 `admin_verify`：
```
POST http://8.130.9.163:8080/api/auth/verification-codes/phone
Body: {"phone": "（你的管理员账号已绑定的手机号）", "scene": "admin_verify"}
```

第二步：提交验证：
```
POST http://8.130.9.163:8080/api/admin/auth/verify-phone
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "code": "123456"
}
```

预期返回：`data` 为 `null`，HTTP 200 表示认证成功（会记录 `admin_phone_verified_at`）。
普通用户调用应返回 `403`（`{"code": 40003, "message": "无权限", ...}`）。

### 2.8 管理员双重认证（邮箱）

前置条件：手机号双重认证必须在有效期内（由环境变量 `ADMIN_VERIFY_EXPIRE_HOURS` 控制，默认 24 小时）。

```
POST http://8.130.9.163:8080/api/admin/auth/verify-email
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "code": "123456"
}
```

预期返回：`data` 为 `null`，HTTP 200，记录 `admin_email_verified_at`。

**测试要点**：先单独测邮箱认证（不先做手机认证），应返回业务错误（如 `40000`/`40003`，
提示需先完成手机号认证）；超过 `ADMIN_VERIFY_EXPIRE_HOURS` 后重新调用应再次要求认证。

### 2.9 管理员代为标记用户手机号/邮箱已验证

需要管理员权限（`user:manage`）。用于客服代处理用户手机号/邮箱无法自助验证的场景：

```
POST http://8.130.9.163:8080/api/admin/auth/verify-phone
（注：与 2.7 节的"管理员双重认证"是同一路径，详见下方已知问题说明）
```

```
POST http://8.130.9.163:8080/api/admin/auth/verify-email
```

⚠️ **已知问题**：根据 `auth/route.go` 实际实现，`POST /api/admin/auth/verify-phone` 和
`POST /api/admin/auth/verify-email` 这两个路径目前**只承载"管理员自身的双重认证"**（见 2.7/2.8 节），
并不是清单旧版本里描述的"管理员代为标记任意用户的手机号/邮箱已验证"。如果你期望的是后一种功能
（即管理员帮其他用户标记验证状态），目前**没有发现对应的接口**，请测试时确认实际效果与预期是否一致，
并将差异反馈给后端甲核对。

### 2.10 封禁/解封用户

需要管理员权限（`user:manage`）：

```
PATCH http://8.130.9.163:8080/api/admin/users/10010/status
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "status": "disabled",
  "reason": "QA 手动测试：违规行为模拟封禁"
}
```

字段说明：
- `status` 必填，仅支持 `"active"`（解封）或 `"disabled"`（封禁）
- `reason` 选填，会写入审计日志

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

**重点测试点（封禁闭环，请重点验证）**：
1. 封禁后，该用户**已签发的 access_token 和 refresh_token 应立即失效**——
   再调用 `GET /api/me`（带该用户的旧 token）应返回 `401`，而不是要等 token 自然过期
2. 封禁后该用户尝试登录应被拒绝（提示账号已被封禁）
3. 解封（`status: "active"`）后，该用户应能重新登录并正常使用
4. 把 `status` 传成非法值（如 `"banned"`）应返回 `400`

---

### 2.11 iam 模块（角色权限管理，全部需要 `role:manage` 权限）

#### 2.11.1 角色列表

```
GET http://8.130.9.163:8080/api/admin/roles
Header: Authorization: Bearer {{admin_token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {"id": 1, "code": "admin", "name": "超级管理员", "description": "拥有全部权限"},
    {"id": 2, "code": "merchant", "name": "商户", "description": "商户角色"}
  ]
}
```

> 注意：`RoleResp` 是数组结构（非分页 items 包裹），具体以实际返回为准。

#### 2.11.2 创建角色

```
POST http://8.130.9.163:8080/api/admin/roles
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "code": "qa_test_role_20260608",
  "name": "QA测试角色",
  "description": "人工测试用临时角色，可随时删除"
}
```

字段说明：`code`、`name` 必填且 `code` 全局唯一；`description` 选填。

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 88,
    "code": "qa_test_role_20260608",
    "name": "QA测试角色",
    "description": "人工测试用临时角色，可随时删除"
  }
}
```

⚠️ **已知问题（P2-#2）**：使用与已存在角色相同的 `code` 重复创建，预期应返回 `400`/`409`，
但实测会返回 `HTTP 500`（数据库唯一索引冲突未被业务层捕获转换）。测试时请验证是否已修复为 `400`/`409`。

#### 2.11.3 更新角色

```
PUT http://8.130.9.163:8080/api/admin/roles/88
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "code": "qa_test_role_20260608",
  "name": "QA测试角色（已更新）",
  "description": "更新后的描述"
}
```

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

#### 2.11.4 删除角色

```
DELETE http://8.130.9.163:8080/api/admin/roles/88
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`{"code": 0, "message": "ok", "data": {"deleted": true}}`

⚠️ **已知问题（P3-#13）**：对一个**不存在**的角色 ID 调用删除接口（如再次删除已删除的角色 88），
预期应返回 `404`，但实测仍返回 `200`（接口未做存在性校验，删除"幽灵记录"也视为成功）。

⚠️ **已知问题（P2-#3）**：`docs/full-api-design.md` §3.10 声明了 `GET /api/admin/roles/:id`
（角色详情）接口，但实现的 `iam/route.go` 中**未注册该路由**（只注册了 `PUT`/`DELETE`），
请求会返回 `HTTP 405 Method Not Allowed`。管理后台目前无法单独查询某个角色的详情/已分配权限列表。

⚠️ **已知问题（P2-#4）**：`docs/full-api-design.md` §3.12 声明了 `PATCH /api/admin/roles/:id/permissions`
（为角色配置权限列表）接口，但**未实现**（请求返回 `404`）。目前没有接口可以单独为某个角色
批量配置权限，只能通过创建角色时附带描述等基础信息。

#### 2.11.5 权限码列表

```
GET http://8.130.9.163:8080/api/admin/permissions
Header: Authorization: Bearer {{admin_token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {"id": 1, "code": "user:manage", "name": "用户管理", "resource": "user", "action": "manage"},
    {"id": 2, "code": "product:view", "name": "商品查看", "resource": "product", "action": "view"},
    {"id": 3, "code": "order:list", "name": "订单列表", "resource": "order", "action": "list"}
  ]
}
```

#### 2.11.6 查询用户已绑定角色

```
GET http://8.130.9.163:8080/api/admin/users/10010/roles
Header: Authorization: Bearer {{admin_token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {
      "id": 2,
      "code": "merchant",
      "name": "商户",
      "description": "商户角色",
      "created_at": "2026-06-01T08:00:00Z"
    }
  ]
}
```

#### 2.11.7 给用户绑定角色

```
POST http://8.130.9.163:8080/api/admin/users/10010/roles
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "role_id": 2,
  "reason": "QA 测试：为用户绑定商户角色以验证权限联动"
}
```

字段说明：`role_id` 必填（角色 ID，可从 2.11.1 角色列表中获取）；`reason` 选填，会写入审计日志。

预期返回：`{"code": 0, "message": "ok", "data": {"assigned": true}}`

#### 2.11.8 解绑用户角色

```
DELETE http://8.130.9.163:8080/api/admin/users/10010/roles/2
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`{"code": 0, "message": "ok", "data": {"revoked": true}}`

#### 2.11.9 查询用户权限覆盖项

```
GET http://8.130.9.163:8080/api/admin/users/10010/permission-overrides
Header: Authorization: Bearer {{admin_token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": [
    {
      "id": 5,
      "permission_id": 3,
      "permission_code": "order:list",
      "effect": "allow",
      "reason": "临时授予订单查看权限",
      "expires_at": null
    }
  ]
}
```

#### 2.11.10 新增权限覆盖项

用于单独授予/收回某个用户的特定权限码（不影响其角色本身）：

```
POST http://8.130.9.163:8080/api/admin/users/10010/permission-overrides
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "permission_id": 3,
  "effect": "allow",
  "reason": "QA 测试：临时授予该用户 order:list 查看权限"
}
```

字段说明：
- `permission_id` 必填，需是 2.11.5 权限列表中真实存在的权限 ID
- `effect` 必填，**只能是 `"allow"`（额外授予）或 `"deny"`（强制收回）**，
  非法值应被拦截返回 `400`（此项已在近期 P1 安全修复中补齐校验，详见
  `audit-week1.md` HIGH-02，测试时请确认传入非法 `effect` 值会被正确拒绝）
- `reason` 选填

预期返回：`{"code": 0, "message": "ok", "data": {"id": 5, "created": true}}`

#### 2.11.11 删除权限覆盖项

```
DELETE http://8.130.9.163:8080/api/admin/users/10010/permission-overrides/5
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`{"code": 0, "message": "ok", "data": {"deleted": true}}`

**重点测试点（iam 模块整体）**：
- 普通用户调用以上任何 `/api/admin/...` IAM 接口，均应返回 `403`（`code: 40003`）
- `effect` 字段传入非 `allow`/`deny` 的值应被拦截（参考 `audit-week1.md` HIGH-02 的修复验证记录）
- 权限覆盖（override）的 `deny` 优先级应高于角色赋予的权限——即使角色有某权限，
  一旦被 `deny` override，该用户应无法再使用该权限

---

### 2.12 identity 模块（实名认证）

#### 2.12.1 提交实名认证申请

```
POST http://8.130.9.163:8080/api/identity/verifications
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
Body (raw JSON):
{
  "real_name": "张三",
  "id_card_no": "110105199003078888",
  "attachments": ["id_card_front_20260608_001.jpg", "id_card_back_20260608_001.jpg"]
}
```

字段说明：
- `real_name`、`id_card_no` 必填；`attachments` 选填（文件 key 数组，对应 MinIO 中已上传的文件）
- `id_card_no` 仅在请求体中以明文传递，**后端只会保存 `HMAC-SHA256` 哈希值和脱敏后的 masked 值
  （前 6 后 4，中间用 `*` 替换），绝不会明文存库**
- 身份证号格式建议使用 18 位标准格式（示例中的号码为虚构测试号段，不对应真实人员）

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "verification_id": 301,
    "status": "pending"
  }
}
```

**测试要点**：
- 身份证号格式非法（位数不对/校验位错误）应被拦截返回 `400`
- 重复提交（已有 `pending` 状态的申请时再次提交）不应导致 `5xx`，应被合理拦截或排队处理

#### 2.12.2 查询自己的实名认证状态

```
GET http://8.130.9.163:8080/api/identity/verifications/me
Header: Authorization: Bearer {{token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 301,
    "real_name": "张三",
    "id_card_no_masked": "110105********8888",
    "status": "pending",
    "reject_reason": null
  }
}
```

⚠️ **已知问题（P3-#14，命名不一致，功能本身正常）**：`docs/full-api-design.md` §2.13
文档中写的路径是 `GET /api/identity/verifications/latest`，但实现中注册的实际路径是
`GET /api/identity/verifications/me`。访问 `/latest` 会得到 `404`，请使用 `/me`。

#### 2.12.3 管理端：实名认证申请列表

需要 `identity:review` 权限：

```
GET http://8.130.9.163:8080/api/admin/identity-verifications?status=pending&page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

Query 参数（均选填）：`user_id`、`status`（pending/approved/rejected）、`real_name`、`page`、`page_size`

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 301,
        "user_id": 10086,
        "real_name": "张三",
        "id_card_no_masked": "110105********8888",
        "status": "pending",
        "reject_reason": null,
        "submitted_at": "2026-06-08T09:00:00Z"
      }
    ],
    "page": 1,
    "page_size": 20,
    "total": 1
  }
}
```

#### 2.12.4 管理端：实名认证申请详情

```
GET http://8.130.9.163:8080/api/admin/identity-verifications/301
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`data` 中包含申请详情、附件列表、审核日志等信息（具体字段以实际返回为准，
基础字段与列表项一致：`id`/`user_id`/`real_name`/`id_card_no_masked`/`status`/`reject_reason`）。

不存在的 ID 应返回 `404`。

#### 2.12.5 审核实名认证申请

```
PATCH http://8.130.9.163:8080/api/admin/identity-verifications/301/review
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON，审核通过):
{
  "approve": true,
  "reason": ""
}
```

```
Body (raw JSON，审核拒绝):
{
  "approve": false,
  "reason": "证件照片模糊不清，请重新上传清晰照片后再次提交"
}
```

⚠️ **重要：字段名与文档不一致，请以下方为准**——`docs/full-api-design.md` §3.15 中写的请求体字段是
`action`（取值 `approve`/`reject`）+ `reject_reason`；但代码 `identity/dto.ReviewReq` 实际定义为
**`approve`（布尔值 true/false）+ `reason`（拒绝理由，拒绝时建议必填）**，`identity/handler` 中
也确认是按 `req.Approve`、`req.Reason` 处理的。**请按 `approve`/`reason` 传参**，
否则会命中"缺少必填字段"或被忽略导致审核行为不符合预期。

预期返回：`{"code": 0, "message": "ok", "data": {"reviewed": true}}`（或类似的审核结果对象）

**重点测试点**：
- 审核通过后，该用户的 `real_name_status` 应同步从 `pending` 更新为 `verified`
  （影响后续"未实名用户禁止购买"业务规则的判断，详见第三步 3.x 商品购买部分）
- 审核拒绝后状态应变为 `rejected`，且 `reject_reason`/`reason` 应可在 2.12.2 查询接口中看到
- 已审核完成的记录再次提交审核，应被状态机校验拦截（不应允许重复审核覆盖结果）

---

### 2.13 audit 模块（审计日志）

```
GET http://8.130.9.163:8080/api/admin/audit-logs?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

Query 参数（均选填）：`operator_id`、`module`、`action`、`created_from`、`created_to`、`page`、`page_size`

文档 `docs/full-api-design.md` §3.16 中描述的预期返回结构：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 9001,
        "operator_id": 1,
        "module": "auth",
        "action": "user_ban",
        "target_type": "user",
        "target_id": 10010,
        "detail": "{...}",
        "created_at": "2026-06-08T09:30:00Z"
      }
    ],
    "page": 1, "page_size": 20, "total": 1
  }
}
```

⚠️ **已知问题（P2-#1，重点关注）**：该接口**目前尚未实现**——`server/internal/modules/audit`
目录下只有一份 `README.md`，**没有 `route.go`，路由从未注册到 `bootstrap/app.go`**。
尽管数据库中 `audit_logs` 表已存在且已经积累了真实数据（其他模块的操作会写入该表），
但管理后台目前**无法通过任何接口查询审计日志**，请求该路径会返回 `404`。
测试时请重点确认是否已补齐该接口；如仍未实现，建议作为本轮测试的重点缺陷登记给后端甲。

---

## 第三步：后端工程师乙负责的接口（商品浏览 → 下单购买 → 钱包充值 → 管理端商品/订单 → 消费计费）

> **操作顺序提示**：购买商品依赖"实名认证已通过"和"钱包余额充足"两个前置条件。
> 建议先完成第二步 2.12 节的实名认证审核通过，再完成 3.3 节的钱包充值，最后再测试购买流程，
> 否则会命中 `70001`（未实名）或 `60001`（余额不足）的拦截。

### 3.1 product 模块 — 用户端商品浏览

#### 3.1.1 商品列表（无需登录也可访问，但建议带 token 看到"是否可购买"等个性化信息）

```
GET http://8.130.9.163:8080/api/products?page=1&page_size=20
Header: Authorization: Bearer {{token}}
```

Query 参数（选填）：`product_type`、`keyword`、`page`、`page_size`

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 101,
        "product_type": "saas",
        "product_code": "molin_basic_plan",
        "name": "Molin 基础版套餐",
        "description": "适合个人开发者使用的基础云服务套餐",
        "status": "active",
        "min_price": "29.00",
        "purchasable": true
      }
    ],
    "page": 1, "page_size": 20, "total": 12
  }
}
```

记下其中一个 `id`（下面用 `101` 占位），用于后续详情/套餐/购买测试。

#### 3.1.2 商品详情

```
GET http://8.130.9.163:8080/api/products/101
Header: Authorization: Bearer {{token}}
```

预期返回：`data` 中包含商品基础信息、套餐列表、价格信息、会员规则、`purchasable`（当前用户是否可购买）等。
访问不存在的商品 ID（如 `999999999`）应返回 `404`。

#### 3.1.3 商品套餐列表

```
GET http://8.130.9.163:8080/api/products/101/plans
Header: Authorization: Bearer {{token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 501,
        "plan_code": "basic_monthly",
        "name": "基础版-包月",
        "billing_type": "subscription",
        "duration_days": 30,
        "quota_json": "{\"api_calls\": 10000}",
        "status": "active",
        "user_price": "29.00",
        "currency": "CNY"
      }
    ]
  }
}
```

说明：`user_price` 是**当前登录用户实际应付价格**，遵循"会员价 > 角色价 > 默认价"的优先级；
如果该套餐尚未配置任何价格，`user_price` 会返回 `-1`。记下其中一个套餐 `id`（如 `501`），下面购买时会用到。

### 3.2 product 模块 — 下单购买（核心闭环入口）

```
POST http://8.130.9.163:8080/api/products/101/purchase
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
  Idempotency-Key: idem_purchase_20260608_001
Body (raw JSON):
{
  "plan_id": 501,
  "quantity": 1,
  "remark": "QA 人工测试下单"
}
```

字段说明：
- `plan_id` 必填（套餐 ID，从 3.1.3 获取）
- `quantity` 选填，默认 `1`，传 `0` 也会按 `1` 处理
- `remark` 选填
- **`Idempotency-Key` 必须在 Header 中传**（不是 Body 字段），值建议为
  `idem_<场景>_<时间戳>_<随机数>`，重复传相同的 key 会返回同一笔订单（幂等）

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "order_id": 20088,
    "order_no": "ORD20260608100001",
    "status": "paid",
    "amount": "29.00",
    "idempotent": false
  }
}
```

说明：`idempotent: true` 表示这是一次重复请求（命中了已有的幂等记录），返回的是已存在的订单而非新建。

**前置条件与依赖关系（务必按顺序操作）**：
1. **未实名认证用户购买会被拦截** → 返回 `HTTP 400`，业务错误码 `70001`，
   响应类似 `{"code": 70001, "message": "请先完成实名认证"}`。请先完成第二步 2.12 节的实名审核通过
2. **余额不足会被拦截** → 返回 `HTTP 400`，业务错误码 `60001`，
   响应类似 `{"code": 60001, "message": "钱包余额不足"}`。请先完成下面 3.3 节的充值流程
3. 商品/套餐处于"已下架"状态时下单应被拒绝
4. 同一个 `Idempotency-Key` 重复提交应返回同一笔订单（`idempotent: true`），不应重复扣款

### 3.3 order 模块 — 订单管理

#### 3.3.1 我的订单列表

```
GET http://8.130.9.163:8080/api/orders?page=1&page_size=20
Header: Authorization: Bearer {{token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 20088,
        "order_no": "ORD20260608100001",
        "order_type": "product_purchase",
        "product_id": 101,
        "product_plan_id": 501,
        "status": "paid",
        "amount": "29.00",
        "currency": "CNY",
        "paid_at": "2026-06-08T10:10:00Z",
        "created_at": "2026-06-08T10:10:00Z"
      }
    ],
    "page": 1, "page_size": 20, "total": 1
  }
}
```

说明：`status` 状态机取值通常包含 `pending`（待支付）/ `paid`（已支付）/ `completed`（已完成）/
`cancelled`（已取消）/ `expired`（已过期）。

#### 3.3.2 订单详情

```
GET http://8.130.9.163:8080/api/orders/20088
Header: Authorization: Bearer {{token}}
```

预期返回：`data` 中包含订单详情、订单明细、支付时间、关联资产等信息。
查询不存在的订单 ID 或他人的订单应返回 `404`/`403`。

#### 3.3.3 发起支付

如果下单后订单状态是 `pending`（例如选择"线下支付"或自定义流程时），可调用此接口发起钱包扣款支付：

```
POST http://8.130.9.163:8080/api/orders/20088/pay
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
  Idempotency-Key: idem_pay_20260608_001
Body (raw JSON):
{
  "pay_method": "wallet"
}
```

字段说明：`pay_method` 必填，目前仅支持 `"wallet"`（钱包余额支付）。

预期返回：`data` 中包含 `order_id`、`status`、`wallet_transaction_id`、`asset_id` 等字段。

> 提示：如果下单接口（3.2）已经直接把订单状态置为 `paid`（购买即扣款的简化闭环），
> 此接口可能命中"订单状态不允许支付"的拦截（返回 `400`），属于正常现象，
> 测试时请先用 3.3.1 查看订单当前状态再决定是否需要调用本接口。

#### 3.3.4 取消订单

```
POST http://8.130.9.163:8080/api/orders/20088/cancel
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
Body (raw JSON):
{
  "reason": "QA 测试：手动取消订单"
}
```

预期返回：`{"code": 0, "message": "ok", "data": {"cancelled": true}}`

**重点测试点（订单状态机）**：
- 取消一个**已支付/已完成**的订单应被拒绝（不能简单取消已扣款订单，需走退款流程）
- 重复取消同一笔已取消订单应被拦截
- 取消他人的订单应返回 `403`/`404`
- 取消一个不存在的订单应返回 `404`

#### 3.3.5 管理端：订单列表

需要 `order:list` 权限：

```
GET http://8.130.9.163:8080/api/admin/orders?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

Query 参数（选填）：`order_type`、`status`、`created_from`、`created_to`、`page`、`page_size`

预期返回结构与 3.3.1 类似，但 `items` 中会包含全平台所有用户的订单（通常还会带上 `user_id` 字段）。

⚠️ **已知问题（P1-#6，重点验证是否已修复并部署生效）**：
此前 `GET /api/admin/orders`、`GET /api/admin/orders/:id` 因权限码 `order:list`
未在数据库 `permissions` 表中播种，导致**包括 admin 在内的任何账号访问都返回 `403`**
（路由要求该权限码，但没人能拥有它）。截至 2026-06-08 该问题已通过 migration `000013`
（`000013_seed_product_view_order_list_permission.up.sql`）补齐 `order:list` 权限码并绑定到
`admin` 角色，**应已部署到测试服务器**。**请用具备 admin 权限的账号实测确认现在能正常返回
`200` 和真实订单列表数据**，如仍返回 `403` 请立即反馈给后端乙复查部署情况。

#### 3.3.6 管理端：订单详情

```
GET http://8.130.9.163:8080/api/admin/orders/20088
Header: Authorization: Bearer {{admin_token}}
```

预期返回：与用户端订单详情结构类似，但管理员可查看任意用户的订单。

---

### 3.4 billing 模块 — 钱包/充值/支付回调

#### 3.4.1 查询我的钱包余额

```
GET http://8.130.9.163:8080/api/wallet
Header: Authorization: Bearer {{token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 5001,
    "user_id": 10086,
    "balance_amount": "0.000000",
    "frozen_amount": "0.000000",
    "currency": "CNY"
  }
}
```

> 说明：首次访问该接口时，如果该用户还没有钱包记录，后端会自动创建一个余额为 0 的钱包。

#### 3.4.2 查询钱包流水

```
GET http://8.130.9.163:8080/api/wallet/transactions?page=1&page_size=20
Header: Authorization: Bearer {{token}}
```

Query 参数（选填）：`type`、`direction`（in/out）、`created_from`、`created_to`、`page`、`page_size`

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 80001,
        "type": "recharge",
        "direction": "in",
        "amount": "100.00",
        "balance_after": "100.000000",
        "related_order_id": 30001,
        "remark": "微信充值到账",
        "created_at": "2026-06-08T10:30:00Z"
      }
    ],
    "page": 1, "page_size": 20, "total": 1
  }
}
```

#### 3.4.3 创建充值订单

```
POST http://8.130.9.163:8080/api/recharge/orders
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
Body (raw JSON):
{
  "amount": "100.00",
  "payment_method": "alipay",
  "remark": "QA 人工测试充值"
}
```

字段说明：
- `amount` 必填，**字符串类型**（避免浮点精度问题），如 `"100.00"`
- `payment_method` 必填，**只能是 `"wechat"` 或 `"alipay"`**
- `remark` 选填

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "order_id": 30001,
    "pay_url": "https://pay.example.com/qrcode?order_no=RCG20260608100001"
  }
}
```

⚠️ **已知问题（P1-#7，重点验证是否已修复并部署生效）**：
此前该接口**不校验 `payment_method` 枚举值**，传入任意非法值（如 `"bitcoin"`）仍返回 `201`
并创建充值订单。截至 2026-06-08 已修复并部署：
- **请求体字段名已从旧的 `provider` 改为 `payment_method`**（务必使用新字段名传参，
  传 `provider` 会因为字段不识别而被忽略）
- 现在传入合法值 `alipay`/`wechat` 应返回 `201`（创建成功）；
  传入非法值/缺省/空字符串应返回 `400`，并提示"不支持的支付方式: xxx，仅支持 wechat / alipay"

**请按以下用例逐一实测**：

| 请求 Body | 期望状态码 |
|---|---|
| `{"amount":"100.00","payment_method":"alipay"}` | `201` |
| `{"amount":"100.00","payment_method":"wechat"}` | `201` |
| `{"amount":"100.00","payment_method":"bitcoin"}` | `400` |
| `{"amount":"100.00"}`（缺省 payment_method） | `400` |
| `{"amount":"100.00","payment_method":""}`（空字符串） | `400` |
| `{"amount":"-10.00","payment_method":"alipay"}`（负金额） | `400` |
| `{"payment_method":"alipay"}`（缺 amount） | `400` |

#### 3.4.4 支付回调（第三方异步通知，无需登录）

```
POST http://8.130.9.163:8080/api/payments/notify/alipay
Header: Content-Type: application/json
Body (raw JSON，模拟一笔伪造回调用于验证签名校验):
{
  "out_trade_no": "RCG20260608100001",
  "transaction_id": "wx_test_1749000999",
  "amount": "100.00",
  "sign": "this_is_a_deliberately_invalid_signature_value"
}
```

说明：
- Path 参数 `provider` 取值 `wechat` 或 `alipay`
- 这是第三方支付平台向我方发起的异步通知接口，**正常情况下应由支付宝/微信服务器调用**，
  人工测试时只能模拟"伪造/篡改签名"的异常场景，验证签名校验是否生效

**重点测试点（资金安全，极其重要）**：
- 携带**错误/伪造签名**的回调请求，应被直接拒绝并返回 `HTTP 400`，
  且**不能影响真实余额**——回调前后调用 3.4.1 查询余额应保持不变
- 必须保证**幂等**：同一个 `out_trade_no`/`provider_trade_no` 多次回调只应处理一次
- 访问未知的支付渠道（如 `POST /api/payments/notify/unknown_provider`）应返回 `400`/`404`

> 说明：由于无法构造真实有效的支付宝/微信签名，本接口的"正常到账"链路建议通过测试脚本
> （如 `tests/stage1_e2e_test.py` 中的模拟回调机制，或联系运维同学了解测试环境是否有
> mock 签名通道）来验证；人工测试重点放在"伪造签名应被拒绝"这一安全性验证上。

#### 3.4.5 管理端：查询全平台钱包流水

需要 `wallet:view` 权限：

```
GET http://8.130.9.163:8080/api/admin/wallet-transactions?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

预期返回结构与 3.4.2 类似，但包含全平台所有用户的流水（通常会带上 `user_id`）。

#### 3.4.6 管理端：查询支付回调记录

```
GET http://8.130.9.163:8080/api/admin/payment-callbacks?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`data.items` 中每条记录包含 `provider`、`provider_trade_no`、`status`
（`received`/`processed`/`failed`）、加密存储的 `notify_body`（管理端通常只展示摘要或脱敏信息，
不应直接暴露原始报文明文）、`created_at` 等字段。

> 安全约定提醒：根据项目规范，`payment_callbacks.notify_body` 应使用 `AES-256-GCM` 加密后存入数据库，
> 接口返回时也不应直接吐出加密密文或敏感原文，测试时如发现接口直接返回了完整明文回调报文，
> 请记录并反馈。

#### 3.4.7 管理端：查询指定用户钱包详情

```
GET http://8.130.9.163:8080/api/admin/users/10086/wallet
Header: Authorization: Bearer {{admin_token}}
```

预期返回：与 3.4.1 结构类似，但是查询指定 `user_id` 的钱包。
**普通用户访问该接口（查询他人钱包）应返回 `403`**——这是一个高优先级的越权防护点，请重点验证。

#### 3.4.8 管理端：冻结/解冻用户钱包余额

```
PATCH http://8.130.9.163:8080/api/admin/users/10086/wallet/freeze
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON，冻结):
{
  "amount": "50.00",
  "action": "freeze",
  "remark": "QA 测试：冻结用户部分余额"
}
```

```
Body (raw JSON，解冻):
{
  "amount": "50.00",
  "action": "unfreeze",
  "remark": "QA 测试：解除冻结"
}
```

字段说明：
- `amount` 必填字符串金额；`action` 必填，仅支持 `"freeze"`/`"unfreeze"`；`remark` 选填
- 传入负金额（如 `"-5.00"`）应被拦截返回 `400`
- 传入金额 `"0.00"` 是否允许视具体业务规则而定（可能放行也可能拦截，测试时请记录实际表现）

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

---

### 3.5 finance_consumer 模块（按量消费计费）

#### 3.5.1 内部上报使用事件（仅限内部 IP 白名单）

```
POST http://8.130.9.163:8080/api/internal/product-usage-events
Header:
  Content-Type: application/json
  Idempotency-Key: idem_usage_20260608_001
Body (raw JSON):
{
  "event_id": "evt_20260608100001",
  "user_id": 10086,
  "product_id": 101,
  "product_type": "saas",
  "product_code": "molin_basic_plan",
  "product_plan_id": 501,
  "instance_id": 1,
  "usage_type": "token",
  "usage_amount": "1000",
  "usage_unit": "tokens",
  "occurred_at": "2026-06-08T10:00:00Z",
  "idempotency_key": "idem_usage_20260608_001"
}
```

⚠️ **已知问题（这部分功能缺口较大，测试时只需确认现状，无需深入排查）**：
- 该接口**默认仅放行来自 `127.0.0.1`/`::1` 的请求**（`finance_consumer/handler.isAllowedIP`），
  生产环境需通过环境变量 `INTERNAL_ALLOWED_IPS`（逗号分隔的 IP 列表）显式配置允许调用的内部服务来源。
  人工通过 ApiPost 从外部网络调用此接口，预期会收到 `403`（IP 不在白名单），这是**符合设计预期的安全行为**，
  不算缺陷
- 以下三个文档（`docs/full-api-design.md` §4.21）中声明的接口**均未实现**（请求会返回 `404`）：
  - `GET /api/product-consumption-records`（用户端查询自己的消费记录）
  - `GET /api/admin/product-billing-rules`（管理端计费规则 CRUD 入口）
  - `GET /api/admin/product-consumption-records`（管理端消费记录查询）

测试时请直接用 ApiPost 访问以上三个路径确认返回 `404`，并将现状反馈给测试工程师统一登记跟踪
（这是已知的功能缺口，不需要深入挖掘细节）。

---

## 第四步：后端工程师丙负责的接口（资产/会员/应用市场用户端 → 管理端配置类）

> **操作顺序提示**：资产（asset）是用户购买商品/会员后系统自动生成的"已拥有的服务实例"，
> 建议先完成第三步的购买流程，再来查看资产是否正确生成；会员（membership）的购买接口目前
> 存在已知问题（见 4.2.3），测试时请重点确认现状。

### 4.1 asset 模块（用户资产/权益）

#### 4.1.1 我的资产列表

```
GET http://8.130.9.163:8080/api/my/assets?page=1&page_size=20
Header: Authorization: Bearer {{token}}
```

Query 参数（选填）：`asset_type`、`status`、`product_id`、`page`、`page_size`

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 7001,
        "user_id": 10086,
        "asset_type": "saas_subscription",
        "product_id": 101,
        "product_plan_id": 501,
        "source_order_id": 20088,
        "business_instance_id": "inst_qa_20260608_001",
        "status": "active",
        "started_at": "2026-06-08T10:10:00Z",
        "expires_at": "2026-07-08T10:10:00Z",
        "created_at": "2026-06-08T10:10:00Z"
      }
    ],
    "page": 1, "page_size": 20, "total": 1
  }
}
```

说明：完成第三步 3.2 节的购买后，正常情况下应能在这里看到自动生成的资产记录，
其 `source_order_id` 应与对应订单 ID 一致，`status` 通常为 `active`/`expired`/`frozen` 等。

#### 4.1.2 资产详情

```
GET http://8.130.9.163:8080/api/my/assets/7001
Header: Authorization: Bearer {{token}}
```

预期返回：与列表项结构一致的单条详情。访问不存在的资产 ID 或他人的资产应返回 `404`/`403`。

#### 4.1.3 我的权益列表

```
GET http://8.130.9.163:8080/api/my/entitlements
Header: Authorization: Bearer {{token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 9001,
        "user_id": 10086,
        "asset_id": 7001,
        "entitlement_type": "api_quota",
        "product_id": 101,
        "quota_total": "10000",
        "quota_used": "120.000000",
        "quota_unit": "次",
        "status": "active",
        "expires_at": "2026-07-08T10:10:00Z"
      }
    ]
  }
}
```

说明：`quota_total` 为 `null` 时表示该权益无总额度限制（如不限量套餐）；`quota_used` 表示已使用量。

#### 4.1.4 管理端：资产列表

需要 `asset:view` 权限：

```
GET http://8.130.9.163:8080/api/admin/assets?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

预期返回：与 4.1.1 结构类似，但包含全平台所有用户的资产记录（含 `user_id`）。

#### 4.1.5 管理端：查询指定用户的资产

```
GET http://8.130.9.163:8080/api/admin/users/10086/assets?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

预期返回结构同上，但限定 `user_id=10086`。**普通用户访问该接口（查询他人资产）应返回 `403`**，
这是越权防护的高优先级测试点。

#### 4.1.6 管理端：调整资产状态/有效期

需要 `asset:manage` 权限：

```
PATCH http://8.130.9.163:8080/api/admin/assets/7001
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "action": "freeze",
  "remark": "QA 测试：人工冻结资产以验证管理端操作链路"
}
```

字段说明：`action` 必填，仅支持 `"freeze"`/`"unfreeze"`；`remark` 选填。

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

传入非法的 `action` 值（如 `"not_a_real_status"`）应返回 `400`；操作不存在的资产 ID 应返回 `404`。

⚠️ **已知问题（P3-#9）**：以下两个文档（`docs/full-api-design.md` §5.1）中声明的接口
**均未实现**（请求会返回 `404`）：
- `GET /api/admin/asset-events`（资产事件审计——查询资产状态变更历史轨迹，`asset_events` 表已有数据）
- `GET /api/admin/users/:id/entitlements`（管理端查询指定用户的权益列表）

测试时请直接访问确认返回 `404`，记录现状即可。

---

### 4.2 membership 模块（会员体系）

#### 4.2.1 会员等级列表（公开，无需登录）

```
GET http://8.130.9.163:8080/api/memberships
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 1,
        "level_code": "vip_monthly",
        "name": "月度 VIP 会员",
        "description": "享受平台全场 9 折优惠及专属客服",
        "sort_order": 1,
        "status": "active"
      }
    ]
  }
}
```

#### 4.2.2 我的会员状态

```
GET http://8.130.9.163:8080/api/my/membership
Header: Authorization: Bearer {{token}}
```

预期返回（未开通会员时 `data` 可能为 `null` 或返回空对象，已开通时如下）：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 3001,
    "user_id": 10086,
    "level_id": 1,
    "asset_id": 7002,
    "status": "active",
    "started_at": "2026-06-08T10:00:00Z",
    "expires_at": "2026-07-08T10:00:00Z"
  }
}
```

#### 4.2.3 购买/开通会员

```
POST http://8.130.9.163:8080/api/memberships/1/purchase
Header:
  Content-Type: application/json
  Authorization: Bearer {{token}}
  Idempotency-Key: idem_membership_purchase_20260608_001
Body (raw JSON):
{
  "plan_id": 1
}
```

⚠️ **已知问题（P2-#15，重点关注）**：根据 `membership/route.go` 实际路由注册情况，
**该路径并未被注册**——模块只注册了 `GET /api/memberships`、`GET /api/my/membership` 和管理端接口，
**没有 `POST /api/memberships/:id/purchase`**。实测调用会返回 `HTTP 404`（路由不存在）。
测试时请确认该接口当前是否仍不可用；如确认仍为 `404`，请将此现状反馈给后端丙核对——
用户购买会员目前可能是通过别的路径（例如统一走 `product` 模块的 `POST /api/products/:id/purchase`，
把"会员"也建模成一种特殊商品）实现的，建议测试时同步确认实际的会员购买路径是哪一条。

#### 4.2.4 管理端：会员等级列表

需要 `membership:view` 权限：

```
GET http://8.130.9.163:8080/api/admin/membership-levels?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`data.items` 中每条记录包含 `id`、`level_code`、`name`、`description`、`sort_order`、`status`。

#### 4.2.5 创建会员等级

需要 `membership:manage` 权限：

```
POST http://8.130.9.163:8080/api/admin/membership-levels
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "level_code": "qa_test_level_20260608",
  "name": "QA测试会员等级",
  "description": "人工测试用临时会员等级",
  "sort_order": 99
}
```

⚠️ **重要：字段名与文档不一致，请以下方为准**——`docs/full-api-design.md` §5.2 中写的会员等级
请求体字段是 `code`/`name`/`level_order`/`status`；但代码 `membership/dto.CreateLevelReq`
实际定义为 **`level_code`（不是 `code`）+ `name` + `description` + `sort_order`（不是 `level_order`）**。
**请按 `level_code`/`sort_order` 等实际字段名传参**，否则会命中"缺少必填字段 level_code"的 `400` 校验。

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": 6,
    "level_code": "qa_test_level_20260608",
    "name": "QA测试会员等级",
    "description": "人工测试用临时会员等级",
    "sort_order": 99,
    "status": "active"
  }
}
```

#### 4.2.6 修改会员等级

```
PATCH http://8.130.9.163:8080/api/admin/membership-levels/6
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "name": "QA测试会员等级（已更新）",
  "sort_order": 88,
  "status": "active"
}
```

字段说明（均选填，传哪个改哪个）：`name`、`description`、`sort_order`、`status`
（`status` 合法取值通常为 `active`/`inactive`，传非法值应返回 `400`）。

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

#### 4.2.7 管理端：会员权益列表

```
GET http://8.130.9.163:8080/api/admin/membership-benefits?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`data.items` 中每条记录包含 `id`、`level_id`、`benefit_type`、`benefit_value`（JSON 字符串）、`status`。

#### 4.2.8 创建会员权益

```
POST http://8.130.9.163:8080/api/admin/membership-benefits
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "level_id": 6,
  "benefit_type": "discount",
  "benefit_value": "{\"discount_rate\": 0.9, \"scope\": \"all_products\"}"
}
```

⚠️ **重要：字段名与文档不一致，请以下方为准**——`docs/full-api-design.md` §5.2 中写的权益
请求体字段是 `membership_level_id`/`benefit_type`/`target_product_id`/`target_product_type`/
`benefit_config_json`；但代码 `membership/dto.CreateBenefitReq` 实际定义为
**`level_id`（不是 `membership_level_id`）+ `benefit_type` + `benefit_value`（一个 JSON 字符串，
不是 `benefit_config_json`）**，没有 `target_product_id`/`target_product_type` 字段。
**请按 `level_id`/`benefit_value` 实际字段名传参**。

预期返回：`data` 中包含新建权益的 `id` 等信息。

#### 4.2.9 修改会员权益

```
PATCH http://8.130.9.163:8080/api/admin/membership-benefits/12
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "status": "inactive"
}
```

字段说明（均选填）：`benefit_type`、`benefit_value`、`status`

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

#### 4.2.10 管理端：用户会员开通记录

```
GET http://8.130.9.163:8080/api/admin/user-memberships?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`data.items` 中每条记录展示某个用户开通某个会员等级的记录
（`user_id`、`level_id`、`status`、`started_at`、`expires_at` 等）。

⚠️ **已知问题（P3-#9）**：`docs/full-api-design.md` §5.2 中声明的
`GET/POST/PATCH /api/admin/product-membership-rules`（商品会员规则配置——为某个商品配置会员折扣
/包含额度规则）**未实现**，请求会返回 `404`。

**重点测试点**：会员等级与会员价的联动——开通某会员等级后，购买商品时 `user_price`
应优先按"会员价"结算（高于角色价和默认价），此核心链路已在 Stage 1 验收测试中验证过。

---

### 4.3 app 模块（应用市场/适配器）

#### 4.3.1 用户端：查看应用业务详情

```
GET http://8.130.9.163:8080/api/marketplace/apps/101
Header: Authorization: Bearer {{token}}
```

预期返回：`data` 中包含应用的图标、名称、描述、所属分类等展示信息。
查询不存在的应用 ID 应返回 `404`。

⚠️ **已知问题（P2-#10，重点关注，请确认实际购买路径）**：`docs/full-api-design.md` §5.3
声明用户端应该有 `GET /api/apps`、`GET /api/apps/:id`、`POST /api/apps/:id/purchase`、
`GET /api/my/apps` 共 4 个接口；但根据 `app/route.go` 实际实现，**用户端只注册了
`GET /api/marketplace/apps/{id}` 这一个接口**，应用列表、应用购买、"我的应用"均未在 app
模块中实现或路径与文档完全不符。访问 `GET /api/apps`、`GET /api/my/apps` 会返回 `404`。

测试时请重点确认：**用户浏览/购买应用市场中的应用，目前实际走的是哪条路径**——
大概率是通过 `product` 模块统一的 `GET /api/products`（按 `product_type` 过滤出"应用"类型商品）
和 `POST /api/products/:id/purchase`（购买入口）来完成的，即"应用"被统一建模为 `product`
模块下的一种商品类型。建议实测确认这一猜测，并将结论反馈给后端丙与前端核对实际对接路径。

#### 4.3.2 管理端：应用列表

需要 `app:manage` 权限：

```
GET http://8.130.9.163:8080/api/admin/apps?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

预期返回：`data.items` 中每条记录包含 `id`、`code`、`name`、`type`、`status`、`icon_url` 等。

#### 4.3.3 管理端：应用详情

```
GET http://8.130.9.163:8080/api/admin/apps/101
Header: Authorization: Bearer {{admin_token}}
```

不存在的应用 ID 应返回 `404`。

#### 4.3.4 创建应用

```
POST http://8.130.9.163:8080/api/admin/apps
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "code": "qa_test_app_20260608",
  "name": "QA测试应用",
  "type": "web",
  "description": "人工测试用临时应用",
  "icon_url": "https://cdn.molin.io/apps/qa_test_icon.png",
  "callback_url": "https://example.com/callback",
  "adapter_config_json": "{}"
}
```

字段说明：`code`、`name`、`type` 必填且 `code` 全局唯一；`description`、`icon_url`、`callback_url`、
`adapter_config_json`（JSON 字符串，应用特有的非交易配置）选填。

预期返回：`data` 中包含新建应用的 `id`。缺少 `code` 应返回 `400`。

#### 4.3.5 修改应用（含上下架）

```
PATCH http://8.130.9.163:8080/api/admin/apps/120
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "name": "QA测试应用（已更新）",
  "status": "active"
}
```

字段说明（均选填）：`name`、`type`、`description`、`icon_url`、`callback_url`、
`adapter_config_json`、`status`（取值 `draft`/`active`/`inactive`/`archived`，用于上下架管理）。

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

⚠️ **已知问题（P3-#12）**：`docs/full-api-design.md` §5.3 中声明的
`PATCH /api/admin/apps/:id/access`（应用访问规则配置）和
`PATCH /api/admin/apps/:id/prices`（应用价格配置）**均未实现**，请求会返回 `404`。

#### 4.3.6 管理端：适配器列表

```
GET http://8.130.9.163:8080/api/admin/app-adapters?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

⚠️ **已知问题（P3-#14，命名不一致，功能本身正常）**：`docs/full-api-design.md` §5.3
中文档路径写的是 `GET/POST/PATCH /api/admin/application-adapters`，但实现的实际路径是
`/api/admin/app-adapters`（更短的命名）。访问 `application-adapters` 会得到 `404`，
请使用 `app-adapters`。

预期返回：`data.items` 中每条记录包含 `id`、`app_code`、`app_name`、`app_type`、
`adapter_type`（`internal`/`external`）、`status` 等。

#### 4.3.7 注册适配器

```
POST http://8.130.9.163:8080/api/admin/app-adapters
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "app_code": "qa_test_app_20260608",
  "app_name": "QA测试应用",
  "app_type": "web",
  "adapter_type": "internal",
  "service_name": "qa-test-svc",
  "callback_url": "https://example.com/adapter-callback",
  "supported_actions_json": "[\"provision\",\"renew\",\"suspend\",\"resume\",\"cancel\"]",
  "usage_event_types_json": "[\"api_call\",\"storage_usage\"]"
}
```

字段说明：`app_code`、`app_name`、`app_type`、`adapter_type` 必填
（`adapter_type` 仅支持 `"internal"`/`"external"`）；其余字段选填，
`supported_actions_json`/`usage_event_types_json` 是 JSON **数组字符串**。

预期返回：`data` 中包含新建适配器的 `id`。缺少必填字段应返回 `400`。

#### 4.3.8 修改/启停适配器

```
PATCH http://8.130.9.163:8080/api/admin/app-adapters/15
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "status": "active"
}
```

字段说明（均选填）：`app_name`、`app_type`、`adapter_type`、`service_name`、`callback_url`、
`supported_actions_json`、`usage_event_types_json`、`status`（仅支持 `active`/`inactive`）。

传入非法状态值（如 `"bogus_status"`）应返回 `400`。

---

### 4.4 content 模块（公告/帮助文档）

#### 4.4.1 用户端：公告列表（按可见性范围过滤）

```
GET http://8.130.9.163:8080/api/announcements?page=1&page_size=20
Header: Authorization: Bearer {{token}}
```

预期返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": 1,
        "title": "平台 6 月维护公告",
        "content": "为了提供更好的服务，平台将于 6 月 10 日凌晨 2:00-4:00 进行例行维护",
        "visible_scope": "all",
        "status": "published",
        "start_at": "2026-06-08T00:00:00Z",
        "end_at": "2026-06-15T00:00:00Z",
        "sort_order": 1
      }
    ],
    "page": 1, "page_size": 20, "total": 1
  }
}
```

说明：`visible_scope` 取值 `all`/`roles`/`members`/`admins`，接口会根据当前登录用户的
角色/会员身份过滤展示范围内的公告。

#### 4.4.2 用户端：帮助文档分类列表（公开，无需登录）

```
GET http://8.130.9.163:8080/api/help/categories
```

预期返回：`data.items` 中每条记录包含 `id`、`name`、`description`、`sort_order`、`status`。

#### 4.4.3 用户端：帮助文档列表（公开，无需登录）

```
GET http://8.130.9.163:8080/api/help/articles?page=1&page_size=20
```

Query 参数（选填）：`category_id`、`keyword`、`page`、`page_size`

预期返回：`data.items` 中每条记录包含 `id`、`category_id`、`title`、`summary`、`status`、`sort_order`。

#### 4.4.4 用户端：帮助文档详情（公开，无需登录）

```
GET http://8.130.9.163:8080/api/help/articles/30
```

预期返回：`data` 中包含完整的文章标题、正文 `content`、所属分类等信息。

⚠️ **已知问题需重点验证**：根据测试脚本中的记录，曾发现已下线（`status = offline`/`draft`）
的帮助文档**仍可通过本接口被公开访问并暴露内容**（未按状态过滤）。测试时请：
1. 先在管理端把一篇文章状态改为草稿/下线（参考 4.4.9）
2. 再用本接口（不带 token）尝试访问该文章详情
3. **预期应返回 `404`（不应暴露未发布内容）**；如仍返回 `200` 并能看到内容，
   说明该问题尚未修复，请记录并反馈给后端丙

访问不存在的文章 ID（如 `999999999`）应返回 `404`。

#### 4.4.5 管理端：公告列表

需要 `content:manage` 权限：

```
GET http://8.130.9.163:8080/api/admin/announcements?page=1&page_size=20
Header: Authorization: Bearer {{admin_token}}
```

#### 4.4.6 创建公告

```
POST http://8.130.9.163:8080/api/admin/announcements
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "title": "QA测试公告_20260608",
  "content": "这是一条人工测试创建的公告内容，用于验证管理端公告创建/发布链路",
  "visible_scope": "all",
  "target_roles_json": "[]",
  "start_at": "2026-06-08T00:00:00Z",
  "end_at": "2026-06-30T23:59:59Z",
  "sort_order": 10
}
```

字段说明：
- `title`、`content`、`visible_scope` 必填
- `visible_scope` 取值 `all`/`roles`/`members`/`admins`；当取值为 `roles` 时，
  `target_roles_json` 才会生效（一个 JSON 字符串数组，如 `"[\"merchant\",\"vip\"]"`）
- `start_at`/`end_at` 选填（ISO 8601 格式时间），用于控制公告展示的时间窗口
- `sort_order` 选填，数字越小排序越靠前

预期返回：`data` 中包含新建公告的 `id`，初始 `status` 通常为 `draft`（草稿）。

#### 4.4.7 修改/发布/下线公告

```
PATCH http://8.130.9.163:8080/api/admin/announcements/50
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON，发布):
{
  "status": "published"
}
```

```
Body (raw JSON，下线):
{
  "status": "offline"
}
```

字段说明（均选填）：`title`、`content`、`visible_scope`、`target_roles_json`、
`status`（取值 `published`/`offline`/`draft`）、`start_at`、`end_at`、`sort_order`

预期返回：`{"code": 0, "message": "ok", "data": {"updated": true}}`

**测试要点**：发布（`status: "published"`）后，应能在 4.4.1 用户端公告列表中看到该公告
（且需在 `start_at`~`end_at` 时间窗口内）；下线（`status: "offline"`）后用户端应不再可见。

#### 4.4.8 管理端：帮助分类管理

```
GET http://8.130.9.163:8080/api/admin/help/categories
Header: Authorization: Bearer {{admin_token}}
```

```
POST http://8.130.9.163:8080/api/admin/help/categories
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "name": "QA测试分类_20260608",
  "description": "人工测试用临时帮助文档分类",
  "sort_order": 5
}
```

字段说明：`name` 必填；`description`、`sort_order` 选填。

```
PATCH http://8.130.9.163:8080/api/admin/help/categories/12
Body (raw JSON):
{
  "name": "QA测试分类_20260608（已更新）",
  "status": "active"
}
```

字段说明（均选填）：`name`、`description`、`sort_order`、`status`

#### 4.4.9 管理端：帮助文档管理

```
GET http://8.130.9.163:8080/api/admin/help/articles
Header: Authorization: Bearer {{admin_token}}
```

```
POST http://8.130.9.163:8080/api/admin/help/articles
Header:
  Content-Type: application/json
  Authorization: Bearer {{admin_token}}
Body (raw JSON):
{
  "category_id": 12,
  "title": "QA测试文章_20260608",
  "content": "这是一篇人工测试创建的帮助文档正文内容，用于验证管理端创建/发布链路是否正常工作。",
  "sort_order": 1
}
```

字段说明：`category_id`、`title`、`content` 必填（`category_id` 需是已存在的分类 ID）；
`sort_order` 选填。

```
PATCH http://8.130.9.163:8080/api/admin/help/articles/30
Body (raw JSON，发布):
{
  "status": "published"
}
```

```
Body (raw JSON，下线):
{
  "status": "draft"
}
```

字段说明（均选填）：`category_id`、`title`、`content`、`sort_order`、`status`（取值 `published`/`draft`）

**重点测试点**：管理端创建并发布（`status: "published"`）后，应能立即在 4.4.3/4.4.4
用户端帮助文档接口中查到该文章；改回 `draft` 后用户端应查不到（详见 4.4.4 已知问题描述）。

---

### 4.5 provision 模块

无对外 HTTP 接口（仅供 `product` 模块在购买成功后内部调用，自动开通对应的应用/服务实例资产），
**无需也无法直接测试**。如果你在第三步 3.2 节完成购买后能在 4.1.1 看到资产记录正确生成，
就说明该模块内部链路工作正常。

---

## 测试建议与反馈方式

1. **优先级建议**：建议先重点验证以下三处标 🔴 的 P1 已修复项是否真的生效——
   - `GET /api/admin/products`、`GET /api/admin/products/:id`（3.1 节，权限码 `product:view`）
   - `GET /api/admin/orders`、`GET /api/admin/orders/:id`（3.3.5 节，权限码 `order:list`）
   - `POST /api/recharge/orders` 的 `payment_method` 枚举校验（3.4.3 节）

   这是本轮最关心的"是否真的修复并部署生效"；确认完之后，再按本文档"第二步→第三步→第四步"
   的操作顺序逐一过一遍其余接口。

2. **遇到问题如何反馈**：发现任何与本文档描述不符的现象（无论是已知问题"还没修复"，
   还是新发现的问题），请记录以下信息并整理反馈给测试工程师登记：
   - 请求方法 + 完整路径 + 请求体（脱敏后）
   - 实际返回的 HTTP 状态码 + 完整响应内容
   - 期望的结果是什么、与本文档描述的差异点在哪里
   测试工程师会统一按 P0~P3 分级跟踪处理。

3. **已知问题列表仅供参考**：本文档标注的 P1/P2/P3 是测试工程师此前地毯式测试的结论快照
   （截至 2026-06-08），实际状态可能已有变化（尤其是标 🔴 的 P1 项，理论上已修复部署）。
   测试时请始终以**实际请求结果**为准，不要直接假设文档中的描述仍然成立——
   如果你发现某个"已知问题"其实已经修复了，这也是一个很重要的信息，请同样记录并反馈，
   以便测试工程师更新缺陷跟踪状态。

4. **关于字段名**：本文档中所有请求体示例字段名均已对照 `server/internal/modules/*/dto/*.go`
   源码核实（而非仅依据 `docs/full-api-design.md` 接口文档），如果测试中发现示例报错
   "缺少必填字段"或返回结构与文档描述的字段不一致，大概率是接口文档本身滞后于代码实现，
   请以实际接口返回为准，并将文档与代码的差异一并反馈，方便后续统一修订接口文档。
