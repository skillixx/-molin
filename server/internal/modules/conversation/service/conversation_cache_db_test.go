package service

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"

	convcache "molin/server/internal/modules/conversation/cache"
)

// newTestRedis 连本地 Redis；连不上则跳过（缓存测试非必跑，缓存 fail-open）。
func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := convEnvOr("REDIS_ADDR", "127.0.0.1:16379")
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("REDIS_PASSWORD"), DB: 0})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("跳过 Redis 缓存测试（连接 %s 失败: %v）", addr, err)
	}
	return rdb
}

// TestRedisCacheWriteThroughAndContext 校验写穿、命中读、删除失效。
func TestRedisCacheWriteThroughAndContext(t *testing.T) {
	repo, _, clean := setupConvTest(t)
	defer clean()
	rdb := newTestRedis(t)
	cache := convcache.NewConversationCache(rdb)
	ctx := context.Background()
	orch := &fakeOrch{reply: "回复一"}
	svc := NewConversationService(repo, orch, nil, cache)

	conv, err := svc.Create(ctx, CreateInput{UserID: convTestUserA, ModelCode: "gpt-4o"})
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	defer cache.Invalidate(ctx, conv.ID)

	// 首轮：buildContext miss→回填(含 user)，assistant 写穿 → 缓存应有 2 条
	if err := svc.Chat(ctx, httptest.NewRecorder(), conv.ID, convTestUserA, "第一句", false); err != nil {
		t.Fatalf("首轮对话失败: %v", err)
	}
	snap, ok := cache.Get(ctx, conv.ID)
	if !ok {
		t.Fatalf("首轮后缓存应命中")
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("缓存应含 2 条(user+assistant)，实际 %d", len(snap.Messages))
	}

	// 第二轮：上下文从缓存取（含历史），轮后缓存应 4 条
	orch.reply = "回复二"
	if err := svc.Chat(ctx, httptest.NewRecorder(), conv.ID, convTestUserA, "第二句", false); err != nil {
		t.Fatalf("二轮对话失败: %v", err)
	}
	if len(orch.lastContext) < 3 {
		t.Fatalf("二轮上下文应含历史(≥3条)，实际 %d", len(orch.lastContext))
	}
	if snap2, _ := cache.Get(ctx, conv.ID); snap2 == nil || len(snap2.Messages) != 4 {
		t.Fatalf("二轮后缓存应为 4 条")
	}

	// 删除会话应清缓存
	if err := svc.Delete(ctx, conv.ID, convTestUserA); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, ok := cache.Get(ctx, conv.ID); ok {
		t.Fatalf("删除会话后缓存应失效")
	}
}
