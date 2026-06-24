package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrSessionNotFound 会话不存在或已过期（D2 反代凭 cookie 取会话时用）。
var ErrSessionNotFound = errors.New("presenton: 会话不存在或已过期")

const sessionKeyPrefix = "presenton:session:"

// RedisSessionStore D2 反代会话存储：launch 用一次性票据换出会话，cookie 持会话 id，
// 后续所有反代请求凭 cookie 取回身份与 key 注入 X-Molin-* 头。
//
// 会话承载与票据相同的 TicketPayload（user_id + token_gateway key），但 TTL 更长
// （编辑会话时长），且仅服务端可读，key 绝不下发浏览器。
type RedisSessionStore struct {
	rdb *redis.Client
}

// NewRedisSessionStore 构造会话存储。
func NewRedisSessionStore(rdb *redis.Client) *RedisSessionStore {
	return &RedisSessionStore{rdb: rdb}
}

func sessionKey(sid string) string {
	return sessionKeyPrefix + sid
}

// Save 写入会话 id → payload，带 TTL。
func (s *RedisSessionStore) Save(ctx context.Context, sid string, p TicketPayload, ttl time.Duration) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, sessionKey(sid), data, ttl).Err()
}

// Load 按会话 id 取回 payload；不存在/过期返回 ErrSessionNotFound。
func (s *RedisSessionStore) Load(ctx context.Context, sid string) (*TicketPayload, error) {
	data, err := s.rdb.Get(ctx, sessionKey(sid)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	var p TicketPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
