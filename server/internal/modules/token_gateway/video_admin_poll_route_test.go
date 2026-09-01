package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVideoG6AdminPollClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoAdminRoutes(mux, nil, nil, false)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	r, err := srv.Client().Post(srv.URL+"/api/admin/token/video-tasks/video_closed/poll", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 503 {
		t.Fatalf("管理轮询默认关闭应503实际%d", r.StatusCode)
	}
}
