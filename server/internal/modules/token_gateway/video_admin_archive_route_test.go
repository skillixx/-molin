package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVideoG6AdminArchiveClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoAdminRoutes(mux, nil, nil, false)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/api/admin/token/video-tasks/video_closed/archive-retry", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("归档重试默认关闭应503实际%d", resp.StatusCode)
	}
}
