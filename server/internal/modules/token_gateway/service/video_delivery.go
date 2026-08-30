package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type VideoDeliveryResult struct {
	RequestID string `json:"request_id"`
	TaskID    string `json:"task_id"`
	Existing  bool   `json:"existing"`
}

// DeliverReady 不参与扣费，只在所有事实一致时原子公开六资产；不生成下载URL或新Provider任务。
func (s *VideoBillingService) DeliverReady(ctx context.Context, taskID string, owner repository.VideoOwner) (*VideoDeliveryResult, error) {
	result, err := s.deliverReady(ctx, taskID, owner, nil)
	if err != nil && !errors.Is(err, repository.ErrVideoCompensationLeaseLost) && !errors.Is(err, repository.ErrVideoCompensationBusy) {
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, s.markVideoDeliveryFailure(markCtx, taskID, owner))
	}
	return result, err
}

// RecoverDelivery 由补偿Worker调用，发布和本租约completed必须在同一事务完成。
func (s *VideoBillingService) RecoverDelivery(ctx context.Context, taskID string, owner repository.VideoOwner, lease repository.VideoCompensationLease) (*VideoDeliveryResult, error) {
	return s.deliverReady(ctx, taskID, owner, &lease)
}

func (s *VideoBillingService) deliverReady(ctx context.Context, taskID string, owner repository.VideoOwner, lease *repository.VideoCompensationLease) (*VideoDeliveryResult, error) {
	var result *VideoDeliveryResult
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			tasks := repository.NewVideoTaskRepository(tx)
			task, err := tasks.LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			existing := task.DeliveryStatus == model.AIDeliveryAvailable
			comp := repository.NewVideoCompensationRepository(tx).WithClock(s.now)
			job, jobErr := comp.FindRequestTx(tx, task.RequestID)
			if jobErr != nil && !errors.Is(jobErr, gorm.ErrRecordNotFound) {
				return jobErr
			}
			if jobErr == nil && job.Status != "completed" {
				if lease == nil {
					return repository.ErrVideoCompensationBusy
				}
				if lease.RequestID != task.RequestID || lease.ID != job.ID {
					return repository.ErrVideoCompensationLeaseLost
				}
				if _, err := comp.CheckLeaseTx(tx, *lease); err != nil {
					return err
				}
			} else if lease != nil {
				return repository.ErrVideoCompensationLeaseLost
			}
			report, err := reconcileVideoTx(tx, taskID, owner, !existing, lease, s.now().UTC())
			if err != nil {
				return err
			}
			if !report.Passed {
				return ErrVideoReconciliation
			}
			if existing {
				result = &VideoDeliveryResult{RequestID: task.RequestID, TaskID: taskID, Existing: true}
				return nil
			}
			var request model.VideoBillingRequest
			if err := tx.Where("request_id=?", task.RequestID).First(&request).Error; err != nil {
				return err
			}
			if request.SettledAmount == nil {
				return ErrVideoReconciliation
			}
			var publicationLease *repository.VideoCompensationLease
			if lease != nil {
				prepared, err := comp.PrepareDeliveryTx(tx, task.RequestID, task.RequestVersionNo, *lease)
				if err != nil {
					return err
				}
				publicationLease = &prepared
				if err := s.injectVideoFault("delivery_prepared"); err != nil {
					return err
				}
				if _, err := comp.CheckLeaseTx(tx, prepared); err != nil {
					return err
				}
			}
			if err := createVideoBillingOutboxTx(tx, task.RequestID, "video_delivery_available", model.AIDeliveryAvailable, *task.Operation, *request.SettledAmount, s.now().UTC()); err != nil {
				return err
			}
			if err := s.injectVideoFault("delivery_outbox"); err != nil {
				return err
			}
			if _, err := tasks.TransitionDelivery(ctx, videoCancelTransition(task, owner, model.AIDeliveryAvailable, "delivery_available", s.now().UTC())); err != nil {
				return err
			}
			if err := s.injectVideoFault("delivery_request"); err != nil {
				return err
			}
			var assets []model.AIImageAsset
			if err := tx.Where("request_id=?", task.RequestID).Order("id ASC").Find(&assets).Error; err != nil {
				return err
			}
			for _, a := range assets {
				if _, err := repository.NewVideoOutputAssetRepository(tx, nil).TransitionLifecycle(ctx, a.PublicID, owner, a.VersionNo, model.AIImageAssetAvailable, s.now().UTC()); err != nil {
					return err
				}
				if err := s.injectVideoFault("delivery_" + a.AssetRole); err != nil {
					return err
				}
			}
			if publicationLease != nil {
				if err := comp.FinishTx(tx, *publicationLease, "completed", ""); err != nil {
					return err
				}
				if err := s.injectVideoFault("delivery_completed"); err != nil {
					return err
				}
			}
			report, err = reconcileVideoTx(tx, taskID, owner, false, nil, s.now().UTC())
			if err != nil {
				return err
			}
			if !report.Passed {
				return ErrVideoReconciliation
			}
			if err := s.injectVideoFault("delivery_checked"); err != nil {
				return err
			}
			// 时间会在长对账或锁等待期间继续前进；最后一刻再次检查已锁定资产与原租约期限。
			now := s.now().UTC()
			for _, a := range assets {
				if !a.ExpiresAt.After(now) {
					return ErrVideoReconciliation
				}
			}
			if publicationLease != nil && !publicationLease.LockedAt.Add(repository.VideoCompensationLeaseDuration).After(now) {
				return repository.ErrVideoCompensationLeaseLost
			}
			result = &VideoDeliveryResult{RequestID: task.RequestID, TaskID: taskID}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *VideoBillingService) markVideoDeliveryFailure(ctx context.Context, taskID string, owner repository.VideoOwner) error {
	return retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, owner)
			if errors.Is(err, repository.ErrVideoTaskNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if task.Status != model.AIImageTaskSucceeded || task.BillingStatus != model.AIBillingSettled || task.DeliveryStatus != model.AIDeliveryPending {
				return nil
			}
			_, _, _, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
			if err != nil {
				return err
			}
			_, existing, err := repository.NewVideoCompensationRepository(tx).WithClock(s.now).EnsureTx(tx, taskID, owner, "delivery_failed")
			if err != nil {
				return err
			}
			if existing {
				return nil
			}
			return createVideoBillingOutboxTx(tx, task.RequestID, "video_compensation_required", "pending", *task.Operation, hold.HoldAmount, s.now().UTC())
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
}
