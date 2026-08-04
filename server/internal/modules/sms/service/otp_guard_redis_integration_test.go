package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestRedisOTPGuardIntegration 只在 CI 显式门禁下连接隔离 Redis，验证真实 Lua 并发和错误次数状态。
// 清理范围严格限制为本用例通过 HMAC 派生的两个键，不扫描或清空 Redis。
func TestRedisOTPGuardIntegration(t *testing.T) {
	if os.Getenv("SMS_REDIS_INTEGRATION_TEST") != "1" {
		t.Skip("未开启 SMS_REDIS_INTEGRATION_TEST，跳过真实 Redis OTP 门禁验证")
	}
	addr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if addr == "" {
		t.Fatal("真实 Redis OTP 门禁测试缺少 REDIS_ADDR")
	}

	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("关闭真实 Redis OTP 测试连接失败: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("真实 Redis 不可用: %v", err)
	}

	phone := fmt.Sprintf("phase4-phone-%d", time.Now().UnixNano())
	guard := NewRedisOTPGuard(client, strings.Repeat("g", 32))
	sendKey := guard.key("send", phone, "register")
	failureKey := guard.key("failure", phone, "register")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		if err := client.Del(cleanupCtx, sendKey, failureKey).Err(); err != nil {
			t.Errorf("清理真实 Redis OTP 测试键失败: %v", err)
		}
	})

	var allowedCount atomic.Int32
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 16)
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			allowed, err := guard.AllowSend(ctx, phone, "register")
			if err == nil && allowed {
				allowedCount.Add(1)
			}
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("真实 Redis 并发发码门禁失败: %v", err)
		}
	}
	if allowedCount.Load() != 1 {
		t.Fatalf("16 路并发发码必须只允许一次，实际 %d", allowedCount.Load())
	}

	for attempt := 1; attempt <= 5; attempt++ {
		allowed, err := guard.AllowCheckAttempt(ctx, phone, "register")
		if err != nil {
			t.Fatalf("第 %d 次错误计数失败: %v", attempt, err)
		}
		if !allowed {
			t.Fatalf("前五次校验尝试都应取得资格: attempt=%d", attempt)
		}
	}
	allowed, err := guard.AllowCheckAttempt(ctx, phone, "register")
	if err != nil || allowed {
		t.Fatalf("第六次校验尝试必须被原子门禁拒绝: allowed=%v err=%v", allowed, err)
	}
	if err := guard.ClearCheckFailures(ctx, phone, "register"); err != nil {
		t.Fatalf("成功消费后的错误次数清理失败: %v", err)
	}
	allowed, err = guard.AllowCheckAttempt(ctx, phone, "register")
	if err != nil || !allowed {
		t.Fatalf("清理后门禁必须恢复: allowed=%v err=%v", allowed, err)
	}

	// 使用独立场景验证并发错误请求最多只有五个能进入数据库校验阶段。
	concurrentFailureKey := guard.key("failure", phone, "login")
	t.Cleanup(func() { _ = client.Del(context.Background(), concurrentFailureKey).Err() })
	var checkAllowedCount atomic.Int32
	var checkWait sync.WaitGroup
	checkErrors := make(chan error, 16)
	for index := 0; index < 16; index++ {
		checkWait.Add(1)
		go func() {
			defer checkWait.Done()
			checkAllowed, checkErr := guard.AllowCheckAttempt(ctx, phone, "login")
			if checkErr == nil && checkAllowed {
				checkAllowedCount.Add(1)
			}
			checkErrors <- checkErr
		}()
	}
	checkWait.Wait()
	close(checkErrors)
	for checkErr := range checkErrors {
		if checkErr != nil {
			t.Fatalf("并发错误次数门禁执行失败: %v", checkErr)
		}
	}
	if checkAllowedCount.Load() != 5 {
		t.Fatalf("16 路并发校验最多只能有五次进入数据库，实际 %d", checkAllowedCount.Load())
	}
}
