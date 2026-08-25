package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

func TestNormalizeImageRequestOnlyAcceptsFrozenMVP(t *testing.T) {
	tests := []struct {
		name         string
		count        uint64
		size         string
		quality      string
		outputFormat string
		wantErr      bool
	}{
		{name: "省略参数时补齐冻结规格"},
		{name: "显式冻结规格", count: 1, size: "2K", quality: "standard", outputFormat: "url"},
		{name: "禁止多图", count: 2, size: "2K", quality: "standard", outputFormat: "url", wantErr: true},
		{name: "禁止其他尺寸", count: 1, size: "1K", quality: "standard", outputFormat: "url", wantErr: true},
		{name: "禁止其他质量", count: 1, size: "2K", quality: "hd", outputFormat: "url", wantErr: true},
		{name: "禁止其他输出格式", count: 1, size: "2K", quality: "standard", outputFormat: "b64_json", wantErr: true},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			normalized, variant, err := normalizeImageRequest("molin/image", "生成一张测试图片", item.count, item.size, item.quality, item.outputFormat)
			if item.wantErr {
				if !errors.Is(err, ErrImageAPIInvalid) {
					t.Fatalf("越出冻结规格必须拒绝: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if normalized.Count != 1 || normalized.Size != "2K" || normalized.Quality != "standard" || normalized.OutputFormat != "url" {
				t.Fatalf("规范化结果偏离冻结规格: %+v", normalized)
			}
			if variant.Resolution != "2K" || variant.AspectRatio != "1:1" || variant.Quality != "standard" || variant.OutputFormat != "provider_default" || variant.Delivery != "url" {
				t.Fatalf("价格变体偏离冻结规格: %+v", variant)
			}
		})
	}
}

func TestValidPublishedImageModelRequiresExplicitImageCapability(t *testing.T) {
	publishedAt := time.Now().UTC()
	item := model.TokenModel{
		Status: "active", Modality: "image", ReleaseVersionNo: 1, PublishedAt: &publishedAt,
		CapabilitiesJSON: json.RawMessage(`["image.generate"]`),
	}
	if !validPublishedImageModel(item) {
		t.Fatal("已发布图片模型必须在显式图片能力存在时可用")
	}
	item.CapabilitiesJSON = json.RawMessage(`["chat.completions"]`)
	if validPublishedImageModel(item) {
		t.Fatal("仅声明image模态但缺少image.generate能力时必须失败关闭")
	}
	item.CapabilitiesJSON = json.RawMessage(`not-json`)
	if validPublishedImageModel(item) {
		t.Fatal("图片能力JSON损坏时必须失败关闭")
	}
}
