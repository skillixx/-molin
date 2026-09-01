package handler

import "testing"

func TestVideoG6InlineReadLimiterBoundsUserMemory(t *testing.T) {
	limiter := &videoInlineReadLimiter{used: map[uint64]int64{}}
	releases := make([]func(), 0, 6)
	for index := 0; index < 6; index++ {
		release, ok := limiter.acquire(7, 10<<20)
		if !ok {
			t.Fatalf("第%d个预算内请求应获准", index+1)
		}
		releases = append(releases, release)
	}
	if _, ok := limiter.acquire(7, 10<<20); ok {
		t.Fatal("同用户累计超过64MiB必须在读取正文前拒绝")
	}
	other, ok := limiter.acquire(8, 10<<20)
	if !ok {
		t.Fatal("不同用户必须使用独立预算")
	}
	other()
	for _, release := range releases {
		release()
	}
	second, ok := limiter.acquire(7, 20<<20)
	if !ok {
		t.Fatal("请求结束释放预算后必须允许重试")
	}
	second()
	if len(limiter.used) != 0 {
		t.Fatal("全部释放后不得残留用户计数")
	}
}
