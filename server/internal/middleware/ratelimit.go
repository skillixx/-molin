package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/pkg/response"
)

// RateLimitByUser 基于 Redis 对“已登录用户 + 指定动作”做计数限流：
// 每个 (userID, action) 组合在 window 时间窗口内最多允许 limit 次请求，超出返回 429 42900。
//
// D-22：用于 /api/me/phone、/api/me/email 等修改绑定信息的接口，防止账号枚举/暴力试探。
// Redis 故障时按"最佳努力"降级：不阻断请求，仅记录日志（与 D-12 约定一致，限流非安全关键吊销操作）。
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

// incrAndCheck 对 key 做 INCR，首次写入时设置 TTL，返回是否未超过 limit。
func incrAndCheck(ctx context.Context, redisClient *redis.Client, key string, limit int, window time.Duration) (bool, error) {
	count, err := redisClient.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		if err := redisClient.Expire(ctx, key, window).Err(); err != nil {
			return false, err
		}
	}
	return count <= int64(limit), nil
}
