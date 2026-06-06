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
