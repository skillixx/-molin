package image

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFakeObjectStoreContract(t *testing.T) {
	now := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	store := NewFakeObjectStore()
	store.now = func() time.Time { return now }
	ref := ObjectRef{Bucket: "ai-result", Key: "tenant/date/object.jpeg"}
	body := []byte("fake-image-bytes")
	stored, err := store.Put(context.Background(), ref, bytes.NewReader(body), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SizeBytes != uint64(len(body)) || len(stored.SHA256) != 64 || !stored.CreatedAt.Equal(now) {
		t.Fatalf("对象元数据错误: %+v", stored)
	}
	got, err := store.Get(context.Background(), ref)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("读取对象失败: %q %v", got, err)
	}
	got[0] = 'X'
	again, _ := store.Get(context.Background(), ref)
	if bytes.Equal(got, again) {
		t.Fatal("Get 必须返回副本，调用方不能改写存储事实")
	}
	url, err := store.SignDownload(context.Background(), ref, 15*time.Minute)
	if err != nil || !strings.HasPrefix(url, "https://object.invalid/") || !strings.Contains(url, "expires=") {
		t.Fatalf("Fake短效地址错误: %s %v", url, err)
	}
	if err := store.Delete(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), ref); err != nil {
		t.Fatalf("重复删除必须幂等: %v", err)
	}
	if _, err := store.Get(context.Background(), ref); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("删除后读取必须返回不存在: %v", err)
	}
}

func TestFakeObjectStoreLimitsConflictsAndReferences(t *testing.T) {
	store := NewFakeObjectStore()
	ref := ObjectRef{Bucket: "ai-result", Key: "tenant/object.jpeg"}
	if _, err := store.Put(context.Background(), ref, bytes.NewReader([]byte("12345")), 4); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("超限对象必须拒绝: %v", err)
	}
	if _, err := store.Put(context.Background(), ref, bytes.NewReader([]byte("same")), 4); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), ref, bytes.NewReader([]byte("diff")), 4); !errors.Is(err, ErrObjectConflict) {
		t.Fatalf("相同键不同内容必须冲突: %v", err)
	}
	for _, invalid := range []ObjectRef{
		{Bucket: "", Key: "a"}, {Bucket: "ai/result", Key: "a"}, {Bucket: "ai-result", Key: "/a"},
		{Bucket: "ai-result", Key: "a/../b"}, {Bucket: "ai-result", Key: "a\\b"},
	} {
		if _, err := store.Put(context.Background(), invalid, bytes.NewReader([]byte("x")), 1); !errors.Is(err, ErrObjectInvalid) {
			t.Fatalf("非法对象定位必须拒绝: %+v err=%v", invalid, err)
		}
	}
	if _, err := store.SignDownload(context.Background(), ref, 16*time.Minute); !errors.Is(err, ErrObjectInvalid) {
		t.Fatalf("超过15分钟的签名地址必须拒绝: %v", err)
	}
}

func TestFakeObjectStoreConcurrentIdempotentPut(t *testing.T) {
	store := NewFakeObjectStore()
	ref := ObjectRef{Bucket: "ai-result", Key: "tenant/concurrent.jpeg"}
	body := []byte("same-content")
	var success atomic.Int64
	var wg sync.WaitGroup
	for index := 0; index < 100; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.Put(context.Background(), ref, bytes.NewReader(body), 1024); err != nil {
				t.Errorf("并发幂等写入失败: %v", err)
				return
			}
			success.Add(1)
		}()
	}
	wg.Wait()
	if success.Load() != 100 {
		t.Fatalf("相同内容并发写入应全部幂等成功: %d", success.Load())
	}
	meta, err := store.Head(context.Background(), ref)
	if err != nil || meta.SizeBytes != uint64(len(body)) {
		t.Fatalf("并发写入后对象事实错误: %+v %v", meta, err)
	}
}
