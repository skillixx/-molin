package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"molin/server/internal/modules/presenton/service"
)

// ---- 测试替身 ----

type fakeTickets struct {
	payload *service.TicketPayload
	err     error
	got     string
}

func (f *fakeTickets) Consume(ctx context.Context, ticket string) (*service.TicketPayload, error) {
	f.got = ticket
	return f.payload, f.err
}

type fakeSessions struct {
	store map[string]service.TicketPayload
}

func newFakeSessions() *fakeSessions { return &fakeSessions{store: map[string]service.TicketPayload{}} }

func (f *fakeSessions) Save(ctx context.Context, sid string, p service.TicketPayload, ttl time.Duration) error {
	f.store[sid] = p
	return nil
}

func (f *fakeSessions) Load(ctx context.Context, sid string) (*service.TicketPayload, error) {
	p, ok := f.store[sid]
	if !ok {
		return nil, service.ErrSessionNotFound
	}
	return &p, nil
}

func newGW(t *testing.T, tickets ticketConsumer, sessions sessionStore, target string) *GatewayHandler {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	return NewGatewayHandler(tickets, sessions, u, "/app/presenton", "", "shared-secret", time.Hour, false)
}

// ---- Launch ----

func TestLaunch_ValidTicketSetsCookieAndRedirects(t *testing.T) {
	tickets := &fakeTickets{payload: &service.TicketPayload{UserID: 42, APIKey: "sk-abc"}}
	sessions := newFakeSessions()
	gw := newGW(t, tickets, sessions, "http://127.0.0.1:5000")

	req := httptest.NewRequest(http.MethodGet, "/app/presenton/launch?ticket=tkt1", nil)
	rec := httptest.NewRecorder()
	gw.Launch(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("应 302，得到 %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/app/presenton/" {
		t.Fatalf("重定向目标错误: %s", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName || cookies[0].Value == "" {
		t.Fatalf("应下发会话 cookie，得到 %+v", cookies)
	}
	// 会话已落库且 payload 正确
	p, err := sessions.Load(context.Background(), cookies[0].Value)
	if err != nil || p.UserID != 42 || p.APIKey != "sk-abc" {
		t.Fatalf("会话未正确保存: p=%+v err=%v", p, err)
	}
	if !cookies[0].HttpOnly {
		t.Fatal("cookie 应为 HttpOnly")
	}
}

func TestLaunch_InvalidTicket(t *testing.T) {
	tickets := &fakeTickets{err: service.ErrTicketNotFound}
	gw := newGW(t, tickets, newFakeSessions(), "http://127.0.0.1:5000")

	req := httptest.NewRequest(http.MethodGet, "/app/presenton/launch?ticket=bad", nil)
	rec := httptest.NewRecorder()
	gw.Launch(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无效票据应 401，得到 %d", rec.Code)
	}
}

func TestLaunch_MissingTicket(t *testing.T) {
	gw := newGW(t, &fakeTickets{}, newFakeSessions(), "http://127.0.0.1:5000")
	req := httptest.NewRequest(http.MethodGet, "/app/presenton/launch", nil)
	rec := httptest.NewRecorder()
	gw.Launch(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("缺 ticket 应 400，得到 %d", rec.Code)
	}
}

// ---- Proxy ----

func TestProxy_InjectsTrustedHeadersAndStripsSpoof(t *testing.T) {
	// 伪上游 presenton：记录收到的头与路径。
	var gotHeaders http.Header
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	sessions := newFakeSessions()
	_ = sessions.Save(context.Background(), "sid1", service.TicketPayload{UserID: 7, APIKey: "sk-real", Model: "DeepSeek"}, time.Hour)
	gw := newGW(t, &fakeTickets{}, sessions, upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/app/presenton/api/v1/ppt/presentation/all", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "sid1"})
	// 客户端伪造可信头，应被剥离后用服务端值覆盖。
	req.Header.Set(hdrUser, "999")
	req.Header.Set(hdrKey, "sk-FORGED")
	rec := httptest.NewRecorder()

	gw.Proxy(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，得到 %d", rec.Code)
	}
	// 前缀被剥离，转为 presenton 内部路径
	if gotPath != "/api/v1/ppt/presentation/all" {
		t.Fatalf("路径前缀未剥离: %s", gotPath)
	}
	// 注入的是服务端可信值，而非伪造值
	if gotHeaders.Get(hdrUser) != "7" {
		t.Fatalf("X-Molin-User-Id 应为 7（会话值），得到 %q", gotHeaders.Get(hdrUser))
	}
	if gotHeaders.Get(hdrKey) != "sk-real" {
		t.Fatalf("X-Molin-LLM-Key 应为 sk-real（会话值），得到 %q", gotHeaders.Get(hdrKey))
	}
	if gotHeaders.Get(hdrSecret) != "shared-secret" {
		t.Fatalf("X-Molin-Auth-Secret 应注入共享密钥，得到 %q", gotHeaders.Get(hdrSecret))
	}
	// F-D：会话所选模型应注入 X-Molin-LLM-Model
	if gotHeaders.Get(hdrModel) != "DeepSeek" {
		t.Fatalf("X-Molin-LLM-Model 应为 DeepSeek（会话值），得到 %q", gotHeaders.Get(hdrModel))
	}
}

func TestProxy_NoCookie(t *testing.T) {
	gw := newGW(t, &fakeTickets{}, newFakeSessions(), "http://127.0.0.1:5000")
	req := httptest.NewRequest(http.MethodGet, "/app/presenton/x", nil)
	rec := httptest.NewRecorder()
	gw.Proxy(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 cookie 应 401，得到 %d", rec.Code)
	}
}

func TestProxy_ExpiredSession(t *testing.T) {
	gw := newGW(t, &fakeTickets{}, newFakeSessions(), "http://127.0.0.1:5000")
	req := httptest.NewRequest(http.MethodGet, "/app/presenton/x", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: "missing"})
	rec := httptest.NewRecorder()
	gw.Proxy(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("失效会话应 401，得到 %d", rec.Code)
	}
}

// 确保替身满足接口（编译期校验）。
var _ sessionStore = (*fakeSessions)(nil)
var _ ticketConsumer = (*fakeTickets)(nil)
