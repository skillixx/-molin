# Auth 模块 — 后端 A 负责

## 职责边界

只负责：注册、登录、退出、刷新令牌、验证码、当前用户、修改密码。

不负责：角色权限（iam 模块）、实名认证（identity 模块）、审计日志（audit 模块）。

## 需要创建的文件

```text
model/
  user.go           -- users 表结构体
  session.go        -- user_sessions 表结构体
  verification.go   -- verification_codes 表结构体
  login_log.go      -- user_login_logs 表结构体

repository/
  user_repo.go      -- 用户 CRUD，含按邮箱/手机号查询
  session_repo.go   -- 会话 CRUD，含按 refresh_token_hash 查询、写入 revoked_at
  verification_repo.go  -- 验证码写入 / 查询 / 标记已使用
  login_log_repo.go -- 写入登录日志

service/
  auth_service.go       -- Register / Login / Logout / Refresh 业务逻辑
  verification_service.go -- 发送验证码、校验验证码

handler/
  auth_handler.go       -- HTTP Handler，不含业务逻辑

dto/
  auth_dto.go           -- 所有请求和响应 DTO

route.go                -- 注册路由到 mux
```

## 关键类型

```go
// user.go
type User struct {
    ID               uint64
    Email            string
    EmailVerified    bool
    Phone            string
    PhoneVerified    bool
    Nickname         string
    AvatarURL        string
    RealNameStatus   string  // unverified / pending / verified / rejected
    PasswordHash     string
    Status           string  // active / disabled
    WalletID         uint64
    CreatedAt        time.Time
    UpdatedAt        time.Time
}

// session.go
type UserSession struct {
    ID                 uint64
    UserID             uint64
    RefreshTokenHash   string    // HMAC-SHA256(token, REFRESH_TOKEN_SECRET)
    UserAgent          string
    IP                 string
    ExpiresAt          time.Time
    RevokedAt          *time.Time  // NULL 表示有效
    CreatedAt          time.Time
}
```

## 安全约定

- 密码：`bcrypt` hash，使用 `server/pkg/crypto/password.go`。
- Refresh Token：生成 UUID 或 crypto/rand 随机串，数据库只存 `HMAC-SHA256(token, env:REFRESH_TOKEN_SECRET)`，不存明文。
- 退出登录：写入 `user_sessions.revoked_at = NOW()`，刷新令牌时先检查 `revoked_at IS NULL`。
- 封禁用户：更新 `users.status = disabled`，同时批量写入该用户所有活跃会话的 `revoked_at`。
- 验证码：TTL 10 分钟，使用后写入 `used_at`，同一验证码不可重复使用。
- 登录限流：依赖 `server/internal/middleware/rate_limit.go`，不在 service 层做限流。

## 接口清单

```text
POST /api/auth/verification-codes/email
POST /api/auth/verification-codes/phone
POST /api/auth/register/email
POST /api/auth/register/phone
POST /api/auth/login/email
POST /api/auth/login/phone
POST /api/auth/logout
POST /api/auth/refresh
GET  /api/me
PATCH /api/me/profile
PATCH /api/me/password
```

## 注册流程

```text
校验请求参数
  -> 验证验证码（scene = register）
  -> 检查邮箱/手机号是否已被注册
  -> bcrypt hash 密码
  -> 写入 users 表
  -> 创建 wallets 记录（调用 billing 模块 service，或发 MQ 事件）
  -> 生成 Access Token + Refresh Token
  -> 写入 user_sessions
  -> 返回 token 对
```

## 依赖关系

- 依赖 `server/pkg/jwt` — Token 生成
- 依赖 `server/pkg/crypto` — 密码 hash、HMAC
- 不依赖其他业务模块（注意：钱包初始化可以通过 MQ 事件异步完成）
