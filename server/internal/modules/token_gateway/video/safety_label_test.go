package video

import (
	"context"
	"errors"
	"testing"
)

func TestVideoSafetyPipelineCoversTextAndImageRequirements(t *testing.T) {
	tests := []struct {
		name          string
		operation     string
		reference     *NormalizedReferenceImage
		wantReference int
	}{
		{name: "t2v", operation: OperationTextToVideo},
		{name: "i2v", operation: OperationImageToVideo, reference: &NormalizedReferenceImage{Bytes: []byte("private-normalized"), MIMEType: "image/png", Width: 16, Height: 9, OriginalSHA256: "a", NormalizedSHA256: "b"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			moderator := NewFakeVideoModerationAdapter(FakeVideoModerationAllow)
			sampler := NewFakeVideoSampler(FakeVideoSampleSuccess)
			pipeline := NewVideoSafetyPipeline(moderator, sampler)
			result, err := pipeline.Assess(context.Background(), VideoSafetyRequest{
				Operation: test.operation, Prompt: "仅在内存中审核", Reference: test.reference,
				Media: VideoMediaMetadata{Width: 1280, Height: 720, DurationMillis: 5000, FrameRate: 24, HasAudio: true},
			})
			if err != nil || !result.Allowed {
				t.Fatalf("审核应通过: result=%+v err=%v", result, err)
			}
			if len(result.FrameKinds) != 4 || result.FrameKinds[0] != FrameFirst || result.FrameKinds[1] != FrameLast ||
				result.FrameKinds[2] != FrameInterval || result.FrameKinds[3] != FrameSceneChange {
				t.Fatalf("抽帧集合不完整: %+v", result.FrameKinds)
			}
			calls := moderator.Calls()
			if calls.Prompt != 1 || calls.OutputFrames != 1 || calls.Audio != 1 {
				t.Fatalf("基础审核调用不完整: %+v", calls)
			}
			if test.operation == OperationImageToVideo {
				if calls.ReferenceOCR != 1 || calls.ReferenceVisual != 1 || calls.ReferenceQR != 1 || calls.ReferenceText != 1 || calls.ReferenceMetadata != 1 {
					t.Fatalf("I2V参考图审核不完整: %+v", calls)
				}
			} else if calls.ReferenceOCR+calls.ReferenceVisual+calls.ReferenceQR+calls.ReferenceText+calls.ReferenceMetadata != 0 {
				t.Fatalf("T2V不得执行参考图审核: %+v", calls)
			}
		})
	}
}

func TestVideoSafetyPipelineFailsClosed(t *testing.T) {
	for _, mode := range []FakeVideoModerationMode{
		FakeVideoModerationRejectPrompt, FakeVideoModerationRejectReference,
		FakeVideoModerationRejectFrames, FakeVideoModerationRejectAudio,
		FakeVideoModerationError,
	} {
		t.Run(string(mode), func(t *testing.T) {
			pipeline := NewVideoSafetyPipeline(NewFakeVideoModerationAdapter(mode), NewFakeVideoSampler(FakeVideoSampleSuccess))
			_, err := pipeline.Assess(context.Background(), VideoSafetyRequest{
				Operation: OperationImageToVideo, Prompt: "测试",
				Reference: &NormalizedReferenceImage{Bytes: []byte("private"), MIMEType: "image/png", Width: 16, Height: 9},
				Media:     VideoMediaMetadata{DurationMillis: 5000, HasAudio: true},
			})
			if !errors.Is(err, ErrVideoModerationRejected) && !errors.Is(err, ErrVideoModerationFailed) {
				t.Fatalf("审核拒绝或错误必须失败关闭: %v", err)
			}
		})
	}
}

func TestFakeAILabelerRequiresExplicitAndImplicitLabels(t *testing.T) {
	for _, mode := range []FakeVideoLabelMode{FakeVideoLabelSuccess, FakeVideoLabelExplicitFailure, FakeVideoLabelImplicitFailure} {
		t.Run(string(mode), func(t *testing.T) {
			labeler := NewFakeVideoAILabeler(mode, "fake-label-v1")
			result, err := labeler.Apply(context.Background(), LabelRequest{TaskID: "vid_task_label", AssetID: "vasset_label", SHA256: "abc"})
			if mode == FakeVideoLabelSuccess {
				if err != nil || result.ExplicitStatus != LabelApplied || result.ImplicitStatus != LabelApplied || result.Version != "fake-label-v1" {
					t.Fatalf("双标识必须完整: result=%+v err=%v", result, err)
				}
			} else if !errors.Is(err, ErrVideoLabelFailed) || result.ExplicitStatus == LabelApplied && result.ImplicitStatus == LabelApplied {
				t.Fatalf("任一标识失败不得视为完成: result=%+v err=%v", result, err)
			}
		})
	}
}
