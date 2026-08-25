package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/internal/modules/token_gateway/model"
)

type fixedResourcePolicyReader struct {
	policies map[string]model.AIResourcePolicy
}

func (r fixedResourcePolicyReader) LoadResourcePolicies(context.Context, map[string]string) (map[string]model.AIResourcePolicy, error) {
	result := make(map[string]model.AIResourcePolicy, len(r.policies))
	for key, value := range r.policies {
		result[key] = value
	}
	return result, nil
}

func TestG4RedisResourceIntegration(t *testing.T) {
	addr := os.Getenv("G4_REDIS_ADDR")
	if addr == "" || os.Getenv("G4_ISOLATED_TEST") != "YES" {
		t.Skip("仅在 G4 隔离 Redis 脚本显式授权时运行")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("隔离 Redis 不可用: %v", err)
	}
	flush := func(t *testing.T) {
		t.Helper()
		if err := client.FlushDB(ctx).Err(); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("八节点一百并发最多二十个准入", func(t *testing.T) {
		flush(t)
		metrics := NewAIGatewayMetrics(nil)
		defaults := ResourceDefaults{
			User: ResourceLimits{Concurrency: 20, RPM: 1000, TPM: 1000000}, Project: ResourceLimits{Concurrency: 1000, RPM: 1000, TPM: 1000000},
			APIKey: ResourceLimits{Concurrency: 1000, RPM: 1000, TPM: 1000000}, Model: ResourceLimits{Concurrency: 1000, RPM: 1000, TPM: 1000000},
		}
		limiters := make([]*ResourceLimiter, 8)
		for index := range limiters {
			limiters[index] = NewResourceLimiter(client, fixedResourcePolicyReader{}, defaults).WithMetrics(metrics)
		}
		var admitted atomic.Int64
		var rejected atomic.Int64
		var unexpected atomic.Int64
		tickets := make(chan *ResourceTicket, 100)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for index := 0; index < 100; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				ticket, err := limiters[index%len(limiters)].Acquire(ctx, fmt.Sprintf("req-concurrency-%03d", index), 1, 2, 3, "molin/test", 10)
				switch {
				case err == nil:
					admitted.Add(1)
					tickets <- ticket
				case errors.Is(err, ErrConcurrencyExceeded):
					rejected.Add(1)
				default:
					unexpected.Add(1)
				}
			}(index)
		}
		close(start)
		wg.Wait()
		close(tickets)
		if admitted.Load() != 20 || rejected.Load() != 80 || unexpected.Load() != 0 {
			t.Fatalf("并发准入不符合限制: admitted=%d rejected=%d unexpected=%d", admitted.Load(), rejected.Load(), unexpected.Load())
		}
		for ticket := range tickets {
			if err := limiters[0].Release(ctx, ticket); err != nil {
				t.Fatal(err)
			}
		}
		metricText, metricErr := metrics.AIGatewayPrometheus(ctx)
		if metricErr != nil {
			t.Fatal(metricErr)
		}
		if !strings.Contains(metricText, `molin_ai_gateway_concurrency_leases{scope="user"} 0`) ||
			!strings.Contains(metricText, `molin_ai_gateway_concurrency_rejections_total{scope="user"} 80`) {
			t.Fatalf("一百并发后的租约和拒绝指标错误:\n%s", metricText)
		}
	})

	t.Run("租约过期自动回收", func(t *testing.T) {
		flush(t)
		defaults := ResourceDefaults{
			User: ResourceLimits{Concurrency: 1, RPM: 100, TPM: 100000}, Project: ResourceLimits{Concurrency: 100, RPM: 100, TPM: 100000},
			APIKey: ResourceLimits{Concurrency: 100, RPM: 100, TPM: 100000}, Model: ResourceLimits{Concurrency: 100, RPM: 100, TPM: 100000},
		}
		firstMetrics := NewAIGatewayMetrics(nil)
		secondMetrics := NewAIGatewayMetrics(nil)
		firstLimiter := NewResourceLimiter(client, fixedResourcePolicyReader{}, defaults).WithMetrics(firstMetrics)
		secondLimiter := NewResourceLimiter(client, fixedResourcePolicyReader{}, defaults).WithMetrics(secondMetrics)
		firstLimiter.leaseTTL = 120 * time.Millisecond
		secondLimiter.leaseTTL = 120 * time.Millisecond
		if _, err := firstLimiter.Acquire(ctx, "req-lease-old", 11, 12, 13, "molin/lease", 10); err != nil {
			t.Fatal(err)
		}
		if _, err := secondLimiter.Acquire(ctx, "req-lease-blocked", 11, 12, 13, "molin/lease", 10); !errors.Is(err, ErrConcurrencyExceeded) {
			t.Fatalf("活动租约应阻止第二个请求: %v", err)
		}
		time.Sleep(180 * time.Millisecond)
		recovered, err := secondLimiter.Acquire(ctx, "req-lease-recovered", 11, 12, 13, "molin/lease", 10)
		if err != nil {
			t.Fatalf("过期租约应自动回收: %v", err)
		}
		firstMetricText, firstMetricErr := firstMetrics.AIGatewayPrometheus(ctx)
		secondMetricText, secondMetricErr := secondMetrics.AIGatewayPrometheus(ctx)
		if firstMetricErr != nil || secondMetricErr != nil {
			t.Fatalf("跨实例租约 Gauge 读取失败: first=%v second=%v", firstMetricErr, secondMetricErr)
		}
		if !strings.Contains(secondMetricText, "molin_ai_gateway_ghost_leases_total 4") ||
			!strings.Contains(firstMetricText, `molin_ai_gateway_concurrency_leases{scope="user"} 1`) ||
			!strings.Contains(secondMetricText, `molin_ai_gateway_concurrency_leases{scope="user"} 1`) {
			t.Fatalf("清理方与原持有方必须读取同一 Redis 权威 Gauge:\nfirst:\n%s\nsecond:\n%s", firstMetricText, secondMetricText)
		}
		if err := secondLimiter.Release(ctx, recovered); err != nil {
			t.Fatal(err)
		}
		firstMetricText, firstMetricErr = firstMetrics.AIGatewayPrometheus(ctx)
		secondMetricText, secondMetricErr = secondMetrics.AIGatewayPrometheus(ctx)
		if firstMetricErr != nil || secondMetricErr != nil ||
			!strings.Contains(firstMetricText, `molin_ai_gateway_concurrency_leases{scope="user"} 0`) ||
			!strings.Contains(secondMetricText, `molin_ai_gateway_concurrency_leases{scope="user"} 0`) {
			t.Fatalf("任一实例释放后全部实例 Gauge 必须归零: first_err=%v second_err=%v\nfirst:\n%s\nsecond:\n%s", firstMetricErr, secondMetricErr, firstMetricText, secondMetricText)
		}
	})

	t.Run("RPM和TPM跨实例共享", func(t *testing.T) {
		flush(t)
		defaults := ResourceDefaults{
			User: ResourceLimits{Concurrency: 100, RPM: 2, TPM: 10}, Project: ResourceLimits{Concurrency: 100, RPM: 100, TPM: 1000},
			APIKey: ResourceLimits{Concurrency: 100, RPM: 100, TPM: 1000}, Model: ResourceLimits{Concurrency: 100, RPM: 100, TPM: 1000},
		}
		first := NewResourceLimiter(client, fixedResourcePolicyReader{}, defaults)
		second := NewResourceLimiter(client, fixedResourcePolicyReader{}, defaults)
		ticket, err := first.Acquire(ctx, "req-rate-1", 21, 22, 23, "molin/rate", 6)
		if err != nil {
			t.Fatal(err)
		}
		_ = first.Release(ctx, ticket)
		if _, err := second.Acquire(ctx, "req-rate-2", 21, 22, 23, "molin/rate", 6); !errors.Is(err, ErrRateLimitExceeded) {
			t.Fatalf("第二节点必须看到共享 TPM: %v", err)
		}
	})

	t.Run("图片硬并发上限不能被高默认值或高数据库策略放宽", func(t *testing.T) {
		flush(t)
		defaults := ResourceDefaults{
			User: ResourceLimits{Concurrency: 100, RPM: 1000, TPM: 1000}, Project: ResourceLimits{Concurrency: 100, RPM: 1000, TPM: 1000},
			APIKey: ResourceLimits{Concurrency: 50, RPM: 1000, TPM: 1000}, Model: ResourceLimits{Concurrency: 500, RPM: 1000, TPM: 1000},
		}
		policies := map[string]model.AIResourcePolicy{
			"user": {ConcurrencyLimit: 999, RPMLimit: 1000, TPMLimit: 1000}, "project": {ConcurrencyLimit: 999, RPMLimit: 1000, TPMLimit: 1000},
			"api_key": {ConcurrencyLimit: 999, RPMLimit: 1000, TPMLimit: 1000}, "model": {ConcurrencyLimit: 999, RPMLimit: 1000, TPMLimit: 1000},
		}
		limiter := NewResourceLimiter(client, fixedResourcePolicyReader{policies: policies}, defaults).WithConcurrencyCeilings(ResourceDefaults{
			User: ResourceLimits{Concurrency: 1}, Project: ResourceLimits{Concurrency: 2},
			APIKey: ResourceLimits{Concurrency: 1}, Model: ResourceLimits{Concurrency: 4},
		})
		first, err := limiter.Acquire(ctx, "img-lease-first", 51, 52, 53, "molin/image", 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := limiter.Acquire(ctx, "img-lease-second", 51, 54, 55, "molin/image", 1); !errors.Is(err, ErrConcurrencyExceeded) {
			t.Fatalf("同一图片用户第二个请求必须被硬上限拒绝: %v", err)
		}
		if err := limiter.Release(ctx, first); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Redis不可用或脚本损坏时失败关闭", func(t *testing.T) {
		defaults := ResourceDefaults{
			User: ResourceLimits{Concurrency: 1, RPM: 1, TPM: 1}, Project: ResourceLimits{Concurrency: 1, RPM: 1, TPM: 1},
			APIKey: ResourceLimits{Concurrency: 1, RPM: 1, TPM: 1}, Model: ResourceLimits{Concurrency: 1, RPM: 1, TPM: 1},
		}
		deadClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, ReadTimeout: 50 * time.Millisecond})
		defer deadClient.Close()
		deadLimiter := NewResourceLimiter(deadClient, fixedResourcePolicyReader{}, defaults)
		if _, err := deadLimiter.Acquire(ctx, "req-redis-down", 31, 32, 33, "molin/down", 1); !errors.Is(err, ErrResourceUnavailable) {
			t.Fatalf("Redis 停止时必须 fail-closed: %v", err)
		}

		broken := NewResourceLimiter(client, fixedResourcePolicyReader{}, defaults)
		broken.acquireLua = redis.NewScript("this is not lua")
		if _, err := broken.Acquire(ctx, "req-script-broken", 41, 42, 43, "molin/broken", 1); !errors.Is(err, ErrResourceUnavailable) {
			t.Fatalf("Lua 损坏时必须 fail-closed: %v", err)
		}
	})
}

func TestG4RedisUnavailableIntegration(t *testing.T) {
	addr := os.Getenv("G4_REDIS_DOWN_ADDR")
	if addr == "" || os.Getenv("G4_ISOLATED_TEST") != "YES" {
		t.Skip("仅在 G4 隔离 Redis 停机演练时运行")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond})
	defer client.Close()
	defaults := ResourceDefaults{
		User: ResourceLimits{Concurrency: 1, RPM: 1, TPM: 1}, Project: ResourceLimits{Concurrency: 1, RPM: 1, TPM: 1},
		APIKey: ResourceLimits{Concurrency: 1, RPM: 1, TPM: 1}, Model: ResourceLimits{Concurrency: 1, RPM: 1, TPM: 1},
	}
	limiter := NewResourceLimiter(client, fixedResourcePolicyReader{}, defaults)
	if _, err := limiter.Acquire(context.Background(), "req-real-redis-down", 1, 2, 3, "molin/down", 1); !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("真实 Redis 停机必须 fail-closed: %v", err)
	}
}
