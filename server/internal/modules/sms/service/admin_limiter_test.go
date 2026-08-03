package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

type fakeRedisEvaluator struct {
	keys   []string
	script string
	result any
	err    error
}

func (f *fakeRedisEvaluator) Eval(_ context.Context, script string, keys []string, _ ...interface{}) *redis.Cmd {
	f.script = script
	f.keys = append([]string(nil), keys...)
	return redis.NewCmdResult(f.result, f.err)
}

func TestRedisSMSAdminLimiterUsesAdminAndPhoneHMACDimensions(t *testing.T) {
	phoneDigest := strings.Repeat("a", 64)
	evaluator := &fakeRedisEvaluator{result: []interface{}{int64(1), int64(0)}}
	limiter := &redisSMSAdminTestLimiter{client: evaluator}

	allowed, retry, err := limiter.Allow(context.Background(), 10, phoneDigest)
	if err != nil || !allowed || retry != 0 {
		t.Fatalf("Redis 限流允许结果错误: allowed=%t retry=%d err=%v", allowed, retry, err)
	}
	if len(evaluator.keys) != 2 || evaluator.keys[0] != "sms:test:admin:10" || evaluator.keys[1] != "sms:test:phone:"+phoneDigest {
		t.Fatalf("限流必须同时使用管理员与手机号 HMAC 两个维度: %#v", evaluator.keys)
	}
	if !strings.Contains(evaluator.script, "a>=10") || !strings.Contains(evaluator.script, "p>=10") || !strings.Contains(evaluator.script, "EXPIRE',KEYS[1],60") || !strings.Contains(evaluator.script, "EXPIRE',KEYS[2],60") {
		t.Fatal("限流脚本必须原子检查两个每分钟十次的计数器")
	}
}

func TestRedisSMSAdminLimiterReturnsSharedRetryAfter(t *testing.T) {
	evaluator := &fakeRedisEvaluator{result: []interface{}{int64(0), int64(27)}}
	allowed, retry, err := (&redisSMSAdminTestLimiter{client: evaluator}).Allow(context.Background(), 10, strings.Repeat("b", 64))
	if err != nil || allowed || retry != 27 {
		t.Fatalf("Redis 限流恢复时间错误: allowed=%t retry=%d err=%v", allowed, retry, err)
	}
}

func TestRedisSMSAdminLimiterFailsClosedOnRedisErrors(t *testing.T) {
	cases := []*fakeRedisEvaluator{
		{err: errors.New("redis unavailable")},
		{result: "malformed"},
	}
	for _, evaluator := range cases {
		allowed, _, err := (&redisSMSAdminTestLimiter{client: evaluator}).Allow(context.Background(), 10, strings.Repeat("c", 64))
		if err == nil || allowed {
			t.Fatalf("Redis 异常必须失败关闭: allowed=%t err=%v", allowed, err)
		}
	}
}
