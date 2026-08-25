package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

type ImageAssetRepository struct {
	db *gorm.DB
}

func NewImageAssetRepository(db *gorm.DB) *ImageAssetRepository {
	return &ImageAssetRepository{db: db}
}

func (r *ImageAssetRepository) Create(ctx context.Context, asset *model.AIImageAsset) error {
	if r == nil || r.db == nil || asset == nil || asset.UserID == 0 || asset.ProjectID == 0 || asset.TaskID == 0 {
		return ErrImageAssetNotFound
	}
	return r.db.WithContext(ctx).Create(asset).Error
}

// FindOwnedForInternal 只供网关内部状态机读取；对外下载必须使用 FindDeliverable。
func (r *ImageAssetRepository) FindOwnedForInternal(ctx context.Context, publicID string, owner ImageOwner) (*model.AIImageAsset, error) {
	if r == nil || r.db == nil || owner.UserID == 0 || owner.ProjectID == 0 {
		return nil, ErrImageAssetNotFound
	}
	var asset model.AIImageAsset
	if err := r.db.WithContext(ctx).Where("public_id = ? AND user_id = ? AND project_id = ?", publicID, owner.UserID, owner.ProjectID).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrImageAssetNotFound
		}
		return nil, err
	}
	return &asset, nil
}

// FindDeliverable 统一关闭删除、隔离、争议中、未审核、未标识和非主图访问，避免调用方各自拼条件。
func (r *ImageAssetRepository) FindDeliverable(ctx context.Context, publicID string, owner ImageOwner) (*model.AIImageAsset, error) {
	if r == nil || r.db == nil || owner.UserID == 0 || owner.ProjectID == 0 {
		return nil, ErrImageAssetAccess
	}
	var asset model.AIImageAsset
	err := r.db.WithContext(ctx).Table("ai_gateway_assets AS assets").
		Select("assets.*").
		Joins("JOIN ai_requests AS requests ON requests.request_id = assets.request_id").
		Where(`assets.public_id = ? AND assets.user_id = ? AND assets.project_id = ?
AND assets.asset_role = ? AND assets.is_billable_output = 1
AND assets.lifecycle_state = ? AND assets.moderation_status = ?
AND assets.explicit_label_status = ? AND assets.implicit_label_status = ?
AND assets.dispute_status <> ? AND assets.deleted_at IS NULL
AND requests.billing_status = ? AND requests.delivery_status = ?`,
			publicID, owner.UserID, owner.ProjectID, model.AIImageAssetPrimaryOutput, model.AIImageAssetAvailable,
			model.AIModerationPassed, model.AIImageLabelApplied, model.AIImageLabelApplied, model.AIImageDisputeOpen,
			model.AIBillingSettled, model.AIDeliveryAvailable).
		First(&asset).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrImageAssetAccess
		}
		return nil, err
	}
	return &asset, nil
}

func (r *ImageAssetRepository) TransitionLifecycle(ctx context.Context, publicID string, owner ImageOwner, expectedVersion uint64, toState string, now time.Time) (*model.AIImageAsset, error) {
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion {
		return nil, ErrImageAssetConflict
	}
	if !imageAssetTransitionAllowed(asset.LifecycleState, toState) {
		return nil, ErrImageAssetTransition
	}
	if (asset.LegalHold || asset.DisputeStatus == model.AIImageDisputeOpen) && imageAssetDestructiveState(toState) {
		return nil, ErrImageAssetTransition
	}
	updates := map[string]interface{}{
		"lifecycle_state": toState, "version_no": gorm.Expr("version_no + 1"), "updated_at": now,
	}
	if toState == model.AIImageAssetQuarantined {
		// 数据库要求隔离资产具有拒绝/错误审核事实；人工隔离按保守策略记为拒绝并立即关闭下载。
		updates["moderation_status"] = model.AIModerationRejected
	}
	if toState == model.AIImageAssetAvailable {
		// 解除隔离只有在显式状态机操作成功时才恢复审核通过，双标识和对象完整性仍由数据库CHECK复核。
		updates["moderation_status"] = model.AIModerationPassed
	}
	if toState == model.AIImageAssetDeleted {
		updates["deleted_at"] = now
	} else {
		updates["deleted_at"] = nil
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id = ? AND user_id = ? AND project_id = ? AND lifecycle_state = ? AND version_no = ?", asset.ID, owner.UserID, owner.ProjectID, asset.LifecycleState, expectedVersion).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrImageAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

// ClaimStaleDeleting 用version CAS续领陈旧deleting资产；legal hold或开放争议出现时立即拒绝破坏性恢复。
func (r *ImageAssetRepository) ClaimStaleDeleting(ctx context.Context, publicID string, owner ImageOwner, expectedVersion uint64, now time.Time) (*model.AIImageAsset, error) {
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where(`public_id = ? AND user_id = ? AND project_id = ? AND lifecycle_state = ? AND version_no = ?
AND legal_hold = 0 AND dispute_status <> ? AND deleted_at IS NULL`,
			publicID, owner.UserID, owner.ProjectID, model.AIImageAssetDeleting, expectedVersion, model.AIImageDisputeOpen).
		Updates(map[string]interface{}{"version_no": gorm.Expr("version_no + 1"), "updated_at": now})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrImageAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

func (r *ImageAssetRepository) OpenDispute(ctx context.Context, publicID string, owner ImageOwner, expectedVersion uint64, now time.Time) (*model.AIImageAsset, error) {
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion || asset.DisputeStatus != model.AIImageDisputeNone || imageAssetDestructiveState(asset.LifecycleState) {
		return nil, ErrImageAssetConflict
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id = ? AND version_no = ? AND dispute_status = ? AND lifecycle_state NOT IN ?", asset.ID, expectedVersion, model.AIImageDisputeNone,
			[]string{model.AIImageAssetExpiring, model.AIImageAssetDeleting, model.AIImageAssetDeleted}).
		Updates(map[string]interface{}{
			"dispute_status": model.AIImageDisputeOpen, "dispute_opened_at": now, "dispute_resolved_at": nil,
			"legal_hold": true, "version_no": gorm.Expr("version_no + 1"), "updated_at": now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrImageAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

// ResolveDispute 只关闭争议访问阻断，继续保留 legal hold；释放保全必须由后续独立审计动作完成。
func (r *ImageAssetRepository) ResolveDispute(ctx context.Context, publicID string, owner ImageOwner, expectedVersion uint64, now time.Time) (*model.AIImageAsset, error) {
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion || asset.DisputeStatus != model.AIImageDisputeOpen {
		return nil, ErrImageAssetConflict
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id = ? AND version_no = ? AND dispute_status = ?", asset.ID, expectedVersion, model.AIImageDisputeOpen).
		Updates(map[string]interface{}{
			"dispute_status": model.AIImageDisputeResolved, "dispute_resolved_at": now,
			"version_no": gorm.Expr("version_no + 1"), "updated_at": now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrImageAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

func imageAssetDestructiveState(state string) bool {
	return state == model.AIImageAssetExpiring || state == model.AIImageAssetDeleting || state == model.AIImageAssetDeleted
}

func imageAssetTransitionAllowed(from, to string) bool {
	allowed := map[string]map[string]bool{
		model.AIImageAssetTemporary:    {model.AIImageAssetAvailable: true, model.AIImageAssetQuarantined: true, model.AIImageAssetDeleting: true},
		model.AIImageAssetAvailable:    {model.AIImageAssetQuarantined: true, model.AIImageAssetExpiring: true},
		model.AIImageAssetQuarantined:  {model.AIImageAssetAvailable: true, model.AIImageAssetDeleting: true},
		model.AIImageAssetExpiring:     {model.AIImageAssetDeleting: true},
		model.AIImageAssetDeleting:     {model.AIImageAssetDeleted: true, model.AIImageAssetDeleteFailed: true},
		model.AIImageAssetDeleteFailed: {model.AIImageAssetDeleting: true},
	}
	return allowed[from][to]
}
