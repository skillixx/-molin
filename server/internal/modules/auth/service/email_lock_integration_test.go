package service

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"molin/server/pkg/crypto"
)

// cleanupEmailRedisIntegrationKey 只删除本轮生成的精确 key，并使用独立短上下文确认清理完成。
// 清理错误只返回固定分类，不拼接连接地址、密码、完整 key 或 Redis 原始错误。
func cleanupEmailRedisIntegrationKey(client *redis.Client, key string) error {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()
	if err := client.Del(cleanupCtx, key).Err(); err != nil {
		return errors.New("cleanup_del_failed")
	}
	exists, err := client.Exists(cleanupCtx, key).Result()
	if err != nil {
		return errors.New("cleanup_exists_failed")
	}
	if exists != 0 {
		return errors.New("cleanup_exists_nonzero")
	}
	return nil
}

// TestEmailRedisLeaseIntegration 只在显式测试开关下连接受控 Redis。
// 地址、密码和库号必须由测试进程环境注入，测试代码不读取或输出任何配置文件与秘密。
func TestEmailRedisLeaseIntegration(t *testing.T) {
	if os.Getenv("RUN_EMAIL_REDIS_INTEGRATION") != "1" {
		t.Skip("未启用受控 Redis 集成测试")
	}
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if addr == "" {
		t.Fatal("Redis 集成测试缺少 REDIS_ADDR")
	}
	dbIndex, err := strconv.Atoi(strings.TrimSpace(os.Getenv("REDIS_DB")))
	if err != nil {
		t.Fatal("Redis 集成测试的 REDIS_DB 非法")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD"), DB: dbIndex})
	// 使用 Cleanup 保持后进先出：本轮 key 清理先执行，最后才关闭 Redis 客户端。
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal("受控 Redis 不可达")
	}

	idempotencySecret := strings.Repeat("i", 32)
	svc := NewEmailService(nil, nil, &MockEmailAdapter{}, nil, client, strings.Repeat("a", 32), idempotencySecret, "test", "mock")
	nonce, err := randomNonce()
	if err != nil {
		t.Fatal("测试 scope 随机数生成失败")
	}
	scope := "integration:email-lock:" + nonce
	key := "lock:email:dispatch:" + crypto.HMAC256(scope, idempotencySecret)
	cleanupVerified := false
	// t.Cleanup 在 Fatal、FailNow 和正常返回时都会执行，保证失败路径也精确清理本轮 key。
	t.Cleanup(func() {
		if cleanupVerified {
			return
		}
		if err := cleanupEmailRedisIntegrationKey(client, key); err != nil {
			t.Errorf("[FAIL] mode=redis_integration classification=cleanup_failed cleanup_exists_zero=false")
		}
	})

	lease, locked, err := svc.acquireDistributedLock(ctx, scope, 1500*time.Millisecond)
	if err != nil || !locked || lease == nil {
		t.Fatal("首次获取 Redis lease 失败")
	}
	defer lease.Release()
	if _, secondLocked, secondErr := svc.acquireDistributedLock(ctx, scope, 1500*time.Millisecond); secondErr != nil || secondLocked {
		t.Fatal("相同 scope 的第二个竞争者不得取得 lease")
	}
	// 等待超过初始 TTL；若 Lua 续租生效，原持有者仍应拥有 lease。
	time.Sleep(2 * time.Second)
	if !lease.Owned(ctx) {
		t.Fatal("Redis lease 未按 TTL 三分之一以内续租")
	}
	if err := client.Set(ctx, key, "foreign-owner", 5*time.Second).Err(); err != nil {
		t.Fatal("模拟所有权转移失败")
	}
	if lease.Owned(ctx) {
		t.Fatal("token 不匹配时必须立即判定丢失所有权")
	}
	lease.Release()
	if got, err := client.Get(ctx, key).Result(); err != nil || got != "foreign-owner" {
		t.Fatal("非所有者释放不得删除其他执行者的锁")
	}
	// 正常路径在测试结束前完成精确清理和 EXISTS=0 断言，避免只依赖延迟清理而没有证据。
	if err := cleanupEmailRedisIntegrationKey(client, key); err != nil {
		t.Fatal("[FAIL] mode=redis_integration classification=cleanup_failed cleanup_exists_zero=false")
	}
	cleanupVerified = true
	t.Log("[PASS] mode=redis_integration classification=lease_verified cleanup_exists_zero=true")
}
