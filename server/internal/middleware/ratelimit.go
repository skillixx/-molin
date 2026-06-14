package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/pkg/httputil"
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

// RateLimitByIP 基于客户端 IP 做计数限流：
// 同一 IP 在 window 时间窗口内最多允许 limit 次请求，超出返回 429 42900。
// D-51：用于验证码发送和密码重置等公开端点，防止短信轰炸和暴力枚举。
// Redis 故障时降级为放行（最佳努力，与 RateLimitByUser 一致）。
func RateLimitByIP(redisClient *redis.Client, action string, limit int, window time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := httputil.ClientIP(r)
		key := fmt.Sprintf("ratelimit:ip:%s:%s", ip, action)

		allowed, err := incrAndCheck(r.Context(), redisClient, key, limit, window)
		if err != nil {
			// Redis 故障：不阻断主流程，仅记录日志（最佳努力降级）
			log.Printf("RateLimitByIP: Redis 操作失败 key=%s err=%v", key, err)
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
