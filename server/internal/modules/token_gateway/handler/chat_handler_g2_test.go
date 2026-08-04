package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/middleware"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

type fakeG2Orchestrator struct {
	prepared      *service.PreparedRequest
	prepareErr    error
	command       service.PrepareCommand
	executeCalled bool
}

func (f *fakeG2Orchestrator) Prepare(_ context.Context, command service.PrepareCommand) (*service.PreparedRequest, error) {
	f.command = command
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	if f.prepared != nil {
		return f.prepared, nil
	}
	return &service.PreparedRequest{RequestID: command.RequestID, ExecutionStatus: "pending", BillingStatus: "unquoted"}, nil
}
func (f *fakeG2Orchestrator) Execute(_ context.Context, _ string, sink service.StreamSink) error {
	f.executeCalled = true
	if f.command.Stream {
		sink.SetHeader("Content-Type", "text/event-stream")
		_ = sink.WriteHeader(http.StatusOK)
		_ = sink.Write([]byte("data: {\"choices\":[]}\n\ndata: [DONE]\n\n"))
		return sink.Flush()
	}
	sink.SetHeader("Content-Type", "application/json")
	_ = sink.WriteHeader(http.StatusOK)
	return sink.Write([]byte(`{"id":"g2-ok","choices":[]}`))
}
func (f *fakeG2Orchestrator) Finalize(context.Context, string, service.ExecutionResult) error {
	return nil
}
func (f *fakeG2Orchestrator) Reconcile(context.Context, string) error { return nil }

type fixedAPIKeyResolver struct{}

func (fixedAPIKeyResolver) ResolveKey(_ context.Context, rawSK string) (uint64, uint64, bool) {
	return 3, 7, rawSK == "sk-molin-test"
}

func serveG2Chat(t *testing.T, orchestrator *fakeG2Orchestrator, body string, idempotency string) *httptest.ResponseRecorder {
	t.Helper()
	handler := NewChatHandler(nil).WithOrchestrator(orchestrator)
	var endpoint http.Handler = http.HandlerFunc(handler.ChatCompletions)
	endpoint = middleware.RequireUserAuth("unused", nil, fixedAPIKeyResolver{}, endpoint)
	endpoint = middleware.RequestID(endpoint)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer sk-molin-test")
	if idempotency != "" {
		request.Header.Set("Idempotency-Key", idempotency)
	}
	recorder := httptest.NewRecorder()
	endpoint.ServeHTTP(recorder, request)
	return recorder
}

func TestChatHandlerG2PassesIdentityIdempotencyAndJSON(t *testing.T) {
	orchestrator := &fakeG2Orchestrator{}
	recorder := serveG2Chat(t, orchestrator, `{"model":"molin/qwen-turbo","messages":[{"role":"user","content":"OK"}]}`, "idem-handler-1")
	if recorder.Code != http.StatusOK || !orchestrator.executeCalled {
		t.Fatalf("G2 JSON 链路未执行: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if orchestrator.command.UserID != 3 || orchestrator.command.APIKeyID != 7 || orchestrator.command.IdempotencyKey != "idem-handler-1" || orchestrator.command.RequestID == "" {
		t.Fatalf("Handler 未完整传递鉴权和幂等身份: %+v", orchestrator.command)
	}
	if recorder.Header().Get("X-Request-ID") != orchestrator.command.RequestID {
		t.Fatal("公开 X-Request-ID 必须与正式账本 request_id 一致")
	}
}

func TestChatHandlerG2SSEAndExistingState(t *testing.T) {
	orchestrator := &fakeG2Orchestrator{}
	recorder := serveG2Chat(t, orchestrator, `{"model":"molin/qwen-turbo","stream":true,"messages":[{"role":"user","content":"OK"}]}`, "")
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" || !bytes.Contains(recorder.Body.Bytes(), []byte("data: [DONE]")) {
		t.Fatalf("G2 SSE 响应错误: status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	existing := &fakeG2Orchestrator{prepared: &service.PreparedRequest{RequestID: "req-existing", ExecutionStatus: "running", BillingStatus: "unquoted", Existing: true}}
	recorder = serveG2Chat(t, existing, `{"model":"molin/qwen-turbo","messages":[{"role":"user","content":"OK"}]}`, "idem-existing")
	if recorder.Code != http.StatusAccepted || existing.executeCalled {
		t.Fatalf("重复请求必须只返回已有状态，不得再次调用上游: status=%d", recorder.Code)
	}
	var envelope map[string]interface{}
	if json.Unmarshal(recorder.Body.Bytes(), &envelope) != nil {
		t.Fatal("已有请求状态响应必须是合法 JSON")
	}
}

func TestChatHandlerG2IdempotencyConflict(t *testing.T) {
	orchestrator := &fakeG2Orchestrator{prepareErr: service.ErrIdempotencyConflict}
	recorder := serveG2Chat(t, orchestrator, `{"model":"molin/qwen-turbo","messages":[{"role":"user","content":"OK"}]}`, "idem-conflict")
	if recorder.Code != http.StatusConflict || orchestrator.executeCalled {
		t.Fatalf("幂等冲突必须在上游前拒绝: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !errors.Is(orchestrator.prepareErr, service.ErrIdempotencyConflict) {
		t.Fatal("测试桩错误")
	}
}

func TestChatHandlerG2RejectsEmptyMessagesBeforePrepare(t *testing.T) {
	orchestrator := &fakeG2Orchestrator{}
	recorder := serveG2Chat(t, orchestrator, `{"model":"molin/qwen-turbo","messages":[]}`, "")
	if recorder.Code != http.StatusBadRequest || orchestrator.command.RequestID != "" || orchestrator.executeCalled {
		t.Fatalf("空消息必须在写账本和上游调用前拒绝: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestChatHandlerG2RejectsMultiValueIdempotencyHeader(t *testing.T) {
	orchestrator := &fakeG2Orchestrator{}
	handler := NewChatHandler(nil).WithOrchestrator(orchestrator)
	var endpoint http.Handler = http.HandlerFunc(handler.ChatCompletions)
	endpoint = middleware.RequireUserAuth("unused", nil, fixedAPIKeyResolver{}, endpoint)
	endpoint = middleware.RequestID(endpoint)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"molin/qwen-turbo","messages":[{"role":"user","content":"OK"}]}`))
	request.Header.Set("Authorization", "Bearer sk-molin-test")
	request.Header.Add("Idempotency-Key", "first")
	request.Header.Add("Idempotency-Key", "second")
	recorder := httptest.NewRecorder()
	endpoint.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || orchestrator.command.RequestID != "" {
		t.Fatalf("多值幂等 Header 必须在 Prepare 前拒绝: status=%d", recorder.Code)
	}
}

func TestChatHandlerG2MapsRealNameAndChannelErrors(t *testing.T) {
	for _, testCase := range []struct {
		err      error
		wantHTTP int
		wantCode int
	}{
		{err: service.ErrRealNameRequired, wantHTTP: http.StatusBadRequest, wantCode: 70001},
		{err: service.ErrChannelUnavailable, wantHTTP: http.StatusServiceUnavailable, wantCode: 50300},
	} {
		recorder := serveG2Chat(t, &fakeG2Orchestrator{prepareErr: testCase.err}, `{"model":"molin/qwen-turbo","messages":[{"role":"user","content":"OK"}]}`, "")
		var body response.Body
		decodeErr := json.Unmarshal(recorder.Body.Bytes(), &body)
		if recorder.Code != testCase.wantHTTP || decodeErr != nil || body.Code != testCase.wantCode {
			t.Fatalf("错误映射不符合契约: err=%v status=%d body=%s", testCase.err, recorder.Code, recorder.Body.String())
		}
	}
}

func TestChatHandlerG3MapsStableBillingErrors(t *testing.T) {
	tests := []struct {
		err       error
		wantHTTP  int
		wantCode  int
		wantError string
	}{
		{service.ErrPriceUnavailable, http.StatusServiceUnavailable, 50310, "pricing_unavailable"},
		{service.ErrPriceExpired, http.StatusServiceUnavailable, 50310, "price_expired"},
		{service.ErrMarginBelowMinimum, http.StatusServiceUnavailable, 50311, "margin_below_minimum"},
		{service.ErrUnquotableRequest, http.StatusBadRequest, 40010, "unquotable_request"},
		{service.ErrWalletInsufficient, http.StatusPaymentRequired, 60001, "insufficient_balance"},
		{service.ErrWalletHoldFailed, http.StatusServiceUnavailable, 50312, "wallet_hold_failed"},
		{&service.BillingStatusError{RequestID: "req-pending", Cause: service.ErrSettlementPending}, http.StatusAccepted, 20201, "settlement_pending"},
		{service.ErrBillingException, http.StatusInternalServerError, 50010, "billing_exception"},
	}
	for _, testCase := range tests {
		recorder := httptest.NewRecorder()
		writeOrchestratorError(recorder, testCase.err)
		var body response.Body
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != testCase.wantHTTP || body.Code != testCase.wantCode || body.ErrorType != testCase.wantError {
			t.Fatalf("G3 错误映射不符: err=%v status=%d body=%+v", testCase.err, recorder.Code, body)
		}
		if errors.Is(testCase.err, service.ErrUnquotableRequest) && !strings.Contains(body.Message, "max_tokens 和 n") {
			t.Fatalf("无法报价提示必须指出两个受限参数: %+v", body)
		}
		if errors.Is(testCase.err, service.ErrSettlementPending) && body.RequestID != "req-pending" {
			t.Fatalf("待结算响应必须返回 request_id: %+v", body)
		}
	}
}

func TestChatHandlerG2FailsClosedWithoutOrchestrator(t *testing.T) {
	handler := NewChatHandler(nil)
	var endpoint http.Handler = http.HandlerFunc(handler.ChatCompletions)
	endpoint = middleware.RequireUserAuth("unused", nil, fixedAPIKeyResolver{}, endpoint)
	endpoint = middleware.RequestID(endpoint)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"molin/qwen-turbo","messages":[{"role":"user","content":"OK"}]}`))
	request.Header.Set("Authorization", "Bearer sk-molin-test")
	recorder := httptest.NewRecorder()
	endpoint.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("编排器未装配时必须失败关闭，不能回落旧 ForwardService: status=%d", recorder.Code)
	}
}
