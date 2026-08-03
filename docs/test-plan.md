# 测试计划

## 1. 测试策略

```text
单元测试（开发者负责）
  - 每个 service 方法都有对应单元测试
  - 覆盖率目标：核心业务模块 > 70%
  - 工具：Go testing 标准库 + testify

集成测试（开发者负责）
  - 测试完整 HTTP 请求链路
  - 使用测试数据库（molin_test）
  - 工具：net/http/httptest

接口测试（测试/产品负责）
  - 测试所有 API 接口
  - 工具：curl 或 Postman / Bruno

功能验收测试（测试/产品负责）
  - 每周验收，测试完整业务流程
  - 手动操作 UI 验证

安全测试（开发者 + 产品共同执行）
  - 权限绕过测试
  - 并发扣费测试
  - 幂等性测试
```

## 2. 后端单元测试文件位置

每个模块测试文件与被测文件放在同一目录：

```text
server/internal/modules/auth/
  service/
    auth_service.go
    auth_service_test.go        -- 注册、登录、退出、刷新 Token 单元测试

server/internal/modules/iam/
  service/
    iam_service.go
    iam_service_test.go         -- 权限计算优先级测试

server/internal/modules/billing/
  service/
    wallet_service.go
    wallet_service_test.go      -- 扣费事务、余额不足、乐观锁冲突测试
    payment_service.go
    payment_service_test.go     -- 支付回调幂等测试

server/internal/modules/product/
  service/
    pricing_service_test.go     -- 价格优先级：会员价 > 角色价 > 默认价

server/internal/modules/finance_consumer/
  service/
    consumer_service_test.go    -- 消费事件幂等测试

server/internal/modules/asset/
  service/
    asset_service_test.go
    entitlement_service_test.go -- 权益原子消耗测试（并发）
```

## 3. 接口测试用例

### 3.1 认证模块

**原有接口：**

| 用例 | 接口 | 输入 | 期望结果 |
|---|---|---|---|
| 邮箱注册成功 | POST /api/auth/register/email | 正确邮箱、密码、验证码 | 201，返回 access_token |
| 重复邮箱注册 | POST /api/auth/register/email | 已注册邮箱 | 409，code=40900 |
| 验证码错误 | POST /api/auth/register/email | 错误验证码 | 400，code=40000 |
| 邮箱登录成功 | POST /api/auth/login/email | 正确邮箱、密码 | 200，返回 token 对 |
| 密码错误 | POST /api/auth/login/email | 错误密码 | 400，code=40000 |
| 退出登录 | POST /api/auth/logout | refresh_token | 200，再次刷新返回 401 |
| 刷新令牌 | POST /api/auth/refresh | 有效 refresh_token | 200，新 access_token |
| 用吊销的 Token 刷新 | POST /api/auth/refresh | 已退出的 refresh_token | 401 |
| 验证码限流 | POST /api/auth/verification-codes/email | 连续 11 次 | 第 11 次返回 429 |

**★ 统一注册（POST /api/auth/register）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 统一注册成功（手机+邮箱双OTP） | 正确手机/邮箱/密码/双验证码 | 201，返回 token 对，phone_verified/email_verified=true |
| 手机号重复 | 已注册手机号 | 409，code=40900 |
| 邮箱重复 | 已注册邮箱 | 409，code=40900 |
| 用户名重复 | 已存在用户名 | 409，code=40900 |
| 手机验证码错误 | 错误 phone_code | 400，code=40000 |
| 邮箱验证码错误 | 错误 email_code | 400，code=40000 |
| 用户名过短（1位） | username="a" | 400 |
| 用户名过长（33位） | username 超长 | 400 |
| 用户名含非法字符 | username 含空格/特殊符号 | 400 |

**★ OTP 密码重置（POST /api/auth/password/reset）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 手机 OTP 重置成功 | 正确手机号、验证码、新密码 | 200；旧密码无法登录；新密码可登录 |
| 邮箱 OTP 重置成功 | 正确邮箱、验证码、新密码 | 200；旧密码无法登录 |
| 重置后旧 Refresh Token 失效 | 使用旧 refresh_token 刷新 | 401（全部会话已吊销） |
| 验证码错误 | 错误 code | 400，code=40000 |
| 不存在的手机/邮箱 | 未注册账号 | 400 |
| 非法 target_type | target_type="wechat" | 400 |

**★ 修改用户名（PATCH /api/me/username）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 修改成功 | 合法新用户名 | 200；GET /api/me 返回新用户名 |
| 用户名重复 | 已存在用户名 | 409，code=40900 |
| 用户名非法 | 含特殊字符 | 400 |
| 无 Token | 无 Authorization 头 | 401 |

**★ 修改手机号（PATCH /api/me/phone）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 修改成功 | 新手机号 + 正确验证码（scene=bind_phone） | 200；phone_verified=true |
| 验证码错误 | 错误 code | 400，code=40000 |
| 无 Token | 无 Authorization 头 | 401 |

**★ 修改邮箱（PATCH /api/me/email）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 修改成功 | 新邮箱 + 正确验证码（scene=bind_email） | 200；email_verified=true |
| 验证码错误 | 错误 code | 400，code=40000 |
| 无 Token | 无 Authorization 头 | 401 |

**★ 管理员手机双重认证（POST /api/admin/auth/verify-phone）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 认证成功 | 管理员 Token + 正确验证码（scene=admin_verify） | 200；admin_phone_verified=true |
| 验证码错误 | 正确 Token + 错误验证码 | 400，code=40000 |
| 无 Token | 无 Authorization 头 | 401 |
| 普通用户访问 | 无 user:manage 权限的 Token | 403，code=40003 |

**★ 管理员邮箱双重认证（POST /api/admin/auth/verify-email）：**

| 用例 | 输入 | 期望结果 |
|---|---|---|
| 认证成功 | 管理员 Token + 手机已认证 + 正确邮箱验证码 | 200；admin_email_verified=true |
| 验证码错误 | 正确 Token + 错误验证码 | 400，code=40000 |
| 无 Token | 无 Authorization 头 | 401 |
| 普通用户访问 | 无 user:manage 权限的 Token | 403，code=40003 |

### 3.2 实名认证

| 用例 | 接口 | 期望结果 |
|---|---|---|
| 提交实名认证 | POST /api/identity/verifications | 200，status=pending |
| 重复提交 | POST /api/identity/verifications | 400，审核中不可重复提交 |
| 未实名购买商品 | POST /api/products/:id/purchase | 400，code=70001 |
| 审核通过 | PATCH /api/admin/identity-verifications/:id/review | 200，用户 real_name_status=verified |
| 审核拒绝 | PATCH /api/admin/identity-verifications/:id/review | 200，用户可重新提交 |

### 3.3 商品与购买

| 用例 | 接口 | 期望结果 |
|---|---|---|
| 用户查看商品列表 | GET /api/products | 只返回该用户角色 can_view=true 的商品 |
| 普通用户买普通应用 | POST /api/products/:id/purchase | 200，扣费+生成资产 |
| VIP 用户买有角色价商品 | POST /api/products/:id/purchase | 按角色价扣费 |
| 会员用户买会员价商品 | POST /api/products/:id/purchase | 按会员价扣费 |
| 余额不足 | POST /api/products/:id/purchase | 400，code=60001 |
| 无购买权限 | POST /api/products/:id/purchase | 403，code=40003 |
| 重复购买（同 Idempotency-Key）| POST /api/products/:id/purchase | 200，返回原订单（不重复扣费） |
| 缺少 Idempotency-Key | POST /api/products/:id/purchase | 400，code=40000 |

### 3.4 钱包与充值

| 用例 | 接口 | 期望结果 |
|---|---|---|
| 查看余额 | GET /api/wallet | 返回当前余额 |
| 创建充值订单 | POST /api/recharge/orders | 200，返回 pay_url |
| 支付回调处理 | POST /api/payments/notify/wechat | 200，钱包余额增加 |
| 重复回调 | POST /api/payments/notify/wechat | 200（幂等），余额不重复增加 |
| 签名错误的回调 | POST /api/payments/notify/wechat | 400，余额不变 |

### 3.5 权限控制

| 用例 | 期望结果 |
|---|---|
| 无 token 访问需要登录的接口 | 401，code=40001 |
| 普通用户访问管理员接口 | 403，code=40003 |
| 管理员给用户添加 deny 权限后，用户无法访问对应接口 | 403 |
| 管理员给用户移除 deny 权限后，用户恢复访问 | 200 |
| 修改角色权限后，缓存失效，新权限立即生效 | 修改后立即生效，不需等 5 分钟 |
| 封禁用户后其 Token 立即失效 | 401 |

### 3.6 阿里云短信与短信模板管理

> 本节是短信功能的正式测试策略，契约基线为 `docs/full-api-design.md §3.19`、
> `docs/frontend-api-reference.md §五之二` 和 `docs/database-schema-design.md §3.1.1`。
> 阶段 1 已进入开发验证；状态列区分本地单元证据和仍待隔离 MySQL/QA 执行的项目。任何 Mock 结果都不得声明为阿里云受理或真实手机收件。
> 旧公开发码端点及 `target` 字段的清理范围见 `docs/sms-template-management-test-cleanup.md`。

#### 3.6.1 阶段 1：数据迁移与关闭态发送链路

| 编号 | 测试项 | 测试方法 | 期望结果 | 当前状态 |
|---|---|---|---|---|
| SMS-S1-01 | `verification_codes.code` 扩容 | migration 升级、降级及边界值测试 | SHA-256 十六进制哈希可完整保存 64 字符；升级不截断数据 | 隔离 MySQL 8.0.46 up/down 通过（2026-08-03） |
| SMS-S1-02 | 历史记录迁移 | 构造历史手机、历史邮箱和已过期记录后执行 migration | `send_status` 默认 `not_applicable`；历史手机号不回填为 `sent`；迁移前等待旧 10 分钟 OTP 窗口耗尽 | 隔离 MySQL 8.0.46 fixture 通过（2026-08-03） |
| SMS-S1-03 | 新手机验证码状态机 | 使用供应商 Mock 分别返回受理、拒绝、超时和网络错误 | 新记录先为 `pending`；仅受理后转为 `sent`；失败转为 `failed` | 本地单测通过 |
| SMS-S1-04 | 手机发送失败不可校验 | Mock 返回失败后提交正确验证码 | `failed` 或非 `sent` 手机记录均无法通过校验，且不可被原子消费 | 本地单测及隔离 MySQL 的 `pending/not_applicable/过期 sent` 用例通过 |
| SMS-S1-05 | 邮箱回归 | 执行注册、登录、重置密码、换绑邮箱和管理员邮箱验证 | 邮箱保持 `not_applicable`，不调用短信适配器，原有校验和单次消费行为不受影响 | 服务/仓储单测通过；全业务回归待 QA |
| SMS-S1-06 | 五场景模板选择 | 使用数据库 fixture 配置 `register/login/reset_password/bind_phone/admin_verify` | 每个场景只读取自身数据库绑定，不读取 `SMS_TEMPLATE_CODE_*`，不串用模板 | 本地 fixture + Mock 通过 |
| SMS-S1-07 | 三张短信表与仓储约束 | migration 升降级、唯一索引和外键/并发测试 | `sms_templates`、`sms_scene_bindings`、`sms_send_logs` 可升降级；模板编码、场景和业务请求标识满足唯一性约束 | 仓储单测及隔离 MySQL 约束/并发通过（2026-08-03） |
| SMS-S1-08 | 功能开关关闭 | 保持 `SMS_ENABLED=false` 调用全部手机发码入口 | 返回 `503/50300`；不调用真实阿里云；不产生可校验手机验证码；邮箱链路仍可用 | 服务与错误映射单测通过；HTTP 全入口待 QA |
| SMS-S1-09 | 配置 fail-closed | 分别缺失供应商、AccessKey、签名、端点、HMAC 密钥或场景绑定 | 启动失败或拒绝短信提交，不回退 Mock、固定验证码或明文验证码响应 | 本地单测通过 |
| SMS-S1-10 | 敏感信息 | 扫描响应、应用日志、审计日志和数据库 | 不出现验证码明文、完整手机号、AccessKey、请求签名原文或完整供应商响应；手机号仅保留脱敏值和独立 HMAC | 模型/响应单测通过；运行态与数据库扫描待 QA |

阶段 1 只允许使用仓储 fixture 和供应商 Mock 验证内部行为。Mock 通过不能证明阿里云账号、签名、模板或网络可用，也不能证明真实手机收到短信。

#### 3.6.2 阶段 2：管理接口、真实阿里云受理与安全测试

九个管理接口必须逐项覆盖：

| 编号 | 方法与路径 | 权限 | 核心检查 | 当前状态 |
|---|---|---|---|---|
| SMS-A01 | `GET /api/admin/sms/summary` | `sms:template:view` | 统计口径正确；从未同步时 `last_synced_at=null`；无客户端多页聚合假设 | 待执行 |
| SMS-A02 | `GET /api/admin/sms/templates` | `sms:template:view` | 筛选、边界分页、空列表及 D-95 `{items,page,page_size,total}` | 待执行 |
| SMS-A03 | `GET /api/admin/sms/templates/{id}` | `sms:template:view` | 完整字段、可空字段、404/40400 和敏感信息边界 | 待执行 |
| SMS-A04 | `POST /api/admin/sms/templates/sync` | `sms:template:sync` | 无 body；幂等计数；供应商失败无部分写；后端总截止 10 秒 | 待执行 |
| SMS-A05 | `GET /api/admin/sms/scenes` | `sms:template:view` | 固定五场景、D-95；未绑定字段为 `null`、`enabled=false`、`version=0` | 待执行 |
| SMS-A06 | `PUT /api/admin/sms/scenes/{scene}` | `sms:template:manage` | 只接收 `template_id/enabled/version`；不接收 `sign_name`；版本冲突返回 409/40900 | 待执行 |
| SMS-A07 | `PATCH /api/admin/sms/templates/{id}/status` | `sms:template:manage` | 乐观锁、审核状态约束及有效绑定阻止停用 | 待执行 |
| SMS-A08 | `POST /api/admin/sms/templates/{id}/test-send` | `sms:template:test` | 白名单、场景绑定、`Idempotency-Key`、双维度限流和受理语义 | 待执行 |
| SMS-A09 | `GET /api/admin/sms/send-logs` | `sms:template:view` | D-95、筛选、可空字段、RFC3339 闭区间、开始不晚于结束、最大 31 天及脱敏 | 待执行 |

四个权限必须分别创建最小权限管理员测试，不能只用超级管理员覆盖：

| 权限测试 | 期望结果 | 当前状态 |
|---|---|---|
| 无 Token 调用任一短信管理接口 | `401/40001` | 待执行 |
| 已登录但未完成管理员双重认证 | `403/40031` | 待执行 |
| 仅有 `sms:template:view` | 只允许 A01/A02/A03/A05/A09；写接口返回 `403/40003` | 待执行 |
| 仅追加 `sms:template:manage` | 允许 A06/A07，不获得同步和测试发送能力 | 待执行 |
| 仅追加 `sms:template:sync` | 只新增 A04 能力 | 待执行 |
| 仅追加 `sms:template:test` | 只新增 A08 能力 | 待执行 |
| 权限 seed 重复执行 | 不产生重复权限或重复角色绑定 | 待执行 |

同步、绑定和测试发送必须补充以下并发与幂等用例：

- 同一阿里云模板连续同步和并发同步，最终仅有一条 `(provider, template_code)` 快照，计数稳定，不重复创建。
- 同步中途超时、阿里云拒绝或网络异常返回 `502/50200`，本次不写入部分快照；不得把旧快照误报为本次同步成功。
- 两个管理员持同一场景版本并发改绑，最多一个成功，另一个返回 `409/40900`；失败方重新读取最新版本后才能重试。
- 测试发送缺少 `Idempotency-Key` 返回 `400/40000`；同一管理员、相同 Key 和相同请求体串行或并发重试，只调用一次阿里云并返回首次 `business_request_id`。
- 相同管理员复用同一 Key 但修改手机号、模板或场景，返回 `409/40900`；不同管理员使用相同 Key 互不串单。
- 测试手机号不在白名单返回 `400/40000`；白名单为空时全拒；完整手机号不得持久化或出现在日志中。
- 测试发送的 `scene` 必须属于目标模板当前已启用的 `bound_scenes`；未绑定、绑定停用、模板未审核通过或本地停用时不得提交。
- 按管理员和手机号两个维度分别触发限流，返回 `429/42900`；HTTP `Retry-After` 与 `data.retry_after_seconds` 一致；幂等重放不消耗新的限流次数。
- `sign_name` 只允许来自 `SMS_ALIYUN_SIGN_NAME`；请求体注入签名字段不能改变绑定或发送签名。
- 阿里云拒绝、签名错误、模板错误、账户异常、超时及网络错误统一清洗并映射为安全错误，不泄露原始供应商响应。

#### 3.6.3 阶段 4：五场景全链路回归与证据分级

| 场景 | 发码入口 | 后续业务 | 必须验证 | 当前状态 |
|---|---|---|---|---|
| `register` | `POST /api/auth/verification-codes/phone`，body 使用 `phone/scene` | 统一注册 | 独立注册模板、正确签名、验证码可单次消费 | 待执行 |
| `login` | `POST /api/auth/verification-codes/phone`，body 使用 `phone/scene` | 手机验证码登录 | 独立登录模板，不可与注册验证码串用 | 待执行 |
| `reset_password` | `POST /api/auth/verification-codes/phone`，body 使用 `phone/scene` | 重置密码 | 独立重置模板、成功后旧会话失效 | 待执行 |
| `bind_phone` | `POST /api/me/verification-codes/phone`，body 仅含新 `phone` | 换绑手机号 | 必须登录；公开端点传该 scene 被拒；成功后手机号更新 | 待执行 |
| `admin_verify` | `POST /api/admin/auth/verification-codes/phone`，无 body | 管理员手机双重认证 | 发往当前管理员绑定手机号；公开端点传该 scene 被拒 | 待执行 |

邮箱注册、登录、重置密码、`POST /api/me/verification-codes/email` 换绑邮箱及
`POST /api/admin/auth/verification-codes/email` 管理员邮箱验证必须全量回归，证明短信改造未将邮箱接入短信适配器，也未错误要求 `send_status=sent`。

验收证据必须分层记录，禁止统一写成“短信发送成功”：

| 证据层级 | 可以证明 | 不能证明 |
|---|---|---|
| 单元测试、fixture、Mock | 参数组装、模板选择、状态机、错误映射、幂等与本地分支 | 阿里云账号、签名、模板、网络或真实收件可用 |
| 阿里云返回 `Code=OK` / `submit_status=accepted` | 本次请求被阿里云受理并获得可追踪请求标识 | 运营商已送达或用户已收到 |
| 白名单真实手机收件记录 | 指定手机收到本次正确签名、场景文案和验证码 | 全量用户送达率及长期稳定性 |

真实链路验收必须保存脱敏的时间、场景、模板编码、`business_request_id`、供应商请求标识和收件确认，禁止保存验证码、完整手机号或密钥。只有外部准备项已核验、`SMS_TEST_MODE=true`、白名单非空且获得测试授权后，才允许执行真实阿里云提交和收件验证。

## 4. 并发与安全测试

### 4.1 并发扣费测试（必须通过）

```text
场景：用户余额 100 元，同时发起 10 个并发请求各扣 20 元
期望：只有 5 个请求成功，剩余 5 个返回余额不足（60001）
方法：使用 wrk 或 ab 工具，或 Go 并发测试
```

```go
// server/internal/modules/billing/service/wallet_service_test.go
func TestConcurrentDeduct(t *testing.T) {
    // 初始化余额 100
    // 10 个 goroutine 同时扣 20
    // 断言：成功次数 = 5，最终余额 = 0，无负数
}
```

### 4.2 幂等性测试

```text
场景：同一 Idempotency-Key 并发发送 5 次购买请求
期望：只生成 1 个订单，只扣费 1 次，只生成 1 个资产
```

### 4.3 权限绕过测试

```text
场景 1：伪造 JWT（修改 payload 后不更新签名），访问需要登录的接口
期望：401

场景 2：使用普通用户 Token 请求 /api/admin/* 接口
期望：403

场景 3：修改 URL 中的 :id 访问他人资产（如 GET /api/my/assets/999）
期望：404 或 403（不能看到他人数据）
```

## 5. 每周验收 Checklist

### Week 1–2 验收标准

```text
□ 管理员可以用邮箱登录管理后台
□ 管理员可以创建角色（platform_admin / normal_user）
□ 管理员可以创建普通用户并分配角色
□ 用户可以用邮箱注册（验证码 → 注册 → 登录）
□ 用户可以用手机号注册
□ 用户可以提交实名认证
□ 管理员可以审核通过实名认证
□ 未实名用户购买商品返回 70001
□ 退出登录后原 Token 不可用
```

### Week 3 验收标准（核心购买闭环）

```text
□ 管理员后台创建商品 → 配置套餐 → 配置价格 → 配置角色权限
□ 用户控制台看到可购买商品
□ 用户余额充值（支付回调模拟）
□ 用户购买商品 → 扣费 → 生成订单 → 生成资产
□ 用户在「我的资产」看到已购买资产
□ 管理员后台看到订单记录
□ 管理员后台看到钱包流水
□ 同一请求重复发送不重复扣费（幂等测试）
□ 余额不足时返回正确错误码
□ 10 并发扣费不出现负余额
```

### Week 4 验收标准

```text
□ 会员用户购买商品按会员价扣费
□ 会员专属商品非会员用户不可购买
□ 管理员发布公告，用户端可见
□ 管理员创建帮助文档，用户端可搜索
□ 帮助文档按可见范围正确过滤
```

## 6. 测试环境数据初始化

测试开始前需要初始化以下基础数据：

```sql
-- 初始化角色
INSERT INTO roles (code, name) VALUES
  ('platform_admin', '平台管理员'),
  ('finance_admin',  '财务管理员'),
  ('ops_admin',      '运维管理员'),
  ('normal_user',    '普通用户'),
  ('vip_user',       'VIP 用户');

-- 初始化平台管理员账号（密码：Admin@123456，bcrypt hash 替换）
INSERT INTO users (email, email_verified, password_hash, real_name_status, status)
  VALUES ('admin@molin.io', 1, '$2a$10$...', 'verified', 'active');

-- 给管理员分配角色
INSERT INTO user_roles (user_id, role_id)
  VALUES (1, (SELECT id FROM roles WHERE code = 'platform_admin'));
```

测试数据初始化脚本：`scripts/seed_test_data.sh`（运维负责创建）。

## 7. 缺陷管理

- 缺陷在 Git Issues 中跟踪。
- 优先级：P0（生产阻断）/ P1（核心功能缺陷）/ P2（一般缺陷）/ P3（体验问题）。
- P0、P1 缺陷必须在下一个迭代前修复，不得上线。
- 每个缺陷 Issue 必须包含：复现步骤、期望结果、实际结果、截图或日志。
