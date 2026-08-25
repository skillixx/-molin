package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	imagegateway "molin/server/internal/modules/token_gateway/image"
)

type fakeImageTaskPublisher struct {
	calls int
	err   error
}

func (p *fakeImageTaskPublisher) Publish(context.Context, string) error {
	p.calls++
	return p.err
}

type fakeImageTaskBilling struct {
	mu                sync.Mutex
	executeCalls      int
	providerCalls     int
	cancelCalls       int
	executeErr        error
	executeStarted    chan struct{}
	cancelErrors      []error
	subject           ImageResourceSubject
	staleCalls        int
	staleResult       int
	staleErr          error
	activeStaleCalls  int
	activeStaleResult int
	activeStaleErr    error
	terminalCalls     int
	terminal          bool
	terminalErr       error
	queueState        imageQueueMessageState
}

func (b *fakeImageTaskBilling) Execute(ctx context.Context, _ string, _ imagegateway.GenerateImageCommand) (*ImageBillingExecution, error) {
	b.mu.Lock()
	b.executeCalls++
	started := b.executeStarted
	err := b.executeErr
	b.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.providerCalls++
	b.mu.Unlock()
	if started != nil {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, err
}

func (b *fakeImageTaskBilling) CancelRequestBeforeExecution(context.Context, string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancelCalls++
	if b.cancelCalls <= len(b.cancelErrors) {
		return b.cancelErrors[b.cancelCalls-1]
	}
	return nil
}

func (b *fakeImageTaskBilling) LoadImageResourceSubject(context.Context, string) (ImageResourceSubject, error) {
	if b.subject.RequestID == "" {
		return ImageResourceSubject{}, ErrImageAsyncUnavailable
	}
	return b.subject, nil
}

func (b *fakeImageTaskBilling) CancelStaleReserved(context.Context, time.Time, int) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.staleCalls++
	return b.staleResult, b.staleErr
}

func (b *fakeImageTaskBilling) RecoverStaleActiveExecutions(context.Context, time.Time, int) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeStaleCalls++
	return b.activeStaleResult, b.activeStaleErr
}

func (b *fakeImageTaskBilling) ImageRequestQueueState(context.Context, string) (imageQueueMessageState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.terminalCalls++
	if b.terminalErr != nil {
		return imageQueueStateUnknown, b.terminalErr
	}
	if b.queueState != imageQueueStateUnknown {
		return b.queueState, nil
	}
	if b.terminal {
		return imageQueueStateInactive, nil
	}
	if b.executeStarted != nil && b.providerCalls > 0 {
		return imageQueueStateActive, nil
	}
	return imageQueueStateReserved, nil
}

type fakeImageResourceLimiter struct {
	mu                 sync.Mutex
	acquireCalls       int
	restoreCalls       int
	heartbeatCalls     int
	releaseCalls       int
	acquiredSubject    ImageResourceSubject
	acquireErr         error
	onAcquire          func()
	heartbeatFailure   chan error
	restoredRequestID  string
	restoredResourceID uint64
}

func (l *fakeImageResourceLimiter) Acquire(_ context.Context, requestID string, userID, projectID, apiKeyID uint64, logicalModel string, _ uint64) (*ResourceTicket, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acquireCalls++
	l.acquiredSubject = ImageResourceSubject{RequestID: requestID, UserID: userID, ProjectID: projectID, APIKeyID: apiKeyID, LogicalModel: logicalModel}
	onAcquire := l.onAcquire
	if l.acquireErr != nil {
		return nil, l.acquireErr
	}
	if onAcquire != nil {
		onAcquire()
	}
	return &ResourceTicket{LeaseID: requestID, Scopes: []string{"user", "project", "api_key", "model"}, Keys: make([]string, 20)}, nil
}

func (l *fakeImageResourceLimiter) RestoreTicket(requestID string, _, _, apiKeyID uint64, _ string) (*ResourceTicket, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.restoreCalls++
	l.restoredRequestID = requestID
	l.restoredResourceID = apiKeyID
	return &ResourceTicket{LeaseID: requestID, Scopes: []string{"user", "project", "api_key", "model"}, Keys: make([]string, 20)}, nil
}

func (l *fakeImageResourceLimiter) StartHeartbeat(ctx context.Context, _ *ResourceTicket) <-chan error {
	l.mu.Lock()
	l.heartbeatCalls++
	failure := l.heartbeatFailure
	l.mu.Unlock()
	if failure != nil {
		return failure
	}
	result := make(chan error)
	go func() {
		<-ctx.Done()
		close(result)
	}()
	return result
}

func (l *fakeImageResourceLimiter) Release(context.Context, *ResourceTicket) error {
	l.mu.Lock()
	l.releaseCalls++
	l.mu.Unlock()
	return nil
}

func imageDispatchFixture() ImageTaskDispatchCommand {
	return ImageTaskDispatchCommand{
		Command: imagegateway.GenerateImageCommand{RequestID: "img_req_dispatch_0001", ModelCode: "molin/image", Prompt: "生成图片", Count: 1},
		Subject: ImageResourceSubject{RequestID: "img_req_dispatch_0001", UserID: 11, ProjectID: 22, APIKeyID: 33, LogicalModel: "molin/image"},
	}
}

func TestImageTaskDispatcherAcquiresFourScopesAndReleasesAtTerminal(t *testing.T) {
	publisher := &fakeImageTaskPublisher{}
	billing := &fakeImageTaskBilling{}
	limiter := &fakeImageResourceLimiter{}
	dispatcher, err := NewImageTaskDispatcher(publisher, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	command := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.HandleImageTask(context.Background(), command.Command.RequestID); err != nil {
		t.Fatal(err)
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if publisher.calls != 1 || limiter.acquireCalls != 1 || limiter.heartbeatCalls != 1 || limiter.releaseCalls != 1 || limiter.acquiredSubject != command.Subject {
		t.Fatalf("异步租约生命周期错误: publish=%d acquire=%d heartbeat=%d release=%d subject=%+v", publisher.calls, limiter.acquireCalls, limiter.heartbeatCalls, limiter.releaseCalls, limiter.acquiredSubject)
	}
}

func TestImageTaskDispatcherFailsClosedAndReleasesOnPublishFailure(t *testing.T) {
	publisher := &fakeImageTaskPublisher{err: errors.New("队列不可用")}
	limiter := &fakeImageResourceLimiter{}
	dispatcher, err := NewImageTaskDispatcher(publisher, &fakeImageTaskBilling{}, limiter)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(context.Background(), imageDispatchFixture()); !errors.Is(err, ErrImageAsyncUnavailable) {
		t.Fatalf("队列失败必须失败关闭: %v", err)
	}
	if limiter.releaseCalls != 1 {
		t.Fatalf("队列发布失败必须释放租约: %d", limiter.releaseCalls)
	}
}

func TestImageTaskDispatcherQueueFullReturns429ClassAndReleasesNewLease(t *testing.T) {
	publisher := &fakeImageTaskPublisher{}
	billing := &fakeImageTaskBilling{}
	limiter := &fakeImageResourceLimiter{}
	dispatcher, err := NewImageTaskDispatcher(publisher, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.maxJobs = 1
	first := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := imageDispatchFixture()
	second.Command.RequestID = "img_req_dispatch_0002"
	second.Subject.RequestID = second.Command.RequestID
	if err := dispatcher.Dispatch(context.Background(), second); !errors.Is(err, ErrImageQueueFull) {
		t.Fatalf("本地队列满载必须返回可映射429的稳定错误: %v", err)
	}
	if publisher.calls != 1 || billing.executeCalls != 0 || limiter.acquireCalls != 2 || limiter.releaseCalls != 1 {
		t.Fatalf("满载不得发布或调用Provider且必须释放新租约: publish=%d execute=%d acquire=%d release=%d", publisher.calls, billing.executeCalls, limiter.acquireCalls, limiter.releaseCalls)
	}
	if err := dispatcher.HandleImageTask(context.Background(), first.Command.RequestID); err != nil {
		t.Fatal(err)
	}
}

func TestImageTaskDispatcherMapsLimiterRejectionBeforePublish(t *testing.T) {
	limitErr := &ResourceLimitError{Cause: ErrConcurrencyExceeded, LimitScope: "project", LimitType: "concurrency", RetryAfter: time.Second}
	publisher := &fakeImageTaskPublisher{}
	limiter := &fakeImageResourceLimiter{acquireErr: limitErr}
	dispatcher, err := NewImageTaskDispatcher(publisher, &fakeImageTaskBilling{}, limiter)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Dispatch(context.Background(), imageDispatchFixture()); !errors.Is(err, ErrConcurrencyExceeded) {
		t.Fatalf("并发拒绝必须保留资源错误: %v", err)
	}
	if publisher.calls != 0 || limiter.releaseCalls != 0 {
		t.Fatalf("准入失败不得发布或伪释放: publish=%d release=%d", publisher.calls, limiter.releaseCalls)
	}
}

func TestImageTaskDispatcherRestoresAndReleasesLeaseWhenPromptMissing(t *testing.T) {
	subject := ImageResourceSubject{RequestID: "img_req_missing_0001", UserID: 101, ProjectID: 202, APIKeyID: 303, LogicalModel: "molin/image"}
	billing := &fakeImageTaskBilling{subject: subject}
	limiter := &fakeImageResourceLimiter{}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.HandleImageTask(context.Background(), subject.RequestID); err != nil {
		t.Fatal(err)
	}
	if limiter.restoreCalls != 1 || limiter.releaseCalls != 1 || limiter.restoredRequestID != subject.RequestID || billing.cancelCalls != 1 {
		t.Fatalf("Prompt丢失恢复释放错误: restore=%d release=%d request=%s cancel=%d", limiter.restoreCalls, limiter.releaseCalls, limiter.restoredRequestID, billing.cancelCalls)
	}
}

func TestImageTaskDispatcherHeartbeatFailureCancelsBeforeProvider(t *testing.T) {
	failure := make(chan error, 1)
	limiter := &fakeImageResourceLimiter{heartbeatFailure: failure}
	billing := &fakeImageTaskBilling{}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	command := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	failure <- ErrResourceUnavailable
	if err := dispatcher.HandleImageTask(context.Background(), command.Command.RequestID); !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("心跳失效必须失败关闭: %v", err)
	}
	if billing.executeCalls != 0 || billing.cancelCalls != 1 || limiter.releaseCalls != 1 {
		t.Fatalf("心跳失效不得调用Provider且必须取消释放: execute=%d cancel=%d release=%d", billing.executeCalls, billing.cancelCalls, limiter.releaseCalls)
	}
}

func TestImageTaskDispatcherPreCancelledExecutionReleasesHoldAndLease(t *testing.T) {
	limiter := &fakeImageResourceLimiter{}
	billing := &fakeImageTaskBilling{}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	command := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	cancelExecution()
	if err := dispatcher.HandleImageTask(executionCtx, command.Command.RequestID); !errors.Is(err, context.Canceled) {
		t.Fatalf("执行权领取前取消必须保留context错误: %v", err)
	}
	billing.mu.Lock()
	executeCalls, providerCalls, cancelCalls := billing.executeCalls, billing.providerCalls, billing.cancelCalls
	billing.mu.Unlock()
	limiter.mu.Lock()
	releaseCalls := limiter.releaseCalls
	limiter.mu.Unlock()
	if executeCalls != 1 || providerCalls != 0 || cancelCalls != 1 || releaseCalls != 1 {
		t.Fatalf("预取消不得调用Provider且必须释放Hold和租约: execute=%d provider=%d cancel=%d release=%d", executeCalls, providerCalls, cancelCalls, releaseCalls)
	}
}

func TestImageTaskDispatcherClaimErrorWithoutExecutionReleasesHoldAndLease(t *testing.T) {
	claimErr := errors.New("数据库锁冲突")
	limiter := &fakeImageResourceLimiter{}
	billing := &fakeImageTaskBilling{executeErr: claimErr}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	command := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.HandleImageTask(context.Background(), command.Command.RequestID); !errors.Is(err, claimErr) {
		t.Fatalf("claim普通错误必须原样返回: %v", err)
	}
	billing.mu.Lock()
	providerCalls, cancelCalls := billing.providerCalls, billing.cancelCalls
	billing.mu.Unlock()
	limiter.mu.Lock()
	releaseCalls := limiter.releaseCalls
	limiter.mu.Unlock()
	if providerCalls != 0 || cancelCalls != 1 || releaseCalls != 1 {
		t.Fatalf("claim普通错误不得调用Provider且必须释放Hold和租约: provider=%d cancel=%d release=%d", providerCalls, cancelCalls, releaseCalls)
	}
}

func TestImageTaskDispatcherCancelledTerminalMessageIsAcked(t *testing.T) {
	limiter := &fakeImageResourceLimiter{}
	billing := &fakeImageTaskBilling{executeErr: ErrImageExecutionStarted, terminal: true}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	command := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.HandleImageTask(context.Background(), command.Command.RequestID); err != nil {
		t.Fatalf("已取消终态消息必须幂等Ack: %v", err)
	}
	billing.mu.Lock()
	providerCalls, cancelCalls, terminalCalls := billing.providerCalls, billing.cancelCalls, billing.terminalCalls
	billing.mu.Unlock()
	if providerCalls != 0 || cancelCalls != 0 || terminalCalls != 1 {
		t.Fatalf("终态确认不得调用Provider且必须核对状态: provider=%d cancel=%d terminal_query=%d", providerCalls, cancelCalls, terminalCalls)
	}
}

func TestImageTaskDispatcherRunningExecutionConflictIsNotSwallowed(t *testing.T) {
	limiter := &fakeImageResourceLimiter{}
	billing := &fakeImageTaskBilling{executeErr: ErrImageExecutionStarted, queueState: imageQueueStateUnknown, terminalErr: errors.New("状态不可确认")}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	command := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.HandleImageTask(context.Background(), command.Command.RequestID); !errors.Is(err, ErrImageExecutionStarted) {
		t.Fatalf("真实并发执行冲突不得伪装终态Ack: %v", err)
	}
}

func TestImageTaskDispatcherWrongInstanceDoesNotReleaseActiveWorkerLease(t *testing.T) {
	started := make(chan struct{})
	request := imageDispatchFixture()
	billing := &fakeImageTaskBilling{executeStarted: started, subject: request.Subject}
	limiter := &fakeImageResourceLimiter{}
	workerOne, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	workerTwo, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	if err := workerOne.Dispatch(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	workerOneCtx, stopWorkerOne := context.WithCancel(context.Background())
	workerOneResult := make(chan error, 1)
	go func() {
		workerOneResult <- workerOne.HandleImageTask(workerOneCtx, request.Command.RequestID)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker1未进入唯一Provider执行")
	}

	if err := workerTwo.HandleImageTask(context.Background(), request.Command.RequestID); err != nil {
		t.Fatalf("错实例重复消息应按active状态Ack: %v", err)
	}
	billing.mu.Lock()
	providerCalls, cancelCalls := billing.providerCalls, billing.cancelCalls
	billing.mu.Unlock()
	limiter.mu.Lock()
	releaseCalls := limiter.releaseCalls
	limiter.mu.Unlock()
	if providerCalls != 1 || cancelCalls != 0 || releaseCalls != 0 {
		t.Fatalf("错实例不得触碰活跃执行: provider=%d cancel=%d release=%d", providerCalls, cancelCalls, releaseCalls)
	}

	stopWorkerOne()
	select {
	case <-workerOneResult:
	case <-time.After(time.Second):
		t.Fatal("worker1取消后未收口")
	}
}

func TestImageTaskDispatcherExpiredLocalJobDoesNotReleaseActiveWorkerLease(t *testing.T) {
	request := imageDispatchFixture()
	billing := &fakeImageTaskBilling{queueState: imageQueueStateActive}
	limiter := &fakeImageResourceLimiter{}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	dispatcher.now = func() time.Time { return base }
	if err := dispatcher.Dispatch(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	dispatcher.now = func() time.Time { return base.Add(6 * time.Minute) }
	if err := dispatcher.HandleImageTask(context.Background(), request.Command.RequestID); err != nil {
		t.Fatalf("已由其他worker执行的过期本地任务应安全Ack: %v", err)
	}
	billing.mu.Lock()
	cancelCalls := billing.cancelCalls
	billing.mu.Unlock()
	limiter.mu.Lock()
	releaseCalls := limiter.releaseCalls
	limiter.mu.Unlock()
	if cancelCalls != 0 || releaseCalls != 0 {
		t.Fatalf("过期清理不得触碰活跃worker: cancel=%d release=%d", cancelCalls, releaseCalls)
	}
}

func TestImageTaskDispatcherExpiresQueuedPromptAtFiveMinutes(t *testing.T) {
	base := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	limiter := &fakeImageResourceLimiter{}
	billing := &fakeImageTaskBilling{}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.now = func() time.Time { return base }
	command := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	dispatcher.now = func() time.Time { return base.Add(5*time.Minute + time.Nanosecond) }
	if err := dispatcher.HandleImageTask(context.Background(), command.Command.RequestID); err != nil {
		t.Fatal(err)
	}
	if billing.executeCalls != 0 || billing.cancelCalls != 1 || limiter.releaseCalls != 1 {
		t.Fatalf("排队超过五分钟必须取消并释放: execute=%d cancel=%d release=%d", billing.executeCalls, billing.cancelCalls, limiter.releaseCalls)
	}
}

func TestImageTaskDispatcherExpiryWorkerCancelsWithoutNewTraffic(t *testing.T) {
	limiter := &fakeImageResourceLimiter{}
	billing := &fakeImageTaskBilling{}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.jobTTL = 40 * time.Millisecond
	command := imageDispatchFixture()
	if err := dispatcher.Dispatch(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerStopped := make(chan struct{})
	go func() {
		dispatcher.StartExpiryWorker(workerCtx, 10*time.Millisecond)
		close(workerStopped)
	}()

	deadline := time.After(time.Second)
	for {
		billing.mu.Lock()
		cancelCalls, executeCalls := billing.cancelCalls, billing.executeCalls
		billing.mu.Unlock()
		limiter.mu.Lock()
		releaseCalls := limiter.releaseCalls
		limiter.mu.Unlock()
		if cancelCalls == 1 && releaseCalls == 1 {
			if executeCalls != 0 {
				t.Fatalf("到期清理不得调用Provider: %d", executeCalls)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("无后续流量时到期任务未自动收口: cancel=%d release=%d", cancelCalls, releaseCalls)
		case <-time.After(5 * time.Millisecond):
		}
	}
	stopWorker()
	select {
	case <-workerStopped:
	case <-time.After(time.Second):
		t.Fatal("到期worker未响应context停止")
	}
	dispatcher.mu.Lock()
	remaining := len(dispatcher.jobs)
	dispatcher.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("到期后不得保留内存Prompt: %d", remaining)
	}
}

func TestImageTaskDispatcherExpiryWorkerScansStaleDatabaseWithEmptyMap(t *testing.T) {
	billing := &fakeImageTaskBilling{staleResult: 1, activeStaleResult: 1}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, &fakeImageResourceLimiter{})
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerStopped := make(chan struct{})
	go func() {
		dispatcher.StartExpiryWorker(workerCtx, 10*time.Millisecond)
		close(workerStopped)
	}()
	deadline := time.After(time.Second)
	for {
		billing.mu.Lock()
		staleCalls, activeStaleCalls, providerCalls := billing.staleCalls, billing.activeStaleCalls, billing.providerCalls
		billing.mu.Unlock()
		if staleCalls > 0 && activeStaleCalls > 0 {
			if providerCalls != 0 {
				t.Fatalf("数据库陈旧任务恢复不得调用Provider: %d", providerCalls)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("空map时到期worker未扫描数据库陈旧reserved/active任务")
		case <-time.After(5 * time.Millisecond):
		}
	}
	stopWorker()
	select {
	case <-workerStopped:
	case <-time.After(time.Second):
		t.Fatal("数据库陈旧任务扫描worker未停止")
	}
}

func TestImageTaskDispatcherExpiryWorkerStopReleasesPendingJob(t *testing.T) {
	limiter := &fakeImageResourceLimiter{}
	billing := &fakeImageTaskBilling{}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.jobTTL = time.Hour
	if err := dispatcher.Dispatch(context.Background(), imageDispatchFixture()); err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerStopped := make(chan struct{})
	go func() {
		dispatcher.StartExpiryWorker(workerCtx, time.Minute)
		close(workerStopped)
	}()
	stopWorker()
	select {
	case <-workerStopped:
	case <-time.After(time.Second):
		t.Fatal("到期worker停止时存在goroutine泄漏")
	}
	billing.mu.Lock()
	cancelCalls, executeCalls := billing.cancelCalls, billing.executeCalls
	billing.mu.Unlock()
	limiter.mu.Lock()
	releaseCalls := limiter.releaseCalls
	limiter.mu.Unlock()
	if cancelCalls != 1 || releaseCalls != 1 || executeCalls != 0 {
		t.Fatalf("worker停止必须取消未执行任务并释放租约: cancel=%d release=%d execute=%d", cancelCalls, releaseCalls, executeCalls)
	}
}

func TestImageTaskDispatcherExpiryRetriesCancellationOnlyUntilSuccess(t *testing.T) {
	limiter := &fakeImageResourceLimiter{}
	billing := &fakeImageTaskBilling{cancelErrors: []error{errors.New("首次数据库取消失败"), nil}}
	dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, billing, limiter)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.jobTTL = 20 * time.Millisecond
	dispatcher.cleanupRetryBase = 10 * time.Millisecond
	dispatcher.cleanupRetryMax = 20 * time.Millisecond
	if err := dispatcher.Dispatch(context.Background(), imageDispatchFixture()); err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerStopped := make(chan struct{})
	go func() {
		dispatcher.StartExpiryWorker(workerCtx, 5*time.Millisecond)
		close(workerStopped)
	}()
	deadline := time.After(time.Second)
	for {
		billing.mu.Lock()
		cancelCalls, providerCalls := billing.cancelCalls, billing.providerCalls
		billing.mu.Unlock()
		dispatcher.mu.Lock()
		remaining := len(dispatcher.jobs)
		dispatcher.mu.Unlock()
		if cancelCalls >= 2 && remaining == 0 {
			if providerCalls != 0 {
				t.Fatalf("cancellation-only重试不得调用Provider: %d", providerCalls)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("取消首败后未自动重试成功: cancel=%d remaining=%d", cancelCalls, remaining)
		case <-time.After(5 * time.Millisecond):
		}
	}
	stopWorker()
	select {
	case <-workerStopped:
	case <-time.After(time.Second):
		t.Fatal("重试完成后到期worker未停止")
	}
}

func TestImageResourceSubjectSeparatesJWTFromRealAPIKeyScope(t *testing.T) {
	jwtSubject, err := imageResourceSubject("img_req_jwt_0001", "molin/image", 11, 22, nil)
	if err != nil {
		t.Fatal(err)
	}
	realKey := uint64(22)
	keySubject, err := imageResourceSubject("img_req_key_0001", "molin/image", 11, 22, &realKey)
	if err != nil {
		t.Fatal(err)
	}
	if jwtSubject.APIKeyID != imageJWTAPIKeyScopeMask|22 || keySubject.APIKeyID != 22 || jwtSubject.APIKeyID == keySubject.APIKeyID {
		t.Fatalf("JWT 派生作用域不得与真实 API Key 冲突: jwt=%d key=%d", jwtSubject.APIKeyID, keySubject.APIKeyID)
	}
}
