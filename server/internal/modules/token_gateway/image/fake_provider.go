package image

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"sync"
)

type FakeImageMode string

const (
	FakeImageSuccess      FakeImageMode = "success"
	FakeImagePartial      FakeImageMode = "partial"
	FakeImageFailed       FakeImageMode = "failed"
	FakeImageTimeout      FakeImageMode = "timeout"
	FakeImageDisconnected FakeImageMode = "disconnected"
	FakeImageUnknown      FakeImageMode = "unknown"
	FakeImageCorrupt      FakeImageMode = "corrupt"
)

// FakeImageAdapter 生成内存PNG或固定故障，不访问网络，也不产生Provider费用。
type FakeImageAdapter struct {
	mu    sync.Mutex
	mode  FakeImageMode
	calls int
}

func NewFakeImageAdapter(mode FakeImageMode) *FakeImageAdapter {
	return &FakeImageAdapter{mode: mode}
}

func (a *FakeImageAdapter) Name() string { return "fake" }

func (a *FakeImageAdapter) Generate(ctx context.Context, request ProviderImageRequest) (ProviderImageResult, error) {
	a.mu.Lock()
	a.calls++
	mode := a.mode
	a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ProviderImageResult{}, ErrProviderDisconnected
	}
	switch mode {
	case FakeImageFailed:
		return ProviderImageResult{}, ErrProviderFailed
	case FakeImageTimeout:
		return ProviderImageResult{}, ErrProviderTimeout
	case FakeImageDisconnected:
		return ProviderImageResult{}, ErrProviderDisconnected
	case FakeImageUnknown:
		return ProviderImageResult{ProviderRequestID: "fake-unknown", ResultUnknown: true}, ErrProviderUnknown
	case FakeImageCorrupt:
		return ProviderImageResult{ProviderRequestID: "fake-corrupt", Images: []ProviderImage{{Index: 0, Base64: base64.StdEncoding.EncodeToString([]byte("not-an-image")), MediaType: "image/png"}}}, nil
	}
	count := request.Count
	if count == 0 {
		count = 1
	}
	images := make([]ProviderImage, 0, count)
	for index := uint64(0); index < count; index++ {
		if mode == FakeImagePartial && index == count-1 {
			images = append(images, ProviderImage{Index: index, Base64: base64.StdEncoding.EncodeToString([]byte("corrupt-partial")), MediaType: "image/png"})
			continue
		}
		raw, err := fakePNG(index)
		if err != nil {
			return ProviderImageResult{}, err
		}
		images = append(images, ProviderImage{Index: index, Base64: base64.StdEncoding.EncodeToString(raw), MediaType: "image/png"})
	}
	return ProviderImageResult{ProviderRequestID: "fake-success", Images: images}, nil
}

func (a *FakeImageAdapter) Calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func fakePNG(index uint64) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	shade := uint8(40 + index%180)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: shade, G: uint8(x * 3), B: uint8(y * 3), A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
