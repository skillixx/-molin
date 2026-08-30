package video

import (
	"context"
	"errors"
	"io"
)

const (
	OperationTextToVideo  = "text_to_video"
	OperationImageToVideo = "image_to_video"
)

var (
	ErrVideoRequestInvalid       = errors.New("视频Provider请求无效")
	ErrProviderTaskNotFound      = errors.New("视频Provider任务不存在")
	ErrProviderExplicitFailure   = errors.New("视频Provider明确失败")
	ErrProviderTimeout           = errors.New("视频Provider超时")
	ErrProviderResultUnknown     = errors.New("视频Provider结果未知")
	ErrProviderResultCorrupt     = errors.New("视频Provider结果损坏")
	ErrProviderFetchTimeout      = errors.New("视频Provider媒体抓取超时")
	ErrSubmitAcknowledgementLost = errors.New("视频Provider提交ACK丢失")
	ErrDuplicateSubmitForbidden  = errors.New("相同request_id禁止重新提交")
)

type ProviderTaskStatus string

const (
	ProviderTaskQueued     ProviderTaskStatus = "queued"
	ProviderTaskProcessing ProviderTaskStatus = "processing"
	ProviderTaskSucceeded  ProviderTaskStatus = "succeeded"
	ProviderTaskFailed     ProviderTaskStatus = "failed"
	ProviderTaskCancelled  ProviderTaskStatus = "cancelled"
	ProviderTaskUnknown    ProviderTaskStatus = "unknown"
)

// ControlledInputRef 只携带内部输入资产快照，不允许出现URL、对象键或签名参数。
type ControlledInputRef struct {
	AssetID string
	SHA256  string
	Version uint64
}

type VideoSpec struct {
	Width           uint32
	Height          uint32
	DurationSeconds uint32
	FrameRate       uint32
	Audio           bool
}

type SubmitRequest struct {
	RequestID string
	Operation string
	Prompt    string `json:"-"`
	Input     *ControlledInputRef
	Spec      VideoSpec
}

type SubmitResult struct {
	RequestID      string
	ProviderCode   string
	ProviderTaskID string
	Status         ProviderTaskStatus
}

type QueryRequest struct {
	ProviderTaskID string
}

// ControlledContentRef 是Provider内容读取能力的不可伪造句柄，不包含外部URL。
type ControlledContentRef struct {
	ProviderTaskID string `json:"-"`
	ContentID      string `json:"-"`
	MediaType      string
}

// StreamContent 通过ReaderAt提供有界随机读取，探测器无需把完整视频加载进内存。
type StreamContent struct {
	Ref       ControlledContentRef `json:"-"`
	SizeBytes int64                `json:"-"`
	ReaderAt  io.ReaderAt          `json:"-"`
	RangeMode string
	// CancelRead 由受控来源提供，用于在超时时中断可能阻塞的底层读取。
	CancelRead func() error `json:"-"`
}

type QueryResult struct {
	ProviderTaskID string
	Status         ProviderTaskStatus
	Progress       uint8
	Content        *ControlledContentRef
	ErrorCode      string
}

type CancelRequest struct {
	ProviderTaskID string
}

// VideoProviderAdapter 只负责Provider异步协议；归属、计费、审核和交付由网关持有。
type VideoProviderAdapter interface {
	Name() string
	Submit(ctx context.Context, request SubmitRequest) (SubmitResult, error)
	Query(ctx context.Context, request QueryRequest) (QueryResult, error)
	Cancel(ctx context.Context, request CancelRequest) (QueryResult, error)
	OpenContent(ctx context.Context, ref ControlledContentRef) (StreamContent, error)
	Delete(ctx context.Context, ref ControlledContentRef) error
}
