package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type ImageCleanupResult struct {
	Scanned int
	Deleted int
	Failed  int
	Skipped int
}

type ImageCleanupWorker struct {
	repo          *repository.ImageCleanupRepository
	assets        *repository.ImageAssetRepository
	store         imagegateway.ObjectStore
	now           func() time.Time
	deleteTimeout time.Duration
}

func NewImageCleanupWorker(db *gorm.DB, store imagegateway.ObjectStore) (*ImageCleanupWorker, error) {
	if db == nil || store == nil {
		return nil, ErrImageBillingState
	}
	return &ImageCleanupWorker{
		repo: repository.NewImageCleanupRepository(db), assets: repository.NewImageAssetRepository(db),
		store: store, now: time.Now, deleteTimeout: 5 * time.Second,
	}, nil
}

// RunBatch 通过资产version CAS取得删除权；legal hold、争议和并发状态变化均跳过，绝不强制覆盖。
func (w *ImageCleanupWorker) RunBatch(ctx context.Context, limit int) (ImageCleanupResult, error) {
	result := ImageCleanupResult{}
	candidates, err := w.repo.ListCleanupCandidates(ctx, w.now().UTC(), limit)
	if err != nil {
		return result, err
	}
	result.Scanned = len(candidates)
	for _, candidate := range candidates {
		owner := repository.ImageOwner{UserID: candidate.UserID, ProjectID: candidate.ProjectID}
		var deleting *model.AIImageAsset
		var transitionErr error
		if candidate.LifecycleState == model.AIImageAssetDeleting {
			deleting, transitionErr = w.assets.ClaimStaleDeleting(ctx, candidate.PublicID, owner, candidate.VersionNo, w.now().UTC())
		} else {
			deleting, transitionErr = w.assets.TransitionLifecycle(ctx, candidate.PublicID, owner, candidate.VersionNo, model.AIImageAssetDeleting, w.now().UTC())
		}
		if transitionErr != nil {
			if errors.Is(transitionErr, repository.ErrImageAssetConflict) || errors.Is(transitionErr, repository.ErrImageAssetTransition) {
				result.Skipped++
				continue
			}
			return result, transitionErr
		}
		if deleting.Bucket == nil || deleting.ObjectKey == nil {
			_, _ = w.assets.TransitionLifecycle(ctx, deleting.PublicID, owner, deleting.VersionNo, model.AIImageAssetDeleteFailed, w.now().UTC())
			result.Failed++
			continue
		}
		deleteErr := w.deleteObject(ctx, imagegateway.ObjectRef{Bucket: *deleting.Bucket, Key: *deleting.ObjectKey})
		if deleteErr != nil && !errors.Is(deleteErr, imagegateway.ErrObjectNotFound) {
			_, _ = w.assets.TransitionLifecycle(ctx, deleting.PublicID, owner, deleting.VersionNo, model.AIImageAssetDeleteFailed, w.now().UTC())
			result.Failed++
			continue
		}
		if _, transitionErr := w.assets.TransitionLifecycle(ctx, deleting.PublicID, owner, deleting.VersionNo, model.AIImageAssetDeleted, w.now().UTC()); transitionErr != nil {
			return result, transitionErr
		}
		result.Deleted++
	}
	return result, nil
}

func (w *ImageCleanupWorker) deleteObject(ctx context.Context, ref imagegateway.ObjectRef) error {
	deleteCtx, cancel := context.WithTimeout(ctx, w.deleteTimeout)
	defer cancel()
	return w.store.Delete(deleteCtx, ref)
}

func (w *ImageCleanupWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, _ = w.RunBatch(ctx, 100)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
