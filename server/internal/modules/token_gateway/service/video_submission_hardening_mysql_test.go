package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 时限会在SQL写入中继续前进，不能用事务入口的旧时钟提交过期的submitted。
func TestVideoG5SubmissionMySQLTailExpiryRetriesOnlyMetadata(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, l, claim, receipt, deadline := videoG5ClaimFixture(t, db)
	now := deadline.Add(-time.Second)
	l.now = func() time.Time { return now }
	l.financialFault = func(at string) error {
		if at == "submission_receipt" {
			now = deadline
		}
		return nil
	}
	r, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, receipt)
	if err != nil || r.Status != videogateway.TaskPendingReconcile || r.ProviderTaskID != receipt.ProviderTaskID {
		t.Fatalf("尾部跨期只能提交待核对绑定: %s %v", r.Status, err)
	}
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), claim.TaskID, f.owner)
	if err != nil || task.AttemptCount != 1 || task.BillingStatus != model.AIBillingSettlementPending {
		t.Fatal("重试不能留下重复提交计数或半份HPC")
	}
	var n int64
	if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND event_type='provider_task_bound'", task.ID).Count(&n).Error; err != nil || n != 0 {
		t.Fatal("过期的正常绑定事件必须回滚")
	}
}

func TestVideoG5SubmissionMySQLRejectedReceiptAuditedWithoutMutation(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, l, claim, receipt, _ := videoG5ClaimFixture(t, db)
	if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, receipt); err != nil {
		t.Fatal(err)
	}
	wrong := receipt
	wrong.ProviderTaskID += "-other"
	for i := 0; i < 2; i++ {
		if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, wrong); err == nil {
			t.Fatal("异ID回执必须拒绝")
		}
	}
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), claim.TaskID, f.owner)
	if err != nil || task.ProviderTaskID == nil || *task.ProviderTaskID != receipt.ProviderTaskID || task.AttemptCount != 1 {
		t.Fatal("拒绝不能改写原绑定")
	}
	var n int64
	if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND event_type='submission_receipt_rejected'", task.ID).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("拒绝审计必须追加且幂等: %d %v", n, err)
	}
	var audit model.VideoFinancialEvent
	if err := db.Where("task_id=? AND event_type='submission_receipt_rejected'", task.ID).First(&audit).Error; err != nil || len(audit.FactSHA256) != 64 || strings.Contains(string(audit.SafeDetailJSON), wrong.ProviderTaskID) || audit.FromStatus != nil || audit.ToStatus != nil {
		t.Fatal("拒绝审计只能保存摘要和低敏原因，不能推进状态")
	}
	for _, eventType := range []string{"submission_receipt_rejected", "submission_receipt_accepted", "provider_task_bound_pending"} {
		e := audit.AIGatewayTaskEvent
		e.EventType = eventType
		e.EventID += "-forged"
		if err := repository.NewVideoTaskEventRepository(db).Append(context.Background(), task.PublicID, f.owner, e); !errors.Is(err, repository.ErrVideoUnsafeDetail) {
			t.Fatal("通用追加入口不得伪造专用回执事实")
		}
	}
	if err := db.Model(&model.AIGatewayTaskEvent{}).Where("id=?", audit.ID).Update("source", "api").Error; err == nil {
		t.Fatal("拒绝审计不得被UPDATE")
	}
	if err := db.Delete(&model.AIGatewayTaskEvent{}, audit.ID).Error; err == nil {
		t.Fatal("拒绝审计不得被DELETE")
	}
}

// 空状态和任意字符串都不能被当成受信的提交成功。
func TestVideoG5SubmissionMySQLInvalidStatusCannotBind(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, l, claim, receipt, _ := videoG5ClaimFixture(t, db)
	for _, status := range []videogateway.ProviderTaskStatus{"", "unexpected"} {
		receipt.Status = status
		if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, receipt); err == nil {
			t.Fatal("未知协议状态不得成为正常绑定")
		}
	}
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), claim.TaskID, f.owner)
	if err != nil || task.Status != model.AIImageTaskSubmitting || task.ProviderTaskID != nil || task.AttemptCount != 0 {
		t.Fatal("无效回执不能改写原提交权")
	}
}

// 同ID不代表同回执；首次受信摘要冻结后，改变安全含义的回复不能伪装成幂等重放。
func TestVideoG5SubmissionMySQLSameIDChangedReceiptRejected(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, initial := range []videogateway.ProviderTaskStatus{videogateway.ProviderTaskQueued, videogateway.ProviderTaskUnknown} {
		t.Run(string(initial), func(t *testing.T) {
			f, l, claim, receipt, _ := videoG5ClaimFixture(t, db)
			receipt.Status = initial
			first, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, receipt)
			if err != nil {
				t.Fatal(err)
			}
			for _, changed := range []videogateway.ProviderTaskStatus{videogateway.ProviderTaskQueued, videogateway.ProviderTaskProcessing, videogateway.ProviderTaskSucceeded, videogateway.ProviderTaskFailed, videogateway.ProviderTaskCancelled, videogateway.ProviderTaskUnknown} {
				candidate := receipt
				candidate.Status = changed
				_, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, candidate)
				if changed == initial && err != nil {
					t.Fatalf("原回执重放应成功: %v", err)
				}
				if changed != initial && !errors.Is(err, ErrVideoBillingConflict) {
					t.Fatalf("不同回执状态%s不能冒充重放: %v", changed, err)
				}
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), claim.TaskID, f.owner)
			if err != nil || task.VersionNo != first.Version || task.AttemptCount != 1 || videogateway.TaskStatus(task.Status) != first.Status {
				t.Fatal("异回执不能倒写已落库状态或身份")
			}
			var n int64
			if err := db.Model(&model.AIGatewayTaskEvent{}).Where("task_id=? AND event_type='submission_receipt_rejected'", task.ID).Count(&n).Error; err != nil || n != 5 {
				t.Fatalf("五种异回执必须各有一条低敏审计: %d %v", n, err)
			}
		})
	}
}

// 在真实MySQL锁等待可见后才推进合成时钟，验证不是仅在进入函数时检查期限。
func TestVideoG5SubmissionMySQLLockWaitCrossesDeadline(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"validate", "receipt"} {
		t.Run(mode, func(t *testing.T) {
			f, l, claim, receipt, deadline := videoG5ClaimFixture(t, db)
			var clock atomic.Int64
			clock.Store(deadline.Add(-time.Second).UnixNano())
			l.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
			blocker := db.Begin()
			if blocker.Error != nil {
				t.Fatal(blocker.Error)
			}
			defer blocker.Rollback()
			var connectionID uint64
			if err := blocker.Raw("SELECT CONNECTION_ID()").Scan(&connectionID).Error; err != nil {
				t.Fatal(err)
			}
			var row model.AIImageTask
			if err := blocker.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id=?", claim.TaskID).First(&row).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if mode == "validate" {
					_, err := l.ValidateSubmissionClaim(ctx, claim.TaskID, claim.Version)
					done <- err
				} else {
					_, err := l.RecordSubmissionReceipt(ctx, claim.TaskID, claim.Version, receipt)
					done <- err
				}
			}()
			videoG5WaitForBlockedSubmission(t, db, connectionID)
			clock.Store(deadline.UnixNano())
			if err := blocker.Commit().Error; err != nil {
				t.Fatal(err)
			}
			err := <-done
			if (mode == "validate" && err == nil) || (mode == "receipt" && err != nil) {
				t.Fatalf("等待锁后须重新判定租期: %v", err)
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), claim.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "receipt" && (task.Status != model.AIImageTaskPendingReconcile || task.BillingStatus != model.AIBillingSettlementPending || task.AttemptCount != 1) {
				t.Fatal("锁等待期间到期，回执只能绑定到待核对任务")
			}
		})
	}
}

func videoG5WaitForBlockedSubmission(t *testing.T, db *gorm.DB, connectionID uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var n int64
		if err := db.WithContext(ctx).Raw(`SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.threads t ON t.THREAD_ID=w.BLOCKING_THREAD_ID WHERE t.PROCESSLIST_ID=?`, connectionID).Scan(&n).Error; err != nil {
			t.Fatal("读取临时库锁等待失败")
		}
		if n > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("未观察到原提交任务行的真实锁等待")
		case <-ticker.C:
		}
	}
}
