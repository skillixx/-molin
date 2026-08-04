package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestRedisSMSAdminLimiterIntegration 只在显式 CI 门禁下连接隔离 Redis，验证真实 Lua 原子计数与 TTL。
// 测试仅删除本用例生成的两个唯一键，不清空数据库，也不读取其他业务键。
func TestRedisSMSAdminLimiterIntegration(t *testing.T) {
	if os.Getenv("SMS_REDIS_INTEGRATION_TEST") != "1" {
		t.Skip("未开启 SMS_REDIS_INTEGRATION_TEST，跳过真实 Redis 限流验证")
	}
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if addr == "" {
		t.Fatal("真实 Redis 限流测试缺少 REDIS_ADDR")
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("关闭真实 Redis 测试连接失败: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("真实 Redis 不可用: %v", err)
	}

	seed := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(seed)))
	adminID := uint64(time.Now().UnixNano())
	adminKey := fmt.Sprintf("sms:test:admin:%d", adminID)
	phoneKey := "sms:test:phone:" + digest
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		if err := client.Del(cleanupCtx, adminKey, phoneKey).Err(); err != nil {
			t.Errorf("清理真实 Redis 测试键失败: %v", err)
		}
	})

	limiter := &redisSMSAdminTestLimiter{client: client}
	for index := 1; index <= 10; index++ {
		allowed, retry, err := limiter.Allow(ctx, adminID, digest)
		if err != nil || !allowed || retry != 0 {
			t.Fatalf("第 %d 次请求应被允许: allowed=%t retry=%d err=%v", index, allowed, retry, err)
		}
	}
	allowed, retry, err := limiter.Allow(ctx, adminID, digest)
	if err != nil || allowed || retry < 1 || retry > 60 {
		t.Fatalf("第 11 次请求必须被真实 Redis 拒绝: allowed=%t retry=%d err=%v", allowed, retry, err)
	}

	values, err := client.MGet(ctx, adminKey, phoneKey).Result()
	if err != nil || len(values) != 2 || values[0] != "10" || values[1] != "10" {
		t.Fatalf("真实 Redis 双维计数必须同时停在 10: values=%#v err=%v", values, err)
	}
	for _, key := range []string{adminKey, phoneKey} {
		ttl, ttlErr := client.TTL(ctx, key).Result()
		if ttlErr != nil || ttl <= 0 || ttl > 60*time.Second {
			t.Fatalf("真实 Redis 限流键 TTL 错误: key=%s ttl=%s err=%v", key, ttl, ttlErr)
		}
	}
}
