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
	svc := NewOpenService(access, keyIssuer, store, "https://molin.example.com/", 5*time.Minute, nil)

	res, err := svc.Open(context.Background(), 42, "DeepSeek")
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
	// F-D：用户所选模型应随票据落库，供 D2 注入
	if store.payload.Model != "DeepSeek" {
		t.Fatalf("票据未携带所选模型: %+v", store.payload)
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
	svc := NewOpenService(access, keyIssuer, store, "https://molin.example.com", 0, nil)

	_, err := svc.Open(context.Background(), 7, "")
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
	svc := NewOpenService(access, &fakeKeyIssuer{}, &fakeTicketStore{}, "https://molin.example.com", 0, nil)

	if _, err := svc.Open(context.Background(), 1, ""); err == nil {
		t.Fatal("闸门出错时应返回错误")
	}
}

func TestOpen_ModelAllowlist(t *testing.T) {
	newSvc := func(allowed []string) (*OpenService, *fakeTicketStore) {
		store := &fakeTicketStore{}
		return NewOpenService(&fakeAccess{ok: true}, &fakeKeyIssuer{key: "k"}, store, "https://x", 0, allowed), store
	}

	// 配了白名单：命中 → 成功且票据带该模型
	svc, store := newSvc([]string{"GPT-4o", "gpt-4o-mini"})
	if _, err := svc.Open(context.Background(), 1, "GPT-4o"); err != nil {
		t.Fatalf("白名单内模型应成功: %v", err)
	}
	if store.payload.Model != "GPT-4o" {
		t.Fatalf("票据未带所选模型: %+v", store.payload)
	}

	// 配了白名单：未命中 → ErrModelNotAllowed，不签发 key、不落票据
	svc2, store2 := newSvc([]string{"GPT-4o"})
	if _, err := svc2.Open(context.Background(), 1, "DeepSeek"); !errors.Is(err, ErrModelNotAllowed) {
		t.Fatalf("白名单外模型应 ErrModelNotAllowed，得到: %v", err)
	}
	if store2.saved {
		t.Fatal("模型不允许却落了票据")
	}

	// 配了白名单：model 为空 → 用默认，放行
	svc3, _ := newSvc([]string{"GPT-4o"})
	if _, err := svc3.Open(context.Background(), 1, ""); err != nil {
		t.Fatalf("空模型应放行（用默认）: %v", err)
	}

	// 未配白名单（nil）：任意模型放行（向后兼容）
	svc4, _ := newSvc(nil)
	if _, err := svc4.Open(context.Background(), 1, "AnyModel"); err != nil {
		t.Fatalf("无白名单应不限制: %v", err)
	}

	// AllowedModels 返回配置的列表
	svc5, _ := newSvc([]string{"GPT-4o", "gpt-4o-mini"})
	if got := svc5.AllowedModels(); len(got) != 2 || got[0] != "GPT-4o" {
		t.Fatalf("AllowedModels 返回错误: %v", got)
	}
}

func TestOpen_DefaultTTL(t *testing.T) {
	// ttl<=0 时取默认 5 分钟
	store := &fakeTicketStore{}
	svc := NewOpenService(&fakeAccess{ok: true}, &fakeKeyIssuer{key: "k"}, store, "https://x", 0, nil)
	res, err := svc.Open(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.ExpiresInSeconds != 300 || store.ttl != 5*time.Minute {
		t.Fatalf("默认 TTL 未生效: secs=%d ttl=%v", res.ExpiresInSeconds, store.ttl)
	}
}
