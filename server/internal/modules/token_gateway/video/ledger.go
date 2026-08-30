package video

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrGatewayTaskNotFound   = errors.New("视频网关任务不存在")
	ErrGatewayTaskConflict   = errors.New("视频网关任务CAS冲突")
	ErrGatewayTaskTransition = errors.New("视频网关任务状态流转不允许")
	ErrCallbackBodyConflict  = errors.New("同一回调事件正文哈希冲突")
	ErrCallbackTaskMismatch  = errors.New("Provider任务标识错绑")
)

type TaskStatus string

const (
	TaskCreated          TaskStatus = "created"
	TaskReserved         TaskStatus = "reserved"
	TaskQueued           TaskStatus = "queued"
	TaskSubmitting       TaskStatus = "submitting"
	TaskSubmitted        TaskStatus = "submitted"
	TaskProcessing       TaskStatus = "processing"
	TaskFetching         TaskStatus = "fetching"
	TaskStoring          TaskStatus = "storing"
	TaskModerating       TaskStatus = "moderating"
	TaskLabeling         TaskStatus = "labeling"
	TaskSucceeded        TaskStatus = "succeeded"
	TaskFailed           TaskStatus = "failed"
	TaskCancelled        TaskStatus = "cancelled"
	TaskExpired          TaskStatus = "expired"
	TaskPendingReconcile TaskStatus = "pending_reconcile"
)

type AssetLifecycle string

const (
	AssetTemporary    AssetLifecycle = "temporary"
	AssetAvailable    AssetLifecycle = "available"
	AssetQuarantined  AssetLifecycle = "quarantined"
	AssetExpiring     AssetLifecycle = "expiring"
	AssetDeleting     AssetLifecycle = "deleting"
	AssetDeleted      AssetLifecycle = "deleted"
	AssetDeleteFailed AssetLifecycle = "delete_failed"
)

type AssetModerationStatus string

const (
	AssetModerationPending  AssetModerationStatus = "pending"
	AssetModerationPassed   AssetModerationStatus = "passed"
	AssetModerationRejected AssetModerationStatus = "rejected"
	AssetModerationError    AssetModerationStatus = "error"
)

type GatewayTaskEvent struct {
	EventID    string
	FromStatus TaskStatus
	ToStatus   TaskStatus
	Source     string
	Reason     string
	CreatedAt  time.Time
}

type GatewayAsset struct {
	AssetID             string
	Role                string
	ParentAssetID       string
	Object              StoredVideoObject `json:"-"`
	MIMEType            string
	SizeBytes           uint64
	SHA256              string
	Width               uint32
	Height              uint32
	DurationMillis      uint64
	FrameRate           uint32
	VideoCodec          string
	AudioCodec          string
	HasAudio            bool
	ModerationPassed    bool
	ModerationStatus    AssetModerationStatus
	ExplicitLabelStatus LabelStatus
	ImplicitLabelStatus LabelStatus
	LabelVersion        string
	Lifecycle           AssetLifecycle
	MediaDeleted        bool
	Children            []GatewayAsset
}

type GatewayTask struct {
	// DeferDelivery由财务仓储装配，不接受客户端字段；媒体成功后仍等待独立结算和交付事务。
	DeferDelivery     bool `json:"-"`
	TaskID            string
	RequestID         string
	Operation         string
	Prompt            string                    `json:"-"`
	Input             *ControlledInputRef       `json:"-"`
	Reference         *NormalizedReferenceImage `json:"-"`
	Spec              VideoSpec
	Status            TaskStatus
	Version           uint64
	CancelRequestedAt *time.Time
	ProviderCode      string                `json:"-"`
	ProviderTaskID    string                `json:"-"`
	Content           *ControlledContentRef `json:"-"`
	Media             *VideoMediaMetadata
	Asset             *GatewayAsset
	LeaseReleased     bool
	LeaseReleaseCount uint32
	Events            []GatewayTaskEvent
}

type TaskMutation func(task *GatewayTask) error

type VideoTaskLedger interface {
	Load(ctx context.Context, taskID string) (GatewayTask, error)
	Advance(ctx context.Context, taskID string, expectedVersion uint64, to TaskStatus, source, reason string, mutate TaskMutation) (GatewayTask, error)
	ReleaseLeaseOnce(ctx context.Context, taskID string) (GatewayTask, error)
	RecordCallback(ctx context.Context, taskID string, callback VerifiedCallback) (duplicate bool, err error)
	PrepareMediaDelete(ctx context.Context, taskID string) (GatewayTask, error)
	CompleteMediaDelete(ctx context.Context, taskID string, succeeded bool) (GatewayTask, error)
}

// InMemoryVideoTaskLedger 仅用于Fake合同测试，生产路径必须使用VID-G3 Repository适配器。
type InMemoryVideoTaskLedger struct {
	mu        sync.Mutex
	tasks     map[string]GatewayTask
	callbacks map[string]string
	now       func() time.Time
}

func NewInMemoryVideoTaskLedger() *InMemoryVideoTaskLedger {
	return &InMemoryVideoTaskLedger{tasks: make(map[string]GatewayTask), callbacks: make(map[string]string), now: time.Now}
}

func (l *InMemoryVideoTaskLedger) Seed(task GatewayTask) error {
	if strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(task.RequestID) == "" || task.Version == 0 || task.Status == "" {
		return ErrGatewayTaskTransition
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.tasks[task.TaskID]; exists {
		return ErrGatewayTaskConflict
	}
	l.tasks[task.TaskID] = cloneGatewayTask(task)
	return nil
}

func (l *InMemoryVideoTaskLedger) Load(ctx context.Context, taskID string) (GatewayTask, error) {
	if err := ctx.Err(); err != nil {
		return GatewayTask{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	task, ok := l.tasks[taskID]
	if !ok {
		return GatewayTask{}, ErrGatewayTaskNotFound
	}
	return cloneGatewayTask(task), nil
}

func (l *InMemoryVideoTaskLedger) Advance(ctx context.Context, taskID string, expectedVersion uint64, to TaskStatus, source, reason string, mutate TaskMutation) (GatewayTask, error) {
	if err := ctx.Err(); err != nil {
		return GatewayTask{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	task, ok := l.tasks[taskID]
	if !ok {
		return GatewayTask{}, ErrGatewayTaskNotFound
	}
	if task.Version != expectedVersion {
		return GatewayTask{}, ErrGatewayTaskConflict
	}
	if !taskTransitionAllowed(task.Status, to) {
		return GatewayTask{}, ErrGatewayTaskTransition
	}
	if task.CancelRequestedAt != nil && (task.Status == TaskCreated || task.Status == TaskReserved || task.Status == TaskQueued) && (to == TaskReserved || to == TaskQueued || to == TaskSubmitting) {
		return GatewayTask{}, ErrGatewayTaskTransition
	}
	from := task.Status
	if mutate != nil {
		if err := mutate(&task); err != nil {
			return GatewayTask{}, err
		}
	}
	task.Status = to
	task.Version++
	task.Events = append(task.Events, GatewayTaskEvent{
		EventID:    fmt.Sprintf("%s-%s-%d", task.TaskID, to, task.Version),
		FromStatus: from, ToStatus: to, Source: source, Reason: reason, CreatedAt: l.now().UTC(),
	})
	l.tasks[taskID] = cloneGatewayTask(task)
	return cloneGatewayTask(task), nil
}

func (l *InMemoryVideoTaskLedger) ReleaseLeaseOnce(ctx context.Context, taskID string) (GatewayTask, error) {
	if err := ctx.Err(); err != nil {
		return GatewayTask{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	task, ok := l.tasks[taskID]
	if !ok {
		return GatewayTask{}, ErrGatewayTaskNotFound
	}
	if !taskSafeTerminal(task.Status) {
		return cloneGatewayTask(task), nil
	}
	if !task.LeaseReleased {
		task.LeaseReleased = true
		task.LeaseReleaseCount++
		l.tasks[taskID] = cloneGatewayTask(task)
	}
	return cloneGatewayTask(task), nil
}

func (l *InMemoryVideoTaskLedger) RecordCallback(ctx context.Context, _ string, callback VerifiedCallback) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key := callback.ProviderCode + "|" + callback.ProviderTaskID + "|" + callback.ExternalEventID
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.callbacks[key]; ok {
		if existing != callback.BodySHA256 {
			return false, ErrCallbackBodyConflict
		}
		return true, nil
	}
	l.callbacks[key] = callback.BodySHA256
	return false, nil
}

func (l *InMemoryVideoTaskLedger) PrepareMediaDelete(ctx context.Context, taskID string) (GatewayTask, error) {
	if err := ctx.Err(); err != nil {
		return GatewayTask{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	task, ok := l.tasks[taskID]
	if !ok || task.Asset == nil {
		return GatewayTask{}, ErrGatewayTaskNotFound
	}
	task.Asset.Lifecycle = AssetDeleting
	for index := range task.Asset.Children {
		task.Asset.Children[index].Lifecycle = AssetDeleting
	}
	task.Version++
	l.tasks[taskID] = cloneGatewayTask(task)
	return cloneGatewayTask(task), nil
}

func (l *InMemoryVideoTaskLedger) CompleteMediaDelete(ctx context.Context, taskID string, succeeded bool) (GatewayTask, error) {
	if err := ctx.Err(); err != nil {
		return GatewayTask{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	task, ok := l.tasks[taskID]
	if !ok || task.Asset == nil || task.Asset.Lifecycle != AssetDeleting {
		return GatewayTask{}, ErrGatewayTaskNotFound
	}
	target := AssetDeleteFailed
	if succeeded {
		target = AssetDeleted
		task.Asset.MediaDeleted = true
	}
	task.Asset.Lifecycle = target
	for index := range task.Asset.Children {
		task.Asset.Children[index].Lifecycle = target
		task.Asset.Children[index].MediaDeleted = succeeded
	}
	task.Version++
	l.tasks[taskID] = cloneGatewayTask(task)
	return cloneGatewayTask(task), nil
}

func taskTransitionAllowed(from, to TaskStatus) bool {
	allowed := map[TaskStatus]map[TaskStatus]bool{
		TaskCreated:    {TaskReserved: true, TaskCancelled: true, TaskFailed: true},
		TaskReserved:   {TaskQueued: true, TaskCancelled: true, TaskFailed: true},
		TaskQueued:     {TaskSubmitting: true, TaskCancelled: true, TaskFailed: true},
		TaskSubmitting: {TaskSubmitted: true, TaskPendingReconcile: true, TaskFailed: true, TaskCancelled: true},
		TaskSubmitted:  {TaskProcessing: true, TaskFetching: true, TaskPendingReconcile: true, TaskFailed: true, TaskCancelled: true},
		TaskProcessing: {TaskFetching: true, TaskPendingReconcile: true, TaskFailed: true, TaskCancelled: true},
		TaskFetching:   {TaskStoring: true, TaskPendingReconcile: true, TaskFailed: true, TaskCancelled: true},
		TaskStoring:    {TaskModerating: true, TaskPendingReconcile: true, TaskFailed: true},
		TaskModerating: {TaskLabeling: true, TaskPendingReconcile: true, TaskFailed: true},
		TaskLabeling:   {TaskSucceeded: true, TaskPendingReconcile: true, TaskFailed: true},
	}
	return allowed[from][to]
}

func taskSafeTerminal(status TaskStatus) bool {
	return status == TaskSucceeded || status == TaskFailed || status == TaskCancelled || status == TaskExpired
}

func taskStatusRank(status TaskStatus) int {
	switch status {
	case TaskCreated:
		return 1
	case TaskReserved:
		return 2
	case TaskQueued:
		return 3
	case TaskSubmitting:
		return 4
	case TaskSubmitted:
		return 5
	case TaskProcessing:
		return 6
	case TaskFetching:
		return 7
	case TaskStoring:
		return 8
	case TaskModerating:
		return 9
	case TaskLabeling:
		return 10
	case TaskPendingReconcile:
		return 90
	case TaskSucceeded, TaskFailed, TaskCancelled, TaskExpired:
		return 100
	default:
		return 0
	}
}

func cloneGatewayTask(task GatewayTask) GatewayTask {
	result := task
	if task.CancelRequestedAt != nil {
		value := *task.CancelRequestedAt
		result.CancelRequestedAt = &value
	}
	if task.Input != nil {
		value := *task.Input
		result.Input = &value
	}
	if task.Reference != nil {
		value := *task.Reference
		value.Bytes = append([]byte(nil), task.Reference.Bytes...)
		result.Reference = &value
	}
	if task.Content != nil {
		value := *task.Content
		result.Content = &value
	}
	if task.Media != nil {
		value := *task.Media
		result.Media = &value
	}
	if task.Asset != nil {
		value := *task.Asset
		value.Children = append([]GatewayAsset(nil), task.Asset.Children...)
		result.Asset = &value
	}
	result.Events = append([]GatewayTaskEvent(nil), task.Events...)
	return result
}
