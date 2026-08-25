package image

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeNetResolver struct {
	mu        sync.Mutex
	responses [][]netip.Addr
	calls     int
}

func (r *fakeNetResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.responses) == 0 {
		return nil, ErrImageURLDenied
	}
	index := r.calls
	if index >= len(r.responses) {
		index = len(r.responses) - 1
	}
	r.calls++
	return append([]netip.Addr(nil), r.responses[index]...), nil
}

type recordingDialer struct {
	mu        sync.Mutex
	addresses []string
}

func (d *recordingDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addresses = append(d.addresses, "called")
	return nil, errors.New("测试拨号停止")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestSafeHTTPFetcherRejectsSSRFAndMixedDNS(t *testing.T) {
	fetcher, err := NewSafeHTTPFetcher([]string{"cdn.example.com"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		addresses []netip.Addr
	}{
		{name: "loopback", addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{name: "private", addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")}},
		{name: "metadata", addresses: []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
		{name: "documentation", addresses: []netip.Addr{netip.MustParseAddr("203.0.113.10")}},
		{name: "mixed", addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}},
		{name: "ipv6_private", addresses: []netip.Addr{netip.MustParseAddr("fd00::1")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fetcher.resolver = &fakeNetResolver{responses: [][]netip.Addr{tc.addresses}}
			if _, err := fetcher.validateURL(context.Background(), "https://cdn.example.com/result.png"); !errors.Is(err, ErrImageURLDenied) {
				t.Fatalf("受限地址必须拒绝: %v", err)
			}
		})
	}
}

func TestSafeHTTPFetcherRejectsSchemeHostPortAndRedirects(t *testing.T) {
	fetcher, err := NewSafeHTTPFetcher([]string{"cdn.example.com"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fetcher.resolver = &fakeNetResolver{responses: [][]netip.Addr{{netip.MustParseAddr("8.8.8.8")}}}
	for _, rawURL := range []string{
		"http://cdn.example.com/result.png", "https://other.example.com/result.png",
		"https://user@cdn.example.com/result.png", "https://cdn.example.com:8443/result.png",
		"https://cdn.example.com/result.png#fragment", "https://cdn.example.com./result.png",
	} {
		if _, err := fetcher.validateURL(context.Background(), rawURL); !errors.Is(err, ErrImageURLDenied) {
			t.Fatalf("非法URL必须拒绝: %s err=%v", rawURL, err)
		}
	}
	request, _ := http.NewRequest(http.MethodGet, "https://other.example.com/result.png", nil)
	if err := fetcher.checkRedirect(request, []*http.Request{{}, {}, {}}); !errors.Is(err, ErrImageURLDenied) {
		t.Fatalf("超过重定向上限必须拒绝: %v", err)
	}
}

func TestSafeHTTPFetcherPreventsDNSRebindingAtDial(t *testing.T) {
	fetcher, err := NewSafeHTTPFetcher([]string{"cdn.example.com"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &fakeNetResolver{responses: [][]netip.Addr{
		{netip.MustParseAddr("8.8.8.8")},
		{netip.MustParseAddr("127.0.0.1")},
	}}
	dialer := &recordingDialer{}
	fetcher.resolver = resolver
	fetcher.dialer = dialer
	if _, err := fetcher.validateURL(context.Background(), "https://cdn.example.com/result.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.dialContext(context.Background(), "tcp", "cdn.example.com:443"); !errors.Is(err, ErrImageURLDenied) {
		t.Fatalf("拨号前DNS重绑定必须拒绝: %v", err)
	}
	if len(dialer.addresses) != 0 {
		t.Fatal("DNS重绑定被拒后不得拨号")
	}
}

func TestSafeHTTPFetcherBoundedResponse(t *testing.T) {
	fetcher, err := NewSafeHTTPFetcher([]string{"cdn.example.com"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fetcher.resolver = &fakeNetResolver{responses: [][]netip.Addr{{netip.MustParseAddr("8.8.8.8")}}}
	fetcher.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}},
			Body: io.NopCloser(strings.NewReader("12345")), ContentLength: -1,
		}, nil
	})}
	if _, err := fetcher.Fetch(context.Background(), "https://cdn.example.com/result.png", 4); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("无Content-Length响应仍必须有界读取: %v", err)
	}
	fetcher.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"image/png"}},
			Body: io.NopCloser(strings.NewReader("1234")), ContentLength: 4,
		}, nil
	})}
	result, err := fetcher.Fetch(context.Background(), "https://cdn.example.com/result.png", 4)
	if err != nil || string(result.Bytes) != "1234" || result.MediaType != "image/png" {
		t.Fatalf("有界响应读取错误: %+v err=%v", result, err)
	}
}

func TestAllowedPublicIPBoundary(t *testing.T) {
	if !allowedPublicIP(netip.MustParseAddr("8.8.8.8")) || !allowedPublicIP(netip.MustParseAddr("2606:4700:4700::1111")) {
		t.Fatal("公开单播地址应允许")
	}
	for _, raw := range []string{"0.0.0.0", "100.64.0.1", "192.0.2.1", "198.18.0.1", "203.0.113.1", "::1", "2001:db8::1"} {
		if allowedPublicIP(netip.MustParseAddr(raw)) {
			t.Fatalf("保留或不可路由地址必须拒绝: %s", raw)
		}
	}
}

func TestSafeHTTPFetcherRejectsInvalidAllowlistHosts(t *testing.T) {
	for _, host := range []string{
		"localhost", "127.0.0.1", "cdn_example.com", "cdn..example.com", "-cdn.example.com",
		"cdn-.example.com", "cdn.example.com.", "cdn.例子.com", "cdn.example.com:443",
	} {
		if _, err := NewSafeHTTPFetcher([]string{host}, time.Second); !errors.Is(err, ErrImageURLDenied) {
			t.Fatalf("非法Host白名单必须拒绝: %s err=%v", host, err)
		}
	}
	if _, err := NewSafeHTTPFetcher([]string{"CDN.Example.COM"}, time.Second); err != nil {
		t.Fatalf("合法ASCII Host应规范化为小写: %v", err)
	}
}
