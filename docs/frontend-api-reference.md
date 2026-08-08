# 前端接口参考文档

> Token 文字模型执行层：前端继续调用既有 `/api/token/chat/completions` 或 `/v1/chat/completions`，无需感知 Native/Bifrost。公开响应不包含 Bifrost `extra_fields`、路由信息、供应商响应头或内部 Key 名称。成功响应缺少 Usage 时，后台记录为 `settlement_pending`，前端不得自行使用 `max_tokens` 估算已扣金额。

> G4 前端处理：40310 显示后端稳定拒绝文案；该文案不可由策略编辑器修改。40311 提示联系管理员；42920 提示预算达到上限；42921/42922 读取 `Retry-After` 后允许倒计时重试；50320/50321 显示服务暂不可用。SSE 收到 `molin.content_policy` 后显示其 message 并以随后 `[DONE]` 正常结束，不展示已经缓冲但未通过审核的内容；该请求用户消费为 0，上游成本只供平台财务对账。

> G0/G1 说明：`000060` 新商业账本是后端 Expand Schema，当前不改变前端请求和用量查询。G2 切换新读写前，前端不得依赖 `ai_requests`、`ai_usage_items` 或执行模型内部字段；阶段契约见 [`ai-gateway-g0-g1-contract.md`](./ai-gateway-g0-g1-contract.md)。

> **版本**：Week 1 + Week 2 已验收（2026-06-06）；2026-06-10 补丁更新（发码拦截 + 管理员双重认证强制）；2026-06-11 接口变更同步（用户列表 keyword、角色/权限模糊搜索、实名审核 status 过滤、权限覆盖过滤参数及 snake_case 字段、实名审核详情新增 user_id/submitted_at/reviewed_at、POST 实名认证响应新增 data.id）；2026-06-12 更新（认证/角色权限/用户分组/实名认证）：分页响应字段 `list` → `items`（仅认证/角色权限/实名认证相关章节）；发送验证码接口拆分为 `/api/auth/verification-codes/email` 和 `/api/auth/verification-codes/phone` 两个独立接口，`email`/`phone`/`scene` 均为必填；手机号登录改为密码登录（`{phone, password}`）；实名认证提交响应字段修正为 `{id, status}`（`verification_id` 为设计文档冗余字段，已于 2026-06-12 从 `full-api-design.md` 中移除，不再视为缺口）；新增角色详情接口 `GET /api/admin/roles/{id}`；新增审计日志接口 `GET /api/admin/audit-logs`；新增"用户分组管理"章节（16 个接口）；2026-06-13 更新：手机号登录改为验证码登录（`{phone, code}`，PR#20）；退出登录后当前 Access Token 立即吊销，401/40001（PR#22）；`/api/auth/login/phone`、`/api/auth/login/email` 对未注册账号统一返回 404/40404（PR#25）；**2026-06-15 更新（Round 7 审计 D-93/D-94/D-95/D-96 全部闭环）**：登录/注册/刷新令牌响应新增 `user` 对象（D-93，PR#91）；密码长度约束统一为 6-72 位（D-94，PR#95）；auth/iam/identity 模块 11 个分页接口响应结构改为扁平（去掉嵌套 `pagination` 对象，D-95，PR#97）；`bind_phone`/`bind_email`/`admin_verify` 三个 scene 迁移到专属认证态发码接口，不再接受公开端点的请求（D-96，PR#93）；**2026-06-16 更新（后端乙缺陷修复闭环，88/88 回归全通过）**：`GET /api/wallet` 响应字段 `id` → `wallet_id`（D-008，PR#135）；`PATCH /api/admin/products/{id}/prices` body 结构统一为 `{"items":[{"product_plan_id":...,...}]}`（D-009，PR#135）；`PATCH /api/admin/products/{id}/access` body key 统一为 `items`，缺失 `items` 字段返回 400（D-011，PR#137）；购买接口 `POST /api/products/{id}/purchase` 响应新增 `idempotent` 字段，`status` 直接返回 `paid`（BUG-A，PR#136）；商品/套餐/计划不存在时接口统一返回 404/40400（BUG-B，PR#136）；重复 product_code/plan_code 返回 400 友好提示（BUG-C，PR#136）；多套餐价格覆盖写入改为单事务原子操作（BUG-D，PR#136）；**2026-06-16 更新（二）（后端乙契约勘误 + #144，已部署测试服回归 52/52 通过）**：套餐 `user_price` 未配置价格时统一返回 `"-1"`（区别于合法免费价 `"0"`，#144，PR#144）；`GET /api/products/{id}/plans` 响应订正为 D-95 扁平分页 `{items,page,page_size,total}`（原文档误写 `{plans:[]}`）；购买响应补 `asset_id` 字段（异步开通时为 `null`/`0`）；`order_type` 取值订正为 `product`（购买）/`recharge`（充值）（原误写 `purchase`）；商品状态切换 `PATCH /api/admin/products/{id}/status` 仅接受 `active`/`inactive`（`draft` 为创建初始态、不可设置）；**2026-06-19 更新（后端丙会员对接增强 #167~#170，已部署测试服回归 22 用例通过）**：新增公开权益端点 `GET /api/memberships/{id}/benefits`（无需登录，仅返回 `status=active` 权益，等级不存在/未上架返回 404/40400，见 §11.1b，PR#168）；`GET /api/my/membership`（§11.2）与 `GET /api/admin/user-memberships`（§11.5）会员对象**内联** `level_code`/`level_name`（保留 `level_id`，纯增量，前端无需再按 level_id 映射等级名，PR#168）；`asset_id` 去掉 `omitempty`，无关联资产时返回 `null`（key 恒在，PR#169）；管理端列表 `page_size` 上限 100、用户端公告上限 50；帮助文章详情 `GET /api/help/articles/{id}` 的 `data` 直接为文章对象（非包裹，§12.2，PR#167）
> **测试服务器**：`http://8.130.9.163:8080`
> **鉴权方式**：所有需要登录的接口在 Header 中携带 `Authorization: Bearer <access_token>`
> **2026-07-23 Phase 2 待复验历史输入**：既往 Go、MySQL、migration、故障注入和 `register` 真实发送记录仅作为后续复验线索；本轮未重新执行或验收，不代表 Phase 1/Phase 2 已通过。真实 Redis、五场景、RAM 否定矩阵、管理前端和完整 E2E 均待 Phase 2 验收。

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
| 40003 | 403  | 无权限；管理员未完成手机+邮箱双重认证时 message 固定为「请先完成管理员双重认证」 |
| 40400 | 404  | 资源不存在；邮件资源不存在时 message 固定为「邮件资源不存在」 |
| 40101 | 401  | 账号已被封禁 |
| 40404 | 404  | 账号未注册，请先注册（登录发码时账号不存在；`/api/auth/login/phone`、`/api/auth/login/email` 本身对未注册账号也返回此码，见 §1.3） |
| 40900 | 409  | 账号已注册（注册发码时账号已存在） |
| 42900 | 429  | 请求频率超限 |
| 42901 | 423  | 邮箱登录失败达到 5 次，锁定 15 分钟 |
| 50000 | 500  | 服务器内部错误 |
| 51001 | 422  | 邮件模板缺少 Code 或 ExpireMinutes |
| 51002 | 502  | DirectMail 调用或 RAM 授权失败 |
| 51003 | 503  | 生产邮件 Adapter 或必要配置未就绪 |
| 60001 | 400  | 余额不足（钱包） |
| 60005 | 400  | 权益额度不足（含预付 token 套餐额度耗尽，第二阶段复用此码；勿用 60002，那是「重复支付」） |
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

> 测试环境若启用 `SMS_TEST_MODE=true`，手机号与业务场景必须分别位于 `SMS_TEST_PHONE_WHITELIST` 和 `SMS_TEST_SCENE_ALLOWLIST`。任一不满足均返回 `503/50300`，前端统一显示“短信功能当前不可用”；长期测试服登录仅放行 `scene=login`，其他场景不会创建 OTP 或调用供应商。

请求体：
```json
{
  "phone": "<11位手机号>",
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

公开邮箱发码成功响应仍为 `{sent,expires_in}`；手机发码成功响应为：

```json
{
  "sent": true,
  "expires_in": 600,
  "business_request_id": "sms_...",
  "submit_status": "accepted"
}
```

手机 `accepted` 只表示短信供应商受理，不表示运营商送达或用户实际收到。手机号与场景的 60 秒冷却超限返回
`429/42900`；发码前 Redis 门禁不可用返回 `503/50300`，消费前门禁不可用则按安全策略统一返回
`400/40000「验证码错误或已过期」`。前端只有在四个手机成功字段全部有效时才能进入倒计时，
失败时不得模拟成功。生产环境永不返回 code。既有显式非生产邮件调试模式可能额外返回 `data.code`，但该字段不属于
稳定契约；前端类型、页面、console、埋点和错误上报均不得读取或记录。验证码和过期时间均由服务端生成，前端不得
生成 code 或自行决定有效期。

公开手机发码和密码重置同样使用全局可信代理来源解析器进行 IP 限流。浏览器不得主动构造任何来源 Header；
可信代理来源异常返回 `403/40003`，来源解析器不可用返回 `503/50300「验证码服务当前不可用」`。手机发码页面
按既有稳定映射向用户显示“短信功能当前不可用”；密码重置提交保留后端“验证码服务当前不可用”文案。

---

### 1.2 注册

> ⚠️ 旧的单独邮箱注册（`/api/auth/register/email`）和单独手机号注册（`/api/auth/register/phone`）已下线，唯一入口为统一注册。

**POST** `/api/auth/register` — 统一注册（手机 + 邮箱 + 用户名，需双验证码）

```json
{
  "username": "张三",
  "phone": "<11位手机号>",
  "email": "user@example.com",
  "password": "Test1234!",
  "phone_code": "<6位手机验证码>",
  "email_code": "<6位邮箱验证码>",
  "invite_code": "ABC12345"
}
```

密码 `password` 长度须为 **6-72 位**（D-94）；低于 6 位返回 `400 40000`，超过 72 位同返回 `400 40000`。

`invite_code` 为**可选**字段，用于注册即落入对应用户分组：
- 传有效邀请码 → 落入该邀请码对应的分组，并赋予邀请码配置的组内角色；
- 传无效/过期/已满的邀请码 → **降级落入默认兜底组**（不报错，注册照常成功）；
- 不传 → 落入默认兜底组（`is_default=true` 的分组）；
- 系统未配置默认组时 → 注册成功但不落任何组。

> 落组失败不影响注册结果（best-effort）；注册成功后用户的分组归属可在管理后台「用户分组」中查看与调整。

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

**POST** `/api/auth/login/email/code` — 邮箱 + 验证码登录（Phase 1 delta，待 QA/PM 签署）

先调用公开邮箱发码端点并传 `scene=login`。登录 Body 必须严格为：

```json
{ "email": "user@example.com", "code": "<6位验证码>" }
```

只允许 `email`、`code` 两个字段；缺失、空值、类型错误或额外字段固定
`400/40000「请求参数错误」`。验证码必须为 `scene=login`、accepted、未使用、未过期且与邮箱/验证码匹配；验证与
`used_at` 更新由后端原子完成。验证码错误、scene 错误、非 accepted、已使用、过期或并发消费失败均固定
`400/40000「验证码错误或已过期」`。

- 未注册：`404/40404「邮箱未注册，请先注册」`。
- 禁用：`403/40003「账号已被禁用」`。
- 与邮箱密码登录共用 D-16 失败计数：累计 5 次锁定 15 分钟，锁定期返回
  `423/42901「登录失败次数过多，请15分钟后重试」`；任一邮箱密码/验证码登录成功都清除计数。
- `scene=login` 发码同时受 10 次/分钟/IP 与 10 次/分钟/规范化邮箱 HMAC 限制，超限为
  `429/42900「请求频率超限」`。

成功响应复用 `LoginResp`，与邮箱密码登录相同。成功新增当前会话但不吊销其他会话；普通登录 Token 不改变管理员手机/邮箱
MFA 状态，也不能绕过管理接口的双重认证。`POST /api/auth/login/email` 的密码登录保持不变。

该 delta 复用现有 verification_codes，不需要 schema 变更；当前只冻结前端契约，不代表接口已实现或验收通过。

**POST** `/api/auth/login/phone` — 手机号 + 验证码登录（PR#20，非密码登录）

登录前需先调用 `POST /api/auth/verification-codes/phone`（`scene=login`）获取验证码：

```json
// POST /api/auth/verification-codes/phone
{
  "phone": "<11位手机号>",
  "scene": "login"
}
```

再调用登录接口：

```json
{
  "phone": "<11位手机号>",
  "code": "<6位验证码>"
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
  "code": "<6位验证码>",
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
{ "phone": "<新手机号>", "code": "<6位验证码>" }
```

> 调用前须先通过 `POST /api/me/verification-codes/phone` 向新手机号发送验证码（§1.8.1）。成功换绑会把 `phone_verified` 置为 true，并清空旧号码的 `admin_phone_verified_at`；管理员必须使用新手机号重新完成手机 MFA。

**PATCH** `/api/me/email` *(需登录)*
```json
{ "email": "new@example.com", "code": "<6位验证码>" }
```

> 调用前须先通过 `POST /api/me/verification-codes/email` 向新邮箱发送验证码（§1.8.1）。成功换绑会把 `email_verified` 置为 true，并清空旧邮箱的 `admin_email_verified_at`；管理员必须使用新邮箱重新完成邮箱 MFA。

响应：`data: null`

---

### 1.8.1 换绑发码（D-96，需登录）

> ⚠️ **D-96（2026-06-15）新增**：换绑手机号/邮箱的验证码不再走公开发码端点，必须使用以下认证态接口（需携带有效 Bearer Token）。

**POST** `/api/me/verification-codes/phone` — 向新手机号发送换绑验证码

```json
{ "phone": "<新手机号>" }
```

**POST** `/api/me/verification-codes/email` — 向新邮箱发送换绑验证码

```json
{ "email": "new@example.com" }
```

两个换绑发码端点成功响应均为 `data={sent:true,expires_in:600}`。生产环境永不返回 code；既有显式非生产调试模式的
可选 `data.code` 不属于稳定前端契约，页面不得读取、展示或记录。验证码和过期时间由服务端生成。

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
> 未完成时返回 403/40003"请先完成管理员双重认证"。
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

响应：`data={sent:true,expires_in:600}`；生产环境永不返回 code，前端不得读取非生产调试扩展字段。

**POST** `/api/admin/auth/verification-codes/email` *(需登录 + user:manage 权限)* — 向当前管理员绑定的邮箱发送验证码

响应：`data={sent:true,expires_in:600}`；生产环境永不返回 code，前端不得读取非生产调试扩展字段。

**POST** `/api/admin/auth/verify-phone` *(需登录 + user:manage 权限)*
```json
{ "code": "<6位验证码>" }
```

**POST** `/api/admin/auth/verify-email` *(需登录 + user:manage 权限，需手机已认证)*
```json
{ "code": "<6位验证码>" }
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

### 3.0c 管理员修改用户

**PATCH** `/api/admin/users/{id}` *(需登录 + `user:manage` 权限 + 管理员双重认证)*

```json
{ "email": "new@example.com", "phone": "<新手机号>", "status": "active" }
```

字段均为可选；提交手机号或邮箱时，管理员编辑接口仍沿用既有“基础 verified 自动置为 true”的管理规则，但服务端必须清空目标账号对应的 `admin_phone_verified_at` 或 `admin_email_verified_at`。目标管理员不得继承旧联系方式的 MFA，必须使用新联系方式重新认证。响应 `data` 为字符串 `updated`。

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

## 五之二、邮件模板管理（后端甲，DirectMail Phase 3 前端范围已形成，Phase 4 待验收）

> 当前状态：**Phase 1 契约评审、Phase 2 本地实现证据与 Phase 3 前端范围已经形成；Phase 4 真实测试环境尚未通过，禁止进入 Phase 5。阶段真相与验收证据统一以 `docs/aliyun-directmail-email-template-feature-acceptance.md §17` 为准，不得用本地测试、未认证冒烟或供应商 accepted 代替真实全链路验收。**
> 固定场景只有 `register`、`login`、`reset_password`、`bind_email`、`admin_verify`。
> 所有管理端接口都需要管理员手机与邮箱双重认证；四个权限对应的读写请求都必须过 MFA。未完成时固定处理
> `403/40003「请先完成管理员双重认证」`；资源不存在统一为 `404/40400`，不得兼容 `40004/40031`。

发布与回滚必须按 `000055 → 000056 → 000057` 的完整停机链执行，具体前置条件、成功 receipt 分支、备份恢复与回滚矩阵统一以
`docs/aliyun-directmail-email-template-feature-acceptance.md §17` 为权威。禁止滚动部署，禁止新旧应用共存；前端联调必须在目标 schema、
应用版本、health、ready、Redis、邮件配置和 Bootstrap 默认关闭状态全部核验后开始。前端不得依据旧验证码字段推导或展示验证码，
也不得实现或调用 Bootstrap 内部入口。

`migration_000055_permission_ownership` 与 `migration_000056_permission_ownership` 均为 migration-only 技术表，不属于邮件业务页面或前端接口范围。

| 权限码 | 前端能力 |
|---|---|
| `email:template:view` | 查看概览、模板、场景、白名单、同步与发送日志 |
| `email:template:manage` | 修改模板本地启停、场景绑定、维护测试邮箱白名单 |
| `email:template:sync` | 执行模板同步 |
| `email:template:test` | 执行白名单测试发送 |
| `email:template:bootstrap` | 仅供内部运维首次配置；前端不得据此显示菜单、按钮或调用入口 |

### 5.2.1 模板镜像

**GET** `/api/admin/email/summary` — 返回对象：

```typescript
interface EmailSummary {
  template_total: number
  approved_count: number
  local_enabled_count: number
  unbound_scene_count: number
  submitted_today_count: number
  failed_today_count: number
  last_synced_at: string | null
}
```

前六项为非负整数。今日按 Asia/Shanghai 自然日 `[00:00,次日00:00)`；submitted_today_count 包含该窗口全部
accepted/failed 最终日志，数据库内部 pending 不统计；failed_today_count 只含 failed。last_synced_at 是最近 succeeded 同步完成时间，从未成功同步时为 null。
template_total 含全部镜像；approved_count 仅 approved 且非 missing；local_enabled_count 按本地开关统计；
unbound_scene_count 只统计五场景中 template_id=null。

**GET** `/api/admin/email/templates` — D-95 扁平分页。

Query：`keyword`、`provider_status=draft|pending|approved|rejected`、`local_enabled`、`variables_complete`、
`missing=true|false`、`scene`、`page`、`page_size`。

`items` 单条：

```typescript
interface EmailTemplate {
  id: number
  provider: 'aliyun_directmail'
  provider_template_id: string
  name: string
  subject: string
  provider_status: 'draft' | 'pending' | 'approved' | 'rejected'
  review_comment: string | null
  variables_complete: boolean
  local_enabled: boolean
  bound_scenes: EmailScene[]
  missing: boolean
  missing_since: string | null
  last_synced_at: string
  version: number
}
```

**GET** `/api/admin/email/templates/{id}` — 在列表字段上增加：

```typescript
{
  sender_nickname: string | null
  template_text: string
  variables: string[]
  content_sha256: string
}
```

`variables` 只包含变量名，不含变量值。前端不得显示供应商原始响应或任何凭据。

**PATCH** `/api/admin/email/templates/{id}/status` — Body：`{local_enabled:boolean,version:number}`。成功返回更新后的
EmailTemplate 且 version+1；版本冲突 `409/40900`。启用要求 approved、非 missing 且 variables_complete=true；
缺变量返回 `422/51001`。停用仍展示当前变量完整性，但为保证故障模板可安全关停，不因缺变量被拒绝；停用立即阻断绑定发送和模板测试，概览需重新拉取。

`template_text` 是不可信 HTML。预览只能使用独立 iframe `srcdoc`，设置空 `sandbox`，禁止添加
`allow-scripts`、`allow-forms`、`allow-top-navigation`、`allow-top-navigation-by-user-activation`、`allow-popups`、`allow-same-origin`；srcdoc 还需注入
`default-src 'none'; img-src data:; style-src 'unsafe-inline'` CSP，并移除 script、事件属性、form、iframe/object/embed、
base、meta refresh。禁止使用 `v-html` 直接注入管理后台主文档。

### 5.2.2 五场景绑定

```typescript
type EmailScene = 'register' | 'login' | 'reset_password' | 'bind_email' | 'admin_verify'

interface EmailSceneBinding {
  scene: EmailScene
  display_name: string
  template_id: number | null
  provider_template_id: string | null
  provider_status: 'draft' | 'pending' | 'approved' | 'rejected' | null
  local_enabled: boolean
  variables_complete: boolean
  missing: boolean
  enabled: boolean
  variable_mapping: { code: 'Code'; expire_minutes: 'ExpireMinutes' }
  version: number
  updated_at: string
}
```

**GET** `/api/admin/email/scenes` — 固定五条但仍返回 D-95 `{items,page,page_size,total}`。

**PUT** `/api/admin/email/scenes/{scene}` — 全量替换当前场景绑定：

```json
{ "template_id": 12, "enabled": true, "version": 3 }
```

只允许选择 `provider_status=approved && local_enabled=true && missing=false && variables_complete=true` 的模板。
绑定保存、启用绑定、模板测试和正式发送都会由后端重新校验变量同时包含大小写完全一致的 Code 与 ExpireMinutes；
缺任一变量返回 `422/51001`。成功响应返回更新后的绑定，`version` 加一。
版本冲突返回 `409/40900`，前端必须提示「配置已被其他管理员修改，请刷新后重试」并重新拉取，禁止静默覆盖。
场景名和映射 `code→Code`、`expire_minutes→ExpireMinutes` 均为只读，不提供编辑控件。

### 5.2.3 原子同步

**POST** `/api/admin/email/templates/sync`

- Header 必须带 `Idempotency-Key`；同一次用户操作重试必须复用原 key。
- Body 固定：`{"provider":"aliyun_directmail"}`。
- 后端 scope 固定为跨管理员全局 `admin-email-template-sync:aliyun_directmail`，指纹为规范化 method+path+provider；
  因此不同管理员复用同 key+同请求也返回原 run，同 key 不同指纹返回 `409/40900`。
- 响应：

```typescript
interface EmailTemplateSyncResult {
  run_id: number
  provider: 'aliyun_directmail'
  status: 'running' | 'succeeded' | 'failed'
  created_count: number
  updated_count: number
  missing_count: number
  unchanged_count: number
  error_code: string | null
  error_message: string | null
  created_by: number
  started_at: string
  idempotent: boolean
  completed_at: string | null
}
```

running 时 completed_at/error_code/error_message 为 null；succeeded 时 completed_at 非 null、错误字段为 null；failed 时
completed_at/error_code/error_message 均非 null。GET 同步记录与此类型相同但不含 idempotent。

同 key 同请求返回原结果并令 `idempotent=true`；同 key 不同请求或已有另一同步任务运行时返回 `409/40900`。
只有远端全分页和详情读取全部成功，后端才原子更新镜像并维护 missing/missing_since；local_enabled 永不被同步覆盖。
首次同步的新模板 local_enabled=false，需管理员在模板列表显式启用。
失败时页面应保留旧列表并显示后端中文消息。

**GET** `/api/admin/email/template-sync-runs` — `status` 过滤 + D-95 分页。页面只展示计数、发起人和时间，不展示供应商原始错误。

### 5.2.4 测试邮箱白名单

- **GET** `/api/admin/email/test-recipient-allowlist` — D-95；单条为 `{id,email_masked,status,version,created_by,created_at}`。
- **POST** `/api/admin/email/test-recipient-allowlist` — body `{email}`；成功 HTTP 201，data 固定为
  `{id:number,email_masked:string,status:'active',version:number,created_at:string}`；重复返回 `409/40900`。
- **DELETE** `/api/admin/email/test-recipient-allowlist/{id}` — body `{version}`；成功 HTTP 200，data 固定为
  `{id:number,email_masked:string,status:'revoked',version:number,revoked_at:string}`；版本冲突 `409/40900`。

完整邮箱仅在新增和测试发送请求中短暂输入。列表、Toast、浏览器日志、埋点、审计详情均不得展示或记录完整邮箱。
数据库迁移后历史邮箱验证码统一失效且不可关联，不提供历史邮箱恢复或展示能力；只有新发送记录使用真实邮箱 HMAC。
模板测试未命中 active 白名单固定返回 `400/40000`，前端不得按 403 分支处理。
revoked 记录保留 30 天后由后端物理删除，列表中消失是预期行为；审计记录不受影响。

### 5.2.5 模板测试发送与发送日志

**POST** `/api/admin/email/templates/{id}/test-send`

- `{id}` 为平台模板镜像 ID；Header 必须带 `Idempotency-Key`；Body：`{"scene":"register","email":"<已加入白名单的测试邮箱>"}`。
- 锁 scope 固定为 `admin-email-template-test:admin:{admin_id}:template:{platform_template_id}:scene:{scene}:recipient:{recipient_hmac}`；邮箱先 trim、统一小写并校验单裸地址后计算 HMAC。Redis key 只使用 scope 的 HMAC 摘要，Idempotency-Key 不进入 scope；同管理员/模板/场景/收件人竞争同锁，任一维度不同不竞争。
- 后端生成无认证用途的测试码，固定变量为 `Code` 与 `ExpireMinutes=10`；响应绝不返回验证码。
- `TemplateId` 只用于平台模板镜像、场景绑定和发送日志追踪；后端从冻结的 `TemplateText` 本地渲染固定变量后，以 `Subject + HtmlBody` 调用 `SingleSendMail`，前端接口不新增或改变字段。模板非 approved、本地停用、missing、缺少 Code/ExpireMinutes、
  未命中 active 白名单或生产 Adapter 未就绪时均拒绝发送。缺变量为 `422/51001`，非白名单固定 `400/40000`。
- 调用供应商前先写 `pending` 幂等占位；进程中断或并发重试命中 pending 时返回 409/40900，不再次发送。最终状态收敛为 accepted/failed。
- 响应：

```typescript
{
  send_log_id: number
  business_request_no: string
  template_id: number
  scene: EmailScene
  recipient_masked: string
  status: 'accepted'
  failure_reason: null
  idempotent: boolean
  submitted_at: string
}
```

`accepted` 只表示 DirectMail 同步受理，不等于最终送达。前端只能显示“供应商已受理发送请求”，禁止写成“已送达”。
供应商明确失败/拒绝由后端先写 failed 日志并返回通用 HTTP 502/code=51002；响应未知/超时按下方专用文案和持久化阻断规则处理；test-send 失败绝不返回
HTTP 200/status=failed。同 Idempotency-Key 重放 accepted 返回同一 200 且 idempotent=true；重放 failed 返回同一安全
502/51002 错误信封，前端不得自动换 key 重试。
pending 必须按明确响应或未知响应规则及时收敛，不能长期保留。外呼期间丢锁后，明确 accepted/rejected 由后端以
`WHERE id=? AND status='pending'` 唯一收敛 accepted/failed；未知或超时则复用原 `email_send_logs` 行写
failed、`failure_reason=provider_outcome_unknown` 并保留 scope；正式 OTP 保留 expires_at 且同事务置 failed，purpose=test 的
`email_send_logs.expires_at` 必须保持 null，不新增墓碑表。统一派生 `cooldown_until`：OTP 取 expires_at，test 取 submitted_at+10分钟。
每次新外呼取得 Redis 锁后仍按同 scope 与 cooldown_until 查冷却期内 pending/unknown failed，因此 Redis 重启也不能绕过。
原未知请求及旧 key 重放为 `502/51002「供应商响应未知，请在验证码过期后重试」`，旧 key 重放带 `idempotent=true`；
墓碑期内新 key 为 `409/40900「邮件发送结果确认中，请在验证码过期后重试」` 且不外呼；cooldown_until 到期后仅新 key 可重新发送。

**GET** `/api/admin/email/send-logs` — 支持 `scene`、`purpose=otp|test`、`status=accepted|failed`、
`template_id`、时间范围和 D-95 分页。列表固定展示内部日志 ID、场景、用途、脱敏邮箱、平台模板镜像 ID、
DirectMail `TemplateId`、业务请求号、阿里云 `RequestId`、accepted/failed、安全失败原因与提交时间；数据库内部 pending 永不返回，查询 `status=pending` 返回 400/40000。
禁止依赖验证码、完整邮箱、模板变量或供应商原始响应等字段。

```typescript
interface EmailSendLog {
  id: number
  scene: EmailScene
  purpose: 'otp' | 'test'
  recipient_masked: string
  template_id: number
  provider_template_id: string
  business_request_no: string
  provider_request_id: string | null
  status: 'accepted' | 'failed'
  failure_reason: string | null
  submitted_at: string
}
```

accepted 时 provider_request_id 非 null、failure_reason 为 null；failed 时 failure_reason 非 null、provider_request_id 可为 null。

当前范围不接入投递回执 Webhook，不提供最终送达、打开率、点击率状态或统计字段。

邮件管理接口错误处理：平台权限不足 `403/40003`；乐观锁/幂等/同步并发冲突 `409/40900`；缺少模板变量
`422/51001`；供应商 RAM 或调用失败 `502/51002`；生产 Adapter/必要配置未就绪 `503/51003`；非法场景、参数或
测试邮箱未命中白名单固定 `400/40000`。前端只展示平台中文 message，不拼接供应商错误详情。

前置失败必须按下表精确分支，前端不得合并或自行改写 message：

| 条件 | HTTP/code | message |
|---|---|---|
| 路径模板/邮件资源不存在 | `404/40400` | `邮件资源不存在` |
| 场景无绑定 | `409/40900` | `邮件场景未绑定模板` |
| 绑定停用 | `409/40900` | `邮件场景已停用` |
| 模板本地停用 | `409/40900` | `邮件模板已停用` |
| draft | `409/40900` | `邮件模板尚未提交审核` |
| pending | `409/40900` | `邮件模板正在审核` |
| rejected | `409/40900` | `邮件模板审核未通过` |
| missing | `409/40900` | `邮件模板在供应商侧不存在` |
| 缺少变量 | `422/51001` | `邮件模板变量不完整` |
| 未取得 Redis 锁、外呼前丢锁，或生产 Adapter/必要配置未就绪 | `503/51003` | `邮件发送服务未就绪` |

Redis 分布式锁是发布必需依赖；只有未取得锁或外呼开始前丢锁才按 `503/51003` 处理且 Adapter 增量为 0。
外呼开始后的续租/所有权失败不返回 503，也不假定 Adapter 未调用，按明确响应 fencing 或 unknown failed 持久化阻断规则处理。
锁原语及 TTL 以 `docs/full-api-design.md §3.19.4` 为准，本轮 Go 集成未验收。

### 5.2.6 邮件 OTP 的前端语义

- 正式 OTP 发码客户端只提交各端点既定业务字段：公开端点 `{email,scene}`、换绑邮箱端点 `{email}`、管理员邮箱发码无 Body。
  发码请求绝不提交 `code`、`expire_minutes`、平台 `template_id` 或供应商 `TemplateId`；OTP 与 10 分钟有效期由服务端生成。
- OTP 发送前后端写 `verification_codes.send_status=pending`；只有 DirectMail 明确受理并原子置 accepted 后才可校验。
  明确失败/拒绝与响应未知/超时均置 failed 且不可用；unknown 使用 `provider_outcome_unknown` 和专用重试文案。前端只消费统一发码响应 `data={sent,expires_in}`。
- 五个邮件场景的正式幂等完全由服务端业务请求号、固定入口 scope、目标 HMAC 与请求指纹实现，现有客户端不得新增 Idempotency-Key Header；重放成功响应仍为 `{sent,expires_in}`，冲突为 `409/40900`。
  accepted 重放的 expires_in 按原 expires_at 递减；failed 重放返回原安全错误；首请求仍 pending 时并发重放返回
  `409/40900「邮件正在发送，请稍后重试」`，三者都不会重复发信。
- `accepted` 不是最终送达证明；当前范围页面不得推导或展示送达、打开率、点击率。
- 发码同时受 10 次/分钟/IP 与 10 次/分钟/账号限制，任一维度超限均按 `429/42900「请求频率超限」` 展示；前端不得显示或持久化后端的账号限流键。
- 正式发送使用生产 DirectMail Adapter；Mock Adapter 仅用于显式非生产环境。UI 不提供 Adapter 切换或凭据输入。
- 正式 OTP 的平台 template_id 与供应商 TemplateId 均由后端按 scene 解析当前绑定；客户端不得硬编码、缓存或作为发码参数提交。
  平台 template_id 只在管理员配置场景绑定等管理接口中使用，不属于正式 OTP 发码请求。
- `bind_email` 的邮箱只能是当前登录用户本次换绑流程目标；`admin_verify` 只能发送到当前管理员已绑定邮箱且端点严格无 Body。管理员端点携带额外 email 固定 `400/40000「请求参数错误」`；服务层受控 fixture 注入越权目标固定 `403/40003「无权向该邮箱发送验证码」`。

### 5.2.7 管理员邮箱认证首次配置 bootstrap（前端无入口）

`POST /api/internal/email/bootstrap/admin-verify` 是一次性内部运维端点，不属于管理后台或用户控制台接口。前端无需新增页面、菜单、按钮、API 封装、路由或状态管理，也不得保存或发送 `X-Email-Bootstrap-Token`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_*` 配置、`provider_template_id` 或来源 Header。

- 该端点默认不注册并返回 404，只允许批准的运维网络在短时维护窗口调用；前端不得依据 404 探测或展示配置状态。
- 运维调用仍需正常管理员 JWT、当前有效的手机 MFA 和专用 `email:template:bootstrap` 权限；邮箱 MFA 不会被绕过或代填。
- 四个后端配置键固定为 `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS`。enabled 只有配置键缺失时默认 false；字面 true/false（大小写不敏感）有效，显式空字符串或其他值必须使应用启动失败；显式 false 时所有方法404，enabled=true 时其余三项任一缺失或非法均使应用启动失败。Token 复用内部 Token 的客观校验基线实现但必须使用独立值，至少含 8 种不同原始字节且不得与 `INTERNAL_API_TOKEN` 相等；CIDR 禁止零前缀及配置回退，但独立显式配置后允许与平台既有 INTERNAL/TRUSTED 列表同值或重叠。Bootstrap allowed 与 bootstrap trusted-proxy 之间存在规范化后完全相同的 CIDR 条目时启动失败，不同前缀仅部分重叠允许。两个列表分别求规范化 CIDR 地址并集；任一列表通过多条非零前缀覆盖完整 IPv4 或 IPv6 地址族也启动失败，例如 `0.0.0.0/1,128.0.0.0/1`、`::/1,8000::/1`，两个列表不跨语义合并计算全地址族并集。它们不是前端环境变量。
- 请求仅为 POST、单值 Authorization/X-Email-Bootstrap-Token/Idempotency-Key、`application/json` 和严格 `{provider_template_id}`。`provider_template_id` 仅允许 1-64 字节 ASCII 十进制正整数；空值、全零、65 字节及以上、非数字、符号、小数、指数或任何空白均前置返回 `400/40000「请求参数错误」`，attempt 审计、Adapter 与数据库增量均为 0。Bootstrap Token 缺失/空/重复/逗号多值/错误统一 `403/40003「无权限」`；Authorization 继续走标准 401；Idempotency-Key 异常为 `400/40000「请求参数错误」`。
- 阿里云官方 `DescTemplate` 详情真实且未废弃字段为 `RequestId/CreateTime/TemplateSubject/TemplateStatus/TemplateName/TemplateText`；`QueryTemplateByParam` 列表字段 `TemplateId/TemplateName/TemplateStatus/CreateTime` 另行处理，不得混用。现有 Adapter 将 JSON `TemplateName` 精确映射为 `ProviderTemplate.Name`。Name 必须大小写精确等于 `molin_admin_verify_code_v1`，Status=`approved` 且变量包含 `Code`/`ExpireMinutes`。字段来源见 [阿里云 DirectMail DescTemplate](https://help.aliyun.com/en/direct-mail/api-dm-2015-11-23-desctemplate)；前端不参与或展示这些校验。
- 成功只表示 `admin_verify` 投递场景完成首次配置。管理员仍须通过现有邮箱发码及 `POST /api/admin/auth/verify-email` 完成邮箱 MFA，之后才能进入邮件管理页。
- 现有 13 个 `/api/admin/email/*` 的前端契约、四个既有权限和完整 MFA 门禁不变。邮件接口权限不足继续精确消费 `403/40003「无权限」`；后端以邮件专用权限包装器对齐该文案，不要求前端兼容历史 `「无操作权限」`。
- `DescribeTemplate` 指标复用 `operation=describe_template,scene=template_sync`，不新增 bootstrap 指标序列。不同 key 可并发完成只读 Describe，真正写阶段由数据库 `SELECT ... FOR UPDATE` 锁定 admin_verify 行、初始态 CAS 与 receipt scope 唯一约束决定唯一胜者。`admin_id` 同时纳入 key HMAC 作用域和 request fingerprint，receipt 重放还必须匹配当前管理员的 `completed_by`；同一管理员、同一 key、同一 fingerprint 的并发首次请求即使均已 Describe，后取得行锁者仍返回原成功且 `idempotent=true`，跨管理员复用同 key 固定 `409/40900` 且不泄露原操作者。000056 receipt、ownership、内部 Token、CIDR、幂等、强事务审计与回滚均由后端/运维负责，前端不得提供任何“重新 bootstrap”能力。
- 手机 MFA 使用 users 当前时间戳；`ADMIN_VERIFY_EXPIRE_HOURS<0` 无论 bootstrap 是否启用均启动失败，`=0` 只表示不因历史时间过期。时间戳缺失、恰到过期边界或晚于当前数据库 UTC 时间均无效，未来时间不能因 expireHours=0 放行，且拒绝时无 attempt 审计、Adapter 或数据库副作用。result 审计固定关联 `email_admin_verify_bootstrap_receipt` 及 receipt 内部 ID。仅通过动态覆盖得到 bootstrap 权限但不直接关联 admin 角色的普通用户仍返回 `403/40003「无权限」`。
- 401 的现有 JWT 文案、`403/40003「无权限」`、`403/40003「请先完成手机号认证」`、`409/40900` 模板/冲突文案、`422/51001「邮件模板变量不完整」`、`502/51002` 上游文案、`503/51003「邮件发送服务未就绪」` 和 `500/50000「系统内部错误」` 均只供运维客户端按 SSOT 处理，不新增前端页面分支。

### 5.2.8 公开验证码来源 IP（前端不传来源头）

公开邮件/手机验证码发码和密码重置的来源 IP 由应用与受信反向代理判定。浏览器、用户控制台和管理后台均不得主动发送、复制或信任 `X-Real-IP`、`X-Forwarded-For`、`Forwarded`；这些 Header 不是前端参数，也不能用于规避 IP 限流。

- 全局 `TRUSTED_PROXY_IPS` 与 metrics 的 `INTERNAL_TRUSTED_PROXY_IPS` 分离。前者为空表示合法直连模式，只用 `RemoteAddr`；非空时每个逗号分隔项 trim 后必须是精确 IP 或 CIDR，空项、非法项或 IPv6 zone 会使应用启动失败或 ready 不通过。
- 非 trusted 连接始终只用 `RemoteAddr`，忽略全部来源 Header。trusted proxy 连接必须携带代理覆盖的恰好一个合法单值 `X-Real-IP`；缺失、空值、非法、逗号多值或重复 Header 固定 `403/40003「无权限」`。
- Header 拒绝不消耗发码限流次数，不进入验证码业务服务，外部 Sender 调用为 0；来源解析器运行时不可用时，邮件发码固定 `503/51003「邮件发送服务未就绪」`，手机发码与密码重置固定 `503/50300「验证码服务当前不可用」`，同样无副作用。
- 应用永不使用 XFF 做安全判定；Nginx 覆盖 `X-Real-IP=$remote_addr` 并删除 XFF/Forwarded。前端遇到 403 或 503 按下述冻结展示口径处理，不自行构造来源 Header 重试。

展示口径：手机发码组件对 `50300` 使用既有稳定提示“短信功能当前不可用”，不直接透传后端文案；密码重置
提交和其他非短信发送入口使用后端安全文案或本地保守 fallback。两者都不得进入下一步、启动倒计时或模拟成功。

邮件入口既有实现与前端契约保持不变；短信阶段 4 已将同一来源规则接入手机发码和密码重置。本地自动化通过，
真实 Redis、HTTP、Linux race 与 Nginx 部署配置仍须按各自验收证据标记，不能由本文档替代。

### 5.2.9 内部邮件 Adapter 指标（非前端消费，Phase 1 metrics delta 待 QA/PM 复签）

`GET /api/internal/metrics` 只供内部监控系统抓取，用户控制台和管理后台均不得调用、代理、展示或缓存该端点，也不得在前端代码、构建产物或运行时配置中保存 `X-Internal-Token`。

契约摘要仅供前后端划清边界：

- 仅 `GET`；其他方法（包括 `HEAD`）返回 405 并带 `Allow: GET`。
- 200 响应使用 Prometheus text 0.0.4，并带 `Cache-Control: no-store`、`X-Content-Type-Options: nosniff`。
- `INTERNAL_API_TOKEN` 按不 trim 的原始 UTF-8 值校验：无首尾空白、至少 32 字节，并大小写不敏感拒绝空值及 `REPLACE_WITH_INTERNAL_API_TOKEN/CHANGE_ME/CHANGEME/DEFAULT/SECRET/TEST`；请求 Token 按原始字节常量时间比较且不得记录日志。`INTERNAL_ALLOWED_IPS`、`INTERNAL_TRUSTED_PROXY_IPS` 均须为非空、无空项且每项 trim 后是精确 IP 或 CIDR，任一非法即失败关闭。
- 来源先解析 `RemoteAddr`。只有其命中 trusted proxy 时才要求并信任代理覆盖的恰好一个合法 `X-Real-IP`；否则始终只用 `RemoteAddr`，任何来源头不能改写结果。应用永不读取 `X-Forwarded-For`。Token 或最终来源 IP 任一校验失败只返回 `403/40003「无权限」`。
- 只输出 `email_adapter_calls_total`。`operation` 固定为 `query_templates/describe_template/send_mail`；前两者仅配 `scene=template_sync`，`send_mail` 仅配五个固定邮件 scene；`result` 固定为 `accepted/failed/timeout`。21 个封闭序列启动即以 0 输出，进程内单调递增，重启允许归零。
- 禁止任何高基数或敏感 label，禁止输出其他指标族。反向代理仅允许监控网络访问，删除 XFF/Forwarded 并覆盖 `X-Real-IP` 单值；不能替代或绕过应用双闸。

该 delta 已落档 QA 阻断修订，尚待 QA/PM 书面复签，且没有完成实现或环境验收；不得据此标记前端、后端或监控接入完成。

### 5.3 短信模板管理（阶段 2 后端契约，阶段 3 管理后台对接）

阶段 2 已由 PR #315 合并至 `main@9e50ee1`。阶段 3 在管理后台新增 `/message/sms-templates` 页面、菜单、路由和交互，必须以 `docs/full-api-design.md` 的“阿里云短信验证码阶段 2 管理 API 契约”为 SSOT，不得从环境变量读取或硬编码模板编码、签名、白名单和 AccessKey。

九个接口：

| 方法 | 路径 | 权限 |
|---|---|---|
| GET | `/api/admin/sms/summary` | `sms:template:view` |
| GET | `/api/admin/sms/templates` | `sms:template:view` |
| GET | `/api/admin/sms/templates/{id}` | `sms:template:view` |
| POST | `/api/admin/sms/templates/sync` | `sms:template:sync` |
| GET | `/api/admin/sms/scenes` | `sms:template:view` |
| PUT | `/api/admin/sms/scenes/{scene}` | `sms:template:manage` |
| PATCH | `/api/admin/sms/templates/{id}/status` | `sms:template:manage` |
| POST | `/api/admin/sms/templates/{id}/test-send` | `sms:template:test` |
| GET | `/api/admin/sms/send-logs` | `sms:template:view` |

全部接口需要管理员登录和有效的手机+邮箱双重认证。列表遵循 D-95 `{items,page,page_size,total}`。场景更新只提交 `{template_id,enabled,version}`，模板启停只提交 `{enabled,version}`，禁止提交 `sign_name`。五个场景必须分别选择独立模板；同一模板已被其他启用场景使用时，后端返回 `409/40900「该模板已绑定其他短信场景，请为当前场景选择独立模板」`，阶段 3 页面必须保留当前表单并引导重新选择。测试发送请求体为 `{scene,phone}`，并必须携带 `Idempotency-Key`；完整手机号只能存在于单次请求内存中，页面不得缓存、日志或埋点记录。

`submit_status=accepted` 的界面语义固定为“供应商已受理/提交成功”，不得显示“发送成功”“送达成功”或“用户已收到”。阶段 3 对接时必须分别处理 `40000/40001/40003/40031/40400/40900/42900/50200/50300`，其中版本冲突应刷新最新配置，频率限制按 `Retry-After` 展示剩余时间。

阶段 3 前端约束：

- `src/types/sms.ts` 必须覆盖概览、模板、场景、同步结果、启停结果、测试提交和发送日志的全部 DTO；字段保持 snake_case。
- `src/api/sms.ts` 必须消费上表九个接口；同步接口严格为空 body，后端没有同步幂等 Header 契约，页面以 loading 防止重复点击；仅测试提交携带 `Idempotency-Key`。
- 模板候选只允许 `approved`、`verification`、变量精确为 `code` 且本地启用；同一启用模板不能选择给另一启用场景。
- `409/40900` 场景写入失败时重新加载最新版本但保留管理员尚未提交成功的模板与启停选择，禁止静默覆盖。
- 完整手机号只能位于测试弹窗响应式内存，弹窗关闭或提交成功立即清空；禁止写入浏览器持久层、URL、控制台和错误详情。
- 页面必须覆盖加载、空数据、错误、无权限和正常五态，并适配 1440/1024/768/390 像素宽度。

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

**GET** `/api/admin/products/{id}/access` *(需 `product:view` 权限)*

回显该商品**已配置**的角色访问规则，用于打开"配置访问规则"对话框时勾选回显。`data` 为 `{ items: [...] }`（与 PATCH 写入 body 键名对称），无配置时 `items` 为 `[]`：
```json
{
  "items": [
    { "id": 10, "product_id": 1, "role_id": 1, "can_view": true, "can_buy": true, "can_use": true, "created_at": "2026-06-26T10:00:00Z", "updated_at": "2026-06-26T10:00:00Z" },
    { "id": 11, "product_id": 1, "role_id": 2, "can_view": true, "can_buy": false, "can_use": false, "created_at": "2026-06-26T10:00:00Z", "updated_at": "2026-06-26T10:00:00Z" }
  ]
}
```

**GET** `/api/admin/products/{id}/prices` *(需 `product:view` 权限)*

回显该商品**所有套餐**已配置的价格（跨套餐扁平列表，用 `product_plan_id` 区分归属），用于"访问与价格"页回显。`data` 为 `{ items: [...] }`，无配置时 `items` 为 `[]`：
```json
{
  "items": [
    { "id": 20, "product_plan_id": 1, "role_id": null, "membership_level_id": null, "price_amount": "10.000000", "currency": "CNY", "created_at": "2026-06-26T10:00:00Z", "updated_at": "2026-06-26T10:00:00Z" },
    { "id": 21, "product_plan_id": 1, "role_id": 2, "membership_level_id": null, "price_amount": "8.000000", "currency": "CNY", "created_at": "2026-06-26T10:00:00Z", "updated_at": "2026-06-26T10:00:00Z" }
  ]
}
```

> 说明：`access`/`prices` 的 GET 回显与 PATCH 覆盖写入键名对称（均为 `items`），前端"加载已配置项 → 勾选/填值 → 全量提交"即可闭环；该回显接口非分页，直接返回全量 `items`。

> **前端注意（PR #270 验收实测，2026-06-26）**：
> 1. **`price_amount` 按数值解析展示，不要依赖固定小数位数**。该字段类型为字符串（符合契约），但后端 decimal 序列化会去除尾随零——例如写入 `"50.000000"`，回显为 `"50"`；写入 `"8.00"` 回显为 `"8"`。前端展示/比较时应先转成数值（如 `Number(price_amount)`）再格式化，不要假定返回固定 6 位小数。
> 2. **不存在的商品 id 返回 HTTP 200 + `items: []`**（两个 GET 接口均不做商品存在性校验，符合现有约定，docs 未强制 404）。前端不应以「非空 items」作为商品存在与否的判断依据；商品是否存在请以 `GET /api/admin/products/{id}` 为准。

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
{ "code": "<6位验证码>" }
```

**POST** `/api/admin/auth/verify-email`
```json
{ "code": "<6位验证码>" }
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
{ "id": 1, "code": "netdisk-basic", "name": "基础网盘", "type": "netdisk", "description": "...", "icon_url": "https://...", "access_url": "https://app.example.com", "status": "active", "created_at": "..." }
```

> 白名单字段：`{id, code, name, type, description, icon_url, access_url, status, created_at}`。
> **`access_url`**：用户「进入应用」跳转目标（面向用户，已配置才返回，未配为 null）。前端可据此在已购/有权应用上渲染「进入应用」按钮；为空则不显示入口。
> **不含 `callback_url` / `adapter_config_json`（仅管理端 AP2/AP3 `GET /api/admin/apps`、`GET /api/admin/apps/{id}` 返回），亦不含 `updated_at`。** 这两个字段属内部回调地址与非交易配置（可能含集成参数/内网地址/密钥），用户端禁止下发。

### 13.1.1 进入应用（阶段二 SSO 一次性票据）

**POST** `/api/apps/{id}/launch` *(需登录)* — 用户「进入应用」时由前端调用，校验使用权后签发一次性票据。

请求体（应用 ID 走路径，body 可选）：
```json
{ "entitlement_id": 123 }
```
- `entitlement_id`（可选，整数）：用户**本次选择进入的那个套餐/权益的 ID**（即 `/api/my/entitlements` 列表项的 `entitlement_id`）。
- **多套餐必传**：当用户在同一应用下持有多个套餐（多条权益）时，必须把用户点的那一条的 `entitlement_id` 带上，否则应用侧无法区分、只会识别到第一个套餐。
- 单套餐或旧逻辑可不传 / 传 0，后端回退取任一 active 资产（行为不变）。

响应 `data`：
```json
{ "access_url": "https://app.example.com", "launch_ticket": "lt_xxxxxxxx", "expires_in": 60 }
```

前端流程（取代「直接打开 access_url」的阶段一做法，用于需可信身份的应用）：
1. 用户在「我的资产/某个套餐」上点「进入应用」→ 调 `POST /api/apps/{id}/launch`，body 带该套餐的 `entitlement_id`；
2. 拿到 `{access_url, launch_ticket}` → 浏览器跳转 `{access_url}?ticket={launch_ticket}`（票据 60s 有效、一次性，注意日志脱敏）；
3. 应用方后端用 `ticket` 调内部接口换身份（票据里已含 `entitlement_id`），完成免登并对应到正确套餐。

> **多套餐 UI 提示**：若入口是「应用卡片」级别（用户未先选套餐），前端需先让用户选择要进入的套餐再调 launch；若入口本就在「我的资产」某条记录上，直接取该条的 `entitlement_id` 即可。

错误码：`40400` 应用不存在/未开放入口（不显示按钮或提示未开放）；`40003` 无使用权（提示先购买/开通）或所选套餐权益无效/不属于本人（提示重新选择）。

> `POST /api/internal/app-launch/verify` 是**应用后端**用的内部接口（`X-Internal-Token` + IP 白名单），**前端不调用**，详见 `full-api-design.md` §5.3.1。

### 13.2 管理端应用 CRUD

- **GET** `/api/admin/apps?status=&type=&page=1&page_size=20` *(需 `app:manage`)* → 扁平分页 `{ items, page, page_size, total }`
- **GET** `/api/admin/apps/{id}` *(需 `app:manage`)* → 单个应用对象
- **POST** `/api/admin/apps` *(需 `app:manage`)*
  ```json
  { "code": "netdisk-basic", "name": "基础网盘", "type": "netdisk", "description": "...", "icon_url": "https://...", "access_url": "https://app.example.com", "callback_url": "https://...", "adapter_config_json": null }
  ```
- **PATCH** `/api/admin/apps/{id}` *(需 `app:manage`)* → 可改 name/type/description/icon_url/`access_url`/callback_url/adapter_config_json/`status`(draft/active/inactive/archived)
  > `access_url`（用户访问入口）写入校验：**必须 `https://`**，拒绝 `http`/`javascript:`/`data:` 等危险或不安全 scheme，长度 ≤512；传空串表示清空入口。校验失败返回 `40000`。

### 13.3 管理端适配器

- **GET** `/api/admin/app-adapters?status=&page=1&page_size=20` *(需 `app:manage`)* → **扁平分页** `{ items, page, page_size, total }`（`page_size` 默认 20、上限 100，可选 `status` 过滤；前端按分页处理，勿当作不分页 `{items}`）
- **POST** `/api/admin/app-adapters` *(需 `app:manage`)*
  ```json
  { "app_code": "netdisk-basic", "app_name": "基础网盘", "app_type": "netdisk", "adapter_type": "internal", "service_name": "netdisk-svc", "callback_url": "https://...", "supported_actions_json": "[\"provision\",\"renew\",\"cancel\"]", "usage_event_types_json": "[\"storage_gb\"]" }
  ```
- **PATCH** `/api/admin/app-adapters/{id}` *(需 `app:manage`)* → 可改各字段及 `status`(active/inactive)

---

## 十四、Token 网关模块（第二阶段）

### G4 管理页面接口规划

管理后台新增“安全治理”“资源策略”“预算策略”“补偿任务”四个页面；页面和列表读取统一要求 `ai_gateway:view`，写按钮再分别检查 `ai_gateway:safety_manage`、`ai_gateway:resource_manage`、`ai_gateway:budget_manage`、`ai_gateway:reconcile_manage`。接口清单和字段以 `docs/full-api-design.md` 的 G4 节为准；所有列表使用 `{items,page,page_size,total}`。策略创建只提交 `rules`，必须提示并校验七类安全底线完整覆盖，不得显示可编辑的拒绝文案。补偿页允许有权限人员对 Outbox dead 事件填写原因后按原 event_id 重试。策略、处置、预算和补偿写操作必须展示提交中、成功、失败和 409 冲突状态；409 后重新拉取记录，不能覆盖更新。

输出审核阻断且 Usage 暂缺时，对账详情页使用 `POST /api/admin/token/billing/content-policy/{request_id}/resolve` 补录四类 Token Usage。按钮只对 `ai_gateway:reconcile_manage` 且已完成管理员二次认证的人员显示；确认框明确展示“用户消费 0 元、平台承担上游成本”。成功后刷新请求、钱包 hold、Usage 和 Outbox 状态，409 时禁止覆盖提交。G4 当前只交付该接口契约，Vue 页面仍属后续前端阶段。

用户控制台先调用 `GET /api/token/safety/events?page=1&page_size=20` 查询本人最小化事件，再调用 `POST /api/token/safety/appeals`，body 为 `{event_id,reason}`。前端不得收集或回传完整违规提示词，只使用事件 ID 和用户填写的申诉理由；后端会再次验证事件归属。

桌面端使用表格和侧栏编辑；平板压缩操作列；手机端改为单列记录和底部抽屉。危险操作使用确认对话框，按钮必须有可见反馈。当前 G4 分支只交付接口，不代表这些 Vue 页面已完成。

> 模块：`token_gateway`（后端丁），sk 鉴权由后端甲提供。
> 计费口径（2026-06-21 决策）：**按量（token 数）+ 按次（调用次数）+ 套餐（预付 token 额度）三种并存**；按量/按次为后付扣钱包，套餐为预付扣 entitlement 额度。Agent/Skill/插件均免费，唯一收费点是模型 token 调用。
> 状态标记：✅ 已实现并合并 main ｜ 🔜 待实现（含归属）。前端按状态决定可对接时间。
> 站内聊天工作台的 Agent 对话端点（tool-use 编排）契约见 §14.8（待实现）。
> G3 增量：公开 `/api/token/chat/completions` 与 `/v1/chat/completions` 已在 Project SK + RequestOrchestrator 上启用人民币钱包按量计费。`max_tokens` 可选；缺省时后端使用平台兜底上限与模型上限的较小值报价和预占，再执行并按可信 Usage 一次结算。G3 只允许单候选 `n=1`，`n>1` 在调用上游前拒绝。G3 不使用积分或套餐额度。

### 本模块专用错误码（chat 转发）

| code | HTTP | 含义 |
|------|------|------|
| 40003 | 403 | 套餐额度归属不符（prepaid sk 绑定的权益不属于该用户 / 权益已失效） |
| 40300 | 403 | 未开通 token 服务，无法调用（区别于通用 40003 无权限） |
| 60001 | 402 | 钱包余额不足（postpaid 预扣保证金前置闸拒绝） |
| 60005 | 402 | 权益额度不足（prepaid 套餐额度耗尽，前置闸拒绝，未转发上游；勿用 60002） |
| 50200 | 502 | 上游服务调用失败 |
| 50300 | 503 | 上游渠道不可用（未配置可用渠道 / 渠道停用） |
| 50301 | 503 | 系统繁忙，请稍后重试（高并发钱包乐观锁冲突重试耗尽，**可重试**；D-M2-02，区别于 60001 余额不足） |
| 40901 | 409 | 相同 Idempotency-Key 对应的请求内容不一致 |
| 40010 | 400 | 显式 `max_tokens` 非法、超过模型上限、`n` 不为 1 或无法计算最大费用 |
| 20201 | 202 | 结果正在结算，保留 request_id 继续查询 |
| 50010 | 500 | 计费异常，已进入人工对账 |
| 50310 | 503 | 无有效价格或成本过期 |
| 50311 | 503 | 毛利保护暂停接单 |
| 50312 | 503 | 钱包预占事务失败，可稍后重试 |

### 鉴权说明（双模式）

- **G2 Project/Key 管理**：只允许登录态 JWT。
- **G2 公开 chat**：必须使用 Project SK；JWT 不能绕过 Project 归属进入正式文字链。
- **models / 历史 usage**：仍支持双模式凭证：
  - 登录态 JWT：`Authorization: Bearer <access_token>`（✅ 当前已支持）
  - 平台 API Key（sk）：`Authorization: Bearer sk-molin-xxxx`（🔜 后端甲 sk 系统上线后支持，外部程序/Agent 用）
  - 两条路最终都解析出 `user_id`，后续门禁/计费逻辑一致。
- **管理端**：JWT + `token:manage` 权限 + 管理员双重认证。

---

### 14.1 列出可用模型（用户端）✅

- **GET** `/api/token/models?modality=&page=1&page_size=20` *(登录态 / sk)*
- 仅返回已上架（active）模型的公开精简字段，供对话页选择；不含渠道/上游/商品等内部路由字段。
- 可选筛选：`modality`（chat/image/audio/video，本期仅 chat）。
- 响应 `data`：**扁平分页** `{ items, page, page_size, total }`（与实现一致，S2-测1 实测校准；前端按分页处理）
  ```json
  {
    "items": [
      { "logical_model_code": "gpt-4o", "display_name": "GPT-4o", "modality": "chat" },
      { "logical_model_code": "deepseek-chat", "display_name": "DeepSeek Chat", "modality": "chat" }
    ],
    "page": 1, "page_size": 20, "total": 2
  }
  ```

### 14.2 OpenAI 兼容对话转发（用户端）✅

- **POST** `/api/token/chat/completions` 或 `/v1/chat/completions` *(G3：Project SK + 人民币钱包)*
- **GET** `/api/token/requests/{request_id}` 或 `/v1/requests/{request_id}` *(G3：查询当前请求执行与计费状态，只允许原 Project SK)*
- Header 可带一个 `Idempotency-Key`。重复 Header、逗号多值、空值或超过 191 字节返回 `400/40000`；相同 Key、相同请求指纹返回 HTTP 202 和已有 `{request_id,execution_status,billing_status,existing:true}`，不重复调用上游；不同指纹返回 `409/40901`。
- 若服务端已确认请求在网络失败前从未写出上游并释放资金，客户端复用同一 `Idempotency-Key` 会创建新的 `request_id` 并安全重试；请求是否已发出不明确时仍只返回原状态。
- 请求体 = 标准 OpenAI Chat Completions 报文，门面近似纯透传，**`model` 与至少一条非空文字 `messages` 必填**（`model` 填 14.1 的 `logical_model_code`）；`max_tokens` 可选，缺省由服务端采用保守兜底值；G3 只允许 `n=1`；G2 拒绝图片、音频等多模态消息，`stream=true` 时走 SSE。
  ```json
  {
    "model": "deepseek-chat",
    "messages": [{ "role": "user", "content": "你好" }],
    "max_tokens": 1024,
    "stream": true
  }
  ```
- **非流式**（`stream=false`/缺省）：原样透传上游 OpenAI 响应体（`choices`/`usage` 等），HTTP 200。
- **流式**（`stream=true`）：`Content-Type: text/event-stream`，服务端按有界审核段输出，不保证上游单个 chunk 立即透传；账本 Finalize 成功后才发送 `data: [DONE]`。客户端断连后后台继续读取可确定的尾部 Usage。
- **前置错误**（尚未开始透传时）：返回统一 JSON `{code,error,message,data}`。`error` 为 G3 稳定字符串分类；既有非财务错误可能省略该字段。
- G3 计量：确定结果且完整一致的 Usage 按价格快照结算；缺失、不一致、结果未知或 SSE 未正常结束时返回 202 或已有待结算状态，即使已看到中间 Usage 也不实扣。`billing_status` 可能为 `held/settlement_pending/settled/released/exception`；**对话内容不落明文日志**。
- SSE 已开始后，待结算或异常通过 `event: molin.status` 通知，`data` 包含 `request_id` 与 `error`。前端收到后停止展示“已完成”，并调用请求状态接口刷新；不得把 `settlement_pending` 显示为免费或已退款。
- G3 暂不提供人工对账 UI；后台已提供 `POST /api/admin/token/billing/exceptions/{request_id}/resolve`，后续管理页面只能在 `token:manage` 和管理员二次认证通过后调用。

### 14.3 我的用量（用户端）🔜（后端丁）

- **GET** `/api/token/usage?model=&start=&end=&page=1&page_size=20` *(登录态 / sk)*
- 查本人 token 调用流水与消费，**扁平分页** `{ items, page, page_size, total }`。
- 可选筛选：`model`（logical_model_code）、`start`/`end`（时间范围，RFC3339）。
- `items[]` 字段：
  ```json
  {
    "request_id": "req_xxx",
    "logical_model_code": "deepseek-chat",
    "modality": "chat",
    "input_tokens": 12,
    "output_tokens": 220,
    "total_tokens": 232,
    "sale_amount": "0.003480",
    "is_stream": true,
    "status": "success",
    "error_code": null,
    "created_at": "2026-06-20T10:00:00Z"
  }
  ```
- 说明：`api_key_id` 为内部字段，用户端不返回；登录态调用本就无 sk。

### 14.4 平台 API Key（sk）管理（用户端）🔜（后端甲）

> 沿用 Refresh Token「只存 HMAC、明文只回一次」模式。`billing_mode`：`postpaid`（按量/按次扣钱包）/ `prepaid`（套餐预付，绑 entitlement 额度）。一般由「开通按量服务 / 购买套餐」后端自动签发，前端展示与吊销为主。

- **POST** `/api/keys` *(登录态)* — 创建 sk
  - 请求：`{ "name": "我的脚本", "model_scope": ["deepseek-chat"] }`（`model_scope` 可选，缺省=不限模型；`billing_mode`/`source_id` 由后端按购买上下文决定，前端通常不传）
  - 响应：**明文 `secret_key` 仅本次返回一次，请前端提示用户立即保存**
    ```json
    { "id": 10, "name": "我的脚本", "key_prefix": "sk-molin-AbCd", "secret_key": "sk-molin-AbCd....完整明文", "billing_mode": "postpaid", "status": "active", "created_at": "2026-06-21T10:00:00Z" }
    ```
- **GET** `/api/keys` *(登录态)* — 列出本人 sk，**扁平分页**；只回 `key_prefix`，绝不回明文/hash
  ```json
  { "id": 10, "name": "我的脚本", "key_prefix": "sk-molin-AbCd", "billing_mode": "postpaid", "model_scope": ["deepseek-chat"], "status": "active", "last_used_at": "2026-06-21T11:00:00Z", "created_at": "2026-06-21T10:00:00Z" }
  ```
- **DELETE** `/api/keys/{id}` *(登录态)* — 吊销 sk（`status=revoked`，立即失效）
- 联动：用户被封禁 → 名下所有 sk 失效。

### 14.4A Project 与 Project SK（G2）✅

Project 管理接口只用 JWT：

```text
POST   /api/token/projects
GET    /api/token/projects?page=1&page_size=20
GET    /api/token/projects/{id}
PATCH  /api/token/projects/{id}
```

- 创建：`{"name":"我的服务"}`。
- 更新：`{"name":"新名称","status":"active|suspended|archived"}`，字段均可选。
- 停用/归档不物理删除；停用后其 Project SK 不能调用模型。
- 列表响应为 `{items,page,page_size,total}`。

Project SK 接口只用 JWT：

```text
POST   /api/token/projects/{id}/keys
GET    /api/token/projects/{id}/keys
POST   /api/token/projects/{id}/keys/{key_id}/rotate
DELETE /api/token/projects/{id}/keys/{key_id}
```

创建示例：

```json
{
  "name": "服务端调用",
  "scope_mode": "allowlist",
  "model_codes": ["molin/qwen-turbo"],
  "expires_at": null
}
```

- `scope_mode` 缺省为 `allowlist`；空 `model_codes` 表示拒绝全部模型。
- 全模型必须显式提交 `scope_mode=all`，此时 `model_codes` 必须为空。
- 创建和轮换响应中的 `secret_key` 仅出现一次；前端必须立即展示保存提示，离开后不可找回。
- 创建、轮换和吊销会写入只含内部 ID、权限模式和模型代码的审计摘要，不记录完整 SK 或 HMAC Secret。
- 列表返回 `id,project_id,name,key_prefix,scope_mode,model_codes,status,expires_at,last_used_at,created_at`，不返回明文和 hash。
- `/v1/models` 和 `/api/token/models` 会按当前 Project SK 的权限过滤。

---

### 14.5 渠道管理（管理端）✅

> 需 `token:manage` + 管理员双重认证。安全红线：请求可传 `api_key_plaintext`，**任何响应绝不返回 key**，用 `has_api_key` 表征是否已配置。

- **GET** `/api/admin/token/channels?page=1&page_size=20` → **扁平分页** `{ items, page, page_size, total }`
- **POST** `/api/admin/token/channels`
  ```json
  { "code": "deepseek", "name": "DeepSeek", "type": "openai_compatible", "base_url": "https://api.deepseek.com", "api_key_plaintext": "上游真实key", "status": "active", "priority": 10 }
  ```
  - `type` 缺省 `openai_compatible`；`status` 缺省 `active`。
- **GET** `/api/admin/token/channels/{id}` → 单条 `ChannelResp`
- **PATCH** `/api/admin/token/channels/{id}` → 各字段可选；`api_key_plaintext` 非空才重新加密覆盖，缺省/nil 不动已存 key
- **DELETE** `/api/admin/token/channels/{id}`
- 响应体 `ChannelResp`（无 key 字段）：
  ```json
  { "id": 1, "code": "deepseek", "name": "DeepSeek", "type": "openai_compatible", "base_url": "https://api.deepseek.com", "has_api_key": true, "status": "active", "priority": 10, "created_at": "...", "updated_at": "..." }
  ```

### 14.6 对外模型目录管理（管理端）✅

> 需 `token:manage` + 管理员双重认证。把对外 `logical_model_code` 路由到渠道 + 上游真实模型名。

- **GET** `/api/admin/token/models?page=1&page_size=20` → **扁平分页** `{ items, page, page_size, total }`
- **POST** `/api/admin/token/models`
  ```json
  { "logical_model_code": "deepseek-chat", "display_name": "DeepSeek Chat", "modality": "chat", "product_id": 8, "channel_id": 1, "upstream_model": "deepseek-chat", "status": "active", "sort_order": 10 }
  ```
  - `logical_model_code` 唯一（对外名）；`modality` 缺省 `chat`；`status` 缺省 `active`；`product_id` 关联 token 商品（控门禁），可空。
- **GET** `/api/admin/token/models/{id}` → 单条 `ModelResp`
- **PATCH** `/api/admin/token/models/{id}` → 各字段可选（指针，nil 不更新）
- **DELETE** `/api/admin/token/models/{id}`
- 响应体 `ModelResp`（含内部路由字段，与 14.1 公开视图区分）：
  ```json
  { "id": 5, "logical_model_code": "deepseek-chat", "display_name": "DeepSeek Chat", "modality": "chat", "product_id": 8, "channel_id": 1, "upstream_model": "deepseek-chat", "status": "active", "sort_order": 10, "created_at": "...", "updated_at": "..." }
  ```

### 14.7 全量用量（管理端）🔜（后端丁）

- **GET** `/api/admin/token/usage?user_id=&api_key_id=&model=&start=&end=&page=1&page_size=20` *(需 `token:manage`)*
- 全量 token 用量流水，**扁平分页** `{ items, page, page_size, total }`；可按 `user_id`/`api_key_id`/`model`/时间范围筛选。
- `items[]` 在 14.3 字段基础上额外含 `user_id`、`api_key_id`（可空）。

---

> 以下为多模型聊天工作台（M3，🔜 后端丁）。Agent / Skill / 插件均**免费**，仅模型 token 调用计费。后端契约见 `docs/backend-chat-workbench-contract.md`。

### 14.8 Agent 对话（站内聊天，tool-use 编排）✅ 后端就绪（S2-丁10）

- **POST** `/api/agents/{id}/chat` *(**仅登录态**；sk 不可调用本端点——sk 仅用于透传端点 §14.2)*
- 请求：
  ```json
  { "messages": [{ "role": "user", "content": "查一下今天的新闻并总结" }], "model": "deepseek-chat", "stream": true }
  ```
  - `messages` 必填（客户端自持完整对话历史，后端不落库存储对话内容）；`model` 可选，缺省用该 Agent 的 `default_model_code`；`stream=true` 走 SSE。
  - 可见性：仅官方 active 或本人自建 Agent 可调，否则 `40003`；Agent 不存在 `40404`。
- 行为：门面注入 Agent 人设（system）+ 绑定且 enabled 的 active skill/插件作为 `tools`，自动执行工具调用循环（默认上限 `MAX_ROUNDS=5`），返回最终答案。**与 §14.2 的区别**：14.2 是纯透传（开发者自理工具），本接口由门面编排工具。
- **流式（`stream:true`）SSE 事件**（`event: <type>\ndata: <json>\n\n`）：
  - `event: tool_call` → `{ "name": "web_search", "arguments": "{…}" }`（开始调用某工具）
  - `event: tool_result` → `{ "name": "web_search", "content": "…" }`（该工具返回；失败时 content 为「工具执行失败: …」，模型自行降级，不中断对话）
  - `event: message` → `{ "content": "最终答案文本", "finish_reason": "stop" | "max_rounds" }`（最终答案）
  - `event: error` → `{ "message": "…" }`（编排中途出错，已开始流式无法回退 HTTP 码时）
  - 末尾固定 `data: [DONE]`
  - 超 `MAX_ROUNDS` 仍未收敛：发 `message`（`finish_reason:"max_rounds"`，文案含「已达上限，已正常计费」）后 `[DONE]`。
- **非流式（`stream:false`）**：返回单条 JSON `{ "choices":[{ "message":{"role":"assistant","content":"…"}, "finish_reason":"stop" }] }`（中间工具事件不下发）。
- 计费：按 token 累加各轮 / 按次计 1（**一次提问算 1 次**，仅首轮计次）。Agent/skill/插件本身免费。
- 错误码（未开始流式时走 HTTP 码）：`40300`（无可用模型/未开通 token 服务）、`60001`（钱包余额不足）、`60005`（套餐额度不足）、`40003`（越权/套餐归属不符）、`50200`（上游失败）、`50300`（渠道不可用）、`50301`（系统繁忙可重试）。
- 内置 skill：`doc_read`（抓取 https 文档，SSRF 防护）已可用；`web_search` 占位（未配置服务商时返回工具错误，模型降级）。付费插件按 `daily_limit` 限每用户每日调用次数（超限当轮工具返回「已达上限」，不中断对话）。

### 14.9 Agent / 角色（用户端）✅ 后端就绪（S2-丁9）

> 列表均为扁平分页 `{items,page,page_size,total}`（顶层 `data`），支持 `?page=&page_size=`。

- **GET** `/api/agent-categories` *(登录态)* → Agent 分类列表（前端分类导航 Tab：办公/学习/商务/娱乐），仅 active，按 `sort_order` 升序。量小不分页，仍包 `{items}`：
  ```json
  { "items": [ { "code": "office", "name": "办公", "icon": "", "sort_order": 1, "status": "active" }, … ] }
  ```
- **GET** `/api/agents` *(登录态)* → 可用 Agent 列表：官方（official+active 且**按定向可见性命中当前用户**）+ 本人自建（全状态）。`items` 元素结构同详情。
  - 新增可选筛选 **`?category=office`** 按分类过滤（不传=不过滤；展示维度，不影响可见性/计费）。
  - **定向可见性**：官方 Agent 可被运营定向到指定分组/全局角色（见 `visible_scope`）。列表只返回对当前用户可见的官方 Agent；非目标受众看不到、也不能直连详情/对话。
- **GET** `/api/agents/{id}` *(登录态)* → 详情。仅本人自建、或对当前用户可见的官方 active 可见，否则 `40003`（定向不命中视同越权）。
  ```json
  {
    "id": 3, "code": null, "name": "新闻助手", "description": "...", "avatar": "https://...",
    "owner_type": "official", "owner_user_id": null,
    "system_prompt": "你是新闻助手", "default_model_code": "deepseek-chat",
    "category_code": "office", "category_name": "办公",
    "status": "active",
    "visible_scope": "all", "target_audience": null,
    "sort_order": 0,
    "skills":  [{ "id": 1, "code": "web_search", "name": "联网搜索" }],
    "plugins": [{ "id": 2, "code": "weather", "name": "天气查询" }],
    "created_at": "...", "updated_at": "..."
  }
  ```
  - `category_code`：所属分类编码，`null` = 未分类；`category_name`：联字典带出的分类名称（未分类/字典缺失为空串，前端可直接显示分类标签）。
  - `visible_scope`：`all`（全员可见，默认）/`groups`（指定分组，可再按组内角色）/`roles`（指定全局角色）。
  - `target_audience`：按 `visible_scope` 解释，`all` 时为 `null`；`groups` 时形如 `{"group_ids":[10,12],"group_roles":["admin"]}`（`group_roles` 缺省=组内任意角色）；`roles` 时形如 `{"role_codes":["vip","merchant"]}`。用户端一般只读官方 Agent 时用于展示，普通用户无需关注。
- **POST** `/api/agents` *(登录态)* — 用户自建 Agent（`owner_type` 强制 `user`，不可传 `code`）
  ```json
  { "name": "我的助手", "description": "", "avatar": "", "system_prompt": "你是…", "default_model_code": "deepseek-chat", "category_code": "office", "skill_ids": [1], "plugin_ids": [] }
  ```
  - `name` / `system_prompt` / `default_model_code` 必填（缺失 `40000`）；`skill_ids`/`plugin_ids` 仅可填 **active 官方** skill/插件，否则 `40000`。
  - `category_code` 可选：空/不传 = 未分类；非空须存在于分类字典（`GET /api/agent-categories` 的 `code`），否则 `40000`。返回创建后的 Agent 详情（HTTP 201）。
- **PATCH** `/api/agents/{id}` *(登录态)* — 仅本人自建可改（官方/他人 `40003`）。标量字段缺省不改；`skill_ids`/`plugin_ids` 传则**覆盖**对应绑定（传 `[]` = 清空，不传 = 保留）。`category_code` 传 `""` = 清为未分类，传非法 code = `40000`，不传 = 保留。
- **DELETE** `/api/agents/{id}` *(登录态)* — 仅本人自建可删（越权 `40003`），返回 `{"deleted":true}`。
- **GET** `/api/skills` *(登录态)* — 列 active skill 供自建绑定：`{ "id", "code", "name", "description", "category" }`（不回 `handler_key`）。
- **GET** `/api/plugins` *(登录态)* — 列 active 插件供自建绑定：`{ "id", "code", "name", "description", "is_paid" }`（**不回** endpoint/凭证/配额）。

### 14.10 Agent / Skill / 插件管理（管理端）✅ 后端就绪（S2-丁8）

> 列表均为扁平分页 `{items,page,page_size,total}`。错误码：`40000` 参数校验 / `40900` code 已存在 / `40400` 不存在 / `40003` 越权。

**Agent**（需 `agent:manage` + 双重认证）
- **GET** `/api/admin/agents`（`?owner_type=`，默认 official；`?status=`；`?category=` 按分类过滤；`?visible_scope=all|groups|roles` 按定向范围过滤，便于运营核对） / **GET** `/api/admin/agents/{id}` → 同 §14.9 详情结构（含 `category_code` / `category_name` / `visible_scope` / `target_audience`）。管理端 list/get **不做**可见性过滤（运营看全量）。
- **POST** `/api/admin/agents`（含 `code`、`category_code`、`skill_ids`、`plugin_ids`，以及可选定向字段）/ **PATCH** `/api/admin/agents/{id}`（标量指针 + `category_code` + `skill_ids`/`plugin_ids` 覆盖语义 + 可选定向字段）/ **DELETE** `/api/admin/agents/{id}`。
  - `category_code` 可选：空/不传 = 未分类；非空须存在于分类字典，否则 `40000`。PATCH 传 `""` = 清为未分类。
  - **定向可见性字段（可选）**：`visible_scope`（`all`/`groups`/`roles`）+ `group_ids`（`[]uint64`）+ `group_roles`（`["admin"|"member"]`）+ `role_codes`（`["vip",...]`）。后端按 `visible_scope` 组装为 `target_audience` 落库。
    - POST：`visible_scope` 不传/为空 = `all`（全员可见，向后兼容）。
    - PATCH：`visible_scope` 不传 = 保留原定向；传则**整体覆盖**（连同 group/role 字段一起重设，覆盖语义）。
    - 校验（均 `40000`）：`visible_scope` 非 `all`/`groups`/`roles`（`members`/`users` 为预留，暂拒绝）；`groups` 但 `group_ids` 为空；`group_roles` 含非 `admin`/`member`；`roles` 但 `role_codes` 为空；`group_ids` 含不存在分组 / `role_codes` 含不存在角色。
- **PUT** `/api/admin/agents/{id}/visibility` — **独立设置定向可见性**（覆盖语义，与 skills/plugins 绑定风格一致，便于前端单独改定向）。body：
  ```json
  { "visible_scope": "groups", "group_ids": [10], "group_roles": ["admin"] }
  ```
  - 仅对官方 Agent 生效（对自建/他人返回校验错误）；校验规则同上；返回更新后的 Agent 详情（含 `visible_scope` / `target_audience`）。
  - 例：`{"visible_scope":"roles","role_codes":["vip","merchant"]}`（按全局角色）；`{"visible_scope":"all"}`（恢复全员可见，`target_audience` 置 `null`）。
- **POST** `/api/admin/agents/{id}/skills`、**POST** `/api/admin/agents/{id}/plugins` — 绑定/解绑，**覆盖语义**，body `{ "ids": [1,2] }`（`[]` = 全部解绑），返回更新后的 Agent 详情。

**Agent 分类**（需 `agent:manage` + 双重认证）
- **GET** `/api/admin/agent-categories` → 全量分类（含 inactive），按 `sort_order` 升序：`{ "items": [ { "code","name","icon","sort_order","status" } ] }`。本期固定 4 类（办公/学习/商务/娱乐），暂不提供分类 CRUD。

**Skill**（需 `skill:manage` + 双重认证）
- **GET** `/api/admin/skills`（`?status=&category=`） / **GET** `/api/admin/skills/{id}`
- **POST** `/api/admin/skills`：`{ "code", "name", "description", "category", "tool_schema_json": {…}, "handler_key", "status" }`（`code`/`name`/`handler_key`/`tool_schema_json` 必填，`tool_schema_json` 须合法 JSON）
- **PATCH/DELETE** `/api/admin/skills/{id}`。响应含 `tool_schema_json`、`handler_key` 等完整字段。

**插件**（需 `plugin:manage` + 双重认证）
- **GET** `/api/admin/plugins`（`?status=`） / **GET** `/api/admin/plugins/{id}`
- **POST** `/api/admin/plugins`：`{ "code", "name", "description", "tool_schema_json": {…}, "endpoint_url", "auth_config", "timeout_ms", "is_paid", "daily_limit", "status" }`
  - `endpoint_url` 必须 **https** 且非内网/回环（否则 `40000`）；`timeout_ms` ≤ 30000；`auth_config` 为**明文鉴权配置**，加密落库后丢弃。
- **PATCH/DELETE** `/api/admin/plugins/{id}`：`auth_config` 传则重设（传 `""` 清空凭证）。
- 响应**绝不回凭证**，以 `has_auth`（bool）表征是否已配置：`{ "id","code","name","description","tool_schema_json","endpoint_url","has_auth","timeout_ms","is_paid","daily_limit","status",... }`。

### 14.11 MCP server 管理（第二种工具源）✅ 后端就绪（第三阶段·阶段三）

> MCP（Model Context Protocol）server 是「插件」之外的第二种工具源：接一个 server 即自动暴露它的一批工具到 Agent。与插件并存，互不替换。
> v1 范围：Streamable HTTP transport + tools 原语 + 静态鉴权（Bearer/header）。**权限码复用 `plugin:manage`**（不新增），管理端需双重认证。
> 列表扁平分页 `{items,page,page_size,total}`。错误码：`40000` 参数/SSRF（endpoint 非 https 或内网）/ `40900` code 已存在 / `40400` server/工具不存在 / `40003` 越权（绑非官方 Agent）/ `502`(`50200`) discover 连接/握手失败（不改 server 状态）。

**MCP server CRUD**（需 `plugin:manage` + 双重认证）
- **GET** `/api/admin/mcp-servers`（`?status=` 过滤）/ **GET** `/api/admin/mcp-servers/{id}`
- **POST** `/api/admin/mcp-servers`：`{ "code","name","description","endpoint_url","auth_config","timeout_ms","is_paid","daily_limit","status" }`
  - `code` 唯一，作工具命名空间前缀；`endpoint_url` 必须 **https** 且非内网/回环（否则 `40000`）；`timeout_ms` ≤ 30000（空默认 15000）；`auth_config` 为**明文鉴权配置**（如 `{"header":"Authorization","value":"Bearer xxx"}`），加密落库后丢弃。
  - `status` 空默认 **inactive**（新建后须 discover + 审核工具，再置 active 才会被编排使用）。
- **PATCH/DELETE** `/api/admin/mcp-servers/{id}`：`auth_config` 传则重设（传 `""` 清空凭证）。删除级联清工具快照。
- 响应**绝不回凭证**，以 `has_auth`（bool）表征：`{ "id","code","name","description","endpoint_url","has_auth","protocol_version","timeout_ms","is_paid","daily_limit","status","last_discovered_at","created_at","updated_at" }`。`protocol_version` / `last_discovered_at` 在 discover 后回填。

**工具发现与审核**（需 `plugin:manage` + 双重认证）
- **POST** `/api/admin/mcp-servers/{id}/discover` — 触发 `initialize` + `tools/list`，把发现的工具 upsert 到工具快照，回填 `protocol_version` / `last_discovered_at`。
  - 响应：`{ "protocol_version","discovered"(本次发现工具数),"changed"(新增或定义变更需重审的数),"tools":[<工具快照>] }`。
  - 定义变化（`schema_hash` 变）的工具会被**自动置未启用待重审**（挡 tool poisoning / rug-pull）。
  - 连接/握手失败 → `502`，**不改 server 状态**。
- **GET** `/api/admin/mcp-servers/{id}/tools` — 列该 server 全部工具快照（含未启用）：`{ "items":[ { "id","server_id","tool_name","description","input_schema_json","enabled","schema_hash","created_at","updated_at" } ] }`。
- **PATCH** `/api/admin/mcp-servers/{id}/tools/{toolId}` — 审核启用/停用单工具：body `{ "enabled": true|false }`，返回更新后的工具快照。**仅 `enabled=true` 的工具会暴露给编排**。

**Agent 绑定 MCP server**（需 `plugin:manage` + 双重认证）
- **POST** `/api/admin/agents/{id}/mcp-servers` — 覆盖式绑定（同 skills/plugins 风格），body `{ "ids": [1,2] }`（`[]` = 全部解绑），返回 `{ "bound": true }`。
  - **v1 仅官方 Agent 可绑 MCP**；绑用户自建 Agent 返回 `40003`。绑定后该 server 下所有 `enabled` 工具进入 Agent 工具集，编排时以 `mcp__{server_code}__{tool_name}` 命名暴露给模型（防与 skill/plugin 撞名）。

**用户端只读**（仅登录态）
- **GET** `/api/mcp-servers` — 仅 active server 精简视图（**不回 endpoint/凭证/配额**）：`{ "items":[ { "id","code","name","description","is_paid" } ], page,page_size,total }`。

> 计费：MCP 工具调用对用户**免费**（唯一收费=模型 token）；`is_paid=1` 的 server 仅按 `daily_limit` 做每用户每日防滥用限流（与插件共用通用计数表）。

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
| `token:manage` | Token 网关渠道/模型目录管理 + 全量用量（后端丁，需管理员双重认证） |
| `ai_gateway:view` | AI 网关治理只读列表（后端丁，需管理员双重认证） |
| `ai_gateway:safety_manage` | AI 网关安全策略、主体处置与申诉处理（后端丁，需管理员双重认证） |
| `ai_gateway:resource_manage` | AI 网关并发、RPM、TPM 资源策略（后端丁，需管理员双重认证） |
| `ai_gateway:budget_manage` | AI 网关 Project/SK 预算策略与临时增额（后端丁，需管理员双重认证） |
| `ai_gateway:reconcile_manage` | AI 网关补偿任务人工处置（后端丁，需管理员双重认证） |
| `agent:manage` | Agent（官方预设）管理 + skill/插件绑定（后端丁，需管理员双重认证） |
| `skill:manage` | Skill 内置能力管理（后端丁，需管理员双重认证） |
| `plugin:manage` | 外部插件管理 + **MCP server 管理**（第二种工具源，复用同权限码，后端丁，需管理员双重认证） |
| `email:template:view` | 查看邮件模板、场景绑定、同步、白名单与发送日志（后端甲，需管理员双重认证） |
| `email:template:manage` | 修改邮件场景绑定、维护测试邮箱白名单（后端甲，需管理员双重认证） |
| `email:template:sync` | 从 DirectMail 同步邮件模板（后端甲，需管理员双重认证） |
| `email:template:test` | 向白名单邮箱发送模板测试邮件（后端甲，需管理员双重认证） |
| `email:template:bootstrap` | 仅供内部运维一次性配置 `admin_verify`（需管理员 JWT、有效手机 MFA 与内部双闸；无前端入口） |

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
| `agent.visible_scope` | `all` / `groups` / `roles`（`members` / `users` 预留未启用） |
| `announcement.status` | `draft` / `published` / `offline` |
| `help_article.status` / `help_category.status` | `draft` / `published`（分类：`active` / `inactive`） |
| `application.status` | `draft` / `active` / `inactive` / `archived` |
| `app_adapter.status` | `active` / `inactive` |
| `provider` | `wechat` / `alipay` |
| `token_channel.status` | `active` / `inactive` |
| `token_channel.type` | `openai_compatible`（扩展点：`anthropic` / `gemini`，本期仅 openai_compatible） |
| `token_model.status` | `active` / `inactive` |
| `token_model.modality` | `chat` / `image` / `audio` / `video`（本期仅 chat） |
| `token_usage_log.status` | `success` / `failed` / `timeout` |
| `api_key.billing_mode` | `postpaid`（按量/按次扣钱包）/ `prepaid`（套餐预付，绑 entitlement 额度） |
| `api_key.status` | `active` / `revoked` |
| `billing usage_type`（token） | `input_tokens` / `output_tokens`（按量，unit=tokens）/ `calls`（按次，unit=count） |
| `agent.owner_type` | `official`（运营预设）/ `user`（用户自建） |
| `agent.status` / `skill.status` / `plugin.status` / `mcp_server.status` | `active` / `inactive`（mcp_server 新建默认 `inactive`） |
| `mcp_server_tool.enabled` | `true`（审核通过，暴露给编排）/ `false`（待审/停用，定义变更自动置 false） |
| `tool_daily_call_logs.tool_type` | `plugin` / `mcp`（通用工具每用户每日限流维度，收口替代 plugin_daily_call_logs） |
| `entitlement_type`（token 套餐） | `token_quota`（quota_unit=tokens，预付额度） |
| `email_scene` | `register` / `login` / `reset_password` / `bind_email` / `admin_verify` |
| `email_template.provider_status` | `draft` / `pending` / `approved` / `rejected`；供应商侧消失另用 `missing=true` |
| `verification_code.send_status` | `pending` / `accepted` / `failed` |
| `email_template_sync_run.status` | `running` / `succeeded` / `failed` |
| `email_test_allowlist.status` | `active` / `revoked` |
| `email_send_log.status` | `accepted` / `failed` |

## 短信验证码阶段 1 前端约定

- 手机发码成功必须同时校验 `sent=true`、`expires_in`、`business_request_id`、`submit_status=accepted`，契约不完整时不得进入倒计时或下一步。
- HTTP `503` / code `50300` 显示“短信功能当前不可用”；HTTP `502` / code `50200` 显示“短信发送失败，请稍后重试”。
- 手机验证码响应永远没有明文 `code`；前端不得读取或展示供应商请求标识。
- 找回密码等成功确认区域必须显示脱敏手机号，接口失败后按钮恢复可操作状态。
- 邮箱验证码继续使用 DirectMail 独立契约和非生产调试门禁，不受 `SMS_ENABLED` 影响。

## AI 网关 G5 管理工作台

页面入口：`/token/workbench`，读取权限 `ai_gateway:view`，并要求管理员双重认证。接口和状态机详见 `docs/ai-gateway-g5-development.md`。

- `GET /api/admin/token/overview`：支持 `from/to/model/channel_id/status`，返回请求量、成功率、Token、人民币销售额/成本/毛利、治理拒绝和配置异常；金额和比率为十进制字符串。
- `POST /api/admin/token/models/{id}/rollback`：从历史快照创建新的发布版本，不修改历史版本。
- `POST /api/admin/token/prices/{id}/rollback`：从历史价格复制新草稿，前端必须提示重新审批发布。
- `POST /api/admin/token/channels/{id}/health-check`：执行无密钥、无模型费用的 Bifrost `/health` 检测。
- 模型状态使用 `active/inactive`，路由状态使用 `active/disabled`，二者不得混用。
- 模型文档字段为 `intro_url/docs_url/quick_start_url`，仅允许 HTTP/HTTPS 静态网页。
- 价格金额全部按字符串接收和展示，前端不得转为浮点数后再提交。
- 路由编辑必须回传当前 `version_no`；收到 409 时提示刷新，禁止静默覆盖。
- 发布、审批、暂停、退役、下架均需二次确认；按钮在请求中进入 loading，失败后恢复可操作。
- 价格固定展示 `input_tokens/output_tokens/cached_tokens/reasoning_tokens` 四项，不在 G5 页面伪造图片或视频计量。
