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
- `Idempotency-Key`：购买、支付、充值、按量计费、资产开通、邮件模板同步、邮件测试发送等关键写操作必须传。

### 1.4 限流策略

所有请求经过以下中间件链：

```text
RequestID -> Logger -> Recovery -> RateLimit -> Auth（非公开接口）-> Permission（需权限接口）
```

限流规则：

| 接口分类 | 限制 | 说明 |
|---|---|---|
| 全局 | 1000 req/s / IP | 超出返回 429 |
| 注册 / 登录 / 验证码 | 10 req/min / IP，且 10 req/min / 账号 | 两个维度分别计数，任一超限即拒绝；邮箱账号键使用规范化值的 HMAC，不存完整邮箱 |
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
42901  登录失败次数超限，账号临时锁定（HTTP 423）
50000  系统内部错误
50200  上游模型服务失败（HTTP 502，token_gateway 透传上游失败）
50300  渠道不可用（HTTP 503，token_gateway 未配置可用渠道 / 渠道停用）
50301  系统繁忙/可重试（HTTP 503，token_gateway 乐观锁冲突重试耗尽，可重试；D-M2-02，区别于 60001 余额不足）
51001  邮件模板变量不完整（HTTP 422，缺少 Code 或 ExpireMinutes）
51002  邮件上游调用失败（HTTP 502，DirectMail 调用或 RAM 授权失败）
51003  邮件发送服务未就绪（HTTP 503，生产 Adapter 或必要配置不可用）
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
| expires_in | integer | 从响应时刻计算的剩余有效秒数；首次成功为 600，幂等重放不得重置有效期 |

成功统一返回 HTTP 200、`data={"sent":true,"expires_in":600}`。生产环境响应永远不得包含明文 `code`。
既有非生产调试模式只有在 `APP_ENV` 被显式设置，且经 trim+小写后精确属于 `local/development/dev/test/testing`，同时原调试开关经 trim 后精确等于小写 `true` 时，才可额外返回 `data.code`。`APP_ENV` 缺失、空白、`staging`、未知值或 `production` 均按生产安全边界失败关闭；调试开关的大写、混合大小写、数字及其他宽松布尔别名均不接受。该字段不属于稳定契约，
前端类型、页面、日志、审计和 telemetry 均不得读取或记录。Phase 2 不扩大这一既有调试边界。

邮件 OTP 发送补充约定：

- 邮件业务场景固定为 `register`、`login`、`reset_password`、`bind_email`、`admin_verify` 五种；公开端点仍只接受前三种，后两种必须走 D-96 认证态端点。
- auth 模块仅依赖稳定的 `EmailOTPSender.SendOTP(business_request_no, scene, recipient, code, expire_minutes)` 接口，不依赖阿里云 SDK、`TemplateId` 或供应商响应结构；业务请求号由服务端生成/复用，不要求现有客户端新增 Idempotency-Key Header。
- 业务字段固定映射到 DirectMail 模板变量：`code` → `Code`、`expire_minutes` → `ExpireMinutes`。大小写必须完全一致，不允许由调用方覆盖。
- 邮件验证码有效期固定为 10 分钟；发送前先落库并显式写 `verification_codes.send_status=pending`。只有 DirectMail 明确受理后才能原子更新为 `send_status=accepted, accepted_at=NOW()`，并写入 accepted 发送日志。
- 验证码校验仅接受 `send_status=accepted` 且未使用、未过期的记录。供应商拒绝、失败或超时都原子改为 `send_status=failed`；手机验证码由全新应用显式写 accepted，不经过邮件 Adapter。
- 邮件验证码、发送日志、模板镜像、场景绑定、模板同步记录、测试收件人白名单和 bootstrap 成功凭据涉及的 MySQL `DATETIME` 冻结为 UTC 墙上时间语义。即使现有全局 DSN 仍为 `loc=Local`，auth 邮件仓储也必须在写入、条件查询和扫描返回三处完成对称转换；进程位于 `Asia/Shanghai` 时不得把 UTC 时间额外加减八小时。`pending/accepted/failed/running/succeeded`、`expires_at/accepted_at/used_at/submitted_at/started_at/completed_at/created_at/updated_at/revoked_at/missing_since/provider_created_at/last_synced_at` 和验证码原子消费使用同一 UTC 时间域。模板同步比较供应商创建时间前必须先把数据库扫描值恢复为 UTC 墙钟；供应商内容与时间完全相同的连续同步必须计为 `unchanged`，不得重复递增模板版本。现有列为 MySQL `DATETIME`，契约精度固定到秒，不得用不可落库的纳秒决定过期或 stale；恰到 `expires_at` 边界即失效，`started_at` 必须严格早于五分钟前的秒级边界才可收敛 stale，部署前旧 failed 记录只能阻断至原真实十分钟窗口结束。本契约不授权修改全局 DSN，也不要求 migration。
- `accepted` 仅表示供应商同步受理，不等于最终送达。当前范围不接入投递回执 Webhook，不跟踪最终送达、打开率或点击率，页面和接口不得把 `accepted` 表述为“已送达”。
- 正式 OTP 与后台模板测试均从已冻结的本地模板镜像读取 `TemplateText`，只按固定映射在本地渲染 `Code` 与 `ExpireMinutes`，再由 DirectMail 生产 Adapter 调用 `SingleSendMail` 并提交 `Subject + HtmlBody`。`TemplateId` 仅用于场景绑定、发送日志和追踪：正式 OTP 从当前场景绑定解析，模板测试从 URL 中的平台模板镜像 ID 解析；不得硬编码，也不得作为 `SingleSendMail` 的 `Template.TemplateId/Template.TemplateData` 参数发送。
- 模板查询字段分开冻结：`QueryTemplateByParam` 列表只读 `TemplateId/TemplateName/TemplateStatus/CreateTime`；`DescTemplate` 详情只读非废弃字段 `RequestId/CreateTime/TemplateSubject/TemplateStatus/TemplateName/TemplateText`，不得以废弃的 TemplateNickName 为依据。
- `DescTemplate` 只有供应商 Code 精确命中显式 not-found 白名单时才能归为模板不存在；禁止用 contains、前后缀或模糊匹配。形似 not-found 的未知 Code 必须走通用上游失败并归一为安全 `other`，不得泄露原始 Code 或 Message。
- `APP_ENV=production` 时未配置生产 Adapter、凭据、发信地址、已审核模板或当前场景绑定，发送必须失败关闭；Mock Adapter 只能在上述显式非生产配置下启用，未知环境不得启用 Mock 或验证码调试回传。
- 收件邮箱必须是单个裸地址，拒绝逗号、多地址、显示名与 CR/LF；trim 后完整地址统一小写，再计算 HMAC。邮件验证码行只保存 `target_hash/target_masked`，`target_value` 为 null；手机号验证码继续使用 `target_value`。
- 000055 采用PM B全停机方案：保留旧code VARCHAR(64) NULL并新增code_hash；历史email OTP全部置failed/过期/已使用，逐行写不可关联随机占位hash与统一masked占位并清空target_value。仅全新应用读写code_hash。
- `migration_000055_permission_ownership` 仅是 migration-only 技术表，不属于五张邮件业务表。up 用四行记录权限/admin 绑定的预存 ownership，补缺项并回填最终 ID；down 仅按 created 标志精确清理本次新增记录，未知角色、用户覆盖或分组引用一律 fail-closed，写后断言通过才删除技术表。真实 MySQL 门禁必须验证五业务表+一技术表，down 后两类表均按预期清理且预存权限/绑定保留。
- 发布固定顺序：停止邮箱/手机 OTP 发码、OTP 校验、注册、登录流量 → 等待 10 分钟 → 停止全部 auth/API 实例 → 备份并验证可恢复 → 执行 000055 up → 部署全部新版本应用实例 → 核验 health、ready、应用版本、schema 版本与配置 → 恢复流量。回滚固定顺序：停止上述流量 → 等待 10 分钟 → 停止全部实例 → 备份并验证可恢复 → 先执行 000055 down（删除 `code_hash` 并保留 `code VARCHAR(64) NULL`）→ 部署旧版本应用 → 核验 health 与 schema → 恢复流量。禁止滚动部署，禁止新旧应用共存。
- `SingleSendMail` 固定提交 `FromAlias=墨灵`、`ClickTrace=0`，并以当前模板镜像 `subject` 提交 `Subject`；主题必须是有效 UTF-8、非空且不超过 100 个 Unicode 字符。`HtmlBody` 必须是有效 UTF-8、非空且按 UTF-8 字节不超过 80 KiB；别名允许由 `DIRECTMAIL_FROM_ALIAS` 配置覆盖但不得为空。
- 日志和审计禁止出现验证码、完整邮箱、AccessKey、完整 `TemplateData`、供应商原始响应；本次新增的邮件模板管理、测试发送和发送日志 API 响应同样禁止返回这些字段。敏感扫描应排除接收 email/code 的合法请求入参内存，只扫描响应、日志、审计、持久化和 telemetry。只允许记录内部日志 ID、场景、脱敏邮箱、业务请求号、阿里云 RequestId 和归一化安全失败原因。
- 所有管理写动作先写 `.attempt` 审计，失败则不执行；动作后写 `.result`，结果审计失败必须告警但不得把已生效动作返回为500。清理任务失败同样必须写运行日志以便告警。

正式邮件 OTP 的服务端幂等不新增客户端 Header。auth 层在原子限流/冷却窗口内为同一入口和目标生成或复用
`business_request_no`，并按以下固定 scope 计算 `idempotency_key_hash=HMAC-SHA256(business_request_no|scope, EMAIL_IDEMPOTENCY_SECRET)`：

```text
register       auth:register:email:{target_hmac}
login          auth:login:email:{target_hmac}
reset_password auth:reset_password:email:{target_hmac}
bind_email     auth:bind_email:user:{user_id}:email:{target_hmac}
admin_verify   auth:admin_verify:user:{admin_id}:email:{target_hmac}
```

请求指纹固定为规范化 `{endpoint,scene,target_hmac,purpose,expire_minutes,template_id,binding_version}` 的 SHA-256。
后端必须按 scope 获取 Redis 分布式锁，在 `verification_codes` 中原子创建或复用 business_request_no、scope、fingerprint；数据库条件更新仅用于 fencing 和唯一收敛，不得替代 Redis 锁。
同一冷却窗口内相同 scope+指纹且已 accepted 的重放返回同一业务结果，`sent=true` 且 expires_in 按原 expires_at 计算剩余秒数；
failed 重放返回原安全错误；仍 pending 的并发重放返回 `409/40900「邮件正在发送，请稍后重试」`。三者均不重新生成验证码、
不重置有效期、不再次调用供应商；同一业务请求号或
幂等键对应不同指纹返回 `409/40900`。冷却窗口结束后的明确新发码生成新的业务请求号。五个入口均执行同一规则。
同步running超过5分钟仅成为陈旧候选；收敛前必须竞争同一同步lease，原任务仍续租时返回冲突且不得标failed。同步事务首尾以run的running状态作fencing，最终更新RowsAffected不是1则回滚全部镜像。测试发送pending规则不变。

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
| sent | boolean | 是否已被短信供应商受理；固定为 `true` |
| expires_in | integer | 验证码有效秒数 |
| business_request_id | string | 平台业务请求标识，不是供应商原始请求标识 |
| submit_status | string | 固定为 `accepted`；只表示供应商受理，不表示送达 |

手机发码除入口 IP/用户限流外，还按“手机号 HMAC + scene”执行 Redis 原子 60 秒冷却；超限返回
`429/42900「操作过于频繁，请稍后再试」`，且不得生成验证码、写入发送记录或调用供应商。Redis
门禁不可用时失败关闭为 `503/50300`。同一手机号与场景连续五次校验失败后，在十分钟窗口内继续统一返回
`400/40000「验证码错误或已过期」`；成功消费后清除该场景的失败计数。限流键只保存手机号 HMAC，禁止保存完整手机号。
公开手机发码及密码重置的 IP 限流必须使用全局 `TRUSTED_PROXY_IPS` 来源解析器：非可信连接只认
`RemoteAddr`，忽略 `X-Real-IP`、`X-Forwarded-For` 和 `Forwarded`；可信代理只接受单值合法
`X-Real-IP`。可信代理来源头异常返回 `403/40003`，解析器不可用返回 `503/50300「验证码服务当前不可用」`，
均不得进入限流计数或业务处理。

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

### 2.4.1 邮箱验证码登录（Phase 1 delta，待 QA/PM 签署）

```text
POST /api/auth/login/email/code
```

`POST /api/auth/login/email` 继续作为邮箱+密码登录，路径、Body 和行为保持不变。新端点只接受严格 JSON Body：

```json
{
  "email": "user@example.com",
  "code": "<6位验证码>"
}
```

Body 必须且只能包含 `email`、`code`；缺少字段、空值、类型错误或任何额外字段统一返回
`400/40000「请求参数错误」`。后端只消费 `scene=login`、`send_status=accepted`、未使用、未过期且邮箱/验证码匹配的记录；
验证码验证与 `used_at` 条件更新必须在同一数据库事务中原子完成，并发请求只有一个能消费成功。验证码不匹配、scene 错误、
非 accepted、已使用、已过期或并发消费失败统一返回 `400/40000「验证码错误或已过期」`。

错误与 D-16：

- 邮箱未注册：`404/40404「邮箱未注册，请先注册」`。
- 账号禁用：`403/40003「账号已被禁用」`。
- 邮箱密码登录与邮箱验证码登录复用同一 D-16 邮箱失败计数。认证失败累计达到 5 次后锁定 15 分钟，锁定期返回
  `423/42901「登录失败次数过多，请15分钟后重试」`；锁定请求不得消费验证码或创建会话。
- 任一邮箱密码登录或邮箱验证码登录成功后清除该规范化邮箱的 D-16 失败计数。
- `scene=login` 发码继续同时执行 10 次/分钟/IP 与 10 次/分钟/规范化邮箱 HMAC，任一维度超限返回
  `429/42900「请求频率超限」`。

成功响应完全复用邮箱密码登录的 `LoginResp`（access_token、refresh_token、expires_in、user）。成功只创建一条新会话，
不吊销该用户的其他有效会话。普通登录签发的 Token 不写入或刷新管理员手机/邮箱 MFA 状态；管理员访问需要双重认证的接口时，
仍必须完成手机+邮箱 MFA，不能借邮箱验证码登录绕过。

本接口复用现有 `verification_codes` 与会话表，不新增数据库表或字段。本节仅为 Phase 1 契约 delta，尚待 QA/PM 书面签署，
不代表 Go 实现、环境或端到端验收通过。

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
发码成功统一返回 `data={"sent":true,"expires_in":600}`；生产环境永不返回 code。仅既有显式非生产调试模式可额外返回
`data.code`，该字段不属于稳定前端契约，也不得进入日志、审计、持久化或 telemetry。

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
该端点严格无 Body；请求携带额外 `email` 字段固定返回 `400/40000「请求参数错误」`。服务层若通过受控测试 fixture 注入非当前管理员绑定邮箱，固定返回 `403/40003「无权向该邮箱发送验证码」`。
发码成功统一返回 `data={"sent":true,"expires_in":600}`；生产环境永不返回 code。仅既有显式非生产调试模式可额外返回
`data.code`，该字段不属于稳定前端契约，也不得进入日志、审计、持久化或 telemetry。

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
若新手机号已被其他账号注册则返回 409（错误码 40900）。发码成功统一返回
`data={"sent":true,"expires_in":600}`；生产环境永不返回 code。仅既有显式非生产调试模式可额外返回 `data.code`，
该字段不属于稳定前端契约，也不得进入日志、审计、持久化或 telemetry。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| phone | string | 是 | 新手机号 |
| code | string | 是 | 新手机号收到的验证码（scene=bind_phone） |

返回 data：`null`（HTTP 200 表示修改成功，`phone_verified` 自动置为 true）。如果目标账号是管理员，服务端必须在同一条更新中把 `admin_phone_verified_at` 清空；新手机号不得继承旧手机号的管理员 MFA，管理员须向新手机号重新发送 `admin_verify` 验证码并完成认证。

### 2.18 修改邮箱

```text
PATCH /api/me/email
```

需要：Bearer Token。先向新邮箱发送验证码（scene=bind_email），再提交本接口。

D-96：发送验证码请调用 `POST /api/me/verification-codes/email`（需 Bearer Token），body 为 `{"email": "<新邮箱>"}`；
若新邮箱已被其他账号注册则返回 409（错误码 40900）。发码成功统一返回
`data={"sent":true,"expires_in":600}`；生产环境永不返回 code。仅既有显式非生产调试模式可额外返回 `data.code`，
该字段不属于稳定前端契约，也不得进入日志、审计、持久化或 telemetry。

`bind_email` 收件人只能来自当前登录用户此次换绑流程提交并校验的新邮箱。服务层若通过受控测试 fixture 注入其他流程或其他用户目标，固定返回 `403/40003「无权向该邮箱发送验证码」`。

Body 参数：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| email | string | 是 | 新邮箱地址 |
| code | string | 是 | 新邮箱收到的验证码（scene=bind_email） |

返回 data：`null`（HTTP 200 表示修改成功，`email_verified` 自动置为 true）。如果目标账号是管理员，服务端必须在同一条更新中把 `admin_email_verified_at` 清空；新邮箱不得继承旧邮箱的管理员 MFA，管理员须使用新邮箱重新完成认证。

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
| email | string | 否 | 邮箱；修改时清空目标账号 `admin_email_verified_at` |
| phone | string | 否 | 手机号；修改时清空目标账号 `admin_phone_verified_at` |
| status | string | 否 | 用户状态 |

返回 data：`updated`。管理员编辑联系方式虽跳过普通用户 OTP，但不得继承旧联系方式的管理员 MFA；目标账号必须使用新联系方式重新完成对应管理员认证。

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

返回 data.items：登录时间、登录方式、脱敏账号、IP、User-Agent、状态。手机号和邮箱账号在写入
`user_login_logs.login_account` 前即脱敏，新写入记录和响应均不得出现完整登录账号。历史数据治理需在单独维护窗执行。

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

### 3.19 邮件模板与发送管理（DirectMail Phase 1 契约，Phase 2 未验收）

> Phase 0 只收集外部资质、模板与 RAM 准备证据；Phase 1 冻结协议与设计，并以 `docs/aliyun-directmail-email-template-phase1-design-review.md §15` 的 QA/PM 书面记录为唯一出口；Phase 2 才验收 Go、migration、真实 MySQL/Redis、DirectMail、前端与 E2E。现有实现和环境材料仅是 Phase 2 待复验输入，不能倒置 Phase 1 门禁。本轮只确认协议与 Redis 锁原语，Go 集成未验收。本节是 API SSOT，不代表功能整体完成。固定五场景为
> `register`、`login`、`reset_password`、`bind_email`、`admin_verify`，不得通过管理端新增第六种场景。

#### 3.19.1 权限与通用规则

| 权限码 | 能力 |
|---|---|
| `email:template:view` | 查看概览统计、模板镜像、场景绑定、同步记录、测试白名单和发送日志 |
| `email:template:manage` | 修改模板本地启停、五场景模板绑定、启停绑定、维护测试邮箱白名单 |
| `email:template:sync` | 从 DirectMail 原子同步模板镜像 |
| `email:template:test` | 向白名单邮箱发送模板测试邮件 |
| `email:template:bootstrap` | 仅授权一次性内部 `admin_verify` 首次配置入口；不授权普通邮件管理接口 |

普通 13 个邮件管理接口均需 Bearer Token + 对应的 view/manage/sync/test 权限 + 管理员手机与邮箱两项认证均在有效期内。这四个权限对应的所有读写接口都强制执行 MFA，不能因 view/sync/test 为细分权限而绕过。bootstrap 专用权限及其手机 MFA 例外只适用于 §3.19.12 的默认关闭内部入口。未完成双重认证固定返回 HTTP 403、code=40003、message=`请先完成管理员双重认证`；权限不足仍返回 HTTP 403、code=40003、message=`无权限`。所有列表接口遵循 D-95，`data` 顶层固定为
`{items,page,page_size,total}`。所有写操作写入 `audit_logs`，但审计摘要只能包含场景、内部记录 ID、
脱敏邮箱、操作结果与版本，不得包含完整邮箱、验证码、模板变量值、凭据或供应商原始响应。
其中 summary、模板/场景/同步记录/白名单/发送日志 GET 使用 view；模板 status、场景绑定和白名单写使用 manage；
同步 POST 使用 sync；模板 test-send 使用 test。

#### 3.19.2 概览统计、模板镜像列表与详情

```text
GET /api/admin/email/summary
GET /api/admin/email/templates
GET /api/admin/email/templates/{id}
PATCH /api/admin/email/templates/{id}/status
```

`GET /summary` 返回对象而非列表，字段和类型固定为：

```json
{
  "template_total": 12,
  "approved_count": 8,
  "local_enabled_count": 7,
  "unbound_scene_count": 1,
  "submitted_today_count": 123,
  "failed_today_count": 3,
  "last_synced_at": "2026-07-22T02:30:00Z"
}
```

前六项均为非负 integer，`last_synced_at` 为最近一次 succeeded 同步的 completed_at（ISO 8601），从未成功同步时为 null。
`template_total` 统计全部本地镜像，`approved_count` 统计 approved 且 missing=false，`local_enabled_count` 统计 local_enabled=true，
`unbound_scene_count` 统计固定五场景中 template_id 为 null 的记录。今日固定为 Asia/Shanghai 自然日 `[00:00, 次日00:00)`：
`submitted_today_count` 仅统计窗口内 accepted/failed 最终发送日志，数据库内部 pending 不计入；`failed_today_count` 仅统计 failed。

列表 Query 参数：`keyword`、`provider_status`（draft/pending/approved/rejected）、`local_enabled`、`variables_complete`、`missing`（boolean）、`scene`、
`page`、`page_size`。

列表 `data.items` 单条字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 平台模板镜像 ID |
| provider | string | 固定 `aliyun_directmail` |
| provider_template_id | string | DirectMail `TemplateId`，仅作供应商资源标识，不允许前端自行拼接发送请求 |
| name | string | 模板名称 |
| subject | string | 邮件主题 |
| provider_status | string | draft / pending / approved / rejected |
| review_comment | string\|null | 归一化审核意见，不保存供应商原始响应 |
| variables_complete | boolean | 是否同时包含 Code 与 ExpireMinutes |
| local_enabled | boolean | 平台本地启停状态；供应商同步不得覆盖 |
| bound_scenes | array\<string\> | 当前绑定到该模板的固定场景 |
| missing | boolean | 最近一次完整同步中供应商侧是否已不存在 |
| missing_since | string\|null | 首次标 missing 时间；重新出现后为 null |
| last_synced_at | string | 最近同步时间（ISO 8601） |
| version | integer | 乐观锁版本 |

详情在上述字段上增加 `sender_nickname`、`template_text`、`variables`、`content_sha256`。`variables` 只返回变量名，
不返回任何实际变量值；`template_text` 一律视为不可信 HTML。资源不存在返回 `404 40400`。
管理前端只能在独立 iframe `srcdoc` 中预览，并使用空 sandbox（不得加入 allow-scripts、allow-forms、
allow-top-navigation、allow-top-navigation-by-user-activation、allow-popups、allow-same-origin）和限制网络加载的 CSP；不得直接用 `v-html` 注入主文档。

`PATCH /templates/{id}/status` Body 固定为 `{ "local_enabled": true, "version": 3 }`，成功返回更新后的完整模板摘要并令
version+1。更新条件必须包含 id+version；冲突返回 `409/40900`。启用时必须同时满足 approved、missing=false、
`variables_complete=true`，否则缺变量返回 `422/51001`，其他模板状态冲突返回 `409/40900`。停用立即阻断已有绑定的
正式发送和模板测试，但保留绑定与历史日志；停用也必须读取并记录当前变量完整性，但为保证故障模板可立即关停，
`local_enabled=false` 不因缺变量被拒绝。供应商同步不得重新开启本地停用模板。

#### 3.19.3 五场景绑定

```text
GET /api/admin/email/scenes
PUT /api/admin/email/scenes/{scene}
```

`GET` 为列表接口，固定五条记录但仍使用 D-95。单条字段：
`scene`、`display_name`、`template_id`（平台镜像 ID）、`provider_template_id`、`provider_status`、`local_enabled`、
`variables_complete`、`missing`、
`enabled`、`variable_mapping`、`version`、`updated_at`。`variable_mapping` 固定返回：

```json
{
  "code": "Code",
  "expire_minutes": "ExpireMinutes"
}
```

`PUT` Body：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| template_id | integer | 是 | 平台模板镜像 ID；必须 approved、local_enabled=true、missing=false 且变量完整 |
| enabled | boolean | 是 | 是否允许该场景发送 |
| version | integer | 是 | 客户端读取到的当前版本 |

返回更新后的场景绑定。更新条件必须包含 `WHERE scene=? AND version=?`，成功后 `version=version+1`；版本不一致返回
`409 40900「配置已被其他管理员修改，请刷新后重试」`。五场景名和变量映射不可修改；模板进入
`rejected`/`missing`/本地停用后，即使绑定仍在也必须停止发送。绑定与 `enabled=true` 都必须重新解析模板变量，
同时包含大小写完全一致的 `Code` 和 `ExpireMinutes`；缺任一变量返回 `422/51001`，不得保存绑定或启用状态。

#### 3.19.4 原子幂等同步

```text
POST /api/admin/email/templates/sync
GET  /api/admin/email/template-sync-runs
```

`POST` 必须传 `Idempotency-Key`，Body 固定为 `{ "provider": "aliyun_directmail" }`。幂等 scope 固定为跨管理员全局
`admin-email-template-sync:aliyun_directmail`，请求指纹固定为规范化 method+path+provider 的 SHA-256；数据库唯一键为
scope+key_hash，而非仅按管理员隔离。同步流程必须先完整拉取
DirectMail 全部分页和详情；只有远端读取全部成功后，才在单个数据库事务中 upsert 全量镜像并把“上次存在、本次未出现”的模板
标为 `missing=true` 并仅在首次缺失时写 `missing_since`；重新出现时清空 missing_since。任一页或详情读取失败时，
本地模板镜像、missing/missing_since 与 local_enabled 均不得改变。
首次同步创建的模板 `local_enabled=false`，必须由管理员通过 status 端点显式启用；后续同步保留本地值。

同一 `Idempotency-Key` + 同一请求指纹重复请求返回原同步结果并带 `idempotent=true`；同 key 不同请求指纹返回
`409 40900`。不同 key 在已有同步运行时返回 `409 40900「模板同步正在进行」`，不允许两个同步任务并发改写镜像。

POST 与 GET 的同步记录字段和可空性固定为：`run_id:integer`、`provider:string`、
`status:running|succeeded|failed`、四个计数 `integer`、`error_code:string|null`、`error_message:string|null`、
`created_by:integer`、`started_at:string`、`completed_at:string|null`；POST 另含 `idempotent:boolean`。
仅 running 的 completed_at 为 null；failed 的安全 error_code/error_message 必须非 null，其他状态两字段必须为 null，且不透传供应商原始响应。

`GET` 为 D-95 列表，支持 `status`、`page`、`page_size`，返回上述计数、发起管理员 ID、开始/完成时间。

Redis 分布式锁是发布必需依赖：同步锁使用 `lock:email:template-sync:aliyun_directmail`、TTL 30 秒；OTP/测试发送锁使用
scope HMAC 摘要 key、TTL 15 秒。加锁必须为 `SET key token NX PX ttl`，续租间隔不超过 TTL 三分之一且用 Lua
比较 token 后 `PEXPIRE`，释放用 Lua 比较 token 后 `DEL`。进入事务/外呼前重新校验所有权；外呼前丢锁立即停止，外呼期间丢锁按下方 fencing/墓碑规则唯一收敛；
只有未取得 Redis 锁，或外呼开始前发生续租/所有权校验失败时，才统一返回 `503/51003「邮件发送服务未就绪」`，Adapter 调用次数增量为 0。外呼开始后的续租/所有权失败不得返回 503，也不得断言 Adapter 增量为 0，必须按明确响应 fencing 或未知结果持久化阻断规则收敛。
本轮只冻结锁协议，Go 集成留待 Phase 2 验收。

邮件同步、测试发送和正式 OTP 均必须使用 Redis 锁，数据库锁或唯一约束只能作为二次 fencing，不能替代 Redis。
test-send scope 固定为
`admin-email-template-test:admin:{admin_id}:template:{platform_template_id}:scene:{scene}:recipient:{recipient_hmac}`。
邮箱先 trim、统一小写并完成单裸地址校验，再计算 `recipient_hmac`；Redis key 仅使用完整 scope 的 HMAC 摘要。
`Idempotency-Key` 不进入锁 scope：同管理员/平台模板/场景/收件人竞争同锁，任一维度不同不竞争。

#### 3.19.5 测试邮箱白名单

```text
GET    /api/admin/email/test-recipient-allowlist
POST   /api/admin/email/test-recipient-allowlist
DELETE /api/admin/email/test-recipient-allowlist/{id}
```

列表使用 D-95，仅返回 `id`、`email_masked`、`status`、`version`、`created_by`、`created_at`；禁止返回完整邮箱或
邮箱 HMAC。新增 Body 为 `{email}`，后端只保存规范化邮箱的 HMAC-SHA256 与脱敏值，重复邮箱返回 `409 40900`。
POST 成功固定 HTTP 201，data 为
`{id:integer,email_masked:string,status:"active",version:integer,created_at:string}`。
DELETE Body 必须带 `{version}`，成功固定 HTTP 200，data 为
`{id:integer,email_masked:string,status:"revoked",version:integer,revoked_at:string}`；版本冲突返回 `409 40900`。
白名单只限制后台测试发送，不得被正式 OTP 发送复用为收件人来源；未命中 active 白名单固定返回 `400/40000`。

#### 3.19.6 模板测试发送与发送日志

```text
POST /api/admin/email/templates/{id}/test-send
GET  /api/admin/email/send-logs
```

测试发送必须传 `Idempotency-Key`，Body 为 `{ "scene": "register", "email": "<测试邮箱>" }`。路径 `{id}` 是平台模板镜像 ID；
后端必须据此解析当前 `approved` 且 `missing=false` 的 DirectMail `TemplateId`，不得要求该模板已绑定到所选场景。
后端先规范化邮箱并计算 HMAC，只有命中 active 白名单才允许通过 `SingleSendMail` 发送。测试验证码由服务端生成且不具备认证用途，
服务端从该镜像的 `TemplateText` 本地渲染 `Code` 与 `ExpireMinutes=10`，仅替换大小写精确的两个固定变量；响应和日志不得返回变量实际值或渲染后的完整正文。
发送前还必须要求 `local_enabled=true`，并从同步后的 variables 再次确认同时包含大小写完全一致的 `Code` 与 `ExpireMinutes`；
缺任一变量固定返回 `422/51001` 且不调用供应商。未命中 active 白名单固定返回 `400/40000`。

响应字段：`send_log_id`、`business_request_no`、`template_id`、`scene`、`recipient_masked`、
`status`（固定 accepted）、`failure_reason`（固定 null）、`idempotent`、`submitted_at`；只有 DirectMail 明确受理才返回
HTTP 200。供应商明确失败/拒绝必须先安全落库 status=failed、归一化 failure_reason，再返回
HTTP 502、code=51002、message=`邮件上游调用失败`、data=null；响应未知/超时则按下方
`provider_outcome_unknown` 与专用 502/51002 文案处理。任何失败不得以 HTTP 200/status=failed 表示。
DirectMail 明确返回业务 Code 时，`failure_reason` 仅允许写
`provider_rejected_{category}_{http_class}`。`category` 是严格白名单枚举
`auth/permission/sender/recipient/content/rate_limited/request/other`，未知 Code 固定归 `other`；
`http_class` 仅允许 `http_2xx/http_3xx/http_4xx/http_5xx/http_other`。不得保存或记录供应商原始 Code、Message、响应正文、请求字段值、HtmlBody、OTP、完整邮箱、AccessKey、Secret 或 Authorization。普通 Mock/内部上游错误可继续归一为不带供应商细节的 `provider_rejected`。该观测复用既有 `failure_reason`，不新增列。
同 Idempotency-Key+同请求重放：原结果 accepted 时返回同一 HTTP 200 并令 idempotent=true；原结果 failed 时返回同一安全
`502/51002` 错误信封，且不再次调用供应商。同 key 不同模板、邮箱或场景返回 `409/40900`。

外呼期间丢锁时不得遗留长期 pending，也不得新增墓碑表。供应商明确 accepted/rejected 时，分别使用
`WHERE id=? AND status='pending'` 唯一收敛 accepted/failed；accepted 只能依据明确 accepted 响应。响应未知或超时时，复用已持久化的
`email_send_logs` pending 行，在同一数据库事务中条件更新 `status=pending→failed`、
`failure_reason=provider_outcome_unknown` 并保留 `idempotency_scope`。`purpose=otp` 保留发送日志/验证码的 `expires_at`，
正式 OTP 同事务把对应 `verification_codes.send_status` 置 failed；`purpose=test` 的 `email_send_logs.expires_at` 必须保持 NULL，
不得为了墓碑写入非空值。该 unknown failed 行即持久化冷却墓碑。

墓碑查询、响应和新旧 key 规则统一使用服务层派生 `cooldown_until`：OTP=`expires_at`，test=`submitted_at + 10分钟`；
`cooldown_until` 不新增数据库列。每次新外呼必须先取得 Redis 锁，再按同 scope 与 `cooldown_until` 查询仍在冷却期的 pending 或 `provider_outcome_unknown` failed 行；命中即阻断，
Redis 重启或锁 key 丢失也不能绕过。原未知请求返回 `502/51002「供应商响应未知，请在验证码过期后重试」`；同一旧
Idempotency-Key 重放原 502/51002 且 `idempotent=true`。墓碑期内新 key 返回
`409/40900「邮件发送结果确认中，请在验证码过期后重试」`，Adapter 调用增量为 0。`cooldown_until` 到期后仅新 key 可重新发送，
旧 key 仍重放原失败。条件更新影响行数非 1 时读取已有终态并返回，不覆盖终态且产生告警。

发送日志列表使用 D-95，支持 `scene`、`purpose`（otp/test）、`status`（accepted/failed）、`template_id`、时间范围、分页；pending 仅为数据库内部幂等状态，筛选 pending 返回 400/40000且列表永不公开。
单条字段和可空性固定为：`id:integer`、`scene:string`、`purpose:otp|test`、`recipient_masked:string`、
`template_id:integer`、`provider_template_id:string`（DirectMail TemplateId）、`business_request_no:string`、
`provider_request_id:string|null`（阿里云 RequestId）、`status:accepted|failed`、`failure_reason:string|null`、
`submitted_at:string`。accepted 时 provider_request_id 非空且 failure_reason 为 null；failed 时 failure_reason 非空，
provider_request_id 可为 null。不返回验证码、完整邮箱、模板变量、AccessKey 或供应商原始响应。

`accepted` 只代表阿里云同步受理，成功文案固定为“供应商已受理发送请求”，严禁显示为“已送达”。当前范围不提供投递回执 Webhook、最终送达状态、
打开率或点击率字段，也不提供相关写接口或统计接口。

#### 3.19.7 所有邮件发送的前置条件

正式 OTP 和后台测试邮件均必须逐项满足：

1. 场景属于固定五场景，且调用端点与场景认证级别匹配；
2. 生产环境只能使用生产 DirectMail Adapter，Mock Adapter 仅允许显式非生产环境；
3. DirectMail 凭据、Region、发信地址已通过安全配置注入，凭据非空；
4. 正式 OTP 的场景绑定存在且 `enabled=true`；后台模板测试的路径模板存在；两类模板都必须 approved、local_enabled=true、missing=false；
5. 正式 OTP 的 `TemplateId` 从绑定关系解析，模板测试的 `TemplateId` 从路径镜像 ID 解析；两者只用于绑定、日志和追踪。同步变量必须同时含 Code/ExpireMinutes，发送前从镜像 `TemplateText` 本地固定渲染 `HtmlBody`，禁止硬编码、由请求覆盖或向 `SingleSendMail` 发送 `Template.*` 参数；
6. 正式 OTP 收件人必须来自当前认证流程已经校验的目标；后台测试收件人必须命中测试邮箱白名单；
7. 通过既有验证码限流与场景前置校验，10 分钟有效期和一次性消费规则不变；
8. 以上模板/变量/收件人前置条件全部通过后，正式 OTP 才持久化验证码并写 `send_status=pending`，再调用供应商；只有明确受理才原子置 accepted 并写 accepted 日志，其他供应商结果原子置 failed 并写 failed 日志；前置失败不创建验证码或发送日志；
9. 幂等键与请求指纹校验通过，不得重复发信或重复生成可用验证码；
10. 审计和应用日志执行脱敏，任何失败均不得泄露供应商凭据或原始响应。

DirectMail RAM 最小权限严格只允许 `dm:SingleSendMail`、`dm:QueryTemplateByParam`、`dm:DescTemplate`。
后台测试发送与正式 OTP 共用 `dm:SingleSendMail`，不得依赖其他测试发送 action。QA 必须覆盖三个允许 action 分别被显式
`Deny` 时的失败行为，并验证 `dm:CreateTemplate`、`dm:ModifyTemplate`、`dm:DeleteTemplate` 均未授权且应用不调用：
供应商授权或调用失败统一返回 `502/51002「邮件上游调用失败」`，本地绑定和模板镜像不被污染、
验证码保持不可用、无任何敏感信息出现在响应或日志中。平台侧缺少上述四个邮件权限码仍返回 `403 40003`；
生产 Adapter 或必要配置未就绪返回 `503/51003`；缺少模板变量返回 `422/51001`；参数、非法场景或测试邮箱未命中白名单返回 `400/40000`。

发送前置失败契约固定如下；除 accepted 的首次外呼外，所有行的 Adapter `send_mail` 调用次数增量必须为 0：

| 条件 | HTTP/code | message |
|---|---|---|
| 路径模板或邮件资源不存在 | `404/40400` | `邮件资源不存在` |
| 场景无绑定 | `409/40900` | `邮件场景未绑定模板` |
| 绑定 `enabled=false` | `409/40900` | `邮件场景已停用` |
| 模板本地停用 | `409/40900` | `邮件模板已停用` |
| 模板 draft | `409/40900` | `邮件模板尚未提交审核` |
| 模板 pending | `409/40900` | `邮件模板正在审核` |
| 模板 rejected | `409/40900` | `邮件模板审核未通过` |
| 模板 missing | `409/40900` | `邮件模板在供应商侧不存在` |
| 变量缺失 | `422/51001` | `邮件模板变量不完整` |
| 未取得 Redis 锁、外呼前丢锁，或生产 Adapter/必要配置未就绪 | `503/51003` | `邮件发送服务未就绪` |

资源不存在统一使用 `404/40400`，不得使用 `40004`。管理员未完成手机+邮箱 MFA 固定返回
`403/40003「请先完成管理员双重认证」`。验证码同时执行 10 次/分钟/IP 与 10 次/分钟/账号限制；公开邮箱账号键为
规范化邮箱 HMAC，换绑和管理员验证分别按 user_id/admin_id，任一超限返回 `429/42900「请求频率超限」`。

可观测性要求：Adapter 每次调用递增 `email_adapter_calls_total{operation,scene,result}`，label 不含任何敏感值；
前置拒绝和幂等重放前后调用计数不变。审计 attempt 失败不执行动作，result 失败以及 Redis 锁所有权异常必须告警。
敏感扫描固定覆盖 HTTP 响应、应用/集中日志、audit_logs、邮件表与 verification_codes、指标/trace/event、前端 console/埋点/持久缓存；
不得出现完整邮箱、OTP、AccessKey、TemplateData、锁 token 或供应商原始响应。

### 3.19.12 首次配置 `admin_verify` 的一次性内部 bootstrap delta

000055 将五个邮件场景初始化为未绑定且停用；普通 13 个 `/api/admin/email/*` 接口又全部要求管理员手机与邮箱 MFA。由于管理员邮箱 MFA 发码本身依赖已经启用的 `admin_verify` 场景，首次配置必须通过以下一次性内部运维入口完成。该入口只建立投递通道，不完成邮箱 MFA、不写 `admin_email_verified_at`、不签发 Token，也不发送测试或正式邮件。

```text
POST /api/internal/email/bootstrap/admin-verify
Authorization: Bearer <管理员 Access Token>
X-Email-Bootstrap-Token: <独立一次性运维 Token>
Idempotency-Key: <本次操作唯一键>
Content-Type: application/json

{"provider_template_id":"<精确 DirectMail TemplateId>"}
```

**注册与网络边界：**

- 四个配置键固定为：`EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TOKEN`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_ALLOWED_IPS`、`EMAIL_ADMIN_VERIFY_BOOTSTRAP_TRUSTED_PROXY_IPS`，不得增加隐式回退别名。
- `EMAIL_ADMIN_VERIFY_BOOTSTRAP_ENABLED` 仅键缺失时默认 false；显式 false 时全方法404。显式空字符串及除字面 true/false 外的任何值均启动失败。
- enabled=true 时，Token、allowed CIDR、trusted proxy CIDR 三项都必须显式非空且合法，任一缺失、空值、弱占位、低多样性或解析失败均使应用启动失败，不能只让 ready 降级后继续承载流量。trusted proxy 采用显式空列表字符串不合法；直连部署必须填入不会命中实际来源的已批准 CIDR，不能通过缺省表达。
- Bootstrap Token 复用 `INTERNAL_API_TOKEN` 的客观安全校验基线和常量时间比较实现，但不复用配置值：按原始 UTF-8 字节校验，至少 32 字节、无首尾空白，大小写不敏感拒绝空值、`REPLACE_WITH_EMAIL_BOOTSTRAP_TOKEN`、`REPLACE_WITH_INTERNAL_API_TOKEN`、`CHANGE_ME`、`CHANGEME`、`DEFAULT`、`SECRET`、`TEST`，且原始值至少包含 8 种不同字节；低于 8 种视为低多样性并启动失败。部署值必须由 CSPRNG 生成至少 32 个随机字节后编码。当 `INTERNAL_API_TOKEN` 同时配置时，Bootstrap Token 与其原始字节完全相等也必须启动失败；比较过程不得记录任一值。
- 三项 bootstrap 安全配置使用独立键，禁止从 `INTERNAL_API_TOKEN`、`INTERNAL_ALLOWED_IPS`、`INTERNAL_TRUSTED_PROXY_IPS` 或 `TRUSTED_PROXY_IPS` 隐式读取、合并或回退。Bootstrap CIDR 与平台既有 INTERNAL/TRUSTED 列表独立显式配置后允许条目同值或部分重叠，同一 Nginx 可信代理网段可按不同端点需要分别写入。allowed/trusted proxy 均为逗号分隔、无空项的精确 IP 或 CIDR；任何解析后前缀长度为 0 的 IPv4/IPv6 网段（包括 `0.0.0.0/0`、`::/0` 及等价写法）一律启动失败。Bootstrap allowed 与 bootstrap trusted-proxy 两个列表之间若存在规范化后完全相同的 CIDR 条目必须启动失败；不同前缀仅部分重叠允许。还必须分别计算两个列表各自的规范化 CIDR 地址并集；任一列表的并集覆盖完整 IPv4 或 IPv6 地址族也必须启动失败，例如 `0.0.0.0/1,128.0.0.0/1` 或 `::/1,8000::/1`。两个不同语义列表之间不合并计算全地址族并集。非 trusted 连接只信任 `RemoteAddr`；trusted 连接只信任代理覆盖的恰好一个合法单值 `X-Real-IP`。应用永不读取 `X-Forwarded-For`。Token 或来源任一失败统一返回 `403/40003「无权限」`，无数据库、审计、锁或 Adapter 副作用。
- 反向代理不得把本路径暴露给公网或浏览器；仅批准的运维网络可达。成功后必须移除 enabled/token 配置并重启，确认路径恢复 404。

**身份与最小权限：**

- 在独立 Token 与来源双闸之后，仍必须校验正常管理员 JWT、账号有效、当前用户直接关联唯一 code=`admin` 角色、当前手机 MFA 有效，以及 `email:template:bootstrap` 专用权限。仅通过角色/分组/用户动态 allow override 获得该权限、但不直接关联 admin 角色的普通用户仍固定返回 `403/40003「无权限」`。该权限仅授权本入口，不隐含 view/manage/sync/test，也不能用于普通 13 个邮件接口。
- `ADMIN_VERIFY_EXPIRE_HOURS<0` 属于非法安全配置，无论 bootstrap 是否启用都必须使应用启动失败；`=0` 明确定义为永不过期。手机 MFA 以 users 当前值为真相源：`admin_phone_verified_at` 必须非空且不得晚于当前数据库 UTC 时间；未来时间即使 `ADMIN_VERIFY_EXPIRE_HOURS=0` 也失败关闭。`ADMIN_VERIFY_EXPIRE_HOURS>0` 时还要求该时间仍在有效窗口内。缺失、未来时间和恰好到达过期边界都视为无效，返回 `403/40003「请先完成手机号认证」`，且 attempt 审计、Adapter 与数据库增量均为 0。
- 邮箱 MFA 缺失是本入口允许修复的唯一认证缺口；手机 MFA 缺失或过期固定拒绝。入口不得接受 user_id、scene、enabled、变量映射、邮箱、验证码、模板正文或任何 MFA 时间戳。
- enabled=true 时只允许 POST；同路径其他方法返回 `405/40000「请求方法不允许」` 并带 `Allow: POST`。`Content-Type` 经标准媒体类型解析后必须为 `application/json`，charset 只允许缺省或 `utf-8`；否则返回 `415/40000「请求参数错误」`。
- `Authorization` 按既有 Bearer JWT 中间件处理：缺失、空、重复/逗号多值或格式错误均走标准 401 契约。`X-Email-Bootstrap-Token` 缺失、空、重复、逗号多值或常量时间比较不匹配，统一返回 `403/40003「无权限」`，不得转成 400。`Idempotency-Key` 必须恰好一个值，缺失、空、重复或逗号多值均返回 `400/40000「请求参数错误」`；其原始 UTF-8 值必须为 16-128 字节、无首尾空白或控制字符。
- Body 上限 4 KiB，必须是单个 JSON 对象且只含一个 `provider_template_id`；该值必须是 1-64 字节的 ASCII 十进制正整数，并按原字节传给供应商。空值、全零、65 字节及以上、非数字、正负号、小数、指数表示、首尾或内部空白、控制字符、Body 缺失/超限、额外字段、重复键、尾随 JSON 均返回 `400/40000「请求参数错误」`，且必须在 attempt 审计、Adapter 和数据库之前拒绝，三者增量均为 0。

**供应商校验与原子写入：**

0. 以 `EMAIL_IDEMPOTENCY_SECRET` 计算包含 admin_id 与原始 key 的 HMAC，并计算覆盖 admin_id、method、path、scope、provider_template_id 的 fingerprint。仅 key hash、fingerprint、completed_by 当前admin 三者匹配才重放；跨admin或指纹不同返回409且不泄露操作者。
1. 若尚无 receipt，先成功写入 fail-closed 的 `email.admin_verify.bootstrap.attempt` 审计，之后才允许调用一次 Adapter `DescribeTemplate(provider_template_id)`（供应商 API 名为 DescTemplate）；attempt 写入失败返回 `500/50000「系统内部错误」`，Adapter `describe_template` 增量必须为 0。不同 Idempotency-Key 的首次并发请求允许各自完成这一只读 Describe；并发控制只在后续数据库写事务执行。
2. 阿里云官方 DescTemplate 响应字段为 `TemplateName`、`TemplateStatus`、`TemplateText`；现有 Adapter 把 JSON `TemplateName` 精确映射为 `ProviderTemplate.Name`、把 `TemplateStatus` 归一化为 `ProviderTemplate.Status`，并从 `TemplateText` 提取变量。官方字段定义见 [阿里云 DirectMail DescTemplate](https://help.aliyun.com/en/direct-mail/api-dm-2015-11-23-desctemplate)。
3. `ProviderTemplate.Name` 必须按 UTF-8 字节、大小写精确等于 `molin_admin_verify_code_v1`，不得 trim、折叠大小写或用 Subject/Text 替代名称校验；Status 必须为 `approved`（官方 `TemplateStatus=2`），模板变量必须包含大小写精确的 `Code` 与 `ExpireMinutes`。资格判断只以上述三个客观条件为准，不接受调用方自定义映射。
4. Describe 校验通过后开启单个数据库事务，先以 `SELECT ... FOR UPDATE` 锁定唯一 `email_scene_bindings.scene='admin_verify'` 行，再在事务内复查 receipt。绑定行必须仍满足 `template_id IS NULL AND enabled=0 AND version=1`；随后 upsert 精确模板镜像并置 `local_enabled=true`、以带相同初始条件的 UPDATE CAS 更新绑定、插入 scope 唯一的 000056 成功 receipt，并写 `email.admin_verify.bootstrap.result` 成功审计。result 审计的 `target_type` 固定为 `email_admin_verify_bootstrap_receipt`，`target_id` 固定为本事务新建 receipt 的内部十进制 ID，不得使用供应商 TemplateId、管理员 ID 或 scene 代替。任一步失败整事务回滚；result 审计失败返回 `500/50000「系统内部错误」`，不能留下已生效动作。
5. 不同 key 可重复Describe但仅一个提交。同admin同key同fingerprint首次并发的第二个即使已Describe，行锁后匹配receipt也返回原成功且idempotent=true。跨admin同key固定409/40900已完成且不泄露操作者。普通13接口不变。
6. 成功只返回 `{scene:"admin_verify",configured:true,idempotent:false}`；不得返回模板正文、供应商原始响应、Token、邮箱、OTP 或 MFA 状态。相同 Idempotency-Key 和相同 fingerprint 重放返回原成功语义且 `idempotent=true`，不再 Describe；同 key 不同 fingerprint 返回 `409/40900「数据冲突，请刷新后重试」`。

`DescribeTemplate` 每次实际调用都复用既有指标 `email_adapter_calls_total{operation="describe_template",scene="template_sync",result}` 并按 accepted/failed/timeout 递增一次，不新增 bootstrap scene、operation 或任何新时间序列；前置拒绝、receipt 预检冲突、attempt 审计失败和幂等重放增量均为 0。

**精确错误契约：**

| 条件 | HTTP/code | message |
|---|---|---|
| 无 Authorization | `401/40001` | `未登录` |
| JWT 无效或过期 | `401/40001` | `token 无效或已过期` |
| JWT 已吊销 | `401/40001` | `token 已失效，请重新登录` |
| 账号已封禁 | `401/40101` | `账号已被封禁` |
| bootstrap Token 缺失/空/重复/逗号多值/错误，来源失败，无专用权限，或非 admin 普通用户仅动态获权 | `403/40003` | `无权限` |
| 手机 MFA 未完成或过期 | `403/40003` | `请先完成手机号认证` |
| 严格 Header/Body/Content-Type 失败 | 见上文 `400` 或 `415`/`40000` | `请求参数错误` |
| 供应商确认模板不存在 | `404/40400` | `邮件资源不存在` |
| Name 不精确匹配 | `409/40900` | `邮件模板名称不符合管理员认证约定` |
| Status=draft/pending/rejected | `409/40900` | 分别为 `邮件模板尚未提交审核` / `邮件模板正在审核` / `邮件模板审核未通过` |
| Status 未知或非 approved | `409/40900` | `邮件模板状态不允许首次配置` |
| 缺 Code 或 ExpireMinutes | `422/51001` | `邮件模板变量不完整` |
| DirectMail/RAM/网络明确失败 | `502/51002` | `邮件上游调用失败` |
| DirectMail 超时或结果未知 | `502/51002` | `供应商响应未知，请稍后重试` |
| binding CAS/receipt/幂等冲突 | `409/40900` | 对应上文冻结文案 |
| attempt/result 审计或数据库内部失败 | `500/50000` | `系统内部错误` |

`email_admin_verify_bootstrap_receipts` 已存在成功记录时，任何新 key 或不同操作者均固定返回 `409/40900「管理员邮箱认证场景已完成首次配置」`，不得再次调用供应商或改写绑定。失败尝试只进入审计，不写成功 receipt，修复可重试。成功后管理员必须继续走既有“手机发码与校验 → 邮箱发码与校验”流程，完整 MFA 生效后才能访问普通 13 个邮件管理接口。

**普通接口与权限文案保持冻结：**

- 现有 13 个 `/api/admin/email/*` 的路径、Body、四个既有权限和完整 MFA 要求全部不变，不得把 bootstrap 条件分支加入普通同步或场景绑定接口。
- 邮件管理权限不足固定为 `403/40003「无权限」`。实现应使用邮件专用权限包装器，不直接改变全局 `RequirePerm` 的历史 `「无操作权限」` 文案，避免影响 auth/iam/identity 及其他管理接口。

**000056 与回滚：**

000056 新增 `email:template:bootstrap` 权限（精确元数据：name=`首次配置管理员邮箱认证模板`、resource=`email_template`、action=`bootstrap`）、admin 角色精确绑定、`migration_000056_permission_ownership` 和 `email_admin_verify_bootstrap_receipts`。执行前要求 code=`admin` 的角色恰好一行；0 行或多行均失败关闭。预存同名权限只有元数据完全一致才可复用，预存 admin 绑定也可复用；ownership 必须在创建前记录预存/新增状态，并在创建后回填最终 ID。

`migration_000056_permission_ownership` 精确复用 000055 ownership 结构：`permission_code VARCHAR(191)` 主键、可空且唯一的 `permission_id BIGINT UNSIGNED`、布尔 `permission_created`、可空且唯一的 `admin_role_permission_id BIGINT UNSIGNED`、布尔 `admin_binding_created`、`created_at DATETIME`；不加外键，且只允许一行 `email:template:bootstrap`。000056 不修改或复用 `migration_000055_permission_ownership`。

000056 可使用 migration-only 临时断言表 `migration_000056_assertions`：`assertion_name VARCHAR(191)` 主键、`passed TINYINT(1)` 非空且 CHECK 固定等于 1。up/down 入口均要求该表不存在，成功结尾必须删除；partial 失败遗留该表时，它只作为断点证据，禁止直接删除后重跑。

up 前 receipt/000056 ownership 两表都必须不存在；任一同名表、权限元数据冲突或 admin 多行立即失败。MySQL DDL 隐式提交，因此 partial-up 不得盲目重跑：按 information_schema、权限、绑定和 ownership 逐项判断断点，优先从已验证备份恢复；选择前向修复时只能补齐缺失对象并重新执行全部写后断言。partial-down 同样先核对 receipt、ownership 和引用，任何未知中间态失败关闭。

down 的第一道断言是 receipt 行数必须为 0。随后仅当 `admin_binding_created=1` 才删除记录的精确 admin role_permission，仅当 `permission_created=1` 且不存在其他 role_permissions、user_permission_overrides、group_permissions 引用时才删除权限；预存权限/绑定必须保留。写后断言通过才删除 receipt 空表和 000056 ownership。成功 receipt 是安全凭据：一旦存在，常规 down 必须在任何删除前失败关闭。应用回滚应关闭 bootstrap 配置并保留 schema 56 与 receipt；如确需移除，必须另行审批、备份恢复验证和不可变审计留存，禁止 migration `force` 绕过。

### 3.19.13 公开验证码来源 IP（Phase 1/短信 Phase 4 delta）

本节适用于公开邮件/手机验证码端点及密码重置入口的 IP 限流与安全判定。全局 `TRUSTED_PROXY_IPS`
仅描述面向公开流量的可信反向代理；它与内部 metrics 专用的 `INTERNAL_TRUSTED_PROXY_IPS` 独立配置、独立校验，禁止互相回退或合并。

- `TRUSTED_PROXY_IPS` 为空是合法的直连模式：应用只使用 `RemoteAddr` 解析出的 IP，忽略所有来源 Header。
- 非空值是逗号分隔的精确 IP 或 CIDR 列表，每项先 trim 再严格解析；拒绝空项、非法 IP/CIDR 与带 IPv6 zone 的地址。非空配置任一项非法时应用启动失败，或至少 `/api/ready` 不通过且不得承载公开验证码与密码重置流量。
- 每个请求先解析 `RemoteAddr`。当其不命中 `TRUSTED_PROXY_IPS`（包括列表为空）时，最终来源只能是 `RemoteAddr`；`X-Real-IP`、`X-Forwarded-For`、`Forwarded` 均不得改变结果。
- 仅当 `RemoteAddr` 命中 trusted proxy 时，才要求并信任代理覆盖写入的恰好一个 `X-Real-IP` 单值。该值必须是无逗号的合法 IP；缺失、空值、非法值、逗号多值或重复 Header 均固定返回 `403/40003「无权限」`，不得透露具体原因。
- 上述 trusted proxy Header 拒绝发生在限流计数与业务服务之前：不递增 IP/账号发码计数，不创建验证码或发送记录，不取得发送锁，不进入邮件或短信服务，外部 Sender 调用增量固定为 0。
- 应用永远不得把 `X-Forwarded-For` 用于安全判定。运行期间来源解析器不可用或无法执行安全判定时失败关闭：邮件发码返回 `503/51003「邮件发送服务未就绪」`；手机发码与密码重置返回 `503/50300「验证码服务当前不可用」`。两者均不计数、不进入业务服务且外部 Sender 增量为 0。
- Nginx 只能从约定公开入口代理验证码与密码重置路径，必须覆盖 `X-Real-IP=$remote_addr`，删除而非透传 `X-Forwarded-For` 与 `Forwarded`，并确保其连接应用所用地址显式配置在 `TRUSTED_PROXY_IPS`。网络位置不能替代应用判定。

邮件入口已按该契约实现并完成既有验收；短信阶段 4 将同一解析器接入手机发码和密码重置。阶段 4 本地单测通过，
真实 Redis、HTTP 和 Linux race 仍以 PR CI 为准；本文件不代表 Nginx 部署配置已经复核。

### 3.20 统一内部指标端点

```text
GET /api/internal/metrics
```

本端点仅供监控系统从内部监控网络抓取，不是用户端或管理端业务 API。邮件指标的既有安全契约继续生效；短信与 AI 网关指标通过同一鉴权端点追加，不创建匿名或弱鉴权旁路。

**请求与响应：**

- 只允许 `GET`。包括 `HEAD` 在内的其他方法统一返回 HTTP 405，并带 `Allow: GET`；不得把其他方法降级为 GET。
- 成功固定返回 HTTP 200、Prometheus text exposition format 0.0.4，`Content-Type: text/plain; version=0.0.4; charset=utf-8`，同时返回 `Cache-Control: no-store` 与 `X-Content-Type-Options: nosniff`。
- 请求必须同时通过 Token 与来源 IP 两道不可降级的安全闸，任一失败固定返回 `403/40003「无权限」`，不得暴露具体失败原因或配置状态。
- `INTERNAL_API_TOKEN` 按原始 UTF-8 字节校验，不做 trim：值不得含首尾空白，编码后至少 32 字节，并以大小写不敏感方式拒绝空值、`REPLACE_WITH_INTERNAL_API_TOKEN`、`CHANGE_ME`、`CHANGEME`、`DEFAULT`、`SECRET`、`TEST`。部署应由 CSPRNG 生成至少 32 个随机字节后再编码为 base64 或 hex，通过安全渠道注入。请求头 `X-Internal-Token` 与配置值按原始字节做常量时间比较；Token 不得写入应用日志、访问日志、审计、指标或错误响应。
- `INTERNAL_ALLOWED_IPS` 与 `INTERNAL_TRUSTED_PROXY_IPS` 都必须为非空的逗号分隔列表；每项 trim 后只能解析为精确 IP 或 CIDR。任一空项、非法项或整个列表为空，metrics 端点均失败关闭，不得承载抓取流量。
- 来源 IP 真相首先来自 `RemoteAddr` 解析出的 IP。仅当 `RemoteAddr` 命中 `INTERNAL_TRUSTED_PROXY_IPS` 时，才要求并信任由代理覆盖写入的恰好一个 `X-Real-IP` 单值，且该值必须是合法 IP；缺失、空值、多值或非法值均返回 `403/40003「无权限」`。非可信代理或直连请求始终只用 `RemoteAddr` 与 `INTERNAL_ALLOWED_IPS` 匹配，任何 `X-Real-IP`、`X-Forwarded-For` 或 `Forwarded` 都不能改变结果。应用永远不得读取 `X-Forwarded-For`。
- 三项配置缺失、为空或非法时不得放宽为匿名、本机默认、可信代理豁免或仅单闸访问；部署就绪检查应阻止错误配置承载监控流量。

**邮件指标族：**

```text
email_adapter_calls_total{operation="...",scene="...",result="..."}
```

- `operation` 只允许 `query_templates`、`describe_template`、`send_mail`。
- `query_templates` 与 `describe_template` 的 `scene` 只能是 `template_sync`；`send_mail` 的 `scene` 只能是 `register`、`login`、`reset_password`、`bind_email`、`admin_verify`。
- `result` 只允许 `accepted`、`failed`、`timeout`。上述 operation/scene 合法配对与三个 result 构成封闭的 21 个时间序列；进程启动后即全部输出，未发生调用的序列值为 0。
- 指标是进程内单调递增计数器；同一进程生命周期内不得递减，进程重启允许重置为 0，不承诺跨重启持久化。
- 禁止增加邮箱、邮箱 HMAC、OTP、用户/管理员 ID、请求 ID、业务请求号、供应商 RequestId、TemplateId、错误原文、IP、Token 或其他高基数/敏感 label。
- 禁止输出 Go runtime、process、通用 HTTP、数据库连接或 Redis key 明细。除已冻结的邮件、短信和下述 AI 网关指标族外，不得动态追加其他指标族。

**AI 网关 G7 指标增量：**

Token 与来源 IP 安全闸完全不变。AI 网关模块装配成功后追加 `molin_ai_gateway_*` 指标，覆盖请求量/耗时、TTFT、流式断连、上游结果/重试、Usage 缺失、治理拒绝、四层并发租约、账务状态、钱包预占数量/金额/最老年龄、Outbox/补偿积压、账单差额、三方金额和已确认平台 SK 泄漏。对账识别的 `missing_usage` 与在线缺失计数接入同一 P1 告警；最老未释放预占超过 300 秒或总额超过 10 元接入 P1。泄漏事实必须先校验请求归属，再以 HMAC 精确匹配请求所属有效 SK，通用疑似文本只拒绝入库且不得升级 P0；五分钟窗口按唯一 API Key 计数。逻辑模型必须由数据库准入且最多 32 个，超出或非法值收敛为 `other`；其他标签均为封闭枚举。禁止出现 `request_id/user_id/project_id/api_key/prompt/secret` 或任何正文、密钥、错误原文。

持久 Gauge 读取失败时端点返回 `503/50300「指标服务暂不可用」`，不输出邮件/短信/AI 的部分文本，也不回显数据库错误。三项财务差额为：`request_usage`、`request_hold`、`request_wallet`；七类异常为：`duplicate_settlement`、`unbilled_execution`、`missing_price_snapshot`、`missing_wallet_transaction`、`missing_usage`、`completed_pending`、`billing_exception`。三方金额固定为 `request_settled`、`model_usage`、`wallet_consumed`；安全发现当前只允许 `secret_leak`。金额以 CNY Decimal 文本输出。

**反向代理边界：** 反向代理只能从专用监控网络暴露该路径，必须删除 `X-Forwarded-For` 与 `Forwarded`，并覆盖而非追加 `X-Real-IP` 为代理直接看到的单一客户端 IP；代理自身地址必须显式位于 `INTERNAL_TRUSTED_PROXY_IPS`。网络隔离只是附加防护，应用层 Token 与来源 IP 双闸必须始终执行，禁止因请求来自内网、回环地址或反向代理而绕过任一安全闸。

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

**POST `/api/apps/{id}/launch`**（用户 JWT）：校验①应用 active 且已配 `access_url`；②确定本次进入对应的套餐 product_id。通过后签发随机短时票据。

Body（可选）：`{ entitlement_id }` —— 用户本次选择的权益 ID（`user_entitlements.id`）。
- **多套餐场景必传**：平台校验该权益归属本人、active、其父资产 `user_assets` 也为 active（冻结/暂停的资产不可进入）、且其商品挂在本应用名下，并由它反推 product_id，把 `entitlement_id` 一并写入票据透传给应用，从源头消除应用「只能识别第一个套餐」的问题；
- 缺省 / 为 0 时回退为「取用户在该应用下任一 active 资产」（单套餐，兼容旧前端）。

返回 data：`{ access_url, launch_ticket, expires_in }`（票据 `lt_` 前缀，TTL 60s，一次性）。
错误码：`40400` 应用不存在/未开放入口；`40003` 无使用权 / 所选权益无效或不属于该用户/应用。

端到端流程：用户在某套餐上点「进入应用」→ 前端调 launch 带上该套餐的 `entitlement_id` 拿 `{access_url, launch_ticket}` → 跳转 `{access_url}?ticket={launch_ticket}` → 应用后端调 verify 换身份（含 `entitlement_id`，可直接用于额度操作）。

**POST `/api/internal/app-launch/verify`**（`X-Internal-Token` 主闸 fail-closed + IP 白名单）：

Body：`{ launch_ticket }`。返回 data：`{ user_id, app_id, product_id, entitlement_id }`（校验通过并**消费**票据，Redis `GETDEL` 原子防重放）。
- `entitlement_id`：用户本次选定的权益 ID；应用可直接据此调内部 `entitlement-balance / reserve / settle / consume`，**无需再调 `user-entitlements` 解析猜测**。为 `0` 表示用户进入时未指定套餐（单套餐场景），应用按 `product_id` 自行兜底解析。

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
GET   /api/token/requests/{request_id}
GET   /v1/models
POST  /v1/chat/completions
GET   /v1/requests/{request_id}
GET   /api/token/usage
POST  /api/token/projects
GET   /api/token/projects
GET   /api/token/projects/{id}
PATCH /api/token/projects/{id}
POST  /api/token/projects/{id}/keys
GET   /api/token/projects/{id}/keys
POST  /api/token/projects/{id}/keys/{key_id}/rotate
DELETE /api/token/projects/{id}/keys/{key_id}
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
POST  /api/admin/token/billing/exceptions/{request_id}/resolve
```

> G2 文字执行契约：公开 Chat Completions 要求 Project SK，在用户状态/实名、Project、SK、显式模型权限、用户分组/角色可见性和模型发布状态通过后调用唯一 `RequestOrchestrator`，再进入统一 `ExecutionDriver`。部署默认 `native`，可显式切换 `bifrost`。Bifrost 响应会移除 `extra_fields`、路由信息、供应商响应头和内部 Key 名称；HTTP 200 业务错误仍按失败处理。Usage 缺失不生成计量行，禁止按 `max_tokens` 猜测。

> G4 治理契约：通过上述检查后，调用顺序固定为输入审核、G3 报价、MySQL 预算预留、Redis 四层资源准入、G3 钱包 hold、上游执行、输出审核、G3 结算与治理回收。安全、预算或 Redis 依赖异常均失败关闭。SSE 违规分段不得外泄，但必须继续收集可信 Usage；原始 Usage 使用 `provider` 行，冻结成本单价和平台成本金额使用独立 `provider_cost` 行，用户钱包 hold 全额释放、销售金额为 0，并写入 `billing_content_policy_waived` Outbox 事件。Usage 暂缺时保持 `settlement_pending` 与 hold，只能通过下述受控管理接口补录，不允许直接修改数据库。

**POST** `/api/admin/token/billing/content-policy/{request_id}/resolve` *(需 `ai_gateway:reconcile_manage` + 管理员二次认证)*

用于为 `error_code=output_moderation_blocked` 且处于 `settlement_pending` 或 `billing_exception` 的请求补录可信 Provider Usage。Body：

```json
{
  "prompt_tokens": 10,
  "completion_tokens": 5,
  "cached_tokens": 0,
  "reasoning_tokens": 0
}
```

接口先写管理员操作审计，再在单个 MySQL 事务中写入 `provider` 原始 Usage、按请求冻结价格快照计算 `provider_cost`、释放钱包 hold、将用户销售金额固定为 0，并写入 `billing_content_policy_waived` Outbox。相同 Usage 重放返回成功；已补录 Usage、请求状态或错误类型不一致返回 HTTP 409 + `40900`。该接口不接受正文、关键词证据或上游密钥，也不能对普通计费异常免单。

> G0/G1 商业账本契约：`request_id`、Project、SK、逻辑模型、执行模型、三类正交状态、标准 Usage 和执行尝试见 [`ai-gateway-g0-g1-contract.md`](./ai-gateway-g0-g1-contract.md)。G3 已在 G2 RequestOrchestrator 上启用价格快照、钱包预占和一次终态结算；公开 Chat 不再使用 G2 的 `unquoted` 规则。

> Request ID 与幂等：`X-Request-ID` 由墨灵生成，并由 Token Handler、执行驱动和账本共用。客户端重放可使用单值 `Idempotency-Key`；重复、逗号多值、空值或超过 191 字节在写账本前返回 400/40000。同用户、Project、SK、请求指纹返回已有状态，不同指纹返回 HTTP 409/code 40901。

Project 管理使用登录态 JWT，不能使用 SK 自助扩大权限。创建 body 为 `{name,timezone?}`；更新 body 可包含 `name`、`status=active|suspended|archived` 和 `timezone`。时区必须是有效 IANA 标识（如 `Asia/Shanghai`），创建时缺省为 `Asia/Shanghai`，禁止使用依赖宿主机的 `Local`。列表为扁平分页 `{items,page,page_size,total}`。

Project SK 创建 body：

```json
{
  "name": "生产服务",
  "scope_mode": "allowlist",
  "model_codes": ["molin/qwen-turbo"],
  "expires_at": "2026-12-31T16:00:00Z"
}
```

新 Key 默认 `allowlist`，空 allowlist 拒绝全部模型；`all` 必须显式选择且不能同时提交 `model_codes`。创建和轮换响应包含一次性 `secret_key` 并设置 `Cache-Control: no-store`；列表只返回 prefix、状态、权限和时间。停用 Project、吊销或过期 Key 均在上游调用前拒绝。

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
| max_tokens | integer | 否 | G3 最大输出 Token 数；缺省采用平台兜底上限与模型上限的较小值，显式非法或超过模型上限时拒绝报价 |
| n | integer | 否 | G3 仅允许 JSON 整数 `1`；字符串、浮点、指数写法或多候选值均在预占和上游调用前拒绝 |

说明：

- `stream = true` 时响应使用 Server-Sent Events（SSE）格式，`Content-Type: text/event-stream`。
- 网关不缓冲完整流式响应，但会按不超过 2 MiB 的有界段执行输出审核；段内公开字段和跨段增量生成文字均通过后才写出。空白、标点、斜杠和零宽格式字符不能用于拆分关键词。Bifrost 扩展元数据不对外透传，`[DONE]` 只在请求、attempt 和 Usage 成功持久化后发送。
- 当前一次请求只选择一个执行驱动。Native 使用模型绑定的活动渠道与 `upstream_model`；Bifrost 使用冻结的显式 Provider 模型映射。结果未知或已经输出 SSE 后禁止自动切换供应商，本阶段不启用加权随机和透明熔断回退。

### G4 内容安全、资源和预算治理接口

除用户事件和申诉外，以下接口统一要求 JWT 和管理员双重认证；治理列表统一使用 `ai_gateway:view`，只返回治理元数据且不返回提示词或模型正文。安全治理写操作使用 `ai_gateway:safety_manage`，资源治理写操作使用 `ai_gateway:resource_manage`，预算治理写操作使用 `ai_gateway:budget_manage`，补偿任务写操作使用 `ai_gateway:reconcile_manage`。所有写操作先写审计，失败时不执行变更。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/admin/token/safety/policies` | 策略版本列表 |
| POST | `/api/admin/token/safety/policies` | 创建 draft；body 只包含 rules，拒绝文案由平台固定且不可自定义 |
| POST | `/api/admin/token/safety/policies/{id}/publish` | 按 version_no 发布并退休旧 active |
| POST | `/api/admin/token/safety/policies/{id}/rollback` | 复制历史规则为新 active 版本 |
| GET | `/api/admin/token/safety/events` | 扁平分页安全事件 |
| GET | `/api/admin/token/safety/actions` | 扁平分页主体处置 |
| POST | `/api/admin/token/safety/actions` | 暂停 user 或 api_key |
| POST | `/api/admin/token/safety/actions/{id}/revoke` | 按 version_no 撤销处置 |
| GET | `/api/token/safety/events` | JWT 用户扁平分页查询本人事件 |
| POST | `/api/token/safety/appeals` | JWT 用户提交事件申诉 |
| GET | `/api/admin/token/safety/appeals` | 扁平分页申诉 |
| POST | `/api/admin/token/safety/appeals/{id}/resolve` | 按 version_no approved/rejected |
| GET | `/api/admin/token/resource-policies` | 扁平分页资源策略 |
| PUT | `/api/admin/token/resource-policies` | 乐观锁更新四层并发/RPM/TPM |
| GET | `/api/admin/token/budget-policies` | 扁平分页预算策略 |
| PUT | `/api/admin/token/budget-policies` | 乐观锁更新 Project/SK 日月预算 |
| GET | `/api/admin/token/budget-overrides` | 扁平分页临时增额 |
| POST | `/api/admin/token/budget-overrides` | 创建有有效期的临时增额 |
| GET | `/api/admin/token/budget-alerts` | 扁平分页预算阈值事件 |
| GET | `/api/admin/token/compensation-tasks` | 扁平分页补偿任务 |
| POST | `/api/admin/token/compensation-tasks/{id}/resolve` | 按 updated_at 转 retry/manual_review |
| POST | `/api/admin/token/outbox-events/{event_id}/requeue` | 按原 event_id 重试 dead 事件，请求体必须包含 `reason` |

策略规则项结构为 `{code,category,keywords}`；每个可发布版本必须完整覆盖 illegal、sexual、gambling、drugs、terror、hate、self_harm 七类，规范化后的单个关键词最长 256 字符。列表统一返回 `{items,page,page_size,total}`。用户事件接口只返回当前 JWT 用户的最小化事件，申诉时仓储层再次校验 event_id 归属。资源策略 scope_type 为 user/project/api_key/model，预算 scope_type 为 project/api_key；每个非空日/月限额都必须大于 0。Outbox 重试要求 `ai_gateway:reconcile_manage`、管理员二次认证、非空原因和前置审计，只允许 dead 状态按原 event_id 重排，重复或状态冲突返回 409。

G4/G8 错误码：40310 内容违规、40311 主体暂停、42920 hard 预算、42921 RPM/TPM、42922 并发、50320 审核不可用、50321 治理不可用、50330 商业流量总闸关闭。42921/42922 包含 `Retry-After`、`request_id` 和公开 `limit_scope`。50330 只表示新文字模型调用尚未开放或已受控关闭，不泄露生产配置缺项。
- G3 在上游调用前写不可变价格快照并创建钱包 hold。JSON 或正常结束 SSE 的可信 Usage 完整时，无论执行成功或明确失败都按四类 SKU 汇总一次金额并 settle；成功且存在正用量时才应用最低收费。只有确认未产生成本且无 Usage 时 release；Usage 缺失、不一致、结果未知或 SSE 未正常结束时，即使已经取得中间 Usage 也返回 `202/settlement_pending` 并保留 hold。Settlement Worker 对遗留 held/pending 请求按相同规则收敛，超过期限进入人工异常。正式链路不写旧 `token_usage_logs`。
- 请求状态可通过 `GET /api/token/requests/{request_id}` 或 OpenAI 兼容入口 `GET /v1/requests/{request_id}` 查询；必须使用原 Project SK，跨用户或跨 SK 统一拒绝且不泄露请求是否存在。
- `messages` 必须包含至少一条非空文字内容；G2 对图片、音频等多模态消息在写账本和调用上游前返回 400/40000。未实名返回 400/70001，渠道不可用返回 503/50300。
- 周期恢复扫描先找候选，再在事务锁内重新校验状态和截止时间，只把仍超过安全窗口的 pending/running 请求收敛为 `unknown`，不重放上游。Project SK 创建、轮换和吊销必须在同一数据库事务中写脱敏安全审计；审计组件缺失或持久化失败时返回 HTTP 503 + `50030`，并回滚密钥变更，禁止出现密钥状态已变化但安全审计缺失的事实断裂。紧急吊销可用性由数据库与审计表共用同一事务保证，测试环境和生产环境必须监控该错误码并优先恢复数据库写入能力。

返回 data：模型列表、OpenAI 兼容响应、Token 用量统计。

G3 前置或结算错误继续返回平台数字 `code`，并新增稳定字符串 `error`：

| HTTP | code | error | 说明 |
|---:|---:|---|---|
| 503 | 50310 | `pricing_unavailable` / `price_expired` | 无有效价格或成本过期 |
| 503 | 50311 | `margin_below_minimum` | 毛利低于发布下限 |
| 503 | 50330 | `ai_gateway_traffic_closed` | 生产商业流量总闸关闭；不创建请求账本、不调用上游、不产生扣费 |
| 400 | 40010 | `unquotable_request` | 显式 `max_tokens` 非法、超过模型上限、`n` 不为 1 或无法证明最大费用 |
| 402 | 60001 | `insufficient_balance` | 钱包可用余额不足，上游未调用 |
| 503 | 50312 | `wallet_hold_failed` | 预占事务失败且整体回滚 |
| 202 | 20201 | `settlement_pending` | 结果或 Usage 待确认，hold 保留 |
| 500 | 50010 | `billing_exception` | 超额或人工对账异常 |

相同 `Idempotency-Key` 重放通常返回已有 `request_id`、`execution_status` 和 `billing_status`，不重复调用上游或扣费；只有服务端确认请求从未写出上游且已释放 hold 时，才允许该幂等键原子转移到一个新请求并再次执行。SSE 已开始后不能改写 HTTP 状态；结算待确认或计费异常时发送 `event: molin.status`，其 `data` 只包含 `request_id` 和稳定 `error`，客户端随后调用请求状态接口查询。

**POST** `/api/admin/token/billing/exceptions/{request_id}/resolve` *(需 `token:manage` + 管理员二次认证)*

Body 使用 `resolution=release|settle`；`settle` 时同时提交 `prompt_tokens`、`completion_tokens`、`cached_tokens` 和 `reasoning_tokens`，且输入与输出合计必须大于 0；确认零成本必须使用 `release`。接口在资金操作前写包含核定用量的审计，审计失败则拒绝操作；原始 Provider Usage 与人工核定的 `reconciled` Usage 分别留存。Project/SK 越权统一返回 HTTP 403 + `40003`。

---

## 阿里云短信验证码阶段 1 契约

现有五个手机验证码入口覆盖 `register`、`login`、`reset_password`、`bind_phone`、`admin_verify`。成功响应统一为：

```json
{"code":0,"message":"ok","data":{"sent":true,"expires_in":600,"business_request_id":"平台业务请求标识","submit_status":"accepted"}}
```

手机号验证码在任何环境都不得返回明文 `code`。`business_request_id` 是平台追踪标识，不是阿里云原始请求标识。`SMS_ENABLED=false`、配置不完整、手机号不在 `SMS_TEST_PHONE_WHITELIST`、场景不在 `SMS_TEST_SCENE_ALLOWLIST` 或场景没有有效数据库绑定时返回 HTTP `503`、业务码 `50300`；未放行场景必须在 OTP 创建、限流占用、发送日志和供应商调用前失败关闭。供应商提交失败返回 HTTP `502`、业务码 `50200`。`accepted` 只表示供应商受理，不代表运营商最终送达。

阶段 1 不提供 `/api/admin/sms/*` 管理接口；模板同步、绑定管理和测试发送属于后续阶段。

## 阿里云短信验证码阶段 2 管理 API 契约

阶段 2 的九个接口统一要求 Bearer Token、对应短信权限及有效的管理员手机和邮箱双重认证。鉴权失败使用 `401/40001`，权限不足使用 `403/40003`，双重认证未完成使用 `403/40031`。所有写操作先记录 `sms` 模块请求审计，写入失败则不执行业务；业务完成后记录结果审计，结果审计失败产生安全告警但不得把已生效操作返回为 500。响应、日志、审计和数据库均不得出现 AccessKey、验证码明文或完整手机号。

权限码：

| 权限码 | 接口范围 |
|---|---|
| `sms:template:view` | 概览、模板列表/详情、场景列表和发送记录 |
| `sms:template:manage` | 场景绑定和模板本地启停 |
| `sms:template:sync` | 阿里云模板只读同步 |
| `sms:template:test` | 白名单测试提交 |

列表接口使用 D-95 扁平分页：`data={items,page,page_size,total}`。`page` 默认 1，`page_size` 默认 20、最大 100。

### 1. `GET /api/admin/sms/summary`

权限：`sms:template:view`。返回：

```json
{
  "template_total": 5,
  "approved_total": 5,
  "enabled_total": 5,
  "bound_scene_total": 5,
  "unbound_scene_total": 0,
  "last_synced_at": "2026-08-03T12:00:00Z"
}
```

从未同步时 `last_synced_at=null`。统计必须由后端一次查询完成，客户端不得通过拉取多页模板自行聚合。

### 2. `GET /api/admin/sms/templates`

权限：`sms:template:view`。查询参数：`page`、`page_size`、`keyword`、`audit_status`、`enabled`、`scene`。单条模板字段：

```json
{
  "id": 1,
  "provider": "aliyun",
  "template_code": "SMS_******",
  "template_name": "注册验证码",
  "template_type": "verification",
  "content": "验证码为 ${code}",
  "variables": ["code"],
  "provider_audit_status": "approved",
  "rejection_reason": null,
  "provider_updated_at": null,
  "local_enabled": true,
  "bound_scenes": ["register"],
  "version": 2,
  "last_synced_at": "2026-08-03T12:00:00Z",
  "created_at": "2026-08-03T12:00:00Z",
  "updated_at": "2026-08-03T12:00:00Z"
}
```

`provider_audit_status` 只允许 `pending/approved/rejected`。只有验证码类型、变量集合精确为 `code`、审核通过且固定签名匹配的模板可以本地启用；含额外变量的模板不得同步为可管理模板。

### 3. `GET /api/admin/sms/templates/{id}`

权限：`sms:template:view`。返回字段与模板列表单条一致。模板不存在返回 `404/40400「短信模板不存在」`。

### 4. `POST /api/admin/sms/templates/sync`

权限：`sms:template:sync`。请求必须无 body。服务端使用阿里云 `QuerySmsTemplateList` 分页读取账号下模板，只保存只读快照；不得调用创建、修改或删除模板/签名的接口。同步必须先完整取得供应商结果，再在数据库事务内串行应用，失败不得留下本轮部分快照。

```json
{
  "created_count": 1,
  "updated_count": 1,
  "unchanged_count": 3,
  "ignored_count": 0,
  "total_count": 5,
  "last_synced_at": "2026-08-03T12:00:00Z"
}
```

重复同步不得产生重复 `(provider,template_code)`，供应商异常返回 `502/50200`，总截止时间 10 秒。

### 5. `GET /api/admin/sms/scenes`

权限：`sms:template:view`。固定返回五个场景并使用 D-95。未绑定项返回 `template_id/template_code/template_name/provider_audit_status/sign_name/updated_by/updated_at=null`、`enabled=false`、`version=0`。

### 6. `PUT /api/admin/sms/scenes/{scene}`

权限：`sms:template:manage`。`scene` 只允许 `register/login/reset_password/bind_phone/admin_verify`。严格请求体：

```json
{"template_id":1,"enabled":true,"version":0}
```

不接受 `sign_name`。签名只能读取服务端 `SMS_ALIYUN_SIGN_NAME`。模板必须审核通过、类型为验证码、变量集合精确为 `code` 且本地启用；含额外变量的模板不得绑定。五个场景必须分别使用独立模板；同一模板已经绑定其他启用场景时返回 `409/40900「该模板已绑定其他短信场景，请为当前场景选择独立模板」`。`version` 必须与当前值一致；并发冲突返回 `409/40900「配置已被其他管理员修改，请刷新后重试」`。停用历史共用绑定不受独立模板检查阻断，以便先关闭再换绑整改。

### 7. `PATCH /api/admin/sms/templates/{id}/status`

权限：`sms:template:manage`。严格请求体：`{"enabled":true,"version":1}`。启用必须满足审核、类型、变量和签名约束；存在启用场景绑定时禁止停用，返回 `409/40900`。版本冲突同样返回 `409/40900`。

### 8. `POST /api/admin/sms/templates/{id}/test-send`

权限：`sms:template:test`，并强制管理员双重认证、`SMS_ENABLED=true`、`SMS_TEST_MODE=true` 和非空白名单。Header 必须包含 1～128 字节 `Idempotency-Key`；请求体严格为：

```json
{"scene":"register","phone":"<白名单手机号>"}
```

服务端校验目标模板正被该启用场景绑定、模板审核通过且本地启用。签名不接受客户端输入。幂等 scope 固定包含管理员、模板、场景和手机号 HMAC；同一管理员相同 key 与相同请求重放首次结果且不再次调用阿里云，相同 key 修改任一业务参数返回 `409/40900`，不同管理员互不串单。管理员和手机号两个维度分别限流；超限返回 `429/42900`，`Retry-After` 与 `data.retry_after_seconds` 一致。幂等重放不消耗新限流次数。

受理响应：

```json
{
  "business_request_id": "sms_******",
  "submit_status": "accepted",
  "idempotent": false,
  "template_code": "SMS_******",
  "phone_masked": "138****5678",
  "submitted_at": "2026-08-03T12:00:00Z"
}
```

`accepted` 只表示阿里云受理，不代表运营商送达或用户收到。供应商拒绝返回 `502/50200`，对应验证码或测试记录不可被当成成功结果。

### 9. `GET /api/admin/sms/send-logs`

权限：`sms:template:view`。支持 `page/page_size/scene/status/template_id/business_request_id/start_time/end_time`。时间为 RFC3339 闭区间，开始不得晚于结束，跨度最大 31 天。单条字段：`id/purpose/scene/phone_masked/template_id/template_code/sign_name/provider/business_request_id/provider_request_id/provider_code/submit_status/failure_summary/submitted_at/completed_at`。不返回手机号 HMAC、幂等摘要、请求指纹、验证码或供应商原始响应。

### 阶段 2 错误码

| HTTP/业务码 | 固定语义 |
|---|---|
| `400/40000` | 参数、分页、时间范围或 `Idempotency-Key` 不合法 |
| `401/40001` | 未登录或 Token 无效 |
| `403/40003` | 缺少对应短信权限 |
| `403/40031` | 未完成管理员双重认证 |
| `404/40400` | 模板不存在 |
| `409/40900` | 审核/绑定/启停/版本/幂等冲突；同一模板不得绑定多个启用场景 |
| `429/42900` | 管理员或手机号维度频率超限 |
| `502/50200` | 阿里云拒绝、超时、网络或未知供应商错误 |
| `503/50300` | 短信关闭、测试模式/白名单或运行配置不完整 |

## AI 网关 G5 管理工作台接口

所有接口要求管理员 JWT、有效双重认证和表中权限。写请求严格拒绝重复 JSON 键、未知字段和尾随文档；所有写操作先记录审计。

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/api/admin/token/overview` | `ai_gateway:view` | 模型、渠道、价格、路由和异常聚合 |
| GET | `/api/admin/token/models/{id}/versions` | `ai_gateway:view` | 不可变模型发布版本 |
| POST | `/api/admin/token/models/{id}/publish` | `ai_gateway:model_manage` | body：`{"reason":"..."}`；相同快照幂等返回既有版本；发布门禁错误为 `40910` 文档未就绪、`40911` 生效价格数量异常、`40912` 健康路由缺失、`40913` 状态并发变化 |
| POST | `/api/admin/token/models/{id}/unpublish` | `ai_gateway:model_manage` | 下架且退役当前快照 |
| POST | `/api/admin/token/models/{id}/rollback` | `ai_gateway:model_manage` | body：`{"target_version_no":1,"reason":"..."}`；创建新发布版本 |
| POST | `/api/admin/token/channels/{id}/health-check` | `ai_gateway:route_manage` | 只访问渠道根 `/health`，不携带密钥且不调用模型；默认仅允许公网 HTTPS，并在实际拨号前校验全部 DNS 结果，拒绝 loopback、link-local、RFC1918、IPv6 本地地址和重定向。测试 Bifrost 内网目标必须由 `AI_GATEWAY_HEALTH_INTERNAL_ALLOWLIST` 精确放行 |
| GET/POST | `/api/admin/token/routes` | view / route_manage | Bifrost 路由列表与创建 |
| PUT | `/api/admin/token/routes/{id}` | `ai_gateway:route_manage` | 全量提交路由及当前 `version_no` |
| GET/POST | `/api/admin/token/prices` | view / price_manage | 价格版本列表与草稿创建 |
| GET | `/api/admin/token/prices/{id}` | `ai_gateway:view` | 价格版本及四项 SKU |
| POST | `/api/admin/token/prices/{id}/approve` | `ai_gateway:price_manage` | 草稿审批 |
| POST | `/api/admin/token/prices/{id}/publish` | `ai_gateway:price_manage` | 发布已审批版本 |
| POST | `/api/admin/token/prices/{id}/suspend` | `ai_gateway:price_manage` | body：`{"reason":"..."}` |
| POST | `/api/admin/token/prices/{id}/retire` | `ai_gateway:price_manage` | 退役价格版本 |
| POST | `/api/admin/token/prices/{id}/rollback` | `ai_gateway:price_manage` | 复制历史 SKU 为新草稿，必须重新审批发布 |

`overview` 支持 `from/to/model/channel_id/status`，时间窗最大 90 天。金额字段均为人民币十进制字符串：销售额来自已结算请求，成本从请求不可变价格快照和计量事实反算，毛利为销售额减成本。治理拒绝来自脱敏拒绝事件，不保存提示词、响应内容或密钥。

G5 路由仅在确认请求未发送时按 `max_retries` 重试；超时、结果未知、已收到 HTTP/SSE 数据均禁止重试。安全失败达到阈值后写入共享熔断表并开启 30 秒窗口，后续请求按优先级和回退顺序选择其他路由。

新增权限：`ai_gateway:model_manage`、`ai_gateway:price_manage`、`ai_gateway:route_manage`。冲突统一返回 `409/40900`，不满足发布门禁返回 `409/40900`，参数错误返回 `400/40000`。

## AI 网关 G6 用户端模型市场与请求账本接口

以下接口均要求用户 JWT，`user_id` 只取鉴权上下文，禁止由 Query 或 Body 覆盖。模型目录还执行发布快照可见性规则；Project、平台 SK、请求、钱包关联和申诉均强制本人隔离。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/token/catalog/models` | 已发布文字模型；支持 `q/provider/capability/service_status/context_min/context_max/sort/page/page_size` |
| GET | `/api/token/catalog/models/{model_code}` | 发布快照详情、当前人民币销售价格及文档健康状态 |
| GET | `/api/token/customer/usage/overview?timezone=Asia/Shanghai` | 今日、本月请求、Token 和已结算金额按展示时区统计；预算进度按各 Project 在准入时固化的月周期聚合，禁止用单一展示时区重切预算周期 |
| GET | `/api/token/customer/limits` | 用户、Project、平台 SK 经父级收紧后的有效并发/RPM/TPM、来源，以及包含当前临时增额的 G4 实际预算策略 |
| GET | `/api/token/customer/requests` | 本人请求账本，支持 `project_id/api_key_id/model/status/start/end/page/page_size` |
| GET | `/api/token/customer/requests/{request_id}` | 三维状态、确认用量、价格版本、销售计价行、钱包流水和申诉 |
| GET | `/api/token/customer/requests/export` | 本人 CSV；必须提供不超过 93 天的 `start/end`，最多 5000 行 |
| POST | `/api/token/customer/requests/{request_id}/disputes` | 对本人请求提交唯一账单申诉，body `{"reason":"10-1000 字"}`；检测到 API Key、Bearer Token、JWT 或其他密钥样式时拒绝入库 |

模型目录使用扁平分页 `{items,page,page_size,total}`。每个模型只返回公开代码、名称、厂商、说明、能力、上下文、文档 URL 与健康状态、发布版本、服务状态和人民币销售 SKU；禁止返回渠道、Bifrost 地址、上游模型、成本价或密钥。公开内容来自 `ai_model_release_versions.snapshot_json`，当前价格和健康路由只作为运行状态聚合，后台未发布工作副本不得提前泄漏。

请求详情的 `price_lines[]` 包含 `meter_type/meter_source/quantity/sale_unit_price/scale/amount/currency`；`meter_source=provider_confirmed` 表示上游确认用量。金额和 Token 数量使用 Decimal JSON 字符串。详情不返回提示词、响应正文、完整 SK、内部执行模型或上游响应头。

CSV 使用 UTF-8 BOM，任何以 `= + - @` 开头的单元格增加安全前缀；导出和申诉均写审计。申诉按 `(request_id,user_id)` 幂等，重复提交返回 409。未实名用户不能签发或轮换可调用平台 SK；吊销仍允许，以便用户及时止损。
