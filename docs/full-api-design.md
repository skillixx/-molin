# 完整接口设计

## 1. 通用约定

### 1.1 基础响应结构

所有接口统一返回：

```json
{
  "code": 0,
  "message": "ok",
  "data": {},
  "request_id": "req_xxx"
}
```

### 1.2 分页返回结构

列表接口的 `data` 使用：

```json
{
  "items": [],
  "page": 1,
  "page_size": 20,
  "total": 100
}
```

### 1.3 通用请求头

```text
Authorization: Bearer <access_token>
X-Request-ID: req_xxx
Idempotency-Key: idem_xxx
```

说明：

- `Authorization`：需要登录的接口必须传。
- `X-Request-ID`：可选，不传则后端生成。
- `Idempotency-Key`：购买、支付、充值、按量计费、资产开通等关键写操作必须传。

### 1.4 限流策略

所有请求经过以下中间件链：

```text
RequestID -> Logger -> Recovery -> RateLimit -> Auth（非公开接口）-> Permission（需权限接口）
```

限流规则：

| 接口分类 | 限制 | 说明 |
|---|---|---|
| 全局 | 1000 req/s / IP | 超出返回 429 |
| 注册 / 登录 / 验证码 | 10 req/min / IP | 防暴力破解 |
| 充值创建订单 | 20 req/min / 用户 | 防重复充值 |
| 支付回调 | 不限流 | 第三方平台回调，需签名校验 |
| Token 网关调用 | 按 token_quota_accounts.monthly_limit_tokens | 用户级别月度配额 |

限流响应头：

```text
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 950
X-RateLimit-Reset: 1748000000
```

超限响应：

```json
{
  "code": 42900,
  "message": "rate limit exceeded",
  "data": null,
  "request_id": "req_xxx"
}
```

### 1.4 通用错误码

```text
0      成功
40000  请求参数错误
40001  未登录
40003  无权限
40400  资源不存在
40404  账号未注册（登录/发验证码接口对未注册手机号或邮箱返回此码，提示先注册）
40900  数据冲突
50000  系统内部错误
50200  上游模型服务失败（HTTP 502，token_gateway 透传上游失败）
50300  渠道不可用（HTTP 503，token_gateway 未配置可用渠道 / 渠道停用）
50301  系统繁忙/可重试（HTTP 503，token_gateway 乐观锁冲突重试耗尽，可重试；D-M2-02，区别于 60001 余额不足）
60001  余额不足
60002  重复支付
60003  商品状态不可用
60004  资产未生效
60005  权益额度不足
70001  未完成实名制认证
70002  实名认证审核中
70003  实名认证被拒绝
```

## 2. 认证和实名接口

### 2.1 发送邮箱验证码

```text
POST /api/auth/verification-codes/email
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| email | string | 是 | 邮箱地址 |
| scene | string | 是 | 场景（**D-96：公开端点仅接受** register、login、reset_password）；bind_email/bind_phone/admin_verify 已移除，传入返回 400/40000 |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| sent | boolean | 是否发送成功 |
| expires_in | integer | 有效秒数 |

### 2.2 发送短信验证码

```text
POST /api/auth/verification-codes/phone
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| phone | string | 是 | 手机号 |
| scene | string | 是 | 场景（**D-96：公开端点仅接受** register、login、reset_password）；bind_email/bind_phone/admin_verify 已移除，传入返回 400/40000 |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| sent | boolean | 是否发送成功 |
| expires_in | integer | 有效秒数 |

> ⚠️ **D-96（2026-06-15）**：换绑/管理员认证发码已迁移到专属认证态端点，公开端点不再接受对应 scene：
> - 换绑手机/邮箱：`POST /api/me/verification-codes/{phone,email}`（需登录）
> - 管理员双重认证：`POST /api/admin/auth/verification-codes/{phone,email}`（需 user:manage）

### 2.3 统一注册（手机+邮箱+用户名，唯一注册入口）

```text
POST /api/auth/register
```

> 说明：本接口是系统**唯一**的注册入口。原先的 `POST /api/auth/register/email`、
> `POST /api/auth/register/phone` 两个旧接口已下线（产品确认前端尚未对接，
> 无兼容性负担），客户端一律使用本接口完成注册。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| username | string | 否 | 用户名（2-32位字母/数字/下划线，全局唯一） |
| phone | string | 是 | 手机号 |
| email | string | 是 | 邮箱地址 |
| password | string | 是 | 密码（6-72 位） |
| phone_code | string | 是 | 手机验证码（scene=register） |
| email_code | string | 是 | 邮箱验证码（scene=register） |
| invite_code | string | 否 | 组邀请码。传有效码 → 落入对应分组并赋邀请码配置的组内角色；为空/无效/过期/已满 → 落入默认组 |

返回 data：同登录接口（access_token / refresh_token / expires_in / user）。

注册成功后 phone_verified 和 email_verified 自动置为 true。

**注册落组**：注册成功后系统按以下策略将新用户落入用户分组（落组逻辑在 `iam.GroupService.AssignOnRegister`）：

| 场景 | 落组结果 |
|---|---|
| 传有效 `invite_code` | 落入邀请码对应分组，组内角色 = 邀请码的 `default_group_role` |
| 传无效/过期/已满 `invite_code` | **降级落入默认组**（方案 A，不报错，注册仍成功） |
| 不传 `invite_code` | 落入默认组（`user_groups.is_default=true`） |
| 系统未配置默认组 | 注册成功，但不落任何组 |

落组为 best-effort：落组失败不回滚注册，仅记日志（与创建后台用户分配角色的约定一致）。
方案 A 适用边界：当前邀请码仅承载「分组归属 + 组内角色」，不承载准入门槛语义；若将来升级为强准入门槛需重新评估降级策略。

### 2.4 邮箱登录

```text
POST /api/auth/login/email
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| email | string | 是 | 邮箱地址 |
| password | string | 是 | 密码 |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| access_token | string | 访问令牌 |
| refresh_token | string | 刷新令牌 |
| expires_in | integer | access_token 有效秒数 |
| user | object | 用户摘要 |

user 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 用户 ID |
| email | string | 邮箱 |
| phone | string | 手机号 |
| real_name_status | string | 实名状态 |
| status | string | 用户状态 |

错误：
- 邮箱未注册 → `404 40404`「邮箱未注册，请先注册」
- 账号已被禁用 → `403 40003`
- 密码错误 → `401 40001`「邮箱或密码错误」

### 2.5 手机号登录

```text
POST /api/auth/login/phone
```

> 手机号登录为**验证码登录**（非密码登录）：登录前需先调用 `POST /api/auth/verification-codes/phone`（`scene=login`）获取验证码，再携带该验证码调用本接口。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| phone | string | 是 | 手机号 |
| code | string | 是 | 登录验证码（`scene=login`，先调用发送验证码接口获取） |

返回 data 同邮箱登录。

错误：
- 验证码错误或已过期 → `400 40000`
- 手机号未注册 → `404 40404`「手机号未注册，请先注册」
- 账号已被禁用 → `403 40003`

### 2.6 退出登录

```text
POST /api/auth/logout
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| refresh_token | string | 否 | 需要失效的刷新令牌 |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| logged_out | boolean | 是否退出成功 |

> **Token 即时吊销**：退出成功后，本次请求 `Authorization` 头携带的当前 Access Token 会被加入 Redis 吊销黑名单（TTL=该 token 剩余有效期），在自然过期前立即失效；之后用该 Token 访问任意需鉴权接口均返回 `401 40001`「token 已失效，请重新登录」。仅吊销当前这一个 Access Token，不影响同账号其他会话/设备。

### 2.7 刷新令牌

```text
POST /api/auth/refresh
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| refresh_token | string | 是 | 刷新令牌 |

返回 data 同登录接口。

### 2.8 当前用户

```text
GET /api/me
```

请求参数：无。

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 用户 ID |
| username | string | 用户名（可为 null） |
| email | string | 邮箱（脱敏：@前保留2位，其余替换为 `***`） |
| phone | string | 手机号（脱敏：前3后4，中间替换为 `****`） |
| email_verified | boolean | 邮箱是否已验证 |
| phone_verified | boolean | 手机号是否已验证 |
| real_name_status | string | 实名状态（unverified / pending / verified / rejected） |
| status | string | 账号状态（active / disabled） |
| admin_phone_verified | boolean | 管理员手机认证是否有效（超过有效期自动变 false） |
| admin_email_verified | boolean | 管理员邮箱认证是否有效（超过有效期自动变 false） |
| created_at | string | 注册时间（ISO 8601） |
| last_login_at | string | 最后登录时间（ISO 8601，可为 null） |

### 2.9 修改当前用户资料

```text
PATCH /api/me/profile
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| nickname | string | 否 | 昵称 |
| avatar_url | string | 否 | 头像地址 |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| updated | boolean | 是否更新成功 |

### 2.10 修改密码

```text
PATCH /api/me/password
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| old_password | string | 是 | 旧密码 |
| new_password | string | 是 | 新密码（6-72 位） |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| updated | boolean | 是否更新成功 |

### 2.11 提交实名认证

```text
POST /api/identity/verifications
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| real_name | string | 是 | 真实姓名 |
| id_card_no | string | 是 | 身份证号，后端只保存 hash 和 masked |
| verification_type | string | 是 | 认证类型，默认 id_card |
| attachments | array\<string\> | 否 | 认证附件 URL 数组，每项须以 `https://` 开头，最多 5 个 |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 新建实名认证记录 ID |
| status | string | 审核状态：pending |

### 2.12 查询最新实名认证

```text
GET /api/identity/verifications/latest
```

请求参数：无。

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 实名认证记录 ID |
| status | string | 审核状态 |
| reject_reason | string | 拒绝原因 |
| submitted_at | string | 提交时间 |
| verified_at | string | 审核通过时间 |

### 2.13 OTP 密码重置

```text
POST /api/auth/password/reset
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| target | string | 是 | 手机号或邮箱地址 |
| target_type | string | 是 | 类型：phone 或 email |
| code | string | 是 | 验证码（scene=reset_password） |
| new_password | string | 是 | 新密码（6-72 位） |

返回 data：`null`（HTTP 200 表示成功）。

重置成功后该用户所有 Refresh Token 自动吊销，强制重新登录。

### 2.14 管理员手机号双重认证

```text
POST /api/admin/auth/verify-phone
```

需要：Bearer Token + `user:manage` 权限（仅限管理员账号）。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 是 | 手机验证码（scene=admin_verify） |

返回 data：`null`（HTTP 200 表示认证成功，记录 admin_phone_verified_at）。
普通用户调用返回 403（错误码 40003）。

D-96：获取 `scene=admin_verify` 验证码请调用 `POST /api/admin/auth/verification-codes/phone`（需 Bearer Token + `user:manage` 权限，无请求体），
验证码发送至当前登录管理员自己绑定的手机号；若该账号未绑定手机号则返回 400（错误码 40000）。
返回 data 非生产环境含 `code` 字段（明文验证码，便于调试），生产环境为 `{}`。

### 2.15 管理员邮箱双重认证

```text
POST /api/admin/auth/verify-email
```

需要：Bearer Token + `user:manage` 权限（仅限管理员账号）。前置条件：手机号认证必须在有效期内。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 是 | 邮箱验证码（scene=admin_verify） |

返回 data：`null`（HTTP 200 表示认证成功，记录 admin_email_verified_at）。

认证有效期由环境变量 `ADMIN_VERIFY_EXPIRE_HOURS` 控制（默认 24 小时），超期后需重新认证。

D-96：获取 `scene=admin_verify` 验证码请调用 `POST /api/admin/auth/verification-codes/email`（需 Bearer Token + `user:manage` 权限，无请求体），
验证码发送至当前登录管理员自己绑定的邮箱；若该账号未绑定邮箱则返回 400（错误码 40000）。
返回 data 非生产环境含 `code` 字段（明文验证码，便于调试），生产环境为 `{}`。

### 2.16 修改用户名

```text
PATCH /api/me/username
```

需要：Bearer Token。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| username | string | 是 | 新用户名（2-32位字母/数字/下划线，全局唯一） |

返回 data：`null`（HTTP 200 表示修改成功）。

### 2.17 修改手机号

```text
PATCH /api/me/phone
```

需要：Bearer Token。先向新手机号发送验证码（scene=bind_phone），再提交本接口。

D-96：发送验证码请调用 `POST /api/me/verification-codes/phone`（需 Bearer Token），body 为 `{"phone": "<新手机号>"}`；
若新手机号已被其他账号注册则返回 409（错误码 40900）。返回 data 非生产环境含 `code` 字段（明文验证码，便于调试），生产环境为 `{}`。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| phone | string | 是 | 新手机号 |
| code | string | 是 | 新手机号收到的验证码（scene=bind_phone） |

返回 data：`null`（HTTP 200 表示修改成功，phone_verified 自动置为 true）。

### 2.18 修改邮箱

```text
PATCH /api/me/email
```

需要：Bearer Token。先向新邮箱发送验证码（scene=bind_email），再提交本接口。

D-96：发送验证码请调用 `POST /api/me/verification-codes/email`（需 Bearer Token），body 为 `{"email": "<新邮箱>"}`；
若新邮箱已被其他账号注册则返回 409（错误码 40900）。返回 data 非生产环境含 `code` 字段（明文验证码，便于调试），生产环境为 `{}`。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| email | string | 是 | 新邮箱地址 |
| code | string | 是 | 新邮箱收到的验证码（scene=bind_email） |

返回 data：`null`（HTTP 200 表示修改成功，email_verified 自动置为 true）。

### 2.19 当前用户最终生效权限码

```text
GET /api/me/permissions
```

需要：Bearer Token（`RequireAuth`），无需额外权限码。

请求参数：无。

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| permissions | array\<string\> | 当前登录用户最终生效的权限码集合 |

计算逻辑：角色权限 ∪ 组权限，再叠加 `user_permission_overrides` 的 allow/deny 调整
（deny 从集合中移除对应权限码，allow 追加进集合）。供前端做按钮级权限控制（菜单/按钮显隐），
避免只能依赖接口返回 403 才能感知无权限。

## 3. 管理后台账号、实名、权限接口

### 3.1 用户列表

```text
GET /api/admin/users
```

Query 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| keyword | string | 否 | 模糊搜索，匹配邮箱（脱敏前缀）或手机号（脱敏前缀） |
| status | string | 否 | 用户状态：active / disabled |
| real_name_status | string | 否 | 实名状态：unverified / pending / verified / rejected |
| role_code | string | 否 | 角色 code |
| page | integer | 否 | 页码，默认 1 |
| page_size | integer | 否 | 每页数量，默认 20 |

返回 data.items 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 用户 ID |
| email | string | 邮箱（脱敏） |
| phone | string | 手机号（脱敏） |
| real_name_status | string | 实名状态 |
| status | string | 用户状态 |
| roles | array | 角色列表（每项含 id、code、name） |
| created_at | string | 创建时间（ISO 8601） |

### 3.2 用户详情

```text
GET /api/admin/users/:id
```

Path 参数：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 用户 ID |

返回 data 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 用户 ID |
| email | string | 邮箱（脱敏） |
| phone | string | 手机号（脱敏） |
| status | string | 用户状态 |
| real_name_status | string | 实名状态 |
| roles | array | 角色列表（每项含 id、code、name） |
| permission_overrides | array | 动态权限覆盖列表 |
| wallet_summary | object | 钱包摘要（balance、frozen） |
| asset_summary | object | 资产摘要（total_count） |
| created_at | string | 注册时间（ISO 8601） |

### 3.3 创建后台用户

```text
POST /api/admin/users
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| email | string | 否 | 邮箱 |
| phone | string | 否 | 手机号 |
| password | string | 是 | 初始密码（6-72 位） |
| role_ids | array | 否 | 角色 ID 列表 |
| status | string | 否 | 用户状态 |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| user_id | integer | 用户 ID |

### 3.4 修改用户

```text
PATCH /api/admin/users/:id
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| email | string | 否 | 邮箱 |
| phone | string | 否 | 手机号 |
| status | string | 否 | 用户状态 |

返回 data：`updated`。

### 3.5 修改用户状态

```text
PATCH /api/admin/users/:id/status
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| status | string | 是 | active 或 disabled |
| reason | string | 否 | 操作原因 |

返回 data：`updated`。

### 3.6 用户角色

```text
GET   /api/admin/users/:id/roles
PATCH /api/admin/users/:id/roles
```

PATCH Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| role_ids | array | 是 | 角色 ID 列表 |
| reason | string | 否 | 调整原因 |

GET 返回 data：角色列表。

PATCH 返回 data：`updated`。

### 3.7 用户动态权限

```text
GET   /api/admin/users/:id/permission-overrides
PATCH /api/admin/users/:id/permission-overrides
```

GET Query 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| effect | string | 否 | 过滤 allow 或 deny，不传则返回全部 |
| permission_code | string | 否 | 按权限 code 精确过滤 |
| page | integer | 否 | 页码，默认 1 |
| page_size | integer | 否 | 每页数量，默认 20 |

GET 返回 data.list 字段（snake_case）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 覆盖记录 ID |
| user_id | integer | 用户 ID |
| permission_id | integer | 权限 ID |
| permission_code | string | 权限 code |
| effect | string | allow 或 deny |
| reason | string | 原因 |
| expires_at | string | 过期时间（ISO 8601，无过期为 null） |
| created_at | string | 创建时间（ISO 8601） |

PATCH Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| items | array | 是 | 权限覆盖列表 |

items 字段：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| permission_id | integer | 是 | 权限 ID |
| effect | string | 是 | allow 或 deny（只接受小写） |
| reason | string | 否 | 原因 |
| expires_at | string | 否 | 过期时间（ISO 8601） |

PATCH 返回 data：`updated`。

### 3.8 用户登录日志

```text
GET /api/admin/users/:id/login-logs
```

Query 参数：page、page_size。

返回 data.items：登录时间、登录方式、账号、IP、User-Agent、状态。

### 3.9 用户实名信息

```text
GET /api/admin/users/:id/identity
```

返回 data：实名状态、最近一次实名记录、脱敏证件号、审核时间。

### 3.10 角色管理

```text
GET    /api/admin/roles
POST   /api/admin/roles
GET    /api/admin/roles/:id
PUT    /api/admin/roles/:id
DELETE /api/admin/roles/:id
```

GET /api/admin/roles Query 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| keyword | string | 否 | 模糊搜索，匹配角色 code 或 name |
| page | integer | 否 | 页码，默认 1 |
| page_size | integer | 否 | 每页数量，默认 20 |

POST Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 是 | 角色 code |
| name | string | 是 | 角色名称 |
| description | string | 否 | 描述 |

PUT Body 参数（仅 name/description 生效，code 不可修改）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 否 | 角色 code（传入会被忽略，不会更新） |
| name | string | 是 | 角色名称 |
| description | string | 否 | 描述 |

POST 返回 data：角色信息（`RoleResp{id, code, name, description}`）。

PUT 返回 data：`null`（更新成功）。

#### GET /api/admin/roles/:id

返回 data（`RoleResp`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 角色 ID |
| code | string | 角色 code |
| name | string | 角色名称 |
| description | string | 描述，可为 null |

错误码：

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| 40400 | 404 | 角色不存在 |

#### DELETE /api/admin/roles/:id

返回 data：`null`（删除成功）。

### 3.11 权限管理

```text
GET  /api/admin/permissions
POST /api/admin/permissions
```

GET /api/admin/permissions Query 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| keyword | string | 否 | 模糊搜索，匹配权限 code 或 name |
| page | integer | 否 | 页码，默认 1 |
| page_size | integer | 否 | 每页数量，默认 20 |

POST Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 是 | 权限 code，例如 product:create |
| name | string | 是 | 权限名称 |
| resource | string | 是 | 资源 |
| action | string | 是 | 动作 |

返回 data：权限信息。

### 3.12 配置角色权限

```text
GET   /api/admin/roles/:id/permissions
PATCH /api/admin/roles/:id/permissions
```

需要：登录 + `role:manage` 权限 + 管理员双重认证。

GET 返回 data（A-11）：

| 字段 | 类型 | 说明 |
|---|---|---|
| permissions | array\<string\> | 该角色当前拥有的权限码列表 |

GET 错误码：

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| 40400 | 404 | 角色不存在 |

> 用途：解决管理后台无法展示"该角色当前有哪些权限"、编辑权限时无法预填充当前值的问题。
> `PATCH /api/admin/roles/:id/permissions` 是全量替换写接口，必须先 GET 当前集合才能正确增删。

PATCH Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| permission_ids | array | 是 | 权限 ID 列表（全量替换） |

PATCH 返回 data：`updated`。

### 3.13 实名审核列表

```text
GET /api/admin/identity-verifications
```

Query 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| user_id | integer | 否 | 用户 ID |
| status | string | 否 | 审核状态：pending / verified / rejected；不传则返回全部 |
| real_name | string | 否 | 真实姓名 |
| page | integer | 否 | 页码 |
| page_size | integer | 否 | 每页数量 |

返回 data.items：实名记录列表。

### 3.14 实名审核详情

```text
GET /api/admin/identity-verifications/:id
```

返回 data 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 记录 ID |
| user_id | integer | 所属用户 ID |
| real_name | string | 真实姓名 |
| id_card_no_masked | string | 脱敏证件号（前6后4，中间 * 替代） |
| status | string | 审核状态：pending / verified / rejected |
| reject_reason | string | 拒绝原因（rejected 时有值） |
| submitted_at | string | 提交时间（ISO 8601） |
| reviewed_at | string | 审核操作时间（ISO 8601，待审为 null） |
| attachments | array\<string\> | 附件 URL 数组（https:// 开头） |

### 3.15 审核实名

```text
PATCH /api/admin/identity-verifications/:id/review
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| action | string | 是 | approve 或 reject |
| reject_reason | string | 否 | 拒绝原因，reject 时必填 |

返回 data：`reviewed`。

### 3.16 审计日志

```text
GET /api/admin/audit-logs
```

Query 参数：operator_id、module、action、created_from、created_to、page、page_size。

返回 data.items：审计日志列表。

### 3.17 用户分组管理

> 以下接口均需登录 + `group:manage` 权限 + 管理员双重认证。

#### 3.17.1 分组 CRUD

```text
GET    /api/admin/user-groups
POST   /api/admin/user-groups
GET    /api/admin/user-groups/:id
PUT    /api/admin/user-groups/:id
DELETE /api/admin/user-groups/:id
```

GET（列表）Query 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| type | string | 否 | 按分组类型过滤：region / org / custom |
| keyword | string | 否 | 模糊搜索，匹配分组 code 或 name |
| page | integer | 否 | 页码，默认 1 |
| page_size | integer | 否 | 每页数量，默认 20 |

GET（列表）返回 data.items（`GroupResp`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 分组 ID |
| code | string | 分组 code |
| name | string | 分组名称 |
| type | string | 分组类型：region / org / custom |
| is_default | boolean | 是否为默认分组（无邀请码注册时的兜底组，全局最多一个） |
| description | string | 描述，可为 null |
| created_at | string | 创建时间（ISO 8601） |

POST（创建）Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 是 | 分组 code |
| name | string | 是 | 分组名称 |
| type | string | 否 | 分组类型：region / org / custom，默认 custom |
| is_default | boolean | 否 | 是否设为默认分组 |
| description | string | 否 | 描述 |

POST 返回 data：分组信息（`GroupResp`），HTTP 201。

错误码：

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| 40000 | 400 | code 或 name 为空 |

GET /api/admin/user-groups/:id 返回 data：分组信息（`GroupResp`）；分组不存在返回 `404 40400「分组不存在」`。

PUT /api/admin/user-groups/:id Body 参数（仅以下字段可改，code 不可改）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| name | string | 是 | 分组名称 |
| type | string | 否 | 分组类型 |
| is_default | boolean | 否 | 是否为默认分组 |
| description | string | 否 | 描述 |

PUT 返回 data：`null`（更新成功）。

DELETE /api/admin/user-groups/:id 返回 data：`null`（删除成功）。

错误码：

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| 40901 | 409 | 分组内仍有成员，请先移除所有成员 |
| 40902 | 409 | 分组内仍有有效邀请码，请先禁用后再删除分组 |

#### 3.17.2 分组成员管理

```text
GET    /api/admin/user-groups/:id/members
POST   /api/admin/user-groups/:id/members
PATCH  /api/admin/user-groups/:id/members/:uid
DELETE /api/admin/user-groups/:id/members/:uid
```

GET（列表）Query 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| group_role | string | 否 | 按组内角色过滤：admin / member |
| page | integer | 否 | 页码，默认 1 |
| page_size | integer | 否 | 每页数量，默认 20 |

GET（列表）返回 data.items（`GroupMemberResp`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 成员关系记录 ID |
| user_id | integer | 用户 ID |
| group_id | integer | 分组 ID |
| group_role | string | 组内角色：admin（组管理员）/ member（普通组员） |
| created_at | string | 加入时间（ISO 8601） |

POST（加成员）Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| user_id | integer | 是 | 用户 ID |
| group_role | string | 否 | 组内角色：admin / member，默认 member |

POST 返回 data：`null`，HTTP 201。

错误码：

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| 40000 | 400 | user_id 为空 |
| 40900 | 409 | 用户已在该分组中 |

PATCH（修改成员组内角色）Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| group_role | string | 是 | 组内角色：admin / member |

PATCH 返回 data：`null`。

DELETE（移除成员）返回 data：`null`。

PATCH / DELETE 错误码：

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| 40400 | 404 | 用户不在该分组中 |

#### 3.17.3 查询用户所在分组

```text
GET /api/admin/users/:id/groups
```

返回 data（数组，`UserGroupsResp[]`，非分页）：

| 字段 | 类型 | 说明 |
|---|---|---|
| group_id | integer | 分组 ID |
| group_role | string | 该用户在此分组内的角色：admin / member |
| joined_at | string | 加入时间（ISO 8601） |

#### 3.17.4 分组权限码

> `GroupPermission` 存储的是权限码字符串（`permission_code`），不关联 `permissions.id`。

```text
GET    /api/admin/user-groups/:id/permissions
POST   /api/admin/user-groups/:id/permissions
DELETE /api/admin/user-groups/:id/permissions/:code
```

GET 返回 data（数组，`GroupPermissionResp[]`，非分页）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 记录 ID |
| group_id | integer | 分组 ID |
| permission_code | string | 权限码 |
| created_at | string | 添加时间（ISO 8601） |

POST Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| permission_code | string | 是 | 权限码 |

POST 返回 data：`null`，HTTP 201。

错误码：

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| 40000 | 400 | permission_code 为空 |
| 40900 | 409 | 该权限码已添加到此分组 |

DELETE `:code` 为权限码字符串本身（如 `app:use:xxx`），返回 data：`null`。

#### 3.17.4a 组角色（绑定全局角色）

> 组员经 `GetUserRoleIDs` 继承所在组绑定的全局角色，用于商品访问/定价（`product_role_access` / `product_prices` 的角色判定）。设计见 `docs/backend-a-group-roles-design.md`。
> **A 版边界**：组角色只影响商品访问/定价，**不进入权限码判定**（绑角色 ≠ 获得该角色的管理权限码）。绑定/解绑即时生效（无缓存延迟）。

```text
GET    /api/admin/user-groups/:id/roles
POST   /api/admin/user-groups/:id/roles
DELETE /api/admin/user-groups/:id/roles/:role_id
```

GET 返回 data（数组，`GroupRoleResp[]`，非分页）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 记录 ID |
| group_id | integer | 分组 ID |
| role_id | integer | 绑定的全局角色 ID |
| created_at | string | 绑定时间（ISO 8601） |

POST Body：`{ "role_id": <integer> }`，返回 data：`null`，HTTP 201。

错误码：

| 错误码 | HTTP | 说明 |
|---|---|---|
| 40000 | 400 | role_id 为空 / 角色不存在 / 绑定系统角色（如 admin）被拒 |
| 40400 | 404 | 分组不存在（POST）/ 该角色未绑定到此分组（DELETE） |
| 40900 | 409 | 该角色已绑定到此分组 |

> 约束：被任意分组绑定的角色不可删除，`DELETE /api/admin/roles/:id` 会返回「角色已绑定到分组，请先解绑」，需先解绑。

#### 3.17.5 邀请码

```text
GET   /api/admin/user-groups/:id/invite-codes
POST  /api/admin/user-groups/:id/invite-codes
PATCH /api/admin/user-groups/:id/invite-codes/:invite_id/disable
```

GET（列表）Query 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| status | string | 否 | 按状态过滤：active / disabled |
| page | integer | 否 | 页码，默认 1 |
| page_size | integer | 否 | 每页数量，默认 20 |

GET（列表）返回 data.items（`InviteCodeResp`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 邀请码 ID |
| code | string | 邀请码 |
| group_id | integer | 所属分组 ID |
| default_group_role | string | 使用此邀请码注册的用户默认组内角色：admin / member |
| max_uses | integer | 最大使用次数，0 表示不限 |
| used_count | integer | 已使用次数 |
| expires_at | string | 过期时间（ISO 8601），永不过期为 null |
| status | string | 状态：active / disabled |
| created_by | integer | 创建人用户 ID，可为 null |
| created_at | string | 创建时间（ISO 8601） |

POST（创建）Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 否 | 邀请码，为空时系统自动生成 8 位随机码 |
| default_group_role | string | 否 | 默认组内角色：admin / member，默认 member |
| max_uses | integer | 否 | 最大使用次数，0 表示不限，默认 0 |
| expires_at | string | 否 | 过期时间（ISO 8601），不传或 null 表示永不过期 |

POST 返回 data：邀请码信息（`InviteCodeResp`），HTTP 201。

错误码：

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| 40000 | 400 | expires_at 格式错误（需 ISO 8601） |
| 40900 | 409 | 邀请码已存在，请更换 |

PATCH（禁用邀请码）无 Body，返回 data：`null`。

### 3.18 用户最终生效权限（排查/一览）

```text
GET /api/admin/users/:id/effective-permissions
```

需要：登录 + `role:manage` 权限 + 管理员双重认证。

请求参数：路径参数 `id` 为目标用户 ID。

返回 data（A-12）：

| 字段 | 类型 | 说明 |
|---|---|---|
| permissions | array\<string\> | 该用户最终生效的权限码集合（角色权限 ∪ 组权限，叠加 overrides 调整后的结果） |
| overrides | array | 该用户当前实际生效（未过期）的权限覆盖调整明细 |

`overrides` 数组元素字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| code | string | 权限码 |
| effect | string | allow 或 deny |

计算逻辑与 2.19 `GET /api/me/permissions` 一致（角色权限 ∪ 组权限，再叠加
`user_permission_overrides` 的 allow/deny 调整：deny 移除、allow 追加），区别仅在于
目标用户是路径参数 `:id` 指定的用户，而非当前登录用户。

> 用途：解决管理后台无"用户权限排查/一览"功能、只能由运维/开发直连数据库写 SQL
> 手动计算的问题。

**404 行为说明：** 本接口不校验路径参数 `:id` 对应的用户是否存在（与本模块其他
`/api/admin/users/{id}/...` 接口——如 3.6 用户角色、用户权限覆盖等——保持一致，IAM 模块
不持有用户表，无法做存在性校验）。若 `:id` 不存在，`permissions` 和 `overrides` 均返回
空数组 `[]`，HTTP 状态码仍为 `200`，**不会**返回 404。

## 4. 商品、订单、钱包和计费接口

### 4.1 商品列表

```text
GET /api/products
```

Query 参数：product_type、keyword、page、page_size。

返回 data.items：商品 ID、类型、code、名称、描述、状态、最低价格、是否可购买。

### 4.2 商品详情

```text
GET /api/products/:id
```

返回 data：商品详情、套餐、价格、会员规则、用户是否可购买。

### 4.3 商品套餐

```text
GET /api/products/:id/plans
```

返回 data.items：套餐 ID、套餐 code、名称、计费类型、时长、额度、价格。

### 4.4 购买商品

```text
POST /api/products/:id/purchase
```

Header：必须传 `Idempotency-Key`。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| plan_id | integer | 是 | 套餐 ID |
| quantity | integer | 是 | 数量 |
| remark | string | 否 | 备注 |

返回 data：order_id、order_no、status、amount、idempotent。

> BUG-A 修复：购买在同一事务内完成扣费与置 paid，`status` 直接返回 `"paid"`（不再经历 `pending` 中间态）。`idempotent: true` 表示同 Idempotency-Key 重复请求，返回原订单，不重复扣费。

### 4.5 我的商品

```text
GET /api/my/products
```

Query 参数：product_type、status、page、page_size。

返回 data.items：商品、资产、到期时间、状态。

### 4.6 管理后台商品列表

```text
GET /api/admin/products
```

Query 参数：product_type、status、keyword、page、page_size。

返回 data.items：商品列表。

### 4.7 创建商品

```text
POST /api/admin/products
```

Body 参数：product_type、product_code、name、description、business_ref_id、status。

返回 data：product_id。

### 4.8 商品详情和修改

```text
GET   /api/admin/products/:id
PATCH /api/admin/products/:id
PATCH /api/admin/products/:id/status
```

PATCH Body 参数：name、description、business_ref_id、status。

返回 data：商品详情或 `updated`。

### 4.9 商品套餐管理

```text
GET   /api/admin/products/:id/plans
POST  /api/admin/products/:id/plans
PATCH /api/admin/products/:id/plans/:plan_id
```

Body 参数：plan_code、name、billing_type、duration_days、quota_json、status。

返回 data：套餐信息或 `updated`。

### 4.10 商品访问规则

```text
GET   /api/admin/products/:id/access   -- [product:view] 回显已配置访问规则
PATCH /api/admin/products/:id/access   -- [product:edit] 覆盖写入
```

GET 返回 data：`{"items": [...]}`（与 PATCH 写入 body 键名对称），无配置时 `items` 为 `[]`。单条字段：id、product_id、role_id、can_view、can_buy、can_use、created_at、updated_at。

PATCH Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| items | array | 是 | 角色访问规则（覆盖写入，`[]` 表示清空所有规则） |

items 字段：role_id、can_view、can_buy、can_use。

> D-011：请求体必须包含 `items` 字段，缺失时返回 `400 40000`（不会静默删除已有规则）。传 `"items": []` 为合法操作，表示清空该商品的所有角色访问规则。

返回 data：`{"message": "访问权限配置成功"}`。

### 4.11 商品价格

```text
GET   /api/admin/products/:id/prices   -- [product:view] 回显商品所有套餐已配置价格（跨套餐）
PATCH /api/admin/products/:id/prices   -- [product:edit] 覆盖写入
```

GET 返回 data：`{"items": [...]}`，跨该商品所有套餐的扁平价格列表，用 `product_plan_id` 区分归属，无配置时 `items` 为 `[]`。单条字段：id、product_plan_id、role_id、membership_level_id、price_amount、currency、created_at、updated_at。

PATCH Body 参数：items（批量覆盖写入）。

items 字段：product_plan_id、role_id（可空=非角色价）、membership_level_id（可空=非会员价）、price_amount、currency。

返回 data：`updated`。

> 价格优先级：会员价（membership_level_id 非空）> 角色价（role_id 非空）> 默认价（两者均空）。

### 4.12 商品处理器

```text
GET /api/admin/product-handlers
```

返回 data.items：product_type、handler_code、service_name、status。

### 4.13 订单列表和详情

```text
GET /api/orders
GET /api/orders/:id
GET /api/admin/orders
GET /api/admin/orders/:id
```

Query 参数：order_type、status、created_from、created_to、page、page_size。

返回 data：订单信息、订单明细、支付时间、关联资产。

### 4.14 支付订单

```text
POST /api/orders/:id/pay
```

Header：必须传 `Idempotency-Key`。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| pay_method | string | 是 | wallet |

返回 data：order_id、status、wallet_transaction_id、asset_id。

### 4.15 取消订单

```text
POST /api/orders/:id/cancel
```

Body 参数：reason。

返回 data：`cancelled`。

### 4.16 钱包信息

```text
GET /api/wallet
```

返回 data：wallet_id、balance_amount、frozen_amount、currency。

### 4.17 钱包流水

```text
GET /api/wallet/transactions
GET /api/admin/wallet-transactions
```

Query 参数：type、direction、created_from、created_to、page、page_size。

返回 data.items：流水 ID、金额、方向、余额快照、关联订单、时间。

### 4.18 创建充值订单

```text
POST /api/recharge/orders
```

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| amount | string | 是 | 充值金额，字符串传递避免浮点精度问题，例如 "100.00" |
| payment_method | string | 是 | wechat / alipay |
| return_url | string | 否 | 前端跳转回调 URL，仅用于展示，不作为充值完成依据 |

返回 data：

| 字段 | 类型 | 说明 |
|---|---|---|
| order_id | integer | 充值订单 ID |
| order_no | string | 订单号 |
| amount | string | 充值金额 |
| status | string | pending |
| pay_url | string | 支付链接或二维码内容（由具体支付渠道决定格式） |

### 4.19 支付回调（第三方支付平台异步通知）

```text
POST /api/payments/notify/:provider
```

Path 参数：

| 字段 | 类型 | 说明 |
|---|---|---|
| provider | string | wechat / alipay |

说明：

- 此接口无需登录态（`Authorization` 不需要）。
- 必须校验第三方签名（微信支付用 RSA-OAEP / AEAD_AES_256_GCM，支付宝用 RSA2），签名校验失败直接返回 HTTP 400。
- 必须幂等：同一 `provider_trade_no` 收到多次回调只处理一次（查 `payment_callbacks` 表）。
- 处理完成后必须按第三方协议返回成功标志（如微信支付返回 `{"code":"SUCCESS","message":"成功"}`），否则第三方平台会持续重试。
- 严禁在回调处理中做耗时操作，应写入 `payment_callbacks` 后异步处理充值入账。

幂等处理流程：

```text
收到回调
  -> 校验签名
  -> 写入 payment_callbacks（status = received）
  -> 查询 payment_callbacks 是否已存在 processed 记录（按 provider + provider_trade_no）
  -> 如已处理，直接返回成功
  -> 查询关联 order，校验订单状态和金额
  -> 开启事务：更新 order 状态、钱包加款、写入 wallet_transactions
  -> 更新 payment_callbacks.status = processed
  -> 提交事务
  -> 返回第三方成功响应
```

### 4.19 用户钱包后台接口

```text
GET   /api/admin/users/:id/wallet              -- 权限 wallet:view
PATCH /api/admin/users/:id/wallet/freeze       -- 权限 wallet:manage
```

冻结/解冻 Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| action | string | 是 | freeze / unfreeze |
| amount | string | 是 | 冻结/解冻金额 |
| reason | string | 否 | 操作原因 |

返回 data：钱包信息或 `updated`。

> `wallet:manage` 为冻结操作专用权限码，需配套 seed migration 写入 admin 角色。

### 4.20 按量计费事件

```text
POST /api/internal/product-usage-events
```

Header：必须传 `Idempotency-Key`。

Body 参数：event_id、user_id、product_id、product_type、product_code、product_plan_id、instance_id、usage_type、usage_amount、usage_unit、occurred_at、idempotency_key。

返回 data：consumption_record_id、wallet_transaction_id、amount、idempotency_key。

> 内部接口（IP 白名单保护），不对外公开。金额 = 命中 product_billing_rules 的 price_amount × 扣除 free_quota 后的计费用量。

### 4.21 计费规则和消费记录

```text
GET   /api/product-consumption-records          -- 用户查本人消费记录（登录）
GET   /api/admin/product-consumption-records    -- 管理员查全量（权限 wallet:view）
GET   /api/admin/product-billing-rules          -- 计费规则列表（权限 product:view）
POST  /api/admin/product-billing-rules          -- 新增计费规则（权限 product:create）
PATCH /api/admin/product-billing-rules/:id      -- 修改计费规则（权限 product:edit）
```

消费记录 Query 参数：product_id、usage_type、created_from、created_to、page、page_size（管理员额外支持 user_id）。返回 data.items：记录 ID、商品、用量、金额、关联流水、时间。

计费规则 Body 参数：product_id、product_plan_id（可空=商品级通用规则）、usage_type、usage_unit、price_amount、currency、billing_mode、free_quota、status。

返回 data：规则信息、消费记录列表（扁平分页）或 `updated`。

> 计费规则归 product 模块管理，消费记录归 finance_consumer 模块；均由后端乙负责。

## 5. 用户资产、会员、应用和内容接口

### 5.1 用户资产

```text
GET /api/my/assets
GET /api/my/assets/:id
GET /api/my/entitlements
GET /api/admin/user-assets
GET /api/admin/user-entitlements
GET /api/admin/asset-events
GET /api/admin/users/:id/assets
GET /api/admin/users/:id/entitlements
```

Query 参数：asset_type、status、product_id、page、page_size。

返回 data：资产列表、资产详情、权益额度、资产事件。

### 5.2 会员接口

```text
GET   /api/memberships
GET   /api/memberships/:id/benefits      # 公开：某等级 active 权益（#168）
GET   /api/my/membership
GET   /api/admin/membership-levels
POST  /api/admin/membership-levels
PATCH /api/admin/membership-levels/:id
GET   /api/admin/membership-benefits
POST  /api/admin/membership-benefits
PATCH /api/admin/membership-benefits/:id
GET   /api/admin/user-memberships
POST  /api/admin/user-memberships        # 管理端手动开通/续期（M10，#154）
PATCH /api/admin/user-memberships/:id    # 管理端取消/改期（M11，#154）
```

> 变更记录：
> - 会员**购买无独立接口**，统一走商品流程（`product_type=membership` → order → provision → `CreateOrRenewMembership`）；原 `POST /api/memberships/:id/purchase` 与 `/api/admin/product-membership-rules`（×3）已删除（C-OPT-1/2）。
> - `GET /api/my/membership` 与 `GET /api/admin/user-memberships` 的会员对象已内联 `level_code`/`level_name`（保留 `level_id`，纯增量，#168）；`asset_id` 无关联资产时返回 `null`（key 恒在，#169）。
> - 字段契约与示例以 `docs/frontend-api-reference.md` §十一为准。

会员等级 Body 参数：code、name、level_order、status。

会员权益 Body 参数：membership_level_id、benefit_type、target_product_id、target_product_type、benefit_config_json、status。

商品会员规则 Body 参数：product_id、membership_level_id、rule_type、discount_rate、included_quota_json、status。

返回 data：会员等级、权益、用户会员或 `updated`。

### 5.3 应用接口

```text
GET   /api/apps
GET   /api/apps/:id
POST  /api/apps/:id/purchase
GET   /api/my/apps
GET   /api/admin/apps
POST  /api/admin/apps
PATCH /api/admin/apps/:id
PATCH /api/admin/apps/:id/access
PATCH /api/admin/apps/:id/prices
GET   /api/admin/application-adapters
POST  /api/admin/application-adapters
PATCH /api/admin/application-adapters/:id
```

应用 Body 参数：code、name、type、description、icon_url、access_url、callback_url、adapter_config_json、status。
（`access_url` 为用户访问入口，面向用户、进用户端白名单返回；写入须 https、禁危险 scheme、≤512。`callback_url`/`adapter_config_json` 为内部字段，用户端剔除。）

应用适配器 Body 参数：app_code、app_name、app_type、adapter_type、service_name、callback_url、supported_actions_json、usage_event_types_json、status。

返回 data：应用信息、适配器信息或 `updated`。

#### 5.3.1 进入应用（SSO 一次性票据，阶段二）

```text
POST /api/apps/:id/launch                -- 用户端签发一次性进入票据（需登录）
POST /api/internal/app-launch/verify     -- 应用后端用票据换身份（X-Internal-Token + IP 白名单，不对外公开）
```

**POST `/api/apps/{id}/launch`**（用户 JWT）：校验①应用 active 且已配 `access_url`；②用户对该应用持有 active 资产（使用权）。通过后签发随机短时票据。

返回 data：`{ access_url, launch_ticket, expires_in }`（票据 `lt_` 前缀，TTL 60s，一次性）。
错误码：`40400` 应用不存在/未开放入口；`40003` 无使用权。

端到端流程：用户点「进入应用」→ 前端调 launch 拿 `{access_url, launch_ticket}` → 跳转 `{access_url}?ticket={launch_ticket}` → 应用后端调 verify 换身份。

**POST `/api/internal/app-launch/verify`**（`X-Internal-Token` 主闸 fail-closed + IP 白名单）：

Body：`{ launch_ticket }`。返回 data：`{ user_id, app_id, product_id }`（校验通过并**消费**票据，Redis `GETDEL` 原子防重放）。
错误码：`40003` 鉴权失败 / 票据无效/已过期/已被使用。仅返回最小必要身份字段，不含用户敏感资料。

**GET `/api/internal/user-entitlements?user_id={uid}&product_id={pid}`**（`X-Internal-Token` 主闸 fail-closed + IP 白名单，不对外公开）：

第三方应用经 SSO 票据只换得 `{user_id, product_id}`、无 `entitlement_id` 也无用户 JWT，用本接口按商品解析该用户的权益以做 prepaid 扣额度。
返回 data：`{ entitlements: [{ entitlement_id, user_id, quota_total, quota_used, quota_reserved, remaining, status, expires_at, usable }] }`（仅 active 权益）。
错误码：`40003` 鉴权失败；`40000` 参数错误。字段级契约见 `docs/app/billing-integration-spec.md §5.0`。

### 5.4 公告和帮助文档

```text
GET   /api/announcements
GET   /api/help/categories
GET   /api/help/articles
GET   /api/help/articles/:id
GET   /api/admin/announcements
POST  /api/admin/announcements
PATCH /api/admin/announcements/:id
GET   /api/admin/help/categories
POST  /api/admin/help/categories
PATCH /api/admin/help/categories/:id
GET   /api/admin/help/articles
POST  /api/admin/help/articles
PATCH /api/admin/help/articles/:id
```

公告 Body 参数：title、content、type、priority、status、visible_scope、target_roles_json、start_at、end_at。

帮助分类 Body 参数：parent_id、name、sort_order、status。

帮助文章 Body 参数：category_id、title、content、summary、tags_json、status、sort_order。

返回 data：公告、分类、文章或 `updated`。

## 6. 后续扩展接口

### 6.1 GPU

```text
GET    /api/gpu/devices
GET    /api/gpu/devices/:id
POST   /api/gpu/rentals
GET    /api/gpu/rentals
GET    /api/gpu/rentals/:id
GET    /api/admin/gpu/devices
POST   /api/admin/gpu/devices
PATCH  /api/admin/gpu/devices/:id
GET    /api/admin/gpu/rentals
```

设备 Body 参数：device_no、region、gpu_model、gpu_count、status、price_per_hour、price_per_day。

租赁 Body 参数：device_id、billing_mode、duration。

返回 data：设备信息、租赁订单和租赁状态。

### 6.2 Agent

```text
GET   /api/agents/templates
GET   /api/agents/templates/:id
POST  /api/agents/customization-orders
GET   /api/my/agents
POST  /api/my/agents
PATCH /api/my/agents/:id
GET   /api/admin/agent-templates
POST  /api/admin/agent-templates
PATCH /api/admin/agent-templates/:id
GET   /api/admin/agent-customization-orders
PATCH /api/admin/agent-customization-orders/:id
```

Agent 模板 Body 参数：code、name、description、base_prompt、status。

用户 Agent Body 参数：template_id、name、system_prompt、model_id、status。

定制订单 Body 参数：agent_template_id、requirement。

返回 data：模板、用户 Agent、定制订单信息。

### 6.3 Skills

```text
GET   /api/skills
GET   /api/skills/:id
POST  /api/skills/:id/purchase
POST  /api/my/agents/:id/skills
GET   /api/admin/skills
POST  /api/admin/skills
PATCH /api/admin/skills/:id
POST  /api/admin/skills/:id/versions
```

Skill Body 参数：code、name、description、category、status。

Skill 版本 Body 参数：version、manifest_json、package_url、changelog、status。

绑定 Body 参数：skill_id、skill_version_id、enabled。

返回 data：Skill、版本、购买或绑定结果。

### 6.4 Token

```text
GET   /api/token/models
POST  /api/token/chat/completions
GET   /api/token/usage
GET   /api/admin/token/providers
POST  /api/admin/token/providers
PATCH /api/admin/token/providers/:id
GET   /api/admin/token/models
POST  /api/admin/token/models
PATCH /api/admin/token/models/:id
GET   /api/admin/token/routes
POST  /api/admin/token/routes
PATCH /api/admin/token/routes/:id
GET   /api/admin/token/usage
```

供应商 Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 是 | 供应商唯一 code |
| name | string | 是 | 供应商名称 |
| base_url | string | 是 | API 基础 URL |
| auth_type | string | 是 | api_key / oauth |
| api_key_plaintext | string | 否 | 明文 API Key，后端加密后存储，接口不返回 |
| status | string | 是 | active / inactive |
| priority | integer | 否 | 默认路由优先级 |

说明：接口接收 `api_key_plaintext`，后端使用 `AES-256-GCM` 加密后存入 `api_key_encrypted`，**接口响应绝不返回任何形式的明文 API Key**。

模型 Body 参数：provider_id、model_code、display_name、context_window、input_price_per_1k、output_price_per_1k、sale_input_price_per_1k、sale_output_price_per_1k、status。

模型路由 Body 参数：logical_model_code、provider_model_id、weight、priority、status。

Chat 请求 Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| model | string | 是 | 逻辑模型名，例如 gpt-4o |
| messages | array | 是 | 消息列表，兼容 OpenAI messages 格式 |
| stream | boolean | 否 | 是否流式返回，默认 false |
| temperature | number | 否 | 采样温度 |
| max_tokens | integer | 否 | 最大输出 Token 数 |

说明：

- `stream = true` 时响应使用 Server-Sent Events（SSE）格式，`Content-Type: text/event-stream`。
- 网关层不缓冲流式响应 body，直接透传上游 SSE 数据；确认所有中间件（Logger、Recovery）不会缓冲响应 body。
- 路由选择：根据 `logical_model_code` 查找 `token_model_routes`，按 `weight` 加权随机选择上游；若选中的上游断路器熔断，按 `priority` 升序取下一个。

返回 data：模型列表、OpenAI 兼容响应、Token 用量统计。
