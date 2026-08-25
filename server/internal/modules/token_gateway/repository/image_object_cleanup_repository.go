package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
)

const (
	imageObjectCleanupTaskType      = "image_object_cleanup"
	imageObjectCleanupTaskKeyPrefix = "image-object-cleanup:"
)

var (
	ErrImageObjectCleanupInvalid   = errors.New("图片对象回收任务无效")
	ErrImageObjectCleanupLeaseLost = errors.New("图片对象回收任务租约已失效")

	imageCleanupRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	imageCleanupObjectKeyPattern = regexp.MustCompile(`^([0-9a-f]{32})/(0|[1-9][0-9]{0,19})/(primary|thumbnail)\.png$`)
	imageCleanupAggregatePattern = regexp.MustCompile(`^(temp|result|quarantine):([0-9a-f]{32}):(0|[1-9][0-9]{0,19}):(primary|thumbnail)$`)
)

type ImageObjectCleanupRepository struct {
	db  *gorm.DB
	now func() time.Time
}

func NewImageObjectCleanupRepository(db *gorm.DB) *ImageObjectCleanupRepository {
	return &ImageObjectCleanupRepository{db: db, now: time.Now}
}

// RecordObjectCleanup 仅接受网关可生成的固定对象路径，并在返回nil前把幂等补偿任务写入MySQL。
func (r *ImageObjectCleanupRepository) RecordObjectCleanup(ctx context.Context, task imagegateway.ObjectCleanupTask) error {
	if r == nil || r.db == nil {
		return ErrImageObjectCleanupInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := buildImageObjectCleanupModel(task, r.now().UTC())
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "task_key"}}, DoNothing: true,
	}).Create(&record).Error
}

func buildImageObjectCleanupModel(task imagegateway.ObjectCleanupTask, now time.Time) (model.AICompensationTask, error) {
	if !imageCleanupRequestIDPattern.MatchString(task.RequestID) {
		return model.AICompensationTask{}, ErrImageObjectCleanupInvalid
	}
	matches := imageCleanupObjectKeyPattern.FindStringSubmatch(task.Ref.Key)
	if len(matches) != 4 {
		return model.AICompensationTask{}, ErrImageObjectCleanupInvalid
	}
	index, err := strconv.ParseUint(matches[2], 10, 64)
	if err != nil || strconv.FormatUint(index, 10) != matches[2] {
		return model.AICompensationTask{}, ErrImageObjectCleanupInvalid
	}
	requestHash := sha256.Sum256([]byte(task.RequestID))
	wantNamespace := hex.EncodeToString(requestHash[:16])
	if matches[1] != wantNamespace {
		return model.AICompensationTask{}, ErrImageObjectCleanupInvalid
	}
	bucketType, allowed := cleanupBucketType(task.Ref.Bucket, matches[3], task.Reason)
	if !allowed {
		return model.AICompensationTask{}, ErrImageObjectCleanupInvalid
	}
	taskKey, err := ImageObjectCleanupTaskKey(task.Ref)
	if err != nil {
		return model.AICompensationTask{}, err
	}
	reason := string(task.Reason)
	return model.AICompensationTask{
		TaskKey: taskKey, TaskType: imageObjectCleanupTaskType,
		AggregateID: fmt.Sprintf("%s:%s:%d:%s", bucketType, wantNamespace, index, matches[3]),
		Status:      "pending", NextRetryAt: now.Add(time.Minute), LastErrorClass: &reason,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

// cleanupBucketType 对原因和bucket做笛卡尔白名单，避免新增原因意外扩大可删除对象范围。
func cleanupBucketType(bucket, role string, reason imagegateway.ObjectCleanupReason) (string, bool) {
	switch reason {
	case imagegateway.ObjectCleanupAfterTempPutUnknown:
		return "temp", bucket == imagegateway.TemporaryObjectBucket && role == "primary"
	case imagegateway.ObjectCleanupAfterQuarantinePutUnknown:
		return "quarantine", bucket == imagegateway.QuarantineObjectBucket && role == "primary"
	case imagegateway.ObjectCleanupAfterResultPutUnknown:
		return "result", bucket == imagegateway.ResultObjectBucket && role == "primary"
	case imagegateway.ObjectCleanupAfterThumbnailPutUnknown:
		return "result", bucket == imagegateway.ResultObjectBucket && role == "thumbnail"
	}
	if reason == imagegateway.ObjectCleanupAfterMetadataPersistFailure {
		switch {
		case bucket == imagegateway.ResultObjectBucket && (role == "primary" || role == "thumbnail"):
			return "result", true
		case bucket == imagegateway.QuarantineObjectBucket && role == "primary":
			return "quarantine", true
		default:
			return "", false
		}
	}
	if role != "primary" || bucket != imagegateway.TemporaryObjectBucket || !temporaryCleanupReason(reason) {
		return "", false
	}
	return "temp", true
}

func temporaryCleanupReason(reason imagegateway.ObjectCleanupReason) bool {
	switch reason {
	case imagegateway.ObjectCleanupAfterModerationFailure,
		imagegateway.ObjectCleanupAfterQuarantineStoreFailure,
		imagegateway.ObjectCleanupAfterResultStoreFailure,
		imagegateway.ObjectCleanupAfterQuarantineStored,
		imagegateway.ObjectCleanupAfterResultStored:
		return true
	default:
		return false
	}
}

// ImageObjectCleanupTaskKey 为受控对象生成跨Gateway、Billing与Worker一致的tombstone键。
func ImageObjectCleanupTaskKey(ref imagegateway.ObjectRef) (string, error) {
	matches := imageCleanupObjectKeyPattern.FindStringSubmatch(ref.Key)
	if len(matches) != 4 {
		return "", ErrImageObjectCleanupInvalid
	}
	switch {
	case ref.Bucket == imagegateway.TemporaryObjectBucket && matches[3] == "primary":
	case ref.Bucket == imagegateway.ResultObjectBucket && (matches[3] == "primary" || matches[3] == "thumbnail"):
	case ref.Bucket == imagegateway.QuarantineObjectBucket && matches[3] == "primary":
	default:
		return "", ErrImageObjectCleanupInvalid
	}
	digest := sha256.Sum256([]byte(ref.Bucket + "\x00" + ref.Key))
	return imageObjectCleanupTaskKeyPrefix + hex.EncodeToString(digest[:]), nil
}

func resolveImageObjectCleanupRef(task model.AICompensationTask) (imagegateway.ObjectRef, error) {
	if task.TaskType != imageObjectCleanupTaskType {
		return imagegateway.ObjectRef{}, ErrImageObjectCleanupInvalid
	}
	matches := imageCleanupAggregatePattern.FindStringSubmatch(task.AggregateID)
	if len(matches) != 5 {
		return imagegateway.ObjectRef{}, ErrImageObjectCleanupInvalid
	}
	index, err := strconv.ParseUint(matches[3], 10, 64)
	if err != nil || strconv.FormatUint(index, 10) != matches[3] {
		return imagegateway.ObjectRef{}, ErrImageObjectCleanupInvalid
	}
	var bucket string
	switch {
	case matches[1] == "temp" && matches[4] == "primary":
		bucket = imagegateway.TemporaryObjectBucket
	case matches[1] == "result" && (matches[4] == "primary" || matches[4] == "thumbnail"):
		bucket = imagegateway.ResultObjectBucket
	case matches[1] == "quarantine" && matches[4] == "primary":
		bucket = imagegateway.QuarantineObjectBucket
	default:
		return imagegateway.ObjectRef{}, ErrImageObjectCleanupInvalid
	}
	ref := imagegateway.ObjectRef{Bucket: bucket, Key: fmt.Sprintf("%s/%d/%s.png", matches[2], index, matches[4])}
	wantTaskKey, err := ImageObjectCleanupTaskKey(ref)
	if err != nil || !strings.EqualFold(wantTaskKey, task.TaskKey) || task.TaskKey != strings.ToLower(task.TaskKey) {
		return imagegateway.ObjectRef{}, ErrImageObjectCleanupInvalid
	}
	return ref, nil
}

func (r *ImageObjectCleanupRepository) ResolveObjectRef(task model.AICompensationTask) (imagegateway.ObjectRef, error) {
	return resolveImageObjectCleanupRef(task)
}

// HasAssetReference 在删除前查询任何资产元数据引用；查询未知必须由Worker失败关闭，不能继续删对象。
func (r *ImageObjectCleanupRepository) HasAssetReference(ctx context.Context, ref imagegateway.ObjectRef) (bool, error) {
	if r == nil || r.db == nil || ref.Bucket == "" || ref.Key == "" {
		return false, ErrImageObjectCleanupInvalid
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("bucket = ? AND object_key = ?", ref.Bucket, ref.Key).Limit(1).Count(&count).Error
	return count > 0, err
}

// ClaimBatch 使用行锁和旧租约值共同构成CAS；并发Worker不能同时取得同一删除任务。
func (r *ImageObjectCleanupRepository) ClaimBatch(ctx context.Context, now, staleBefore time.Time, limit int) ([]model.AICompensationTask, error) {
	if r == nil || r.db == nil {
		return nil, ErrImageObjectCleanupInvalid
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	now = now.UTC().Truncate(time.Second)
	staleBefore = staleBefore.UTC().Truncate(time.Second)
	var tasks []model.AICompensationTask
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("task_type = ?", imageObjectCleanupTaskType).
			Where("((status IN ('pending','retry') AND next_retry_at <= ?) OR (status = 'running' AND locked_at < ?))", now, staleBefore).
			Order("id ASC").Limit(limit).Find(&tasks).Error; err != nil {
			return err
		}
		for index := range tasks {
			query := tx.Model(&model.AICompensationTask{}).
				Where("id = ? AND task_type = ? AND status = ?", tasks[index].ID, imageObjectCleanupTaskType, tasks[index].Status)
			if tasks[index].LockedAt == nil {
				query = query.Where("locked_at IS NULL")
			} else {
				query = query.Where("locked_at = ?", *tasks[index].LockedAt)
			}
			result := query.Updates(map[string]interface{}{"status": "running", "locked_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrImageObjectCleanupLeaseLost
			}
			tasks[index].Status = "running"
			tasks[index].LockedAt = &now
		}
		return nil
	})
	return tasks, err
}

func (r *ImageObjectCleanupRepository) MarkCompleted(ctx context.Context, id uint64, lease, now time.Time) error {
	if r == nil || r.db == nil || id == 0 || lease.IsZero() {
		return ErrImageObjectCleanupLeaseLost
	}
	result := r.db.WithContext(ctx).Model(&model.AICompensationTask{}).
		Where("id = ? AND task_type = ? AND status = 'running' AND locked_at = ?", id, imageObjectCleanupTaskType, lease).
		Updates(map[string]interface{}{"status": "completed", "locked_at": nil, "next_retry_at": now.UTC(), "last_error_class": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrImageObjectCleanupLeaseLost
	}
	return nil
}

// RescheduleQuiescence 延长Put未知观察窗但不增加失败次数，避免正常静默期把任务误推入dead。
func (r *ImageObjectCleanupRepository) RescheduleQuiescence(ctx context.Context, id uint64, lease, next time.Time) error {
	if r == nil || r.db == nil || id == 0 || lease.IsZero() {
		return ErrImageObjectCleanupLeaseLost
	}
	result := r.db.WithContext(ctx).Model(&model.AICompensationTask{}).
		Where("id = ? AND task_type = ? AND status = 'running' AND locked_at = ?", id, imageObjectCleanupTaskType, lease).
		Updates(map[string]interface{}{"status": "retry", "locked_at": nil, "next_retry_at": next.UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrImageObjectCleanupLeaseLost
	}
	return nil
}

func (r *ImageObjectCleanupRepository) MarkFailure(ctx context.Context, id uint64, lease, next time.Time, errorClass string) error {
	if r == nil || r.db == nil || id == 0 || lease.IsZero() || errorClass == "" || len(errorClass) > 64 {
		return ErrImageObjectCleanupLeaseLost
	}
	// MySQL按SET顺序求值：先递增次数，再让状态读取新次数，确保第8次失败进入dead。
	result := r.db.WithContext(ctx).Exec(`UPDATE ai_compensation_tasks
SET retry_count = retry_count + 1,
    status = IF(retry_count >= 8, 'dead', 'retry'),
    next_retry_at = ?, locked_at = NULL, last_error_class = ?
WHERE id = ? AND task_type = 'image_object_cleanup' AND status = 'running' AND locked_at = ?`, next.UTC(), errorClass, id, lease)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrImageObjectCleanupLeaseLost
	}
	return nil
}

var _ imagegateway.ObjectCleanupRecorder = (*ImageObjectCleanupRepository)(nil)
