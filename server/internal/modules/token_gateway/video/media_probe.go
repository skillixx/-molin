package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strings"
	"time"
)

var (
	ErrMediaMIME          = errors.New("视频MIME无效")
	ErrMediaSourceUnsafe  = errors.New("视频来源不安全")
	ErrMediaContainer     = errors.New("视频容器损坏或不允许")
	ErrMediaResourceLimit = errors.New("视频资源超过限制")
	ErrMediaCodec         = errors.New("视频Codec不允许")
	ErrMediaRange         = errors.New("视频Range读取异常")
	ErrMediaInterrupted   = errors.New("视频流中途断开")
	ErrMediaProbeBusy     = errors.New("视频探测读取资源已达上限")
)

const mediaReadWorkerLimit = 4

var mediaReadWorkerSlots = make(chan struct{}, mediaReadWorkerLimit)

type VideoProbeLimits struct {
	MaxBytes           int64
	MaxBoxBytes        int64
	MaxDurationMillis  uint64
	MaxWidth           uint32
	MaxHeight          uint32
	MinFrameRate       uint32
	MaxFrameRate       uint32
	AllowedVideoCodecs map[string]bool
	AllowedAudioCodecs map[string]bool
	MaxProbeDuration   time.Duration
	MaxTopLevelBoxes   uint32
}

type VideoMediaMetadata struct {
	MIMEType          string
	Container         string
	Width             uint32
	Height            uint32
	DurationMillis    uint64
	FrameRate         uint32
	VideoCodec        string
	AudioCodec        string
	HasAudio          bool
	SizeBytes         uint64
	SHA256            string
	BytesRead         uint64
	PeakBufferedBytes uint64
}

type VideoMediaProbe struct {
	limits VideoProbeLimits
}

func NewVideoMediaProbe(limits VideoProbeLimits) *VideoMediaProbe {
	if limits.MaxProbeDuration <= 0 {
		limits.MaxProbeDuration = 5 * time.Second
	}
	if limits.MaxTopLevelBoxes == 0 {
		limits.MaxTopLevelBoxes = 16
	}
	return &VideoMediaProbe{limits: limits}
}

// Probe 只读取受控ReaderAt，先解析有限moov盒，再以固定缓冲区流式计算正文哈希。
func (p *VideoMediaProbe) Probe(ctx context.Context, content StreamContent) (VideoMediaMetadata, error) {
	if p == nil || content.ReaderAt == nil || content.SizeBytes <= 0 || p.limits.MaxBytes <= 0 || p.limits.MaxBoxBytes <= 0 {
		return VideoMediaMetadata{}, ErrMediaContainer
	}
	workCtx, cancel := context.WithTimeout(ctx, p.limits.MaxProbeDuration)
	defer cancel()
	if err := workCtx.Err(); err != nil {
		return VideoMediaMetadata{}, err
	}
	if content.Ref.MediaType != "video/mp4" {
		return VideoMediaMetadata{}, ErrMediaMIME
	}
	if content.RangeMode != "supported" {
		return VideoMediaMetadata{}, ErrMediaRange
	}
	if !strings.HasPrefix(content.Ref.ProviderTaskID, "taskUUID-") || strings.Contains(content.Ref.ProviderTaskID, "://") || !strings.HasPrefix(content.Ref.ContentID, "content-") {
		return VideoMediaMetadata{}, ErrMediaSourceUnsafe
	}
	if content.SizeBytes > p.limits.MaxBytes {
		return VideoMediaMetadata{}, ErrMediaResourceLimit
	}

	var moov []byte
	var foundFTYP, foundMDAT bool
	var boxCount uint32
	for offset := int64(0); offset < content.SizeBytes; {
		boxCount++
		if p.limits.MaxTopLevelBoxes == 0 || boxCount > p.limits.MaxTopLevelBoxes {
			return VideoMediaMetadata{}, ErrMediaResourceLimit
		}
		header := make([]byte, 8)
		if err := readExactAt(workCtx, content.ReaderAt, header, offset, content.CancelRead); err != nil {
			return VideoMediaMetadata{}, classifyMediaReadError(err)
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		boxType := string(header[4:8])
		if size < 8 || size > content.SizeBytes-offset {
			return VideoMediaMetadata{}, ErrMediaContainer
		}
		switch boxType {
		case "ftyp":
			if size < 16 || size > p.limits.MaxBoxBytes {
				return VideoMediaMetadata{}, ErrMediaContainer
			}
			payload := make([]byte, size-8)
			if err := readExactAt(workCtx, content.ReaderAt, payload, offset+8, content.CancelRead); err != nil {
				return VideoMediaMetadata{}, classifyMediaReadError(err)
			}
			brand := string(payload[:4])
			if brand != "isom" && brand != "mp42" {
				return VideoMediaMetadata{}, ErrMediaContainer
			}
			foundFTYP = true
		case "moov":
			if size > p.limits.MaxBoxBytes {
				return VideoMediaMetadata{}, ErrMediaResourceLimit
			}
			moov = make([]byte, size-8)
			if err := readExactAt(workCtx, content.ReaderAt, moov, offset+8, content.CancelRead); err != nil {
				return VideoMediaMetadata{}, classifyMediaReadError(err)
			}
		case "mdat":
			foundMDAT = true
		}
		offset += size
	}
	if !foundFTYP || !foundMDAT || len(moov) == 0 {
		return VideoMediaMetadata{}, ErrMediaContainer
	}
	parsed, err := parseMovieMetadata(moov)
	if err != nil {
		return VideoMediaMetadata{}, err
	}
	if parsed.Width == 0 || parsed.Height == 0 || parsed.Width > p.limits.MaxWidth || parsed.Height > p.limits.MaxHeight ||
		parsed.DurationMillis == 0 || parsed.DurationMillis > p.limits.MaxDurationMillis ||
		parsed.FrameRate < p.limits.MinFrameRate || parsed.FrameRate > p.limits.MaxFrameRate {
		return VideoMediaMetadata{}, ErrMediaResourceLimit
	}
	if !p.limits.AllowedVideoCodecs[parsed.VideoCodec] || (parsed.HasAudio && !p.limits.AllowedAudioCodecs[parsed.AudioCodec]) {
		return VideoMediaMetadata{}, ErrMediaCodec
	}
	if err := workCtx.Err(); err != nil {
		return VideoMediaMetadata{}, err
	}
	hasher := sha256.New()
	buffer := make([]byte, 64<<10)
	if int64(len(buffer)) > p.limits.MaxBoxBytes {
		buffer = make([]byte, p.limits.MaxBoxBytes)
	}
	for offset := int64(0); offset < content.SizeBytes; {
		remaining := content.SizeBytes - offset
		chunk := buffer
		if remaining < int64(len(chunk)) {
			chunk = buffer[:remaining]
		}
		if err := readExactAt(workCtx, content.ReaderAt, chunk, offset, content.CancelRead); err != nil {
			return VideoMediaMetadata{}, classifyMediaReadError(err)
		}
		_, _ = hasher.Write(chunk)
		offset += int64(len(chunk))
	}
	parsed.MIMEType = "video/mp4"
	parsed.Container = "mp4"
	parsed.SizeBytes = uint64(content.SizeBytes)
	parsed.SHA256 = hex.EncodeToString(hasher.Sum(nil))
	parsed.BytesRead = uint64(content.SizeBytes)
	parsed.PeakBufferedBytes = uint64(maxInt64(int64(len(moov)), int64(len(buffer))))
	return parsed, nil
}

type mediaReadResult struct {
	count int
	err   error
}

// readExactAt 使单次阻塞ReaderAt可被阶段上下文截断；受控ReaderAt实现仍应自行响应取消。
func readExactAt(ctx context.Context, reader io.ReaderAt, buffer []byte, offset int64, cancelRead func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case mediaReadWorkerSlots <- struct{}{}:
	case <-ctx.Done():
		return ErrMediaProbeBusy
	}
	result := make(chan mediaReadResult, 1)
	go func() {
		defer func() { <-mediaReadWorkerSlots }()
		count, err := reader.ReadAt(buffer, offset)
		result <- mediaReadResult{count: count, err: err}
	}()
	var count int
	var err error
	select {
	case <-ctx.Done():
		if cancelRead != nil {
			_ = cancelRead()
		}
		// 不信任取消函数一定合作；未退出任务继续占用固定工作槽，阻止资源无限增长。
		select {
		case <-result:
		case <-time.After(10 * time.Millisecond):
		}
		return ctx.Err()
	case completed := <-result:
		count, err = completed.count, completed.err
	}
	return validateMediaRead(count, len(buffer), err)
}

func validateMediaRead(count, expected int, err error) error {
	if count != expected {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func classifyMediaReadError(err error) error {
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return ErrMediaInterrupted
	}
	return err
}

func parseMovieMetadata(moov []byte) (VideoMediaMetadata, error) {
	var result VideoMediaMetadata
	mvhd, ok := findBox(moov, "mvhd")
	if !ok || len(mvhd) < 20 {
		return result, ErrMediaContainer
	}
	timescale := binary.BigEndian.Uint32(mvhd[12:16])
	duration := binary.BigEndian.Uint32(mvhd[16:20])
	if timescale == 0 || uint64(duration) > math.MaxUint64/1000 {
		return result, ErrMediaContainer
	}
	result.DurationMillis = uint64(duration) * 1000 / uint64(timescale)
	tracks := findBoxes(moov, "trak")
	for _, track := range tracks {
		handler := parseHandler(track)
		switch handler {
		case "vide":
			tkhd, exists := findBox(track, "tkhd")
			if !exists || len(tkhd) < 84 {
				return result, ErrMediaContainer
			}
			result.Width = binary.BigEndian.Uint32(tkhd[len(tkhd)-8:]) >> 16
			result.Height = binary.BigEndian.Uint32(tkhd[len(tkhd)-4:]) >> 16
			result.VideoCodec = parseSampleCodec(track)
			result.FrameRate = parseFrameRate(track, timescale)
		case "soun":
			result.HasAudio = true
			result.AudioCodec = parseSampleCodec(track)
		}
	}
	if result.VideoCodec == "" || result.FrameRate == 0 {
		return result, ErrMediaContainer
	}
	return result, nil
}

func parseHandler(track []byte) string {
	hdlr, ok := findBoxRecursive(track, "hdlr")
	if !ok || len(hdlr) < 12 {
		return ""
	}
	return string(hdlr[8:12])
}

func parseSampleCodec(track []byte) string {
	stsd, ok := findBoxRecursive(track, "stsd")
	if !ok || len(stsd) < 16 || binary.BigEndian.Uint32(stsd[4:8]) == 0 {
		return ""
	}
	return string(stsd[12:16])
}

func parseFrameRate(track []byte, timescale uint32) uint32 {
	stts, ok := findBoxRecursive(track, "stts")
	if !ok || len(stts) < 16 || binary.BigEndian.Uint32(stts[4:8]) == 0 {
		return 0
	}
	delta := binary.BigEndian.Uint32(stts[12:16])
	if delta == 0 {
		return 0
	}
	return timescale / delta
}

func findBox(data []byte, boxType string) ([]byte, bool) {
	boxes := findBoxes(data, boxType)
	if len(boxes) == 0 {
		return nil, false
	}
	return boxes[0], true
}

func findBoxes(data []byte, boxType string) [][]byte {
	var result [][]byte
	for offset := 0; offset+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		if size < 8 || size > len(data)-offset {
			return nil
		}
		if string(data[offset+4:offset+8]) == boxType {
			result = append(result, data[offset+8:offset+size])
		}
		offset += size
	}
	return result
}

func findBoxRecursive(data []byte, boxType string) ([]byte, bool) {
	if box, ok := findBox(data, boxType); ok {
		return box, true
	}
	for _, container := range []string{"mdia", "minf", "stbl"} {
		for _, child := range findBoxes(data, container) {
			if box, ok := findBoxRecursive(child, boxType); ok {
				return box, true
			}
		}
	}
	return nil, false
}

func buildFakeMP4Fixture(spec VideoSpec) []byte {
	const timescale uint32 = 24_000
	duration := spec.DurationSeconds * timescale
	frameDelta := timescale / maxUint32(spec.FrameRate, 1)
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:16], timescale)
	binary.BigEndian.PutUint32(mvhd[16:20], duration)

	videoTKHD := make([]byte, 84)
	binary.BigEndian.PutUint32(videoTKHD[76:80], spec.Width<<16)
	binary.BigEndian.PutUint32(videoTKHD[80:84], spec.Height<<16)
	videoTrack := makeTrack("vide", "avc1", videoTKHD, spec.DurationSeconds*spec.FrameRate, frameDelta)
	moovChildren := append(makeBox("mvhd", mvhd), videoTrack...)
	if spec.Audio {
		moovChildren = append(moovChildren, makeTrack("soun", "mp4a", make([]byte, 84), 0, 0)...)
	}
	ftyp := makeBox("ftyp", append([]byte("isom"), []byte{0, 0, 0, 1, 'i', 's', 'o', 'm', 'm', 'p', '4', '2'}...))
	moov := makeBox("moov", moovChildren)
	mdat := makeBox("mdat", bytes.Repeat([]byte{0x5a}, 1024))
	return append(append(ftyp, moov...), mdat...)
}

func makeTrack(handler, codec string, tkhd []byte, sampleCount, sampleDelta uint32) []byte {
	hdlr := make([]byte, 12)
	copy(hdlr[8:12], handler)
	stsd := make([]byte, 16)
	binary.BigEndian.PutUint32(stsd[4:8], 1)
	binary.BigEndian.PutUint32(stsd[8:12], 8)
	copy(stsd[12:16], codec)
	stts := make([]byte, 16)
	if handler == "vide" {
		binary.BigEndian.PutUint32(stts[4:8], 1)
		binary.BigEndian.PutUint32(stts[8:12], sampleCount)
		binary.BigEndian.PutUint32(stts[12:16], sampleDelta)
	}
	stbl := makeBox("stbl", append(makeBox("stsd", stsd), makeBox("stts", stts)...))
	minf := makeBox("minf", stbl)
	mdia := makeBox("mdia", append(makeBox("hdlr", hdlr), minf...))
	return makeBox("trak", append(makeBox("tkhd", tkhd), mdia...))
}

func makeBox(boxType string, payload []byte) []byte {
	result := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(result[:4], uint32(len(result)))
	copy(result[4:8], boxType)
	copy(result[8:], payload)
	return result
}

func maxUint32(left, right uint32) uint32 {
	if left > right {
		return left
	}
	return right
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
