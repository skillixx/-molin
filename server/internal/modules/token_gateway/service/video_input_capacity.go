package service

import (
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
)

// 调用方先锁用户行，上传和来源导入共享并发与容量；已知但尚未清理的失败对象继续占额。
func checkVideoInputCapacity(tx *gorm.DB, owner repository.VideoOwner, now time.Time, reserve, maximum uint64) error {
	for _, scope := range []struct {
		column string
		id     uint64
		limit  int64
	}{{"user_id", owner.UserID, 2}, {"project_id", owner.ProjectID, 4}} {
		var uploads, imports int64
		if err := tx.Table("ai_upload_sessions").Where(scope.column+"=? AND status IN ('created','uploading','verifying') AND expires_at>?", scope.id, now).Count(&uploads).Error; err != nil {
			return err
		}
		if err := tx.Table("ai_video_input_imports").Where(scope.column+"=? AND status='processing' AND expires_at>?", scope.id, now).Count(&imports).Error; err != nil {
			return err
		}
		if uploads+imports >= scope.limit {
			return ErrVideoUploadConcurrency
		}
	}
	if reserve > maximum {
		return ErrVideoUploadCapacity
	}
	remaining := maximum - reserve
	for _, table := range []string{"ai_video_upload_controls", "ai_video_input_imports"} {
		var used uint64
		if err := tx.Table(table).Select("COALESCE(SUM(reserved_bytes),0)").Where("user_id=? AND cleaned_at IS NULL", owner.UserID).Scan(&used).Error; err != nil {
			return err
		}
		if used > remaining {
			return ErrVideoUploadCapacity
		}
		remaining -= used
	}
	var legacy uint64
	if err := tx.Table("ai_gateway_input_assets a").Select("COALESCE(SUM(a.size_bytes),0)").Where(`a.user_id=? AND a.lifecycle_state<>'deleted'
AND NOT EXISTS (SELECT 1 FROM ai_video_upload_controls c WHERE c.session_id=a.upload_session_id)
AND NOT EXISTS (SELECT 1 FROM ai_video_input_imports i WHERE i.input_asset_id=a.id)`, owner.UserID).Scan(&legacy).Error; err != nil {
		return err
	}
	if legacy > remaining {
		return ErrVideoUploadCapacity
	}
	return nil
}
