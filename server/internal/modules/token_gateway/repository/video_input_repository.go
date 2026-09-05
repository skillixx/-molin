package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrVideoUploadNotFound     = errors.New("视频上传会话不存在")
	ErrVideoInputNotFound      = errors.New("视频输入不存在")
	ErrVideoInputUnavailable   = errors.New("视频输入当前不可用")
	ErrVideoInputConflict      = errors.New("视频输入状态已变化")
	ErrVideoInputLeaseActive   = errors.New("视频输入仍被执行任务占用")
	ErrVideoInputSnapshotDrift = errors.New("视频输入快照已漂移")
)

// VideoUploadSessionRepository 对上传会话提供不泄露存在性的归属查询。
type VideoUploadSessionRepository struct{ db *gorm.DB }

func NewVideoUploadSessionRepository(db *gorm.DB) *VideoUploadSessionRepository {
	return &VideoUploadSessionRepository{db: db}
}

// FindForOwner 对User、Project或API Key任一不匹配统一返回不存在。
func (r *VideoUploadSessionRepository) FindForOwner(ctx context.Context, publicID string, owner VideoOwner) (*model.AIUploadSession, error) {
	if r == nil || r.db == nil || !validVideoOwner(owner) || strings.TrimSpace(publicID) == "" {
		return nil, ErrVideoUploadNotFound
	}
	query := r.db.WithContext(ctx).Where("public_id=? AND user_id=? AND project_id=?", strings.TrimSpace(publicID), owner.UserID, owner.ProjectID)
	if owner.APIKeyID == nil {
		query = query.Where("api_key_id IS NULL")
	} else {
		query = query.Where("api_key_id=?", *owner.APIKeyID)
	}
	var session model.AIUploadSession
	if err := query.First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoUploadNotFound
		}
		return nil, err
	}
	return &session, nil
}

// VideoInputAssetRepository 管理规范化输入快照、删除竞争和执行租约校验。
type VideoInputAssetRepository struct{ db *gorm.DB }

func NewVideoInputAssetRepository(db *gorm.DB) *VideoInputAssetRepository {
	return &VideoInputAssetRepository{db: db}
}

// FindReadyForBindingTx 在调用方事务内锁定输入并复核，供Quote/Hold/Task同事务创建使用。
func (r *VideoInputAssetRepository) FindReadyForBindingTx(tx *gorm.DB, publicID string, owner VideoOwner, now time.Time) (*model.AIGatewayInputAsset, error) {
	if tx == nil || !validVideoOwner(owner) || now.IsZero() {
		return nil, ErrVideoInputUnavailable
	}
	asset, err := findVideoInputForOwner(tx, publicID, owner, true)
	if err != nil {
		return nil, err
	}
	if asset.LifecycleState != model.AIInputAssetReady || asset.ModerationStatus != model.AIModerationPassed || asset.NormalizedSHA256 == nil || !asset.ExpiresAt.After(now) || asset.DeleteRequestedAt != nil || asset.PendingDeleteAt != nil || asset.DeletedAt != nil {
		return nil, ErrVideoInputUnavailable
	}
	if missing, err := videoInputObjectMissing(tx, asset); err != nil || missing {
		return nil, ErrVideoInputUnavailable
	}
	return asset, nil
}

// FindForOwner 同时验证User、Project与来源API Key；任一越权都返回统一不存在语义。
func (r *VideoInputAssetRepository) FindForOwner(ctx context.Context, publicID string, owner VideoOwner) (*model.AIGatewayInputAsset, error) {
	if r == nil || r.db == nil || !validVideoOwner(owner) || strings.TrimSpace(publicID) == "" {
		return nil, ErrVideoInputNotFound
	}
	return findVideoInputForOwner(r.db.WithContext(ctx), publicID, owner, false)
}

// FindReadyForBinding 仅返回未过期、审核通过且未进入删除流程的不可变输入快照。
func (r *VideoInputAssetRepository) FindReadyForBinding(ctx context.Context, publicID string, owner VideoOwner, now time.Time) (*model.AIGatewayInputAsset, error) {
	asset, err := r.FindForOwner(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.LifecycleState != model.AIInputAssetReady || asset.ModerationStatus != model.AIModerationPassed || asset.NormalizedSHA256 == nil || asset.ExpiresAt.Before(now) || asset.ExpiresAt.Equal(now) || asset.DeleteRequestedAt != nil || asset.PendingDeleteAt != nil || asset.DeletedAt != nil {
		return nil, ErrVideoInputUnavailable
	}
	if missing, err := videoInputObjectMissing(r.db.WithContext(ctx), asset); err != nil || missing {
		return nil, ErrVideoInputUnavailable
	}
	return asset, nil
}

// RequestDelete 在同一事务锁定输入和租约范围；只要存在活跃租约就拒绝进入pending_delete。
func (r *VideoInputAssetRepository) RequestDelete(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, now time.Time) (*model.AIGatewayInputAsset, error) {
	if expectedVersion == 0 || now.IsZero() {
		return nil, ErrVideoInputConflict
	}
	var updated *model.AIGatewayInputAsset
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		asset, err := findVideoInputForOwner(tx, publicID, owner, true)
		if err != nil {
			return err
		}
		if asset.VersionNo != expectedVersion || asset.LegalHold || !videoInputDeleteRequestAllowed(asset.LifecycleState) {
			return ErrVideoInputConflict
		}
		var active int64
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Model(&model.AIGatewayTaskInput{}).
			Where("input_asset_id=? AND user_id=? AND project_id=? AND lease_released_at IS NULL", asset.ID, owner.UserID, owner.ProjectID).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return ErrVideoInputLeaseActive
		}
		result := tx.Model(&model.AIGatewayInputAsset{}).
			Where("id=? AND user_id=? AND project_id=? AND version_no=? AND lifecycle_state=? AND legal_hold=0", asset.ID, owner.UserID, owner.ProjectID, expectedVersion, asset.LifecycleState).
			Updates(map[string]interface{}{
				"lifecycle_state": model.AIInputAssetPendingDelete, "delete_requested_at": now,
				"pending_delete_at": now, "version_no": gorm.Expr("version_no + 1"), "updated_at": now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrVideoInputConflict
		}
		updated, err = findVideoInputForOwner(tx, publicID, owner, false)
		return err
	})
	return updated, err
}

// TransitionLifecycle 用version_no CAS推进输入生命周期，禁止状态回退和相反终态覆盖。
func (r *VideoInputAssetRepository) TransitionLifecycle(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, toState string, now time.Time) (*model.AIGatewayInputAsset, error) {
	asset, err := r.FindForOwner(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if err := r.transitionLifecycleCAS(ctx, asset, owner, expectedVersion, toState, now); err != nil {
		return nil, err
	}
	return r.FindForOwner(ctx, publicID, owner)
}

// 复用原状态矩阵与CAS；管理隔离不会改写原审核结论、规范化快照、期限或输入租约。
func (r *VideoInputAssetRepository) transitionLifecycleCAS(ctx context.Context, asset *model.AIGatewayInputAsset, owner VideoOwner, expectedVersion uint64, toState string, now time.Time) error {
	if asset.VersionNo != expectedVersion || !videoInputTransitionAllowed(asset.LifecycleState, toState) || (asset.LegalHold && videoInputDestructiveState(toState)) {
		return ErrVideoInputConflict
	}
	updates := map[string]interface{}{"lifecycle_state": toState, "version_no": gorm.Expr("version_no + 1"), "updated_at": now}
	if toState == model.AIInputAssetDeleted {
		updates["deleted_at"] = now
	}
	result := r.db.WithContext(ctx).Model(&model.AIGatewayInputAsset{}).
		Where("id=? AND user_id=? AND project_id=? AND lifecycle_state=? AND version_no=?", asset.ID, owner.UserID, owner.ProjectID, asset.LifecycleState, expectedVersion).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVideoInputConflict
	}
	return nil
}

// 仅供已完成管理员鉴权及原来源Key证明的事务使用；只增加隔离，不提供使用授权或解除隔离。
func (r *VideoInputAssetRepository) QuarantineForManagementTx(ctx context.Context, tx *gorm.DB, publicID string, owner VideoOwner, expectedVersion uint64, now time.Time) (*model.AIGatewayInputAsset, error) {
	if tx == nil || owner.UserID == 0 || owner.ProjectID == 0 {
		return nil, ErrVideoInputNotFound
	}
	var asset model.AIGatewayInputAsset
	q := tx.WithContext(ctx)
	if err := q.Clauses(clause.Locking{Strength: "UPDATE"}).Where("public_id=? AND user_id=? AND project_id=?", publicID, owner.UserID, owner.ProjectID).Take(&asset).Error; err != nil {
		return nil, err
	}
	local := NewVideoInputAssetRepository(q)
	if err := local.transitionLifecycleCAS(ctx, &asset, owner, expectedVersion, model.AIInputAssetQuarantined, now); err != nil {
		return nil, err
	}
	if err := q.Where("id=? AND user_id=? AND project_id=?", asset.ID, owner.UserID, owner.ProjectID).Take(&asset).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

// ValidateTaskInputForProvider 在提交Provider前重读TaskInput和InputAsset，验证数量、归属、状态、hash与version快照。
func (r *VideoInputAssetRepository) ValidateTaskInputForProvider(ctx context.Context, taskPublicID string, owner VideoOwner, now time.Time) (*model.AIGatewayTaskInput, error) {
	if r == nil || r.db == nil {
		return nil, ErrVideoInputUnavailable
	}
	var binding *model.AIGatewayTaskInput
	// 复用调用方事务；所有实体使用当前锁读，避免RR旧快照漏掉撤销、删除版本变化或租约释放。
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		binding, err = NewVideoInputAssetRepository(tx).validateTaskInputForProviderTx(ctx, taskPublicID, owner, now)
		return err
	})
	return binding, err
}

func (r *VideoInputAssetRepository) validateTaskInputForProviderTx(ctx context.Context, taskPublicID string, owner VideoOwner, now time.Time) (*model.AIGatewayTaskInput, error) {
	task, err := NewVideoTaskRepository(r.db).LockForOwnerTx(r.db, taskPublicID, owner)
	if err != nil {
		if errors.Is(err, ErrVideoTaskNotFound) {
			return nil, ErrVideoInputNotFound
		}
		return nil, err
	}
	var inputs []model.AIGatewayTaskInput
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("task_id=? AND user_id=? AND project_id=?", task.ID, owner.UserID, owner.ProjectID).Order("ordinal ASC").Find(&inputs).Error; err != nil {
		return nil, err
	}
	if task.Operation == nil {
		return nil, ErrVideoInputSnapshotDrift
	}
	if *task.Operation == model.AIVideoOperationTextToVideo {
		if len(inputs) != 0 {
			return nil, ErrVideoInputSnapshotDrift
		}
		return nil, nil
	}
	if *task.Operation != model.AIVideoOperationImageToVideo || len(inputs) != 1 || inputs[0].Role != model.AITaskInputReferenceImage || inputs[0].Ordinal != 0 || inputs[0].LeaseReleasedAt != nil {
		return nil, ErrVideoInputSnapshotDrift
	}
	var asset model.AIGatewayInputAsset
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND project_id=?", inputs[0].InputAssetID, owner.UserID, owner.ProjectID).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoInputSnapshotDrift
		}
		return nil, err
	}
	trustedAsset, err := findVideoInputForOwner(r.db.WithContext(ctx), asset.PublicID, owner, true)
	if err != nil {
		if errors.Is(err, ErrVideoInputNotFound) {
			return nil, ErrVideoInputSnapshotDrift
		}
		return nil, err
	}
	asset = *trustedAsset
	if missing, err := videoInputObjectMissing(r.db.WithContext(ctx), &asset); err != nil || missing {
		return nil, ErrVideoInputSnapshotDrift
	}
	valid, err := videoBoundInputSnapshotValid(r.db.WithContext(ctx), &asset, &inputs[0], owner, now)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrVideoInputSnapshotDrift
	}
	return &inputs[0], nil
}

// ReleaseTaskLeases 仅在执行与计费都进入安全终态后释放；pending_reconcile永不释放。
func (r *VideoInputAssetRepository) ReleaseTaskLeases(ctx context.Context, taskPublicID string, owner VideoOwner, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := findVideoTaskRecord(tx, taskPublicID, owner, true)
		if err != nil {
			return err
		}
		var leaseCount int64
		if err := tx.Model(&model.AIGatewayTaskInput{}).
			Where("task_id=? AND user_id=? AND project_id=? AND lease_released_at IS NULL", record.ID, owner.UserID, owner.ProjectID).
			Count(&leaseCount).Error; err != nil {
			return err
		}
		if leaseCount == 0 {
			return nil
		}
		if !videoExecutionTerminal(record.Status) || (record.BillingStatus != model.AIBillingSettled && record.BillingStatus != model.AIBillingReleased && record.BillingStatus != model.AIBillingAdjusted) {
			return ErrVideoInputLeaseActive
		}
		return tx.Model(&model.AIGatewayTaskInput{}).
			Where("task_id=? AND user_id=? AND project_id=? AND lease_released_at IS NULL", record.ID, owner.UserID, owner.ProjectID).
			Update("lease_released_at", now).Error
	})
}

// VideoTaskInputRepository 只提供归属受限读取，绑定事实由任务创建事务原子写入。
type VideoTaskInputRepository struct{ db *gorm.DB }

func NewVideoTaskInputRepository(db *gorm.DB) *VideoTaskInputRepository {
	return &VideoTaskInputRepository{db: db}
}

// BindReadyInput 在同一事务锁定I2V任务和ready输入，再写入不可变快照与执行租约。
// 正常创建路径仍由VID-G2预占事务一次性创建Request、Task和TaskInput；本方法供受控补偿与并发契约验证。
func (r *VideoTaskInputRepository) BindReadyInput(ctx context.Context, taskPublicID, inputPublicID string, owner VideoOwner, now time.Time) (*model.AIGatewayTaskInput, error) {
	if r == nil || r.db == nil || !validVideoOwner(owner) || now.IsZero() {
		return nil, ErrVideoInputUnavailable
	}
	var binding model.AIGatewayTaskInput
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := findVideoTaskRecord(tx, taskPublicID, owner, true)
		if err != nil || task.Operation == nil || *task.Operation != model.AIVideoOperationImageToVideo || (task.Status != model.AIImageTaskCreated && task.Status != model.AIImageTaskReserved) {
			return ErrVideoInputUnavailable
		}
		asset, err := findVideoInputForOwner(tx, inputPublicID, owner, true)
		if err != nil || asset.LifecycleState != model.AIInputAssetReady || asset.ModerationStatus != model.AIModerationPassed || asset.NormalizedSHA256 == nil || !asset.ExpiresAt.After(now) || asset.DeleteRequestedAt != nil || asset.PendingDeleteAt != nil || asset.DeletedAt != nil {
			return ErrVideoInputUnavailable
		}
		if missing, err := videoInputObjectMissing(tx, asset); err != nil || missing {
			return ErrVideoInputUnavailable
		}
		var count int64
		if err := tx.Model(&model.AIGatewayTaskInput{}).Where("task_id=?", task.ID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrVideoInputConflict
		}
		binding = model.AIGatewayTaskInput{
			TaskID: task.ID, InputAssetID: asset.ID, UserID: owner.UserID, ProjectID: owner.ProjectID,
			Role: model.AITaskInputReferenceImage, Ordinal: 0, NormalizedSHA256: *asset.NormalizedSHA256,
			InputVersion: asset.VersionNo, CreatedAt: now,
		}
		if err := tx.Create(&binding).Error; err != nil {
			return ErrVideoInputConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

// confirmed对象缺失观察是直接使用围栏；恢复扫描将其resolved后才允许重新绑定或提交。
func videoInputObjectMissing(db *gorm.DB, asset *model.AIGatewayInputAsset) (bool, error) {
	if db == nil || asset == nil || asset.Bucket == nil || asset.ObjectKey == nil {
		return true, nil
	}
	var count int64
	err := db.Table("ai_video_object_reconciliation_observations").Where("direction='db_missing_object' AND bucket=? AND object_key=? AND status='confirmed'", *asset.Bucket, *asset.ObjectKey).Count(&count).Error
	return count > 0, err
}

func (r *VideoTaskInputRepository) ListForOwner(ctx context.Context, taskPublicID string, owner VideoOwner) ([]model.AIGatewayTaskInput, error) {
	task, err := NewVideoTaskRepository(r.db).FindForOwner(ctx, taskPublicID, owner)
	if err != nil {
		return nil, ErrVideoInputNotFound
	}
	var inputs []model.AIGatewayTaskInput
	if err := r.db.WithContext(ctx).Where("task_id=? AND user_id=? AND project_id=?", task.ID, owner.UserID, owner.ProjectID).Order("ordinal ASC").Find(&inputs).Error; err != nil {
		return nil, err
	}
	return inputs, nil
}

func findVideoInputForOwner(db *gorm.DB, publicID string, owner VideoOwner, forUpdate bool) (*model.AIGatewayInputAsset, error) {
	query := db.Table("ai_gateway_input_assets AS inputs").Select("inputs.*").
		Where("inputs.public_id=? AND inputs.user_id=? AND inputs.project_id=?", strings.TrimSpace(publicID), owner.UserID, owner.ProjectID)
	if owner.APIKeyID == nil {
		query = query.Where(`(
(inputs.upload_session_id IS NOT NULL AND EXISTS (SELECT 1 FROM ai_upload_sessions s WHERE s.id=inputs.upload_session_id AND s.user_id=inputs.user_id AND s.project_id=inputs.project_id AND s.status='completed' AND s.final_input_asset_id=inputs.id AND s.api_key_id IS NULL)) OR
(inputs.source_gateway_asset_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM ai_gateway_assets a JOIN ai_gateway_tasks t ON t.id=a.task_id
  WHERE a.id=inputs.source_gateway_asset_id AND a.user_id=inputs.user_id AND a.project_id=inputs.project_id
    AND a.modality='image' AND a.lifecycle_state='available' AND a.moderation_status='passed'
    AND a.explicit_label_status='applied' AND a.implicit_label_status='applied'
    AND a.expires_at>CURRENT_TIMESTAMP AND a.deleted_at IS NULL AND a.media_deleted_at IS NULL AND a.dispute_status<>'open'
    AND t.capability='image.generate' AND t.operation IS NULL AND t.api_key_id IS NULL
))
)`)
	} else {
		query = query.Where(`(
(inputs.upload_session_id IS NOT NULL AND EXISTS (SELECT 1 FROM ai_upload_sessions s WHERE s.id=inputs.upload_session_id AND s.user_id=inputs.user_id AND s.project_id=inputs.project_id AND s.status='completed' AND s.final_input_asset_id=inputs.id AND s.api_key_id=?)) OR
(inputs.source_gateway_asset_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM ai_gateway_assets a JOIN ai_gateway_tasks t ON t.id=a.task_id
  WHERE a.id=inputs.source_gateway_asset_id AND a.user_id=inputs.user_id AND a.project_id=inputs.project_id
    AND a.modality='image' AND a.lifecycle_state='available' AND a.moderation_status='passed'
    AND a.explicit_label_status='applied' AND a.implicit_label_status='applied'
    AND a.expires_at>CURRENT_TIMESTAMP AND a.deleted_at IS NULL AND a.media_deleted_at IS NULL AND a.dispute_status<>'open'
    AND t.capability='image.generate' AND t.operation IS NULL AND t.api_key_id=?
))
)`, *owner.APIKeyID, *owner.APIKeyID)
	}
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var asset model.AIGatewayInputAsset
	if err := query.First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoInputNotFound
		}
		return nil, err
	}
	return &asset, nil
}

func videoInputDeleteRequestAllowed(state string) bool {
	return state == model.AIInputAssetReady || state == model.AIInputAssetRejected || state == model.AIInputAssetQuarantined
}

func videoInputDestructiveState(state string) bool {
	return state == model.AIInputAssetPendingDelete || state == model.AIInputAssetExpiring || state == model.AIInputAssetDeleting || state == model.AIInputAssetDeleted
}

func videoInputTransitionAllowed(from, to string) bool {
	allowed := map[string]map[string]bool{
		model.AIInputAssetPending:       {model.AIInputAssetNormalizing: true, model.AIInputAssetRejected: true, model.AIInputAssetQuarantined: true},
		model.AIInputAssetNormalizing:   {model.AIInputAssetModerating: true, model.AIInputAssetRejected: true, model.AIInputAssetQuarantined: true},
		model.AIInputAssetModerating:    {model.AIInputAssetReady: true, model.AIInputAssetRejected: true, model.AIInputAssetQuarantined: true},
		model.AIInputAssetReady:         {model.AIInputAssetQuarantined: true, model.AIInputAssetPendingDelete: true, model.AIInputAssetExpiring: true},
		model.AIInputAssetRejected:      {model.AIInputAssetPendingDelete: true},
		model.AIInputAssetQuarantined:   {model.AIInputAssetReady: true, model.AIInputAssetPendingDelete: true},
		model.AIInputAssetPendingDelete: {model.AIInputAssetDeleting: true},
		model.AIInputAssetExpiring:      {model.AIInputAssetDeleting: true},
		model.AIInputAssetDeleting:      {model.AIInputAssetDeleted: true, model.AIInputAssetDeleteFailed: true},
		model.AIInputAssetDeleteFailed:  {model.AIInputAssetDeleting: true},
	}
	return allowed[from][to]
}
