package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 生命周期路径缺依赖时必须有明确关闭态，不能因漏注册返回普通404。
func TestVideoG6AssetLifecycleClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	s := httptest.NewServer(mux)
	defer s.Close()
	r, err := s.Client().Get(s.URL + "/api/token/video-assets/asset_fixture/lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 503 {
		t.Fatalf("应关闭态503，实际%d", r.StatusCode)
	}
}
