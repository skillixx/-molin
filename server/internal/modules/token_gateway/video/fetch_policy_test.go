package video

import (
	"errors"
	"net"
	"testing"
)

func TestLocalOnlyMediaFetchPolicyRejectsNetworkThreatMatrix(t *testing.T) {
	policy := LocalOnlyMediaFetchPolicy{}
	tests := []struct {
		name    string
		attempt MediaFetchAttempt
		err     error
	}{
		{name: "external_url", attempt: MediaFetchAttempt{URL: "https://example.com/video.mp4"}, err: ErrMediaSSRF},
		{name: "loopback", attempt: MediaFetchAttempt{URL: "http://127.0.0.1/video.mp4", FirstResolution: []net.IP{net.ParseIP("127.0.0.1")}}, err: ErrMediaSSRF},
		{name: "metadata", attempt: MediaFetchAttempt{URL: "http://169.254.169.254/latest/meta-data"}, err: ErrMediaSSRF},
		{name: "redirect", attempt: MediaFetchAttempt{URL: "https://example.com/video", RedirectURL: "http://10.0.0.1/video"}, err: ErrMediaRedirect},
		{name: "dns_rebinding", attempt: MediaFetchAttempt{URL: "https://example.com/video", FirstResolution: []net.IP{net.ParseIP("203.0.113.10")}, SecondResolution: []net.IP{net.ParseIP("10.0.0.1")}}, err: ErrMediaDNSRebinding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := policy.Validate(test.attempt); !errors.Is(err, test.err) {
				t.Fatalf("威胁分类错误: want=%v got=%v", test.err, err)
			}
		})
	}
	if err := policy.Validate(MediaFetchAttempt{ControlledRef: &ControlledContentRef{ProviderTaskID: "taskUUID-safe", ContentID: "content-safe", MediaType: "video/mp4"}}); err != nil {
		t.Fatalf("受控内容句柄应通过: %v", err)
	}
}
