package image

import (
	"context"
	"errors"
)

var (
	ErrProviderFailed       = errors.New("图片Provider明确失败")
	ErrProviderTimeout      = errors.New("图片Provider超时")
	ErrProviderDisconnected = errors.New("图片Provider连接中断")
	ErrProviderUnknown      = errors.New("图片Provider结果未知")
)

type ProviderImageRequest struct {
	RequestID    string
	ModelCode    string
	Prompt       string `json:"-"`
	Count        uint64
	Resolution   string
	AspectRatio  string
	Quality      string
	OutputFormat string
}

type ProviderImage struct {
	Index     uint64
	URL       string `json:"-"`
	Base64    string `json:"-"`
	MediaType string
}

type ProviderImageResult struct {
	Images            []ProviderImage
	ProviderRequestID string `json:"-"`
	ProviderCostUSD   string
	ResultUnknown     bool
}

// ImageProviderAdapter 只处理Provider协议，不负责用户鉴权、价格、钱包、资产归属或最终内容安全。
type ImageProviderAdapter interface {
	Name() string
	Generate(ctx context.Context, request ProviderImageRequest) (ProviderImageResult, error)
}
