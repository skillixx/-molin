package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/dto"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

type fakeImageApplication struct {
	generateInput *service.ImageGenerationInput
	generate      *service.ImageGenerationResult
	generateErr   error
	quote         *dto.ImageQuoteResp
	tasks         []dto.ImageTaskResp
	total         int64
	openAI        *dto.OpenAIImageGenerationResp
	openAIErr     error
	called        int
}

func (f *fakeImageApplication) CreateQuote(_ context.Context, _ service.ImageCaller, _ dto.ImageQuoteReq) (*dto.ImageQuoteResp, error) {
	f.called++
	return f.quote, nil
}
func (f *fakeImageApplication) Generate(_ context.Context, input service.ImageGenerationInput) (*service.ImageGenerationResult, error) {
	f.called++
	f.generateInput = &input
	return f.generate, f.generateErr
}
func (f *fakeImageApplication) ListTasks(_ context.Context, _ service.ImageTaskListInput) ([]dto.ImageTaskResp, int64, error) {
	f.called++
	return f.tasks, f.total, nil
}
func (f *fakeImageApplication) GetTask(_ context.Context, _ service.ImageCaller, taskID string, _ uint64) (*dto.ImageTaskResp, error) {
	f.called++
	return &dto.ImageTaskResp{TaskID: taskID}, nil
}
func (f *fakeImageApplication) GetTaskByRequest(_ context.Context, _ service.ImageCaller, requestID string, _ uint64) (*dto.ImageTaskResp, error) {
	f.called++
	return &dto.ImageTaskResp{RequestID: requestID}, nil
}
func (f *fakeImageApplication) CancelTask(_ context.Context, _ service.ImageCaller, _ uint64, taskID string) (*dto.ImageTaskResp, error) {
	f.called++
	return &dto.ImageTaskResp{TaskID: taskID}, nil
}
func (f *fakeImageApplication) DownloadURL(_ context.Context, _ service.ImageCaller, _ uint64, assetID string) (*dto.ImageDownloadResp, error) {
	f.called++
	return &dto.ImageDownloadResp{AssetID: assetID}, nil
}
func (f *fakeImageApplication) OpenAIResponse(_ context.Context, _ service.ImageCaller, _ dto.ImageTaskResp) (*dto.OpenAIImageGenerationResp, error) {
	f.called++
	return f.openAI, f.openAIErr
}

func TestImageOpenAIRequiresStrictIdempotencyBeforeService(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{name: "缺失"},
		{name: "过短", values: []string{"short"}},
		{name: "重复", values: []string{"0123456789abcdef", "fedcba9876543210"}},
		{name: "逗号多值", values: []string{"0123456789abcdef,second"}},
		{name: "控制字符", values: []string{"0123456789abcde\n"}},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			app := &fakeImageApplication{}
			h := NewImageHandler(app)
			r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
			for _, value := range item.values {
				r.Header.Add("Idempotency-Key", value)
			}
			w := httptest.NewRecorder()
			h.OpenAIGenerate(w, r)
			if w.Code != http.StatusBadRequest || app.called != 0 || !strings.Contains(w.Body.String(), "invalid_idempotency_key") {
				t.Fatalf("幂等键门禁错误: status=%d called=%d body=%s", w.Code, app.called, w.Body.String())
			}
		})
	}
}

func TestImageOpenAIGenerationReturnsRawCompatibleResponse(t *testing.T) {
	app := &fakeImageApplication{
		generate: &service.ImageGenerationResult{Task: dto.ImageTaskResp{TaskID: "task-1", RequestID: "req-1"}},
		openAI: &dto.OpenAIImageGenerationResp{
			Created: 1, MolinRequestID: "req-1",
			Data: []dto.OpenAIImageDataResp{{URL: "https://object.invalid/a", MolinAssetID: "asset-1", ExpiresAt: time.Unix(1, 0).UTC()}},
		},
	}
	h := NewImageHandler(app)
	r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
	r.Header.Set("Idempotency-Key", "0123456789abcdef")
	w := httptest.NewRecorder()
	h.OpenAIGenerate(w, r)
	if w.Code != http.StatusOK || app.generateInput == nil || !app.generateInput.RequireSK || strings.Contains(w.Body.String(), `"code":0`) ||
		!strings.Contains(w.Body.String(), `"molin_request_id":"req-1"`) || !strings.Contains(w.Body.String(), `"molin_asset_id":"asset-1"`) {
		t.Fatalf("OpenAI兼容响应错误: status=%d input=%+v body=%s", w.Code, app.generateInput, w.Body.String())
	}
}

func TestImageOpenAIUnknownReturns504WithRequestID(t *testing.T) {
	app := &fakeImageApplication{generate: &service.ImageGenerationResult{
		Task: dto.ImageTaskResp{TaskID: "task-unknown", RequestID: "req-unknown"}, ExecutionErr: service.ErrImageRequestPending,
	}}
	h := NewImageHandler(app)
	r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
	r.Header.Set("Idempotency-Key", "0123456789abcdef")
	w := httptest.NewRecorder()
	h.OpenAIGenerate(w, r)
	if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), "request_timeout_unknown") || !strings.Contains(w.Body.String(), "req-unknown") {
		t.Fatalf("结果未知契约错误: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestImageOpenAIOutputPolicyRejectedUsesStableError(t *testing.T) {
	errorCode := "no_deliverable_image"
	app := &fakeImageApplication{generate: &service.ImageGenerationResult{
		Task: dto.ImageTaskResp{TaskID: "task-rejected", RequestID: "req-rejected", ErrorCode: &errorCode}, ExecutionErr: errors.New("内部审核错误"),
	}}
	h := NewImageHandler(app)
	r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
	r.Header.Set("Idempotency-Key", "0123456789abcdef")
	w := httptest.NewRecorder()
	h.OpenAIGenerate(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "output_policy_rejected") || !strings.Contains(w.Body.String(), "req-rejected") {
		t.Fatalf("输出审核拒绝契约错误: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestImagePlatformGenerationRequiresQuote(t *testing.T) {
	app := &fakeImageApplication{}
	h := NewImageHandler(app)
	r := httptest.NewRequest(http.MethodPost, "/api/token/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试","project_id":1}`))
	r.Header.Set("Idempotency-Key", "0123456789abcdef")
	w := httptest.NewRecorder()
	h.PlatformGenerate(w, r)
	if w.Code != http.StatusBadRequest || app.called != 0 || !strings.Contains(w.Body.String(), "quote_required") {
		t.Fatalf("Quote门禁错误: status=%d called=%d body=%s", w.Code, app.called, w.Body.String())
	}
}

func TestImageTaskListUsesD95FlatPagination(t *testing.T) {
	app := &fakeImageApplication{tasks: []dto.ImageTaskResp{{TaskID: "task-1"}}, total: 1}
	h := NewImageHandler(app)
	r := httptest.NewRequest(http.MethodGet, "/api/token/image-tasks?project_id=1&page=2&page_size=5", nil)
	w := httptest.NewRecorder()
	h.ListTasks(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `"items"`) || !strings.Contains(body, `"page":2`) || !strings.Contains(body, `"page_size":5`) || !strings.Contains(body, `"total":1`) || strings.Contains(body, "pagination") {
		t.Fatalf("D-95响应错误: status=%d body=%s", w.Code, body)
	}
}

func TestImageStrictJSONRejectsDuplicateAndUnknownFields(t *testing.T) {
	for _, body := range []string{
		`{"model":"a","model":"b","prompt":"测试"}`,
		`{"model":"a","prompt":"测试","secret":"x"}`,
	} {
		app := &fakeImageApplication{}
		h := NewImageHandler(app)
		r := httptest.NewRequest(http.MethodPost, "/api/token/images/quotes", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.CreateQuote(w, r)
		if w.Code != http.StatusBadRequest || app.called != 0 {
			t.Fatalf("严格JSON门禁错误: status=%d called=%d body=%s", w.Code, app.called, w.Body.String())
		}
	}
}

func TestImageProjectIDBodyAndQueryConflictRejected(t *testing.T) {
	app := &fakeImageApplication{}
	h := NewImageHandler(app)
	r := httptest.NewRequest(http.MethodPost, "/api/token/images/quotes?project_id=2", strings.NewReader(`{"model":"a","prompt":"测试","project_id":1}`))
	w := httptest.NewRecorder()
	h.CreateQuote(w, r)
	if w.Code != http.StatusBadRequest || app.called != 0 {
		t.Fatalf("Project冲突必须在服务前拒绝: status=%d called=%d body=%s", w.Code, app.called, w.Body.String())
	}
}

func TestImageExecutionErrorDoesNotExposeInternalError(t *testing.T) {
	app := &fakeImageApplication{generateErr: errors.New("provider secret raw failure")}
	h := NewImageHandler(app)
	r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
	r.Header.Set("Idempotency-Key", "0123456789abcdef")
	w := httptest.NewRecorder()
	h.OpenAIGenerate(w, r)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "provider secret raw failure") {
		t.Fatalf("内部错误泄露: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestImageStablePreProviderErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
	}{
		{name: "Quote不存在", err: repository.ErrImageQuoteNotFound, wantStatus: http.StatusNotFound, wantType: "quote_not_found"},
		{name: "审核不可用", err: imagegateway.ErrModerationFailed, wantStatus: http.StatusServiceUnavailable, wantType: "moderation_unavailable"},
		{name: "账号不可用", err: service.ErrUserUnavailable, wantStatus: http.StatusForbidden, wantType: "account_unavailable"},
		{name: "并发限制", err: &service.ResourceLimitError{Cause: service.ErrConcurrencyExceeded, LimitScope: "project", LimitType: "concurrency", RetryAfter: 1500 * time.Millisecond}, wantStatus: http.StatusTooManyRequests, wantType: "concurrency_limit_exceeded"},
		{name: "治理不可用", err: service.ErrResourceUnavailable, wantStatus: http.StatusServiceUnavailable, wantType: "governance_unavailable"},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			app := &fakeImageApplication{generateErr: item.err}
			h := NewImageHandler(app)
			r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
			r.Header.Set("Idempotency-Key", "0123456789abcdef")
			w := httptest.NewRecorder()
			h.OpenAIGenerate(w, r)
			if w.Code != item.wantStatus || !strings.Contains(w.Body.String(), item.wantType) {
				t.Fatalf("稳定错误合同错误: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestImageConcurrencyLimitReturnsRetryAfter(t *testing.T) {
	app := &fakeImageApplication{generateErr: &service.ResourceLimitError{
		Cause: service.ErrConcurrencyExceeded, LimitScope: "project", LimitType: "concurrency", RetryAfter: 1500 * time.Millisecond,
	}}
	handler := NewImageHandler(app)
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	response := httptest.NewRecorder()
	handler.OpenAIGenerate(response, request)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "2" || !strings.Contains(response.Body.String(), "concurrency_limit_exceeded") || !strings.Contains(response.Body.String(), `"limit_scope":"project"`) {
		t.Fatalf("图片并发限制响应错误: status=%d retry=%s body=%s", response.Code, response.Header().Get("Retry-After"), response.Body.String())
	}
}

func TestImageQueueFullReturns429WhileQueueFailureRemains503(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
	}{
		{name: "本地容量满载", err: service.ErrImageQueueFull, wantStatus: http.StatusTooManyRequests, wantType: "concurrency_limit_exceeded"},
		{name: "RabbitMQ不可用", err: service.ErrImageAsyncUnavailable, wantStatus: http.StatusServiceUnavailable, wantType: "image_queue_unavailable"},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			app := &fakeImageApplication{generateErr: item.err}
			handler := NewImageHandler(app)
			request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"molin/image","prompt":"测试"}`))
			request.Header.Set("Idempotency-Key", "0123456789abcdef")
			response := httptest.NewRecorder()
			handler.OpenAIGenerate(response, request)
			if response.Code != item.wantStatus || !strings.Contains(response.Body.String(), item.wantType) {
				t.Fatalf("队列错误分类错误: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
