package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

var (
	ErrVideoObjectObservationInvalid  = errors.New("视频对象对账观察无效")
	ErrVideoObjectObservationConflict = errors.New("视频对象对账观察冲突")
)

const (
	VideoObjectDBMissing    = "db_missing_object"
	VideoObjectUnreferenced = "storage_unreferenced_object"
)

type VideoObjectObservation struct {
	ID                                         uint64 `gorm:"primaryKey"`
	Direction, Bucket, ObjectKey, ObjectSHA256 string
	SizeBytes                                  uint64
	FirstSeenAt, LastSeenAt, NextObserveAt     time.Time
	ObservationCount, VersionNo                uint64
	Status                                     string
	ResolvedAt                                 *time.Time
	CreatedAt, UpdatedAt                       time.Time
}

func (VideoObjectObservation) TableName() string {
	return "ai_video_object_reconciliation_observations"
}

type VideoObjectReconciliationRepository struct{ db *gorm.DB }

type VideoObjectCleanupLease struct {
	TaskID      uint64
	LockedAt    time.Time
	Observation VideoObjectObservation
}

func NewVideoObjectReconciliationRepository(db *gorm.DB) *VideoObjectReconciliationRepository {
	return &VideoObjectReconciliationRepository{db: db}
}

// Observe要求同一异常跨过静默窗再次出现才确认；一次暂时不可见不会生成删除或补偿任务。
func (r *VideoObjectReconciliationRepository) Observe(ctx context.Context, direction string, ref video.VideoObjectRef, digest string, size uint64, now time.Time, grace time.Duration) (*VideoObjectObservation, error) {
	if r == nil || r.db == nil || !validVideoObjectObservation(direction, ref, digest, size) || now.IsZero() || grace < time.Minute || grace > 24*time.Hour {
		return nil, ErrVideoObjectObservationInvalid
	}
	now = now.UTC()
	var result VideoObjectObservation
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("direction=? AND bucket=? AND object_key=? AND status IN ('observing','confirmed')", direction, ref.Bucket, ref.ObjectKey).Order("id DESC").Take(&result).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result = VideoObjectObservation{Direction: direction, Bucket: ref.Bucket, ObjectKey: ref.ObjectKey, ObjectSHA256: digest, SizeBytes: size, FirstSeenAt: now, LastSeenAt: now, NextObserveAt: now.Add(grace), ObservationCount: 1, Status: "observing", VersionNo: 1, CreatedAt: now, UpdatedAt: now}
			return tx.Create(&result).Error
		}
		if err != nil {
			return err
		}
		if result.ObjectSHA256 != digest || result.SizeBytes != size {
			return ErrVideoObjectObservationConflict
		}
		if result.Status == "confirmed" || now.Before(result.NextObserveAt) {
			return nil
		}
		updated := tx.Model(&VideoObjectObservation{}).Where("id=? AND status='observing' AND version_no=? AND observation_count=?", result.ID, result.VersionNo, result.ObservationCount).Updates(map[string]any{
			"status": "confirmed", "observation_count": gorm.Expr("observation_count+1"), "last_seen_at": now, "next_observe_at": now.Add(grace), "version_no": gorm.Expr("version_no+1"), "updated_at": now,
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrVideoObjectObservationConflict
		}
		result.Status, result.ObservationCount, result.VersionNo, result.LastSeenAt, result.NextObserveAt, result.UpdatedAt = "confirmed", result.ObservationCount+1, result.VersionNo+1, now, now.Add(grace), now
		taskType := "video_orphan_cleanup"
		if direction == VideoObjectDBMissing {
			taskType = "video_object_missing_reconcile"
		}
		reason := direction
		task := model.AICompensationTask{TaskKey: videoObjectObservationTaskKey(result.ID, direction), TaskType: taskType, AggregateID: strconv.FormatUint(result.ID, 10), Status: "pending", NextRetryAt: now, LastErrorClass: &reason, CreatedAt: now, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "task_key"}}, DoNothing: true}).Create(&task).Error
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// Resolve只关闭当前活动观察并保留历史；相同位置未来再次异常会创建新观察episode。
func (r *VideoObjectReconciliationRepository) Resolve(ctx context.Context, direction string, ref video.VideoObjectRef, now time.Time) error {
	if r == nil || r.db == nil || !validVideoObjectObservationRef(direction, ref) || now.IsZero() {
		return ErrVideoObjectObservationInvalid
	}
	now = now.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current VideoObjectObservation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("direction=? AND bucket=? AND object_key=? AND status IN ('observing','confirmed')", direction, ref.Bucket, ref.ObjectKey).Order("id DESC").Take(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		taskType := "video_orphan_cleanup"
		if direction == VideoObjectDBMissing {
			taskType = "video_object_missing_reconcile"
		}
		var compensation struct{ Status string }
		taskKey := videoObjectObservationTaskKey(current.ID, direction)
		compensationErr := tx.Table("ai_compensation_tasks").Clauses(clause.Locking{Strength: "UPDATE"}).Select("status").Where("task_key=? AND task_type=?", taskKey, taskType).Take(&compensation).Error
		if compensationErr != nil && !errors.Is(compensationErr, gorm.ErrRecordNotFound) {
			return compensationErr
		}
		// 已领取任务必须由Worker在对象与引用临界区收口，扫描器不能越过租约覆盖终态。
		if compensationErr == nil && compensation.Status == "running" {
			return nil
		}
		updated := tx.Model(&VideoObjectObservation{}).Where("id=? AND version_no=? AND status=?", current.ID, current.VersionNo, current.Status).Updates(map[string]any{"status": "resolved", "resolved_at": now, "last_seen_at": now, "next_observe_at": now, "version_no": gorm.Expr("version_no+1"), "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrVideoObjectObservationConflict
		}
		if compensationErr == nil && (compensation.Status == "pending" || compensation.Status == "retry") {
			completed := tx.Table("ai_compensation_tasks").Where("task_key=? AND task_type=? AND status=?", taskKey, taskType, compensation.Status).Updates(map[string]any{"status": "completed", "completed_at": now, "next_retry_at": now, "locked_at": nil, "locked_by": nil, "last_error_class": nil, "updated_at": now})
			if completed.Error != nil {
				return completed.Error
			}
			if completed.RowsAffected != 1 {
				return ErrVideoObjectObservationConflict
			}
		}
		return nil
	})
}

// ClaimCleanup只领取已跨静默窗确认的MinIO无引用对象；DB缺失方向由独立恢复器处理，绝不删除对象。
func (r *VideoObjectReconciliationRepository) ClaimCleanup(ctx context.Context, worker string, now time.Time) (*VideoObjectCleanupLease, error) {
	if r == nil || r.db == nil || !videoWorkerLeaseOwner.MatchString(worker) || now.IsZero() {
		return nil, ErrVideoObjectObservationInvalid
	}
	now = now.UTC()
	var lease *VideoObjectCleanupLease
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct {
			ID          uint64
			AggregateID string
			Status      string
			LockedAt    *time.Time
		}
		err := tx.Table("ai_compensation_tasks").Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("task_type='video_orphan_cleanup' AND ((status IN ('pending','retry') AND next_retry_at<=?) OR (status='running' AND locked_at<?))", now, now.Add(-2*time.Minute)).Order("id").Take(&task).Error
		if err != nil {
			return err
		}
		observationID, err := strconv.ParseUint(task.AggregateID, 10, 64)
		if err != nil || observationID == 0 || strconv.FormatUint(observationID, 10) != task.AggregateID {
			return ErrVideoObjectObservationInvalid
		}
		var observation VideoObjectObservation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND direction=? AND status='confirmed'", observationID, VideoObjectUnreferenced).Take(&observation).Error; err != nil {
			return err
		}
		query := tx.Table("ai_compensation_tasks").Where("id=? AND task_type='video_orphan_cleanup' AND status=?", task.ID, task.Status)
		if task.LockedAt == nil {
			query = query.Where("locked_at IS NULL")
		} else {
			query = query.Where("locked_at=?", *task.LockedAt)
		}
		updated := query.Updates(map[string]any{"status": "running", "locked_at": now, "locked_by": worker, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrVideoObjectObservationConflict
		}
		lease = &VideoObjectCleanupLease{TaskID: task.ID, LockedAt: now, Observation: observation}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lease, nil
}

// ClaimMissing领取数据库有引用但MinIO对象缺失的恢复任务；该方向永远不能删除任何对象或业务事实。
func (r *VideoObjectReconciliationRepository) ClaimMissing(ctx context.Context, worker string, now time.Time) (*VideoObjectCleanupLease, error) {
	if r == nil || r.db == nil || !videoWorkerLeaseOwner.MatchString(worker) || now.IsZero() {
		return nil, ErrVideoObjectObservationInvalid
	}
	now = now.UTC()
	var lease *VideoObjectCleanupLease
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct {
			ID          uint64
			AggregateID string
			Status      string
			LockedAt    *time.Time
		}
		err := tx.Table("ai_compensation_tasks").Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("task_type='video_object_missing_reconcile' AND ((status IN ('pending','retry') AND next_retry_at<=?) OR (status='running' AND locked_at<?))", now, now.Add(-2*time.Minute)).Order("id").Take(&task).Error
		if err != nil {
			return err
		}
		observationID, err := strconv.ParseUint(task.AggregateID, 10, 64)
		if err != nil || observationID == 0 || strconv.FormatUint(observationID, 10) != task.AggregateID {
			return ErrVideoObjectObservationInvalid
		}
		var observation VideoObjectObservation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND direction=? AND status='confirmed'", observationID, VideoObjectDBMissing).Take(&observation).Error; err != nil {
			return err
		}
		query := tx.Table("ai_compensation_tasks").Where("id=? AND task_type='video_object_missing_reconcile' AND status=?", task.ID, task.Status)
		if task.LockedAt == nil {
			query = query.Where("locked_at IS NULL")
		} else {
			query = query.Where("locked_at=?", *task.LockedAt)
		}
		updated := query.Updates(map[string]any{"status": "running", "locked_at": now, "locked_by": worker, "updated_at": now})
		if updated.Error != nil || updated.RowsAffected != 1 {
			return errors.Join(updated.Error, ErrVideoObjectObservationConflict)
		}
		lease = &VideoObjectCleanupLease{TaskID: task.ID, LockedAt: now, Observation: observation}
		return nil
	})
	return lease, err
}

func (r *VideoObjectReconciliationRepository) CompleteMissing(ctx context.Context, lease *VideoObjectCleanupLease, now time.Time) error {
	if r == nil || r.db == nil || lease == nil || lease.TaskID == 0 || lease.Observation.ID == 0 || lease.LockedAt.IsZero() || now.IsZero() {
		return ErrVideoObjectObservationInvalid
	}
	now = now.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Model(&VideoObjectObservation{}).Where("id=? AND direction=? AND status='confirmed' AND version_no=?", lease.Observation.ID, VideoObjectDBMissing, lease.Observation.VersionNo).Updates(map[string]any{"status": "resolved", "resolved_at": now, "last_seen_at": now, "next_observe_at": now, "version_no": gorm.Expr("version_no+1"), "updated_at": now})
		if updated.Error != nil || updated.RowsAffected != 1 {
			return errors.Join(updated.Error, ErrVideoObjectObservationConflict)
		}
		updated = tx.Table("ai_compensation_tasks").Where("id=? AND task_type='video_object_missing_reconcile' AND status='running' AND locked_at=?", lease.TaskID, lease.LockedAt).Updates(map[string]any{"status": "completed", "completed_at": now, "next_retry_at": now, "locked_at": nil, "locked_by": nil, "last_error_class": nil, "updated_at": now})
		if updated.Error != nil || updated.RowsAffected != 1 {
			return errors.Join(updated.Error, ErrVideoObjectObservationConflict)
		}
		return nil
	})
}

func (r *VideoObjectReconciliationRepository) FailMissing(ctx context.Context, lease *VideoObjectCleanupLease, now time.Time, errorClass string, manual bool) error {
	if r == nil || r.db == nil || lease == nil || lease.TaskID == 0 || lease.LockedAt.IsZero() || now.IsZero() || errorClass == "" || len(errorClass) > 64 {
		return ErrVideoObjectObservationInvalid
	}
	now = now.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct{ RetryCount uint64 }
		if err := tx.Table("ai_compensation_tasks").Clauses(clause.Locking{Strength: "UPDATE"}).Select("retry_count").Where("id=? AND task_type='video_object_missing_reconcile' AND status='running' AND locked_at=?", lease.TaskID, lease.LockedAt).Take(&task).Error; err != nil {
			return err
		}
		nextCount := task.RetryCount + 1
		status, next := "retry", now.Add(videoObjectRetryDelay(nextCount))
		if manual {
			status, next = "manual_review", now
		} else if nextCount >= 9 {
			status, next = "dead", now
		}
		updated := tx.Table("ai_compensation_tasks").Where("id=? AND task_type='video_object_missing_reconcile' AND status='running' AND locked_at=? AND retry_count=?", lease.TaskID, lease.LockedAt, task.RetryCount).Updates(map[string]any{"status": status, "retry_count": nextCount, "next_retry_at": next, "locked_at": nil, "locked_by": nil, "last_error_class": errorClass, "updated_at": now})
		if updated.Error != nil || updated.RowsAffected != 1 {
			return errors.Join(updated.Error, ErrVideoObjectObservationConflict)
		}
		return nil
	})
}

func (r *VideoObjectReconciliationRepository) CompleteCleanup(ctx context.Context, lease *VideoObjectCleanupLease, now time.Time) error {
	if r == nil || r.db == nil || lease == nil || lease.TaskID == 0 || lease.Observation.ID == 0 || lease.LockedAt.IsZero() || now.IsZero() {
		return ErrVideoObjectObservationInvalid
	}
	now = now.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var observation VideoObjectObservation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND direction=? AND status='confirmed' AND version_no=?", lease.Observation.ID, VideoObjectUnreferenced, lease.Observation.VersionNo).Take(&observation).Error; err != nil {
			return err
		}
		updated := tx.Model(&VideoObjectObservation{}).Where("id=? AND status='confirmed' AND version_no=?", observation.ID, observation.VersionNo).Updates(map[string]any{"status": "resolved", "resolved_at": now, "last_seen_at": now, "next_observe_at": now, "version_no": gorm.Expr("version_no+1"), "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrVideoObjectObservationConflict
		}
		updated = tx.Table("ai_compensation_tasks").Where("id=? AND task_type='video_orphan_cleanup' AND status='running' AND locked_at=?", lease.TaskID, lease.LockedAt).Updates(map[string]any{"status": "completed", "locked_at": nil, "locked_by": nil, "completed_at": now, "last_error_class": nil, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrVideoObjectObservationConflict
		}
		return nil
	})
}

func (r *VideoObjectReconciliationRepository) FailCleanup(ctx context.Context, lease *VideoObjectCleanupLease, now time.Time, errorClass string, manual bool) error {
	if r == nil || r.db == nil || lease == nil || lease.TaskID == 0 || lease.LockedAt.IsZero() || now.IsZero() || errorClass == "" || len(errorClass) > 64 {
		return ErrVideoObjectObservationInvalid
	}
	now = now.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct{ RetryCount uint64 }
		if err := tx.Table("ai_compensation_tasks").Clauses(clause.Locking{Strength: "UPDATE"}).Select("retry_count").Where("id=? AND task_type='video_orphan_cleanup' AND status='running' AND locked_at=?", lease.TaskID, lease.LockedAt).Take(&task).Error; err != nil {
			return err
		}
		nextCount := task.RetryCount + 1
		status, next := "retry", now.Add(videoObjectRetryDelay(nextCount))
		if manual {
			status, next = "manual_review", now
		} else if nextCount >= 9 {
			status, next = "dead", now
		}
		updated := tx.Table("ai_compensation_tasks").Where("id=? AND task_type='video_orphan_cleanup' AND status='running' AND locked_at=? AND retry_count=?", lease.TaskID, lease.LockedAt, task.RetryCount).Updates(map[string]any{"status": status, "retry_count": nextCount, "next_retry_at": next, "locked_at": nil, "locked_by": nil, "last_error_class": errorClass, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrVideoObjectObservationConflict
		}
		return nil
	})
}

func videoObjectRetryDelay(attempt uint64) time.Duration {
	if attempt > 7 {
		attempt = 7
	}
	return time.Minute * time.Duration(uint64(1)<<(attempt-1))
}

func validVideoObjectObservation(direction string, ref video.VideoObjectRef, digest string, size uint64) bool {
	return validVideoObjectObservationRef(direction, ref) && lowerVideoObservationHex(digest) && size > 0
}

func validVideoObjectObservationRef(direction string, ref video.VideoObjectRef) bool {
	if direction != VideoObjectDBMissing && direction != VideoObjectUnreferenced {
		return false
	}
	if ref.ObjectKey == "" || len(ref.ObjectKey) > 191 || strings.ContainsAny(ref.ObjectKey, "\x00\r\n\\") {
		return false
	}
	switch ref.Bucket {
	case "ai-upload-temp":
		return strings.HasPrefix(ref.ObjectKey, "vid_") || strings.HasPrefix(ref.ObjectKey, "video_") || strings.HasPrefix(ref.ObjectKey, "original/") || strings.HasPrefix(ref.ObjectKey, "inline/")
	case "ai-result":
		return strings.HasPrefix(ref.ObjectKey, "vid_") || strings.HasPrefix(ref.ObjectKey, "video_") || strings.HasPrefix(ref.ObjectKey, "normalized/")
	case "ai-quarantine":
		return strings.HasPrefix(ref.ObjectKey, "vid_") || strings.HasPrefix(ref.ObjectKey, "video_")
	case "ai-user-assets":
		return strings.HasPrefix(ref.ObjectKey, "vsave_")
	default:
		return false
	}
}

func lowerVideoObservationHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func videoObjectObservationTaskKey(id uint64, direction string) string {
	digest := sha256.Sum256([]byte("video-object-observation\x00" + strconv.FormatUint(id, 10) + "\x00" + direction))
	return "video-object-observation:" + hex.EncodeToString(digest[:])
}
