package service

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"molin/server/pkg/crypto"
	pkgjwt "molin/server/pkg/jwt"
)

var ErrVideoJWTInvalid = errors.New("登录凭据无效或已失效")

// 仅内存保留JWT时效与摘要复验能力，不持有原始Token，也不允许普通JSON序列化。
type videoReadCredential struct {
	userID     uint64
	expiresAt  time.Time
	revalidate func(context.Context) error
}

func revalidateVideoReadCredential(ctx context.Context, caller VideoCaller) error {
	if caller.APIKeyID != 0 {
		return nil
	}
	if caller.credential == nil || caller.credential.revalidate == nil || caller.credential.userID != caller.UserID {
		return ErrVideoJWTInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	bounded, cancelExpiry := context.WithDeadline(bounded, caller.credential.expiresAt)
	defer cancelExpiry()
	return caller.credential.revalidate(bounded)
}

// VideoTokenRevocations只接收Token摘要，不持有签名密钥或明文Token。
type VideoTokenRevocations interface {
	IsRevoked(context.Context, string) (bool, error)
}

// VideoJWTAuthenticator复用既有JWT签名合同与users事实；吊销依赖失败时不沿用旧fail-open行为。
type VideoJWTAuthenticator struct {
	db          *gorm.DB
	secret      string
	revocations VideoTokenRevocations
}

func NewVideoJWTAuthenticator(db *gorm.DB, secret string, revocations VideoTokenRevocations) (*VideoJWTAuthenticator, error) {
	if db == nil || len(secret) < 32 || revocations == nil {
		return nil, ErrVideoAccessUnavailable
	}
	return &VideoJWTAuthenticator{db: db, secret: secret, revocations: revocations}, nil
}
func (a *VideoJWTAuthenticator) Authenticate(ctx context.Context, raw string) (VideoCaller, error) {
	if a == nil || a.db == nil || a.revocations == nil {
		return VideoCaller{}, ErrVideoAccessUnavailable
	}
	claims, err := pkgjwt.Parse(raw, a.secret)
	if err != nil || claims.UserID == 0 || claims.ExpiresAt == nil || claims.IssuedAt == nil || claims.IssuedAt.After(time.Now()) {
		return VideoCaller{}, ErrVideoJWTInvalid
	}
	// 初次认证也受内部期限限制，不能在流式复验有界的同时留下无界吊销依赖。
	until := time.Now().Add(30 * time.Second)
	if claims.ExpiresAt.Time.Before(until) {
		until = claims.ExpiresAt.Time
	}
	bounded, cancel := context.WithDeadline(ctx, until)
	defer cancel()
	revoked, err := a.revocations.IsRevoked(bounded, crypto.SHA256Hex(raw))
	if err != nil {
		return VideoCaller{}, ErrVideoAccessUnavailable
	}
	if revoked {
		return VideoCaller{}, ErrVideoJWTInvalid
	}
	var user struct{ Status string }
	if err := a.db.WithContext(bounded).Table("users").Select("status").Where("id=?", claims.UserID).Take(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return VideoCaller{}, ErrVideoJWTInvalid
		}
		return VideoCaller{}, ErrVideoAccessUnavailable
	}
	if user.Status != "active" || bounded.Err() != nil || !claims.ExpiresAt.After(time.Now()) {
		return VideoCaller{}, ErrVideoJWTInvalid
	}
	digest := crypto.SHA256Hex(raw)
	expires := claims.ExpiresAt.Time
	// 有效Token的证明只属于其认证主体，不能配上另一个调用参数UserID借用权限。
	credential := &videoReadCredential{userID: claims.UserID, expiresAt: expires}
	credential.revalidate = func(ctx context.Context) error {
		if ctx.Err() != nil || !expires.After(time.Now()) {
			return ErrVideoJWTInvalid
		}
		revoked, err := a.revocations.IsRevoked(ctx, digest)
		if err != nil {
			return ErrVideoAccessUnavailable
		}
		if revoked || ctx.Err() != nil || !expires.After(time.Now()) {
			return ErrVideoJWTInvalid
		}
		return nil
	}
	return VideoCaller{UserID: claims.UserID, credential: credential}, nil
}

// RedisVideoTokenRevocations读取既有auth退出登录的同名吊销键，不创建另一套会话真相源。
// G6隔离测试显式替换存储边界，不在本阶段连接真实Redis。
type RedisVideoTokenRevocations struct{ Client *redis.Client }

func (s RedisVideoTokenRevocations) IsRevoked(ctx context.Context, digest string) (bool, error) {
	if s.Client == nil || !lowerHex64.MatchString(digest) {
		return false, ErrVideoAccessUnavailable
	}
	value, err := s.Client.Get(ctx, "revoked:token:"+digest).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, ErrVideoAccessUnavailable
	}
	if value != "1" {
		return false, ErrVideoAccessUnavailable
	}
	return true, nil
}
