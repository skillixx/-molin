package service

import (
	"context"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
)

type ImageCompensationWorker struct {
	repo    *repository.ImageCompensationRepository
	billing *ImageBillingService
	now     func() time.Time
}

func NewImageCompensationWorker(repo *repository.ImageCompensationRepository, billing *ImageBillingService) *ImageCompensationWorker {
	return &ImageCompensationWorker{repo: repo, billing: billing, now: time.Now}
}

// RunBatch 只重放本地结算/交付补偿，不调用Provider；结果未知会继续retry并在第8次进入dead。
func (w *ImageCompensationWorker) RunBatch(ctx context.Context, limit int) (int, error) {
	now := w.now().UTC().Truncate(time.Second)
	tasks, err := w.repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, task := range tasks {
		if task.LockedAt == nil {
			continue
		}
		err := w.billing.ReconcilePending(ctx, task.AggregateID)
		if err == nil {
			if markErr := w.repo.MarkCompleted(ctx, task.ID, *task.LockedAt, w.now().UTC()); markErr != nil {
				return completed, markErr
			}
			completed++
			continue
		}
		if markErr := w.repo.MarkFailure(ctx, task.ID, *task.LockedAt, w.now().UTC().Add(time.Minute), "still_pending"); markErr != nil {
			return completed, markErr
		}
	}
	return completed, nil
}

func (w *ImageCompensationWorker) Start(ctx context.Context, interval time.Duration) {
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
