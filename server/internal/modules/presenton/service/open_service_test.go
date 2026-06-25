package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---- 测试替身 ----

type fakeAccess struct {
	ok  bool
	err error
}

func (f *fakeAccess) HasActiveAccess(ctx context.Context, userID uint64) (bool, error) {
	return f.ok, f.err
}

type fakeKeyIssuer struct {
	key     string
	err     error
	called  bool
	gotUser uint64
}

func (f *fakeKeyIssuer) IssueUserKey(ctx context.Context, userID uint64) (string, error) {
	f.called = true
	f.gotUser = userID
	return f.key, f.err
}

type fakeTicketStore struct {
	saved   bool
	ticket  string
	payload TicketPayload
	ttl     time.Duration
}

func (f *fakeTicketStore) Save(ctx context.Context, ticket string, p TicketPayload, ttl time.Duration) error {
	f.saved = true
	f.ticket = ticket
	f.payload = p
	f.ttl = ttl
	return nil
}

// ---- 用例 ----

func TestOpen_Success(t *testing.T) {
	access := &fakeAccess{ok: true}
	keyIssuer := &fakeKeyIssuer{key: "sk-user-123"}
	store := &fakeTicketStore{}
	svc := NewOpenService(access, keyIssuer, store, "https://molin.example.com/", 5*time.Minute)

	res, err := svc.Open(context.Background(), 42)
	if err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
	// 签发 key 时带对了用户
	if !keyIssuer.called || keyIssuer.gotUser != 42 {
		t.Fatalf("应为 user=42 签发 key，实际 called=%v user=%d", keyIssuer.called, keyIssuer.gotUser)
	}
	// 票据已落库，payload 含本人 + key
	if !store.saved {
		t.Fatal("票据未保存")
	}
	if store.payload.UserID != 42 || store.payload.APIKey != "sk-user-123" {
		t.Fatalf("票据 payload 错误: %+v", store.payload)
	}
	if store.ttl != 5*time.Minute {
		t.Fatalf("TTL 错误: %v", store.ttl)
	}
	// EmbedURL 指向反代且带票据；base 末尾多余斜杠被规整
	if !strings.HasPrefix(res.EmbedURL, "https://molin.example.com/app/presenton/launch?ticket=") {
		t.Fatalf("EmbedURL 前缀错误: %s", res.EmbedURL)
	}
	if !strings.HasSuffix(res.EmbedURL, store.ticket) {
		t.Fatalf("EmbedURL 未携带保存的票据: url=%s ticket=%s", res.EmbedURL, store.ticket)
	}
	if res.ExpiresInSeconds != 300 {
		t.Fatalf("ExpiresInSeconds 错误: %d", res.ExpiresInSeconds)
	}
}

func TestOpen_NoAccess(t *testing.T) {
	access := &fakeAccess{ok: false}
	keyIssuer := &fakeKeyIssuer{key: "sk-should-not-issue"}
	store := &fakeTicketStore{}
	svc := NewOpenService(access, keyIssuer, store, "https://molin.example.com", 0)

	_, err := svc.Open(context.Background(), 7)
	if !errors.Is(err, ErrNoAccess) {
		t.Fatalf("期望 ErrNoAccess，得到: %v", err)
	}
	// 无权时不应签发 key，也不应落票据
	if keyIssuer.called {
		t.Fatal("无权访问却签发了 key")
	}
	if store.saved {
		t.Fatal("无权访问却保存了票据")
	}
}

func TestOpen_AccessError(t *testing.T) {
	access := &fakeAccess{err: errors.New("db down")}
	svc := NewOpenService(access, &fakeKeyIssuer{}, &fakeTicketStore{}, "https://molin.example.com", 0)

	if _, err := svc.Open(context.Background(), 1); err == nil {
		t.Fatal("闸门出错时应返回错误")
	}
}

func TestOpen_DefaultTTL(t *testing.T) {
	// ttl<=0 时取默认 5 分钟
	store := &fakeTicketStore{}
	svc := NewOpenService(&fakeAccess{ok: true}, &fakeKeyIssuer{key: "k"}, store, "https://x", 0)
	res, err := svc.Open(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExpiresInSeconds != 300 || store.ttl != 5*time.Minute {
		t.Fatalf("默认 TTL 未生效: secs=%d ttl=%v", res.ExpiresInSeconds, store.ttl)
	}
}
