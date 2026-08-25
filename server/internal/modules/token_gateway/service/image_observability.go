package service

import (
	"context"
	"errors"
	"time"

	imagegateway "molin/server/internal/modules/token_gateway/image"
)

type ObservedImageAdapter struct {
	inner   imagegateway.ImageProviderAdapter
	metrics *AIGatewayMetrics
}

func NewObservedImageAdapter(inner imagegateway.ImageProviderAdapter, metrics *AIGatewayMetrics) (*ObservedImageAdapter, error) {
	if inner == nil || metrics == nil {
		return nil, ErrImageAPIInvalid
	}
	return &ObservedImageAdapter{inner: inner, metrics: metrics}, nil
}

func (a *ObservedImageAdapter) Name() string { return a.inner.Name() }

// Generate 只记录封闭枚举的模型/驱动/结果和耗时，不记录Prompt、request_id、URL或图片正文。
func (a *ObservedImageAdapter) Generate(ctx context.Context, request imagegateway.ProviderImageRequest) (imagegateway.ProviderImageResult, error) {
	started := time.Now()
	result, err := a.inner.Generate(ctx, request)
	outcome := "success"
	if err != nil {
		outcome = "unknown"
		switch {
		case errors.Is(err, imagegateway.ErrProviderTimeout):
			outcome = "timeout"
		case errors.Is(err, imagegateway.ErrProviderFailed):
			outcome = "server_error"
		case errors.Is(err, imagegateway.ErrImageResultInvalid):
			outcome = "malformed"
		}
	}
	a.metrics.RecordUpstream(request.ModelCode, a.Name(), outcome)
	requestOutcome := "success"
	if err != nil {
		requestOutcome = "failure"
	}
	a.metrics.RecordRequest(request.ModelCode, "image", requestOutcome, time.Since(started))
	return result, err
}

var _ imagegateway.ImageProviderAdapter = (*ObservedImageAdapter)(nil)
