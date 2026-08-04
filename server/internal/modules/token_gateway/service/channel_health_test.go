package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeChannelHealthUsesPublicHealthWithoutCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health" || request.Header.Get("Authorization") != "" {
			t.Fatalf("健康探测必须只访问公开 /health 且不携带凭证: path=%s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	status, class := probeChannelHealth(context.Background(), server.Client(), server.URL+"/v1")
	if status != "healthy" || class != "" {
		t.Fatalf("健康入口返回 200 时应标记健康: status=%s class=%s", status, class)
	}
}

func TestProbeChannelHealthClassifiesFailureWithoutRawDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	status, class := probeChannelHealth(context.Background(), server.Client(), server.URL)
	if status != "down" || class != "http_503" {
		t.Fatalf("非 200 响应必须保存受控错误类别: status=%s class=%s", status, class)
	}
	status, class = probeChannelHealth(context.Background(), server.Client(), "file:///etc/passwd")
	if status != "down" || class != "invalid_base_url" {
		t.Fatalf("非法协议必须在发出请求前拒绝: status=%s class=%s", status, class)
	}
}

func TestProbeChannelHealthRejectsMetadataAndRedirect(t *testing.T) {
	status, class := probeChannelHealth(context.Background(), http.DefaultClient, "http://169.254.169.254/latest/meta-data")
	if status != "down" || class != "restricted_address" {
		t.Fatalf("云元数据链路本地地址必须在发出请求前拒绝: status=%s class=%s", status, class)
	}
	targetReached := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetReached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	status, class = probeChannelHealth(context.Background(), redirect.Client(), redirect.URL)
	if status != "down" || class != "http_302" || targetReached {
		t.Fatalf("健康检测不得跟随重定向: status=%s class=%s target_reached=%t", status, class, targetReached)
	}
}
