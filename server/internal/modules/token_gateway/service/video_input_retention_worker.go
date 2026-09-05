package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type VideoInputRetentionWorker struct {
	app      *VideoHTTPService
	workerID string
	now      func() time.Time
}

type videoRetentionCandidate struct {
	ID                uint64
	PublicID          string
	UserID, ProjectID uint64
	APIKeyID          *uint64
	VersionNo         uint64
}

type videoRetentionCursor struct {
	ScopeKey        string
	LastNumericID   uint64
	CompletedCycles uint64
	VersionNo       uint64
}

type videoUploadSessionRetentionCandidate struct {
	ID                                      uint64
	PublicID                                string
	UserID, ProjectID                       uint64
	APIKeyID                                *uint64
	SourceType, MIMEType, Bucket, ObjectKey string
	SizeBytes                               uint64
	ExpiresAt                               time.Time
	ExpectedSHA256, InputPublicID           string
	NormalizedBucket, NormalizedKey         string
	UploadExpiresAt                         time.Time
}

type videoUploadSessionRetentionFact struct {
	SessionID                          uint64 `gorm:"primaryKey"`
	UserID, ProjectID, SizeBytes       uint64
	ExpectedSHA256, PolicyVersion      string
	EligibleAt, CompletedAt, CreatedAt time.Time
}

func (videoUploadSessionRetentionFact) TableName() string {
	return "ai_video_upload_session_retention_facts"
}

func (videoRetentionCursor) TableName() string { return "ai_video_object_scan_cursors" }

func NewVideoInputRetentionWorker(app *VideoHTTPService, workerID string) (*VideoInputRetentionWorker, error) {
	if app == nil || app.db == nil || (app.uploads == nil && app.imports == nil) || !videoBillingPublicID.MatchString(workerID) || len(workerID) > 64 {
		return nil, ErrVideoAccessUnavailable
	}
	return &VideoInputRetentionWorker{app: app, workerID: workerID, now: time.Now}, nil
}

// RunOnce先为已到期ready输入创建确定性retention请求，再复用原清理事务处理所有上传来源pending_delete。
func (w *VideoInputRetentionWorker) RunOnce(ctx context.Context, limit int) (int, error) {
	if w == nil || ctx == nil || limit < 1 || limit > 500 {
		return 0, ErrVideoAccessUnavailable
	}
	now := w.now().UTC().Truncate(time.Second)
	processed := 0
	if w.app.uploads != nil {
		count, err := w.cleanupExpiredUploadSessions(ctx, limit, now)
		processed += count
		if err != nil {
			return processed, err
		}
	}
	turn, err := w.loadCursor(ctx, "retention|turn", now)
	if err != nil {
		return 0, err
	}
	uploadQuota, importQuota := w.quotas(limit, turn.CompletedCycles)
	for _, source := range []struct {
		name  string
		quota int
	}{{"upload", uploadQuota}, {"import", importQuota}} {
		if source.quota == 0 {
			continue
		}
		ready, cursor, done, err := w.loadCandidates(ctx, source.name, "ready", source.quota, now)
		if err != nil {
			return processed, err
		}
		for _, item := range ready {
			owner := repository.VideoOwner{UserID: item.UserID, ProjectID: item.ProjectID, APIKeyID: item.APIKeyID}
			if _, _, err := repository.NewVideoInputAssetRepository(w.app.db).RequestRetentionDelete(ctx, item.PublicID, owner, item.VersionNo, now); err != nil && !errors.Is(err, repository.ErrVideoInputConflict) {
				return processed, err
			}
		}
		if err := w.advanceCursor(ctx, cursor, lastRetentionID(ready), done, now); err != nil {
			return processed, err
		}
	}
	for _, source := range []struct {
		name  string
		quota int
	}{{"upload", uploadQuota}, {"import", importQuota}} {
		if source.quota == 0 {
			continue
		}
		pending, cursor, done, err := w.loadCandidates(ctx, source.name, "pending_delete", source.quota, now)
		if err != nil {
			return processed, err
		}
		for _, item := range pending {
			owner := repository.VideoOwner{UserID: item.UserID, ProjectID: item.ProjectID, APIKeyID: item.APIKeyID}
			reply, err := w.app.CleanupInput(ctx, item.PublicID, owner, VideoInputCleanupPolicy{Purpose: "non_commercial_test_fixture", Version: currentVideoRetentionPolicy.InputPolicyVersion, BoundRetention: currentVideoRetentionPolicy.InputBound, Now: w.now})
			if err != nil {
				if errors.Is(err, repository.ErrVideoInputNotFound) || errors.Is(err, ErrVideoInputDeleteConflict) || errors.Is(err, ErrVideoUploadConflict) {
					continue
				}
				return processed, err
			}
			if reply != nil && reply.MediaDeleted {
				processed++
			}
		}
		if err := w.advanceCursor(ctx, cursor, lastRetentionID(pending), done, now); err != nil {
			return processed, err
		}
	}
	if err := w.advanceCursor(ctx, turn, 0, true, now); err != nil {
		return processed, err
	}
	return processed, nil
}

// cleanupExpiredUploadSessions只处理24小时仍未形成InputAsset的会话；墓碑确认先于数据库终态和追加事实。
func (w *VideoInputRetentionWorker) cleanupExpiredUploadSessions(ctx context.Context, limit int, now time.Time) (int, error) {
	adapter, ok := w.app.uploads.options.Store.(videoSynchronousUploadCleanup)
	if !ok || !cleanupAdapterPresent(adapter) || !adapter.SupportsSynchronousDeletion() {
		return 0, ErrVideoUploadUnavailable
	}
	cursor, err := w.loadCursor(ctx, "retention|upload_session", now)
	if err != nil {
		return 0, err
	}
	items := []videoUploadSessionRetentionCandidate{}
	query := `SELECT u.id,u.public_id,u.user_id,u.project_id,u.api_key_id,u.source_type,u.mime_type,u.size_bytes,u.bucket,u.object_key,u.expires_at,
c.expected_sha256,c.input_public_id,c.normalized_bucket,c.normalized_key,c.upload_expires_at
FROM ai_upload_sessions u JOIN ai_video_upload_controls c ON c.session_id=u.id AND c.user_id=u.user_id AND c.project_id=u.project_id
WHERE u.final_input_asset_id IS NULL AND u.status IN ('created','uploading','verifying') AND u.expires_at<=? AND u.id>?
ORDER BY u.id LIMIT ?`
	if err := w.app.db.WithContext(ctx).Raw(query, now, cursor.LastNumericID, limit+1).Scan(&items).Error; err != nil {
		return 0, err
	}
	done := len(items) <= limit
	if !done {
		items = items[:limit]
	}
	processed := 0
	var firstErr error
	for _, item := range items {
		target := VideoUploadTarget{SessionID: item.PublicID, InputAssetID: item.InputPublicID, UserID: item.UserID, ProjectID: item.ProjectID, SourceType: item.SourceType, SourceBucket: item.Bucket, SourceKey: item.ObjectKey, NormalizedBucket: item.NormalizedBucket, NormalizedKey: item.NormalizedKey, MIMEType: item.MIMEType, ExpectedSHA256: item.ExpectedSHA256, SizeBytes: item.SizeBytes, UploadExpiresAt: item.UploadExpiresAt}
		if err := adapter.Discard(ctx, target); err != nil {
			firstErr = errors.Join(firstErr, err)
			continue
		}
		confirmed, err := adapter.VerifyDiscarded(ctx, target)
		if err != nil || !confirmed {
			firstErr = errors.Join(firstErr, err, ErrVideoUploadUnavailable)
			continue
		}
		err = w.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var session model.AIUploadSession
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", item.ID).Take(&session).Error; err != nil {
				return err
			}
			var control videoUploadControl
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id=?", item.ID).Take(&control).Error; err != nil {
				return err
			}
			if !videoUploadActive(session.Status) || session.FinalInputAssetID != nil || session.ExpiresAt.After(now) || session.UserID != item.UserID || session.ProjectID != item.ProjectID || control.ExpectedSHA256 != item.ExpectedSHA256 {
				return ErrVideoUploadConflict
			}
			changed := tx.Model(&videoUploadControl{}).Where("session_id=? AND version_no=?", item.ID, control.VersionNo).Updates(map[string]any{"cleanup_pending": false, "cleaned_at": now, "last_safe_error": "", "version_no": control.VersionNo + 1})
			if changed.Error != nil || changed.RowsAffected != 1 {
				return errors.Join(changed.Error, ErrVideoUploadConflict)
			}
			changed = tx.Model(&model.AIUploadSession{}).Where("id=? AND status=? AND final_input_asset_id IS NULL", item.ID, session.Status).Updates(map[string]any{"status": model.AIUploadSessionExpired, "expired_at": now, "updated_at": now})
			if changed.Error != nil || changed.RowsAffected != 1 {
				return errors.Join(changed.Error, ErrVideoUploadConflict)
			}
			fact := videoUploadSessionRetentionFact{SessionID: item.ID, UserID: item.UserID, ProjectID: item.ProjectID, ExpectedSHA256: item.ExpectedSHA256, SizeBytes: item.SizeBytes, PolicyVersion: currentVideoRetentionPolicy.UploadPolicyVersion, EligibleAt: item.ExpiresAt, CompletedAt: now, CreatedAt: now}
			return tx.Create(&fact).Error
		})
		if err != nil {
			firstErr = errors.Join(firstErr, err)
			continue
		}
		processed++
	}
	lastID := uint64(0)
	if len(items) > 0 {
		lastID = items[len(items)-1].ID
	}
	if err := w.advanceCursor(ctx, cursor, lastID, done, now); err != nil {
		return processed, errors.Join(firstErr, err)
	}
	return processed, firstErr
}

func (w *VideoInputRetentionWorker) quotas(limit int, cycle uint64) (int, int) {
	if w.app.uploads == nil {
		return 0, limit
	}
	if w.app.imports == nil {
		return limit, 0
	}
	upload := limit / 2
	if limit%2 == 1 && cycle%2 == 0 {
		upload++
	}
	return upload, limit - upload
}

func (w *VideoInputRetentionWorker) sourceQuery(ctx context.Context, source string) *gorm.DB {
	query := w.app.db.WithContext(ctx).Session(&gorm.Session{NewDB: true}).Table("ai_gateway_input_assets AS i").Select("i.id,i.public_id,i.user_id,i.project_id,i.version_no")
	if source == "upload" {
		return query.Select("i.id,i.public_id,i.user_id,i.project_id,u.api_key_id,i.version_no").Joins("JOIN ai_upload_sessions u ON u.id=i.upload_session_id AND u.user_id=i.user_id AND u.project_id=i.project_id AND u.final_input_asset_id=i.id")
	}
	return query.Select("i.id,i.public_id,i.user_id,i.project_id,m.api_key_id,i.version_no").Joins("JOIN ai_video_input_imports m ON m.input_asset_id=i.id AND m.user_id=i.user_id AND m.project_id=i.project_id")
}

func (w *VideoInputRetentionWorker) loadCandidates(ctx context.Context, source, state string, limit int, now time.Time) ([]videoRetentionCandidate, videoRetentionCursor, bool, error) {
	scope := "retention|" + state + "|" + source
	cursor, err := w.loadCursor(ctx, scope, now)
	if err != nil {
		return nil, cursor, false, err
	}
	items := []videoRetentionCandidate{}
	query := w.sourceQuery(ctx, source).Where("i.lifecycle_state=? AND i.legal_hold=0 AND i.deleted_at IS NULL AND i.id>?", state, cursor.LastNumericID)
	if state == "ready" {
		query = query.Where("i.moderation_status='passed' AND i.expires_at<=?", now)
	}
	if err := query.Order("i.id").Limit(limit + 1).Scan(&items).Error; err != nil {
		return nil, cursor, false, err
	}
	done := len(items) <= limit
	if !done {
		items = items[:limit]
	}
	return items, cursor, done, nil
}

func (w *VideoInputRetentionWorker) loadCursor(ctx context.Context, scope string, now time.Time) (videoRetentionCursor, error) {
	if err := w.app.db.WithContext(ctx).Exec("INSERT IGNORE INTO ai_video_object_scan_cursors(scope_key,direction,created_at,updated_at) VALUES(?,'retention',?,?)", scope, now, now).Error; err != nil {
		return videoRetentionCursor{}, err
	}
	var cursor videoRetentionCursor
	err := w.app.db.WithContext(ctx).Table(cursor.TableName()).Where("scope_key=? AND direction='retention'", scope).Take(&cursor).Error
	return cursor, err
}

func (w *VideoInputRetentionWorker) advanceCursor(ctx context.Context, cursor videoRetentionCursor, lastID uint64, done bool, now time.Time) error {
	updates := map[string]any{"last_numeric_id": lastID, "last_scan_at": now, "updated_at": now, "version_no": gorm.Expr("version_no+1"), "completed_cycles": gorm.Expr("completed_cycles")}
	if done {
		updates["last_numeric_id"] = 0
		updates["completed_cycles"] = gorm.Expr("completed_cycles+1")
	}
	return w.app.db.WithContext(ctx).Table(cursor.TableName()).Where("scope_key=? AND version_no=?", cursor.ScopeKey, cursor.VersionNo).Updates(updates).Error
}

func lastRetentionID(items []videoRetentionCandidate) uint64 {
	if len(items) == 0 {
		return 0
	}
	return items[len(items)-1].ID
}

func (w *VideoInputRetentionWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		_, _ = w.RunOnce(ctx, 100)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
