package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	ErrReferenceImageInvalid  = errors.New("参考图无效")
	ErrReferenceImageFormat   = errors.New("参考图格式不允许")
	ErrReferenceImageMismatch = errors.New("参考图扩展名、MIME或魔数不一致")
	ErrReferenceImageTooLarge = errors.New("参考图超过大小限制")
	ErrReferenceImagePixels   = errors.New("参考图尺寸或像素超过限制")
	ErrReferenceImageAnimated = errors.New("参考图不允许动画")
	ErrReferenceImageMetadata = errors.New("参考图元数据不安全")
	ErrReferenceImagePolyglot = errors.New("参考图包含尾随或混合内容")
	ErrReferenceImageBusy     = errors.New("参考图安全处理资源已达上限")
)

const referenceReadWorkerLimit = 4

var referenceReadWorkerSlots = make(chan struct{}, referenceReadWorkerLimit)

const (
	referenceHelperFlag          = "--molin-video-reference-helper"
	referenceHelperExitInvalid   = 20
	referenceHelperExitPixels    = 21
	referenceHelperExitTooLarge  = 22
	referenceHelperExitArguments = 23
)

// init只在本可执行文件由Normalizer以专用参数重新启动时接管进程。
// 解码CPU工作因此位于可被CommandContext强制终止的子进程，而不是不可杀死的goroutine。
func init() {
	if len(os.Args) < 2 || os.Args[1] != referenceHelperFlag {
		return
	}
	os.Exit(runReferenceImageHelper(os.Args[2:], os.Stdin, os.Stdout))
}

type ReferenceImageLimits struct {
	MaxSourceBytes     int64
	MaxNormalizedBytes int64
	MaxPixels          uint64
	MaxWidth           int
	MaxHeight          int
	MinAspectRatio     float64
	MaxAspectRatio     float64
	MaxEXIFBytes       int
	MaxICCBytes        int
	MaxDecodeDuration  time.Duration
	MaxTempDiskBytes   int64
}

type ReferenceImageInput struct {
	Filename     string
	DeclaredMIME string
	Body         io.Reader
}

type NormalizedReferenceImage struct {
	Bytes            []byte `json:"-"`
	MIMEType         string
	Width            int
	Height           int
	SizeBytes        uint64
	OriginalSHA256   string
	NormalizedSHA256 string
	TempDiskBytes    uint64
}

type ReferenceImageNormalizer struct {
	limits          ReferenceImageLimits
	testHelperDelay time.Duration
}

func NewReferenceImageNormalizer(limits ReferenceImageLimits) (*ReferenceImageNormalizer, error) {
	if limits.MaxSourceBytes <= 0 || limits.MaxNormalizedBytes <= 0 || limits.MaxPixels == 0 || limits.MaxWidth <= 0 || limits.MaxHeight <= 0 ||
		limits.MinAspectRatio <= 0 || limits.MaxAspectRatio < limits.MinAspectRatio || limits.MaxEXIFBytes <= 0 || limits.MaxICCBytes <= 0 {
		return nil, ErrReferenceImageInvalid
	}
	if limits.MaxDecodeDuration <= 0 || limits.MaxTempDiskBytes <= 0 {
		return nil, ErrReferenceImageInvalid
	}
	return &ReferenceImageNormalizer{limits: limits}, nil
}

// Normalize 有界读取、完整解码并重编码为PNG；Provider只能获得该无元数据副本的资产引用。
func (n *ReferenceImageNormalizer) Normalize(ctx context.Context, input ReferenceImageInput) (NormalizedReferenceImage, error) {
	if n == nil || input.Body == nil {
		return NormalizedReferenceImage{}, ErrReferenceImageInvalid
	}
	workCtx, cancel := context.WithTimeout(ctx, n.limits.MaxDecodeDuration)
	defer cancel()
	if err := workCtx.Err(); err != nil {
		return NormalizedReferenceImage{}, err
	}
	raw, err := readReferenceBytes(workCtx, input.Body, n.limits.MaxSourceBytes+1)
	if err != nil {
		return NormalizedReferenceImage{}, err
	}
	if len(raw) == 0 || int64(len(raw)) > n.limits.MaxSourceBytes {
		return NormalizedReferenceImage{}, ErrReferenceImageTooLarge
	}
	format, mimeType, err := inspectReferenceContainer(raw, n.limits)
	if err != nil {
		return NormalizedReferenceImage{}, err
	}
	if !matchesReferenceDeclaration(input.Filename, input.DeclaredMIME, format, mimeType) {
		return NormalizedReferenceImage{}, ErrReferenceImageMismatch
	}
	orientation := uint16(1)
	if format == "jpeg" {
		orientation, _, err = referenceJPEGEXIF(raw)
		if err != nil {
			return NormalizedReferenceImage{}, ErrReferenceImageMetadata
		}
	}
	processed, err := n.decodeAndNormalize(workCtx, raw, format, orientation)
	if err != nil {
		return NormalizedReferenceImage{}, err
	}
	originalHash := sha256.Sum256(raw)
	normalizedHash := sha256.Sum256(processed.bytes)
	return NormalizedReferenceImage{
		Bytes: append([]byte(nil), processed.bytes...), MIMEType: "image/png", Width: processed.width, Height: processed.height,
		SizeBytes: uint64(len(processed.bytes)), OriginalSHA256: hex.EncodeToString(originalHash[:]), NormalizedSHA256: hex.EncodeToString(normalizedHash[:]),
	}, nil
}

type normalizedReferenceResult struct {
	bytes         []byte
	width, height int
	err           error
}

// readReferenceBytes 让阻塞Reader不能越过阶段CPU/IO期限；调用方Reader仍由调用方负责关闭。
func readReferenceBytes(ctx context.Context, body io.Reader, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case referenceReadWorkerSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ErrReferenceImageBusy
	}
	closer, cancellable := body.(io.ReadCloser)
	result := make(chan normalizedReferenceResult, 1)
	go func() {
		defer func() { <-referenceReadWorkerSlots }()
		raw, err := io.ReadAll(io.LimitReader(body, maxBytes))
		result <- normalizedReferenceResult{bytes: raw, err: err}
	}()
	select {
	case <-ctx.Done():
		if cancellable {
			_ = closer.Close()
		}
		// 不信任Close一定合作；未退出任务继续占用固定工作槽，阻止资源无限增长。
		select {
		case <-result:
		case <-time.After(10 * time.Millisecond):
		}
		return nil, ctx.Err()
	case completed := <-result:
		return completed.bytes, completed.err
	}
}

// decodeAndNormalize 在当前可执行文件的受控子进程中解码；deadline会杀死并回收整个CPU工作实体。
func (n *ReferenceImageNormalizer) decodeAndNormalize(ctx context.Context, raw []byte, format string, orientation uint16) (normalizedReferenceResult, error) {
	if err := ctx.Err(); err != nil {
		return normalizedReferenceResult{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return normalizedReferenceResult{}, ErrReferenceImageInvalid
	}
	arguments := []string{referenceHelperFlag, format, strconv.FormatUint(uint64(orientation), 10),
		strconv.Itoa(n.limits.MaxWidth), strconv.Itoa(n.limits.MaxHeight), strconv.FormatUint(n.limits.MaxPixels, 10),
		strconv.FormatInt(n.limits.MaxNormalizedBytes, 10), strconv.FormatInt(int64(n.testHelperDelay/time.Millisecond), 10)}
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Stdin = bytes.NewReader(raw)
	output, err := command.Output()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return normalizedReferenceResult{}, ctxErr
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			switch exitErr.ExitCode() {
			case referenceHelperExitPixels:
				return normalizedReferenceResult{}, ErrReferenceImagePixels
			case referenceHelperExitTooLarge:
				return normalizedReferenceResult{}, ErrReferenceImageTooLarge
			}
		}
		return normalizedReferenceResult{}, ErrReferenceImageInvalid
	}
	if int64(len(output)) > n.limits.MaxNormalizedBytes {
		return normalizedReferenceResult{}, ErrReferenceImageTooLarge
	}
	config, err := png.DecodeConfig(bytes.NewReader(output))
	if err != nil || !n.validDimensions(config.Width, config.Height) {
		return normalizedReferenceResult{}, ErrReferenceImageInvalid
	}
	return normalizedReferenceResult{bytes: append([]byte(nil), output...), width: config.Width, height: config.Height}, nil
}

func runReferenceImageHelper(arguments []string, input io.Reader, output io.Writer) int {
	if len(arguments) != 7 {
		return referenceHelperExitArguments
	}
	orientationValue, orientationErr := strconv.ParseUint(arguments[1], 10, 16)
	maxWidth, widthErr := strconv.Atoi(arguments[2])
	maxHeight, heightErr := strconv.Atoi(arguments[3])
	maxPixels, pixelsErr := strconv.ParseUint(arguments[4], 10, 64)
	maxOutput, outputErr := strconv.ParseInt(arguments[5], 10, 64)
	delayMillis, delayErr := strconv.ParseInt(arguments[6], 10, 64)
	if orientationErr != nil || widthErr != nil || heightErr != nil || pixelsErr != nil || outputErr != nil || delayErr != nil ||
		maxWidth <= 0 || maxHeight <= 0 || maxPixels == 0 || maxOutput <= 0 || delayMillis < 0 {
		return referenceHelperExitArguments
	}
	if delayMillis > 0 {
		time.Sleep(time.Duration(delayMillis) * time.Millisecond)
	}
	raw, err := io.ReadAll(input)
	if err != nil || len(raw) == 0 {
		return referenceHelperExitInvalid
	}
	config, decodedFormat, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || decodedFormat != arguments[0] || !validReferenceDimensions(config.Width, config.Height, maxWidth, maxHeight, maxPixels) {
		return referenceHelperExitPixels
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(raw))
	if err != nil || decodedFormat != arguments[0] {
		return referenceHelperExitInvalid
	}
	canvas := applyReferenceOrientation(decoded, uint16(orientationValue))
	var normalized bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&normalized, canvas); err != nil {
		return referenceHelperExitInvalid
	}
	if int64(normalized.Len()) > maxOutput {
		return referenceHelperExitTooLarge
	}
	if _, err := output.Write(normalized.Bytes()); err != nil {
		return referenceHelperExitInvalid
	}
	return 0
}

func (n *ReferenceImageNormalizer) validDimensions(width, height int) bool {
	return validReferenceDimensions(width, height, n.limits.MaxWidth, n.limits.MaxHeight, n.limits.MaxPixels) &&
		float64(width)/float64(height) >= n.limits.MinAspectRatio && float64(width)/float64(height) <= n.limits.MaxAspectRatio
}

func validReferenceDimensions(width, height, maxWidth, maxHeight int, maxPixels uint64) bool {
	if width <= 0 || height <= 0 || width > maxWidth || height > maxHeight {
		return false
	}
	if uint64(width) > math.MaxUint64/uint64(height) || uint64(width)*uint64(height) > maxPixels {
		return false
	}
	return true
}

func matchesReferenceDeclaration(filename, declaredMIME, format, detectedMIME string) bool {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))
	switch format {
	case "png":
		if extension != ".png" {
			return false
		}
	case "jpeg":
		if extension != ".jpg" && extension != ".jpeg" {
			return false
		}
	default:
		return false
	}
	return strings.EqualFold(strings.TrimSpace(declaredMIME), detectedMIME)
}

func inspectReferenceContainer(raw []byte, limits ReferenceImageLimits) (string, string, error) {
	switch {
	case len(raw) >= 8 && bytes.Equal(raw[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		if err := inspectReferencePNG(raw, limits); err != nil {
			return "", "", err
		}
		return "png", "image/png", nil
	case len(raw) >= 4 && raw[0] == 0xff && raw[1] == 0xd8:
		if err := inspectReferenceJPEG(raw, limits); err != nil {
			return "", "", err
		}
		return "jpeg", "image/jpeg", nil
	case bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<svg")) || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<html")) || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("<!DOCTYPE")):
		return "", "", ErrReferenceImageFormat
	case bytes.HasPrefix(raw, []byte("GIF87a")) || bytes.HasPrefix(raw, []byte("GIF89a")):
		return "", "", ErrReferenceImageAnimated
	default:
		return "", "", ErrReferenceImageFormat
	}
}

func inspectReferencePNG(raw []byte, limits ReferenceImageLimits) error {
	offset := 8
	for offset+12 <= len(raw) {
		length := int(binary.BigEndian.Uint32(raw[offset : offset+4]))
		if length < 0 || length > len(raw)-offset-12 {
			return ErrReferenceImageInvalid
		}
		chunkType := string(raw[offset+4 : offset+8])
		data := raw[offset+8 : offset+8+length]
		switch chunkType {
		case "acTL", "fcTL", "fdAT":
			return ErrReferenceImageAnimated
		case "eXIf", "tEXt", "zTXt", "iTXt":
			if length > limits.MaxEXIFBytes || containsLocationMetadata(data) {
				return ErrReferenceImageMetadata
			}
		case "iCCP":
			separator := bytes.IndexByte(data, 0)
			if length > limits.MaxICCBytes || separator <= 0 || separator > 79 || separator+2 >= len(data) || data[separator+1] != 0 {
				return ErrReferenceImageMetadata
			}
		case "IEND":
			if length != 0 {
				return ErrReferenceImageInvalid
			}
			offset += 12
			if offset != len(raw) {
				return ErrReferenceImagePolyglot
			}
			return nil
		}
		offset += 12 + length
	}
	return ErrReferenceImageInvalid
}

func inspectReferenceJPEG(raw []byte, limits ReferenceImageLimits) error {
	var exifBytes, iccBytes int
	err := walkJPEGSegments(raw, func(marker byte, data []byte) error {
		switch marker {
		case 0xe1:
			exifBytes += len(data)
			if exifBytes > limits.MaxEXIFBytes || containsLocationMetadata(data) {
				return ErrReferenceImageMetadata
			}
		case 0xe2:
			iccBytes += len(data)
			if iccBytes > limits.MaxICCBytes {
				return ErrReferenceImageMetadata
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	_, hasGPS, err := referenceJPEGEXIF(raw)
	if err != nil || hasGPS {
		return ErrReferenceImageMetadata
	}
	return nil
}

// walkJPEGSegments 解析所有扫描段，并要求真实EOI紧邻文件末尾，拒绝追加正文后伪造EOI的polyglot。
func walkJPEGSegments(raw []byte, visit func(marker byte, data []byte) error) error {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return ErrReferenceImageInvalid
	}
	for offset := 2; offset < len(raw); {
		if raw[offset] != 0xff {
			return ErrReferenceImageInvalid
		}
		markerStart := offset
		for offset < len(raw) && raw[offset] == 0xff {
			offset++
		}
		if offset >= len(raw) {
			return ErrReferenceImageInvalid
		}
		marker := raw[offset]
		offset++
		if marker == 0xd9 {
			if offset != len(raw) {
				return ErrReferenceImagePolyglot
			}
			return nil
		}
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if offset+2 > len(raw) {
			return ErrReferenceImageInvalid
		}
		length := int(binary.BigEndian.Uint16(raw[offset : offset+2]))
		if length < 2 || offset+length > len(raw) {
			return ErrReferenceImageInvalid
		}
		data := raw[offset+2 : offset+length]
		if visit != nil {
			if err := visit(marker, data); err != nil {
				return err
			}
		}
		offset += length
		if marker != 0xda {
			continue
		}
		// SOS之后跳过熵编码字节；0xFF00为转义，RST标记不结束扫描。
		for offset < len(raw) {
			if raw[offset] != 0xff {
				offset++
				continue
			}
			next := offset + 1
			for next < len(raw) && raw[next] == 0xff {
				next++
			}
			if next >= len(raw) {
				return ErrReferenceImageInvalid
			}
			code := raw[next]
			if code == 0x00 || (code >= 0xd0 && code <= 0xd7) {
				offset = next + 1
				continue
			}
			offset = markerStart + (offset - markerStart)
			break
		}
	}
	return ErrReferenceImageInvalid
}

func containsLocationMetadata(raw []byte) bool {
	lower := bytes.ToLower(raw)
	return bytes.Contains(lower, []byte("gps")) || bytes.Contains(lower, []byte("latitude")) || bytes.Contains(lower, []byte("longitude"))
}

// ValidateNormalizedReference 在Provider前重新证明私有参考图正文、MIME、尺寸和冻结SHA一致。
func ValidateNormalizedReference(ref ControlledInputRef, snapshot *NormalizedReferenceImage) error {
	if snapshot == nil || snapshot.MIMEType != "image/png" || len(snapshot.Bytes) == 0 ||
		snapshot.NormalizedSHA256 != ref.SHA256 || !lowerHexSHA256.MatchString(ref.SHA256) {
		return ErrVideoRequestInvalid
	}
	if err := inspectNormalizedPNG(snapshot.Bytes); err != nil {
		return err
	}
	config, err := png.DecodeConfig(bytes.NewReader(snapshot.Bytes))
	if err != nil || config.Width != snapshot.Width || config.Height != snapshot.Height {
		return ErrReferenceImageInvalid
	}
	if _, err := png.Decode(bytes.NewReader(snapshot.Bytes)); err != nil {
		return ErrReferenceImageInvalid
	}
	digest := sha256.Sum256(snapshot.Bytes)
	if hex.EncodeToString(digest[:]) != ref.SHA256 {
		return ErrVideoRequestInvalid
	}
	return nil
}

func inspectNormalizedPNG(raw []byte) error {
	if len(raw) < 20 || !bytes.Equal(raw[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		return ErrReferenceImageInvalid
	}
	for offset := 8; offset+12 <= len(raw); {
		length := int(binary.BigEndian.Uint32(raw[offset : offset+4]))
		if length < 0 || length > len(raw)-offset-12 {
			return ErrReferenceImageInvalid
		}
		chunkType := string(raw[offset+4 : offset+8])
		switch chunkType {
		case "eXIf", "tEXt", "zTXt", "iTXt", "iCCP", "acTL", "fcTL", "fdAT":
			return ErrReferenceImageMetadata
		case "IEND":
			if length != 0 || offset+12 != len(raw) {
				return ErrReferenceImagePolyglot
			}
			return nil
		}
		offset += 12 + length
	}
	return ErrReferenceImageInvalid
}

func referenceJPEGEXIF(raw []byte) (uint16, bool, error) {
	orientation := uint16(1)
	hasGPS := false
	err := walkJPEGSegments(raw, func(marker byte, data []byte) error {
		if marker == 0xe1 && len(data) >= 14 && bytes.Equal(data[:6], []byte{'E', 'x', 'i', 'f', 0, 0}) {
			tiff := data[6:]
			var order binary.ByteOrder
			switch string(tiff[:2]) {
			case "II":
				order = binary.LittleEndian
			case "MM":
				order = binary.BigEndian
			default:
				return ErrReferenceImageMetadata
			}
			if order.Uint16(tiff[2:4]) != 42 {
				return ErrReferenceImageMetadata
			}
			ifdOffset := int(order.Uint32(tiff[4:8]))
			if ifdOffset < 8 || ifdOffset+2 > len(tiff) {
				return ErrReferenceImageMetadata
			}
			count := int(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
			if count > 256 || ifdOffset+2+count*12 > len(tiff) {
				return ErrReferenceImageMetadata
			}
			for index := 0; index < count; index++ {
				entry := tiff[ifdOffset+2+index*12 : ifdOffset+2+(index+1)*12]
				tag := order.Uint16(entry[:2])
				switch tag {
				case 0x0112:
					if order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
						return ErrReferenceImageMetadata
					}
					orientation = order.Uint16(entry[8:10])
					if orientation < 1 || orientation > 8 {
						return ErrReferenceImageMetadata
					}
				case 0x8825:
					hasGPS = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, false, ErrReferenceImageMetadata
	}
	return orientation, hasGPS, nil
}

func applyReferenceOrientation(source image.Image, orientation uint16) *image.NRGBA {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	outputWidth, outputHeight := width, height
	if orientation >= 5 {
		outputWidth, outputHeight = height, width
	}
	output := image.NewNRGBA(image.Rect(0, 0, outputWidth, outputHeight))
	for y := 0; y < outputHeight; y++ {
		for x := 0; x < outputWidth; x++ {
			sourceX, sourceY := x, y
			switch orientation {
			case 2:
				sourceX = width - 1 - x
			case 3:
				sourceX, sourceY = width-1-x, height-1-y
			case 4:
				sourceY = height - 1 - y
			case 5:
				sourceX, sourceY = y, x
			case 6:
				sourceX, sourceY = y, height-1-x
			case 7:
				sourceX, sourceY = width-1-y, height-1-x
			case 8:
				sourceX, sourceY = width-1-y, x
			}
			output.Set(x, y, source.At(bounds.Min.X+sourceX, bounds.Min.Y+sourceY))
		}
	}
	return output
}

// 显式引用受控解码器，避免后续整理导入时破坏PNG/JPEG支持。
var (
	_ = jpeg.DefaultQuality
	_ = png.BestSpeed
)
