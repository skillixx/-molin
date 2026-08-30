package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

// RequestCancellation 以Task锁和版本CAS只记录一次用户意图，不把意图当作Provider接受或财务退款。
func (r *VideoTaskRepository) RequestCancellation(ctx context.Context, publicID string, owner VideoOwner, now time.Time) (*VideoTaskRecord, error) {
	if r == nil || r.db == nil || !validVideoOwner(owner) || strings.TrimSpace(publicID) == "" || now.IsZero() {
		return nil, ErrVideoTaskNotFound
	}
	var result *VideoTaskRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := findVideoTaskRecord(tx, publicID, owner, true)
		if err != nil {
			return err
		}
		if task.CancelRequestedAt != nil || videoExecutionTerminal(task.Status) {
			result = task
			return nil
		}
		updated := tx.Model(&model.AIImageTask{}).Where("id=? AND version_no=? AND cancel_requested_at IS NULL", task.ID, task.VersionNo).Updates(map[string]interface{}{"cancel_requested_at": now, "version_no": gorm.Expr("version_no+1"), "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrVideoTaskConflict
		}
		e := model.AIGatewayTaskEvent{EventID: "vid_cancel_" + strconv.FormatUint(task.ID, 10), TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "cancel_requested", Source: "api", SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`), CreatedAt: now}
		if err := tx.Create(&e).Error; err != nil {
			return err
		}
		result, err = findVideoTaskRecord(tx, publicID, owner, false)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return result, err
}
