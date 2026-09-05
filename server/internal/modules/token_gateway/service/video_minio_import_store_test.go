package service

import (
	"bytes"
	"context"
	"errors"
	"testing"

	imagegateway "molin/server/internal/modules/token_gateway/image"
)

func TestVideoMinIOImportStoreSeparatesSourceAndNormalizedTarget(t *testing.T) {
	store := imagegateway.NewFakeObjectStore()
	adapter, err := NewVideoMinIOImportStore(store, "ai-result")
	if err != nil {
		t.Fatal(err)
	}
	source := VideoImportObject{Bucket: "ai-result", Key: "0123456789abcdef0123456789abcdef/0/primary.png"}
	raw := []byte("source-image")
	if _, err := store.Put(context.Background(), imagegateway.ObjectRef{Bucket: source.Bucket, Key: source.Key}, bytes.NewReader(raw), 1024); err != nil {
		t.Fatal(err)
	}
	if read, err := adapter.Read(context.Background(), source, 1024); err != nil || !bytes.Equal(read, raw) {
		t.Fatalf("来源读取失败: body=%q err=%v", read, err)
	}
	target := VideoImportObject{Bucket: "ai-result", Key: "import/1/2/vin_fixture.png"}
	normalized := []byte("normalized")
	digest := videoPayloadSHA256(normalized)
	if err := adapter.Put(context.Background(), target, normalized, digest); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Put(context.Background(), target, normalized, digest); err != nil {
		t.Fatalf("规范化写入重放必须幂等: %v", err)
	}
	if err := adapter.Put(context.Background(), target, []byte("different"), digest); !errors.Is(err, ErrVideoImportConflict) {
		t.Fatalf("正文与摘要漂移必须拒绝: %v", err)
	}
	if err := adapter.Discard(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if discarded, err := adapter.VerifyDiscarded(context.Background(), target); err != nil || !discarded {
		t.Fatalf("删除确认失败: discarded=%t err=%v", discarded, err)
	}
	if _, err := adapter.Read(context.Background(), target, 1024); !errors.Is(err, ErrVideoImportUnavailable) {
		t.Fatalf("import目标不得冒充图片来源: %v", err)
	}
}
