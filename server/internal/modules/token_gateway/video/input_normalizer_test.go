package video

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"
)

func TestReferenceImageNormalizerProducesPrivateMetadataFreeSnapshot(t *testing.T) {
	normalizer, err := NewReferenceImageNormalizer(ReferenceImageLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 1 << 20, MaxPixels: 1_000_000,
		MaxWidth: 2048, MaxHeight: 2048, MinAspectRatio: 0.25, MaxAspectRatio: 4,
		MaxEXIFBytes: 64 << 10, MaxICCBytes: 64 << 10, MaxDecodeDuration: 5 * time.Second, MaxTempDiskBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name string
		ext  string
		mime string
		raw  []byte
	}{
		{name: "png", ext: ".png", mime: "image/png", raw: encodeReferencePNG(t, 16, 9)},
		{name: "jpeg", ext: ".jpg", mime: "image/jpeg", raw: encodeReferenceJPEG(t, 16, 9)},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			result, normalizeErr := normalizer.Normalize(context.Background(), ReferenceImageInput{Filename: "reference" + fixture.ext, DeclaredMIME: fixture.mime, Body: bytes.NewReader(fixture.raw)})
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			if result.MIMEType != "image/png" || result.Width != 16 || result.Height != 9 || len(result.OriginalSHA256) != 64 || len(result.NormalizedSHA256) != 64 {
				t.Fatalf("规范化快照不完整: %+v", result)
			}
			if _, decodeErr := png.Decode(bytes.NewReader(result.Bytes)); decodeErr != nil {
				t.Fatalf("规范化副本必须是可解码PNG: %v", decodeErr)
			}
			if bytes.Contains(result.Bytes, []byte("Exif")) || bytes.Contains(result.Bytes, []byte("GPS")) || bytes.Contains(result.Bytes, []byte("XML")) {
				t.Fatal("规范化副本不得保留EXIF、GPS或XMP")
			}
		})
	}
}

func TestReferenceImageNormalizerRejectsUnsafeMatrix(t *testing.T) {
	normalizer, err := NewReferenceImageNormalizer(ReferenceImageLimits{
		MaxSourceBytes: 2048, MaxNormalizedBytes: 4096, MaxPixels: 100,
		MaxWidth: 64, MaxHeight: 64, MinAspectRatio: 0.5, MaxAspectRatio: 2,
		MaxEXIFBytes: 16, MaxICCBytes: 16, MaxDecodeDuration: 5 * time.Second, MaxTempDiskBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	validPNG := encodeReferencePNG(t, 10, 10)
	tests := []struct {
		name string
		in   ReferenceImageInput
		err  error
	}{
		{name: "svg", in: ReferenceImageInput{Filename: "a.svg", DeclaredMIME: "image/svg+xml", Body: bytes.NewReader([]byte("<svg></svg>"))}, err: ErrReferenceImageFormat},
		{name: "html", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader([]byte("<html></html>"))}, err: ErrReferenceImageFormat},
		{name: "animation", in: ReferenceImageInput{Filename: "a.gif", DeclaredMIME: "image/gif", Body: bytes.NewReader([]byte("GIF89a"))}, err: ErrReferenceImageAnimated},
		{name: "mime_mismatch", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/jpeg", Body: bytes.NewReader(validPNG)}, err: ErrReferenceImageMismatch},
		{name: "extension_mismatch", in: ReferenceImageInput{Filename: "a.jpg", DeclaredMIME: "image/png", Body: bytes.NewReader(validPNG)}, err: ErrReferenceImageMismatch},
		{name: "polyglot", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(append(append([]byte(nil), validPNG...), []byte("<html>")...))}, err: ErrReferenceImagePolyglot},
		{name: "truncated", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(validPNG[:20])}, err: ErrReferenceImageInvalid},
		{name: "gps", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(insertPNGChunk(t, validPNG, "tEXt", []byte("GPS=1")))}, err: ErrReferenceImageMetadata},
		{name: "animated_png", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(insertPNGChunk(t, validPNG, "acTL", make([]byte, 8)))}, err: ErrReferenceImageAnimated},
		{name: "oversized_icc", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(insertPNGChunk(t, validPNG, "iCCP", bytes.Repeat([]byte("x"), 17)))}, err: ErrReferenceImageMetadata},
		{name: "malformed_icc", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(insertPNGChunk(t, validPNG, "iCCP", []byte("bad")))}, err: ErrReferenceImageMetadata},
		{name: "pixel_bomb", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(encodeReferencePNG(t, 11, 10))}, err: ErrReferenceImagePixels},
		{name: "oversized_file", in: ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(bytes.Repeat([]byte("x"), 2049))}, err: ErrReferenceImageTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, normalizeErr := normalizer.Normalize(context.Background(), test.in); !errors.Is(normalizeErr, test.err) {
				t.Fatalf("错误分类不符合预期: want=%v got=%v", test.err, normalizeErr)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := normalizer.Normalize(cancelled, ReferenceImageInput{Filename: "a.png", DeclaredMIME: "image/png", Body: bytes.NewReader(validPNG)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CPU时间取消必须透传: %v", err)
	}
}

func TestReferenceImageNormalizerAppliesEXIFOrientation(t *testing.T) {
	normalizer, err := NewReferenceImageNormalizer(ReferenceImageLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 1 << 20, MaxPixels: 1_000_000,
		MaxWidth: 2048, MaxHeight: 2048, MinAspectRatio: 0.25, MaxAspectRatio: 4,
		MaxEXIFBytes: 64 << 10, MaxICCBytes: 64 << 10, MaxDecodeDuration: 5 * time.Second, MaxTempDiskBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := insertJPEGOrientation(t, encodeReferenceJPEG(t, 16, 9), 6)
	result, err := normalizer.Normalize(context.Background(), ReferenceImageInput{Filename: "oriented.jpg", DeclaredMIME: "image/jpeg", Body: bytes.NewReader(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 9 || result.Height != 16 {
		t.Fatalf("EXIF方向6必须顺时针旋转并交换尺寸: %+v", result)
	}
	if bytes.Contains(result.Bytes, []byte("Exif")) {
		t.Fatal("规范化副本不得保留EXIF")
	}
}

func TestReferenceImageNormalizerTimeoutInterruptsBlockingReader(t *testing.T) {
	normalizer, err := NewReferenceImageNormalizer(ReferenceImageLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 1 << 20, MaxPixels: 1_000_000,
		MaxWidth: 2048, MaxHeight: 2048, MinAspectRatio: 0.25, MaxAspectRatio: 4,
		MaxEXIFBytes: 64 << 10, MaxICCBytes: 64 << 10, MaxDecodeDuration: 20 * time.Millisecond, MaxTempDiskBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := &blockingReferenceReader{unblock: make(chan struct{}), done: make(chan struct{})}
	started := time.Now()
	_, normalizeErr := normalizer.Normalize(context.Background(), ReferenceImageInput{Filename: "slow.png", DeclaredMIME: "image/png", Body: reader})
	if !errors.Is(normalizeErr, context.DeadlineExceeded) || time.Since(started) > 300*time.Millisecond {
		t.Fatalf("阻塞读取必须被期限快速截断: err=%v elapsed=%v", normalizeErr, time.Since(started))
	}
	select {
	case <-reader.done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("超时返回前必须终止底层读取实体")
	}
}

func TestReferenceImageReadWorkersRemainBoundedWhenCloseDoesNotCooperate(t *testing.T) {
	readers := make([]*nonCooperativeReferenceReader, 0, referenceReadWorkerLimit)
	for index := 0; index < referenceReadWorkerLimit; index++ {
		reader := &nonCooperativeReferenceReader{unblock: make(chan struct{})}
		readers = append(readers, reader)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		_, err := readReferenceBytes(ctx, reader, 16)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("不合作Reader必须按期限返回: index=%d err=%v", index, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	_, err := readReferenceBytes(ctx, &nonCooperativeReferenceReader{unblock: make(chan struct{})}, 16)
	cancel()
	if !errors.Is(err, ErrReferenceImageBusy) || len(referenceReadWorkerSlots) != referenceReadWorkerLimit {
		t.Fatalf("工作槽耗尽后必须立即失败关闭且不再创建goroutine: err=%v active=%d", err, len(referenceReadWorkerSlots))
	}
	for _, reader := range readers {
		close(reader.unblock)
	}
	deadline := time.Now().Add(time.Second)
	for len(referenceReadWorkerSlots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(referenceReadWorkerSlots) != 0 {
		t.Fatal("测试解除阻塞后所有工作槽必须回收")
	}
}

func TestReferenceImageCPUWorkerIsKilledAtDecodeDeadline(t *testing.T) {
	normalizer, err := NewReferenceImageNormalizer(ReferenceImageLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 1 << 20, MaxPixels: 1_000_000,
		MaxWidth: 2048, MaxHeight: 2048, MinAspectRatio: 0.25, MaxAspectRatio: 4,
		MaxEXIFBytes: 64 << 10, MaxICCBytes: 64 << 10, MaxDecodeDuration: 150 * time.Millisecond, MaxTempDiskBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 子进程故意阻塞2秒，CommandContext必须在阶段deadline杀死并等待进程退出。
	normalizer.testHelperDelay = 2 * time.Second
	started := time.Now()
	_, normalizeErr := normalizer.Normalize(context.Background(), ReferenceImageInput{
		Filename: "slow-cpu.png", DeclaredMIME: "image/png", Body: bytes.NewReader(encodeReferencePNG(t, 16, 9)),
	})
	elapsed := time.Since(started)
	if !errors.Is(normalizeErr, context.DeadlineExceeded) || elapsed > time.Second {
		t.Fatalf("解码CPU子进程必须被deadline强制终止: err=%v elapsed=%v", normalizeErr, elapsed)
	}
	// 紧接着的正常任务必须成功，证明没有遗留占用同一执行实体。
	normalizer.testHelperDelay = 0
	normalizer.limits.MaxDecodeDuration = 2 * time.Second
	if _, err := normalizer.Normalize(context.Background(), ReferenceImageInput{
		Filename: "after-timeout.png", DeclaredMIME: "image/png", Body: bytes.NewReader(encodeReferencePNG(t, 16, 9)),
	}); err != nil {
		t.Fatalf("超时子进程回收后正常解码必须可用: %v", err)
	}
}

func TestReferenceJPEGRejectsTailWithFakeEOIAndAggregateMetadata(t *testing.T) {
	limits := ReferenceImageLimits{MaxEXIFBytes: 24, MaxICCBytes: 24}
	valid := encodeReferenceJPEG(t, 16, 9)
	polyglot := append(append([]byte(nil), valid...), []byte("<html>tail</html>")...)
	polyglot = append(polyglot, 0xff, 0xd9)
	if err := inspectReferenceJPEG(polyglot, limits); !errors.Is(err, ErrReferenceImagePolyglot) {
		t.Fatalf("真实EOI后的正文即使附加伪EOI也必须拒绝: %v", err)
	}
	withICC := insertJPEGSegment(t, valid, 0xe2, bytes.Repeat([]byte{0x31}, 13))
	withICC = insertJPEGSegment(t, withICC, 0xe2, bytes.Repeat([]byte{0x32}, 13))
	if err := inspectReferenceJPEG(withICC, limits); !errors.Is(err, ErrReferenceImageMetadata) {
		t.Fatalf("多个ICC段必须按总量限制: %v", err)
	}
}

type blockingReferenceReader struct {
	unblock chan struct{}
	done    chan struct{}
	once    sync.Once
}

type nonCooperativeReferenceReader struct{ unblock chan struct{} }

func (r *nonCooperativeReferenceReader) Read(_ []byte) (int, error) {
	<-r.unblock
	return 0, io.EOF
}

func (r *blockingReferenceReader) Read(_ []byte) (int, error) {
	<-r.unblock
	close(r.done)
	return 0, io.EOF
}

func (r *blockingReferenceReader) Close() error {
	r.once.Do(func() { close(r.unblock) })
	return nil
}

func insertPNGChunk(t *testing.T, raw []byte, chunkType string, data []byte) []byte {
	t.Helper()
	if len(raw) < 33 {
		t.Fatal("PNG fixture过短")
	}
	ihdrLength := int(binary.BigEndian.Uint32(raw[8:12]))
	insertAt := 8 + 12 + ihdrLength
	var chunk bytes.Buffer
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	chunk.Write(length[:])
	chunk.WriteString(chunkType)
	chunk.Write(data)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write([]byte(chunkType))
	_, _ = checksum.Write(data)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], checksum.Sum32())
	chunk.Write(crc[:])
	result := append([]byte(nil), raw[:insertAt]...)
	result = append(result, chunk.Bytes()...)
	result = append(result, raw[insertAt:]...)
	return result
}

func insertJPEGOrientation(t *testing.T, raw []byte, orientation uint16) []byte {
	t.Helper()
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		t.Fatal("JPEG fixture无效")
	}
	exif := make([]byte, 32)
	copy(exif[:6], []byte{'E', 'x', 'i', 'f', 0, 0})
	copy(exif[6:10], []byte{'I', 'I', 42, 0})
	binary.LittleEndian.PutUint32(exif[10:14], 8)
	binary.LittleEndian.PutUint16(exif[14:16], 1)
	binary.LittleEndian.PutUint16(exif[16:18], 0x0112)
	binary.LittleEndian.PutUint16(exif[18:20], 3)
	binary.LittleEndian.PutUint32(exif[20:24], 1)
	binary.LittleEndian.PutUint16(exif[24:26], orientation)
	segment := make([]byte, 4+len(exif))
	segment[0], segment[1] = 0xff, 0xe1
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(exif)+2))
	copy(segment[4:], exif)
	result := append([]byte(nil), raw[:2]...)
	result = append(result, segment...)
	result = append(result, raw[2:]...)
	return result
}

func insertJPEGSegment(t *testing.T, raw []byte, marker byte, payload []byte) []byte {
	t.Helper()
	if len(raw) < 4 || len(payload) > 65533 {
		t.Fatal("JPEG段fixture无效")
	}
	segment := make([]byte, 4+len(payload))
	segment[0], segment[1] = 0xff, marker
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	copy(segment[4:], payload)
	result := append([]byte(nil), raw[:2]...)
	result = append(result, segment...)
	return append(result, raw[2:]...)
}

func encodeReferencePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: uint8(x + 1), G: uint8(y + 1), B: 80, A: 255})
		}
	}
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func encodeReferenceJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: 120, G: uint8(x + y), B: 20, A: 255})
		}
	}
	if err := jpeg.Encode(&output, canvas, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
