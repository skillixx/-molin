package video

import (
	"errors"
	"net"
	"strings"
)

var (
	ErrMediaSSRF         = errors.New("视频媒体来源违反SSRF策略")
	ErrMediaRedirect     = errors.New("视频媒体抓取禁止重定向")
	ErrMediaDNSRebinding = errors.New("视频媒体来源发生DNS重绑定")
)

type MediaFetchAttempt struct {
	ControlledRef    *ControlledContentRef
	URL              string
	RedirectURL      string
	FirstResolution  []net.IP
	SecondResolution []net.IP
}

// LocalOnlyMediaFetchPolicy 在G4完全关闭网络数据面，只允许Provider返回的受控内容句柄。
type LocalOnlyMediaFetchPolicy struct{}

func (LocalOnlyMediaFetchPolicy) Validate(attempt MediaFetchAttempt) error {
	if strings.TrimSpace(attempt.RedirectURL) != "" {
		return ErrMediaRedirect
	}
	if resolutionChanged(attempt.FirstResolution, attempt.SecondResolution) {
		return ErrMediaDNSRebinding
	}
	if strings.TrimSpace(attempt.URL) != "" || containsUnsafeIP(attempt.FirstResolution) || containsUnsafeIP(attempt.SecondResolution) {
		return ErrMediaSSRF
	}
	if attempt.ControlledRef == nil || !strings.HasPrefix(attempt.ControlledRef.ProviderTaskID, "taskUUID-") ||
		!strings.HasPrefix(attempt.ControlledRef.ContentID, "content-") || attempt.ControlledRef.MediaType != "video/mp4" {
		return ErrMediaSourceUnsafe
	}
	return nil
}

func resolutionChanged(first, second []net.IP) bool {
	if len(second) == 0 {
		return false
	}
	if len(first) != len(second) {
		return true
	}
	for index := range first {
		if !first[index].Equal(second[index]) {
			return true
		}
	}
	return false
}

func containsUnsafeIP(addresses []net.IP) bool {
	for _, address := range addresses {
		if address == nil || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() || address.IsMulticast() {
			return true
		}
	}
	return false
}
