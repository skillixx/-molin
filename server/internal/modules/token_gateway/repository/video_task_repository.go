package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrVideoTaskNotFound       = errors.New("视频任务不存在")
	ErrVideoTaskConflict       = errors.New("视频任务状态已变化")
	ErrVideoTaskTransition     = errors.New("视频任务状态流转不允许")
	ErrVideoBillingTransition  = errors.New("视频计费状态流转不允许")
	ErrVideoDeliveryTransition = errors.New("视频交付状态流转不允许")
	ErrVideoUnsafeDetail       = errors.New("视频事件包含敏感字段")
)

// VideoOwner 是视频任务、输入和产物统一使用的横向归属边界。
type VideoOwner struct {
	UserID    uint64
	ProjectID uint64
	APIKeyID  *uint64
}

// VideoTaskRecord 合并共享任务事实和请求三轴状态，不复制报价或财务账本。
type VideoTaskRecord struct {
	model.AIImageTask
	RequestExecutionStatus string `gorm:"column:request_execution_status"`
	BillingStatus          string `gorm:"column:billing_status"`
	DeliveryStatus         string `gorm:"column:delivery_status"`
	RequestVersionNo       uint64 `gorm:"column:request_version_no"`
}

// VideoStateTransition 描述一次带追加式事件的状态迁移命令。
type VideoStateTransition struct {
	TaskPublicID    string
	Owner           VideoOwner
	ExpectedVersion uint64
	ToStatus        string
	Progress        uint8
	EventID         string
	Source          string
	SafeDetailJSON  json.RawMessage
	Now             time.Time
}

// VideoTaskRepository 在共享ai_gateway_tasks和ai_requests上实现视频三轴CAS。
type VideoTaskRepository struct{ db *gorm.DB }

func NewVideoTaskRepository(db *gorm.DB) *VideoTaskRepository { return &VideoTaskRepository{db: db} }

// FindForOwner 对任一归属维度不匹配均返回相同不存在语义，避免泄露任务是否存在。
func (r *VideoTaskRepository) FindForOwner(ctx context.Context, publicID string, owner VideoOwner) (*VideoTaskRecord, error) {
	if r == nil || r.db == nil || !validVideoOwner(owner) || strings.TrimSpace(publicID) == "" {
		return nil, ErrVideoTaskNotFound
	}
	return findVideoTaskRecord(r.db.WithContext(ctx), publicID, owner, false)
}

// TransitionExecution 以任务version_no执行CAS，并在同一事务追加不可变TaskEvent。
func (r *VideoTaskRepository) TransitionExecution(ctx context.Context, command VideoStateTransition) (*VideoTaskRecord, error) {
	if err := validateVideoTransitionCommand(command); err != nil {
		return nil, err
	}
	var updated *VideoTaskRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := findVideoTaskRecord(tx, command.TaskPublicID, command.Owner, true)
		if err != nil {
			return err
		}
		if record.VersionNo != command.ExpectedVersion {
			return ErrVideoTaskConflict
		}
		if !videoExecutionTransitionAllowed(record.Status, command.ToStatus) || command.Progress < record.Progress || command.Progress > 100 {
			return ErrVideoTaskTransition
		}
		updates := map[string]interface{}{
			"status": command.ToStatus, "progress": command.Progress,
			"version_no": gorm.Expr("version_no + 1"), "updated_at": command.Now,
		}
		if videoExecutionTerminal(command.ToStatus) {
			updates["completed_at"] = command.Now
		}
		result := tx.Model(&model.AIImageTask{}).
			Where("id=? AND user_id=? AND project_id=? AND status=? AND version_no=?", record.ID, command.Owner.UserID, command.Owner.ProjectID, record.Status, command.ExpectedVersion).
			Where("capability=? AND operation IN ?", model.AIVideoCapability, []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo}).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrVideoTaskConflict
		}
		// 请求账本只保存兼容的粗粒度执行态；细粒度执行权威仍是共享Task状态。
		if err := tx.Model(&model.AIRequest{}).Where("request_id=? AND user_id=? AND project_id=?", record.RequestID, command.Owner.UserID, command.Owner.ProjectID).
			Updates(map[string]interface{}{"execution_status": videoRequestExecutionStatus(command.ToStatus), "version_no": gorm.Expr("version_no + 1"), "updated_at": command.Now}).Error; err != nil {
			return err
		}
		if err := appendVideoTaskEventTx(tx, record.ID, command.Owner, command.EventID, "execution_status_changed", record.Status, command.ToStatus, command.Source, command.SafeDetailJSON, command.Now); err != nil {
			return err
		}
		updated, err = findVideoTaskRecord(tx, command.TaskPublicID, command.Owner, false)
		return err
	})
	return updated, err
}

// TransitionBilling 只推进请求计费轴，不修改任务执行状态或交付状态。
func (r *VideoTaskRepository) TransitionBilling(ctx context.Context, command VideoStateTransition) (*VideoTaskRecord, error) {
	if err := validateVideoTransitionCommand(command); err != nil {
		return nil, err
	}
	var updated *VideoTaskRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := findVideoTaskRecord(tx, command.TaskPublicID, command.Owner, true)
		if err != nil {
			return err
		}
		if record.RequestVersionNo != command.ExpectedVersion {
			return ErrVideoTaskConflict
		}
		if !videoBillingTransitionAllowed(record.BillingStatus, command.ToStatus) {
			return ErrVideoBillingTransition
		}
		result := tx.Model(&model.AIRequest{}).
			Where("request_id=? AND user_id=? AND project_id=? AND billing_status=? AND version_no=?", record.RequestID, command.Owner.UserID, command.Owner.ProjectID, record.BillingStatus, command.ExpectedVersion).
			Where("modality='video' AND capability=?", model.AIVideoCapability).
			Updates(map[string]interface{}{"billing_status": command.ToStatus, "version_no": gorm.Expr("version_no + 1"), "updated_at": command.Now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrVideoTaskConflict
		}
		if err := appendVideoTaskEventTx(tx, record.ID, command.Owner, command.EventID, "billing_status_changed", record.BillingStatus, command.ToStatus, command.Source, command.SafeDetailJSON, command.Now); err != nil {
			return err
		}
		updated, err = findVideoTaskRecord(tx, command.TaskPublicID, command.Owner, false)
		return err
	})
	return updated, err
}

// TransitionDelivery 只允许任务安全终结后选择一次交付终态；pending_reconcile永远不能交付。
func (r *VideoTaskRepository) TransitionDelivery(ctx context.Context, command VideoStateTransition) (*VideoTaskRecord, error) {
	if err := validateVideoTransitionCommand(command); err != nil {
		return nil, err
	}
	var updated *VideoTaskRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := findVideoTaskRecord(tx, command.TaskPublicID, command.Owner, true)
		if err != nil {
			return err
		}
		if record.RequestVersionNo != command.ExpectedVersion {
			return ErrVideoTaskConflict
		}
		if !videoDeliveryTransitionAllowed(record.DeliveryStatus, command.ToStatus) || !videoExecutionTerminal(record.Status) {
			return ErrVideoDeliveryTransition
		}
		if command.ToStatus == model.AIDeliveryAvailable && (record.Status != model.AIImageTaskSucceeded || (record.BillingStatus != model.AIBillingSettled && record.BillingStatus != model.AIBillingAdjusted)) {
			return ErrVideoDeliveryTransition
		}
		result := tx.Model(&model.AIRequest{}).
			Where("request_id=? AND user_id=? AND project_id=? AND delivery_status=? AND version_no=?", record.RequestID, command.Owner.UserID, command.Owner.ProjectID, record.DeliveryStatus, command.ExpectedVersion).
			Where("modality='video' AND capability=?", model.AIVideoCapability).
			Updates(map[string]interface{}{"delivery_status": command.ToStatus, "version_no": gorm.Expr("version_no + 1"), "updated_at": command.Now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrVideoTaskConflict
		}
		if err := appendVideoTaskEventTx(tx, record.ID, command.Owner, command.EventID, "delivery_status_changed", record.DeliveryStatus, command.ToStatus, command.Source, command.SafeDetailJSON, command.Now); err != nil {
			return err
		}
		updated, err = findVideoTaskRecord(tx, command.TaskPublicID, command.Owner, false)
		return err
	})
	return updated, err
}

func findVideoTaskRecord(db *gorm.DB, publicID string, owner VideoOwner, forUpdate bool) (*VideoTaskRecord, error) {
	query := db.Table("ai_gateway_tasks AS tasks").
		Select(`tasks.*, requests.execution_status AS request_execution_status, requests.billing_status,
requests.delivery_status, requests.version_no AS request_version_no`).
		Joins("JOIN ai_requests AS requests ON requests.request_id=tasks.request_id AND requests.user_id=tasks.user_id AND requests.project_id=tasks.project_id").
		Where("tasks.public_id=? AND tasks.user_id=? AND tasks.project_id=?", strings.TrimSpace(publicID), owner.UserID, owner.ProjectID).
		Where("tasks.capability=? AND tasks.operation IN ?", model.AIVideoCapability, []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo}).
		Where("requests.modality='video' AND requests.capability=?", model.AIVideoCapability)
	if owner.APIKeyID == nil {
		query = query.Where("tasks.api_key_id IS NULL AND requests.api_key_id IS NULL")
	} else {
		query = query.Where("tasks.api_key_id=? AND requests.api_key_id=?", *owner.APIKeyID, *owner.APIKeyID)
	}
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var record VideoTaskRecord
	if err := query.First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoTaskNotFound
		}
		return nil, err
	}
	return &record, nil
}

func validateVideoTransitionCommand(command VideoStateTransition) error {
	if !validVideoOwner(command.Owner) || strings.TrimSpace(command.TaskPublicID) == "" || command.ExpectedVersion == 0 || strings.TrimSpace(command.ToStatus) == "" || strings.TrimSpace(command.EventID) == "" || command.Now.IsZero() {
		return ErrVideoTaskTransition
	}
	if command.Source != "api" && command.Source != "worker" && command.Source != "provider_callback" && command.Source != "reconciler" && command.Source != "system" {
		return ErrVideoTaskTransition
	}
	return validateVideoSafeJSON(command.SafeDetailJSON)
}

func validateVideoSafeJSON(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var value map[string]interface{}
	if err := json.Unmarshal(raw, &value); err != nil || len(value) > 4 {
		return ErrVideoUnsafeDetail
	}
	allowedReasons := map[string]bool{
		"cas_test": true, "state_advanced": true, "signature_invalid": true,
		"task_not_found": true, "out_of_order_or_terminal": true, "cas_conflict": true,
	}
	allowedStatuses := map[string]bool{
		model.AIImageTaskCreated: true, model.AIImageTaskReserved: true, model.AIImageTaskQueued: true,
		model.AIImageTaskSubmitting: true, model.AIImageTaskSubmitted: true, model.AIImageTaskProcessing: true,
		model.AIImageTaskFetching: true, model.AIImageTaskStoring: true, model.AIImageTaskModerating: true,
		model.AIImageTaskLabeling: true, model.AIImageTaskSucceeded: true, model.AIImageTaskFailed: true,
		model.AIImageTaskCancelled: true, model.AIImageTaskExpired: true, model.AIImageTaskPendingReconcile: true,
	}
	allowedResults := map[string]bool{"success": true, "applied": true, "ignored": true, "failed": true}
	for key, rawValue := range value {
		switch key {
		case "reason":
			item, ok := rawValue.(string)
			if !ok || !allowedReasons[item] {
				return ErrVideoUnsafeDetail
			}
		case "status":
			item, ok := rawValue.(string)
			if !ok || !allowedStatuses[item] {
				return ErrVideoUnsafeDetail
			}
		case "result":
			item, ok := rawValue.(string)
			if !ok || !allowedResults[item] {
				return ErrVideoUnsafeDetail
			}
		case "attempt":
			item, ok := rawValue.(float64)
			if !ok || item < 0 || item > 100 || item != float64(uint64(item)) {
				return ErrVideoUnsafeDetail
			}
		default:
			return ErrVideoUnsafeDetail
		}
	}
	return nil
}

func appendVideoTaskEventTx(tx *gorm.DB, taskID uint64, owner VideoOwner, eventID, eventType, from, to, source string, detail json.RawMessage, now time.Time) error {
	fromCopy, toCopy := from, to
	event := model.AIGatewayTaskEvent{
		EventID: strings.TrimSpace(eventID), TaskID: taskID, UserID: owner.UserID, ProjectID: owner.ProjectID,
		EventType: eventType, FromStatus: &fromCopy, ToStatus: &toCopy, Source: source,
		SafeDetailJSON: detail, CreatedAt: now,
	}
	if err := tx.Create(&event).Error; err != nil {
		return fmt.Errorf("追加视频任务事件失败: %w", err)
	}
	return nil
}

func videoExecutionTerminal(status string) bool {
	return status == model.AIImageTaskSucceeded || status == model.AIImageTaskFailed || status == model.AIImageTaskCancelled || status == model.AIImageTaskExpired
}

func videoRequestExecutionStatus(taskStatus string) string {
	switch taskStatus {
	case model.AIImageTaskSucceeded:
		return model.AIExecutionSucceeded
	case model.AIImageTaskFailed, model.AIImageTaskExpired:
		return model.AIExecutionFailed
	case model.AIImageTaskCancelled:
		return model.AIExecutionCancelled
	case model.AIImageTaskPendingReconcile:
		return model.AIExecutionUnknown
	default:
		return model.AIExecutionRunning
	}
}

func validVideoOwner(owner VideoOwner) bool { return owner.UserID != 0 && owner.ProjectID != 0 }
