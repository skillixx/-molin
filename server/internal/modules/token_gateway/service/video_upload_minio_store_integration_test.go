package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestVideoG7MinIOUploadSealIntegration(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G7_MINIO_ISOLATED") != "YES" {
		t.Skip("VID-G7只允许隔离MinIO门禁执行")
	}
	endpoint := os.Getenv("MOLIN_VIDEO_G7_MINIO_ENDPOINT")
	internalURL, err := url.Parse("http://" + endpoint)
	if err != nil {
		t.Fatal(err)
	}
	// 浏览器上传入口必须是回环HTTP或HTTPS；隔离测试用回环代理连接容器内MinIO。
	publicProxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(internalURL))
	defer publicProxy.Close()
	store, err := NewMinIOVideoUploadStore(MinIOVideoUploadStoreConfig{
		Endpoint: endpoint, PublicUploadEndpoint: publicProxy.URL,
		AccessKey: os.Getenv("MOLIN_VIDEO_G7_MINIO_ACCESS"), SecretKey: os.Getenv("MOLIN_VIDEO_G7_MINIO_SECRET"),
		SourceBucket: "ai-upload-temp", NormalizedBucket: "ai-result",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := store.EnsureBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	original := []byte("synthetic-image-fixture")
	target := VideoUploadTarget{SessionID: "vup_minio_contract", InputAssetID: "vin_minio_contract", UserID: 71, ProjectID: 72, SourceType: "platform_presigned", SourceBucket: "ai-upload-temp", SourceKey: "original/71/72/vup_minio_contract", NormalizedBucket: "ai-result", NormalizedKey: "normalized/71/72/vin_minio_contract.png", MIMEType: "image/png", ExpectedSHA256: videoPayloadSHA256(original), SizeBytes: uint64(len(original)), UploadExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
	grant, err := store.Issue(ctx, target)
	if err != nil || grant == nil || grant.Method != http.MethodPut || len(grant.Headers) != 5 {
		t.Fatalf("签发冻结上传能力失败: grant=%+v err=%v", grant, err)
	}
	upload := func(raw []byte, headers map[string]string) int {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, http.MethodPut, grant.URL, bytes.NewReader(raw))
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return response.StatusCode
	}
	wrongHeaders := make(map[string]string, len(grant.Headers))
	for name, value := range grant.Headers {
		wrongHeaders[name] = value
	}
	wrongHeaders["Content-Type"] = "image/jpeg"
	if status := upload(original, wrongHeaders); status < 400 {
		t.Fatalf("漂移MIME必须被签名拒绝: status=%d", status)
	}
	if status := upload(original, grant.Headers); status != http.StatusOK {
		t.Fatalf("受控PUT失败: status=%d", status)
	}
	if status := upload([]byte("late-overwrite"), grant.Headers); status < 400 {
		t.Fatalf("同一短效URL不得覆写已上传对象: status=%d", status)
	}
	sealed, err := store.Seal(ctx, target, videoUploadMaxBytes)
	if err != nil || !bytes.Equal(sealed.Bytes, original) || sealed.MIMEType != target.MIMEType || sealed.ETag == "" {
		t.Fatalf("封存快照失败: sealed=%+v err=%v", sealed, err)
	}
	normalized := []byte("normalized-png-fixture")
	normalizedHash := videoPayloadSHA256(normalized)
	if err := store.PutNormalized(ctx, target, normalized, normalizedHash); err != nil {
		t.Fatal(err)
	}
	if err := store.PutNormalized(ctx, target, normalized, normalizedHash); err != nil {
		t.Fatalf("规范化副本重放必须幂等: %v", err)
	}
	read, err := store.ReadNormalized(ctx, target.NormalizedBucket, target.NormalizedKey, videoUploadMaxBytes)
	if err != nil || !bytes.Equal(read, normalized) {
		t.Fatalf("规范化副本读取失败: body=%q err=%v", read, err)
	}
	if err := store.Discard(ctx, target); err != nil {
		t.Fatal(err)
	}
	if discarded, err := store.VerifyDiscarded(ctx, target); err != nil || !discarded {
		t.Fatalf("墓碑清理确认失败: discarded=%t err=%v", discarded, err)
	}
	if status := upload(original, grant.Headers); status < 400 {
		t.Fatalf("清理后旧URL不得复活原图: status=%d", status)
	}
	if _, err := store.Seal(ctx, target, videoUploadMaxBytes); !errors.Is(err, ErrVideoUploadConflict) {
		t.Fatalf("墓碑不得被当作原图封存: %v", err)
	}

	inline := target
	inline.SessionID = "vup_inline_contract"
	inline.InputAssetID = "vin_inline_contract"
	inline.SourceType = "openai_inline_multipart"
	inline.SourceKey = "inline/71/72/vup_inline_contract"
	inline.NormalizedKey = "normalized/71/72/vin_inline_contract.png"
	inline.ExpectedSHA256 = videoPayloadSHA256(original)
	if err := store.PutOriginal(ctx, inline, bytes.NewReader(original), uint64(len(original)), inline.ExpectedSHA256); err != nil {
		t.Fatal(err)
	}
	if err := store.PutOriginal(ctx, inline, bytes.NewReader(original), uint64(len(original)), inline.ExpectedSHA256); err != nil {
		t.Fatalf("inline同正文重放必须幂等: %v", err)
	}
	if err := store.PutOriginal(ctx, inline, bytes.NewReader([]byte("different")), uint64(len(original)), inline.ExpectedSHA256); !errors.Is(err, ErrVideoUploadConflict) {
		t.Fatalf("inline漂移正文必须拒绝: %v", err)
	}
}
