# Auth 模块 — 后端 A 负责

## 职责边界

只负责：注册、登录、退出、刷新令牌、验证码发送/校验、当前用户信息、修改密码。

不负责：角色权限（iam 模块）、实名认证（identity 模块）、审计日志（audit 模块）。

---

## Week 1 任务清单（按顺序执行）

```text
基础设施（先于所有模块）：
✅ server/pkg/db/db.go           — MySQL 连接池（GORM）
✅ server/pkg/cache/redis.go     — Redis 客户端
✅ server/pkg/crypto/password.go — bcrypt hash/verify
✅ server/pkg/crypto/hmac.go     — HMAC-SHA256
✅ server/pkg/jwt/jwt.go         — Access Token 生成/校验
✅ server/internal/config/config.go — 补充 DB/Redis/JWT/AdminVerifyExpireHours 配置字段

Auth 模块：
✅ model/user.go                 — 含 username / admin_phone_verified_at / admin_email_verified_at
✅ model/session.go
✅ model/verification.go
✅ model/login_log.go
✅ repository/user_repo.go
✅ repository/session_repo.go
✅ repository/verification_repo.go
✅ repository/login_log_repo.go
✅ service/auth_service.go       — 含统一注册、OTP密码重置、管理员双重认证、修改用户名/手机/邮箱
✅ service/verification_service.go
✅ handler/auth_handler.go
✅ dto/auth_dto.go
✅ route.go

Migration：
✅ server/migrations/000001_create_auth_tables.up.sql
✅ server/migrations/000001_create_auth_tables.down.sql
✅ server/migrations/000005_add_username_admin_verify.up.sql — users 表新增 username / admin_phone_verified_at / admin_email_verified_at

Bootstrap 接入：
✅ server/internal/bootstrap/app.go — 注入 DB / Redis / auth 路由（含 iamService 传参，用于管理员双重认证权限校验）
```

---

## 基础设施代码（先做，其他模块依赖）

### server/pkg/db/db.go

```go
package db

import (
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// New 初始化 GORM 连接池。调用方应在 app 启动时调用一次，持有单例。
func New(host, port, user, password, dbname string) (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		user, password, host, port, dbname)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	sqlDB, _ := db.DB()
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)
	return db, nil
}
```

### server/pkg/cache/redis.go

```go
package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// New 初始化 Redis 客户端。
func New(addr, password string, dbIndex int) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       dbIndex,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("连接 Redis 失败: %w", err)
	}
	return client, nil
}
```

### server/pkg/crypto/password.go

```go
package crypto

import "golang.org/x/crypto/bcrypt"

// HashPassword 使用 bcrypt 对密码进行 hash。cost 固定 12。
func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
	return string(hash), err
}

// CheckPassword 校验密码是否匹配 hash。
func CheckPassword(plain, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
```

### server/pkg/crypto/hmac.go

```go
package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HMAC256 对 data 使用 key 计算 HMAC-SHA256，返回十六进制字符串。
// 用于：身份证号 hash、Refresh Token hash。
func HMAC256(data, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}
```

### server/pkg/jwt/jwt.go

```go
package jwt

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID uint64 `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// Generate 生成 Access Token。secret 来自环境变量 JWT_SECRET。
func Generate(userID uint64, email, secret string, expireSeconds int64) (string, error) {
	claims := Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expireSeconds) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// Parse 校验并解析 Access Token，返回 Claims。
func Parse(tokenStr, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("签名算法不匹配")
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("token 无效或已过期")
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, errors.New("claims 解析失败")
	}
	return claims, nil
}
```

### config.go 需补充的字段

```go
// 在 Config 结构体中新增：
type Config struct {
    AppEnv  string
    AppName string
    APIHost string
    APIPort string

    // 数据库
    MySQLHost     string
    MySQLPort     string
    MySQLDatabase string
    MySQLUser     string
    MySQLPassword string

    // Redis
    RedisAddr     string
    RedisPassword string
    RedisDB       int

    // JWT
    JWTSecret          string
    JWTExpireSeconds   int64

    // Refresh Token
    RefreshTokenSecret      string
    RefreshTokenExpireDays  int

    // 身份证号 HMAC
    IDCardHMACSecret string

    // 管理员双重认证有效期（小时），默认 24
    // env: ADMIN_VERIFY_EXPIRE_HOURS
    AdminVerifyExpireHours int
}
```

---

## Auth 模块代码模板

### model/user.go

```go
package model

import "time"

type User struct {
    ID                   uint64     `gorm:"primaryKey;autoIncrement"`
    Username             *string    `gorm:"uniqueIndex;size:64"`        // 2-32位字母/数字/下划线，全局唯一
    Email                *string    `gorm:"uniqueIndex;size:191"`
    EmailVerified        bool       `gorm:"default:false"`
    Phone                *string    `gorm:"uniqueIndex;size:32"`
    PhoneVerified        bool       `gorm:"default:false"`
    PasswordHash         string     `gorm:"size:255;not null"`
    RealNameStatus       string     `gorm:"size:32;default:unverified"` // unverified/pending/verified/rejected
    Status               string     `gorm:"size:32;default:active"`     // active/disabled
    WalletID             *uint64
    AdminPhoneVerifiedAt *time.Time // 管理员手机认证时间，NULL 或超过 AdminVerifyExpireHours 视为未认证
    AdminEmailVerifiedAt *time.Time // 管理员邮箱认证时间，NULL 或超过 AdminVerifyExpireHours 视为未认证
    CreatedAt            time.Time
    UpdatedAt            time.Time
}
```

### model/session.go

```go
package model

import "time"

type UserSession struct {
    ID               uint64     `gorm:"primaryKey;autoIncrement"`
    UserID           uint64     `gorm:"not null;index"`
    RefreshTokenHash string     `gorm:"size:128;not null;uniqueIndex"`
    UserAgent        string     `gorm:"size:512"`
    IP               string     `gorm:"size:64"`
    ExpiresAt        time.Time  `gorm:"not null;index"`
    RevokedAt        *time.Time // NULL 表示会话有效
    CreatedAt        time.Time
}
```

### service/auth_service.go — 注册方法

```go
func (s *AuthService) RegisterEmail(ctx context.Context, req dto.RegisterEmailReq) (*dto.TokenPair, error) {
    // 1. 校验验证码
    if err := s.verifySvc.Check(ctx, "email", req.Email, "register", req.Code); err != nil {
        return nil, ErrInvalidCode
    }
    // 2. 检查邮箱唯一性
    if exists, _ := s.userRepo.ExistsByEmail(ctx, req.Email); exists {
        return nil, ErrEmailAlreadyExists
    }
    // 3. 密码 hash
    hash, err := crypto.HashPassword(req.Password)
    if err != nil {
        return nil, err
    }
    // 4. 写用户
    user := &model.User{Email: &req.Email, PasswordHash: hash}
    if err := s.userRepo.Create(ctx, user); err != nil {
        return nil, err
    }
    // 5. 异步初始化钱包（发 MQ 事件，不直接调用 billing 模块）
    s.mq.Publish("user.registered", map[string]interface{}{"user_id": user.ID})
    // 6. 生成 token 对
    return s.generateTokenPair(ctx, user)
}
```

### service/auth_service.go — 生成 Token 对

```go
func (s *AuthService) generateTokenPair(ctx context.Context, user *model.User) (*dto.TokenPair, error) {
    // 生成 Access Token
    accessToken, err := jwt.Generate(user.ID, ptrStr(user.Email), s.cfg.JWTSecret, s.cfg.JWTExpireSeconds)
    if err != nil {
        return nil, err
    }
    // 生成 Refresh Token（随机串）
    rawRefresh := generateRandom32()
    // 数据库只存 HMAC hash
    refreshHash := crypto.HMAC256(rawRefresh, s.cfg.RefreshTokenSecret)
    session := &model.UserSession{
        UserID:           user.ID,
        RefreshTokenHash: refreshHash,
        ExpiresAt:        time.Now().AddDate(0, 0, s.cfg.RefreshTokenExpireDays),
    }
    if err := s.sessionRepo.Create(ctx, session); err != nil {
        return nil, err
    }
    return &dto.TokenPair{
        AccessToken:  accessToken,
        RefreshToken: rawRefresh, // 明文只返回给客户端，不存库
        ExpiresIn:    s.cfg.JWTExpireSeconds,
    }, nil
}
```

### service/auth_service.go — 刷新令牌

```go
func (s *AuthService) Refresh(ctx context.Context, rawRefreshToken string) (*dto.TokenPair, error) {
    hash := crypto.HMAC256(rawRefreshToken, s.cfg.RefreshTokenSecret)
    session, err := s.sessionRepo.FindByHash(ctx, hash)
    if err != nil || session == nil {
        return nil, ErrUnauthorized
    }
    // 校验：未被吊销、未过期
    if session.RevokedAt != nil || session.ExpiresAt.Before(time.Now()) {
        return nil, ErrUnauthorized
    }
    // 吊销旧会话（Refresh Token 轮换）
    _ = s.sessionRepo.Revoke(ctx, session.ID)
    // 查用户并生成新 token 对
    user, _ := s.userRepo.FindByID(ctx, session.UserID)
    return s.generateTokenPair(ctx, user)
}
```

### middleware/auth.go 模板

```go
package middleware

import (
    "net/http"
    "strings"
    "molin/server/pkg/jwt"
    "molin/server/pkg/response"
)

// RequireAuth 解析 Bearer Token，注入 user_id 到 context。
func RequireAuth(secret string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        auth := r.Header.Get("Authorization")
        if !strings.HasPrefix(auth, "Bearer ") {
            response.Error(w, http.StatusUnauthorized, 40001, "未登录")
            return
        }
        claims, err := jwt.Parse(strings.TrimPrefix(auth, "Bearer "), secret)
        if err != nil {
            response.Error(w, http.StatusUnauthorized, 40001, "token 无效或已过期")
            return
        }
        ctx := context.WithValue(r.Context(), ctxKeyUserID, claims.UserID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

---

## Migration 文件

### server/migrations/000001_create_auth_tables.up.sql

```sql
CREATE TABLE IF NOT EXISTS users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email VARCHAR(191) NULL,
  email_verified TINYINT(1) NOT NULL DEFAULT 0,
  phone VARCHAR(32) NULL,
  phone_verified TINYINT(1) NOT NULL DEFAULT 0,
  password_hash VARCHAR(255) NOT NULL,
  real_name_status VARCHAR(32) NOT NULL DEFAULT 'unverified',
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  wallet_id BIGINT UNSIGNED NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_users_email (email),
  UNIQUE KEY uk_users_phone (phone)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_sessions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  refresh_token_hash VARCHAR(128) NOT NULL,
  user_agent VARCHAR(512) NULL,
  ip VARCHAR(64) NULL,
  expires_at DATETIME NOT NULL,
  revoked_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_sessions_token_hash (refresh_token_hash),
  KEY idx_sessions_user_id (user_id),
  KEY idx_sessions_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS verification_codes (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  target_type VARCHAR(32) NOT NULL,
  target_value VARCHAR(191) NOT NULL,
  code VARCHAR(16) NOT NULL,
  scene VARCHAR(32) NOT NULL,
  expires_at DATETIME NOT NULL,
  used_at DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_verification_target (target_type, target_value, scene),
  KEY idx_verification_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_login_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NULL,
  login_type VARCHAR(32) NOT NULL,
  login_account VARCHAR(191) NOT NULL,
  ip VARCHAR(64) NULL,
  user_agent VARCHAR(512) NULL,
  status VARCHAR(32) NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_login_logs_user_id (user_id),
  KEY idx_login_logs_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## go.mod 需要的依赖

```bash
go get gorm.io/gorm
go get gorm.io/driver/mysql
go get github.com/redis/go-redis/v9
go get golang.org/x/crypto
go get github.com/golang-jwt/jwt/v5
go get github.com/shopspring/decimal
go get github.com/google/uuid
```

---

## 接口清单

```text
-- 无需鉴权 --
POST /api/auth/verification-codes/email    -- 发送邮箱验证码（scene: register/reset_password/bind_email/admin_verify）
POST /api/auth/verification-codes/phone    -- 发送短信验证码（scene: register/reset_password/bind_phone/admin_verify）
POST /api/auth/register/email             -- 邮箱注册（兼容旧接口，可选 username）
POST /api/auth/register/phone             -- 手机号注册（兼容旧接口，可选 username）
POST /api/auth/register                   -- 统一注册（手机+邮箱双OTP，可选 username）★新增
POST /api/auth/login/email                -- 邮箱登录
POST /api/auth/login/phone                -- 手机号登录
POST /api/auth/refresh                    -- 刷新 Access Token
POST /api/auth/password/reset             -- OTP 密码重置（支持手机或邮箱，重置后吊销全部会话）★新增

-- 需要 Bearer Token --
POST /api/auth/logout                     -- 退出（吊销 Refresh Token）
GET  /api/me                              -- 当前登录用户信息（含 username/脱敏手机邮箱/admin_*_verified 等新字段）
PATCH /api/me/password                    -- 修改密码
PATCH /api/me/username                    -- 修改用户名（2-32位字母/数字/下划线）★新增
PATCH /api/me/phone                       -- 修改手机号（需先发 scene=bind_phone 验证码）★新增
PATCH /api/me/email                       -- 修改邮箱（需先发 scene=bind_email 验证码）★新增

-- 需要 Bearer Token + user:manage 权限（仅限管理员） --
POST /api/admin/auth/verify-phone         -- 管理员手机双重认证（scene=admin_verify）★新增
POST /api/admin/auth/verify-email         -- 管理员邮箱双重认证（需手机先通过，scene=admin_verify）★新增
```

## GET /api/me 返回字段（完整）

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 用户 ID |
| username | string\|null | 用户名（注册时未填则为 null） |
| email | string\|null | 邮箱（脱敏：@前保留2位，其余 `***`，如 `ab***@example.com`） |
| phone | string\|null | 手机号（脱敏：前3后4，中间 `****`，如 `138****5678`） |
| email_verified | boolean | 邮箱是否已验证 |
| phone_verified | boolean | 手机号是否已验证 |
| real_name_status | string | 实名状态（unverified / pending / verified / rejected） |
| status | string | 账号状态（active / disabled） |
| admin_phone_verified | boolean | 管理员手机认证是否在有效期内（超过 ADMIN_VERIFY_EXPIRE_HOURS 自动 false） |
| admin_email_verified | boolean | 管理员邮箱认证是否在有效期内（超过 ADMIN_VERIFY_EXPIRE_HOURS 自动 false） |
| created_at | string | 注册时间（ISO 8601） |
| last_login_at | string\|null | 最后登录时间（ISO 8601） |

## 错误码

| 错误码 | 含义 |
|---|---|
| 40001 | 未登录或 token 无效 |
| 40002 | token 已过期 |
| 40003 | 权限不足（普通用户访问管理员接口返回此码） |
| 40900 | 邮箱/手机号/用户名已被注册 |
| 40000 | 验证码错误或已过期 |
| 42900 | 请求频率超限 |
