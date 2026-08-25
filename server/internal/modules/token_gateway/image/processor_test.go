package image

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

type staticResultFetcher struct {
	result FetchedResult
	err    error
}

func (f staticResultFetcher) Fetch(context.Context, string, int64) (FetchedResult, error) {
	return f.result, f.err
}

func TestImageProcessorNormalizesPNGJPEGAndWebP(t *testing.T) {
	processor := mustImageProcessor(t, ImageProcessingLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 2 << 20, MaxPixels: 1000000,
		MaxWidth: 1000, MaxHeight: 1000, ThumbnailMaxEdge: 32,
	})
	pngRaw := testPNG(t, 64, 64)
	pngWithMetadata, err := addPNGTextLabels(pngRaw, "secret-original-metadata")
	if err != nil {
		t.Fatal(err)
	}
	jpegRaw := testJPEG(t, 64, 64)
	// WebP测试夹具使用十六进制保存，避免把图片Base64正文写入源码或Git历史。
	webpRaw, err := hex.DecodeString("52494646b2010000574542505650384ca50100002f4ac018000f30fff33ffff31f7890246d7bda486ee6f10dc67d848125e930433b66fc8719960c279962269f604aeda16606d9d58abeaaffff153a4144ff19b86da4c8bbc738f00ac4a3af81df314a6259f7a6a0a5482297d1b7a015301714e2d71d2c85f1c08d719106e0ecb0b80e0a5557c90a202b53b18080923cfa524ffce28c4ff7c10237af83571807b615905b9681ada5c8f8b92341c5cb9613a56207834459a649e24555bda1d1c028ec28b16b8e19dc48ca7d8ebda083be183fc1ee93c1a74f04f6ea055e7c32c2e6309f3266739693c491cf837e428c8f2fe3276a6cccbdc135ac7344afdd45f462993d551c4bdc3b3e1847dfab2e07da8f7986ffa0b93a72e4e2274c0e2b79b987570a8d6e8455909830aeddc5c28205d80ff4790aafd82400ed8ff0629919655d2006ad41afb5203a6deaaca8ad5c1dcb4d71756f0991f93ac63117995410f8741d16be8e2a120ddf87575aad3ed2aafa10948279e54b1fdfa0bc64cbcaa33ae4f438e228739535f140a8ca6c0bec857822afb2e297dc382f66ef3327268d072a5da3023ba065636f22f8538bcdb7c8d6f12ac40868b6870000")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		raw       []byte
		mediaType string
	}{
		{name: "png", raw: pngWithMetadata, mediaType: "image/png"},
		{name: "jpeg", raw: jpegRaw, mediaType: "image/jpeg"},
		{name: "webp", raw: webpRaw, mediaType: "image/webp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := ProviderImage{Index: 0, Base64: base64.StdEncoding.EncodeToString(tc.raw), MediaType: tc.mediaType}
			normalized, err := processor.ProcessProviderImage(context.Background(), input, "content-123")
			if err != nil {
				t.Fatal(err)
			}
			if normalized.Format != "png" || normalized.MIMEType != "image/png" || normalized.Width <= 0 || normalized.Height <= 0 ||
				!normalized.ExplicitLabelApplied || !normalized.ImplicitLabelApplied || len(normalized.SHA256) != 64 {
				t.Fatalf("归一化结果错误: %+v", normalized)
			}
			if !bytes.Contains(normalized.Bytes, []byte("molin.ai.generated")) || !bytes.Contains(normalized.Bytes, []byte("molin.content_id")) {
				t.Fatal("归一化PNG缺少批准的隐式标识")
			}
			if bytes.Contains(normalized.Bytes, []byte("secret-original-metadata")) {
				t.Fatal("重编码后不得保留原始元数据")
			}
			decoded, err := png.Decode(bytes.NewReader(normalized.Bytes))
			if err != nil {
				t.Fatal(err)
			}
			bottom := color.NRGBAModel.Convert(decoded.At(1, decoded.Bounds().Max.Y-2)).(color.NRGBA)
			if bottom.A == 0 {
				t.Fatal("显式标识区域未写入图片像素")
			}
			thumbnail, err := processor.CreateThumbnail(context.Background(), normalized, "content-123-thumbnail")
			if err != nil || thumbnail.Width > 32 || thumbnail.Height > 32 || !thumbnail.ExplicitLabelApplied || !thumbnail.ImplicitLabelApplied {
				t.Fatalf("缩略图归一化失败: %+v err=%v", thumbnail, err)
			}
		})
	}
}

func TestImageProcessorRejectsMIMEFormatsLimitsAndBombs(t *testing.T) {
	processor := mustImageProcessor(t, ImageProcessingLimits{
		MaxSourceBytes: 1024, MaxNormalizedBytes: 1 << 20, MaxPixels: 4096,
		MaxWidth: 100, MaxHeight: 100, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32,
	})
	pngRaw := testPNG(t, 64, 64)
	if _, err := processor.ProcessProviderImage(context.Background(), ProviderImage{Base64: base64.StdEncoding.EncodeToString(pngRaw), MediaType: "image/jpeg"}, "content"); err != ErrImageResultInvalid {
		t.Fatalf("MIME与签名不一致必须拒绝: %v", err)
	}
	for _, raw := range [][]byte{[]byte("<svg><script/></svg>"), []byte("<html></html>"), []byte("GIF89a")} {
		if _, err := processor.ProcessProviderImage(context.Background(), ProviderImage{Base64: base64.StdEncoding.EncodeToString(raw)}, "content"); err != ErrImageFormatDenied {
			t.Fatalf("脚本型或未批准格式必须拒绝: %v", err)
		}
	}
	large := testPNG(t, 100, 100)
	if _, err := processor.ProcessProviderImage(context.Background(), ProviderImage{Base64: base64.StdEncoding.EncodeToString(large), MediaType: "image/png"}, "content"); err != ErrImagePixelLimit {
		t.Fatalf("像素炸弹门禁必须拒绝: %v", err)
	}
	oversized := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 1025))
	if _, err := processor.ProcessProviderImage(context.Background(), ProviderImage{Base64: oversized}, "content"); err != ErrObjectTooLarge {
		t.Fatalf("Base64解码上限必须拒绝: %v", err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngRaw)
	if _, err := processor.ProcessProviderImage(context.Background(), ProviderImage{Base64: dataURL, MediaType: "image/jpeg"}, "content"); err != ErrImageResultInvalid {
		t.Fatalf("data URL媒体类型冲突必须拒绝: %v", err)
	}
}

func TestImageProcessorRejectsInvalidContentIdentifier(t *testing.T) {
	processor := mustImageProcessor(t, ImageProcessingLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 1 << 20, MaxPixels: 10000,
		MaxWidth: 100, MaxHeight: 100, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32,
	})
	raw := testPNG(t, 64, 64)
	_, err := processor.ProcessProviderImage(context.Background(), ProviderImage{Base64: base64.StdEncoding.EncodeToString(raw), MediaType: "image/png"}, strings.Repeat("x", 129))
	if err != ErrImageMetadata {
		t.Fatalf("过长内容编号必须拒绝: %v", err)
	}
}

func TestImageProcessorURLPathUsesBoundedFetcherAndMIMEValidation(t *testing.T) {
	raw := testPNG(t, 64, 64)
	processor, err := NewImageProcessor(ImageProcessingLimits{
		MaxSourceBytes: 1 << 20, MaxNormalizedBytes: 1 << 20, MaxPixels: 10000,
		MaxWidth: 100, MaxHeight: 100, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32,
	}, staticResultFetcher{result: FetchedResult{Bytes: raw, MediaType: "image/png"}})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := processor.ProcessProviderImage(context.Background(), ProviderImage{URL: "https://cdn.example.invalid/result"}, "content-url")
	if err != nil || normalized.MIMEType != "image/png" {
		t.Fatalf("URL结果处理失败: %+v err=%v", normalized, err)
	}
	processor.fetcher = staticResultFetcher{result: FetchedResult{Bytes: raw, MediaType: "image/jpeg"}}
	if _, err := processor.ProcessProviderImage(context.Background(), ProviderImage{URL: "https://cdn.example.invalid/result"}, "content-url"); err != ErrImageResultInvalid {
		t.Fatalf("URL响应MIME与签名不一致必须拒绝: %v", err)
	}
}

func mustImageProcessor(t *testing.T, limits ImageProcessingLimits) *ImageProcessor {
	t.Helper()
	processor, err := NewImageProcessor(limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	return processor
}

func testPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 100, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, canvas, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
