package repository

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrVideoAssetNotFound   = errors.New("视频资产不存在")
	ErrVideoAssetAccess     = errors.New("视频资产当前不可访问")
	ErrVideoAssetConflict   = errors.New("视频资产状态已变化")
	ErrVideoAssetTransition = errors.New("视频资产状态流转不允许")
	ErrVideoEventNotFound   = errors.New("视频任务事件不存在")
	ErrVideoPayloadNotFound = errors.New("视频任务载荷不存在")
)

// VideoObjectLocation 是服务端对象存储定位结果，永不接受客户端bucket、object_key或签名参数。
type VideoObjectLocation struct {
	Bucket    string
	ObjectKey string
}

// VideoObjectLocationFactory 为Fake ObjectStore和后续真实对象存储预留同一服务端接口；VID-G3不访问真实对象存储。
type VideoObjectLocationFactory interface {
	NewVideoObjectLocation(ctx context.Context, owner VideoOwner, taskPublicID, assetPublicID, role string, resultIndex uint32) (VideoObjectLocation, error)
}

// VideoOutputAssetDraft 只允许低敏媒体元数据，故意不暴露bucket、object_key、URL和签名字段。
type VideoOutputAssetDraft struct {
	PublicID          string
	TaskPublicID      string
	ParentPublicID    string
	Owner             VideoOwner
	ResultIndex       uint32
	AssetRole         string
	IsBillableOutput  bool
	MIMEType          string
	SizeBytes         uint64
	SHA256            string
	Width             uint32
	Height            uint32
	DurationSeconds   decimal.Decimal
	FrameRate         decimal.Decimal
	Container         string
	VideoCodec        string
	AudioCodec        string
	HasAudio          *bool
	Source            string
	RetentionPolicyID string
	ExpiresAt         time.Time
	Now               time.Time
}

// VideoOutputAssetRepository 在共享ai_gateway_assets中管理视频产物和派生关系。
type VideoOutputAssetRepository struct {
	db      *gorm.DB
	locator VideoObjectLocationFactory
}

func NewVideoOutputAssetRepository(db *gorm.DB, locator VideoObjectLocationFactory) *VideoOutputAssetRepository {
	return &VideoOutputAssetRepository{db: db, locator: locator}
}

// Create 先确定可信任务归属和父资产，再由服务端定位器生成对象位置。
func (r *VideoOutputAssetRepository) Create(ctx context.Context, draft VideoOutputAssetDraft) (*model.AIImageAsset, error) {
	if r == nil || r.db == nil || r.locator == nil || !validVideoOutputDraft(draft) {
		return nil, ErrVideoAssetNotFound
	}
	var created model.AIImageAsset
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, err := findVideoTaskRecord(tx, draft.TaskPublicID, draft.Owner, true)
		if err != nil {
			return ErrVideoAssetNotFound
		}
		var parentID *uint64
		if videoAssetRequiresParent(draft.AssetRole) {
			var parent model.AIImageAsset
			if err := videoAssetOwnerQuery(tx, draft.ParentPublicID, draft.Owner).
				Where("task_id=? AND request_id=? AND modality='video' AND asset_role='content'", task.ID, task.RequestID).First(&parent).Error; err != nil {
				return ErrVideoAssetNotFound
			}
			parentID = &parent.ID
		} else if strings.TrimSpace(draft.ParentPublicID) != "" {
			return ErrVideoAssetNotFound
		}
		location, err := r.locator.NewVideoObjectLocation(ctx, draft.Owner, task.PublicID, draft.PublicID, draft.AssetRole, draft.ResultIndex)
		if err != nil || strings.TrimSpace(location.Bucket) == "" || strings.TrimSpace(location.ObjectKey) == "" {
			return ErrVideoAssetNotFound
		}
		mime, size, sha := draft.MIMEType, draft.SizeBytes, strings.ToLower(draft.SHA256)
		width, height := draft.Width, draft.Height
		var duration, frameRate *decimal.Decimal
		var container, videoCodec *string
		var audioCodec *string
		if draft.MIMEType == "video/mp4" {
			durationValue, frameRateValue := draft.DurationSeconds, draft.FrameRate
			containerValue, videoCodecValue := draft.Container, draft.VideoCodec
			duration, frameRate = &durationValue, &frameRateValue
			container, videoCodec = &containerValue, &videoCodecValue
		}
		if draft.HasAudio != nil && *draft.HasAudio {
			audio := draft.AudioCodec
			audioCodec = &audio
		}
		created = model.AIImageAsset{
			PublicID: draft.PublicID, UserID: draft.Owner.UserID, ProjectID: draft.Owner.ProjectID,
			RequestID: task.RequestID, TaskID: task.ID, ResultIndex: draft.ResultIndex, AssetRole: draft.AssetRole,
			ParentAssetID: parentID, IsBillableOutput: draft.IsBillableOutput,
			Bucket: &location.Bucket, ObjectKey: &location.ObjectKey, MIMEType: &mime, SizeBytes: &size, SHA256: &sha,
			Width: &width, Height: &height, Modality: "video", DurationSeconds: duration, FrameRate: frameRate,
			Container: container, VideoCodec: videoCodec, AudioCodec: audioCodec, HasAudio: draft.HasAudio,
			Source: draft.Source, ModerationStatus: model.AIModerationPending,
			ExplicitLabelStatus: model.AIImageLabelPending, ImplicitLabelStatus: model.AIImageLabelPending,
			LifecycleState: model.AIImageAssetTemporary, RetentionPolicyID: draft.RetentionPolicyID,
			ExpiresAt: draft.ExpiresAt, VersionNo: 1, DisputeStatus: model.AIImageDisputeNone,
			CreatedAt: draft.Now, UpdatedAt: draft.Now,
		}
		return tx.Create(&created).Error
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// FindOwnedForInternal 对内部状态机返回完整对象元数据，但仍强制全部横向归属维度。
func (r *VideoOutputAssetRepository) FindOwnedForInternal(ctx context.Context, publicID string, owner VideoOwner) (*model.AIImageAsset, error) {
	if r == nil || r.db == nil || !validVideoOwner(owner) {
		return nil, ErrVideoAssetNotFound
	}
	var asset model.AIImageAsset
	if err := videoAssetOwnerQuery(r.db.WithContext(ctx), publicID, owner).First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoAssetNotFound
		}
		return nil, err
	}
	return &asset, nil
}

// FindDeliverable 同时检查任务、计费、交付、审核、标签、争议和删除状态。
func (r *VideoOutputAssetRepository) FindDeliverable(ctx context.Context, publicID string, owner VideoOwner) (*model.AIImageAsset, error) {
	query := videoAssetOwnerQuery(r.db.WithContext(ctx), publicID, owner).
		Joins("JOIN ai_requests AS requests ON requests.request_id=ai_gateway_assets.request_id").
		Joins("JOIN ai_gateway_tasks AS tasks ON tasks.id=ai_gateway_assets.task_id").
		Where(`ai_gateway_assets.asset_role='content' AND ai_gateway_assets.is_billable_output=1
AND ai_gateway_assets.lifecycle_state='available' AND ai_gateway_assets.moderation_status='passed'
AND ai_gateway_assets.explicit_label_status='applied' AND ai_gateway_assets.implicit_label_status='applied'
AND ai_gateway_assets.dispute_status<>'open' AND ai_gateway_assets.deleted_at IS NULL AND ai_gateway_assets.media_deleted_at IS NULL
AND NOT EXISTS (SELECT 1 FROM ai_video_object_reconciliation_observations o WHERE o.direction='db_missing_object' AND o.bucket=ai_gateway_assets.bucket AND o.object_key=ai_gateway_assets.object_key AND o.status='confirmed')
AND tasks.capability=? AND tasks.operation IN ? AND tasks.status='succeeded'
AND requests.modality='video' AND requests.capability=? AND requests.billing_status IN ? AND requests.delivery_status='available'`,
			model.AIVideoCapability, []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo}, model.AIVideoCapability,
			[]string{model.AIBillingSettled, model.AIBillingAdjusted})
	var asset model.AIImageAsset
	if err := query.First(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoAssetAccess
		}
		return nil, err
	}
	return &asset, nil
}

// MoveObjectLocation 只允许服务端把同一对象键从临时区晋级到结果区或迁入隔离区。
func (r *VideoOutputAssetRepository) MoveObjectLocation(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, from, to VideoObjectLocation, now time.Time) (*model.AIImageAsset, error) {
	legacyMove := from.Bucket == "video-temp" && (to.Bucket == "video-result" || to.Bucket == "video-quarantine")
	sharedMove := from.Bucket == "ai-upload-temp" && (to.Bucket == "ai-result" || to.Bucket == "ai-quarantine")
	if from.ObjectKey == "" || from.ObjectKey != to.ObjectKey || (!legacyMove && !sharedMove) || now.IsZero() {
		return nil, ErrVideoAssetTransition
	}
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion || asset.Bucket == nil || asset.ObjectKey == nil ||
		*asset.Bucket != from.Bucket || *asset.ObjectKey != from.ObjectKey {
		return nil, ErrVideoAssetConflict
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id=? AND user_id=? AND project_id=? AND modality='video' AND version_no=? AND bucket=? AND object_key=?",
			asset.ID, owner.UserID, owner.ProjectID, expectedVersion, from.Bucket, from.ObjectKey).
		Updates(map[string]interface{}{"bucket": to.Bucket, "object_key": to.ObjectKey, "version_no": gorm.Expr("version_no + 1"), "updated_at": now})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrVideoAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

// ApplyModerationResult 以CAS写入本地Fake审核结论和策略版本，不保存任何审核正文。
func (r *VideoOutputAssetRepository) ApplyModerationResult(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, status, policyVersion string, now time.Time) (*model.AIImageAsset, error) {
	if status != model.AIModerationPassed && status != model.AIModerationRejected && status != model.AIModerationError {
		return nil, ErrVideoAssetTransition
	}
	policyVersion = strings.TrimSpace(policyVersion)
	if policyVersion == "" || len(policyVersion) > 64 || now.IsZero() {
		return nil, ErrVideoAssetTransition
	}
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion || asset.ModerationStatus != model.AIModerationPending || asset.LifecycleState != model.AIImageAssetTemporary {
		return nil, ErrVideoAssetConflict
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id=? AND user_id=? AND project_id=? AND modality='video' AND version_no=? AND moderation_status='pending' AND lifecycle_state='temporary'",
			asset.ID, owner.UserID, owner.ProjectID, expectedVersion).
		Updates(map[string]interface{}{
			"moderation_status": status, "moderation_policy_version": policyVersion,
			"version_no": gorm.Expr("version_no + 1"), "updated_at": now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrVideoAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

// ApplyLabelResult 只在审核通过后写入显式和隐式标识状态及版本，任一失败都不能进入available。
func (r *VideoOutputAssetRepository) ApplyLabelResult(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, explicitStatus, implicitStatus, version string, now time.Time) (*model.AIImageAsset, error) {
	if !validVideoLabelStatus(explicitStatus) || !validVideoLabelStatus(implicitStatus) {
		return nil, ErrVideoAssetTransition
	}
	version = strings.TrimSpace(version)
	if version == "" || len(version) > 64 || now.IsZero() {
		return nil, ErrVideoAssetTransition
	}
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion || asset.ModerationStatus != model.AIModerationPassed ||
		asset.ExplicitLabelStatus != model.AIImageLabelPending || asset.ImplicitLabelStatus != model.AIImageLabelPending ||
		asset.LifecycleState != model.AIImageAssetTemporary {
		return nil, ErrVideoAssetConflict
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id=? AND user_id=? AND project_id=? AND modality='video' AND version_no=? AND moderation_status='passed' AND explicit_label_status='pending' AND implicit_label_status='pending' AND lifecycle_state='temporary'",
			asset.ID, owner.UserID, owner.ProjectID, expectedVersion).
		Updates(map[string]interface{}{
			"explicit_label_status": explicitStatus, "implicit_label_status": implicitStatus,
			"explicit_label_version": version, "implicit_label_version": version,
			"version_no": gorm.Expr("version_no + 1"), "updated_at": now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrVideoAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

// MarkMediaDeleted 只记录正文已删除事实，保留对象定位、hash、规格、归属和审计链。
func (r *VideoOutputAssetRepository) MarkMediaDeleted(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, now time.Time) (*model.AIImageAsset, error) {
	if now.IsZero() {
		return nil, ErrVideoAssetTransition
	}
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion {
		return nil, ErrVideoAssetConflict
	}
	if asset.MediaDeletedAt != nil {
		return asset, nil
	}
	for asset.LifecycleState != model.AIImageAssetDeleted {
		var next string
		switch asset.LifecycleState {
		case model.AIImageAssetAvailable:
			next = model.AIImageAssetExpiring
		case model.AIImageAssetTemporary, model.AIImageAssetQuarantined, model.AIImageAssetDeleteFailed:
			next = model.AIImageAssetDeleting
		case model.AIImageAssetExpiring:
			next = model.AIImageAssetDeleting
		case model.AIImageAssetDeleting:
			next = model.AIImageAssetDeleted
		default:
			return nil, ErrVideoAssetTransition
		}
		asset, err = r.TransitionLifecycle(ctx, publicID, owner, asset.VersionNo, next, now)
		if err != nil {
			return nil, err
		}
	}
	return asset, nil
}

// TransitionLifecycle 以version_no CAS推进资产生命周期，并受legal hold和争议状态保护。
func (r *VideoOutputAssetRepository) TransitionLifecycle(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, toState string, now time.Time) (*model.AIImageAsset, error) {
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion || !videoAssetTransitionAllowed(asset.LifecycleState, toState) || ((asset.LegalHold || asset.DisputeStatus == model.AIImageDisputeOpen) && videoAssetDestructiveState(toState)) {
		return nil, ErrVideoAssetTransition
	}
	if toState == model.AIImageAssetAvailable && !videoAssetReadyForAvailable(asset) {
		return nil, ErrVideoAssetTransition
	}
	if toState == model.AIImageAssetQuarantined && asset.ModerationStatus != model.AIModerationRejected && asset.ModerationStatus != model.AIModerationError &&
		asset.ExplicitLabelStatus != model.AIImageLabelFailed && asset.ImplicitLabelStatus != model.AIImageLabelFailed {
		return nil, ErrVideoAssetTransition
	}
	updates := map[string]interface{}{"lifecycle_state": toState, "version_no": gorm.Expr("version_no + 1"), "updated_at": now}
	if toState == model.AIImageAssetDeleted {
		updates["deleted_at"] = now
		updates["media_deleted_at"] = now
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id=? AND user_id=? AND project_id=? AND modality='video' AND lifecycle_state=? AND version_no=?", asset.ID, owner.UserID, owner.ProjectID, asset.LifecycleState, expectedVersion).
		Updates(updates)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrVideoAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

// OpenDispute 以CAS开启争议并自动设置legal hold，立即阻断交付和破坏性清理。
func (r *VideoOutputAssetRepository) OpenDispute(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, now time.Time) (*model.AIImageAsset, error) {
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion || asset.DisputeStatus != model.AIImageDisputeNone || videoAssetDestructiveState(asset.LifecycleState) {
		return nil, ErrVideoAssetConflict
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id=? AND user_id=? AND project_id=? AND modality='video' AND version_no=? AND dispute_status='none'", asset.ID, owner.UserID, owner.ProjectID, expectedVersion).
		Updates(map[string]interface{}{
			"dispute_status": model.AIImageDisputeOpen, "dispute_opened_at": now, "dispute_resolved_at": nil,
			"legal_hold": true, "version_no": gorm.Expr("version_no + 1"), "updated_at": now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrVideoAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

// ResolveDispute 关闭访问争议但保留legal hold；保全释放必须由后续独立审计动作完成。
func (r *VideoOutputAssetRepository) ResolveDispute(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, now time.Time) (*model.AIImageAsset, error) {
	asset, err := r.FindOwnedForInternal(ctx, publicID, owner)
	if err != nil {
		return nil, err
	}
	if asset.VersionNo != expectedVersion || asset.DisputeStatus != model.AIImageDisputeOpen {
		return nil, ErrVideoAssetConflict
	}
	result := r.db.WithContext(ctx).Model(&model.AIImageAsset{}).
		Where("id=? AND user_id=? AND project_id=? AND modality='video' AND version_no=? AND dispute_status='open'", asset.ID, owner.UserID, owner.ProjectID, expectedVersion).
		Updates(map[string]interface{}{
			"dispute_status": model.AIImageDisputeResolved, "dispute_resolved_at": now,
			"version_no": gorm.Expr("version_no + 1"), "updated_at": now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrVideoAssetConflict
	}
	return r.FindOwnedForInternal(ctx, publicID, owner)
}

func videoAssetOwnerQuery(db *gorm.DB, publicID string, owner VideoOwner) *gorm.DB {
	query := db.Model(&model.AIImageAsset{}).
		Where("ai_gateway_assets.public_id=? AND ai_gateway_assets.user_id=? AND ai_gateway_assets.project_id=? AND ai_gateway_assets.modality='video'", strings.TrimSpace(publicID), owner.UserID, owner.ProjectID).
		Where("EXISTS (SELECT 1 FROM ai_gateway_tasks scoped_task WHERE scoped_task.id=ai_gateway_assets.task_id AND scoped_task.capability=? AND scoped_task.operation IN ?)", model.AIVideoCapability, []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo})
	if owner.APIKeyID == nil {
		return query.Where("EXISTS (SELECT 1 FROM ai_gateway_tasks scoped_key WHERE scoped_key.id=ai_gateway_assets.task_id AND scoped_key.api_key_id IS NULL)")
	}
	return query.Where("EXISTS (SELECT 1 FROM ai_gateway_tasks scoped_key WHERE scoped_key.id=ai_gateway_assets.task_id AND scoped_key.api_key_id=?)", *owner.APIKeyID)
}

func validVideoOutputDraft(draft VideoOutputAssetDraft) bool {
	if !validVideoOwner(draft.Owner) || strings.TrimSpace(draft.PublicID) == "" || strings.TrimSpace(draft.TaskPublicID) == "" || draft.SizeBytes == 0 || len(draft.SHA256) != 64 || draft.Width == 0 || draft.Height == 0 || strings.TrimSpace(draft.Source) == "" || strings.TrimSpace(draft.RetentionPolicyID) == "" || draft.ExpiresAt.IsZero() || draft.Now.IsZero() {
		return false
	}
	if draft.MIMEType == "video/mp4" {
		if !draft.DurationSeconds.IsPositive() || !draft.FrameRate.IsPositive() || strings.TrimSpace(draft.Container) == "" || strings.TrimSpace(draft.VideoCodec) == "" || draft.HasAudio == nil || (*draft.HasAudio && strings.TrimSpace(draft.AudioCodec) == "") || (!*draft.HasAudio && strings.TrimSpace(draft.AudioCodec) != "") {
			return false
		}
	} else if draft.MIMEType == "image/png" || draft.MIMEType == "image/jpeg" || draft.MIMEType == "image/webp" {
		if draft.DurationSeconds.IsPositive() || draft.FrameRate.IsPositive() || strings.TrimSpace(draft.Container) != "" || strings.TrimSpace(draft.VideoCodec) != "" || strings.TrimSpace(draft.AudioCodec) != "" || draft.HasAudio != nil {
			return false
		}
	} else {
		return false
	}
	if draft.AssetRole == model.AIImageAssetContent {
		return draft.IsBillableOutput
	}
	return videoAssetRequiresParent(draft.AssetRole) && !draft.IsBillableOutput && strings.TrimSpace(draft.ParentPublicID) != ""
}

func videoAssetRequiresParent(role string) bool {
	return role == model.AIImageAssetCover || role == model.AIImageAssetPreview || role == model.AIImageAssetThumbnail || role == model.AIImageAssetModerationCopy || role == model.AIImageAssetDerived
}

func videoAssetReadyForAvailable(asset *model.AIImageAsset) bool {
	return asset != nil && asset.ModerationStatus == model.AIModerationPassed &&
		asset.ExplicitLabelStatus == model.AIImageLabelApplied && asset.ImplicitLabelStatus == model.AIImageLabelApplied &&
		asset.ModerationPolicyVersion != nil && strings.TrimSpace(*asset.ModerationPolicyVersion) != "" &&
		asset.ExplicitLabelVersion != nil && strings.TrimSpace(*asset.ExplicitLabelVersion) != "" &&
		asset.ImplicitLabelVersion != nil && strings.TrimSpace(*asset.ImplicitLabelVersion) != "" &&
		asset.Bucket != nil && strings.TrimSpace(*asset.Bucket) != "" && asset.ObjectKey != nil && strings.TrimSpace(*asset.ObjectKey) != "" &&
		asset.MIMEType != nil && asset.SizeBytes != nil && *asset.SizeBytes > 0 && asset.SHA256 != nil && len(*asset.SHA256) == 64 &&
		asset.Width != nil && *asset.Width > 0 && asset.Height != nil && *asset.Height > 0
}

func validVideoLabelStatus(status string) bool {
	return status == model.AIImageLabelApplied || status == model.AIImageLabelFailed
}

func videoAssetDestructiveState(state string) bool {
	return state == model.AIImageAssetExpiring || state == model.AIImageAssetDeleting || state == model.AIImageAssetDeleted
}

func videoAssetTransitionAllowed(from, to string) bool {
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

// VideoTaskEventRepository 只暴露追加和归属读取，不提供UPDATE或DELETE入口。
type VideoTaskEventRepository struct{ db *gorm.DB }

func NewVideoTaskEventRepository(db *gorm.DB) *VideoTaskEventRepository {
	return &VideoTaskEventRepository{db: db}
}

func (r *VideoTaskEventRepository) Append(ctx context.Context, taskPublicID string, owner VideoOwner, event model.AIGatewayTaskEvent) error {
	// 执行租约审计只能随仓储认领/释放事务形成，通用事件入口不能伪造持有者证明。
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(event.EventType)), "video_worker_lease_") {
		return ErrVideoUnsafeDetail
	}
	// 释放证据必须来自原执行CAS，通用追加入口不接受旧式或伪造的财务释放标记。
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(event.EventType)), "video_release_") || strings.EqualFold(strings.TrimSpace(event.EventType), "cancel_requested") || strings.EqualFold(strings.TrimSpace(event.EventType), "provider_no_product_confirmed") || strings.EqualFold(strings.TrimSpace(event.EventType), "provider_result_conflict") || strings.EqualFold(strings.TrimSpace(event.EventType), "submission_receipt_rejected") || strings.EqualFold(strings.TrimSpace(event.EventType), "submission_receipt_accepted") || strings.EqualFold(strings.TrimSpace(event.EventType), "provider_task_bound_pending") {
		return ErrVideoUnsafeDetail
	}
	if err := validateVideoSafeJSON(event.SafeDetailJSON); err != nil {
		return err
	}
	task, err := NewVideoTaskRepository(r.db).FindForOwner(ctx, taskPublicID, owner)
	if err != nil {
		return ErrVideoEventNotFound
	}
	event.ID = 0
	event.TaskID, event.UserID, event.ProjectID = task.ID, owner.UserID, owner.ProjectID
	if strings.TrimSpace(event.EventID) == "" || event.CreatedAt.IsZero() {
		return ErrVideoEventNotFound
	}
	return r.db.WithContext(ctx).Create(&event).Error
}

func (r *VideoTaskEventRepository) ListForOwner(ctx context.Context, taskPublicID string, owner VideoOwner) ([]model.AIGatewayTaskEvent, error) {
	task, err := NewVideoTaskRepository(r.db).FindForOwner(ctx, taskPublicID, owner)
	if err != nil {
		return nil, ErrVideoEventNotFound
	}
	var events []model.AIGatewayTaskEvent
	if err := r.db.WithContext(ctx).Where("task_id=? AND user_id=? AND project_id=?", task.ID, owner.UserID, owner.ProjectID).Order("id ASC").Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

// VideoTaskPayloadEnvelopeValidator 由持有阶段专用密钥的Service实现，Repository不得绕过认证解密验证。
type VideoTaskPayloadEnvelopeValidator interface {
	ValidateEnvelope(payload *model.AIGatewayTaskPayload) error
}

type VideoTaskPayloadRepository struct {
	db        *gorm.DB
	validator VideoTaskPayloadEnvelopeValidator
}

func NewVideoTaskPayloadRepository(db *gorm.DB, validator VideoTaskPayloadEnvelopeValidator) *VideoTaskPayloadRepository {
	return &VideoTaskPayloadRepository{db: db, validator: validator}
}

func (r *VideoTaskPayloadRepository) Create(ctx context.Context, taskPublicID string, owner VideoOwner, payload *model.AIGatewayTaskPayload) error {
	if r == nil || r.db == nil || r.validator == nil {
		return ErrVideoPayloadNotFound
	}
	task, err := NewVideoTaskRepository(r.db).FindForOwner(ctx, taskPublicID, owner)
	if err != nil || payload == nil || payload.TaskID != task.ID || payload.UserID != owner.UserID || payload.ProjectID != owner.ProjectID || !validVideoTaskPayloadEnvelopeShape(payload) {
		return ErrVideoPayloadNotFound
	}
	if err := r.validator.ValidateEnvelope(payload); err != nil {
		return ErrVideoPayloadNotFound
	}
	return r.db.WithContext(ctx).Create(payload).Error
}

func (r *VideoTaskPayloadRepository) FindForOwner(ctx context.Context, taskPublicID, payloadKind string, owner VideoOwner) (*model.AIGatewayTaskPayload, error) {
	if r == nil || r.db == nil || r.validator == nil {
		return nil, ErrVideoPayloadNotFound
	}
	task, err := NewVideoTaskRepository(r.db).FindForOwner(ctx, taskPublicID, owner)
	if err != nil {
		return nil, ErrVideoPayloadNotFound
	}
	var payload model.AIGatewayTaskPayload
	if err := r.db.WithContext(ctx).Where("task_id=? AND user_id=? AND project_id=? AND payload_kind=?", task.ID, owner.UserID, owner.ProjectID, payloadKind).First(&payload).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoPayloadNotFound
		}
		return nil, fmt.Errorf("读取视频任务密文失败: %w", err)
	}
	if !validVideoTaskPayloadEnvelopeShape(&payload) || r.validator.ValidateEnvelope(&payload) != nil {
		return nil, ErrVideoPayloadNotFound
	}
	return &payload, nil
}

func validVideoTaskPayloadEnvelopeShape(payload *model.AIGatewayTaskPayload) bool {
	if payload == nil || payload.TaskID == 0 || payload.UserID == 0 || payload.ProjectID == 0 || strings.TrimSpace(payload.KeyVersion) == "" || len(payload.KeyVersion) > 64 || len(payload.Nonce) != 12 || len(payload.Ciphertext) < 17 {
		return false
	}
	if payload.PayloadKind != model.AITaskPayloadPrompt && payload.PayloadKind != model.AITaskPayloadProviderRequest && payload.PayloadKind != model.AITaskPayloadProviderResult {
		return false
	}
	expectedAAD := videoTaskPayloadRepositorySHA256([]byte(fmt.Sprintf("molin:video-task-payload:v1:%d:%d:%d:%s", payload.TaskID, payload.UserID, payload.ProjectID, payload.PayloadKind)))
	expectedCiphertext := videoTaskPayloadRepositorySHA256(payload.Ciphertext)
	return subtle.ConstantTimeCompare([]byte(payload.AADSHA256), []byte(expectedAAD)) == 1 &&
		subtle.ConstantTimeCompare([]byte(payload.CiphertextSHA256), []byte(expectedCiphertext)) == 1
}

func videoTaskPayloadRepositorySHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
