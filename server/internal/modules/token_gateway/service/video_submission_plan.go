package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// RecordSubmissionPlan仅冻结原提交意图，不返回容量或执行许可，也不调用Provider。
// G7运行时须另行完成ready/promoting/提交确认；不能把此方法成功当作允许create。
func (l *VideoRepositoryTaskLedger) RecordSubmissionPlan(ctx context.Context, taskID string, claimVersion uint64, provider string) error {
	if l == nil || l.db == nil || ctx == nil || !l.deferDelivery || claimVersion < 2 || provider != "fake-native-async" || l.db.Statement == nil {
		return ErrVideoBillingState
	}
	// 只允许根事务确定提交；savepoint成功或COMMIT未知不能冒称已持久化计划。
	if _, nested := l.db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return ErrVideoBillingState
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return l.db.WithContext(bounded).Transaction(func(tx *gorm.DB) error {
		tasks := repository.NewVideoTaskRepository(tx)
		task, err := tasks.LockForOwnerTx(tx, taskID, l.owner)
		if err != nil {
			return err
		}
		if err := ensureLegacyVideoCapacityTx(tx); err != nil {
			return err
		}
		if _, _, _, _, err := loadVideoFinancialFactsTx(tx, task, l.owner); err != nil {
			return err
		}
		deadline, err := videoSubmissionClaimTx(tx, task, claimVersion)
		if err != nil {
			return err
		}
		eventID := "vg7_plan_" + videoBillingDigest(task.PublicID)
		if task.PlannedProviderCode != nil {
			// 已有计划只读核对，不用当前Worker或当前路由覆盖原提交身份。
			if *task.PlannedProviderCode != provider || task.SubmissionIntentID == nil || !videoProviderTaskUUIDPattern.MatchString(*task.SubmissionIntentID) || task.SubmissionClaimVersion == nil || *task.SubmissionClaimVersion != claimVersion || task.SubmissionWorkerVersion == nil || *task.SubmissionWorkerVersion == 0 || task.SubmissionCapacityEpoch != nil {
				return ErrVideoBillingConflict
			}
			var events []model.AIGatewayTaskEvent
			if err := tx.Where("task_id=? AND event_type='video_submission_planned'", task.ID).Find(&events).Error; err != nil {
				return err
			}
			if len(events) != 1 {
				return ErrVideoBillingState
			}
			e := events[0]
			if e.EventID != eventID || e.EventType != "video_submission_planned" || e.Source != "worker" || e.UserID != task.UserID || e.ProjectID != task.ProjectID || e.FromStatus != nil || e.ToStatus != nil || string(e.SafeDetailJSON) != "{}" {
				return ErrVideoBillingState
			}
			return nil
		}
		if task.Status != model.AIImageTaskSubmitting || task.VersionNo != claimVersion || task.VersionNo == math.MaxUint64 || task.ProviderCode != nil || task.ProviderTaskID != nil || task.AttemptCount != 0 || task.CancelRequestedAt != nil || task.ArchiveTokenHash != nil {
			return ErrVideoBillingState
		}
		// 该新入口不沿用历史零代次兼容；首次计划只能属于有效submit租约。
		if task.WorkerLeaseVersion == 0 || task.WorkerStage == nil || *task.WorkerStage != "submit" {
			return repository.ErrVideoWorkerLeaseLost
		}
		if err := repository.CheckVideoWorkerLeaseTx(bounded, tx, task); err != nil {
			return err
		}
		var clock struct{ Now time.Time }
		if err := tx.Raw("SELECT UTC_TIMESTAMP(6) AS now").Scan(&clock).Error; err != nil {
			return err
		}
		if clock.Now.IsZero() || !clock.Now.Before(deadline) {
			return ErrVideoBillingState
		}
		providerTaskID, err := newVideoProviderTaskUUID()
		if err != nil {
			return err
		}
		if err := videoBillingCASResult(tx.Table("ai_gateway_tasks").Where("id=? AND version_no=? AND planned_provider_code IS NULL", task.ID, claimVersion).Updates(map[string]any{
			"planned_provider_code": provider, "submission_intent_id": providerTaskID,
			"submission_claim_version": claimVersion, "submission_worker_version": task.WorkerLeaseVersion,
			"version_no": gorm.Expr("version_no+1"), "updated_at": clock.Now,
		})); err != nil {
			return err
		}
		// 容量代次保留NULL；恢复epoch不是可执行的容量授权，不从门闩猜测补入。
		e := model.AIGatewayTaskEvent{EventID: eventID, TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "video_submission_planned", Source: "worker", SafeDetailJSON: json.RawMessage(`{}`), CreatedAt: clock.Now}
		if err := tx.Create(&e).Error; err != nil {
			return err
		}
		if err := (&VideoBillingService{fault: l.financialFault}).injectVideoFault("submission_plan"); err != nil {
			return err
		}
		if err := repository.CheckVideoWorkerLeaseTx(bounded, tx, task); err != nil {
			return err
		}
		if err := tx.Raw("SELECT UTC_TIMESTAMP(6) AS now").Scan(&clock).Error; err != nil {
			return err
		}
		if !clock.Now.Before(deadline) {
			return ErrVideoBillingState
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
