package service

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fixedHealthResolver struct {
	addresses []net.IPAddr
}

func (r fixedHealthResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, nil
}

func TestProbeChannelHealthUsesPublicHealthWithoutCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health" || request.Header.Get("Authorization") != "" {
			t.Fatalf("健康探测必须只访问公开 /health 且不携带凭证: path=%s auth=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	status, class := probeChannelHealthWithPolicy(context.Background(), server.Client(), net.DefaultResolver, server.URL+"/v1", []string{server.Listener.Addr().String()})
	if status != "healthy" || class != "" {
		t.Fatalf("健康入口返回 200 时应标记健康: status=%s class=%s", status, class)
	}
}

func TestProbeChannelHealthClassifiesFailureWithoutRawDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	status, class := probeChannelHealthWithPolicy(context.Background(), server.Client(), net.DefaultResolver, server.URL, []string{server.Listener.Addr().String()})
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
	status, class = probeChannelHealthWithPolicy(context.Background(), redirect.Client(), net.DefaultResolver, redirect.URL, []string{redirect.Listener.Addr().String()})
	if status != "down" || class != "http_302" || targetReached {
		t.Fatalf("健康检测不得跟随重定向: status=%s class=%s target_reached=%t", status, class, targetReached)
	}
}

func TestProbeChannelHealthRejectsPrivateLoopbackAndIPv6(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1:8080",
		"http://10.0.0.1:8080",
		"http://172.16.0.1:8080",
		"http://192.168.1.1:8080",
		"http://[::1]:8080",
		"http://[fc00::1]:8080",
		"http://[fe80::1]:8080",
	}
	for _, target := range blocked {
		status, class := probeChannelHealth(context.Background(), http.DefaultClient, target)
		if status != "down" || (class != "restricted_address" && class != "insecure_scheme") {
			t.Fatalf("受限地址必须在外呼前拒绝: target=%s status=%s class=%s", target, status, class)
		}
	}
}

func TestProbeChannelHealthRejectsDNSResolvedPrivateAddress(t *testing.T) {
	resolver := fixedHealthResolver{addresses: []net.IPAddr{{IP: net.ParseIP("10.0.0.8")}}}
	status, class := probeChannelHealthWithPolicy(context.Background(), http.DefaultClient, resolver, "https://gateway.example.com", nil)
	if status != "down" || class != "network_error" {
		t.Fatalf("域名解析到私网时必须拒绝: status=%s class=%s", status, class)
	}
}

func TestProbeChannelHealthAllowsOnlyExplicitInternalTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusOK) }))
	defer server.Close()
	status, class := probeChannelHealthWithPolicy(context.Background(), server.Client(), net.DefaultResolver, server.URL, []string{server.Listener.Addr().String()})
	if status != "healthy" || class != "" {
		t.Fatalf("显式白名单内网健康入口应可探测: status=%s class=%s", status, class)
	}
}
