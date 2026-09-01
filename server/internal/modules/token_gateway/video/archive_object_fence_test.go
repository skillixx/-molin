package video

import (
	"bytes"
	"context"
	"io"
	"testing"
)

type archiveBlockedReader struct {
	entered chan struct{}
	release chan struct{}
	read    bool
}

func (r *archiveBlockedReader) Read(b []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	close(r.entered)
	<-r.release
	return copy(b, []byte("old-body")), nil
}

func TestVideoG6ArchiveObjectFence(t *testing.T) {
	s := NewFakeVideoObjectStore()
	ctx := context.Background()
	r := &archiveBlockedReader{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := s.Put(ctx, PutVideoObjectRequest{Zone: VideoObjectTemporary, TaskID: "video_fence", AssetID: "vasset_fence", Role: "content", Body: r, MaxBytes: 100})
		done <- err
	}()
	<-r.entered
	if err := s.AdvanceArchiveFence(ctx, "video_fence", 1); err != nil {
		t.Fatal(err)
	}
	close(r.release)
	if err := <-done; err == nil {
		t.Fatal("读体期间被新代次接管的旧写入不得提交")
	}
	one := WithArchiveWriteGeneration(ctx, "video_fence", 1)
	asset, err := s.Put(one, PutVideoObjectRequest{Zone: VideoObjectTemporary, TaskID: "video_fence", AssetID: "vasset_fence", Role: "content", Body: bytes.NewBufferString("new-body"), MaxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PromoteToResult(ctx, asset.Ref); err == nil {
		t.Fatal("无新代次证明的旧Worker不能提升对象")
	}
	if err := s.AdvanceArchiveFence(ctx, "video_fence", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveToQuarantine(one, asset.Ref); err == nil {
		t.Fatal("旧归档代次不能移动新代次的对象")
	}
	if err := s.Delete(one, asset.Ref); err == nil {
		t.Fatal("旧归档代次不能删除对象")
	}
	two := WithArchiveWriteGeneration(ctx, "video_fence", 2)
	if _, err := s.PromoteToResult(two, asset.Ref); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceArchiveFence(ctx, "video_fence", 1); err == nil {
		t.Fatal("存储代次不可回退")
	}
}
