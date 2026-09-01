package video

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
)

// 转存只接受服务端给定的独立目标；同目标幂等，不能覆盖不同正文或复活已删除对象。
func TestVideoG6SavedCopyImmutable(t *testing.T) {
	ctx := context.Background()
	s := NewFakeVideoObjectStore()
	source, err := s.Put(ctx, PutVideoObjectRequest{Zone: VideoObjectTemporary, TaskID: "task-save-fixture", AssetID: "asset-save-fixture", Role: "content", Body: bytes.NewReader([]byte("synthetic-video-body")), MaxBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	target := VideoObjectRef{Bucket: "ai-user-assets", ObjectKey: "saved/frozen-content/content.bin"}
	first, err := s.CopyImmutable(ctx, source.Ref, target, source.SHA256, source.SizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CopyImmutable(ctx, source.Ref, target, source.SHA256, source.SizeBytes)
	if err != nil || first != second {
		t.Fatal("同一原内容与目标必须幂等")
	}
	if _, err := s.CopyImmutable(ctx, source.Ref, target, "0000000000000000000000000000000000000000000000000000000000000000", source.SizeBytes); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatal("错误hash不能复制或覆盖")
	}
	if _, err := s.CopyImmutable(ctx, source.Ref, source.Ref, source.SHA256, source.SizeBytes); !errors.Is(err, ErrVideoObjectInvalid) {
		t.Fatal("长期目标必须独立于原临时对象")
	}
	deletedTarget := VideoObjectRef{Bucket: "ai-user-assets", ObjectKey: "saved/deleted-target/content.bin"}
	if _, err := s.CopyImmutable(ctx, source.Ref, deletedTarget, source.SHA256, source.SizeBytes); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, deletedTarget); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(ctx, source.Ref); err != nil {
		t.Fatal("墓碑反例必须保持源对象存在")
	}
	if _, err := s.CopyImmutable(ctx, source.Ref, deletedTarget, source.SHA256, source.SizeBytes); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatal("源仍存在时也不能复活目标墓碑")
	}
	if err := s.Delete(ctx, source.Ref); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRange(ctx, target, 0, int64(first.SizeBytes))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(body) != "synthetic-video-body" {
		t.Fatal("原临时正文删除不能破坏长期副本")
	}
	if err := s.Delete(ctx, target); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CopyImmutable(ctx, source.Ref, target, source.SHA256, source.SizeBytes); err == nil {
		t.Fatal("复制不得复活已删除目标")
	}
}

// 百并发请求共享同一长期对象；另一个来源不得用相同目标替换已确认的字节。
func TestVideoG6SavedCopyConcurrent(t *testing.T) {
	ctx := context.Background()
	s := NewFakeVideoObjectStore()
	put := func(id, body string) StoredVideoObject {
		t.Helper()
		v, err := s.Put(ctx, PutVideoObjectRequest{Zone: VideoObjectResult, TaskID: id, AssetID: id, Role: "content", Body: bytes.NewBufferString(body), MaxBytes: 64})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	source := put("source-one", "first-content")
	other := put("source-two", "other-content")
	target := VideoObjectRef{Bucket: "ai-user-assets", ObjectKey: "save-operation/source-one/content.bin"}
	start := make(chan struct{})
	results := make(chan StoredVideoObject, 100)
	failures := make(chan error, 100)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, err := s.CopyImmutable(ctx, source.Ref, target, source.SHA256, source.SizeBytes)
			if err != nil {
				failures <- err
				return
			}
			results <- v
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	if len(failures) != 0 || len(results) != 100 {
		t.Fatal("百并发复制必须全部幂等收敛")
	}
	first := <-results
	for result := range results {
		if result != first {
			t.Fatal("同一长期对象必须保持首次创建事实")
		}
	}
	if _, err := s.CopyImmutable(ctx, other.Ref, target, other.SHA256, other.SizeBytes); !errors.Is(err, ErrVideoObjectConflict) {
		t.Fatal("冲突来源不能覆盖已保存内容")
	}
	meta, err := s.Head(ctx, target)
	if err != nil || meta != first {
		t.Fatal("冲突返回后仍须保持原目标hash和元数据")
	}
	r, err := s.GetRange(ctx, target, 0, int64(first.SizeBytes))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(body) != "first-content" {
		t.Fatal("冲突不能先覆盖正文再返回错误")
	}
	if _, err := s.CopyImmutable(ctx, source.Ref, VideoObjectRef{Bucket: "untrusted", ObjectKey: target.ObjectKey}, source.SHA256, source.SizeBytes); !errors.Is(err, ErrVideoObjectInvalid) {
		t.Fatal("禁止向非长期白名单区域复制")
	}
}
