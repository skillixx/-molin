package service

import (
	"context"
	"errors"
	"sync"
	"time"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrImageAsyncUnavailable = errors.New("图片异步执行暂不可用")
	ErrImageQueueFull        = errors.New("图片任务队列已满")
)

const (
	imageJWTAPIKeyScopeMask        uint64 = 1 << 63
	imageStaleActiveRecoveryWindow        = 5 * time.Minute
)

type imageTaskPublisher interface {
	Publish(ctx context.Context, requestID string) error
}

// ImageResourceLimiter 是图片执行复用 G4 Redis 四维治理的最小注入合同。
// RestoreTicket 只用于 Prompt 丢失后的跨实例幂等释放，不能创建新租约。
type ImageResourceLimiter interface {
	Acquire(context.Context, string, uint64, uint64, uint64, string, uint64) (*ResourceTicket, error)
	RestoreTicket(string, uint64, uint64, uint64, string) (*ResourceTicket, error)
	StartHeartbeat(context.Context, *ResourceTicket) <-chan error
	Release(context.Context, *ResourceTicket) error
}

// ImageResourceSubject 冻结一次图片请求对应的四维资源主体。
// JWT 请求没有真实 API Key 时使用带高位标记的 Project 派生作用域，避免所有 JWT 请求落入 api_key:0。
type ImageResourceSubject struct {
	RequestID    string
	UserID       uint64
	ProjectID    uint64
	APIKeyID     uint64
	LogicalModel string
}

// ImageTaskDispatchCommand 同时携带内存 Prompt 命令与不含敏感信息的资源主体；RabbitMQ 仍只写 request_id。
type ImageTaskDispatchCommand struct {
	Command imagegateway.GenerateImageCommand
	Subject ImageResourceSubject
}

type imageTaskBilling interface {
	Execute(context.Context, string, imagegateway.GenerateImageCommand) (*ImageBillingExecution, error)
	CancelRequestBeforeExecution(context.Context, string) error
	CancelStaleReserved(context.Context, time.Time, int) (int, error)
	RecoverStaleActiveExecutions(context.Context, time.Time, int) (int, error)
	ImageRequestQueueState(context.Context, string) (imageQueueMessageState, error)
	LoadImageResourceSubject(context.Context, string) (ImageResourceSubject, error)
}

type imageQueueMessageState uint8

const (
	imageQueueStateUnknown imageQueueMessageState = iota
	imageQueueStateReserved
	imageQueueStateActive
	imageQueueStateInactive
)

type imagePendingCommand struct {
	command         imagegateway.GenerateImageCommand
	expiresAt       time.Time
	ticket          *ResourceTicket
	heartbeat       <-chan error
	heartbeatCancel context.CancelFunc
	cleanupOnly     bool
	cleanupAttempts uint8
}

// ImageTaskDispatcher 让 RabbitMQ 只携带 request_id，Prompt 保留在单实例有界内存中。
// Redis 租约从发布前持续到执行终态；重启或错实例丢失 Prompt 时按数据库归属重建票据并释放。
type ImageTaskDispatcher struct {
	mu               sync.Mutex
	jobs             map[string]imagePendingCommand
	queue            imageTaskPublisher
	billing          imageTaskBilling
	limiter          ImageResourceLimiter
	now              func() time.Time
	maxJobs          int
	jobTTL           time.Duration
	expiryWake       chan struct{}
	cleanupRetryBase time.Duration
	cleanupRetryMax  time.Duration
}

func NewImageTaskDispatcher(queue imageTaskPublisher, billing imageTaskBilling, limiter ImageResourceLimiter) (*ImageTaskDispatcher, error) {
	if queue == nil || billing == nil || limiter == nil {
		return nil, ErrImageAsyncUnavailable
	}
	return &ImageTaskDispatcher{
		jobs: make(map[string]imagePendingCommand), queue: queue, billing: billing, limiter: limiter,
		now: time.Now, maxJobs: 1000, jobTTL: 5 * time.Minute, expiryWake: make(chan struct{}, 1),
		cleanupRetryBase: time.Second, cleanupRetryMax: time.Minute,
	}, nil
}

func (d *ImageTaskDispatcher) Dispatch(ctx context.Context, request ImageTaskDispatchCommand) error {
	if d == nil || !validImageDispatchCommand(request) {
		return ErrImageAsyncUnavailable
	}
	ticket, err := d.limiter.Acquire(ctx, request.Subject.RequestID, request.Subject.UserID, request.Subject.ProjectID, request.Subject.APIKeyID, request.Subject.LogicalModel, 1)
	if err != nil {
		// 资源治理错误必须原样上抛，HTTP 层才能稳定映射 429 或 503。
		return err
	}
	leaseCtx, leaseCancel := context.WithCancel(context.Background())
	heartbeat := d.limiter.StartHeartbeat(leaseCtx, ticket)
	if heartbeat == nil {
		leaseCancel()
		d.releaseTicket(ctx, ticket)
		return ErrResourceUnavailable
	}
	job := imagePendingCommand{
		command: request.Command, expiresAt: d.now().UTC().Add(d.jobTTL), ticket: ticket,
		heartbeat: heartbeat, heartbeatCancel: leaseCancel,
	}

	d.mu.Lock()
	expired := d.pruneLocked()
	if len(d.jobs) >= d.maxJobs {
		d.mu.Unlock()
		d.cleanupExpired(ctx, expired)
		d.cleanupLease(ctx, job)
		return ErrImageQueueFull
	}
	if _, exists := d.jobs[request.Command.RequestID]; exists {
		d.mu.Unlock()
		d.cleanupExpired(ctx, expired)
		d.cleanupLease(ctx, job)
		return ErrImageAsyncUnavailable
	}
	d.jobs[request.Command.RequestID] = job
	d.mu.Unlock()
	d.signalExpiryWorker()
	d.cleanupExpired(ctx, expired)

	if err := d.queue.Publish(ctx, request.Command.RequestID); err != nil {
		d.mu.Lock()
		delete(d.jobs, request.Command.RequestID)
		d.mu.Unlock()
		d.signalExpiryWorker()
		d.cleanupLease(ctx, job)
		return ErrImageAsyncUnavailable
	}
	// 发布确认期间心跳若已失败，禁止把本机 Prompt 继续交给 Provider；队列消息会走恢复取消路径。
	select {
	case heartbeatErr, ok := <-heartbeat:
		if !ok || heartbeatErr != nil {
			d.mu.Lock()
			_, stillPending := d.jobs[request.Command.RequestID]
			if stillPending {
				delete(d.jobs, request.Command.RequestID)
			}
			d.mu.Unlock()
			d.signalExpiryWorker()
			// 消费侧已经取得任务时，由消费侧心跳监视器负责取消或收口，分发请求不覆盖其终态。
			if !stillPending {
				return nil
			}
			d.cleanupLease(ctx, job)
			_ = d.cancelBeforeExecution(ctx, request.Command.RequestID)
			return ErrResourceUnavailable
		}
	default:
	}
	return nil
}

// HandleImageTask 取得一次性内存 Prompt 后执行；没有 Prompt 表示重启或错实例，只释放未调用 Provider 的任务。
func (d *ImageTaskDispatcher) HandleImageTask(ctx context.Context, requestID string) error {
	if d == nil || requestID == "" {
		return ErrImageAsyncUnavailable
	}
	d.mu.Lock()
	job, exists := d.jobs[requestID]
	delete(d.jobs, requestID)
	d.mu.Unlock()
	d.signalExpiryWorker()
	if !exists {
		return d.releaseMissingPrompt(ctx, requestID)
	}
	if job.cleanupOnly {
		return d.cleanupCancellationOnly(ctx, job)
	}
	if !job.expiresAt.After(d.now().UTC()) {
		return d.cleanupCancellationOnly(ctx, job)
	}
	// Provider 调用前再次检查续租通道，Redis 已不可确认时直接取消预占并失败关闭。
	select {
	case heartbeatErr, ok := <-job.heartbeat:
		cleanupErr := d.cleanupCancellationOnly(ctx, job)
		if !ok || heartbeatErr == nil {
			heartbeatErr = ErrResourceUnavailable
		}
		return errors.Join(heartbeatErr, cleanupErr)
	default:
	}

	executionCtx, executionCancel := context.WithCancel(ctx)
	leaseResult := make(chan error, 1)
	watchDone := make(chan struct{})
	go func() {
		select {
		case heartbeatErr, ok := <-job.heartbeat:
			if !ok || heartbeatErr == nil {
				heartbeatErr = ErrResourceUnavailable
			}
			leaseResult <- heartbeatErr
			executionCancel()
		case <-watchDone:
			leaseResult <- nil
		}
	}()
	execution, executionErr := d.billing.Execute(executionCtx, requestID, job.command)
	close(watchDone)
	heartbeatErr := <-leaseResult
	executionCancel()
	var cancelErr error
	if execution == nil {
		if errors.Is(executionErr, ErrImageExecutionStarted) {
			state, stateErr := d.imageRequestQueueState(ctx, requestID)
			if stateErr != nil || state == imageQueueStateUnknown {
				d.stopImageHeartbeat(job)
				return errors.Join(executionErr, stateErr)
			}
			if state == imageQueueStateActive {
				// 其他worker持有真实执行权时，本消息只是重复投递；不释放共享Lease，也不写cancel intent。
				d.stopImageHeartbeat(job)
				return nil
			}
			if state == imageQueueStateInactive {
				executionErr = nil
			} else {
				cancelErr = d.cancelBeforeExecution(ctx, requestID)
				postState, postStateErr := d.imageRequestQueueState(ctx, requestID)
				if postStateErr != nil || postState == imageQueueStateUnknown {
					d.stopImageHeartbeat(job)
					return errors.Join(executionErr, cancelErr, postStateErr)
				}
				if postState == imageQueueStateActive {
					d.stopImageHeartbeat(job)
					return nil
				}
				if postState == imageQueueStateInactive {
					executionErr = nil
				}
			}
		} else {
			// claimExecution 未形成任何执行事实时使用独立窗口释放 Hold。
			cancelErr = d.cancelBeforeExecution(ctx, requestID)
			postState, postStateErr := d.imageRequestQueueState(ctx, requestID)
			if postStateErr != nil || postState == imageQueueStateUnknown || postState == imageQueueStateActive {
				d.stopImageHeartbeat(job)
				return errors.Join(executionErr, cancelErr, postStateErr)
			}
		}
	}
	releaseErr := d.cleanupLease(ctx, job)
	if cancelErr != nil || releaseErr != nil {
		d.scheduleCleanupRetry(job)
	}
	if heartbeatErr != nil {
		return errors.Join(heartbeatErr, cancelErr, releaseErr)
	}
	if cancelErr != nil || releaseErr != nil {
		return errors.Join(executionErr, cancelErr, releaseErr)
	}
	if errors.Is(executionErr, ErrImagePendingReconcile) || errors.Is(executionErr, imagegateway.ErrProviderFailed) ||
		errors.Is(executionErr, imagegateway.ErrImageResultInvalid) || errors.Is(executionErr, imagegateway.ErrModerationRejected) {
		return nil
	}
	return executionErr
}

func (d *ImageTaskDispatcher) releaseMissingPrompt(ctx context.Context, requestID string) error {
	state, err := d.imageRequestQueueState(ctx, requestID)
	if err != nil {
		return err
	}
	switch state {
	case imageQueueStateActive:
		// 错实例或重复消息不得触碰活跃worker的共享Lease和取消状态。
		return nil
	case imageQueueStateInactive:
		return d.releaseRestoredTicket(ctx, requestID)
	case imageQueueStateReserved:
		cancelErr := d.cancelBeforeExecution(ctx, requestID)
		if cancelErr != nil {
			return cancelErr
		}
		// 取消与claim存在竞态；只有复查确认不再活跃时才允许释放共享Lease。
		postState, stateErr := d.imageRequestQueueState(ctx, requestID)
		if stateErr != nil {
			return stateErr
		}
		if postState == imageQueueStateActive {
			return nil
		}
		return d.releaseRestoredTicket(ctx, requestID)
	default:
		return ErrImageAsyncUnavailable
	}
}

func (d *ImageTaskDispatcher) releaseRestoredTicket(ctx context.Context, requestID string) error {
	subject, err := d.billing.LoadImageResourceSubject(ctx, requestID)
	if err != nil {
		return err
	}
	ticket, err := d.limiter.RestoreTicket(subject.RequestID, subject.UserID, subject.ProjectID, subject.APIKeyID, subject.LogicalModel)
	if err != nil {
		return err
	}
	return d.releaseTicket(ctx, ticket)
}

func (d *ImageTaskDispatcher) cleanupExpired(ctx context.Context, jobs []imagePendingCommand) {
	for _, job := range jobs {
		_ = d.cleanupCancellationOnly(ctx, job)
	}
}

func (d *ImageTaskDispatcher) cleanupCancellationOnly(ctx context.Context, job imagePendingCommand) error {
	state, stateErr := d.imageRequestQueueState(ctx, job.command.RequestID)
	if stateErr != nil || state == imageQueueStateUnknown {
		// 状态不可确认时宁可让Redis租约自然过期，也不能误释放另一实例的活跃Lease。
		d.stopImageHeartbeat(job)
		d.scheduleCleanupRetry(job)
		return errors.Join(ErrImageAsyncUnavailable, stateErr)
	}
	if state == imageQueueStateActive {
		d.stopImageHeartbeat(job)
		return nil
	}
	if state == imageQueueStateInactive {
		return d.cleanupLease(ctx, job)
	}

	// 只有权威状态仍为reserved/pending/held时才尝试取消；随后复查避免与claim竞态时误释放。
	cancelErr := d.cancelBeforeExecution(ctx, job.command.RequestID)
	postState, postStateErr := d.imageRequestQueueState(ctx, job.command.RequestID)
	if postStateErr != nil || postState == imageQueueStateUnknown {
		d.stopImageHeartbeat(job)
		d.scheduleCleanupRetry(job)
		return errors.Join(cancelErr, postStateErr, ErrImageAsyncUnavailable)
	}
	if postState == imageQueueStateActive {
		d.stopImageHeartbeat(job)
		return cancelErr
	}
	releaseErr := d.cleanupLease(ctx, job)
	if cancelErr != nil || releaseErr != nil {
		d.scheduleCleanupRetry(job)
	}
	return errors.Join(cancelErr, releaseErr)
}

// scheduleCleanupRetry 清空 Prompt 后只保留取消所需的低敏事实；后续 Handle 或到期worker都不得进入Provider。
func (d *ImageTaskDispatcher) scheduleCleanupRetry(job imagePendingCommand) {
	job.cleanupOnly = true
	job.cleanupAttempts++
	job.command.Prompt = ""
	job.heartbeat = nil
	job.heartbeatCancel = nil
	job.expiresAt = d.now().UTC().Add(d.cleanupRetryDelay(job.cleanupAttempts))
	d.mu.Lock()
	if _, exists := d.jobs[job.command.RequestID]; !exists {
		d.jobs[job.command.RequestID] = job
	}
	d.mu.Unlock()
	d.signalExpiryWorker()
}

func (d *ImageTaskDispatcher) cleanupRetryDelay(attempt uint8) time.Duration {
	base, maximum := d.cleanupRetryBase, d.cleanupRetryMax
	if base <= 0 {
		base = time.Second
	}
	if maximum < base {
		maximum = base
	}
	delay := base
	for index := uint8(1); index < attempt && delay < maximum; index++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}

// StartExpiryWorker 独立收敛超过五分钟仍未执行的内存任务；不依赖后续请求触发 prune。
// ctx 取消时会停止计时器并清理尚未交给 Provider 的任务，防止心跳 goroutine、Redis 租约和 Hold 泄漏。
func (d *ImageTaskDispatcher) StartExpiryWorker(ctx context.Context, interval time.Duration) {
	if d == nil || ctx == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	nextStaleScan := time.Time{}
	for {
		if ctx.Err() != nil {
			d.cleanupExpired(ctx, d.takeAllPending())
			return
		}
		wallNow := time.Now()
		if !wallNow.Before(nextStaleScan) {
			d.recoverStaleDatabaseTasks(ctx)
			nextStaleScan = wallNow.Add(interval)
		}
		expired, wait, hasPending := d.expirySnapshot(interval)
		d.cleanupExpired(ctx, expired)
		if ctx.Err() != nil {
			d.cleanupExpired(ctx, d.takeAllPending())
			return
		}
		if len(expired) > 0 {
			continue
		}
		if !hasPending {
			wait = interval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			stopImageExpiryTimer(timer)
			d.cleanupExpired(ctx, d.takeAllPending())
			return
		case <-d.expiryWake:
			stopImageExpiryTimer(timer)
		case <-timer.C:
		}
	}
}

func (d *ImageTaskDispatcher) recoverStaleDatabaseTasks(ctx context.Context) {
	reservedCtx, cancelReserved := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	_, _ = d.billing.CancelStaleReserved(reservedCtx, d.now().UTC().Add(-d.jobTTL), 100)
	cancelReserved()
	activeCtx, cancelActive := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	_, _ = d.billing.RecoverStaleActiveExecutions(activeCtx, d.now().UTC().Add(-imageStaleActiveRecoveryWindow), 100)
	cancelActive()
}

func (d *ImageTaskDispatcher) expirySnapshot(maxWait time.Duration) ([]imagePendingCommand, time.Duration, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	expired := d.pruneLocked()
	if len(d.jobs) == 0 {
		return expired, maxWait, false
	}
	now := d.now().UTC()
	wait := maxWait
	for _, job := range d.jobs {
		remaining := job.expiresAt.Sub(now)
		if remaining <= 0 {
			return expired, time.Nanosecond, true
		}
		if remaining < wait {
			wait = remaining
		}
	}
	return expired, wait, true
}

func (d *ImageTaskDispatcher) takeAllPending() []imagePendingCommand {
	d.mu.Lock()
	defer d.mu.Unlock()
	jobs := make([]imagePendingCommand, 0, len(d.jobs))
	for requestID, job := range d.jobs {
		jobs = append(jobs, job)
		delete(d.jobs, requestID)
	}
	return jobs
}

func (d *ImageTaskDispatcher) signalExpiryWorker() {
	if d == nil || d.expiryWake == nil {
		return
	}
	select {
	case d.expiryWake <- struct{}{}:
	default:
	}
}

func stopImageExpiryTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (d *ImageTaskDispatcher) cleanupLease(ctx context.Context, job imagePendingCommand) error {
	d.stopImageHeartbeat(job)
	return d.releaseTicket(ctx, job.ticket)
}

func (d *ImageTaskDispatcher) stopImageHeartbeat(job imagePendingCommand) {
	if job.heartbeatCancel != nil {
		job.heartbeatCancel()
	}
}

func (d *ImageTaskDispatcher) releaseTicket(ctx context.Context, ticket *ResourceTicket) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return d.limiter.Release(releaseCtx, ticket)
}

func (d *ImageTaskDispatcher) cancelBeforeExecution(ctx context.Context, requestID string) error {
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return d.billing.CancelRequestBeforeExecution(cancelCtx, requestID)
}

func (d *ImageTaskDispatcher) imageRequestQueueState(ctx context.Context, requestID string) (imageQueueMessageState, error) {
	queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return d.billing.ImageRequestQueueState(queryCtx, requestID)
}

func (d *ImageTaskDispatcher) pruneLocked() []imagePendingCommand {
	now := d.now().UTC()
	expired := make([]imagePendingCommand, 0)
	for requestID, job := range d.jobs {
		if !job.expiresAt.After(now) {
			expired = append(expired, job)
			delete(d.jobs, requestID)
		}
	}
	return expired
}

func validImageDispatchCommand(request ImageTaskDispatchCommand) bool {
	return request.Command.RequestID != "" && request.Command.Prompt != "" && request.Command.Count == 1 &&
		request.Subject.RequestID == request.Command.RequestID && request.Subject.LogicalModel == request.Command.ModelCode &&
		request.Subject.UserID != 0 && request.Subject.ProjectID != 0 && request.Subject.APIKeyID != 0
}

func imageResourceSubject(requestID, logicalModel string, userID, projectID uint64, apiKeyID *uint64) (ImageResourceSubject, error) {
	if requestID == "" || logicalModel == "" || userID == 0 || projectID == 0 {
		return ImageResourceSubject{}, ErrResourceUnavailable
	}
	resourceAPIKeyID := uint64(0)
	if apiKeyID != nil {
		resourceAPIKeyID = *apiKeyID
	} else {
		if projectID >= imageJWTAPIKeyScopeMask {
			return ImageResourceSubject{}, ErrResourceUnavailable
		}
		resourceAPIKeyID = projectID | imageJWTAPIKeyScopeMask
	}
	if resourceAPIKeyID == 0 {
		return ImageResourceSubject{}, ErrResourceUnavailable
	}
	return ImageResourceSubject{
		RequestID: requestID, UserID: userID, ProjectID: projectID, APIKeyID: resourceAPIKeyID, LogicalModel: logicalModel,
	}, nil
}

// LoadImageResourceSubject 从 MySQL 请求事实恢复四维主体，不信任 RabbitMQ 消息携带任何归属信息。
func (s *ImageBillingService) LoadImageResourceSubject(ctx context.Context, requestID string) (ImageResourceSubject, error) {
	if s == nil || s.db == nil || requestID == "" {
		return ImageResourceSubject{}, ErrImageAsyncUnavailable
	}
	var request model.AIRequest
	if err := s.db.WithContext(ctx).Select("request_id", "user_id", "project_id", "api_key_id", "logical_model_code").
		Where("request_id = ? AND modality = ?", requestID, "image").First(&request).Error; err != nil || request.ProjectID == nil {
		return ImageResourceSubject{}, ErrImageAsyncUnavailable
	}
	return imageResourceSubject(request.RequestID, request.LogicalModelCode, request.UserID, *request.ProjectID, request.APIKeyID)
}

// CancelStaleReserved 扫描进程在 Hold 提交后、入队前崩溃留下的陈旧任务；候选只来自数据库低敏事实。
// RequestCancel 会重新锁定并复查状态，若 claim 已开始只记录取消意图，不会误释放执行中的 Hold。
func (s *ImageBillingService) CancelStaleReserved(ctx context.Context, staleBefore time.Time, limit int) (int, error) {
	if s == nil || s.db == nil || staleBefore.IsZero() {
		return 0, ErrImageAsyncUnavailable
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	type staleReservation struct {
		TaskPublicID string  `gorm:"column:task_public_id"`
		RequestID    string  `gorm:"column:request_id"`
		UserID       uint64  `gorm:"column:user_id"`
		ProjectID    uint64  `gorm:"column:project_id"`
		APIKeyID     *uint64 `gorm:"column:api_key_id"`
	}
	var candidates []staleReservation
	if err := s.db.WithContext(ctx).Table("ai_gateway_tasks AS task").
		Select("task.public_id AS task_public_id, task.request_id, task.user_id, task.project_id, task.api_key_id").
		Joins("JOIN ai_requests AS request ON request.request_id = task.request_id").
		Where("task.status = ? AND task.created_at <= ? AND request.created_at <= ?", model.AIImageTaskReserved, staleBefore, staleBefore).
		Where("request.modality = ? AND request.execution_status = ? AND request.billing_status = ?", "image", model.AIExecutionPending, model.AIBillingHeld).
		Order("task.id ASC").Limit(limit).Scan(&candidates).Error; err != nil {
		return 0, err
	}
	completed := 0
	var recoveryErr error
	for _, candidate := range candidates {
		owner := repository.ImageOwner{UserID: candidate.UserID, ProjectID: candidate.ProjectID, APIKeyID: candidate.APIKeyID}
		err := retryImageBillingTransaction(ctx, func() error {
			_, cancelErr := s.RequestCancel(ctx, candidate.TaskPublicID, owner)
			return cancelErr
		})
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
			continue
		}
		completed++
	}
	return completed, recoveryErr
}

// ImageRequestQueueState 读取队列消息对应的权威三态，调用方据此决定取消、保留活跃Lease或幂等Ack。
func (s *ImageBillingService) ImageRequestQueueState(ctx context.Context, requestID string) (imageQueueMessageState, error) {
	if s == nil || s.db == nil || requestID == "" {
		return imageQueueStateUnknown, ErrImageAsyncUnavailable
	}
	var state struct {
		TaskStatus      string `gorm:"column:task_status"`
		ExecutionStatus string `gorm:"column:execution_status"`
		BillingStatus   string `gorm:"column:billing_status"`
	}
	if err := s.db.WithContext(ctx).Table("ai_gateway_tasks AS task").
		Select("task.status AS task_status, request.execution_status, request.billing_status").
		Joins("JOIN ai_requests AS request ON request.request_id = task.request_id").
		Where("task.request_id = ? AND request.modality = ?", requestID, "image").Take(&state).Error; err != nil {
		return imageQueueStateUnknown, err
	}
	activeTask := state.TaskStatus == model.AIImageTaskSubmitted || state.TaskStatus == model.AIImageTaskProcessing ||
		state.TaskStatus == model.AIImageTaskStoring || state.TaskStatus == model.AIImageTaskModerating
	if state.ExecutionStatus == model.AIExecutionRunning || activeTask {
		return imageQueueStateActive, nil
	}
	if state.TaskStatus == model.AIImageTaskReserved && state.ExecutionStatus == model.AIExecutionPending && state.BillingStatus == model.AIBillingHeld {
		return imageQueueStateReserved, nil
	}
	if state.TaskStatus == model.AIImageTaskPendingReconcile || state.BillingStatus == model.AIBillingSettlementPending {
		return imageQueueStateInactive, nil
	}
	taskTerminal := imageTaskTerminalForCancel(state.TaskStatus)
	executionTerminal := state.ExecutionStatus == model.AIExecutionSucceeded || state.ExecutionStatus == model.AIExecutionFailed || state.ExecutionStatus == model.AIExecutionCancelled
	billingTerminal := state.BillingStatus == model.AIBillingSettled || state.BillingStatus == model.AIBillingReleased || state.BillingStatus == model.AIBillingException
	if taskTerminal && executionTerminal && billingTerminal {
		return imageQueueStateInactive, nil
	}
	return imageQueueStateUnknown, nil
}

var _ imagegateway.ImageTaskMessageHandler = (*ImageTaskDispatcher)(nil)
