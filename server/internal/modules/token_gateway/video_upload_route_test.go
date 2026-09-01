package token_gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 四条上传合同必须有明确关闭态，不因未装配存储而冒充可用或返回普通404。
func TestVideoG6UploadHTTPClosedRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	server := httptest.NewServer(mux)
	defer server.Close()
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/token/video-inputs/upload-sessions"},
		{"POST", "/api/token/video-inputs/from-image-asset"},
		{"GET", "/api/token/video-inputs/upload-sessions/vup_fixture"},
		{"POST", "/api/token/video-inputs/upload-sessions/vup_fixture/complete"},
		{"DELETE", "/api/token/video-inputs/upload-sessions/vup_fixture"},
	} {
		req, _ := http.NewRequest(tc.method, server.URL+tc.path, nil)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 503 {
			t.Errorf("%s %s应关闭态503，实际%d", tc.method, tc.path, res.StatusCode)
		}
	}
}

// 输入元数据查询也必须经过显式关闭门禁，不能误走普通404或绕过身份链。
func TestVideoG6InputReadHTTPClosedRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	server := httptest.NewServer(mux)
	defer server.Close()
	request, err := http.NewRequest("DELETE", server.URL+"/api/token/video-inputs/vin_fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 503 {
		t.Errorf("输入删除关闭态应为503，实际%d", response.StatusCode)
	}
	for _, path := range []string{"/api/token/video-inputs", "/api/token/video-inputs/vin_fixture", "/api/token/video-input-source-images"} {
		res, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 503 {
			t.Errorf("输入查询关闭态应为503，实际%d", res.StatusCode)
		}
	}
}

// 平台任务与账单查询同样默认关闭，不能因路径未注册而遗漏应用门禁。
func TestVideoG6TaskReadHTTPClosedRoutes(t *testing.T) {
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, nil, nil, false)
	server := httptest.NewServer(mux)
	defer server.Close()
	for _, path := range []string{"/api/token/video-tasks", "/api/token/video-tasks/video_fixture_0001", "/api/token/video-tasks/video_fixture_0001/events", "/api/token/videos/requests/vid_req_fixture", "/api/token/videos/requests/by-video/video_fixture_0001"} {
		res, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 503 {
			t.Errorf("平台读取应关闭态503：%s %d", path, res.StatusCode)
		}
	}
}
