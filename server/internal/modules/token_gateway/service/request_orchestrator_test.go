package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

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
func (s *memoryOrchestratorStore) FinalizeRequest(_ context.Context, requestID string, attempt model.AIExecutionAttempt, usage []model.AIUsageItem, disconnected bool, _, _ *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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

type fakeOrchestratorDriver struct {
	executeErr bool
	incomplete bool
}

func (f *fakeOrchestratorDriver) Name() string { return "bifrost" }
func (f *fakeOrchestratorDriver) ChatCompletion(_ context.Context, req ExecutionRequest) (*ExecutionResponse, error) {
	now := time.Now()
	if f.executeErr {
		return &ExecutionResponse{Attempt: ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", ProviderModel: "bailian/qwen-turbo", StartedAt: now.Add(-time.Millisecond), FinishedAt: now, Outcome: "timeout", ErrorClass: "network_timeout", ResultUnknown: true}}, context.DeadlineExceeded
	}
	return &ExecutionResponse{
		Response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(`{"id":"ok","choices":[{"message":{"content":"OK"}}]}`))},
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
	return &ExecutionResponse{
		Response: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewBufferString(body))},
		Attempt:  ExecutionAttempt{AttemptNo: 1, Driver: "bifrost", ProviderCode: "bailian", EndpointCode: "bailian", ProviderModel: "bailian/qwen-turbo", StartedAt: now, Outcome: "success"},
	}, nil
}
func (f *fakeOrchestratorDriver) NormalizeStreamLine(line []byte, logicalModel string) (ExecutionStreamChunk, error) {
	return normalizeExecutionStreamLine(line, logicalModel)
}

type memorySink struct {
	status     int
	body       bytes.Buffer
	failWrite  bool
	failHeader bool
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
	if s.failWrite {
		return errors.New("客户端已断开")
	}
	_, _ = s.body.Write(data)
	return nil
}
func (s *memorySink) Flush() error { return nil }

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
