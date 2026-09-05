package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// ReleaseUnserviceable 只依据已确认无产物或明确安全拒绝事实全额释放，不接受调用方金额或失败原因。
func (s *VideoBillingService) ReleaseUnserviceable(ctx context.Context, taskID string, owner repository.VideoOwner) (*VideoFinancialResult, error) {
	r, err := s.releaseUnserviceable(ctx, taskID, owner, nil)
	if err != nil && s != nil && s.db != nil && !errors.Is(err, repository.ErrVideoCompensationBusy) && !errors.Is(err, repository.ErrVideoCompensationLeaseLost) && !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		err = errors.Join(err, s.markVideoReleasePending(markCtx, taskID, owner))
	}
	return r, err
}

// RecoverRelease 必须持有当前请求的补偿租约；事务结束前再次核对租约，防止旧Worker提交。
func (s *VideoBillingService) RecoverRelease(ctx context.Context, taskID string, owner repository.VideoOwner, lease repository.VideoCompensationLease) (*VideoFinancialResult, error) {
	return s.releaseUnserviceable(ctx, taskID, owner, &lease)
}

func (s *VideoBillingService) releaseUnserviceable(ctx context.Context, taskID string, owner repository.VideoOwner, lease *repository.VideoCompensationLease) (*VideoFinancialResult, error) {
	if s == nil || s.db == nil {
		return nil, ErrVideoBillingState
	}
	var result *VideoFinancialResult
	err := retryVideoBillingTransaction(ctx, func() error {
		result = nil
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			tasks := repository.NewVideoTaskRepository(tx)
			task, err := tasks.LockForOwnerTx(tx, taskID, owner)
			if err != nil {
				return err
			}
			comp := repository.NewVideoCompensationRepository(tx).WithClock(s.now)
			job, ce := comp.FindRequestTx(tx, task.RequestID)
			if ce != nil && !errors.Is(ce, gorm.ErrRecordNotFound) {
				return ce
			}
			if ce == nil && job.Status != "completed" {
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
			r, q, link, hold, err := loadVideoFinancialFactsTx(tx, task, owner)
			if err != nil {
				return err
			}
			if _, err := loadVideoProviderReleaseProofTx(tx, task, q); err != nil {
				return err
			}
			existing := r.BillingStatus == model.AIBillingReleased
			if !existing {
				if ce == nil && job.Status == "completed" {
					return repository.ErrVideoCompensationLeaseLost
				}
				// 普通首次退款需要当前执行证明；补偿入口已在上方验证自己的同请求当前租约。
				if lease == nil {
					if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
						return err
					}
				}
				if (r.BillingStatus != model.AIBillingHeld && r.BillingStatus != model.AIBillingSettlementPending) || r.DeliveryStatus != model.AIDeliveryPending || hold.Status != billingmodel.HoldStatusHolding || hold.SettledAmount != nil || link.SettledAmount != nil || link.SettleTransactionID != nil || link.ReleaseTransactionID != nil {
					return ErrVideoBillingState
				}
				now, zero := s.now().UTC(), decimal.Zero
				if r.BillingStatus == model.AIBillingHeld {
					task, err = tasks.TransitionBilling(ctx, videoCancelTransition(task, owner, model.AIBillingSettlementPending, "release_pending", now))
					if err != nil {
						return err
					}
				}
				if err := s.injectVideoFault("release_pending"); err != nil {
					return err
				}
				released, err := s.holds.ReleaseHoldTx(tx, hold.ID, task.RequestID+":video-release")
				if err != nil {
					return err
				}
				if released == nil || released.Status != billingmodel.HoldStatusReleased || !released.SettledAmount.IsZero() || released.SettleTransaction != nil || released.ReleaseTransaction == 0 {
					return ErrVideoBillingState
				}
				if err := s.injectVideoFault("release_hold"); err != nil {
					return err
				}
				if err := videoBillingCASResult(tx.Model(&model.AIRequestWalletLink{}).Where("id=? AND settled_amount IS NULL AND settle_transaction_id IS NULL AND release_transaction_id IS NULL", link.ID).Updates(map[string]interface{}{"settled_amount": zero, "release_transaction_id": released.ReleaseTransaction, "updated_at": now})); err != nil {
					return err
				}
				if err := s.injectVideoFault("release_link"); err != nil {
					return err
				}
				price, err := NewVideoPricingService(nil).CalculateVideoFinal(task.RequestID, q.PriceSnapshotJSON, zero)
				if err != nil {
					return err
				}
				currency := "CNY"
				usage := price.UsageFact
				usage.UnitPrice, usage.Amount, usage.Currency, usage.PriceVersionID = &zero, &zero, &currency, &q.PriceVersionID
				// 只追加用户零计量与零销售；Provider计量和成本原样保留，绝不添加虚假的零成本覆盖它们。
				for _, fact := range []model.AIUsageItem{usage, price.SaleLine} {
					if _, old, err := repository.NewVideoUsageRepository(tx).AppendTx(tx, taskID, owner, fact, now); err != nil {
						return err
					} else if old {
						return ErrVideoBillingState
					}
					if err := s.injectVideoFault("release_" + fact.RecordKind); err != nil {
						return err
					}
				}
				task, err = tasks.TransitionBilling(ctx, videoCancelTransition(task, owner, model.AIBillingReleased, "released", now))
				if err != nil {
					return err
				}
				if err := videoBillingCASResult(tx.Model(&model.VideoBillingRequest{}).Where("request_id=? AND version_no=? AND settled_amount IS NULL", task.RequestID, task.RequestVersionNo).Updates(map[string]interface{}{"settled_amount": zero, "version_no": gorm.Expr("version_no+1"), "updated_at": now})); err != nil {
					return err
				}
				if s.budget != nil {
					if err := s.budget.SyncTx(ctx, tx, task.RequestID, s.now); err != nil {
						return err
					}
					if err := s.injectVideoFault("release_budget"); err != nil {
						return err
					}
				}
				task.RequestVersionNo++
				task, err = tasks.TransitionDelivery(ctx, videoCancelTransition(task, owner, model.AIDeliveryRejected, "delivery_rejected", now))
				if err != nil {
					return err
				}
				if err := s.injectVideoFault("release_state"); err != nil {
					return err
				}
				if err := repository.NewVideoInputAssetRepository(tx).ReleaseTaskLeases(ctx, taskID, owner, now); err != nil {
					return err
				}
				if err := s.injectVideoFault("release_lease"); err != nil {
					return err
				}
				if err := createVideoBillingOutboxTx(tx, task.RequestID, "video_billing_released", model.AIBillingReleased, *task.Operation, hold.HoldAmount, now); err != nil {
					return err
				}
				if err := s.injectVideoFault("release_outbox"); err != nil {
					return err
				}
				if err := createVideoBillingOutboxTx(tx, task.RequestID, "video_delivery_rejected", model.AIDeliveryRejected, *task.Operation, zero, now); err != nil {
					return err
				}
				if err := s.injectVideoFault("release_rejected_outbox"); err != nil {
					return err
				}
			}
			if lease != nil {
				if err := comp.FinishTx(tx, *lease, "completed", ""); err != nil {
					return err
				}
				if err := s.injectVideoFault("release_completed"); err != nil {
					return err
				}
			}
			report, err := reconcileVideoTx(tx, taskID, owner, false, nil, s.now().UTC())
			if err != nil {
				return err
			}
			if !report.Passed {
				return ErrVideoReconciliation
			}
			if err := s.injectVideoFault("release_checked"); err != nil {
				return err
			}
			// FinishTx已消费同一租约，事务尾部只能比较原租约时限，不能再用旧version读取完成记录。
			if lease != nil && !lease.LockedAt.Add(repository.VideoCompensationLeaseDuration).After(s.now().UTC()) {
				return repository.ErrVideoCompensationLeaseLost
			}
			if !existing && lease == nil {
				// 钱包、零销售、拒绝交付、输入释放和Outbox必须随过期执行权一起回滚。
				if err := repository.CheckVideoWorkerLeaseTx(ctx, tx, task); err != nil {
					return err
				}
			}
			result = &VideoFinancialResult{RequestID: task.RequestID, TaskID: task.PublicID, ExecutionStatus: task.Status, BillingStatus: model.AIBillingReleased, DeliveryStatus: model.AIDeliveryRejected, HeldAmount: hold.HoldAmount, SettledAmount: decimal.Zero, ReleasedAmount: hold.HoldAmount, Existing: existing}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	return result, err
}

// loadVideoProviderReleaseProofTx 区分无产物确认与安全拒绝；仅有failed或quarantined状态并不构成退款依据。
func loadVideoProviderReleaseProofTx(tx *gorm.DB, task *repository.VideoTaskRecord, q *model.AIGatewayQuote) (*model.VideoUsageItem, error) {
	return loadVideoProviderReleaseProof(tx, task, q, true)
}

// RR容量快照只读同一证明，不取得写锁；证明字段和事件要求与财务事务完全一致。
func loadVideoProviderReleaseProofSnapshotTx(tx *gorm.DB, task *repository.VideoTaskRecord, q *model.AIGatewayQuote) (*model.VideoUsageItem, error) {
	return loadVideoProviderReleaseProof(tx, task, q, false)
}

func loadVideoProviderReleaseProof(tx *gorm.DB, task *repository.VideoTaskRecord, q *model.AIGatewayQuote, lockAssets bool) (*model.VideoUsageItem, error) {
	if task.Status != model.AIImageTaskFailed && task.Status != model.AIImageTaskCancelled {
		return nil, ErrVideoBillingState
	}
	var assets []model.AIImageAsset
	query := tx.Where("request_id=?", task.RequestID)
	if lockAssets {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Find(&assets).Error; err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		outcome := videogateway.ProviderTaskFailed
		if task.Status == model.AIImageTaskCancelled {
			outcome = videogateway.ProviderTaskCancelled
		}
		cost, err := loadVideoConfirmedOutcomeCostTx(tx, task, q, outcome)
		if err != nil || !cost.Quantity.IsZero() || !cost.Amount.IsZero() {
			return nil, ErrVideoBillingState
		}
		if err := validateVideoNoProductProofTx(tx, task, cost); err != nil {
			return nil, err
		}
		return cost, nil
	}
	if len(assets) != 1 || task.Status != model.AIImageTaskFailed {
		return nil, ErrVideoBillingState
	}
	a := assets[0]
	if a.TaskID != task.ID || a.UserID != task.UserID || a.ProjectID != task.ProjectID || a.Modality != "video" || a.AssetRole != "content" || a.ParentAssetID != nil || !a.IsBillableOutput || a.LifecycleState != model.AIImageAssetQuarantined || a.SHA256 == nil || !lowerHex64.MatchString(*a.SHA256) || a.DurationSeconds == nil || !a.DurationSeconds.IsPositive() || a.MediaDeletedAt != nil || a.DeletedAt != nil {
		return nil, ErrVideoBillingState
	}
	origin, from := "", ""
	if a.ModerationStatus == model.AIModerationRejected && a.ModerationPolicyVersion != nil && *a.ModerationPolicyVersion != "" {
		origin, from = "moderation_rejected", model.AIImageTaskModerating
	}
	if a.ModerationStatus == model.AIModerationPassed && (a.ExplicitLabelStatus == model.AIImageLabelFailed || a.ImplicitLabelStatus == model.AIImageLabelFailed) && a.ExplicitLabelVersion != nil && *a.ExplicitLabelVersion != "" && a.ImplicitLabelVersion != nil && *a.ImplicitLabelVersion != "" {
		origin, from = "label_failed", model.AIImageTaskLabeling
	}
	if origin == "" {
		return nil, ErrVideoBillingState
	}
	// 只能使用唯一真实终止迁移的不可变原因；补充事件、派生失败或未知标识不能替换它。
	var events []model.VideoExecutionFailureEvent
	if err := tx.Where("task_id=? AND event_type IN ? AND to_status='failed'", task.ID, []string{"execution_status_changed", "provider_callback_status_changed"}).Find(&events).Error; err != nil {
		return nil, err
	}
	if len(events) != 1 {
		return nil, ErrVideoBillingState
	}
	event := events[0]
	if event.UserID != task.UserID || event.ProjectID != task.ProjectID || event.EventType != "execution_status_changed" || event.Source != "worker" || event.FailureOrigin != origin || event.FromStatus == nil || *event.FromStatus != from || task.CompletedAt == nil || !event.CreatedAt.Equal(*task.CompletedAt) {
		return nil, ErrVideoBillingState
	}
	cost, err := loadVideoConfirmedCostTx(tx, task, q)
	if err != nil || !cost.Quantity.Equal(*a.DurationSeconds) {
		return nil, ErrVideoBillingState
	}
	return cost, nil
}
