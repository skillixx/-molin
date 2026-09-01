package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 缺依赖时保存入口必须明确关闭，不得漏注册或偷偷启用Fake。
func TestVideoG6AssetSaveClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	s := httptest.NewServer(mux)
	defer s.Close()
	resp, err := s.Client().Post(s.URL+"/api/token/video-assets/asset_fixture/save", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("保存应关闭503，实际%d", resp.StatusCode)
	}
}
