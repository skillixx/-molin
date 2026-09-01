package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 私有内容也必须注册默认关闭合同，不能漏注册后用普通404冒充鉴权拒绝。
func TestVideoG6ContentHTTPClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	server := httptest.NewServer(mux)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/v1/videos/video_fixture/content")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("应为503关闭态，实际%d", response.StatusCode)
	}
}
