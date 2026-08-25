package image

import "testing"

func TestMinIOObjectStoreRejectsUnsafeConfiguration(t *testing.T) {
	tests := []MinIOObjectStoreConfig{
		{},
		{Endpoint: "http://127.0.0.1:9000", PublicDownloadEndpoint: "https://assets.example.com", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "https://assets.example.com", AccessKey: "", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "http://assets.example.com", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "http://0.0.0.0:9000", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "https://user:pass@assets.example.com", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "https://assets.example.com/private", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "https://assets.example.com?internal=1", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "https://minio:9000", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"ai-result"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "https://assets.example.com", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"../escape"}},
		{Endpoint: "127.0.0.1:9000", PublicDownloadEndpoint: "https://assets.example.com", AccessKey: "fake", SecretKey: "fake", Buckets: []string{"same", "same"}},
	}
	for index, config := range tests {
		if _, err := NewMinIOObjectStore(config); err == nil {
			t.Fatalf("不安全配置必须拒绝: index=%d", index)
		}
	}
}

func TestMinIOObjectStoreAcceptsHTTPSAndLoopbackHTTPDownloadEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"https://assets.example.com",
		"https://assets.example.com:9443",
		"https://8.8.8.8",
		"http://127.0.0.1:19000",
		"http://localhost:19000",
		"http://[::1]:19000",
	} {
		store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
			Endpoint: "minio:9000", PublicDownloadEndpoint: endpoint,
			AccessKey: "fake-access", SecretKey: "fake-secret", Buckets: []string{"ai-result"},
		})
		if err != nil || store == nil {
			t.Fatalf("合法下载端点应通过构造: endpoint=%s err=%v", endpoint, err)
		}
	}
}
