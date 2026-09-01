package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type videoRouteProbe struct {
	method string
	path   string
}

// TestVideoG6CompleteRouteMatrixClosedByDefault 以真实ServeMux核对完整47条路由，避免文档计数掩盖漏注册或重复注册。
func TestVideoG6CompleteRouteMatrixClosedByDefault(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	RegisterVideoAdminRoutes(mux, nil, nil, false)
	RegisterVideoInternalRoutes(mux, nil, false)

	routes := []videoRouteProbe{
		{"POST", "/v1/videos"},
		{"GET", "/v1/videos"},
		{"GET", "/v1/videos/v_test"},
		{"DELETE", "/v1/videos/v_test"},
		{"GET", "/v1/videos/v_test/content"},
		{"POST", "/api/token/videos/quotes"},
		{"POST", "/api/token/videos/generations"},
		{"GET", "/api/token/video-tasks"},
		{"GET", "/api/token/video-assets/asset_test/lifecycle"},
		{"GET", "/api/token/video-assets/asset_test/download-url"},
		{"GET", "/api/token/video-assets/asset_test/content"},
		{"POST", "/api/token/video-assets/asset_test/save"},
		{"DELETE", "/api/token/video-assets/asset_test"},
		{"GET", "/api/token/video-saved-assets/1/content/download-url"},
		{"GET", "/api/token/video-saved-assets/1/content/content"},
		{"GET", "/api/token/video-tasks/task_test"},
		{"DELETE", "/api/token/video-tasks/task_test"},
		{"DELETE", "/api/token/video-tasks/by-video/v_test"},
		{"GET", "/api/token/video-tasks/task_test/events"},
		{"GET", "/api/token/videos/requests/req_test"},
		{"GET", "/api/token/videos/requests/by-video/v_test"},
		{"GET", "/api/token/video-rights-policy"},
		{"GET", "/api/token/projects/1/video-rights-acceptance"},
		{"POST", "/api/token/projects/1/video-rights-acceptance"},
		{"POST", "/api/token/video-inputs/upload-sessions"},
		{"POST", "/api/token/video-inputs/from-image-asset"},
		{"GET", "/api/token/video-inputs"},
		{"GET", "/api/token/video-input-source-images"},
		{"GET", "/api/token/video-inputs/input_test"},
		{"DELETE", "/api/token/video-inputs/input_test"},
		{"GET", "/api/token/video-inputs/upload-sessions/upload_test"},
		{"POST", "/api/token/video-inputs/upload-sessions/upload_test/complete"},
		{"DELETE", "/api/token/video-inputs/upload-sessions/upload_test"},
		{"POST", "/api/admin/token/video-project-grants"},
		{"POST", "/api/admin/token/video-adjustments"},
		{"POST", "/api/admin/token/video-tasks/task_test/archive-retry"},
		{"POST", "/api/admin/token/video-tasks/task_test/poll"},
		{"POST", "/api/admin/token/video-assets/asset_test/release"},
		{"POST", "/api/admin/token/video-assets/asset_test/quarantine"},
		{"POST", "/api/admin/token/video-input-assets/input_test/quarantine"},
		{"POST", "/api/admin/token/video-tasks/task_test/cancel"},
		{"GET", "/api/admin/token/video-reconciliation/summary"},
		{"GET", "/api/admin/token/video-assets"},
		{"GET", "/api/admin/token/video-input-assets"},
		{"GET", "/api/admin/token/video-tasks"},
		{"GET", "/api/admin/token/video-tasks/task_test"},
		{"POST", "/api/internal/ai/provider-callbacks/fake"},
	}
	if len(routes) != 47 {
		t.Fatalf("VID-G6路由清单必须精确为47条，实际%d", len(routes))
	}
	seen := map[string]struct{}{}
	for _, route := range routes {
		key := route.method + " " + route.path
		if _, exists := seen[key]; exists {
			t.Fatalf("路由清单存在重复：%s", key)
		}
		seen[key] = struct{}{}
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(""))
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("默认关闭路由必须返回503：route=%s status=%d body=%s", key, res.Code, res.Body.String())
		}
	}
}
