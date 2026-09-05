package token_gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	auditmodel "molin/server/internal/modules/audit/model"
	auditrepo "molin/server/internal/modules/audit/repository"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
)

type VideoRuntimeSecrets struct {
	Quote, Payload, Callback, AdminReason, Download []byte `json:"-"`
}

type VideoRuntimeDeps struct {
	DB               *gorm.DB
	Recovery         *repository.VideoCapacityRecoveryRepository
	Capacity         *service.RedisVideoCapacityStore
	CapacityNonce    *service.VideoCapacityNonceKey
	Provider         video.VideoProviderAdapter
	ObjectStore      video.VideoObjectStore
	UploadStore      service.VideoUploadStore
	ImportStore      service.VideoInputImportStore
	Publisher        *video.TaskPublisher
	Consumer         *video.TaskConsumer
	WorkerID         string
	AdminVerifyHours int
	Secrets          VideoRuntimeSecrets
}

// VideoRuntime只复用原HTTP、财务、任务、资产与Outbox账本，统一持有G7基础设施运行入口。
type VideoRuntime struct {
	UserApp       *service.VideoHTTPService
	AdminApp      *service.VideoAdminService
	CallbackApp   *service.VideoCallbackService
	Outbox        *service.OutboxWorker
	Consumer      *video.TaskConsumer
	TaskHandler   *service.VideoRabbitTaskHandler
	ObjectScanner *service.VideoObjectReconciliationScanner
	OrphanCleanup *service.VideoOrphanCleanupWorker
	MissingRepair *service.VideoObjectMissingWorker
	InputCleanup  *service.VideoInputRetentionWorker
	OutputCleanup *service.VideoOutputRetentionWorker
	db            *gorm.DB
	workerID      string
	lifecycleMu   sync.Mutex
	healthMu      sync.RWMutex
	cancel        context.CancelFunc
	done          chan struct{}
	started       bool
	components    map[string]VideoRuntimeComponentHealth
	workerRunner  videoTaskWorkerRunner
	poisonState   func(context.Context, video.TaskStage) (bool, error)
	poisonBlock   func(context.Context, video.TaskStage, string) error
	retryDelay    time.Duration
}

type videoTaskWorkerRunner interface {
	RunWorkers(context.Context, video.TaskStage, int, video.TaskMessageHandler) error
}

type VideoRuntimeComponentHealth struct {
	Up            bool
	FailureCount  uint64
	LastSuccessAt time.Time
	LastFailureAt time.Time
}

func NewVideoRuntime(deps VideoRuntimeDeps) (*VideoRuntime, error) {
	if deps.DB == nil || deps.Recovery == nil || deps.Capacity == nil || deps.CapacityNonce == nil || deps.Provider == nil || deps.ObjectStore == nil || deps.UploadStore == nil || deps.ImportStore == nil || deps.Publisher == nil || deps.Consumer == nil || deps.WorkerID == "" || deps.AdminVerifyHours < 0 {
		return nil, service.ErrVideoGovernanceUnavailable
	}
	mediaDeleteStore, ok := deps.ObjectStore.(service.VideoMediaDeleteStore)
	if !ok || !mediaDeleteStore.SupportsSynchronousDeletion() {
		return nil, service.ErrVideoGovernanceUnavailable
	}
	inventory, ok := deps.ObjectStore.(video.VideoObjectInventory)
	if !ok {
		return nil, service.ErrVideoGovernanceUnavailable
	}
	protector, err := service.NewVideoTaskPayloadProtector("vid-g7-payload-v1", deps.Secrets.Payload)
	if err != nil {
		return nil, err
	}
	promptSecret := deriveVideoRuntimeSecret(deps.Secrets.Payload, "prompt-hmac-v1")
	intentSecret := deriveVideoRuntimeSecret(deps.Secrets.Payload, "intent-hmac-v1")
	defer clear(promptSecret)
	defer clear(intentSecret)
	safety := video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess))
	app, err := service.NewVideoHTTPService(deps.DB, service.VideoBillingOptions{
		QuoteSecret: deps.Secrets.Quote, PromptSecret: promptSecret, IntentSecret: intentSecret,
		Protector: protector, Safety: safety,
	}, service.VideoHTTPOptions{
		Uploads:      &service.VideoUploadOptions{Store: deps.UploadStore, SourceBucket: "ai-upload-temp", NormalizedBucket: "ai-result", ModerationPolicyVersion: "vid-g7-reference-v1", MaxUserReservedBytes: 128 << 20},
		Imports:      &service.VideoInputImportOptions{Store: deps.ImportStore, NormalizedBucket: "ai-result", ModerationPolicyVersion: "vid-g7-reference-v1", MaxUserReservedBytes: 128 << 20},
		ContentStore: deps.ObjectStore, MediaDeleteStore: mediaDeleteStore, DownloadSigningSecret: deps.Secrets.Download,
	})
	if err != nil {
		return nil, err
	}
	if err := app.EnableVideoCapacityReservation(deps.Recovery, deps.Capacity, deps.CapacityNonce); err != nil {
		return nil, err
	}
	callback, err := service.NewVideoCallbackService(app, service.VideoCallbackOptions{FakeOnlyEnabled: true, SigningSecret: deps.Secrets.Callback})
	if err != nil {
		return nil, err
	}
	reasons, err := service.NewVideoAdminReasonProtector("vid-g7-admin-v1", deps.Secrets.AdminReason)
	if err != nil {
		return nil, err
	}
	admin, err := service.NewVideoAdminService(app, deps.AdminVerifyHours, service.VideoAdminWriteOptions{ReasonProtector: reasons, DLQConsumer: deps.Consumer})
	if err != nil {
		return nil, err
	}
	factory, err := service.NewVideoRuntimeGatewayFactory(service.VideoRuntimeGatewayDependencies{DB: deps.DB, App: app, Recovery: deps.Recovery, Capacity: deps.Capacity, NonceKey: deps.CapacityNonce, Provider: deps.Provider, Store: deps.ObjectStore})
	if err != nil {
		return nil, err
	}
	finalizer, err := service.NewVideoRabbitTaskFinalizer(app, deps.Recovery, deps.Capacity, deps.CapacityNonce)
	if err != nil {
		return nil, err
	}
	handler, err := service.NewVideoRabbitTaskHandler(deps.DB, deps.Publisher, factory, finalizer, deps.WorkerID)
	if err != nil {
		return nil, err
	}
	transport, err := service.NewVideoOutboxPublisher(deps.DB, deps.Publisher)
	if err != nil {
		return nil, err
	}
	outbox := service.NewOutboxWorker(repository.NewVideoOutboxRepository(deps.DB), transport)
	scanner, err := service.NewVideoObjectReconciliationScanner(deps.DB, inventory, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	orphanCleanup, err := service.NewVideoOrphanCleanupWorker(scanner, inventory, deps.WorkerID+"-orphan")
	if err != nil {
		return nil, err
	}
	missingRepair, err := service.NewVideoObjectMissingWorker(deps.DB, inventory, deps.WorkerID+"-missing")
	if err != nil {
		return nil, err
	}
	inputCleanup, err := service.NewVideoInputRetentionWorker(app, deps.WorkerID+"-input")
	if err != nil {
		return nil, err
	}
	outputCleanup, err := service.NewVideoOutputRetentionWorker(app)
	if err != nil {
		return nil, err
	}
	runtime := &VideoRuntime{UserApp: app, AdminApp: admin, CallbackApp: callback, Outbox: outbox, Consumer: deps.Consumer, TaskHandler: handler, ObjectScanner: scanner, OrphanCleanup: orphanCleanup, MissingRepair: missingRepair, InputCleanup: inputCleanup, OutputCleanup: outputCleanup, db: deps.DB, workerID: deps.WorkerID, components: map[string]VideoRuntimeComponentHealth{}, workerRunner: deps.Consumer, retryDelay: 2 * time.Second}
	runtime.poisonState = runtime.rabbitPoisonBlocked
	runtime.poisonBlock = runtime.blockRabbitPoison
	return runtime, nil
}

// Start在模块启用后运行已有任务收口；是否接收新流量由HTTP Handler的独立开关决定。
func (r *VideoRuntime) Start(ctx context.Context) error {
	if r == nil || ctx == nil || ctx.Err() != nil || r.Outbox == nil || r.Consumer == nil || r.workerRunner == nil || r.TaskHandler == nil || r.ObjectScanner == nil || r.OrphanCleanup == nil || r.MissingRepair == nil || r.InputCleanup == nil || r.OutputCleanup == nil || r.poisonState == nil || r.poisonBlock == nil || r.retryDelay <= 0 {
		return service.ErrVideoGovernanceUnavailable
	}
	r.lifecycleMu.Lock()
	if r.started {
		r.lifecycleMu.Unlock()
		return service.ErrVideoGovernanceUnavailable
	}
	runtimeCtx, cancel := context.WithCancel(ctx)
	r.cancel, r.done, r.started = cancel, make(chan struct{}), true
	done := r.done
	r.lifecycleMu.Unlock()
	var workers sync.WaitGroup
	r.launchPeriodic(&workers, runtimeCtx, "outbox", 5*time.Second, func(call context.Context) error { _, err := r.Outbox.RunOnce(call, 50); return err })
	r.launchPeriodic(&workers, runtimeCtx, "orphan_cleanup", time.Minute, func(call context.Context) error { _, err := r.OrphanCleanup.RunOnce(call); return err })
	r.launchPeriodic(&workers, runtimeCtx, "missing_repair", time.Minute, func(call context.Context) error { _, err := r.MissingRepair.RunOnce(call); return err })
	r.launchPeriodic(&workers, runtimeCtx, "input_retention", time.Minute, func(call context.Context) error { _, err := r.InputCleanup.RunOnce(call, 100); return err })
	r.launchPeriodic(&workers, runtimeCtx, "output_retention", time.Minute, func(call context.Context) error { _, err := r.OutputCleanup.RunOnce(call, 100); return err })
	r.launchPeriodic(&workers, runtimeCtx, "object_scanner", time.Minute, func(call context.Context) error {
		if _, err := r.ObjectScanner.ScanExpected(call, 500); err != nil {
			return err
		}
		_, err := r.ObjectScanner.ScanStorage(call, 500)
		return err
	})
	for _, item := range []struct {
		stage   video.TaskStage
		workers int
	}{{video.TaskSubmit, 2}, {video.TaskPoll, 2}, {video.TaskFetch, 2}} {
		item := item
		workers.Add(1)
		go func() {
			defer workers.Done()
			r.runConsumerGroup(runtimeCtx, item.stage, item.workers)
		}()
	}
	go func() {
		workers.Wait()
		close(done)
	}()
	return nil
}

func (r *VideoRuntime) runConsumerGroup(ctx context.Context, stage video.TaskStage, workers int) {
	name := "consumer_" + string(stage)
	for ctx.Err() == nil {
		blocked, err := r.poisonState(ctx, stage)
		if err != nil || blocked {
			if err == nil {
				err = ErrVideoRabbitPoisonFuse
			}
			r.recordComponent(name, err)
			return
		}
		r.recordComponent(name, nil)
		err = r.workerRunner.RunWorkers(ctx, stage, workers, r.TaskHandler)
		if ctx.Err() == nil && err != nil {
			r.recordComponent(name, err)
			if digest, invalid := video.TaskMessageInvalidSHA256(err); invalid {
				if blockErr := r.poisonBlock(ctx, stage, digest); blockErr != nil {
					r.recordComponent(name, blockErr)
				}
				return
			}
		}
		if !waitVideoRuntimeRetry(ctx, r.retryDelay) {
			return
		}
	}
}

var ErrVideoRabbitPoisonFuse = errors.New("视频Rabbit队列因非法消息持久熔断")

type videoRabbitPoisonFuse struct {
	Stage            string
	Status           string
	BodySHA256       *string
	BlockedAuditID   *uint64
	RecoveredAuditID *uint64
	VersionNo        uint64
}

// rabbitPoisonBlocked只读取受Trigger保护的固定状态表，普通审计插入不能解除熔断。
func (r *VideoRuntime) rabbitPoisonBlocked(ctx context.Context, stage video.TaskStage) (bool, error) {
	if r == nil || r.db == nil {
		return true, service.ErrVideoGovernanceUnavailable
	}
	var row videoRabbitPoisonFuse
	err := r.db.WithContext(ctx).Table("ai_video_rabbit_poison_fuses").Where("stage=?", string(stage)).Take(&row).Error
	if err != nil || row.Stage != string(stage) || row.VersionNo == 0 {
		return true, service.ErrVideoGovernanceUnavailable
	}
	if row.Status == "ready" {
		initial := row.BodySHA256 == nil && row.BlockedAuditID == nil && row.RecoveredAuditID == nil
		recovered := row.BodySHA256 != nil && len(*row.BodySHA256) == 64 && row.BlockedAuditID != nil && row.RecoveredAuditID != nil
		if !initial && !recovered {
			return true, service.ErrVideoGovernanceUnavailable
		}
		return false, nil
	}
	if row.Status != "blocked" || row.BodySHA256 == nil || len(*row.BodySHA256) != 64 || row.BlockedAuditID == nil || row.RecoveredAuditID != nil {
		return true, service.ErrVideoGovernanceUnavailable
	}
	return true, nil
}

func (r *VideoRuntime) blockRabbitPoison(ctx context.Context, stage video.TaskStage, bodySHA256 string) error {
	if r == nil || r.db == nil || len(bodySHA256) != 64 {
		return service.ErrVideoGovernanceUnavailable
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var fuse videoRabbitPoisonFuse
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Table("ai_video_rabbit_poison_fuses").Where("stage=?", string(stage)).Take(&fuse).Error; err != nil || fuse.VersionNo == 0 {
			return service.ErrVideoGovernanceUnavailable
		}
		if fuse.Status == "blocked" {
			if fuse.BodySHA256 != nil && *fuse.BodySHA256 == bodySHA256 {
				return nil
			}
			return ErrVideoRabbitPoisonFuse
		}
		if fuse.Status != "ready" {
			return service.ErrVideoGovernanceUnavailable
		}
		raw, err := json.Marshal(map[string]any{"schema": 1, "stage": stage, "body_sha256": bodySHA256, "result": "blocked"})
		if err != nil {
			return err
		}
		target, targetID, summary := "video_queue", string(stage), string(raw)
		row := auditmodel.AuditLog{Module: "token_gateway", Action: "video_rabbit_poison_blocked", TargetType: &target, TargetID: &targetID, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
		if err := auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &row); err != nil {
			return err
		}
		result := tx.Table("ai_video_rabbit_poison_fuses").Where("stage=? AND status='ready' AND version_no=?", string(stage), fuse.VersionNo).Updates(map[string]any{"status": "blocked", "body_sha256": bodySHA256, "blocked_audit_id": row.ID, "recovered_audit_id": nil, "version_no": gorm.Expr("version_no+1"), "updated_at": time.Now().UTC()})
		if result.Error != nil || result.RowsAffected != 1 {
			return service.ErrVideoGovernanceUnavailable
		}
		return nil
	})
}

func (r *VideoRuntime) launchPeriodic(workers *sync.WaitGroup, ctx context.Context, name string, interval time.Duration, action func(context.Context) error) {
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			err := action(ctx)
			if ctx.Err() == nil {
				r.recordComponent(name, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (r *VideoRuntime) recordComponent(name string, err error) {
	r.healthMu.Lock()
	defer r.healthMu.Unlock()
	state := r.components[name]
	if err == nil {
		state.Up, state.LastSuccessAt = true, time.Now().UTC()
	} else {
		state.Up, state.LastFailureAt, state.FailureCount = false, time.Now().UTC(), state.FailureCount+1
	}
	r.components[name] = state
}

func (r *VideoRuntime) HealthSnapshot() map[string]VideoRuntimeComponentHealth {
	r.healthMu.RLock()
	defer r.healthMu.RUnlock()
	result := make(map[string]VideoRuntimeComponentHealth, len(r.components))
	for name, state := range r.components {
		result[name] = state
	}
	return result
}

// Shutdown先取消新领取，再等待Consumer在途处理和后台扫描退出；调用方只能在成功后关闭Redis/Rabbit/MinIO依赖。
func (r *VideoRuntime) Shutdown(ctx context.Context) error {
	if r == nil || ctx == nil {
		return service.ErrVideoGovernanceUnavailable
	}
	r.lifecycleMu.Lock()
	if !r.started {
		r.lifecycleMu.Unlock()
		return nil
	}
	cancel, done := r.cancel, r.done
	r.lifecycleMu.Unlock()
	cancel()
	select {
	case <-done:
		r.lifecycleMu.Lock()
		r.started, r.cancel, r.done = false, nil, nil
		r.lifecycleMu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitVideoRuntimeRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func deriveVideoRuntimeSecret(root []byte, purpose string) []byte {
	digest := hmac.New(sha256.New, root)
	_, _ = digest.Write([]byte("molin/video/runtime/" + purpose))
	return digest.Sum(nil)
}
