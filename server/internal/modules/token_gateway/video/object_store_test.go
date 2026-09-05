package video

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFakeVideoObjectStoreGeneratesLocationAndStreamsRanges(t *testing.T) {
	store := NewFakeVideoObjectStore()
	payload := []byte("0123456789-video-body")
	stored, err := store.Put(context.Background(), PutVideoObjectRequest{
		Zone: VideoObjectResult, TaskID: "vid_task_1", AssetID: "vasset_1", Role: "content",
		Body: bytes.NewReader(payload), MaxBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Ref.Bucket != "ai-result" || stored.Ref.ObjectKey != "vid_task_1/vasset_1/content.bin" || len(stored.SHA256) != 64 {
		t.Fatalf("服务端对象定位不符合合同: %+v", stored)
	}
	reader, err := store.GetRange(context.Background(), stored.Ref, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil || string(raw) != "56789" {
		t.Fatalf("Range读取错误: body=%q err=%v", raw, err)
	}
}

func TestFakeVideoObjectStoreIsIdempotentAndRejectsConflicts(t *testing.T) {
	store := NewFakeVideoObjectStore()
	request := PutVideoObjectRequest{Zone: VideoObjectTemporary, TaskID: "vid_task_2", AssetID: "vasset_2", Role: "preview", Body: strings.NewReader("same"), MaxBytes: 16}
	first, err := store.Put(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Body = strings.NewReader("same")
	second, err := store.Put(context.Background(), request)
	if err != nil || second.SHA256 != first.SHA256 {
		t.Fatalf("同键同内容必须幂等: first=%+v second=%+v err=%v", first, second, err)
	}
	request.Body = strings.NewReader("different")
	if _, err := store.Put(context.Background(), request); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatalf("同键不同内容必须冲突: %v", err)
	}
}

func TestFakeVideoObjectStoreQuarantineAndDeleteAreSafe(t *testing.T) {
	store := NewFakeVideoObjectStore()
	stored, err := store.Put(context.Background(), PutVideoObjectRequest{Zone: VideoObjectResult, TaskID: "vid_task_3", AssetID: "vasset_3", Role: "content", Body: strings.NewReader("media"), MaxBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	quarantined, err := store.MoveToQuarantine(context.Background(), stored.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if quarantined.Ref.Bucket != "ai-quarantine" || quarantined.SHA256 != stored.SHA256 {
		t.Fatalf("隔离迁移必须保留内容事实: %+v", quarantined)
	}
	if _, err := store.Head(context.Background(), stored.Ref); !errors.Is(err, ErrVideoObjectNotFound) {
		t.Fatalf("迁移后原结果对象必须不可见: %v", err)
	}
	for index := 0; index < 2; index++ {
		if err := store.Delete(context.Background(), quarantined.Ref); err != nil {
			t.Fatalf("删除不存在对象必须幂等: %v", err)
		}
	}
}

func TestFakeVideoObjectStoreEnforcesBoundsAndServerOwnedLocation(t *testing.T) {
	store := NewFakeVideoObjectStore()
	invalid := []PutVideoObjectRequest{
		{Zone: VideoObjectResult, TaskID: "../escape", AssetID: "vasset_4", Role: "content", Body: strings.NewReader("x"), MaxBytes: 16},
		{Zone: VideoObjectZone("client-bucket"), TaskID: "vid_task_4", AssetID: "vasset_4", Role: "content", Body: strings.NewReader("x"), MaxBytes: 16},
		{Zone: VideoObjectResult, TaskID: "vid_task_4", AssetID: "vasset_4", Role: "content", Body: strings.NewReader("too-large"), MaxBytes: 2},
	}
	for _, request := range invalid {
		if _, err := store.Put(context.Background(), request); err == nil {
			t.Fatalf("非法对象写入必须失败关闭: %+v", request)
		}
	}
	stored, err := store.Put(context.Background(), PutVideoObjectRequest{
		Zone: VideoObjectResult, TaskID: "vid_task_range", AssetID: "vasset_range", Role: "content",
		Body: bytes.NewReader(bytes.Repeat([]byte{0x5a}, (1<<20)+1)), MaxBytes: (1 << 20) + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRange(context.Background(), stored.Ref, 0, (1<<20)+1); !errors.Is(err, ErrVideoObjectInvalid) {
		t.Fatalf("单次Range超过1MiB必须拒绝: %v", err)
	}
}
