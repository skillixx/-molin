package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// VideoObjectMissingWorker只恢复数据库已引用对象的观察状态；永不伪造对象、删除元数据或改写财务事实。
type VideoObjectMissingWorker struct {
	repo      *repository.VideoObjectReconciliationRepository
	inventory video.VideoObjectInventory
	workerID  string
	now       func() time.Time
}

func NewVideoObjectMissingWorker(db *gorm.DB, inventory video.VideoObjectInventory, workerID string) (*VideoObjectMissingWorker, error) {
	if db == nil || inventory == nil || !videoBillingPublicID.MatchString(workerID) || len(workerID) > 64 {
		return nil, repository.ErrVideoObjectObservationInvalid
	}
	return &VideoObjectMissingWorker{repo: repository.NewVideoObjectReconciliationRepository(db), inventory: inventory, workerID: workerID, now: time.Now}, nil
}

func (w *VideoObjectMissingWorker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || ctx == nil {
		return false, repository.ErrVideoObjectObservationInvalid
	}
	lease, err := w.repo.ClaimMissing(ctx, w.workerID, w.now().UTC())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ref := video.VideoObjectRef{Bucket: lease.Observation.Bucket, ObjectKey: lease.Observation.ObjectKey}
	item, err := w.inventory.InspectObject(ctx, ref)
	if errors.Is(err, video.ErrVideoObjectNotFound) {
		return true, errors.Join(err, w.repo.FailMissing(ctx, lease, w.now().UTC(), "object_still_missing", false))
	}
	if err != nil {
		return true, errors.Join(err, w.repo.FailMissing(ctx, lease, w.now().UTC(), "object_read_failed", false))
	}
	digest := item.SHA256
	if item.Discarded {
		digest = videoPayloadSHA256([]byte{0})
	}
	if digest != lease.Observation.ObjectSHA256 || item.SizeBytes != lease.Observation.SizeBytes {
		return true, errors.Join(video.ErrVideoObjectConflict, w.repo.FailMissing(ctx, lease, w.now().UTC(), "object_identity_drift", true))
	}
	return true, w.repo.CompleteMissing(ctx, lease, w.now().UTC())
}
