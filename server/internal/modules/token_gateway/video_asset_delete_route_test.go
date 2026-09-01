package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 平台删除路由在未装配依赖或流量关闭时明确失败关闭，不能因漏注册而退化为普通404。
func TestVideoG6AssetDeleteClosedRoute(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mux := http.NewServeMux()
		RegisterVideoUserRoutes(mux, nil, nil, enabled)
		s := httptest.NewServer(mux)
		func() {
			defer s.Close()
			req, err := http.NewRequest(http.MethodDelete, s.URL+"/api/token/video-assets/vid_asset_closed_fixture", nil)
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{Proxy: nil}
			defer transport.CloseIdleConnections()
			resp, err := (&http.Client{Transport: transport}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("未装配平台删除应503实际%d", resp.StatusCode)
			}
		}()
	}
}
