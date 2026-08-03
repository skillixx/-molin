package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/sms/model"
	"molin/server/internal/modules/sms/repository"
	"molin/server/internal/modules/sms/sender"
)

// concurrentTestSendRepository 用内存互斥模型复现数据库唯一抢占语义，专门验证并发调用边界。
type concurrentTestSendRepository struct {
	mu      sync.Mutex
	nextID  uint64
	byOwner map[string]*model.SendLog
}

func newConcurrentTestSendRepository() *concurrentTestSendRepository {
	return &concurrentTestSendRepository{byOwner: make(map[string]*model.SendLog)}
}

func (r *concurrentTestSendRepository) ReserveTestSend(_ context.Context, log *model.SendLog) (*model.SendLog, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner := *log.IdempotencyOwnerKeyHash
	if existing := r.byOwner[owner]; existing != nil {
		if existing.RequestFingerprint == nil || log.RequestFingerprint == nil || *existing.RequestFingerprint != *log.RequestFingerprint {
			return nil, false, repository.ErrTestSendIdempotencyConflict
		}
		copyLog := *existing
		return &copyLog, false, nil
	}
	r.nextID++
	copyLog := *log
	copyLog.ID = r.nextID
	r.byOwner[owner] = &copyLog
	return &copyLog, true, nil
}

func (r *concurrentTestSendRepository) CompleteTestSend(_ context.Context, id uint64, status string, providerRequestID, providerCode, failureSummary *string, completedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, log := range r.byOwner {
		if log.ID != id || log.SubmitStatus != "pending" {
			continue
		}
		log.SubmitStatus = status
		log.ProviderRequestID = providerRequestID
		log.ProviderCode = providerCode
		log.FailureSummary = failureSummary
		log.CompletedAt = &completedAt
		return nil
	}
	return errors.New("测试发送终态记录不存在")
}

type blockingTestDispatcher struct {
	sendCalls  atomic.Int32
	started    chan struct{}
	release    chan struct{}
	once       sync.Once
	prepareErr error
	sendErr    error
}

func (d *blockingTestDispatcher) Prepare(_ context.Context, scene, _ string) (PreparedSend, error) {
	if d.prepareErr != nil {
		return PreparedSend{}, d.prepareErr
	}
	return PreparedSend{Scene: scene, TemplateID: 7, TemplateCode: "SMS_SAFE", SignName: "固定签名", Provider: "aliyun"}, nil
}

func (d *blockingTestDispatcher) SendProvider(ctx context.Context, _ PreparedSend, _, rawCode, _ string) (DispatchResult, error) {
	d.sendCalls.Add(1)
	if len(rawCode) != 6 {
		return DispatchResult{}, errors.New("验证码长度错误")
	}
	if d.started != nil {
		d.once.Do(func() { close(d.started) })
	}
	if d.release != nil {
		select {
		case <-d.release:
		case <-ctx.Done():
			return DispatchResult{}, ctx.Err()
		}
	}
	if d.sendErr != nil {
		return DispatchResult{ProviderCode: "SAFE_REJECTED"}, d.sendErr
	}
	return DispatchResult{Accepted: true, ProviderRequestID: "provider-safe", ProviderCode: "OK"}, nil
}

type atomicTestLimiter struct {
	calls   atomic.Int32
	allowed bool
	err     error
}

func (l *atomicTestLimiter) Allow(context.Context, uint64, string) (bool, int64, error) {
	l.calls.Add(1)
	return l.allowed, 30, l.err
}

func newSecurityTestService(repo smsAdminTestSendRepository, dispatcher smsTestDispatcher, limiter smsTestSendLimiter) *SMSAdminService {
	svc := NewSMSAdminService(repo)
	svc.testConfig = testSendConfig()
	svc.testDispatcher = dispatcher
	svc.testLimiter = limiter
	return svc
}

func TestAdminTestSendConcurrentReplayCallsProviderOnce(t *testing.T) {
	repo := newConcurrentTestSendRepository()
	dispatcher := &blockingTestDispatcher{started: make(chan struct{}), release: make(chan struct{})}
	limiter := &atomicTestLimiter{allowed: true}
	svc := newSecurityTestService(repo, dispatcher, limiter)

	firstDone := make(chan error, 1)
	go func() {
		_, err := svc.TestSend(context.Background(), 10, 7, "register", "phone-test-a", "concurrent-key")
		firstDone <- err
	}()
	select {
	case <-dispatcher.started:
	case <-time.After(2 * time.Second):
		t.Fatal("首次测试发送未进入供应商调用")
	}

	const replayCount = 16
	var wait sync.WaitGroup
	wait.Add(replayCount)
	errorsSeen := make(chan error, replayCount)
	for i := 0; i < replayCount; i++ {
		go func() {
			defer wait.Done()
			_, err := svc.TestSend(context.Background(), 10, 7, "register", "phone-test-a", "concurrent-key")
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if !errors.Is(err, ErrSMSTestSendPending) {
			t.Fatalf("并发重放必须看到处理中冲突，实际: %v", err)
		}
	}
	close(dispatcher.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("首次测试发送应被受理: %v", err)
	}
	if dispatcher.sendCalls.Load() != 1 || limiter.calls.Load() != 1 {
		t.Fatalf("并发重放不得二次限流或发送: send=%d limiter=%d", dispatcher.sendCalls.Load(), limiter.calls.Load())
	}
}

func TestAdminTestSendSameAdminKeyChangedSceneConflicts(t *testing.T) {
	repo := newConcurrentTestSendRepository()
	dispatcher := &blockingTestDispatcher{}
	limiter := &atomicTestLimiter{allowed: true}
	svc := newSecurityTestService(repo, dispatcher, limiter)

	if _, err := svc.TestSend(context.Background(), 10, 7, "register", "phone-test-a", "stable-key"); err != nil {
		t.Fatalf("首次测试发送失败: %v", err)
	}
	if _, err := svc.TestSend(context.Background(), 10, 7, "login", "phone-test-a", "stable-key"); !errors.Is(err, ErrSMSTestSendIdempotencyConflict) {
		t.Fatalf("同管理员复用 key 修改场景必须冲突: %v", err)
	}
	if dispatcher.sendCalls.Load() != 1 || limiter.calls.Load() != 1 {
		t.Fatal("参数冲突不得消耗新限流次数或调用供应商")
	}
}

func TestAdminTestSendDifferentAdminsDoNotShareKey(t *testing.T) {
	repo := newConcurrentTestSendRepository()
	dispatcher := &blockingTestDispatcher{}
	limiter := &atomicTestLimiter{allowed: true}
	svc := newSecurityTestService(repo, dispatcher, limiter)
	for _, adminID := range []uint64{10, 11} {
		if _, err := svc.TestSend(context.Background(), adminID, 7, "register", "phone-test-a", "shared-key"); err != nil {
			t.Fatalf("不同管理员不得串用幂等结果: admin=%d err=%v", adminID, err)
		}
	}
	if dispatcher.sendCalls.Load() != 2 || limiter.calls.Load() != 2 {
		t.Fatal("不同管理员应各自完成一次限流和供应商调用")
	}
}

func TestAdminTestSendWhitelistAndLimiterRejectBeforeProvider(t *testing.T) {
	t.Run("白名单拒绝", func(t *testing.T) {
		repo := newConcurrentTestSendRepository()
		dispatcher := &blockingTestDispatcher{prepareErr: ErrPhoneNotAllowed}
		limiter := &atomicTestLimiter{allowed: true}
		svc := newSecurityTestService(repo, dispatcher, limiter)
		if _, err := svc.TestSend(context.Background(), 10, 7, "register", "phone-not-allowed", "whitelist-key"); !errors.Is(err, ErrSMSInvalidRequest) {
			t.Fatalf("非白名单号码应在抢占前拒绝: %v", err)
		}
		if repo.nextID != 0 || limiter.calls.Load() != 0 || dispatcher.sendCalls.Load() != 0 {
			t.Fatal("白名单拒绝不得落库、限流或外呼")
		}
	})

	t.Run("双维限流拒绝", func(t *testing.T) {
		repo := newConcurrentTestSendRepository()
		dispatcher := &blockingTestDispatcher{}
		limiter := &atomicTestLimiter{allowed: false}
		svc := newSecurityTestService(repo, dispatcher, limiter)
		result, err := svc.TestSend(context.Background(), 10, 7, "register", "phone-test-a", "limited-key")
		if !errors.Is(err, ErrSMSTestSendRateLimited) || result.RetryAfterSeconds != 30 {
			t.Fatalf("限流错误或恢复时间错误: result=%#v err=%v", result, err)
		}
		if dispatcher.sendCalls.Load() != 0 {
			t.Fatal("限流拒绝不得调用供应商")
		}
		if _, replayErr := svc.TestSend(context.Background(), 10, 7, "register", "phone-test-a", "limited-key"); !errors.Is(replayErr, ErrSMSTestSendRateLimited) {
			t.Fatalf("限流结果必须幂等重放: %v", replayErr)
		}
		if limiter.calls.Load() != 1 {
			t.Fatal("限流结果重放不得增加限流计数")
		}
	})
}

func TestAdminTestSendProviderFailureConvergesAndReplaysSafeError(t *testing.T) {
	repo := newConcurrentTestSendRepository()
	providerErr := sender.NewProviderError(sender.ErrorKindSignature, "isv.SMS_SIGNATURE_ILLEGAL", errors.New("private provider body"))
	dispatcher := &blockingTestDispatcher{sendErr: providerErr}
	limiter := &atomicTestLimiter{allowed: true}
	svc := newSecurityTestService(repo, dispatcher, limiter)

	if _, err := svc.TestSend(context.Background(), 10, 7, "register", "phone-test-a", "provider-key"); !errors.Is(err, ErrSMSTestSendProviderFailed) {
		t.Fatalf("供应商拒绝必须映射为安全错误: %v", err)
	}
	if _, err := svc.TestSend(context.Background(), 10, 7, "register", "phone-test-a", "provider-key"); !errors.Is(err, ErrSMSTestSendProviderFailed) {
		t.Fatalf("供应商失败结果必须幂等重放: %v", err)
	}
	if dispatcher.sendCalls.Load() != 1 || limiter.calls.Load() != 1 {
		t.Fatal("供应商失败重放不得二次限流或外呼")
	}
	for _, log := range repo.byOwner {
		if log.SubmitStatus != "failed" || log.FailureSummary == nil || *log.FailureSummary != "短信签名不可用" {
			t.Fatalf("失败记录必须只保留安全摘要: %#v", log)
		}
	}
}
