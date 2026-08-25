package image

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestImageG7MinIOIntegration(t *testing.T) {
	if os.Getenv("MOLIN_IMAGE_G7_ISOLATED") != "YES" {
		t.Skip("IMG-G7只允许隔离基础设施门禁执行")
	}
	internalEndpoint := os.Getenv("MOLIN_IMAGE_G7_MINIO_ENDPOINT")
	internalURL, err := url.Parse("http://" + internalEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	// 隔离测试用回环反向代理模拟浏览器可访问入口，并保留原始Host供MinIO校验签名。
	publicProxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(internalURL))
	defer publicProxy.Close()
	store, err := NewMinIOObjectStore(MinIOObjectStoreConfig{
		Endpoint: internalEndpoint, PublicDownloadEndpoint: publicProxy.URL,
		AccessKey: os.Getenv("MOLIN_IMAGE_G7_MINIO_ACCESS"), SecretKey: os.Getenv("MOLIN_IMAGE_G7_MINIO_SECRET"),
		Buckets: []string{"ai-upload-temp", "ai-result", "ai-quarantine"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := store.EnsureBuckets(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyBuckets(ctx); err != nil {
		t.Fatalf("运行时bucket只读校验失败: %v", err)
	}
	ref := ObjectRef{Bucket: "ai-result", Key: "g7/test/image.png"}
	body := testPNG(t, 8, 8)
	stored, err := store.Put(ctx, ref, bytes.NewReader(body), 1024)
	if err != nil || stored.SizeBytes != uint64(len(body)) {
		t.Fatalf("MinIO写入错误: stored=%+v err=%v", stored, err)
	}
	if _, err := store.Put(ctx, ref, strings.NewReader("conflict"), 1024); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("同键不同内容必须冲突: %v", err)
	}
	raw, err := store.Get(ctx, ref)
	if err != nil || !bytes.Equal(raw, body) {
		t.Fatalf("MinIO读取错误: body=%q err=%v", raw, err)
	}
	signed, err := store.SignDownload(ctx, ref, 5*time.Minute)
	if err != nil || signed == "" {
		t.Fatalf("MinIO签名错误: url=%s err=%v", signed, err)
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("签名URL读取错误: status=%v err=%v", responseStatus(response), err)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	signedBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1025))
	response.Body.Close()
	if mediaErr != nil || mediaType != "image/png" || readErr != nil || !bytes.Equal(signedBody, body) || !bytes.HasPrefix(signedBody, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("签名下载必须返回真实PNG: media=%s bytes=%d parse=%v read=%v", mediaType, len(signedBody), mediaErr, readErr)
	}
	unsignedRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, publicProxy.URL+"/ai-result/g7/test/image.png", nil)
	unsignedResponse, unsignedErr := http.DefaultClient.Do(unsignedRequest)
	if unsignedErr != nil || unsignedResponse.StatusCode == http.StatusOK {
		t.Fatalf("匿名读取必须拒绝: status=%v err=%v", responseStatus(unsignedResponse), unsignedErr)
	}
	if unsignedResponse != nil {
		_, _ = io.Copy(io.Discard, unsignedResponse.Body)
		unsignedResponse.Body.Close()
	}
	if err := store.Delete(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Head(ctx, ref); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("删除后对象必须不存在: %v", err)
	}
}

func TestImageG7RabbitMQTopologyAndDLQ(t *testing.T) {
	if os.Getenv("MOLIN_IMAGE_G7_ISOLATED") != "YES" {
		t.Skip("IMG-G7只允许隔离基础设施门禁执行")
	}
	queue, err := NewImageTaskQueue(ImageTaskQueueConfig{
		URL: os.Getenv("MOLIN_IMAGE_G7_RABBIT_URL"), Exchange: "molin.image.g7", Queue: "molin.image.g7.generate", RoutingKey: "image.generate",
		DeadExchange: "molin.image.g7.dead", DeadQueue: "molin.image.g7.dead", DeadRouting: "image.generate.dead",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := queue.EnsureTopology(ctx); err != nil {
		t.Fatal(err)
	}
	if err := queue.Publish(ctx, "g7-success"); err != nil {
		t.Fatal(err)
	}
	handler := &g7MessageHandler{}
	if err := queue.ConsumeOne(ctx, handler); err != nil || handler.requestID != "g7-success" {
		t.Fatalf("Rabbit消费错误: request=%s err=%v", handler.requestID, err)
	}
	if err := queue.Publish(ctx, "g7-failure"); err != nil {
		t.Fatal(err)
	}
	if err := queue.ConsumeOne(ctx, g7FailMessageHandler{}); err == nil {
		t.Fatal("处理失败必须返回错误并进入DLQ")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mainDepth, deadDepth, inspectErr := queue.QueueDepths()
		if inspectErr == nil && mainDepth == 0 && deadDepth == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("DLQ深度错误: main=%d dead=%d err=%v", mainDepth, deadDepth, inspectErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

type g7MessageHandler struct{ requestID string }

func (h *g7MessageHandler) HandleImageTask(_ context.Context, requestID string) error {
	h.requestID = requestID
	return nil
}

type g7FailMessageHandler struct{}

func (g7FailMessageHandler) HandleImageTask(context.Context, string) error {
	return errors.New("注入失败")
}

func responseStatus(response *http.Response) interface{} {
	if response == nil {
		return nil
	}
	return response.StatusCode
}
