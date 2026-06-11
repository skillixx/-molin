# 前端接口参考文档

> **版本**：Week 1 + Week 2 已验收（2026-06-06）；2026-06-10 补丁更新（发码拦截 + 管理员双重认证强制）；2026-06-11 接口变更同步（用户列表 keyword、角色/权限模糊搜索、实名审核 status 过滤、权限覆盖过滤参数及 snake_case 字段、实名审核详情新增 user_id/submitted_at/reviewed_at、POST 实名认证响应新增 data.id）；2026-06-12 更新（认证/角色权限/用户分组/实名认证）：分页响应字段 `list` → `items`（仅认证/角色权限/实名认证相关章节）；发送验证码接口拆分为 `/api/auth/verification-codes/email` 和 `/api/auth/verification-codes/phone` 两个独立接口，`email`/`phone`/`scene` 均为必填；手机号登录改为密码登录（`{phone, password}`）；实名认证提交响应字段修正为 `{id, status}`（`verification_id` 为已知缺口，待后续实现）；新增角色详情接口 `GET /api/admin/roles/{id}`；新增审计日志接口 `GET /api/admin/audit-logs`；新增"用户分组管理"章节（16 个接口）
> **测试服务器**：`http://8.130.9.163:8080`
> **鉴权方式**：所有需要登录的接口在 Header 中携带 `Authorization: Bearer <access_token>`

---

## 通用规范

### 响应结构

所有接口统一返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": { ... }
}
```

失败时：

```json
{
  "code": 40000,
  "message": "请求参数错误",
  "data": null
}
```

### 错误码速查

| code  | HTTP | 含义 |
|-------|------|------|
| 40000 | 400  | 请求参数错误 / 验证码错误或已过期 |
| 40001 | 401  | 未登录 / Token 无效或过期 |
| 40003 | 403  | 无权限 |
| 40004 | 404  | 资源不存在 |
| 40031 | 403  | 管理员未完成双重认证（手机+邮箱），需先调用 verify-phone 和 verify-email |
| 40101 | 401  | 账号已被封禁 |
| 40404 | 404  | 账号未注册，请先注册（登录发码时账号不存在） |
| 40900 | 409  | 账号已注册（注册发码时账号已存在） |
| 42900 | 429  | 请求频率超限 |
| 50000 | 500  | 服务器内部错误 |
| 60001 | 400  | 余额不足 |
| 70001 | 400  | 需要先完成实名认证 |

### 分页参数（列表接口通用）

请求：`?page=1&page_size=10`

响应 `data` 结构：
```json
{
  "items": [...],
  "pagination": {
    "page": 1,
    "page_size": 10,
    "total": 100
  }
}
```

> 说明：分页响应字段已统一为 `items`（原 `list` 已废弃）。本文档中"认证 / 角色权限 / 用户分组 / 实名认证"相关章节均已按此更新；第六/七/八章（商品/订单/钱包，后端乙模块）暂未同步，仍沿用 `list`，将在对应模块文档更新时一并修正。

---

## 一、认证模块（后端甲）

### 1.1 发送验证码

> ⚠️ 旧的统一 `{target, scene}` 请求体已废弃，当前为两个独立接口，字段名分别为 `email` / `phone`。

**POST** `/api/auth/verification-codes/email` — 发送邮箱验证码

请求体：
```json
{
  "email": "user@example.com",
  "scene": "register"
}
```

**POST** `/api/auth/verification-codes/phone` — 发送手机验证码

请求体：
```json
{
  "phone": "13812345678",
  "scene": "register"
}
```

> `email`（或 `phone`）和 `scene` 均为必填字段，缺失时返回 HTTP 400 / code=40000："email 和 scene 为必填字段"（手机接口对应为 "phone 和 scene 为必填字段"）。

`scene` 可选值及前置校验规则：

| scene | 说明 | 前置校验 |
|---|---|---|
| `register` | 注册验证码 | 账号已注册 → 返回 409/40900，拒绝发码 |
| `login` | 登录验证码 | 账号未注册 → 返回 404/40404，提示先注册 |
| `reset_password` | 重置密码 | 无前置校验 |
| `bind_phone` | 换绑手机号 | 无前置校验 |
| `bind_email` | 换绑邮箱 | 无前置校验 |
| `admin_verify` | 管理员双重认证 | 需要 Bearer Token + user:manage 权限 |

响应：`data: null`（成功即可）；测试环境响应体包含明文 `code` 字段

---

### 1.2 注册

> ⚠️ 旧的单独邮箱注册（`/api/auth/register/email`）和单独手机号注册（`/api/auth/register/phone`）已下线，唯一入口为统一注册。

**POST** `/api/auth/register` — 统一注册（手机 + 邮箱 + 用户名，需双验证码）

```json
{
  "username": "张三",
  "phone": "13812345678",
  "email": "user@example.com",
  "password": "Test1234!",
  "phone_code": "123456",
  "email_code": "654321"
}
```

响应（HTTP 201）：
```json
{
  "access_token": "eyJhbGci...",
  "refresh_token": "eyJhbGci...",
  "expires_in": 7200
}
```

---

### 1.3 登录

**POST** `/api/auth/login/email` — 邮箱 + 密码登录

```json
{
  "email": "user@example.com",
  "password": "Test1234!"
}
```

**POST** `/api/auth/login/phone` — 手机号 + 密码登录（与邮箱登录一致，使用 `crypto.CheckPassword` 校验密码）

```json
{
  "phone": "13812345678",
  "password": "Test1234!"
}
```

响应：同注册，返回 `access_token` / `refresh_token` / `expires_in`

---

### 1.4 刷新 Token

**POST** `/api/auth/refresh`

```json
{
  "refresh_token": "eyJhbGci..."
}
```

响应：同登录，返回新的 token 对

---

### 1.5 退出登录

**POST** `/api/auth/logout` *(需登录)*

```json
{
  "refresh_token": "eyJhbGci..."
}
```

响应：`data: null`

---

### 1.6 重置密码（忘记密码，无需旧密码）

**POST** `/api/auth/password/reset`

```json
{
  "target": "user@example.com",
  "target_type": "email",
  "code": "123456",
  "new_password": "NewPass1234!"
}
```

`target_type`：`phone` 或 `email`

---

### 1.7 个人信息

**GET** `/api/me` *(需登录)*

响应 `data`：
```json
{
  "id": 1,
  "username": "张三",
  "email": "us***@example.com",
  "email_verified": true,
  "phone": "138****5678",
  "phone_verified": true,
  "real_name_status": "unverified",
  "status": "active",
  "admin_phone_verified": false,
  "admin_email_verified": false,
  "created_at": "2026-06-01T10:00:00Z",
  "last_login_at": "2026-06-06T08:00:00Z"
}
```

`real_name_status`：`unverified` / `pending` / `verified` / `rejected`

---

### 1.8 修改个人信息

**PATCH** `/api/me/password` *(需登录)*
```json
{ "old_password": "OldPass!", "new_password": "NewPass!" }
```

**PATCH** `/api/me/username` *(需登录)*
```json
{ "username": "新用户名" }
```

**PATCH** `/api/me/phone` *(需登录)*
```json
{ "phone": "13912345678", "code": "123456" }
```

**PATCH** `/api/me/email` *(需登录)*
```json
{ "email": "new@example.com", "code": "123456" }
```

响应：`data: null`

---

## 二、实名认证模块（后端甲）

### 2.1 提交实名认证

**POST** `/api/identity/verifications` *(需登录)*

```json
{
  "real_name": "张三",
  "id_card_no": "110101199001011234",
  "attachments": ["https://oss.example.com/front.jpg", "https://oss.example.com/back.jpg"]
}
```

> 注意：身份证号不存明文，后端仅用于 HMAC 校验后丢弃，响应中返回脱敏值

响应 `data`：

```json
{
  "id": 1,
  "status": "pending"
}
```

> `id` 为新建认证记录的 ID，前端可用于后续查询或跳转；`status` 新建记录固定为 `pending`。
>
> ⚠️ 已知缺口：`verification_id` 字段为接口设计文档（`docs/full-api-design.md` §2.11）中规划但尚未实现的字段，当前响应中不存在该字段，已记录为已知缺口，后续版本补充。

---

### 2.2 查询我的认证状态

**GET** `/api/identity/verifications/me` *(需登录)*

响应 `data`：
```json
{
  "id": 1,
  "real_name": "张三",
  "id_card_no_masked": "110101******1234",
  "status": "pending",
  "reject_reason": null
}
```

`status`：`pending`（待审核）/ `verified`（已认证）/ `rejected`（已拒绝）

---

### 1.9 管理员双重认证（仅管理员账号）

> 管理员登录后，调用 IAM / 实名审核 / 封禁用户等管理端接口前必须先完成双重认证。
> 未完成时返回 403/40031"请先完成管理员双重认证（手机+邮箱）"。
> 认证有效期由服务端 `ADMIN_VERIFY_EXPIRE_HOURS` 配置（默认 24 小时），超时需重新认证。

**流程：**
```
1. 发手机验证码：POST /api/auth/verification-codes/phone  scene=admin_verify
2. 完成手机认证：POST /api/admin/auth/verify-phone  {"code": "..."}
3. 发邮箱验证码：POST /api/auth/verification-codes/email  scene=admin_verify
4. 完成邮箱认证：POST /api/admin/auth/verify-email  {"code": "..."}
5. 此后可调用管理端接口
```

**POST** `/api/admin/auth/verify-phone` *(需登录 + user:manage 权限)*
```json
{ "code": "123456" }
```

**POST** `/api/admin/auth/verify-email` *(需登录 + user:manage 权限，需手机已认证)*
```json
{ "code": "123456" }
```

---

## 三、用户管理（后端丙，需 `user:manage` 权限）

### 3.0 用户列表

**GET** `/api/admin/users` *(需登录 + `user:manage` 权限)*

Query 参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| keyword | string | 模糊搜索，匹配邮箱（脱敏前缀）或手机号（脱敏前缀） |
| status | string | active / disabled，不传则返回全部 |
| page | integer | 页码，默认 1 |
| page_size | integer | 每页数量，默认 20 |

响应 `data`：
```json
{
  "items": [
    {
      "id": 1,
      "email": "zh***@example.com",
      "phone": "138****5678",
      "status": "active",
      "real_name_status": "verified",
      "roles": [{ "id": 2, "code": "vip", "name": "VIP会员" }],
      "created_at": "2026-01-01T00:00:00Z"
    }
  ],
  "pagination": { "page": 1, "page_size": 20, "total": 100 }
}
```

> 邮箱和手机号均为脱敏值，明文不出现在任何响应中。

---

### 3.0b 用户详情

**GET** `/api/admin/users/{id}` *(需登录 + `user:manage` 权限)*

响应 `data`：
```json
{
  "id": 1,
  "email": "zh***@example.com",
  "phone": "138****5678",
  "status": "active",
  "real_name_status": "verified",
  "roles": [{ "id": 2, "code": "vip", "name": "VIP会员" }],
  "permission_overrides": [],
  "wallet_summary": { "balance": "100.00", "frozen": "0.00" },
  "asset_summary": { "total_count": 3 },
  "created_at": "2026-01-01T00:00:00Z"
}
```

---

## 四、角色权限模块（后端甲，需 `role:manage` 权限 + 管理员双重认证）

### 3.1 角色管理

**GET** `/api/admin/roles` — 角色列表（支持 `?keyword=` 模糊搜索角色 code / name，`?page=&page_size=` 分页）

**GET** `/api/admin/roles/{id}` — 角色详情（新增接口）

响应 `data`（与角色列表单条结构一致）：
```json
{
  "id": 2,
  "code": "vip",
  "name": "VIP会员",
  "description": "可见高级商品"
}
```

角色不存在时返回 HTTP 404 / code=40400"角色不存在"。

**POST** `/api/admin/roles`
```json
{ "code": "vip", "name": "VIP用户", "description": "可见高级商品" }
```

**PUT** `/api/admin/roles/{id}`
```json
{ "code": "vip", "name": "VIP会员", "description": "更新描述" }
```

**DELETE** `/api/admin/roles/{id}`

### 3.2 权限列表

**GET** `/api/admin/permissions` — 查看所有权限定义（支持 `?keyword=` 模糊搜索权限 code / name，`?page=&page_size=` 分页）

### 3.3 用户角色分配

**GET** `/api/admin/users/{id}/roles` — 查询用户角色

**POST** `/api/admin/users/{id}/roles`
```json
{ "role_id": 2, "reason": "升级为 VIP" }
```

**DELETE** `/api/admin/users/{id}/roles/{role_id}`

### 3.4 用户权限覆盖

**GET** `/api/admin/users/{id}/permission-overrides` — 支持以下 Query 过滤参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| effect | string | allow 或 deny，不传则返回全部 |
| permission_code | string | 按权限 code 精确过滤 |
| page | integer | 页码，默认 1 |
| page_size | integer | 每页数量，默认 20 |

响应 `data.items` 字段（全部 snake_case）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 覆盖记录 ID |
| user_id | integer | 用户 ID |
| permission_id | integer | 权限 ID |
| permission_code | string | 权限 code |
| effect | string | allow 或 deny |
| reason | string | 原因 |
| expires_at | string | 过期时间（无过期为 null） |
| created_at | string | 创建时间（ISO 8601） |

**POST** `/api/admin/users/{id}/permission-overrides`
```json
{ "permission_id": 5, "effect": "allow", "reason": "临时授权" }
```

`effect`：`allow` / `deny`（只接受小写）

**DELETE** `/api/admin/users/{id}/permission-overrides/{override_id}`

### 3.5 审计日志（新增接口）

**GET** `/api/admin/audit-logs`

> ⚠️ 当前权限要求复用 `role:manage`（非独立 `audit:read`，已知待办，后续可能拆分为独立权限码）。

Query 参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| module | string | 按模块精确过滤（如 `iam` / `auth` / `identity`），不传则返回全部 |
| action | string | 按操作类型精确过滤（如 `create` / `update` / `delete`），不传则返回全部 |
| page | integer | 页码，默认 1 |
| page_size | integer | 每页数量，默认 20 |

响应 `data.items` 单条结构：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 日志记录 ID |
| operator_id | integer\|null | 操作人用户 ID |
| module | string | 所属模块 |
| action | string | 操作类型 |
| target_type | string\|null | 操作对象类型 |
| target_id | string\|null | 操作对象 ID |
| ip | string\|null | 操作人 IP |
| created_at | string | 操作时间（ISO 8601） |

```json
{
  "items": [
    {
      "id": 1,
      "operator_id": 1,
      "module": "iam",
      "action": "role:update",
      "target_type": "role",
      "target_id": "2",
      "ip": "127.0.0.1",
      "created_at": "2026-06-12T10:00:00Z"
    }
  ],
  "pagination": { "page": 1, "page_size": 20, "total": 1 }
}
```

> 按 `created_at` 倒序排列。`request_summary` 字段（请求摘要 JSON）当前未在响应中返回。

---

## 五、实名审核（后端甲，需 `identity:review` 权限）

**GET** `/api/admin/identity-verifications` — 审核列表（支持 `?status=pending|verified|rejected`，不传则返回全部；支持 `?page=&page_size=` 分页）

**GET** `/api/admin/identity-verifications/{id}` — 审核详情

响应 `data` 关键字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 记录 ID |
| user_id | integer | 所属用户 ID |
| real_name | string | 真实姓名 |
| id_card_no_masked | string | 脱敏证件号 |
| status | string | pending / verified / rejected |
| reject_reason | string | 拒绝原因（rejected 时有值） |
| submitted_at | string | 提交时间（ISO 8601） |
| reviewed_at | string | 审核操作时间（ISO 8601，待审为 null） |

**PATCH** `/api/admin/identity-verifications/{id}/review`
```json
{ "approve": true, "reason": "" }
```
拒绝时：`{ "approve": false, "reason": "证件模糊" }`

---

## 五之一、用户分组管理（后端甲，需 `group:manage` 权限 + 管理员双重认证）

> 全部 16 个接口均需 `Bearer Token` + `group:manage` 权限 + 管理员双重认证（参见"1.9 管理员双重认证"）。
> 分页响应统一使用 `items` 字段。

### 5.1.1 分组 CRUD

**GET** `/api/admin/user-groups` — 分组列表

Query 参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| type | string | 按分组类型过滤：`region` / `org` / `custom`，不传则返回全部 |
| keyword | string | 模糊搜索分组 code / name |
| page | integer | 页码，默认 1 |
| page_size | integer | 每页数量，默认 20 |

响应 `data`：
```json
{
  "items": [
    {
      "id": 1,
      "code": "default",
      "name": "默认分组",
      "type": "custom",
      "is_default": true,
      "description": "系统默认分组",
      "created_at": "2026-06-01T00:00:00Z"
    }
  ],
  "pagination": { "page": 1, "page_size": 20, "total": 1 }
}
```

**POST** `/api/admin/user-groups` — 创建分组

```json
{
  "code": "region-east",
  "name": "华东区",
  "type": "region",
  "is_default": false,
  "description": "华东区域分组"
}
```

`code`、`name` 为必填，缺失返回 400/40000"code 和 name 不能为空"。`type` 不传默认为 `custom`，可选值：`region` / `org` / `custom`。

响应（HTTP 201）`data`：与列表单条结构一致（含 `id` / `created_at`）。

**GET** `/api/admin/user-groups/{id}` — 分组详情

响应 `data`：与列表单条结构一致。分组不存在返回 404/40400"分组不存在"。

**PUT** `/api/admin/user-groups/{id}` — 更新分组（仅 `name` / `type` / `description` / `is_default` 可改，`code` 不可改）

```json
{
  "name": "华东区（更新）",
  "type": "region",
  "is_default": false,
  "description": "更新后的描述"
}
```

响应：`data: null`

**DELETE** `/api/admin/user-groups/{id}` — 删除分组

错误情况：
- 分组内仍有成员 → HTTP 409 / code=40901"分组内仍有成员，请先移除所有成员"
- 分组内仍有有效邀请码 → HTTP 409 / code=40902"分组内仍有有效邀请码，请先禁用后再删除分组"

### 5.1.2 成员管理

**GET** `/api/admin/user-groups/{id}/members` — 分组成员列表

Query 参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| group_role | string | 按组内角色过滤：`admin` / `member`，不传则返回全部 |
| page | integer | 页码，默认 1 |
| page_size | integer | 每页数量，默认 20 |

响应 `data`：
```json
{
  "items": [
    {
      "id": 10,
      "user_id": 5,
      "group_id": 1,
      "group_role": "member",
      "created_at": "2026-06-01T00:00:00Z"
    }
  ],
  "pagination": { "page": 1, "page_size": 20, "total": 1 }
}
```

**POST** `/api/admin/user-groups/{id}/members` — 添加成员

```json
{
  "user_id": 5,
  "group_role": "member"
}
```

`user_id` 必填（缺失或为 0 返回 400/40000"user_id 不能为空"）。`group_role` 可选值：`admin` / `member`，不传默认为 `member`。

响应：HTTP 201，`data: null`。用户已在该分组中返回 HTTP 409 / code=40900"用户已在该分组中"。

**PATCH** `/api/admin/user-groups/{id}/members/{uid}` — 修改成员组内角色

```json
{ "group_role": "admin" }
```

`group_role` 只能为 `admin` 或 `member`，否则返回 400/40000"group_role 只能为 admin 或 member"。

响应：`data: null`。用户不在该分组中返回 404/40400"用户不在该分组中"。

**DELETE** `/api/admin/user-groups/{id}/members/{uid}` — 移除成员

响应：`data: null`。用户不在该分组中返回 404/40400"用户不在该分组中"。

### 5.1.3 用户所在分组

**GET** `/api/admin/users/{id}/groups` — 查询指定用户所属的所有分组

响应 `data`（数组，非分页）：
```json
[
  {
    "group_id": 1,
    "group_role": "member",
    "joined_at": "2026-06-01T00:00:00Z"
  }
]
```

### 5.1.4 组权限

**GET** `/api/admin/user-groups/{id}/permissions` — 查询分组权限列表

响应 `data`（数组，非分页）：
```json
[
  {
    "id": 1,
    "group_id": 1,
    "permission_code": "app:use:cloud-disk",
    "created_at": "2026-06-01T00:00:00Z"
  }
]
```

**POST** `/api/admin/user-groups/{id}/permissions` — 给分组添加权限码

```json
{ "permission_code": "app:use:cloud-disk" }
```

`permission_code` 必填，缺失返回 400/40000"permission_code 不能为空"。

响应：HTTP 201，`data: null`。该权限码已添加到此分组返回 HTTP 409 / code=40900"该权限码已添加到此分组"。

**DELETE** `/api/admin/user-groups/{id}/permissions/{code}` — 移除分组权限码

`{code}` 为权限码（如 `app:use:cloud-disk`）。响应：`data: null`。

### 5.1.5 邀请码

**GET** `/api/admin/user-groups/{id}/invite-codes` — 邀请码列表

Query 参数：

| 参数 | 类型 | 说明 |
|---|---|---|
| status | string | 按状态过滤：`active` / `disabled`，不传则返回全部 |
| page | integer | 页码，默认 1 |
| page_size | integer | 每页数量，默认 20 |

响应 `data`：
```json
{
  "items": [
    {
      "id": 1,
      "code": "ABCD1234",
      "group_id": 1,
      "default_group_role": "member",
      "max_uses": 0,
      "used_count": 0,
      "expires_at": null,
      "status": "active",
      "created_by": 1,
      "created_at": "2026-06-01T00:00:00Z"
    }
  ],
  "pagination": { "page": 1, "page_size": 20, "total": 1 }
}
```

**POST** `/api/admin/user-groups/{id}/invite-codes` — 创建邀请码

```json
{
  "code": "ABCD1234",
  "default_group_role": "member",
  "max_uses": 0,
  "expires_at": null
}
```

字段说明：
- `code`：邀请码，留空时由后端自动生成 8 位随机码
- `default_group_role`：通过该邀请码注册时分配的组内角色，可选值 `admin` / `member`，不传默认为 `member`
- `max_uses`：最大使用次数，`0` 表示不限次数
- `expires_at`：过期时间，ISO 8601 格式字符串，`null` 表示永不过期；格式错误返回 400/40000"expires_at 格式错误，需 ISO 8601"

响应（HTTP 201）`data`：与列表单条结构一致。邀请码重复返回 HTTP 409 / code=40900"邀请码已存在，请更换"。

**PATCH** `/api/admin/user-groups/{id}/invite-codes/{invite_id}/disable` — 禁用邀请码

响应：`data: null`。禁用后该邀请码 `status` 变为 `disabled`，无法再用于注册。

### 枚举值小结

| 字段 | 可选值 |
|---|---|
| `user_group.type` | `region`（区域）/ `org`（机构）/ `custom`（自定义，默认） |
| `group_role`（成员/邀请码默认角色） | `admin`（组管理员）/ `member`（普通组员，默认） |
| `invite_code.status` | `active` / `disabled` |

---

## 六、商品模块（后端乙）

### 5.1 用户端

**GET** `/api/products?page=1&page_size=10` *(需登录)*

响应 `data`：
```json
{
  "list": [
    {
      "id": 1,
      "product_type": "service",
      "product_code": "cloud-001",
      "name": "云服务基础版",
      "description": "...",
      "status": "active"
    }
  ],
  "pagination": { "page": 1, "page_size": 10, "total": 5 }
}
```

**GET** `/api/products/{id}` *(需登录)*

响应 `data`：
```json
{
  "product": { "id": 1, "name": "...", "status": "active" },
  "plans": [
    {
      "id": 1,
      "plan_code": "basic",
      "name": "基础版一年",
      "billing_type": "one_time",
      "duration_days": 365,
      "user_price": "10.000000",
      "currency": "CNY",
      "status": "active"
    }
  ]
}
```

**GET** `/api/products/{id}/plans` *(需登录)*

响应 `data`：`{ "plans": [...] }`（含用户实际价格）

---

### 5.2 购买商品

**POST** `/api/products/{id}/purchase` *(需登录)*

> **必须携带请求头** `Idempotency-Key: <唯一字符串>`（防重复提交）

```json
{
  "plan_id": 1,
  "quantity": 1,
  "remark": "购买备注（可选）"
}
```

响应 `data`：
```json
{
  "order_id": 101,
  "order_no": "ORD2026060600001",
  "status": "paid",
  "amount": "10.000000",
  "idempotent": false
}
```

`idempotent: true` 表示该 Idempotency-Key 已存在，返回原订单，不重复扣费。

**常见错误**：
- `70001` — 需要先完成实名认证
- `40003` — 无购买权限（角色未配置 can_buy）
- `60001` — 余额不足

---

### 5.3 管理端商品

**GET** `/api/admin/products?page=1&page_size=10` *(需 `product:view` 权限)*

**POST** `/api/admin/products` *(需 `product:create` 权限)*
```json
{
  "product_type": "service",
  "product_code": "cloud-001",
  "name": "云服务基础版",
  "description": "描述文字",
  "status": "draft"
}
```

**GET** `/api/admin/products/{id}` *(需 `product:view` 权限)*

**PATCH** `/api/admin/products/{id}` *(需 `product:edit` 权限)*
```json
{ "name": "新名称", "description": "新描述" }
```

**PATCH** `/api/admin/products/{id}/status` *(需 `product:edit` 权限)*
```json
{ "status": "active" }
```
`status`：`draft` / `active` / `inactive`

**GET** `/api/admin/products/{id}/plans` *(需 `product:create` 权限)*

**POST** `/api/admin/products/{id}/plans` *(需 `product:create` 权限)*
```json
{
  "plan_code": "basic",
  "name": "基础版",
  "billing_type": "one_time",
  "duration_days": 365,
  "status": "active"
}
```
`billing_type`：`one_time` / `monthly` / `yearly` / `usage`

**PATCH** `/api/admin/products/{id}/plans/{plan_id}` *(需 `product:edit` 权限)*
```json
{ "name": "新套餐名", "status": "inactive" }
```

**PATCH** `/api/admin/products/{id}/prices` *(需 `product:edit` 权限)*

覆盖写入（全量替换该套餐的价格）：
```json
{
  "prices": [
    { "price_amount": "10.00", "currency": "CNY" },
    { "role_id": 2, "price_amount": "8.00", "currency": "CNY" },
    { "membership_level_id": 1, "price_amount": "6.00", "currency": "CNY" }
  ]
}
```

价格优先级：**会员价 > 角色价 > 默认价**（三者均可配置，取用户匹配的最高优先级）

**PATCH** `/api/admin/products/{id}/access` *(需 `product:edit` 权限)*

覆盖写入角色访问规则：
```json
{
  "accesses": [
    { "role_id": 1, "can_view": true, "can_buy": true, "can_use": true },
    { "role_id": 2, "can_view": true, "can_buy": false, "can_use": false }
  ]
}
```

---

## 七、订单模块（后端乙）

### 6.1 用户端

**GET** `/api/orders?page=1&page_size=10` *(需登录)*

支持过滤：`?status=paid&order_type=purchase`

响应 `data`：
```json
{
  "list": [
    {
      "id": 101,
      "order_no": "ORD2026060600001",
      "order_type": "purchase",
      "product_id": 1,
      "product_plan_id": 1,
      "status": "paid",
      "amount": "10.000000",
      "currency": "CNY",
      "paid_at": "2026-06-06T10:00:00Z",
      "created_at": "2026-06-06T09:59:00Z"
    }
  ],
  "pagination": { "page": 1, "page_size": 10, "total": 3 }
}
```

`status`：`pending` / `paid` / `cancelled` / `failed`

`order_type`：`purchase`（购买订单）/ `recharge`（充值订单）

**GET** `/api/orders/{id}` *(需登录)*

---

### 6.2 管理端

**GET** `/api/admin/orders?page=1&page_size=10` *(需 `order:list` 权限)*

支持过滤：`?user_id=1&status=paid&order_type=purchase`

**GET** `/api/admin/orders/{id}` *(需 `order:list` 权限)*

---

## 八、钱包 & 支付模块（后端乙）

### 7.1 用户端

**GET** `/api/wallet` *(需登录)*

响应 `data`：
```json
{
  "id": 1,
  "user_id": 1,
  "balance_amount": "90.000000",
  "frozen_amount": "0.000000",
  "currency": "CNY"
}
```

**GET** `/api/wallet/transactions?page=1&page_size=10` *(需登录)*

响应 `data.list` 单条结构：
```json
{
  "id": 1,
  "type": "recharge",
  "direction": "in",
  "amount": "100.000000",
  "balance_after": "100.000000",
  "remark": "微信支付充值",
  "created_at": "2026-06-06T10:00:00Z"
}
```

`type`：`recharge`（充值）/ `consume`（消费）/ `refund`（退款）/ `freeze`（冻结）/ `unfreeze`（解冻）

`direction`：`in`（入账）/ `out`（出账）

**POST** `/api/recharge/orders` *(需登录)*

```json
{
  "amount": "100.00",
  "provider": "wechat",
  "remark": "充值"
}
```

`provider`：`wechat` / `alipay`

响应 `data`：
```json
{
  "order_id": 201,
  "pay_url": "https://pay.example.com/..."
}
```

---

### 7.2 支付回调（无需登录）

**POST** `/api/payments/notify/{provider}`

`provider`：`wechat` 或 `alipay`

微信必须携带请求头：
```
Wechatpay-Signature: <签名>
Wechatpay-Timestamp: <时间戳>
Wechatpay-Nonce: <随机串>
```

支付宝 body 中必须含 `sign` 字段。

缺少签名字段返回 HTTP 400 / code=40000。

---

### 7.3 管理端钱包

**GET** `/api/admin/users/{id}/wallet` *(需 `wallet:view` 权限)*

**GET** `/api/admin/wallet-transactions?page=1&page_size=10` *(需 `wallet:view` 权限)*

支持过滤：`?user_id=1`

**PATCH** `/api/admin/users/{id}/wallet/freeze` *(需 `wallet:view` 权限)*
```json
{
  "amount": "50.00",
  "action": "freeze",
  "remark": "风控冻结"
}
```
`action`：`freeze` / `unfreeze`

**GET** `/api/admin/payment-callbacks?page=1&page_size=10` *(需 `wallet:view` 权限)*

支持过滤：`?provider=wechat&status=processed`

---

## 九、管理员双重认证（后端甲，需 `user:manage` 权限）

**POST** `/api/admin/auth/verify-phone`
```json
{ "code": "123456" }
```

**POST** `/api/admin/auth/verify-email`
```json
{ "code": "123456" }
```

---

## 附录

### 权限码清单

| 权限码 | 说明 |
|--------|------|
| `role:manage` | 角色与权限管理（含角色详情、审计日志，审计日志为复用，已知待办） |
| `group:manage` | 用户分组管理（分组/成员/组权限/邀请码） |
| `identity:review` | 实名认证审核 |
| `user:manage` | 用户管理（管理员双重认证） |
| `product:view` | 查看商品（只读） |
| `product:create` | 创建商品/套餐 |
| `product:edit` | 编辑商品/价格/权限 |
| `order:list` | 查看订单 |
| `wallet:view` | 查看钱包/流水/回调 |

### 枚举值汇总

| 字段 | 可选值 |
|------|--------|
| `real_name_status` | `unverified` / `pending` / `verified` / `rejected` |
| `order.status` | `pending` / `paid` / `cancelled` / `failed` |
| `order.order_type` | `purchase` / `recharge` |
| `product.status` | `draft` / `active` / `inactive` |
| `wallet_transaction.type` | `recharge` / `consume` / `refund` / `freeze` / `unfreeze` |
| `wallet_transaction.direction` | `in` / `out` |
| `payment_callback.status` | `received` / `processed` / `ignored` |
| `billing_type` | `one_time` / `monthly` / `yearly` / `usage` |
| `provider` | `wechat` / `alipay` |
