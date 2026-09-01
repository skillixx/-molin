package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 签发和兑换均须显式关闭，不能漏注册成404，也不能缺依赖时旁路下载。
func TestVideoG6AssetDownloadClosedRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	s := httptest.NewServer(mux)
	defer s.Close()
	for _, suffix := range []string{"download-url", "content"} {
		r, err := s.Client().Get(s.URL + "/api/token/video-assets/asset_fixture/" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 503 {
			t.Fatalf("%s应明确关闭503，实际%d", suffix, r.StatusCode)
		}
	}
}
