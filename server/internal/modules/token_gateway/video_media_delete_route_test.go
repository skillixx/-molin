package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 媒体删除未开放时也必须明确返回关闭态，而不是遗漏路由。
func TestVideoG6MediaDeleteClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	s := httptest.NewServer(mux)
	defer s.Close()
	r, err := http.NewRequest("DELETE", s.URL+"/v1/videos/video_fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("媒体删除应返回关闭态503，实际%d", resp.StatusCode)
	}
}
