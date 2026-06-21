package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	pkgjwt "molin/server/pkg/jwt"
)

// fakeBanChecker 内存桩，记录被封禁用户与被吊销 token。并发安全。
type fakeBanChecker struct {
	blocked map[uint64]bool
	revoked map[string]bool
}

func (f *fakeBanChecker) IsUserBlocked(_ context.Context, userID uint64) bool {
	return f.blocked[userID]
}
func (f *fakeBanChecker) IsAccessTokenRevoked(_ context.Context, rawToken string) bool {
	return f.revoked[rawToken]
}

// fakeAPIKeyResolver 内存桩，按明文 sk 映射到 (userID, apiKeyID)。线程安全（map 只读）。
type fakeAPIKeyResolver struct {
	keys      map[string][2]uint64 // sk 明文 -> [userID, apiKeyID]
	callCount int64                // 记录调用次数（验证 JWT 路径不会误调 resolver）
}

func (f *fakeAPIKeyResolver) ResolveKey(_ context.Context, rawSK string) (uint64, uint64, bool) {
	atomic.AddInt64(&f.callCount, 1)
	v, ok := f.keys[rawSK]
	if !ok {
		return 0, 0, false
	}
	return v[0], v[1], true
}

const testJWTSecret = "test-secret-for-require-user-auth"

// captureHandler 记录被放行请求注入的 user_id / api_key_id。
type captureHandler struct {
	mu       sync.Mutex
	called   bool
	userID   uint64
	apiKeyID uint64
}

func (c *captureHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = true
	c.userID = UserIDFromContext(r.Context())
	c.apiKeyID = APIKeyIDFromContext(r.Context())
}

func newReq(authHeader string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if authHeader != "" {
		r.Header.Set("Authorization", authHeader)
	}
	return r
}

// TestRequireUserAuth_SKPath sk 路径：合法 sk 注入 user_id + api_key_id。
func TestRequireUserAuth_SKPath(t *testing.T) {
	resolver := &fakeAPIKeyResolver{keys: map[string][2]uint64{
		"sk-molin-ABC123": {42, 7},
	}}
	cap := &captureHandler{}
	h := RequireUserAuth(testJWTSecret, nil, resolver, cap)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("Bearer sk-molin-ABC123"))

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", rec.Code)
	}
	if !cap.called {
		t.Fatal("期望放行到下游，实际被拦截")
	}
	if cap.userID != 42 {
		t.Errorf("期望 user_id=42，实际 %d", cap.userID)
	}
	if cap.apiKeyID != 7 {
		t.Errorf("期望 api_key_id=7，实际 %d", cap.apiKeyID)
	}
}

// TestRequireUserAuth_SKInvalid 无效 sk → 401，不放行。
func TestRequireUserAuth_SKInvalid(t *testing.T) {
	resolver := &fakeAPIKeyResolver{keys: map[string][2]uint64{}}
	cap := &captureHandler{}
	h := RequireUserAuth(testJWTSecret, nil, resolver, cap)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("Bearer sk-molin-UNKNOWN"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", rec.Code)
	}
	if cap.called {
		t.Fatal("无效 sk 不应放行到下游")
	}
}

// TestRequireUserAuth_JWTPath JWT 路径：合法 token 注入 user_id，api_key_id=0，且不调用 resolver。
func TestRequireUserAuth_JWTPath(t *testing.T) {
	token, err := pkgjwt.Generate(99, "u@example.com", testJWTSecret, 3600)
	if err != nil {
		t.Fatalf("生成 token 失败: %v", err)
	}
	resolver := &fakeAPIKeyResolver{keys: map[string][2]uint64{}}
	banChecker := &fakeBanChecker{blocked: map[uint64]bool{}, revoked: map[string]bool{}}
	cap := &captureHandler{}
	h := RequireUserAuth(testJWTSecret, banChecker, resolver, cap)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("Bearer "+token))

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", rec.Code)
	}
	if cap.userID != 99 {
		t.Errorf("期望 user_id=99，实际 %d", cap.userID)
	}
	if cap.apiKeyID != 0 {
		t.Errorf("JWT 路径 api_key_id 应为 0，实际 %d", cap.apiKeyID)
	}
	if atomic.LoadInt64(&resolver.callCount) != 0 {
		t.Errorf("JWT 路径不应调用 apiKeyResolver，实际调用 %d 次", resolver.callCount)
	}
}

// TestRequireUserAuth_JWTBlocked JWT 路径：封禁用户 → 401 40101。
func TestRequireUserAuth_JWTBlocked(t *testing.T) {
	token, _ := pkgjwt.Generate(7, "b@example.com", testJWTSecret, 3600)
	banChecker := &fakeBanChecker{blocked: map[uint64]bool{7: true}, revoked: map[string]bool{}}
	cap := &captureHandler{}
	h := RequireUserAuth(testJWTSecret, banChecker, nil, cap)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("Bearer "+token))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", rec.Code)
	}
	if cap.called {
		t.Fatal("封禁用户不应放行")
	}
}

// TestRequireUserAuth_JWTRevoked JWT 路径：吊销 token → 401。
func TestRequireUserAuth_JWTRevoked(t *testing.T) {
	token, _ := pkgjwt.Generate(8, "r@example.com", testJWTSecret, 3600)
	banChecker := &fakeBanChecker{blocked: map[uint64]bool{}, revoked: map[string]bool{token: true}}
	cap := &captureHandler{}
	h := RequireUserAuth(testJWTSecret, banChecker, nil, cap)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("Bearer "+token))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", rec.Code)
	}
	if cap.called {
		t.Fatal("吊销 token 不应放行")
	}
}

// TestRequireUserAuth_NilResolverDegrade resolver=nil 时，sk 形态 token 一律 401（灰度退化）。
func TestRequireUserAuth_NilResolverDegrade(t *testing.T) {
	cap := &captureHandler{}
	h := RequireUserAuth(testJWTSecret, nil, nil, cap)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("Bearer sk-molin-ANY"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("resolver=nil 时 sk 应 401，实际 %d", rec.Code)
	}
	if cap.called {
		t.Fatal("resolver=nil 时 sk 不应放行")
	}
}

// TestRequireUserAuth_NilResolverJWTStillWorks resolver=nil 时纯 JWT 仍可正常放行。
func TestRequireUserAuth_NilResolverJWTStillWorks(t *testing.T) {
	token, _ := pkgjwt.Generate(123, "ok@example.com", testJWTSecret, 3600)
	cap := &captureHandler{}
	h := RequireUserAuth(testJWTSecret, nil, nil, cap)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("Bearer "+token))

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d", rec.Code)
	}
	if cap.userID != 123 {
		t.Errorf("期望 user_id=123，实际 %d", cap.userID)
	}
}

// TestRequireUserAuth_MissingHeader 缺少 Authorization → 401。
func TestRequireUserAuth_MissingHeader(t *testing.T) {
	cap := &captureHandler{}
	h := RequireUserAuth(testJWTSecret, nil, &fakeAPIKeyResolver{keys: map[string][2]uint64{}}, cap)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq(""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望 401，实际 %d", rec.Code)
	}
	if cap.called {
		t.Fatal("无 Authorization 不应放行")
	}
}
