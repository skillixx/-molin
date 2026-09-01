package video

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrVideoModerationRejected = errors.New("视频内容安全拒绝")
	ErrVideoModerationFailed   = errors.New("视频内容审核失败")
)

type FrameKind string

const (
	FrameFirst       FrameKind = "first"
	FrameLast        FrameKind = "last"
	FrameInterval    FrameKind = "interval"
	FrameSceneChange FrameKind = "scene_change"
)

type VideoFrameSample struct {
	Kind  FrameKind
	Bytes []byte
}

type VideoAudioSample struct {
	Bytes []byte
}

type VideoSampleSet struct {
	Frames []VideoFrameSample
	Audio  *VideoAudioSample
}

type VideoSampler interface {
	Sample(ctx context.Context, media VideoMediaMetadata) (VideoSampleSet, error)
}

type VideoModerationAdapter interface {
	ModeratePrompt(ctx context.Context, prompt string) error
	ReferenceOCR(ctx context.Context, reference NormalizedReferenceImage) error
	ReferenceVisual(ctx context.Context, reference NormalizedReferenceImage) error
	ReferenceQRCode(ctx context.Context, reference NormalizedReferenceImage) error
	ReferenceText(ctx context.Context, reference NormalizedReferenceImage) error
	ReferenceMetadata(ctx context.Context, reference NormalizedReferenceImage) error
	ModerateOutputFrames(ctx context.Context, frames []VideoFrameSample) error
	ModerateAudio(ctx context.Context, audio VideoAudioSample) error
}

type VideoSafetyRequest struct {
	Operation string
	Prompt    string
	Reference *NormalizedReferenceImage
	Media     VideoMediaMetadata
}

type VideoSafetyResult struct {
	Allowed    bool
	FrameKinds []FrameKind
}

type VideoSafetyPipeline struct {
	moderator VideoModerationAdapter
	sampler   VideoSampler
}

func NewVideoSafetyPipeline(moderator VideoModerationAdapter, sampler VideoSampler) *VideoSafetyPipeline {
	return &VideoSafetyPipeline{moderator: moderator, sampler: sampler}
}

// Assess 按任务类型执行完整审核链；任何拒绝、错误或缺失采样均失败关闭。
func (p *VideoSafetyPipeline) Assess(ctx context.Context, request VideoSafetyRequest) (VideoSafetyResult, error) {
	if p == nil || p.moderator == nil || p.sampler == nil || request.Prompt == "" {
		return VideoSafetyResult{}, ErrVideoModerationFailed
	}
	if err := p.Preflight(ctx, request); err != nil {
		return VideoSafetyResult{}, err
	}
	return p.AssessOutput(ctx, request.Media)
}

// Preflight 在Provider Submit前完成Prompt和I2V参考图全部输入审核。
func (p *VideoSafetyPipeline) Preflight(ctx context.Context, request VideoSafetyRequest) error {
	if err := p.AssessPrompt(ctx, request.Prompt); err != nil {
		return err
	}
	switch request.Operation {
	case OperationTextToVideo:
		if request.Reference != nil {
			return ErrVideoModerationFailed
		}
	case OperationImageToVideo:
		if request.Reference == nil {
			return ErrVideoModerationFailed
		}
		return p.AssessReference(ctx, *request.Reference)
	default:
		return ErrVideoModerationFailed
	}
	return nil
}

// AssessPrompt供inline上传前的只读准入使用；最终生成仍会连同规范化参考图再次执行完整Preflight。
func (p *VideoSafetyPipeline) AssessPrompt(ctx context.Context, prompt string) error {
	if p == nil || p.moderator == nil || prompt == "" {
		return ErrVideoModerationFailed
	}
	return p.moderator.ModeratePrompt(ctx, prompt)
}

// AssessReference供受控上传执行相同的五类输入审核，不伪造Prompt来借用生成预检。
func (p *VideoSafetyPipeline) AssessReference(ctx context.Context, reference NormalizedReferenceImage) error {
	if p == nil || p.moderator == nil {
		return ErrVideoModerationFailed
	}
	for _, check := range []func(context.Context, NormalizedReferenceImage) error{p.moderator.ReferenceOCR, p.moderator.ReferenceVisual, p.moderator.ReferenceQRCode, p.moderator.ReferenceText, p.moderator.ReferenceMetadata} {
		if err := check(ctx, reference); err != nil {
			return err
		}
	}
	return nil
}

// AssessOutput 在Provider结果通过Probe后审核四类帧和可选音轨。
func (p *VideoSafetyPipeline) AssessOutput(ctx context.Context, media VideoMediaMetadata) (VideoSafetyResult, error) {
	if p == nil || p.moderator == nil || p.sampler == nil {
		return VideoSafetyResult{}, ErrVideoModerationFailed
	}
	samples, err := p.sampler.Sample(ctx, media)
	if err != nil || !validFrameSampleSet(samples.Frames) {
		return VideoSafetyResult{}, ErrVideoModerationFailed
	}
	if err := p.moderator.ModerateOutputFrames(ctx, samples.Frames); err != nil {
		return VideoSafetyResult{}, err
	}
	if media.HasAudio {
		if samples.Audio == nil {
			return VideoSafetyResult{}, ErrVideoModerationFailed
		}
		if err := p.moderator.ModerateAudio(ctx, *samples.Audio); err != nil {
			return VideoSafetyResult{}, err
		}
	}
	kinds := make([]FrameKind, 0, len(samples.Frames))
	for _, frame := range samples.Frames {
		kinds = append(kinds, frame.Kind)
	}
	return VideoSafetyResult{Allowed: true, FrameKinds: kinds}, nil
}

// AssessDerived 对每个派生正文重新执行与媒体类型匹配的Fake审核，不直接复制父资产结论。
func (p *VideoSafetyPipeline) AssessDerived(ctx context.Context, asset GatewayAsset, body []byte) error {
	if p == nil || p.moderator == nil || len(body) == 0 {
		return ErrVideoModerationFailed
	}
	if asset.MIMEType == "video/mp4" {
		_, err := p.AssessOutput(ctx, VideoMediaMetadata{
			Width: asset.Width, Height: asset.Height, DurationMillis: asset.DurationMillis,
			FrameRate: asset.FrameRate, VideoCodec: asset.VideoCodec, AudioCodec: asset.AudioCodec,
			HasAudio: asset.HasAudio, MIMEType: asset.MIMEType, Container: "mp4",
		})
		return err
	}
	reference := NormalizedReferenceImage{Bytes: body, MIMEType: asset.MIMEType, Width: int(asset.Width), Height: int(asset.Height), NormalizedSHA256: asset.SHA256}
	if err := p.moderator.ReferenceVisual(ctx, reference); err != nil {
		return err
	}
	return p.moderator.ReferenceMetadata(ctx, reference)
}

func validFrameSampleSet(frames []VideoFrameSample) bool {
	if len(frames) != 4 {
		return false
	}
	required := []FrameKind{FrameFirst, FrameLast, FrameInterval, FrameSceneChange}
	for index := range required {
		if frames[index].Kind != required[index] || len(frames[index].Bytes) == 0 {
			return false
		}
	}
	return true
}

type FakeVideoSampleMode string

const (
	FakeVideoSampleSuccess FakeVideoSampleMode = "success"
	FakeVideoSampleFailure FakeVideoSampleMode = "failure"
)

type FakeVideoSampler struct{ mode FakeVideoSampleMode }

func NewFakeVideoSampler(mode FakeVideoSampleMode) *FakeVideoSampler {
	return &FakeVideoSampler{mode: mode}
}

func (s *FakeVideoSampler) Sample(ctx context.Context, media VideoMediaMetadata) (VideoSampleSet, error) {
	if err := ctx.Err(); err != nil {
		return VideoSampleSet{}, err
	}
	if s == nil || s.mode == FakeVideoSampleFailure || media.DurationMillis == 0 {
		return VideoSampleSet{}, ErrVideoModerationFailed
	}
	frames := []VideoFrameSample{
		{Kind: FrameFirst, Bytes: []byte("frame-first")},
		{Kind: FrameLast, Bytes: []byte("frame-last")},
		{Kind: FrameInterval, Bytes: []byte("frame-interval")},
		{Kind: FrameSceneChange, Bytes: []byte("frame-scene")},
	}
	result := VideoSampleSet{Frames: frames}
	if media.HasAudio {
		result.Audio = &VideoAudioSample{Bytes: []byte("audio-sample")}
	}
	return result, nil
}

type FakeVideoModerationMode string

const (
	FakeVideoModerationAllow           FakeVideoModerationMode = "allow"
	FakeVideoModerationRejectPrompt    FakeVideoModerationMode = "reject_prompt"
	FakeVideoModerationRejectReference FakeVideoModerationMode = "reject_reference"
	FakeVideoModerationRejectFrames    FakeVideoModerationMode = "reject_frames"
	FakeVideoModerationRejectAudio     FakeVideoModerationMode = "reject_audio"
	FakeVideoModerationError           FakeVideoModerationMode = "error"
)

type VideoModerationCalls struct {
	Prompt            int
	ReferenceOCR      int
	ReferenceVisual   int
	ReferenceQR       int
	ReferenceText     int
	ReferenceMetadata int
	OutputFrames      int
	Audio             int
}

// FakeVideoModerationAdapter 只记录低敏调用计数，不保存Prompt、图片、帧或音轨正文。
type FakeVideoModerationAdapter struct {
	mu    sync.Mutex
	mode  FakeVideoModerationMode
	calls VideoModerationCalls
}

func NewFakeVideoModerationAdapter(mode FakeVideoModerationMode) *FakeVideoModerationAdapter {
	return &FakeVideoModerationAdapter{mode: mode}
}

func (a *FakeVideoModerationAdapter) ModeratePrompt(ctx context.Context, _ string) error {
	return a.record(ctx, func(c *VideoModerationCalls) { c.Prompt++ }, FakeVideoModerationRejectPrompt)
}
func (a *FakeVideoModerationAdapter) ReferenceOCR(ctx context.Context, _ NormalizedReferenceImage) error {
	return a.record(ctx, func(c *VideoModerationCalls) { c.ReferenceOCR++ }, FakeVideoModerationRejectReference)
}
func (a *FakeVideoModerationAdapter) ReferenceVisual(ctx context.Context, _ NormalizedReferenceImage) error {
	return a.record(ctx, func(c *VideoModerationCalls) { c.ReferenceVisual++ }, FakeVideoModerationRejectReference)
}
func (a *FakeVideoModerationAdapter) ReferenceQRCode(ctx context.Context, _ NormalizedReferenceImage) error {
	return a.record(ctx, func(c *VideoModerationCalls) { c.ReferenceQR++ }, FakeVideoModerationRejectReference)
}
func (a *FakeVideoModerationAdapter) ReferenceText(ctx context.Context, _ NormalizedReferenceImage) error {
	return a.record(ctx, func(c *VideoModerationCalls) { c.ReferenceText++ }, FakeVideoModerationRejectReference)
}
func (a *FakeVideoModerationAdapter) ReferenceMetadata(ctx context.Context, _ NormalizedReferenceImage) error {
	return a.record(ctx, func(c *VideoModerationCalls) { c.ReferenceMetadata++ }, FakeVideoModerationRejectReference)
}
func (a *FakeVideoModerationAdapter) ModerateOutputFrames(ctx context.Context, _ []VideoFrameSample) error {
	return a.record(ctx, func(c *VideoModerationCalls) { c.OutputFrames++ }, FakeVideoModerationRejectFrames)
}
func (a *FakeVideoModerationAdapter) ModerateAudio(ctx context.Context, _ VideoAudioSample) error {
	return a.record(ctx, func(c *VideoModerationCalls) { c.Audio++ }, FakeVideoModerationRejectAudio)
}

func (a *FakeVideoModerationAdapter) record(ctx context.Context, increment func(*VideoModerationCalls), rejectMode FakeVideoModerationMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	increment(&a.calls)
	if a.mode == FakeVideoModerationError {
		return ErrVideoModerationFailed
	}
	if a.mode == rejectMode {
		return ErrVideoModerationRejected
	}
	return nil
}

func (a *FakeVideoModerationAdapter) Calls() VideoModerationCalls {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}
