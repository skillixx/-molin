# 前端接口参考文档

> **版本**：Week 1 + Week 2 已验收（2026-06-06）；2026-06-10 补丁更新（发码拦截 + 管理员双重认证强制）；2026-06-11 接口变更同步（用户列表 keyword、角色/权限模糊搜索、实名审核 status 过滤、权限覆盖过滤参数及 snake_case 字段、实名审核详情新增 user_id/submitted_at/reviewed_at、POST 实名认证响应新增 data.id）；2026-06-12 更新（认证/角色权限/用户分组/实名认证）：分页响应字段 `list` → `items`（仅认证/角色权限/实名认证相关章节）；发送验证码接口拆分为 `/api/auth/verification-codes/email` 和 `/api/auth/verification-codes/phone` 两个独立接口，`email`/`phone`/`scene` 均为必填；手机号登录改为密码登录（`{phone, password}`）；实名认证提交响应字段修正为 `{id, status}`（`verification_id` 为设计文档冗余字段，已于 2026-06-12 从 `full-api-design.md` 中移除，不再视为缺口）；新增角色详情接口 `GET /api/admin/roles/{id}`；新增审计日志接口 `GET /api/admin/audit-logs`；新增"用户分组管理"章节（16 个接口）；2026-06-13 更新：手机号登录改为验证码登录（`{phone, code}`，PR#20）；退出登录后当前 Access Token 立即吊销，401/40001（PR#22）；`/api/auth/login/phone`、`/api/auth/login/email` 对未注册账号统一返回 404/40404（PR#25）；**2026-06-15 更新（Round 7 审计 D-93/D-94/D-95/D-96 全部闭环）**：登录/注册/刷新令牌响应新增 `user` 对象（D-93，PR#91）；密码长度约束统一为 6-72 位（D-94，PR#95）；auth/iam/identity 模块 11 个分页接口响应结构改为扁平（去掉嵌套 `pagination` 对象，D-95，PR#97）；`bind_phone`/`bind_email`/`admin_verify` 三个 scene 迁移到专属认证态发码接口，不再接受公开端点的请求（D-96，PR#93）；**2026-06-16 更新（后端乙缺陷修复闭环，88/88 回归全通过）**：`GET /api/wallet` 响应字段 `id` → `wallet_id`（D-008，PR#135）；`PATCH /api/admin/products/{id}/prices` body 结构统一为 `{"items":[{"product_plan_id":...,...}]}`（D-009，PR#135）；`PATCH /api/admin/products/{id}/access` body key 统一为 `items`，缺失 `items` 字段返回 400（D-011，PR#137）；购买接口 `POST /api/products/{id}/purchase` 响应新增 `idempotent` 字段，`status` 直接返回 `paid`（BUG-A，PR#136）；商品/套餐/计划不存在时接口统一返回 404/40400（BUG-B，PR#136）；重复 product_code/plan_code 返回 400 友好提示（BUG-C，PR#136）；多套餐价格覆盖写入改为单事务原子操作（BUG-D，PR#136）；**2026-06-16 更新（二）（后端乙契约勘误 + #144，已部署测试服回归 52/52 通过）**：套餐 `user_price` 未配置价格时统一返回 `"-1"`（区别于合法免费价 `"0"`，#144，PR#144）；`GET /api/products/{id}/plans` 响应订正为 D-95 扁平分页 `{items,page,page_size,total}`（原文档误写 `{plans:[]}`）；购买响应补 `asset_id` 字段（异步开通时为 `null`/`0`）；`order_type` 取值订正为 `product`（购买）/`recharge`（充值）（原误写 `purchase`）；商品状态切换 `PATCH /api/admin/products/{id}/status` 仅接受 `active`/`inactive`（`draft` 为创建初始态、不可设置）；**2026-06-19 更新（后端丙会员对接增强 #167~#170，已部署测试服回归 22 用例通过）**：新增公开权益端点 `GET /api/memberships/{id}/benefits`（无需登录，仅返回 `status=active` 权益，等级不存在/未上架返回 404/40400，见 §11.1b，PR#168）；`GET /api/my/membership`（§11.2）与 `GET /api/admin/user-memberships`（§11.5）会员对象**内联** `level_code`/`level_name`（保留 `level_id`，纯增量，前端无需再按 level_id 映射等级名，PR#168）；`asset_id` 去掉 `omitempty`，无关联资产时返回 `null`（key 恒在，PR#169）；管理端列表 `page_size` 上限 100、用户端公告上限 50；帮助文章详情 `GET /api/help/articles/{id}` 的 `data` 直接为文章对象（非包裹，§12.2，PR#167）
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
| 40404 | 404  | 账号未注册，请先注册（登录发码时账号不存在；`/api/auth/login/phone`、`/api/auth/login/email` 本身对未注册账号也返回此码，见 §1.3） |
| 40900 | 409  | 账号已注册（注册发码时账号已存在） |
| 42900 | 429  | 请求频率超限 |
| 50000 | 500  | 服务器内部错误 |
| 60001 | 400  | 余额不足 |
| 70001 | 400  | 需要先完成实名认证 |

### 分页参数（列表接口通用）

请求：`?page=1&page_size=10`

响应 `data` 结构（auth/iam/identity 模块，D-95 后扁平化）：
```json
{
  "items": [...],
  "page": 1,
  "page_size": 10,
  "total": 100
}
```

> **注意**：全部列表接口（第一至八章，含商品/订单/钱包/消费记录等后端乙模块）已统一为扁平结构（D-95 及其姊妹修复，2026-06-15 全量完成），`items`/`page`/`page_size`/`total` 同级位于 `data` 顶层，不再有 `list` 字段或 `pagination` 子对象。

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

`scene` 可选值及前置校验规则（公开端点仅接受以下 3 个 scene）：

| scene | 说明 | 前置校验 |
|---|---|---|
| `register` | 注册验证码 | 账号已注册 → 返回 409/40900，拒绝发码 |
| `login` | 登录验证码 | 账号未注册 → 返回 404/40404，提示先注册 |
| `reset_password` | 重置密码 | 无前置校验 |

> ⚠️ **D-96（2026-06-15）**：`bind_phone` / `bind_email` / `admin_verify` 三个 scene 已从公开端点移除，调用此端点传入这三个 scene 会返回 `400 40000`。请改用以下专属认证态接口：
>
> - 换绑手机号发码：`POST /api/me/verification-codes/phone`（需登录，§1.8.1）
> - 换绑邮箱发码：`POST /api/me/verification-codes/email`（需登录，§1.8.1）
> - 管理员双重认证发码：`POST /api/admin/auth/verification-codes/{phone,email}`（需 user:manage 权限，§1.9）

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

密码 `password` 长度须为 **6-72 位**（D-94）；低于 6 位返回 `400 40000`，超过 72 位同返回 `400 40000`。

响应（HTTP 201，D-93）：
```json
{
  "access_token": "eyJhbGci...",
  "refresh_token": "eyJhbGci...",
  "expires_in": 7200,
  "user": {
    "id": 1,
    "email": "us***@example.com",
    "phone": "138****5678",
    "real_name_status": "unverified",
    "status": "active"
  }
}
```

> `user` 字段（D-93）：登录成功后前端可直接读取用户基本信息，无需再单独调用 `GET /api/me`。`email`/`phone` 为脱敏值。

---

### 1.3 登录

**POST** `/api/auth/login/email` — 邮箱 + 密码登录

```json
{
  "email": "user@example.com",
  "password": "Test1234!"
}
```

错误：
- 邮箱未注册 → `404 40404`「邮箱未注册，请先注册」
- 账号已被禁用 → `403 40003`
- 密码错误 → `401 40001`「邮箱或密码错误」

**POST** `/api/auth/login/phone` — 手机号 + 验证码登录（PR#20，非密码登录）

登录前需先调用 `POST /api/auth/verification-codes/phone`（`scene=login`）获取验证码：

```json
// POST /api/auth/verification-codes/phone
{
  "phone": "13812345678",
  "scene": "login"
}
```

再调用登录接口：

```json
{
  "phone": "13812345678",
  "code": "123456"
}
```

错误：
- 验证码错误或已过期 → `400 40000`
- 手机号未注册 → `404 40404`「手机号未注册，请先注册」
- 账号已被禁用 → `403 40003`

响应（两者一致，D-93）：与注册响应结构相同，返回 `access_token` / `refresh_token` / `expires_in` / `user`

---

### 1.4 刷新 Token

**POST** `/api/auth/refresh`

```json
{
  "refresh_token": "eyJhbGci..."
}
```

响应（D-93）：与登录响应结构相同，返回新的 token 对及 `user` 对象

---

### 1.5 退出登录

**POST** `/api/auth/logout` *(需登录)*

```json
{
  "refresh_token": "eyJhbGci..."
}
```

响应：`data: null`

> **Token 即时吊销（PR#22）**：退出成功后，本次请求 `Authorization` 头携带的 Access Token 会立即被加入吊销黑名单，在自然过期前失效。此后再用该 Token 访问任意需鉴权接口均返回 `401 40001`「token 已失效，请重新登录」，前端应在退出后清除本地 Token 并跳转登录页。该吊销仅影响当前这一个 Access Token，不会影响同账号在其他设备/标签页的登录状态。

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

`new_password` 长度须为 **6-72 位**（D-94）；低于 6 位或超过 72 位均返回 `400 40000`。

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

`new_password` 长度须为 **6-72 位**（D-94）；低于 6 位或超过 72 位均返回 `400 40000`。

**PATCH** `/api/me/username` *(需登录)*
```json
{ "username": "新用户名" }
```

**PATCH** `/api/me/phone` *(需登录)*
```json
{ "phone": "13912345678", "code": "123456" }
```

> 调用前须先通过 `POST /api/me/verification-codes/phone` 向新手机号发送验证码（§1.8.1）。

**PATCH** `/api/me/email` *(需登录)*
```json
{ "email": "new@example.com", "code": "123456" }
```

> 调用前须先通过 `POST /api/me/verification-codes/email` 向新邮箱发送验证码（§1.8.1）。

响应：`data: null`

---

### 1.8.1 换绑发码（D-96，需登录）

> ⚠️ **D-96（2026-06-15）新增**：换绑手机号/邮箱的验证码不再走公开发码端点，必须使用以下认证态接口（需携带有效 Bearer Token）。

**POST** `/api/me/verification-codes/phone` — 向新手机号发送换绑验证码

```json
{ "phone": "13912345678" }
```

**POST** `/api/me/verification-codes/email` — 向新邮箱发送换绑验证码

```json
{ "email": "new@example.com" }
```

响应：`data: null`；测试环境响应体包含明文 `code` 字段。

错误：
- `phone`/`email` 缺失 → `400 40000`
- 未登录 → `401 40001`

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
> `verification_id` 字段经核实为设计文档（`docs/full-api-design.md` §2.11）中的冗余重复字段（与 `id` 同值），已从设计文档中移除。当前 `{id, status}` 响应即为最终形态，不再是待实现缺口。

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

**流程（D-96 后，2026-06-15 更新）：**
```
1. 发手机验证码：POST /api/admin/auth/verification-codes/phone
2. 完成手机认证：POST /api/admin/auth/verify-phone  {"code": "..."}
3. 发邮箱验证码：POST /api/admin/auth/verification-codes/email
4. 完成邮箱认证：POST /api/admin/auth/verify-email  {"code": "..."}
5. 此后可调用管理端接口
```

> ⚠️ **D-96（2026-06-15）**：admin_verify 发码已从公开端点（`/api/auth/verification-codes/*`）迁移到以下专属管理员认证端点，旧调用方式不再有效。

**POST** `/api/admin/auth/verification-codes/phone` *(需登录 + user:manage 权限)* — 向当前管理员绑定的手机号发送验证码

响应：`data: null`；测试环境包含明文 `code`。

**POST** `/api/admin/auth/verification-codes/email` *(需登录 + user:manage 权限)* — 向当前管理员绑定的邮箱发送验证码

响应：`data: null`；测试环境包含明文 `code`。

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
  "page": 1,
  "page_size": 20,
  "total": 100
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
  "page": 1,
  "page_size": 20,
  "total": 1
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
  "page": 1,
  "page_size": 20,
  "total": 1
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
  "page": 1,
  "page_size": 20,
  "total": 1
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
  "page": 1,
  "page_size": 20,
  "total": 1
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

响应 `data`（D-95 扁平分页）：
```json
{
  "items": [
    {
      "id": 1,
      "product_type": "service",
      "product_code": "cloud-001",
      "name": "云服务基础版",
      "description": "...",
      "status": "active"
    }
  ],
  "page": 1,
  "page_size": 10,
  "total": 5
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
      "quota_json": null,
      "user_price": "10.000000",
      "currency": "CNY",
      "status": "active"
    }
  ]
}
```

> **`user_price`（#144，2026-06-16）**：为「当前用户实际价格」（按 会员价 > 角色价 > 默认价 优先级计算）。
> **未配置任何价格时返回 `"-1"`**（哨兵值），用以与「合法免费价 `"0"`」区分。前端应以 `user_price === "-1"`（或 `Number(user_price) < 0`）判定「未定价/暂不可购买」并禁用购买按钮，**不要**把 `"0"` 当作未配置。

**GET** `/api/products/{id}/plans` *(需登录)*

响应 `data`（**D-95 扁平分页**，注意不是 `{plans:[]}`）：`{ "items": [ /* 同上 plan 结构，含 user_price */ ], "page": 1, "page_size": 20, "total": N }`。用户端套餐不真正分页，但契约仍为扁平分页结构。

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
  "asset_id": null,
  "idempotent": false
}
```

`idempotent: true` 表示该 Idempotency-Key 已存在，返回原订单，不重复扣费。
`asset_id`：开通的资产 ID；异步开通时为 `null`，资产生效后请通过「我的资产」接口查询。

**常见错误**（前端需分别处理）：
- `70001`（HTTP 400）— 需要先完成实名认证 → 引导实名
- `40003`（HTTP 403）— 无购买权限（角色未配置 can_buy）
- `60001`（HTTP 400）— 余额不足 → 引导充值
- `40000`（HTTP 400）— 该套餐未配置价格 / `plan_id` 缺失
- `50000`（HTTP 409）— 系统繁忙（高并发乐观锁耗尽）→ 可复用同一 Idempotency-Key 重试

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

**GET** `/api/admin/products/{id}/plans` *(需 `product:view` 权限)*

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

覆盖写入（全量替换该套餐的价格）。**批量写入键名统一为 `items`**：
```json
{
  "items": [
    { "product_plan_id": 1, "price_amount": "10.00", "currency": "CNY" },
    { "product_plan_id": 1, "role_id": 2, "price_amount": "8.00", "currency": "CNY" },
    { "product_plan_id": 1, "membership_level_id": 1, "price_amount": "6.00", "currency": "CNY" }
  ]
}
```

价格优先级：**会员价 > 角色价 > 默认价**（三者均可配置，取用户匹配的最高优先级）

**PATCH** `/api/admin/products/{id}/access` *(需 `product:edit` 权限)*

覆盖写入角色访问规则。**批量写入键名统一为 `items`**：
```json
{
  "items": [
    { "role_id": 1, "can_view": true, "can_buy": true, "can_use": true },
    { "role_id": 2, "can_view": true, "can_buy": false, "can_use": false }
  ]
}
```

---

### 5.4 计费规则（按量计费，需对应权限）

商品按量计费规则管理（对应 `product_billing_rules`）。

**GET** `/api/admin/product-billing-rules?page=1&page_size=10` *(需 `product:view` 权限)*

支持过滤：`?product_id=1&status=active`。响应 `data` 为 D-95 扁平分页，`items` 单条结构：
```json
{
  "id": 1,
  "product_id": 1,
  "product_plan_id": 1,
  "usage_type": "api_call",
  "usage_unit": "次",
  "price_amount": "0.010000",
  "currency": "CNY",
  "billing_mode": "per_unit",
  "free_quota": "100",
  "status": "active",
  "created_at": "2026-06-15T10:00:00Z",
  "updated_at": "2026-06-15T10:00:00Z"
}
```

**POST** `/api/admin/product-billing-rules` *(需 `product:create` 权限)*
```json
{
  "product_id": 1,
  "product_plan_id": 1,
  "usage_type": "api_call",
  "usage_unit": "次",
  "price_amount": "0.01",
  "currency": "CNY",
  "billing_mode": "per_unit",
  "free_quota": "100",
  "status": "active"
}
```
说明：`product_plan_id` 可空（空=商品级通用规则）；`price_amount` 必须 > 0；商品不存在返回 `404 40004`；必填项缺失返回 `40000`。返回 `data` 为规则详情（含 `id`）。

**PATCH** `/api/admin/product-billing-rules/{id}` *(需 `product:edit` 权限)*

body 字段均可选：`usage_type`、`usage_unit`、`price_amount`、`currency`、`billing_mode`、`free_quota`、`status`。规则不存在返回 `404 40004`。返回 `data`：`{ "updated": true }`。

---

## 七、订单模块（后端乙）

### 6.1 用户端

**GET** `/api/orders?page=1&page_size=10` *(需登录)*

支持过滤：`?status=paid&order_type=product`

响应 `data`（D-95 扁平分页）：
```json
{
  "items": [
    {
      "id": 101,
      "order_no": "ORD2026060600001",
      "order_type": "product",
      "product_id": 1,
      "product_plan_id": 1,
      "status": "paid",
      "amount": "10.000000",
      "currency": "CNY",
      "paid_at": "2026-06-06T10:00:00Z",
      "created_at": "2026-06-06T09:59:00Z"
    }
  ],
  "page": 1,
  "page_size": 10,
  "total": 3
}
```

`status`：`pending` / `paid` / `cancelled` / `failed`

`order_type`：`product`（购买订单）/ `recharge`（充值订单）

**GET** `/api/orders/{id}` *(需登录)*

---

**POST** `/api/orders/{id}/pay` *(需登录，仅本人订单)*

用钱包余额支付存量 `pending` 的**购买订单**（O3）。**仅 `order_type=product` 的 pending 订单可用钱包支付**；`recharge`（充值）订单不支持钱包支付（充值通过第三方 `pay_url` 完成），对其调用返回 `40000`「该订单不支持钱包支付」。

请求头：`Idempotency-Key` 必填（缺失返回 `code=40000`）。

请求 body：
```json
{ "pay_method": "wallet" }
```
> 目前仅支持 `wallet`，传其它值返回 `code=40000`。

响应 `data`：
```json
{
  "order_id": 101,
  "status": "paid",
  "wallet_transaction_id": 5001,
  "asset_id": 0
}
```
说明：
- `wallet_transaction_id`：本次扣费生成的钱包流水 ID（真实返回）。
- `asset_id`：开通由后端异步执行，支付响应阶段恒为 `0`；资产生效后请通过「我的资产」接口查询。
- 幂等：对已 `paid` 订单重复调用返回成功（`status=paid`），不重复扣费。
- 错误码：余额不足 `60001`；订单已支付 `60002`（请勿重复操作，D-007）；订单不存在/非本人 `404 40004`；订单状态不可支付（cancelled/failed 等）`40900`；非 product 订单 / 不支持的支付方式 `40000`。

---

**POST** `/api/orders/{id}/cancel` *(需登录，仅本人订单)*

取消存量 `pending` 订单（O4）。

请求 body（可选）：
```json
{ "reason": "用户主动取消" }
```

响应 `data`：
```json
{ "cancelled": true }
```
说明：
- 仅 `pending` 订单可取消；非 pending 返回 `40900`。
- `reason` 落地到订单 `remark` 字段（订单无独立 cancel_reason 列）。

---

### 6.2 管理端

**GET** `/api/admin/orders?page=1&page_size=10` *(需 `order:list` 权限)*

支持过滤：`?user_id=1&status=paid&order_type=product`

**GET** `/api/admin/orders/{id}` *(需 `order:list` 权限)*

---

## 八、钱包 & 支付模块（后端乙）

### 7.1 用户端

**GET** `/api/wallet` *(需登录)*

响应 `data`：
```json
{
  "wallet_id": 1,
  "user_id": 1,
  "balance_amount": "90.000000",
  "frozen_amount": "0.000000",
  "currency": "CNY"
}
```

> D-008：字段名 `id` 已改为 `wallet_id`（PR#135）。

**GET** `/api/wallet/transactions?page=1&page_size=10` *(需登录)*

响应 `data` 为 D-95 扁平分页（`items`/`page`/`page_size`/`total`），`items` 单条结构：
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
  "payment_method": "wechat",
  "return_url": "https://console.example.com/wallet"
}
```

`payment_method`：`wechat` / `alipay`；`return_url` 可选（仅用于前端展示跳转，不作为充值完成依据）。

响应 `data`：
```json
{
  "order_id": 201,
  "order_no": "RCG2026060600001",
  "amount": "100.00",
  "status": "pending",
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

**PATCH** `/api/admin/users/{id}/wallet/freeze` *(需 `wallet:manage` 权限)*
```json
{
  "action": "freeze",
  "amount": "50.00",
  "reason": "风控冻结"
}
```
`action`：`freeze` / `unfreeze`；`reason` 可选。
> 该接口为写操作，权限码由 `wallet:view` 收紧为 **`wallet:manage`**（最小权限原则，需 migration 000023 已执行）。

**GET** `/api/admin/payment-callbacks?page=1&page_size=10` *(需 `wallet:view` 权限)*

支持过滤：`?provider=wechat&status=processed`。响应 `data` 为 D-95 扁平分页（不返回明文 `notify_body`）。

---

### 7.4 消费记录（按量计费流水）

**GET** `/api/product-consumption-records?page=1&page_size=10` *(需登录，仅本人)*

支持过滤：`?product_id=1&usage_type=api_call&created_from=2026-06-01&created_to=2026-06-15`。
> 强制按当前登录用户过滤，query 传 `user_id` 对本接口无效，无法查询他人记录。

响应 `data` 为 D-95 扁平分页，`items` 单条结构：
```json
{
  "id": 1,
  "user_id": 1,
  "product_id": 1,
  "product_plan_id": 1,
  "instance_id": 0,
  "usage_type": "api_call",
  "usage_amount": "120",
  "usage_unit": "次",
  "amount": "0.200000",
  "event_id": "evt-uuid",
  "created_at": "2026-06-15T10:00:00Z"
}
```

**GET** `/api/admin/product-consumption-records?page=1&page_size=10` *(需 `wallet:view` 权限)*

管理端查询全量消费记录，过滤参数同上，额外支持 `?user_id=1`（不传=全量）。响应结构同上。

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

## 十、用户资产模块（后端丙）

> ✅ **落地状态**（2026-06-18 核对代码）：C-FIX-2a（资产 `action:cancel`）、C-FIX-4（管理端列表响应含 `page_size`）、C-FIX-6（用户端公告分页）已随 PR#151 合并 main 并上线，**前端可直接按本文最终形态对接，无需任何"待发版"兜底**。

### 10.1 我的资产列表

**GET** `/api/my/assets?status=active` *(需登录)*

响应 `data`：`{ "items": [资产对象] }`（用户端不分页，`status` 可选过滤）。资产对象：
```json
{
  "id": 1,
  "user_id": 1,
  "asset_type": "application",
  "product_id": 10,
  "product_plan_id": 5,
  "source_order_id": 100,
  "business_instance_id": null,
  "status": "active",
  "started_at": "2026-06-17T10:00:00Z",
  "expires_at": "2026-12-17T10:00:00Z",
  "created_at": "2026-06-17T10:00:00Z"
}
```
> `expires_at` 为 `null` 表示永久资产；`status`：`active`/`suspended`(冻结)/`expired`(到期)/`cancelled`(取消)。

### 10.2 资产详情

**GET** `/api/my/assets/{id}` *(需登录，非本人返回 403)*

响应 `data` 为单个资产对象（结构同上）。

### 10.3 我的权益额度

**GET** `/api/my/entitlements` *(需登录)*

响应 `data`：`{ "items": [权益对象] }`。权益对象：
```json
{
  "id": 1,
  "user_id": 1,
  "asset_id": 1,
  "entitlement_type": "api_calls",
  "product_id": 10,
  "quota_total": "100000000",
  "quota_used": "0",
  "quota_unit": "次",
  "status": "active",
  "expires_at": "2026-12-17T10:00:00Z"
}
```
> `quota_total` 为 `null` 表示不限量；剩余 = `quota_total - quota_used`。买断配额消耗（`quota_used` 递增）为 LATER 功能，本阶段恒为 `"0"`。

### 10.4 管理端资产列表

**GET** `/api/admin/assets?user_id=&status=&page=1&page_size=20` *(需 `asset:view`)*

响应 `data` 为 D-95 扁平分页 `{ items, page, page_size, total }`，`items` 单条结构同 10.1。

### 10.5 指定用户的资产

**GET** `/api/admin/users/{id}/assets` *(需 `asset:view`)*

响应 `data`：`{ "items": [资产对象] }`（不分页）。

### 10.6 冻结 / 解冻 / 取消资产

**PATCH** `/api/admin/assets/{id}` *(需 `asset:manage`)*
```json
{ "action": "freeze", "remark": "违规冻结" }
```
- `action`：`freeze`（active→suspended）/ `unfreeze`（suspended→active）/ `cancel`（active|suspended→cancelled，同步取消关联权益，建议带 `remark` 作为取消原因）
- 成功返回 `{ "message": "操作成功" }`；状态机越界返回 400。

---

## 十一、会员模块（后端丙）

> ✅ C-FIX-1（已上线）：会员**续期**——同一用户重复开通同等级时，`expires_at` 在原有效期上叠加延长（而非新增一条记录）。前端「会员中心」续费后应重新拉取 `/api/my/membership` 展示新到期时间。
> 会员**购买**统一走商品流程（`product_type=membership` 商品 → 下单 → 支付 → 开通），**无独立 purchase 接口**；管理员可经 §11.6 手动开通/调整。

### 11.1 会员等级列表（公开）

**GET** `/api/memberships` *(公开，无需登录)*

响应 `data`：`{ "items": [等级对象] }`（仅 `status=active`）。等级对象：
```json
{
  "id": 1,
  "level_code": "vip",
  "name": "黄金会员",
  "description": "尊享折扣",
  "sort_order": 1,
  "status": "active",
  "created_at": "2026-06-17T10:00:00Z",
  "updated_at": "2026-06-17T10:00:00Z"
}
```

> ℹ️ **本接口仅返回等级本身，不含权益（benefits）明细**；如需按等级展示/对比权益，请用下方 `§11.1b GET /api/memberships/{id}/benefits`（公开端点，仅返回 `status=active` 权益）。管理端权益接口 `§11.4 GET /api/admin/membership-benefits` 为 `membership:view` 权限，用户端不可用。

### 11.1b 会员等级权益（公开）

**GET** `/api/memberships/{id}/benefits` *(公开，无需登录)*

- `{id}` 为**会员等级 ID**。
- 响应 `data`：`{ "items": [权益对象] }`，**仅含 `status=active` 的权益**。权益对象结构同 §11.4：
  ```json
  {
    "id": 1,
    "level_id": 1,
    "benefit_type": "discount",
    "benefit_value": "{\"rate\":0.8}",
    "status": "active",
    "created_at": "2026-06-17T10:00:00Z",
    "updated_at": "2026-06-17T10:00:00Z"
  }
  ```
  > `benefit_value` 为 JSON 字符串，前端读取时需 `JSON.parse` 并做解析失败兜底。
- 等级不存在 **或** 未上架（`status != active`）→ `404 / code 40400`，message「会员等级不存在」（避免泄露未上架等级）。
- 等级存在且 active 但无任何 active 权益时返回 `{ "items": [] }`。

> 用户端「会员中心」可对每个等级调用本端点拉取权益用于展示/对比；无需登录，可与 `§11.1` 等级列表配合使用。

### 11.2 我的会员

**GET** `/api/my/membership` *(需登录)*

响应统一为 `data.membership`（有会员/无会员两种情形结构对称，前端无需分支判断）：
- 有有效会员时，`data.membership` 为会员对象：
```json
{ "membership": { "id": 1, "user_id": 1, "level_id": 1, "level_code": "vip", "level_name": "黄金会员", "asset_id": 2, "status": "active", "started_at": "2026-06-17T10:00:00Z", "expires_at": "2026-12-17T10:00:00Z" } }
```
- 无有效会员时，`data.membership` 为 `null`：`{ "membership": null }`。

> ✅ **会员对象已内联 `level_code`/`level_name`**（在保留 `level_id` 的基础上新增，纯增量）。前端可直接展示等级名，无需再按 `level_id` 映射等级列表（等级查询异常的极端情形下两字段可能为空字符串，前端可兜底回退到 §11.1 映射）。
> 📌 **`asset_id`**：关联的会员资产 ID；无关联资产时为 `null`（key 恒在，不省略），前端无需做存在性判断。
> ⚠️ **多等级并存时只返回一条**：同一用户可同时持有不同等级的多条有效会员（管理员手动叠加开通），本接口按「永久会员优先，其次到期时间最晚」只返回**单条最优**会员。如需查看用户全部有效会员，用管理端 `§11.5 GET /api/admin/user-memberships?user_id=`。

### 11.3 管理端会员等级

- **GET** `/api/admin/membership-levels` *(需 `membership:view`)* → `{ "items": [等级对象] }`（含 inactive）
- **POST** `/api/admin/membership-levels` *(需 `membership:manage`)*
  ```json
  { "level_code": "vip", "name": "黄金会员", "description": "尊享折扣", "sort_order": 1 }
  ```
- **PATCH** `/api/admin/membership-levels/{id}` *(需 `membership:manage`)* → 可改 `name`/`description`/`sort_order`/`status`

### 11.4 管理端会员权益

- **GET** `/api/admin/membership-benefits?level_id=1` *(需 `membership:view`)* → `{ "items": [权益对象] }`
- **POST** `/api/admin/membership-benefits` *(需 `membership:manage`)*
  ```json
  { "level_id": 1, "benefit_type": "discount", "benefit_value": "{\"rate\":0.8}" }
  ```
  > `benefit_value` 为 JSON 字符串，业务自定义结构。
- **PATCH** `/api/admin/membership-benefits/{id}` *(需 `membership:manage`)* → 可改 `benefit_type`/`benefit_value`/`status`

### 11.5 管理端用户会员列表

**GET** `/api/admin/user-memberships?user_id=&page=1&page_size=20` *(需 `membership:view`)*

响应 `data` 为扁平分页 `{ items, page, page_size, total }`（`page_size` 最大 100）。`items` 单条在 §11.2 会员字段基础上额外含 `created_at`/`updated_at`，并**已内联 `level_code`/`level_name`**：
```json
{ "id": 1, "user_id": 1, "level_id": 1, "level_code": "vip", "level_name": "黄金会员", "asset_id": 2, "status": "active", "started_at": "2026-06-17T10:00:00Z", "expires_at": "2026-12-17T10:00:00Z", "created_at": "2026-06-17T10:00:00Z", "updated_at": "2026-06-17T10:00:00Z" }
```

> ✅ `items` 已内联 `level_code`/`level_name`，前端无需再按 `level_id` 映射 §11.3 等级列表即可展示等级名（服务端批量加载等级，无 N+1）。
> 📌 **`asset_id`**：关联的会员资产 ID；无关联资产时为 `null`（key 恒在，不省略），前端无需做存在性判断。
> ⚠️ **仍不含用户身份（用户名/邮箱），仅 `user_id`**（属后端甲用户域，本轮未做）。展示用户信息须配合后端甲用户接口（如按 `user_id` 查用户详情）。**建议本列表主要按 `user_id` 过滤使用**（从用户管理页进入），全量浏览时用户列仅能显示数字 `user_id`。

### 11.6 管理端手动开通 / 调整用户会员

- **POST** `/api/admin/user-memberships` *(需 `membership:manage`)* —— 手动开通 / 续期会员
  ```json
  { "user_id": 1, "level_id": 1, "duration_days": 30 }
  ```
  > `duration_days` 为 `null` 表示永久会员；对已有同等级有效会员重复开通时按 C-FIX-1 在原到期时间上叠加续期。成功返回 `{ "message": "开通成功" }`。
- **PATCH** `/api/admin/user-memberships/{id}` *(需 `membership:manage`)* —— 取消会员 / 覆盖到期时间
  ```json
  { "action": "cancel" }
  ```
  或
  ```json
  { "expires_at": "2026-12-31T00:00:00Z" }
  ```
  > `action: "cancel"` 将会员 `status` 置为 `cancelled`；`expires_at` 直接覆盖到期时间（两者可单独使用）。

---

## 十二、内容模块（公告 / 帮助，后端丙）

> ✅ C-FIX-6（已上线）：用户端公告列表已支持分页参数 `page`/`page_size`（默认 20，最大 50），响应为完整扁平分页信封。

### 12.1 公告列表（用户端）

**GET** `/api/announcements?page=1&page_size=20` *(需登录，按可见范围过滤)*

响应 `data` 为扁平分页 `{ items, page, page_size, total }`。公告对象：
```json
{
  "id": 1,
  "title": "系统维护通知",
  "content": "...",
  "visible_scope": "all",
  "target_roles_json": null,
  "status": "published",
  "start_at": "2026-06-17T00:00:00Z",
  "end_at": null,
  "sort_order": 0,
  "created_by": 1,
  "created_at": "2026-06-17T10:00:00Z"
}
```
> `visible_scope`：`all`（所有登录用户）/`roles`（命中 `target_roles_json` 任一角色）/`members`（有效会员）/`admins`（用户端不可见）。仅返回 `status=published` 且当前在 `start_at`/`end_at` 时间窗内的公告。
> ⚠️ `created_by` 为创建公告的管理员用户 ID，**前端勿在用户端展示**（仅内部字段，避免暴露管理员身份）。

### 12.2 帮助文档（公开）

- **GET** `/api/help/categories` *(公开)* → `{ "items": [{id,name,description,sort_order,status}] }`（仅 active）
- **GET** `/api/help/articles?category_id=1` *(公开)* → `{ "items": [文章对象] }`（仅 published，`category_id` 可选）
- **GET** `/api/help/articles/{id}` *(公开)* → 单篇文章（仅 published，否则 404/40400）；**`data` 直接为文章对象本身，非 `{item}`/`{article}` 包裹**，前端直接取 `data.title` 等字段。

文章对象（即 `/api/help/articles/{id}` 的 `data`，也是列表 `items` 单条）：
```json
{ "id": 1, "category_id": 1, "title": "如何充值", "content": "...", "sort_order": 0, "status": "published", "created_by": 1, "created_at": "2026-06-17T10:00:00Z" }
```
> 帮助分类 `§12.2 /api/help/categories`、文章列表 `/api/help/articles` 均为不分页 `{ items: [...] }`。
> ⚠️ `created_by` 为创建文章的管理员用户 ID，**前端勿在用户端展示**（仅内部字段）。

### 12.3 管理端公告

> 管理端列表 `page_size` 上限 100（用户端公告 `§12.1` 上限 50）；超限按上限钳制。
- **GET** `/api/admin/announcements?page=1&page_size=20` *(需 `content:manage`)* → 扁平分页 `{ items, page, page_size, total }`
- **POST** `/api/admin/announcements` *(需 `content:manage`)*
  ```json
  { "title": "标题", "content": "正文", "visible_scope": "roles", "target_roles_json": "[\"merchant\",\"vip\"]", "start_at": "2026-06-17T00:00:00Z", "end_at": null, "sort_order": 0 }
  ```
  > 创建后默认 `status=draft`，需 PATCH 改为 `published` 才对用户端可见。
- **PATCH** `/api/admin/announcements/{id}` *(需 `content:manage`)* → 可改 title/content/visible_scope/target_roles_json/`status`(published/offline/draft)/start_at/end_at/sort_order

### 12.4 管理端帮助分类 / 文章

- 分类：**GET/POST** `/api/admin/help/categories`、**PATCH** `/api/admin/help/categories/{id}` *(需 `content:manage`)*
  - **GET 响应不分页**：`data` 为 `{ items: [分类对象] }`（无 page/page_size/total，前端勿建分页 UI）
  - POST body：`{ "name": "充值相关", "description": "...", "sort_order": 0 }`
- 文章：**GET** `/api/admin/help/articles?category_id=&page=1&page_size=20`、**POST** `/api/admin/help/articles`、**PATCH** `/api/admin/help/articles/{id}` *(需 `content:manage`)*
  - POST body：`{ "category_id": 1, "title": "如何充值", "content": "...", "sort_order": 0 }`（默认 draft）
  - 列表为扁平分页 `{ items, page, page_size, total }`

---

## 十三、应用模块（后端丙）

> 应用 `applications` 仅存业务详情（图标/描述/回调/适配器配置）；套餐/价格/角色权限走商品模块（§六），上架为可购买商品需在商品管理新建 `product_type=application` 且 `business_ref_id` 指向应用 ID。

### 13.1 应用详情（用户端）

**GET** `/api/marketplace/apps/{id}` *(需登录；🔜 C-OPT-3 拟放开为公开只读)*

响应 `data`（**用户向白名单**，固定为以下字段）：
```json
{ "id": 1, "code": "netdisk-basic", "name": "基础网盘", "type": "netdisk", "description": "...", "icon_url": "https://...", "status": "active", "created_at": "..." }
```

> 白名单字段：`{id, code, name, type, description, icon_url, status, created_at}`。
> **不含 `callback_url` / `adapter_config_json`（仅管理端 AP2/AP3 `GET /api/admin/apps`、`GET /api/admin/apps/{id}` 返回），亦不含 `updated_at`。** 这两个字段属内部回调地址与非交易配置（可能含集成参数/内网地址/密钥），用户端禁止下发。

### 13.2 管理端应用 CRUD

- **GET** `/api/admin/apps?status=&type=&page=1&page_size=20` *(需 `app:manage`)* → 扁平分页 `{ items, page, page_size, total }`
- **GET** `/api/admin/apps/{id}` *(需 `app:manage`)* → 单个应用对象
- **POST** `/api/admin/apps` *(需 `app:manage`)*
  ```json
  { "code": "netdisk-basic", "name": "基础网盘", "type": "netdisk", "description": "...", "icon_url": "https://...", "callback_url": "https://...", "adapter_config_json": null }
  ```
- **PATCH** `/api/admin/apps/{id}` *(需 `app:manage`)* → 可改 name/type/description/icon_url/callback_url/adapter_config_json/`status`(draft/active/inactive/archived)

### 13.3 管理端适配器

- **GET** `/api/admin/app-adapters?status=&page=1&page_size=20` *(需 `app:manage`)* → **扁平分页** `{ items, page, page_size, total }`（`page_size` 默认 20、上限 100，可选 `status` 过滤；前端按分页处理，勿当作不分页 `{items}`）
- **POST** `/api/admin/app-adapters` *(需 `app:manage`)*
  ```json
  { "app_code": "netdisk-basic", "app_name": "基础网盘", "app_type": "netdisk", "adapter_type": "internal", "service_name": "netdisk-svc", "callback_url": "https://...", "supported_actions_json": "[\"provision\",\"renew\",\"cancel\"]", "usage_event_types_json": "[\"storage_gb\"]" }
  ```
- **PATCH** `/api/admin/app-adapters/{id}` *(需 `app:manage`)* → 可改各字段及 `status`(active/inactive)

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
| `wallet:view` | 查看钱包/流水/回调/消费记录（只读） |
| `wallet:manage` | 钱包写操作（冻结/解冻），migration 000023 起生效 |
| `asset:view` | 查看用户资产（只读，后端丙） |
| `asset:manage` | 资产写操作（冻结/解冻/取消，后端丙） |
| `membership:view` | 查看会员等级/权益/用户会员（只读，后端丙） |
| `membership:manage` | 会员等级/权益写操作（后端丙） |
| `content:manage` | 公告/帮助文档管理（后端丙） |
| `app:manage` | 应用与适配器管理（后端丙） |

### 枚举值汇总

| 字段 | 可选值 |
|------|--------|
| `real_name_status` | `unverified` / `pending` / `verified` / `rejected` |
| `order.status` | `pending` / `paid` / `cancelled` / `failed` |
| `order.order_type` | `product` / `recharge` |
| `product.status` | `draft` / `active` / `inactive` |
| `wallet_transaction.type` | `recharge` / `consume` / `refund` / `freeze` / `unfreeze` |
| `wallet_transaction.direction` | `in` / `out` |
| `payment_callback.status` | `received` / `processed` / `ignored` |
| `billing_type` | `one_time` / `monthly` / `yearly` / `usage` |
| `user_asset.status` | `active` / `suspended` / `expired` / `cancelled` |
| `entitlement.status` | `active` / `suspended` / `expired` / `cancelled` |
| `user_membership.status` | `active` / `expired` / `cancelled` |
| `membership_level.status` | `active` / `inactive` |
| `announcement.visible_scope` | `all` / `roles` / `members` / `admins` |
| `announcement.status` | `draft` / `published` / `offline` |
| `help_article.status` / `help_category.status` | `draft` / `published`（分类：`active` / `inactive`） |
| `application.status` | `draft` / `active` / `inactive` / `archived` |
| `app_adapter.status` | `active` / `inactive` |
| `provider` | `wechat` / `alipay` |
