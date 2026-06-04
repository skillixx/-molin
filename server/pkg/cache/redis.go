package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// New 初始化 Redis 客户端，启动时 Ping 确认连通性。
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
