package service

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// NewTaskLedger 只装配既有G5财务账本与本应用的私有输入读取；不创建Provider或后台运行器。
func (s *VideoHTTPService) NewTaskLedger(owner repository.VideoOwner, locator repository.VideoObjectLocationFactory) *VideoRepositoryTaskLedger {
	if s == nil || s.billing == nil {
		return nil
	}
	ledger := NewVideoBillingTaskLedger(s.db, owner, s.billing.protector, locator, s.billing.referenceLoader)
	ledger.taskReferenceLoader = s.loadTaskReference
	// G6 HTTP的Fake执行使用原Task账本做本地运行容量裁决；不装配Redis、Provider租约或G7 Worker。
	ledger.runningAdmission = true
	ledger.runningLimits = videoG6RunningLimits()
	return ledger
}

// 读取期间持有原Task/Request、InputAsset及未释放绑定的锁，最后租约不能在对象IO途中被释放并清理。
// 接受账本当前连接，嵌套调用仅使用保存点，不另借连接；上层事务仍拥有锁的释放边界。
func (s *VideoHTTPService) loadTaskReference(ctx context.Context, db *gorm.DB, taskID string, owner repository.VideoOwner) (*video.ControlledInputRef, *video.NormalizedReferenceImage, error) {
	if s == nil || s.access == nil || db == nil {
		return nil, nil, ErrVideoAccessUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var ref *video.ControlledInputRef
	var image *video.NormalizedReferenceImage
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, taskID, owner)
		if err != nil {
			return err
		}
		if task.Operation == nil || *task.Operation != model.AIVideoOperationImageToVideo || videoG4TerminalStatus(task.Status) {
			return repository.ErrVideoInputSnapshotDrift
		}
		if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
			return err
		}
		binding, err := repository.NewVideoInputAssetRepository(tx).ValidateTaskInputForProvider(ctx, taskID, owner, time.Now().UTC())
		if err != nil {
			return err
		}
		if binding == nil {
			return repository.ErrVideoInputSnapshotDrift
		}
		var asset model.AIGatewayInputAsset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND project_id=?", binding.InputAssetID, owner.UserID, owner.ProjectID).Take(&asset).Error; err != nil {
			return err
		}
		if !videoHTTPInputSnapshotWellFormed(asset) {
			return repository.ErrVideoInputSnapshotDrift
		}
		caller := VideoCaller{UserID: owner.UserID, ProjectID: owner.ProjectID}
		if owner.APIKeyID != nil {
			caller.APIKeyID = *owner.APIKeyID
		}
		if asset.SourceType == "gateway_asset_snapshot" {
			if s.imports == nil || asset.SourceGatewayAssetID == nil {
				return ErrVideoImportUnavailable
			}
			var imported videoInputImportRecord
			if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("input_asset_id=? AND user_id=? AND project_id=?", asset.ID, owner.UserID, owner.ProjectID).Take(&imported).Error; err != nil {
				return err
			}
			if imported.Status != "completed" || imported.SourceAssetID != *asset.SourceGatewayAssetID || imported.NormalizedBucket != *asset.Bucket || imported.NormalizedKey != *asset.ObjectKey {
				return repository.ErrVideoInputSnapshotDrift
			}
			source, err := s.imports.source(tx, caller, imported.SourcePublicID)
			if err != nil {
				return err
			}
			if !importSourceMatches(imported, source) {
				return repository.ErrVideoInputSnapshotDrift
			}
			image, err = s.imports.readReferenceObject(ctx, asset)
			if err != nil {
				return err
			}
		} else {
			if s.uploads == nil || asset.UploadSessionID == nil {
				return ErrVideoUploadUnavailable
			}
			var session model.AIUploadSession
			q := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND project_id=? AND status='completed' AND final_input_asset_id=?", *asset.UploadSessionID, owner.UserID, owner.ProjectID, asset.ID)
			if owner.APIKeyID == nil {
				q = q.Where("api_key_id IS NULL")
			} else {
				q = q.Where("api_key_id=?", *owner.APIKeyID)
			}
			if err := q.Take(&session).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoInputNotFound)
			}
			image, err = s.uploads.readReferenceObject(ctx, asset)
			if err != nil {
				return err
			}
		}
		// IO可能跨越有效期；再次执行当前实体检查，不延长原输入、授权或工作租约。
		if _, err := repository.NewVideoInputAssetRepository(tx).ValidateTaskInputForProvider(ctx, taskID, owner, time.Now().UTC()); err != nil {
			return err
		}
		if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
			return err
		}
		ref = &video.ControlledInputRef{AssetID: asset.PublicID, SHA256: binding.NormalizedSHA256, Version: binding.InputVersion}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, nil, err
	}
	return ref, image, nil
}
