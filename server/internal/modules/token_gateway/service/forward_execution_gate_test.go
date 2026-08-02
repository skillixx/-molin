package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

type fixedTokenModelReader struct {
	model *model.TokenModel
}

func (r fixedTokenModelReader) FindByCode(_ context.Context, _ string) (*model.TokenModel, error) {
	if r.model == nil {
		return nil, errors.New("模型不存在")
	}
	return r.model, nil
}

type fixedTokenChannelReader struct {
	channel *model.TokenChannel
}

func (r fixedTokenChannelReader) FindByID(_ context.Context, _ uint64) (*model.TokenChannel, error) {
	if r.channel == nil {
		return nil, errors.New("渠道不存在")
	}
	return r.channel, nil
}

type fixedAssetGate struct {
	allowed bool
}

func (g fixedAssetGate) HasActiveTokenAsset(_ context.Context, _ uint64) (bool, error) {
	return g.allowed, nil
}

type countingExecutionDriver struct {
	calls int
}

func (d *countingExecutionDriver) Name() string { return "bifrost" }

func (d *countingExecutionDriver) ChatCompletion(_ context.Context, _ ExecutionRequest) (*ExecutionResponse, error) {
	d.calls++
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	return &ExecutionResponse{
		Response: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))},
		Usage:    ExecutionUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2, Present: true},
		Attempt:  ExecutionAttempt{Driver: "bifrost", Outcome: "success"},
	}, nil
}

func (d *countingExecutionDriver) ChatCompletionStream(ctx context.Context, req ExecutionRequest) (*ExecutionResponse, error) {
	return d.ChatCompletion(ctx, req)
}

func (d *countingExecutionDriver) NormalizeStreamLine(line []byte) (ExecutionStreamChunk, error) {
	return ExecutionStreamChunk{PublicLine: line}, nil
}

func newGateTestService(assetAllowed bool, scope ModelScopeResolver, driver ExecutionDriver) *ForwardService {
	channelID := uint64(1)
	upstreamModel := "provider/model"
	return &ForwardService{
		modelRepo: fixedTokenModelReader{model: &model.TokenModel{
			LogicalModelCode: "molin/qwen-turbo", Modality: "chat", Status: "active", VisibleScope: "all",
			ChannelID: &channelID, UpstreamModel: &upstreamModel,
		}},
		channelRepo:    fixedTokenChannelReader{channel: &model.TokenChannel{ID: channelID, BaseURL: "http://bifrost.internal", Status: "active"}},
		usageRepo:      &memoryUsageLogWriter{},
		assetGate:      fixedAssetGate{allowed: assetAllowed},
		scopeResolver:  scope,
		driverSelector: staticExecutionDriverSelector{driver: driver},
	}
}

func TestForward_ExecutionDriverCannotBypassAssetGate(t *testing.T) {
	driver := &countingExecutionDriver{}
	service := newGateTestService(false, nil, driver)
	err := service.Forward(context.Background(), httptest.NewRecorder(), ForwardInput{
		RequestID: "req-asset-denied", UserID: 1, Model: "molin/qwen-turbo", Body: map[string]interface{}{},
	})
	if !errors.Is(err, ErrAccessDenied) || driver.calls != 0 {
		t.Fatalf("资产门禁拒绝后不得调用执行驱动: err=%v calls=%d", err, driver.calls)
	}
}

func TestForward_ExecutionDriverPreservesSKAndJWTModelScope(t *testing.T) {
	t.Run("SK 越界时拒绝", func(t *testing.T) {
		driver := &countingExecutionDriver{}
		scope := &fakeScopeResolver{scopes: map[uint64][]string{9: {"molin/other"}}, valid: map[uint64]bool{9: true}}
		service := newGateTestService(true, scope, driver)
		err := service.Forward(context.Background(), httptest.NewRecorder(), ForwardInput{
			RequestID: "req-sk-denied", UserID: 1, APIKeyID: 9, Model: "molin/qwen-turbo", Body: map[string]interface{}{},
		})
		if !errors.Is(err, ErrModelNotInScope) || driver.calls != 0 {
			t.Fatalf("SK 模型越界后不得调用执行驱动: err=%v calls=%d", err, driver.calls)
		}
	})

	t.Run("JWT 与授权 SK 均可到达驱动", func(t *testing.T) {
		for _, input := range []ForwardInput{
			{RequestID: "req-jwt", UserID: 1, Model: "molin/qwen-turbo", Body: map[string]interface{}{}},
			{RequestID: "req-sk", UserID: 1, APIKeyID: 9, Model: "molin/qwen-turbo", Body: map[string]interface{}{}},
		} {
			driver := &countingExecutionDriver{}
			scope := &fakeScopeResolver{scopes: map[uint64][]string{9: {"molin/qwen-turbo"}}, valid: map[uint64]bool{9: true}}
			service := newGateTestService(true, scope, driver)
			recorder := httptest.NewRecorder()
			if err := service.Forward(context.Background(), recorder, input); err != nil || driver.calls != 1 || recorder.Code != http.StatusOK {
				t.Fatalf("合法调用应到达一次执行驱动: request=%s err=%v calls=%d status=%d", input.RequestID, err, driver.calls, recorder.Code)
			}
		}
	})
}
