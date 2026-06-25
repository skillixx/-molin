package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrTicketNotFound 票据不存在或已过期（D2 反代校验时用）。
var ErrTicketNotFound = errors.New("presenton: 票据不存在或已过期")

const ticketKeyPrefix = "presenton:ticket:"

// RedisTicketStore 用 Redis 存短期 SSO 票据。
type RedisTicketStore struct {
	rdb *redis.Client
}

// NewRedisTicketStore 构造 Redis 票据存储。
func NewRedisTicketStore(rdb *redis.Client) *RedisTicketStore {
	return &RedisTicketStore{rdb: rdb}
}

func ticketKey(ticket string) string {
	return ticketKeyPrefix + ticket
}

// Save 写入票据 → payload，带 TTL。
func (s *RedisTicketStore) Save(ctx context.Context, ticket string, p TicketPayload, ttl time.Duration) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, ticketKey(ticket), data, ttl).Err()
}

// Load 按票据取回 payload（供 D2 反代用）；不存在/过期返回 ErrTicketNotFound。
func (s *RedisTicketStore) Load(ctx context.Context, ticket string) (*TicketPayload, error) {
	data, err := s.rdb.Get(ctx, ticketKey(ticket)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrTicketNotFound
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

// Consume 一次性取回并删除票据（GETDEL，防重放）；不存在/过期返回 ErrTicketNotFound。
func (s *RedisTicketStore) Consume(ctx context.Context, ticket string) (*TicketPayload, error) {
	data, err := s.rdb.GetDel(ctx, ticketKey(ticket)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrTicketNotFound
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
