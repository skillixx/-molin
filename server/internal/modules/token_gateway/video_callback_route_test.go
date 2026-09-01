package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVideoG6CallbackClosedRoute(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mux := http.NewServeMux()
		RegisterVideoInternalRoutes(mux, nil, enabled)
		srv := httptest.NewServer(mux)
		client := srv.Client()
		resp, err := client.Post(srv.URL+"/api/internal/ai/provider-callbacks/fake-native-async", "application/json", nil)
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		srv.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("关闭或缺依赖时必须503：%d", resp.StatusCode)
		}
	}
}
