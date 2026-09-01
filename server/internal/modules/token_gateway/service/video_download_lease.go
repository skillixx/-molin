package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

var ErrVideoDownloadLimited = errors.New("视频下载并发已达上限")

// 60秒是连接租约的工程期限，不改变媒体保留期限；续约以数据库UTC微秒时钟为准。
const videoDownloadLeaseSeconds = 60

const (
	videoDownloadUserLimit    = 2
	videoDownloadProjectLimit = 4
)

type videoDownloadLimits struct {
	User    int64
	Project int64
}

func videoG6DownloadLimits() videoDownloadLimits {
	return videoDownloadLimits{User: videoDownloadUserLimit, Project: videoDownloadProjectLimit}
}

type videoDownloadLease struct {
	db                                          *gorm.DB
	id                                          string
	userID, projectID, assetID, taskID, version uint64
	mu                                          sync.Mutex
	closed                                      bool
}

// recoverCommitted只处理真实提交后确认丢失；随机ID及完整归属任何一项不匹配都不能接管其他租约。
func (l *videoDownloadLease) recoverCommitted(ctx context.Context) error {
	if l == nil || l.db == nil || l.id == "" || l.userID == 0 || l.projectID == 0 || l.taskID == 0 || l.assetID == 0 || l.version != 1 {
		return ErrVideoContentUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var count int64
	err := l.db.WithContext(bounded).Table("ai_video_download_leases").Where("lease_id=? AND user_id=? AND project_id=? AND task_id=? AND asset_id=? AND version_no=1 AND released_at IS NULL AND lease_until>UTC_TIMESTAMP(6)", l.id, l.userID, l.projectID, l.taskID, l.assetID).Count(&count).Error
	if err != nil || count != 1 {
		return ErrVideoContentUnavailable
	}
	return nil
}

func acquireVideoDownloadTx(tx *gorm.DB, asset *model.AIImageAsset, limits videoDownloadLimits) (*videoDownloadLease, error) {
	if limits.User <= 0 || limits.Project <= 0 {
		return nil, ErrVideoContentUnavailable
	}
	// 所有实例按用户再Project锁定相同范围；计数与新租约同事务，Key不会划分独立额度。
	if err := lockVideoDownloadScopesTx(tx, asset.UserID, asset.ProjectID); err != nil {
		return nil, err
	}
	return insertVideoDownloadLeaseTx(tx, asset, limits)
}

// 申请和续约必须共用相同范围锁，防止未提交续约跨旧TTL时，新申请把该名额误判为空闲。
func lockVideoDownloadScopesTx(tx *gorm.DB, userID, projectID uint64) error {
	for _, scope := range []struct {
		kind string
		id   uint64
	}{{"user", userID}, {"project", projectID}} {
		if err := tx.Exec("INSERT INTO ai_video_download_scopes(scope_type,scope_id) VALUES(?,?) ON DUPLICATE KEY UPDATE scope_id=VALUES(scope_id)", scope.kind, scope.id).Error; err != nil {
			return ErrVideoContentUnavailable
		}
	}
	return nil
}

func insertVideoDownloadLeaseTx(tx *gorm.DB, asset *model.AIImageAsset, limits videoDownloadLimits) (*videoDownloadLease, error) {
	for _, scope := range []struct {
		column string
		id     uint64
		limit  int64
	}{{"user_id", asset.UserID, limits.User}, {"project_id", asset.ProjectID, limits.Project}} {
		var count int64
		if err := tx.Table("ai_video_download_leases").Where(scope.column+"=? AND released_at IS NULL AND lease_until>UTC_TIMESTAMP(6)", scope.id).Count(&count).Error; err != nil {
			return nil, ErrVideoContentUnavailable
		}
		if count >= scope.limit {
			return nil, ErrVideoDownloadLimited
		}
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, ErrVideoContentUnavailable
	}
	id := hex.EncodeToString(entropy[:])
	if err := tx.Exec("INSERT INTO ai_video_download_leases(lease_id,user_id,project_id,task_id,asset_id,version_no,created_at,lease_until) VALUES(?,?,?,?,?,1,UTC_TIMESTAMP(6),DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? SECOND))", id, asset.UserID, asset.ProjectID, asset.TaskID, asset.ID, videoDownloadLeaseSeconds).Error; err != nil {
		return nil, ErrVideoContentUnavailable
	}
	return &videoDownloadLease{id: id, userID: asset.UserID, projectID: asset.ProjectID, taskID: asset.TaskID, assetID: asset.ID, version: 1}, nil
}

// 只允许原连接以当前CAS续约；已过期、已释放或其他持有者不能复活该令牌。
func (l *videoDownloadLease) renew(ctx context.Context) (time.Time, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return time.Time{}, ErrVideoContentUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var until time.Time
	err := l.db.WithContext(bounded).Transaction(func(tx *gorm.DB) error {
		if err := lockVideoDownloadScopesTx(tx, l.userID, l.projectID); err != nil {
			return err
		}
		r := tx.Exec("UPDATE ai_video_download_leases SET version_no=version_no+1,lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL ? SECOND) WHERE lease_id=? AND user_id=? AND project_id=? AND task_id=? AND asset_id=? AND version_no=? AND released_at IS NULL AND lease_until>UTC_TIMESTAMP(6)", videoDownloadLeaseSeconds, l.id, l.userID, l.projectID, l.taskID, l.assetID, l.version)
		if r.Error != nil || r.RowsAffected != 1 {
			return ErrVideoContentUnavailable
		}
		return tx.Table("ai_video_download_leases").Select("lease_until").Where("lease_id=?", l.id).Scan(&until).Error
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return time.Time{}, ErrVideoContentUnavailable
	}
	l.version++
	return until, nil
}

// 请求取消、异常和成功都释放同一唯一令牌；失败时依赖有界TTL回收，不释放钱包或媒体事实。
func (l *videoDownloadLease) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := l.db.WithContext(ctx).Exec("UPDATE ai_video_download_leases SET released_at=UTC_TIMESTAMP(6),version_no=version_no+1 WHERE lease_id=? AND user_id=? AND project_id=? AND task_id=? AND asset_id=? AND version_no=? AND released_at IS NULL", l.id, l.userID, l.projectID, l.taskID, l.assetID, l.version)
	if r.Error != nil || r.RowsAffected != 1 {
		return ErrVideoContentUnavailable
	}
	l.closed = true
	l.version++
	return nil
}
