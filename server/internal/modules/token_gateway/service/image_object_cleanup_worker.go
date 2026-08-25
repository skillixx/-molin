package service

import (
	"context"
	"errors"
	"time"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
)

var ErrImageObjectCleanupUnavailable = errors.New("图片对象回收服务不可用")

type imageObjectCleanupTaskStore interface {
	ClaimBatch(ctx context.Context, now, staleBefore time.Time, limit int) ([]model.AICompensationTask, error)
	ResolveObjectRef(task model.AICompensationTask) (imagegateway.ObjectRef, error)
	HasAssetReference(ctx context.Context, ref imagegateway.ObjectRef) (bool, error)
	MarkCompleted(ctx context.Context, id uint64, lease, now time.Time) error
	RescheduleQuiescence(ctx context.Context, id uint64, lease, next time.Time) error
	MarkFailure(ctx context.Context, id uint64, lease, next time.Time, errorClass string) error
}

type ImageObjectCleanupResult struct {
	Scanned   int
	Completed int
	Protected int
	Quiescing int
	Retried   int
}

type ImageObjectCleanupWorker struct {
	repo              imageObjectCleanupTaskStore
	store             imagegateway.ObjectStore
	now               func() time.Time
	deleteTimeout     time.Duration
	referenceTimeout  time.Duration
	visibilityTimeout time.Duration
}

func NewImageObjectCleanupWorker(repo imageObjectCleanupTaskStore, store imagegateway.ObjectStore) (*ImageObjectCleanupWorker, error) {
	if repo == nil || store == nil {
		return nil, ErrImageObjectCleanupUnavailable
	}
	return &ImageObjectCleanupWorker{
		repo: repo, store: store, now: time.Now,
		deleteTimeout: 5 * time.Second, referenceTimeout: 5 * time.Second, visibilityTimeout: 5 * time.Second,
	}, nil
}

// RunBatch 只删除Recorder白名单重建出的对象；描述符损坏时绝不尝试猜测路径或扩大删除范围。
func (w *ImageObjectCleanupWorker) RunBatch(ctx context.Context, limit int) (ImageObjectCleanupResult, error) {
	result := ImageObjectCleanupResult{}
	if w == nil || w.repo == nil || w.store == nil {
		return result, ErrImageObjectCleanupUnavailable
	}
	now := w.now().UTC().Truncate(time.Second)
	tasks, err := w.repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), limit)
	if err != nil {
		return result, err
	}
	result.Scanned = len(tasks)
	for _, task := range tasks {
		if task.LockedAt == nil {
			continue
		}
		ref, resolveErr := w.repo.ResolveObjectRef(task)
		if resolveErr != nil {
			if markErr := w.markFailure(ctx, task, now, "object_cleanup_descriptor_invalid"); markErr != nil {
				return result, markErr
			}
			result.Retried++
			continue
		}
		putUnknown := imageObjectPutUnknownTask(task)
		quietUntil := task.CreatedAt.UTC().Add(5 * time.Minute)
		referenceCtx, cancelReference := context.WithTimeout(ctx, w.referenceTimeout)
		referenced, referenceErr := w.repo.HasAssetReference(referenceCtx, ref)
		cancelReference()
		if referenceErr != nil {
			if putUnknown && now.Before(quietUntil) {
				if rescheduleErr := w.rescheduleQuiescence(ctx, task, now, quietUntil); rescheduleErr != nil {
					return result, rescheduleErr
				}
				result.Quiescing++
				continue
			}
			if markErr := w.markFailure(ctx, task, now, "object_reference_check_failed"); markErr != nil {
				return result, markErr
			}
			result.Retried++
			continue
		}
		if referenced {
			if markErr := w.repo.MarkCompleted(ctx, task.ID, *task.LockedAt, w.now().UTC()); markErr != nil {
				return result, markErr
			}
			result.Protected++
			result.Completed++
			continue
		}
		if putUnknown {
			visibilityCtx, cancelVisibility := context.WithTimeout(ctx, w.visibilityTimeout)
			_, visibilityErr := w.store.Head(visibilityCtx, ref)
			cancelVisibility()
			if errors.Is(visibilityErr, imagegateway.ErrObjectNotFound) {
				if now.Before(quietUntil) {
					if rescheduleErr := w.rescheduleQuiescence(ctx, task, now, quietUntil); rescheduleErr != nil {
						return result, rescheduleErr
					}
					result.Quiescing++
					continue
				}
			} else if visibilityErr != nil {
				if now.Before(quietUntil) {
					if rescheduleErr := w.rescheduleQuiescence(ctx, task, now, quietUntil); rescheduleErr != nil {
						return result, rescheduleErr
					}
					result.Quiescing++
					continue
				}
				if markErr := w.markFailure(ctx, task, now, "object_visibility_check_failed"); markErr != nil {
					return result, markErr
				}
				result.Retried++
				continue
			}
		}
		deleteCtx, cancelDelete := context.WithTimeout(ctx, w.deleteTimeout)
		deleteErr := w.store.Delete(deleteCtx, ref)
		cancelDelete()
		if deleteErr == nil || errors.Is(deleteErr, imagegateway.ErrObjectNotFound) {
			if markErr := w.repo.MarkCompleted(ctx, task.ID, *task.LockedAt, w.now().UTC()); markErr != nil {
				return result, markErr
			}
			result.Completed++
			continue
		}
		if markErr := w.markFailure(ctx, task, now, "object_delete_failed"); markErr != nil {
			return result, markErr
		}
		result.Retried++
	}
	return result, nil
}

func (w *ImageObjectCleanupWorker) rescheduleQuiescence(ctx context.Context, task model.AICompensationTask, now, quietUntil time.Time) error {
	next := now.Add(time.Minute)
	if next.After(quietUntil) {
		next = quietUntil
	}
	return w.repo.RescheduleQuiescence(ctx, task.ID, *task.LockedAt, next)
}

func imageObjectPutUnknownTask(task model.AICompensationTask) bool {
	return task.LastErrorClass != nil && imagegateway.IsPutUnknownCleanupReason(imagegateway.ObjectCleanupReason(*task.LastErrorClass))
}

func (w *ImageObjectCleanupWorker) markFailure(ctx context.Context, task model.AICompensationTask, now time.Time, errorClass string) error {
	delay := imageObjectCleanupRetryDelay(task.RetryCount)
	return w.repo.MarkFailure(ctx, task.ID, *task.LockedAt, now.Add(delay), errorClass)
}

func imageObjectCleanupRetryDelay(retryCount uint64) time.Duration {
	if retryCount >= 6 {
		return time.Hour
	}
	return time.Minute * time.Duration(uint64(1)<<retryCount)
}

// Start 立即执行一轮后按间隔继续扫描；取消运行时上下文即可停止，不创建不可回收的后台协程。
func (w *ImageObjectCleanupWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	for {
		_, _ = w.RunBatch(ctx, 100)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}
