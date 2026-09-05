package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	video "molin/server/internal/modules/token_gateway/video"
)

// NewVideoArchiveObjectFenceVerifier把MinIO物理变更绑定到原任务的数据库当前读。
// generation=0只允许没有生效归档接管的普通Worker；正代次必须精确匹配仍有效的归档租约。
func NewVideoArchiveObjectFenceVerifier(db *gorm.DB) video.VideoArchiveFenceVerifier {
	return func(ctx context.Context, taskID string, generation uint64) error {
		if db == nil || ctx == nil || !videoBillingPublicID.MatchString(taskID) {
			return video.ErrVideoObjectConflict
		}
		var fact struct {
			ArchiveGeneration uint64
			ArchiveTokenHash  *string
			ArchiveLeaseUntil *time.Time
			ArchivePhase      *string
		}
		err := db.WithContext(ctx).Table("ai_gateway_tasks").Select("archive_generation,archive_token_hash,archive_lease_until,archive_phase").Where("public_id=?", taskID).Take(&fact).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return video.ErrVideoObjectConflict
		}
		if err != nil {
			return err
		}
		active := fact.ArchiveTokenHash != nil || fact.ArchiveLeaseUntil != nil || fact.ArchivePhase != nil
		if generation == 0 {
			if active {
				return video.ErrVideoObjectConflict
			}
			return nil
		}
		if !active || fact.ArchiveTokenHash == nil || len(*fact.ArchiveTokenHash) != 64 || fact.ArchiveLeaseUntil == nil || !fact.ArchiveLeaseUntil.After(time.Now().UTC()) || fact.ArchivePhase == nil || fact.ArchiveGeneration != generation {
			return video.ErrVideoObjectConflict
		}
		switch *fact.ArchivePhase {
		case "fetching", "storing", "moderating", "labeling", "verified":
			return nil
		default:
			return video.ErrVideoObjectConflict
		}
	}
}
