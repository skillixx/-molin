package video

import (
	"context"
	"errors"
	"github.com/shopspring/decimal"
	"io"
	"time"
)

const (
	OperationTextToVideo  = "text_to_video"
	OperationImageToVideo = "image_to_video"
)

var (
	ErrVideoRequestInvalid             = errors.New("视频Provider请求无效")
	ErrProviderTaskNotFound            = errors.New("视频Provider任务不存在")
	ErrProviderExplicitFailure         = errors.New("视频Provider明确失败")
	ErrProviderTimeout                 = errors.New("视频Provider超时")
	ErrProviderResultUnknown           = errors.New("视频Provider结果未知")
	ErrProviderResultCorrupt           = errors.New("视频Provider结果损坏")
	ErrProviderFetchTimeout            = errors.New("视频Provider媒体抓取超时")
	ErrSubmitAcknowledgementLost       = errors.New("视频Provider提交ACK丢失")
	ErrDuplicateSubmitForbidden        = errors.New("相同request_id禁止重新提交")
	ErrProviderCancelRejected          = errors.New("Provider拒绝取消，继续跟踪原任务")
	ErrProviderCancelUnsupported       = errors.New("Provider不支持取消，继续跟踪原任务")
	ErrVideoCancelBeforeSubmitRequired = errors.New("未提交视频须通过原子财务取消入口处理")
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

// VideoSubmissionLedger 对原提交授权和回执做数据库围栏，不把恢复入口变成第二次Provider提交。
type VideoSubmissionLedger interface {
	ValidateSubmissionClaim(context.Context, string, uint64) (time.Time, error)
	RecordSubmissionReceipt(context.Context, string, uint64, SubmitResult) (GatewayTask, error)
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
	Confirmation   *ProviderCostConfirmation `json:"-"`
	ProviderTaskID string
	Status         ProviderTaskStatus
	Progress       uint8
	Content        *ControlledContentRef
	ErrorCode      string
}

// ProviderCostConfirmation 是受信Adapter已确认的低敏计量，不是客户端报价，也不包含Provider正文。
type ProviderCostConfirmation struct {
	ProviderCode    string
	ProviderTaskID  string
	ExternalEventID string
	Operation       string
	Outcome         ProviderTaskStatus
	Quantity        decimal.Decimal
	UnitPrice       decimal.Decimal
	Amount          decimal.Decimal
	Currency        string
}

// VideoProviderCostSink 在财务模式持久化受信Adapter确认；补偿不会持有Adapter或再次调用上游。
type VideoProviderCostSink interface {
	RecordProviderConfirmation(context.Context, string, ProviderCostConfirmation) error
}

// VideoProviderNoProductSink 原子保存成本确认及无产物证据；不能仅凭零成本推断不存在产物。
type VideoProviderNoProductSink interface {
	RecordNoProductOutcome(context.Context, string, ProviderCostConfirmation) error
}

// VideoProviderConflictSink 保留矛盾观察；后到的同成本摘要不能抹去曾观察到产物的事实。
type VideoProviderConflictSink interface {
	RecordProviderResultConflict(context.Context, string, ProviderCostConfirmation) error
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
