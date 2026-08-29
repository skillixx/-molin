package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
)

type FakeVideoMode string

const (
	FakeVideoSuccess            FakeVideoMode = "success"
	FakeVideoExplicitFailure    FakeVideoMode = "explicit_failure"
	FakeVideoProviderCancelled  FakeVideoMode = "provider_cancelled"
	FakeVideoSubmitTimeout      FakeVideoMode = "submit_timeout"
	FakeVideoQueryTimeout       FakeVideoMode = "query_timeout"
	FakeVideoFetchTimeout       FakeVideoMode = "fetch_timeout"
	FakeVideoResultUnknown      FakeVideoMode = "result_unknown"
	FakeVideoCorruptResult      FakeVideoMode = "corrupt_result"
	FakeVideoAckLostKnownTask   FakeVideoMode = "ack_lost_known_task"
	FakeVideoAckLostUnknownTask FakeVideoMode = "ack_lost_unknown_task"
)

var lowerHexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)

type fakeProviderTask struct {
	request SubmitRequest
	result  SubmitResult
	queries uint32
	status  ProviderTaskStatus
	content []byte
}

// FakeAsyncVideoAdapter 是完全内存化的原生异步Provider，不访问网络或真实供应商。
type FakeAsyncVideoAdapter struct {
	mu          sync.Mutex
	mode        FakeVideoMode
	tasks       map[string]*fakeProviderTask
	requestTask map[string]string
	submitCalls int
}

func NewFakeAsyncVideoAdapter(mode FakeVideoMode) *FakeAsyncVideoAdapter {
	return &FakeAsyncVideoAdapter{mode: mode, tasks: make(map[string]*fakeProviderTask), requestTask: make(map[string]string)}
}

func (a *FakeAsyncVideoAdapter) Name() string { return "fake-native-async" }

func (a *FakeAsyncVideoAdapter) Submit(ctx context.Context, request SubmitRequest) (SubmitResult, error) {
	if err := ctx.Err(); err != nil {
		return SubmitResult{}, err
	}
	if err := validateSubmitRequest(request); err != nil {
		return SubmitResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if taskID, exists := a.requestTask[request.RequestID]; exists {
		return a.tasks[taskID].result, ErrDuplicateSubmitForbidden
	}
	a.submitCalls++
	digest := sha256.Sum256([]byte(request.RequestID))
	taskID := "taskUUID-" + hex.EncodeToString(digest[:12])
	result := SubmitResult{RequestID: request.RequestID, ProviderCode: a.Name(), ProviderTaskID: taskID, Status: ProviderTaskQueued}
	task := &fakeProviderTask{request: request, result: result, status: ProviderTaskQueued, content: buildFakeMP4Fixture(request.Spec)}
	a.tasks[taskID] = task
	a.requestTask[request.RequestID] = taskID
	switch a.mode {
	case FakeVideoSubmitTimeout:
		return SubmitResult{}, ErrProviderTimeout
	case FakeVideoAckLostKnownTask:
		return result, ErrSubmitAcknowledgementLost
	case FakeVideoAckLostUnknownTask:
		return SubmitResult{RequestID: request.RequestID, ProviderCode: a.Name()}, ErrSubmitAcknowledgementLost
	default:
		return result, nil
	}
}

func (a *FakeAsyncVideoAdapter) Query(ctx context.Context, request QueryRequest) (QueryResult, error) {
	if err := ctx.Err(); err != nil {
		return QueryResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	task, ok := a.tasks[strings.TrimSpace(request.ProviderTaskID)]
	if !ok {
		return QueryResult{}, ErrProviderTaskNotFound
	}
	if task.status == ProviderTaskCancelled {
		return QueryResult{ProviderTaskID: task.result.ProviderTaskID, Status: ProviderTaskCancelled}, nil
	}
	task.queries++
	if a.mode == FakeVideoQueryTimeout {
		return QueryResult{}, ErrProviderTimeout
	}
	if task.queries == 1 {
		task.status = ProviderTaskProcessing
		return QueryResult{ProviderTaskID: task.result.ProviderTaskID, Status: ProviderTaskProcessing, Progress: 50}, nil
	}
	switch a.mode {
	case FakeVideoExplicitFailure:
		task.status = ProviderTaskFailed
		return QueryResult{ProviderTaskID: task.result.ProviderTaskID, Status: ProviderTaskFailed, ErrorCode: "fake_failed"}, ErrProviderExplicitFailure
	case FakeVideoProviderCancelled:
		task.status = ProviderTaskCancelled
		return QueryResult{ProviderTaskID: task.result.ProviderTaskID, Status: ProviderTaskCancelled}, nil
	case FakeVideoResultUnknown, FakeVideoAckLostUnknownTask:
		task.status = ProviderTaskUnknown
		return QueryResult{ProviderTaskID: task.result.ProviderTaskID, Status: ProviderTaskUnknown}, ErrProviderResultUnknown
	case FakeVideoCorruptResult:
		task.status = ProviderTaskSucceeded
		task.content = []byte("corrupt-http-200")
	default:
		task.status = ProviderTaskSucceeded
	}
	ref := &ControlledContentRef{ProviderTaskID: task.result.ProviderTaskID, ContentID: "content-" + task.result.ProviderTaskID, MediaType: "video/mp4"}
	return QueryResult{ProviderTaskID: task.result.ProviderTaskID, Status: ProviderTaskSucceeded, Progress: 100, Content: ref}, nil
}

func (a *FakeAsyncVideoAdapter) Cancel(ctx context.Context, request CancelRequest) (QueryResult, error) {
	if err := ctx.Err(); err != nil {
		return QueryResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	task, ok := a.tasks[strings.TrimSpace(request.ProviderTaskID)]
	if !ok {
		return QueryResult{}, ErrProviderTaskNotFound
	}
	if task.status != ProviderTaskSucceeded && task.status != ProviderTaskFailed {
		task.status = ProviderTaskCancelled
	}
	return QueryResult{ProviderTaskID: task.result.ProviderTaskID, Status: task.status}, nil
}

func (a *FakeAsyncVideoAdapter) OpenContent(ctx context.Context, ref ControlledContentRef) (StreamContent, error) {
	if err := ctx.Err(); err != nil {
		return StreamContent{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mode == FakeVideoFetchTimeout {
		return StreamContent{}, ErrProviderFetchTimeout
	}
	task, ok := a.tasks[ref.ProviderTaskID]
	if !ok || task.status != ProviderTaskSucceeded || ref.ContentID != "content-"+ref.ProviderTaskID || len(task.content) == 0 {
		return StreamContent{}, ErrProviderTaskNotFound
	}
	return StreamContent{Ref: ref, SizeBytes: int64(len(task.content)), ReaderAt: bytes.NewReader(append([]byte(nil), task.content...)), RangeMode: "supported"}, nil
}

func (a *FakeAsyncVideoAdapter) Delete(ctx context.Context, ref ControlledContentRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if task, ok := a.tasks[ref.ProviderTaskID]; ok {
		task.content = nil
	}
	return nil
}

func (a *FakeAsyncVideoAdapter) SubmitCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.submitCalls
}

func validateSubmitRequest(request SubmitRequest) error {
	if strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.Prompt) == "" || request.Spec.Width == 0 || request.Spec.Height == 0 || request.Spec.DurationSeconds == 0 || request.Spec.FrameRate == 0 {
		return ErrVideoRequestInvalid
	}
	switch request.Operation {
	case OperationTextToVideo:
		if request.Input != nil {
			return ErrVideoRequestInvalid
		}
	case OperationImageToVideo:
		if request.Input == nil || !validControlledInput(*request.Input) {
			return ErrVideoRequestInvalid
		}
	default:
		return ErrVideoRequestInvalid
	}
	return nil
}

func validControlledInput(input ControlledInputRef) bool {
	return strings.HasPrefix(input.AssetID, "vin_") && !strings.Contains(input.AssetID, "://") && lowerHexSHA256.MatchString(input.SHA256) && input.Version > 0
}
