package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
)

const VideoCompensationLeaseDuration = 2 * time.Minute

var (
	ErrVideoCompensationBusy      = errors.New("视频补偿租约仍由其他执行器持有")
	ErrVideoCompensationNotReady  = errors.New("视频补偿尚不可执行或已停止")
	ErrVideoCompensationLeaseLost = errors.New("视频补偿租约已失效")
	ErrVideoCompensationReview    = errors.New("视频补偿需要两个不同的有效核对主体")
	videoWorkerID                 = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
)

type VideoCompensationLease struct {
	ID        uint64
	RequestID string
	VersionNo uint64
	LockedBy  string
	LockedAt  time.Time
	Mode      string
}

// VideoCompensationRepository 只管理共享MySQL任务与租约，没有Provider或外部消息能力。
type VideoCompensationRepository struct {
	db  *gorm.DB
	now func() time.Time
}

func NewVideoCompensationRepository(db *gorm.DB) *VideoCompensationRepository {
	return &VideoCompensationRepository{db: db, now: time.Now}
}
func (r *VideoCompensationRepository) WithClock(clock func() time.Time) *VideoCompensationRepository {
	copy := *r
	if clock != nil {
		copy.now = clock
	}
	return &copy
}

func validVideoCompensationError(code string) bool {
	switch code {
	case "settlement_failed", "release_failed", "delivery_failed", "facts_missing", "facts_conflict", "provider_unknown", "media_unavailable", "delivery_pending", "retry_exhausted", "manual_review":
		return true
	}
	return false
}

// EnsureTx 在请求事务中创建唯一补偿；重放不重置次数、dead或completed状态。
func (r *VideoCompensationRepository) EnsureTx(tx *gorm.DB, taskID string, owner VideoOwner, code string) (*model.VideoCompensationTask, bool, error) {
	if tx == nil || !validVideoCompensationError(code) {
		return nil, false, ErrVideoCompensationNotReady
	}
	task, err := findVideoTaskRecord(tx, taskID, owner, true)
	if err != nil {
		return nil, false, err
	}
	item, err := r.FindRequestTx(tx, task.RequestID)
	if err == nil {
		return item, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	now := r.now().UTC().Truncate(time.Second)
	item = &model.VideoCompensationTask{AICompensationTask: model.AICompensationTask{TaskKey: "video:" + task.RequestID, TaskType: "video_reconcile", AggregateID: task.RequestID, Status: "pending", NextRetryAt: now, LastErrorClass: &code, CreatedAt: now, UpdatedAt: now}, VersionNo: 1, LastSafeErrorCode: &code}
	item.OriginErrorCode = code
	item.InitialBillingStatus = task.BillingStatus
	if err := tx.Create(item).Error; err != nil {
		return nil, false, err
	}
	return item, false, nil
}

func (r *VideoCompensationRepository) FindRequestTx(tx *gorm.DB, requestID string) (*model.VideoCompensationTask, error) {
	var item model.VideoCompensationTask
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("task_key=? AND task_type='video_reconcile' AND aggregate_id=?", "video:"+requestID, requestID).First(&item).Error
	return &item, err
}

func (r *VideoCompensationRepository) GetForTask(ctx context.Context, taskID string, owner VideoOwner) (*model.VideoCompensationTask, error) {
	task, err := NewVideoTaskRepository(r.db).FindForOwner(ctx, taskID, owner)
	if err != nil {
		return nil, err
	}
	var item model.VideoCompensationTask
	err = r.db.WithContext(ctx).Where("task_key=? AND task_type='video_reconcile' AND aggregate_id=?", "video:"+task.RequestID, task.RequestID).First(&item).Error
	return &item, err
}

// Claim 使用版本围栏而非仅时间戳，进程即使重复使用同一worker_id也不能持有旧执行权。
func (r *VideoCompensationRepository) Claim(ctx context.Context, requestID, worker string) (*VideoCompensationLease, error) {
	if !videoWorkerID.MatchString(worker) {
		return nil, ErrVideoCompensationNotReady
	}
	var lease *VideoCompensationLease
	exhausted := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		item, err := r.FindRequestTx(tx, requestID)
		if err != nil {
			return err
		}
		now := r.now().UTC().Truncate(time.Second)
		if item.Status == "running" && item.LockedAt != nil && item.LockedAt.Add(VideoCompensationLeaseDuration).After(now) {
			return ErrVideoCompensationBusy
		}
		if item.Status != "pending" && item.Status != "retry" && item.Status != "running" {
			return ErrVideoCompensationNotReady
		}
		if item.Status != "running" && item.NextRetryAt.After(now) {
			return ErrVideoCompensationNotReady
		}
		if item.AttemptCount >= 8 {
			exhausted = true
			return videoCompensationCAS(tx, item, map[string]interface{}{"status": "dead", "locked_at": nil, "locked_by": nil, "lease_mode": nil, "last_error_class": "retry_exhausted", "last_safe_error_code": "retry_exhausted", "updated_at": now})
		}
		if err := videoCompensationCAS(tx, item, map[string]interface{}{"status": "running", "locked_at": now, "locked_by": worker, "lease_mode": "worker", "attempt_count": item.AttemptCount + 1, "delivery_request_version": nil, "delivery_prepared_at": nil, "updated_at": now}); err != nil {
			return err
		}
		lease = &VideoCompensationLease{ID: item.ID, RequestID: requestID, VersionNo: item.VersionNo + 1, LockedAt: now, LockedBy: worker, Mode: "worker"}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if exhausted {
		return nil, ErrVideoCompensationNotReady
	}
	return lease, nil
}

// CheckLeaseTx 在财务写入前后重复核验同一围栏，并持有行锁直到财务事务提交。
func (r *VideoCompensationRepository) CheckLeaseTx(tx *gorm.DB, lease VideoCompensationLease) (*model.VideoCompensationTask, error) {
	item, err := r.FindRequestTx(tx, lease.RequestID)
	if err != nil {
		return nil, ErrVideoCompensationLeaseLost
	}
	now := r.now().UTC()
	if item.ID != lease.ID || item.Status != "running" || item.VersionNo != lease.VersionNo || item.LockedBy == nil || *item.LockedBy != lease.LockedBy || item.LockedAt == nil || !item.LockedAt.Equal(lease.LockedAt) || item.LeaseMode == nil || *item.LeaseMode != lease.Mode || !item.LockedAt.Add(VideoCompensationLeaseDuration).After(now) {
		return nil, ErrVideoCompensationLeaseLost
	}
	return item, nil
}

// FinishTx 只能由当前租约结束尝试；第8次失败进入dead，不能通过重放重置次数。
func (r *VideoCompensationRepository) FinishTx(tx *gorm.DB, lease VideoCompensationLease, status, code string) error {
	if status != "completed" && status != "retry" && status != "dead" && status != "manual_review" {
		return ErrVideoCompensationNotReady
	}
	if status != "completed" && !validVideoCompensationError(code) {
		return ErrVideoCompensationNotReady
	}
	item, err := r.CheckLeaseTx(tx, lease)
	if err != nil {
		return err
	}
	if status == "completed" {
		// completed表示业务恢复闭合，不只是执行器退出；财务完成但交付pending仍不得完成。
		var count int64
		err := tx.Raw(`SELECT COUNT(*) FROM ai_requests r
JOIN ai_gateway_tasks t ON t.request_id=r.request_id AND t.user_id=r.user_id AND t.project_id=r.project_id
JOIN ai_request_wallet_links l ON l.request_id=r.request_id
JOIN wallet_holds h ON h.id=l.wallet_hold_id AND h.user_id=r.user_id AND h.wallet_id=l.wallet_id
WHERE r.request_id=? AND r.command_kind='create_video' AND r.settled_amount=l.settled_amount AND l.settled_amount=h.settled_amount
AND NOT EXISTS(SELECT 1 FROM ai_gateway_task_inputs i WHERE i.task_id=t.id AND i.lease_released_at IS NULL)
AND ((r.billing_status='released' AND r.delivery_status='rejected' AND h.status='released' AND r.settled_amount=0 AND t.status IN ('failed','cancelled','expired'))
OR (r.billing_status='settled' AND r.delivery_status='available' AND h.status='settled' AND r.settled_amount>0 AND t.status='succeeded'
AND (SELECT COUNT(*) FROM ai_gateway_assets a WHERE a.task_id=t.id AND a.lifecycle_state='available')=6))`, item.AggregateID).Scan(&count).Error
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrVideoCompensationNotReady
		}
	}
	now := r.now().UTC().Truncate(time.Second)
	changes := map[string]interface{}{"status": status, "locked_at": nil, "locked_by": nil, "lease_mode": nil, "updated_at": now, "next_retry_at": now, "completed_at": nil, "last_error_class": nil, "last_safe_error_code": nil}
	if status == "completed" {
		changes["completed_at"] = now
	} else {
		changes["delivery_request_version"], changes["delivery_prepared_at"] = nil, nil
		changes["last_error_class"], changes["last_safe_error_code"] = code, code
		changes["retry_count"] = item.AttemptCount
		if status == "retry" && item.AttemptCount >= 8 {
			changes["status"], changes["last_error_class"], changes["last_safe_error_code"] = "dead", "retry_exhausted", "retry_exhausted"
		} else if status == "retry" {
			delay := time.Duration(1<<item.AttemptCount) * time.Second
			changes["next_retry_at"] = now.Add(delay)
		}
	}
	return videoCompensationCAS(tx, item, changes)
}

// ClaimManual 不抢占活跃租约；核对主体必须不同且存在。每次核对另加不可变TaskEvent。
func (r *VideoCompensationRepository) ClaimManual(ctx context.Context, requestID, worker string, maker, checker uint64) (*VideoCompensationLease, error) {
	if !videoWorkerID.MatchString(worker) {
		return nil, ErrVideoCompensationReview
	}
	var lease *VideoCompensationLease
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task model.AIImageTask
		if err := tx.Where("request_id=? AND capability=?", requestID, model.AIVideoCapability).First(&task).Error; err != nil {
			return err
		}
		owner := VideoOwner{UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID}
		if _, err := findVideoTaskRecord(tx, task.PublicID, owner, true); err != nil {
			return err
		}
		item, err := r.FindRequestTx(tx, requestID)
		if err != nil {
			return err
		}
		now := r.now().UTC().Truncate(time.Second)
		if item.Status == "running" && item.LockedAt != nil && item.LockedAt.Add(VideoCompensationLeaseDuration).After(now) {
			return ErrVideoCompensationBusy
		}
		if item.Status == "completed" {
			return ErrVideoCompensationNotReady
		}
		if maker == 0 || checker == 0 || maker == checker {
			return ErrVideoCompensationReview
		}
		var n int64
		if err := tx.Table("users").Where("id IN ? AND status='active'", []uint64{maker, checker}).Count(&n).Error; err != nil {
			return err
		}
		if n != 2 {
			return ErrVideoCompensationReview
		}
		event := model.VideoCompensationReviewEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: fmt.Sprintf("vg5_comp_review_%d_%d", item.ID, item.VersionNo+1), TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "video_compensation_manual_claimed", Source: "reconciler", CreatedAt: now}, ReviewMakerID: maker, ReviewCheckerID: checker}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		if err := videoCompensationCAS(tx, item, map[string]interface{}{"status": "running", "locked_at": now, "locked_by": worker, "lease_mode": "manual", "review_maker_id": maker, "review_checker_id": checker, "delivery_request_version": nil, "delivery_prepared_at": nil, "updated_at": now}); err != nil {
			return err
		}
		lease = &VideoCompensationLease{ID: item.ID, RequestID: requestID, VersionNo: item.VersionNo + 1, LockedAt: now, LockedBy: worker, Mode: "manual"}
		return nil
	})
	return lease, err
}

// PrepareDeliveryTx 仅在发布事务中签发本租约/本请求版本的临时发布标记；回滚会撤销标记及新围栏。
func (r *VideoCompensationRepository) PrepareDeliveryTx(tx *gorm.DB, requestID string, requestVersion uint64, lease VideoCompensationLease) (VideoCompensationLease, error) {
	if lease.RequestID != requestID {
		return lease, ErrVideoCompensationLeaseLost
	}
	item, err := r.CheckLeaseTx(tx, lease)
	if err != nil {
		return lease, err
	}
	var count int64
	if err := tx.Table("ai_requests").Where("request_id=? AND command_kind='create_video' AND version_no=? AND billing_status='settled' AND delivery_status='pending'", requestID, requestVersion).Count(&count).Error; err != nil {
		return lease, err
	}
	if count != 1 || item.DeliveryRequestVersion != nil {
		return lease, ErrVideoCompensationNotReady
	}
	now := r.now().UTC().Truncate(time.Second)
	if err := videoCompensationCAS(tx, item, map[string]interface{}{"delivery_request_version": requestVersion + 1, "delivery_prepared_at": now, "updated_at": now}); err != nil {
		return lease, err
	}
	lease.VersionNo++
	return lease, nil
}

func videoCompensationCAS(tx *gorm.DB, item *model.VideoCompensationTask, changes map[string]interface{}) error {
	changes["version_no"] = item.VersionNo + 1
	result := tx.Model(&model.VideoCompensationTask{}).Where("id=? AND task_type='video_reconcile' AND status=? AND version_no=?", item.ID, item.Status, item.VersionNo).Updates(changes)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVideoCompensationLeaseLost
	}
	return nil
}
