package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"testing"

	video "molin/server/internal/modules/token_gateway/video"
)

// 本地FFmpeg合成夹具只编译进测试，生产二进制不会包含媒体或替代Provider。
//
//go:embed testdata/vid_g6_playable.mp4
var videoG6PlayableFixture []byte

// 只向外部测试包提供本地合成字节副本，防止测试修改共享嵌入数据。
func VideoPlayableFixtureBytes() []byte { return append([]byte(nil), videoG6PlayableFixture...) }

func TestVideoG6PlayableMP4Probe(t *testing.T) {
	if fmt.Sprintf("%x", sha256.Sum256(videoG6PlayableFixture)) != "17983e00fd03ae81974ea6e38f3c8d7c73b3518c5e02325e7c9e32fb564c7f0a" {
		t.Fatal("可播放夹具hash不匹配")
	}
	got, err := video.NewVideoMediaProbe(videoG4TestProbeLimits()).Probe(context.Background(), video.StreamContent{Ref: video.ControlledContentRef{ProviderTaskID: "taskUUID-playable", ContentID: "content-playable", MediaType: "video/mp4"}, SizeBytes: int64(len(videoG6PlayableFixture)), ReaderAt: bytes.NewReader(videoG6PlayableFixture), RangeMode: "supported"})
	if err != nil {
		t.Fatalf("实际可解码MP4必须可通过原探测器：%v", err)
	}
	if got.Width != 1280 || got.Height != 720 || got.FrameRate != 24 || got.DurationMillis != 5000 || got.HasAudio || got.SizeBytes <= 2<<20 {
		t.Fatalf("真实规格探测不一致：%dx%d fps=%d duration=%d", got.Width, got.Height, got.FrameRate, got.DurationMillis)
	}
}

// 只替换Fake的外部媒体边界；Submit/Query/取消与确认成本仍沿用既有Fake原生异步协议。
type videoPlayableProvider struct {
	*video.FakeAsyncVideoAdapter
	media []byte
}

func (p *videoPlayableProvider) OpenContent(ctx context.Context, ref video.ControlledContentRef) (video.StreamContent, error) {
	content, err := p.FakeAsyncVideoAdapter.OpenContent(ctx, ref)
	if err != nil || len(p.media) == 0 {
		return content, err
	}
	content.SizeBytes = int64(len(p.media))
	content.ReaderAt = bytes.NewReader(p.media)
	return content, nil
}
func newVideoContentFixtureProvider(media []byte) *videoPlayableProvider {
	return &videoPlayableProvider{FakeAsyncVideoAdapter: video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess), media: media}
}
