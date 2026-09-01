package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 两个取消别名都必须有显式关闭门禁，不能漏注册或绕过默认关闭配置。
func TestVideoG6CancelHTTPClosedRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	server := httptest.NewServer(mux)
	defer server.Close()
	for _, path := range []string{"/api/token/video-tasks/video_fixture", "/api/token/video-tasks/by-video/video_fixture"} {
		r, err := http.NewRequest("DELETE", server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("取消路径必须关闭态503，实际%d", resp.StatusCode)
		}
	}
}
