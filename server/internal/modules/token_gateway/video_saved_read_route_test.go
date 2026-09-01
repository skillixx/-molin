package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 长期读取也必须显式关闭，不能漏注册或缺依赖时绕过鉴权。
func TestVideoG6SavedReadClosedRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	s := httptest.NewServer(mux)
	defer s.Close()
	for _, suffix := range []string{"download-url", "content"} {
		r, err := s.Client().Get(s.URL + "/api/token/video-saved-assets/1/content/" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 503 {
			t.Fatalf("长期读取应关闭503，实际%d", r.StatusCode)
		}
	}
}
