package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type VideoOrphanCleanupWorker struct {
	scanner   *VideoObjectReconciliationScanner
	repo      *repository.VideoObjectReconciliationRepository
	inventory video.VideoObjectInventory
	workerID  string
	now       func() time.Time
}

func NewVideoOrphanCleanupWorker(scanner *VideoObjectReconciliationScanner, inventory video.VideoObjectInventory, workerID string) (*VideoOrphanCleanupWorker, error) {
	if scanner == nil || scanner.db == nil || inventory == nil || !videoBillingPublicID.MatchString(workerID) || len(workerID) > 64 {
		return nil, repository.ErrVideoObjectObservationInvalid
	}
	return &VideoOrphanCleanupWorker{scanner: scanner, repo: repository.NewVideoObjectReconciliationRepository(scanner.db), inventory: inventory, workerID: workerID, now: time.Now}, nil
}

// RunOnce在confirmed观察锁定期间重查数据库引用和对象摘要；删除成功但DB回写失败可由过期租约重入确认。
func (w *VideoOrphanCleanupWorker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || ctx == nil {
		return false, repository.ErrVideoObjectObservationInvalid
	}
	now := w.now().UTC()
	lease, err := w.repo.ClaimCleanup(ctx, w.workerID, now)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ref := video.VideoObjectRef{Bucket: lease.Observation.Bucket, ObjectKey: lease.Observation.ObjectKey}
	referenced, err := w.scanner.hasReference(ctx, ref, now)
	if err != nil {
		return true, errors.Join(err, w.fail(ctx, lease, "reference_read_failed", false))
	}
	if referenced {
		return true, w.repo.CompleteCleanup(ctx, lease, w.now().UTC())
	}
	item, err := w.inventory.InspectObject(ctx, ref)
	if errors.Is(err, video.ErrVideoObjectNotFound) {
		return true, w.repo.CompleteCleanup(ctx, lease, w.now().UTC())
	}
	if err != nil {
		return true, errors.Join(err, w.fail(ctx, lease, "object_read_failed", false))
	}
	digest := item.SHA256
	if item.Discarded {
		digest = videoPayloadSHA256([]byte{0})
	}
	if digest != lease.Observation.ObjectSHA256 || item.SizeBytes != lease.Observation.SizeBytes {
		return true, errors.Join(video.ErrVideoObjectConflict, w.fail(ctx, lease, "object_identity_drift", true))
	}
	// confirmed观察会阻止新资产/输入/上传绑定；删除紧前仍再读一次保存计划等全部引用。
	referenced, err = w.scanner.hasReference(ctx, ref, w.now().UTC())
	if err != nil {
		return true, errors.Join(err, w.fail(ctx, lease, "reference_recheck_failed", false))
	}
	if referenced {
		return true, w.repo.CompleteCleanup(ctx, lease, w.now().UTC())
	}
	if err := w.inventory.DeleteObservedObject(ctx, ref, lease.Observation.ObjectSHA256, lease.Observation.SizeBytes); err != nil {
		manual := errors.Is(err, video.ErrVideoObjectConflict)
		return true, errors.Join(err, w.fail(ctx, lease, "object_delete_failed", manual))
	}
	return true, w.repo.CompleteCleanup(ctx, lease, w.now().UTC())
}

func (w *VideoOrphanCleanupWorker) fail(ctx context.Context, lease *repository.VideoObjectCleanupLease, class string, manual bool) error {
	return w.repo.FailCleanup(ctx, lease, w.now().UTC(), class, manual)
}

func (w *VideoOrphanCleanupWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, _ = w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
