package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type videoRetentionDeleteKey struct{}

func videoRetentionDeleteTime(ctx context.Context) (time.Time, bool) {
	value, ok := ctx.Value(videoRetentionDeleteKey{}).(time.Time)
	return value, ok && !value.IsZero()
}

func (s *VideoHTTPService) taskForMediaDeleteTx(ctx context.Context, tx *gorm.DB, caller VideoCaller, id string, retention bool) (*repository.VideoTaskRecord, repository.VideoOwner, error) {
	if !retention {
		return s.taskForPlatformTx(ctx, tx, caller, id, false)
	}
	var identity struct {
		UserID, ProjectID uint64
		APIKeyID          *uint64
	}
	if err := tx.Table("ai_gateway_tasks").Select("user_id,project_id,api_key_id").Where("public_id=? AND capability='video.generate' AND operation IN ('text_to_video','image_to_video')", id).Take(&identity).Error; err != nil {
		return nil, repository.VideoOwner{}, err
	}
	owner := repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
	task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, id, owner)
	return task, owner, err
}

func videoRetentionAssetsEligible(assets []model.AIImageAsset, now time.Time) bool {
	if now.IsZero() || len(assets) != 6 {
		return false
	}
	for _, asset := range assets {
		if asset.ExpiresAt.After(now) {
			return false
		}
	}
	return true
}

type videoOutputRetentionFact struct {
	TaskID, UserID, ProjectID          uint64
	RequestID, PolicyVersion           string
	EligibleAt, CompletedAt, CreatedAt time.Time
}

func (videoOutputRetentionFact) TableName() string { return "ai_video_output_retention_facts" }

// CleanupExpiredVideoMedia复用原媒体删除两阶段账本，仅跳过已不应阻止系统清理的当前用户凭据授权。
func (s *VideoHTTPService) CleanupExpiredVideoMedia(ctx context.Context, taskID string, eligibleAt, now time.Time) error {
	if s == nil || s.db == nil || !videoBillingPublicID.MatchString(taskID) || eligibleAt.IsZero() || now.Before(eligibleAt) {
		return ErrVideoMediaProtected
	}
	key := "retention-" + videoBillingDigest(fmt.Sprintf("%s:%d", taskID, eligibleAt.UnixNano()))[:32]
	retentionCtx := context.WithValue(ctx, videoRetentionDeleteKey{}, now)
	result, err := s.deleteMedia(retentionCtx, VideoCaller{}, taskID, key, nil)
	if err != nil {
		return err
	}
	if result == nil || !result.Deleted {
		return ErrVideoMediaProtected
	}
	return s.db.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
		task, owner, err := s.taskForMediaDeleteTx(ctx, tx, VideoCaller{}, taskID, true)
		if err != nil {
			return err
		}
		var maxExpiry time.Time
		if err := tx.Table("ai_gateway_assets").Select("MAX(expires_at)").Where("task_id=? AND modality='video'", task.ID).Scan(&maxExpiry).Error; err != nil || maxExpiry.IsZero() || maxExpiry.After(now) || !maxExpiry.Equal(eligibleAt) {
			return ErrVideoMediaProtected
		}
		fact := videoOutputRetentionFact{TaskID: task.ID, RequestID: task.RequestID, UserID: owner.UserID, ProjectID: owner.ProjectID, PolicyVersion: currentVideoRetentionPolicy.OutputPolicyVersion, EligibleAt: eligibleAt, CompletedAt: now, CreatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&fact).Error; err != nil {
			return err
		}
		return nil
	})
}

type VideoOutputRetentionWorker struct {
	app *VideoHTTPService
	now func() time.Time
}

type videoOutputRetentionCandidate struct {
	ID         uint64
	PublicID   string
	EligibleAt time.Time
}

func NewVideoOutputRetentionWorker(app *VideoHTTPService) (*VideoOutputRetentionWorker, error) {
	if app == nil || app.db == nil || app.mediaDeleteStore == nil || !app.mediaDeleteStore.SupportsSynchronousDeletion() {
		return nil, ErrVideoMediaDeleteUnavailable
	}
	return &VideoOutputRetentionWorker{app: app, now: time.Now}, nil
}

func (w *VideoOutputRetentionWorker) RunOnce(ctx context.Context, limit int) (int, error) {
	if w == nil || ctx == nil || limit < 1 || limit > 500 {
		return 0, ErrVideoMediaDeleteUnavailable
	}
	now := w.now().UTC().Truncate(time.Microsecond)
	items, cursor, done, err := w.loadOutputCandidates(ctx, limit, now)
	if err != nil {
		return 0, err
	}
	processed := 0
	var firstErr error
	for _, item := range items {
		if err := w.app.CleanupExpiredVideoMedia(ctx, item.PublicID, item.EligibleAt, now); err != nil {
			if errors.Is(err, ErrVideoMediaProtected) || errors.Is(err, ErrVideoMediaRunning) {
				continue
			}
			firstErr = errors.Join(firstErr, err)
			continue
		}
		processed++
	}
	lastID := uint64(0)
	if len(items) > 0 {
		lastID = items[len(items)-1].ID
	}
	// 受保护任务只影响自身删除，游标仍越过该任务；到达尾部后回卷，解除保护的任务会在下一周期重试。
	if err := w.advanceOutputCursor(ctx, cursor, lastID, done, now); err != nil {
		firstErr = errors.Join(firstErr, err)
	}
	return processed, firstErr
}

func (w *VideoOutputRetentionWorker) loadOutputCandidates(ctx context.Context, limit int, now time.Time) ([]videoOutputRetentionCandidate, videoRetentionCursor, bool, error) {
	const scope = "retention|output|available"
	cursor, err := w.loadOutputCursor(ctx, scope, now)
	if err != nil {
		return nil, cursor, false, err
	}
	items := []videoOutputRetentionCandidate{}
	query := `SELECT t.id,t.public_id,MAX(a.expires_at) AS eligible_at FROM ai_gateway_tasks t
JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id
JOIN ai_gateway_assets a ON a.task_id=t.id AND a.request_id=t.request_id AND a.user_id=t.user_id AND a.project_id=t.project_id AND a.modality='video'
LEFT JOIN ai_video_output_retention_facts f ON f.task_id=t.id
WHERE f.task_id IS NULL AND t.id>? AND t.status='succeeded' AND r.billing_status IN ('settled','released','adjusted') AND r.delivery_status='available'
GROUP BY t.id,t.public_id HAVING COUNT(*)=6 AND MAX(a.expires_at)<=? ORDER BY t.id LIMIT ?`
	if err := w.app.db.WithContext(ctx).Raw(query, cursor.LastNumericID, now, limit+1).Scan(&items).Error; err != nil {
		return nil, cursor, false, err
	}
	done := len(items) <= limit
	if !done {
		items = items[:limit]
	}
	return items, cursor, done, nil
}

func (w *VideoOutputRetentionWorker) loadOutputCursor(ctx context.Context, scope string, now time.Time) (videoRetentionCursor, error) {
	if err := w.app.db.WithContext(ctx).Exec("INSERT IGNORE INTO ai_video_object_scan_cursors(scope_key,direction,created_at,updated_at) VALUES(?,'retention',?,?)", scope, now, now).Error; err != nil {
		return videoRetentionCursor{}, err
	}
	var cursor videoRetentionCursor
	err := w.app.db.WithContext(ctx).Table(cursor.TableName()).Where("scope_key=? AND direction='retention'", scope).Take(&cursor).Error
	return cursor, err
}

func (w *VideoOutputRetentionWorker) advanceOutputCursor(ctx context.Context, cursor videoRetentionCursor, lastID uint64, done bool, now time.Time) error {
	updates := map[string]any{"last_numeric_id": lastID, "last_scan_at": now, "updated_at": now, "version_no": gorm.Expr("version_no+1"), "completed_cycles": gorm.Expr("completed_cycles")}
	if done {
		updates["last_numeric_id"] = 0
		updates["completed_cycles"] = gorm.Expr("completed_cycles+1")
	}
	result := w.app.db.WithContext(ctx).Table(cursor.TableName()).Where("scope_key=? AND direction='retention' AND version_no=?", cursor.ScopeKey, cursor.VersionNo).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVideoMediaDeleteUnavailable
	}
	return nil
}
