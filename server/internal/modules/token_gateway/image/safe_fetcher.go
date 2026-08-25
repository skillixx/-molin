package image

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var (
	ErrImageURLDenied   = errors.New("图片URL目标不允许")
	ErrImageURLResponse = errors.New("图片URL响应无效")
)

type netIPResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type contextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type SafeHTTPFetcher struct {
	allowedHosts map[string]struct{}
	resolver     netIPResolver
	dialer       contextDialer
	client       *http.Client
}

func NewSafeHTTPFetcher(allowedHosts []string, timeout time.Duration) (*SafeHTTPFetcher, error) {
	if timeout <= 0 || len(allowedHosts) == 0 {
		return nil, ErrImageURLDenied
	}
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, host := range allowedHosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if !validAllowedDNSHost(normalized) {
			return nil, ErrImageURLDenied
		}
		allowed[normalized] = struct{}{}
	}
	fetcher := &SafeHTTPFetcher{allowedHosts: allowed, resolver: net.DefaultResolver, dialer: &net.Dialer{Timeout: timeout}}
	transport := &http.Transport{
		Proxy: nil, DisableCompression: true, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout,
	}
	transport.DialContext = fetcher.dialContext
	fetcher.client = &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: fetcher.checkRedirect}
	return fetcher, nil
}

func validAllowedDNSHost(host string) bool {
	if host == "" || len(host) > 253 || strings.HasSuffix(host, ".") || !strings.Contains(host, ".") {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			character := label[index]
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func (f *SafeHTTPFetcher) Fetch(ctx context.Context, rawURL string, maxBytes int64) (FetchedResult, error) {
	if f == nil || f.client == nil || maxBytes <= 0 {
		return FetchedResult{}, ErrImageURLDenied
	}
	parsed, err := f.validateURL(ctx, rawURL)
	if err != nil {
		return FetchedResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return FetchedResult{}, ErrImageURLDenied
	}
	response, err := f.client.Do(request)
	if err != nil {
		return FetchedResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return FetchedResult{}, ErrImageURLResponse
	}
	if response.ContentLength > maxBytes {
		return FetchedResult{}, ErrObjectTooLarge
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return FetchedResult{}, err
	}
	if int64(len(raw)) > maxBytes {
		return FetchedResult{}, ErrObjectTooLarge
	}
	return FetchedResult{Bytes: raw, MediaType: response.Header.Get("Content-Type")}, nil
}

func (f *SafeHTTPFetcher) checkRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return ErrImageURLDenied
	}
	_, err := f.validateURL(request.Context(), request.URL.String())
	return err
}

func (f *SafeHTTPFetcher) validateURL(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Hostname() == "" {
		return nil, ErrImageURLDenied
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.HasSuffix(host, ".") {
		return nil, ErrImageURLDenied
	}
	if _, ok := f.allowedHosts[host]; !ok {
		return nil, ErrImageURLDenied
	}
	port := parsed.Port()
	if port != "" && port != "443" {
		return nil, ErrImageURLDenied
	}
	if _, err := f.resolvePublic(ctx, host); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (f *SafeHTTPFetcher) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return nil, ErrImageURLDenied
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if _, ok := f.allowedHosts[host]; !ok || port != "443" {
		return nil, ErrImageURLDenied
	}
	addresses, err := f.resolvePublic(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, addressIP := range addresses {
		connection, dialErr := f.dialer.DialContext(ctx, network, net.JoinHostPort(addressIP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr == nil {
		lastErr = ErrImageURLDenied
	}
	return nil, lastErr
}

func (f *SafeHTTPFetcher) resolvePublic(ctx context.Context, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		literal = literal.Unmap()
		if !allowedPublicIP(literal) {
			return nil, ErrImageURLDenied
		}
		return []netip.Addr{literal}, nil
	}
	addresses, err := f.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrImageURLDenied
	}
	result := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !allowedPublicIP(address) {
			return nil, ErrImageURLDenied
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	if len(result) == 0 {
		return nil, ErrImageURLDenied
	}
	return result, nil
}

func allowedPublicIP(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefixText := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32",
	} {
		prefix := netip.MustParsePrefix(prefixText)
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
