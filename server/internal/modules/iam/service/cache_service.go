package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	permCacheKeyFmt = "perm:user:%d"
	permCacheTTL    = 5 * time.Minute
)

// CacheService 管理用户权限的 Redis 缓存（key: perm:user:{userID}，TTL: 5min）。
type CacheService struct {
	redis *redis.Client
}

func NewCacheService(redis *redis.Client) *CacheService {
	return &CacheService{redis: redis}
}

// GetUserPerms 从 Redis 读取用户权限码列表，未命中返回 false。
func (s *CacheService) GetUserPerms(ctx context.Context, userID uint64) ([]string, bool) {
	key := fmt.Sprintf(permCacheKeyFmt, userID)
	val, err := s.redis.Get(ctx, key).Result()
	if err != nil {
		return nil, false
	}
	var codes []string
	if err := json.Unmarshal([]byte(val), &codes); err != nil {
		return nil, false
	}
	return codes, true
}

// SetUserPerms 将用户权限码列表写入 Redis，TTL 5 分钟。
func (s *CacheService) SetUserPerms(ctx context.Context, userID uint64, codes []string) {
	key := fmt.Sprintf(permCacheKeyFmt, userID)
	b, _ := json.Marshal(codes)
	_ = s.redis.Set(ctx, key, string(b), permCacheTTL).Err()
}

// InvalidateUserPerms 修改角色/权限时失效用户权限缓存。
func (s *CacheService) InvalidateUserPerms(ctx context.Context, userID uint64) {
	key := fmt.Sprintf(permCacheKeyFmt, userID)
	_ = s.redis.Del(ctx, key).Err()
}
