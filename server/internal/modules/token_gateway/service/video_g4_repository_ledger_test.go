package service

import (
	"encoding/json"
	"testing"

	videogateway "molin/server/internal/modules/token_gateway/video"
)

var _ videogateway.VideoTaskLedger = (*VideoRepositoryTaskLedger)(nil)

func TestParseVideoG4TaskSpecUsesFrozenLowSensitiveShape(t *testing.T) {
	raw := json.RawMessage("{\"operation\":\"text_to_video\",\"resolution\":\"1280x720\",\"duration_seconds\":5,\"aspect_ratio\":\"16:9\",\"frame_rate\":24,\"audio\":true}")
	spec, err := parseVideoG4TaskSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Width != 1280 || spec.Height != 720 || spec.DurationSeconds != 5 || spec.FrameRate != 24 || !spec.Audio {
		t.Fatalf("任务低敏规格解析错误: %+v", spec)
	}
	for _, unsafe := range []json.RawMessage{
		json.RawMessage("{\"resolution\":\"1280x720\",\"duration_seconds\":5,\"frame_rate\":24,\"audio\":true,\"prompt\":\"secret\"}"),
		json.RawMessage("{\"resolution\":\"https://example.com/video\",\"duration_seconds\":5,\"frame_rate\":24,\"audio\":true}"),
	} {
		if _, err := parseVideoG4TaskSpec(unsafe); err == nil {
			t.Fatalf("未知或外部字段必须失败关闭: %s", unsafe)
		}
	}
}
