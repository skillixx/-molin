package image

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrObjectInvalid           = errors.New("对象定位或参数无效")
	ErrObjectTooLarge          = errors.New("对象超过允许大小")
	ErrObjectNotFound          = errors.New("对象不存在")
	ErrObjectConflict          = errors.New("相同对象键已存在不同内容")
	ErrObjectCleanupUnrecorded = errors.New("对象删除失败且补偿事实未持久化")
)

type ObjectCleanupReason string

const (
	TemporaryObjectBucket  = "ai-upload-temp"
	ResultObjectBucket     = "ai-result"
	QuarantineObjectBucket = "ai-quarantine"

	ObjectCleanupAfterModerationFailure      ObjectCleanupReason = "moderation_failed"
	ObjectCleanupAfterQuarantineStoreFailure ObjectCleanupReason = "quarantine_store_failed"
	ObjectCleanupAfterResultStoreFailure     ObjectCleanupReason = "result_store_failed"
	ObjectCleanupAfterQuarantineStored       ObjectCleanupReason = "quarantine_stored"
	ObjectCleanupAfterResultStored           ObjectCleanupReason = "result_stored"
	ObjectCleanupAfterMetadataPersistFailure ObjectCleanupReason = "metadata_persist_failed"
	ObjectCleanupAfterTempPutUnknown         ObjectCleanupReason = "temp_put_unknown"
	ObjectCleanupAfterQuarantinePutUnknown   ObjectCleanupReason = "quarantine_put_unknown"
	ObjectCleanupAfterResultPutUnknown       ObjectCleanupReason = "result_put_unknown"
	ObjectCleanupAfterThumbnailPutUnknown    ObjectCleanupReason = "thumbnail_put_unknown"
)

type ObjectRef struct {
	Bucket string
	Key    string
}

type StoredObject struct {
	Ref       ObjectRef
	SizeBytes uint64
	SHA256    string
	CreatedAt time.Time
}

// ObjectCleanupTask 只携带可重建删除动作的低敏事实，不包含Prompt、图片正文、Provider地址或凭据。
type ObjectCleanupTask struct {
	RequestID string
	Ref       ObjectRef
	Reason    ObjectCleanupReason
}

// ObjectCleanupRecorder 必须在返回nil前完成幂等持久化；实现方不能只写内存队列或普通日志。
type ObjectCleanupRecorder interface {
	RecordObjectCleanup(ctx context.Context, task ObjectCleanupTask) error
}

// ObjectStore 隐藏对象存储实现；调用方只能通过受控对象引用读写，不能拼接Provider URL或长期下载地址。
type ObjectStore interface {
	Put(ctx context.Context, ref ObjectRef, body io.Reader, maxBytes int64) (StoredObject, error)
	Get(ctx context.Context, ref ObjectRef) ([]byte, error)
	Head(ctx context.Context, ref ObjectRef) (StoredObject, error)
	Delete(ctx context.Context, ref ObjectRef) error
	SignDownload(ctx context.Context, ref ObjectRef, expiresIn time.Duration) (string, error)
}
