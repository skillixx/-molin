package service

import (
	"testing"
)

func validVideoUploadMinIOConfig(t *testing.T) MinIOVideoUploadStoreConfig {
	t.Helper()
	return MinIOVideoUploadStoreConfig{Endpoint: "minio:9000", PublicUploadEndpoint: "http://127.0.0.1:19000", AccessKey: "fake-access", SecretKey: "fake-secret-value", SourceBucket: "ai-upload-temp", NormalizedBucket: "ai-result"}
}

func TestMinIOVideoUploadStoreRejectsUnsafeConfiguration(t *testing.T) {
	valid := validVideoUploadMinIOConfig(t)
	tests := []func(*MinIOVideoUploadStoreConfig){
		func(c *MinIOVideoUploadStoreConfig) { c.Endpoint = "http://minio:9000" },
		func(c *MinIOVideoUploadStoreConfig) { c.PublicUploadEndpoint = "http://minio:9000" },
		func(c *MinIOVideoUploadStoreConfig) { c.PublicUploadEndpoint = "http://10.0.0.1:9000" },
		func(c *MinIOVideoUploadStoreConfig) { c.PublicUploadEndpoint = "https://minio" },
		func(c *MinIOVideoUploadStoreConfig) { c.AccessKey = c.SecretKey },
		func(c *MinIOVideoUploadStoreConfig) { c.SourceBucket = c.NormalizedBucket },
		func(c *MinIOVideoUploadStoreConfig) { c.SourceBucket = "../escape" },
	}
	for index, mutate := range tests {
		candidate := valid
		mutate(&candidate)
		if _, err := NewMinIOVideoUploadStore(candidate); err == nil {
			t.Fatalf("不安全上传存储配置必须拒绝: index=%d", index)
		}
	}
}

func TestMinIOVideoUploadStoreValidatesFrozenTargets(t *testing.T) {
	store, err := NewMinIOVideoUploadStore(validVideoUploadMinIOConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	target := VideoUploadTarget{SessionID: "vup_contract", InputAssetID: "vin_contract", UserID: 7, ProjectID: 8, SourceType: "platform_presigned", SourceBucket: "ai-upload-temp", SourceKey: "original/7/8/vup_contract", NormalizedBucket: "ai-result", NormalizedKey: "normalized/7/8/vin_contract.png", MIMEType: "image/png", ExpectedSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 100}
	if !store.validTarget(target) {
		t.Fatal("冻结服务端目标应通过")
	}
	for _, mutate := range []func(*VideoUploadTarget){
		func(v *VideoUploadTarget) { v.SourceKey = "original/7/9/vup_contract" },
		func(v *VideoUploadTarget) { v.NormalizedKey = "normalized/7/8/other.png" },
		func(v *VideoUploadTarget) { v.SourceBucket = "forged" },
		func(v *VideoUploadTarget) { v.ExpectedSHA256 = "bad" },
		func(v *VideoUploadTarget) { v.SourceType = "external_url" },
	} {
		candidate := target
		mutate(&candidate)
		if store.validTarget(candidate) {
			t.Fatal("客户端漂移目标必须拒绝")
		}
	}
}
