package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type memoryOrchestratorStore struct {
	mu                    sync.Mutex
	key                   authmodel.APIKey
	snapshot              repository.G2AccessSnapshot
	requests              map[string]*model.AIRequest
	idem                  map[string]string
	attempts              map[string]model.AIExecutionAttempt
	usage                 map[string][]model.AIUsageItem
	recoverable           []string
	recoverableStillStale bool
	finalizeContextErr    error
	findProjectKeyDelay   time.Duration
}

func newMemoryOrchestratorStore() *memoryOrchestratorStore {
	projectID := uint64(8)
	channelID := uint64(4)
	upstream := "qwen-turbo"
	return &memoryOrchestratorStore{
		key: authmodel.APIKey{ID: 7, UserID: 3, ProjectID: &projectID, Status: "active", ScopeMode: ScopeModeAllowlist},
		snapshot: repository.G2AccessSnapshot{
			UserStatus: "active", RealNameStatus: "verified", ProjectStatus: "active", KeyStatus: "active",
			ScopeMode: ScopeModeAllowlist, ModelAllowed: true,
			TokenModel: model.TokenModel{LogicalModelCode: "molin/qwen-turbo", Modality: "chat", Status: "active", ChannelID: &channelID, UpstreamModel: &upstream},
		},
		requests: map[string]*model.AIRequest{}, idem: map[string]string{}, attempts: map[string]model.AIExecutionAttempt{}, usage: map[string][]model.AIUsageItem{},
	}
}

func (s *memoryOrchestratorStore) FindProjectKeyByID(_ context.Context, userID, keyID uint64) (*authmodel.APIKey, error) {
	if s.findProjectKeyDelay > 0 {
		time.Sleep(s.findProjectKeyDelay)
	}
	if userID != s.key.UserID || keyID != s.key.ID {
		return nil, repository.ErrProjectKeyNotFound
	}
	key := s.key
	return &key, nil
}
func (s *memoryOrchestratorStore) LoadAccessSnapshot(_ context.Context, userID, projectID, keyID uint64, _ string) (*repository.G2AccessSnapshot, error) {
	if userID != s.key.UserID || keyID != s.key.ID || s.key.ProjectID == nil || projectID != *s.key.ProjectID {
		return nil, repository.ErrProjectKeyNotFound
	}
	snapshot := s.snapshot
	return &snapshot, nil
}
func (s *memoryOrchestratorStore) FindRequestByIdentity(_ context.Context, requestID string) (*model.AIRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request, ok := s.requests[requestID]
	if !ok {
		return nil, repository.ErrRequestNotFound
	}
	copy := *request
	return &copy, nil
}
func (s *memoryOrchestratorStore) FindRequestByIdempotency(_ context.Context, userID uint64, key string) (*model.AIRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	requestID, ok := s.idem[key]
	if !ok || s.requests[requestID].UserID != userID {
		return nil, repository.ErrRequestNotFound
	}
	copy := *s.requests[requestID]
	return &copy, nil
}
func (s *memoryOrchestratorStore) CreateRequest(_ context.Context, request *model.AIRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.requests[request.RequestID]; exists {
		return errors.New("重复请求")
	}
	if request.IdempotencyKey != nil {
		if _, exists := s.idem[*request.IdempotencyKey]; exists {
			return errors.New("重复幂等键")
		}
		s.idem[*request.IdempotencyKey] = request.RequestID
	}
	copy := *request
	s.requests[request.RequestID] = &copy
	return nil
}
func (s *memoryOrchestratorStore) StartRequest(_ context.Context, requestID string, attempt *model.AIExecutionAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := s.requests[requestID]
	if request == nil || request.ExecutionStatus != model.AIExecutionPending {
		return repository.ErrRequestStateConflict
	}
	request.ExecutionStatus = model.AIExecutionRunning
	s.attempts[requestID] = *attempt
	return nil
}
func (s *memoryOrchestratorStore) FinalizeRequest(ctx context.Context, requestID string, attempt model.AIExecutionAttempt, usage []model.AIUsageItem, disconnected bool, errorClass, errorCode *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeContextErr = ctx.Err()
	if s.finalizeContextErr != nil {
		return s.finalizeContextErr
	}
	request := s.requests[requestID]
	if request == nil {
		return repository.ErrRequestNotFound
	}
	if request.ExecutionStatus != model.AIExecutionRunning {
		if s.attempts[requestID].Status == attempt.Status {
			return nil
		}
		return repository.ErrRequestStateConflict
	}
	s.attempts[requestID] = attempt
	s.usage[requestID] = append([]model.AIUsageItem(nil), usage...)
	request.ErrorClass, request.ErrorCode = errorClass, errorCode
	request.ClientDisconnected = disconnected
	request.ExecutionStatus = model.AIExecutionFailed
	if attempt.ResultUnknown || attempt.Status == "unknown" || attempt.Status == "timeout" {
		request.ExecutionStatus = model.AIExecutionUnknown
	} else if attempt.Status == "succeeded" {
		request.ExecutionStatus = model.AIExecutionSucceeded
	}
	request.BillingStatus = model.AIBillingUnquoted
	return nil
}

func TestFinalizeAfterExecutionIgnoresCanceledExecutionContext(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	prepared := prepareTestRequest(t, orchestrator, "req-finalize-after-timeout", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	now := time.Now()
	running := ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", ProviderModel: "qwen-turbo", StartedAt: now, Outcome: "running"}
	if err := store.StartRequest(context.Background(), prepared.RequestID, ptrAttempt(running.ToLedgerModel(prepared.RequestID, ExecutionUsage{}))); err != nil {
		t.Fatal(err)
	}
	executionCtx, cancel := context.WithCancel(context.Background())
	cancel()
	result := ExecutionResult{Attempt: failedAttempt(running, "network_timeout", true), ErrorCode: "upstream_execution_error"}
	if err := orchestrator.finalizeAfterExecution(executionCtx, prepared.RequestID, result); err != nil {
		t.Fatalf("上游上下文取消后仍应完成账务终结: %v", err)
	}
	if store.finalizeContextErr != nil || store.requests[prepared.RequestID].ExecutionStatus != model.AIExecutionUnknown {
		t.Fatalf("终结必须使用新的有效上下文并保存未知终态: ctx_err=%v status=%s", store.finalizeContextErr, store.requests[prepared.RequestID].ExecutionStatus)
	}
}

func TestRequestOrchestratorRecordsAccessRejectionReasons(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(*memoryOrchestratorStore)
		reason string
	}{
		{name: "模型停用", reason: "model_disabled", mutate: func(store *memoryOrchestratorStore) { store.snapshot.TokenModel.Status = "inactive" }},
		{name: "模型权限拒绝", reason: "permission_denied", mutate: func(store *memoryOrchestratorStore) { store.snapshot.ModelAllowed = false }},
		{name: "密钥冻结", reason: "api_key_frozen", mutate: func(store *memoryOrchestratorStore) { store.snapshot.KeyStatus = "frozen" }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store := newMemoryOrchestratorStore()
			testCase.mutate(store)
			metrics := NewAIGatewayMetrics(nil)
			orchestrator := newTestOrchestrator(store).WithMetrics(metrics)
			_, err := orchestrator.Prepare(context.Background(), PrepareCommand{
				RequestID: "req-rejection", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo",
				Body: map[string]interface{}{"model": "molin/qwen-turbo"},
			})
			if err == nil {
				t.Fatal("拒绝场景不应创建请求")
			}
			metricText, metricErr := metrics.AIGatewayPrometheus(context.Background())
			if metricErr != nil || !strings.Contains(metricText, `molin_ai_gateway_rejections_total{rejection_reason="`+testCase.reason+`"} 1`) ||
				!strings.Contains(metricText, `molin_ai_gateway_requests_total{logical_model_code="other",request_type="json",outcome="rejected"} 1`) {
				t.Fatalf("拒绝原因指标缺失 reason=%s err=%v\n%s", testCase.reason, metricErr, metricText)
			}
		})
	}
}

func TestRequestOrchestratorInputModerationRecordsRequestOutcome(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		repo       *memorySafetyRepository
		bodyText   string
		reason     string
		outcome    string
		timeout    time.Duration
		expectedIs error
	}{
		{name: "内容策略拒绝", repo: &memorySafetyRepository{policy: testSafetyPolicy(t)}, bodyText: "网络赌博", reason: "content_policy", outcome: "rejected", expectedIs: ErrContentPolicyViolation},
		{name: "分类器超时", repo: &memorySafetyRepository{waitPolicy: true}, bodyText: "正常问题", reason: "classifier_timeout", outcome: "failure", timeout: 10 * time.Millisecond, expectedIs: ErrModerationUnavailable},
		{name: "审核依赖失败关闭", repo: &memorySafetyRepository{err: errors.New("审核数据库不可用")}, bodyText: "正常问题", reason: "fail_closed", outcome: "failure", expectedIs: ErrModerationUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := newMemoryOrchestratorStore()
			metrics := NewAIGatewayMetrics(nil)
			governance := NewGovernanceService(
				NewSafetyService(testCase.repo, "0123456789abcdef0123456789abcdef"),
				&memoryBudgetRepository{}, &memoryResourceLimiter{},
			).WithMetrics(metrics)
			if testCase.timeout > 0 {
				governance.safetyTimeout = testCase.timeout
			}
			orchestrator := newTestOrchestrator(store).WithMetrics(metrics).WithGovernance(governance)
			// 输入审核发生在报价前，零值 billing 仅用于进入正式 Prepare 治理分支，不会在拒绝路径被调用。
			orchestrator.billing = &AIBillingService{}
			_, err := orchestrator.Prepare(context.Background(), PrepareCommand{
				RequestID: "req-input-" + testCase.outcome + "-" + testCase.reason,
				UserID:    3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo",
				Body: map[string]interface{}{"model": "molin/qwen-turbo", "messages": []interface{}{map[string]interface{}{"role": "user", "content": testCase.bodyText}}},
			})
			if !errors.Is(err, testCase.expectedIs) {
				t.Fatalf("输入审核错误分类不符: got=%v want=%v", err, testCase.expectedIs)
			}
			metricText, metricErr := metrics.AIGatewayPrometheus(context.Background())
			if metricErr != nil ||
				!strings.Contains(metricText, `molin_ai_gateway_rejections_total{rejection_reason="`+testCase.reason+`"} 1`) ||
				!strings.Contains(metricText, `molin_ai_gateway_requests_total{logical_model_code="molin/qwen-turbo",request_type="json",outcome="`+testCase.outcome+`"} 1`) {
				t.Fatalf("输入审核必须记录一次拒绝原因和请求结果: err=%v\n%s", metricErr, metricText)
			}
		})
	}
}

func TestRequestOrchestratorBillingDenialsRecordRejectedOutcome(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "请求无法报价", err: ErrUnquotableRequest},
		{name: "钱包余额不足", err: ErrWalletInsufficient},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			metrics := NewAIGatewayMetrics(nil)
			// 报价参数无效和余额不足均属于客户端可修正的业务拒绝，不得污染网关可用性失败率。
			metrics.RecordRequest("molin/qwen-turbo", "json", prepareMetricOutcome(testCase.err), time.Millisecond)
			metricText, metricErr := metrics.AIGatewayPrometheus(context.Background())
			if metricErr != nil || !strings.Contains(metricText, `molin_ai_gateway_requests_total{logical_model_code="other",request_type="json",outcome="rejected"} 1`) {
				t.Fatalf("计费业务拒绝必须记录 rejected: err=%v\n%s", metricErr, metricText)
			}
		})
	}
}

func TestRequestOrchestratorDurationIncludesPrepare(t *testing.T) {
	store := newMemoryOrchestratorStore()
	store.findProjectKeyDelay = 30 * time.Millisecond
	metrics := NewAIGatewayMetrics(nil)
	orchestrator := newTestOrchestrator(store).WithMetrics(metrics)
	prepared := prepareTestRequest(t, orchestrator, "req-end-to-end-duration", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	if prepared.metricStartedAt.IsZero() {
		t.Fatal("Prepare 成功后必须把端到端计时起点移交给 Execute")
	}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); err != nil {
		t.Fatal(err)
	}
	metricText, metricErr := metrics.AIGatewayPrometheus(context.Background())
	if metricErr != nil {
		t.Fatal(metricErr)
	}
	prefix := `molin_ai_gateway_request_duration_seconds_sum{request_type="json"} `
	var durationSeconds float64
	found := false
	for _, line := range strings.Split(metricText, "\n") {
		if strings.HasPrefix(line, prefix) {
			value, parseErr := strconv.ParseFloat(strings.TrimPrefix(line, prefix), 64)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			durationSeconds, found = value, true
			break
		}
	}
	if !found || durationSeconds < 0.03 {
		t.Fatalf("端到端耗时必须包含 Prepare 的 30ms 延迟: found=%t duration=%f\n%s", found, durationSeconds, metricText)
	}
}

func TestFinalizePreservesCallerDeadline(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	prepared := prepareTestRequest(t, orchestrator, "req-finalize-deadline", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	now := time.Now()
	running := ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", ProviderModel: "qwen-turbo", StartedAt: now, Outcome: "running"}
	if err := store.StartRequest(context.Background(), prepared.RequestID, ptrAttempt(running.ToLedgerModel(prepared.RequestID, ExecutionUsage{}))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := orchestrator.Finalize(ctx, prepared.RequestID, ExecutionResult{Attempt: failedAttempt(running, "network_timeout", true)})
	if !errors.Is(err, context.Canceled) || !errors.Is(store.finalizeContextErr, context.Canceled) {
		t.Fatalf("Finalize 必须保留调用方取消信号，避免终结无限阻塞: err=%v store_err=%v", err, store.finalizeContextErr)
	}
}

func TestModerationSegmentByteLimitIsBounded(t *testing.T) {
	if moderationSegmentWouldOverflow(0, maxModerationSegmentBytes) {
		t.Fatal("空缓冲区必须允许接收单个达到上限的事件行")
	}
	if moderationSegmentWouldOverflow(maxModerationSegmentBytes-8, 7) {
		t.Fatal("加入换行后恰好达到上限时不应提前刷新")
	}
	if !moderationSegmentWouldOverflow(maxModerationSegmentBytes-8, 8) {
		t.Fatal("追加后超过上限时必须先刷新已有审核段")
	}
}
func (s *memoryOrchestratorStore) MarkPendingOrRunningUnknown(_ context.Context, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request := s.requests[requestID]; request != nil && (request.ExecutionStatus == model.AIExecutionPending || request.ExecutionStatus == model.AIExecutionRunning) {
		request.ExecutionStatus = model.AIExecutionUnknown
	}
	if attempt, ok := s.attempts[requestID]; ok && attempt.Status == "running" {
		attempt.Status = "unknown"
		attempt.ResultUnknown = true
		s.attempts[requestID] = attempt
	}
	return nil
}
func (s *memoryOrchestratorStore) ListRecoverableRequestIDs(_ context.Context, _, _ time.Time, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit > len(s.recoverable) {
		limit = len(s.recoverable)
	}
	return append([]string(nil), s.recoverable[:limit]...), nil
}
func (s *memoryOrchestratorStore) MarkRecoverableUnknown(ctx context.Context, requestID string, _, _ time.Time) (bool, error) {
	if !s.recoverableStillStale {
		return false, nil
	}
	return true, s.MarkPendingOrRunningUnknown(ctx, requestID)
}
func (s *memoryOrchestratorStore) MarkClientDisconnected(_ context.Context, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request := s.requests[requestID]; request != nil {
		request.ClientDisconnected = true
	}
	return nil
}

type fakeChannelReader struct{ channel model.TokenChannel }

func (f fakeChannelReader) FindByID(_ context.Context, id uint64) (*model.TokenChannel, error) {
	if id != f.channel.ID {
		return nil, repository.ErrChannelNotFound
	}
	channel := f.channel
	return &channel, nil
}

type fakeVisibilityChecker struct{ visible bool }

func (f fakeVisibilityChecker) VisibleToUser(context.Context, uint64, string) (bool, error) {
	return f.visible, nil
}

type fakeRuntimeRouteResolver struct {
	route model.AIModelRoute
	err   error
}

func (f fakeRuntimeRouteResolver) ResolveActiveRoute(context.Context, string, string) (*model.AIModelRoute, error) {
	if f.err != nil {
		return nil, f.err
	}
	route := f.route
	return &route, nil
}

type recordingRuntimeRouteResolver struct {
	fakeRuntimeRouteResolver
	failures int
	resets   int
}

type switchingRuntimeRouteResolver struct {
	primary  model.AIModelRoute
	fallback model.AIModelRoute
	opened   bool
}

func (r *switchingRuntimeRouteResolver) ResolveActiveRoute(context.Context, string, string) (*model.AIModelRoute, error) {
	route := r.primary
	if r.opened {
		route = r.fallback
	}
	return &route, nil
}

func (r *switchingRuntimeRouteResolver) RecordRouteTransportFailure(_ context.Context, routeID, _ uint64) error {
	if routeID == r.primary.ID {
		r.opened = true
	}
	return nil
}

func (r *switchingRuntimeRouteResolver) ResetRouteTransportFailures(context.Context, uint64) error {
	return nil
}

func (r *recordingRuntimeRouteResolver) RecordRouteTransportFailure(context.Context, uint64, uint64) error {
	r.failures++
	return nil
}
func (r *recordingRuntimeRouteResolver) ResetRouteTransportFailures(context.Context, uint64) error {
	r.resets++
	return nil
}

type fakeOrchestratorDriver struct {
	executeErr            bool
	incomplete            bool
	requestNotSent        bool
	streamBody            io.ReadCloser
	jsonBody              string
	lastRequest           ExecutionRequest
	calls                 int
	failuresBeforeSuccess int
}

func (f *fakeOrchestratorDriver) Name() string { return "bifrost" }
func (f *fakeOrchestratorDriver) ChatCompletion(_ context.Context, req ExecutionRequest) (*ExecutionResponse, error) {
	f.calls++
	f.lastRequest = req
	now := time.Now()
	if f.executeErr || f.calls <= f.failuresBeforeSuccess {
		if f.requestNotSent || f.calls <= f.failuresBeforeSuccess {
			return &ExecutionResponse{Attempt: ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", ProviderModel: "bailian/qwen-turbo", StartedAt: now.Add(-time.Millisecond), FinishedAt: now, Outcome: "failed", ErrorClass: "request_not_sent", ResultUnknown: false}}, errors.New("连接前失败")
		}
		return &ExecutionResponse{Attempt: ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", ProviderModel: "bailian/qwen-turbo", StartedAt: now.Add(-time.Millisecond), FinishedAt: now, Outcome: "timeout", ErrorClass: "network_timeout", ResultUnknown: true}}, context.DeadlineExceeded
	}
	body := f.jsonBody
	if body == "" {
		body = `{"id":"ok","choices":[{"message":{"content":"OK"}}]}`
	}
	return &ExecutionResponse{
		Response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(body))},
		Usage:    ExecutionUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3, Present: true},
		Attempt:  ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", EndpointCode: "bailian", ProviderModel: "bailian/qwen-turbo", StartedAt: now.Add(-time.Millisecond), FinishedAt: now, Outcome: "success"},
	}, nil
}
func (f *fakeOrchestratorDriver) ChatCompletionStream(_ context.Context, _ ExecutionRequest) (*ExecutionResponse, error) {
	now := time.Now()
	body := "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"O\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	if f.incomplete {
		body = "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"O\"}}]}\n\n"
	}
	responseBody := io.ReadCloser(io.NopCloser(bytes.NewBufferString(body)))
	if f.streamBody != nil {
		responseBody = f.streamBody
	}
	return &ExecutionResponse{
		Response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: responseBody},
		Attempt:  ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", EndpointCode: "bailian", ProviderModel: "bailian/qwen-turbo", StartedAt: now, Outcome: "success"},
	}, nil
}
func (f *fakeOrchestratorDriver) NormalizeStreamLine(line []byte, logicalModel string) (ExecutionStreamChunk, error) {
	return normalizeExecutionStreamLine(line, logicalModel)
}

type memorySink struct {
	status      int
	body        bytes.Buffer
	failWrite   bool
	failDone    bool
	failFlush   bool
	failFlushAt int
	flushes     int
	failHeader  bool
}

func (s *memorySink) SetHeader(_, _ string) {}
func (s *memorySink) WriteHeader(status int) error {
	s.status = status
	if s.failHeader {
		return errors.New("客户端在响应头阶段断开")
	}
	return nil
}
func (s *memorySink) Write(data []byte) error {
	if s.failWrite || (s.failDone && bytes.Contains(data, []byte("data: [DONE]"))) {
		return errors.New("客户端已断开")
	}
	_, _ = s.body.Write(data)
	return nil
}
func (s *memorySink) Flush() error {
	s.flushes++
	if s.failFlush || (s.failFlushAt > 0 && s.flushes == s.failFlushAt) {
		return errors.New("客户端刷新失败")
	}
	return nil
}

func newTestOrchestrator(store *memoryOrchestratorStore) *RequestOrchestratorService {
	orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bailian", BaseURL: "http://unused", Status: "active"}}, nil)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{}})
	orchestrator.WithVisibilityChecker(fakeVisibilityChecker{visible: true})
	return orchestrator
}

func prepareTestRequest(t *testing.T, orchestrator *RequestOrchestratorService, requestID, idem string, body map[string]interface{}) *PreparedRequest {
	t.Helper()
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{
		RequestID: requestID, IdempotencyKey: idem, UserID: 3, APIKeyID: 7,
		LogicalModel: "molin/qwen-turbo", Body: body,
	})
	if err != nil {
		t.Fatalf("Prepare 失败: %v", err)
	}
	return prepared
}

func TestRequestOrchestratorPrepareRejectsEmptyAllowlistAndUnverifiedUser(t *testing.T) {
	store := newMemoryOrchestratorStore()
	store.snapshot.ModelAllowed = false
	_, err := newTestOrchestrator(store).Prepare(context.Background(), PrepareCommand{RequestID: "req-deny", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if !errors.Is(err, ErrG2ModelNotAllowed) {
		t.Fatalf("空 allowlist 必须拒绝，实际: %v", err)
	}
	store.snapshot.ModelAllowed = true
	store.snapshot.RealNameStatus = "pending"
	_, err = newTestOrchestrator(store).Prepare(context.Background(), PrepareCommand{RequestID: "req-realname", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if !errors.Is(err, ErrRealNameRequired) {
		t.Fatalf("未实名用户必须拒绝，实际: %v", err)
	}
	orchestrator := newTestOrchestrator(store).WithVisibilityChecker(fakeVisibilityChecker{visible: false})
	store.snapshot.RealNameStatus = "verified"
	_, err = orchestrator.Prepare(context.Background(), PrepareCommand{RequestID: "req-hidden", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if !errors.Is(err, ErrG2ModelUnavailable) {
		t.Fatalf("定向隐藏模型必须在上游调用前拒绝，实际: %v", err)
	}
}

func TestRequestOrchestratorUsesG5RuntimeRoute(t *testing.T) {
	store := newMemoryOrchestratorStore()
	driver := &fakeOrchestratorDriver{}
	orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bifrost-main", BaseURL: "http://unused", Status: "active"}}, nil).
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).
		WithRouteResolver(fakeRuntimeRouteResolver{route: model.AIModelRoute{ID: 88, ChannelID: 4, ProviderModel: "openrouter/runtime-model", TimeoutMS: 2000}})
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	prepared := prepareTestRequest(t, orchestrator, "req-g5-route", "", map[string]interface{}{"messages": []interface{}{}})
	if prepared.tokenModel.UpstreamModel == nil || *prepared.tokenModel.UpstreamModel != "openrouter/runtime-model" || prepared.endpointCode != "route:88" || prepared.routeTimeout != 2*time.Second {
		t.Fatalf("G5 路由未写入执行上下文: %+v", prepared)
	}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if driver.lastRequest.ProviderModel != "openrouter/runtime-model" {
		t.Fatalf("运行时未使用 G5 provider/model: %+v", driver.lastRequest)
	}
}

func TestRequestOrchestratorOnlyFallsBackWhenG5RouteWasNeverConfigured(t *testing.T) {
	store := newMemoryOrchestratorStore()
	legacy := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bailian", BaseURL: "http://unused", Status: "active"}}, nil).
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).
		WithRouteResolver(fakeRuntimeRouteResolver{err: gorm.ErrRecordNotFound})
	legacy.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{}})
	if _, err := legacy.Prepare(context.Background(), PrepareCommand{RequestID: "req-legacy-route", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}}); err != nil {
		t.Fatalf("从未配置 G5 路由的旧模型应继续使用冻结路由: %v", err)
	}

	unavailable := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bailian", BaseURL: "http://unused", Status: "active"}}, nil).
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).
		WithRouteResolver(fakeRuntimeRouteResolver{err: repository.ErrRouteUnavailable})
	unavailable.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{}})
	_, err := unavailable.Prepare(context.Background(), PrepareCommand{RequestID: "req-circuit-open", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{}})
	if !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("已配置但熔断或 down 的 G5 路由必须失败关闭，不能回退旧路由: %v", err)
	}
}

func TestRequestOrchestratorRetriesOnlyConfirmedNotSentAndResetsCircuit(t *testing.T) {
	store := newMemoryOrchestratorStore()
	driver := &fakeOrchestratorDriver{failuresBeforeSuccess: 2}
	resolver := &recordingRuntimeRouteResolver{fakeRuntimeRouteResolver: fakeRuntimeRouteResolver{route: model.AIModelRoute{ID: 89, ChannelID: 4, ProviderModel: "openrouter/runtime-model", TimeoutMS: 5000, MaxRetries: 2, CircuitBreakerThreshold: 3}}}
	orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bifrost-main", BaseURL: "http://unused", Status: "active"}}, nil).
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithRouteResolver(resolver)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	prepared := prepareTestRequest(t, orchestrator, "req-g5-safe-retry", "", map[string]interface{}{"messages": []interface{}{}})
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); err != nil {
		t.Fatalf("确认未发送的短暂失败应在上限内恢复: %v", err)
	}
	if driver.calls != 3 || resolver.failures != 0 || resolver.resets != 1 {
		t.Fatalf("安全重试或熔断复位次数错误: calls=%d failures=%d resets=%d", driver.calls, resolver.failures, resolver.resets)
	}
}

func TestRequestOrchestratorMetricsCountRetriesWithoutCountingIdempotentReplay(t *testing.T) {
	store := newMemoryOrchestratorStore()
	driver := &fakeOrchestratorDriver{failuresBeforeSuccess: 2}
	metrics := NewAIGatewayMetrics(nil)
	resolver := &recordingRuntimeRouteResolver{fakeRuntimeRouteResolver: fakeRuntimeRouteResolver{route: model.AIModelRoute{ID: 189, ChannelID: 4, ProviderModel: "openrouter/runtime-model", TimeoutMS: 5000, MaxRetries: 2, CircuitBreakerThreshold: 3}}}
	orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bifrost-main", BaseURL: "http://unused", Status: "active"}}, nil).
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithRouteResolver(resolver).WithMetrics(metrics)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	body := map[string]interface{}{"messages": []interface{}{"same"}}
	prepared := prepareTestRequest(t, orchestrator, "req-g7-metrics", "idem-g7-metrics", body)
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); err != nil {
		t.Fatalf("带安全重试的指标请求执行失败: %v", err)
	}
	replay := prepareTestRequest(t, orchestrator, "req-g7-metrics-replay", "idem-g7-metrics", body)
	if !replay.Existing || replay.RequestID != prepared.RequestID {
		t.Fatalf("幂等回放未返回原请求: %+v", replay)
	}

	text, err := metrics.AIGatewayPrometheus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`molin_ai_gateway_requests_total{logical_model_code="molin/qwen-turbo",request_type="json",outcome="success"} 1`,
		`molin_ai_gateway_upstream_requests_total{logical_model_code="molin/qwen-turbo",driver="bifrost",outcome="unknown"} 2`,
		`molin_ai_gateway_upstream_requests_total{logical_model_code="molin/qwen-turbo",driver="bifrost",outcome="success"} 1`,
		`molin_ai_gateway_upstream_retries_total{logical_model_code="molin/qwen-turbo",driver="bifrost"} 2`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("缺少编排事件指标 %q\n%s", expected, text)
		}
	}
}

func TestRequestOrchestratorMetricsObserveTTFTAndClientDisconnect(t *testing.T) {
	store := newMemoryOrchestratorStore()
	metrics := NewAIGatewayMetrics(nil)
	orchestrator := newTestOrchestrator(store).WithMetrics(metrics)
	driver := &fakeOrchestratorDriver{}
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	prepared := prepareTestRequest(t, orchestrator, "req-g7-stream-metrics", "", map[string]interface{}{"messages": []interface{}{"stream"}})
	prepared.command.Stream = true
	prepared.command.Body["stream"] = true
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{failWrite: true}); err != nil {
		t.Fatalf("客户端断连后服务端仍应完成读取与结算: %v", err)
	}
	text, err := metrics.AIGatewayPrometheus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `molin_ai_gateway_stream_interruptions_total{logical_model_code="molin/qwen-turbo",driver="bifrost"} 1`) {
		t.Fatalf("流式断连未计入指标:\n%s", text)
	}
	if strings.Contains(text, `molin_ai_gateway_ttft_seconds_count{logical_model_code="molin/qwen-turbo",driver="bifrost"} 1`) {
		t.Fatalf("首段未成功写给客户端时不得伪造 TTFT:\n%s", text)
	}
}

func TestRequestOrchestratorOpensCircuitAfterSafeRetriesExhausted(t *testing.T) {
	store := newMemoryOrchestratorStore()
	driver := &fakeOrchestratorDriver{failuresBeforeSuccess: 5}
	resolver := &recordingRuntimeRouteResolver{fakeRuntimeRouteResolver: fakeRuntimeRouteResolver{route: model.AIModelRoute{ID: 90, ChannelID: 4, ProviderModel: "openrouter/runtime-model", TimeoutMS: 5000, MaxRetries: 1, CircuitBreakerThreshold: 2}}}
	orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bifrost-main", BaseURL: "http://unused", Status: "active"}}, nil).
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithRouteResolver(resolver)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	prepared := prepareTestRequest(t, orchestrator, "req-g5-retry-exhausted", "", map[string]interface{}{"messages": []interface{}{}})
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("安全重试耗尽后必须返回上游失败: %v", err)
	}
	if driver.calls != 2 || resolver.failures != 1 || resolver.resets != 0 {
		t.Fatalf("耗尽后必须只登记一次连续失败: calls=%d failures=%d resets=%d", driver.calls, resolver.failures, resolver.resets)
	}
}

func TestRequestOrchestratorNextRequestAvoidsOpenedRouteWithoutDuplicateUsage(t *testing.T) {
	store := newMemoryOrchestratorStore()
	driver := &fakeOrchestratorDriver{failuresBeforeSuccess: 1}
	resolver := &switchingRuntimeRouteResolver{
		primary:  model.AIModelRoute{ID: 90, ChannelID: 4, ProviderModel: "openrouter/primary", TimeoutMS: 5000, MaxRetries: 0, CircuitBreakerThreshold: 1},
		fallback: model.AIModelRoute{ID: 92, ChannelID: 4, ProviderModel: "openrouter/fallback", TimeoutMS: 5000, MaxRetries: 0, CircuitBreakerThreshold: 1},
	}
	orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bifrost-main", BaseURL: "http://unused", Status: "active"}}, nil).
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithRouteResolver(resolver)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})

	first := prepareTestRequest(t, orchestrator, "req-route-primary", "", map[string]interface{}{"messages": []interface{}{}})
	if err := orchestrator.Execute(context.Background(), first.RequestID, &memorySink{}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("主路由失败应返回受控上游错误: %v", err)
	}
	second := prepareTestRequest(t, orchestrator, "req-route-fallback", "", map[string]interface{}{"messages": []interface{}{}})
	if second.endpointCode != "route:92" {
		t.Fatalf("下一请求必须避开已熔断主路由，实际 endpoint=%s", second.endpointCode)
	}
	if err := orchestrator.Execute(context.Background(), second.RequestID, &memorySink{}); err != nil {
		t.Fatalf("备用路由应成功完成请求: %v", err)
	}
	if len(store.usage[first.RequestID]) != 0 || len(store.usage[second.RequestID]) == 0 {
		t.Fatalf("失败请求不得产生用量事实，成功请求只保存自身事实: first=%d second=%d", len(store.usage[first.RequestID]), len(store.usage[second.RequestID]))
	}
	if driver.lastRequest.ProviderModel != "openrouter/fallback" || driver.calls != 2 {
		t.Fatalf("不得重复执行或回到主路由: calls=%d provider=%s", driver.calls, driver.lastRequest.ProviderModel)
	}
}

func TestRequestOrchestratorNeverRetriesUnknownResult(t *testing.T) {
	store := newMemoryOrchestratorStore()
	driver := &fakeOrchestratorDriver{executeErr: true}
	resolver := &recordingRuntimeRouteResolver{fakeRuntimeRouteResolver: fakeRuntimeRouteResolver{route: model.AIModelRoute{ID: 91, ChannelID: 4, ProviderModel: "openrouter/runtime-model", TimeoutMS: 5000, MaxRetries: 3, CircuitBreakerThreshold: 2}}}
	orchestrator := NewRequestOrchestrator(store, fakeChannelReader{channel: model.TokenChannel{ID: 4, Code: "bifrost-main", BaseURL: "http://unused", Status: "active"}}, nil).
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithRouteResolver(resolver)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	prepared := prepareTestRequest(t, orchestrator, "req-g5-unknown-no-retry", "", map[string]interface{}{"messages": []interface{}{}})
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("结果未知必须失败并进入对账: %v", err)
	}
	if driver.calls != 1 || resolver.failures != 0 {
		t.Fatalf("结果未知禁止重试和熔断计数: calls=%d failures=%d", driver.calls, resolver.failures)
	}
}

func TestRequestOrchestratorIdempotencyReturnsExistingAndRejectsFingerprintConflict(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	body := map[string]interface{}{"model": "molin/qwen-turbo", "messages": []interface{}{map[string]interface{}{"role": "user", "content": "A"}}}
	first := prepareTestRequest(t, orchestrator, "req-first", "idem-1", body)
	if first.Existing {
		t.Fatal("首次请求不应标记 Existing")
	}
	second := prepareTestRequest(t, orchestrator, "req-second", "idem-1", body)
	if !second.Existing || second.RequestID != "req-first" {
		t.Fatalf("重复请求应返回首个请求状态: %+v", second)
	}
	_, err := orchestrator.Prepare(context.Background(), PrepareCommand{
		RequestID: "req-conflict", IdempotencyKey: "idem-1", UserID: 3, APIKeyID: 7,
		LogicalModel: "molin/qwen-turbo", Body: map[string]interface{}{"model": "molin/qwen-turbo", "messages": []interface{}{map[string]interface{}{"content": "B"}}},
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("同幂等键不同内容必须冲突，实际: %v", err)
	}
}

func TestRequestOrchestratorConcurrentIdempotencyCreatesOneRequest(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	body := map[string]interface{}{"model": "molin/qwen-turbo", "messages": []interface{}{"same"}}
	const workers = 20
	var wait sync.WaitGroup
	errorsFound := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := orchestrator.Prepare(context.Background(), PrepareCommand{
				RequestID: "req-concurrent-" + string(rune('a'+index)), IdempotencyKey: "idem-concurrent",
				UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Body: body,
			})
			if err != nil {
				errorsFound <- err
			}
		}(i)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("并发幂等请求失败: %v", err)
	}
	store.mu.Lock()
	requestCount := len(store.requests)
	store.mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("并发相同幂等键只能创建一条正式请求，实际 %d", requestCount)
	}
}

func TestRequestOrchestratorSameRequestIDDifferentOwnerIsDenied(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	body := map[string]interface{}{"model": "molin/qwen-turbo", "messages": []interface{}{"same"}}
	prepareTestRequest(t, orchestrator, "req-owner-bound", "", body)

	otherProject := uint64(10)
	store.key = authmodel.APIKey{ID: 8, UserID: 3, ProjectID: &otherProject, Status: "active", ScopeMode: ScopeModeAll}
	_, err := orchestrator.Prepare(context.Background(), PrepareCommand{RequestID: "req-owner-bound", UserID: 3, APIKeyID: 8, LogicalModel: "molin/qwen-turbo", Body: body})
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("同 request_id 换 Project/SK 必须统一拒绝，实际: %v", err)
	}

	store.key = authmodel.APIKey{ID: 9, UserID: 4, ProjectID: &otherProject, Status: "active", ScopeMode: ScopeModeAll}
	_, err = orchestrator.Prepare(context.Background(), PrepareCommand{RequestID: "req-owner-bound", UserID: 4, APIKeyID: 9, LogicalModel: "molin/qwen-turbo", Body: body})
	if !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("同 request_id 换用户必须统一拒绝且不泄露原请求，实际: %v", err)
	}
}

func TestRequestOrchestratorStatusQueryIsTenantBound(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	prepared := prepareTestRequest(t, orchestrator, "req-status", "idem-status", map[string]interface{}{"model": "molin/qwen-turbo"})
	status, err := orchestrator.GetRequestStatus(context.Background(), prepared.RequestID, 3, 7)
	if err != nil || status.RequestID != prepared.RequestID || status.BillingStatus != model.AIBillingUnquoted {
		t.Fatalf("原 Project SK 应可查询请求状态: status=%+v err=%v", status, err)
	}
	if _, err := orchestrator.GetRequestStatus(context.Background(), prepared.RequestID, 3, 8); !errors.Is(err, ErrProjectAccessDenied) {
		t.Fatalf("其他 SK 不得查询请求状态: %v", err)
	}
}

func TestFindExistingMarksRequestNotSentAsRetryable(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	body := map[string]interface{}{"model": "molin/qwen-turbo", "max_tokens": float64(10)}
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{executeErr: true, requestNotSent: true}})
	prepared := prepareTestRequest(t, orchestrator, "req-old", "idem-retry", body)
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("连接前失败应返回统一上游错误: %v", err)
	}
	request := store.requests[prepared.RequestID]
	if request.ErrorCode == nil || *request.ErrorCode != "request_not_sent" || request.ExecutionStatus != model.AIExecutionFailed {
		t.Fatalf("正式执行链必须保留 request_not_sent: %+v", request)
	}
	// 内存仓储不执行钱包逻辑，此处只补齐真实计费服务会写入的 released 状态，再验证重试门禁。
	request.BillingStatus = model.AIBillingReleased
	orchestrator.billing = &AIBillingService{}
	fingerprint, err := requestFingerprint(body)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := orchestrator.findExisting(context.Background(), PrepareCommand{UserID: 3, APIKeyID: 7, IdempotencyKey: "idem-retry"}, 8, fingerprint)
	if err != nil || retry == nil || retry.retrySourceRequestID != "req-old" || retry.Existing {
		t.Fatalf("明确未发出的失败请求应允许同幂等键创建新请求: prepared=%+v err=%v", retry, err)
	}
}

func TestRequestOrchestratorJSONPersistsBeforeSuccessAndKeepsUnquoted(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	prepared := prepareTestRequest(t, orchestrator, "req-json", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	sink := &memorySink{}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, sink); err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	request := store.requests[prepared.RequestID]
	if request.ExecutionStatus != model.AIExecutionSucceeded || request.BillingStatus != model.AIBillingUnquoted {
		t.Fatalf("请求终态错误: %+v", request)
	}
	if len(store.usage[prepared.RequestID]) != 3 || sink.status != http.StatusOK || !bytes.Contains(sink.body.Bytes(), []byte(`"id":"ok"`)) {
		t.Fatalf("响应或 Usage 不完整 usage=%d status=%d body=%s", len(store.usage[prepared.RequestID]), sink.status, sink.body.String())
	}
}

func TestRequestOrchestratorFinalizeRetryDoesNotDuplicateUsage(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	prepared := prepareTestRequest(t, orchestrator, "req-finalize-retry", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	result := ExecutionResult{
		Attempt: ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", ProviderModel: "bailian/qwen-turbo", StartedAt: now.Add(-time.Millisecond), FinishedAt: now, Outcome: "success"},
		Usage:   ExecutionUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3, Present: true},
	}
	if err := orchestrator.Finalize(context.Background(), prepared.RequestID, result); err != nil {
		t.Fatalf("相同终态 Finalize 重试应幂等成功: %v", err)
	}
	if len(store.usage[prepared.RequestID]) != 3 {
		t.Fatalf("Finalize 重试不得重复 Usage，实际 %d", len(store.usage[prepared.RequestID]))
	}
}

func TestRequestOrchestratorJSONDisconnectMarksTransportFact(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	prepared := prepareTestRequest(t, orchestrator, "req-json-disconnect", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{failWrite: true}); err != nil {
		t.Fatalf("JSON 写回失败不应撤销已形成的执行事实: %v", err)
	}
	request := store.requests[prepared.RequestID]
	if !request.ClientDisconnected || request.ExecutionStatus != model.AIExecutionSucceeded || request.BillingStatus != model.AIBillingUnquoted {
		t.Fatalf("JSON 断连事实错误: %+v", request)
	}
	prepared = prepareTestRequest(t, orchestrator, "req-json-header-disconnect", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{failHeader: true}); err != nil {
		t.Fatalf("JSON 响应头写回失败不应撤销执行事实: %v", err)
	}
	if !store.requests[prepared.RequestID].ClientDisconnected {
		t.Fatal("JSON 响应头阶段断连也必须记录 client_disconnected")
	}
}

func TestRequestOrchestratorSSEDisconnectStillPersistsUsage(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{
		RequestID: "req-sse", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true,
		Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &memorySink{failWrite: true}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, sink); err != nil {
		t.Fatalf("客户端断开后仍应完成持久化: %v", err)
	}
	request := store.requests[prepared.RequestID]
	if !request.ClientDisconnected || request.ExecutionStatus != model.AIExecutionSucceeded || len(store.usage[prepared.RequestID]) != 3 {
		t.Fatalf("断连持久化错误 request=%+v usage=%d", request, len(store.usage[prepared.RequestID]))
	}
}

func TestRequestOrchestratorSSEFinalDoneFailureMarksDisconnect(t *testing.T) {
	for _, testCase := range []struct {
		name string
		sink *memorySink
	}{
		{name: "结束帧写入失败", sink: &memorySink{failDone: true}},
		{name: "结束帧刷新失败", sink: &memorySink{failFlushAt: 2}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := newMemoryOrchestratorStore()
			orchestrator := newTestOrchestrator(store).WithMetrics(NewAIGatewayMetrics(nil))
			prepared, prepareErr := orchestrator.Prepare(context.Background(), PrepareCommand{
				RequestID: "req-sse-final-" + testCase.name, UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true,
				Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true},
			})
			if prepareErr != nil {
				t.Fatal(prepareErr)
			}
			if err := orchestrator.Execute(context.Background(), prepared.RequestID, testCase.sink); err != nil {
				t.Fatalf("结束帧写回失败不应回滚既有执行和账务事实: %v", err)
			}
			request := store.requests[prepared.RequestID]
			if !request.ClientDisconnected || request.ExecutionStatus != model.AIExecutionSucceeded || len(store.usage[prepared.RequestID]) != 3 {
				t.Fatalf("结束帧断连事实错误 request=%+v usage=%d", request, len(store.usage[prepared.RequestID]))
			}
		})
	}
}

func TestRequestOrchestratorSSEDoneDoesNotWaitForUpstreamClose(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	reader, writer := io.Pipe()
	driver := &fakeOrchestratorDriver{streamBody: reader}
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{
		RequestID: "req-sse-done-open", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true,
		Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true},
	})
	if err != nil {
		t.Fatal(err)
	}

	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := writer.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\ndata: [DONE]\n\n"))
		writeDone <- writeErr
	}()
	executeDone := make(chan error, 1)
	sink := &memorySink{}
	go func() {
		executeDone <- orchestrator.Execute(context.Background(), prepared.RequestID, sink)
	}()

	select {
	case err := <-executeDone:
		if err != nil {
			t.Fatalf("收到 [DONE] 后应立即完成结算: %v", err)
		}
	case <-time.After(time.Second):
		_ = writer.Close()
		t.Fatal("收到 [DONE] 后不得等待上游关闭连接")
	}
	_ = writer.Close()
	if err := <-writeDone; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("写入测试 SSE 失败: %v", err)
	}
	request := store.requests[prepared.RequestID]
	if request.ExecutionStatus != model.AIExecutionSucceeded || len(store.usage[prepared.RequestID]) != 3 {
		t.Fatalf("SSE 终态持久化错误 request=%+v usage=%d", request, len(store.usage[prepared.RequestID]))
	}
	if !bytes.Contains(sink.body.Bytes(), []byte("[DONE]")) {
		t.Fatal("客户端必须及时收到 SSE 终止符")
	}
}

func TestRequestOrchestratorSSEBlocksViolatingSegmentWithoutLeakAndPersistsUsage(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	metrics := NewAIGatewayMetrics(nil)
	orchestrator.WithMetrics(metrics)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"blocked phrase\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{streamBody: io.NopCloser(strings.NewReader(stream))}})
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{
		RequestID: "req-sse-safety", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true,
		Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal([]safetyRule{{Code: "illegal-001", Category: "illegal", Keywords: []string{"blocked phrase"}}})
	safetyRepo := &memorySafetyRepository{policy: &model.AISafetyPolicyVersion{ID: 1, VersionNo: 1, Status: model.AISafetyPolicyActive, RefusalMessage: DefaultSafetyRefusal, RulesJSON: rules}}
	orchestrator.governance = NewGovernanceService(
		NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef"),
		&memoryBudgetRepository{}, &memoryResourceLimiter{},
	).WithMetrics(metrics)
	orchestrator.activeTickets.Store(prepared.RequestID, &GovernanceTicket{
		Subject:  SafetySubject{RequestID: prepared.RequestID, UserID: 3, ProjectID: 8, APIKeyID: 7},
		Resource: &ResourceTicket{},
	})
	defer orchestrator.activeTickets.Delete(prepared.RequestID)
	sink := &memorySink{}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, sink); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sink.body.String(), "blocked phrase") || !strings.Contains(sink.body.String(), "event: molin.content_policy") || !strings.Contains(sink.body.String(), "[DONE]") {
		t.Fatalf("违规分段不得泄漏且必须稳定终止 SSE: %s", sink.body.String())
	}
	if len(store.usage[prepared.RequestID]) != 3 || safetyRepo.marked[prepared.RequestID] != model.AIModerationRejected {
		t.Fatalf("违规输出仍须持久化 usage 和审核终态: usage=%d status=%s", len(store.usage[prepared.RequestID]), safetyRepo.marked[prepared.RequestID])
	}
	metricText, metricErr := metrics.AIGatewayPrometheus(context.Background())
	if metricErr != nil ||
		!strings.Contains(metricText, `molin_ai_gateway_rejections_total{rejection_reason="content_policy"} 1`) ||
		!strings.Contains(metricText, `molin_ai_gateway_requests_total{logical_model_code="molin/qwen-turbo",request_type="stream",outcome="rejected"} 1`) {
		t.Fatalf("输出内容策略拒绝必须进入 G7 指标: err=%v metrics=%s", metricErr, metricText)
	}
}

func TestRequestOrchestratorSSEModerationTerminalDisconnectIsRecordedOnce(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	metrics := NewAIGatewayMetrics(nil)
	orchestrator.WithMetrics(metrics)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"blocked phrase\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	driver := &fakeOrchestratorDriver{streamBody: io.NopCloser(strings.NewReader(stream))}
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{
		RequestID: "req-sse-safety-disconnect", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true,
		Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal([]safetyRule{{Code: "illegal-001", Category: "illegal", Keywords: []string{"blocked phrase"}}})
	safetyRepo := &memorySafetyRepository{policy: &model.AISafetyPolicyVersion{ID: 1, VersionNo: 1, Status: model.AISafetyPolicyActive, RulesJSON: rules}}
	orchestrator.governance = NewGovernanceService(
		NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef"),
		&memoryBudgetRepository{}, &memoryResourceLimiter{},
	).WithMetrics(metrics)
	orchestrator.activeTickets.Store(prepared.RequestID, &GovernanceTicket{
		Subject: SafetySubject{RequestID: prepared.RequestID, UserID: 3, ProjectID: 8, APIKeyID: 7}, Resource: &ResourceTicket{},
	})
	defer orchestrator.activeTickets.Delete(prepared.RequestID)

	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{failDone: true}); err != nil {
		t.Fatalf("审核拒绝终帧断连不应撤销已形成的财务事实: %v", err)
	}
	if !store.requests[prepared.RequestID].ClientDisconnected {
		t.Fatal("审核拒绝终帧写入失败必须补记 client_disconnected")
	}
	metricText, metricErr := metrics.AIGatewayPrometheus(context.Background())
	if metricErr != nil ||
		!strings.Contains(metricText, `molin_ai_gateway_stream_interruptions_total{logical_model_code="molin/qwen-turbo",driver="bifrost"} 1`) ||
		!strings.Contains(metricText, `molin_ai_gateway_requests_total{logical_model_code="molin/qwen-turbo",request_type="stream",outcome="rejected"} 1`) {
		t.Fatalf("审核拒绝终帧断连必须各记录一次 rejected 与流中断: err=%v\n%s", metricErr, metricText)
	}
}

func TestRequestOrchestratorOutputModerationPersistenceFailureRecordsFailClosedOnce(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		stream bool
	}{
		{name: "JSON"},
		{name: "SSE", stream: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := newMemoryOrchestratorStore()
			orchestrator := newTestOrchestrator(store)
			metrics := NewAIGatewayMetrics(nil)
			orchestrator.WithMetrics(metrics)
			driver := &fakeOrchestratorDriver{jsonBody: `{"id":"blocked","choices":[{"message":{"content":"blocked phrase"}}]}`}
			if testCase.stream {
				driver.streamBody = io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"blocked phrase\"}}]}\n\n" +
					"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
					"data: [DONE]\n\n"))
			}
			orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: driver})
			prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{
				RequestID: "req-output-mark-failed-" + strings.ToLower(testCase.name), UserID: 3, APIKeyID: 7,
				LogicalModel: "molin/qwen-turbo", Stream: testCase.stream,
				Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": testCase.stream},
			})
			if err != nil {
				t.Fatal(err)
			}
			rules, _ := json.Marshal([]safetyRule{{Code: "illegal-001", Category: "illegal", Keywords: []string{"blocked phrase"}}})
			safetyRepo := &memorySafetyRepository{
				policy:  &model.AISafetyPolicyVersion{ID: 1, VersionNo: 1, Status: model.AISafetyPolicyActive, RulesJSON: rules},
				markErr: errors.New("审核状态存储不可用"),
			}
			budget := &memoryBudgetRepository{}
			orchestrator.governance = NewGovernanceService(
				NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef"), budget, &memoryResourceLimiter{},
			).WithMetrics(metrics)
			orchestrator.activeTickets.Store(prepared.RequestID, &GovernanceTicket{
				Subject: SafetySubject{RequestID: prepared.RequestID, UserID: 3, ProjectID: 8, APIKeyID: 7}, Resource: &ResourceTicket{},
			})
			defer orchestrator.activeTickets.Delete(prepared.RequestID)

			executeErr := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{})
			if !testCase.stream && !errors.Is(executeErr, ErrModerationUnavailable) {
				t.Fatalf("JSON 审核状态落库失败必须返回审核不可用: %v", executeErr)
			}
			if testCase.stream && executeErr != nil {
				t.Fatalf("SSE 审核状态落库失败应以受控事件结束: %v", executeErr)
			}
			metricText, metricErr := metrics.AIGatewayPrometheus(context.Background())
			requestType := "json"
			if testCase.stream {
				requestType = "stream"
			}
			if metricErr != nil || !strings.Contains(metricText, `molin_ai_gateway_rejections_total{rejection_reason="fail_closed"} 1`) ||
				strings.Contains(metricText, `molin_ai_gateway_rejections_total{rejection_reason="content_policy"} 1`) ||
				!strings.Contains(metricText, `molin_ai_gateway_requests_total{logical_model_code="molin/qwen-turbo",request_type="`+requestType+`",outcome="failure"} 1`) {
				t.Fatalf("审核状态落库失败必须且只能记一次 fail_closed: err=%v\n%s", metricErr, metricText)
			}
			if len(budget.rejections) != 1 || budget.rejections[0].ReasonCode != "fail_closed" {
				t.Fatalf("审核状态落库失败必须形成一次脱敏失败关闭事实: %+v", budget.rejections)
			}
		})
	}
}

func TestRequestOrchestratorOutputModerationHasIndependentTimeout(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	metrics := NewAIGatewayMetrics(nil)
	orchestrator.WithMetrics(metrics)
	prepared := prepareTestRequest(t, orchestrator, "req-output-timeout", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	safetyRepo := &memorySafetyRepository{waitPolicy: true}
	budget := &memoryBudgetRepository{}
	orchestrator.governance = NewGovernanceService(
		NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef"), budget, &memoryResourceLimiter{},
	).WithMetrics(metrics)
	orchestrator.moderationTimeout = 10 * time.Millisecond
	orchestrator.activeTickets.Store(prepared.RequestID, &GovernanceTicket{
		Subject: SafetySubject{RequestID: prepared.RequestID, UserID: 3, ProjectID: 8, APIKeyID: 7}, Resource: &ResourceTicket{},
	})
	defer orchestrator.activeTickets.Delete(prepared.RequestID)

	startedAt := time.Now()
	err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{})
	if !errors.Is(err, ErrModerationUnavailable) || time.Since(startedAt) > time.Second {
		t.Fatalf("输出审核必须在独立期限内失败关闭: err=%v elapsed=%s", err, time.Since(startedAt))
	}
	metricText, metricErr := metrics.AIGatewayPrometheus(context.Background())
	if metricErr != nil || !strings.Contains(metricText, `molin_ai_gateway_rejections_total{rejection_reason="classifier_timeout"} 1`) {
		t.Fatalf("输出审核独立超时必须记为 classifier_timeout: err=%v\n%s", metricErr, metricText)
	}
	if len(budget.rejections) != 1 || budget.rejections[0].ReasonCode != "classifier_timeout" {
		t.Fatalf("输出审核超时必须形成一次脱敏拒绝事实: %+v", budget.rejections)
	}
}

func TestRequestOrchestratorSSEBlocksNormalizedKeywordAcrossSegments(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	separators := strings.Repeat("/\u200b", 300)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"网。\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"" + separators + "络赌博推广。\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{streamBody: io.NopCloser(strings.NewReader(stream))}})
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{RequestID: "req-sse-cross-segment", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true, Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true}})
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal([]safetyRule{{Code: "gambling-001", Category: "gambling", Keywords: []string{"网络赌博"}}})
	safetyRepo := &memorySafetyRepository{policy: &model.AISafetyPolicyVersion{ID: 1, VersionNo: 1, Status: model.AISafetyPolicyActive, RefusalMessage: DefaultSafetyRefusal, RulesJSON: rules}}
	orchestrator.governance = NewGovernanceService(NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef"), &memoryBudgetRepository{}, &memoryResourceLimiter{})
	orchestrator.activeTickets.Store(prepared.RequestID, &GovernanceTicket{Subject: SafetySubject{RequestID: prepared.RequestID, UserID: 3, ProjectID: 8, APIKeyID: 7}, Resource: &ResourceTicket{}})
	defer orchestrator.activeTickets.Delete(prepared.RequestID)
	sink := &memorySink{}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, sink); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sink.body.String(), "络赌博推广") || !strings.Contains(sink.body.String(), "molin.content_policy") {
		t.Fatalf("跨段且含大量可删除字符的违规内容不得泄漏: %s", sink.body.String())
	}
}

func TestRequestOrchestratorSSEBlocksKeywordAcrossLogprobsSegments(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	stream := "data: {\"choices\":[{\"logprobs\":{\"content\":[{\"token\":\"网络。\"}]}}]}\n\n" +
		"data: {\"choices\":[{\"logprobs\":{\"content\":[{\"token\":\"赌博推广。\"}]}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{streamBody: io.NopCloser(strings.NewReader(stream))}})
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{RequestID: "req-sse-logprobs-cross-segment", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true, Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true}})
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal([]safetyRule{{Code: "gambling-001", Category: "gambling", Keywords: []string{"网络赌博"}}})
	safetyRepo := &memorySafetyRepository{policy: &model.AISafetyPolicyVersion{ID: 1, VersionNo: 1, Status: model.AISafetyPolicyActive, RefusalMessage: DefaultSafetyRefusal, RulesJSON: rules}}
	orchestrator.governance = NewGovernanceService(NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef"), &memoryBudgetRepository{}, &memoryResourceLimiter{})
	orchestrator.activeTickets.Store(prepared.RequestID, &GovernanceTicket{Subject: SafetySubject{RequestID: prepared.RequestID, UserID: 3, ProjectID: 8, APIKeyID: 7}, Resource: &ResourceTicket{}})
	defer orchestrator.activeTickets.Delete(prepared.RequestID)
	sink := &memorySink{}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, sink); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sink.body.String(), "赌博推广") || !strings.Contains(sink.body.String(), "molin.content_policy") {
		t.Fatalf("跨 logprobs 分段的违规内容不得泄漏: %s", sink.body.String())
	}
	if len(store.usage[prepared.RequestID]) != 3 || safetyRepo.marked[prepared.RequestID] != model.AIModerationRejected {
		t.Fatalf("logprobs 违规输出仍须持久化 Usage 和审核终态: usage=%d status=%s", len(store.usage[prepared.RequestID]), safetyRepo.marked[prepared.RequestID])
	}
}

func TestRequestOrchestratorSSEBlocksViolatingToolArguments(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	stream := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"blocked phrase\"}}]}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{streamBody: io.NopCloser(strings.NewReader(stream))}})
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{RequestID: "req-sse-tool-safety", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true, Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true}})
	if err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal([]safetyRule{{Code: "illegal-001", Category: "illegal", Keywords: []string{"blocked phrase"}}})
	safetyRepo := &memorySafetyRepository{policy: &model.AISafetyPolicyVersion{ID: 1, VersionNo: 1, Status: model.AISafetyPolicyActive, RefusalMessage: DefaultSafetyRefusal, RulesJSON: rules}}
	orchestrator.governance = NewGovernanceService(NewSafetyService(safetyRepo, "0123456789abcdef0123456789abcdef"), &memoryBudgetRepository{}, &memoryResourceLimiter{})
	orchestrator.activeTickets.Store(prepared.RequestID, &GovernanceTicket{Subject: SafetySubject{RequestID: prepared.RequestID, UserID: 3, ProjectID: 8, APIKeyID: 7}, Resource: &ResourceTicket{}})
	defer orchestrator.activeTickets.Delete(prepared.RequestID)
	sink := &memorySink{}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, sink); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sink.body.String(), "blocked phrase") || !strings.Contains(sink.body.String(), "molin.content_policy") {
		t.Fatalf("违规工具参数不得泄漏: %s", sink.body.String())
	}
}

func TestRequestOrchestratorUnknownFailureAndIncompleteSSEDoNotFallback(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{executeErr: true}})
	prepared := prepareTestRequest(t, orchestrator, "req-timeout", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("上游结果未知应返回统一错误，实际: %v", err)
	}
	if store.requests[prepared.RequestID].ExecutionStatus != model.AIExecutionUnknown || len(store.usage[prepared.RequestID]) != 0 {
		t.Fatal("结果未知不得伪造 Usage 或自动 fallback")
	}

	store = newMemoryOrchestratorStore()
	orchestrator = newTestOrchestrator(store)
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{incomplete: true}})
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{RequestID: "req-incomplete-sse", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true, Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true}})
	if err != nil {
		t.Fatal(err)
	}
	sink := &memorySink{}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, sink); err != nil {
		t.Fatal(err)
	}
	if store.requests[prepared.RequestID].ExecutionStatus != model.AIExecutionUnknown || bytes.Contains(sink.body.Bytes(), []byte("[DONE]")) {
		t.Fatal("缺少 [DONE] 的 SSE 必须进入 unknown，且不能向客户端伪造成功终止符")
	}
}

func TestRequestOrchestratorRejectsConflictingStreamUpstreamReferences(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	stream := "data: {\"id\":\"upstream-one\",\"choices\":[{\"delta\":{\"content\":\"O\"}}]}\n\n" +
		"data: {\"id\":\"upstream-two\",\"choices\":[{\"delta\":{\"content\":\"K\"}}]}\n\n" +
		"data: [DONE]\n\n"
	orchestrator.SetExecutionDriverSelector(staticExecutionDriverSelector{driver: &fakeOrchestratorDriver{streamBody: io.NopCloser(strings.NewReader(stream))}})
	prepared, err := orchestrator.Prepare(context.Background(), PrepareCommand{
		RequestID: "req-sse-upstream-mismatch", UserID: 3, APIKeyID: 7, LogicalModel: "molin/qwen-turbo", Stream: true,
		Body: map[string]interface{}{"model": "molin/qwen-turbo", "stream": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Execute(context.Background(), prepared.RequestID, &memorySink{}); err != nil {
		t.Fatalf("上游引用冲突应收敛为可查询的未知终态: %v", err)
	}
	attempt := store.attempts[prepared.RequestID]
	if attempt.Status != "failed" || !attempt.ResultUnknown || attempt.ErrorClass == nil || *attempt.ErrorClass != "upstream_reference_mismatch" {
		t.Fatalf("流式上游引用冲突必须失败关闭且不得拼接不同请求证据: %+v", attempt)
	}
	if store.requests[prepared.RequestID].ExecutionStatus != model.AIExecutionUnknown {
		t.Fatalf("引用冲突的请求必须收敛为 unknown: %+v", store.requests[prepared.RequestID])
	}
	if attempt.UpstreamRequestID == nil || *attempt.UpstreamRequestID != "upstream-one" {
		t.Fatalf("冲突时只能保留首个已验证低敏引用: %+v", attempt.UpstreamRequestID)
	}
}

func TestWriteStreamBillingStatusEmitsMachineReadableEvent(t *testing.T) {
	sink := &memorySink{}
	if err := writeStreamBillingStatus(sink, "req-settlement-pending", ErrSettlementPending); err != nil {
		t.Fatal(err)
	}
	body := sink.body.String()
	if !strings.Contains(body, "event: molin.status") ||
		!strings.Contains(body, `"request_id":"req-settlement-pending"`) ||
		!strings.Contains(body, `"error":"settlement_pending"`) {
		t.Fatalf("流式结算状态事件不完整: %s", body)
	}
	if strings.Contains(body, "wallet") || strings.Contains(body, "database") {
		t.Fatalf("流式状态事件不得泄露内部实现: %s", body)
	}
}

func TestRequestOrchestratorReconcileNeverRetriesUnknownExecution(t *testing.T) {
	store := newMemoryOrchestratorStore()
	orchestrator := newTestOrchestrator(store)
	prepared := prepareTestRequest(t, orchestrator, "req-reconcile", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	if err := orchestrator.Reconcile(context.Background(), prepared.RequestID); err != nil {
		t.Fatal(err)
	}
	if store.requests[prepared.RequestID].ExecutionStatus != model.AIExecutionUnknown {
		t.Fatal("中断请求必须收敛为 unknown，禁止自动 fallback")
	}
	prepared = prepareTestRequest(t, orchestrator, "req-reconcile-running", "", map[string]interface{}{"model": "molin/qwen-turbo"})
	running := ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", ProviderModel: "bailian/qwen-turbo", StartedAt: time.Now(), Outcome: "running"}
	if err := store.StartRequest(context.Background(), prepared.RequestID, ptrAttempt(running.ToLedgerModel(prepared.RequestID, ExecutionUsage{}))); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Reconcile(context.Background(), prepared.RequestID); err != nil {
		t.Fatal(err)
	}
	if store.requests[prepared.RequestID].ExecutionStatus != model.AIExecutionUnknown || store.attempts[prepared.RequestID].Status != "unknown" || !store.attempts[prepared.RequestID].ResultUnknown {
		t.Fatal("Reconcile 必须同时收敛请求和运行中的执行尝试")
	}
	store.recoverable = []string{prepared.RequestID}
	store.recoverableStillStale = true
	count, err := orchestrator.ReconcileInterrupted(context.Background(), 100)
	if err != nil || count != 1 {
		t.Fatalf("恢复扫描必须消费仓储返回的遗留请求: count=%d err=%v", count, err)
	}
	store.recoverableStillStale = false
	count, err = orchestrator.ReconcileInterrupted(context.Background(), 100)
	if err != nil || count != 0 {
		t.Fatalf("扫描后已重新开始执行的请求不得误收敛: count=%d err=%v", count, err)
	}
}
