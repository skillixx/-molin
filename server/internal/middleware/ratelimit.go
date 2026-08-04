package middleware

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/pkg/response"
)

// incrAtomicLua D-48：原子执行 INCR + PEXPIRE。
// 使用 Lua 脚本保证两步操作的原子性：若 INCR 后 count==1（首次写入），
// 则立即设置 TTL，防止 EXPIRE 单独失败导致 key 无 TTL 永久存在引发永久限流（DoS）。
var incrAtomicLua = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return count
`)

// RateLimitByUser 基于 Redis 对"已登录用户 + 指定动作"做计数限流：
// 每个 (userID, action) 组合在 window 时间窗口内最多允许 limit 次请求，超出返回 429 42900。
//
// D-22：用于 /api/me/phone、/api/me/email 等修改绑定信息的接口，防止账号枚举/暴力试探。
// Redis 故障时按最佳努力降级：不阻断请求，仅记录日志（与 D-12 约定一致，限流非安全关键吊销操作）。
func RateLimitByUser(redisClient *redis.Client, action string, limit int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := UserIDFromContext(r.Context())
		key := fmt.Sprintf("ratelimit:user:%d:%s", userID, action)

		allowed, err := incrAndCheck(r.Context(), redisClient, key, limit, window)
		if err != nil {
			// Redis 故障：不阻断主流程，仅记录日志（最佳努力降级）
			log.Printf("RateLimitByUser: Redis 操作失败 key=%s err=%v", key, err)
			next.ServeHTTP(w, r)
			return
		}
		if !allowed {
			response.Error(w, http.StatusTooManyRequests, 42900, "操作过于频繁，请稍后再试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimitByIP 基于直连 RemoteAddr 做计数限流：
// 同一 IP 在 window 时间窗口内最多允许 limit 次请求，超出返回 429 42900。
// 该兼容入口有意忽略所有来源 Header；需要识别可信反向代理时应使用 RateLimitVerificationByIP。
func RateLimitByIP(redisClient *redis.Client, action string, limit int, window time.Duration, next http.Handler) http.Handler {
	return RateLimitVerificationByIP(redisClient, NewPublicSourceIPResolver(nil), action, limit, window, next)
}

// RateLimitEmailByIP 仅用于邮件发送入口，保持邮件阶段二冻结的 42900 错误文案，不改变既有短信等接口契约。

type publicIPRateCounter func(context.Context, string, int, time.Duration) (bool, error)

// RateLimitVerificationByIP 使用可信来源解析器保护公开验证码与密码重置入口。
// 非可信直连永远只认 RemoteAddr；可信代理只接受严格单值 X-Real-IP，禁止读取可伪造的 X-Forwarded-For。
func RateLimitVerificationByIP(redisClient *redis.Client, resolver PublicSourceIPResolver, action string, limit int, window time.Duration, next http.Handler) http.Handler {
	counter := func(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
		return incrAndCheck(ctx, redisClient, key, limit, window)
	}
	return rateLimitVerificationByIP(resolver, counter, action, limit, window, next)
}

func rateLimitVerificationByIP(resolver PublicSourceIPResolver, counter publicIPRateCounter, action string, limit int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if resolver == nil {
			response.Error(w, http.StatusServiceUnavailable, 50300, "验证码服务当前不可用")
			return
		}
		ip, err := resolver.Resolve(r)
		if err != nil {
			if errors.Is(err, ErrPublicSourceIPForbidden) {
				response.Error(w, http.StatusForbidden, 40003, "无权限")
				return
			}
			response.Error(w, http.StatusServiceUnavailable, 50300, "验证码服务当前不可用")
			return
		}
		key := fmt.Sprintf("ratelimit:ip:%s:%s", ip, action)
		allowed, err := counter(r.Context(), key, limit, window)
		if err != nil {
			// Redis 故障时由手机号+场景门禁、验证码错误次数和一次性消费继续提供纵深保护。
			log.Printf("RateLimitVerificationByIP: Redis 操作失败 key=%s err=%v", key, err)
			next.ServeHTTP(w, r)
			return
		}
		if !allowed {
			response.Error(w, http.StatusTooManyRequests, 42900, "请求过于频繁，请稍后再试")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RateLimitEmailByIP(redisClient *redis.Client, resolver PublicSourceIPResolver, action string, limit int, window time.Duration, next http.Handler) http.Handler {
	counter := func(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
		return incrAndCheck(ctx, redisClient, key, limit, window)
	}
	return rateLimitEmailByIP(resolver, counter, action, limit, window, next)
}

func rateLimitEmailByIP(resolver PublicSourceIPResolver, counter publicIPRateCounter, action string, limit int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if resolver == nil {
			response.Error(w, http.StatusServiceUnavailable, 51003, "邮件发送服务未就绪")
			return
		}
		ip, err := resolver.Resolve(r)
		if err != nil {
			if errors.Is(err, ErrPublicSourceIPForbidden) {
				response.Error(w, http.StatusForbidden, 40003, "无权限")
				return
			}
			response.Error(w, http.StatusServiceUnavailable, 51003, "邮件发送服务未就绪")
			return
		}
		key := fmt.Sprintf("ratelimit:ip:%s:%s", ip, action)
		allowed, err := counter(r.Context(), key, limit, window)
		if err != nil {
			// IP 限流是纵深防御；Redis 故障时由服务层账户限流继续关闭失败。
			log.Printf("RateLimitEmailByIP: Redis 操作失败 key=%s err=%v", key, err)
			next.ServeHTTP(w, r)
			return
		}
		if !allowed {
			response.Error(w, http.StatusTooManyRequests, 42900, "请求频率超限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// incrAndCheck D-48：使用 Lua 脚本原子执行 INCR + PEXPIRE，返回是否未超过 limit。
// 避免 INCR 成功而 EXPIRE 失败导致 key 无 TTL 永久存在的竞态条件。
func incrAndCheck(ctx context.Context, redisClient *redis.Client, key string, limit int, window time.Duration) (bool, error) {
	windowMs := window.Milliseconds()
	count, err := incrAtomicLua.Run(ctx, redisClient, []string{key}, windowMs).Int64()
	if err != nil {
		return false, err
	}
	return count <= int64(limit), nil
}
