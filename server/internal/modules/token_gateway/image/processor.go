package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"mime"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/webp"
)

var (
	ErrImageResultInvalid = errors.New("图片结果无效")
	ErrImageFormatDenied  = errors.New("图片格式不允许")
	ErrImagePixelLimit    = errors.New("图片像素或尺寸超过限制")
	ErrImageMetadata      = errors.New("图片标识元数据写入失败")
)

type ImageProcessingLimits struct {
	MaxSourceBytes      int64
	MaxNormalizedBytes  int64
	MaxPixels           uint64
	MaxWidth            int
	MaxHeight           int
	ExpectedAspectRatio float64
	AspectTolerance     float64
	ThumbnailMaxEdge    int
}

type FetchedResult struct {
	Bytes     []byte
	MediaType string
}

type ResultFetcher interface {
	Fetch(ctx context.Context, rawURL string, maxBytes int64) (FetchedResult, error)
}

type NormalizedImage struct {
	Bytes                []byte `json:"-"`
	Format               string
	MIMEType             string
	Width                int
	Height               int
	SizeBytes            uint64
	SHA256               string
	ExplicitLabelApplied bool
	ImplicitLabelApplied bool
}

type ImageProcessor struct {
	limits  ImageProcessingLimits
	fetcher ResultFetcher
}

func NewImageProcessor(limits ImageProcessingLimits, fetcher ResultFetcher) (*ImageProcessor, error) {
	if limits.MaxSourceBytes <= 0 || limits.MaxNormalizedBytes <= 0 || limits.MaxPixels == 0 ||
		limits.MaxWidth <= 0 || limits.MaxHeight <= 0 || limits.ThumbnailMaxEdge <= 0 {
		return nil, ErrImageResultInvalid
	}
	return &ImageProcessor{limits: limits, fetcher: fetcher}, nil
}

// ProcessProviderImage 有界读取Provider URL或Base64，完整解码后统一重编码PNG，移除原始元数据并写入双标识。
func (p *ImageProcessor) ProcessProviderImage(ctx context.Context, input ProviderImage, contentID string) (NormalizedImage, error) {
	if p == nil || strings.TrimSpace(contentID) == "" || (input.URL == "") == (input.Base64 == "") {
		return NormalizedImage{}, ErrImageResultInvalid
	}
	var raw []byte
	declaredMedia := strings.TrimSpace(input.MediaType)
	if input.Base64 != "" {
		encoded, dataMedia, err := splitBase64Payload(input.Base64)
		if err != nil {
			return NormalizedImage{}, err
		}
		if declaredMedia == "" {
			declaredMedia = dataMedia
		} else if dataMedia != "" && !sameMediaType(declaredMedia, dataMedia) {
			return NormalizedImage{}, ErrImageResultInvalid
		}
		raw, err = decodeBase64Bounded(encoded, p.limits.MaxSourceBytes)
		if err != nil {
			return NormalizedImage{}, err
		}
	} else {
		if p.fetcher == nil {
			return NormalizedImage{}, ErrImageResultInvalid
		}
		fetched, err := p.fetcher.Fetch(ctx, input.URL, p.limits.MaxSourceBytes)
		if err != nil {
			return NormalizedImage{}, err
		}
		raw = fetched.Bytes
		if declaredMedia == "" {
			declaredMedia = fetched.MediaType
		} else if fetched.MediaType != "" && !sameMediaType(declaredMedia, fetched.MediaType) {
			return NormalizedImage{}, ErrImageResultInvalid
		}
	}
	return p.normalize(ctx, raw, declaredMedia, contentID, 0)
}

func (p *ImageProcessor) CreateThumbnail(ctx context.Context, source NormalizedImage, contentID string) (NormalizedImage, error) {
	if p == nil || len(source.Bytes) == 0 {
		return NormalizedImage{}, ErrImageResultInvalid
	}
	return p.normalize(ctx, source.Bytes, "image/png", contentID, p.limits.ThumbnailMaxEdge)
}

func (p *ImageProcessor) normalize(ctx context.Context, raw []byte, declaredMedia, contentID string, maxEdge int) (NormalizedImage, error) {
	if err := ctx.Err(); err != nil {
		return NormalizedImage{}, err
	}
	if len(raw) == 0 || int64(len(raw)) > p.limits.MaxSourceBytes {
		return NormalizedImage{}, ErrObjectTooLarge
	}
	format, detectedMedia, err := detectImageFormat(raw)
	if err != nil {
		return NormalizedImage{}, err
	}
	if declaredMedia != "" && !sameMediaType(declaredMedia, detectedMedia) {
		return NormalizedImage{}, ErrImageResultInvalid
	}
	config, err := decodeImageConfig(format, bytes.NewReader(raw))
	if err != nil || !p.validDimensions(config.Width, config.Height) {
		return NormalizedImage{}, ErrImagePixelLimit
	}
	decoded, err := decodeImage(format, bytes.NewReader(raw))
	if err != nil {
		return NormalizedImage{}, ErrImageResultInvalid
	}
	canvas := copyImage(decoded)
	if maxEdge > 0 && (canvas.Bounds().Dx() > maxEdge || canvas.Bounds().Dy() > maxEdge) {
		canvas = resizeNearest(canvas, maxEdge)
	}
	applyVisibleLabel(canvas)
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&encoded, canvas); err != nil {
		return NormalizedImage{}, ErrImageResultInvalid
	}
	labelled, err := addPNGTextLabels(encoded.Bytes(), contentID)
	if err != nil || int64(len(labelled)) > p.limits.MaxNormalizedBytes {
		if err != nil {
			return NormalizedImage{}, ErrImageMetadata
		}
		return NormalizedImage{}, ErrObjectTooLarge
	}
	if _, err := png.Decode(bytes.NewReader(labelled)); err != nil {
		return NormalizedImage{}, ErrImageMetadata
	}
	sum := sha256.Sum256(labelled)
	bounds := canvas.Bounds()
	return NormalizedImage{
		Bytes: labelled, Format: "png", MIMEType: "image/png", Width: bounds.Dx(), Height: bounds.Dy(),
		SizeBytes: uint64(len(labelled)), SHA256: hex.EncodeToString(sum[:]),
		ExplicitLabelApplied: true, ImplicitLabelApplied: true,
	}, nil
}

func (p *ImageProcessor) validDimensions(width, height int) bool {
	if width <= 0 || height <= 0 || width > p.limits.MaxWidth || height > p.limits.MaxHeight {
		return false
	}
	if uint64(width) > math.MaxUint64/uint64(height) || uint64(width)*uint64(height) > p.limits.MaxPixels {
		return false
	}
	if p.limits.ExpectedAspectRatio > 0 {
		ratio := float64(width) / float64(height)
		if math.Abs(ratio-p.limits.ExpectedAspectRatio) > p.limits.AspectTolerance {
			return false
		}
	}
	return true
}

func splitBase64Payload(raw string) (payload, mediaType string, err error) {
	if strings.HasPrefix(raw, "data:") {
		comma := strings.IndexByte(raw, ',')
		if comma <= 5 {
			return "", "", ErrImageResultInvalid
		}
		header := raw[5:comma]
		parts := strings.Split(header, ";")
		if len(parts) < 2 || parts[len(parts)-1] != "base64" {
			return "", "", ErrImageResultInvalid
		}
		return raw[comma+1:], parts[0], nil
	}
	return raw, "", nil
}

func decodeBase64Bounded(encoded string, maxBytes int64) ([]byte, error) {
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded))
	raw, err := io.ReadAll(io.LimitReader(decoder, maxBytes+1))
	if err != nil {
		return nil, ErrImageResultInvalid
	}
	if int64(len(raw)) > maxBytes {
		return nil, ErrObjectTooLarge
	}
	return raw, nil
}

func detectImageFormat(raw []byte) (format, mediaType string, err error) {
	switch {
	case len(raw) >= 8 && bytes.Equal(raw[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "png", "image/png", nil
	case len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff:
		return "jpeg", "image/jpeg", nil
	case len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP":
		return "webp", "image/webp", nil
	default:
		return "", "", ErrImageFormatDenied
	}
}

func decodeImageConfig(format string, reader io.Reader) (image.Config, error) {
	switch format {
	case "png":
		return png.DecodeConfig(reader)
	case "jpeg":
		return jpeg.DecodeConfig(reader)
	case "webp":
		return webp.DecodeConfig(reader)
	default:
		return image.Config{}, ErrImageFormatDenied
	}
}

func decodeImage(format string, reader io.Reader) (image.Image, error) {
	switch format {
	case "png":
		return png.Decode(reader)
	case "jpeg":
		return jpeg.Decode(reader)
	case "webp":
		return webp.Decode(reader)
	default:
		return nil, ErrImageFormatDenied
	}
}

func sameMediaType(left, right string) bool {
	leftType, _, leftErr := mime.ParseMediaType(left)
	rightType, _, rightErr := mime.ParseMediaType(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(leftType, rightType)
}

func copyImage(source image.Image) *image.NRGBA {
	bounds := source.Bounds()
	canvas := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(canvas, canvas.Bounds(), source, bounds.Min, draw.Src)
	return canvas
}

func resizeNearest(source *image.NRGBA, maxEdge int) *image.NRGBA {
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	scale := math.Min(float64(maxEdge)/float64(width), float64(maxEdge)/float64(height))
	newWidth := int(math.Max(1, math.Round(float64(width)*scale)))
	newHeight := int(math.Max(1, math.Round(float64(height)*scale)))
	result := image.NewNRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := 0; y < newHeight; y++ {
		sourceY := y * height / newHeight
		for x := 0; x < newWidth; x++ {
			sourceX := x * width / newWidth
			result.SetNRGBA(x, y, source.NRGBAAt(sourceX, sourceY))
		}
	}
	return result
}

func applyVisibleLabel(canvas *image.NRGBA) {
	width, height := canvas.Bounds().Dx(), canvas.Bounds().Dy()
	stripHeight := 22
	if height < 44 {
		stripHeight = max(13, height/2)
	}
	draw.Draw(canvas, image.Rect(0, height-stripHeight, width, height), &image.Uniform{C: color.NRGBA{A: 180}}, image.Point{}, draw.Over)
	drawer := font.Drawer{Dst: canvas, Src: image.NewUniform(color.White), Face: basicfont.Face7x13}
	drawer.Dot = fixed.P(4, height-5)
	label := "AI GENERATED"
	if width < 90 {
		label = "AI"
	}
	drawer.DrawString(label)
}

func addPNGTextLabels(raw []byte, contentID string) ([]byte, error) {
	if len(raw) < 33 || !bytes.Equal(raw[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) || string(raw[12:16]) != "IHDR" {
		return nil, ErrImageMetadata
	}
	if len(contentID) == 0 || len(contentID) > 128 {
		return nil, ErrImageMetadata
	}
	for _, character := range []byte(contentID) {
		if character < 0x20 || character > 0x7e {
			return nil, ErrImageMetadata
		}
	}
	ihdrLength := int(binary.BigEndian.Uint32(raw[8:12]))
	insertAt := 8 + 4 + 4 + ihdrLength + 4
	if insertAt > len(raw) {
		return nil, ErrImageMetadata
	}
	labels := [][2]string{
		{"molin.ai.generated", "1"},
		{"molin.service", "molin"},
		{"molin.content_id", contentID},
		{"molin.label_version", "1"},
	}
	var extra bytes.Buffer
	for _, label := range labels {
		if strings.ContainsRune(label[0], 0) || strings.ContainsRune(label[1], 0) || len(label[0]) == 0 || len(label[0]) > 79 {
			return nil, ErrImageMetadata
		}
		data := append(append([]byte(label[0]), 0), []byte(label[1])...)
		writePNGChunk(&extra, "tEXt", data)
	}
	result := make([]byte, 0, len(raw)+extra.Len())
	result = append(result, raw[:insertAt]...)
	result = append(result, extra.Bytes()...)
	result = append(result, raw[insertAt:]...)
	return result, nil
}

func writePNGChunk(writer io.Writer, chunkType string, data []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(chunkType))
	_, _ = writer.Write(data)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte(chunkType))
	_, _ = crc.Write(data)
	var checksum [4]byte
	binary.BigEndian.PutUint32(checksum[:], crc.Sum32())
	_, _ = writer.Write(checksum[:])
}
