package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

func (l *VideoRepositoryTaskLedger) ValidateSubmissionClaim(ctx context.Context, taskID string, version uint64) (time.Time, error) {
	if !l.deferDelivery || version < 2 {
		return time.Time{}, ErrVideoBillingState
	}
	var deadline time.Time
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, l.owner)
		if err != nil {
			return err
		}
		if err := ensureLegacyVideoCapacityTx(tx); err != nil {
			return err
		}
		if _, _, _, _, err := loadVideoFinancialFactsTx(tx, task, l.owner); err != nil {
			return err
		}
		if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
			return err
		}
		deadline, err = videoSubmissionClaimTx(tx, task, version)
		if err != nil {
			return err
		}
		if task.Status != model.AIImageTaskSubmitting || task.ProviderTaskID != nil || task.AttemptCount != 0 || !l.now().UTC().Before(deadline) {
			return ErrVideoBillingState
		}
		if task.WorkerLeaseVersion > 0 && task.WorkerLeaseUntil != nil && task.WorkerLeaseUntil.Before(deadline) {
			deadline = *task.WorkerLeaseUntil
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return deadline, err
}

// RecordSubmissionReceipt 即使原ctx已结束，也只用最多5秒保存原回执，不解密Prompt、不读取参考图。
func (l *VideoRepositoryTaskLedger) RecordSubmissionReceipt(parent context.Context, taskID string, claimVersion uint64, receipt videogateway.SubmitResult) (videogateway.GatewayTask, error) {
	if !l.deferDelivery || claimVersion < 2 || receipt.ProviderCode != "fake-native-async" || !videoBillingPublicID.MatchString(receipt.ProviderTaskID) {
		return videogateway.GatewayTask{}, ErrVideoBillingState
	}
	switch receipt.Status {
	case videogateway.ProviderTaskQueued, videogateway.ProviderTaskProcessing, videogateway.ProviderTaskSucceeded, videogateway.ProviderTaskFailed, videogateway.ProviderTaskCancelled, videogateway.ProviderTaskUnknown:
	default:
		return videogateway.GatewayTask{}, ErrVideoBillingState
	}
	hash, err := videoSubmissionReceiptHash(claimVersion, receipt)
	if err != nil {
		return videogateway.GatewayTask{}, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	var result videogateway.GatewayTask
	var rejected error
	err = retryVideoBillingTransaction(ctx, func() error {
		rejected = nil
		return l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			tasks := repository.NewVideoTaskRepository(tx)
			task, err := tasks.LockForOwnerTx(tx, taskID, l.owner)
			if err != nil {
				return err
			}
			if receipt.RequestID != task.RequestID {
				return ErrVideoBillingConflict
			}
			if task.SubmissionIntentID != nil && (!videoProviderTaskUUIDPattern.MatchString(*task.SubmissionIntentID) || receipt.ProviderTaskID != *task.SubmissionIntentID) {
				return ErrVideoBillingConflict
			}
			if _, _, _, _, err := loadVideoFinancialFactsTx(tx, task, l.owner); err != nil {
				return err
			}
			deadline, err := videoSubmissionClaimTx(tx, task, claimVersion)
			if err != nil {
				return err
			}
			if task.ProviderTaskID != nil || task.ProviderCode != nil {
				var accepted model.VideoFinancialEvent
				if err := tx.Where("event_id=? AND task_id=? AND user_id=? AND project_id=? AND event_type='submission_receipt_accepted' AND source='worker' AND from_status IS NULL AND to_status IS NULL", "vg5_"+videoBillingDigest(task.RequestID+":submission_accepted"), task.ID, task.UserID, task.ProjectID).First(&accepted).Error; err != nil {
					return ErrVideoBillingState
				}
				if task.ProviderTaskID == nil || task.ProviderCode == nil || *task.ProviderTaskID != receipt.ProviderTaskID || *task.ProviderCode != receipt.ProviderCode || task.AttemptCount != 1 || accepted.FactSHA256 != hash {
					// 原claim已验明：拒绝新绑定，但单独提交低敏审计，不能随业务错误一起回滚。
					rejected = ErrVideoBillingConflict
					return appendVideoSubmissionRejectionTx(tx, task, hash, l.now().UTC())
				}
				result = videoSubmissionMetadata(task)
				return nil
			}
			if task.Status != model.AIImageTaskSubmitting && task.Status != model.AIImageTaskPendingReconcile {
				return ErrVideoBillingState
			}
			// 已接受的相同回执已在上方只读返回；未绑定身份的首次写入必须服从当前执行代次。
			// WithoutCancel只隔离原RPC取消，不允许旧Worker绕过pending分支继续绑定。
			if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
				return err
			}
			now := l.now().UTC()
			if task.Status == model.AIImageTaskSubmitting && (!now.Before(deadline) || receipt.Status == videogateway.ProviderTaskUnknown || receipt.Status == videogateway.ProviderTaskFailed || receipt.Status == videogateway.ProviderTaskCancelled) {
				task, err = tasks.TransitionExecution(ctx, repository.VideoStateTransition{TaskPublicID: taskID, Owner: l.owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskPendingReconcile, Progress: task.Progress, EventID: "vg5_" + videoBillingDigest(task.RequestID+":submission_expired"), Source: "reconciler", Now: now})
				if err != nil {
					return err
				}
			}
			if task.Status == model.AIImageTaskSubmitting {
				task, err = tasks.BindProviderTask(ctx, repository.VideoProviderBinding{TaskPublicID: taskID, Owner: l.owner, ExpectedVersion: task.VersionNo, ProviderCode: receipt.ProviderCode, ProviderTaskID: receipt.ProviderTaskID, EventID: "vg5_" + videoBillingDigest(task.RequestID+":submission_bound"), Now: now})
				if err != nil {
					return err
				}
			} else {
				// 迟到的有效ID只追加身份，不让pending回到submitted，也不表示Provider成本已确认。
				if err := videoBillingCASResult(tx.Model(&model.AIImageTask{}).Where("id=? AND version_no=? AND provider_code IS NULL AND provider_task_id IS NULL AND attempt_count=0", task.ID, task.VersionNo).Updates(map[string]interface{}{"provider_code": receipt.ProviderCode, "provider_task_id": receipt.ProviderTaskID, "attempt_count": 1, "version_no": gorm.Expr("version_no+1"), "updated_at": now})); err != nil {
					return err
				}
				e := model.AIGatewayTaskEvent{EventID: "vg5_" + videoBillingDigest(task.RequestID+":submission_bound_pending"), TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "provider_task_bound_pending", Source: "worker", SafeDetailJSON: json.RawMessage(`{"reason":"provider_bound"}`), CreatedAt: now}
				if err := tx.Create(&e).Error; err != nil {
					return err
				}
				task, err = tasks.LockForOwnerTx(tx, taskID, l.owner)
				if err != nil {
					return err
				}
				billing := &VideoBillingService{db: tx, now: l.now, fault: l.financialFault}
				if _, err := billing.reconcileVideoExecutionTx(ctx, tx, task, l.owner); err != nil {
					return err
				}
			}
			// 原回执与绑定同事务冻结摘要；后续查询状态不经过此入口，不能冒充提交重放。
			accepted := model.VideoFinancialEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: "vg5_" + videoBillingDigest(task.RequestID+":submission_accepted"), TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "submission_receipt_accepted", Source: "worker", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}, FactSHA256: hash}
			if err := tx.Create(&accepted).Error; err != nil {
				return err
			}
			if err := (&VideoBillingService{fault: l.financialFault}).injectVideoFault("submission_receipt"); err != nil {
				return err
			}
			// SQL锁等待和写入也消耗租期。跨期时撤销整份正常绑定，重试仅持久化迟到回执。
			// 已存在的同ID重放已在上方只读返回，不会因事后到期推翻成功绑定。
			if task.Status == model.AIImageTaskSubmitted && !l.now().UTC().Before(deadline) {
				return billingservice.ErrConcurrentUpdate
			}
			// 绑定、接受事件及恢复检查都消耗租期；事务尾部再次读数据库时钟，过期则整笔回滚。
			// Task锁仍由本事务持有，其他执行者不能在此期间续期或换代。
			if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
				return err
			}
			result = videoSubmissionMetadata(task)
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err == nil && rejected != nil {
		return videogateway.GatewayTask{}, rejected
	}
	return result, err
}

// 规范化元数据只在内存求摘要，原Provider正文不进入事件表。
func videoSubmissionReceiptHash(claimVersion uint64, receipt videogateway.SubmitResult) (string, error) {
	canonical, err := json.Marshal(struct {
		ClaimVersion uint64
		Receipt      videogateway.SubmitResult
	}{claimVersion, receipt})
	if err != nil {
		return "", err
	}
	return videoBillingDigest(string(canonical)), nil
}

// 仅保存回执摘要及固定原因；不保存原Provider正文、未知状态字符串或可访问媒体的标识。
func appendVideoSubmissionRejectionTx(tx *gorm.DB, task *repository.VideoTaskRecord, hash string, now time.Time) error {
	eventID := "vg5_" + videoBillingDigest(task.RequestID+":submission_rejected:"+hash)
	var old model.VideoFinancialEvent
	err := tx.Where("event_id=?", eventID).First(&old).Error
	if err == nil {
		if old.TaskID != task.ID || old.UserID != task.UserID || old.ProjectID != task.ProjectID || old.EventType != "submission_receipt_rejected" || old.Source != "worker" || old.FactSHA256 != hash || old.FromStatus != nil || old.ToStatus != nil {
			return ErrVideoBillingConflict
		}
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	e := model.VideoFinancialEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: eventID, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "submission_receipt_rejected", Source: "worker", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}, FactSHA256: hash}
	return tx.Create(&e).Error
}

func videoSubmissionMetadata(task *repository.VideoTaskRecord) videogateway.GatewayTask {
	r := videogateway.GatewayTask{DeferDelivery: true, TaskID: task.PublicID, RequestID: task.RequestID, Status: videogateway.TaskStatus(task.Status), Version: task.VersionNo, CancelRequestedAt: task.CancelRequestedAt}
	if task.SubmissionClaimVersion != nil {
		r.SubmissionClaimVersion = *task.SubmissionClaimVersion
	}
	if task.Operation != nil {
		r.Operation = *task.Operation
	}
	if task.ProviderCode != nil {
		r.ProviderCode = *task.ProviderCode
	}
	if task.ProviderTaskID != nil {
		r.ProviderTaskID = *task.ProviderTaskID
	}
	return r
}
