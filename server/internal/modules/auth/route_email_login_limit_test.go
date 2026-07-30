package auth

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"molin/server/internal/middleware"
)

func TestEmailCodeLoginIPLimitFrozenContract(t *testing.T) {
	if emailLoginCodeIPLimit != 10 || emailLoginCodeIPWindow.String() != "1m0s" {
		t.Fatalf("邮箱验证码登录 IP 限流必须保持每分钟十次: limit=%d window=%s", emailLoginCodeIPLimit, emailLoginCodeIPWindow)
	}
}

func TestEmailCodeLoginIPLimitRedisFailureKeepsExistingDegradation(t *testing.T) {
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: 0})
	defer redisClient.Close()
	resolver := middleware.NewPublicSourceIPResolver([]netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")})
	called := 0
	h := limitEmailCodeLoginByIP(redisClient, resolver, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login/email/code", strings.NewReader(`{"email":"user@example.invalid","code":"123456"}`))
	req.RemoteAddr = "198.51.100.7:443"
	req.Header.Set("X-Real-IP", "192.0.2.10")
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, req)
	if resp.Code != http.StatusNoContent || called != 1 {
		t.Fatalf("Redis 故障必须沿用邮件 IP 纵深限流的降级放行策略: status=%d called=%d body=%s", resp.Code, called, resp.Body.String())
	}
}
