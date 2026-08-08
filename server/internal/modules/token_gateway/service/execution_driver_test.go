package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

func TestBifrostDriver_NonStreamMappingAuthUsageAndRedaction(t *testing.T) {
	var gotModel, gotAuth, gotRequestID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotRequestID = r.Header.Get("X-Request-ID")
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","choices":[{"message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":4}},"cost":99,"extra_fields":{"routing_info":{"key":"secret-name"},"provider_response_headers":{"x":"y"}}}`)
	}))
	defer server.Close()

	driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "internal-test-token", HTTPClient: server.Client()})
	result, err := driver.ChatCompletion(context.Background(), ExecutionRequest{RequestID: "req-bifrost-1", LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{"messages": []interface{}{}}})
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	defer result.Response.Body.Close()
	raw, _ := io.ReadAll(result.Response.Body)
	expectedAuth := "Bearer " + "internal-test-token"
	if gotModel != "bailian/qwen-turbo" || gotAuth != expectedAuth || gotRequestID != "req-bifrost-1" {
		t.Fatalf("映射、内部鉴权或 request_id 错误 model=%q auth=%q request_id=%q", gotModel, gotAuth, gotRequestID)
	}
	if !result.Usage.Present || result.Usage.PromptTokens != 3 || result.Usage.CompletionTokens != 5 || result.Usage.ReasoningTokens != 4 || result.Usage.CachedTokens != 2 {
		t.Fatalf("usage 归一化错误: %+v", result.Usage)
	}
	if bytes.Contains(raw, []byte("extra_fields")) || bytes.Contains(raw, []byte("routing_info")) || bytes.Contains(raw, []byte("secret-name")) || bytes.Contains(raw, []byte("\"cost\"")) {
		t.Fatalf("响应泄露 Bifrost 内部字段: %s", raw)
	}
}

func TestBifrostDriver_PrefersPublishedProviderModel(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		receivedModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "internal", ModelMapping: map[string]string{"molin/demo": "legacy/old"}, HTTPClient: server.Client()})
	resp, err := driver.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "molin/demo", ProviderModel: "openrouter/new-model", EndpointCode: "route:88", Body: map[string]interface{}{"messages": []interface{}{}}})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	defer resp.Response.Body.Close()
	if receivedModel != "openrouter/new-model" {
		t.Fatalf("期望数据库路由优先，实际 %q", receivedModel)
	}
}

func TestBifrostDriver_LegacyModelKeepsFrozenMapping(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		receivedModel, _ = body["model"].(string)
		_, _ = w.Write([]byte(`{"id":"ok","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "internal", ModelMapping: map[string]string{"molin/demo": "legacy/frozen"}, HTTPClient: server.Client()})
	resp, err := driver.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "molin/demo", ProviderModel: "legacy-model-without-provider", EndpointCode: "legacy-channel", Body: map[string]interface{}{"messages": []interface{}{}}})
	if err != nil {
		t.Fatalf("旧模型映射执行失败: %v", err)
	}
	defer resp.Response.Body.Close()
	if receivedModel != "legacy/frozen" {
		t.Fatalf("旧链路必须保留冻结映射，实际 %q", receivedModel)
	}
}

func TestBifrostDriver_RecognizesHTTPAndBodyErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"401", http.StatusUnauthorized, `{"error":{"message":"secret provider stack"}}`},
		{"429", http.StatusTooManyRequests, `{"error":"rate limited"}`},
		{"500", http.StatusInternalServerError, `boom`},
		{"200-error", http.StatusOK, `{"is_bifrost_error":true,"error":{"message":"bad key"},"extra_fields":{"key":"upstream"}}`},
		{"missing-choices", http.StatusOK, `{"id":"bad"}`},
		{"invalid-json", http.StatusOK, `{`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "token", HTTPClient: server.Client()})
			result, err := driver.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
			if err != nil {
				t.Fatalf("HTTP 响应错误应归一化为响应，不应泄露底层错误: %v", err)
			}
			defer result.Response.Body.Close()
			raw, _ := io.ReadAll(result.Response.Body)
			if result.Response.StatusCode < 400 || result.Attempt.Outcome != "failed" {
				t.Fatalf("应识别为失败 status=%d attempt=%+v", result.Response.StatusCode, result.Attempt)
			}
			if strings.Contains(string(raw), "secret provider") || strings.Contains(string(raw), "bad key") || strings.Contains(string(raw), "\"key\":\"upstream\"") {
				t.Fatalf("错误响应泄露内部信息: %s", raw)
			}
		})
	}
}

func TestBifrostDriver_UsageMissingIsExplicit(t *testing.T) {
	for _, responseBody := range []string{
		`{"choices":[{"message":{"content":"OK"}}]}`,
		`{"choices":[{"message":{"content":"OK"}}],"usage":{}}`,
		`{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":1}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, responseBody)
		}))
		driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "token", HTTPClient: server.Client()})
		result, err := driver.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
		server.Close()
		if err != nil || result.Usage.Present {
			t.Fatalf("Usage 缺失或不完整应显式标记 Present=false: body=%s result=%+v err=%v", responseBody, result, err)
		}
	}
}

func TestBifrostDriverPreservesUsageFromErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"provider rejected"},"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer server.Close()
	driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "token", HTTPClient: server.Client()})
	result, err := driver.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempt.Outcome != "failed" || !result.Usage.Present || result.Usage.PromptTokens != 10 || result.Usage.CompletionTokens != 5 {
		t.Fatalf("错误响应中的可信 Usage 必须保留: %+v", result)
	}
}

func TestBifrostDriverPreservesUsageFromSSEErrorEvent(t *testing.T) {
	driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: "http://unused", InternalToken: "token"})
	chunk, err := driver.NormalizeStreamLine([]byte(`data: {"error":{"message":"provider rejected"},"usage":{"prompt_tokens":10,"completion_tokens":5}}`), "molin/qwen-turbo")
	if err == nil || !chunk.Usage.Present || chunk.Usage.PromptTokens != 10 || chunk.Usage.CompletionTokens != 5 {
		t.Fatalf("SSE 错误事件中的可信 Usage 必须随错误返回: chunk=%+v err=%v", chunk, err)
	}
}

func TestExecutionUsageRejectsInconsistentBreakdown(t *testing.T) {
	usage := parseExecutionUsage([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":5,"cached_tokens":11,"reasoning_tokens":6}}`))
	if usage.Present {
		t.Fatalf("缓存或推理数量超过总量时不得视为可信 Usage: %+v", usage)
	}
	usage = parseExecutionUsage([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":16}}`))
	if usage.Present {
		t.Fatalf("total_tokens 与输入输出合计不一致时不得视为可信 Usage: %+v", usage)
	}
}

func TestBifrostDriver_ChatCompletionStreamInjectsUsageAndReadsSSE(t *testing.T) {
	var includeUsage bool
	var streamRequested bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		streamRequested, _ = body["stream"].(bool)
		if options, ok := body["stream_options"].(map[string]interface{}); ok {
			includeUsage, _ = options["include_usage"].(bool)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n"+
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n"+
			"data: [DONE]\n\n")
	}))
	defer server.Close()
	driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "token", HTTPClient: server.Client()})
	result, err := driver.ChatCompletionStream(context.Background(), ExecutionRequest{LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if err != nil {
		t.Fatalf("Bifrost HTTP SSE 调用失败: %v", err)
	}
	defer result.Response.Body.Close()
	usage, err := readExecutionStreamForTest(result.Response.Body, func(line []byte) (ExecutionStreamChunk, error) {
		return driver.NormalizeStreamLine(line, "molin/qwen-turbo")
	})
	if err != nil || !streamRequested || !includeUsage || !usage.Present || usage.TotalTokens != 3 {
		t.Fatalf("Bifrost HTTP SSE 契约错误 stream=%v include_usage=%v usage=%+v err=%v", streamRequested, includeUsage, usage, err)
	}
}

func TestBifrostDriver_SSESuccessAndMidstreamError(t *testing.T) {
	driver := NewBifrostDriver(BifrostDriverConfig{})
	chunk, err := driver.NormalizeStreamLine([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5,\"extra_fields\":{\"key\":\"secret\"}}}\n"), "molin/qwen-turbo")
	if err != nil || !chunk.Usage.Present || chunk.Usage.TotalTokens != 5 || bytes.Contains(chunk.PublicLine, []byte("secret")) {
		t.Fatalf("SSE usage 或脱敏错误: chunk=%+v err=%v", chunk, err)
	}
	_, err = driver.NormalizeStreamLine([]byte("data: {\"is_bifrost_error\":true,\"error\":{\"message\":\"provider failed\"}}\n"), "molin/qwen-turbo")
	if err == nil {
		t.Fatal("SSE 中途业务错误必须被识别")
	}
	usage, err := readExecutionStreamForTest(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"O\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\ndata: [DONE]\n\n"), func(line []byte) (ExecutionStreamChunk, error) {
		return driver.NormalizeStreamLine(line, "molin/qwen-turbo")
	})
	if err != nil || !usage.Present || usage.TotalTokens != 2 {
		t.Fatalf("SSE 完整读取失败 usage=%+v err=%v", usage, err)
	}
	if _, err := driver.NormalizeStreamLine([]byte(`{"is_bifrost_error":true,"error":{"message":"stream failed"}}`), "molin/qwen-turbo"); err == nil {
		t.Fatal("HTTP 200 普通 JSON 流式错误必须被识别，不能作为 SSE 透传")
	}
	metadata, err := driver.NormalizeStreamLine([]byte(": routing_info=secret\n"), "molin/qwen-turbo")
	if err != nil || len(metadata.PublicLine) != 0 {
		t.Fatalf("Bifrost 非 data 扩展行必须丢弃: chunk=%+v err=%v", metadata, err)
	}
}

func TestBifrostDriver_TimeoutAndNoAutomaticFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer server.Close()
	client := &http.Client{Timeout: 20 * time.Millisecond}
	driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "token", HTTPClient: client})
	result, err := driver.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if err == nil || !isTimeout(err) {
		t.Fatalf("应返回超时且不得 fallback: %v", err)
	}
	if result == nil || !result.Attempt.ResultUnknown || result.Attempt.Outcome != "timeout" || result.Attempt.ErrorClass != "network_timeout" {
		t.Fatalf("超时也必须返回独立且结果未知的 attempt: %+v", result)
	}
	_, err = driver.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "unknown/model", Body: map[string]interface{}{}})
	if err == nil {
		t.Fatal("未映射模型必须拒绝，不能自动解析 provider")
	}
}

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("连接前失败")
}

func TestExecutionDriversReleaseWhenRequestWasNotSent(t *testing.T) {
	client := &http.Client{Transport: failingRoundTripper{}}
	tests := []ExecutionDriver{
		NewNativeOpenAICompatibleDriver(client),
		NewBifrostDriver(BifrostDriverConfig{BaseURL: "http://unreachable.invalid", InternalToken: "test-token", HTTPClient: client}),
	}
	for _, driver := range tests {
		result, err := driver.ChatCompletion(context.Background(), ExecutionRequest{
			RequestID: "req-not-sent", LogicalModel: "molin/qwen-turbo", ProviderModel: "qwen-turbo",
			BaseURL: "http://unreachable.invalid", APIKey: "test-key", Body: map[string]interface{}{},
		})
		if err == nil || result == nil {
			t.Fatalf("%s 应返回建连前失败", driver.Name())
		}
		if result.Attempt.ResultUnknown || result.Attempt.ErrorClass != "request_not_sent" {
			t.Fatalf("%s 建连前失败必须可安全释放: %+v", driver.Name(), result.Attempt)
		}
	}
}

type memoryUsageLogWriter struct {
	logs   []*model.TokenUsageLog
	ctxErr error
	err    error
}

func (w *memoryUsageLogWriter) Create(ctx context.Context, usageLog *model.TokenUsageLog) error {
	w.ctxErr = ctx.Err()
	if w.err != nil {
		return w.err
	}
	w.logs = append(w.logs, usageLog)
	return nil
}

func TestForwardJSON_LogFailurePreventsResponseAndSettlement(t *testing.T) {
	usageWriter := &memoryUsageLogWriter{err: errors.New("数据库不可用")}
	reporter := &fakeReporter{charged: map[string]decimal.Decimal{}}
	service := &ForwardService{usageRepo: usageWriter, reporter: reporter}
	productID := uint64(8)
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)), Header: make(http.Header)}
	recorder := httptest.NewRecorder()
	bill := &billDecision{}
	err := service.forwardJSON(context.Background(), recorder, response, ExecutionUsage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2, Present: true}, ForwardInput{RequestID: "req-log-failed", UserID: 1, Model: "molin/qwen-turbo"}, &model.TokenModel{ProductID: &productID, Modality: "chat"}, "req-log-failed", bill)
	if !errors.Is(err, ErrUpstream) || recorder.Body.Len() != 0 || len(reporter.events) != 0 || bill.settled {
		t.Fatalf("终态日志失败时不得返回成功或结算: err=%v body=%q events=%d bill=%+v", err, recorder.Body.String(), len(reporter.events), bill)
	}
}

func TestForwardStream_LogFailurePreventsSettlement(t *testing.T) {
	usageWriter := &memoryUsageLogWriter{err: errors.New("数据库不可用")}
	reporter := &fakeReporter{charged: map[string]decimal.Decimal{}}
	service := &ForwardService{usageRepo: usageWriter, reporter: reporter}
	driver := NewBifrostDriver(BifrostDriverConfig{})
	productID := uint64(8)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream)), Header: make(http.Header)}
	recorder := httptest.NewRecorder()
	attempt := &ExecutionAttempt{}
	bill := &billDecision{}

	err := service.forwardStream(context.Background(), recorder, response, driver, attempt, ForwardInput{RequestID: "req-stream-log-failed", UserID: 1, Model: "molin/qwen-turbo", Stream: true}, &model.TokenModel{ProductID: &productID, Modality: "chat"}, "req-stream-log-failed", bill)
	if err != nil {
		t.Fatalf("SSE 已提交后日志失败不应追加第二个 HTTP 错误: %v", err)
	}
	if len(reporter.events) != 0 || bill.settled {
		t.Fatalf("流式终态日志失败时不得结算: events=%d bill=%+v", len(reporter.events), bill)
	}
	if attempt.Outcome != "pending_reconcile" || attempt.ResultUnknown {
		t.Fatalf("模型结果已知但计费日志失败时应进入待对账: %+v", attempt)
	}
}

type disconnectingResponseWriter struct {
	header http.Header
	writes int
}

type holdOpenAfterPayloadReader struct {
	payload []byte
	sent    bool
	closed  chan struct{}
}

func (r *holdOpenAfterPayloadReader) Read(buffer []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(buffer, r.payload), nil
	}
	<-r.closed
	return 0, io.EOF
}

func (r *holdOpenAfterPayloadReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func (w *disconnectingResponseWriter) Header() http.Header { return w.header }
func (w *disconnectingResponseWriter) WriteHeader(_ int)   {}
func (w *disconnectingResponseWriter) Flush()              {}
func (w *disconnectingResponseWriter) Write(_ []byte) (int, error) {
	w.writes++
	return 0, errors.New("客户端已断开")
}

func TestForwardStream_ClientDisconnectStillReadsFinalUsage(t *testing.T) {
	usageWriter := &memoryUsageLogWriter{}
	service := &ForwardService{usageRepo: usageWriter}
	driver := NewBifrostDriver(BifrostDriverConfig{})
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"O\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":9}}\n\n" +
		"data: [DONE]\n\n"
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream)), Header: make(http.Header)}
	w := &disconnectingResponseWriter{header: make(http.Header)}
	attempt := &ExecutionAttempt{}
	bill := &billDecision{}
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	err := service.forwardStream(canceledContext, w, response, driver, attempt, ForwardInput{RequestID: "req-disconnect", UserID: 1, Model: "molin/qwen-turbo", Stream: true}, &model.TokenModel{Modality: "chat"}, "req-disconnect", bill)
	if err != nil {
		t.Fatalf("客户端断开后服务端应继续读取并完成处理: %v", err)
	}
	if w.writes != 1 {
		t.Fatalf("客户端断开后必须停止继续写，实际写入次数=%d", w.writes)
	}
	if len(usageWriter.logs) != 1 || usageWriter.logs[0].InputTokens != 7 || usageWriter.logs[0].OutputTokens != 9 || usageWriter.logs[0].Status != "success" {
		t.Fatalf("客户端断开后未读取末尾 Usage: %+v", usageWriter.logs)
	}
	if usageWriter.ctxErr != nil {
		t.Fatalf("客户端 Context 取消后终态日志必须使用独立 Context: %v", usageWriter.ctxErr)
	}
	if attempt.Outcome != "success" || attempt.ResultUnknown {
		t.Fatalf("拿到末尾 Usage 后 attempt 应成功: %+v", attempt)
	}
}

func TestForwardStream_UsageWithoutDoneRemainsPending(t *testing.T) {
	usageWriter := &memoryUsageLogWriter{}
	service := &ForwardService{usageRepo: usageWriter}
	driver := NewBifrostDriver(BifrostDriverConfig{})
	stream := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":9}}\n\n"
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(stream)), Header: make(http.Header)}
	attempt := &ExecutionAttempt{}
	bill := &billDecision{}
	err := service.forwardStream(context.Background(), httptest.NewRecorder(), response, driver, attempt, ForwardInput{RequestID: "req-no-done", UserID: 1, Model: "molin/qwen-turbo", Stream: true}, &model.TokenModel{Modality: "chat"}, "req-no-done", bill)
	if err != nil {
		t.Fatalf("不完整流应记录待对账而不是返回二次 HTTP 错误: %v", err)
	}
	if len(usageWriter.logs) != 1 || usageWriter.logs[0].Status != "pending_reconcile" || usageWriter.logs[0].ErrorCode == nil || *usageWriter.logs[0].ErrorCode != "upstream_stream_incomplete" {
		t.Fatalf("缺少 [DONE] 必须进入 pending_reconcile: %+v", usageWriter.logs)
	}
	if attempt.Outcome != "pending_reconcile" || !attempt.ResultUnknown || bill.settled {
		t.Fatalf("缺少 [DONE] 不得成功结算: attempt=%+v bill=%+v", attempt, bill)
	}
}

func TestForwardStream_DoneStopsWithoutWaitingForConnectionClose(t *testing.T) {
	usageWriter := &memoryUsageLogWriter{}
	service := &ForwardService{usageRepo: usageWriter}
	driver := NewBifrostDriver(BifrostDriverConfig{})
	reader := &holdOpenAfterPayloadReader{
		payload: []byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}\n\ndata: [DONE]\n"),
		closed:  make(chan struct{}),
	}
	defer reader.Close()
	response := &http.Response{StatusCode: http.StatusOK, Body: reader, Header: make(http.Header)}
	attempt := &ExecutionAttempt{}
	result := make(chan error, 1)
	go func() {
		result <- service.forwardStream(context.Background(), httptest.NewRecorder(), response, driver, attempt, ForwardInput{RequestID: "req-done-open", UserID: 1, Model: "molin/qwen-turbo", Stream: true}, &model.TokenModel{Modality: "chat"}, "req-done-open", &billDecision{})
	}()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("收到 [DONE] 后应正常结束: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("收到 [DONE] 后仍等待上游关闭连接")
	}
	if len(usageWriter.logs) != 1 || usageWriter.logs[0].Status != "success" || attempt.Outcome != "success" {
		t.Fatalf("收到 [DONE] 和完整 Usage 后应成功结算: logs=%+v attempt=%+v", usageWriter.logs, attempt)
	}
}

func TestForwardStream_FirstBusinessErrorDoesNotCommitSSEHeaders(t *testing.T) {
	usageWriter := &memoryUsageLogWriter{}
	service := &ForwardService{usageRepo: usageWriter}
	driver := NewBifrostDriver(BifrostDriverConfig{})
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"is_bifrost_error":true,"error":{"message":"secret stack"}}`)), Header: make(http.Header)}
	recorder := httptest.NewRecorder()
	attempt := &ExecutionAttempt{}
	err := service.forwardStream(context.Background(), recorder, response, driver, attempt, ForwardInput{RequestID: "req-first-error", UserID: 1, Model: "molin/qwen-turbo", Stream: true}, &model.TokenModel{Modality: "chat"}, "req-first-error", &billDecision{})
	if !errors.Is(err, ErrUpstream) || recorder.Flushed || recorder.Body.Len() != 0 {
		t.Fatalf("首事件业务错误应在提交 SSE 200 前返回统一错误: err=%v flushed=%v body=%q", err, recorder.Flushed, recorder.Body.String())
	}
	if len(usageWriter.logs) != 1 || usageWriter.logs[0].Status != "failed" {
		t.Fatalf("首事件业务错误必须记录失败: %+v", usageWriter.logs)
	}
}

func readExecutionStreamForTest(reader io.Reader, normalize func([]byte) (ExecutionStreamChunk, error)) (ExecutionUsage, error) {
	buf := bufio.NewReader(reader)
	var usage ExecutionUsage
	for {
		line, err := buf.ReadBytes('\n')
		if len(line) > 0 {
			chunk, normalizeErr := normalize(line)
			if normalizeErr != nil {
				return usage, normalizeErr
			}
			if chunk.Usage.Present {
				usage = chunk.Usage
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return usage, nil
			}
			return usage, err
		}
	}
}

func TestBifrostDriver_ClientCancellationReturnsUnknownWithoutFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	driver := NewBifrostDriver(BifrostDriverConfig{BaseURL: server.URL, InternalToken: "token", HTTPClient: server.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := driver.ChatCompletionStream(ctx, ExecutionRequest{LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if err == nil {
		t.Fatal("客户端取消后必须返回结果未知错误，不能自动 fallback")
	}
}

func TestNativeDriver_PreservesCompatibleResponse(t *testing.T) {
	upstream := `{"choices":[{"message":{"content":"OK"},"vendor_text":"网络赌博推广"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"extra_fields":{"native":"internal"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-key" {
			t.Fatal("原生驱动未传递渠道密钥")
		}
		_, _ = io.WriteString(w, upstream)
	}))
	defer server.Close()
	driver := NewNativeOpenAICompatibleDriver(server.Client())
	result, err := driver.ChatCompletion(context.Background(), ExecutionRequest{ProviderModel: "provider/model", BaseURL: server.URL, APIKey: "provider-key", Body: map[string]interface{}{}})
	if err != nil {
		t.Fatalf("原生驱动调用失败: %v", err)
	}
	defer result.Response.Body.Close()
	raw, _ := io.ReadAll(result.Response.Body)
	if bytes.Contains(raw, []byte("extra_fields")) || bytes.Contains(raw, []byte("internal")) || bytes.Contains(raw, []byte("vendor_text")) || bytes.Contains(raw, []byte("网络赌博推广")) || !result.Usage.Present || result.Usage.TotalTokens != 3 {
		t.Fatalf("原生兼容响应回归 raw=%s usage=%+v", raw, result.Usage)
	}
}

func TestNativeDriver_RemovesUnknownChoiceFieldsFromSSE(t *testing.T) {
	driver := NewNativeOpenAICompatibleDriver(&http.Client{})
	chunk, err := driver.NormalizeStreamLine([]byte(`data: {"choices":[{"index":0,"delta":{"content":"OK"},"vendor_text":"网络赌博推广"}]}`+"\n"), "molin/qwen-turbo")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(chunk.PublicLine, []byte("vendor_text")) || bytes.Contains(chunk.PublicLine, []byte("网络赌博推广")) || !bytes.Contains(chunk.PublicLine, []byte(`"content":"OK"`)) {
		t.Fatalf("SSE 只能公开兼容 choice 字段: %s", chunk.PublicLine)
	}
}

func TestExecutionDriverFreezesNestedMessageAndDeltaFields(t *testing.T) {
	driver := NewNativeOpenAICompatibleDriver(&http.Client{})
	line := []byte(`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"OK","vendor_trace":"secret","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}","provider_id":"secret"},"provider_call":"secret"}]}}]}` + "\n")
	chunk, err := driver.NormalizeStreamLine(line, "molin/qwen-turbo")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("vendor_trace"), []byte("provider_id"), []byte("provider_call"), []byte("secret")} {
		if bytes.Contains(chunk.PublicLine, forbidden) {
			t.Fatalf("SSE 嵌套私有字段不得公开: %s", chunk.PublicLine)
		}
	}
	for _, required := range [][]byte{[]byte(`"content":"OK"`), []byte(`"name":"lookup"`), []byte(`"arguments":"{}"`)} {
		if !bytes.Contains(chunk.PublicLine, required) {
			t.Fatalf("标准嵌套字段必须保留: %s", chunk.PublicLine)
		}
	}

	value := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"message": map[string]interface{}{
				"role": "assistant", "content": "OK", "private": "secret",
				"annotations": []interface{}{map[string]interface{}{
					"type": "url_citation", "private": "secret",
					"url_citation": map[string]interface{}{"start_index": 0, "end_index": 2, "title": "文档", "url": "https://example.com", "provider_rank": 1},
				}},
			},
		}},
	}
	sanitizeExecutionResponse(value, "molin/qwen-turbo")
	raw, _ := json.Marshal(value)
	if bytes.Contains(raw, []byte("private")) || bytes.Contains(raw, []byte("provider_rank")) || bytes.Contains(raw, []byte("secret")) {
		t.Fatalf("非流式嵌套私有字段不得公开: %s", raw)
	}
	if !bytes.Contains(raw, []byte("url_citation")) || !bytes.Contains(raw, []byte("https://example.com")) {
		t.Fatalf("标准 URL 引用字段必须保留: %s", raw)
	}
}

func TestExecutionDriverFreezesUsageAndLogprobsFields(t *testing.T) {
	value := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"index": 0,
			"logprobs": map[string]interface{}{
				"provider_trace": "secret",
				"content": []interface{}{map[string]interface{}{
					"token": "OK", "logprob": -0.1, "bytes": []interface{}{79, 75}, "provider_rank": 1,
					"top_logprobs": []interface{}{map[string]interface{}{"token": "OK", "logprob": -0.1, "bytes": []interface{}{79, 75}, "route": "secret"}},
				}},
			},
		}},
		"usage": map[string]interface{}{
			"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3, "internal_route": "secret",
			"prompt_tokens_details":     map[string]interface{}{"cached_tokens": 1, "provider_cache_id": "secret"},
			"completion_tokens_details": map[string]interface{}{"reasoning_tokens": 1, "provider_cost": "secret"},
		},
	}
	sanitizeExecutionResponse(value, "molin/qwen-turbo")
	raw, _ := json.Marshal(value)
	for _, forbidden := range [][]byte{[]byte("provider_trace"), []byte("provider_rank"), []byte("internal_route"), []byte("provider_cache_id"), []byte("provider_cost"), []byte("secret"), []byte(`"route"`)} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("Usage 与 logprobs 私有字段不得公开: %s", raw)
		}
	}
	for _, required := range [][]byte{[]byte(`"total_tokens":3`), []byte(`"cached_tokens":1`), []byte(`"reasoning_tokens":1`), []byte(`"token":"OK"`)} {
		if !bytes.Contains(raw, required) {
			t.Fatalf("冻结的兼容字段必须保留: %s", raw)
		}
	}
}

func TestExecutionDriverDropsMalformedUsageAndLogprobsTypes(t *testing.T) {
	value := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"message":  map[string]interface{}{"role": "assistant", "content": "OK"},
			"logprobs": "provider-private-data",
		}},
		"usage": map[string]interface{}{
			"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2,
			"prompt_tokens_details":     "provider-private-data",
			"completion_tokens_details": map[string]interface{}{"reasoning_tokens": map[string]interface{}{"trace": "private"}},
		},
	}
	sanitizeExecutionResponse(value, "molin/qwen-turbo")
	raw, _ := json.Marshal(value)
	for _, forbidden := range [][]byte{[]byte("provider-private-data"), []byte("trace"), []byte("private"), []byte("logprobs"), []byte("prompt_tokens_details"), []byte("reasoning_tokens")} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("异常类型不得绕过嵌套白名单: %s", raw)
		}
	}
	if !bytes.Contains(raw, []byte(`"total_tokens":2`)) {
		t.Fatalf("合法 Usage 标量应继续保留: %s", raw)
	}

	value["usage"] = "provider-private-data"
	sanitizeExecutionResponse(value, "molin/qwen-turbo")
	if _, exists := value["usage"]; exists {
		t.Fatal("非对象 Usage 必须整体删除")
	}
}

func TestExecutionDriverDropsMalformedNestedMessageAndLogprobsTypes(t *testing.T) {
	value := map[string]interface{}{
		"choices": []interface{}{map[string]interface{}{
			"message": map[string]interface{}{
				"role":        "assistant",
				"content":     []interface{}{map[string]interface{}{"type": "text", "text": "OK"}, "provider-private-content"},
				"tool_calls":  []interface{}{map[string]interface{}{"id": "call-1", "type": "function", "function": map[string]interface{}{"name": "ok", "arguments": "{}"}}, "provider-private-call"},
				"annotations": []interface{}{map[string]interface{}{"type": "url_citation", "url_citation": "provider-private-citation"}},
			},
			"logprobs": map[string]interface{}{
				"content": []interface{}{map[string]interface{}{"token": "OK", "logprob": -0.1}, "provider-private-logprob"},
				"refusal": "provider-private-refusal",
			},
		}},
	}
	sanitizeExecutionResponse(value, "molin/qwen-turbo")
	raw, _ := json.Marshal(value)
	for _, forbidden := range [][]byte{[]byte("provider-private"), []byte(`"content"`), []byte(`"tool_calls"`), []byte(`"refusal"`)} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("畸形深层结构必须整体丢弃对应字段: %s", raw)
		}
	}
}

func TestExecutionDriverDropsMalformedOuterResponseTypes(t *testing.T) {
	value := map[string]interface{}{
		"id":                 map[string]interface{}{"private": "provider-private-id"},
		"created":            "provider-private-created",
		"system_fingerprint": map[string]interface{}{"private": "provider-private-fingerprint"},
		"service_tier":       []interface{}{"provider-private-tier"},
		"choices": []interface{}{map[string]interface{}{
			"index":         map[string]interface{}{"private": "provider-private-index"},
			"text":          map[string]interface{}{"private": "provider-private-text"},
			"finish_reason": []interface{}{"provider-private-finish"},
			"message":       "provider-private-message",
			"delta":         []interface{}{"provider-private-delta"},
		}},
	}
	sanitizeExecutionResponse(value, "molin/qwen-turbo")
	raw, _ := json.Marshal(value)
	if bytes.Contains(raw, []byte("provider-private")) {
		t.Fatalf("畸形外层与 choice 字段不得绕过响应白名单: %s", raw)
	}
	for _, key := range []string{"id", "created", "system_fingerprint", "service_tier"} {
		if _, exists := value[key]; exists {
			t.Fatalf("畸形顶层字段必须删除 key=%s raw=%s", key, raw)
		}
	}

	value["choices"] = []interface{}{map[string]interface{}{"message": map[string]interface{}{"role": "assistant", "content": "OK"}}, "provider-private-choice"}
	sanitizeExecutionResponse(value, "molin/qwen-turbo")
	if _, exists := value["choices"]; exists {
		t.Fatal("choices 含非对象元素时必须整体删除")
	}
}

func TestNativeDriver_RecognizesBusinessAndProtocolErrors(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"provider secret"}}`,
		`{"choices":[]}`,
		`{"choices":["网络赌博推广"]}`,
		`{"id":"missing-choices"}`,
		`{`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		driver := NewNativeOpenAICompatibleDriver(server.Client())
		result, err := driver.ChatCompletion(context.Background(), ExecutionRequest{ProviderModel: "provider/model", BaseURL: server.URL, APIKey: "provider-key", Body: map[string]interface{}{}})
		server.Close()
		if err != nil {
			t.Fatalf("Native 协议错误应归一化为受控响应: %v", err)
		}
		defer result.Response.Body.Close()
		raw, _ := io.ReadAll(result.Response.Body)
		if result.Response.StatusCode != http.StatusBadGateway || result.Attempt.Outcome != "failed" || bytes.Contains(raw, []byte("provider secret")) {
			t.Fatalf("Native 必须与 Bifrost 使用同一错误和脱敏契约: status=%d attempt=%+v body=%s", result.Response.StatusCode, result.Attempt, raw)
		}
	}
}

func TestNativeDriverRejectsNonObjectChoiceInSSE(t *testing.T) {
	driver := NewNativeOpenAICompatibleDriver(&http.Client{})
	if _, err := driver.NormalizeStreamLine([]byte(`data: {"choices":["网络赌博推广"]}`+"\n"), "molin/qwen-turbo"); err == nil {
		t.Fatal("非对象 choice 必须作为畸形上游协议拒绝，不能进入公开 SSE")
	}
}

func TestNativeAndBifrostDriversExposeEquivalentStandardResponse(t *testing.T) {
	standardResponse := `{"id":"equivalent","model":"provider/private-model","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":4}}}`
	nativeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, standardResponse)
	}))
	defer nativeServer.Close()
	bifrostServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var payload map[string]interface{}
		_ = json.Unmarshal([]byte(standardResponse), &payload)
		payload["extra_fields"] = map[string]interface{}{"routing_info": map[string]interface{}{"key": "internal"}}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer bifrostServer.Close()

	native := NewNativeOpenAICompatibleDriver(nativeServer.Client())
	bifrost := NewBifrostDriver(BifrostDriverConfig{BaseURL: bifrostServer.URL, InternalToken: "internal-token", HTTPClient: bifrostServer.Client()})
	nativeResult, err := native.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "molin/qwen-turbo", ProviderModel: "provider/model", BaseURL: nativeServer.URL, APIKey: "provider-key", Body: map[string]interface{}{}})
	if err != nil {
		t.Fatalf("Native 等价契约调用失败: %v", err)
	}
	defer nativeResult.Response.Body.Close()
	bifrostResult, err := bifrost.ChatCompletion(context.Background(), ExecutionRequest{LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if err != nil {
		t.Fatalf("Bifrost 等价契约调用失败: %v", err)
	}
	defer bifrostResult.Response.Body.Close()

	var nativePublic, bifrostPublic map[string]interface{}
	if err := json.NewDecoder(nativeResult.Response.Body).Decode(&nativePublic); err != nil {
		t.Fatalf("解析 Native 公开响应失败: %v", err)
	}
	if err := json.NewDecoder(bifrostResult.Response.Body).Decode(&bifrostPublic); err != nil {
		t.Fatalf("解析 Bifrost 公开响应失败: %v", err)
	}
	if !reflect.DeepEqual(nativePublic, bifrostPublic) {
		t.Fatalf("Native 与 Bifrost 的标准公开响应不等价: native=%v bifrost=%v", nativePublic, bifrostPublic)
	}
	if nativePublic["model"] != "molin/qwen-turbo" || bifrostPublic["model"] != "molin/qwen-turbo" {
		t.Fatalf("公开响应只能返回墨灵逻辑模型，不能泄露执行模型: native=%v bifrost=%v", nativePublic["model"], bifrostPublic["model"])
	}
	if nativeResult.Usage != bifrostResult.Usage || !nativeResult.Usage.Present || nativeResult.Usage.ReasoningTokens != 4 || nativeResult.Usage.CachedTokens != 2 {
		t.Fatalf("Native 与 Bifrost 的标准 Usage 不等价: native=%+v bifrost=%+v", nativeResult.Usage, bifrostResult.Usage)
	}
}

func TestNativeDriver_PreservesSSEAndUsage(t *testing.T) {
	driver := NewNativeOpenAICompatibleDriver(&http.Client{})
	line := []byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":6}}\n")
	chunk, err := driver.NormalizeStreamLine(line, "molin/qwen-turbo")
	if err != nil || !chunk.Usage.Present || chunk.Usage.TotalTokens != 10 {
		t.Fatalf("Native SSE 行为回归: chunk=%+v err=%v", chunk, err)
	}
	var publicEvent map[string]interface{}
	publicData := bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(chunk.PublicLine), []byte("data:")))
	if err := json.Unmarshal(publicData, &publicEvent); err != nil {
		t.Fatalf("Native SSE 公开事件必须保持合法 JSON: %v", err)
	}
	if _, ok := publicEvent["choices"]; !ok {
		t.Fatalf("Native SSE 标准 choices 字段丢失: %v", publicEvent)
	}
	if publicEvent["model"] != "molin/qwen-turbo" {
		t.Fatalf("Native SSE 必须公开墨灵逻辑模型: %v", publicEvent)
	}
	done, err := driver.NormalizeStreamLine([]byte("data: [DONE]\n"), "molin/qwen-turbo")
	if err != nil || !done.Done {
		t.Fatalf("Native SSE 必须识别 [DONE]: chunk=%+v err=%v", done, err)
	}
	if _, err := driver.NormalizeStreamLine([]byte(`data: {"error":{"message":"secret"}}`+"\n"), "molin/qwen-turbo"); err == nil {
		t.Fatal("Native SSE 业务错误必须与 Bifrost 一样被拒绝")
	}
}

func TestStaticDriverSelector_DoesNotFallback(t *testing.T) {
	driver := NewBifrostDriver(BifrostDriverConfig{})
	selector := staticExecutionDriverSelector{driver: driver}
	selected, err := selector.Select("molin/qwen-turbo")
	if err != nil || selected != driver {
		t.Fatalf("选择器必须固定返回一次选择结果: %v", err)
	}
	if _, err := (staticExecutionDriverSelector{}).Select("molin/qwen-turbo"); err == nil {
		t.Fatal("未配置驱动必须失败")
	}
}

func TestExecutionAttemptLedgerStateMapping(t *testing.T) {
	tests := []struct {
		outcome       string
		resultUnknown bool
		attemptStatus string
		requestStatus string
	}{
		{outcome: "success", attemptStatus: "succeeded", requestStatus: model.AIExecutionSucceeded},
		{outcome: "failed", attemptStatus: "failed", requestStatus: model.AIExecutionFailed},
		{outcome: "failed", resultUnknown: true, attemptStatus: "failed", requestStatus: model.AIExecutionUnknown},
		{outcome: "timeout", resultUnknown: true, attemptStatus: "timeout", requestStatus: model.AIExecutionUnknown},
		{outcome: "pending_reconcile", resultUnknown: true, attemptStatus: "unknown", requestStatus: model.AIExecutionUnknown},
		{outcome: "running", attemptStatus: "running", requestStatus: model.AIExecutionRunning},
	}
	for _, tc := range tests {
		attempt := ExecutionAttempt{Outcome: tc.outcome, ResultUnknown: tc.resultUnknown}
		if attempt.LedgerStatus() != tc.attemptStatus || attempt.RequestExecutionStatus() != tc.requestStatus {
			t.Fatalf("运行态映射错误 outcome=%s ledger=%s request=%s", tc.outcome, attempt.LedgerStatus(), attempt.RequestExecutionStatus())
		}
	}

	started := time.Now().Add(-20 * time.Millisecond)
	finished := time.Now()
	attempt := ExecutionAttempt{
		AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", EndpointCode: "bifrost/bailian",
		ProviderModel: "bailian/qwen-turbo", StartedAt: started, FinishedAt: finished, Outcome: "success",
	}
	ledger := attempt.ToLedgerModel("req-ledger-map", ExecutionUsage{PromptTokens: 3, CompletionTokens: 5, ReasoningTokens: 2, CachedTokens: 1, Present: true})
	if ledger.RequestID != "req-ledger-map" || ledger.Status != "succeeded" || ledger.ProviderCode != "bailian" || ledger.EndpointCode == nil || *ledger.EndpointCode != "bifrost/bailian" {
		t.Fatalf("执行尝试持久化映射缺字段: %+v", ledger)
	}
	if ledger.PromptTokens == nil || *ledger.PromptTokens != 3 || ledger.CompletionTokens == nil || *ledger.CompletionTokens != 5 || ledger.LatencyMS == nil {
		t.Fatalf("执行尝试 Usage 或耗时映射错误: %+v", ledger)
	}
	clockSkewAttempt := ExecutionAttempt{StartedAt: finished, FinishedAt: started}
	clockSkewLedger := clockSkewAttempt.ToLedgerModel("req-clock-skew", ExecutionUsage{})
	if clockSkewLedger.LatencyMS == nil || *clockSkewLedger.LatencyMS != 0 {
		t.Fatalf("时钟回拨时耗时必须归零，不能发生无符号整数溢出: %+v", clockSkewLedger)
	}
}

func TestForwardService_ExecutionDriverDefaultsAndValidation(t *testing.T) {
	service := &ForwardService{httpClient: &http.Client{Timeout: time.Second}}
	if err := service.ConfigureExecutionDriver("", "", ""); err != nil {
		t.Fatalf("空配置必须安全回退到 native: %v", err)
	}
	driver, err := service.driverSelector.Select("any")
	if err != nil || driver.Name() != "native" {
		t.Fatalf("默认驱动必须为 native: driver=%v err=%v", driver, err)
	}
	native, ok := driver.(*NativeOpenAICompatibleDriver)
	if !ok || native.streamClient == nil || native.streamClient.Timeout != 0 {
		t.Fatalf("流式驱动必须使用无整体 Timeout 的专用 HTTP Client: %#v", driver)
	}
	if err := service.ConfigureExecutionDriver("bifrost", "http://127.0.0.1:18080", ""); err == nil {
		t.Fatal("Bifrost 缺少内部 Token 时必须拒绝配置")
	}
	if err := service.ConfigureExecutionDriver("bifrost", "http://127.0.0.1:18080", strings.Repeat("a", 32)); err != nil {
		t.Fatalf("合法 Bifrost 内部 Token 应允许配置: %v", err)
	}
	if err := service.ConfigureExecutionDriver("bifrost", "http://127.0.0.1:18080", strings.Repeat("a", 31)+"\""); err == nil {
		t.Fatal("含模板危险字符的 Bifrost 内部 Token 必须拒绝")
	}
}

func TestBifrostFrozenModelsMatchInfrastructureConfig(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", ".."))
	raw, err := os.ReadFile(filepath.Join(repoRoot, "infra", "bifrost", "config.json"))
	if err != nil {
		t.Fatalf("读取 Bifrost 配置失败: %v", err)
	}
	var config struct {
		Providers map[string]struct {
			Keys []struct {
				Value  string   `json:"value"`
				Models []string `json:"models"`
			} `json:"keys"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("解析 Bifrost 配置失败: %v", err)
	}
	for provider, expectedEnv := range map[string]string{
		"bailian":    "env.BAILIAN_API_KEY",
		"openrouter": "env.OPENROUTER_API_KEY",
	} {
		providerConfig, exists := config.Providers[provider]
		if !exists || len(providerConfig.Keys) == 0 {
			t.Fatalf("G1 双上游配置缺少 Provider: %s", provider)
		}
		for _, key := range providerConfig.Keys {
			if key.Value != expectedEnv {
				t.Fatalf("Provider %s 必须只引用受限环境变量，实际为 %q", provider, key.Value)
			}
		}
	}
	for logicalModel, mappedModel := range DefaultBifrostModelMapping() {
		provider, modelName, ok := strings.Cut(mappedModel, "/")
		if !ok {
			t.Fatalf("冻结映射缺少 Provider: %s=%s", logicalModel, mappedModel)
		}
		providerConfig, exists := config.Providers[provider]
		if !exists || !providerAllowsModel(providerConfig.Keys, modelName) {
			t.Fatalf("Bifrost 配置未允许冻结模型: %s=%s", logicalModel, mappedModel)
		}
	}

	templateRaw, err := os.ReadFile(filepath.Join(repoRoot, "infra", "bifrost", "nginx.conf.template"))
	if err != nil {
		t.Fatalf("读取 Bifrost Nginx 模板失败: %v", err)
	}
	templateText := string(templateRaw)
	for _, required := range []string{"${BIFROST_INTERNAL_TOKEN}", "return 401", "proxy_set_header Authorization \"\""} {
		if !strings.Contains(templateText, required) {
			t.Fatalf("Bifrost Nginx 模板缺少内部鉴权约束: %s", required)
		}
	}

	pocRaw, err := os.ReadFile(filepath.Join(repoRoot, "infra", "scripts", "run-bifrost-g1-poc.sh"))
	if err != nil {
		t.Fatalf("读取 G1 POC 脚本失败: %v", err)
	}
	pocText := string(pocRaw)
	for _, required := range []string{
		"AI_GATEWAY_G1_POC_APPROVED",
		"max_tokens\\\":1",
		"bailian/qwen-turbo",
		"openrouter/cohere/north-mini-code:free",
		"\\\"stream\\\":true",
		"stream_options",
		"data: [DONE]",
		"-H @",
		"text/event-stream",
		"G1_POC=PASS",
	} {
		if !strings.Contains(pocText, required) {
			t.Fatalf("G1 POC 脚本缺少安全或验收约束: %s", required)
		}
	}
	approvalGate := strings.Index(pocText, "AI_GATEWAY_G1_POC_APPROVED")
	firstNetworkRequest := strings.Index(pocText, "for auth_mode in missing wrong duplicate")
	if approvalGate < 0 || firstNetworkRequest < 0 || approvalGate > firstNetworkRequest {
		t.Fatal("G1 POC 付费授权必须发生在任何网络探测之前")
	}
	if strings.Contains(pocText, `-H "Authorization: Bearer ${BIFROST_INTERNAL_TOKEN}"`) {
		t.Fatal("G1 POC 不得把内部 Token 展开到 curl 进程参数")
	}
}

func providerAllowsModel(keys []struct {
	Value  string   `json:"value"`
	Models []string `json:"models"`
}, modelName string) bool {
	for _, key := range keys {
		for _, allowedModel := range key.Models {
			if allowedModel == "*" || allowedModel == modelName {
				return true
			}
		}
	}
	return false
}
