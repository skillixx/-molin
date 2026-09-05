package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type VideoObjectReconciliationSummary struct {
	ExpectedChecked, StorageChecked int
	Observed, Resolved              int
}

type videoExpectedObject struct {
	Bucket, ObjectKey, SHA256 string
	SizeBytes                 uint64
	Variants                  uint64
}

type videoObjectScanCursor struct {
	ScopeKey, Direction, Bucket, ObjectPrefix string
	LastBucket, LastObjectKey                 string
	CompletedCycles, VersionNo                uint64
}

func (videoObjectScanCursor) TableName() string { return "ai_video_object_scan_cursors" }

type VideoObjectReconciliationScanner struct {
	db        *gorm.DB
	inventory video.VideoObjectInventory
	repo      *repository.VideoObjectReconciliationRepository
	grace     time.Duration
	now       func() time.Time
}

func NewVideoObjectReconciliationScanner(db *gorm.DB, inventory video.VideoObjectInventory, grace time.Duration) (*VideoObjectReconciliationScanner, error) {
	if db == nil || inventory == nil || grace < time.Minute || grace > 24*time.Hour {
		return nil, repository.ErrVideoObjectObservationInvalid
	}
	return &VideoObjectReconciliationScanner{db: db, inventory: inventory, repo: repository.NewVideoObjectReconciliationRepository(db), grace: grace, now: time.Now}, nil
}

// ScanExpected核对数据库权威对象；缺失、墓碑或摘要漂移只形成观察，不直接改资产或删除财务事实。
func (s *VideoObjectReconciliationScanner) ScanExpected(ctx context.Context, limit int) (VideoObjectReconciliationSummary, error) {
	if s == nil || limit < 1 || limit > 1000 {
		return VideoObjectReconciliationSummary{}, repository.ErrVideoObjectObservationInvalid
	}
	cursor, err := s.loadScanCursor(ctx, "db-expected", "db_expected", "", "")
	if err != nil {
		return VideoObjectReconciliationSummary{}, err
	}
	expected, done, err := s.loadExpected(ctx, cursor.LastBucket, cursor.LastObjectKey, limit)
	if err != nil {
		return VideoObjectReconciliationSummary{}, err
	}
	summary := VideoObjectReconciliationSummary{}
	now := s.now().UTC()
	for _, object := range expected {
		summary.ExpectedChecked++
		ref := video.VideoObjectRef{Bucket: object.Bucket, ObjectKey: object.ObjectKey}
		actual, inspectErr := s.inventory.InspectObject(ctx, ref)
		missing := errors.Is(inspectErr, video.ErrVideoObjectNotFound)
		if inspectErr != nil && !missing {
			return summary, inspectErr
		}
		if missing || actual.Discarded || actual.SHA256 != object.SHA256 || actual.SizeBytes != object.SizeBytes {
			if _, err := s.repo.Observe(ctx, repository.VideoObjectDBMissing, ref, object.SHA256, object.SizeBytes, now, s.grace); err != nil {
				return summary, err
			}
			summary.Observed++
			continue
		}
		if err := s.repo.Resolve(ctx, repository.VideoObjectDBMissing, ref, now); err != nil {
			return summary, err
		}
		summary.Resolved++
	}
	nextBucket, nextKey := "", ""
	if !done && len(expected) > 0 {
		nextBucket, nextKey = expected[len(expected)-1].Bucket, expected[len(expected)-1].ObjectKey
	}
	if err := s.advanceScanCursor(ctx, cursor, nextBucket, nextKey, done, now); err != nil {
		return summary, err
	}
	return summary, nil
}

// ScanStorage仅扫描冻结的视频用途前缀；图片对象和其他共享Bucket内容永远不进入候选集。
func (s *VideoObjectReconciliationScanner) ScanStorage(ctx context.Context, limitPerPrefix int) (VideoObjectReconciliationSummary, error) {
	if s == nil || limitPerPrefix < 1 || limitPerPrefix > 1000 {
		return VideoObjectReconciliationSummary{}, repository.ErrVideoObjectObservationInvalid
	}
	prefixes := []struct{ bucket, prefix string }{
		{"ai-upload-temp", "vid_"}, {"ai-upload-temp", "video_"}, {"ai-upload-temp", "original/"}, {"ai-upload-temp", "inline/"},
		{"ai-result", "vid_"}, {"ai-result", "video_"}, {"ai-result", "normalized/"},
		{"ai-quarantine", "vid_"}, {"ai-quarantine", "video_"}, {"ai-user-assets", "vsave_"},
	}
	summary := VideoObjectReconciliationSummary{}
	now := s.now().UTC()
	for _, scope := range prefixes {
		scopeKey := fmt.Sprintf("storage|%s|%s", scope.bucket, scope.prefix)
		cursor, err := s.loadScanCursor(ctx, scopeKey, "storage", scope.bucket, scope.prefix)
		if err != nil {
			return summary, err
		}
		page, err := s.inventory.ListPrefix(ctx, scope.bucket, scope.prefix, cursor.LastObjectKey, limitPerPrefix)
		if err != nil {
			return summary, err
		}
		for _, item := range page.Items {
			summary.StorageChecked++
			referenced, err := s.hasReference(ctx, item.Ref, now)
			if err != nil {
				return summary, err
			}
			if referenced {
				if err := s.repo.Resolve(ctx, repository.VideoObjectUnreferenced, item.Ref, now); err != nil {
					return summary, err
				}
				summary.Resolved++
				continue
			}
			digest, size := item.SHA256, item.SizeBytes
			if item.Discarded {
				digest, size = videoPayloadSHA256([]byte{0}), 1
			}
			if _, err := s.repo.Observe(ctx, repository.VideoObjectUnreferenced, item.Ref, digest, size, now, s.grace); err != nil {
				return summary, err
			}
			summary.Observed++
		}
		if err := s.advanceScanCursor(ctx, cursor, "", page.NextStartAfter, page.Done, now); err != nil {
			return summary, err
		}
	}
	return summary, nil
}

func (s *VideoObjectReconciliationScanner) loadExpected(ctx context.Context, lastBucket, lastObjectKey string, limit int) ([]videoExpectedObject, bool, error) {
	rows := []videoExpectedObject{}
	query := `
SELECT MIN(bucket) AS bucket,MIN(object_key) AS object_key,MIN(sha256) AS sha256,MIN(size_bytes) AS size_bytes,
       COUNT(DISTINCT CONCAT(sha256,CHAR(0),CAST(size_bytes AS CHAR))) AS variants
FROM (
SELECT bucket,object_key,LOWER(sha256) AS sha256,size_bytes FROM ai_gateway_assets
 WHERE modality='video' AND bucket IN ('ai-upload-temp','ai-result','ai-quarantine') AND object_key IS NOT NULL AND sha256 IS NOT NULL AND size_bytes IS NOT NULL AND media_deleted_at IS NULL
UNION ALL
SELECT bucket,object_key,LOWER(normalized_sha256),size_bytes FROM ai_gateway_input_assets
 WHERE bucket IN ('ai-result','ai-quarantine') AND object_key IS NOT NULL AND normalized_sha256 IS NOT NULL AND size_bytes IS NOT NULL AND lifecycle_state<>'deleted'
UNION ALL
SELECT u.bucket,u.object_key,LOWER(c.expected_sha256),u.size_bytes FROM ai_upload_sessions u
 JOIN ai_video_upload_controls c ON c.session_id=u.id AND c.user_id=u.user_id AND c.project_id=u.project_id
 WHERE u.bucket='ai-upload-temp' AND u.status='completed' AND u.final_input_asset_id IS NOT NULL
UNION ALL
SELECT j.target_bucket,j.target_key,LOWER(j.sha256),j.size_bytes FROM ai_video_asset_saves s
 JOIN JSON_TABLE(s.plan_json,'$[*]' COLUMNS(
   target_bucket VARCHAR(63) PATH '$.target_bucket',target_key VARCHAR(191) PATH '$.target_key',
   sha256 CHAR(64) PATH '$.sha256',size_bytes BIGINT UNSIGNED PATH '$.size'
 )) j
 WHERE s.status IN ('completed','cleanup_pending')
) refs
WHERE (?='' OR BINARY bucket>BINARY ? OR (BINARY bucket=BINARY ? AND BINARY object_key>BINARY ?))
GROUP BY BINARY bucket,BINARY object_key
ORDER BY BINARY bucket,BINARY object_key LIMIT ?`
	if err := s.db.WithContext(ctx).Raw(query, lastBucket, lastBucket, lastBucket, lastObjectKey, limit+1).Scan(&rows).Error; err != nil {
		return nil, false, err
	}
	done := len(rows) <= limit
	if !done {
		rows = rows[:limit]
	}
	for _, row := range rows {
		if row.Variants != 1 || !lowerHex64.MatchString(row.SHA256) || row.SizeBytes == 0 {
			return nil, false, repository.ErrVideoObjectObservationInvalid
		}
	}
	return rows, done, nil
}

func (s *VideoObjectReconciliationScanner) loadScanCursor(ctx context.Context, scopeKey, direction, bucket, prefix string) (videoObjectScanCursor, error) {
	if s == nil || scopeKey == "" || (direction != "db_expected" && direction != "storage") {
		return videoObjectScanCursor{}, repository.ErrVideoObjectObservationInvalid
	}
	var bucketValue, prefixValue any
	if direction == "storage" {
		bucketValue, prefixValue = bucket, prefix
	}
	now := s.now().UTC()
	if err := s.db.WithContext(ctx).Exec(`INSERT IGNORE INTO ai_video_object_scan_cursors(scope_key,direction,bucket,object_prefix,created_at,updated_at) VALUES(?,?,?,?,?,?)`, scopeKey, direction, bucketValue, prefixValue, now, now).Error; err != nil {
		return videoObjectScanCursor{}, err
	}
	var cursor videoObjectScanCursor
	if err := s.db.WithContext(ctx).Table(cursor.TableName()).Where("scope_key=? AND direction=? AND bucket <=> ? AND object_prefix <=> ?", scopeKey, direction, bucketValue, prefixValue).Take(&cursor).Error; err != nil {
		return videoObjectScanCursor{}, err
	}
	return cursor, nil
}

// advanceScanCursor仅在整页核对成功后CAS推进；到尾部清空位置并累计完整扫描轮次。
func (s *VideoObjectReconciliationScanner) advanceScanCursor(ctx context.Context, cursor videoObjectScanCursor, nextBucket, nextObjectKey string, done bool, now time.Time) error {
	updates := map[string]any{"last_scan_at": now, "updated_at": now, "version_no": gorm.Expr("version_no+1")}
	if done {
		updates["last_bucket"], updates["last_object_key"] = nil, nil
		updates["completed_cycles"] = gorm.Expr("completed_cycles+1")
	} else {
		if cursor.Direction == "db_expected" {
			updates["last_bucket"] = nextBucket
		} else {
			updates["last_bucket"] = nil
		}
		updates["last_object_key"] = nextObjectKey
		updates["completed_cycles"] = gorm.Expr("completed_cycles")
	}
	result := s.db.WithContext(ctx).Table(cursor.TableName()).Where("scope_key=? AND version_no=?", cursor.ScopeKey, cursor.VersionNo).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	// 并发扫描者已推进时，本页观察仍然有效；下一轮从数据库新游标继续，不能回退位置。
	return nil
}

func (s *VideoObjectReconciliationScanner) hasReference(ctx context.Context, ref video.VideoObjectRef, now time.Time) (bool, error) {
	var count int64
	query := `SELECT COUNT(*) FROM (
SELECT id FROM ai_gateway_assets WHERE bucket=? AND object_key=?
UNION ALL SELECT id FROM ai_gateway_input_assets WHERE bucket=? AND object_key=?
UNION ALL SELECT u.id FROM ai_upload_sessions u JOIN ai_video_upload_controls c ON c.session_id=u.id
 WHERE u.bucket=? AND u.object_key=? AND (c.cleaned_at IS NULL OR c.upload_expires_at>?)
) refs`
	if err := s.db.WithContext(ctx).Raw(query, ref.Bucket, ref.ObjectKey, ref.Bucket, ref.ObjectKey, ref.Bucket, ref.ObjectKey, now).Scan(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	if ref.Bucket != "ai-user-assets" {
		return false, nil
	}
	var plans []struct{ PlanJSON json.RawMessage }
	if err := s.db.WithContext(ctx).Table("ai_video_asset_saves").Select("plan_json").Where("status IN ('copying','completed','cleanup_pending')").Find(&plans).Error; err != nil {
		return false, err
	}
	for _, plan := range plans {
		var items []struct {
			TargetBucket string `json:"target_bucket"`
			TargetKey    string `json:"target_key"`
		}
		if json.Unmarshal(plan.PlanJSON, &items) != nil {
			return false, repository.ErrVideoObjectObservationInvalid
		}
		for _, item := range items {
			if item.TargetBucket == ref.Bucket && item.TargetKey == ref.ObjectKey {
				return true, nil
			}
		}
	}
	return false, nil
}
