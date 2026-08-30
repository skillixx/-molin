package service

import (
	"strings"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

// TestVideoGenerationIntentDoesNotBindFacadeAssetIDs 保证同一图像内容与版本不因门面资产ID变化产生另一生成意图。
func TestVideoGenerationIntentDoesNotBindFacadeAssetIDs(t *testing.T) {
	secret := []byte(strings.Repeat("i", 32))
	prompt, err := VideoGenerationPromptHMAC(secret, "  示例视频\r\n第二行  ")
	if err != nil {
		t.Fatal(err)
	}
	intent := VideoGenerationIntent{LogicalModelCode: "molin/video-fixture", PromptHMAC: prompt, RightsPolicyVersion: "rights-fixture-v1",
		Variant: VideoPriceVariant{Operation: model.AIVideoOperationImageToVideo, Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24},
		Input:   &VideoQuoteInputBinding{InputAssetID: "vin_first", InternalID: 1, NormalizedSHA256: strings.Repeat("a", 64), Version: 1}}
	first, err := BuildVideoGenerationIntentFingerprint(secret, intent)
	if err != nil {
		t.Fatal(err)
	}
	copyInput := *intent.Input
	copyInput.InputAssetID, copyInput.InternalID = "vin_second", 2
	intent.Input = &copyInput
	second, err := BuildVideoGenerationIntentFingerprint(secret, intent)
	if err != nil || first != second {
		t.Fatalf("等内容与版本的输入应共享意图: %v", err)
	}
	intent.Input.Version++
	changed, err := BuildVideoGenerationIntentFingerprint(secret, intent)
	if err != nil || first == changed {
		t.Fatal("输入版本改变必须形成不同意图")
	}
	canonical, err := VideoGenerationPromptHMAC(secret, "示例视频\n第二行")
	if err != nil || canonical != prompt {
		t.Fatal("Prompt换行及边缘空白应规范化")
	}
}

// TestVideoGenerationIntentFreezesAllBillingDimensions 固化所有影响生成权益与计价的维度。
func TestVideoGenerationIntentFreezesAllBillingDimensions(t *testing.T) {
	secret := []byte(strings.Repeat("k", 32))
	base := VideoGenerationIntent{LogicalModelCode: "molin/video-fixture", PromptHMAC: strings.Repeat("a", 64), RightsPolicyVersion: "rights-v1",
		Variant: VideoPriceVariant{Operation: model.AIVideoOperationTextToVideo, Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24}}
	fingerprint, err := BuildVideoGenerationIntentFingerprint(secret, base)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*VideoGenerationIntent){
		"模型":   func(v *VideoGenerationIntent) { v.LogicalModelCode = "molin/other" },
		"提示词":  func(v *VideoGenerationIntent) { v.PromptHMAC = strings.Repeat("b", 64) },
		"时长":   func(v *VideoGenerationIntent) { v.Variant.DurationSeconds = 6 },
		"分辨率":  func(v *VideoGenerationIntent) { v.Variant.Resolution = "720x1280" },
		"比例":   func(v *VideoGenerationIntent) { v.Variant.AspectRatio = "9:16" },
		"帧率":   func(v *VideoGenerationIntent) { v.Variant.FrameRate = 30 },
		"音轨":   func(v *VideoGenerationIntent) { v.Variant.Audio = true },
		"权益策略": func(v *VideoGenerationIntent) { v.RightsPolicyVersion = "rights-v2" },
		"操作": func(v *VideoGenerationIntent) {
			v.Variant.Operation = model.AIVideoOperationImageToVideo
			v.Input = &VideoQuoteInputBinding{NormalizedSHA256: strings.Repeat("c", 64), Version: 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			got, e := BuildVideoGenerationIntentFingerprint(secret, value)
			if e != nil || got == fingerprint {
				t.Fatalf("维度变化未隔离意图: %v", e)
			}
		})
	}
	invalid := base
	invalid.Input = &VideoQuoteInputBinding{NormalizedSHA256: strings.Repeat("c", 64), Version: 1}
	if _, err := BuildVideoGenerationIntentFingerprint(secret, invalid); err == nil {
		t.Fatal("T2V禁止输入")
	}
	invalid = base
	invalid.Variant.Operation = model.AIVideoOperationImageToVideo
	if _, err := BuildVideoGenerationIntentFingerprint(secret, invalid); err == nil {
		t.Fatal("I2V必须有唯一输入")
	}
	invalid = base
	invalid.RightsPolicyVersion = ""
	if _, err := BuildVideoGenerationIntentFingerprint(secret, invalid); err == nil {
		t.Fatal("权益版本缺失必须拒绝")
	}
	if _, err := BuildVideoGenerationIntentFingerprint([]byte("short"), base); err == nil {
		t.Fatal("必须使用专用强HMAC密钥")
	}
	if _, err := VideoGenerationPromptHMAC(secret, " \n "); err == nil {
		t.Fatal("空Prompt必须拒绝")
	}
}
