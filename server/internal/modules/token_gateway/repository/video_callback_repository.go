package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrVideoCallbackInvalid      = errors.New("Provider回调参数无效")
	ErrVideoCallbackBodyConflict = errors.New("同一Provider事件正文不一致")
)

// VideoProviderCallbackCommand 只在内存中短暂携带原始正文以计算SHA-256，持久化模型没有原文列。
type VideoProviderCallbackCommand struct {
	ProviderCode    string
	ProviderTaskID  string
	ExternalEventID string
	RawBody         []byte
	SignatureStatus string
	ToStatus        string
	EventID         string
	SafeResultJSON  json.RawMessage
	ReceivedAt      time.Time
}

// VideoProviderCallbackOutcome 描述回调是否重放、是否安全应用及其低敏事实。
type VideoProviderCallbackOutcome struct {
	Event    *model.AIGatewayProviderCallbackEvent
	Replayed bool
	Applied  bool
}

// VideoProviderCallbackEventRepository 在共享回调表中完成去重、验签结论和状态应用。
type VideoProviderCallbackEventRepository struct{ db *gorm.DB }

func NewVideoProviderCallbackEventRepository(db *gorm.DB) *VideoProviderCallbackEventRepository {
	return &VideoProviderCallbackEventRepository{db: db}
}

// RecordAndApply 对三元唯一键执行一次性写入；同event_id同正文返回幂等ACK，不同正文失败关闭。
func (r *VideoProviderCallbackEventRepository) RecordAndApply(ctx context.Context, command VideoProviderCallbackCommand) (*VideoProviderCallbackOutcome, error) {
	if r == nil || r.db == nil || !validVideoCallbackCommand(command) {
		return nil, ErrVideoCallbackInvalid
	}
	bodyHash := videoCallbackSHA256(command.RawBody)
	var outcome *VideoProviderCallbackOutcome
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existing, found, err := findVideoCallbackByIdentity(tx, command)
		if err != nil {
			return err
		}
		if found {
			if existing.BodySHA256 != bodyHash {
				return ErrVideoCallbackBodyConflict
			}
			outcome = &VideoProviderCallbackOutcome{Event: existing, Replayed: true, Applied: existing.ProcessStatus == model.AIProviderCallbackApplied}
			return nil
		}

		task, taskFound, err := findVideoTaskByProviderRef(tx, command.ProviderCode, command.ProviderTaskID)
		if err != nil {
			return err
		}
		event := &model.AIGatewayProviderCallbackEvent{
			ProviderCode: command.ProviderCode, ProviderTaskID: command.ProviderTaskID,
			ExternalEventID: command.ExternalEventID, BodySHA256: bodyHash,
			SignatureStatus: command.SignatureStatus, ProcessStatus: model.AIProviderCallbackReceived, ReceivedAt: command.ReceivedAt,
		}
		if taskFound {
			event.TaskID, event.UserID, event.ProjectID = &task.ID, &task.UserID, &task.ProjectID
		}
		if err := tx.Create(event).Error; err != nil {
			if !isVideoCallbackDuplicateError(err) {
				return err
			}
			// 唯一键竞争的输家重新读取赢家；同正文幂等，不同正文关闭。
			winner, winnerFound, loadErr := findVideoCallbackByIdentity(tx, command)
			if loadErr != nil {
				return loadErr
			}
			if !winnerFound || winner.BodySHA256 != bodyHash {
				return ErrVideoCallbackBodyConflict
			}
			outcome = &VideoProviderCallbackOutcome{Event: winner, Replayed: true, Applied: winner.ProcessStatus == model.AIProviderCallbackApplied}
			return nil
		}

		processStatus, applied := model.AIProviderCallbackIgnored, false
		result := map[string]interface{}{"result": "ignored"}
		if command.SignatureStatus != model.AIProviderCallbackSignatureValid {
			processStatus = model.AIProviderCallbackFailed
			result["reason"] = "signature_invalid"
		} else if !taskFound {
			result["reason"] = "task_not_found"
		} else if !videoExecutionTransitionAllowed(task.Status, command.ToStatus) {
			result["reason"] = "out_of_order_or_terminal"
		} else {
			update := tx.Model(&model.AIImageTask{}).
				Where("id=? AND capability=? AND status=? AND version_no=?", task.ID, model.AIVideoCapability, task.Status, task.VersionNo).
				Updates(map[string]interface{}{
					"status": command.ToStatus, "version_no": gorm.Expr("version_no + 1"),
					"completed_at": callbackCompletedAt(command.ToStatus, command.ReceivedAt), "updated_at": command.ReceivedAt,
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				result["reason"] = "cas_conflict"
			} else {
				if err := tx.Model(&model.AIRequest{}).
					Where("request_id=? AND user_id=? AND project_id=? AND modality='video' AND capability=?", task.RequestID, task.UserID, task.ProjectID, model.AIVideoCapability).
					Updates(map[string]interface{}{
						"execution_status": videoRequestExecutionStatus(command.ToStatus),
						"version_no":       gorm.Expr("version_no + 1"),
						"updated_at":       command.ReceivedAt,
					}).Error; err != nil {
					return err
				}
				owner := VideoOwner{UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID}
				if err := appendVideoTaskEventTx(tx, task.ID, owner, command.EventID, "provider_callback_status_changed", task.Status, command.ToStatus, "provider_callback", command.SafeResultJSON, command.ReceivedAt); err != nil {
					return err
				}
				processStatus, applied = model.AIProviderCallbackApplied, true
				result["result"] = "applied"
			}
		}
		applicationResult, err := json.Marshal(result)
		if err != nil {
			return err
		}
		processedAt := command.ReceivedAt
		if err := tx.Model(&model.AIGatewayProviderCallbackEvent{}).Where("id=? AND process_status=?", event.ID, model.AIProviderCallbackReceived).
			Updates(map[string]interface{}{"process_status": processStatus, "application_result_json": applicationResult, "processed_at": processedAt}).Error; err != nil {
			return err
		}
		event.ProcessStatus, event.ApplicationResultJSON, event.ProcessedAt = processStatus, applicationResult, &processedAt
		outcome = &VideoProviderCallbackOutcome{Event: event, Applied: applied}
		return nil
	})
	return outcome, err
}

func findVideoCallbackByIdentity(tx *gorm.DB, command VideoProviderCallbackCommand) (*model.AIGatewayProviderCallbackEvent, bool, error) {
	var event model.AIGatewayProviderCallbackEvent
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("provider_code=? AND provider_task_id=? AND external_event_id=?", command.ProviderCode, command.ProviderTaskID, command.ExternalEventID).First(&event).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	return &event, err == nil, err
}

func findVideoTaskByProviderRef(tx *gorm.DB, providerCode, providerTaskID string) (*model.AIImageTask, bool, error) {
	var task model.AIImageTask
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("provider_code=? AND provider_task_id=? AND capability=? AND operation IN ?", providerCode, providerTaskID, model.AIVideoCapability, []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo}).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	return &task, err == nil, err
}

func validVideoCallbackCommand(command VideoProviderCallbackCommand) bool {
	if strings.TrimSpace(command.ProviderCode) == "" || strings.TrimSpace(command.ProviderTaskID) == "" || strings.TrimSpace(command.ExternalEventID) == "" || len(command.RawBody) == 0 || command.ReceivedAt.IsZero() {
		return false
	}
	if command.SignatureStatus != model.AIProviderCallbackSignatureValid && command.SignatureStatus != model.AIProviderCallbackSignatureInvalid && command.SignatureStatus != model.AIProviderCallbackSignatureUnverified {
		return false
	}
	if command.SignatureStatus == model.AIProviderCallbackSignatureValid && (strings.TrimSpace(command.EventID) == "" || strings.TrimSpace(command.ToStatus) == "") {
		return false
	}
	return validateVideoSafeJSON(command.SafeResultJSON) == nil
}

func isVideoCallbackDuplicateError(err error) bool {
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func callbackCompletedAt(status string, now time.Time) interface{} {
	if videoExecutionTerminal(status) {
		return now
	}
	return nil
}

func videoCallbackSHA256(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
