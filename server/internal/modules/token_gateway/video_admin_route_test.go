package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVideoG6AdminTaskClosedRoute(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mux := http.NewServeMux()
		RegisterVideoAdminRoutes(mux, nil, nil, enabled)
		srv := httptest.NewServer(mux)
		resp, err := srv.Client().Get(srv.URL + "/api/admin/token/video-tasks/vid-closed")
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		srv.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("关闭或缺依赖的管理入口必须503：%d", resp.StatusCode)
		}
	}
}

func TestVideoG6AdminListClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoAdminRoutes(mux, nil, nil, false)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/api/admin/token/video-tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("管理列表关闭时必须503：%d", resp.StatusCode)
	}
}

func TestVideoG6AdminInputListClosedRoute(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mux := http.NewServeMux()
		RegisterVideoAdminRoutes(mux, nil, nil, enabled)
		srv := httptest.NewServer(mux)
		resp, err := srv.Client().Get(srv.URL + "/api/admin/token/video-input-assets")
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		srv.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("管理输入列表关闭或缺依赖必须503：%d", resp.StatusCode)
		}
	}
}

func TestVideoG6AdminOutputListClosedRoute(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mux := http.NewServeMux()
		RegisterVideoAdminRoutes(mux, nil, nil, enabled)
		srv := httptest.NewServer(mux)
		resp, err := srv.Client().Get(srv.URL + "/api/admin/token/video-assets")
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		srv.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("管理输出列表关闭或缺依赖必须503：%d", resp.StatusCode)
		}
	}
}

func TestVideoG6AdminSummaryClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoAdminRoutes(mux, nil, nil, false)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/api/admin/token/video-reconciliation/summary")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("对账汇总关闭时必须503：%d", resp.StatusCode)
	}
}

func TestVideoG6AdminCancelClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoAdminRoutes(mux, nil, nil, false)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/api/admin/token/video-tasks/video-closed/cancel", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("管理员取消关闭时必须503，实际%d", resp.StatusCode)
	}
}

func TestVideoG6AdminInputQuarantineClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoAdminRoutes(mux, nil, nil, false)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/api/admin/token/video-input-assets/vin_closed/quarantine", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("输入隔离关闭时必须503，实际%d", resp.StatusCode)
	}
}

func TestVideoG6AdminOutputQuarantineClosedRoute(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoAdminRoutes(mux, nil, nil, false)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/api/admin/token/video-assets/vasset_closed/quarantine", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("输出隔离关闭时应503实际%d", resp.StatusCode)
	}
}
