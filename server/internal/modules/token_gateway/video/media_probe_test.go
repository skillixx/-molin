package video

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVideoMediaProbeValidatesStreamingMP4(t *testing.T) {
	adapter := NewFakeAsyncVideoAdapter(FakeVideoSuccess)
	submitted, err := adapter.Submit(context.Background(), SubmitRequest{
		RequestID: "vid_req_probe", Operation: OperationTextToVideo, Prompt: "测试",
		Spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24, Audio: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = adapter.Query(context.Background(), QueryRequest{ProviderTaskID: submitted.ProviderTaskID})
	query, err := adapter.Query(context.Background(), QueryRequest{ProviderTaskID: submitted.ProviderTaskID})
	if err != nil {
		t.Fatal(err)
	}
	content, err := adapter.OpenContent(context.Background(), *query.Content)
	if err != nil {
		t.Fatal(err)
	}
	probe := NewVideoMediaProbe(defaultVideoProbeLimits())
	result, err := probe.Probe(context.Background(), content)
	if err != nil {
		t.Fatal(err)
	}
	if result.Container != "mp4" || result.MIMEType != "video/mp4" || result.Width != 1280 || result.Height != 720 ||
		result.DurationMillis != 5000 || result.FrameRate != 24 || result.VideoCodec != "avc1" || result.AudioCodec != "mp4a" || !result.HasAudio || len(result.SHA256) != 64 {
		t.Fatalf("MP4低敏规格不完整: %+v", result)
	}
	if result.BytesRead != uint64(content.SizeBytes) || result.PeakBufferedBytes > uint64(defaultVideoProbeLimits().MaxBoxBytes) {
		t.Fatalf("探测必须流式且有界: %+v", result)
	}
}

func TestVideoMediaProbeRejectsUnsafeMatrix(t *testing.T) {
	limits := defaultVideoProbeLimits()
	probe := NewVideoMediaProbe(limits)
	valid := buildFakeMP4Fixture(VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24})
	tests := []struct {
		name    string
		content StreamContent
		err     error
	}{
		{name: "mime_spoof", content: StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-safe", ContentID: "content-safe", MediaType: "text/html"}, SizeBytes: int64(len(valid)), ReaderAt: bytes.NewReader(valid), RangeMode: "supported"}, err: ErrMediaMIME},
		{name: "external_reference", content: StreamContent{Ref: ControlledContentRef{ProviderTaskID: "https://127.0.0.1/video", ContentID: "content-safe", MediaType: "video/mp4"}, SizeBytes: int64(len(valid)), ReaderAt: bytes.NewReader(valid), RangeMode: "supported"}, err: ErrMediaSourceUnsafe},
		{name: "corrupt_http_200", content: StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-corrupt", ContentID: "content-corrupt", MediaType: "video/mp4"}, SizeBytes: 16, ReaderAt: bytes.NewReader([]byte("corrupt-http-200")), RangeMode: "supported"}, err: ErrMediaContainer},
		{name: "oversized", content: StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-large", ContentID: "content-large", MediaType: "video/mp4"}, SizeBytes: limits.MaxBytes + 1, ReaderAt: bytes.NewReader(valid), RangeMode: "supported"}, err: ErrMediaResourceLimit},
		{name: "interrupted", content: StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-cut", ContentID: "content-cut", MediaType: "video/mp4"}, SizeBytes: int64(len(valid)), ReaderAt: failingReaderAt{reader: bytes.NewReader(valid), failAfter: 32}, RangeMode: "supported"}, err: ErrMediaInterrupted},
		{name: "range_invalid", content: StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-range", ContentID: "content-range", MediaType: "video/mp4"}, SizeBytes: int64(len(valid)), ReaderAt: bytes.NewReader(valid), RangeMode: "invalid"}, err: ErrMediaRange},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := probe.Probe(context.Background(), test.content); !errors.Is(err, test.err) {
				t.Fatalf("错误分类不符合预期: want=%v got=%v", test.err, err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := probe.Probe(cancelled, StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-timeout", ContentID: "content-timeout", MediaType: "video/mp4"}, SizeBytes: int64(len(valid)), ReaderAt: bytes.NewReader(valid), RangeMode: "supported"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("超时或取消必须透传: %v", err)
	}
}

func TestVideoMediaProbeRejectsCodecDurationAndFrameRate(t *testing.T) {
	tests := []struct {
		name string
		spec VideoSpec
		edit func(VideoProbeLimits) VideoProbeLimits
	}{
		{name: "duration", spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 61, FrameRate: 24}},
		{name: "frame_rate", spec: VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 121}},
		{name: "dimensions", spec: VideoSpec{Width: 8192, Height: 720, DurationSeconds: 5, FrameRate: 24}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := buildFakeMP4Fixture(test.spec)
			content := StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-" + strings.ReplaceAll(test.name, "_", ""), ContentID: "content-safe", MediaType: "video/mp4"}, SizeBytes: int64(len(raw)), ReaderAt: bytes.NewReader(raw), RangeMode: "supported"}
			if _, err := NewVideoMediaProbe(defaultVideoProbeLimits()).Probe(context.Background(), content); !errors.Is(err, ErrMediaResourceLimit) {
				t.Fatalf("资源限制必须失败关闭: %v", err)
			}
		})
	}
	deniedCodec := bytes.Replace(buildFakeMP4Fixture(VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24}), []byte("avc1"), []byte("vp09"), 1)
	content := StreamContent{Ref: ControlledContentRef{ProviderTaskID: "taskUUID-codec", ContentID: "content-codec", MediaType: "video/mp4"}, SizeBytes: int64(len(deniedCodec)), ReaderAt: bytes.NewReader(deniedCodec), RangeMode: "supported"}
	if _, err := NewVideoMediaProbe(defaultVideoProbeLimits()).Probe(context.Background(), content); !errors.Is(err, ErrMediaCodec) {
		t.Fatalf("非允许Codec必须拒绝: %v", err)
	}
}

func TestVideoMediaProbeTimeoutInterruptsBlockingRangeRead(t *testing.T) {
	raw := buildFakeMP4Fixture(VideoSpec{Width: 1280, Height: 720, DurationSeconds: 5, FrameRate: 24})
	reader := &blockingReaderAt{raw: raw, unblock: make(chan struct{}), done: make(chan struct{})}
	limits := defaultVideoProbeLimits()
	limits.MaxProbeDuration = 20 * time.Millisecond
	started := time.Now()
	_, err := NewVideoMediaProbe(limits).Probe(context.Background(), StreamContent{
		Ref:       ControlledContentRef{ProviderTaskID: "taskUUID-slow", ContentID: "content-slow", MediaType: "video/mp4"},
		SizeBytes: int64(len(raw)), ReaderAt: reader, RangeMode: "supported", CancelRead: reader.Cancel,
	})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 300*time.Millisecond {
		t.Fatalf("阻塞Range读取必须被期限快速截断: err=%v elapsed=%v", err, time.Since(started))
	}
	select {
	case <-reader.done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("超时返回前必须终止底层Range读取实体")
	}
}

func TestMediaReadWorkersRemainBoundedWhenCancelDoesNotCooperate(t *testing.T) {
	readers := make([]*nonCooperativeReaderAt, 0, mediaReadWorkerLimit)
	for index := 0; index < mediaReadWorkerLimit; index++ {
		reader := &nonCooperativeReaderAt{unblock: make(chan struct{})}
		readers = append(readers, reader)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		err := readExactAt(ctx, reader, make([]byte, 8), 0, func() error { return nil })
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("不合作ReaderAt必须按期限返回: index=%d err=%v", index, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	err := readExactAt(ctx, &nonCooperativeReaderAt{unblock: make(chan struct{})}, make([]byte, 8), 0, nil)
	cancel()
	if !errors.Is(err, ErrMediaProbeBusy) || len(mediaReadWorkerSlots) != mediaReadWorkerLimit {
		t.Fatalf("探测工作槽耗尽后必须失败关闭且不再创建goroutine: err=%v active=%d", err, len(mediaReadWorkerSlots))
	}
	for _, reader := range readers {
		close(reader.unblock)
	}
	deadline := time.Now().Add(time.Second)
	for len(mediaReadWorkerSlots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(mediaReadWorkerSlots) != 0 {
		t.Fatal("测试解除阻塞后所有探测工作槽必须回收")
	}
}

type blockingReaderAt struct {
	raw     []byte
	unblock chan struct{}
	done    chan struct{}
	once    sync.Once
}

type nonCooperativeReaderAt struct{ unblock chan struct{} }

func (r *nonCooperativeReaderAt) ReadAt(_ []byte, _ int64) (int, error) {
	<-r.unblock
	return 0, io.EOF
}

func (r *blockingReaderAt) ReadAt(buffer []byte, offset int64) (int, error) {
	<-r.unblock
	r.once.Do(func() {})
	select {
	case <-r.done:
	default:
		close(r.done)
	}
	return bytes.NewReader(r.raw).ReadAt(buffer, offset)
}

func (r *blockingReaderAt) Cancel() error {
	r.once.Do(func() { close(r.unblock) })
	return nil
}

type failingReaderAt struct {
	reader    *bytes.Reader
	failAfter int64
}

func (r failingReaderAt) ReadAt(buffer []byte, offset int64) (int, error) {
	if offset >= r.failAfter {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(buffer)) > r.failAfter-offset {
		buffer = buffer[:r.failAfter-offset]
	}
	count, err := r.reader.ReadAt(buffer, offset)
	if int64(count)+offset >= r.failAfter {
		return count, io.ErrUnexpectedEOF
	}
	return count, err
}

func defaultVideoProbeLimits() VideoProbeLimits {
	return VideoProbeLimits{
		MaxBytes: 8 << 20, MaxBoxBytes: 1 << 20, MaxDurationMillis: 60_000,
		MaxWidth: 4096, MaxHeight: 4096, MinFrameRate: 1, MaxFrameRate: 120,
		AllowedVideoCodecs: map[string]bool{"avc1": true, "hvc1": true},
		AllowedAudioCodecs: map[string]bool{"mp4a": true},
		MaxProbeDuration:   time.Second, MaxTopLevelBoxes: 16,
	}
}
