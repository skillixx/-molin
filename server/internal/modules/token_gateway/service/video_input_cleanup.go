package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 本阶段只接受明确的非商业合成策略，调用方不得通过HTTP提交时钟或留存期限。
type VideoInputCleanupPolicy struct {
	Purpose        string           `json:"-"`
	Version        string           `json:"-"`
	BoundRetention time.Duration    `json:"-"`
	Now            func() time.Time `json:"-"`
}

// 清理适配器必须同步遵守取消、删除原件/封存/规范化副本并建立不可复活围栏。
// 任意读取失败不等于不存在；VerifyDiscarded须独立确认同一服务端目标。
type videoSynchronousUploadCleanup interface {
	VideoUploadStore
	SupportsSynchronousDeletion() bool
	VerifyDiscarded(context.Context, VideoUploadTarget) (bool, error)
}

type videoSynchronousImportCleanup interface {
	VideoInputImportStore
	SupportsSynchronousDeletion() bool
	VerifyDiscarded(context.Context, VideoImportObject) (bool, error)
}

// 完成事实只在同一目标的删除及围栏被确认之后追加，普通InputAsset.deleted标志不能替代它。
type videoInputCleanupFact struct {
	InputAssetID          uint64    `gorm:"primaryKey" json:"-"`
	UserID                uint64    `json:"-"`
	ProjectID             uint64    `json:"-"`
	InputVersionBefore    uint64    `json:"-"`
	InputVersionAfter     uint64    `json:"-"`
	NormalizedSHA256      string    `json:"-"`
	PolicyVersion         string    `json:"-"`
	BoundRetentionSeconds uint64    `json:"-"`
	SourceKind            string    `json:"-"`
	EligibleAt            time.Time `json:"-"`
	CompletedAt           time.Time `json:"-"`
}

func (videoInputCleanupFact) TableName() string { return "ai_video_input_cleanup_facts" }

func cleanupOwnerKeyEqual(a, b *uint64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func cleanupAdapterPresent(v any) bool {
	return v != nil && !(reflect.ValueOf(v).Kind() == reflect.Ptr && reflect.ValueOf(v).IsNil())
}

// CleanupInput是限定用途的内部清理入口，不是新的生成授权；用户HTTP必须另行完成当前主体校验。
// 数据库锁仅覆盖同步、遵守取消约定的适配器；外部异步存储仍须独立围栏合同，不能自动降级装配。
func (s *VideoHTTPService) CleanupInput(ctx context.Context, id string, owner repository.VideoOwner, policy VideoInputCleanupPolicy) (*VideoInputDeletionReply, error) {
	if s == nil || s.db == nil || policy.Purpose != "non_commercial_test_fixture" || policy.BoundRetention != currentVideoRetentionPolicy.InputBound || !videoIntentPolicyCode.MatchString(policy.Version) {
		return nil, ErrVideoAccessUnavailable
	}
	if !videoBillingPublicID.MatchString(id) || owner.UserID == 0 || owner.ProjectID == 0 {
		return nil, repository.ErrVideoInputNotFound
	}
	if policy.Now == nil {
		policy.Now = time.Now
	}
	operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 请求取消不应先释放数据库锁，再让同步Store继续执行；等待适配器返回后由本回调结束事务。
	dbCtx, dbCancel := context.WithTimeout(context.WithoutCancel(ctx), 35*time.Second)
	defer dbCancel()
	var reply *VideoInputDeletionReply
	err := s.db.WithContext(dbCtx).Transaction(func(tx *gorm.DB) error {
		if err := operationCtx.Err(); err != nil {
			return err
		}
		var identity struct{ ID uint64 }
		if err := tx.Table("ai_gateway_input_assets").Select("id").Where("public_id=? AND user_id=? AND project_id=?", id, owner.UserID, owner.ProjectID).Take(&identity).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoInputNotFound)
		}
		var observed []model.AIGatewayTaskInput
		if err := tx.Where("input_asset_id=?", identity.ID).Find(&observed).Error; err != nil {
			return err
		}
		var taskIDs []uint64
		for _, b := range observed {
			taskIDs = append(taskIDs, b.TaskID)
		}
		var names []struct {
			ID       uint64
			PublicID string
		}
		if len(taskIDs) > 0 {
			if err := tx.Table("ai_gateway_tasks").Select("id,public_id").Where("id IN ?", taskIDs).Find(&names).Error; err != nil {
				return err
			}
		}
		if len(names) != len(taskIDs) {
			return ErrVideoAccessUnavailable
		}
		sort.Slice(names, func(i, j int) bool { return names[i].PublicID < names[j].PublicID })
		tasks := map[uint64]*repository.VideoTaskRecord{}
		for _, name := range names {
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, name.PublicID, owner)
			if err != nil {
				return err
			}
			tasks[task.ID] = task
		}
		var asset model.AIGatewayInputAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND project_id=?", identity.ID, owner.UserID, owner.ProjectID).Take(&asset).Error; err != nil {
			return err
		}
		var deletion repository.VideoInputDeletionRequest
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("input_asset_id=? AND user_id=? AND project_id=?", asset.ID, owner.UserID, owner.ProjectID).Take(&deletion).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoInputNotFound)
		}
		if !cleanupOwnerKeyEqual(deletion.APIKeyID, owner.APIKeyID) {
			return repository.ErrVideoInputNotFound
		}
		var proof videoInputCleanupFact
		proofErr := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("input_asset_id=?", asset.ID).Take(&proof).Error
		if proofErr == nil {
			if !videoCleanupFactMatches(asset, deletion, proof) {
				return ErrVideoAccessUnavailable
			}
			reply = videoCleanupReply(asset, deletion, true, true)
			return nil
		}
		if !errors.Is(proofErr, gorm.ErrRecordNotFound) {
			return proofErr
		}
		if !repository.VideoPendingDeletionMatches(&asset, deletion) || asset.VersionNo > math.MaxUint64-2 {
			return ErrVideoInputDeleteConflict
		}
		reply = videoCleanupReply(asset, deletion, false, false)
		var bindings []model.AIGatewayTaskInput
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("input_asset_id=?", asset.ID).Find(&bindings).Error; err != nil {
			return err
		}
		if len(bindings) != len(observed) {
			return ErrVideoInputDeleteConflict
		}
		eligible := asset.ExpiresAt
		for _, b := range bindings {
			task := tasks[b.TaskID]
			if task == nil || b.UserID != owner.UserID || b.ProjectID != owner.ProjectID {
				return ErrVideoInputDeleteConflict
			}
			if b.LeaseReleasedAt == nil || !videoG4TerminalStatus(task.Status) || (task.BillingStatus != "settled" && task.BillingStatus != "released" && task.BillingStatus != "adjusted") {
				return nil
			}
			if task.CompletedAt == nil || b.LeaseReleasedAt.Before(*task.CompletedAt) {
				return ErrVideoAccessUnavailable
			}
			deadline := b.LeaseReleasedAt.Add(policy.BoundRetention)
			if deadline.After(eligible) {
				eligible = deadline
			}
		}
		now := policy.Now().UTC().Truncate(time.Second)
		if now.Before(eligible) {
			return nil
		}
		// 原来源可以到期或已删除，但归属与安全状态必须仍可确认；绝不读取或删除来源图片正文。
		if asset.SourceGatewayAssetID != nil {
			var source model.AIImageAsset
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND project_id=? AND modality='image'", *asset.SourceGatewayAssetID, owner.UserID, owner.ProjectID).Take(&source).Error; err != nil {
				return err
			}
			if source.LegalHold || source.DisputeStatus == "open" || source.LifecycleState == "quarantined" || source.ModerationStatus != "passed" {
				return ErrVideoInputDeleteConflict
			}
		}
		var discard func() error
		var confirm func() (bool, error)
		var releaseCapacity func(time.Time) error
		sourceKind := "upload"
		if asset.UploadSessionID != nil {
			if s.uploads == nil {
				return ErrVideoUploadUnavailable
			}
			adapter, ok := s.uploads.options.Store.(videoSynchronousUploadCleanup)
			if !ok || !cleanupAdapterPresent(adapter) || !adapter.SupportsSynchronousDeletion() {
				return ErrVideoUploadUnavailable
			}
			var session model.AIUploadSession
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", *asset.UploadSessionID).Take(&session).Error; err != nil {
				return err
			}
			r, err := s.uploads.load(tx, owner, session.PublicID, true)
			if err != nil {
				return err
			}
			if r.session.Status != "completed" || r.session.FinalInputAssetID == nil || *r.session.FinalInputAssetID != asset.ID || r.control.InputPublicID != asset.PublicID || asset.Bucket == nil || asset.ObjectKey == nil || r.control.NormalizedBucket != *asset.Bucket || r.control.NormalizedKey != *asset.ObjectKey || r.control.CleanedAt != nil {
				return ErrVideoInputDeleteConflict
			}
			target := r.target()
			discard = func() error { return adapter.Discard(operationCtx, target) }
			confirm = func() (bool, error) { return adapter.VerifyDiscarded(operationCtx, target) }
			releaseCapacity = func(at time.Time) error {
				return s.uploads.advance(tx, &r, r.session.Status, map[string]any{"cleaned_at": at, "cleanup_pending": false, "lease_until": nil})
			}
		} else if asset.SourceGatewayAssetID != nil {
			if s.imports == nil {
				return ErrVideoImportUnavailable
			}
			adapter, ok := s.imports.options.Store.(videoSynchronousImportCleanup)
			if !ok || !cleanupAdapterPresent(adapter) || !adapter.SupportsSynchronousDeletion() {
				return ErrVideoImportUnavailable
			}
			r, err := loadVideoImport(tx, videoInputImportRecord{InputAssetID: asset.ID, UserID: owner.UserID, ProjectID: owner.ProjectID})
			if err != nil {
				return err
			}
			if r.Status != "completed" || !cleanupOwnerKeyEqual(r.APIKeyID, owner.APIKeyID) || r.SourceAssetID != *asset.SourceGatewayAssetID || asset.Bucket == nil || asset.ObjectKey == nil || r.NormalizedBucket != *asset.Bucket || r.NormalizedKey != *asset.ObjectKey || r.CleanedAt != nil {
				return ErrVideoInputDeleteConflict
			}
			target := r.target()
			sourceKind = "import"
			discard = func() error { return adapter.Discard(operationCtx, target) }
			confirm = func() (bool, error) { return adapter.VerifyDiscarded(operationCtx, target) }
			releaseCapacity = func(at time.Time) error {
				return s.imports.update(tx, &r, map[string]any{"cleaned_at": at, "cleanup_pending": false, "lease_until": nil})
			}
		} else {
			return ErrVideoInputDeleteConflict
		}
		originalVersion := asset.VersionNo
		changed := tx.Model(&model.AIGatewayInputAsset{}).Where("id=? AND version_no=? AND lifecycle_state='pending_delete' AND legal_hold=0", asset.ID, originalVersion).Updates(map[string]any{"lifecycle_state": "deleting", "version_no": originalVersion + 1, "updated_at": now})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoInputDeleteConflict
		}
		if err := discard(); err != nil {
			return err
		}
		absent, err := confirm()
		if err != nil {
			return err
		}
		if !absent {
			return ErrVideoAccessUnavailable
		}
		if err := operationCtx.Err(); err != nil {
			return err
		}
		at := policy.Now().UTC().Truncate(time.Second)
		if at.Before(eligible) {
			return ErrVideoAccessUnavailable
		}
		changed = tx.Model(&model.AIGatewayInputAsset{}).Where("id=? AND version_no=? AND lifecycle_state='deleting' AND legal_hold=0", asset.ID, originalVersion+1).Updates(map[string]any{"lifecycle_state": "deleted", "version_no": originalVersion + 2, "deleted_at": at, "updated_at": at})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoInputDeleteConflict
		}
		if err := releaseCapacity(at); err != nil {
			return err
		}
		proof = videoInputCleanupFact{InputAssetID: asset.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, InputVersionBefore: originalVersion, InputVersionAfter: originalVersion + 2, NormalizedSHA256: deletion.NormalizedSHA256, PolicyVersion: policy.Version, BoundRetentionSeconds: uint64(policy.BoundRetention / time.Second), SourceKind: sourceKind, EligibleAt: eligible, CompletedAt: at}
		if err := tx.Create(&proof).Error; err != nil {
			return err
		}
		asset.LifecycleState, asset.VersionNo, asset.DeletedAt = "deleted", originalVersion+2, &at
		reply = videoCleanupReply(asset, deletion, true, false)
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	return reply, nil
}

func videoCleanupReply(asset model.AIGatewayInputAsset, d repository.VideoInputDeletionRequest, deleted, replay bool) *VideoInputDeletionReply {
	return &VideoInputDeletionReply{InputAssetID: asset.PublicID, LifecycleState: asset.LifecycleState, VersionNo: asset.VersionNo, DeleteRequestedAt: d.RequestedAt, MediaDeleted: deleted, Idempotent: replay}
}
func videoCleanupFactMatches(asset model.AIGatewayInputAsset, d repository.VideoInputDeletionRequest, f videoInputCleanupFact) bool {
	return f.InputAssetID == asset.ID && f.UserID == asset.UserID && f.ProjectID == asset.ProjectID && f.InputVersionBefore == d.DeletionVersion && f.InputVersionAfter == asset.VersionNo && asset.LifecycleState == "deleted" && asset.DeletedAt != nil && asset.DeletedAt.Equal(f.CompletedAt) && asset.NormalizedSHA256 != nil && *asset.NormalizedSHA256 == f.NormalizedSHA256 && f.NormalizedSHA256 == d.NormalizedSHA256
}
